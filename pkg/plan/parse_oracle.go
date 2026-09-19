package plan

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	oraclePlanHashRe  = regexp.MustCompile(`Plan hash value:\s*(\d+)`)
	oraclePredicateRe = regexp.MustCompile(`^\s*(\d+)\s+-\s+(.*\S)\s*$`)
	oracleNoteRe      = regexp.MustCompile(`^\s+-\s+(.*\S)\s*$`)
)

// parseOracle parses the DBMS_XPLAN text output of EXPLAIN PLAN FOR.
func parseOracle(mode Mode, raw string) (*Plan, error) {
	lines := strings.Split(raw, "\n")

	var (
		nodes     []*oraclePlanRow
		predicate = map[int][]string{}
		warnings  []string
	)

	section := ""
	lastPredicateID := -1

	for _, line := range lines {
		if m := oraclePlanHashRe.FindStringSubmatch(line); m != nil {
			warnings = append(warnings, "plan hash value: "+m[1])
		}

		trimmed := strings.TrimSpace(line)

		// Track the sections after the plan table.
		switch {
		case strings.HasPrefix(trimmed, "Predicate Information"):
			section = "predicate"
			continue
		case trimmed == "Note":
			section = "note"
			continue
		case strings.HasPrefix(trimmed, "Query Block Name") ||
			strings.HasPrefix(trimmed, "Column Projection") ||
			strings.HasPrefix(trimmed, "Hint Report"):
			section = "other"
			continue
		}

		switch section {
		case "predicate":
			if m := oraclePredicateRe.FindStringSubmatch(line); m != nil {
				id, _ := strconv.Atoi(m[1])
				predicate[id] = append(predicate[id], strings.TrimSuffix(m[2], ")"))
				lastPredicateID = id
			} else if trimmed != "" && lastPredicateID >= 0 &&
				!strings.HasPrefix(trimmed, "---") {
				predicate[lastPredicateID] = append(predicate[lastPredicateID], trimmed)
			}
		case "note":
			if m := oracleNoteRe.FindStringSubmatch(line); m != nil {
				warnings = append(warnings, "note: "+m[1])
			}
		}

		if row, ok := parseOraclePlanLine(line); ok {
			nodes = append(nodes, row)
		}
	}

	root, err := buildOracleTree(nodes, predicate)
	if err != nil {
		return nil, err
	}

	return &Plan{
		Mode:       mode,
		Root:       root,
		Warnings:   warnings,
		CapturedAt: capturedAt(),
	}, nil
}

// oraclePlanRow is an intermediate representation of a DBMS_XPLAN row.
type oraclePlanRow struct {
	id      int
	depth   int
	rawName string
	name    string
	rows    float64
	cost    float64
	rawLine string
}

// parseOraclePlanLine parses one "| ... |" plan table row.
func parseOraclePlanLine(line string) (*oraclePlanRow, bool) {
	if !strings.HasPrefix(strings.TrimSpace(line), "|") {
		return nil, false
	}

	// Ignore separator and header lines.
	if strings.Contains(line, "---") {
		return nil, false
	}
	if strings.Contains(line, "Operation") {
		return nil, false
	}

	cells := strings.Split(line, "|")
	// Expected: "", id, operation, name, rows, bytes, cost, time, "".
	if len(cells) < 7 {
		return nil, false
	}

	idCell := strings.TrimSpace(strings.Trim(cells[1], "* "))
	if idCell == "" {
		// Wrapped continuation of the previous operation line.
		return nil, false
	}

	id, err := strconv.Atoi(idCell)
	if err != nil {
		return nil, false
	}

	opCell := cells[2]
	depth := 0
	for depth < len(opCell) && opCell[depth] == ' ' {
		depth++
	}
	if depth > 0 {
		depth--
	}

	row := &oraclePlanRow{
		id:      id,
		depth:   depth,
		rawName: strings.TrimSpace(opCell),
		name:    strings.TrimSpace(cells[3]),
		rows:    parseFloat(cells[4]),
		rawLine: line,
	}

	if len(cells) > 6 {
		if fields := strings.Fields(cells[6]); len(fields) > 0 {
			row.cost = parseFloat(fields[0])
		}
	}

	return row, true
}

// buildOracleTree assembles the rows into a Node tree using indentation.
func buildOracleTree(rows []*oraclePlanRow, predicates map[int][]string) (*Node, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("parsing oracle DBMS_XPLAN: no plan rows found")
	}

	var (
		stack []*Node
		root  *Node
	)

	for _, r := range rows {
		n := &Node{
			OpType:     Normalize(DialectOracle, r.rawName),
			RawName:    r.rawName,
			Index:      "",
			Extra:      map[string]string{},
			RawPayload: r.rawLine,
			Metrics: Metrics{
				TotalCost: r.cost,
				Rows:      r.rows,
			},
		}

		if r.name != "" {
			switch n.OpType {
			case OpIndexScan, OpBitmapIndexScan:
				n.Index = r.name
			default:
				n.Object = r.name
			}
		}

		if preds := predicates[r.id]; len(preds) > 0 {
			n.Predicate = strings.Join(preds, "; ")
		}

		for len(stack) > 0 && len(stack)-1 >= r.depth {
			stack = stack[:len(stack)-1]
		}

		if len(stack) == 0 {
			root = n
		} else {
			stack[len(stack)-1].Children = append(stack[len(stack)-1].Children, n)
		}

		stack = append(stack, n)
	}

	if root == nil {
		return nil, fmt.Errorf("parsing oracle DBMS_XPLAN: could not find root node")
	}

	return root, nil
}
