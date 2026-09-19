package plan

import "strings"

// pgExactOps maps the exact "Node Type" values of PostgreSQL EXPLAIN JSON.
//
// PostgreSQL node types are a closed vocabulary, so matching stays exact:
// this prevents short auxiliary labels such as "Hash" from being captured
// by a loose substring / prefix rule and reported as a hash join.
var pgExactOps = map[string]OpType{
	"Result":               OpResult,
	"ProjectSet":           OpProject,
	"ModifyTable":          OpModifyTable,
	"Append":               OpAppend,
	"Merge Append":         OpAppend,
	"Recursive Union":      OpRecursiveUnion,
	"Bitmap And":           OpBitmapCombine,
	"Bitmap Or":            OpBitmapCombine,
	"Seq Scan":             OpSeqScan,
	"Sample Scan":          OpSeqScan,
	"Gather":               OpGather,
	"Gather Merge":         OpGatherMerge,
	"Index Scan":           OpIndexScan,
	"Index Only Scan":      OpIndexOnlyScan,
	"Bitmap Index Scan":    OpBitmapIndexScan,
	"Bitmap Heap Scan":     OpBitmapHeapScan,
	"Tid Scan":             OpTIDScan,
	"Tid Range Scan":       OpTIDScan,
	"Subquery Scan":        OpSubquery,
	"Function Scan":        OpFunctionScan,
	"Table Function Scan": OpFunctionScan,
	"Values Scan":          OpValuesScan,
	"CTE Scan":             OpCTEScan,
	"Named Tuplestore Scan": OpOtherScan,
	"WorkTable Scan":       OpWorkTableScan,
	"Foreign Scan":         OpOtherScan,
	"Custom Scan":          OpOtherScan,
	"Nested Loop":          OpNestedLoopJoin,
	"Merge Join":           OpMergeJoin,
	"Hash Join":            OpHashJoin,
	"Materialize":          OpMaterialize,
	"Memoize":              OpMemoize,
	"Sort":                 OpSort,
	"Incremental Sort":     OpSort,
	"Limit":                OpLimit,
	"Hash":                 OpHashBuild,
	"HashAggregate":        OpHashAggregate,
	"Group Aggregate":      OpGroupAggregate,
	"Aggregate":            OpAggregate,
	"WindowAgg":            OpWindow,
	"Unique":               OpUnique,
	"SetOp":                OpSetOp,
	"LockRows":             OpLockRows,
}

// mysqlAccessTypes maps the traditional EXPLAIN / FORMAT=JSON access_type.
var mysqlAccessTypes = map[string]OpType{
	"ALL":              OpSeqScan,
	"index":            OpIndexScan,
	"range":            OpIndexScan,
	"ref":              OpIndexScan,
	"eq_ref":           OpIndexScan,
	"const":            OpIndexScan,
	"system":           OpSeqScan,
	"ref_or_null":      OpIndexScan,
	"unique_subquery":  OpIndexScan,
	"index_subquery":   OpIndexScan,
	"index_merge":      OpIndexScan,
	"fulltext":         OpIndexScan,
}

