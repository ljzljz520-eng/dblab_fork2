package plan

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	sqliteIndexRe = regexp.MustCompile(`(?i)USING (?:COVERING )?INDEX (\S+)`)
	sqliteParenRe = regexp.MustCompile(`\(([^()]*)\)\s*$`)
)

// parseSQLite parses EXPLAIN QUERY PLAN rows:
// id | parent | notused | detail.
//
// The parent references are used directly to build the tree.
func parseSQLite(mode Mode, headers []string, rows [][]string) (*Plan, error) {
	idx := columnIndex(headers)

	col := func(cells []string, name string) string {
		i, ok := idx[name]
		if !ok || i >= len(cells) {
			return ""
		}
		return cells[i]
	}

	type entry struct {
		id     int
		parent int
		node   *Node
	}

	var (
		entries []entry
		ids     = map[int]bool{}
	)

	for _, cells := range rows {
		id, err := strconv.Atoi(strings.TrimSpace(col(cells, "id")))
		if err != nil {
			continue
		}

		parent, _ := strconv.Atoi(strings.TrimSpace(col(cells, "parent")))
		detail := strings.TrimSpace(col(cells, "detail"))

		node := &Node{
			OpType:     Normalize(DialectSQLite, detail),
			RawName:    detail,
			Object:     sqliteObject(detail),
			Index:      sqliteIndex(detail),
			Predicate:  sqlitePredicate(detail),
			Extra:      map[string]string{},
			RawPayload: strings.Join(cells, "|"),
		}

		entries = append(entries, entry{id: id, parent: parent, node: node})
		ids[id] = true
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("parsing sqlite EXPLAIN QUERY PLAN: no plan rows found")
	}

	nodesByID := make(map[int]*Node, len(entries))
	children := map[int][]*Node{}

	var roots []*Node

	for _, e := range entries {
		nodesByID[e.id] = e.node
	}

	for _, e := range entries {
		if ids[e.parent] {
			children[e.parent] = append(children[e.parent], e.node)
		} else {
			roots = append(roots, e.node)
		}
	}

	for id, kids := range children {
		if n, ok := nodesByID[id]; ok {
			n.Children = kids
		}
	}

	var root *Node
	if len(roots) == 1 {
		root = roots[0]
	} else {
		root = &Node{
			OpType:   OpResult,
			RawName:  "query plan",
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

// sqliteObject extracts the relation name from a detail line.
func sqliteObject(detail string) string {
	low := strings.ToLower(detail)

	var after string

	switch {
	case strings.HasPrefix(low, "scan table "):
		after = detail[len("scan table "):]
	case strings.HasPrefix(low, "search table "):
		after = detail[len("search table "):]
	case strings.HasPrefix(low, "scan "):
		after = detail[len("scan "):]
	case strings.HasPrefix(low, "search "):
		after = detail[len("search "):]
	default:
		return ""
	}

	end := strings.IndexAny(after, " \t(")
	if end < 0 {
		end = len(after)
	}

	return strings.Trim(after[:end], "`")
}

// sqliteIndex extracts the index name from a detail line.
func sqliteIndex(detail string) string {
	m := sqliteIndexRe.FindStringSubmatch(detail)
	if m == nil {
		return ""
	}
	return strings.Trim(m[1], "`")
}

// sqlitePredicate extracts the trailing parenthesized access expression.
func sqlitePredicate(detail string) string {
	m := sqliteParenRe.FindStringSubmatch(detail)
	if m == nil {
		return ""
	}

	expr := strings.TrimSpace(m[1])
	// Skip empty predicates and subquery markers such as "SUBQUERY 1".
	if expr == "" || strings.Contains(strings.ToLower(expr), "subquery") {
		return ""
	}

	return expr
}
