package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type attemptLog struct {
	mu     sync.Mutex
	bodies []map[string]any
	auth   []string
	agents []string
	ctype  []string
}

func (l *attemptLog) record(body []byte, h http.Header) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	l.bodies = append(l.bodies, m)
	l.auth = append(l.auth, h.Get("Authorization"))
	l.agents = append(l.agents, h.Get("User-Agent"))
	l.ctype = append(l.ctype, h.Get("Content-Type"))
}

func (l *attemptLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.bodies)
}

func (l *attemptLog) body(i int) map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i >= len(l.bodies) {
		return nil
	}
	return l.bodies[i]
}

func startServer(t *testing.T, handler func(n int, w http.ResponseWriter, r *http.Request)) (*httptest.Server, *attemptLog) {
	t.Helper()
	log := &attemptLog{}
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		log.mu.Lock()
		n++
		count := n
		log.mu.Unlock()
		log.record(body, r.Header)
		handler(count, w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func newTestClient(t *testing.T, baseURL string, tune func(*Options)) *Client {
	t.Helper()
	opts := Options{
		BaseURL: baseURL,
		Model:   "test-model",
		APIKey:  "test-key",
		Version: "9.9.9",
		Wait:    func(context.Context, time.Duration) error { return nil },
	}
	if tune != nil {
		tune(&opts)
	}
	c, err := New(opts)
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	return c
}

func writeOK(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":3}}`, content)
}

func TestCompleteSuccess(t *testing.T) {
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) { writeOK(w, "ok") })
	c := newTestClient(t, srv.URL, nil)
	text, usage, err := c.Complete(t.Context(), "be brief", "hello")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if text != "ok" {
		t.Errorf("text = %q, want ok", text)
	}
	if usage.PromptTokens != 11 || usage.CompletionTokens != 3 {
		t.Errorf("usage = %+v, want {11 3}", usage)
	}
	if log.count() != 1 {
		t.Fatalf("requests = %d, want 1", log.count())
	}
	b := log.body(0)
	if b["model"] != "test-model" {
		t.Errorf("model = %v", b["model"])
	}
	if v, ok := b["stream"]; !ok || v != false {
		t.Errorf("stream = %v, want false", b["stream"])
	}
	if _, ok := b["max_tokens"]; ok {
		t.Error("max_tokens should be absent when unset")
	}
	msgs, ok := b["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages = %v", b["messages"])
	}
	first := msgs[0].(map[string]any)
	second := msgs[1].(map[string]any)
	if first["role"] != "system" || first["content"] != "be brief" {
		t.Errorf("system message = %v", first)
	}
	if second["role"] != "user" || second["content"] != "hello" {
		t.Errorf("user message = %v", second)
	}
	if got := log.agents[0]; got != "claude-code-memory-takeout/9.9.9" {
		t.Errorf("User-Agent = %q", got)
	}
	if got := log.auth[0]; got != "Bearer test-key" {
		t.Errorf("Authorization = %q", got)
	}
	if got := log.ctype[0]; got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestMaxTokensSet(t *testing.T) {
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) { writeOK(w, "ok") })
	c := newTestClient(t, srv.URL, func(o *Options) { o.MaxOutputTokens = 8000 })
	if _, _, err := c.Complete(t.Context(), "s", "u"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := log.body(0)["max_tokens"]; got != 8000.0 {
		t.Errorf("max_tokens = %v, want 8000", got)
	}
}

func TestThinkingStyleOpenAIAuto(t *testing.T) {
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) { writeOK(w, "ok") })
	c := newTestClient(t, srv.URL, func(o *Options) {
		o.ThinkingStyle = "auto"
		o.ThinkingLevel = "medium"
	})
	if _, _, err := c.Complete(t.Context(), "s", "u"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := log.body(0)["reasoning_effort"]; got != "medium" {
		t.Errorf("reasoning_effort = %v, want medium", got)
	}
	if _, ok := log.body(0)["reasoning"]; ok {
		t.Error("reasoning should be absent for the openai style")
	}
}

func TestThinkingStyleOpenRouter(t *testing.T) {
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) { writeOK(w, "ok") })
	c := newTestClient(t, srv.URL, func(o *Options) {
		o.ThinkingStyle = "openrouter"
		o.ThinkingLevel = "high"
	})
	if _, _, err := c.Complete(t.Context(), "s", "u"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	reasoning, ok := log.body(0)["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Errorf("reasoning = %v, want {effort: high}", log.body(0)["reasoning"])
	}
}

