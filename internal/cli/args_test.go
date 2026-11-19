package cli

import (
	"bytes"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"
)

func TestParseInterspersed(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		check   func(*testing.T, *Flags)
		wantErr bool
	}{
		{
			name: "flags before positional",
			args: []string{"--force", "-o", "out.md", "/proj"},
			check: func(t *testing.T, f *Flags) {
				if f.ProjectDir != "/proj" || !f.Force || f.Output != "out.md" {
					t.Errorf("got %+v", f)
				}
			},
		},
		{
			name: "flags after positional",
			args: []string{"/proj", "--llm", "--model", "m"},
			check: func(t *testing.T, f *Flags) {
				if f.ProjectDir != "/proj" || !f.LLM || f.Model != "m" {
					t.Errorf("got %+v", f)
				}
			},
		},
		{
			name: "flags on both sides",
			args: []string{"--quiet", "/proj", "-v"},
			check: func(t *testing.T, f *Flags) {
				if f.ProjectDir != "/proj" || !f.Quiet || !f.Verbose {
					t.Errorf("got %+v", f)
				}
			},
		},
		{
			name: "long output",
			args: []string{"--output", "out/MEMORY.md", "/proj"},
			check: func(t *testing.T, f *Flags) {
				if f.ProjectDir != "/proj" || f.Output != "out/MEMORY.md" {
					t.Errorf("got %+v", f)
				}
			},
		},
		{
			name: "list and no-subdirs",
			args: []string{"--list", "/proj", "--no-subdirs"},
			check: func(t *testing.T, f *Flags) {
				if f.ProjectDir != "/proj" || !f.List || !f.NoSubdirs {
					t.Errorf("got %+v", f)
				}
			},
		},
		{
			name: "config flags",
			args: []string{
				"--claude-dir", "/cc", "--config", "/c.toml",
				"--base-url", "https://example.test/v1", "--api-key-env", "KEY",
				"--thinking", "high", "--thinking-style", "none", "--extra-body", `{"a":1}`,
				"--max-part-tokens", "100", "--max-output-tokens", "7", "--timeout", "2m",
				"/proj",
			},
			check: func(t *testing.T, f *Flags) {
				if f.ProjectDir != "/proj" || f.ClaudeDir != "/cc" || f.ConfigPath != "/c.toml" {
					t.Errorf("got %+v", f)
				}
				if f.BaseURL != "https://example.test/v1" || f.APIKeyEnv != "KEY" {
					t.Errorf("got %+v", f)
				}
				if f.Thinking != "high" || f.ThinkingStyle != "none" || f.ExtraBody != `{"a":1}` {
					t.Errorf("got %+v", f)
				}
				if f.MaxPartTokens != 100 || f.MaxOutputTokens != 7 || f.Timeout != 2*time.Minute {
					t.Errorf("got %+v", f)
				}
			},
		},
		{
			name: "no args",
			args: nil,
			check: func(t *testing.T, f *Flags) {
				if f.ProjectDir != "" {
					t.Errorf("ProjectDir = %q, want empty", f.ProjectDir)
				}
			},
		},
		{
			name:    "second positional",
			args:    []string{"/one", "/two"},
			wantErr: true,
		},
		{
			name:    "positional after flags",
			args:    []string{"--force", "/one", "--list", "/two"},
			wantErr: true,
		},
		{
			name:    "unknown flag",
			args:    []string{"--nope", "/proj"},
			wantErr: true,
		},
		{
			name:    "bad duration",
			args:    []string{"--timeout", "soon", "/proj"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%v) = %+v, want error", tt.args, got)
				}
				if !errors.Is(err, ErrUsage) {
					t.Fatalf("Parse(%v) error %v, want ErrUsage", tt.args, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%v): %v", tt.args, err)
			}
			tt.check(t, got)
		})
	}
}

func TestParseDefaults(t *testing.T) {
	f, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.MaxPartTokens != DefaultMaxPartTokens {
		t.Errorf("MaxPartTokens = %d, want %d", f.MaxPartTokens, DefaultMaxPartTokens)
	}
	if f.MaxOutputTokens != 0 {
		t.Errorf("MaxOutputTokens = %d, want 0", f.MaxOutputTokens)
	}
	if f.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", f.Timeout, DefaultTimeout)
	}
	if f.Timeout != 10*time.Minute {
		t.Errorf("Timeout = %v, want 10m", f.Timeout)
	}
}

func TestHelpFlags(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"-h", "/proj"}, {"/proj", "--help"}} {
		f, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v): %v", args, err)
		}
		if !f.ShowHelp {
			t.Errorf("Parse(%v) did not set ShowHelp", args)
		}
	}
}

func TestShorthandCanonicalization(t *testing.T) {
	f, err := Parse([]string{"-q", "--quiet", "--verbose", "-v", "/proj"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !f.Quiet || !f.Verbose {
		t.Errorf("got %+v", f)
	}
	if got := f.Values(); len(got) != 0 {
		t.Errorf("Values() = %v, want empty: bool flags carry no config meaning", got)
	}
	if v, ok := f.Value("quiet"); ok || v != "" {
		t.Errorf("Value(quiet) = %q, %v, want unset", v, ok)
	}
}

func TestValueTracking(t *testing.T) {
	f, err := Parse([]string{"--model", "m", "-o", "out", "/p"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]string{"model": "m"}
	if got := f.Values(); !maps.Equal(got, want) {
		t.Errorf("Values() = %v, want %v", got, want)
	}
	if v, ok := f.Value("output"); !ok || v != "out" {
		t.Errorf("output = %q, %v, want out, true", v, ok)
	}
	if v, ok := f.Value("model"); !ok || v != "m" {
		t.Errorf("model = %q, %v, want m, true", v, ok)
	}
	if v, ok := f.Value("timeout"); ok || v != "" {
		t.Errorf("timeout = %q, %v, want unset", v, ok)
	}
	if _, ok := f.Values()["output"]; ok {
		t.Error("output is not config-backed but appears in Values")
	}
}

func TestValueTimeout(t *testing.T) {
	f, err := Parse([]string{"--timeout", "5m", "/p"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v, ok := f.Value("timeout"); !ok || v != "5m0s" {
		t.Errorf("timeout = %q, %v, want 5m0s, true", v, ok)
	}
}

func TestValueShortOutputOnly(t *testing.T) {
	f, err := Parse([]string{"-o", "x", "/p"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := f.Values(); len(got) != 0 {
		t.Errorf("Values() = %v, want empty", got)
	}
	if v, ok := f.Value("output"); !ok || v != "x" {
		t.Errorf("output = %q, %v, want x, true", v, ok)
	}
}

func TestPrintUsage(t *testing.T) {
	var buf bytes.Buffer
	PrintUsage(&buf)
	got := buf.String()
	if !strings.HasPrefix(got, "claude-code-memory-takeout [flags] [PROJECT_DIR]\n") {
		t.Errorf("usage starts with %q", got[:60])
	}
	if strings.HasSuffix(got, "\n\n") {
		t.Error("usage has a trailing blank line")
	}
	if !strings.HasSuffix(got, "130 cancelled.\n") {
		t.Errorf("usage ends with %q", got[len(got)-40:])
	}
}
