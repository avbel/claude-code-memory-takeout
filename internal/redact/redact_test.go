package redact

import (
	"path/filepath"
	"strings"
	"testing"
)

func newTestRedactor(t *testing.T, patterns ...string) *Redactor {
	t.Helper()
	r, err := New(WithPatterns(patterns))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

// setHome points the home-dir lookup at dir on every OS: os.UserHomeDir
// reads USERPROFILE on Windows and HOME elsewhere, so both are set.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func TestBuiltinPatterns(t *testing.T) {
	cases := []struct {
		name string
		kind string
		pos  string
		want string
		neg  string
	}{
		{
			name: "pem_key",
			kind: "pem_key",
			pos: "-----BEGIN " + "OPENSSH PRIVATE KEY" + "-----\n" +
				strings.Repeat("b3Bh", 40) + "\n-----END " + "OPENSSH PRIVATE KEY" + "-----",
			want: "[REDACTED:pem_key]",
			neg:  "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----",
		},
		{
			name: "aws_key",
			kind: "aws_key",
			pos:  "key " + "AKIA" + "ABCDEFGHIJKLMNOP" + " end",
			want: "[REDACTED:aws_key]",
			neg:  "key " + "AKIA" + "ABCDEFGHIJKLMNO" + " end",
		},
		{
			name: "github_token",
			kind: "github_token",
			pos:  "token " + "ghp_" + strings.Repeat("A1", 18),
			want: "[REDACTED:github_token]",
			neg:  "token " + "ghp_" + strings.Repeat("A1", 17) + "A",
		},
		{
			name: "github_fine_grained",
			kind: "github_token",
			pos:  "token " + "github_pat_" + "Ab1_" + strings.Repeat("x", 18),
			want: "[REDACTED:github_token]",
			neg:  "token " + "github_pat_" + "Ab1_" + strings.Repeat("x", 17),
		},
		{
			name: "openai_key",
			kind: "openai_key",
			pos:  "key " + "sk-" + strings.Repeat("aB9_-", 4),
			want: "[REDACTED:openai_key]",
			neg:  "key " + "sk-" + strings.Repeat("a", 19),
		},
		{
			name: "slack_token",
			kind: "slack_token",
			pos:  "token " + "xoxb-" + strings.Repeat("a1-", 4),
			want: "[REDACTED:slack_token]",
			neg:  "token " + "xoxb-" + strings.Repeat("a", 9),
		},
		{
			name: "google_api_key",
			kind: "google_api_key",
			pos:  "key " + "AIza" + strings.Repeat("0Az-_", 7),
			want: "[REDACTED:google_api_key]",
			neg:  "key " + "AIza" + strings.Repeat("0", 34),
		},
		{
			name: "stripe_key",
			kind: "stripe_key",
			pos:  "key " + "sk_" + "live_" + strings.Repeat("9f", 8),
			want: "[REDACTED:stripe_key]",
			neg:  "key " + "sk_" + "prod_" + strings.Repeat("9f", 8),
		},
		{
			name: "jwt",
			kind: "jwt",
			pos:  "eyJ" + strings.Repeat("e30", 4) + "." + strings.Repeat("pay", 4) + "." + strings.Repeat("sig", 4),
			want: "[REDACTED:jwt]",
			neg:  "eyJ" + strings.Repeat("e30", 4) + "." + strings.Repeat("pay", 4),
		},
		{
			name: "yrem_token",
			kind: "yrem_token",
			pos:  "token ydb_" + strings.Repeat("ab", 16) + " end",
			want: "[REDACTED:yrem_token]",
			neg:  "token ydb_" + strings.Repeat("ab", 15) + "z end",
		},
		{
			name: "webhook_url",
			kind: "webhook_url",
			pos: "see https://hooks." + "slack.com/services/" + "T00/B00/xxxxx and https://discord" +
				".com/api/webhooks/" + "1234567890/" + strings.Repeat("Ab", 10),
			want: "[REDACTED:webhook_url]",
			neg:  "see https://discord.com/api/channels/123 and https://example.com/services/x",
		},
		{
			name: "url_credential",
			kind: "url_credential",
			pos:  "postgres://" + "user" + ":" + "secret" + "@db.example.com:5432/app",
			want: "postgres://[REDACTED]@db.example.com:5432/app",
			neg:  "postgres://db.example.com:5432/app",
		},
		{
			name: "url_credential_empty_username",
			kind: "url_credential",
			pos:  "redis://" + ":" + "hunter2" + "@cache.example.com:6379/0",
			want: "redis://[REDACTED]@cache.example.com:6379/0",
			neg:  "redis://cache.example.com:6379/0",
		},
		{
			name: "url_credential_slash_password",
			kind: "url_credential",
			pos:  "postgres://u:pa" + "/ss" + "@h.example.com/db",
			want: "postgres://[REDACTED]@h.example.com/db",
			neg:  "see https://example.com/docs/page.html for details",
		},
		{
			name: "url_credential_at_password",
			kind: "url_credential",
			pos:  "postgres://u:pa" + "@ss" + "@h.example.com/db",
			want: "postgres://[REDACTED]@h.example.com/db",
			neg:  "clone git@github.com:avbel/demo.git",
		},
		{
			name: "url_credential_uppercase_scheme",
			kind: "url_credential",
			pos:  "POSTGRES://" + "user" + ":" + "secret1" + "@db.example.com/app",
			want: "POSTGRES://[REDACTED]@db.example.com/app",
			neg:  "POSTGRES://db.example.com/app",
		},
		{
			name: "basic_auth",
			kind: "basic_auth",
			pos:  "Authorization: Basic " + "dXNlcjpw" + "YXNzMTE" + "=",
			want: "Authorization: Basic [REDACTED]",
			neg:  "authorization: basic abc",
		},
		{
			name: "basic_auth_json",
			kind: "basic_auth",
			pos:  `{\"authorization\":\"Basic ` + "dXNlcjpw" + "YXNzMTE" + `\"}`,
			want: `Basic [REDACTED]`,
			neg:  `{\"authorization\":\"Basic\"}`,
		},
		{
			name: "curl_user",
			kind: "curl_user",
			pos:  "curl -u admin:" + "hunter2" + " https://api.example.com",
			want: "curl -u [REDACTED] https://api.example.com",
			neg:  "sort -u names.txt",
		},
		{
			name: "curl_user_quoted",
			kind: "curl_user",
			pos:  `curl --user 'admin:` + "hunter2" + `'`,
			want: "curl --user [REDACTED]",
			neg:  "curl --user",
		},
		{
			name: "bearer",
			kind: "bearer",
			pos:  "Authorization: Bearer " + strings.Repeat("t0k", 6),
			want: "Authorization: Bearer [REDACTED]",
			neg:  "Authorization: Bearer " + strings.Repeat("a", 15),
		},
		{
			name: "assignment",
			kind: "assignment",
			pos:  `MY_SECRET_TOKEN = "abc123def456"`,
			want: "MY_SECRET_TOKEN = [REDACTED]",
			neg:  "MY_TOKEN=abc",
		},
		{
			name: "assignment_json",
			kind: "assignment",
			pos:  `"api_key": "value1234567"`,
			want: `"api_key: [REDACTED]`,
			neg:  `"api_key": "abc"`,
		},
		{
			name: "assignment_json_compact",
			kind: "assignment",
			pos:  `{\"password\":\"hunter2\"}`,
			want: `{\"password:[REDACTED]}`,
			neg:  `{\"password\":\"ab\"}`,
		},
		{
			name: "assignment_quoted_spaces",
			kind: "assignment",
			pos:  `password: "correct horse battery staple"`,
			want: `password: [REDACTED]`,
			neg:  `password: "a b"`,
		},
		{
			name: "assignment_api_key_dashed",
			kind: "assignment",
			pos:  `"x-api-key": "abcdefgh12345678"`,
			want: `"x-api-key: [REDACTED]`,
			neg:  `"x-api-key": "abc"`,
		},
		{
			name: "pem_key_truncated",
			kind: "pem_key",
			pos: "-----BEGIN " + "OPENSSH PRIVATE KEY" + "-----\n" +
				strings.Repeat("b3Bh", 40),
			want: "[REDACTED:pem_key]",
			neg:  "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----",
		},
		{
			name: "pgp_key_block",
			kind: "pem_key",
			pos: "-----BEGIN " + "PGP PRIVATE KEY BLOCK" + "-----\n" +
				strings.Repeat("lQ0Y", 20) + "\n-----END " + "PGP PRIVATE KEY BLOCK" + "-----",
			want: "[REDACTED:pem_key]",
			neg:  "-----BEGIN PGP SIGNATURE-----\nabc\n-----END PGP SIGNATURE-----",
		},
		{
			name: "pgp_key_truncated",
			kind: "pem_key",
			pos:  "-----BEGIN " + "PGP PRIVATE KEY BLOCK" + "-----\n" + strings.Repeat("lQ0Y", 20),
			want: "[REDACTED:pem_key]",
			neg:  "-----BEGIN PGP MESSAGE-----\nabc\n-----END PGP MESSAGE-----",
		},
		{
			name: "gitlab_token",
			kind: "gitlab_token",
			pos:  "token " + "glpat-" + strings.Repeat("K3", 10),
			want: "[REDACTED:gitlab_token]",
			neg:  "token " + "glpat-" + strings.Repeat("K", 19),
		},
		{
			name: "npm_token",
			kind: "npm_token",
			pos:  "token " + "npm_" + strings.Repeat("d7", 18),
			want: "[REDACTED:npm_token]",
			neg:  "token " + "npm_" + strings.Repeat("d7", 17) + "d",
		},
		{
			name: "slack_app_token",
			kind: "slack_app_token",
			pos:  "token " + "xapp-" + strings.Repeat("q1-", 4),
			want: "[REDACTED:slack_app_token]",
			neg:  "token " + "xapp-" + strings.Repeat("q", 9),
		},
		{
			name: "hf_token",
			kind: "hf_token",
			pos:  "key hf_" + strings.Repeat("aB9", 11) + "c end",
			want: "[REDACTED:hf_token]",
			neg:  "key hf_" + strings.Repeat("a", 33),
		},
		{
			name: "sendgrid_key",
			kind: "sendgrid_key",
			pos:  "key SG." + strings.Repeat("a", 22) + "." + strings.Repeat("b", 43) + " end",
			want: "[REDACTED:sendgrid_key]",
			neg:  "key SG." + strings.Repeat("a", 21) + "." + strings.Repeat("b", 43),
		},
		{
			name: "telegram_bot_token",
			kind: "telegram_bot_token",
			pos:  "token 12345678:" + strings.Repeat("zA4_", 8) + "zzz",
			want: "[REDACTED:telegram_bot_token]",
			neg:  "token 1234567:" + strings.Repeat("z", 35),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRedactor(t)
			got := r.Apply(tc.pos)
			if !strings.Contains(got.Text, tc.want) {
				t.Errorf("positive: got %q, want it to contain %q", got.Text, tc.want)
			}
			if got.Counts[tc.kind] < 1 {
				t.Errorf("positive: counts[%s] = %d, want >= 1", tc.kind, got.Counts[tc.kind])
			}
			clean := r.Apply(tc.neg)
			if clean.Text != tc.neg {
				t.Errorf("negative: got %q, want unchanged %q", clean.Text, tc.neg)
			}
			if clean.Total() != 0 {
				t.Errorf("negative: counts = %v, want no matches", clean.Counts)
			}
		})
	}
}