func TestThinkingStyleNone(t *testing.T) {
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) { writeOK(w, "ok") })
	c := newTestClient(t, srv.URL, func(o *Options) {
		o.ThinkingStyle = "none"
		o.ThinkingLevel = "high"
	})
	if _, _, err := c.Complete(t.Context(), "s", "u"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	b := log.body(0)
	if _, ok := b["reasoning"]; ok {
		t.Error("reasoning should be absent for style none")
	}
	if _, ok := b["reasoning_effort"]; ok {
		t.Error("reasoning_effort should be absent for style none")
	}
}

func TestExtraBodyDeepMerge(t *testing.T) {
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) { writeOK(w, "ok") })
	c := newTestClient(t, srv.URL, func(o *Options) {
		o.ThinkingStyle = "openrouter"
		o.ThinkingLevel = "high"
		o.MaxOutputTokens = 8000
		o.ExtraBody = json.RawMessage(`{"temperature":0.5,"reasoning":{"effort":"low","top_k":4},"max_tokens":99}`)
	})
	if _, _, err := c.Complete(t.Context(), "s", "u"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	b := log.body(0)
	if b["temperature"] != 0.5 {
		t.Errorf("temperature = %v", b["temperature"])
	}
	if b["max_tokens"] != 99.0 {
		t.Errorf("max_tokens = %v, want override to 99", b["max_tokens"])
	}
	reasoning, ok := b["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %v", b["reasoning"])
	}
	if reasoning["effort"] != "low" {
		t.Errorf("reasoning.effort = %v, want override to low", reasoning["effort"])
	}
	if reasoning["top_k"] != 4.0 {
		t.Errorf("reasoning.top_k = %v, want 4", reasoning["top_k"])
	}
}

func TestExtraBodyNonObjectRejected(t *testing.T) {
	for _, raw := range []string{`[1,2]`, `"x"`, `42`, `null`, `true`} {
		_, err := New(Options{BaseURL: "http://127.0.0.1:1/v1", Model: "m", ExtraBody: json.RawMessage(raw)})
		if !errors.Is(err, ErrUsage) {
			t.Errorf("extra body %s: err = %v, want ErrUsage", raw, err)
		}
	}
	_, err := New(Options{BaseURL: "http://127.0.0.1:1/v1", Model: "m", ExtraBody: json.RawMessage(`{}`)})
	if err != nil {
		t.Errorf("empty object extra body: %v", err)
	}
}

func TestRetryOn429HonorsRetryAfter(t *testing.T) {
	var waits []time.Duration
	var mu sync.Mutex
	srv, log := startServer(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		writeOK(w, "ok")
	})
	c := newTestClient(t, srv.URL, func(o *Options) {
		o.Wait = func(_ context.Context, d time.Duration) error {
			mu.Lock()
			waits = append(waits, d)
			mu.Unlock()
			return nil
		}
	})
	text, _, err := c.Complete(t.Context(), "s", "u")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if text != "ok" {
		t.Errorf("text = %q", text)
	}
	if log.count() != 2 {
		t.Errorf("requests = %d, want 2", log.count())
	}
	if len(waits) != 1 || waits[0] != 2*time.Second {
		t.Errorf("waits = %v, want [2s]", waits)
	}
}

func TestRetryAfterCappedAt60s(t *testing.T) {
	var waits []time.Duration
	var mu sync.Mutex
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})
	c := newTestClient(t, srv.URL, func(o *Options) {
		o.Wait = func(_ context.Context, d time.Duration) error {
			mu.Lock()
			waits = append(waits, d)
			mu.Unlock()
			return nil
		}
	})
	_, _, err := c.Complete(t.Context(), "s", "u")
	if err == nil {
		t.Fatal("want error after exhausting retries")
	}
	if log.count() != 3 {
		t.Errorf("requests = %d, want 3", log.count())
	}
	for i, d := range waits {
		if d != time.Minute {
			t.Errorf("waits[%d] = %v, want 1m", i, d)
		}
	}
}

