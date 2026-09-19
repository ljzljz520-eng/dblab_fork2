package plan

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
)

var (
	ssObjectRe    = regexp.MustCompile(`<Object\b([^>]*?)/>`)
	ssAttrRe      = regexp.MustCompile(`([\w:]+)="([^"]*)"`)
	ssRuntimeRe   = regexp.MustCompile(`<RunTimeCountersPerThread\b([^>]*?)/>`)
	ssSeekRe      = regexp.MustCompile(`(?s)<SeekPredicates\b[^>]*>(.*?)</SeekPredicates>`)
	ssPredicateRe = regexp.MustCompile(`(?s)<Predicate\b[^>]*>(.*?)</Predicate>`)
	ssColumnRefRe = regexp.MustCompile(`<ColumnReference\b([^>]*?)/>`)
	ssScalarRe    = regexp.MustCompile(`\bScalarString="([^"]*)"`)
	ssTagRe       = regexp.MustCompile(`<[^>]+>`)
)

// relOpXML is a minimal view of a ShowPlanXML RelOp element.
type relOpXML struct {
	XMLName      xml.Name `xml:"RelOp"`
	NodeID       string   `xml:"NodeId,attr"`
	PhysicalOp   string   `xml:"PhysicalOp,attr"`
	LogicalOp    string   `xml:"LogicalOp,attr"`
	EstimateRows string   `xml:"EstimateRows,attr"`
	EstimateIO   string   `xml:"EstimateIO,attr"`
	EstimateCPU  string   `xml:"EstimateCPU,attr"`
	AvgRowSize   string   `xml:"AvgRowSize,attr"`
	Content      string   `xml:",innerxml"`
}

// parseSQLServer parses SHOWPLAN_XML output, falling back to the text
// showplan produced when SHOWPLAN_TEXT / SHOWPLAN_ALL is used.
func parseSQLServer(mode Mode, raw string) (*Plan, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "<") {
		return parseSQLServerXML(mode, raw)
	}
	return parseSQLServerText(raw)
}

// parseSQLServerXML parses the XML showplan.
func parseSQLServerXML(mode Mode, raw string) (*Plan, error) {
	blocks := relOpBlocks(raw)
	if len(blocks) == 0 {
		return nil, fmt.Errorf("parsing sqlserver showplan XML: no RelOp elements found")
	}

	roots := make([]*Node, 0, len(blocks))
	hasActual := false

	for _, b := range blocks {
		n, actual := convertSQLServerRelOp(b)
		if n != nil {
			roots = append(roots, n)
		}
		if actual {
			hasActual = true
		}
	}

	if hasActual {
		mode = ModeAnalyze
	}

	var root *Node
	if len(roots) == 1 {
		root = roots[0]
	} else {
		root = &Node{
			OpType:   OpResult,
			RawName:  "batch",
			Extra:    map[string]string{},
			Children: roots,
		}
	}

	return &Plan{
		Mode:       mode,
		Root:       root,
		CapturedAt: capturedAt(),
	}, nil
}

// convertSQLServerRelOp converts one RelOp block and its descendants.
func convertSQLServerRelOp(block string) (*Node, bool) {
	var rx relOpXML
	if err := xml.Unmarshal([]byte(block), &rx); err != nil {
		return nil, false
	}

	n := &Node{
		OpType:     Normalize(DialectSQLServer, rx.PhysicalOp),
		RawName:    rx.PhysicalOp,
		Extra:      map[string]string{},
		RawPayload: strings.TrimSpace(block),
		Metrics: Metrics{
			Rows:  parseFloat(rx.EstimateRows),
			Width: parseFloat(rx.AvgRowSize),
		},
	}

	ioCost := parseFloat(rx.EstimateIO)
	cpuCost := parseFloat(rx.EstimateCPU)
	n.Metrics.TotalCost = ioCost + cpuCost
	n.Metrics.StartupCost = cpuCost

	// "Hash Match" with an aggregate logical op is an aggregate, not a join.
	if rx.PhysicalOp == "Hash Match" && strings.Contains(rx.LogicalOp, "Aggregate") {
		n.OpType = OpHashAggregate
	}

	// Extract the accessed object and index, if declared.
	if attrs := firstAttrs(ssObjectRe, rx.Content); attrs != nil {
		if table := attrs["Table"]; table != "" {
			schema := attrs["Schema"]
			if schema != "" {
				n.Object = schema + "." + table
			} else {
				n.Object = table
			}
		}
		if index := attrs["Index"]; index != "" && attrs["IndexKind"] != "Clustered" {
			n.Index = index
		} else if index != "" {
			n.Extra["clustered_index"] = index
		}
	}

	n.Predicate = sqlServerPredicate(rx.Content)

	if strings.Contains(rx.Content, "SpillToTempDb") {
		n.Extra["spill"] = "SpillToTempDb"
	}

	actual := false
	if counters := ssRuntimeRe.FindAllStringSubmatch(rx.Content, -1); len(counters) > 0 {
		fillSQLServerActual(n, counters)
		actual = true
	}

	for _, childBlock := range relOpBlocks(rx.Content) {
		if child, _ := convertSQLServerRelOp(childBlock); child != nil {
			n.Children = append(n.Children, child)
		}
	}

	return n, actual
}

