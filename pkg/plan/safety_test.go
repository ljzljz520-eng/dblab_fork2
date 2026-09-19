package plan

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGuardPlannerOnlyAlwaysAllowed(t *testing.T) {
	t.Parallel()

	// Even a write statement is allowed under planner-only EXPLAIN, because
	// the statement is never executed.
	d := Guard(DialectPostgres, "UPDATE actor SET last_name = 'X'", false)
	require.Equal(t, ActionAllow, d.Action)
	require.Empty(t, d.Reasons)
}

func TestGuardAnalyzeUnsupportedDialects(t *testing.T) {
	t.Parallel()

	for _, dialect := range []Dialect{DialectOracle, DialectSQLServer, DialectSQLite} {
		d := Guard(dialect, "SELECT * FROM actor", true)
		require.Equal(t, ActionBlock, d.Action)
		require.NotEmpty(t, d.Reasons)
		require.Contains(t, d.Reasons[0], "not supported")
	}
}

func TestGuardAnalyzeWriteStatements(t *testing.T) {
	t.Parallel()

	for _, query := range []string{
		"INSERT INTO actor VALUES (1)",
		"UPDATE actor SET x = 1",
		"DELETE FROM actor",
		"TRUNCATE TABLE actor",
		"MERGE INTO actor USING s ON (1) WHEN MATCHED THEN DELETE",
		"CALL my_proc()",
		"VACUUM actor",
	} {
		d := Guard(DialectPostgres, query, true)
		require.Equal(t, ActionBlock, d.Action, query)
		require.Contains(t, d.Reasons[0], "write statement", query)
	}
}

func TestGuardPostgresSequenceSideEffects(t *testing.T) {
	t.Parallel()

	d := Guard(DialectPostgres, "SELECT nextval('actor_id_seq')", true)
	require.Equal(t, ActionBlock, d.Action)
	require.Contains(t, d.Reasons[0], "sequences")

	d = Guard(DialectPostgres, "SELECT setval('s', 10)", true)
	require.Equal(t, ActionBlock, d.Action)
}

func TestGuardPostgresDBLink(t *testing.T) {
	t.Parallel()

	d := Guard(DialectPostgres, "SELECT * FROM dblink('host=db1', 'SELECT 1') AS t(x int)", true)
	require.Equal(t, ActionBlock, d.Action)
	require.Contains(t, d.Reasons[0], "dblink")

	d = Guard(DialectPostgres, "SELECT dblink_exec('host=db1', 'DELETE FROM x')", true)
	require.Equal(t, ActionBlock, d.Action)
}

func TestGuardAnalyzeReadIsolated(t *testing.T) {
	t.Parallel()

	d := Guard(DialectPostgres, "SELECT * FROM actor JOIN film ON actor.id = film.id", true)
	require.Equal(t, ActionIsolate, d.Action)
	require.Equal(t, DefaultAnalyzeTimeout, d.Timeout)
	require.Len(t, d.Reasons, 2)
	require.Contains(t, d.Reasons[0], "rolled back")
}

func TestGuardAnalyzeFunctionVolatility(t *testing.T) {
	t.Parallel()

	d := Guard(DialectMySQL, "SELECT risky_func(actor_id) FROM actor", true)
	require.Equal(t, ActionIsolate, d.Action)
	require.Len(t, d.Reasons, 3)
	require.Contains(t, d.Reasons[2], "volatility")
}

func TestGuardStripsExplainPrefix(t *testing.T) {
	t.Parallel()

	// Users may type EXPLAIN themselves in addition to the directive.
	d := Guard(DialectPostgres, "EXPLAIN ANALYZE UPDATE actor SET x = 1", true)
	require.Equal(t, ActionBlock, d.Action)
	require.Contains(t, d.Reasons[0], "write statement")
}

func TestAnalyzeTimeoutFromEnv(t *testing.T) {
	t.Setenv("DBLAB_EXPLAIN_TIMEOUT", "5s")
	require.Equal(t, 5*time.Second, AnalyzeTimeout())
}

func TestAnalyzeTimeoutInvalidEnvFallsBack(t *testing.T) {
	t.Setenv("DBLAB_EXPLAIN_TIMEOUT", "not-a-duration")
	require.Equal(t, DefaultAnalyzeTimeout, AnalyzeTimeout())
}

func TestAnalyzeSupported(t *testing.T) {
	t.Parallel()

	require.True(t, AnalyzeSupported(DialectPostgres))
	require.True(t, AnalyzeSupported(DialectMySQL))
	require.False(t, AnalyzeSupported(DialectOracle))
	require.False(t, AnalyzeSupported(DialectSQLServer))
	require.False(t, AnalyzeSupported(DialectSQLite))
}
