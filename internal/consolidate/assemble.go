package consolidate

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/avbel/claude-code-memory-takeout/internal/redact"
	"github.com/avbel/claude-code-memory-takeout/internal/render"
)

// modifiedLineRe strips the LLM-only hint lines from every block that
// reaches the final document.
var modifiedLineRe = regexp.MustCompile(`(?m)^<!-- modified: .*-->\n?`)

// assemble post-processes the consolidated sections and renders the final
// document: redact again, demote bodies, assign anchors over the whole
// document order, rewrite links to merged and dropped names, and append
// the consolidation log when something was merged or dropped.
func assemble(project string, opts Options, sources []render.Source, sections []section, log []LogEntry, kept int) string {
	merged := make(map[string]string)
	dropped := make(map[string]bool)
	for _, e := range log {
		if e.Merge != "" {
			merged[e.Name] = e.Merge
			// Links inside bodies carry the anchor form of a name, so the
			// maps answer both spellings.
			merged[render.Anchor(e.Name)] = e.Merge
		} else {
			dropped[e.Name] = true
			dropped[render.Anchor(e.Name)] = true
		}
	}
	for si := range sections {
		for bi := range sections[si].blocks {
			sections[si].blocks[bi] = cleanBlock(sections[si].blocks[bi], opts.Redactor)
		}
	}

	// Anchor assignment walks the final document order; a repeated fact
	// name consumes its number but a link keeps pointing at the first
	// occurrence's anchor, the way GitHub's #name convention resolves.
	anchors := render.NewAnchorer()
	anchors.Next("Memory: " + project)
	factAnchor := make(map[string]string)
	for _, sec := range sections {
		anchors.Next(sec.name)
		for _, b := range sec.blocks {
			name := blockName(b)
			if _, ok := factAnchor[name]; !ok {
				factAnchor[name] = anchors.Next(name)
			} else {
				anchors.Next(name)
			}
			for _, h := range render.BodyHeadings(blockBody(b)) {
				anchors.Next(h)
			}
		}
	}
	anchor := func(target string) string {
		return factAnchor[target]
	}

	blocks := []string{render.Header(project, "llm ("+opts.Model+")", sources, kept, opts.Redacted)}
	for _, sec := range sections {
		rewritten := make([]string, len(sec.blocks))
		for i, b := range sec.blocks {
			heading, desc, body := splitBlock(b)
			rewritten[i] = joinBlock(heading, desc, render.RetargetLinks(body, merged, dropped, anchor))
		}
		blocks = append(blocks, "## "+sec.name+"\n"+strings.Join(rewritten, "\n\n"))
	}
	if logBlock := consolidationLog(log, factAnchor); logBlock != "" {
		blocks = append(blocks, logBlock)
	}
	return render.Join(blocks)
}

// consolidationLog renders the log section, sorted by section order then
// name; empty when nothing was merged or dropped.
func consolidationLog(log []LogEntry, factAnchor map[string]string) string {
	if len(log) == 0 {
		return ""
	}
	rank := make(map[string]int)
	for i, name := range render.SectionHeadings() {
		rank[name] = i
	}
	sorted := slices.Clone(log)
	slices.SortStableFunc(sorted, func(a, b LogEntry) int {
		if c := cmp.Compare(rank[a.Section], rank[b.Section]); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	lines := make([]string, 0, len(sorted))
	for _, e := range sorted {
		if e.Merge != "" {
			lines = append(lines, fmt.Sprintf("- %s → merged into [%s](#%s)", e.Name, e.Merge, factAnchor[e.Merge]))
		} else {
			lines = append(lines, fmt.Sprintf("- %s → dropped: %s", e.Name, e.Reason))
		}
	}
	return "## Consolidation log\n" + strings.Join(lines, "\n")
}

// cleanBlock strips the modified hints, redacts again — the model may
// have written a fresh secret — and re-demotes the body headings. The
// redaction runs with existing markers shielded: the assignment rule
// would otherwise eat a marker as an unquoted value and shorten the text
// around it.
func cleanBlock(b string, rd *redact.Redactor) string {
	b = modifiedLineRe.ReplaceAllString(b, "")
	if rd != nil {
		var markers []string
		b = markerRe.ReplaceAllStringFunc(b, func(m string) string {
			markers = append(markers, m)
			return fmt.Sprintf("\ue000%d\ue001", len(markers)-1)
		})
		b = rd.Apply(b).Text
		for i, m := range markers {
			b = strings.ReplaceAll(b, fmt.Sprintf("\ue000%d\ue001", i), m)
		}
	}
	heading, desc, body := splitBlock(b)
	return joinBlock(heading, desc, render.Demote(body))
}

// splitBlock returns a block's heading line, its description line (""
// when absent) and the body text.
func splitBlock(b string) (heading, desc, body string) {
	heading, rest, _ := strings.Cut(b, "\n")
	if d, r, _ := strings.Cut(rest, "\n"); strings.HasPrefix(d, "> ") {
		return heading, d, r
	}
	return heading, "", rest
}

// joinBlock reassembles a block with exactly one blank line between its
// parts and no trailing newline.
func joinBlock(heading, desc, body string) string {
	body = strings.TrimLeft(body, "\n")
	switch {
	case desc != "" && body != "":
		return heading + "\n" + desc + "\n\n" + body
	case desc != "":
		return heading + "\n" + desc
	case body != "":
		return heading + "\n" + body
	default:
		return heading
	}
}

// blockName returns the name of a `### name` block.
func blockName(b string) string {
	line, _, _ := strings.Cut(b, "\n")
	return strings.TrimSpace(strings.TrimPrefix(line, "### "))
}

// blockBody returns a block's body (everything after the heading and the
// optional description line).
func blockBody(b string) string {
	_, _, body := splitBlock(b)
	return strings.TrimLeft(body, "\n")
}