func TestBackoffSequence(t *testing.T) {
	limits := [][2]float64{{1600, 2400}, {3200, 4800}, {6400, 9600}}
	for i, lim := range limits {
		d := backoffDelay(i + 1)
		ms := float64(d.Milliseconds())
		if ms < lim[0] || ms > lim[1] {
			t.Errorf("backoffDelay(%d) = %v, want between %v and %v", i+1, d, lim[0], lim[1])
		}
	}
}

func TestNoRetryOn401(t *testing.T) {
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	})
	c := newTestClient(t, srv.URL, nil)
	_, _, err := c.Complete(t.Context(), "s", "u")
	if err == nil {
		t.Fatal("want error")
	}
	if log.count() != 1 {
		t.Errorf("requests = %d, want 1 (no retry on 401)", log.count())
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad key") {
		t.Errorf("err = %v, want status and body", err)
	}
}

func TestRetryOnNetworkError(t *testing.T) {
	var waits int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	c := newTestClient(t, url, func(o *Options) {
		o.Wait = func(context.Context, time.Duration) error {
			waits++
			return nil
		}
	})
	_, _, err := c.Complete(t.Context(), "s", "u")
	if err == nil {
		t.Fatal("want error")
	}
	if waits != 2 {
		t.Errorf("retries = %d, want 2", waits)
	}
}

func TestErrorBodyRedacted(t *testing.T) {
	secret := "sk-abc1234567890secretvalue"
	srv, _ := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"invalid key `+secret+`"}}`, http.StatusInternalServerError)
	})
	c := newTestClient(t, srv.URL, func(o *Options) {
		o.Redact = func(s string) string { return strings.ReplaceAll(s, secret, "[REDACTED]") }
	})
	_, _, err := c.Complete(t.Context(), "s", "u")
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks the secret: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("error = %v, want the redact func applied", err)
	}
	apiErr, ok := errors.AsType[*APIError](err)
	if !ok {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if !strings.Contains(apiErr.Body, "[REDACTED]") {
		t.Errorf("APIError.Body = %q, want redacted", apiErr.Body)
	}
}

func TestErrorBodyTruncated(t *testing.T) {
	srv, _ := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		http.Error(w, strings.Repeat("x", 2000), http.StatusInternalServerError)
	})
	c := newTestClient(t, srv.URL, nil)
	_, _, err := c.Complete(t.Context(), "s", "u")
	apiErr, ok := errors.AsType[*APIError](err)
	if !ok {
		t.Fatalf("err = %T, want *APIError", err)
	}
	runes := []rune(apiErr.Body)
	if len(runes) > maxErrorBodyLen+1 {
		t.Errorf("body length = %d, want ≤ %d", len(runes), maxErrorBodyLen+1)
	}
	if !strings.HasPrefix(apiErr.Body, strings.Repeat("x", maxErrorBodyLen)) {
		t.Errorf("body = %q, want the first 500 chars", apiErr.Body)
	}
}

func TestReasoningOnlyError(t *testing.T) {
	for _, field := range []string{"reasoning_content", "reasoning"} {
		t.Run(field, func(t *testing.T) {
			srv, _ := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":"","%s":"thinking"},"finish_reason":"stop"}]}`, field)
			})
			c := newTestClient(t, srv.URL, nil)
			_, _, err := c.Complete(t.Context(), "s", "u")
			if err == nil || !strings.Contains(err.Error(), "model returned only reasoning") ||
				!strings.Contains(err.Error(), "--max-output-tokens") {
				t.Errorf("err = %v, want reasoning-only hint", err)
			}
		})
	}
}

func TestEmptyContentError(t *testing.T) {
	srv, _ := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`)
	})
	c := newTestClient(t, srv.URL, nil)
	_, _, err := c.Complete(t.Context(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "empty content") {
		t.Errorf("err = %v, want empty-content error", err)
	}
}

func TestLengthFinishError(t *testing.T) {
	srv, _ := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"partial answer"},"finish_reason":"length"}],"usage":{"prompt_tokens":9,"completion_tokens":8}}`)
	})
	c := newTestClient(t, srv.URL, nil)
	_, _, err := c.Complete(t.Context(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "--max-output-tokens") {
		t.Errorf("err = %v, want length hint", err)
	}
}

