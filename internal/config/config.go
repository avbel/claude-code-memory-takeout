// Package config loads and merges the claude-code-memory-takeout
// configuration with the precedence flag > environment > config file >
// built-in default, and validates the result. All failures are usage
// errors (exit code 2).
package config

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/avbel/claude-code-memory-takeout/internal/llm"
)

// ErrUsage marks a configuration problem; the CLI maps it to exit code 2.
var ErrUsage = errors.New("config error")

// Env reads one environment variable; injectable for tests.
type Env func(string) string

// Config is the merged, validated configuration.
type Config struct {
	BaseURL         string
	Model           string
	Thinking        string
	ThinkingStyle   string
	APIKeyEnv       string
	MaxPartTokens   int
	MaxOutputTokens int
	Timeout         time.Duration
	RedactPatterns  []string
	ExtraBody       string
	ClaudeDir       string
}

// LoadOptions carries the inputs to Load.
type LoadOptions struct {
	// Values holds set command-line flags by canonical name, in string form.
	Values map[string]string
	// Env reads the process environment.
	Env Env
	// RequireModel makes a missing model an error (--llm needs one).
	RequireModel bool
}

// envNames maps flag/config names to the CCMT_* env overrides. The
// "api-key-env" entry names an env-var *name*, not a credential.
// claude-dir has no env override: it is a path, not a machine setting.
var envNames = map[string]string{ //nolint:gosec // G101 false positive: values are env var names, not secrets
	"base-url":       "CCMT_BASE_URL",
	"model":          "CCMT_MODEL",
	"thinking":       "CCMT_THINKING",
	"thinking-style": "CCMT_THINKING_STYLE",
	"api-key-env":    "CCMT_API_KEY_ENV",
	"timeout":        "CCMT_TIMEOUT",
}

// Path resolves the config file path: --config, then CCMT_CONFIG, then the
// per-OS default built by hand (never os.UserConfigDir, which would pick
// ~/Library/Application Support on macOS).
func Path(flagPath string, env Env, goos string) string {
	if flagPath != "" {
		return expandHome(flagPath)
	}
	if p := env("CCMT_CONFIG"); p != "" {
		return expandHome(p)
	}
	home, _ := os.UserHomeDir()
	if goos == "windows" {
		base := env("APPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(base, "claude-code-memory-takeout", "config.toml")
	}
	if xdg := env("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "claude-code-memory-takeout", "config.toml")
	}
	return filepath.Join(home, ".config", "claude-code-memory-takeout", "config.toml")
}

// Load merges and validates the configuration.
func Load(opts LoadOptions) (*Config, error) {
	path := Path(opts.Values["config"], opts.Env, runtime.GOOS)
	file, err := readFile(path)
	if err != nil {
		return nil, err
	}

	c := Config{
		BaseURL:       cmp.Or(pickString("base-url", opts, file.BaseURL), "https://api.openai.com/v1"),
		Model:         pickString("model", opts, file.Model),
		Thinking:      pickString("thinking", opts, file.Thinking),
		ThinkingStyle: pickString("thinking-style", opts, file.ThinkingStyle),
		APIKeyEnv:     pickString("api-key-env", opts, file.APIKeyEnv),
		ClaudeDir:     pickString("claude-dir", opts, file.Dirs.Claude),
	}
	if raw := pickString("extra-body", opts, ""); raw != "" {
		c.ExtraBody = raw
	} else {
		c.ExtraBody, err = extraBodyJSON(file.ExtraBody)
		if err != nil {
			return nil, err
		}
	}
	if c.MaxPartTokens, err = pickInt("max-part-tokens", opts, file.MaxPartTokens, 24000); err != nil {
		return nil, err
	}
	if c.MaxOutputTokens, err = pickInt("max-output-tokens", opts, file.MaxOutputTokens, 0); err != nil {
		return nil, err
	}
	if c.Timeout, err = pickDuration("timeout", opts, file.Timeout, 10*time.Minute); err != nil {
		return nil, err
	}
	if c.RedactPatterns, err = redactPatterns(file.RedactPatterns); err != nil {
		return nil, err
	}

	expandPaths(&c)
	if err := c.validate(opts.RequireModel, path); err != nil {
		return nil, err
	}
	return &c, nil
}

// pickString applies the precedence chain to one string setting: flag, then
// env, then file. Empty results mean "unset".
func pickString(name string, opts LoadOptions, fileVal string) string {
	if v, ok := opts.Values[name]; ok {
		return v
	}
	if envName := envNames[name]; envName != "" {
		if v := opts.Env(envName); v != "" {
			return v
		}
	}
	return fileVal
}

// configKey renders a flag name as its config-file key for error messages.
func configKey(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

// pickInt resolves an integer setting; the file value is already typed by the
// TOML decoder.
func pickInt(name string, opts LoadOptions, fileVal *int64, def int) (int, error) {
	if v, ok := opts.Values[name]; ok {
		return parseIntSetting(name, v)
	}
	if envName := envNames[name]; envName != "" {
		if v := opts.Env(envName); v != "" {
			return parseIntSetting(name, v)
		}
	}
	if fileVal != nil {
		return int(*fileVal), nil
	}
	return def, nil
}

func parseIntSetting(name, raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be a number, got %q", ErrUsage, configKey(name), raw)
	}
	return n, nil
}

// pickDuration resolves a duration setting (default form: 10m).
func pickDuration(name string, opts LoadOptions, fileVal string, def time.Duration) (time.Duration, error) {
	raw, have := "", false
	if v, ok := opts.Values[name]; ok {
		raw, have = v, true
	} else {
		if envName := envNames[name]; envName != "" {
			if v := opts.Env(envName); v != "" {
				raw, have = v, true
			}
		}
		if !have && fileVal != "" {
			raw, have = fileVal, true
		}
	}
	if !have {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%w: %s must be a duration like 10m, got %q", ErrUsage, configKey(name), raw)
	}
	return d, nil
}

func extraBodyJSON(v map[string]any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("%w: encode extra_body: %v", ErrUsage, err)
	}
	return string(b), nil
}

