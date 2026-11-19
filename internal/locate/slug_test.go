package locate

import "testing"

func TestEncodeGolden(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/Users/avbel/Projects/fluidd", "-Users-avbel-Projects-fluidd"},
		{"/Users/avbel/Projects/fluidd/.claude/worktrees/ebay-keyset-pool",
			"-Users-avbel-Projects-fluidd--claude-worktrees-ebay-keyset-pool"},
		{"/private/tmp/claude-501/-Users-avbel-Projects-yrem/70b3bf2d-5b67-435a-8dac-e3bda68e965c/scratchpad",
			"-private-tmp-claude-501--Users-avbel-Projects-yrem-70b3bf2d-5b67-435a-8dac-e3bda68e965c-scratchpad"},
		{`C:\Users\me\proj`, "C--Users-me-proj"},
		{"A1b2", "A1b2"},
	}
	for _, tc := range cases {
		if got := Encode(tc.path); got != tc.want {
			t.Errorf("Encode(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestEncodeNonASCII documents the chosen behaviour for runes the machine
// data never showed: one rune outside the ASCII range encodes as one "-",
// except runes beyond the BMP, which are two UTF-16 units in Claude Code's
// JavaScript and encode as "--".
func TestEncodeNonASCII(t *testing.T) {
	if got := Encode("café"); got != "caf-" {
		t.Errorf("Encode(%q) = %q, want %q", "café", got, "caf-")
	}
	if got := Encode("a\u00e9b"); got != "a-b" {
		t.Errorf("Encode with U+00E9 = %q, want %q", got, "a-b")
	}
	if got := Encode("a\U0001F600b"); got != "a--b" {
		t.Errorf("Encode with U+1F600 = %q, want %q", got, "a--b")
	}
}
