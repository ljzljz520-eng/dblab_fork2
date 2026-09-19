package plan

import (
	"fmt"
	"math"
	"strings"
)

// ChangeTag classifies the kind of change detected on a node.
type ChangeTag string

const (
	// TagAdded means the node only exists in the newer plan.
	TagAdded ChangeTag = "NODE_ADDED"
	// TagRemoved means the node only exists in the older plan.
	TagRemoved ChangeTag = "NODE_REMOVED"
	// TagOpChanged means the operator category changed.
	TagOpChanged ChangeTag = "OP_CHANGED"
	// TagIndexChanged means the selected index changed.
	TagIndexChanged ChangeTag = "INDEX_CHANGED"
	// TagCostChanged means the total cost changed.
	TagCostChanged ChangeTag = "COST_CHANGED"
	// TagRowsChanged means the estimated cardinality changed.
	TagRowsChanged ChangeTag = "ROWS_CHANGED"
	// TagActualChanged means the actual row count changed.
	TagActualChanged ChangeTag = "ACTUAL_CHANGED"
)

// NodeChange describes every difference detected for one operator.
type NodeChange struct {
	// Path is the node path in the newer plan (empty for removals).
	Path string
	// OldPath is the node path in the older plan (empty for additions).
	OldPath string
	// Tags holds all change categories detected for the node.
	Tags []ChangeTag
	// OpType is the operator category in the newer plan.
	OpType OpType
	// OldOpType is the operator category in the older plan.
	OldOpType OpType
	// Object is the relation the node refers to.
	Object string
	// Index is the index in the newer plan.
	Index string
	// OldIndex is the index in the older plan.
	OldIndex string
	// CostOld / CostNew are the total costs.
	CostOld float64
	CostNew float64
	// RowsOld / RowsNew are the estimated cardinalities.
	RowsOld float64
	RowsNew float64
	// ActualOld / ActualNew are the actual row counts.
	ActualOld float64
	ActualNew float64
}

