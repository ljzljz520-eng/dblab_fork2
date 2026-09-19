// Package plan provides a unified, dialect-independent representation of
// database execution plans.
//
// Plans produced by PostgreSQL, MySQL, Oracle, SQL Server and SQLite are
// parsed into a single operator tree (Node), every node keeping the exact
// vendor payload it was built from (RawPayload). The package also offers
// stable plan comparison, plan baselines and the safety policy used to run
// EXPLAIN ANALYZE against the database.
package plan

import (
	"strings"
	"time"
)

// Dialect identifies the source dialect of a plan.
type Dialect string

const (
	// DialectPostgres is the PostgreSQL dialect.
	DialectPostgres Dialect = "postgresql"
	// DialectMySQL is the MySQL / MariaDB dialect.
	DialectMySQL Dialect = "mysql"
	// DialectOracle is the Oracle Database dialect.
	DialectOracle Dialect = "oracle"
	// DialectSQLServer is the Microsoft SQL Server dialect.
	DialectSQLServer Dialect = "sqlserver"
	// DialectSQLite is the SQLite dialect.
	DialectSQLite Dialect = "sqlite"
)

// Mode describes how a plan was obtained.
type Mode string

const (
	// ModeExplain means the plan came from a planner-only EXPLAIN.
	ModeExplain Mode = "EXPLAIN"
	// ModeAnalyze means the statement was executed and actual metrics exist.
	ModeAnalyze Mode = "EXPLAIN ANALYZE"
)

// OpType is the unified, dialect-independent operator category.
type OpType string

const (
	// OpUnknown is returned when no mapping exists.
	OpUnknown OpType = "UNKNOWN"
	// OpResult is a trivial result / SELECT statement node.
	OpResult OpType = "RESULT"
	// OpProject computes scalar expressions.
	OpProject OpType = "PROJECT"
	// OpFilter evaluates a filter expression.
	OpFilter OpType = "FILTER"
	// OpSeqScan is a full sequential / table scan.
	OpSeqScan OpType = "SEQ_SCAN"
	// OpIndexScan is an index range / lookup scan that visits the heap.
	OpIndexScan OpType = "INDEX_SCAN"
	// OpIndexOnlyScan is an index-only / covering index scan.
	OpIndexOnlyScan OpType = "INDEX_ONLY_SCAN"
	// OpBitmapIndexScan builds a bitmap from an index.
	OpBitmapIndexScan OpType = "BITMAP_INDEX_SCAN"
	// OpBitmapHeapScan turns a bitmap into heap visits.
	OpBitmapHeapScan OpType = "BITMAP_HEAP_SCAN"
	// OpBitmapCombine combines bitmaps (AND / OR / MERGE).
	OpBitmapCombine OpType = "BITMAP_COMBINE"
	// OpTIDScan is an internal tuple identifier lookup.
	OpTIDScan OpType = "TID_SCAN"
	// OpFunctionScan scans the output of a function.
	OpFunctionScan OpType = "FUNCTION_SCAN"
	// OpValuesScan scans an inlined VALUES list.
	OpValuesScan OpType = "VALUES_SCAN"
	// OpOtherScan is an uncategorized scan (foreign, custom scan, ...).
	OpOtherScan OpType = "OTHER_SCAN"
	// OpWorkTableScan scans the working table of a recursive query.
	OpWorkTableScan OpType = "WORKTABLE_SCAN"
	// OpCTEScan scans a Common Table Expression.
	OpCTEScan OpType = "CTE_SCAN"
	// OpSubquery scans the result of an explicit subquery.
	OpSubquery OpType = "SUBQUERY"
	// OpView scans a stored or inline view.
	OpView OpType = "VIEW"
	// OpTableAccessRowID fetches rows by rowid after an index lookup.
	OpTableAccessRowID OpType = "TABLE_ACCESS_ROWID"
	// OpNestedLoopJoin is a nested loops join.
	OpNestedLoopJoin OpType = "NESTED_LOOP_JOIN"
	// OpHashJoin is a hash join.
	OpHashJoin OpType = "HASH_JOIN"
	// OpMergeJoin is a merge / sort-merge join.
	OpMergeJoin OpType = "MERGE_JOIN"
	// OpHashBuild is the auxiliary hash-build child of a hash join.
	OpHashBuild OpType = "HASH_BUILD"
	// OpSort is an explicit sort node.
	OpSort OpType = "SORT"
	// OpAggregate is a scalar aggregate without grouping.
	OpAggregate OpType = "AGGREGATE"
	// OpGroupAggregate is a group-based (sorted) aggregate.
	OpGroupAggregate OpType = "GROUP_AGGREGATE"
	// OpHashAggregate is a hashed aggregate.
	OpHashAggregate OpType = "HASH_AGGREGATE"
	// OpWindow is a window function computation.
	OpWindow OpType = "WINDOW"
	// OpUnique removes duplicates.
	OpUnique OpType = "UNIQUE"
	// OpSetOp is a set operation (INTERSECT / EXCEPT / UNION).
	OpSetOp OpType = "SET_OP"
	// OpRecursiveUnion is a recursive union node.
	OpRecursiveUnion OpType = "RECURSIVE_UNION"
	// OpLimit enforces a row count / stopkey limit.
	OpLimit OpType = "LIMIT"
	// OpAppend concatenates its children.
	OpAppend OpType = "APPEND"
	// OpMaterialize materializes its input.
	OpMaterialize OpType = "MATERIALIZE"
	// OpMemoize caches its inner result.
	OpMemoize OpType = "MEMOIZE"
	// OpGather gathers parallel workers.
	OpGather OpType = "GATHER"
	// OpGatherMerge gathers and merges parallel workers.
	OpGatherMerge OpType = "GATHER_MERGE"
	// OpParallel is a parallel exchange (PX / repartition streams).
	OpParallel OpType = "PARALLEL"
	// OpPartition iterates over table partitions.
	OpPartition OpType = "PARTITION"
	// OpModifyTable is a write operation on a table.
	OpModifyTable OpType = "MODIFY_TABLE"
	// OpLockRows explicitly locks rows.
	OpLockRows OpType = "LOCK_ROWS"
)

