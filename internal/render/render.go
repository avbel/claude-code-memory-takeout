package render

import (
	"fmt"
	"slices"
	"strings"

	"github.com/avbel/claude-code-memory-takeout/internal/memory"
)

// sectionHeadings is the fixed section order; Other collects unknown or
// empty types. Empty sections are omitted.
var sectionHeadings = []string{"User", "Feedback", "Project", "Reference", "Other"}

var sectionIndex = map[string]int{"user": 0, "feedback": 1, "project": 2, "reference": 3}

// SectionOf maps a fact type to its section index; unknown or empty types
// land in Other.
func SectionOf(kind string) int {
	if i, ok := sectionIndex[kind]; ok {
		return i
	}
	return len(sectionIndex)
}

// SectionHeadings returns the section headings in document order.
func SectionHeadings() []string {
	return slices.Clone(sectionHeadings)
}

// Source is one memory dir on the Sources header line.
type Source struct {
	Rel   string
	Facts int
}

// Doc is one generated document. Mode is "verbatim" or "llm (<model>)".
type Doc struct {
	Project  string
	Mode     string
	Sources  []Source
	Facts    []memory.Fact
	Redacted int
}

// Document renders the whole MEMORY.md in verbatim mode: the marker and
// header, the sections in fixed order with their facts sorted by Name,
// Rel, File — no dates, no version, byte-identical across re-runs.
func Document(doc Doc) string {
	// The caller keeps its fact slice; sorting a clone keeps Document a
	// pure function.
	facts := slices.Clone(doc.Facts)
	slices.SortStableFunc(facts, memory.Less)
	doc.Facts = facts
	sections := groupSections(doc.Facts)
	headings := Headings(doc.Facts)
	bodies := make([]string, len(doc.Facts))
	for i, f := range doc.Facts {
		bodies[i] = Demote(f.Body)
	}
	anchors := assignAnchors(doc.Project, sections, headings, bodies)
	for i := range bodies {
		bodies[i] = RewriteLinks(bodies[i],
			nameResolver(doc.Facts, anchors, i),
			fileResolver(doc.Facts, anchors, i))
	}
	blocks := []string{Header(doc.Project, doc.Mode, doc.Sources, len(doc.Facts), doc.Redacted)}
	for _, sec := range sections {
		if len(sec.facts) > 0 {
			blocks = append(blocks, sectionBlock(sec, doc.Facts, headings, bodies))
		}
	}
	return Join(blocks)
}

// Header renders the document header block: the marker line, the title,
// and the Sources and Facts lines. It has no trailing newline; Join puts
// one blank line between blocks.
func Header(project, mode string, sources []Source, facts, redacted int) string {
	parts := make([]string, 0, len(sources))
	for _, s := range sources {
		parts = append(parts, fmt.Sprintf("`%s` (%d)", s.Rel, s.Facts))
	}
	return Marker + "\n" +
		"# Memory: " + project + "\n\n" +
		"Sources: " + strings.Join(parts, ", ") + "\n" +
		fmt.Sprintf("Facts: %d · redacted values: %d · mode: %s", facts, redacted, mode)
}

// Join assembles the document from its blocks with exactly one blank line
// between them; the file ends with one newline.
func Join(blocks []string) string {
	return strings.Join(blocks, "\n\n") + "\n"
}

// Block renders one fact — the ### heading, the optional > description
// line, and the demoted, link-rewritten body — for callers that assemble
// section text themselves (--llm input rendering).
func Block(f memory.Fact, heading string, wiki, file LinkResolver) string {
	return blockText(heading, f.Description, RewriteLinks(Demote(f.Body), wiki, file))
}

func blockText(heading, description, body string) string {
	var b strings.Builder
	b.WriteString("### " + heading + "\n")
	if description != "" {
		b.WriteString("> " + collapse(description) + "\n")
	}
	if body != "" {
		b.WriteString("\n" + body)
	}
	return b.String()
}

// collapse folds every whitespace run of a description, newlines
// included, into one space so it fits on one `> ` line.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

type section struct {
	name  string
	facts []int // indices into the fact slice
}

func groupSections(facts []memory.Fact) []section {
	out := make([]section, len(sectionHeadings))
	for i, name := range sectionHeadings {
		out[i] = section{name: name}
	}
	for i, f := range facts {
		out[SectionOf(f.Type)].facts = append(out[SectionOf(f.Type)].facts, i)
	}
	return out
}

func sectionBlock(sec section, facts []memory.Fact, headings, bodies []string) string {
	blocks := make([]string, len(sec.facts))
	for k, i := range sec.facts {
		blocks[k] = blockText(headings[i], facts[i].Description, bodies[i])
	}
	return "## " + sec.name + "\n" + strings.Join(blocks, "\n\n")
}

// assignAnchors walks the final document order — the title, every emitted
// section heading, fact headings and their demoted body headings — through
// one Anchorer and returns each fact's assigned anchor.
func assignAnchors(project string, sections []section, headings, bodies []string) []string {
	a := NewAnchorer()
	a.Next("Memory: " + project)
	out := make([]string, len(headings))
	for _, sec := range sections {
		if len(sec.facts) == 0 {
			continue
		}
		a.Next(sec.name)
		for _, i := range sec.facts {
			out[i] = a.Next(headings[i])
			for _, h := range BodyHeadings(bodies[i]) {
				a.Next(h)
			}
		}
	}
	return out
}

// nameResolver resolves a [[wiki]] target as seen from the fact at index
// from: prefer facts in the same memory dir, then the project's own ".",
// then the first by sort order.
func nameResolver(facts []memory.Fact, anchors []string, from int) LinkResolver {
	return func(target string) (string, bool) {
		for j, f := range facts {
			if f.Name == target && f.Rel == facts[from].Rel {
				return anchors[j], true
			}
		}
		for j, f := range facts {
			if f.Name == target && f.Rel == "." {
				return anchors[j], true
			}
		}
		for j, f := range facts {
			if f.Name == target {
				return anchors[j], true
			}
		}
		return "", false
	}
}

// fileResolver resolves a bare <file>.md link to a fact loaded from the
// same memory dir as the fact at index from.
func fileResolver(facts []memory.Fact, anchors []string, from int) LinkResolver {
	return func(name string) (string, bool) {
		for j, f := range facts {
			if f.File == name && f.Rel == facts[from].Rel {
				return anchors[j], true
			}
		}
		return "", false
	}
}
