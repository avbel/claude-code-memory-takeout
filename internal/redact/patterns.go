package redact

import "regexp"

type ruleDef struct {
	pattern string
	kind    string
	repl    string
}

func marker(kind string) string {
	return "[REDACTED:" + kind + "]"
}

func compileRules(defs []ruleDef) []rule {
	rules := make([]rule, len(defs))
	for i, d := range defs {
		repl := d.repl
		if repl == "" {
			repl = marker(d.kind)
		}
		rules[i] = rule{re: regexp.MustCompile(d.pattern), repl: repl, kind: d.kind}
	}
	return rules
}

// builtin runs in this order: the key-block rules first, the token
// patterns next, then bearer/URL-credential/basic-auth/curl forms, and the
// assignment rule last among the shaped ones so it does not eat their
// output. The private-key fallback must come right after the full-block
// rule: it redacts a truncated key that has no END line.
var builtin = compileRules([]ruleDef{
	{pattern: `(?s)-----BEGIN [A-Z ]*PRIVATE KEY(?: BLOCK)?-----.*?-----END [A-Z ]*PRIVATE KEY(?: BLOCK)?-----`, kind: "pem_key"},
	{pattern: `(?s)-----BEGIN [A-Z ]*PRIVATE KEY(?: BLOCK)?-----.*`, kind: "pem_key"},
	{pattern: `\b(AKIA|ASIA)[0-9A-Z]{16}\b`, kind: "aws_key"},
	{pattern: `\bgh[pousr]_[A-Za-z0-9]{36,}\b`, kind: "github_token"},
	{pattern: `\bgithub_pat_[A-Za-z0-9_]{22,}\b`, kind: "github_token"},
	{pattern: `\bsk-[A-Za-z0-9_-]{20,}`, kind: "openai_key"},
	{pattern: `\bxox[abprs]-[A-Za-z0-9-]{10,}`, kind: "slack_token"},
	{pattern: `\bAIza[0-9A-Za-z_-]{35}\b`, kind: "google_api_key"},
	{pattern: `\b(sk|rk)_(live|test)_[0-9A-Za-z]{16,}\b`, kind: "stripe_key"},
	{pattern: `\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`, kind: "jwt"},
	{pattern: `\bydb_[0-9a-f]{32,}\b`, kind: "yrem_token"},
	{pattern: `https://(discord(app)?\.com/api/webhooks|hooks\.slack\.com/services)/\S+`, kind: "webhook_url"},
	// Greedy up to the last @ before whitespace, so a password containing
	// / or @ is covered: postgres://u:pa/ss@h → postgres://[REDACTED]@h.
	// The run stops at quotes so adjacent JSON fields are not swallowed;
	// an empty username (redis://:pw@host) still matches.
	{pattern: `(?i)\b([a-z][a-z0-9+.-]*://)[^\s"'\x60]*@`, kind: "url_credential", repl: `$1[REDACTED]@`},
	{pattern: `(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]{16,}`, kind: "bearer", repl: `$1 [REDACTED]`},
	// Keeps the "authorization: basic [REDACTED]" shape via the capture.
	{pattern: `(?i)\b(authorization\\?["']?\s*[:=]\s*\\?["']?basic\s+)[A-Za-z0-9+/=]{8,}`, kind: "basic_auth", repl: `$1[REDACTED]`},
	// curl -u user:pass / --user user:pass; the argument must contain a
	// colon so an unrelated flag argument like `sort -u names.txt` is left
	// alone.
	{pattern: `(?i)((?:^|\s)(?:-u|--user)\s+)\\?["']?[^\s"',]*:[^\s"',]+\\?["']?`, kind: "curl_user", repl: `$1[REDACTED]`},
	// \x5c? before each quote lets JSON-compact tool input match too:
	// {\"password\":\"hunter2\"}. The quoted branch allows spaces inside
	// the value (correct horse battery staple).
	{pattern: `(?i)\b([A-Z0-9_]*(?:SECRET|TOKEN|PASSWORD|PASSWD|API[-_]?KEY|PRIVATE[-_]?KEY|ACCESS[-_]?KEY|CREDENTIALS?)[A-Z0-9_]*)\x5c?["']?(\s*[:=]\s*)(?:\x5c?["']([^"'\n]{6,})["']|\x5c?["']?[^\s"',\x5c]{6,}\x5c?["']?)`, kind: "assignment", repl: `$1$2[REDACTED]`},
	{pattern: `\bglpat-[A-Za-z0-9_-]{20,}`, kind: "gitlab_token"},
	{pattern: `\bhf_[A-Za-z0-9]{34}\b`, kind: "hf_token"},
	{pattern: `\bSG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}\b`, kind: "sendgrid_key"},
	{pattern: `\bnpm_[A-Za-z0-9]{36}\b`, kind: "npm_token"},
	{pattern: `\bxapp-[A-Za-z0-9-]{10,}`, kind: "slack_app_token"},
	{pattern: `\b\d{8,10}:[A-Za-z0-9_-]{35}\b`, kind: "telegram_bot_token"},
})
