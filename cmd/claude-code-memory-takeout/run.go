package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/avbel/claude-code-memory-takeout/internal/cli"
	"github.com/avbel/claude-code-memory-takeout/internal/config"
	"github.com/avbel/claude-code-memory-takeout/internal/consolidate"
	"github.com/avbel/claude-code-memory-takeout/internal/llm"
	"github.com/avbel/claude-code-memory-takeout/internal/locate"
	"github.com/avbel/claude-code-memory-takeout/internal/memory"
	"github.com/avbel/claude-code-memory-takeout/internal/output"
	"github.com/avbel/claude-code-memory-takeout/internal/redact"
	"github.com/avbel/claude-code-memory-takeout/internal/render"
)

// Exit codes per the CLI spec.
const (
	exitOK        = 0
	exitRuntime   = 1
	exitUsage     = 2
	exitNoMemory  = 3
	exitLLM       = 4
	exitCancelled = 130
)

// errNoMemory marks a project with no matching memory dir; exit code 3.
var errNoMemory = errors.New("no Claude memory found")

// app bundles the process IO environment, injectable for tests.
type app struct {
	stdout  io.Writer
	stderr  io.Writer
	getenv  config.Env
	version string
}

// Run parses the arguments and runs the checkout, returning the exit code.
func (a *app) Run(args []string) int {
	flags, err := cli.Parse(args)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "claude-code-memory-takeout: %v\n", err)
		return exitUsage
	}
	if flags.ShowVersion {
		_, _ = fmt.Fprintln(a.stdout, a.version)
		return exitOK
	}
	if flags.ShowHelp {
		cli.PrintUsage(a.stdout)
		return exitOK
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := a.run(ctx, flags); err != nil {
		if ctx.Err() != nil {
			// Ctrl-C interrupted a step: report the cancellation no
			// matter how the step's own error was wrapped.
			_, _ = fmt.Fprintln(a.stderr, "claude-code-memory-takeout: cancelled")
			return exitCancelled
		}
		_, _ = fmt.Fprintf(a.stderr, "claude-code-memory-takeout: %v\n", err)
		return exitFor(err)
	}
	return exitOK
}

// exitFor maps an error to its exit code.
func exitFor(err error) int {
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, context.Canceled):
		return exitCancelled
	case errors.Is(err, errNoMemory):
		return exitNoMemory
	case errors.Is(err, consolidate.ErrLLM):
		return exitLLM
	case errors.Is(err, cli.ErrUsage), errors.Is(err, config.ErrUsage),
		errors.Is(err, llm.ErrUsage), errors.Is(err, output.ErrExists):
		return exitUsage
	default:
		return exitRuntime
	}
}

func (a *app) run(ctx context.Context, flags *cli.Flags) error {
	cfg, err := config.Load(config.LoadOptions{
		Values:       flags.Values(),
		Env:          a.getenv,
		RequireModel: flags.LLM,
	})
	if err != nil {
		return err
	}
	root, err := projectRoot(flags.ProjectDir)
	if err != nil {
		return err
	}
	u := &ui{stderr: a.stderr, quiet: flags.Quiet, verbose: flags.Verbose}
	claudeDir := cmp.Or(cfg.ClaudeDir, a.getenv("CLAUDE_CONFIG_DIR"), defaultClaudeDir())

	matches, err := locate.Find(claudeDir, root, locate.Options{
		Subdirs: !flags.NoSubdirs,
		Warn:    u.warn,
		Verbose: u.debug,
	})
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return noMemoryError(claudeDir, root)
	}
	loaded := memory.LoadDirs(matches)
	u.printWarnings(loaded)
	if len(loaded.Facts) == 0 {
		// Matches with nothing readable left are effectively no memory:
		// exit 3 keeps the output file untouched instead of replacing it
		// with a header-only document.
		return noMemoryError(claudeDir, root)
	}

	if flags.List {
		return printList(a.stdout, matches, loaded.Facts)
	}

	rd, err := redact.New(redact.WithPatterns(cfg.RedactPatterns))
	if err != nil {
		return err
	}
	redacted, kinds, facts := redactedFacts(loaded.Facts, rd)
	u.info(memoryLine(matches, loaded.Facts))
	if line := redactedLine(redacted, kinds); line != "" {
		u.info(line)
	}

	doc, err := a.document(ctx, flags, cfg, root, matches, facts, redacted, rd, u)
	if err != nil {
		return err
	}
	return a.write(flags, root, doc)
}

