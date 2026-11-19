package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixturePath is the module's testdata/memory dir; tests run from the
// package dir, so the fixtures live two levels up.
var fixturePath = filepath.Join("..", "..", "testdata", "memory")

func TestParseFilePlacements(t *testing.T) {
	pr := ParseFile(filepath.Join(fixturePath, "typed", "metadata-type.md"))
	if !pr.OK || pr.Warning != "" {
		t.Fatalf("parse metadata-type: ok=%v warning=%q", pr.OK, pr.Warning)
	}
	f := pr.Fact
	if f.Name != "metadata-type" || f.Type != "project" || f.Description != "Fact with metadata.type" {
		t.Errorf("fact = %+v", f)
	}
	if f.Body != "Body of metadata-type." {
		t.Errorf("body = %q", f.Body)
	}
	want := time.Date(2026, 11, 16, 10, 29, 0, 0, time.UTC)
	if !f.Modified.Equal(want) {
		t.Errorf("modified = %v, want %v", f.Modified, want)
	}
	if f.Rel != "" || f.File != "metadata-type.md" {
		t.Errorf("file/rel = %q/%q", f.File, f.Rel)
	}

	pr = ParseFile(filepath.Join(fixturePath, "typed", "top-level-type.md"))
	if !pr.OK || pr.Warning != "" {
		t.Fatalf("parse top-level-type: ok=%v warning=%q", pr.OK, pr.Warning)
	}
	if pr.Fact.Type != "user" {
		t.Errorf("type = %q, want the top-level type", pr.Fact.Type)
	}

	pr = ParseFile(filepath.Join(fixturePath, "typed", "no-frontmatter.md"))
	if !pr.OK || pr.Warning != "" {
		t.Fatalf("parse no-frontmatter: ok=%v warning=%q", pr.OK, pr.Warning)
	}
	f = pr.Fact
	if f.Name != "no-frontmatter" || f.Type != "" || f.Description != "" {
		t.Errorf("fact = %+v", f)
	}
	if f.Body != "Just a body, no frontmatter at all." {
		t.Errorf("body = %q", f.Body)
	}
}

func TestParseFileBadYAML(t *testing.T) {
	pr := ParseFile(filepath.Join(fixturePath, "edge", "bad-yaml.md"))
	if !pr.OK {
		t.Fatalf("bad yaml must fall back to a body-only fact, got skipped: %q", pr.Warning)
	}
	if !strings.Contains(pr.Warning, "parse frontmatter") {
		t.Errorf("warning = %q, want a frontmatter parse note", pr.Warning)
	}
	if pr.Fact.Name != "bad-yaml" || pr.Fact.Type != "" {
		t.Errorf("fact = %+v", pr.Fact)
	}
	if !strings.Contains(pr.Fact.Body, "---") || !strings.Contains(pr.Fact.Body, "Body never reached.") {
		t.Errorf("body = %q, want the whole file kept", pr.Fact.Body)
	}
}

func TestParseFileEmptyFact(t *testing.T) {
	pr := ParseFile(filepath.Join(fixturePath, "edge", "empty.md"))
	if pr.OK {
		t.Fatal("empty fact must be skipped")
	}
	if !strings.Contains(pr.Warning, "no description and no body") {
		t.Errorf("warning = %q", pr.Warning)
	}
}

func TestParseFileUnclosedFrontmatter(t *testing.T) {
	pr := ParseFile(filepath.Join(fixturePath, "edge", "unclosed-fm.md"))
	if !pr.OK {
		t.Fatalf("unclosed frontmatter must fall back, got skipped: %q", pr.Warning)
	}
	if !strings.Contains(pr.Warning, "no closing ---") {
		t.Errorf("warning = %q", pr.Warning)
	}
	if !strings.Contains(pr.Fact.Body, "never closes its frontmatter") {
		t.Errorf("body = %q, want the whole file kept", pr.Fact.Body)
	}
}

func TestParseFileBadModified(t *testing.T) {
	pr := ParseFile(filepath.Join(fixturePath, "edge", "modified-bad.md"))
	if !pr.OK || pr.Warning != "" {
		t.Fatalf("parse modified-bad: ok=%v warning=%q", pr.OK, pr.Warning)
	}
	if !pr.Fact.Modified.IsZero() {
		t.Errorf("modified = %v, want zero time without a warning", pr.Fact.Modified)
	}
	if pr.Fact.Type != "reference" {
		t.Errorf("type = %q", pr.Fact.Type)
	}
}

func TestParseFileIndexSkipped(t *testing.T) {
	for _, name := range []string{"MEMORY.md", "memory.md"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("- [x](x.md)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if pr := ParseFile(path); pr.OK || pr.Warning != "" {
			t.Errorf("ParseFile(%s) = ok=%v warning=%q, want silently skipped", name, pr.OK, pr.Warning)
		}
	}
}

// TestParseFileCRLFBOM covers Windows-written files and fixtures checked
// out by Windows CI: a leading UTF-8 BOM and CRLF line endings must not
// break the `---` frontmatter check.
func TestParseFileCRLFBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "win.md")
	body := "\uFEFF---\r\n" +
		"name: win\r\n" +
		"description: Windows line endings\r\n" +
		"metadata:\r\n" +
		"  type: project\r\n" +
		"---\r\n" +
		"\r\n" +
		"Body written on Windows.\r\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pr := ParseFile(path)
	if !pr.OK || pr.Warning != "" {
		t.Fatalf("parse win.md: ok=%v warning=%q", pr.OK, pr.Warning)
	}
	if pr.Fact.Name != "win" || pr.Fact.Type != "project" {
		t.Errorf("fact = %+v", pr.Fact)
	}
	if pr.Fact.Body != "Body written on Windows." {
		t.Errorf("body = %q", pr.Fact.Body)
	}
}

func TestParseFileNameFallbacks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stem-fallback.md")
	text := "---\nname: \"   \"\ndescription: Whitespace-only name falls back to the stem\nmetadata:\n  type: project\n---\n\nBody.\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	pr := ParseFile(path)
	if !pr.OK || pr.Warning != "" {
		t.Fatalf("parse: ok=%v warning=%q", pr.OK, pr.Warning)
	}
	if pr.Fact.Name != "stem-fallback" {
		t.Errorf("name = %q, want the file stem", pr.Fact.Name)
	}
}