// oracleExactOps maps the exact operation labels of DBMS_XPLAN output.
var oracleExactOps = map[string]OpType{
	"SELECT STATEMENT":                    OpResult,
	"FAST DUAL":                           OpResult,
	"TABLE ACCESS FULL":                   OpSeqScan,
	"TABLE ACCESS STORAGE FULL":           OpSeqScan,
	"TABLE ACCESS BY INDEX ROWID":         OpTableAccessRowID,
	"TABLE ACCESS BY LOCAL INDEX ROWID":   OpTableAccessRowID,
	"TABLE ACCESS BY GLOBAL INDEX ROWID":  OpTableAccessRowID,
	"TABLE ACCESS STORAGE BY INDEX ROWID": OpTableAccessRowID,
	"TABLE ACCESS BY USER ROWID":          OpTIDScan,
	"TABLE ACCESS BY ROWID RANGE":         OpTIDScan,
	"INDEX UNIQUE SCAN":                   OpIndexScan,
	"INDEX RANGE SCAN":                    OpIndexScan,
	"INDEX RANGE SCAN (MIN/MAX)":          OpIndexScan,
	"INDEX RANGE SCAN DESCENDING":         OpIndexScan,
	"INDEX SKIP SCAN":                     OpIndexScan,
	"INDEX FULL SCAN":                     OpIndexScan,
	"INDEX FULL SCAN DESCENDING":          OpIndexScan,
	"INDEX FAST FULL SCAN":                OpIndexScan,
	"INDEX SAMPLE FAST FULL SCAN":         OpIndexScan,
	"INDEX SAMPLE RANGE SCAN":             OpIndexScan,
	"INDEX BUILD NON UNIQUE":              OpIndexScan,
	"INDEX BUILD UNIQUE":                  OpIndexScan,
	"NESTED LOOPS":                        OpNestedLoopJoin,
	"NESTED LOOPS OUTER":                  OpNestedLoopJoin,
	"NESTED LOOPS ANTI":                   OpNestedLoopJoin,
	"NESTED LOOPS SEMI":                   OpNestedLoopJoin,
	"HASH JOIN":                           OpHashJoin,
	"HASH JOIN OUTER":                     OpHashJoin,
	"HASH JOIN RIGHT OUTER":               OpHashJoin,
	"HASH JOIN FULL OUTER":                OpHashJoin,
	"HASH JOIN ANTI":                      OpHashJoin,
	"HASH JOIN SEMI":                      OpHashJoin,
	"MERGE JOIN":                          OpMergeJoin,
	"MERGE JOIN OUTER":                    OpMergeJoin,
	"MERGE JOIN ANTI":                    OpMergeJoin,
	"MERGE JOIN SEMI":                    OpMergeJoin,
	"MERGE JOIN CARTESIAN":                OpMergeJoin,
	"SORT ORDER BY":                       OpSort,
	"SORT UNIQUE":                         OpSort,
	"SORT GROUP BY":                       OpSort,
	"SORT JOIN":                           OpSort,
	"SORT AGGREGATE":                      OpAggregate,
	"BUFFER SORT":                         OpSort,
	"HASH GROUP BY":                       OpHashAggregate,
	"VIEW":                                OpView,
	"UNION-ALL":                           OpAppend,
	"UNION":                               OpSetOp,
	"INTERSECTION":                        OpSetOp,
	"MINUS":                               OpSetOp,
	"CONCATENATION":                       OpAppend,
	"FILTER":                              OpFilter,
	"COUNT":                               OpAggregate,
	"COUNT STOPKEY":                       OpLimit,
	"COLLECTOR ITERATOR":                  OpMaterialize,
	"BITMAP CONVERSION TO ROWIDS":         OpBitmapHeapScan,
	"BITMAP CONVERSION FROM ROWIDS":       OpBitmapIndexScan,
	"BITMAP CONVERSION COUNT":             OpBitmapCombine,
	"BITMAP INDEX SINGLE VALUE":           OpBitmapIndexScan,
	"BITMAP INDEX RANGE SCAN":             OpBitmapIndexScan,
	"BITMAP INDEX FAST FULL SCAN":         OpBitmapIndexScan,
	"BITMAP INDEX FULL SCAN":              OpBitmapIndexScan,
	"BITMAP MERGE":                        OpBitmapCombine,
	"BITMAP KEY ITERATION":                OpBitmapCombine,
	"BITMAP OR":                           OpBitmapCombine,
	"BITMAP AND":                          OpBitmapCombine,
	"BITMAP MINUS":                        OpBitmapCombine,
	"CONNECT BY":                          OpRecursiveUnion,
	"CONNECT BY NO FILTERING WITH STOPCONDITION": OpRecursiveUnion,
	"WINDOW":                              OpWindow,
	"WINDOW BUFFER":                       OpWindow,
	"WINDOW SORT":                         OpWindow,
	"WINDOW NOSORT":                       OpWindow,
	"SQL MODEL":                           OpProject,
	"PX COORDINATOR":                      OpGather,
	"PX SEND QC (RANDOM)":                 OpParallel,
	"PX SEND QC (ORDER)":                  OpParallel,
	"PX SEND QC (TUPLE)":                  OpParallel,
	"PX BLOCK ITERATOR":                   OpParallel,
	"PX PARTITION RANGE ALL":              OpParallel,
	"PARTITION RANGE ALL":                 OpPartition,
	"PARTITION RANGE SINGLE":              OpPartition,
	"PARTITION RANGE ITERATOR":            OpPartition,
	"PARTITION RANGE INLIST":              OpPartition,
	"PARTITION HASH ALL":                  OpPartition,
	"PARTITION HASH SINGLE":               OpPartition,
	"PARTITION HASH ITERATOR":             OpPartition,
	"PARTITION LIST ALL":                  OpPartition,
	"PARTITION LIST SINGLE":               OpPartition,
	"PARTITION LIST ITERATOR":             OpPartition,
	"PARTITION LIST INLIST":               OpPartition,
	"LOAD TABLE CONVENTIONAL":             OpModifyTable,
	"INSERT":                              OpModifyTable,
	"UPDATE":                              OpModifyTable,
	"DELETE":                              OpModifyTable,
	"MERGE":                               OpModifyTable,
	"FOR UPDATE":                          OpLockRows,
	"ENQUEUE":                             OpLockRows,
}

