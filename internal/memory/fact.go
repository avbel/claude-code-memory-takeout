// Package memory parses Claude Code auto-memory fact files: one Markdown
// file with YAML frontmatter per fact, directly inside a project's
// memory/ dir.
package memory

import (
	"cmp"
	"time"
)

// Fact is one memory fact parsed from a memory/*.md file.
type Fact struct {
	Name        string
	Description string
	Type        string // lower-cased; unknown or empty types render under "Other"
	Body        string
	File        string    // base name of the source file
	Rel         string    // the locate.Match.Rel of its memory dir
	Modified    time.Time // zero when the file has no parsable date; an LLM hint only
}

// ParseResult is one memory file's parse outcome: either a Fact, or a
// warning about why the file was skipped. The index file MEMORY.md yields
// no fact and no warning.
type ParseResult struct {
	Fact    Fact
	OK      bool
	Warning string
}

// Less orders facts inside a section: by Name, then Rel, then File.
func Less(a, b Fact) int {
	if c := cmp.Compare(a.Name, b.Name); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Rel, b.Rel); c != 0 {
		return c
	}
	return cmp.Compare(a.File, b.File)
}
