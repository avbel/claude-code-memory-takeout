package memory

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/avbel/claude-code-memory-takeout/internal/locate"
)

func TestLoadDirs(t *testing.T) {
	matches := []locate.Match{
		{Slug: "typed", Dir: filepath.Join(fixturePath, "typed"), Rel: "sub"},
		{Slug: "indexed", Dir: filepath.Join(fixturePath, "indexed"), Rel: "."},
	}
	res := LoadDirs(matches)

	names := make([]string, len(res.Facts))
	for i, f := range res.Facts {
		names[i] = f.Name
	}
	// Global sort by Name: alpha, beta, metadata-type, no-frontmatter,
	// top-level-type.
	want := []string{"alpha", "beta", "metadata-type", "no-frontmatter", "top-level-type"}
	if !slices.Equal(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
	// Facts carry the Rel of their memory dir.
	for _, f := range res.Facts {
		if f.Name == "alpha" && f.Rel != "." {
			t.Errorf("alpha rel = %q, want \".\"", f.Rel)
		}
		if f.Name == "metadata-type" && f.Rel != "sub" {
			t.Errorf("metadata-type rel = %q, want \"sub\"", f.Rel)
		}
	}
}

func TestLoadDirsIndexWarnings(t *testing.T) {
	matches := []locate.Match{{Slug: "indexed", Dir: filepath.Join(fixturePath, "indexed"), Rel: "."}}
	res := LoadDirs(matches)
	if res.Skipped != 0 {
		t.Errorf("skipped = %d, want 0", res.Skipped)
	}
	if !slices.Contains(res.Warnings, "index points to missing missing.md") {
		t.Errorf("warnings = %v, want the missing pointer", res.Warnings)
	}
	if !slices.Contains(res.Verbose, ".: 1 facts not in the index") {
		t.Errorf("verbose = %v, want the unindexed count for beta.md", res.Verbose)
	}
}

func TestLoadDirsSkippedAndUnreadable(t *testing.T) {
	matches := []locate.Match{
		{Slug: "edge", Dir: filepath.Join(fixturePath, "edge"), Rel: "edge"},
		{Slug: "gone", Dir: filepath.Join(fixturePath, "edge", "no-such-dir"), Rel: "gone"},
	}
	res := LoadDirs(matches)
	if res.Skipped != 1 {
		t.Errorf("skipped = %d, want 1 (empty.md)", res.Skipped)
	}
	if !slices.ContainsFunc(res.Verbose, func(v string) bool {
		return strings.HasPrefix(v, "skipped edge/empty.md: no description and no body")
	}) {
		t.Errorf("verbose = %v, want the skipped-file detail", res.Verbose)
	}
	// A degraded parse is kept as a fact with an always-shown warning.
	if !slices.ContainsFunc(res.Warnings, func(w string) bool {
		return strings.HasPrefix(w, "edge/bad-yaml.md: parse frontmatter")
	}) {
		t.Errorf("warnings = %v, want the bad-yaml note", res.Warnings)
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool {
		return strings.HasPrefix(w, "gone: read memory dir:")
	}) {
		t.Errorf("warnings = %v, want the unreadable-dir note", res.Warnings)
	}
}

func TestLoadDirsEmpty(t *testing.T) {
	res := LoadDirs(nil)
	if len(res.Facts) != 0 || res.Skipped != 0 {
		t.Errorf("LoadDirs(nil) = %+v, want empty", res)
	}
}
