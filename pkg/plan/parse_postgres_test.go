package plan

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const pgJSONFixture = `
[
  {
    "Plan": {
      "Node Type": "Nested Loop",
      "Join Type": "Inner",
      "Startup Cost": 0.00,
      "Total Cost": 35.00,
      "Plan Rows": 100,
      "Plan Width": 50,
      "Actual Startup Time": 0.02,
      "Actual Total Time": 0.50,
      "Actual Rows": 90,
      "Actual Loops": 1,
      "Plans": [
        {
          "Node Type": "Index Scan",
          "Parent Relationship": "Outer",
          "Relation Name": "actor",
          "Alias": "a",
          "Index Name": "idx_actor_last_name",
          "Index Cond": "(a.last_name = 'KILMER')",
          "Startup Cost": 0.28,
          "Total Cost": 8.27,
          "Plan Rows": 10,
          "Plan Width": 18,
          "Actual Startup Time": 0.01,
          "Actual Total Time": 0.04,
          "Actual Rows": 10,
          "Actual Loops": 1
        },
        {
          "Node Type": "Hash Join",
          "Parent Relationship": "Inner",
          "Join Type": "Inner",
          "Startup Cost": 10.0,
          "Total Cost": 26.0,
          "Plan Rows": 90,
          "Plan Width": 32,
          "Actual Startup Time": 0.1,
          "Actual Total Time": 0.4,
          "Actual Rows": 80,
          "Actual Loops": 1,
          "Plans": [
            {
              "Node Type": "Seq Scan",
              "Parent Relationship": "Outer",
              "Relation Name": "film",
              "Startup Cost": 0.0,
              "Total Cost": 20.0,
              "Plan Rows": 1000,
              "Plan Width": 30,
              "Actual Startup Time": 0.0,
              "Actual Total Time": 0.1,
              "Actual Rows": 1000,
              "Actual Loops": 1
            },
            {
              "Node Type": "Hash",
              "Parent Relationship": "Inner",
              "Hash Batches": 1,
              "Hash Buckets": 1024,
              "Peak Memory Usage": 48,
              "Startup Cost": 0.0,
              "Total Cost": 0.0,
              "Plan Rows": 200,
              "Plan Width": 10,
              "Actual Startup Time": 0.0,
              "Actual Total Time": 0.05,
              "Actual Rows": 200,
              "Actual Loops": 1,
              "Plans": [
                {
                  "Node Type": "Seq Scan",
                  "Parent Relationship": "Outer",
                  "Relation Name": "inventory",
                  "Total Cost": 15.0,
                  "Plan Rows": 200,
                  "Plan Width": 10,
                  "Actual Total Time": 0.01,
                  "Actual Startup Time": 0.0,
                  "Actual Rows": 200,
                  "Actual Loops": 1
                }
              ]
            }
          ]
        }
      ]
    },
    "Planning Time": 0.20,
    "Execution Time": 0.80
  }
]
`

func TestParsePostgres(t *testing.T) {
	t.Parallel()

	p, err := Parse("postgres", ModeExplain, nil, [][]string{{pgJSONFixture}})
	require.NoError(t, err)

	require.Equal(t, DialectPostgres, p.Dialect)
	require.Equal(t, ModeAnalyze, p.Mode)
	require.Equal(t, OpNestedLoopJoin, p.Root.OpType)
	require.Equal(t, 35.0, p.Root.Metrics.TotalCost)
	require.Equal(t, 100.0, p.Root.Metrics.Rows)
	require.Equal(t, 90.0, p.Root.Metrics.ActualRows)
	require.True(t, p.Root.Metrics.HasActual())

	// Outer child: index scan with index and predicate lifted.
	idxScan := p.Root.Children[0]
	require.Equal(t, OpIndexScan, idxScan.OpType)
	require.Equal(t, "actor", idxScan.Object)
	require.Equal(t, "a", idxScan.Alias)
	require.Equal(t, "idx_actor_last_name", idxScan.Index)
	require.Contains(t, idxScan.Predicate, "KILMER")
	require.Equal(t, 8.27, idxScan.Metrics.TotalCost)

	// Inner child: hash join.
	hashJoin := p.Root.Children[1]
	require.Equal(t, OpHashJoin, hashJoin.OpType)
	require.Equal(t, "Inner", hashJoin.Extra["Join Type"])

	// The auxiliary Hash node MUST NOT be normalized as a hash join.
	hashNode := hashJoin.Children[1]
	require.Equal(t, OpHashBuild, hashNode.OpType)
	require.Equal(t, "1", hashNode.Extra["Hash Batches"])
	require.Equal(t, "1024", hashNode.Extra["Hash Buckets"])

	// The inner relation of the hash build is preserved.
	require.Equal(t, OpSeqScan, hashNode.Children[0].OpType)
	require.Equal(t, "inventory", hashNode.Children[0].Object)

	// Vendor payloads are preserved at node and plan level.
	require.NotEmpty(t, p.Root.RawPayload)
	require.NotEmpty(t, hashNode.RawPayload)
	require.Contains(t, p.RawPayload, "Nested Loop")

	// Top level timings are surfaced as warnings.
	require.Contains(t, p.Warnings, "planning time: 0.200 ms")
	require.Contains(t, p.Warnings, "execution time: 0.800 ms")

	// Stable path IDs assigned in pre-order.
	require.Equal(t, "0", p.Root.ID)
	require.Equal(t, "0.0", idxScan.ID)
}

func TestPostgresBiasRatio(t *testing.T) {
	t.Parallel()

	m := Metrics{Rows: 100, ActualRows: 4, ActualValid: true}

	ratio, ok := m.BiasRatio()
	require.True(t, ok)
	require.Equal(t, 25.0, ratio)
}

func TestParsePostgresInvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := Parse("postgresql", ModeExplain, nil, [][]string{{"{not json"}})
	require.Error(t, err)
}
