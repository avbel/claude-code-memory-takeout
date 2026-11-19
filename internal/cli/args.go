// Package cli parses the claude-code-memory-takeout command line with the
// stdlib flag package. Flags may appear before and after the PROJECT_DIR
// positional; env and config fallbacks are applied in internal/config.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"time"
)

// ErrUsage marks a command-line problem; the CLI maps it to exit code 2.
var ErrUsage = errors.New("usage error")

// Defaults for the flags whose values also live in the config file.
const (
	DefaultMaxPartTokens = 24000
	DefaultTimeout       = 10 * time.Minute
)

// Flags is the parsed command line.
type Flags struct {
	ProjectDir string

	Output    string
	Force     bool
	List      bool
	NoSubdirs bool

	ClaudeDir  string
	ConfigPath string

	LLM             bool
	BaseURL         string
	Model           string
	APIKeyEnv       string
	Thinking        string
	ThinkingStyle   string
	ExtraBody       string
	MaxPartTokens   int
	MaxOutputTokens int
	Timeout         time.Duration

	Quiet       bool
	Verbose     bool
	ShowVersion bool
	ShowHelp    bool

	set map[string]bool
}

// Parse loops fs.Parse over the argument list so that flags are accepted
// before and after the single optional PROJECT_DIR positional. The FlagSet
// is built once and reused: re-defining flags each round would reset their
// values to the defaults.
func Parse(args []string) (*Flags, error) {
	f := &Flags{
		MaxPartTokens: DefaultMaxPartTokens,
		Timeout:       DefaultTimeout,
		set:           map[string]bool{},
	}
	fs := newFlagSet(f)
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				f.ShowHelp = true
				return f, nil
			}
			return nil, fmt.Errorf("%w: %v", ErrUsage, err)
		}
		fs.Visit(func(fl *flag.Flag) { f.set[canonical(fl.Name)] = true })
		if fs.NArg() == 0 {
			return f, nil
		}
		if err := f.takePositional(fs.Arg(0)); err != nil {
			return nil, err
		}
		rest = fs.Args()[1:]
		if len(rest) == 0 {
			return f, nil
		}
	}
}

// canonical maps the shorthands onto their long names so that tracking and
// config lookup see one spelling per flag.
func canonical(name string) string {
	switch name {
	case "o":
		return "output"
	case "q":
		return "quiet"
	case "v":
		return "verbose"
	case "h":
		return "help"
	default:
		return name
	}
}

func (f *Flags) takePositional(pos string) error {
	if f.ProjectDir != "" {
		return fmt.Errorf("%w: unexpected argument %q", ErrUsage, pos)
	}
	f.ProjectDir = pos
	return nil
}

// Value returns the string form of a flag that was given on the command
// line; ok is false for unset flags and names that carry no config meaning.
func (f *Flags) Value(name string) (string, bool) {
	if !f.set[name] {
		return "", false
	}
	switch name {
	case "output":
		return f.Output, true
	case "base-url":
		return f.BaseURL, true
	case "model":
		return f.Model, true
	case "api-key-env":
		return f.APIKeyEnv, true
	case "thinking":
		return f.Thinking, true
	case "thinking-style":
		return f.ThinkingStyle, true
	case "extra-body":
		return f.ExtraBody, true
	case "max-part-tokens":
		return strconv.Itoa(f.MaxPartTokens), true
	case "max-output-tokens":
		return strconv.Itoa(f.MaxOutputTokens), true
	case "timeout":
		return f.Timeout.String(), true
	case "claude-dir":
		return f.ClaudeDir, true
	case "config":
		return f.ConfigPath, true
	default:
		return "", false
	}
}

// Values returns every set flag that has a config meaning, in string form.
func (f *Flags) Values() map[string]string {
	out := map[string]string{}
	for _, name := range configFlagNames {
		if v, ok := f.Value(name); ok {
			out[name] = v
		}
	}
	return out
}

var configFlagNames = []string{
	"base-url", "model", "api-key-env", "thinking", "thinking-style",
	"extra-body", "max-part-tokens", "max-output-tokens", "timeout",
	"claude-dir", "config",
}

func newFlagSet(f *Flags) *flag.FlagSet {
	fs := flag.NewFlagSet("claude-code-memory-takeout", flag.ContinueOnError)
	fs.SetOutput(errorSink{})

	boolFlag := func(p *bool, name, help string) {
		fs.BoolVar(p, name, false, help)
	}
	fs.StringVar(&f.Output, "output", "", "output path (`-` = stdout; default <project>/MEMORY.md)")
	fs.StringVar(&f.Output, "o", "", "shorthand for --output")
	boolFlag(&f.Force, "force", "overwrite an output file that lacks the generated marker")
	boolFlag(&f.List, "list", "print the matched memory dirs and fact counts; write nothing")
	boolFlag(&f.NoSubdirs, "no-subdirs", "match only the exact project slug")
	fs.StringVar(&f.ClaudeDir, "claude-dir", "", "Claude Code config dir (default $CLAUDE_CONFIG_DIR or ~/.claude)")
	fs.StringVar(&f.ConfigPath, "config", "", "config file path")
	boolFlag(&f.LLM, "llm", "consolidate facts through an LLM")
	fs.StringVar(&f.BaseURL, "base-url", "", "OpenAI-compatible API base URL")
	fs.StringVar(&f.Model, "model", "", "model name (required with --llm)")
	fs.StringVar(&f.APIKeyEnv, "api-key-env", "", "env var holding the API key (default OPENAI_API_KEY)")
	fs.StringVar(&f.Thinking, "thinking", "", "thinking level: off|minimal|low|medium|high|xhigh")
	fs.StringVar(&f.ThinkingStyle, "thinking-style", "", "thinking style: auto|openai|openrouter|none")
	fs.StringVar(&f.ExtraBody, "extra-body", "", "extra JSON object merged into the request body")
	fs.IntVar(&f.MaxPartTokens, "max-part-tokens", DefaultMaxPartTokens, "max input tokens of one LLM call (default 24000)")
	fs.IntVar(&f.MaxOutputTokens, "max-output-tokens", 0, "max completion tokens (0 = server default)")
	fs.DurationVar(&f.Timeout, "timeout", DefaultTimeout, "timeout for the whole --llm phase (default 10m)")
	boolFlag(&f.Quiet, "quiet", "print only warnings and errors")
	fs.BoolVar(&f.Quiet, "q", false, "shorthand for --quiet")
	boolFlag(&f.Verbose, "verbose", "print debug details to stderr")
	fs.BoolVar(&f.Verbose, "v", false, "shorthand for --verbose")
	boolFlag(&f.ShowVersion, "version", "print the version and exit")
	boolFlag(&f.ShowHelp, "help", "print this help and exit")
	fs.BoolVar(&f.ShowHelp, "h", false, "shorthand for --help")
	return fs
}

// errorSink discards the flag package's own error output; Parse callers
// print a single formatted message instead.
type errorSink struct{}

func (errorSink) Write(p []byte) (int, error) { return len(p), nil }
