package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avbel/claude-code-memory-takeout/internal/memory"
)

var update = flag.Bool("update", false, "rewrite golden files")

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run go test -update ./internal/render/)", path, err)
	}
	if string(want) != got {
		t.Errorf("golden %s mismatch\ngot:\n%s\nwant:\n%s", name, got, string(want))
	}
}

func TestAnchorKnownGitHub(t *testing.T) {
	cases := []struct{ heading, want string }{
		{"Hello World", "hello-world"},
		{"foo_bar", "foo_bar"},
		{"Foo Bar!", "foo-bar"},
		{"C++", "c"},
		{"1. Heading", "1-heading"},
		{"Über café", "über-café"},
		{"a\U0001F600b", "ab"},
		{"  spaces  ", "spaces"},
		{"--dashes--", "--dashes--"},
	}
	for _, tc := range cases {
		if got := Anchor(tc.heading); got != tc.want {
			t.Errorf("Anchor(%q) = %q, want %q", tc.heading, got, tc.want)
		}
	}
}

func TestAnchorNumbering(t *testing.T) {
	a := NewAnchorer()
	var got []string
	for _, h := range []string{"Hello", "Hello", "Other", "Hello"} {
		got = append(got, a.Next(h))
	}
	want := []string{"hello", "hello-1", "other", "hello-2"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("anchors = %v, want %v", got, want)
	}
}

func TestHeadings(t *testing.T) {
	facts := []memory.Fact{
		{Name: "solo", Rel: ".", File: "a.md"},
		{Name: "dup", Rel: ".", File: "dup.md"},
		{Name: "dup", Rel: "packages/web", File: "dup.md"},
		{Name: "dup", Rel: "packages/web", File: "dup-2.md"},
		{Name: "twice", Rel: ".", File: "t1.md"},
		{Name: "twice", Rel: ".", File: "t2.md"},
	}
	got := Headings(facts)
	want := []string{
		"solo", "dup", "dup (packages/web) (dup.md)", "dup (packages/web) (dup-2.md)",
		"twice (t1.md)", "twice (t2.md)",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("headings = %v, want %v", got, want)
	}
}

func TestDemote(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"shallow to four", "# h\n## h2", "#### h\n##### h2"},
		{"deep raised", "###### deep", "#### deep"},
		{"fenced untouched", "```\n# in fence\n```\n## out", "```\n# in fence\n```\n#### out"},
		{"tilde fence", "~~~\n# t\n~~~\n# out", "~~~\n# t\n~~~\n#### out"},
		{"no headings", "plain text", "plain text"},
		{"already four", "#### stays\n##### stays too", "#### stays\n##### stays too"},
		{"no space not heading", "#nospace\n# real", "#nospace\n#### real"},
		{"seven hashes", "####### seven\n# real", "####### seven\n#### real"},
	}
	for _, tc := range cases {
		if got := Demote(tc.in); got != tc.want {
			t.Errorf("%s: Demote = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRewriteLinks(t *testing.T) {
	wiki := func(target string) (string, bool) {
		if target == "known" {
			return "known-anchor", true
		}
		return "", false
	}
	file := func(name string) (string, bool) {
		if name == "known.md" {
			return "known-anchor", true
		}
		return "", false
	}
	cases := []struct{ name, in, want string }{
		{"wiki resolved", "see [[known]]", "see [known](#known-anchor)"},
		{"wiki label", "see [[known|the label]]", "see [the label](#known-anchor)"},
		{"wiki unresolved", "see [[unknown]]", "see unknown"},
		{"md link same dir", "see [text](known.md)", "see [text](#known-anchor)"},
		{"md link other file", "see [text](other.md)", "see [text](other.md)"},
		{"md link with path", "see [text](dir/known.md)", "see [text](dir/known.md)"},
		{"image untouched", "![alt](known.md)", "![alt](known.md)"},
		{"inline code untouched", "run `[[known]]` now", "run `[[known]]` now"},
		{"fenced untouched", "```\n[[known]] and # x\n```", "```\n[[known]] and # x\n```"},
		{"anchor link untouched", "see [x](#other)", "see [x](#other)"},
	}
	for _, tc := range cases {
		if got := RewriteLinks(tc.in, wiki, file); got != tc.want {
			t.Errorf("%s: RewriteLinks = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBodyHeadings(t *testing.T) {
	body := "#### a\n\n```\n# fenced\n```\n\n##### b"
	got := BodyHeadings(body)
	if strings.Join(got, "|") != "a|b" {
		t.Errorf("BodyHeadings = %v, want [a b]", got)
	}
}

// TestDocumentNumbering covers the plan's anchor rules: a fact named
// "project" is numbered after the "## Project" section heading, and a body
// heading equal to a fact name takes the next number.
func TestDocumentNumbering(t *testing.T) {
	facts := []memory.Fact{
		{Name: "pp", Type: "project", Rel: ".", File: "pp.md", Body: "Body."},
		{Name: "project", Type: "", Rel: ".", File: "project.md", Body: "The plain fact."},
		{Name: "zz", Type: "", Rel: ".", File: "zz.md", Body: "#### project\n\nPoints at [[project]]."},
	}
	doc := Document(Doc{
		Project: "numbered",
		Mode:    "verbatim",
		Sources: []Source{{Rel: ".", Facts: len(facts)}},
		Facts:   facts,
	})
	for _, want := range []string{
		"### project\n",
		"[project](#project-1)",
		"#### project\n",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document missing %q:\n%s", want, doc)
		}
	}
}

func TestDocumentEmptySectionsOmitted(t *testing.T) {
	facts := []memory.Fact{{Name: "only", Type: "user", Rel: ".", File: "only.md", Body: "Body."}}
	doc := Document(Doc{
		Project: "solo", Mode: "verbatim",
		Sources: []Source{{Rel: ".", Facts: 1}},
		Facts:   facts,
	})
	if strings.Contains(doc, "## Feedback") || strings.Contains(doc, "## Other") {
		t.Errorf("empty sections must be omitted:\n%s", doc)
	}
	if !strings.Contains(doc, "## User\n### only\n\nBody.") {
		t.Errorf("document = \n%s", doc)
	}
	if !strings.HasSuffix(doc, "Body.\n") {
		t.Errorf("document must end with exactly one newline:\n%q", doc[len(doc)-10:])
	}
}
