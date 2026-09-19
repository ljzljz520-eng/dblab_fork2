package plan

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// renderExtraKeys is the whitelist of vendor evidence shown in the view.
var renderExtraKeys = []string{
	"spill",
	"Sort Method",
	"Hash Batches",
	"Original Hash Batches",
	"Join Type",
	"Partial Mode",
	"using_filesort",
	"using_temporary_table",
}

// Tree drawing components.
//
// Every branch is four terminal columns wide, so continuation slots line up
// across nesting levels.
const (
	branchRoot   = "┌── "
	branchMid    = "├── "
	branchLast   = "└── "
	slotContinue = "│   "
	slotBlank    = "    "
	branchIndent = "    "
)

// Render renders the unified plan tree as readable, stable text.
func Render(p *Plan) string {
	if p == nil || p.Root == nil {
		return "no plan available"
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Execution Plan · %s · %s\n", p.Dialect, p.Mode)
	b.WriteString(strings.Repeat("─", 60))
	b.WriteByte('\n')

	renderNode(&b, p.Root, branchRoot, "")

	if len(p.Warnings) > 0 {
		b.WriteString(strings.Repeat("─", 60))
		b.WriteString("\n")
		b.WriteString("Notes:\n")
		for _, w := range p.Warnings {
			fmt.Fprintf(&b, "• %s\n", w)
		}
	}

	return b.String()
}

// renderNode renders one node and its descendants.
//
// branch is the connector drawn for the node itself and indent is the
// continuation prefix accumulated from the ancestor levels.
func renderNode(b *strings.Builder, n *Node, branch, indent string) {
	b.WriteString(indent)
	b.WriteString(branch)
	b.WriteString(string(n.OpType))

	if target := nodeTarget(n); target != "" {
		b.WriteByte(' ')
		b.WriteString(target)
	}

	if metrics := renderMetrics(n); metrics != "" {
		b.WriteString("  [")
		b.WriteString(metrics)
		b.WriteByte(']')
	}

	b.WriteByte('\n')

	innerIndent := indent + branchIndent

	if n.Predicate != "" {
		b.WriteString(innerIndent)
		b.WriteString("predicate: ")
		b.WriteString(truncate(n.Predicate, 200))
		b.WriteByte('\n')
	}

	if evidence := renderEvidence(n); evidence != "" {
		b.WriteString(innerIndent)
		b.WriteString(evidence)
		b.WriteString("\n")
	}

	if actual := renderActual(n); actual != "" {
		b.WriteString(innerIndent)
		b.WriteString(actual)
		b.WriteString("\n")
	}

	for i, ch := range n.Children {
		last := i == len(n.Children)-1

		var nextBranch, nextIndent string
		if last {
			nextBranch = branchLast
			nextIndent = indent + slotBlank
		} else {
			nextBranch = branchMid
			nextIndent = indent + slotContinue
		}

		renderNode(b, ch, nextBranch, nextIndent)
	}
}

// nodeTarget returns the object[.index] / alias target of a node.
func nodeTarget(n *Node) string {
	var target string
	switch {
	case n.Object != "" && n.Index != "":
		target = n.Object + " (" + n.Index + ")"
	case n.Object != "":
		target = n.Object
	case n.Index != "":
		target = "(" + n.Index + ")"
	}
	if n.Alias != "" && n.Alias != n.Object {
		target += " AS " + n.Alias
	}
	return target
}

// renderMetrics renders estimated cost / rows / width.
func renderMetrics(n *Node) string {
	var parts []string
	if n.Metrics.TotalCost != 0 {
		parts = append(parts, "cost "+trimFloat(n.Metrics.TotalCost))
	}
	if n.Metrics.Rows != 0 {
		parts = append(parts, "rows "+trimFloat(n.Metrics.Rows))
	}
	if n.Metrics.Width != 0 {
		parts = append(parts, "width "+trimFloat(n.Metrics.Width))
	}
	return strings.Join(parts, " · ")
}

// renderActual renders actual execution data, including the bias marker.
func renderActual(n *Node) string {
	if !n.Metrics.HasActual() {
		return ""
	}

	parts := []string{
		"actual rows " + trimFloat(n.Metrics.ActualRows),
		"loops " + trimFloat(n.Metrics.ActualLoops),
	}

	if n.Metrics.ActualTotalMS != 0 {
		parts = append(parts,
			trimFloat(n.Metrics.ActualStartupMS)+"→"+trimFloat(n.Metrics.ActualTotalMS)+" ms")
	}

	if ratio, ok := n.Metrics.BiasRatio(); ok && (ratio >= 2 || ratio <= 0.5) {
		parts = append(parts, "⚠ est/actual "+trimFloat(ratio)+"x")
	}

	if n.Metrics.SharedReadBlocks != 0 {
		parts = append(parts,
			"shared hit/read "+trimFloat(n.Metrics.SharedHitBlocks)+"/"+trimFloat(n.Metrics.SharedReadBlocks))
	}

	return strings.Join(parts, " · ")
}

// renderEvidence renders the whitelisted vendor evidence, sorted by key.
func renderEvidence(n *Node) string {
	var parts []string

	for _, k := range renderExtraKeys {
		if v, ok := n.Extra[k]; ok {
			parts = append(parts, k+": "+v)
		}
	}

	if len(parts) == 0 {
		return ""
	}

	sort.Strings(parts)

	return strings.Join(parts, " · ")
}

// trimFloat formats a float without trailing zeros.
func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// truncate caps a string, appending an ellipsis when shortened.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
