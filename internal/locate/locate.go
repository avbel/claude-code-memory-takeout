package locate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Match is one memory directory that belongs to the project.
type Match struct {
	Slug string
	Dir  string // absolute path of the memory dir
	Rel  string // "." for the project itself, else the path relative to it
}

// Options tunes Find; a nil Warn or Verbose drops the message.
type Options struct {
	Subdirs bool
	Warn    func(string) // always shown
	Verbose func(string) // shown only with -v
}

// slugTruncateLen is where newer Claude Code versions may cut a long slug
// and append a hash; matches by prefix start there.
const slugTruncateLen = 200

// Find returns every Claude Code memory dir that belongs to root: the
// project's own slug, plus — when opts.Subdirs is set — the slugs of its
// subdirectories and .claude/worktrees/*. Paths are encoded forward into
// slugs and compared, never decoded back, so a sibling that merely shares
// a name prefix (fluidd vs fluidd-bench) stays excluded.
func Find(claudeDir, root string, opts Options) ([]Match, error) {
	warn, verbose := opts.Warn, opts.Verbose
	if warn == nil {
		warn = func(string) {}
	}
	if verbose == nil {
		verbose = func(string) {}
	}
	opts.Warn, opts.Verbose = warn, verbose

	bases := slugBases(root)
	entries, err := os.ReadDir(filepath.Join(claudeDir, "projects"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", filepath.Join(claudeDir, "projects"), err)
	}
	var matches []Match
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if m, ok := matchSlug(claudeDir, e.Name(), root, bases, opts); ok {
			matches = append(matches, m)
		}
	}
	return matches, nil
}

// matchSlug decides whether one projects/<slug> dir belongs to root. It
// qualifies only when its memory/ dir holds at least one *.md file, so
// empty memory dirs never produce a match.
func matchSlug(claudeDir, slug, root string, bases []string, opts Options) (Match, bool) {
	memDir := filepath.Join(claudeDir, "projects", slug, "memory")
	if !hasFacts(memDir) {
		return Match{}, false
	}
	if slices.Contains(bases, slug) {
		warnCollisions(slug, root, opts)
		return Match{Slug: slug, Dir: memDir, Rel: "."}, true
	}
	if opts.Subdirs {
		for _, base := range bases {
			if m, ok := matchSubdir(slug, memDir, base, root, opts); ok {
				return m, true
			}
		}
	}
	for _, base := range bases {
		// Newer Claude Code versions may truncate a very long slug and
		// append a hash; accept a dir whose name starts with the first
		// 200 chars of the encoding and continues with a bare cut or a
		// hex-ish suffix. Without the suffix rule, a sibling's slug would
		// be imported as the project's own memory.
		if len(base) <= slugTruncateLen || slug == base || !strings.HasPrefix(slug, base[:slugTruncateLen]) {
			continue
		}
		if rest := slug[slugTruncateLen:]; len(rest) == 0 || hashLike(rest) {
			opts.Warn(fmt.Sprintf("matched %s by truncated prefix (encoding is %d chars)", slug, len(base)))
			warnCollisions(slug, root, opts)
			return Match{Slug: slug, Dir: memDir, Rel: "."}, true
		}
	}
	return Match{}, false
}

// hashLike reports whether s looks like the hash suffix Claude Code
// appends to a truncated slug.
func hashLike(s string) bool {
	if len(s) < 4 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if (s[i] < '0' || s[i] > '9') && (s[i] < 'a' || s[i] > 'f') {
			return false
		}
	}
	return true
}

