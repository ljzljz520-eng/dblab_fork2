package plan

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSQLiteSingleRoot(t *testing.T) {
	t.Parallel()

	headers := []string{"id", "parent", "notused", "detail"}
	rows := [][]string{
		{"1", "0", "0", "SCAN TABLE actor USING COVERING INDEX idx_x"},
		{"2", "1", "0", "USE TEMP B-TREE FOR ORDER BY"},
	}

	p, err := Parse("sqlite", ModeExplain, headers, rows)
	require.NoError(t, err)

	require.Equal(t, DialectSQLite, p.Dialect)
	require.Equal(t, OpIndexOnlyScan, p.Root.OpType)
	require.Equal(t, "actor", p.Root.Object)
	require.Equal(t, "idx_x", p.Root.Index)
	require.NotEmpty(t, p.Root.RawPayload)

	sortNode := p.Root.Children[0]
	require.Equal(t, OpSort, sortNode.OpType)
}

func TestParseSQLiteMultiRoot(t *testing.T) {
	t.Parallel()

	headers := []string{"id", "parent", "notused", "detail"}
	rows := [][]string{
		{"1", "0", "0", "SCAN TABLE actor"},
		{"2", "0", "0", "SEARCH actor USING INDEX idx_actor (last_name>? )"},
	}

	p, err := Parse("sqlite", ModeExplain, headers, rows)
	require.NoError(t, err)

	// Two top level nodes are wrapped under a RESULT node.
	require.Equal(t, OpResult, p.Root.OpType)
	require.Len(t, p.Root.Children, 2)

	seq := p.Root.Children[0]
	require.Equal(t, OpSeqScan, seq.OpType)
	require.Equal(t, "actor", seq.Object)

	idx := p.Root.Children[1]
	require.Equal(t, OpIndexScan, idx.OpType)
	require.Equal(t, "idx_actor", idx.Index)
	require.Equal(t, "last_name>?", idx.Predicate)
}

func TestParseSQLiteSubquery(t *testing.T) {
	t.Parallel()

	headers := []string{"id", "parent", "notused", "detail"}
	rows := [][]string{
		{"2", "0", "0", "SCALAR SUBQUERY 1"},
		{"3", "2", "0", "SCAN TABLE actor"},
	}

	p, err := Parse("sqlite", ModeExplain, headers, rows)
	require.NoError(t, err)

	subquery := p.Root
	require.Equal(t, OpSubquery, subquery.OpType)
	require.Equal(t, OpSeqScan, subquery.Children[0].OpType)
	require.Equal(t, "actor", subquery.Children[0].Object)
}
