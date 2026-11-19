# claude-code-memory-takeout

Collect the [Claude Code](https://claude.com/claude-code) auto-memory of a project into one `MEMORY.md` that can be committed, shared and read by any teammate or agent — on any machine.

Claude Code keeps an auto-memory per project in `<claude>/projects/<slug>/memory/*.md` — one file per fact, written across sessions: decisions, measurements, incidents, preferences. Those facts are invisible from the repository: a teammate, another machine or another agent never sees them. This tool checks them out:

- finds every memory dir that belongs to a project — its own slug, its subdirectories and its `.claude/worktrees/*`, including worktrees that were already deleted;
- parses all facts, redacts secrets, and renders them deterministically into one `<project>/MEMORY.md`;
- optionally (`--llm`) asks an OpenAI-compatible model to merge duplicates and drop useless facts while keeping every detail of what remains.

A re-run with unchanged memory produces a byte-identical file, so `MEMORY.md` diffs cleanly in review.

## Install

Homebrew:

```sh
brew install avbel/tap/claude-code-memory-takeout
```

Or grab an archive from the [releases](https://github.com/avbel/claude-code-memory-takeout/releases): `.tar.xz` for macOS and Linux, `.zip` for Windows. Put the binary on your `PATH`.

Go:

```sh
go install github.com/avbel/claude-code-memory-takeout/cmd/claude-code-memory-takeout@latest
```

## Usage

```
claude-code-memory-takeout [flags] [PROJECT_DIR]
```

Run it inside a project (or pass the directory). The output lands in `<project>/MEMORY.md`.

```
PROJECT_DIR   the project directory (default "."). Memory is matched for
              the project itself, its subdirectories and its
              .claude/worktrees/*, including deleted worktrees.

Output:
  -o, --output PATH    output path ("-" = stdout; default <project>/MEMORY.md)
      --force          overwrite an output file that lacks the generated marker
      --list           print the matched memory dirs and fact counts; write nothing
      --no-subdirs     match only the exact project slug

Sources:
      --claude-dir DIR Claude Code config dir (default: config file, then
                       $CLAUDE_CONFIG_DIR, then ~/.claude)
      --config PATH    config file (default $CCMT_CONFIG >
                       $XDG_CONFIG_HOME/claude-code-memory-takeout/config.toml >
                       ~/.config/claude-code-memory-takeout/config.toml)

--llm mode:
      --llm                    consolidate through an LLM before writing
      --base-url URL           OpenAI-compatible base URL (default https://api.openai.com/v1)
      --model NAME             model name (required with --llm)
      --api-key-env NAME       env var holding the API key (default OPENAI_API_KEY)
      --thinking LEVEL         off|minimal|low|medium|high|xhigh
      --thinking-style STYLE   auto|openai|openrouter|none
      --extra-body JSON        extra JSON object merged into the request body
      --max-part-tokens N      max input tokens of one LLM call (default 24000)
      --max-output-tokens N    max completion tokens (0 = server default)
      --timeout DUR            timeout for the whole --llm phase (default 10m)

Misc:
  -q, --quiet          print only warnings and errors
  -v, --verbose        print debug details to stderr
      --version        print the version and exit
  -h, --help           print this help and exit

Exit codes: 0 ok (also "already up to date") · 1 runtime error ·
2 usage error · 3 no Claude memory found · 4 LLM error · 130 cancelled.
```

Examples:

```sh
claude-code-memory-takeout                     # ./MEMORY.md
claude-code-memory-takeout ~/Projects/fluidd --list
claude-code-memory-takeout ~/Projects/fluidd -o -
claude-code-memory-takeout --no-subdirs --force
```

An existing `MEMORY.md` is overwritten only when it carries the generated
marker line (or with `--force`); a hand-written file is never clobbered
accidentally. Identical content is left alone, so re-running does not
touch the file's mtime.

## Config file

Optional, TOML, at `~/.config/claude-code-memory-takeout/config.toml`
(`%APPDATA%\claude-code-memory-takeout\config.toml` on Windows).
Precedence: flag > environment (`CCMT_BASE_URL`, `CCMT_MODEL`, `CCMT_THINKING`, `CCMT_THINKING_STYLE`, `CCMT_API_KEY_ENV`, `CCMT_TIMEOUT`, `CCMT_CONFIG`) > config file > default.

```toml
base_url = "https://api.openai.com/v1"
model = "gpt-5.2"
thinking = "medium"
thinking_style = "auto"
api_key_env = "OPENAI_API_KEY"
max_part_tokens = 24000
max_output_tokens = 0
timeout = "10m"

# extra regexes redacted as [REDACTED:user]
redact_patterns = ["internal-token-[a-z0-9]{20}"]

[extra_body]
temperature = 0.2

[dirs]
claude = "/Users/me/.claude"
```

## `--llm` consolidation

Without `--llm` the output is verbatim: every fact, deterministic
ordering, nothing invented. With it, each section is sent to an
OpenAI-compatible chat/completions endpoint with a strict output contract:

- duplicates and near-duplicates are merged, keeping the union of their
  details, with dated history preserved;
- a fact is dropped only when fully superseded, trivial, or an empty
  placeholder;
- the answer is validated mechanically — the exact section heading, only
  input names, a trailing `<!-- consolidation ... -->` block listing every
  merged or dropped name, all `[REDACTED]` markers intact, and a size
  floor against summarizing. One retry on failure, then that part falls
  back to verbatim with a warning (validation fallback is never fatal);
- the final document carries a `## Consolidation log` section.

```sh
claude-code-memory-takeout --llm --model gpt-5.2 --max-output-tokens 32000
CCMT_BASE_URL=http://localhost:11434/v1 CCMT_MODEL=qwen3:32b claude-code-memory-takeout --llm
```

The redacted text is what is sent; the answer is redacted again before it
reaches the file.

Known limitation: duplicates are only merged within one section part, not
across sections or parts. A split section consolidates each part
independently (see `--max-part-tokens`).

The validator constrains the *shape* of the answer, not its intent: memory
content itself can steer the model's merge and drop decisions, and any
non-empty reason passes. The source memory files stay untouched, so
`MEMORY.md` is always regenerable — treat a reviewed file as the source of
truth, not the model's compression of it.

## Redaction

Redaction is always on: tokens, keys, assignments (`token = "…"`),
authorization headers, URL credentials, private keys and more are replaced
with `[REDACTED]` / `[REDACTED:<kind>]` markers, and home paths fold to
`~`. Add your own patterns with `redact_patterns` in the config file.

**Review `MEMORY.md` before committing it.** Redaction is pattern-based
and can miss secrets written in prose.

## Development

```sh
git config core.hooksPath .githooks   # once; enables the lint pre-commit hook
go test ./...                          # unit tests
go test -tags e2e ./e2e/...            # black-box tests of the built binary
scripts/lint.sh                       # gofmt + vet + go fix + golangci-lint
go test -update ./internal/render/     # rewrite golden files
```

License: MIT.