func TestContextCancellation(t *testing.T) {
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	c := newTestClient(t, srv.URL, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, _, err := c.Complete(ctx, "s", "u")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if log.count() != 1 {
		t.Errorf("requests = %d, want 1 (no retry after cancellation)", log.count())
	}
}

func TestContextCancelDuringWait(t *testing.T) {
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "busy", http.StatusTooManyRequests)
	})
	c := newTestClient(t, srv.URL, func(o *Options) {
		o.Wait = func(context.Context, time.Duration) error { return context.Canceled }
	})
	_, _, err := c.Complete(t.Context(), "s", "u")
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if log.count() != 1 {
		t.Errorf("requests = %d, want 1", log.count())
	}
}

func TestInsecureHTTPRejected(t *testing.T) {
	_, err := New(Options{BaseURL: "http://api.example.com/v1", Model: "m", APIKey: "k"})
	if !errors.Is(err, ErrUsage) {
		t.Errorf("err = %v, want ErrUsage", err)
	}
	if _, err = New(Options{BaseURL: "http://localhost:11434/v1", Model: "m", APIKey: ""}); err != nil {
		t.Errorf("http on localhost: %v", err)
	}
	if _, err = New(Options{BaseURL: "https://api.example.com/v1/", Model: "m", APIKey: "k"}); err != nil {
		t.Errorf("https with trailing slash: %v", err)
	}
	if _, err = New(Options{BaseURL: "ftp://example.com", Model: "m", APIKey: "k"}); !errors.Is(err, ErrUsage) {
		t.Errorf("err = %v, want ErrUsage", err)
	}
}

func TestMissingKeyRejected(t *testing.T) {
	_, err := New(Options{BaseURL: "https://api.openai.com/v1", Model: "m"})
	if !errors.Is(err, ErrUsage) {
		t.Errorf("err = %v, want ErrUsage", err)
	}
	_, err = New(Options{BaseURL: "https://api.openai.com/v1", Model: ""})
	if !errors.Is(err, ErrUsage) {
		t.Errorf("missing model: err = %v, want ErrUsage", err)
	}
}

func TestLoopbackSendsNoAuthHeaderWithoutKey(t *testing.T) {
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) { writeOK(w, "ok") })
	c := newTestClient(t, srv.URL, func(o *Options) { o.APIKey = "" })
	if _, _, err := c.Complete(t.Context(), "s", "u"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := log.auth[0]; got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
}

func TestChatPreservesMessageOrder(t *testing.T) {
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) { writeOK(w, "ok") })
	c := newTestClient(t, srv.URL, nil)
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
	}
	if _, _, err := c.Chat(t.Context(), msgs); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	raw, ok := log.body(0)["messages"].([]any)
	if !ok || len(raw) != 4 {
		t.Fatalf("messages = %v", log.body(0)["messages"])
	}
	want := [][2]string{{"system", "sys"}, {"user", "u1"}, {"assistant", "a1"}, {"user", "u2"}}
	for i, w := range want {
		m := raw[i].(map[string]any)
		if m["role"] != w[0] || m["content"] != w[1] {
			t.Errorf("message %d = %v, want %v", i, m, w)
		}
	}
}

func TestChatValidatesMessages(t *testing.T) {
	c := newTestClient(t, "http://127.0.0.1:1/v1", nil)
	for _, msgs := range [][]Message{
		nil,
		{},
		{{Role: "root", Content: "x"}},
		{{Role: "", Content: "x"}},
		{{Role: "user", Content: "  "}},
	} {
		if _, _, err := c.Chat(t.Context(), msgs); !errors.Is(err, ErrUsage) {
			t.Errorf("Chat(%v): err = %v, want ErrUsage", msgs, err)
		}
	}
}

