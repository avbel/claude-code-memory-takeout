package cli

import (
	"fmt"
	"io"
	"strings"
)

const usageText = `claude-code-memory-takeout [flags] [PROJECT_DIR]

Collect the Claude Code auto-memory of a project — the facts recorded in
<claude>/projects/<slug>/memory — into one deterministic MEMORY.md that
can be committed, shared and read by any teammate or agent.

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
2 usage error · 3 no Claude memory found · 4 LLM error · 130 cancelled.`

// PrintUsage writes the full help text.
func PrintUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, strings.TrimRight(usageText, "\n"))
}
