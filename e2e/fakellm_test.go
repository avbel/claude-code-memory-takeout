//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// llmMode selects the fake server's behaviour for one run.
type llmMode int

const (
	llmValid        llmMode = iota // always the canned valid consolidation
	llm401                         // 401 forever
	llmInvalidTwice                // an invalid answer every time
)

// validConsolidation is the canned answer the validator accepts for the
// llm fixture facts alpha, beta and gamma: beta merged into alpha, gamma
// dropped.
const validConsolidation = `## Project
### alpha
> Alpha, merged with beta

Alpha body and the secret spot token = [REDACTED].

Beta body, same shape as alpha.

<!-- consolidation
- beta -> merged into alpha
- gamma -> dropped: empty placeholder
-->`

// invalidConsolidation invents a name and drops every input name without
// a consolidation block, so the validator rejects it.
const invalidConsolidation = `## Project
### delta
> Invented

No input name survives.

`

// recordedRequest is one captured chat/completions request.
type recordedRequest struct {
	Raw  string
	Body map[string]any
}

// fakeLLM is an OpenAI-compatible server that records every request body.
type fakeLLM struct {
	server *httptest.Server
	mode   llmMode

	mu       sync.Mutex
	requests []recordedRequest
}

func newFakeLLM(t *testing.T, mode llmMode) *fakeLLM {
	t.Helper()
	f := &fakeLLM{mode: mode}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeLLM) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{Raw: string(raw), Body: body})
	f.mu.Unlock()

	switch f.mode {
	case llm401:
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
		return
	case llmInvalidTwice:
		writeChatResponse(w, invalidConsolidation)
		return
	default:
		writeChatResponse(w, validConsolidation)
	}
}

// writeChatResponse answers with a single non-streamed choice.
func writeChatResponse(w http.ResponseWriter, content string) {
	resp := map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 120, "completion_tokens": 60},
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

// all returns every recorded request in order.
func (f *fakeLLM) all() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

// messageContent returns the first message with the given role.
func messageContent(t *testing.T, body map[string]any, role string) string {
	t.Helper()
	msgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("request body has no messages: %v", body)
	}
	for _, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if msg["role"] == role {
			content, _ := msg["content"].(string)
			return content
		}
	}
	t.Fatalf("request body has no %q message", role)
	return ""
}
