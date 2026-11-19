# claude-code-memory-takeout — Implementation Plan

> Written for an implementer model. Follow it literally. Where the plan says "verify", check a real local file before coding.
> If something in the real data contradicts this plan, follow the data and record the difference in the final report.
> Template project: `/Users/avbel/Projects/ai-session-to-md` (called **the template** below). It is read-only: copy files out of it, never edit it.

## 0. Context

Claude Code keeps an auto-memory per project: `<claude>/projects/<slug>/memory/*.md`, where `<claude>` is `$CLAUDE_CONFIG_DIR` or `~/.claude`.
Each file holds one fact (YAML frontmatter + markdown body). `MEMORY.md` in that dir is only an index of one-line pointers.
The facts are invisible from the repository: a teammate, another machine, or another agent never sees them.

`claude-code-memory-takeout` is a single-binary Go CLI with this flow:
1. Take a project directory (default: the current dir).
2. Find every Claude memory dir that belongs to it: the project itself, its subdirectories and its `.claude/worktrees/*` (also deleted ones).
3. Parse all facts, redact secrets, and render them deterministically into one `<project>/MEMORY.md`.
4. Optionally (`--llm`), ask an OpenAI-compatible LLM to remove duplicates and useless facts per section, while keeping all details of what remains.

Repository: `/Users/avbel/Projects/claude-code-memory-takeout`. It holds only this file and is not a git repo yet. The GitHub repo is expected to be `github.com/avbel/claude-code-memory-takeout`. License: MIT.

## 1. Decisions (fixed, do not revisit)

