package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avbel/claude-code-memory-takeout/internal/cli"
	"github.com/avbel/claude-code-memory-takeout/internal/config"
	"github.com/avbel/claude-code-memory-takeout/internal/consolidate"
	"github.com/avbel/claude-code-memory-takeout/internal/llm"
	"github.com/avbel/claude-code-memory-takeout/internal/output"
)

func TestExitFor(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, exitOK},
		{"cancelled", context.Canceled, exitCancelled},
		{"no memory", errNoMemory, exitNoMemory},
		{"llm", consolidate.ErrLLM, exitLLM},
		{"cli usage", cli.ErrUsage, exitUsage},
		{"config usage", config.ErrUsage, exitUsage},
		{"llm usage", llm.ErrUsage, exitUsage},
		{"output exists", output.ErrExists, exitUsage},
		{"wrapped usage", errors.Join(cli.ErrUsage, errors.New("detail")), exitUsage},
		{"wrapped llm", errors.Join(consolidate.ErrLLM, errors.New("HTTP 401")), exitLLM},
		{"runtime", errors.New("boom"), exitRuntime},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitFor(tc.err); got != tc.want {
				t.Errorf("exitFor(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestProjectRoot(t *testing.T) {
	// The empty positional means the current dir.
	got, err := projectRoot("")
	if err != nil {
		t.Fatalf("projectRoot(\"\"): %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("projectRoot(\"\") = %q, want an absolute path", got)
	}

	dir := t.TempDir()
	got, err = projectRoot(dir)
	if err != nil {
		t.Fatalf("projectRoot(%s): %v", dir, err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("projectRoot(%s) = %q, want an absolute path", dir, got)
	}

	// A missing dir is a usage error, never a silent default.
	if _, err := projectRoot(filepath.Join(dir, "no-such-dir")); !errors.Is(err, cli.ErrUsage) {
		t.Errorf("missing dir: err = %v, want cli.ErrUsage", err)
	}

	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := projectRoot(file); !errors.Is(err, cli.ErrUsage) {
		t.Errorf("file as project: err = %v, want cli.ErrUsage", err)
	}
}

func TestNoMemoryError(t *testing.T) {
	err := noMemoryError("/claude", "/proj")
	if !errors.Is(err, errNoMemory) {
		t.Fatalf("err = %v, want errNoMemory", err)
	}
	if !strings.Contains(err.Error(), "/claude/projects/-proj/memory") {
		t.Errorf("err = %v, want the computed slug in the message", err)
	}
}
