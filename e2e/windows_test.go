//go:build e2e && windows

package e2e

import (
	"strings"
	"testing"
)

// TestWindowsDriveSlug runs the plain flow on Windows, where the project
// path starts with a drive letter and the slug keeps it: C:\… encodes to
// C--…. The stdout list shows Rel values, never slugs.
func TestWindowsDriveSlug(t *testing.T) {
	e := newTestEnv(t)
	if !strings.HasPrefix(e.project, "C:") && !strings.HasPrefix(e.project, "c:") {
		t.Skipf("temp dir is on %q, not a C: drive", e.project)
	}
	root := e.slugDir()
	writeFact(t, root, "win-fact", "project", "Windows fact", "Body.")

	res := e.runOK("--list")
	if !strings.Contains(res.Stdout, ". (1 facts: project 1)") {
		t.Errorf("stdout = %q, want the one-fact list line", res.Stdout)
	}
	if strings.Contains(res.Stdout, "C--") {
		t.Errorf("stdout leaks slug artifacts: %q", res.Stdout)
	}

	// The computed slug keeps the drive letter; the exit-3 message is the
	// place where it shows.
	empty := newTestEnv(t)
	res = empty.run()
	if res.Code != 3 || !strings.Contains(res.Stderr, "C--") {
		t.Errorf("exit=%d stderr=%q, want exit 3 naming the C--… slug", res.Code, res.Stderr)
	}
}
