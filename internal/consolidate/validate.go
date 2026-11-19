package consolidate

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/avbel/claude-code-memory-takeout/internal/render"
)

// Input describes one section part for the validator.
type Input struct {
	Section string
	Names   []string            // input fact names, sorted, duplicates collapsed
	HasDesc map[string]bool     // which inputs carry a > description line
	Markers map[string][]string // distinct [REDACTED...] texts per name
	Bytes   map[string]int      // rendered input bytes per name, duplicates summed
	Links   map[string]bool     // every markdown link target of the part's input
}

// Entry is one line of the trailing consolidation block.
type Entry struct {
	Name   string
	Merge  string // target name; empty for drops
	Reason string // drop reason
}

var (
	markerRe     = regexp.MustCompile(`\[REDACTED(:[a-z_]+)?\]`)
	linkTargetRe = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	mergeRe      = regexp.MustCompile(`^-\s*(.+?)\s*(?:->|→)\s*merged into\s*(.+?)\s*$`)
	dropRe       = regexp.MustCompile(`^-\s*(.+?)\s*(?:->|→)\s*dropped:\s*(.*?)\s*$`)
	modifiedRe   = regexp.MustCompile(`^<!-- modified: .*-->$`)

	blockOpen  = "<!-- consolidation"
	blockClose = "-->"
)

// Validate checks one answer against its input and returns the answer
// with the trailing consolidation block removed plus the parsed entries.
// errs lists every rule violation; the caller retries once and then falls
// back to the verbatim part.
func Validate(answer string, in Input) (string, []Entry, []string) {
	answer = strings.TrimSpace(stripWholeFence(answer))
	clean, entries, errs := splitConsolidationBlock(answer)

	nameSet := make(map[string]bool, len(in.Names))
	for _, n := range in.Names {
		nameSet[n] = true
	}
	seen := map[string]bool{}
	var outNames []string
	lines := strings.Split(clean, "\n")
	fence := render.NewFence()
	for i, line := range lines {
		if fence.Step(line) {
			continue
		}
		if i == 0 {
			if line != "## "+in.Section {
				errs = append(errs, fmt.Sprintf("first line must be exactly `## %s`, got %q", in.Section, line))
			}
			continue
		}
		if strings.HasPrefix(line, "## ") {
			errs = append(errs, fmt.Sprintf("line %d starts with `## ` outside the section heading: %q", i+1, line))
			continue
		}
		// A model steered by injected memory content could otherwise
		// smuggle a link to an external target that never existed in the
		// input into the committed document.
		for _, m := range linkTargetRe.FindAllStringSubmatch(line, -1) {
			if target := m[1]; !strings.HasPrefix(target, "#") && !in.Links[target] {
				errs = append(errs, fmt.Sprintf("link to a target the input did not have: %s", target))
			}
		}
		if !strings.HasPrefix(line, "### ") {
			continue
		}
		name := strings.TrimSpace(line[len("### "):])
		switch {
		case !nameSet[name]:
			errs = append(errs, fmt.Sprintf("`%s` is not a name from the input", name))
		case seen[name]:
			errs = append(errs, fmt.Sprintf("`%s` appears more than once", name))
		default:
			seen[name] = true
			outNames = append(outNames, name)
			if in.HasDesc[name] && !hasDescriptionLine(lines, i+1) {
				errs = append(errs, fmt.Sprintf("`%s` lost its description line", name))
			}
		}
	}
	errs = append(errs, checkEntries(entries, nameSet, seen)...)
	errs = append(errs, checkCoverage(in.Names, seen, entries)...)
	errs = append(errs, checkMarkers(clean, in, outNames, entries)...)
	errs = append(errs, checkSizeFloor(clean, in, outNames, entries)...)
	return clean, entries, errs
}

// hasDescriptionLine reports whether the first non-blank line after the
// heading at index i (ignoring modified hints) is a `> ` description.
func hasDescriptionLine(lines []string, from int) bool {
	for j := from; j < len(lines); j++ {
		if lines[j] == "" || modifiedRe.MatchString(lines[j]) {
			continue
		}
		return strings.HasPrefix(lines[j], "> ")
	}
	return false
}

