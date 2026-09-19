package plan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	mysqlCostRe   = regexp.MustCompile(`cost=([\d.]+)(?:\.\.([\d.]+))? rows=(\d+)`)
	mysqlActualRe = regexp.MustCompile(`actual time=([\d.]+)\.\.([\d.]+) rows=(\d+) loops=(\d+)`)
	mysqlOnRe     = regexp.MustCompile(`\bon\s+(\S+)`)
	mysqlUsingRe  = regexp.MustCompile(`\busing\s+(\S+)`)
)

// parseMySQL parses any of the three MySQL plan formats:
// EXPLAIN FORMAT=JSON, the traditional tabular EXPLAIN and the iterator
// tree produced by EXPLAIN ANALYZE.
func parseMySQL(mode Mode, raw string) (*Plan, error) {
	trimmed := strings.TrimSpace(raw)

	switch {
	case strings.HasPrefix(trimmed, "{"):
		return parseMySQLJSON(mode, raw)
	case strings.Contains(trimmed, "\n->") || strings.HasPrefix(trimmed, "->"):
		return parseMySQLIterator(raw)
	default:
		return parseMySQLTraditional(raw)
	}
}

// parseMySQLJSON parses EXPLAIN FORMAT=JSON output.
func parseMySQLJSON(mode Mode, raw string) (*Plan, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("parsing mysql EXPLAIN JSON: %w", err)
	}

	qb, ok := doc["query_block"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("parsing mysql EXPLAIN JSON: missing query_block")
	}

	root := newMySQLBlock("query_block", qb)
	if root == nil {
		return nil, fmt.Errorf("parsing mysql EXPLAIN JSON: empty query_block")
	}

	p := &Plan{
		Mode:       mode,
		Root:       root,
		CapturedAt: capturedAt(),
	}

	return p, nil
}

// newMySQLBlock converts a query_block map into a RESULT node with the
// actual operation as its child.
func newMySQLBlock(label string, m map[string]any) *Node {
	n := &Node{
		OpType:     OpResult,
		RawName:    label,
		Extra:      map[string]string{},
		Children:   nil,
		RawPayload: compactJSON(m),
	}

	if ci, ok := m["cost_info"].(map[string]any); ok {
		n.Metrics.TotalCost = mapFloat(ci, "query_cost")
	}

	if inner := mysqlInner(m); inner != nil {
		n.Children = []*Node{inner}
	}

	return n
}

// mysqlInner converts the first operation container present in a block.
func mysqlInner(m map[string]any) *Node {
	for _, key := range []string{
		"nested_loop",
		"ordering_operation",
		"grouping_operation",
		"duplicates_removal",
		"union_result",
		"table",
	} {
		if v, ok := m[key]; ok {
			if node := mysqlContainer(key, v); node != nil {
				return node
			}
		}
	}
	return nil
}

// mysqlContainer converts one of the named operation containers.
func mysqlContainer(name string, v any) *Node {
	switch name {
	case "nested_loop":
		entries, ok := v.([]any)
		if !ok {
			return nil
		}

		n := &Node{
			OpType:     OpNestedLoopJoin,
			RawName:    "nested_loop",
			Extra:      map[string]string{},
			RawPayload: compactJSON(map[string]any{name: v}),
		}

		for _, e := range entries {
			if em, ok := e.(map[string]any); ok {
				if child := mysqlInner(em); child != nil {
					n.Children = append(n.Children, child)
				}
			}
		}

		return n
	case "ordering_operation":
		om, ok := v.(map[string]any)
		if !ok {
			return nil
		}

		n := &Node{
			OpType:     OpSort,
			RawName:    "ordering_operation",
			Extra:      map[string]string{},
			RawPayload: compactJSON(om),
		}

		liftMySQLFlags(n, om)
		if inner := mysqlInner(om); inner != nil {
			n.Children = []*Node{inner}
		}

		return n
	case "grouping_operation":
		gm, ok := v.(map[string]any)
		if !ok {
			return nil
		}

		n := &Node{
			OpType:     OpAggregate,
			RawName:    "grouping_operation",
			Extra:      map[string]string{},
			RawPayload: compactJSON(gm),
		}

		liftMySQLFlags(n, gm)
		if inner := mysqlInner(gm); inner != nil {
			n.Children = []*Node{inner}
		}

		return n
	case "duplicates_removal":
		dm, ok := v.(map[string]any)
		if !ok {
			return nil
		}

		n := &Node{
			OpType:     OpUnique,
			RawName:    "duplicates_removal",
			Extra:      map[string]string{},
			RawPayload: compactJSON(dm),
		}

		if inner := mysqlInner(dm); inner != nil {
			n.Children = []*Node{inner}
		}

		return n
	case "union_result":
		um, ok := v.(map[string]any)
		if !ok {
			return nil
		}

		n := &Node{
			OpType:     OpAppend,
			RawName:    "union_result",
			Extra:      map[string]string{},
			RawPayload: compactJSON(um),
		}

		if specs, ok := um["query_specifications"].([]any); ok {
			for _, s := range specs {
				if sm, ok := s.(map[string]any); ok {
					if qb, ok := sm["query_block"].(map[string]any); ok {
						n.Children = append(n.Children, newMySQLBlock("query_block", qb))
					}
				}
			}
		}

		return n
	case "table":
		tm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		return mysqlTable(tm)
	default:
		return nil
	}
}