// document builds the MEMORY.md content in verbatim or --llm mode.
func (a *app) document(ctx context.Context, flags *cli.Flags, cfg *config.Config, root string,
	matches []locate.Match, facts []memory.Fact, redacted int, rd *redact.Redactor, u *ui) (string, error) {

	sources := sourcesOf(matches, facts)
	if flags.LLM {
		client, err := llm.New(llm.Options{
			BaseURL:         cfg.BaseURL,
			Model:           cfg.Model,
			APIKey:          cfg.APIKey(a.getenv),
			Version:         a.version,
			MaxOutputTokens: cfg.MaxOutputTokens,
			ThinkingStyle:   cfg.ThinkingStyle,
			ThinkingLevel:   cfg.Thinking,
			ExtraBody:       json.RawMessage(cfg.ExtraBody),
			Redact:          func(s string) string { return rd.Apply(s).Text },
		})
		if err != nil {
			return "", err
		}
		// The timeout bounds the whole --llm phase, not one request.
		cctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
		doc, _, err := consolidate.Run(cctx, facts, filepath.Base(root), sources, client, consolidate.Options{
			Model:           cfg.Model,
			MaxPartTokens:   cfg.MaxPartTokens,
			MaxOutputTokens: cfg.MaxOutputTokens,
			Redacted:        redacted,
			Redactor:        rd,
			Warn:            u.warn,
			Info:            u.info,
			Verbose:         u.debug,
		})
		return doc, err
	}
	return render.Document(render.Doc{
		Project:  filepath.Base(root),
		Mode:     "verbatim",
		Sources:  sources,
		Facts:    facts,
		Redacted: redacted,
	}), nil
}

// write stores the document and reports the outcome on stderr.
func (a *app) write(flags *cli.Flags, root, content string) error {
	path := flags.Output
	if path == "" {
		path = filepath.Join(root, "MEMORY.md")
	}
	status, err := output.Write(path, []byte(content), flags.Force)
	if err != nil {
		return err
	}
	u := &ui{stderr: a.stderr, quiet: flags.Quiet}
	switch status {
	case output.StatusStdout:
	case output.StatusUpToDate:
		u.info(fmt.Sprintf("%s is up to date", filepath.Base(path)))
	default:
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		u.info("wrote " + abs)
	}
	return nil
}

// ui prints the stderr lines: info is silenced by -q, warnings always
// show with a prefix, debug lines need -v.
type ui struct {
	stderr  io.Writer
	quiet   bool
	verbose bool
}

func (u *ui) info(msg string) {
	if u.quiet {
		return
	}
	_, _ = fmt.Fprintln(u.stderr, msg)
}

func (u *ui) warn(msg string) {
	_, _ = fmt.Fprintln(u.stderr, "warning: "+msg)
}

func (u *ui) debug(msg string) {
	if !u.verbose {
		return
	}
	_, _ = fmt.Fprintln(u.stderr, msg)
}

func (u *ui) printWarnings(loaded memory.LoadResult) {
	for _, w := range loaded.Warnings {
		u.warn(w)
	}
	for _, v := range loaded.Verbose {
		u.debug(v)
	}
	if loaded.Skipped > 0 {
		u.warn(fmt.Sprintf("skipped %d unreadable memory files", loaded.Skipped))
	}
}

