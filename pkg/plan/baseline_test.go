package plan

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testPlan returns a minimal plan for baseline tests.
func testPlan(dialect Dialect, query string) *Plan {
	root := &Node{
		OpType:  OpSeqScan,
		RawName: "Seq Scan",
		Object:  "actor",
		Extra:   map[string]string{},
		Metrics: Metrics{TotalCost: 4.5, Rows: 200},
	}
	assignIDs(root, "0")

	return &Plan{
		Dialect:    dialect,
		Mode:       ModeExplain,
		Query:      query,
		Root:       root,
		CapturedAt: time.Now(),
	}
}

func TestFingerprintStable(t *testing.T) {
	t.Parallel()

	// Formatting differences of the same statement share the fingerprint.
	fp1 := Fingerprint(DialectPostgres, "SELECT * FROM actor WHERE id = 1")
	fp2 := Fingerprint(DialectPostgres, "select  *   from  actor where id = 1")

	require.Equal(t, fp1, fp2)

	// A different statement produces a different fingerprint.
	fp3 := Fingerprint(DialectPostgres, "SELECT * FROM film")
	require.NotEqual(t, fp1, fp3)

	// The dialect is part of the fingerprint.
	fp4 := Fingerprint(DialectMySQL, "SELECT * FROM actor WHERE id = 1")
	require.NotEqual(t, fp1, fp4)
}

func TestBaselineSaveLoad(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	query := "SELECT * FROM actor"
	p := testPlan(DialectPostgres, query)

	b, err := SaveBaseline(baseDir, p, "prod")
	require.NoError(t, err)
	require.Equal(t, "prod", b.Name)
	require.Equal(t, Fingerprint(DialectPostgres, query), b.Fingerprint)
	require.Equal(t, DialectPostgres, b.Dialect)
	require.False(t, b.CreatedAt.IsZero())

	loaded, err := LoadBaseline(baseDir, b.Fingerprint, "prod")
	require.NoError(t, err)
	require.Equal(t, "prod", loaded.Name)
	require.Equal(t, OpSeqScan, loaded.Plan.Root.OpType)
	require.Equal(t, "actor", loaded.Plan.Root.Object)
}

func TestBaselineDefaultName(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	p := testPlan(DialectPostgres, "SELECT 1")

	b, err := SaveBaseline(baseDir, p, "")
	require.NoError(t, err)
	require.NotEmpty(t, b.Name)
}

func TestBaselineLatest(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	p := testPlan(DialectPostgres, "SELECT * FROM actor")

	_, err := SaveBaseline(baseDir, p, "first")
	require.NoError(t, err)

	// Timestamps of mod time may tie; save the second baseline under a name
	// that sorts later than "first".
	time.Sleep(10 * time.Millisecond)

	_, err = SaveBaseline(baseDir, p, "second")
	require.NoError(t, err)

	// Latest is selected by creation time in the metadata listing.
	latest, err := LatestBaseline(baseDir, Fingerprint(DialectPostgres, p.Query))
	require.NoError(t, err)
	require.Equal(t, "second", latest.Name)
}

func TestBaselineList(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	p := testPlan(DialectPostgres, "SELECT * FROM actor")

	_, err := SaveBaseline(baseDir, p, "one")
	require.NoError(t, err)
	_, err = SaveBaseline(baseDir, p, "two")
	require.NoError(t, err)

	metas, err := ListBaselines(baseDir, Fingerprint(DialectPostgres, p.Query))
	require.NoError(t, err)
	require.Len(t, metas, 2)

	all, err := ListAllBaselines(baseDir)
	require.NoError(t, err)
	require.Len(t, all, 2)

	view := RenderBaselineList(metas)
	require.Contains(t, view, "Plan Baselines")
	require.Contains(t, view, "one")
}

func TestBaselineEmptyListing(t *testing.T) {
	t.Parallel()

	view := RenderBaselineList(nil)
	require.Contains(t, view, "No baselines saved yet")
}