func TestChatCompleteEquivalence(t *testing.T) {
	srv, log := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) { writeOK(w, "ok") })
	c := newTestClient(t, srv.URL, nil)
	if _, _, err := c.Complete(t.Context(), "sys", "usr"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	raw, ok := log.body(0)["messages"].([]any)
	if !ok || len(raw) != 2 {
		t.Fatalf("messages = %v", log.body(0)["messages"])
	}
	first := raw[0].(map[string]any)
	second := raw[1].(map[string]any)
	if first["role"] != "system" || first["content"] != "sys" {
		t.Errorf("first = %v", first)
	}
	if second["role"] != "user" || second["content"] != "usr" {
		t.Errorf("second = %v", second)
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	// A redirect is returned to the caller as a plain non-200 instead of
	// being followed, so the Authorization header can never reach the
	// redirect target.
	srv, log := startServer(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			http.Redirect(w, &http.Request{URL: &url.URL{Scheme: "http", Host: "attacker.example"}}, "http://attacker.example/steal", http.StatusFound)
			return
		}
		writeOK(w, "leaked")
	})
	c := newTestClient(t, srv.URL, nil)
	_, _, err := c.Complete(t.Context(), "s", "u")
	apiErr, ok := errors.AsType[*APIError](err)
	if !ok {
		t.Fatalf("err = %T (%v), want *APIError for the 302 itself", err, err)
	}
	if apiErr.Status != http.StatusFound {
		t.Errorf("status = %d, want 302", apiErr.Status)
	}
	if log.count() != 1 {
		t.Errorf("requests = %d, want 1 (redirect not followed)", log.count())
	}
}

func TestProvidedClientGetsSafeRedirectPolicy(t *testing.T) {
	srv, log := startServer(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			http.Redirect(w, &http.Request{URL: &url.URL{Scheme: "http", Host: "x"}}, "/second", http.StatusTemporaryRedirect)
			return
		}
		writeOK(w, "followed")
	})
	// A caller-provided client with a nil CheckRedirect must still not
	// follow redirects.
	c := newTestClient(t, srv.URL, func(o *Options) { o.HTTPClient = &http.Client{} })
	_, _, err := c.Complete(t.Context(), "s", "u")
	apiErr, ok := errors.AsType[*APIError](err)
	if !ok || apiErr.Status != http.StatusTemporaryRedirect {
		t.Fatalf("err = %v, want the 307 as APIError", err)
	}
	if log.count() != 1 {
		t.Errorf("requests = %d, want 1", log.count())
	}
}

func TestOversizedBodyCapped(t *testing.T) {
	old := maxBodyRead
	maxBodyRead = 128
	t.Cleanup(func() { maxBodyRead = old })
	srv, _ := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"` + strings.Repeat("x", 400) + `"}]}`))
	})
	c := newTestClient(t, srv.URL, nil)
	_, _, err := c.Complete(t.Context(), "s", "u")
	if err == nil {
		t.Fatal("want a parse error for the truncated body")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("err = %v, want a truncation mention", err)
	}
}

func TestBodyAtCapNotTruncated(t *testing.T) {
	old := maxBodyRead
	maxBodyRead = 4096
	t.Cleanup(func() { maxBodyRead = old })
	srv, _ := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, strings.Repeat("y", 4000))
	})
	c := newTestClient(t, srv.URL, nil)
	text, _, err := c.Complete(t.Context(), "s", "u")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(text) != 4000 {
		t.Errorf("text len = %d, want 4000 (body exactly at cap must parse)", len(text))
	}
}

func TestIsLoopbackHost(t *testing.T) {
	positives := []string{"localhost", "LOCALHOST", "127.0.0.1", "127.255.1.2", "::1", "[::1]"}
	for _, h := range positives {
		if !IsLoopbackHost(h) {
			t.Errorf("IsLoopbackHost(%q) = false, want true", h)
		}
	}
	negatives := []string{
		"127.0.0.1.attacker.tld",
		"localhost.evil.com",
		"notlocalhost",
		"::2",
		"fe80::1%en0",
		"[fe80::1%25en0]",
		"10.0.0.1",
		"example.com",
		"",
		"2130706433",
	}
	for _, h := range negatives {
		if IsLoopbackHost(h) {
			t.Errorf("IsLoopbackHost(%q) = true, want false", h)
		}
	}
}

// TestLengthFinishIsErrTruncated lets callers detect a truncated
// completion with errors.Is and fall back to verbatim output.
func TestLengthFinishIsErrTruncated(t *testing.T) {
	srv, _ := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`)
	})
	c := newTestClient(t, srv.URL, nil)
	_, _, err := c.Complete(t.Context(), "s", "u")
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want errors.Is(err, ErrTruncated)", err)
	}
	if !strings.Contains(err.Error(), "--max-output-tokens") {
		t.Errorf("err = %v, want the raise hint", err)
	}
}
