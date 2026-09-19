package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/danvergara/dblab/pkg/plan"
)

// baselineOp is the action requested through the | baseline directive.
type baselineOp string

const (
	baselineNone baselineOp = ""
	baselineSave baselineOp = "save"
	baselineDiff baselineOp = "diff"
	baselineList baselineOp = "list"
)

// directive is a parsed plan suffix of a user query.
type directive struct {
	mode       plan.Mode
	baselineOp baselineOp
	name       string
}

var planDirectiveRe = regexp.MustCompile(
	`(?i)\s*\|\s*(plan|analyze|baseline)(?:\s+(save|diff|list)(?:\s+(.+))?)?\s*$`)

var stripExplainRe = regexp.MustCompile(`(?i)^\s*EXPLAIN\s+(?:ANALYZE\s+)?(.*)$`)

// trimPlanDirective reports whether the query carries a plan suffix,
// returning the inner statement and the parsed directive.
func trimPlanDirective(query string) (string, directive, bool) {
	m := planDirectiveRe.FindStringSubmatchIndex(query)
	if m == nil {
		return query, directive{}, false
	}

	keyword := strings.ToLower(strings.TrimSpace(query[m[2]:m[3]]))

	dir := directive{mode: plan.ModeExplain}
	inner := strings.TrimSpace(query[:m[0]])

	switch keyword {
	case "plan":
		dir.mode = plan.ModeExplain
	case "analyze":
		dir.mode = plan.ModeAnalyze
	case "baseline":
		op := ""
		if m[4] >= 0 {
			op = strings.ToLower(strings.TrimSpace(query[m[4]:m[5]]))
		}
		if m[6] >= 0 {
			dir.name = strings.TrimSpace(query[m[6]:m[7]])
		}

		switch baselineOp(op) {
		case baselineSave:
			dir.baselineOp = baselineSave
		case baselineDiff:
			dir.baselineOp = baselineDiff
			dir.mode = plan.ModeExplain
		default:
			dir.baselineOp = baselineList
		}
	}

	return inner, dir, true
}

// runPlan executes a plan directive against the database and fills result.
func (c *Client) runPlan(
	ctx context.Context,
	stmt string,
	dir directive,
	result *QueryResult,
	args ...any,
) {
	if m := stripExplainRe.FindStringSubmatch(stmt); m != nil {
		stmt = strings.TrimSpace(m[1])
	}

	dialect := plan.DialectFromDriver(c.driver)

	if dir.baselineOp == baselineList {
		view, err := renderAllBaselines()
		if err != nil {
			result.Error = err
			return
		}

		result.QueryType = PlanQuery
		result.PlanView = view
		return
	}

	if dir.mode == plan.ModeAnalyze {
		decision := plan.Guard(dialect, stmt, true)
		if decision.Action == plan.ActionBlock {
			result.Error = fmt.Errorf(
				"EXPLAIN ANALYZE blocked by safety policy:\n  - %s",
				strings.Join(decision.Reasons, "\n  - "))
			return
		}
	}

	headers, grid, err := c.executePlanQuery(ctx, dialect, dir.mode, stmt, args...)
	if err != nil {
		result.Error = err
		return
	}

	p, err := plan.Parse(c.driver, dir.mode, headers, grid)
	if err != nil {
		result.Error = err
		return
	}

	p.Query = stmt

	c.fillPlanResult(result, p, dir)
}

// fillPlanResult applies the baseline action and renders the final view.
func (c *Client) fillPlanResult(result *QueryResult, p *plan.Plan, dir directive) {
	result.QueryType = PlanQuery

	switch dir.baselineOp {
	case baselineSave:
		b, err := plan.SaveBaseline(mustUserConfigDir(), p, dir.name)
		if err != nil {
			result.Error = err
			return
		}

		result.PlanView = plan.Render(p) +
			fmt.Sprintf("\nBaseline %q saved · fingerprint %s\n", b.Name, b.Fingerprint[:12])
	case baselineDiff:
		b, err := loadBaselineForPlan(p, dir.name)
		if err != nil {
			result.Error = err
			return
		}

		result.PlanView = plan.Diff(b.Plan, p).Render()
	default:
		result.PlanView = plan.Render(p)
	}
}

// loadBaselineForPlan loads the named or latest baseline of a plan query.
func loadBaselineForPlan(p *plan.Plan, name string) (*plan.Baseline, error) {
	baseDir := mustUserConfigDir()
	fingerprint := plan.Fingerprint(p.Dialect, p.Query)

	if name != "" {
		return plan.LoadBaseline(baseDir, fingerprint, name)
	}

	return plan.LatestBaseline(baseDir, fingerprint)
}

