package llm

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestResolveStyle(t *testing.T) {
	cases := []struct {
		style ThinkingStyle
		host  string
		want  ThinkingStyle
	}{
		{StyleAuto, "openrouter.ai", StyleOpenRouter},
		{StyleAuto, "api.openrouter.ai", StyleOpenRouter},
		{StyleAuto, "api.openai.com", StyleOpenAI},
		{StyleAuto, "127.0.0.1", StyleOpenAI},
		{StyleOpenAI, "openrouter.ai", StyleOpenAI},
		{StyleOpenRouter, "api.openai.com", StyleOpenRouter},
		{StyleNone, "openrouter.ai", StyleNone},
	}
	for _, tc := range cases {
		if got := resolveStyle(tc.style, tc.host); got != tc.want {
			t.Errorf("resolveStyle(%q, %q) = %q, want %q", tc.style, tc.host, got, tc.want)
		}
	}
}

func TestApplyThinking(t *testing.T) {
	cases := []struct {
		name  string
		style ThinkingStyle
		level string
		want  map[string]any
	}{
		{"openai high", StyleOpenAI, "high", map[string]any{"reasoning_effort": "high"}},
		{"openai minimal", StyleOpenAI, "minimal", map[string]any{"reasoning_effort": "minimal"}},
		{"openai xhigh", StyleOpenAI, "xhigh", map[string]any{"reasoning_effort": "xhigh"}},
		{"openai off omits", StyleOpenAI, "off", nil},
		{"openai unset omits", StyleOpenAI, "", nil},
		{"openrouter high", StyleOpenRouter, "high", map[string]any{"reasoning": map[string]any{"effort": "high"}}},
		{"openrouter off disables", StyleOpenRouter, "off", map[string]any{"reasoning": map[string]any{"enabled": false}}},
		{"openrouter unset omits", StyleOpenRouter, "", nil},
		{"none never sends", StyleNone, "high", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"model": "m", "stream": false}
			applyThinking(body, tc.style, tc.level)
			for k, v := range tc.want {
				if got, ok := body[k]; !ok || !equalAny(got, v) {
					t.Errorf("body[%q] = %#v, want %#v", k, got, v)
				}
			}
			for k := range body {
				if k == "model" || k == "stream" {
					continue
				}
				if _, ok := tc.want[k]; !ok {
					t.Errorf("unexpected body key %q", k)
				}
			}
		})
	}
}

func TestUnknownStyleOrLevelRejected(t *testing.T) {
	srv, _ := startServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) { writeOK(w, "ok") })
	_, err := New(Options{BaseURL: srv.URL, Model: "m", ThinkingStyle: "bogus"})
	if !errors.Is(err, ErrUsage) {
		t.Errorf("style: err = %v, want ErrUsage", err)
	}
	_, err = New(Options{BaseURL: srv.URL, Model: "m", ThinkingLevel: "super"})
	if !errors.Is(err, ErrUsage) {
		t.Errorf("level: err = %v, want ErrUsage", err)
	}
	_, err = New(Options{BaseURL: srv.URL, Model: "m", ThinkingStyle: "", ThinkingLevel: "high"})
	if err != nil {
		t.Errorf("auto style with level: %v", err)
	}
}

// equalAny compares two decoded JSON values by re-encoding them.
func equalAny(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}
