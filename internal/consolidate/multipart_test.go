package consolidate

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/avbel/claude-code-memory-takeout/internal/llm"
	"github.com/avbel/claude-code-memory-takeout/internal/memory"
)

// partFacts returns three project facts without descriptions, so each
// part's canned answer is minimal.
func partFacts() []memory.Fact {
	return []memory.Fact{
		{Name: "alpha", Type: "project", Rel: ".", File: "alpha.md", Body: "Alpha body."},
		{Name: "beta", Type: "project", Rel: ".", File: "beta.md", Body: "Beta body."},
		{Name: "gamma", Type: "project", Rel: ".", File: "gamma.md", Body: "Gamma body."},
	}
}

// partAnswer is a valid answer for a one-fact part: the fact kept, no
// consolidation block needed.
func partAnswer(name, body string) string {
	return "## Project\n### " + name + "\n\n" + body + "\n"
}

// multiPartOpts shrinks the budget so each fact becomes its own part.
func multiPartOpts(s *sink) Options {
	o := testOpts(s)
	o.MaxPartTokens = 8 // one ~6-token block plus the heading fits, two do not
	return o
}

func TestRunTwoPartsPerFact(t *testing.T) {
	facts := partFacts()
	steps := []step{
		{answer: partAnswer("alpha", "Alpha body.")},
		{answer: partAnswer("beta", "Beta body.")},
		{answer: partAnswer("gamma", "Gamma body.")},
	}
	s := &sink{}
	caller := &scriptedCaller{steps: steps}
	doc, res, err := Run(t.Context(), facts, "testproj", nil, caller, multiPartOpts(s))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(caller.calls) != 3 {
		t.Fatalf("calls = %d, want 3 (one per part)", len(caller.calls))
	}
	for i, want := range []string{"llm: Project part 1/3 … kept 1", "llm: Project part 2/3 … kept 1", "llm: Project part 3/3 … kept 1"} {
		if !slices.ContainsFunc(s.infos, func(l string) bool { return strings.HasPrefix(l, want) }) {
			t.Errorf("infos = %v, want %q", s.infos, want)
		}
		// The prompt labels use the "i of n" form.
		if user := caller.calls[i][1].Content; !strings.Contains(user, "Part: "+fmt.Sprintf("%d of 3", i+1)) {
			t.Errorf("call %d user message missing its part label:\n%s", i+1, user)
		}
	}
	for _, name := range []string{"### alpha", "### beta", "### gamma"} {
		if !strings.Contains(doc, name) {
			t.Errorf("document lost %s:\n%s", name, doc)
		}
	}
	if res.Kept != 3 {
		t.Errorf("kept = %d, want 3", res.Kept)
	}
}

// TestRunTwoPartsMixedFallback proves a failed part does not poison the
// others: part 2 truncates, parts 1 and 3 consolidate.
func TestRunTwoPartsMixedFallback(t *testing.T) {
	facts := partFacts()
	truncated := fmt.Errorf("%w (finish_reason length); raise --max-output-tokens", llm.ErrTruncated)
	steps := []step{
		{answer: partAnswer("alpha", "Alpha body.")},
		{err: truncated},
		{answer: partAnswer("gamma", "Gamma body.")},
	}
	s := &sink{}
	caller := &scriptedCaller{steps: steps}
	doc, res, err := Run(t.Context(), facts, "testproj", nil, caller, multiPartOpts(s))
	if err != nil {
		t.Fatalf("Run: %v (a truncated part must fall back, not fail)", err)
	}
	if !slices.ContainsFunc(s.warns, func(w string) bool { return strings.Contains(w, "Project part 2/3") }) {
		t.Errorf("warns = %v, want the part-2 fallback", s.warns)
	}
	for _, name := range []string{"### alpha", "### beta", "### gamma"} {
		if !strings.Contains(doc, name) {
			t.Errorf("document lost %s:\n%s", name, doc)
		}
	}
	if res.Kept != 3 {
		t.Errorf("kept = %d, want every block counted", res.Kept)
	}
}