func redactPatterns(patterns []string) ([]string, error) {
	out := make([]string, 0, len(patterns))
	for _, s := range patterns {
		if _, err := regexp.Compile(s); err != nil {
			return nil, fmt.Errorf("%w: redact pattern %q: %v", ErrUsage, s, err)
		}
		out = append(out, s)
	}
	return out, nil
}

func expandPaths(c *Config) {
	c.ClaudeDir = expandHome(c.ClaudeDir)
}

// expandHome rewrites a leading ~ to the user's home dir.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, "~\\") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), string(filepath.Separator)))
}

func (c *Config) validate(requireModel bool, cfgPath string) error {
	// Redact patterns were already compiled by redactPatterns at load;
	// recompiling here would only duplicate the failure path.
	if err := validateBaseURL(c.BaseURL); err != nil {
		return err
	}
	if err := validateThinking(c.Thinking, c.ThinkingStyle); err != nil {
		return err
	}
	if c.MaxPartTokens <= 0 {
		return fmt.Errorf("%w: max_part_tokens must be positive", ErrUsage)
	}
	if c.MaxOutputTokens < 0 {
		return fmt.Errorf("%w: max_output_tokens must not be negative", ErrUsage)
	}
	if err := validateExtraBody(c.ExtraBody); err != nil {
		return err
	}
	if requireModel && strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("%w: missing model: pass --model or run without --llm, set CCMT_MODEL, or set model = \"...\" in the config file (%s)", ErrUsage, cfgPath)
	}
	return nil
}

func validateBaseURL(raw string) error {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return fmt.Errorf("%w: base URL is required", ErrUsage)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: parse base URL %q", ErrUsage, raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !llm.IsLoopbackHost(u.Hostname()) {
			return fmt.Errorf("%w: base URL %q is http:// but the host is not loopback; the API key would be sent in cleartext", ErrUsage, raw)
		}
	default:
		return fmt.Errorf("%w: base URL scheme must be https (or http on loopback), got %q", ErrUsage, u.Scheme)
	}
	return nil
}

var validLevels = map[string]bool{
	"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true,
}

var validStyles = map[string]bool{
	"auto": true, "openai": true, "openrouter": true, "none": true,
}

func validateThinking(level, style string) error {
	if level != "" && !validLevels[strings.ToLower(level)] {
		return fmt.Errorf("%w: unknown thinking level %q (want off, minimal, low, medium, high or xhigh)", ErrUsage, level)
	}
	if style != "" && !validStyles[strings.ToLower(style)] {
		return fmt.Errorf("%w: unknown thinking style %q (want auto, openai, openrouter or none)", ErrUsage, style)
	}
	return nil
}

func validateExtraBody(raw string) error {
	if raw == "" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj == nil {
		return fmt.Errorf("%w: extra_body must be a JSON object: %v", ErrUsage, err)
	}
	return nil
}

// APIKey resolves the API key from the environment, never from the config
// file or the command line. The default name is OPENAI_API_KEY. A missing
// key is reported by the LLM client, which allows keyless use of loopback
// endpoints.
func (c *Config) APIKey(env Env) string {
	name := c.APIKeyEnv
	if name == "" {
		name = "OPENAI_API_KEY"
	}
	return env(name)
}

// readFile parses the config file; a missing file is fine, a broken one is
// a usage error.
func readFile(path string) (fileConfig, error) {
	if path == "" {
		return fileConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fileConfig{}, nil
		}
		return fileConfig{}, fmt.Errorf("%w: read config file %s: %v", ErrUsage, path, err)
	}
	doc, err := parseTOML(string(data))
	if err != nil {
		return fileConfig{}, fmt.Errorf("%w: %s: %v", ErrUsage, path, err)
	}
	return doc, nil
}