// mysqlTable converts a table map, handling materialized derived tables.
func mysqlTable(m map[string]any) *Node {
	if sub, ok := m["materialized_from_subquery"].(map[string]any); ok {
		n := &Node{
			OpType:     OpSubquery,
			RawName:    "materialized_from_subquery",
			Object:     strings.Trim(mapString(m, "table_name"), "<>"),
			Extra:      map[string]string{},
			RawPayload: compactJSON(m),
		}

		if qb, ok := sub["query_block"].(map[string]any); ok {
			n.Children = []*Node{newMySQLBlock("query_block", qb)}
		}

		return n
	}

	accessType := mapString(m, "access_type")

	n := &Node{
		OpType:     Normalize(DialectMySQL, accessType),
		RawName:    accessType,
		Object:     mapString(m, "table_name"),
		Index:      nonNullKey(mapString(m, "key")),
		Predicate:  mapString(m, "attached_condition"),
		Extra:      map[string]string{},
		RawPayload: compactJSON(m),
	}

	if n.OpType == OpUnknown {
		n.OpType = OpSeqScan
	}

	n.Metrics.Rows = mapFloat(m, "rows_examined_per_scan", "rows")

	if ci, ok := m["cost_info"].(map[string]any); ok {
		n.Metrics.TotalCost = mapFloat(ci, "prefix_cost", "read_cost")
	}

	if produced := mapFloat(m, "rows_produced_per_join"); produced > 0 {
		n.Extra["rows_produced_per_join"] = strconv.FormatFloat(produced, 'f', -1, 64)
	}

	if filtered := mapString(m, "filtered"); filtered != "" && filtered != "100.00" && filtered != "100.0" {
		n.Extra["filtered"] = filtered + "%"
	}

	return n
}

// liftMySQLFlags copies using_filesort / using_temporary_table into Extra.
func liftMySQLFlags(n *Node, m map[string]any) {
	if v, ok := m["using_filesort"].(bool); ok && v {
		n.Extra["using_filesort"] = "true"
	}
	if v, ok := m["using_temporary_table"].(bool); ok && v {
		n.Extra["using_temporary_table"] = "true"
	}
}

// nonNullKey returns the key name unless MySQL reports NULL.
func nonNullKey(key string) string {
	if strings.EqualFold(key, "NULL") {
		return ""
	}
	return key
}

// parseMySQLTraditional parses the tab separated traditional EXPLAIN grid.
func parseMySQLTraditional(raw string) (*Plan, error) {
	lines := strings.Split(raw, "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("parsing mysql traditional EXPLAIN: no data rows")
	}

	headers := strings.Split(lines[0], "\t")
	idx := columnIndex(headers)

	nodes := make([]*Node, 0, len(lines)-1)

	for _, line := range lines[1:] {
		cells := strings.Split(line, "\t")
		get := func(name string) string {
			i, ok := idx[name]
			if !ok || i >= len(cells) {
				return ""
			}
			return cells[i]
		}

		accessType := get("type")

		n := &Node{
			OpType:     Normalize(DialectMySQL, accessType),
			RawName:    accessType,
			Object:     get("table"),
			Index:      nonNullKey(get("key")),
			Extra:      map[string]string{},
			RawPayload: line,
			Metrics: Metrics{
				Rows: parseFloat(get("rows")),
			},
		}

		if n.OpType == OpUnknown {
			n.OpType = OpSeqScan
		}

		extraText := get("extra")
		if extraText != "" {
			n.Extra["Extra"] = extraText
			if strings.Contains(extraText, "Using index") && n.OpType == OpIndexScan {
				n.OpType = OpIndexOnlyScan
			}
		}

		if filtered := get("filtered"); filtered != "" && filtered != "100.00" && filtered != "100" {
			n.Extra["filtered"] = filtered + "%"
		}

		nodes = append(nodes, n)
	}

	var root *Node
	if len(nodes) == 1 {
		root = nodes[0]
	} else {
		root = &Node{
			OpType:   OpNestedLoopJoin,
			RawName:  "nested_loop",
			Extra:    map[string]string{},
			Children: nodes,
		}
	}

	return &Plan{
		Mode:       ModeExplain,
		Root:       root,
		CapturedAt: capturedAt(),
	}, nil
}

