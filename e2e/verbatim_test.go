//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTree is the shared scenario fixture: root memory, a packages/web
// memory dir, a deleted worktree's memory, and a sibling project's slug
// that must never be matched.
func buildTree(t *testing.T) *testEnv {
	t.Helper()
	e := newTestEnv(t)
	root := e.slugDir()
	writeFact(t, root, "root-user-fact", "user", "A user fact", "Body of a.\n\nSee [[b-fact]].")
	writeFact(t, root, "b-fact", "project", "A project fact", "Body of b.")
	web := e.slugDir("packages", "web")
	writeFact(t, web, "web-fact", "project", "", "Body of c.")
	gone := e.slugDirMissing(".claude", "worktrees", "gone")
	writeFact(t, gone, "gone-fact", "feedback", "From the deleted worktree", "Body of d.")

	sibling := filepath.Join(e.home, "work", "proj-bench")
	mkdir(t, sibling)
	bench := filepath.Join(e.claudeDir(), "projects",
		encodeSlug(resolve(t, sibling)), "memory")
	mkdir(t, bench)
	writeFact(t, bench, "bench-fact", "project", "Sibling", "Body of e.")
	return e
}

func TestVerbatimGolden(t *testing.T) {
	e := buildTree(t)
	res := e.runOK()
	if !strings.Contains(res.Stderr, "memory: 3 dirs, 4 facts") {
		t.Errorf("stderr = %q, want the 3-dirs info line", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "wrote ") {
		t.Errorf("stderr = %q, want the wrote line", res.Stderr)
	}
	got, err := os.ReadFile(filepath.Join(e.project, "MEMORY.md"))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	compareGolden(t, "e2e-verbatim.md", string(got))
	doc := string(got)
	for _, unwanted := range []string{"bench-fact", "proj-bench"} {
		if strings.Contains(doc, unwanted) {
			t.Errorf("output leaks the sibling project: %s", unwanted)
		}
	}
	if !strings.Contains(doc, "[b-fact](#b-fact)") {
		t.Errorf("wiki link not rewritten:\n%s", doc)
	}
}

func TestSecondRunUpToDate(t *testing.T) {
	e := buildTree(t)
	e.runOK()
	path := filepath.Join(e.project, "MEMORY.md")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res := e.runOK()
	if !strings.Contains(res.Stderr, "MEMORY.md is up to date") {
		t.Errorf("stderr = %q, want the up-to-date line", res.Stderr)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("the second run rewrote an identical file")
	}
}

func TestExistingFileNeedsForce(t *testing.T) {
	e := buildTree(t)
	path := filepath.Join(e.project, "MEMORY.md")
	writeFile(t, path, "# My own hand-written memory\n\nDo not touch.\n")
	res := e.run()
	if res.Code != 2 {
		t.Fatalf("exit = %d, want 2\nstderr:\n%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "use --force") {
		t.Errorf("stderr = %q, want the --force hint", res.Stderr)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "# My own hand-written memory\n\nDo not touch.\n" {
		t.Error("the hand-written file was modified")
	}
}

func TestForceOverwritesHandWritten(t *testing.T) {
	e := buildTree(t)
	path := filepath.Join(e.project, "MEMORY.md")
	writeFile(t, path, "# My own hand-written memory\n\nDo not touch.\n")
	res := e.runOK("--force")
	if !strings.Contains(res.Stderr, "wrote ") {
		t.Errorf("stderr = %q", res.Stderr)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "## Project") {
		t.Errorf("--force did not replace the hand-written file:\n%s", string(got))
	}
}

func TestOutputStdoutAndList(t *testing.T) {
	e := buildTree(t)

	res := e.runOK("-o", "-")
	if !strings.Contains(res.Stdout, "# Memory: proj") {
		t.Errorf("stdout = %q, want the document", res.Stdout)
	}
	if _, err := os.Stat(filepath.Join(e.project, "MEMORY.md")); !os.IsNotExist(err) {
		t.Error("-o - must not write a file")
	}

	res = e.runOK("--list")
	if !strings.Contains(res.Stdout, ". (2 facts: user 1, project 1)") {
		t.Errorf("stdout = %q, want the list line", res.Stdout)
	}
	if _, err := os.Stat(filepath.Join(e.project, "MEMORY.md")); !os.IsNotExist(err) {
		t.Error("--list must not write a file")
	}
}

func TestNoMemoryExitThree(t *testing.T) {
	e := newTestEnv(t) // no slug dirs at all
	res := e.run()
	if res.Code != 3 {
		t.Fatalf("exit = %d, want 3\nstderr:\n%s", res.Code, res.Stderr)
	}
	slug := encodeSlug(e.project)
	if !strings.Contains(res.Stderr, slug) {
		t.Errorf("stderr = %q, want the computed slug %s", res.Stderr, slug)
	}
}

func TestSecretRedactedInOutput(t *testing.T) {
	e := newTestEnv(t)
	root := e.slugDir()
	writeFact(t, root, "secret-holder", "project", "Holds a secret",
		"The connection uses token = \"supersecret123\" and nothing else.")
	res := e.runOK()
	if !strings.Contains(res.Stderr, "redacted: 1 value") {
		t.Errorf("stderr = %q, want the redaction count", res.Stderr)
	}
	got, _ := os.ReadFile(filepath.Join(e.project, "MEMORY.md"))
	if !strings.Contains(string(got), "[REDACTED]") {
		t.Errorf("the secret was not redacted:\n%s", string(got))
	}
	if strings.Contains(string(got), "supersecret123") {
		t.Error("the raw secret leaked into the output")
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	e := buildTree(t)

	res := e.run("--no-such-flag")
	if res.Code != 2 {
		t.Errorf("unknown flag: exit = %d, want 2\nstderr:\n%s", res.Code, res.Stderr)
	}

	res = e.run("/no/such/project/dir")
	if res.Code != 2 {
		t.Errorf("missing project dir: exit = %d, want 2\nstderr:\n%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not an existing directory") {
		t.Errorf("stderr = %q", res.Stderr)
	}
}
