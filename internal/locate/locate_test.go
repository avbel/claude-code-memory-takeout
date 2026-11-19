package locate

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// newTree returns a symlink-free project parent. t.TempDir's path on
// macOS goes through /var → /private/var; tests build and match under the
// resolved path, the way Claude Code itself records projects, so the
// collision descent from the filesystem root can walk the whole slug.
func newTree(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return dir
}

// sink collects the warnings and verbose lines of one Find call.
type sink struct {
	warns   []string
	verbose []string
}

func (s *sink) opts(subdirs bool) Options {
	return Options{
		Subdirs: subdirs,
		Warn:    func(m string) { s.warns = append(s.warns, m) },
		Verbose: func(m string) { s.verbose = append(s.verbose, m) },
	}
}

// addMemory creates <claude>/projects/<slug>/memory with one fact file.
func addMemory(t *testing.T, claudeDir, slug string) {
	t.Helper()
	dir := filepath.Join(claudeDir, "projects", slug, "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fact.md"), []byte("---\nname: fact\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write fact: %v", err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

// rels returns the Rel values of the matches in order.
func rels(ms []Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Rel
	}
	return out
}

func TestFindExactMatch(t *testing.T) {
	tree := newTree(t)
	root := filepath.Join(tree, "proj")
	mkdir(t, root)
	claude := filepath.Join(tree, "claude")
	addMemory(t, claude, Encode(root))

	s := &sink{}
	ms, err := Find(claude, root, s.opts(true))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !slices.Equal(rels(ms), []string{"."}) {
		t.Fatalf("rels = %v, want [\".\"]", rels(ms))
	}
	if len(s.warns) != 0 {
		t.Errorf("warnings = %v, want none", s.warns)
	}
}

func TestFindSubdirAndWorktree(t *testing.T) {
	tree := newTree(t)
	root := filepath.Join(tree, "proj")
	mkdir(t, filepath.Join(root, "packages", "web"))
	claude := filepath.Join(tree, "claude")
	addMemory(t, claude, Encode(root))
	addMemory(t, claude, Encode(filepath.Join(root, "packages", "web")))
	addMemory(t, claude, Encode(root)+"--claude-worktrees-gone") // worktree deleted

	s := &sink{}
	ms, err := Find(claude, root, s.opts(true))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	want := []string{".", ".claude/worktrees/gone", "packages/web"}
	if !slices.Equal(rels(ms), want) {
		t.Fatalf("rels = %v, want %v", rels(ms), want)
	}
}

func TestFindSiblingExcluded(t *testing.T) {
	tree := newTree(t)
	root := filepath.Join(tree, "fluidd")
	mkdir(t, root)
	sibling := filepath.Join(tree, "fluidd-bench")
	mkdir(t, sibling)
	claude := filepath.Join(tree, "claude")
	addMemory(t, claude, Encode(root))
	addMemory(t, claude, Encode(sibling))

	s := &sink{}
	ms, err := Find(claude, root, s.opts(true))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !slices.Equal(rels(ms), []string{"."}) {
		t.Fatalf("rels = %v, want only the root", rels(ms))
	}
	if len(s.warns) != 0 {
		t.Errorf("warnings = %v, want none", s.warns)
	}
}

// TestFindPreFluidd3 covers two real projects sharing one slug shape:
// fluidd-pre/Fluidd-3 vs fluidd/pre-Fluidd-3. Only the directory that
// actually exists is matched, so one project cannot steal the other's
// memory.
func TestFindPreFluidd3(t *testing.T) {
	tree := newTree(t)
	pre := filepath.Join(tree, "fluidd-pre")
	mkdir(t, filepath.Join(pre, "Fluidd-3"))
	// fluidd/pre/Fluidd-3 does not exist; only the fluidd-pre spelling is real.
	claude := filepath.Join(tree, "claude")
	addMemory(t, claude, Encode(pre))
	addMemory(t, claude, Encode(filepath.Join(pre, "Fluidd-3")))

	s := &sink{}
	ms, err := Find(claude, pre, s.opts(true))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !slices.Equal(rels(ms), []string{".", "Fluidd-3"}) {
		t.Fatalf("rels = %v, want [\".\", \"Fluidd-3\"]", rels(ms))
	}
	if len(s.warns) != 0 {
		t.Errorf("warnings = %v, want none", s.warns)
	}

	// From fluidd (which exists but has no memory of its own) the slug
	// that happens to start with fluidd's encoding must not match.
	fluidd := filepath.Join(tree, "fluidd")
	mkdir(t, fluidd)
	s2 := &sink{}
	ms, err = Find(claude, fluidd, s2.opts(true))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(ms) != 0 {
		t.Fatalf("rels = %v, want no matches", rels(ms))
	}
	if !slices.Contains(s2.verbose, "skipped "+Encode(pre)+"-Fluidd-3: no matching subdirectory") {
		t.Errorf("verbose = %v, want the skipped-slug line", s2.verbose)
	}
}

func TestFindAmbiguousSubdir(t *testing.T) {
	tree := newTree(t)
	root := filepath.Join(tree, "amb")
	mkdir(t, filepath.Join(root, "b-c"))
	mkdir(t, filepath.Join(root, "b", "c"))
	claude := filepath.Join(tree, "claude")
	addMemory(t, claude, Encode(root)+"-b-c")

	s := &sink{}
	ms, err := Find(claude, root, s.opts(true))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !slices.Equal(rels(ms), []string{"b-c"}) {
		t.Fatalf("rels = %v, want the first sorted spelling", rels(ms))
	}
	want := "ambiguous " + Encode(root) + "-b-c: b-c, b/c, using b-c"
	if !slices.Contains(s.warns, want) {
		t.Errorf("warnings = %v, want %q", s.warns, want)
	}
}

func TestFindCollisionWarning(t *testing.T) {
	tree := newTree(t)
	root := filepath.Join(tree, "proj")
	mkdir(t, filepath.Join(root, "packages"))
	sibling := filepath.Join(tree, "proj-packages")
	mkdir(t, sibling)
	claude := filepath.Join(tree, "claude")
	addMemory(t, claude, Encode(root))
	addMemory(t, claude, Encode(sibling)) // same slug as proj/packages

	s := &sink{}
	ms, err := Find(claude, root, s.opts(true))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !slices.Equal(rels(ms), []string{".", "packages"}) {
		t.Fatalf("rels = %v, want [\".\", \"packages\"]", rels(ms))
	}
	want := Encode(sibling) + " also matches " + sibling + "; its memory may belong there"
	if !slices.Contains(s.warns, want) {
		t.Errorf("warnings = %v, want %q", s.warns, want)
	}
}

func TestFindEmptyMemorySkipped(t *testing.T) {
	tree := newTree(t)
	root := filepath.Join(tree, "proj")
	mkdir(t, root)
	claude := filepath.Join(tree, "claude")
	addMemory(t, claude, Encode(root))
	// A slug with an empty memory dir is skipped entirely.
	if err := os.MkdirAll(filepath.Join(claude, "projects", Encode(root)+"-empty", "memory"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := &sink{}
	ms, err := Find(claude, root, s.opts(true))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !slices.Equal(rels(ms), []string{"."}) {
		t.Fatalf("rels = %v, want only the root", rels(ms))
	}
}

func TestFindNoSubdirs(t *testing.T) {
	tree := newTree(t)
	root := filepath.Join(tree, "proj")
	mkdir(t, filepath.Join(root, "packages"))
	claude := filepath.Join(tree, "claude")
	addMemory(t, claude, Encode(root))
	addMemory(t, claude, Encode(filepath.Join(root, "packages")))
	addMemory(t, claude, Encode(root)+"--claude-worktrees-x")

	s := &sink{}
	ms, err := Find(claude, root, s.opts(false))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !slices.Equal(rels(ms), []string{"."}) {
		t.Fatalf("rels = %v, want only the root", rels(ms))
	}
}

func TestFindScratchpadNeverMatches(t *testing.T) {
	tree := newTree(t)
	root := filepath.Join(tree, "proj")
	mkdir(t, root)
	claude := filepath.Join(tree, "claude")
	base := Encode(root)
	addMemory(t, claude, base)
	// Scratchpad slugs embed the project slug in the middle, not at the
	// start; a shorter stand-in for the real
	// -private-tmp-claude-501--<base>-<uuid>-scratchpad shape.
	scratch := "claude-501-" + base + "-70b3bf2d-5b67-435a-8dac-e3bda68e965c-scratchpad"
	addMemory(t, claude, scratch)

	s := &sink{}
	ms, err := Find(claude, root, s.opts(true))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !slices.Equal(rels(ms), []string{"."}) {
		t.Fatalf("rels = %v, want only the root; the scratchpad slug must not match", rels(ms))
	}
}

func TestFindTruncatedSlugPrefix(t *testing.T) {
	tree := newTree(t)
	root := filepath.Join(tree, strings.Repeat("p", 210))
	mkdir(t, root)
	claude := filepath.Join(tree, "claude")
	base := Encode(root)
	if len(base) <= slugTruncateLen {
		t.Fatalf("test setup: base is only %d chars", len(base))
	}
	addMemory(t, claude, base[:slugTruncateLen]+"abc123")

	s := &sink{}
	ms, err := Find(claude, root, s.opts(false))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !slices.Equal(rels(ms), []string{"."}) {
		t.Fatalf("rels = %v, want the truncated-prefix match", rels(ms))
	}
	if len(s.warns) != 1 || !strings.Contains(s.warns[0], "truncated prefix") {
		t.Errorf("warnings = %v, want one truncated-prefix warning", s.warns)
	}
}

// TestFindSymlinkedRoot covers the union of the two encodings: the
// project dir is reached through a symlink, and Claude Code recorded the
// resolved path.
func TestFindSymlinkedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	tree := newTree(t)
	real := filepath.Join(tree, "real", "proj")
	mkdir(t, real)
	mkdir(t, filepath.Join(tree, "link"))
	if err := os.Symlink(real, filepath.Join(tree, "link", "proj")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	claude := filepath.Join(tree, "claude")
	addMemory(t, claude, Encode(real))

	s := &sink{}
	ms, err := Find(claude, filepath.Join(tree, "link", "proj"), s.opts(true))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !slices.Equal(rels(ms), []string{"."}) {
		t.Fatalf("rels = %v, want the symlinked root matched via its resolved slug", rels(ms))
	}
}

func TestFindMissingProjectsDir(t *testing.T) {
	tree := newTree(t)
	s := &sink{}
	ms, err := Find(filepath.Join(tree, "nope"), filepath.Join(tree, "proj"), s.opts(true))
	if err != nil {
		t.Fatalf("Find: %v (a missing projects dir is empty, not an error)", err)
	}
	if len(ms) != 0 {
		t.Fatalf("rels = %v, want none", rels(ms))
	}
}

// TestFindIndexOnlyMemorySkipped covers the memory dir that holds only its
// MEMORY.md index: the index is a pointer list, not a fact, so the dir
// does not count and the project reports no memory.
func TestFindIndexOnlyMemorySkipped(t *testing.T) {
	tree := newTree(t)
	root := filepath.Join(tree, "proj")
	mkdir(t, root)
	claude := filepath.Join(tree, "claude")
	memDir := filepath.Join(claude, "projects", Encode(root), "memory")
	mkdir(t, memDir)
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte("- [x](x.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &sink{}
	ms, err := Find(claude, root, s.opts(true))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(ms) != 0 {
		t.Fatalf("matches = %v, want none: the index is not a fact", rels(ms))
	}
}

// TestFindSymlinkedRootNoFalseCollision pins the fix for the false
// collision warning: a root reached through a symlink must not warn that
// its own resolved path "also matches".
func TestFindSymlinkedRootNoFalseCollision(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	tree := newTree(t)
	real := filepath.Join(tree, "real", "proj")
	mkdir(t, real)
	mkdir(t, filepath.Join(tree, "link"))
	if err := os.Symlink(real, filepath.Join(tree, "link", "proj")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	claude := filepath.Join(tree, "claude")
	addMemory(t, claude, Encode(real))

	s := &sink{}
	if _, err := Find(claude, filepath.Join(tree, "link", "proj"), s.opts(true)); err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(s.warns) != 0 {
		t.Errorf("warnings = %v, want none for the project's own resolved path", s.warns)
	}
}

// TestFindTruncatedSlugNeedsHashSuffix documents that the truncated-prefix
// match requires a bare cut or a hash-like continuation: a sibling slug
// that merely shares the first 200 chars stays excluded. The full
// encoding cannot exist on disk (it exceeds the 255-byte filename limit),
// so only the candidate entries are created.
func TestFindTruncatedSlugNeedsHashSuffix(t *testing.T) {
	tree := newTree(t)
	root := filepath.Join(tree, strings.Repeat("p", 210))
	mkdir(t, root)
	sibling := filepath.Join(tree, strings.Repeat("p", 210)+"-suffix")
	mkdir(t, sibling)
	claude := filepath.Join(tree, "claude")
	base := Encode(root)
	if len(base) <= slugTruncateLen {
		t.Fatalf("test setup: base is only %d chars", len(base))
	}
	addMemory(t, claude, base[:slugTruncateLen]+"-suffix")  // the sibling's shape, not a hash
	addMemory(t, claude, base[:slugTruncateLen]+"abcd1234") // a hash-like continuation

	s := &sink{}
	ms, err := Find(claude, root, s.opts(false))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(ms) != 1 || ms[0].Rel != "." {
		t.Fatalf("matches = %v, want only the hash-continuation match", rels(ms))
	}
	if len(s.warns) != 1 || !strings.Contains(s.warns[0], "truncated prefix") {
		t.Errorf("warnings = %v, want one truncated-prefix warning", s.warns)
	}
}

// TestFindWorktreeCollisionWarning covers the crafted-sibling case: a real
// directory named like the slug of root/.claude/worktrees/x encodes to the
// same slug and would import its memory silently; the descent now warns.
func TestFindWorktreeCollisionWarning(t *testing.T) {
	tree := newTree(t)
	root := filepath.Join(tree, "proj")
	mkdir(t, root)
	// The crafted sibling, a project of its own in everything but name.
	sibling := filepath.Join(tree, "proj--claude-worktrees-x")
	mkdir(t, sibling)
	claude := filepath.Join(tree, "claude")
	addMemory(t, claude, Encode(root)+"--claude-worktrees-x") // deleted worktree + crafted sibling share it

	s := &sink{}
	ms, err := Find(claude, root, s.opts(true))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !slices.Equal(rels(ms), []string{".claude/worktrees/x"}) {
		t.Fatalf("rels = %v, want the worktree match", rels(ms))
	}
	want := Encode(root) + "--claude-worktrees-x also matches " + sibling + "; its memory may belong there"
	if !slices.Contains(s.warns, want) {
		t.Errorf("warnings = %v, want the crafted-sibling warning", s.warns)
	}

	// Without the crafted sibling there is nothing outside root to warn
	// about, so the same match stays silent.
	s2 := &sink{}
	if _, err := Find(claude, filepath.Join(newTree(t), "proj"), s2.opts(true)); err != nil {
		t.Fatalf("Find: %v", err)
	}
}
