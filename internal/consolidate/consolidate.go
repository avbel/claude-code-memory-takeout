package consolidate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/avbel/claude-code-memory-takeout/internal/llm"
	"github.com/avbel/claude-code-memory-takeout/internal/memory"
	"github.com/avbel/claude-code-memory-takeout/internal/redact"
	"github.com/avbel/claude-code-memory-takeout/internal/render"
)

// ErrLLM wraps every failure of the model call itself so the CLI maps it
// to exit code 4; validation fallbacks are warnings, never this error.
var ErrLLM = errors.New("llm step failed")

// Caller sends one multi-message chat completion; *llm.Client satisfies
// it, tests supply a fake.
type Caller interface {
	Chat(ctx context.Context, msgs []llm.Message) (string, llm.Usage, error)
}

// LogEntry is one line of the document's consolidation log.
type LogEntry struct {
	Section string
	Name    string
	Merge   string // target name; empty for drops
	Reason  string // drop reason
}

// Options tunes one Run; nil funcs drop the message.
type Options struct {
	Model           string
	MaxPartTokens   int
	MaxOutputTokens int
	Redacted        int // input redaction total, for the header
	Redactor        *redact.Redactor
	Warn            func(string)
	Info            func(string)
	Verbose         func(string)
}

// Result reports what consolidation did across all sections.
type Result struct {
	Kept    int
	Merged  int
	Dropped int
	Log     []LogEntry
}

// section carries one section's consolidated fact blocks.
type section struct {
	name   string
	blocks []string
}

// partOutcome is what one part contributed to the assembled document.
type partOutcome struct {
	blocks  []string
	entries []Entry
	kept    int
	merged  int
	dropped int
}

// Run consolidates the redacted facts through the LLM, section by section
// and part by part, and returns the assembled --llm MEMORY.md text.
func Run(ctx context.Context, facts []memory.Fact, project string, sources []render.Source, caller Caller, opts Options) (string, Result, error) {
	opts.Warn = orNoop(opts.Warn)
	opts.Info = orNoop(opts.Info)
	opts.Verbose = orNoop(opts.Verbose)

	budget := opts.MaxPartTokens
	if opts.MaxOutputTokens > 0 {
		// The answer is roughly as long as the input, so keep room for it.
		budget = min(budget, 4*opts.MaxOutputTokens/5)
	} else {
		opts.Warn(fmt.Sprintf("--max-output-tokens not set; parts of up to %d tokens may be truncated by the server", budget))
	}

	facts = slices.Clone(facts)
	slices.SortStableFunc(facts, memory.Less)

	var res Result
	var sections []section
	heads := render.SectionHeadings()
	for si := range heads {
		secFacts := factsOfType(facts, si)
		if len(secFacts) == 0 {
			continue
		}
		entries, blocks, stats, err := consolidateSection(ctx, caller, heads[si], secFacts, facts, budget, opts)
		if err != nil {
			return "", Result{}, err
		}
		sections = append(sections, section{name: heads[si], blocks: blocks})
		for _, e := range entries {
			res.Log = append(res.Log, LogEntry{Section: heads[si], Name: e.Name, Merge: e.Merge, Reason: e.Reason})
		}
		res.Kept += stats.kept
		res.Merged += stats.merged
		res.Dropped += stats.dropped
	}
	doc := assemble(project, opts, sources, sections, res.Log, res.Kept)
	return doc, res, nil
}

// consolidateSection runs every part of one section and returns its log
// entries, consolidated blocks and kept/merged/dropped counts.
func consolidateSection(ctx context.Context, caller Caller, name string, secFacts, allFacts []memory.Fact, budget int, opts Options) ([]Entry, []string, partStats, error) {
	blocks := make([]string, len(secFacts))
	for i, f := range secFacts {
		blocks[i] = withModified(inputBlock(f, allFacts), f.Modified)
	}
	groups := Split(blocks, EstimateTokens("## "+name+"\n")+1, budget)
	var entries []Entry
	var out []string
	var stats partStats
	for gi, group := range groups {
		in := buildInput(name, secFacts, blocks, group)
		text := partText(name, blocks, group)
		outcome, err := consolidatePart(ctx, caller, name, gi+1, len(groups), text, in, opts)
		if err != nil {
			return nil, nil, stats, err
		}
		out = append(out, outcome.blocks...)
		for i := range outcome.entries {
			// The reason is model free text that lands in the document's
			// log, so it gets the same second redaction pass as bodies.
			if opts.Redactor != nil {
				outcome.entries[i].Reason = opts.Redactor.Apply(outcome.entries[i].Reason).Text
			}
		}
		entries = append(entries, outcome.entries...)
		stats.kept += outcome.kept
		stats.merged += outcome.merged
		stats.dropped += outcome.dropped
	}
	return entries, out, stats, nil
}