func TestUserPatterns(t *testing.T) {
	r := newTestRedactor(t, `internal-token-[a-z0-9]{20}`)
	in := "got internal-token-" + "abcdefghijklmnopqrst" + " back"
	got := r.Apply(in)
	if want := "got [REDACTED:user] back"; got.Text != want {
		t.Errorf("got %q, want %q", got.Text, want)
	}
	if got.Counts["user"] != 1 {
		t.Errorf("counts[user] = %d, want 1", got.Counts["user"])
	}
}

func TestUserPatternOptionsAppend(t *testing.T) {
	r, err := New(WithPatterns([]string{`zzz-secret-[a-z]{3}`}), WithPatterns([]string{`qqq-token-[a-z]{3}`}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := r.Apply("zzz-secret-abc qqq-token-def")
	if got.Counts["user"] != 2 {
		t.Errorf("counts[user] = %d, want 2", got.Counts["user"])
	}
}

func TestInvalidUserPattern(t *testing.T) {
	_, err := New(WithPatterns([]string{"[unclosed"}))
	if err == nil {
		t.Fatal("want an error for an invalid pattern")
	}
	if !strings.Contains(err.Error(), "redact pattern") {
		t.Errorf("error = %v, want context about the pattern", err)
	}
}

func TestHomeDirReplaced(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	r := newTestRedactor(t)
	got := r.Apply(home + "/projects/x and " + home + "/logs")
	if want := "~/projects/x and ~/logs"; got.Text != want {
		t.Errorf("got %q, want %q", got.Text, want)
	}
	if got.Counts["home"] != 2 {
		t.Errorf("counts[home] = %d, want 2", got.Counts["home"])
	}
}

// TestHomeDirNativeSeparators folds paths spelled with the OS's own
// separator (filepath.Join) and, on Windows, with the other convention too:
// Unix-style tools emit "C:/Users/…" while the OS home is "C:\Users\…".
func TestHomeDirNativeSeparators(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	r := newTestRedactor(t)
	got := r.Apply(filepath.Join(home, "logs") + " then " + filepath.ToSlash(home) + "/logs")
	if want := "~/logs then ~/logs"; got.Text != want {
		t.Errorf("got %q, want %q", got.Text, want)
	}
	if got.Counts["home"] != 2 {
		t.Errorf("counts[home] = %d, want 2", got.Counts["home"])
	}
}

func TestHomeDirPathBoundary(t *testing.T) {
	setHome(t, "/Users/al")
	r := newTestRedactor(t)
	got := r.Apply("/Users/alice visits /Users/al/x, /Users/al end and /Users/al.txt")
	want := "/Users/alice visits ~/x, ~ end and /Users/al.txt"
	if got.Text != want {
		t.Errorf("got %q, want %q", got.Text, want)
	}
}

// TestHomeDirPathBoundaryTempDir is the boundary test built from this
// machine's home instead of a literal: a same-prefix longer sibling keeps
// its name, everything directly under home folds, and counts match.
func TestHomeDirPathBoundaryTempDir(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	r := newTestRedactor(t)
	sibling := home + "x" // same prefix, longer name
	in := sibling + "/v and " + filepath.Join(home, "x") + ", " + home + " end and " + home + ".txt"
	got := r.Apply(in)
	want := sibling + "/v and ~/x, ~ end and " + home + ".txt"
	if got.Text != want {
		t.Errorf("got %q, want %q", got.Text, want)
	}
	if got.Counts["home"] != 2 {
		t.Errorf("counts[home] = %d, want 2", got.Counts["home"])
	}
}

func TestTotalExcludesHome(t *testing.T) {
	setHome(t, "/Users/al")
	r := newTestRedactor(t)
	got := r.Apply("/Users/al/a and /Users/al/b")
	if got.Counts["home"] != 2 {
		t.Errorf("counts[home] = %d, want 2", got.Counts["home"])
	}
	if got.Total() != 0 {
		t.Errorf("Total = %d, want 0: folding a path is not a secret", got.Total())
	}
}

func TestHomeRootSkipped(t *testing.T) {
	// HOME=/ must not be applied: every "/" would fold to "~" and no URL
	// rule could ever match again (the Slack webhook would leak).
	setHome(t, "/")
	r := newTestRedactor(t)
	in := "webhook https://hooks." + "slack.com/services/" + "T00/B00/x1y2z3"
	got := r.Apply(in)
	if !strings.Contains(got.Text, "[REDACTED:webhook_url]") {
		t.Errorf("got %q, want the webhook redacted even with HOME=/", got.Text)
	}
	if strings.ContainsRune(got.Text, '~') {
		t.Errorf("got %q, want no fold with HOME=/", got.Text)
	}
}

func TestApplyCountsAndTotal(t *testing.T) {
	r := newTestRedactor(t)
	in := "AKIA" + "ABCDEFGHIJKLMNOP" + " and " + "sk-" + strings.Repeat("a", 20) +
		" and " + "AKIA" + "QRSTUVWXYZ012345"
	got := r.Apply(in)
	if got.Counts["aws_key"] != 2 {
		t.Errorf("counts[aws_key] = %d, want 2", got.Counts["aws_key"])
	}
	if got.Counts["openai_key"] != 1 {
		t.Errorf("counts[openai_key] = %d, want 1", got.Counts["openai_key"])
	}
	if got.Total() != 3 {
		t.Errorf("Total = %d, want 3", got.Total())
	}
}

func TestApplyEmpty(t *testing.T) {
	r := newTestRedactor(t)
	got := r.Apply("")
	if got.Text != "" {
		t.Errorf("Text = %q, want empty", got.Text)
	}
	if got.Total() != 0 {
		t.Errorf("Total = %d, want 0", got.Total())
	}
}

// TestAssignmentTOML proves the assignment rule covers TOML-style
// `token = "..."` lines, not just JSON/colon forms.
func TestAssignmentTOML(t *testing.T) {
	r := newTestRedactor(t)
	got := r.Apply(`token = "hunter2secret"`)
	if want := `token = [REDACTED]`; got.Text != want {
		t.Errorf("got %q, want %q", got.Text, want)
	}
	if got.Counts["assignment"] != 1 {
		t.Errorf("counts[assignment] = %d, want 1", got.Counts["assignment"])
	}
}
