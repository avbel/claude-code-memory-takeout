package consolidate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/avbel/claude-code-memory-takeout/internal/llm"
	"github.com/avbel/claude-code-memory-takeout/internal/memory"
	"github.com/avbel/claude-code-memory-takeout/internal/redact"
	"github.com/avbel/claude-code-memory-takeout/internal/render"
)

// scriptedCaller plays back fixed answers or errors and records every
// message list it was called with.
type scriptedCaller struct {
	steps []step
	calls [][]llm.Message
}

type step struct {
	answer string
	err    error
}

func (f *scriptedCaller) Chat(_ context.Context, msgs []llm.Message) (string, llm.Usage, error) {
	f.calls = append(f.calls, msgs)
	if len(f.calls) > len(f.steps) {
		return "", llm.Usage{}, fmt.Errorf("unexpected call %d", len(f.calls))
	}
	s := f.steps[len(f.calls)-1]
	if s.err != nil {
		return "", llm.Usage{}, s.err
	}
	return s.answer, llm.Usage{}, nil
}

type sink struct {
	warns []string
	infos []string
}

func testOpts(s *sink) Options {
	r, err := redact.New()
	if err != nil {
		panic(err)
	}
	return Options{
		Model:           "test-model",
		MaxPartTokens:   24000,
		MaxOutputTokens: 8000,
		Redactor:        r,
		Warn:            func(m string) { s.warns = append(s.warns, m) },
		Info:            func(m string) { s.infos = append(s.infos, m) },
		Verbose:         func(string) {},
	}
}

// testFacts is the redacted input of the Run scenarios: alpha carries a
// marker, beta is a near-duplicate, gamma is droppable.
func testFacts() []memory.Fact {
	return []memory.Fact{
		{Name: "alpha", Description: "Alpha fact", Type: "project", Rel: ".", File: "alpha.md",
			Body: "Alpha body with token = [REDACTED]."},
		{Name: "beta", Description: "Beta near-duplicate", Type: "project", Rel: ".", File: "beta.md",
			Body: "Beta body, same shape as alpha."},
		{Name: "gamma", Description: "Gamma to drop", Type: "project", Rel: ".", File: "gamma.md",
			Body: "Gamma body."},
	}
}

const validAnswer = `## Project
### alpha
> Alpha fact, now merged with beta

Alpha body with token = [REDACTED].

Beta body, same shape as alpha.

<!-- consolidation
- beta -> merged into alpha
- gamma -> dropped: superseded by alpha
-->`

func runScripted(t *testing.T, facts []memory.Fact, steps []step) (string, Result, *scriptedCaller, *sink) {
	t.Helper()
	s := &sink{}
	caller := &scriptedCaller{steps: steps}
	doc, res, err := Run(t.Context(), facts, "testproj", []render.Source{{Rel: ".", Facts: len(facts)}}, caller, testOpts(s))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return doc, res, caller, s
}

func TestRunValidAnswer(t *testing.T) {
	doc, res, _, s := runScripted(t, testFacts(), []step{{answer: validAnswer}})
	if res.Kept != 1 || res.Merged != 1 || res.Dropped != 1 {
		t.Errorf("result = %+v, want kept 1, merged 1, dropped 1", res)
	}
	for _, want := range []string{
		"mode: llm (test-model)",
		"## Project\n### alpha",
		"Beta body, same shape as alpha.",
		"## Consolidation log",
		"- beta → merged into [alpha](#alpha)",
		"- gamma → dropped: superseded by alpha",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document missing %q:\n%s", want, doc)
		}
	}
	if len(s.infos) != 1 || !strings.Contains(s.infos[0], "llm: Project part 1/1 … kept 1, merged 1, dropped 1") {
		t.Errorf("infos = %v", s.infos)
	}
	if strings.Contains(doc, "## User") {
		t.Errorf("empty sections must be omitted:\n%s", doc)
	}
}

func TestRunRetryAfterInventedName(t *testing.T) {
	bad := "## Project\n### delta\n\nInvented name, all input names lost.\n"
	_, _, caller, _ := runScripted(t, testFacts(), []step{{answer: bad}, {answer: validAnswer}})
	if len(caller.calls) != 2 {
		t.Fatalf("calls = %d, want a retry", len(caller.calls))
	}
	retry := caller.calls[1]
	if len(retry) != 4 {
		t.Fatalf("retry messages = %d, want 4 (system, user, assistant, retry user)", len(retry))
	}
	if retry[2].Role != "assistant" || retry[2].Content != bad {
		t.Errorf("retry message 2 = %v, want the rejected answer", retry[2])
	}
	if !strings.Contains(retry[3].Content, "- `delta` is not a name from the input") {
		t.Errorf("retry message = %q, want the error list", retry[3].Content)
	}
}

