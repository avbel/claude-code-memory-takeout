package render

import (
	"maps"
	"strings"

	"github.com/avbel/claude-code-memory-takeout/internal/memory"
)

// Headings returns the heading text of every fact, in the order given:
// the Name, disambiguated with " (<Rel>)" when the same Name occurs more
// than once — except for the project's own "." facts — and with " (<File>)"
// when two facts would still share a heading.
func Headings(facts []memory.Fact) []string {
	counts := make(map[string]int, len(facts))
	for _, f := range facts {
		counts[f.Name]++
	}
	out := make([]string, len(facts))
	for i, f := range facts {
		if counts[f.Name] > 1 && f.Rel != "." {
			out[i] = f.Name + " (" + f.Rel + ")"
			continue
		}
		out[i] = f.Name
	}
	byHeading := make(map[string][]int, len(facts))
	for i, h := range out {
		byHeading[h] = append(byHeading[h], i)
	}
	for idx := range maps.Values(byHeading) {
		if len(idx) > 1 {
			for _, i := range idx {
				out[i] += " (" + facts[i].File + ")"
			}
		}
	}
	return out
}

// Demote shifts every heading of a body outside fenced code blocks so
// that the shallowest becomes #### (capped at ######). Body headings would
// otherwise break the document structure; fenced ones keep their level.
func Demote(body string) string {
	lines := strings.Split(body, "\n")
	fence := NewFence()
	inFence := make([]bool, len(lines))
	minLevel := 0
	for i, line := range lines {
		inFence[i] = fence.Step(line)
		if inFence[i] {
			continue
		}
		if n := headingLevel(line); n > 0 && (minLevel == 0 || n < minLevel) {
			minLevel = n
		}
	}
	if minLevel == 0 {
		return body
	}
	shift := 4 - minLevel
	for i, line := range lines {
		if inFence[i] {
			continue
		}
		if n := headingLevel(line); n > 0 {
			lvl := min(6, max(1, n+shift))
			lines[i] = strings.Repeat("#", lvl) + line[n:]
		}
	}
	return strings.Join(lines, "\n")
}

// BodyHeadings returns the text of every heading line outside fenced code
// blocks, in order; the document anchor numbering walks them.
func BodyHeadings(body string) []string {
	var out []string
	fence := NewFence()
	for line := range strings.SplitSeq(body, "\n") {
		if fence.Step(line) {
			continue
		}
		if n := headingLevel(line); n > 0 {
			out = append(out, strings.TrimSpace(line[n:]))
		}
	}
	return out
}

// Fence tracks fenced code blocks (``` or ~~~) while scanning lines.
type Fence struct {
	open bool
	mark byte // '`' or '~' of the open fence
}

// NewFence returns a tracker for one document walk.
func NewFence() *Fence {
	return &Fence{}
}

// Step advances the fence state by one line and reports whether the line
// is inside a fence; the delimiter lines themselves count as inside.
func (f *Fence) Step(line string) bool {
	if f.open {
		if fenceMark(line) == f.mark {
			f.open = false
		}
		return true
	}
	if m := fenceMark(line); m != 0 {
		f.mark = m
		f.open = true
		return true
	}
	return false
}

// fenceMark reports the marker of a column-0 ``` or ~~~ line, 0 otherwise.
func fenceMark(line string) byte {
	switch {
	case strings.HasPrefix(line, "```"):
		return '`'
	case strings.HasPrefix(line, "~~~"):
		return '~'
	default:
		return 0
	}
}

// headingLevel returns the ATX heading level of a column-0 line: 0 when
// the line is not a heading. "#x" without a space is not a heading, and
// neither are seven or more hashes.
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return 0
	}
	if n == len(line) || line[n] == ' ' {
		return n
	}
	return 0
}