// sqlServerExactOps maps the exact PhysicalOp attributes / text showplan ops.
var sqlServerExactOps = map[string]OpType{
	"Table Scan":          OpSeqScan,
	"Clustered Index Scan": OpSeqScan,
	"Clustered Index Seek": OpIndexScan,
	"Index Scan":          OpIndexScan,
	"Index Seek":          OpIndexScan,
	"Nested Loops":        OpNestedLoopJoin,
	"Hash Match":          OpHashJoin,
	"Merge Join":          OpMergeJoin,
	"Sort":                OpSort,
	"Stream Aggregate":    OpGroupAggregate,
	"Compute Scalar":      OpProject,
	"Filter":              OpFilter,
	"Concat":              OpAppend,
	"Sequence":            OpAppend,
	"Spool":               OpMaterialize,
	"Lazy Spool":          OpMaterialize,
	"Eager Spool":         OpMaterialize,
	"Gather Streams":      OpGather,
	"Repartition Streams": OpParallel,
	"Distribute Streams":  OpParallel,
	"Parallelism":         OpParallel,
	"Top":                 OpLimit,
	"Assert":              OpFilter,
	"Constant Scan":       OpValuesScan,
	"Parameter Table Scan": OpOtherScan,
	"Index Insert":        OpModifyTable,
	"Index Update":        OpModifyTable,
	"Index Delete":        OpModifyTable,
	"Clustered Index Insert": OpModifyTable,
	"Clustered Index Update": OpModifyTable,
	"Clustered Index Delete": OpModifyTable,
	"Table Insert":        OpModifyTable,
	"Table Update":        OpModifyTable,
	"Table Delete":        OpModifyTable,
	"Segment":             OpOtherScan,
}

// Normalize maps a vendor operator label to the unified OpType.
//
// The strategy is dialect specific: PostgreSQL and SQL Server use exact
// lookups on closed vocabularies, MySQL uses exact access types plus token
// matching for iterator output, and Oracle uses exact lookups with
// distinctive prefixes as a fallback.
func Normalize(dialect Dialect, raw string) OpType {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return OpUnknown
	}

	switch dialect {
	case DialectPostgres:
		if op, ok := pgExactOps[raw]; ok {
			return op
		}
		return OpUnknown
	case DialectMySQL:
		return normalizeMySQL(raw)
	case DialectOracle:
		return normalizeOracle(raw)
	case DialectSQLServer:
		return normalizeSQLServer(raw)
	case DialectSQLite:
		return normalizeSQLite(raw)
	default:
		return OpUnknown
	}
}

// normalizeMySQL handles access types and the labels of EXPLAIN ANALYZE.
func normalizeMySQL(raw string) OpType {
	if op, ok := mysqlAccessTypes[raw]; ok {
		return op
	}

	label := strings.ToLower(raw)

	// Exact iterator names first; order matters because of prefix overlap.
	switch {
	case strings.HasPrefix(label, "covering index range scan"),
		strings.HasPrefix(label, "covering index scan"),
		strings.HasPrefix(label, "covering index lookup"),
		strings.HasPrefix(label, "covering index skip scan"):
		return OpIndexOnlyScan
	case strings.HasPrefix(label, "index range scan"),
		strings.HasPrefix(label, "index scan"),
		strings.HasPrefix(label, "index lookup"),
		strings.HasPrefix(label, "index seek"),
		strings.HasPrefix(label, "index skip scan"),
		strings.HasPrefix(label, "index dive"),
		strings.HasPrefix(label, "index full text"):
		return OpIndexScan
	case strings.HasPrefix(label, "table scan"),
		strings.HasPrefix(label, "batched key access"),
		strings.HasPrefix(label, "full scan"):
		return OpSeqScan
	case strings.HasPrefix(label, "hash join"):
		return OpHashJoin
	case strings.HasPrefix(label, "nested loop"):
		return OpNestedLoopJoin
	case strings.HasPrefix(label, "merge join"):
		return OpMergeJoin
	case strings.HasPrefix(label, "sort"):
		return OpSort
	case strings.HasPrefix(label, "group aggregate"),
		strings.HasPrefix(label, "stream aggregate"):
		return OpGroupAggregate
	case strings.HasPrefix(label, "aggregate"):
		return OpHashAggregate
	case strings.HasPrefix(label, "filter"):
		return OpFilter
	case strings.HasPrefix(label, "distinct"):
		return OpUnique
	case strings.HasPrefix(label, "limit"):
		return OpLimit
	case strings.HasPrefix(label, "union"),
		strings.HasPrefix(label, "materialize union"):
		return OpAppend
	case strings.HasPrefix(label, "materialize"):
		return OpMaterialize
	case strings.HasPrefix(label, "cte"):
		return OpCTEScan
	default:
		return OpUnknown
	}
}

