package plan

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var baselineNameSanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// Baseline is a saved plan used as a reference for later comparisons.
type Baseline struct {
	// Name is the user provided or generated baseline name.
	Name string `json:"name"`
	// Fingerprint identifies the query the baseline belongs to.
	Fingerprint string `json:"fingerprint"`
	// Dialect is the source dialect.
	Dialect Dialect `json:"dialect"`
	// Query is the statement the baseline refers to.
	Query string `json:"query"`
	// Plan is the saved unified plan.
	Plan *Plan `json:"plan"`
	// CreatedAt is when the baseline was saved.
	CreatedAt time.Time `json:"created_at"`
}

// BaselineMeta is the summary of a baseline used in listings.
type BaselineMeta struct {
	// Name is the baseline name.
	Name string
	// Fingerprint identifies the query.
	Fingerprint string
	// Dialect is the source dialect.
	Dialect Dialect
	// CreatedAt is when the baseline was saved.
	CreatedAt time.Time
}

// Fingerprint returns a stable identifier for a statement within a dialect.
//
// The statement is whitespace-normalized and lowercased, so formatting
// differences do not split the baseline history of the same query.
func Fingerprint(dialect Dialect, query string) string {
	normalized := NormalizeSQL(query)
	sum := sha1.Sum([]byte(string(dialect) + "\n" + normalized))
	return hex.EncodeToString(sum[:])
}

// NormalizeSQL lowercases and whitespace-normalizes a statement.
func NormalizeSQL(query string) string {
	return strings.ToLower(canonicalWhitespace(strings.TrimSpace(query)))
}

// SaveBaseline stores the plan as a named baseline.
//
// An empty name becomes a timestamp based name. It returns the saved
// baseline.
func SaveBaseline(baseDir string, p *Plan, name string) (*Baseline, error) {
	if p == nil || p.Root == nil {
		return nil, fmt.Errorf("cannot save an empty plan as baseline")
	}

	if name = strings.TrimSpace(name); name == "" {
		name = time.Now().Format("20060102-150405")
	}

	fingerprint := Fingerprint(p.Dialect, p.Query)

	b := &Baseline{
		Name:        name,
		Fingerprint: fingerprint,
		Dialect:     p.Dialect,
		Query:       p.Query,
		Plan:        p,
		CreatedAt:   time.Now(),
	}

	dir := baselineDir(baseDir, fingerprint)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating baseline directory: %w", err)
	}

	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding baseline: %w", err)
	}

	file := filepath.Join(dir, sanitizeBaselineName(name)+".json")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return nil, fmt.Errorf("writing baseline file: %w", err)
	}

	return b, nil
}

// LoadBaseline loads the named baseline of a query fingerprint.
func LoadBaseline(baseDir, fingerprint, name string) (*Baseline, error) {
	file := filepath.Join(
		baselineDir(baseDir, fingerprint),
		sanitizeBaselineName(name)+".json",
	)

	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	return decodeBaseline(data)
}

// LatestBaseline returns the most recently saved baseline for a fingerprint.
func LatestBaseline(baseDir, fingerprint string) (*Baseline, error) {
	metas, err := ListBaselines(baseDir, fingerprint)
	if err != nil {
		return nil, err
	}

	if len(metas) == 0 {
		return nil, fmt.Errorf(
			"no baseline found for this query; save one with '| baseline save [name]'")
	}

	return LoadBaseline(baseDir, fingerprint, metas[0].Name)
}

// ListBaselines lists the baselines of a fingerprint, newest first.
func ListBaselines(baseDir, fingerprint string) ([]BaselineMeta, error) {
	return listBaselineDir(filepath.Join(baseDir, "dblab", "plan_baselines", fingerprint))
}

// ListAllBaselines lists every saved baseline across all queries.
func ListAllBaselines(baseDir string) ([]BaselineMeta, error) {
	root := filepath.Join(baseDir, "dblab", "plan_baselines")

	fingerprints, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var metas []BaselineMeta

	for _, fp := range fingerprints {
		if !fp.IsDir() {
			continue
		}

		rows, err := listBaselineDir(filepath.Join(root, fp.Name()))
		if err != nil {
			continue
		}

		metas = append(metas, rows...)
	}

	sortMetasNewestFirst(metas)

	return metas, nil
}

// RenderBaselineList renders baseline metadata as a stable text listing.
func RenderBaselineList(metas []BaselineMeta) string {
	var b strings.Builder

	b.WriteString("Plan Baselines\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteByte('\n')

	if len(metas) == 0 {
		b.WriteString("No baselines saved yet. Use '| baseline save [name]'.\n")
		return b.String()
	}

	for _, m := range metas {
		fmt.Fprintf(&b, "• %s · %s · %s · %s\n",
			m.Name, m.Dialect, m.CreatedAt.Format(time.RFC1123), m.Fingerprint[:12])
	}

	return b.String()
}

// baselineDir returns the directory holding the baselines of a fingerprint.
func baselineDir(baseDir, fingerprint string) string {
	return filepath.Join(baseDir, "dblab", "plan_baselines", fingerprint)
}

// sanitizeBaselineName replaces unsafe characters in a baseline name.
func sanitizeBaselineName(name string) string {
	return baselineNameSanitizeRe.ReplaceAllString(name, "_")
}

// decodeBaseline decodes a JSON encoded baseline.
func decodeBaseline(data []byte) (*Baseline, error) {
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("decoding baseline: %w", err)
	}
	return &b, nil
}

// listBaselineDir reads baseline files in one fingerprint directory.
func listBaselineDir(dir string) ([]BaselineMeta, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	metas := make([]BaselineMeta, 0, len(files))

	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}

		b, err := decodeBaseline(data)
		if err != nil {
			continue
		}

		metas = append(metas, BaselineMeta{
			Name:        b.Name,
			Fingerprint: b.Fingerprint,
			Dialect:     b.Dialect,
			CreatedAt:   b.CreatedAt,
		})
	}

	sortMetasNewestFirst(metas)

	return metas, nil
}

// sortMetasNewestFirst sorts metadata by creation time, newest first.
func sortMetasNewestFirst(metas []BaselineMeta) {
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].CreatedAt.After(metas[j].CreatedAt)
	})
}
