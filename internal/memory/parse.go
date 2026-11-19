package memory

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"
)

// indexName is the memory dir's index file; it holds pointers, not facts.
const indexName = "MEMORY.md"

// MaxFactBytes caps one fact file. The largest real memory file observed
// is ~230 KB; anything over 4 MiB is a log dump or a prompt-injected
// artifact, not a fact, and reading it whole would waste memory and, in
// --llm mode, post it uncapped to the endpoint.
const MaxFactBytes = 4 << 20

// ParseFile reads one memory file into a Fact. A malformed file is a
// warning, never a fatal error: the result then carries the fallback shape
// (the whole file as the body, Name from the file stem) or no fact at all.
func ParseFile(path string) ParseResult {
	base := filepath.Base(path)
	if strings.EqualFold(base, indexName) {
		return ParseResult{}
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > MaxFactBytes {
		return ParseResult{Warning: fmt.Sprintf("file is %d MiB, above the %d MiB cap; not read",
			fi.Size()>>20, MaxFactBytes>>20)}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ParseResult{Warning: "read: " + err.Error()}
	}
	text := normalize(string(data))
	stem := strings.TrimSuffix(base, ".md")

	fm, body, rest, hasFM := splitFrontmatter(text)
	if rest != "" { // `---` opened but never closed
		return fallback(stem, text, "frontmatter with no closing ---")
	}
	name, description, kind, modified := stem, "", "", time.Time{}
	if hasFM {
		parsed, err := parseFrontmatter(fm)
		if err != nil {
			return fallback(stem, text, "parse frontmatter: "+err.Error())
		}
		name = cmp.Or(strings.TrimSpace(parsed.Name), stem)
		description = parsed.Description
		kind = strings.ToLower(cmp.Or(parsed.Metadata.Type, parsed.Type))
		modified = parseModified(parsed.Metadata.Modified)
	}
	body = strings.TrimSpace(body)
	if description == "" && body == "" {
		return ParseResult{Warning: "no description and no body"}
	}
	return ParseResult{
		Fact: Fact{
			Name:        name,
			Description: description,
			Type:        kind,
			Body:        body,
			File:        base,
			Modified:    modified,
		},
		OK: true,
	}
}

// fallback keeps an unreadable-as-frontmatter file as a body-only fact.
func fallback(stem, text, warning string) ParseResult {
	body := strings.TrimSpace(text)
	if body == "" {
		return ParseResult{Warning: warning + "; file is empty"}
	}
	return ParseResult{
		Fact:    Fact{Name: stem, Type: "", Body: body, File: stem + ".md"},
		OK:      true,
		Warning: warning,
	}
}

// normalize prepares Windows-written or BOM-prefixed text: CRLF folds to
// LF and a leading UTF-8 BOM is dropped, so the `---` checks work.
func normalize(text string) string {
	text = strings.TrimPrefix(text, "\uFEFF")
	return strings.ReplaceAll(text, "\r\n", "\n")
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
	Metadata    struct {
		Type     string `yaml:"type"`
		Modified string `yaml:"modified"`
	} `yaml:"metadata"`
}

func parseFrontmatter(fm string) (frontmatter, error) {
	var out frontmatter
	// Unknown keys (originSessionId, node_type, …) are ignored by design.
	if err := yaml.Unmarshal([]byte(fm), &out); err != nil {
		return frontmatter{}, err
	}
	return out, nil
}

// parseModified reads metadata.modified, an RFC 3339 timestamp with
// milliseconds. A bad value is a zero time without a warning: the date is
// only ever an LLM hint.
func parseModified(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

// splitFrontmatter separates the `---`-delimited frontmatter from the
// body; the delimiters must be exactly `---`. rest carries the raw text
// when the opening `---` has no closing delimiter, so the caller can
// fall back with a warning.
func splitFrontmatter(text string) (fm, body, rest string, hasFM bool) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return "", text, "", false
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			return strings.Join(lines[1:i], "\n"),
				strings.Join(lines[i+1:], "\n"),
				"",
				true
		}
	}
	return "", "", text, true
}
