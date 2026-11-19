package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/avbel/claude-code-memory-takeout/internal/locate"
)

// LoadResult is the outcome of loading every matched memory dir.
type LoadResult struct {
	Facts    []Fact
	Skipped  int      // files that produced no fact
	Warnings []string // always shown
	Verbose  []string // shown only with -v
}

// pointerRe matches the index's `[title](file.md)` links.
var pointerRe = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

// LoadDirs parses the fact files of every matched memory dir. A pointer in
// the index whose file does not exist produces a warning; fact files that
// the index never mentions are still included and counted in the verbose
// lines.
func LoadDirs(matches []locate.Match) LoadResult {
	var res LoadResult
	for _, m := range matches {
		loadDir(&res, m)
	}
	slices.SortStableFunc(res.Facts, Less)
	return res
}

func loadDir(res *LoadResult, m locate.Match) {
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s: read memory dir: %v", m.Rel, err))
		return
	}
	var files []string
	present := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			present[e.Name()] = true
		}
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") ||
			strings.EqualFold(e.Name(), indexName) {
			continue
		}
		files = append(files, e.Name())
	}
	slices.Sort(files)
	for _, name := range files {
		pr := ParseFile(filepath.Join(m.Dir, name))
		if !pr.OK {
			res.Skipped++
			res.Verbose = append(res.Verbose, fmt.Sprintf("skipped %s: %s", relPath(m.Rel, name), pr.Warning))
			continue
		}
		pr.Fact.Rel = m.Rel
		res.Facts = append(res.Facts, pr.Fact)
		if pr.Warning != "" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s", relPath(m.Rel, name), pr.Warning))
		}
	}
	checkIndex(res, m, files, present)
}

// checkIndex walks the MEMORY.md pointers of one memory dir: a pointer to
// a file that is not on disk is a warning, and fact files no pointer
// mentions are counted for the verbose output.
func checkIndex(res *LoadResult, m locate.Match, files []string, present map[string]bool) {
	data, err := os.ReadFile(filepath.Join(m.Dir, indexName))
	if err != nil {
		return // no index is fine; the facts stand on their own
	}
	referenced := make(map[string]bool)
	for _, target := range pointerTargets(string(data)) {
		referenced[target] = true
		if !present[target] {
			res.Warnings = append(res.Warnings, "index points to missing "+relPath(m.Rel, target))
		}
	}
	unindexed := 0
	for _, name := range files {
		if !referenced[name] {
			unindexed++
		}
	}
	if unindexed > 0 {
		res.Verbose = append(res.Verbose, fmt.Sprintf("%s: %d facts not in the index", m.Rel, unindexed))
	}
}

// pointerTargets extracts the bare `<file>.md` link targets of an index.
func pointerTargets(index string) []string {
	var out []string
	for _, m := range pointerRe.FindAllStringSubmatch(index, -1) {
		target := m[1]
		if strings.HasSuffix(target, ".md") && !strings.Contains(target, "/") && !strings.Contains(target, "\\") {
			out = append(out, target)
		}
	}
	return out
}

// relPath joins a memory dir Rel and a file name for messages.
func relPath(rel, name string) string {
	if rel == "." {
		return name
	}
	return rel + "/" + name
}
