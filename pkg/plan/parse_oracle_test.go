package plan

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const oracleFixture = `Plan hash value: 365678559

------------------------------------------------------------------------------------
| Id  | Operation                    | Name    | Rows  | Bytes | Cost (%CPU)| Time     |
------------------------------------------------------------------------------------
|   0 | SELECT STATEMENT             |         |   100 |  5000 |     6  (34)| 00:00:01 |
|   1 |  HASH JOIN                   |         |   100 |  5000 |     6  (34)| 00:00:01 |
|*  2 |   TABLE ACCESS FULL          | ACTOR   |   200 |  4000 |     2   (0)| 00:00:01 |
|   3 |   TABLE ACCESS FULL          | FILM    |  1000 | 10000 |     2   (0)| 00:00:01 |
------------------------------------------------------------------------------------

Predicate Information (identified by operation id):
---------------------------------------------------

   2 - filter("LAST_NAME"='KILMER')
   1 - access("A"."ID"="B"."ID")

Note
-----
   - dynamic statistics used: dynamic sampling (level=2)
`

func TestParseOracle(t *testing.T) {
	t.Parallel()

	p, err := Parse("oracle", ModeExplain, nil, [][]string{{oracleFixture}})
	require.NoError(t, err)

	require.Equal(t, DialectOracle, p.Dialect)
	require.Equal(t, OpResult, p.Root.OpType)
	require.Equal(t, 6.0, p.Root.Metrics.TotalCost)
	require.Equal(t, 100.0, p.Root.Metrics.Rows)

	hashJoin := p.Root.Children[0]
	require.Equal(t, OpHashJoin, hashJoin.OpType)
	require.Equal(t, 6.0, hashJoin.Metrics.TotalCost)
	require.Len(t, hashJoin.Children, 2)

	actor := hashJoin.Children[0]
	require.Equal(t, OpSeqScan, actor.OpType)
	require.Equal(t, "ACTOR", actor.Object)
	require.Equal(t, 200.0, actor.Metrics.Rows)
	require.Contains(t, actor.Predicate, "KILMER")

	film := hashJoin.Children[1]
	require.Equal(t, OpSeqScan, film.OpType)
	require.Equal(t, "FILM", film.Object)
	require.Contains(t, hashJoin.Predicate, `A"."ID`)

	require.NotEmpty(t, actor.RawPayload)
	require.Contains(t, p.RawPayload, "SELECT STATEMENT")

	// Plan hash and notes are surfaced as warnings.
	require.Contains(t, p.Warnings, "plan hash value: 365678559")
	require.Contains(t, p.Warnings, "note: dynamic statistics used: dynamic sampling (level=2)")
}

const oracleIndexFixture = `-----------------------------------------------------------------------
| Id  | Operation                    | Name   | Rows  | Cost (%CPU)|
-----------------------------------------------------------------------
|   0 | SELECT STATEMENT             |        |    10 |     3  (34)|
|   1 |  TABLE ACCESS BY INDEX ROWID | ACTOR  |    10 |     3  (34)|
|*  2 |   INDEX RANGE SCAN           | IDX_A  |    10 |     1   (0)|
-----------------------------------------------------------------------

Predicate Information (identified by operation id):
---------------------------------------------------

   2 - access("LAST_NAME"='KILMER')
`

func TestParseOracleIndexAccess(t *testing.T) {
	t.Parallel()

	p, err := Parse("oracle", ModeExplain, nil, [][]string{{oracleIndexFixture}})
	require.NoError(t, err)

	tableAccess := p.Root.Children[0]
	require.Equal(t, OpTableAccessRowID, tableAccess.OpType)
	require.Equal(t, "ACTOR", tableAccess.Object)

	indexNode := tableAccess.Children[0]
	require.Equal(t, OpIndexScan, indexNode.OpType)
	require.Equal(t, "IDX_A", indexNode.Index)
	require.Equal(t, "", indexNode.Object)
	require.Contains(t, indexNode.Predicate, "KILMER")
}
