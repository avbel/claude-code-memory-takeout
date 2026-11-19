package consolidate

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func baseInput() Input {
	return Input{
		Section: "Project",
		Names:   []string{"alpha", "beta"},
		HasDesc: map[string]bool{"alpha": true, "beta": true},
		Markers: map[string][]string{"alpha": {"[REDACTED]"}},
		Bytes:   map[string]int{"alpha": 40, "beta": 20},
	}
}

// validProjectAnswer is the shape every good answer in the table shares.
const validProjectAnswer = `## Project
### alpha
> Alpha kept

Alpha body with [REDACTED].

<!-- consolidation
- beta -> merged into alpha
-->`

func TestValidateAccepts(t *testing.T) {
	cases := []struct{ name, answer string }{
		{"plain", validProjectAnswer},
		{"wrapped in one fence", "```markdown\n" + validProjectAnswer + "\n```"},
		{"arrow entries", strings.Replace(validProjectAnswer, "- beta -> merged into alpha", "- beta → merged into alpha", 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, entries, errs := Validate(tc.answer, baseInput())
			if len(errs) != 0 {
				t.Fatalf("errs = %v", errs)
			}
			if len(entries) != 1 || entries[0].Name != "beta" || entries[0].Merge != "alpha" {
				t.Errorf("entries = %+v", entries)
			}
		})
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		input  Input
		want   string
	}{
		{
			name:   "wrong first line",
			answer: "## Other\n### alpha\n\nAlpha body with [REDACTED].\n",
			want:   "first line must be exactly `## Project`",
		},
		{
			name:   "invented name",
			answer: "## Project\n### delta\n\nNope.\n",
			want:   "`delta` is not a name from the input",
		},
		{
			name: "duplicate name",
			answer: "## Project\n### alpha\n> A\n\nBody.\n\n### alpha\n> A\n\nBody.\n" +
				"<!-- consolidation\n- beta -> merged into alpha\n-->\n",
			want: "`alpha` appears more than once",
		},
		{
			name:   "lost description",
			answer: "## Project\n### alpha\n\nAlpha body with [REDACTED].\n<!-- consolidation\n- beta -> merged into alpha\n-->\n",
			want:   "`alpha` lost its description line",
		},
		{
			name:   "extra section heading",
			answer: "## Project\n### alpha\n> A\n\nAlpha body with [REDACTED].\n\n## Other\n\nStray.\n",
			want:   "starts with `## ` outside the section heading",
		},
		{
			name:   "missing block entry",
			answer: "## Project\n### alpha\n> A\n\nAlpha body with [REDACTED].\n",
			want:   "`beta` is absent from the output and not in the consolidation block",
		},
		{
			name: "entry for kept name",
			answer: "## Project\n### alpha\n> A\n\nAlpha body with [REDACTED].\n\n### beta\n> B\n\nBody.\n" +
				"<!-- consolidation\n- beta -> merged into alpha\n-->\n",
			want: "`beta` is in the output and also in the consolidation block",
		},
		{
			name: "merge target absent",
			answer: "## Project\n### alpha\n> A\n\nAlpha body with [REDACTED].\n" +
				"<!-- consolidation\n- beta -> merged into gamma\n-->\n",
			want: "`beta` was merged into `gamma`, which is not in the output",
		},
		{
			name: "drop without reason",
			answer: "## Project\n### alpha\n> A\n\nAlpha body with [REDACTED].\n" +
				"<!-- consolidation\n- beta -> dropped:\n-->\n",
			want: "`beta` was dropped without a reason",
		},
		{
			name: "missing marker",
			answer: "## Project\n### alpha\n> A\n\nAlpha body without the marker.\n" +
				"<!-- consolidation\n- beta -> merged into alpha\n-->\n",
			want: "[REDACTED] is missing from the answer",
		},
		{
			name: "invented marker",
			answer: "## Project\n### alpha\n> A\n\nAlpha body with [REDACTED:jwt].\n" +
				"<!-- consolidation\n- beta -> merged into alpha\n-->\n",
			want: "answer contains a marker the input did not have",
		},
		{
			name:   "block not trailing",
			answer: "<!-- consolidation\n- beta -> merged into alpha\n-->\n\n## Project\n### alpha\n> A\n\nAlpha body with [REDACTED].\n",
			want:   "consolidation block is not at the end",
		},
		{
			name: "malformed entry",
			answer: "## Project\n### alpha\n> A\n\nAlpha body with [REDACTED].\n" +
				"<!-- consolidation\nbeta went into alpha\n-->\n",
			want: "consolidation block line is not an entry",
		},
		{
			name:   "size floor",
			answer: "## Project\n### alpha\n> A\n\nShort.\n<!-- consolidation\n- beta -> merged into alpha\n-->\n",
			input:  bigByteInput(),
			want:   "less than half of the facts it should carry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.input
			if in.Section == "" {
				in = baseInput()
			}
			_, _, errs := Validate(tc.answer, in)
			if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, tc.want) }) {
				t.Errorf("errs = %v, want one containing %q", errs, tc.want)
			}
		})
	}
}

