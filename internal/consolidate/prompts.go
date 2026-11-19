// Package consolidate runs the --llm phase: each rendered section part is
// sent to an OpenAI-compatible model with a strict output contract, the
// answer is validated mechanically, and any part that fails twice falls
// back to its verbatim input.
package consolidate

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed prompts
var promptFS embed.FS

// The prompt files are embedded at build time; a missing one is a build
// break, not a runtime condition.
var (
	systemPrompt = mustPrompt("system.md")
	userPrompt   = mustPrompt("user.md")
	retryPrompt  = mustPrompt("retry.md")
)

func mustPrompt(name string) string {
	b, err := promptFS.ReadFile("prompts/" + name)
	if err != nil {
		panic("consolidate: embedded prompt " + name + ": " + err.Error())
	}
	return string(b)
}

// SystemPrompt returns the built-in system prompt.
func SystemPrompt() string {
	return systemPrompt
}

// UserMessage renders the per-part user message; part is "1 of 2" style.
// Placeholders are filled with strings.NewReplacer because the input may
// contain `{{`.
func UserMessage(section, part, input string) string {
	return strings.NewReplacer(
		"{{SECTION}}", section,
		"{{PART}}", part,
		"{{INPUT}}", input,
	).Replace(userPrompt)
}

// RetryMessage renders the follow-up sent after a rejected answer. errs
// are the validator's findings, capped here at maxRetryErrors lines.
func RetryMessage(section string, errs []string) string {
	if len(errs) > maxRetryErrors {
		errs = errs[:maxRetryErrors]
	}
	var b strings.Builder
	for _, e := range errs {
		b.WriteString("- ")
		b.WriteString(e)
		b.WriteString("\n")
	}
	return strings.NewReplacer(
		"{{SECTION}}", section,
		"{{ERRORS}}", strings.TrimRight(b.String(), "\n"),
	).Replace(retryPrompt)
}

const maxRetryErrors = 20

// partLabel renders "i of n" for the prompts and the fallback warning.
func partLabel(i, n int) string {
	return fmt.Sprintf("%d of %d", i, n)
}