// columnIndex lowercases header names into a name→position index.
func columnIndex(headers []string) map[string]int {
	idx := make(map[string]int, len(headers))
	for i, h := range headers {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	return idx
}

// parseMySQLIterator parses the indented iterator tree of EXPLAIN ANALYZE.
func parseMySQLIterator(raw string) (*Plan, error) {
	var (
		stack  []*Node
		depths []int
		roots  []*Node
	)

	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		col := strings.Index(line, "->")
		if col < 0 {
			continue
		}

		depth := col / 4
		label := strings.TrimSpace(line[col+2:])

		node := &Node{
			Extra:      map[string]string{},
			RawPayload: line,
		}

		// Extract metrics, then strip them from the label.
		costMatch := mysqlCostRe.FindStringSubmatchIndex(label)
		actualMatch := mysqlActualRe.FindStringSubmatchIndex(label)

		metricsStart := len(label)
		if costMatch != nil && costMatch[0] < metricsStart {
			metricsStart = costMatch[0]
		}
		if actualMatch != nil && actualMatch[0] < metricsStart {
			metricsStart = actualMatch[0]
		}

		label = strings.TrimSpace(label[:metricsStart])

		node.RawName = label
		node.OpType = Normalize(DialectMySQL, label)

		fillMySQLIteratorMetrics(node, line)
		fillMySQLIteratorObject(node, label)

		// Pop shallower-or-equal levels.
		for len(depths) > 0 && depths[len(depths)-1] >= depth {
			stack = stack[:len(stack)-1]
			depths = depths[:len(depths)-1]
		}

		if len(stack) == 0 {
			roots = append(roots, node)
		} else {
			stack[len(stack)-1].Children = append(stack[len(stack)-1].Children, node)
		}

		stack = append(stack, node)
		depths = append(depths, depth)
	}

	if len(roots) == 0 {
		return nil, fmt.Errorf("parsing mysql EXPLAIN ANALYZE: no iterator nodes found")
	}

	var root *Node
	if len(roots) == 1 {
		root = roots[0]
	} else {
		root = &Node{
			OpType:   OpAppend,
			RawName:  "batch",
			Extra:    map[string]string{},
			Children: roots,
		}
	}

	return &Plan{
		Mode:       ModeAnalyze,
		Root:       root,
		CapturedAt: capturedAt(),
	}, nil
}

// fillMySQLIteratorMetrics populates estimated and actual metrics.
func fillMySQLIteratorMetrics(node *Node, line string) {
	if m := mysqlCostRe.FindStringSubmatch(line); m != nil {
		node.Metrics.TotalCost = parseFloat(m[1])
		node.Metrics.Rows = parseFloat(m[3])
	}

	if m := mysqlActualRe.FindStringSubmatch(line); m != nil {
		node.Metrics.ActualValid = true
		node.Metrics.ActualStartupMS = parseFloat(m[1])
		node.Metrics.ActualTotalMS = parseFloat(m[2])
		node.Metrics.ActualRows = parseFloat(m[3])
		node.Metrics.ActualLoops = parseFloat(m[4])
	}
}

// fillMySQLIteratorObject extracts the relation and index from a label.
func fillMySQLIteratorObject(node *Node, label string) {
	if m := mysqlOnRe.FindStringSubmatch(label); m != nil {
		node.Object = strings.Trim(m[1], "`")
	}

	if m := mysqlUsingRe.FindStringSubmatch(label); m != nil {
		node.Index = strings.Trim(m[1], "`")
	}

	if strings.HasPrefix(strings.ToLower(label), "filter:") {
		node.Predicate = strings.TrimSpace(label[strings.Index(label, ":")+1:])
	}
}
