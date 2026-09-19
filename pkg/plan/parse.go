package plan

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// capturedAt returns the current time, centralised so parsers stay uniform.
func capturedAt() time.Time {
	return time.Now()
}

// DialectFromDriver normalizes a dblab driver name into a Dialect.
func DialectFromDriver(driverName string) Dialect {
	switch strings.ToLower(strings.TrimSpace(driverName)) {
	case "postgres", "postgresql", "postgres+ssh":
		return DialectPostgres
	case "mysql":
		return DialectMySQL
	case "oracle":
		return DialectOracle
	case "sqlserver", "mssql":
		return DialectSQLServer
	case "sqlite":
		return DialectSQLite
	default:
		return Dialect(strings.ToLower(strings.TrimSpace(driverName)))
	}
}

// Parse converts the raw result grid of an EXPLAIN-like statement into a
// unified Plan.
//
// The mode is a hint: parsers upgrade it to ModeAnalyze when they find
// actual execution metrics in the payload. Every node keeps the exact raw
// snippet it was built from, and the plan keeps the complete raw payload.
func Parse(driverName string, mode Mode, headers []string, rows [][]string) (*Plan, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("no plan rows returned by %s", driverName)
	}

	dialect := DialectFromDriver(driverName)
	raw := gridToRaw(headers, rows)

	var (
		p   *Plan
		err error
	)

	switch dialect {
	case DialectPostgres:
		p, err = parsePostgres(mode, raw)
	case DialectMySQL:
		p, err = parseMySQL(mode, raw)
	case DialectOracle:
		p, err = parseOracle(mode, raw)
	case DialectSQLServer:
		p, err = parseSQLServer(mode, raw)
	case DialectSQLite:
		p, err = parseSQLite(mode, headers, rows)
	default:
		return nil, fmt.Errorf("plan parsing not supported for driver %q", driverName)
	}

	if err != nil {
		return nil, err
	}

	if p == nil || p.Root == nil {
		return nil, fmt.Errorf("could not extract an operator tree from the %s output", dialect)
	}

	p.Dialect = dialect
	p.RawPayload = raw

	assignIDs(p.Root, "0")

	return p, nil
}

// gridToRaw collapses an EXPLAIN result grid into its textual payload.
//
// Single column outputs (JSON, XML, DBMS_XPLAN lines) are joined into one
// document; wider grids (traditional EXPLAIN, EXPLAIN QUERY PLAN) are turned
// into a tab separated document including the header row.
func gridToRaw(headers []string, rows [][]string) string {
	oneCol := true
	for _, r := range rows {
		if len(r) != 1 {
			oneCol = false
			break
		}
	}

	if oneCol {
		lines := make([]string, 0, len(rows))
		for _, r := range rows {
			lines = append(lines, r[0])
		}
		return strings.Join(lines, "\n")
	}

	var b strings.Builder

	if len(headers) > 0 {
		b.WriteString(strings.Join(headers, "\t"))
		b.WriteByte('\n')
	}

	for _, r := range rows {
		b.WriteString(strings.Join(r, "\t"))
		b.WriteByte('\n')
	}

	return strings.TrimRight(b.String(), "\n")
}

// assignIDs walks the tree in pre-order assigning stable path IDs.
func assignIDs(n *Node, id string) {
	n.ID = id
	for i, ch := range n.Children {
		assignIDs(ch, fmt.Sprintf("%s.%d", id, i))
	}
}

// parseFloat parses a vendor number, tolerating commas, spaces and quotes.
//
// It returns 0 without an error for empty or unparsable input: vendor
// payloads frequently omit optional metrics.
func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	// Strip thousands separators and wrapping quotes.
	s = strings.ReplaceAll(s, ",", "")
	s = strings.Trim(s, "'\"")

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}

	return f
}

// mapString returns the first non-empty string value found among the keys.
func mapString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch val := v.(type) {
			case string:
				if strings.TrimSpace(val) != "" {
					return val
				}
			case float64:
				if val != 0 {
					return strconv.FormatFloat(val, 'f', -1, 64)
				}
			case bool:
				if val {
					return "true"
				}
			}
		}
	}
	return ""
}

// mapFloat returns the numeric value of the first matching key.
func mapFloat(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch val := v.(type) {
			case float64:
				return val
			case string:
				return parseFloat(val)
			}
		}
	}
	return 0
}