// normalizeOracle uses exact labels and distinctive vendor prefixes.
func normalizeOracle(raw string) OpType {
	// Drop parenthesized suffixes, e.g. "HASH JOIN (BUFFERED)".
	base := raw
	if i := strings.IndexByte(base, '('); i >= 0 {
		base = strings.TrimSpace(base[:i])
	}

	if op, ok := oracleExactOps[base]; ok {
		return op
	}
	if op, ok := oracleExactOps[strings.ToUpper(base)]; ok {
		return op
	}

	up := strings.ToUpper(base)
	switch {
	case strings.HasPrefix(up, "TABLE ACCESS"):
		if strings.Contains(up, "FULL") {
			return OpSeqScan
		}
		return OpTableAccessRowID
	case strings.HasPrefix(up, "INDEX"):
		return OpIndexScan
	case strings.HasPrefix(up, "HASH JOIN"):
		return OpHashJoin
	case strings.HasPrefix(up, "NESTED LOOPS"):
		return OpNestedLoopJoin
	case strings.HasPrefix(up, "MERGE JOIN"):
		return OpMergeJoin
	case strings.HasPrefix(up, "SORT"):
		return OpSort
	case strings.HasPrefix(up, "BITMAP"):
		return OpBitmapCombine
	case strings.HasPrefix(up, "PX"):
		return OpParallel
	case strings.HasPrefix(up, "PARTITION"):
		return OpPartition
	case strings.HasPrefix(up, "WINDOW"):
		return OpWindow
	default:
		return OpUnknown
	}
}

// normalizeSQLServer uses exact physical ops and bracket-free text labels.
func normalizeSQLServer(raw string) OpType {
	head := raw
	if i := strings.IndexByte(head, '('); i >= 0 {
		head = strings.TrimSpace(head[:i])
	}

	if op, ok := sqlServerExactOps[head]; ok {
		return op
	}

	// "Hash Match(Aggregate, ...)" is an aggregate, not a join.
	if head == "Hash Match" {
		inside := raw
		if i := strings.IndexByte(inside, '('); i >= 0 {
			inside = inside[i:]
		}
		if strings.Contains(inside, "Aggregate") {
			return OpHashAggregate
		}
		return OpHashJoin
	}

	return OpUnknown
}

// normalizeSQLite maps EXPLAIN QUERY PLAN detail prefixes.
func normalizeSQLite(raw string) OpType {
	label := strings.ToLower(raw)

	switch {
	case strings.HasPrefix(label, "search"):
		if strings.Contains(label, "covering index") {
			return OpIndexOnlyScan
		}
		return OpIndexScan
	case strings.HasPrefix(label, "scan"):
		if strings.Contains(label, "covering index") {
			return OpIndexOnlyScan
		}
		return OpSeqScan
	case strings.HasPrefix(label, "use temp b-tree for order by"):
		return OpSort
	case strings.HasPrefix(label, "use temp b-tree for group by"):
		return OpGroupAggregate
	case strings.HasPrefix(label, "use temp b-tree for distinct"):
		return OpUnique
	case strings.HasPrefix(label, "list subquery"):
		return OpSubquery
	case strings.HasPrefix(label, "correlated scalar subquery"),
		strings.HasPrefix(label, "scalar subquery"):
		return OpSubquery
	case strings.HasPrefix(label, "materialize"):
		return OpMaterialize
	case strings.HasPrefix(label, "union"):
		return OpAppend
	default:
		return OpUnknown
	}
}