// redactedFacts redacts every fact's description and body with the shared
// redactor and returns the grand total, the per-kind counts, and the
// redacted copies. Home-dir folding is counted but is not a secret.
func redactedFacts(facts []memory.Fact, rd *redact.Redactor) (int, map[string]int, []memory.Fact) {
	out := slices.Clone(facts)
	kinds := map[string]int{}
	total := 0
	for i := range out {
		d := rd.Apply(out[i].Description)
		b := rd.Apply(out[i].Body)
		out[i].Description = d.Text
		out[i].Body = b.Text
		for _, r := range []redact.Result{d, b} {
			total += r.Total()
			for kind, n := range r.Counts {
				kinds[kind] += n
			}
		}
	}
	return total, kinds, out
}

// memoryLine renders the per-dir fact counts in Find order.
func memoryLine(matches []locate.Match, facts []memory.Fact) string {
	counts := map[string]int{}
	for _, f := range facts {
		counts[f.Rel]++
	}
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		parts = append(parts, fmt.Sprintf("%s = %d", m.Rel, counts[m.Rel]))
	}
	return fmt.Sprintf("memory: %d dirs, %d facts (%s)", len(matches), len(facts), strings.Join(parts, ", "))
}

// redactedLine renders the secret redaction counts, sorted by kind; empty
// when nothing was redacted.
func redactedLine(total int, kinds map[string]int) string {
	if total == 0 {
		return ""
	}
	var parts []string
	for _, kind := range slices.Sorted(maps.Keys(kinds)) {
		if kind != "home" {
			parts = append(parts, fmt.Sprintf("%s=%d", kind, kinds[kind]))
		}
	}
	return fmt.Sprintf("redacted: %d values (%s)", total, strings.Join(parts, ", "))
}

// sourcesOf renders the Sources header line in Find order.
func sourcesOf(matches []locate.Match, facts []memory.Fact) []render.Source {
	counts := map[string]int{}
	for _, f := range facts {
		counts[f.Rel]++
	}
	out := make([]render.Source, 0, len(matches))
	for _, m := range matches {
		out = append(out, render.Source{Rel: m.Rel, Facts: counts[m.Rel]})
	}
	return out
}

// printList writes one line per matched memory dir: the Rel, the fact
// count and the per-section breakdown.
func printList(w io.Writer, matches []locate.Match, facts []memory.Fact) error {
	totals := map[string]int{}
	sections := map[string][]int{}
	heads := render.SectionHeadings()
	for _, f := range facts {
		totals[f.Rel]++
		per := sections[f.Rel]
		if per == nil {
			per = make([]int, len(heads))
			sections[f.Rel] = per
		}
		per[render.SectionOf(f.Type)]++
	}
	for _, m := range matches {
		line := fmt.Sprintf("%s (%d facts", m.Rel, totals[m.Rel])
		var parts []string
		per := sections[m.Rel]
		for i, h := range heads {
			if per != nil && per[i] > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", strings.ToLower(h), per[i]))
			}
		}
		if len(parts) > 0 {
			line += ": " + strings.Join(parts, ", ")
		}
		if _, err := fmt.Fprintln(w, line+")"); err != nil {
			return fmt.Errorf("write list output: %w", err)
		}
	}
	return nil
}

// noMemoryError tells the user where the memory would live, computed slug
// included — it is the main debugging hint of exit code 3.
func noMemoryError(claudeDir, root string) error {
	slug := locate.Encode(root)
	msg := fmt.Sprintf("no Claude memory for %s; looked for %s/projects/%s/memory",
		root, claudeDir, slug)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		if enc := locate.Encode(resolved); enc != slug {
			msg += fmt.Sprintf(" (also %s/projects/%s/memory)", claudeDir, enc)
		}
	}
	return fmt.Errorf("%w: %s", errNoMemory, msg)
}

// projectRoot resolves and validates the positional project dir.
func projectRoot(dir string) (string, error) {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %s: %w", cli.ErrUsage, dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: %s is not an existing directory", cli.ErrUsage, dir)
	}
	return abs, nil
}

// defaultClaudeDir is where Claude Code lives when nothing else says
// otherwise. Built by hand: never os.UserConfigDir, which would pick
// ~/Library/Application Support on macOS.
func defaultClaudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}
