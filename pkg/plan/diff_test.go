package plan

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// makeDiffPlans builds two versions of the same query plan.
//
// The newer version changes the root cost, the index selection and the
// cardinality estimate of the scan and adds a sort node.
func makeDiffPlans() (*Plan, *Plan) {
	oldRoot := &Node{
		OpType:  OpNestedLoopJoin,
		RawName: "Nested Loop",
		Extra:   map[string]string{},
		Metrics: Metrics{TotalCost: 35, Rows: 100},
		Children: []*Node{
			{
				OpType:  OpIndexScan,
				RawName: "Index Scan",
				Object:  "actor",
				Index:   "idx_a",
				Extra:   map[string]string{},
				Metrics: Metrics{TotalCost: 8, Rows: 100},
			},
			{
				OpType:  OpSeqScan,
				RawName: "Seq Scan",
				Object:  "film",
				Extra:   map[string]string{},
				Metrics: Metrics{TotalCost: 20, Rows: 1000},
			},
		},
	}

	newRoot := &Node{
		OpType:  OpNestedLoopJoin,
		RawName: "Nested Loop",
		Extra:   map[string]string{},
		Metrics: Metrics{TotalCost: 40, Rows: 100},
		Children: []*Node{
			{
				OpType:  OpIndexScan,
				RawName: "Index Scan",
				Object:  "actor",
				Index:   "idx_b",
				Extra:   map[string]string{},
				Metrics: Metrics{TotalCost: 9, Rows: 25},
			},
			{
				OpType:  OpSeqScan,
				RawName: "Seq Scan",
				Object:  "film",
				Extra:   map[string]string{},
				Metrics: Metrics{TotalCost: 20, Rows: 1000},
			},
			{
				OpType:  OpSort,
				RawName: "Sort",
				Object:  "",
				Extra:   map[string]string{},
				Metrics: Metrics{TotalCost: 2, Rows: 100},
			},
		},
	}

	assignIDs(oldRoot, "0")
	assignIDs(newRoot, "0")

	oldPlan := &Plan{Dialect: DialectPostgres, Root: oldRoot, Mode: ModeExplain}
	newPlan := &Plan{Dialect: DialectPostgres, Root: newRoot, Mode: ModeExplain}

	return oldPlan, newPlan
}

func TestDiffDetectsChanges(t *testing.T) {
	t.Parallel()

	oldPlan, newPlan := makeDiffPlans()
	d := Diff(oldPlan, newPlan)

	// Locate the root cost change.
	var rootChange *NodeChange
	for i := range d.Changes {
		if d.Changes[i].Path == "0" {
			rootChange = &d.Changes[i]
			break
		}
	}
	require.NotNil(t, rootChange)
	require.True(t, rootChange.HasTag(TagCostChanged))
	require.Equal(t, 35.0, rootChange.CostOld)
	require.Equal(t, 40.0, rootChange.CostNew)

	// Scan node: index selection + cost + cardinality changes.
	var scanChange *NodeChange
	for i := range d.Changes {
		if d.Changes[i].Path == "0.0" {
			scanChange = &d.Changes[i]
			break
		}
	}
	require.NotNil(t, scanChange)
	require.True(t, scanChange.HasTag(TagIndexChanged))
	require.Equal(t, "idx_a", scanChange.OldIndex)
	require.Equal(t, "idx_b", scanChange.Index)
	require.True(t, scanChange.HasTag(TagRowsChanged))
	require.Equal(t, 100.0, scanChange.RowsOld)
	require.Equal(t, 25.0, scanChange.RowsNew)

	// Added sort node.
	var added *NodeChange
	for i := range d.Changes {
		if d.Changes[i].Path == "0.2" && d.Changes[i].HasTag(TagAdded) {
			added = &d.Changes[i]
			break
		}
	}
	require.NotNil(t, added)
	require.Equal(t, OpSort, added.OpType)

	// Rendered output contains every section.
	view := d.Render()
	require.Contains(t, view, "Plan Diff")
	require.Contains(t, view, "Index Selection Changes")
	require.Contains(t, view, "Cost Changes")
	require.Contains(t, view, "Cardinality Changes")
	require.Contains(t, view, "ADDED")
	require.Contains(t, view, "0.25x")
}

func TestDiffStable(t *testing.T) {
	t.Parallel()

	oldPlan, newPlan := makeDiffPlans()

	first := Diff(oldPlan, newPlan).Render()
	second := Diff(oldPlan, newPlan).Render()

	require.Equal(t, first, second)
}

func TestDiffIdenticalPlans(t *testing.T) {
	t.Parallel()

	oldPlan, _ := makeDiffPlans()
	d := Diff(oldPlan, oldPlan)

	require.Empty(t, d.Changes)
	require.Contains(t, d.Render(), "identical")
}

func TestDiffOperatorSwap(t *testing.T) {
	t.Parallel()

	oldRoot := &Node{
		OpType: OpNestedLoopJoin,
		Extra:  map[string]string{},
		Children: []*Node{
			{OpType: OpSeqScan, Object: "t", Extra: map[string]string{}},
		},
	}

	newRoot := &Node{
		OpType: OpHashJoin,
		Extra:  map[string]string{},
		Children: []*Node{
			{OpType: OpSeqScan, Object: "t", Extra: map[string]string{}},
		},
	}

	assignIDs(oldRoot, "0")
	assignIDs(newRoot, "0")

	oldPlan := &Plan{Dialect: DialectPostgres, Root: oldRoot}
	newPlan := &Plan{Dialect: DialectPostgres, Root: newRoot}

	d := Diff(oldPlan, newPlan)

	var opChange *NodeChange
	for i := range d.Changes {
		if d.Changes[i].HasTag(TagOpChanged) {
			opChange = &d.Changes[i]
			break
		}
	}
	require.NotNil(t, opChange)
	require.Equal(t, OpNestedLoopJoin, opChange.OldOpType)
	require.Equal(t, OpHashJoin, opChange.OpType)
}
