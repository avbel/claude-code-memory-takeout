//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// llmTree builds the --llm fixture: three project facts, one carrying a
// secret that must never reach the request body.
func llmTree(t *testing.T) *testEnv {
	t.Helper()
	e := newTestEnv(t)
	root := e.slugDir()
	writeFact(t, root, "alpha", "project", "Alpha fact",
		"Alpha body and the secret spot token = \"s3cr3tvalue99\".")
	writeFact(t, root, "beta", "project", "Beta near-duplicate", "Beta body, same shape as alpha.")
	writeFact(t, root, "gamma", "project", "Gamma placeholder", "Gamma body.")
	return e
}

func TestLLMConsolidates(t *testing.T) {
	e := llmTree(t)
	fake := e.useLLM(llmValid)
	res := e.runOK("--llm")
	if !strings.Contains(res.Stderr, "llm: Project part 1/1 … kept 1, merged 1, dropped 1") {
		t.Errorf("stderr = %q, want the per-part info line", res.Stderr)
	}
	got := e.readOutput()
	if !strings.Contains(got, "mode: llm (test-model)") {
		t.Errorf("document = \n%s", got)
	}
	if !strings.Contains(got, "## Consolidation log") {
		t.Errorf("document has no consolidation log:\n%s", got)
	}
	if !strings.Contains(got, "- beta → merged into [alpha](#alpha)") {
		t.Errorf("document has no merged log line:\n%s", got)
	}
	if !strings.Contains(got, "- gamma → dropped: empty placeholder") {
		t.Errorf("document has no dropped log line:\n%s", got)
	}
	if strings.Contains(got, "### beta") || strings.Contains(got, "### gamma") {
		t.Errorf("merged and dropped facts still in the document:\n%s", got)
	}
	if strings.Contains(got, "s3cr3tvalue99") {
		t.Errorf("the raw secret leaked into the output:\n%s", got)
	}

	reqs := fake.all()
	if len(reqs) != 1 {
		t.Fatalf("LLM requests = %d, want 1", len(reqs))
	}
	if strings.Contains(reqs[0].Raw, "s3cr3tvalue99") {
		t.Error("the raw secret leaked into the request body")
	}
	if !strings.Contains(reqs[0].Raw, "[REDACTED]") {
		t.Error("the request body must carry the redacted form")
	}
	if msg := messageContent(t, reqs[0].Body, "system"); !strings.Contains(msg, "OUTPUT FORMAT") {
		t.Errorf("system prompt missing the output contract:\n%s", msg[:min(200, len(msg))])
	}
	if msg := messageContent(t, reqs[0].Body, "user"); !strings.Contains(msg, "Section: Project") {
		t.Errorf("user message missing the section header:\n%s", msg[:min(200, len(msg))])
	}
}

func TestLLMUnauthorizedExitsFour(t *testing.T) {
	e := llmTree(t)
	e.useLLM(llm401)
	res := e.run("--llm")
	if res.Code != 4 {
		t.Fatalf("exit = %d, want 4\nstderr:\n%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "401") {
		t.Errorf("stderr = %q, want the status", res.Stderr)
	}
}

func TestLLMInvalidTwiceFallsBack(t *testing.T) {
	e := llmTree(t)
	fake := e.useLLM(llmInvalidTwice)
	res := e.runOK("--llm")
	if !strings.Contains(res.Stderr, "kept verbatim") {
		t.Errorf("stderr = %q, want the fallback warning", res.Stderr)
	}
	got := e.readOutput()
	for _, name := range []string{"### alpha", "### beta", "### gamma"} {
		if !strings.Contains(got, name) {
			t.Errorf("fallback lost %s:\n%s", name, got)
		}
	}
	if strings.Contains(got, "## Consolidation log") {
		t.Errorf("a verbatim fallback must not add a log section:\n%s", got)
	}
	if len(fake.all()) != 2 {
		t.Errorf("LLM requests = %d, want the original plus one retry", len(fake.all()))
	}
}