| Topic | Decision |
|---|---|
| Language | Go **1.27** (`go 1.27` in go.mod), same idioms as the template's PLAN.md §2. |
| Sources | Only Claude Code auto-memory: the project's own slug, subdirectory slugs and worktree slugs. **No** yrem, Codex, CLAUDE.md/AGENTS.md. |
| Matching | Forward-encode real paths into slugs and compare. **Never decode a slug back into a path**, never use plain string-prefix matching on slugs, and never use session `cwd` fields (see §6). |
| Default mode | Verbatim and deterministic: no timestamps or version in the output, stable ordering, so a re-run with unchanged memory produces a byte-identical file. |
| `--llm` mode | Optional consolidation through the template's hand-written OpenAI-compatible client (`internal/llm`). One call per section part. The output is validated; on failure there is one retry, then that part falls back to verbatim. |
| Output | `<project>/MEMORY.md` by default; `-o PATH` overrides; `-o -` writes to stdout. An existing file is overwritten only if it carries our marker line or `--force` is given. |
| Secrets | Redaction is always on (the template's `internal/redact` plus extra rules). In `--llm` mode the redacted text is what is sent, and the LLM answer is redacted again. |
| Read-only | Never write anything under `<claude>`. |
| UI | Plain stderr lines (no TUI, no spinner, no bubbles/huh/lipgloss). `-q` silences info lines, `-v` adds debug lines. |
| Distribution | Identical to the template: CI on ubuntu/macos/windows; release on `v*` tags for macOS arm64, Linux amd64/arm64 and Windows amd64/arm64; Homebrew formula pushed to `avbel/homebrew-tap`. |

## 2. Implementer rules

- Same as the template's PLAN.md §2: Go 1.27 idioms, braces always with bodies on their own lines, functions under 50 lines, files 200–400 lines (800 max), return new values instead of mutating inputs, wrap every error with context, comment only the why.
- `go fix ./...` and `scripts/lint.sh` must leave no diff and no findings. Never `git commit --no-verify`. Never add `//nolint` without a reason on the same line.
- **No real memory data in the repo.** All fixtures are hand-written and synthetic. Real memories on this machine contain production hosts, dataset IDs and tokens.
- **Git:** run `git init` once at step 1. Make no commits and no branches unless the user asks.
- Tolerance: a malformed fact file is a warning ("skipped N unreadable memory files", listed with `-v`), never fatal.

## 3. Dependencies (verify latest versions with `go get <module>@latest`)

| Module | Why |
|---|---|
| `github.com/BurntSushi/toml` | config file (same as the template) |
| `go.yaml.in/yaml/v3` | frontmatter parsing. This is the maintained YAML org fork; **do not** use `gopkg.in/yaml.v3`, which is archived. Verify on pkg.go.dev that the module path and a recent release exist; if `v4` is stable, use it instead. |

Nothing else. The stdlib covers flags, HTTP, embed and filepath.

### 3.1 Size budget
Measure the linux/amd64 release build (`CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -buildid="`) and the `.tar.xz` once step 9 is done. Set `MAX_BIN` and `MAX_TARXZ` in release.yml to the measured size × 1.5, rounded up to 0.5 MiB. Record the measured numbers in a comment there.

## 4. CLI specification

```
claude-code-memory-takeout [flags] [PROJECT_DIR]

PROJECT_DIR          default "."; must exist and be a directory (else exit 2)
-o, --output PATH    default <project>/MEMORY.md; "-" = stdout
    --force          overwrite an output file that lacks our marker line
    --list           print the matched memory dirs and fact counts; write nothing
    --no-subdirs     only the exact project slug (no subdirectory/worktree slugs)
    --claude-dir DIR default: config dirs.claude > $CLAUDE_CONFIG_DIR > ~/.claude
    --config PATH    default: $CCMT_CONFIG > $XDG_CONFIG_HOME/claude-code-memory-takeout/config.toml
                     > ~/.config/... (Windows: %APPDATA%\claude-code-memory-takeout\config.toml)
--llm mode:
    --llm                    enable consolidation
    --base-url URL           default https://api.openai.com/v1
    --model NAME             required with --llm
    --api-key-env NAME       name of the env var holding the key (default OPENAI_API_KEY)
    --thinking LEVEL, --thinking-style STYLE, --extra-body JSON   as in the template
    --max-part-tokens N      default 24000; input size of one LLM call (see §10)
    --max-output-tokens N    default 0 = server default
    --timeout DUR            default 10m, for the whole --llm phase
-q, --quiet   -v, --verbose   --version   -h, --help
```

Precedence: flag > env (`CCMT_BASE_URL`, `CCMT_MODEL`, `CCMT_THINKING`, `CCMT_THINKING_STYLE`, `CCMT_API_KEY_ENV`, `CCMT_TIMEOUT`, `CCMT_CONFIG`) > config file > default.
Config keys: `base_url`, `model`, `thinking`, `thinking_style`, `api_key_env`, `max_part_tokens`, `max_output_tokens`, `timeout`, `redact_patterns` (array of regexes), `extra_body` (table), `[dirs] claude`.
Flags may appear before or after the positional argument (copy the template's parse loop in `internal/cli/args.go`).

Exit codes: `0` ok (also "already up to date"), `1` runtime error, `2` usage error (including "output exists without marker, use --force"), `3` no memory found, `4` LLM failure (network, auth, HTTP error — **not** validation fallback, which is a warning), `130` cancelled (Ctrl-C).

stderr lines (info level, suppressed by `-q`):
```
memory: 3 dirs, 118 facts (. = 112, .claude/worktrees/ebay-keyset-pool = 4, packages/web = 2)
redacted: 3 values (assignment=2, jwt=1)
llm: Project part 1/2 … kept 61, merged 9, dropped 4
wrote /abs/path/MEMORY.md            | or: MEMORY.md is up to date
```
Warnings (always shown, prefixed `warning:`): skipped files, a fact named in the index but missing, ambiguous subdirectory resolution, LLM validation fallback.
Exit 3 message: `no Claude memory for <abs path>; looked for <claude>/projects/<slug>/memory` (print the computed slug, which helps debugging).

## 5. Project layout

```
cmd/claude-code-memory-takeout/
  main.go            app struct with injectable stdout/stderr/getenv/version (copy the template's main.go)
  run.go             Run(args) int, exit-code mapping, wiring of the steps below
internal/cli/        args.go (+_test): flags per §4
internal/config/     trimmed copy of the template's config + toml (only the §4 keys)
internal/locate/     slug.go (Encode), locate.go (Find) + tests                      §6
internal/memory/     fact.go (Fact type), parse.go (ParseFile), load.go (LoadDirs) + tests   §7
internal/render/     render.go, anchor.go, links.go, headings.go + tests             §8
internal/redact/     copy of the template package + extra rules                     §9
internal/llm/        copy of the template package (rename the user agent)
internal/consolidate/ prompts/*.md (go:embed), split.go, validate.go, consolidate.go + tests   §10–11
internal/output/     write.go: marker check, unchanged check, atomic write          §12
e2e/                 black-box tests of the built binary                            §13
testdata/memory/<case>/*.md   hand-written fact files for unit tests
```

## 6. Locating memory dirs (`internal/locate`)

### 6.1 Slug encoding — `Encode(absPath string) string`
Claude Code builds the slug by replacing every character that is not `[A-Za-z0-9]` with `-`. Observed on this machine:
- `/Users/avbel/Projects/fluidd` → `-Users-avbel-Projects-fluidd`
- `/Users/avbel/Projects/fluidd/.claude/worktrees/ebay-keyset-pool` → `-Users-avbel-Projects-fluidd--claude-worktrees-ebay-keyset-pool`
- `/private/tmp/claude-501/-Users-avbel-Projects-yrem/<uuid>/scratchpad` → `-private-tmp-claude-501--Users-avbel-Projects-yrem-<uuid>-scratchpad`

Implement it per rune: ASCII letters and digits are kept, everything else becomes one `-`.
Golden tests: the three cases above, plus `C:\Users\me\proj` → `C--Users-me-proj`.

**Unverified (do not invent rules; write a test that documents the chosen behaviour and note it in the report):**
- Non-ASCII runes. Claude Code is JavaScript, so a character outside the BMP is probably 2 UTF-16 units → `--`. Implement 1 rune = 1 `-`, except runes > U+FFFF = `--`.
- Very long paths. The longest slug on this machine is 100 chars. Newer Claude Code versions may truncate long slugs and append a hash. Implement: if `len(Encode(p)) > 200`, also accept any slug dir whose name starts with the first 200 chars of the encoding, and print a `warning:` saying the match is by truncated prefix.

### 6.2 Which paths to encode
`root := filepath.Abs(PROJECT_DIR)`. Also compute `filepath.EvalSymlinks(root)`; if it differs (macOS `/tmp` → `/private/tmp`), match with both encodings and take the union.
Do **not** climb to the git root: the given dir is the project.

### 6.3 `Find(claudeDir, root string, subdirs bool) ([]Match, error)`
`Match{Slug string; Dir string /*abs memory dir*/; Rel string /*path relative to root, "." for the root*/}`

```
base := Encode(root)
for each entry e in <claudeDir>/projects (dirs only, sorted by name):
    if e has no memory/ dir or memory/ has no *.md files → skip
    if e.Name() == base → Match{Rel: "."}
    else if subdirs && strings.HasPrefix(e.Name(), base+"-"):
        rest := e.Name()[len(base):]               // starts with "-"
        if strings.HasPrefix(rest, "--claude-worktrees-") && len(rest) > len("--claude-worktrees-"):
            Match{Rel: ".claude/worktrees/" + rest[len("--claude-worktrees-"):]}   // also when the worktree was deleted
        else if rels := resolve(root, rest); len(rels) > 0:
            Match{Rel: rels[0]}; if len(rels) > 1 → warning "ambiguous <slug>: <rels...>, using <rels[0]>"
        else:
            verbose "skipped <slug>: no matching subdirectory" (this is how siblings such as fluidd-bench are excluded)
    for every Match except worktree ones: collision check (below)
```

**Collision check.** A slug can encode two real paths at once: `fluidd/packages` and the sibling `fluidd-packages` both give `-Users-avbel-Projects-fluidd-packages`, and so do `fluidd-pre` and `fluidd/pre`.
Run the same guided descent from the filesystem root (`/`, or the volume root `C:\` on Windows) over the full slug. If it finds a real directory outside `root`, keep the match but print `warning: <slug> also matches <other path>; its memory may belong there`. The check reads only the directories along the slug, so it stays cheap.

`resolve(dir, rest)` is a guided descent that reads only directories on the way, so huge trees such as `node_modules` are never walked:
```
for each child c of dir (os.ReadDir, directories only, do not follow symlinks), sorted by name:
    enc := "-" + Encode(c.Name())
    if rest == enc            → result filepath.ToSlash(rel path of c)
    else if HasPrefix(rest, enc+"-") → recurse into c with rest[len(enc):]
return all results, sorted
```

Why session `cwd` cannot be used (verified on this machine 2026-11-18, do not revisit): the first `cwd` in the sessions of both fluidd worktree slugs is a different directory than the slug encodes. 17 of the ~30 dirs that hold memory have no `*.jsonl` left at all.

Required unit tests (build the trees in `t.TempDir()`):
- exact match; subdir `packages/web` → slug `<base>-packages-web`
- sibling `<parent>/fluidd-bench` is **not** matched when root is `<parent>/fluidd`
- `fluidd-pre/Fluidd-3` vs `fluidd/pre-Fluidd-3`: only the path that exists is matched
- both `a/b-c` and `a/b/c` exist → ambiguous warning, first one sorted wins
- root `<tmp>/proj` with subdir `packages`, plus a sibling `<tmp>/proj-packages` → matched as `packages`, with the collision warning naming the sibling
- deleted worktree slug `<base>--claude-worktrees-x` is matched with Rel `.claude/worktrees/x`
- a slug with an empty `memory/` is skipped; `--no-subdirs` returns only the exact match
- a scratchpad slug `-private-tmp-claude-501--<base>-<uuid>-scratchpad` never matches

## 7. Parsing facts (`internal/memory`)

`Fact{Name, Description, Type, Body, File /*base name*/, Rel /*Match.Rel*/ string; Modified time.Time /*zero if absent*/}`

`ParseFile(path)`:
- Skip `MEMORY.md` (any case). Read only `*.md` directly in the memory dir (not recursive).
- First replace `\r\n` with `\n` and strip a leading UTF-8 BOM. Files written on Windows, and fixtures checked out by Windows CI, would otherwise fail the `---` check.
- Frontmatter = the lines between a first line equal to `---` and the next line equal to `---`. Parse it with yaml into
  `struct{Name, Description, Type string; Metadata struct{Type, Modified string}}`; unknown keys are ignored (`originSessionId`, `node_type`, …).
- `metadata.modified` is RFC 3339 with milliseconds (`2026-11-16T10:29:00.000Z`, present in 219 of 437 files on this machine). Parse it with `time.RFC3339Nano`; a bad value → zero time, no warning. It is used only as an LLM hint (§10), never rendered in verbatim mode.
- `Type = cmp.Or(Metadata.Type, Type)`, lower-cased. Both placements exist on this machine (410 files with `metadata.type`, 27 with top-level `type`).
- `Name = cmp.Or(strings.TrimSpace(Name), file stem)`.
- No frontmatter → the whole file is the body, Name = file stem, Type = "". YAML error → warning, same fallback.
- Body = the text after the closing `---`, with surrounding blank lines trimmed. An empty body with an empty description → skip with a warning.

`LoadDirs(matches)` returns the facts of all dirs plus warnings:
- Read the index `MEMORY.md` of each dir. A pointer `[..](file.md)` whose file is missing → warning `index points to missing <rel>/<file>`. Files that are not in the index are still included (count them in verbose output).

## 8. Rendering (`internal/render`)

### 8.1 Document format (golden-test this exactly)
```
<!-- generated by claude-code-memory-takeout from Claude Code memory; edit the source memories, not this file -->
# Memory: <base name of project dir>

Sources: `.` (112), `.claude/worktrees/ebay-keyset-pool` (4)
Facts: 116 · redacted values: 3 · mode: verbatim

## User
### <heading>
> <description>

<body>

## Feedback
…
## Project
…
## Reference
…
## Other
…
## Consolidation log        ← only in --llm mode when something was merged or dropped
- <name> → merged into [<target>](#<anchor>)
- <name> → dropped: <reason>
```
- The first line is **the marker**; `internal/output` relies on it byte for byte.
- `mode: verbatim` or `mode: llm (<model>)`. The Sources line lists Rel values in Find order, with fact counts.
- Section order is fixed: User, Feedback, Project, Reference, Other (unknown or empty type). Empty sections are omitted.
- Within a section, sort by Name, then Rel, then File.
- No dates, no tool version, no absolute paths outside the project (redact folds home to `~`).
- Exactly one blank line between blocks; the file ends with one `\n`.

### 8.2 Headings and anchors
- Heading text = Name. When the same Name occurs more than once in the project (seen on this machine: `batch-checks-and-targeted-tests`), the heading becomes `<name> (<Rel>)` for every occurrence except the one with Rel `.`. If two share the same Rel, append ` (<File>)`.
- `Anchor(heading)` = GitHub's algorithm: lower-case; drop every character that is not a letter, digit, space, `-` or `_`; spaces → `-`. Add a test against a few headings with known GitHub anchors.
- GitHub numbers anchors over **every** heading in the document, in order: the `# Memory:` title, `## User`/`## Project`/… sections, fact headings, and demoted body headings. A repeated anchor becomes `x-1`, `x-2`, and so on. So a fact named `project` gets `#project-1`, because `## Project` comes first.
  Assign anchors in one pass in document order with this rule, and resolve links (§8.3) to the assigned anchor. Test: a fact named `project`, and a body heading equal to a fact name.
- Description: collapse every whitespace run, newlines included, into one space so that it fits on one `> ` line. An empty description renders no `> ` line.

### 8.3 Links (`links.go`)
Applied to every body after redaction:
- `[[x]]` and `[[x|label]]` → `[label-or-x](#anchor)` when x resolves to a fact, otherwise plain `label-or-x`. Resolution prefers a fact with the same Rel, then Rel `.`, then the first by sort order.
- Markdown links whose target is a bare `<file>.md` of a loaded fact in the same dir → `(#anchor)`. All other links are untouched.
- Text inside fenced code blocks (``` or ~~~) and inline code is never rewritten.

### 8.4 Body headings (`headings.go`)
Bodies may contain `#`…`######` lines. They would break the document structure, so outside code fences shift every heading in a body so that its shallowest level becomes `####` (cap at `######`).

## 9. Redaction (`internal/redact`)

Copy the template package and its tests unchanged, then add these rules to `patterns.go`, each with a positive and a negative test:
- `\bydb_[0-9a-f]{32,}\b` → kind `yrem_token` (a yrem/YantrikDB token format seen on this machine)
- `(?i)\b(token|secret|password|api[_-]?key)\s*=\s*"[^"\n]{8,}"` is already covered by the `assignment` rule; add a test case that proves it covers TOML `token = "…"`.

The template emits **two marker forms**: `[REDACTED:<kind>]` from `marker(kind)`, and a bare `[REDACTED]` from the rules with a `repl` (`url_credential`, `bearer`, `basic_auth`, `curl_user`, `assignment`). Everything downstream (prompt, validator, tests) must treat any text starting with `[REDACTED` as a marker. Do not change the template's output forms.

Apply redaction per fact to Description and Body **before** rendering, so anchors and the header are never touched. Sum the counts (`Result.Total()`) for the header and the stderr line. The `home` kind is not a secret and is excluded, as in the template.
User patterns come from config `redact_patterns`.

## 10. LLM consolidation (`internal/consolidate`)

Only with `--llm`. Input = the redacted facts, already grouped into sections.

1. **Render each section** with the same code as §8, but without the document header: `## <Section>` followed by its facts. Links are already `[x](#anchor)`.
   In this LLM input only, a fact with a non-zero `Modified` gets the line `<!-- modified: YYYY-MM-DD -->` directly after its `### ` heading, so the model can resolve conflicts by recency. Post-processing removes every `<!-- modified: … -->` line from the answer.
2. **Split** a section whose `EstimateTokens` (copy the template's `len(bytes)/4` from `internal/reduce/budget.go`) exceeds the part budget into parts at fact boundaries.
   Part budget = `--max-part-tokens`. When `--max-output-tokens` > 0, cap the budget at `0.8 × max-output-tokens`, because the answer is roughly as long as the input. When `--max-output-tokens` is 0, print once: `warning: --max-output-tokens not set; parts of up to <budget> tokens may be truncated by the server`. Split parts greedily in sort order. Every part repeats the `## <Section>` line. A single fact larger than the budget is its own part.
   Measured 2026-11-18: the largest memory on this machine is 230 KB / 112 facts (≈ 58k tokens), mostly in Project, so it will become 2–3 parts at the default 24000.
3. **Call** sequentially, one part at a time, via `llm.Client.Chat` with the system prompt and the user message from §11. The whole phase runs under `--timeout`.
4. **Validate** (§11.4). On failure, send the retry message with the error list once. If it still fails, or the client returned `llm.ErrTruncated`, use the verbatim part.
   `llm.ErrTruncated` does not exist in the template: there, `client.go` returns a plain `fmt.Errorf("llm: response truncated (finish_reason length) …")`. In the copy, add `var ErrTruncated = errors.New("llm: response truncated")`, wrap it at that return (`fmt.Errorf("%w (finish_reason length); raise --max-output-tokens", ErrTruncated)`), and add a test for `errors.Is`. ErrTruncated is a fallback, not exit 4. Every other client error is still exit 4. On fallback, print `warning: llm output for <Section> part i/n failed (<first error>); kept verbatim`.
5. **Post-process** a valid answer in Go (never trust the model for these):
   - Run the redactor again over the answer.
   - Parse the trailing `<!-- consolidation … -->` block into log entries and remove it from the text.
   - Rewrite links: a link to a merged name → the merge target's anchor; a link to a dropped name → plain text. Apply this over **all** sections after all parts are done, since links can cross sections.
   - Re-apply §8.4 heading demotion to the bodies.
6. **Assemble** with the §8.1 header (`mode: llm (<model>)`; `Facts:` = facts kept), the consolidated sections, and a `## Consolidation log` built from all entries (sorted by section order, then name). Print `kept/merged/dropped` per part to stderr.

Known limitation (document it in the README): duplicates are only merged within one section part, not across sections or parts.

## 11. Prompts (`internal/consolidate/prompts/`)

Store each text below as its own file, embed it with `//go:embed`, and fill placeholders with `strings.NewReplacer` (no `text/template`: the input may contain `{{`). Send no `temperature`: some reasoning endpoints reject it; users who want it set it in `extra_body`.

### 11.1 `prompts/system.md` (exact text)
```
You consolidate one section of a developer's project memory file. The memory is a set of facts that a coding agent recorded across sessions: decisions, measurements, incidents, user preferences, environment details. Your output replaces the input section verbatim, so precision matters more than brevity.

Goal, in the owner's words: "keep all main facts and details, just remove dups and remove useless facts."

WHAT TO DO
1. Merge duplicates and near-duplicates into one fact. Keep the union of their details; never keep only the shorter version. Where two facts disagree, keep the newer or more specific statement, keep the older one as a dated "(was: ...)" or "supersedes ..." note so the history stays visible, and say which won. A `<!-- modified: YYYY-MM-DD -->` line under a heading tells you when that fact was last changed; use it to decide which is newer when the body has no dates.
2. Drop a fact only when it is (a) fully superseded by a newer fact that already states the replacement, (b) trivial or already obvious from the code or git history (e.g. "the file X exists", "commit Y did Z" with nothing beyond the commit message), or (c) an empty placeholder. A fact that records that another fact is stale or wrong is a correction, not a superseded fact: keep it. When in doubt, keep it.
3. Keep everything else without shortening: every number, unit, ID, hash, path, command, URL, port, date, time, version, model name, task reference (#NNN), the **Why:** and **How to apply:** lines, and incident narratives with their timestamps. Do not paraphrase in a way that loses precision. Do not reorder or collapse dated log entries into "current state only". Facts about the same topic measured on different hardware, models or dates are different facts: never average them or keep only one. Do not invent, infer or "complete" facts. Keep the original language and wording; fixing typos is fine.
4. `[REDACTED]` and `[REDACTED:...]` markers are secrets that were removed before you saw the text. Copy them exactly as written, never guess, expand, or describe what they hid.
5. You may be looking at only one part of a split section (see "Part N of M" in the message). Consolidate only within what you see; never reference, assume, or re-create facts from other parts, and never add a `##` heading you were not given.

OUTPUT FORMAT (checked mechanically; a deviation is rejected)
- Start with exactly one `## <Section>` line, identical to the input's heading. No text before it, no commentary after the last fact, no code fence around the whole output.
- Each fact is:
    ### <name>
    > <description>

    <body markdown>
  `<name>` must be a name that exists in the input. Never introduce a new name. A merged fact keeps exactly one of the merged names (prefer the most general or most recent one); the other names go to the consolidation block.
- The `> description` line is one line; keep it, or update it so it summarizes the merged fact (include the key dates/numbers the originals carried).
- Links look like `[name](#name)`. If the target was merged, point the link at the merge target: `[name](#target)`. If the target was dropped, replace the link with plain text `name`. Leave links alone when the target is not in your input (it lives in another section or part).
- Every input name that is missing from your output MUST be listed in a trailing block, exactly in this form (one line per name, nothing else inside):
    <!-- consolidation
    - <name> -> merged into <target-name>
    - <name> -> dropped: <short reason>
    -->
  Omit the block entirely when every input name is present in the output.
- Keep unchanged facts byte-for-byte where possible; only edit what consolidation requires.
```

### 11.2 `prompts/user.md` (exact text)
```
Section: {{SECTION}}
Part: {{PART}}

Consolidate the section below. Return the full consolidated section in the required format and nothing else.

{{INPUT}}
```
`{{SECTION}}` = heading text without `## ` (e.g. `Project`); `{{PART}}` = `1 of 1`, `2 of 3`; `{{INPUT}}` = the rendered part starting with its `## ` line.

### 11.3 `prompts/retry.md` (exact text)
Sent as a follow-up in the same conversation: messages = system, original user, the rejected assistant answer, then this as a user message.
```
Your previous answer was rejected by the validator:

{{ERRORS}}

Produce the whole consolidated section again, fixing only these problems. Reminders:
- exactly one `## {{SECTION}}` heading, nothing before it and no trailing commentary;
- every `### <name>` must be a name from the input, no new names;
- every input name that is absent must appear in the `<!-- consolidation ... -->` block as `- <name> -> merged into <target>` or `- <name> -> dropped: <reason>`, with `<target>` present in your output;
- links stay `[name](#name)`; retarget merged ones, un-link dropped ones;
- `[REDACTED]` / `[REDACTED:...]` markers unchanged;
- do not shorten or drop details to make the output fit.
```
`{{ERRORS}}` = one `- <error>` line per problem, at most 20.

### 11.4 Validator (`validate.go`) — every rule returns a readable error string
1. Trim the answer. If the whole answer is wrapped in one ``` fence, strip that fence once, then validate.
2. The first line must be exactly `## <Section>`; no other line outside code fences may start with `## `.
3. Collect `### <name>` headings **outside fenced code blocks** (track fence state). Each name must be in the input name set; a name may occur only once.
4. If that input fact had a description: the first non-blank line after its heading (ignoring `<!-- modified: … -->`) must start with `> `.
5. Parse the trailing `<!-- consolidation … -->` block (lines `- <name> -> merged into <target>` or `- <name> -> dropped: <reason>`; also accept `→` for `->`). The set of names in the block must equal input names − output names exactly. Every merge target must be an output name. A dropped entry needs a non-empty reason.
6. Markers are matched by the regex `\[REDACTED(:[a-z_]+)?\]` (both forms, §9). Every distinct marker text that occurs in a kept or merge-target input fact must still occur in the answer; the answer must contain no marker text the input did not have.
7. Size floor: answer bytes ≥ 50 % of the bytes of the input facts that are still present (kept or merge targets). This catches summarizing instead of consolidating. A false positive only costs a verbatim fallback.

Link retargeting is **not** validated; Go rewrites links itself in §10 step 5.

### 11.5 Hard cases seen in real memory (put each into a consolidate unit test as a fake-model scenario, and check them by hand in §16 step 4)
- Near-duplicates with unique dated lines (e.g. two "which model for which task" facts with "was Haiku → Sonnet" reversals): a merge is valid only if every dated line survives.
- Time-ordered log facts where later entries partly override earlier ones (container names renamed, SSH rules changed three times on one day): history must stay.
- A fact that records that another fact is stale ("was never merged; code still uses `>= 2`"): it must survive and must not be treated as superseded.
- Look-alike benchmarks on different hardware or models (0.077 vs 0.77 img/s): never merged by averaging; the size floor and rule 3 are the safety net.

## 12. Output (`internal/output`)

`Write(path string, content []byte, force bool) (status, error)`:
- `-o -` → write to stdout, done.
- If the file exists: identical bytes → status "up to date" (do not rewrite, keep mtime). The first line is not the marker and `!force` → usage error `"<path> exists and was not generated by claude-code-memory-takeout; use --force"`.
- Write atomically: `os.CreateTemp` in the same dir, write, `Sync`, `Close`, `os.Rename`. Mode 0644. Remove the temp file on any error.
- `--list` never reaches this step.

## 13. Tests

- **Unit:** per package as listed in §6–§12. Golden files live under `testdata/golden/*.md` with an `-update` flag (pattern from the template's `internal/handoff/render_test.go`).
- **LLM:** copy the template's `internal/llm` tests unchanged (rename only). Test consolidate with a fake `Chat` func: valid answer; invented name → retry → valid; invalid twice → verbatim fallback and warning; `length` → verbatim; link rewrite after merge/drop; secrets in the answer get redacted.
- **e2e** (`//go:build e2e`, copy the template's `e2e/main_test.go` `TestMain` build + `testEnv` fake home, `CLAUDE_CONFIG_DIR`, and `fakellm_test.go` reduced to an OpenAI-compatible handler returning canned answers). Build the project tree and its memory slugs inside `t.TempDir()`, computing each slug with the same rule as §6.1 written independently in the test, not by importing `internal/locate`. Scenarios:
  1. verbatim: root + subdir + deleted worktree included, sibling excluded; output equals golden
  2. second run → "up to date", file unchanged
  3. existing hand-written MEMORY.md → exit 2; with `--force` → written
  4. `-o -` → stdout, no file written; `--list` → no file written
  5. no memory → exit 3, message contains the computed slug
  6. secret in a fact → `[REDACTED` in the output, never the value
  7. `--llm` with the fake server: merged + dropped facts, consolidation log present, the request body contains no secret value
  8. `--llm` with 401 → exit 4; invalid output twice → exit 0 with warning and verbatim section
  9. a Windows-path test runs only on windows (`C:` drive slug)

## 14. Implementation steps (each step ends green: `go test ./...` + `scripts/lint.sh`)

1. **Scaffold.** `git init`; `go mod init github.com/avbel/claude-code-memory-takeout`; `go 1.27`. Copy from the template and rename `ai-session-to-md` → `claude-code-memory-takeout`, `AISMD_` → `CCMT_`:
   `LICENSE`, `.gitignore` (`/dist/`, `/claude-code-memory-takeout`, `*.test`, `/.claude/`), a new `.gitattributes` with `* text=auto eol=lf` (the template has none; it keeps fixtures and goldens LF on Windows CI), `.golangci-version`, `scripts/lint.sh`, `.githooks/pre-commit`, `cmd/.../main.go` with `--version`/`--help` only.
   `.golangci.yml`: copy it, then rewrite the exclusion paths. There is no G204 (no exec). G304 applies to `internal/(memory|locate|output|config)`; G306 is not needed (0644 via CreateTemp + Chmod; if gosec still flags it, exclude for `internal/output` with a reason).
   `git update-index --chmod=+x scripts/lint.sh .githooks/pre-commit` after the first `git add`; `git config core.hooksPath .githooks`.
   Done when: `go build ./... && scripts/lint.sh` pass.
2. **locate** (§6). Done when: all §6.3 tests pass.
3. **memory** (§7) with fixtures under `testdata/memory/`. Done when: tests cover both type placements, no frontmatter, bad YAML, empty fact, index pointer to a missing file, unindexed file.
4. **redact** (§9). **render** (§8). Done when: golden test for a fixture set covering all sections, duplicate names, `[[links]]` resolved/unresolved, `.md` links, body headings and a code fence containing `[[x]]` and `# x`.
5. **cli + config + output + run wiring** (verbatim mode end to end). Done when: `go run ./cmd/claude-code-memory-takeout ~/Projects/card-number-extractor -o -` prints 6 facts, all in `## Project` (counted 2026-11-18).
6. **llm** copy (§10), **consolidate** + prompts (§10–11). Done when: the consolidate tests in §13 pass.
7. **e2e** (§13). Done when: `go test -tags e2e ./e2e/...` passes locally.
8. **README.md**: what it does, install (Homebrew + release archives), usage with every flag, config example, `--llm` example with an OpenAI-compatible server, the redaction note ("review before committing MEMORY.md"), the known limitation of §10, Development (`git config core.hooksPath .githooks`).
9. **CI and release** (§15). Measure the size (§3.1).

Parallel waves: {2, 3, 9's ci.yml}, then {4, 6}, then 5, then {7, 8}, then 9's release.yml.

## 15. GitHub Actions and packaging

Copy `.github/workflows/ci.yml`, `.github/workflows/release.yml` and `packaging/homebrew/ai-session-to-md.rb.tmpl` (→ `claude-code-memory-takeout.rb.tmpl`) from the template and change only:
- every `ai-session-to-md` → `claude-code-memory-takeout`, `./cmd/ai-session-to-md` → `./cmd/claude-code-memory-takeout`
- **the pinned golangci-lint-action SHA:** use the commit `ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a` (v9.3.0) in both files. The template's ci.yml pins `d583c34…`, which is the annotated tag object of the same release, not the commit (verified with `gh api` on 2026-11-18). Keep every other pin as in the template.
- release.yml `test` job: add the `go test -tags e2e ./e2e/...` step that ci.yml has and the template's release job lacks
- size guard: the numbers from §3.1
- Formula: class `ClaudeCodeMemoryTakeout`, `desc "Collect Claude Code project memory into one MEMORY.md"`, `bin.install "claude-code-memory-takeout"`, test `--version`

## 16. Verification (on this machine, after step 9)

1. `claude-code-memory-takeout ~/Projects/fluidd --list` → only `.` (112 facts: user 1, feedback 16, project 91, reference 4; counted 2026-11-18, the memory may have grown since). `fluidd-bench`, `fluidd-packages*`, `fluidd-pre*` must **not** appear. The two worktree slugs have no memory, so they don't appear either.
2. `claude-code-memory-takeout ~/Projects/fluidd-pre --list` → `Fluidd-3` with 14 facts (feedback 3, project 6, reference 5). The root slug `-Users-avbel-Projects-fluidd-pre` exists but has no memory, so it is skipped.
3. `claude-code-memory-takeout ~/Projects/fluidd -o /tmp/ccmc-fluidd.md`; run it twice → second run "up to date". Read the output: no `[[`, no raw `---` frontmatter, every section heading present, redaction count reported. Grep it for `ydb_`, `sk-` and `token =` values.
4. `--llm` against the user's configured endpoint (ask the user for model/base URL; do not guess): check the consolidation log for wrongly dropped facts on a sample of 10.
5. Report: the unverified points of §6.1 and anything in real data that contradicted this plan.

## 17. Manual steps for the user (the implementer does not do these)

- Create the GitHub repo `avbel/claude-code-memory-takeout` and add the remote.
- Add the repository secret `HOMEBREW_TAP_TOKEN` (same as the template repo) so release can push the formula.
- Decide per project whether to commit `MEMORY.md`. Review it first: redaction is pattern-based and can miss secrets written in prose.

## 18. Out of scope (v1)

yrem / Codex / OpenCode memories, CLAUDE.md and rules files, writing memory back into `<claude>` (import), cross-section deduplication, a TUI picker, watching for changes.