func TestRunInvalidTwiceFallsBack(t *testing.T) {
	bad := "## Project\n### delta\n\nNo consolidation block at all.\n"
	doc, res, _, s := runScripted(t, testFacts(), []step{{answer: bad}, {answer: bad}})
	if len(s.warns) != 1 || !strings.Contains(s.warns[0], "llm output for Project part 1/1 failed") {
		t.Fatalf("warns = %v, want the fallback warning", s.warns)
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(doc, "### "+name) {
			t.Errorf("fallback lost %s:\n%s", name, doc)
		}
	}
	if res.Kept != 3 || res.Merged != 0 || res.Dropped != 0 {
		t.Errorf("result = %+v, want the verbatim part counted as kept", res)
	}
	if strings.Contains(doc, "## Consolidation log") {
		t.Errorf("a verbatim fallback must not add a log section:\n%s", doc)
	}
}

func TestRunTruncatedFallsBack(t *testing.T) {
	truncated := fmt.Errorf("%w (finish_reason length); raise --max-output-tokens", llm.ErrTruncated)
	doc, _, caller, s := runScripted(t, testFacts(), []step{{err: truncated}})
	if len(caller.calls) != 1 {
		t.Fatalf("calls = %d, want no retry after truncation", len(caller.calls))
	}
	if !slices.ContainsFunc(s.warns, func(w string) bool { return strings.Contains(w, "kept verbatim") }) {
		t.Errorf("warns = %v, want the truncation fallback", s.warns)
	}
	if !strings.Contains(doc, "### alpha") || !strings.Contains(doc, "### gamma") {
		t.Errorf("document = \n%s", doc)
	}
}

func TestRunOtherClientErrorIsFatal(t *testing.T) {
	s := &sink{}
	caller := &scriptedCaller{steps: []step{{err: errors.New("HTTP 401: bad key")}}}
	_, _, err := Run(t.Context(), testFacts(), "testproj", nil, caller, testOpts(s))
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want the client error surfaced", err)
	}
	if len(s.warns) != 0 {
		t.Errorf("warns = %v, want none before the fatal error", s.warns)
	}
}

func TestRunLinkRewriteAfterMergeDrop(t *testing.T) {
	answer := `## Project
### alpha
> Merged fact

Alpha body with token = [REDACTED].

See [the beta note](#beta) and [the gamma note](#gamma).

<!-- consolidation
- beta -> merged into alpha
- gamma -> dropped: superseded by alpha
-->`
	doc, _, _, _ := runScripted(t, testFacts(), []step{{answer: answer}})
	if !strings.Contains(doc, "[the beta note](#alpha)") {
		t.Errorf("merged link not retargeted:\n%s", doc)
	}
	if !strings.Contains(doc, "the gamma note") || strings.Contains(doc, "[the gamma note]") {
		t.Errorf("dropped link not unlinked:\n%s", doc)
	}
}

func TestRunSecretInAnswerRedacted(t *testing.T) {
	answer := `## Project
### alpha
> Alpha fact

Alpha body with token = [REDACTED].

A fresh key sk-abcdefghijklmnopqrstuv appeared.

<!-- consolidation
- beta -> merged into alpha
- gamma -> dropped: superseded by alpha
-->`
	doc, _, _, _ := runScripted(t, testFacts(), []step{{answer: answer}})
	if strings.Contains(doc, "sk-abcdefghijklmnopqrstuv") {
		t.Errorf("the answer's fresh secret leaked:\n%s", doc)
	}
	if !strings.Contains(doc, "[REDACTED:openai_key]") {
		t.Errorf("the answer's fresh secret was not redacted:\n%s", doc)
	}
}

