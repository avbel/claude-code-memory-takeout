package config

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// fileConfig is the TOML schema of the config file. Pointers distinguish
// "key set to zero" from "key absent". Unknown keys and tables are ignored.
type fileConfig struct {
	BaseURL         string         `toml:"base_url"`
	Model           string         `toml:"model"`
	Thinking        string         `toml:"thinking"`
	ThinkingStyle   string         `toml:"thinking_style"`
	APIKeyEnv       string         `toml:"api_key_env"`
	MaxPartTokens   *int64         `toml:"max_part_tokens"`
	MaxOutputTokens *int64         `toml:"max_output_tokens"`
	Timeout         string         `toml:"timeout"`
	RedactPatterns  []string       `toml:"redact_patterns"`
	ExtraBodyPrim   toml.Primitive `toml:"extra_body"`
	ExtraBody       map[string]any `toml:"-"`
	Dirs            dirsFile       `toml:"dirs"`
}

type dirsFile struct {
	Claude string `toml:"claude"`
}

// parseTOML decodes the config file with BurntSushi/toml. extra_body is
// decoded manually from a Primitive, because the decoder silently drops
// non-table values that are meant to hit the JSON-object validation.
func parseTOML(src string) (fileConfig, error) {
	var doc fileConfig
	md, err := toml.Decode(src, &doc)
	if err != nil {
		return fileConfig{}, err
	}
	if md.IsDefined("extra_body") {
		var body map[string]any
		if err := md.PrimitiveDecode(doc.ExtraBodyPrim, &body); err != nil {
			return fileConfig{}, fmt.Errorf("extra_body must be a table: %v", err)
		}
		if body == nil {
			return fileConfig{}, fmt.Errorf("extra_body must be a table, not a scalar")
		}
		doc.ExtraBody = body
	}
	return doc, nil
}
