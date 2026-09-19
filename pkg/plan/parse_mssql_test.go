package plan

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const ssXMLFixture = `<ShowPlanXML xmlns="http://schemas.microsoft.com/sqlserver/2004/07/showplan">
  <BatchSequence>
    <Batch>
      <Statements>
        <StmtSimple>
          <QueryPlan>
            <RelOp NodeId="0" PhysicalOp="Nested Loops" LogicalOp="Inner Join" EstimateRows="100" EstimateIO="0" EstimateCPU="0.001" AvgRowSize="25">
              <NestedLoops>
                <OuterReferences>
                  <ColumnReference Column="Bmk1000"/>
                </OuterReferences>
                <RelOp NodeId="1" PhysicalOp="Index Seek" LogicalOp="Index Seek" EstimateRows="10" EstimateIO="0.01" EstimateCPU="0.001" AvgRowSize="18">
                  <IndexScan Ordered="1">
                    <Object Database="sakila" Schema="dbo" Table="Actor" Index="idx_actor_last_name" IndexKind="NonClustered"/>
                    <SeekPredicates>
                      <Prefix>
                        <ColumnReference Database="sakila" Schema="dbo" Table="Actor" Column="last_name"/>
                      </Prefix>
                    </SeekPredicates>
                  </IndexScan>
                </RelOp>
                <RelOp NodeId="2" PhysicalOp="Table Scan" LogicalOp="Table Scan" EstimateRows="1000" EstimateIO="1.2" EstimateCPU="0.1" AvgRowSize="40">
                  <TableScan>
                    <Object Database="sakila" Schema="dbo" Table="Film"/>
                  </TableScan>
                </RelOp>
              </NestedLoops>
            </RelOp>
          </QueryPlan>
        </StmtSimple>
      </Statements>
    </Batch>
  </BatchSequence>
</ShowPlanXML>`

func TestParseSQLServerXML(t *testing.T) {
	t.Parallel()

	p, err := Parse("sqlserver", ModeExplain, nil, [][]string{{ssXMLFixture}})
	require.NoError(t, err)

	require.Equal(t, DialectSQLServer, p.Dialect)
	require.Equal(t, ModeExplain, p.Mode)
	require.Equal(t, OpNestedLoopJoin, p.Root.OpType)
	// Cost is IO + CPU.
	require.Equal(t, 0.001, p.Root.Metrics.TotalCost)
	require.Equal(t, 100.0, p.Root.Metrics.Rows)

	idxSeek := p.Root.Children[0]
	require.Equal(t, OpIndexScan, idxSeek.OpType)
	require.Equal(t, "dbo.Actor", idxSeek.Object)
	require.Equal(t, "idx_actor_last_name", idxSeek.Index)
	require.Contains(t, idxSeek.Predicate, "last_name")
	require.Equal(t, 0.011, idxSeek.Metrics.TotalCost)
	require.NotEmpty(t, idxSeek.RawPayload)

	tableScan := p.Root.Children[1]
	require.Equal(t, OpSeqScan, tableScan.OpType)
	require.Equal(t, "dbo.Film", tableScan.Object)
	require.Equal(t, 1.3, tableScan.Metrics.TotalCost)
}

const ssXMLActualFixture = `<ShowPlanXML>
  <BatchSequence>
    <Batch>
      <Statements>
        <StmtSimple>
          <QueryPlan>
            <RelOp NodeId="0" PhysicalOp="Table Scan" LogicalOp="Table Scan" EstimateRows="100" EstimateIO="1" EstimateCPU="0.1" AvgRowSize="20">
              <TableScan>
                <Object Schema="dbo" Table="Actor"/>
              </TableScan>
              <RunTimeInformation>
                <RunTimeCountersPerThread Thread="1" ActualRows="50" ActualExecutions="1" ActualElapsedms="3" ActualCPUms="1"/>
                <RunTimeCountersPerThread Thread="2" ActualRows="45" ActualExecutions="1" ActualElapsedms="3" ActualCPUms="1"/>
              </RunTimeInformation>
            </RelOp>
          </QueryPlan>
        </StmtSimple>
      </Statements>
    </Batch>
  </BatchSequence>
</ShowPlanXML>`

func TestParseSQLServerXMLActual(t *testing.T) {
	t.Parallel()

	p, err := Parse("sqlserver", ModeExplain, nil, [][]string{{ssXMLActualFixture}})
	require.NoError(t, err)

	require.Equal(t, ModeAnalyze, p.Mode)
	require.True(t, p.Root.Metrics.HasActual())
	// Totals summed across worker threads.
	require.Equal(t, 95.0, p.Root.Metrics.ActualRows)
	require.Equal(t, 2.0, p.Root.Metrics.ActualLoops)
	require.Equal(t, 6.0, p.Root.Metrics.ActualTotalMS)
}

const ssTextFixture = `  |--Nested Loops(Inner Join)
       |--Index Seek(OBJECT:([sakila].[dbo].[Actor].[idx_actor_last_name]), SEEK:([Actor].[last_name]=Const) ORDERED FORWARD)
       |--Table Scan(OBJECT:([sakila].[dbo].[Film]))`

func TestParseSQLServerText(t *testing.T) {
	t.Parallel()

	p, err := Parse("sqlserver", ModeExplain, nil, [][]string{{ssTextFixture}})
	require.NoError(t, err)

	require.Equal(t, OpNestedLoopJoin, p.Root.OpType)

	idxSeek := p.Root.Children[0]
	require.Equal(t, OpIndexScan, idxSeek.OpType)
	require.Equal(t, "dbo.Actor", idxSeek.Object)
	require.Equal(t, "idx_actor_last_name", idxSeek.Index)
	require.Contains(t, idxSeek.Predicate, "last_name")

	tableScan := p.Root.Children[1]
	require.Equal(t, OpSeqScan, tableScan.OpType)
	require.Equal(t, "dbo.Film", tableScan.Object)
}