// consolidatePart validates one answer with one retry, then falls back to
// the verbatim part input. part is the 1-based index of total; the prompts
// spell it "i of n", the stderr lines "i/n".
func consolidatePart(ctx context.Context, caller Caller, name string, part, total int, text string, in Input, opts Options) (partOutcome, error) {
	stderrPart := fmt.Sprintf("%d/%d", part, total)
	user := UserMessage(name, partLabel(part, total), text)
	answer, _, err := caller.Chat(ctx, []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: user},
	})
	if err != nil {
		return fallbackOrError(text, name, stderrPart, in, err, opts)
	}
	// A whitespace-only answer cannot be echoed back as the rejected
	// assistant message the retry needs, so it goes straight to the
	// verbatim fallback: a warning, never an exit-4 failure.
	if strings.TrimSpace(answer) == "" {
		opts.Warn(fmt.Sprintf("llm output for %s part %s failed (empty answer); kept verbatim", name, stderrPart))
		return verbatimOutcome(text, in), nil
	}
	clean, entries, errs := Validate(answer, in)
	if len(errs) > 0 {
		opts.Verbose(fmt.Sprintf("%s part %s rejected: %s", name, stderrPart, strings.Join(errs, "; ")))
		answer, _, err = caller.Chat(ctx, []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: user},
			{Role: "assistant", Content: answer},
			{Role: "user", Content: RetryMessage(name, errs)},
		})
		if err != nil {
			return fallbackOrError(text, name, stderrPart, in, err, opts)
		}
		clean, entries, errs = Validate(answer, in)
		if len(errs) > 0 {
			opts.Warn(fmt.Sprintf("llm output for %s part %s failed (%s); kept verbatim", name, stderrPart, errs[0]))
			return verbatimOutcome(text, in), nil
		}
	}
	merged := countMerges(entries)
	blocks := answerBlocks(clean)
	opts.Info(fmt.Sprintf("llm: %s part %s … kept %d, merged %d, dropped %d",
		name, stderrPart, len(blocks), merged, len(entries)-merged))
	return partOutcome{
		blocks:  blocks,
		entries: entries,
		kept:    len(blocks),
		merged:  merged,
		dropped: len(entries) - merged,
	}, nil
}

// fallbackOrError maps a caller failure: a truncated completion falls
// back to the verbatim part, every other client error is exit code 4.
func fallbackOrError(text, name, label string, in Input, err error, opts Options) (partOutcome, error) {
	if errors.Is(err, llm.ErrTruncated) {
		opts.Warn(fmt.Sprintf("llm output for %s part %s failed (%s); kept verbatim", name, label, err))
		return verbatimOutcome(text, in), nil
	}
	return partOutcome{}, fmt.Errorf("%w: %w", ErrLLM, err)
}

// verbatimOutcome keeps a part's input blocks as the output; every block
// survives, so kept counts blocks, not unique names.
func verbatimOutcome(text string, in Input) partOutcome {
	return partOutcome{blocks: answerBlocks(text), kept: len(answerBlocks(text))}
}

type partStats struct {
	kept    int
	merged  int
	dropped int
}

func countMerges(entries []Entry) int {
	n := 0
	for _, e := range entries {
		if e.Merge != "" {
			n++
		}
	}
	return n
}

func orNoop(f func(string)) func(string) {
	if f == nil {
		return func(string) {}
	}
	return f
}

// factsOfType returns the facts of one section index, already sorted.
func factsOfType(facts []memory.Fact, section int) []memory.Fact {
	var out []memory.Fact
	for _, f := range facts {
		if render.SectionOf(f.Type) == section {
			out = append(out, f)
		}
	}
	return out
}

// inputBlock renders one fact of the LLM input with its plain name as the
// heading; links resolve to the plain anchor of the target's name.
func inputBlock(f memory.Fact, all []memory.Fact) string {
	wiki := func(target string) (string, bool) {
		for _, g := range all {
			if g.Name == target {
				return render.Anchor(g.Name), true
			}
		}
		return "", false
	}
	file := func(name string) (string, bool) {
		for _, g := range all {
			if g.File == name && g.Rel == f.Rel {
				return render.Anchor(g.Name), true
			}
		}
		return "", false
	}
	return render.Block(f, f.Name, wiki, file)
}

// withModified splices the LLM-only modified hint right under the
// heading; post-processing removes every hint line from the final text.
func withModified(block string, m time.Time) string {
	if m.IsZero() {
		return block
	}
	line, rest, _ := strings.Cut(block, "\n")
	return line + "\n<!-- modified: " + m.Format("2006-01-02") + " -->" + "\n" + rest
}

// buildInput collects the validator's view of one part: the names of its
// facts and their description, marker and byte footprint.
func buildInput(section string, facts []memory.Fact, blocks []string, group []int) Input {
	in := Input{
		Section: section,
		HasDesc: map[string]bool{},
		Markers: map[string][]string{},
		Bytes:   map[string]int{},
		Links:   map[string]bool{},
	}
	seen := map[string]bool{}
	var names []string
	for _, idx := range group {
		f := facts[idx]
		in.HasDesc[f.Name] = in.HasDesc[f.Name] || strings.TrimSpace(f.Description) != ""
		in.Bytes[f.Name] += len(blocks[idx])
		for _, m := range markerRe.FindAllString(blocks[idx], -1) {
			if !slices.Contains(in.Markers[f.Name], m) {
				in.Markers[f.Name] = append(in.Markers[f.Name], m)
			}
		}
		for _, m := range linkTargetRe.FindAllStringSubmatch(blocks[idx], -1) {
			in.Links[m[1]] = true
		}
		if !seen[f.Name] {
			seen[f.Name] = true
			names = append(names, f.Name)
		}
	}
	slices.Sort(names)
	in.Names = names
	return in
}

// answerBlocks splits a section answer (or a verbatim part input) into its
// ### fact blocks, keeping fenced code intact; the section heading line and
// anything before the first fact are dropped.
func answerBlocks(clean string) []string {
	lines := strings.Split(strings.TrimSpace(clean), "\n")
	fence := render.NewFence()
	var blocks []string
	var cur []string
	started := false
	for _, line := range lines {
		inside := fence.Step(line)
		if !inside && strings.HasPrefix(line, "### ") {
			if started {
				blocks = append(blocks, strings.TrimRight(strings.Join(cur, "\n"), "\n"))
			}
			cur = cur[:0]
			started = true
		} else if !inside && !started {
			continue
		}
		cur = append(cur, line)
	}
	if started {
		blocks = append(blocks, strings.TrimRight(strings.Join(cur, "\n"), "\n"))
	}
	return blocks
}