// matchSubdir matches slug against one base as a subdirectory or worktree
// of root; rest is the slug fragment after the base and starts with "-".
func matchSubdir(slug, memDir, base, root string, opts Options) (Match, bool) {
	if !strings.HasPrefix(slug, base+"-") {
		return Match{}, false
	}
	rest := slug[len(base):]
	const worktrees = "--claude-worktrees-"
	if strings.HasPrefix(rest, worktrees) && len(rest) > len(worktrees) {
		// Worktree memory is matched from the slug alone: the worktree
		// dir itself may already be deleted. The collision check is still
		// worth its one descent: a crafted sibling directory named like
		// the slug imports foreign memory without any other warning.
		warnCollisions(slug, root, opts)
		return Match{Slug: slug, Dir: memDir, Rel: ".claude/worktrees/" + rest[len(worktrees):]}, true
	}
	rels := resolve(root, rest)
	if len(rels) == 0 {
		opts.Verbose(fmt.Sprintf("skipped %s: no matching subdirectory", slug))
		return Match{}, false
	}
	if len(rels) > 1 {
		opts.Warn(fmt.Sprintf("ambiguous %s: %s, using %s", slug, strings.Join(rels, ", "), rels[0]))
	}
	warnCollisions(slug, root, opts)
	return Match{Slug: slug, Dir: memDir, Rel: rels[0]}, true
}

// slugBases returns the slug encodings under which root is matched: the
// path as given plus its symlink-resolved form (macOS /tmp →
// /private/tmp); Claude Code records the resolved path, callers may pass
// either.
func slugBases(root string) []string {
	base := Encode(root)
	bases := []string{base}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return bases
	}
	if enc := Encode(resolved); enc != base {
		bases = append(bases, enc)
	}
	return bases
}

// hasFacts reports whether the memory dir exists and holds at least one
// fact file directly inside it. The index MEMORY.md is a pointer list,
// not a fact, so an index-only dir does not count.
func hasFacts(memDir string) bool {
	entries, err := os.ReadDir(memDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || strings.EqualFold(e.Name(), "MEMORY.md") {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			return true
		}
	}
	return false
}

// resolve is a guided descent over dir: it reads only the directories on
// the way, so huge trees such as node_modules are never walked. rest is a
// slug fragment that starts with "-" (one separator plus the next name).
// Results are slash-separated paths relative to dir, sorted.
func resolve(dir, rest string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var results []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// DirEntry.IsDir has lstat semantics, so symlinked directories
		// are not followed.
		enc := "-" + Encode(e.Name())
		if rest == enc {
			results = append(results, e.Name())
			continue
		}
		if strings.HasPrefix(rest, enc+"-") {
			for _, sub := range resolve(filepath.Join(dir, e.Name()), rest[len(enc):]) {
				results = append(results, filepath.ToSlash(filepath.Join(e.Name(), sub)))
			}
		}
	}
	slices.Sort(results)
	return results
}

// warnCollisions reports that a matched slug also encodes a real
// directory outside root: fluidd/packages and the sibling fluidd-packages
// share one slug, so the memory may belong to the other project. The
// descent reads only the directories along the slug, so it stays cheap.
// Paths found under either spelling of root (the given path and its
// symlink-resolved form) are root itself, not collisions.
func warnCollisions(slug, root string, opts Options) {
	roots := []string{root}
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		roots = append(roots, resolved)
	}
	for _, p := range pathsForSlug(slug) {
		if !isOutsideRoots(p, roots) {
			continue
		}
		opts.Warn(fmt.Sprintf("%s also matches %s; its memory may belong there", slug, p))
	}
}

func isOutsideRoots(p string, roots []string) bool {
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+string(os.PathSeparator)) {
			return false
		}
	}
	return true
}

// pathsForSlug returns every real directory whose encoding equals slug,
// found by the same guided descent as resolve, started at the filesystem
// root.
func pathsForSlug(slug string) []string {
	dir, rest := descentRoot(slug)
	var paths []string
	for _, rel := range resolve(dir, rest) {
		if dir == string(os.PathSeparator) {
			paths = append(paths, string(os.PathSeparator)+rel)
		} else {
			paths = append(paths, filepath.Join(dir, rel))
		}
	}
	return paths
}

// descentRoot maps a slug to the directory its descent starts from: the
// POSIX root, or the drive volume for a slug whose first character is a
// drive letter followed by the ":" encoding.
func descentRoot(slug string) (string, string) {
	if runtime.GOOS == "windows" && len(slug) >= 2 && isSlugLetter(slug[0]) && slug[1] == '-' {
		return string(slug[0]) + `:\`, slug[2:]
	}
	return string(os.PathSeparator), slug
}

func isSlugLetter(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}
