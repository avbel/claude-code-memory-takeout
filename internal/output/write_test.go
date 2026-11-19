package output

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/avbel/claude-code-memory-takeout/internal/render"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
}

func TestWriteCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "MEMORY.md")
	content := []byte(render.Marker + "\n\n# Memory\n")

	status, err := Write(path, content, false)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if status != StatusWritten {
		t.Fatalf("status = %q, want %q", status, StatusWritten)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// os.Chmod on Windows only toggles the read-only bit; the perm bits
	// read back as 0666 there no matter what was requested.
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode != 0o644 {
			t.Errorf("mode = %o, want 644", mode)
		}
	}
}

func TestWriteIdenticalIsUpToDate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "MEMORY.md")
	content := []byte(render.Marker + "\n\n# Memory\n")

	if _, err := Write(path, content, false); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	status, err := Write(path, content, false)
	if err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if status != StatusUpToDate {
		t.Fatalf("status = %q, want %q", status, StatusUpToDate)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("mtime moved from %v to %v; identical content must not be rewritten", before.ModTime(), after.ModTime())
	}
}

func TestWriteRefusesForeignFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "MEMORY.md")
	const foreign = "# hand written notes\nkeep me\n"
	writeFile(t, path, foreign)

	_, err := Write(path, []byte(render.Marker+"\n\nnew\n"), false)
	if err == nil {
		t.Fatal("Write succeeded, want ErrExists")
	}
	if !errors.Is(err, ErrExists) {
		t.Errorf("err = %v, want errors.Is ErrExists", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("err = %q, want it to mention %s", err, path)
	}
	if !strings.Contains(err.Error(), "use --force") {
		t.Errorf("err = %q, want it to mention use --force", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != foreign {
		t.Errorf("content = %q, want unchanged %q", got, foreign)
	}
}

func TestWriteForceOverwritesForeignFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "MEMORY.md")
	writeFile(t, path, "# hand written notes\n")

	content := []byte(render.Marker + "\n\nreplaced\n")
	status, err := Write(path, content, true)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if status != StatusWritten {
		t.Fatalf("status = %q, want %q", status, StatusWritten)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}
}

func TestWriteReplacesMarkedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "MEMORY.md")
	writeFile(t, path, render.Marker+"\n\nstale\n")

	content := []byte(render.Marker + "\n\nfresh\n")
	status, err := Write(path, content, false)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if status != StatusWritten {
		t.Fatalf("status = %q, want %q", status, StatusWritten)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}
}

func TestWriteMarkerOnlyFileCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "MEMORY.md")
	writeFile(t, path, render.Marker)

	content := []byte(render.Marker + "\n\nfresh\n")
	status, err := Write(path, content, false)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if status != StatusWritten {
		t.Fatalf("status = %q, want %q", status, StatusWritten)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}
}

func TestWriteCRLFTolerant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MEMORY.md")

	// A marker file checked out with CRLF (git autocrlf on Windows) plus
	// a BOM still counts as ours, and identical content under CRLF is up
	// to date rather than a rewrite.
	content := []byte(render.Marker + "\n# Memory: x\n\nbody\n")
	crlf := []byte("\uFEFF" + strings.ReplaceAll(string(content), "\n", "\r\n"))
	if err := os.WriteFile(path, crlf, 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := Write(path, content, false)
	if err != nil {
		t.Fatalf("CRLF marker file: %v", err)
	}
	if status != StatusUpToDate {
		t.Errorf("status = %q, want up to date", status)
	}
}

func TestWriteRemovesTempOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MEMORY.md")
	// A directory at the output path makes the final rename fail, so the
	// temp file must be cleaned up rather than left behind.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Write(path, []byte(render.Marker+"\nx\n"), true)
	if err == nil {
		t.Fatal("want the rename to fail onto a directory")
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".takeout-") {
			t.Errorf("temp file %s left behind after failure", e.Name())
		}
	}
}