// HasTag reports whether the change carries the given tag.
func (c NodeChange) HasTag(tag ChangeTag) bool {
	for _, t := range c.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// PlanDiff is the stable comparison of two plans of the same query.
type PlanDiff struct {
	// Dialect is the common dialect of both plans.
	Dialect Dialect
	// Changes is the ordered list of node changes.
	Changes []NodeChange
}

// Diff compares two plans and returns their stable, ordered difference.
//
// Nodes are aligned first by operator type and relation, then by operator
// type alone, and finally by ordinal position, so that reordering neither
// hides real changes nor produces spurious ones.
func Diff(oldPlan, newPlan *Plan) *PlanDiff {
	d := &PlanDiff{Dialect: newPlan.Dialect}

	if oldPlan != nil && oldPlan.Root != nil && newPlan != nil && newPlan.Root != nil {
		alignNodes(oldPlan.Root, newPlan.Root, &d.Changes)
	}

	return d
}

// childPair pairs an old child with a new child, either side may be nil.
type childPair struct {
	o *Node
	n *Node
}

// alignNodes compares two nodes and recursively aligns their children.
func alignNodes(o, n *Node, changes *[]NodeChange) {
	if change := compareNodes(o, n); change != nil {
		*changes = append(*changes, *change)
	}

	for _, pair := range pairChildren(o.Children, n.Children) {
		switch {
		case pair.o == nil:
			collectAdded(pair.n, changes)
		case pair.n == nil:
			collectRemoved(pair.o, changes)
		default:
			alignNodes(pair.o, pair.n, changes)
		}
	}
}

// pairChildren aligns two children lists with the three-pass strategy.
func pairChildren(oldKids, newKids []*Node) []childPair {
	matched := make([]int, len(newKids))
	usedOld := make([]bool, len(oldKids))

	for i := range matched {
		matched[i] = -1
	}

	// Pass 1: identical signature (operator type + object).
	for ni, nch := range newKids {
		nsig := nodeSignature(nch)
		for oi, och := range oldKids {
			if !usedOld[oi] && nodeSignature(och) == nsig {
				matched[ni] = oi
				usedOld[oi] = true
				break
			}
		}
	}

	// Pass 2: same operator type.
	for ni := 0; ni < len(newKids); ni++ {
		if matched[ni] >= 0 {
			continue
		}
		for oi, och := range oldKids {
			if !usedOld[oi] && och.OpType == newKids[ni].OpType {
				matched[ni] = oi
				usedOld[oi] = true
				break
			}
		}
	}

	// Pass 3: pair leftovers by ordinal position.
	unused := make([]int, 0)
	for oi, used := range usedOld {
		if !used {
			unused = append(unused, oi)
		}
	}

	ui := 0
	for ni := 0; ni < len(newKids); ni++ {
		if matched[ni] < 0 && ui < len(unused) {
			matched[ni] = unused[ui]
			usedOld[unused[ui]] = true
			ui++
		}
	}

	pairs := make([]childPair, 0, len(newKids)+len(unused)-ui)

	for ni, oi := range matched {
		if oi >= 0 {
			pairs = append(pairs, childPair{o: oldKids[oi], n: newKids[ni]})
		} else {
			pairs = append(pairs, childPair{n: newKids[ni]})
		}
	}

	// Removed nodes keep their old order after the matched slots.
	for ; ui < len(unused); ui++ {
		pairs = append(pairs, childPair{o: oldKids[unused[ui]]})
	}

	return pairs
}

// nodeSignature returns the stable identity of a node for alignment.
func nodeSignature(n *Node) string {
	return string(n.OpType) + "|" + strings.ToLower(n.Object)
}

// compareNodes compares two paired nodes and returns their change record.
func compareNodes(o, n *Node) *NodeChange {
	c := NodeChange{
		OldPath:   o.ID,
		Path:      n.ID,
		OldOpType: o.OpType,
		OpType:    n.OpType,
		Object:    n.Object,
		OldIndex:  o.Index,
		Index:     n.Index,
		CostOld:   o.Metrics.TotalCost,
		CostNew:   n.Metrics.TotalCost,
		RowsOld:   o.Metrics.Rows,
		RowsNew:   n.Metrics.Rows,
	}

	if o.Metrics.ActualValid && n.Metrics.ActualValid {
		c.ActualOld = o.Metrics.ActualRows
		c.ActualNew = n.Metrics.ActualRows
	}

	if o.OpType != n.OpType {
		c.Tags = append(c.Tags, TagOpChanged)
	}

	if o.Index != n.Index {
		c.Tags = append(c.Tags, TagIndexChanged)
	}

	if math.Abs(o.Metrics.TotalCost-n.Metrics.TotalCost) > 1e-9 {
		c.Tags = append(c.Tags, TagCostChanged)
	}

	if math.Abs(o.Metrics.Rows-n.Metrics.Rows) > 1e-9 {
		c.Tags = append(c.Tags, TagRowsChanged)
	}

	if math.Abs(c.ActualOld-c.ActualNew) > 1e-9 {
		c.Tags = append(c.Tags, TagActualChanged)
	}

	if len(c.Tags) == 0 {
		return nil
	}

	return &c
}

// collectAdded records an added node and all of its subtree.
func collectAdded(n *Node, changes *[]NodeChange) {
	c := &NodeChange{
		Path:    n.ID,
		Tags:    []ChangeTag{TagAdded},
		OpType:  n.OpType,
		Object:  n.Object,
		Index:   n.Index,
		CostNew: n.Metrics.TotalCost,
		RowsNew: n.Metrics.Rows,
	}
	*changes = append(*changes, *c)

	for _, ch := range n.Children {
		collectAdded(ch, changes)
	}
}

// collectRemoved records a removed node and all of its subtree.
func collectRemoved(n *Node, changes *[]NodeChange) {
	c := &NodeChange{
		OldPath:   n.ID,
		Tags:      []ChangeTag{TagRemoved},
		OldOpType: n.OpType,
		Object:    n.Object,
		OldIndex:  n.Index,
		CostOld:   n.Metrics.TotalCost,
		RowsOld:   n.Metrics.Rows,
	}
	*changes = append(*changes, *c)

	for _, ch := range n.Children {
		collectRemoved(ch, changes)
	}
}

// Render renders the plan diff as stable text, grouped by change category.
func (d *PlanDiff) Render() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Plan Diff · %s\n", d.Dialect)
	b.WriteString(strings.Repeat("─", 60))
	b.WriteByte('\n')

	if len(d.Changes) == 0 {
		b.WriteString("No changes detected: the operator tree is identical.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "Summary: %s\n\n", d.renderSummary())

	section := func(title string, tag ChangeTag, line func(c NodeChange) string) {
		var rows []string
		for _, c := range d.Changes {
			if c.HasTag(tag) {
				rows = append(rows, line(c))
			}
		}
		if len(rows) == 0 {
			return
		}
		b.WriteString(title)
		b.WriteByte('\n')
		for _, r := range rows {
			b.WriteString("  ")
			b.WriteString(r)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	// Structural changes combine operator swaps, additions and removals.
	var structural []string
	for _, c := range d.Changes {
		switch {
		case c.HasTag(TagOpChanged):
			structural = append(structural, fmt.Sprintf("OP      %s: %s → %s (%s)",
				pathLabel(c), c.OldOpType, c.OpType, objectLabel(c.Object)))
		case c.HasTag(TagAdded):
			structural = append(structural, fmt.Sprintf("ADDED   %s %s %s",
				c.Path, c.OpType, nodeTargetLabel(c.Object, c.Index)))
		case c.HasTag(TagRemoved):
			structural = append(structural, fmt.Sprintf("REMOVED %s %s %s",
				c.OldPath, c.OldOpType, nodeTargetLabel(c.Object, c.OldIndex)))
		}
	}
	if len(structural) > 0 {
		b.WriteString("Structural Changes\n")
		for _, r := range structural {
			b.WriteString("  ")
			b.WriteString(r)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	section("Index Selection Changes", TagIndexChanged, func(c NodeChange) string {
		oi, ni := c.OldIndex, c.Index
		if oi == "" {
			oi = "<none>"
		}
		if ni == "" {
			ni = "<none>"
		}
		return fmt.Sprintf("%s: %s → %s (%s)", pathLabel(c), oi, ni, objectLabel(c.Object))
	})
	section("Cost Changes", TagCostChanged, func(c NodeChange) string {
		delta := c.CostNew - c.CostOld
		return fmt.Sprintf("%s %s: %s → %s (Δ%+s)",
			pathLabel(c), c.OpType, trimFloat(c.CostOld), trimFloat(c.CostNew), trimFloat(delta))
	})
	section("Cardinality Changes", TagRowsChanged, func(c NodeChange) string {
		ratio := ""
		if c.RowsOld > 0 {
			ratio = fmt.Sprintf(" (%.2fx)", c.RowsNew/c.RowsOld)
		}
		return fmt.Sprintf("%s %s: %s → %s%s",
			pathLabel(c), c.OpType, trimFloat(c.RowsOld), trimFloat(c.RowsNew), ratio)
	})
	section("Actual Cardinality Changes", TagActualChanged, func(c NodeChange) string {
		return fmt.Sprintf("%s %s actual rows: %s → %s",
			pathLabel(c), c.OpType, trimFloat(c.ActualOld), trimFloat(c.ActualNew))
	})

	return b.String()
}

// renderSummary counts the tags across all changes.
func (d *PlanDiff) renderSummary() string {
	counts := map[ChangeTag]int{}
	for _, c := range d.Changes {
		for _, t := range c.Tags {
			counts[t]++
		}
	}

	order := []struct {
		tag ChangeTag
		lbl string
	}{
		{TagOpChanged, "operator"},
		{TagAdded, "added"},
		{TagRemoved, "removed"},
		{TagIndexChanged, "index"},
		{TagCostChanged, "cost"},
		{TagRowsChanged, "cardinality"},
		{TagActualChanged, "actual rows"},
	}

	var parts []string
	for _, o := range order {
		if counts[o.tag] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[o.tag], o.lbl))
		}
	}

	return strings.Join(parts, " · ")
}

// pathLabel returns the best path reference for a change.
func pathLabel(c NodeChange) string {
	switch {
	case c.Path != "":
		return "#" + c.Path
	case c.OldPath != "":
		return "#" + c.OldPath
	default:
		return "#?"
	}
}

// objectLabel renders the object or a dash when absent.
func objectLabel(object string) string {
	if object == "" {
		return "-"
	}
	return object
}

// nodeTargetLabel renders object and index for add/remove lines.
func nodeTargetLabel(object, index string) string {
	switch {
	case object != "" && index != "":
		return object + " (" + index + ")"
	case object != "":
		return object
	case index != "":
		return "(" + index + ")"
	default:
		return ""
	}
}