// bigByteInput describes an alpha whose rendered input is far longer than
// a summarizing answer could preserve.
func bigByteInput() Input {
	in := baseInput()
	in.Bytes["alpha"] = 400
	in.Markers["alpha"] = nil
	return in
}

func TestValidateIgnoresFencedContent(t *testing.T) {
	answer := "## Project\n### alpha\n> A\n\n```\n## not a section\n### not a fact\n```\n\nAlpha body with [REDACTED].\n" +
		"<!-- consolidation\n- beta -> merged into alpha\n-->\n"
	_, _, errs := Validate(answer, baseInput())
	if len(errs) != 0 {
		t.Errorf("errs = %v, want none: fences must shield their content", errs)
	}
}

func TestSplit(t *testing.T) {
	items := []string{
		"x1",                     // 2 bytes → 0 tokens + 1 for the blank line
		strings.Repeat("b", 400), // 100 tokens
		strings.Repeat("c", 400),
		strings.Repeat("d", 400),
		strings.Repeat("e", 900), // 225 tokens, larger than the budget alone
	}
	groups := Split(items, 2, 200)
	var got []int
	for _, g := range groups {
		got = append(got, g...)
	}
	if !slices.Equal(got, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("items lost or reordered: %v", got)
	}
	// The huge item must sit alone in its own part.
	if len(groups[len(groups)-1]) != 1 || groups[len(groups)-1][0] != 4 {
		t.Errorf("groups = %v, want the oversized item alone", groups)
	}
	if len(groups) < 3 {
		t.Errorf("groups = %v, want a split", groups)
	}
}

func TestSplitEmpty(t *testing.T) {
	if groups := Split(nil, 2, 200); groups != nil {
		t.Errorf("Split(nil) = %v, want nil", groups)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(strings.Repeat("a", 400)); got != 100 {
		t.Errorf("EstimateTokens = %d, want 100", got)
	}
}

func TestPrompts(t *testing.T) {
	if !strings.Contains(SystemPrompt(), "OUTPUT FORMAT (checked mechanically; a deviation is rejected)") {
		t.Error("system prompt text drifted")
	}
	user := UserMessage("Project", "2 of 3", "INPUT WITH {{ braces }}")
	for _, want := range []string{"Section: Project", "Part: 2 of 3", "INPUT WITH {{ braces }}"} {
		if !strings.Contains(user, want) {
			t.Errorf("user message missing %q", want)
		}
	}
	msg := RetryMessage("Project", []string{"e1", "e2"})
	if !strings.Contains(msg, "## Project") || !strings.Contains(msg, "- e1") {
		t.Errorf("retry message = %q", msg)
	}
	// The error list is capped at 20 lines.
	big := make([]string, 30)
	for i := range big {
		big[i] = fmt.Sprintf("e%d", i)
	}
	if strings.Count(RetryMessage("Project", big), "- e2") > 1 || strings.Contains(RetryMessage("Project", big), "- e20") {
		t.Error("retry errors not capped at 20")
	}
}

func TestPartLabel(t *testing.T) {
	if got := partLabel(2, 3); got != "2 of 3" {
		t.Errorf("partLabel = %q", got)
	}
}

func TestWithModified(t *testing.T) {
	block := "### alpha\n> d\n\nbody"
	got := withModified(block, time.Date(2026, 11, 16, 10, 29, 0, 0, time.UTC))
	want := "### alpha\n<!-- modified: 2026-11-16 -->\n> d\n\nbody"
	if got != want {
		t.Errorf("withModified = %q, want %q", got, want)
	}
	if block != withModified(block, time.Time{}) {
		t.Error("a zero Modified must leave the block untouched")
	}
}

func TestValidateLinkTargets(t *testing.T) {
	in := baseInput()
	in.Links = map[string]bool{"https://docs.example/setup": true, "run.md": true}

	// A link that was already in the input passes.
	ok := "## Project\n### alpha\n> A\n\nSee [setup](https://docs.example/setup) and [run](run.md) with [REDACTED].\n" +
		"<!-- consolidation\n- beta -> merged into alpha\n-->\n"
	if _, _, errs := Validate(ok, in); len(errs) != 0 {
		t.Fatalf("errs = %v, want none for input links", errs)
	}

	// A smuggled external target is rejected, fenced ones are not.
	bad := "## Project\n### alpha\n> A\n\nSee [docs](https://evil.example/payload) with [REDACTED].\n" +
		"<!-- consolidation\n- beta -> merged into alpha\n-->\n"
	_, _, errs := Validate(bad, in)
	if !slices.ContainsFunc(errs, func(e string) bool {
		return strings.Contains(e, "link to a target the input did not have: https://evil.example/payload")
	}) {
		t.Errorf("errs = %v, want the smuggling rejection", errs)
	}

	fenced := "## Project\n### alpha\n> A\n\n```\n[docs](https://evil.example/payload)\n```\n\nWith [REDACTED].\n" +
		"<!-- consolidation\n- beta -> merged into alpha\n-->\n"
	if _, _, errs := Validate(fenced, in); len(errs) != 0 {
		t.Errorf("errs = %v, want none: fenced links are data, not links", errs)
	}
}