// stripWholeFence removes one enclosing ``` fence around the whole
// answer, once, when the model wrapped its output.
func stripWholeFence(answer string) string {
	lines := strings.Split(answer, "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "```") {
		return answer
	}
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "```" {
		return answer
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}

// splitConsolidationBlock separates a trailing `<!-- consolidation ... -->`
// block from the answer and parses its entries. A block marker inside a
// fenced code block — a fact that documents this very format — is not the
// trailing block, so only markers outside fences count, the last one wins.
func splitConsolidationBlock(answer string) (clean string, entries []Entry, errs []string) {
	lines := strings.Split(answer, "\n")
	fence := render.NewFence()
	idx := -1
	for i, line := range lines {
		if fence.Step(line) {
			continue
		}
		if strings.TrimSpace(line) == blockOpen {
			idx = i
		}
	}
	if idx < 0 {
		return answer, nil, nil
	}
	end := slices.IndexFunc(lines[idx+1:], func(l string) bool {
		return strings.TrimSpace(l) == blockClose
	})
	if end < 0 {
		return answer, nil, []string{"consolidation block has no closing -->"}
	}
	end += idx + 1
	if strings.TrimSpace(strings.Join(lines[end+1:], "\n")) != "" {
		return answer, nil, []string{"consolidation block is not at the end"}
	}
	blockLines := slices.DeleteFunc(slices.Clone(lines[idx+1:end]),
		func(l string) bool { return strings.TrimSpace(l) == "" })
	for _, l := range blockLines {
		if m := mergeRe.FindStringSubmatch(l); m != nil {
			entries = append(entries, Entry{Name: m[1], Merge: m[2]})
			continue
		}
		if m := dropRe.FindStringSubmatch(l); m != nil {
			entries = append(entries, Entry{Name: m[1], Reason: m[2]})
			continue
		}
		errs = append(errs, fmt.Sprintf("consolidation block line is not an entry: %q", l))
	}
	clean = strings.TrimSpace(strings.Join(lines[:idx], "\n"))
	return clean, entries, errs
}

// checkEntries validates each entry's shape against the output.
func checkEntries(entries []Entry, nameSet, outSet map[string]bool) []string {
	var errs []string
	for _, e := range entries {
		if !nameSet[e.Name] {
			errs = append(errs, fmt.Sprintf("consolidation block names `%s`, which is not an input name", e.Name))
		}
		if e.Merge == "" {
			if strings.TrimSpace(e.Reason) == "" {
				errs = append(errs, fmt.Sprintf("`%s` was dropped without a reason", e.Name))
			}
			continue
		}
		if !outSet[e.Merge] {
			errs = append(errs, fmt.Sprintf("`%s` was merged into `%s`, which is not in the output", e.Name, e.Merge))
		}
	}
	return errs
}

// checkCoverage requires the block to list exactly the input names that
// are absent from the output.
func checkCoverage(names []string, outSet map[string]bool, entries []Entry) []string {
	inBlock := make(map[string]bool, len(entries))
	for _, e := range entries {
		inBlock[e.Name] = true
	}
	var errs []string
	for _, n := range names {
		switch {
		case !outSet[n] && !inBlock[n]:
			errs = append(errs, fmt.Sprintf("`%s` is absent from the output and not in the consolidation block", n))
		case outSet[n] && inBlock[n]:
			errs = append(errs, fmt.Sprintf("`%s` is in the output and also in the consolidation block", n))
		}
	}
	return errs
}

// checkMarkers requires every marker of a kept or merge-target input fact
// to survive, and forbids markers the input never had.
func checkMarkers(answer string, in Input, outNames []string, entries []Entry) []string {
	answerMarkers := make(map[string]bool)
	for _, m := range markerRe.FindAllString(answer, -1) {
		answerMarkers[m] = true
	}
	inputUnion := make(map[string]bool)
	present := make(map[string]bool, len(outNames))
	for _, n := range outNames {
		present[n] = true
	}
	for _, e := range entries {
		if e.Merge != "" {
			present[e.Merge] = true
		}
	}
	for _, ms := range in.Markers {
		for _, m := range ms {
			inputUnion[m] = true
		}
	}
	var errs []string
	for _, m := range slices.Sorted(maps.Keys(answerMarkers)) {
		if !inputUnion[m] {
			errs = append(errs, fmt.Sprintf("answer contains a marker the input did not have: %s", m))
		}
	}
	for _, n := range slices.Sorted(maps.Keys(present)) {
		for _, m := range in.Markers[n] {
			if !answerMarkers[m] {
				errs = append(errs, fmt.Sprintf("%s is missing from the answer", m))
			}
		}
	}
	return errs
}

// checkSizeFloor rejects answers that lost more than half the bytes of
// what they should still carry: the facts they keep, plus the merged-away
// sources — a merge must carry the union of details, so losing a source's
// content wholesale fails the floor. Summarizing instead of consolidating
// fails here too.
func checkSizeFloor(clean string, in Input, outNames []string, entries []Entry) []string {
	present := make(map[string]bool, len(outNames))
	for _, n := range outNames {
		present[n] = true
	}
	for _, e := range entries {
		if e.Merge != "" {
			present[e.Merge] = true
			present[e.Name] = true // merged-away bytes must survive too
		}
	}
	keptBytes := 0
	for n := range present {
		keptBytes += in.Bytes[n]
	}
	var errs []string
	if len(clean)*2 < keptBytes {
		errs = append(errs, fmt.Sprintf("answer is %d bytes, less than half of the facts it should carry (%d) — details look lost", len(clean), keptBytes))
	}
	return errs
}
