package client

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/danvergara/dblab/pkg/plan"
)

func TestTrimPlanDirectivePlan(t *testing.T) {
	t.Parallel()

	inner, dir, ok := trimPlanDirective("SELECT * FROM actor | plan")
	require.True(t, ok)
	require.Equal(t, "SELECT * FROM actor", inner)
	require.Equal(t, plan.ModeExplain, dir.mode)
	require.Equal(t, baselineNone, dir.baselineOp)
}

func TestTrimPlanDirectiveAnalyze(t *testing.T) {
	t.Parallel()

	inner, dir, ok := trimPlanDirective("SELECT * FROM actor | analyze")
	require.True(t, ok)
	require.Equal(t, "SELECT * FROM actor", inner)
	require.Equal(t, plan.ModeAnalyze, dir.mode)
}

func TestTrimPlanDirectiveBaselineSave(t *testing.T) {
	t.Parallel()

	inner, dir, ok := trimPlanDirective("SELECT * FROM actor | baseline save prod")
	require.True(t, ok)
	require.Equal(t, "SELECT * FROM actor", inner)
	require.Equal(t, baselineSave, dir.baselineOp)
	require.Equal(t, "prod", dir.name)
}

func TestTrimPlanDirectiveBaselineDiff(t *testing.T) {
	t.Parallel()

	inner, dir, ok := trimPlanDirective("SELECT * FROM actor | baseline diff")
	require.True(t, ok)
	require.Equal(t, "SELECT * FROM actor", inner)
	require.Equal(t, baselineDiff, dir.baselineOp)
	require.Empty(t, dir.name)

	_, dir, ok = trimPlanDirective("SELECT * FROM actor | baseline diff v1")
	require.True(t, ok)
	require.Equal(t, baselineDiff, dir.baselineOp)
	require.Equal(t, "v1", dir.name)
}

func TestTrimPlanDirectiveBaselineList(t *testing.T) {
	t.Parallel()

	inner, dir, ok := trimPlanDirective("SELECT * FROM actor | baseline list")
	require.True(t, ok)
	require.Equal(t, "SELECT * FROM actor", inner)
	require.Equal(t, baselineList, dir.baselineOp)

	// Bare "baseline" defaults to list.
	_, dir, ok = trimPlanDirective("SELECT * FROM actor | baseline")
	require.True(t, ok)
	require.Equal(t, baselineList, dir.baselineOp)
}

func TestTrimPlanDirectiveCaseInsensitive(t *testing.T) {
	t.Parallel()

	_, dir, ok := trimPlanDirective("SELECT 1 |  PLAN")
	require.True(t, ok)
	require.Equal(t, plan.ModeExplain, dir.mode)

	_, dir, ok = trimPlanDirective("SELECT 1 | Baseline Save My Baseline")
	require.True(t, ok)
	require.Equal(t, baselineSave, dir.baselineOp)
	require.Equal(t, "My Baseline", dir.name)
}

func TestTrimPlanDirectiveNoSuffix(t *testing.T) {
	t.Parallel()

	_, _, ok := trimPlanDirective("SELECT * FROM actor")
	require.False(t, ok)

	// Other suffix directives are not plan directives.
	_, _, ok = trimPlanDirective("SELECT * FROM actor | json")
	require.False(t, ok)

	// A pipe in the middle of the query is not a suffix directive.
	_, _, ok = trimPlanDirective("SELECT 1 | 2")
	require.False(t, ok)
}

func TestInjectMySQLMaxExecutionTime(t *testing.T) {
	t.Parallel()

	stmt := "SELECT * FROM actor"
	out := injectMySQLMaxExecutionTime(stmt, 5000)
	require.Contains(t, out, "MAX_EXECUTION_TIME(5000)")
	require.Contains(t, out, "* FROM actor")

	// Non-SELECT statements keep the hint out (handled by the safety guard).
	update := "UPDATE actor SET x = 1"
	require.Equal(t, update, injectMySQLMaxExecutionTime(update, 5000))
}