// TestRunBudgetCap proves --max-output-tokens caps the part budget: the
// part budget shrinks to 4/5 of it even with a huge --max-part-tokens.
func TestRunBudgetCap(t *testing.T) {
	nameA, nameB := strings.Repeat("a", 300), strings.Repeat("b", 300)
	facts := []memory.Fact{
		{Name: nameA, Type: "project", Rel: ".", File: "a.md", Body: strings.Repeat("x", 1200)},
		{Name: nameB, Type: "project", Rel: ".", File: "b.md", Body: strings.Repeat("y", 1200)},
	}
	steps := []step{
		{answer: partAnswer(nameA, strings.Repeat("x", 1200))},
		{answer: partAnswer(nameB, strings.Repeat("y", 1200))},
	}
	s := &sink{}
	caller := &scriptedCaller{steps: steps}
	opts := testOpts(s)
	opts.MaxPartTokens = 1 << 20 // huge: without the cap this is one part
	opts.MaxOutputTokens = 400   // budget becomes 320 tokens < one ~388-token fact
	_, _, err := Run(t.Context(), facts, "testproj", nil, caller, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(caller.calls) != 2 {
		t.Errorf("calls = %d, want the 0.8x max-output-tokens cap to force one part per fact", len(caller.calls))
	}
}

func TestRunBlankAnswerFallsBack(t *testing.T) {
	s := &sink{}
	caller := &scriptedCaller{steps: []step{{answer: "\n  \n"}}}
	doc, _, err := Run(t.Context(), testFacts(), "testproj", nil, caller, testOpts(s))
	if err != nil {
		t.Fatalf("Run: %v (a blank answer must fall back, not exit 4)", err)
	}
	if !slices.ContainsFunc(s.warns, func(w string) bool { return strings.Contains(w, "empty answer") }) {
		t.Errorf("warns = %v, want the empty-answer fallback", s.warns)
	}
	for _, name := range []string{"### alpha", "### beta", "### gamma"} {
		if !strings.Contains(doc, name) {
			t.Errorf("document lost %s:\n%s", name, doc)
		}
	}
}

// TestRunMergeDroppingContentRejected is the negative §11.5 case: a merge
// that keeps the target but discards the merged source's content fails
// the size floor and falls back to verbatim.
func TestRunMergeDroppingContentRejected(t *testing.T) {
	long := strings.Repeat("detail of beta worth keeping ", 20)
	facts := []memory.Fact{
		{Name: "alpha", Description: "Alpha", Type: "project", Rel: ".", File: "a.md", Body: strings.Repeat("alpha detail ", 30)},
		{Name: "beta", Description: "Beta", Type: "project", Rel: ".", File: "b.md", Body: long},
	}
	answer := `## Project
### alpha
> Alpha

alpha detail alpha detail.

<!-- consolidation
- beta -> merged into alpha
-->`
	s := &sink{}
	caller := &scriptedCaller{steps: []step{{answer: answer}, {answer: answer}}}
	doc, res, err := Run(t.Context(), facts, "testproj", nil, caller, testOpts(s))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.ContainsFunc(s.warns, func(w string) bool { return strings.Contains(w, "kept verbatim") }) {
		t.Fatalf("warns = %v, want the floor to reject the content-dropping merge", s.warns)
	}
	if !strings.Contains(doc, strings.TrimSpace(long)) {
		t.Error("the fallback must restore beta's full body")
	}
	if res.Merged != 0 {
		t.Errorf("merged = %d, want 0 after the fallback", res.Merged)
	}
}

// TestValidateAcceptsKindMarker proves a [REDACTED:<kind>] marker in the
// input survives validation when the answer echoes it — real memories
// redact to kind markers (yrem_token, aws_key, jwt), not bare ones.
func TestValidateAcceptsKindMarker(t *testing.T) {
	in := Input{
		Section: "Project",
		Names:   []string{"alpha"},
		HasDesc: map[string]bool{"alpha": true},
		Markers: map[string][]string{"alpha": {"[REDACTED:aws_key]"}},
		Bytes:   map[string]int{"alpha": 60},
	}
	answer := "## Project\n### alpha\n> A\n\nUses [REDACTED:aws_key] here.\n"
	_, _, errs := Validate(answer, in)
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
}

// TestAnswerBlocksFencedHeadings proves a fenced ### line does not split
// a block — answers about commands routinely contain them.
func TestAnswerBlocksFencedHeadings(t *testing.T) {
	clean := "## Project\n### alpha\n> A\n\n```\n### not a fact\n```\n\nbody\n"
	blocks := answerBlocks(clean)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1:\n%v", len(blocks), blocks)
	}
	if !strings.Contains(blocks[0], "### not a fact") {
		t.Errorf("the fenced line must stay inside the block:\n%s", blocks[0])
	}
}