// TestRunDatedMergeKeepsHistory is the §11.5 hard case: a merge of two
// near-duplicates is only valid when every dated line survives.
func TestRunDatedMergeKeepsHistory(t *testing.T) {
	facts := []memory.Fact{
		{Name: "model-pick", Description: "Which model for which task", Type: "project", Rel: ".", File: "a.md",
			Body: "2026-10-01: Haiku for bulk reads.\n2026-11-01: switched to Sonnet for reviews."},
		{Name: "model-pick-2", Description: "Which model for which task, update", Type: "project", Rel: ".", File: "b.md",
			Body: "2026-11-08: was Sonnet → back to Haiku for reads; Opus xhigh stays for reviews."},
	}
	answer := `## Project
### model-pick
> Which model for which task

2026-10-01: Haiku for bulk reads.
2026-11-01: switched to Sonnet for reviews (was: first choice).
2026-11-08: was Sonnet → back to Haiku for reads; Opus xhigh stays for reviews; supersedes the 2026-11-01 pick for reads.

<!-- consolidation
- model-pick-2 -> merged into model-pick
-->`
	doc, _, _, _ := runScripted(t, facts, []step{{answer: answer}})
	for _, dated := range []string{"2026-10-01:", "2026-11-01:", "2026-11-08:"} {
		if !strings.Contains(doc, dated) {
			t.Errorf("merge lost the dated line %q:\n%s", dated, doc)
		}
	}
}

// TestRunStaleCorrectionSurvives is the §11.5 hard case: a fact that
// records that another fact is stale is a correction and must be kept.
func TestRunStaleCorrectionSurvives(t *testing.T) {
	facts := testFacts()
	facts = append(facts, memory.Fact{
		Name: "stale-note", Description: "Correction", Type: "project", Rel: ".", File: "stale.md",
		Body: "The beta approach was never merged; the code still uses the alpha path.",
	})
	answer := `## Project
### alpha
> Alpha fact

Alpha body with token = [REDACTED].

Beta body, same shape as alpha.

### stale-note
> Correction

The beta approach was never merged; the code still uses the alpha path.

<!-- consolidation
- beta -> merged into alpha
- gamma -> dropped: superseded by alpha
-->`
	doc, res, _, _ := runScripted(t, facts, []step{{answer: answer}})
	if !strings.Contains(doc, "### stale-note") {
		t.Errorf("the stale-correction fact was dropped:\n%s", doc)
	}
	if res.Kept != 2 {
		t.Errorf("kept = %d, want alpha and stale-note", res.Kept)
	}
}

// TestRunAveragedBenchmarksRejected is the §11.5 hard case: two look-alike
// benchmarks merged into one averaged line lose bytes, and the size floor
// rejects it.
func TestRunAveragedBenchmarksRejected(t *testing.T) {
	long := strings.Repeat("Measured on the cpu box: ", 20)
	facts := []memory.Fact{
		{Name: "bench-a", Description: "Benchmark A", Type: "project", Rel: ".", File: "a.md",
			Body: long + "0.077 img/s, batch 8."},
		{Name: "bench-b", Description: "Benchmark B", Type: "project", Rel: ".", File: "b.md",
			Body: long + "0.77 img/s, batch 8, different gpu."},
	}
	answer := "## Project\n### bench-a\n> Averaged\n\n" + strings.Repeat("x", 40) + "\n"
	doc, _, _, s := runScripted(t, facts, []step{{answer: answer}, {answer: answer}})
	if !slices.ContainsFunc(s.warns, func(w string) bool { return strings.Contains(w, "kept verbatim") }) {
		t.Fatalf("warns = %v, want the size-floor fallback", s.warns)
	}
	if !strings.Contains(doc, "0.077 img/s") || !strings.Contains(doc, "0.77 img/s") {
		t.Errorf("the fallback must restore both benchmarks:\n%s", doc)
	}
}

func TestRunModifiedHintStripped(t *testing.T) {
	facts := testFacts()
	for i := range facts {
		facts[i].Modified = parseTime(t, "2026-11-16T10:29:00.836Z")
	}
	doc, _, caller, _ := runScripted(t, facts, []step{{answer: validAnswer}})
	if strings.Contains(doc, "<!-- modified:") {
		t.Errorf("the LLM-only hint leaked into the document:\n%s", doc)
	}
	user := caller.calls[0][1].Content
	if !strings.Contains(user, "<!-- modified: 2026-11-16 -->") {
		t.Errorf("the input must carry the modified hint:\n%s", user)
	}
}

func TestRunWarnNoMaxOutputTokens(t *testing.T) {
	s := &sink{}
	caller := &scriptedCaller{steps: []step{{answer: validAnswer}}}
	opts := testOpts(s)
	opts.MaxOutputTokens = 0
	if _, _, err := Run(t.Context(), testFacts(), "testproj", nil, caller, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.ContainsFunc(s.warns, func(w string) bool { return strings.Contains(w, "--max-output-tokens not set") }) {
		t.Errorf("warns = %v, want the unset --max-output-tokens warning", s.warns)
	}
}

func parseTime(t *testing.T, raw string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