// Metrics holds the cost and cardinality information of an operator.
//
// Zero values mean the metric is not provided by the dialect, except for
// ActualValid, which reports whether the Actual* fields are populated.
type Metrics struct {
	// StartupCost is the cost to produce the first row.
	StartupCost float64
	// TotalCost is the total estimated cost of the node.
	TotalCost float64
	// Rows is the estimated number of output rows (cardinality estimate).
	Rows float64
	// Width is the estimated average row width in bytes.
	Width float64
	// ActualRows is the total number of rows actually produced, across loops.
	ActualRows float64
	// ActualLoops is the number of times the node was executed.
	ActualLoops float64
	// ActualStartupMS is the actual startup time in milliseconds.
	ActualStartupMS float64
	// ActualTotalMS is the actual total execution time in milliseconds.
	ActualTotalMS float64
	// ActualValid reports whether actual execution metrics are available.
	ActualValid bool
	// SharedHitBlocks is the number of shared blocks found in the cache.
	SharedHitBlocks float64
	// SharedReadBlocks is the number of shared blocks read from disk.
	SharedReadBlocks float64
}

// HasActual reports whether actual execution metrics are available.
func (m Metrics) HasActual() bool {
	return m.ActualValid
}

// BiasRatio returns the cardinality estimate bias, i.e. the ratio between
// the estimated rows and the actual rows.
//
// A value greater than 1 means an over-estimate, a value smaller than 1 an
// under-estimate. The boolean is false when the ratio cannot be computed.
func (m Metrics) BiasRatio() (float64, bool) {
	if !m.ActualValid || m.Rows <= 0 || m.ActualRows <= 0 {
		return 0, false
	}
	return m.Rows / m.ActualRows, true
}

// Node is a single operator in the unified plan tree.
type Node struct {
	// ID is the stable, path-based identifier within the plan (e.g. 0.1.2).
	ID string
	// OpType is the unified operator category.
	OpType OpType
	// RawName is the exact vendor provided node label.
	RawName string
	// Object is the accessed table, view or relation.
	Object string
	// Alias is the relation alias, when present.
	Alias string
	// Index is the name of the selected index, when the node uses one.
	Index string
	// Predicate is the access / filter predicate in a best-effort text form.
	Predicate string
	// Metrics holds the cost and cardinality data.
	Metrics Metrics
	// Extra holds vendor specific evidence (spill signals, join type, ...).
	Extra map[string]string
	// RawPayload is the exact raw snippet this node was built from.
	RawPayload string
	// Children are the child operators, in vendor order.
	Children []*Node
}

// Plan is the unified representation of an execution plan.
type Plan struct {
	// Dialect is the source dialect.
	Dialect Dialect
	// Mode is the plan acquisition mode.
	Mode Mode
	// Query is the statement the plan refers to.
	Query string
	// Root is the root of the unified operator tree.
	Root *Node
	// Warnings carries notes and diagnostics from the vendor output.
	Warnings []string
	// RawPayload is the complete, untouched vendor output.
	RawPayload string
	// CapturedAt is when the plan was obtained.
	CapturedAt time.Time
}

// Flatten returns every node of the plan in pre-order traversal.
func (p *Plan) Flatten() []*Node {
	if p == nil || p.Root == nil {
		return nil
	}

	out := make([]*Node, 0)
	var walk func(n *Node)
	walk = func(n *Node) {
		out = append(out, n)
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(p.Root)

	return out
}

// NodeByID returns the node with the given path-based ID, if it exists.
func (p *Plan) NodeByID(id string) (*Node, bool) {
	for _, n := range p.Flatten() {
		if n.ID == id {
			return n, true
		}
	}
	return nil, false
}

// canonicalWhitespace folds all runs of whitespace into single spaces.
func canonicalWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
