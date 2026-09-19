package plan

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const mysqlJSONFixture = `{
  "query_block": {
    "select_id": 1,
    "cost_info": {"query_cost": "35.00"},
    "nested_loop": [
      {
        "table": {
          "table_name": "actor",
          "access_type": "ref",
          "possible_keys": ["idx_actor_last_name"],
          "key": "idx_actor_last_name",
          "rows_examined_per_scan": 10,
          "rows_produced_per_join": 10,
          "filtered": "100.00",
          "cost_info": {"prefix_cost": "8.27"},
          "used_columns": ["actor_id", "last_name"]
        }
      },
      {
        "table": {
          "table_name": "film_actor",
          "access_type": "eq_ref",
          "possible_keys": ["PRIMARY"],
          "key": "PRIMARY",
          "rows_examined_per_scan": 1,
          "rows_produced_per_join": 10,
          "filtered": "100.00",
          "cost_info": {"prefix_cost": "20.00"},
          "used_columns": ["actor_id", "film_id"]
        }
      }
    ]
  }
}`

func TestParseMySQLJSON(t *testing.T) {
	t.Parallel()

	p, err := Parse("mysql", ModeExplain, nil, [][]string{{mysqlJSONFixture}})
	require.NoError(t, err)

	require.Equal(t, DialectMySQL, p.Dialect)
	require.Equal(t, ModeExplain, p.Mode)
	require.Equal(t, OpResult, p.Root.OpType)
	require.Equal(t, 35.0, p.Root.Metrics.TotalCost)

	nlJoin := p.Root.Children[0]
	require.Equal(t, OpNestedLoopJoin, nlJoin.OpType)
	require.Len(t, nlJoin.Children, 2)

	first := nlJoin.Children[0]
	require.Equal(t, OpIndexScan, first.OpType)
	require.Equal(t, "actor", first.Object)
	require.Equal(t, "idx_actor_last_name", first.Index)
	require.Equal(t, 10.0, first.Metrics.Rows)
	require.Equal(t, 8.27, first.Metrics.TotalCost)
	require.NotEmpty(t, first.RawPayload)

	second := nlJoin.Children[1]
	require.Equal(t, OpIndexScan, second.OpType)
	require.Equal(t, "film_actor", second.Object)
	require.Equal(t, "PRIMARY", second.Index)
}

func TestParseMySQLTraditional(t *testing.T) {
	t.Parallel()

	headers := []string{
		"id", "select_type", "table", "type", "possible_keys",
		"key", "key_len", "ref", "rows", "filtered", "Extra",
	}
	rows := [][]string{
		{"1", "SIMPLE", "actor", "ref", "idx_actor_last_name",
			"idx_actor_last_name", "137", "const", "10", "100.00", "Using where"},
		{"1", "SIMPLE", "film_actor", "eq_ref", "PRIMARY",
			"PRIMARY", "4", "func", "1", "100.00", "Using index"},
	}

	p, err := Parse("mysql", ModeExplain, headers, rows)
	require.NoError(t, err)

	require.Equal(t, OpNestedLoopJoin, p.Root.OpType)
	require.Len(t, p.Root.Children, 2)

	actor := p.Root.Children[0]
	require.Equal(t, OpIndexScan, actor.OpType)
	require.Equal(t, "actor", actor.Object)
	require.Equal(t, 10.0, actor.Metrics.Rows)

	// "Using index" promotes an index scan to index-only / covering.
	filmActor := p.Root.Children[1]
	require.Equal(t, OpIndexOnlyScan, filmActor.OpType)
	require.Equal(t, "Using index", filmActor.Extra["Extra"])
}

const mysqlIteratorFixture = `-> Nested loop inner join  (cost=35 rows=100) (actual time=0.05..0.5 rows=90 loops=1)
    -> Index range scan on actor using idx_actor_last_name  (cost=8.27 rows=10) (actual time=0.01..0.04 rows=10 loops=1)
    -> Filter: (film.rating = 'PG')  (actual time=0.01..0.02 rows=5 loops=10)
        -> Table scan on film  (cost=20 rows=1000) (actual time=0.01..0.1 rows=1000 loops=10)`

func TestParseMySQLIterator(t *testing.T) {
	t.Parallel()

	p, err := Parse("mysql", ModeExplain, nil, [][]string{{mysqlIteratorFixture}})
	require.NoError(t, err)

	require.Equal(t, ModeAnalyze, p.Mode)
	require.Equal(t, OpNestedLoopJoin, p.Root.OpType)
	require.Equal(t, 100.0, p.Root.Metrics.Rows)
	require.Equal(t, 90.0, p.Root.Metrics.ActualRows)
	require.True(t, p.Root.Metrics.HasActual())

	idxScan := p.Root.Children[0]
	require.Equal(t, OpIndexScan, idxScan.OpType)
	require.Equal(t, "actor", idxScan.Object)
	require.Equal(t, "idx_actor_last_name", idxScan.Index)

	filter := p.Root.Children[1]
	require.Equal(t, OpFilter, filter.OpType)
	require.Contains(t, filter.Predicate, "film.rating")
	require.Equal(t, 10.0, filter.Metrics.ActualLoops)

	seqScan := filter.Children[0]
	require.Equal(t, OpSeqScan, seqScan.OpType)
	require.Equal(t, "film", seqScan.Object)
	require.Equal(t, 1000.0, seqScan.Metrics.ActualRows)
}