// firstAttrs returns the attributes of the first regex match in content.
func firstAttrs(re *regexp.Regexp, content string) map[string]string {
	m := re.FindStringSubmatch(content)
	if m == nil {
		return nil
	}
	return parseSSAttrs(m[1])
}

// parseSSAttrs turns a raw attribute string into a map.
func parseSSAttrs(raw string) map[string]string {
	attrs := map[string]string{}
	for _, m := range ssAttrRe.FindAllStringSubmatch(raw, -1) {
		attrs[m[1]] = m[2]
	}
	return attrs
}

// sqlServerExpression renders seek/filter predicates as tag-free text.
//
// Column and value data lives as attributes of self-closing tags, so the
// relevant attributes are expanded before the remaining markup is removed.
func sqlServerExpression(inner string) string {
	inner = ssColumnRefRe.ReplaceAllStringFunc(inner, func(tag string) string {
		attrs := parseSSAttrs(tag)

		var parts []string
		if v := attrs["Column"]; v != "" {
			parts = append(parts, v)
		}
		if v := attrs["ParameterCompiledValue"]; v != "" {
			parts = append(parts, "=", v)
		} else if v := attrs["ParameterValue"]; v != "" {
			parts = append(parts, "=", v)
		} else if v := attrs["Value"]; v != "" {
			parts = append(parts, "=", v)
		}

		if len(parts) == 0 {
			return " "
		}

		return " " + strings.Join(parts, " ") + " "
	})

	var scalarParts []string
	for _, m := range ssScalarRe.FindAllStringSubmatch(inner, -1) {
		scalarParts = append(scalarParts, m[1])
	}

	text := ssTagRe.ReplaceAllString(inner, " ")

	if len(scalarParts) > 0 {
		text += " " + strings.Join(scalarParts, "; ")
	}

	return canonicalWhitespace(text)
}

// sqlServerPredicate extracts seek and filter predicates from content.
func sqlServerPredicate(content string) string {
	var parts []string
	for _, m := range ssSeekRe.FindAllStringSubmatch(content, -1) {
		if expr := sqlServerExpression(m[1]); expr != "" {
			parts = append(parts, "seek: "+expr)
		}
	}
	for _, m := range ssPredicateRe.FindAllStringSubmatch(content, -1) {
		if expr := sqlServerExpression(m[1]); expr != "" {
			parts = append(parts, "filter: "+expr)
		}
	}
	return strings.Join(parts, "; ")
}

// fillSQLServerActual aggregates runtime counters across threads.
//
// When a thread 0 row exists it carries the totals; otherwise totals are
// summed across all worker threads.
func fillSQLServerActual(n *Node, counters [][]string) {
	var (
		total    map[string]string
		sumRows  float64
		sumLoops float64
		sumMS    float64
		sumCPU   float64
	)

	for _, c := range counters {
		attrs := parseSSAttrs(c[1])
		if attrs["Thread"] == "0" {
			total = attrs
			break
		}
		sumRows += parseFloat(attrs["ActualRows"])
		sumLoops += parseFloat(attrs["ActualExecutions"])
		sumMS += parseFloat(attrs["ActualElapsedms"])
		sumCPU += parseFloat(attrs["ActualCPUms"])
	}

	n.Metrics.ActualValid = true

	if total != nil {
		n.Metrics.ActualRows = parseFloat(total["ActualRows"])
		n.Metrics.ActualLoops = parseFloat(total["ActualExecutions"])
		n.Metrics.ActualTotalMS = parseFloat(total["ActualElapsedms"])
		n.Metrics.ActualStartupMS = parseFloat(total["ActualCPUms"])
		return
	}

	n.Metrics.ActualRows = sumRows
	n.Metrics.ActualLoops = sumLoops
	n.Metrics.ActualTotalMS = sumMS
	n.Metrics.ActualStartupMS = sumCPU
}

