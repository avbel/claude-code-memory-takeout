package render

import (
	"testing"

	"github.com/avbel/claude-code-memory-takeout/internal/memory"
)

// goldenFacts is the fixture set of the verbatim golden test: every
// section, a duplicate name across dirs, resolved and unresolved wiki
// links, a same-dir .md link, a demoted body heading, and a code fence
// holding a wiki link and a heading that must stay untouched.
func goldenFacts() []memory.Fact {
	return []memory.Fact{
		{
			Name:        "log-format-rules",
			Description: "Log structured lines",
			Type:        "user",
			Rel:         ".",
			File:        "log-format-rules.md",
			Body:        "One JSON line per event.\n\nAlso see [[missing-fact]] for the unimplemented part.",
		},
		{
			Name:        "prefer-verbose-errors",
			Description: "Wrap\nerrors   with context",
			Type:        "user",
			Rel:         ".",
			File:        "prefer-verbose-errors.md",
			Body:        "See [[log-format-rules]] for the paired logging rule.",
		},
		{
			Name:        "batch-checks-and-targeted-tests",
			Description: "Targeted tests before the batch",
			Type:        "feedback",
			Rel:         ".",
			File:        "batch-checks.md",
			Body:        "# How to run them\n\nRun the targeted tests first, then the batch.",
		},
		{
			Name:        "batch-checks-and-targeted-tests",
			Description: "Web package flavour",
			Type:        "feedback",
			Rel:         "packages/web",
			File:        "batch-checks.md",
			Body:        "Same rule for the web package.\n\n```\n[[log-format-rules]] and # not a heading\n```",
		},
		{
			Name:        "web-notes",
			Description: "Notes for the web package",
			Type:        "project",
			Rel:         "packages/web",
			File:        "web-notes.md",
			Body:        "Link: [batch checks](batch-checks.md).",
		},
		{
			Name: "migrated-postgres-port",
			Type: "project",
			Rel:  ".",
			File: "migrated.md",
			Body: "The migrated database listens on port 5433.",
		},
		{
			Name: "legacy-shell-script",
			Type: "reference",
			Rel:  ".",
			File: "legacy.md",
			Body: "```bash\n# a comment, not a heading\n```",
		},
		{
			Name: "unknown-kind-fact",
			Type: "weird",
			Rel:  ".",
			File: "weird.md",
			Body: "Lands under Other.",
		},
	}
}

func TestDocumentGolden(t *testing.T) {
	doc := Document(Doc{
		Project: "golden-demo",
		Mode:    "verbatim",
		Sources: []Source{{Rel: ".", Facts: 6}, {Rel: "packages/web", Facts: 2}},
		Facts:   goldenFacts(),
	})
	compareGolden(t, "verbatim.md", doc)
}
