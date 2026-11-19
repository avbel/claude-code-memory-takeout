package config

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) Env {
	return func(k string) string { return m[k] }
}

// setHomeEnv points the home-dir lookup at dir on every OS: os.UserHomeDir
// reads USERPROFILE on Windows and HOME elsewhere, so both are set.
func setHomeEnv(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
}

// loadTest loads with an empty home dir so a real config file cannot leak in.
func loadTest(t *testing.T, opts LoadOptions) (*Config, error) {
	t.Helper()
	setHomeEnv(t, t.TempDir())
	if opts.Env == nil {
		opts.Env = envOf(nil)
	}
	return Load(opts)
}

func TestLoadDefaults(t *testing.T) {
	_, err := loadTest(t, LoadOptions{Values: map[string]string{}, RequireModel: true})
	if err == nil || !strings.Contains(err.Error(), "missing model") {
		t.Fatalf("missing model = %v, want exit-2 hint", err)
	}
	c, err := loadTest(t, LoadOptions{Values: map[string]string{"model": "m"}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if c.MaxPartTokens != 24000 || c.Timeout != 10*time.Minute || c.MaxOutputTokens != 0 {
		t.Errorf("defaults: %+v", c)
	}
}

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	body := "base_url = \"https://file.example/v1\"\nmodel = \"file-model\"\n" +
		"timeout = \"30m\"\nmax_part_tokens = 1234\n\n[dirs]\nclaude = \"/from-file\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// file only
	c, err := loadTest(t, LoadOptions{Values: map[string]string{"config": cfg}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Model != "file-model" || c.BaseURL != "https://file.example/v1" ||
		c.Timeout != 30*time.Minute || c.MaxPartTokens != 1234 || c.ClaudeDir != "/from-file" {
		t.Fatalf("file values: %+v", c)
	}

	// env beats file
	e := envOf(map[string]string{"CCMT_MODEL": "env-model", "CCMT_TIMEOUT": "1m"})
	c, err = loadTest(t, LoadOptions{Values: map[string]string{"config": cfg}, Env: e})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Model != "env-model" || c.Timeout != time.Minute || c.BaseURL != "https://file.example/v1" {
		t.Fatalf("env over file: %+v", c)
	}

	// flag beats env and file
	v := map[string]string{
		"config": cfg, "model": "flag-model", "max-part-tokens": "99", "claude-dir": "/from-flag",
	}
	c, err = loadTest(t, LoadOptions{Values: v, Env: e})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Model != "flag-model" || c.MaxPartTokens != 99 || c.ClaudeDir != "/from-flag" {
		t.Fatalf("flag wins: %+v", c)
	}
}

func TestPath(t *testing.T) {
	home := t.TempDir()
	setHomeEnv(t, home)
	defaultPath := filepath.Join(home, ".config", "claude-code-memory-takeout", "config.toml")
	sub := filepath.Join("claude-code-memory-takeout", "config.toml")

	tests := []struct {
		name string
		flag string
		env  map[string]string
		goos string
		want string
	}{
		{"flag wins", "/flag.toml", map[string]string{"CCMT_CONFIG": "/env.toml"}, "darwin", "/flag.toml"},
		{"env", "", map[string]string{"CCMT_CONFIG": "/env.toml"}, "darwin", "/env.toml"},
		{"xdg", "", map[string]string{"XDG_CONFIG_HOME": "/xdg"}, "linux", filepath.Join("/xdg", sub)},
		{"home fallback", "", nil, "darwin", defaultPath},
		{"windows appdata", "", map[string]string{"APPDATA": `C:\Users\u\AppData\Roaming`}, "windows",
			filepath.Join(`C:\Users\u\AppData\Roaming`, sub)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Empty process-env values must not shadow the injected ones.
			t.Setenv("CCMT_CONFIG", "")
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("APPDATA", "")
			if got := Path(tt.flag, envOf(tt.env), tt.goos); got != tt.want {
				t.Errorf("Path(%q, %v, %q) = %q, want %q", tt.flag, tt.env, tt.goos, got, tt.want)
			}
		})
	}

	t.Run("tilde flag", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home dir: %v", err)
		}
		// Comparing against the home join, not "no ~ in the string":
		// the Windows runner's temp path contains the 8.3 name
		// RUNNER~1, whose ~ would defeat that heuristic.
		want := filepath.Join(home, "cfg.toml")
		if p := Path("~/cfg.toml", envOf(nil), "darwin"); p != want {
			t.Errorf("flag path = %q, want %q", p, want)
		}
	})
	t.Run("process env", func(t *testing.T) {
		t.Setenv("CCMT_CONFIG", "/from-process.toml")
		if p := Path("", os.Getenv, "linux"); p != "/from-process.toml" {
			t.Errorf("process env path = %q", p)
		}
	})
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		vals  map[string]string
		wantE string
	}{
		{"bad base url", "base_url = \"http://api.example.com/v1\"\n", nil, "cleartext"},
		{"bad thinking level", "thinking = \"maximum\"\n", nil, "thinking level"},
		{"bad extra body", "extra_body = [1]\n", nil, "extra_body must be a table"},
		{"zero max part tokens", "", map[string]string{"max-part-tokens": "0"}, "max_part_tokens"},
		{"bad redact pattern", "redact_patterns = ['([bad']\n", nil, "redact pattern"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vals := map[string]string{"config": filepath.Join(t.TempDir(), "config.toml")}
			maps.Copy(vals, tt.vals)
			if tt.body != "" {
				if err := os.WriteFile(vals["config"], []byte(tt.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := loadTest(t, LoadOptions{Values: vals})
			if err == nil || !errors.Is(err, ErrUsage) || !strings.Contains(err.Error(), tt.wantE) {
				t.Fatalf("Load error = %v, want ErrUsage containing %q", err, tt.wantE)
			}
		})
	}
}

func TestAPIKey(t *testing.T) {
	c, err := loadTest(t, LoadOptions{Values: map[string]string{"api-key-env": "FLAG_KEY"}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.APIKey(envOf(map[string]string{"FLAG_KEY": "flagkey"})); got != "flagkey" {
		t.Errorf("APIKey = %q", got)
	}
	if got := c.APIKey(envOf(nil)); got != "" {
		t.Errorf("missing env var must yield an empty key, got %q", got)
	}

	// no api_key_env: the default name is OPENAI_API_KEY
	c2, err := loadTest(t, LoadOptions{Values: map[string]string{}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c2.APIKey(envOf(map[string]string{"OPENAI_API_KEY": "b"})); got != "b" {
		t.Errorf("default key env = %q, want OPENAI_API_KEY", got)
	}
}