// executePlanQuery runs the dialect specific EXPLAIN wrapper and returns
// the raw result grid.
func (c *Client) executePlanQuery(
	ctx context.Context,
	dialect plan.Dialect,
	mode plan.Mode,
	stmt string,
	args ...any,
) ([]string, [][]string, error) {
	switch dialect {
	case plan.DialectPostgres:
		q := "EXPLAIN (FORMAT JSON) " + stmt
		if mode == plan.ModeAnalyze {
			q = "EXPLAIN (ANALYZE, COSTS, BUFFERS, FORMAT JSON) " + stmt
			return c.runAnalyzeIsolated(ctx, dialect, q, args...)
		}
		return queryToGrid(ctx, c.db, q, args...)
	case plan.DialectMySQL:
		if mode == plan.ModeAnalyze {
			timeoutMS := int64(plan.AnalyzeTimeout() / time.Millisecond)
			q := "EXPLAIN ANALYZE " + injectMySQLMaxExecutionTime(stmt, timeoutMS)
			return c.runAnalyzeIsolated(ctx, dialect, q, args...)
		}
		q := "EXPLAIN FORMAT=JSON " + stmt
		return queryToGrid(ctx, c.db, q, args...)
	case plan.DialectOracle:
		return c.runOracleExplain(ctx, stmt, args...)
	case plan.DialectSQLServer:
		return c.runSQLServerExplain(ctx, stmt, args...)
	case plan.DialectSQLite:
		q := "EXPLAIN QUERY PLAN " + stmt
		return queryToGrid(ctx, c.db, q, args...)
	default:
		return nil, nil, fmt.Errorf("unsupported dialect %q", dialect)
	}
}

// runAnalyzeIsolated executes EXPLAIN ANALYZE inside a rolled back
// transaction with a bounded timeout.
func (c *Client) runAnalyzeIsolated(
	ctx context.Context,
	dialect plan.Dialect,
	q string,
	args ...any,
) ([]string, [][]string, error) {
	timeout := plan.AnalyzeTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tx, err := c.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if dialect == plan.DialectPostgres {
		if _, err := tx.ExecContext(
			ctx,
			fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds()),
		); err != nil {
			return nil, nil, err
		}
	}

	headers, grid, err := queryToGrid(ctx, tx, q, args...)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, nil, fmt.Errorf(
				"EXPLAIN ANALYZE timed out after %s; the statement was cancelled and rolled back: %w",
				timeout, err)
		}
		return nil, nil, err
	}

	if err := tx.Rollback(); err != nil {
		return nil, nil, err
	}

	return headers, grid, nil
}

// runOracleExplain plans the statement into PLAN_TABLE and displays it.
func (c *Client) runOracleExplain(
	ctx context.Context, stmt string, args ...any,
) ([]string, [][]string, error) {
	conn, err := c.db.Connx(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(
		ctx,
		fmt.Sprintf("EXPLAIN PLAN SET STATEMENT_ID = 'dblab' FOR %s", stmt),
		args...,
	); err != nil {
		return nil, nil, err
	}

	q := "SELECT PLAN_TABLE_OUTPUT FROM TABLE(DBMS_XPLAN.DISPLAY(NULL, 'dblab', 'ALL'))"

	return queryToGrid(ctx, conn, q)
}

// runSQLServerExplain enables SHOWPLAN_XML on one pinned connection.
func (c *Client) runSQLServerExplain(
	ctx context.Context, stmt string, args ...any,
) ([]string, [][]string, error) {
	conn, err := c.db.Connx(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "SET SHOWPLAN_XML ON"); err != nil {
		return nil, nil, err
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "SET SHOWPLAN_XML OFF")
	}()

	return queryToGrid(ctx, conn, stmt, args...)
}

// injectMySQLMaxExecutionTime adds the MAX_EXECUTION_TIME hint in ms.
func injectMySQLMaxExecutionTime(stmt string, timeoutMS int64) string {
	upper := strings.ToUpper(strings.TrimLeft(stmt, " \t\r\n"))
	if !strings.HasPrefix(upper, "SELECT") {
		return stmt
	}

	return "SELECT /*+ MAX_EXECUTION_TIME(" + strconv.FormatInt(timeoutMS, 10) + ") */ " +
		strings.TrimSpace(stmt[len("SELECT"):])
}

// queryToGrid executes a query and returns headers and a string grid.
func queryToGrid(
	ctx context.Context,
	q sqlx.QueryerContext,
	query string,
	args ...any,
) ([]string, [][]string, error) {
	rows, err := q.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	headers, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}

	grid := make([][]string, 0)

	for rows.Next() {
		cols, err := rows.SliceScan()
		if err != nil {
			return nil, nil, err
		}

		cells := make([]string, len(cols))
		for i, v := range cols {
			cells[i] = cellString(v)
		}

		grid = append(grid, cells)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	return headers, grid, nil
}

// cellString renders any scanned value as a plain string.
func cellString(v any) string {
	switch val := v.(type) {
	case []byte:
		return string(val)
	case string:
		return val
	case nil:
		return "<nil>"
	default:
		return fmt.Sprintf("%v", val)
	}
}

// renderAllBaselines lists every stored baseline.
func renderAllBaselines() (string, error) {
	metas, err := plan.ListAllBaselines(mustUserConfigDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return plan.RenderBaselineList(nil), nil
		}
		return "", err
	}

	return plan.RenderBaselineList(metas), nil
}

// mustUserConfigDir returns the user config directory.
func mustUserConfigDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		// Fall back to the home directory rather than crashing the session.
		if home, herr := os.UserHomeDir(); herr == nil {
			return home
		}
		return "."
	}
	return dir
}
