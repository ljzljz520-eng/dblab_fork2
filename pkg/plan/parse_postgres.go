package plan

import (
	"encoding/json"
	"fmt"
)

// pgExtraKeys are vendor specific fields lifted into Node.Extra.
var pgExtraKeys = []string{
	"Join Type",
	"Strategy",
	"Partial Mode",
	"Parallel Aware",
	"Hash Batches",
	"Original Hash Batches",
	"Hash Buckets",
	"Peak Memory Usage",
	"Sort Method",
	"Sort Space Type",
	"Sort Space Used",
	"Workers Planned",
	"Workers Launched",
	"Conflict Resolution",
	"One-Time Filter",
	"Operation",
	"Command",
	"Group Key",
	"Sort Key",
}

// parsePostgres parses the JSON output of
// EXPLAIN (FORMAT JSON) / EXPLAIN (ANALYZE, FORMAT JSON).
func parsePostgres(mode Mode, raw string) (*Plan, error) {
	var top []map[string]any
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		return nil, fmt.Errorf("parsing postgresql EXPLAIN JSON: %w", err)
	}

	if len(top) == 0 {
		return nil, fmt.Errorf("parsing postgresql EXPLAIN JSON: empty document")
	}

	planMap, ok := top[0]["Plan"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("parsing postgresql EXPLAIN JSON: missing top-level Plan")
	}

	// Actual execution metrics indicate EXPLAIN ANALYZE.
	if _, hasActual := planMap["Actual Total Time"]; hasActual {
		mode = ModeAnalyze
	}

	p := &Plan{
		Mode:       mode,
		Root:       convertPGNode(planMap),
		CapturedAt: capturedAt(),
	}

	if pt, ok := top[0]["Planning Time"].(float64); ok {
		p.Warnings = append(p.Warnings, fmt.Sprintf("planning time: %.3f ms", pt))
	}

	if et, ok := top[0]["Execution Time"].(float64); ok {
		p.Warnings = append(p.Warnings, fmt.Sprintf("execution time: %.3f ms", et))
	}

	return p, nil
}

// convertPGNode converts one PostgreSQL plan node recursively.
func convertPGNode(m map[string]any) *Node {
	rawName := mapString(m, "Node Type")

	n := &Node{
		OpType:   Normalize(DialectPostgres, rawName),
		RawName:  rawName,
		Object:   mapString(m, "Relation Name"),
		Alias:    mapString(m, "Alias"),
		Index:    mapString(m, "Index Name"),
		Extra:    map[string]string{},
		Children: convertPGChildren(m),
	}

	n.Metrics = Metrics{
		StartupCost: mapFloat(m, "Startup Cost"),
		TotalCost:   mapFloat(m, "Total Cost"),
		Rows:        mapFloat(m, "Plan Rows"),
		Width:       mapFloat(m, "Plan Width"),
	}

	if _, ok := m["Actual Total Time"]; ok {
		n.Metrics.ActualValid = true
		n.Metrics.ActualStartupMS = mapFloat(m, "Actual Startup Time")
		n.Metrics.ActualTotalMS = mapFloat(m, "Actual Total Time")
		n.Metrics.ActualRows = mapFloat(m, "Actual Rows")
		n.Metrics.ActualLoops = mapFloat(m, "Actual Loops")
		n.Metrics.SharedHitBlocks = mapFloat(m, "Shared Hit Blocks")
		n.Metrics.SharedReadBlocks = mapFloat(m, "Shared Read Blocks")
	}

	n.Predicate = firstNonEmptyPG(m,
		"Index Cond", "TID Cond", "Recheck Cond", "Filter", "Hash Cond", "Join Filter",
	)

	for _, k := range pgExtraKeys {
		if v := mapString(m, k); v != "" {
			n.Extra[k] = v
		}
	}

	n.RawPayload = compactJSON(m)

	return n
}

// convertPGChildren converts the nested Plans array, including init/subplans.
func convertPGChildren(m map[string]any) []*Node {
	plans, ok := m["Plans"].([]any)
	if !ok {
		return nil
	}

	children := make([]*Node, 0, len(plans))

	for _, p := range plans {
		if cm, ok := p.(map[string]any); ok {
			children = append(children, convertPGNode(cm))
		}
	}

	return children
}

// firstNonEmptyPG returns the first non-empty vendor field among the keys.
func firstNonEmptyPG(m map[string]any, keys ...string) string {
	return mapString(m, keys...)
}

// compactJSON returns the compact JSON encoding of a vendor node map.
func compactJSON(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}
