package config

import (
	"strings"
	"testing"
)

func TestParseTOMLSchema(t *testing.T) {
	src := `
# comment
base_url = "https://openrouter.ai/api/v1"   # trailing comment
model = 'some/model'
thinking = "medium"
thinking_style = "auto"
api_key_env = "OPENROUTER_API_KEY"
max_part_tokens = 24000
max_output_tokens = 8000
timeout = "10m"
redact_patterns = ['(?i)internal-token-[a-z0-9]{20}', 'second']
extra_body = { temperature = 0.2, top_p = 0.9, flag = true, note = "x=y" }

[dirs]
claude = "~/.claude"

unknown_key = "ignored"
[unknown_table]
x = 1
`
	doc, err := parseTOML(src)
	if err != nil {
		t.Fatalf("parseTOML: %v", err)
	}
	if doc.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("base_url = %v", doc.BaseURL)
	}
	if doc.Model != "some/model" {
		t.Errorf("model = %v", doc.Model)
	}
	if doc.MaxPartTokens == nil || *doc.MaxPartTokens != 24000 {
		t.Errorf("max_part_tokens = %v", doc.MaxPartTokens)
	}
	if doc.MaxOutputTokens == nil || *doc.MaxOutputTokens != 8000 {
		t.Errorf("max_output_tokens = %v", doc.MaxOutputTokens)
	}
	if len(doc.RedactPatterns) != 2 || doc.RedactPatterns[0] != "(?i)internal-token-[a-z0-9]{20}" {
		t.Errorf("redact_patterns = %#v", doc.RedactPatterns)
	}
	extra := doc.ExtraBody
	if extra["temperature"] != float64(0.2) || extra["top_p"] != float64(0.9) ||
		extra["flag"] != true || extra["note"] != "x=y" {
		t.Errorf("extra_body = %#v", extra)
	}
	if doc.Dirs.Claude != "~/.claude" {
		t.Errorf("dirs = %#v", doc.Dirs)
	}
}

func TestParseTOMLEscapes(t *testing.T) {
	doc, err := parseTOML(`base_url = "tab\there\nand \"quoted\" and \\ done"
model = 'no \escapes \d'
`)
	if err != nil {
		t.Fatalf("parseTOML: %v", err)
	}
	if doc.BaseURL != "tab\there\nand \"quoted\" and \\ done" {
		t.Errorf("base_url = %q", doc.BaseURL)
	}
	if doc.Model != `no \escapes \d` {
		t.Errorf("model = %q", doc.Model)
	}
}

func TestParseTOMLErrors(t *testing.T) {
	tests := []struct{ src, want string }{
		{"key\n", "expected '.' or '='"},
		{"key = \n", "expected value"},
		{"key = \"unterminated\n", "newlines"},
		{"key = [\"a\", \"b\"\n", "array terminator"},
		{"key = { a = 1\n", "terminator"},
		{"key = whatisthis\n", "expected value"},
		{"[unclosed\n", "table name"},
		{"timeout = 5\n", "incompatible types"},
		{"max_part_tokens = \"lots\"\n", "incompatible types"},
		{"max_part_tokens = [1]\n", "incompatible types"},
		{"[dirs]\nclaude = 5\n", "incompatible types"},
		{"extra_body = 3\n", "extra_body must be a table"},
		{"extra_body = \"x\"\n", "extra_body must be a table"},
	}
	for _, tt := range tests {
		_, err := parseTOML(tt.src)
		if err == nil {
			t.Errorf("parseTOML(%q) = nil error, want %q", tt.src, tt.want)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("parseTOML(%q) = %q, want it to contain %q", tt.src, err, tt.want)
		}
	}
}

func TestParseTOMLAcceptsDottedAndEmptyExtraBody(t *testing.T) {
	doc, err := parseTOML("a.b.c = 1\nextra_body = {}\n")
	if err != nil {
		t.Fatalf("parseTOML: %v", err)
	}
	if doc.ExtraBody == nil {
		t.Error("empty extra_body table must decode to a non-nil map")
	}
}
