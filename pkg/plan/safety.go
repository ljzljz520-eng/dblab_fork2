package plan

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// DefaultAnalyzeTimeout bounds EXPLAIN ANALYZE execution by default.
const DefaultAnalyzeTimeout = 30 * time.Second

// Action is the policy decision for an EXPLAIN ANALYZE request.
type Action string

const (
	// ActionAllow means the statement never executes (planner-only EXPLAIN).
	ActionAllow Action = "ALLOW"
	// ActionIsolate means execution happens in a rolled back transaction,
	// with a bounded timeout.
	ActionIsolate Action = "ISOLATE_IN_TX"
	// ActionBlock means the statement must not be executed.
	ActionBlock Action = "BLOCK"
)

// Decision is the result of the EXPLAIN ANALYZE safety policy.
type Decision struct {
	// Action is the required action.
	Action Action
	// Reasons explains the decision, in order.
	Reasons []string
	// Timeout is the execution timeout applied under isolation.
	Timeout time.Duration
}

// blockReasons returns a blocking decision with the given reasons.
func blockDecision(reasons ...string) Decision {
	return Decision{
		Action:  ActionBlock,
		Reasons: reasons,
		Timeout: AnalyzeTimeout(),
	}
}

// writeVerbs are the leading keywords of statements that mutate state.
var writeVerbs = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true,
	"TRUNCATE": true, "CREATE": true, "ALTER": true, "DROP": true,
	"RENAME": true, "GRANT": true, "REVOKE": true, "CALL": true,
	"BEGIN": true, "START": true, "COMMIT": true, "ROLLBACK": true,
	"VACUUM": true, "REINDEX": true, "CLUSTER": true, "REFRESH": true,
}

var (
	safetyCommentRegex    = regexp.MustCompile(`(?s)/\*.*?\*/|--.*?\n`)
	pgSequenceCallRe      = regexp.MustCompile(`(?i)\b(?:nextval|setval)\s*\(`)
	pgDBLinkRe            = regexp.MustCompile(`(?i)\bdblink(?:_exec|_open|_send_query)?\s*\(`)
	oracleSequenceRe      = regexp.MustCompile(`(?i)\.(?:nextval|currval)\b`)
	oracleDBMSCallRe      = regexp.MustCompile(`(?i)\bdbms_[a-z_]+\.`)
	functionCallRe        = regexp.MustCompile(`(?i)\b[a-z_][a-z0-9_]*\s*\(`)
)

// AnalyzeSupported reports whether a dialect supports EXPLAIN ANALYZE.
//
// PostgreSQL and MySQL (8.0.18+) execute the statement and report actual
// metrics. Oracle, SQL Server and SQLite have no equivalent statement
// dblab can run, so requests for them are blocked with an explicit reason.
func AnalyzeSupported(d Dialect) bool {
	switch d {
	case DialectPostgres, DialectMySQL:
		return true
	default:
		return false
	}
}

// AnalyzeTimeout returns the configured EXPLAIN ANALYZE timeout.
//
// It defaults to DefaultAnalyzeTimeout and honors DBLAB_EXPLAIN_TIMEOUT,
// accepting any Go duration (e.g. 15s, 2m).
func AnalyzeTimeout() time.Duration {
	if v := os.Getenv("DBLAB_EXPLAIN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return DefaultAnalyzeTimeout
}

// Guard applies the EXPLAIN ANALYZE safety policy to a statement.
//
// Planner-only EXPLAIN is always allowed since the statement is never
// executed. EXPLAIN ANALYZE blocks write statements, dialect specific
// non-transactional side effects and unsupported dialects; read statements
// are isolated in a rolled back transaction under a bounded timeout.
func Guard(dialect Dialect, query string, analyze bool) Decision {
	query = unwrapExplain(query)
	verb := firstVerb(query)

	if !analyze {
		return Decision{Action: ActionAllow, Timeout: AnalyzeTimeout()}
	}

	if !AnalyzeSupported(dialect) {
		return blockDecision(fmt.Sprintf(
			"EXPLAIN ANALYZE is not supported by %s; use '| plan' for a planner-only plan",
			dialect))
	}

	if writeVerbs[verb] {
		return blockDecision(fmt.Sprintf(
			"%s is a write statement: EXPLAIN ANALYZE executes it and the effects "+
				"cannot be fully undone (e.g. triggers, sequences, autonomous transactions)",
			verb))
	}

	// Dialect specific, non-transactional side effects.
	switch dialect {
	case DialectPostgres:
		if pgSequenceCallRe.MatchString(query) {
			return blockDecision(
				"sequence functions nextval()/setval() advance sequences even if the transaction is rolled back")
		}
		if pgDBLinkRe.MatchString(query) {
			return blockDecision(
				"dblink() functions perform remote work that local rollback cannot undo")
		}
	case DialectOracle:
		if oracleSequenceRe.MatchString(query) {
			return blockDecision(
				"sequence .NEXTVAL/.CURRVAL effects are not rolled back")
		}
		if oracleDBMSCallRe.MatchString(query) {
			return blockDecision(
				"calls to DBMS_* packages may include autonomous transactions that cannot be rolled back")
		}
	}

	decision := Decision{
		Action:  ActionIsolate,
		Timeout: AnalyzeTimeout(),
		Reasons: []string{
			"statement executes inside a transaction that is always rolled back",
			"a bounded statement timeout cancels long executions",
		},
	}

	if functionCallRe.MatchString(query) {
		decision.Reasons = append(decision.Reasons,
			"function calls found; volatility cannot be proven from the SQL text, so rollback isolation is mandatory")
	}

	return decision
}

// WithTimeout derives a context bounded by the decision timeout.
func (d Decision) WithTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d.Timeout)
}

// unwrapExplain strips a leading EXPLAIN / EXPLAIN ANALYZE keyword.
func unwrapExplain(query string) string {
	cleaned := safetyCommentRegex.ReplaceAllString(query, " ")
	cleaned = strings.TrimSpace(cleaned)

	upper := strings.ToUpper(cleaned)
	if strings.HasPrefix(upper, "EXPLAIN ANALYZE") {
		return strings.TrimSpace(cleaned[len("EXPLAIN ANALYZE"):])
	}
	if strings.HasPrefix(upper, "EXPLAIN") {
		return strings.TrimSpace(cleaned[len("EXPLAIN"):])
	}

	return cleaned
}

// firstVerb returns the uppercased leading keyword of a statement.
func firstVerb(query string) string {
	cleaned := safetyCommentRegex.ReplaceAllString(query, " ")
	fields := strings.Fields(cleaned)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}