// relOpBlocks returns the balanced blocks of the outermost RelOp elements.
//
// It is a small depth aware scanner: inner RelOps are captured as part of
// the outer block and converted recursively later.
func relOpBlocks(doc string) []string {
	var (
		blocks []string
		depth  int
		open   int
	)

	i := 0
	for i < len(doc) {
		lt := strings.IndexByte(doc[i:], '<')
		if lt < 0 {
			break
		}
		lt += i

		gt := strings.IndexByte(doc[lt:], '>')
		if gt < 0 {
			break
		}
		gt += lt
		tag := doc[lt : gt+1]
		next := gt + 1

		switch {
		case strings.HasPrefix(tag, "<!--"):
			if end := strings.Index(doc[lt+4:], "-->"); end >= 0 {
				i = lt + 4 + end + 3
				continue
			}
			return blocks
		case strings.HasPrefix(tag, "<?") || strings.HasPrefix(tag, "<!"):
			i = next
			continue
		}

		selfClose := strings.HasSuffix(tag[:len(tag)-1], "/")
		closing := strings.HasPrefix(tag, "</")
		name := ssTagName(tag)

		if name == "RelOp" {
			switch {
			case closing:
				if depth > 0 {
					depth--
				}
				if depth == 0 {
					blocks = append(blocks, doc[open:next])
				}
			case selfClose:
				if depth == 0 {
					blocks = append(blocks, tag)
				}
			default:
				if depth == 0 {
					open = lt
				}
				depth++
			}
		}

		i = next
	}

	return blocks
}

// ssTagName extracts the element name from a raw tag.
func ssTagName(tag string) string {
	t := strings.TrimPrefix(tag, "</")
	t = strings.TrimPrefix(t, "<")
	t = strings.TrimSuffix(t, ">")
	for i, r := range t {
		if r == ' ' || r == '\t' || r == '\n' || r == '/' {
			return t[:i]
		}
	}
	return t
}

var (
	ssConnectorRe = regexp.MustCompile(`^(\s*)[|\\]--\s*(.*)$`)
	ssBracketRe   = regexp.MustCompile(`\[([^\]]*)\]`)
)

// parseSQLServerText parses an indented text showplan.
func parseSQLServerText(raw string) (*Plan, error) {
	type entry struct {
		col  int
		node *Node
	}

	var (
		entries []entry
		roots   []*Node
	)

	positionRank := map[int]int{}

	for _, line := range strings.Split(raw, "\n") {
		m := ssConnectorRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		col := len(m[1])
		label := m[2]

		if _, ok := positionRank[col]; !ok {
			positionRank[col] = len(positionRank)
		}

		node := &Node{
			RawName:    label,
			Extra:      map[string]string{},
			RawPayload: line,
		}

		head := label
		if i := strings.IndexByte(head, '('); i >= 0 {
			head = strings.TrimSpace(head[:i])
		}
		node.OpType = Normalize(DialectSQLServer, head)

		fillSQLServerTextObject(node, label)

		entries = append(entries, entry{col: col, node: node})
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("parsing sqlserver text showplan: no operators found")
	}

	// Build the tree using indentation rank as the depth signal.
	var stack []entry
	for _, e := range entries {
		depth := positionRank[e.col]

		for len(stack) > 0 && positionRank[stack[len(stack)-1].col] >= depth {
			stack = stack[:len(stack)-1]
		}

		if len(stack) == 0 {
			roots = append(roots, e.node)
		} else {
			stack[len(stack)-1].node.Children = append(stack[len(stack)-1].node.Children, e.node)
		}

		stack = append(stack, e)
	}

	var root *Node
	if len(roots) == 1 {
		root = roots[0]
	} else {
		root = &Node{
			OpType:   OpResult,
			RawName:  "batch",
			Extra:    map[string]string{},
			Children: roots,
		}
	}

	return &Plan{
		Mode:       ModeExplain,
		Root:       root,
		CapturedAt: capturedAt(),
	}, nil
}

// fillSQLServerTextObject extracts OBJECT / SEEK / WHERE information.
func fillSQLServerTextObject(node *Node, label string) {
	if i := strings.Index(label, "OBJECT:("); i >= 0 {
		rest := label[i+len("OBJECT:"):]
		if inside, end, ok := balancedParens(rest); ok {
			tokens := ssBracketRe.FindAllStringSubmatch(inside, -1)
			if len(tokens) >= 3 {
				node.Object = tokens[1][1] + "." + tokens[2][1]
			}
			if len(tokens) >= 4 && tokens[3][1] != "" {
				node.Index = tokens[3][1]
			}
			_ = end
		}
	}

	var preds []string
	if i := strings.Index(label, "SEEK:("); i >= 0 {
		rest := label[i+len("SEEK:"):]
		if inside, _, ok := balancedParens(rest); ok {
			preds = append(preds, "seek: "+canonicalWhitespace(inside))
		}
	}
	if i := strings.Index(label, "WHERE:("); i >= 0 {
		rest := label[i+len("WHERE:"):]
		if inside, _, ok := balancedParens(rest); ok {
			preds = append(preds, "filter: "+canonicalWhitespace(inside))
		}
	}
	node.Predicate = strings.Join(preds, "; ")
}

// balancedParens returns the content of the first parenthesized group and
// the index right after its closing paren.
func balancedParens(s string) (string, int, bool) {
	if len(s) == 0 || s[0] != '(' {
		return "", 0, false
	}

	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[1:i], i + 1, true
			}
		}
	}

	return "", 0, false
}
