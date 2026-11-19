//go:build e2e

// Package e2e runs the real claude-code-memory-takeout binary against a
// fake Claude Code config dir and, for --llm, a fake OpenAI-compatible
// server. Everything here is black box: the tests only see argv,
// environment, files, stdout and stderr.
package e2e

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite e2e golden files")

// binaryPath is the binary built once in TestMain.
var binaryPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "takeout-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create binary dir:", err)
		os.Exit(1)
	}
	name := "claude-code-memory-takeout"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binaryPath = filepath.Join(dir, name)
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/claude-code-memory-takeout")
	build.Dir = repoRoot()
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build binary: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// repoRoot is the module root, one directory above this file.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ".."
	}
	return filepath.Dir(filepath.Dir(file))
}

// encodeSlug is the slug rule of PLAN.md §6.1, written independently from
// internal/locate so the tests can catch drift.
func encodeSlug(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// resolve returns the symlink-free path of dir, the form Claude Code
// records.
func resolve(t *testing.T, dir string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	return real
}

// testEnv is one isolated run environment: a fake home with a project
// tree and a fake $CLAUDE_CONFIG_DIR.
type testEnv struct {
	t       *testing.T
	home    string
	project string
	env     map[string]string
	llm     *fakeLLM
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	home := t.TempDir()
	project := filepath.Join(home, "work", "proj")
	mkdir(t, project)
	e := &testEnv{t: t, home: home, project: project, env: map[string]string{}}
	e.env["HOME"] = home
	e.env["USERPROFILE"] = home
	e.env["CLAUDE_CONFIG_DIR"] = e.claudeDir()
	e.env["XDG_CONFIG_HOME"] = filepath.Join(home, ".config")
	e.env["APPDATA"] = filepath.Join(home, "AppData", "Roaming")
	e.env["LOCALAPPDATA"] = filepath.Join(home, "AppData", "Local")
	e.env["NO_COLOR"] = "1"
	return e
}

func (e *testEnv) claudeDir() string {
	return filepath.Join(e.home, ".claude")
}

// slugDir creates the real path and its slug memory dir, and returns the
// memory dir.
func (e *testEnv) slugDir(parts ...string) string {
	e.t.Helper()
	path := filepath.Join(append([]string{e.project}, parts...)...)
	mkdir(e.t, path)
	dir := filepath.Join(e.claudeDir(), "projects", encodeSlug(resolve(e.t, path)), "memory")
	mkdir(e.t, dir)
	return dir
}

// slugDirMissing returns the slug memory dir of a path that does not
// exist on disk — the deleted-worktree case, matched from the slug alone.
func (e *testEnv) slugDirMissing(parts ...string) string {
	e.t.Helper()
	path := filepath.Join(append([]string{resolve(e.t, e.project)}, parts...)...)
	dir := filepath.Join(e.claudeDir(), "projects", encodeSlug(path), "memory")
	mkdir(e.t, dir)
	return dir
}

// useLLM starts a fake OpenAI-compatible server and points the binary at
// it.
func (e *testEnv) useLLM(mode llmMode) *fakeLLM {
	e.llm = newFakeLLM(e.t, mode)
	e.env["CCMT_BASE_URL"] = e.llm.server.URL + "/v1"
	e.env["CCMT_MODEL"] = "test-model"
	return e.llm
}

// droppedEnvKeys are inherited values that would break isolation or leak
// the developer's real configuration into the child.
var droppedEnvKeys = map[string]bool{
	"HOME": true, "USERPROFILE": true, "CLAUDE_CONFIG_DIR": true,
	"XDG_CONFIG_HOME": true, "APPDATA": true, "LOCALAPPDATA": true,
	"OPENAI_API_KEY": true, "NO_COLOR": true,
}

// childEnv is the process environment for one binary run.
func (e *testEnv) childEnv() []string {
	out := make([]string, 0, len(e.env)+32)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if droppedEnvKeys[key] || strings.HasPrefix(key, "CCMT_") {
			continue
		}
		out = append(out, kv)
	}
	for _, key := range slices.Sorted(maps.Keys(e.env)) {
		out = append(out, key+"="+e.env[key])
	}
	return out
}

// runResult is one captured binary invocation.
type runResult struct {
	Stdout string
	Stderr string
	Code   int
}

// run executes the binary in the project dir with the isolated env.
func (e *testEnv) run(args ...string) runResult {
	e.t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = e.project
	cmd.Env = e.childEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			e.t.Fatalf("run %v: %v", args, err)
		}
		code = exit.ExitCode()
	}
	return runResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: code}
}

func (e *testEnv) runOK(args ...string) runResult {
	e.t.Helper()
	res := e.run(args...)
	if res.Code != 0 {
		e.t.Fatalf("run %v exited %d, want 0\nstderr:\n%s", args, res.Code, res.Stderr)
	}
	return res
}

// readOutput returns the written MEMORY.md of the project dir.
func (e *testEnv) readOutput() string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.project, "MEMORY.md"))
	if err != nil {
		e.t.Fatalf("read output: %v", err)
	}
	return string(b)
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeFact writes one synthetic fact file.
func writeFact(t *testing.T, dir, name, kind, description, body string) {
	t.Helper()
	text := "---\nname: " + name + "\n"
	if description != "" {
		text += "description: \"" + description + "\"\n"
	}
	text += "metadata:\n  type: " + kind + "\n---\n\n" + body + "\n"
	writeFile(t, filepath.Join(dir, name+".md"), text)
}

// compareGolden checks output against testdata/golden with -update support.
func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(repoRoot(), "testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run go test -tags e2e -update ./e2e/)", path, err)
	}
	if string(want) != got {
		t.Errorf("golden %s mismatch\ngot:\n%s\nwant:\n%s", name, got, string(want))
	}
}
