package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseFileTrailingSpaceDelimiter(t *testing.T) {
	// The frontmatter delimiters must be exactly `---`; a `--- ` line is
	// not a delimiter, so the whole file stays one body-only fact.
	path := filepath.Join(t.TempDir(), "space.md")
	text := "--- \nname: nope\n--- \n\nBody.\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	pr := ParseFile(path)
	if !pr.OK {
		t.Fatalf("ok = %v, warning %q", pr.OK, pr.Warning)
	}
	if pr.Fact.Name != "space" || pr.Fact.Type != "" {
		t.Errorf("fact = %+v, want a no-frontmatter fallback", pr.Fact)
	}
	if !strings.Contains(pr.Fact.Body, "name: nope") {
		t.Errorf("body = %q, want the whole raw file", pr.Fact.Body)
	}
}

func TestParseFileZeroBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.md")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	pr := ParseFile(path)
	if pr.OK {
		t.Fatal("a 0-byte file must be skipped")
	}
	if !strings.Contains(pr.Warning, "no description and no body") {
		t.Errorf("warning = %q", pr.Warning)
	}
}

func TestParseFileModifiedWithOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "offset.md")
	text := "---\nname: offset\nmetadata:\n  type: project\n  modified: 2026-11-16T23:30:00+02:00\n---\n\nBody.\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	pr := ParseFile(path)
	if !pr.OK || pr.Warning != "" {
		t.Fatalf("ok=%v warning=%q", pr.OK, pr.Warning)
	}
	// The instant matches the +02:00 interpretation, and the LLM hint
	// renders the date in the parsed zone.
	want := time.Date(2026, 11, 16, 23, 30, 0, 0, time.FixedZone("", 2*3600))
	if !pr.Fact.Modified.Equal(want) {
		t.Errorf("modified = %v, want %v", pr.Fact.Modified, want)
	}
	if got := pr.Fact.Modified.Format("2006-01-02"); got != "2026-11-16" {
		t.Errorf("hint date = %q, want the offset-local date", got)
	}
}

func TestParseFileTooLargeSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.md")
	big := make([]byte, MaxFactBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	pr := ParseFile(path)
	if pr.OK {
		t.Fatal("a file above the cap must be skipped without reading it")
	}
	if !strings.Contains(pr.Warning, "MiB cap") {
		t.Errorf("warning = %q", pr.Warning)
	}
}
