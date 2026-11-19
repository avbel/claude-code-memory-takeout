package render

import (
	"regexp"
	"strings"
)

// LinkResolver maps a link target to the anchor of a fact; ok is false
// when no fact matches, and the link collapses to plain text or stays as
// it was.
type LinkResolver func(target string) (string, bool)

var (
	wikiRe    = regexp.MustCompile(`\[\[([^|\]]+)(?:\|([^\]]*))?\]\]`)
	mdLinkRe  = regexp.MustCompile(`(!?)\[([^\]]*)\]\(([^)]+)\)`)
	fenceChar = byte('`')
)

// RewriteLinks rewrites the links of one fact body outside fenced code
// blocks and inline code: [[wiki]] links to known facts become
// [label](#anchor), markdown links to a bare <file>.md of a loaded fact
// become (#anchor). Everything else is left untouched.
func RewriteLinks(body string, wiki, file LinkResolver) string {
	lines := strings.Split(body, "\n")
	fence := NewFence()
	for i, line := range lines {
		if fence.Step(line) {
			continue
		}
		segs := splitInline(line)
		for k := 0; k < len(segs); k += 2 {
			segs[k] = rewriteSegment(segs[k], wiki, file)
		}
		lines[i] = strings.Join(segs, "")
	}
	return strings.Join(lines, "\n")
}

// RetargetLinks rewrites `(#target)` links outside fenced code blocks and
// inline code: a target that was merged into another fact points at the
// anchor fn returns for the merge target's name, a dropped target
// collapses to the plain label, everything else stays as it was.
func RetargetLinks(body string, merged map[string]string, dropped map[string]bool, anchor func(target string) string) string {
	lines := strings.Split(body, "\n")
	fence := NewFence()
	for i, line := range lines {
		if fence.Step(line) {
			continue
		}
		segs := splitInline(line)
		for k := 0; k < len(segs); k += 2 {
			segs[k] = retargetSegment(segs[k], merged, dropped, anchor)
		}
		lines[i] = strings.Join(segs, "")
	}
	return strings.Join(lines, "\n")
}

func retargetSegment(seg string, merged map[string]string, dropped map[string]bool, anchor func(string) string) string {
	return anchorRe.ReplaceAllStringFunc(seg, func(m string) string {
		sub := anchorRe.FindStringSubmatch(m)
		target := sub[2]
		if dropped[target] {
			return sub[1]
		}
		if name, ok := merged[target]; ok {
			return "[" + sub[1] + "](#" + anchor(name) + ")"
		}
		return m
	})
}

var anchorRe = regexp.MustCompile(`\[([^\]]*)\]\(#([^)\s]+)\)`)

func rewriteSegment(seg string, wiki, file LinkResolver) string {
	seg = wikiRe.ReplaceAllStringFunc(seg, func(m string) string {
		sub := wikiRe.FindStringSubmatch(m)
		target := strings.TrimSpace(sub[1])
		label := target
		if sub[2] != "" {
			label = strings.TrimSpace(sub[2])
		}
		anchor, ok := wiki(target)
		if !ok {
			return label
		}
		return "[" + label + "](#" + anchor + ")"
	})
	return mdLinkRe.ReplaceAllStringFunc(seg, func(m string) string {
		sub := mdLinkRe.FindStringSubmatch(m)
		if sub[1] == "!" || !isBareFile(sub[3]) {
			return m
		}
		anchor, ok := file(sub[3])
		if !ok {
			return m
		}
		return "[" + sub[2] + "](#" + anchor + ")"
	})
}

// isBareFile reports whether a link target is a bare `<file>.md` name, the
// form the memory index uses inside one memory dir.
func isBareFile(target string) bool {
	return strings.HasSuffix(target, ".md") &&
		!strings.Contains(target, "/") && !strings.Contains(target, "\\")
}

// splitInline splits one line into segments; odd indices are inline code
// between single backticks and are never rewritten.
func splitInline(line string) []string {
	var segs []string
	for {
		i := strings.IndexByte(line, fenceChar)
		if i < 0 {
			return append(segs, line)
		}
		if j := strings.IndexByte(line[i+1:], fenceChar); j < 0 {
			return append(segs, line)
		} else {
			segs = append(segs, line[:i], line[:i+2+j][i:])
			line = line[i+2+j:]
		}
	}
}
