# Corvex

> AI-powered development orchestrator — decompose specs into tasks, execute with AI agents, validate automatically.

Corvex is an open-source CLI tool written in Go that orchestrates AI agents to execute complex software development tasks autonomously. You define a specification, Corvex decomposes it into a DAG of tasks, executes each one in a fresh context, validates the result with an independent reviewer, and advances automatically.

## Features

- **Task Pipeline with DAG** — Topological sort, dependency resolution, and anchored summarization between tasks
- **Planner → Worker → Reviewer** — Infrastructure-enforced separation between orchestration and execution
- **Reviewer escalation tree** — Categorised rejections escalate via configurable policies (upgrade-model, spawn-investigation, human-prompt) instead of looping blindly
- **A/B model comparison** — `corvex run --task S03 --ab sonnet,opus` runs the same task on two worktrees and merges the winner; outcomes feed `.corvex/ab-stats.json`
- **Sandbox profiles** — Docker / local / Nix (reads `flake.nix`) / Devcontainer (delegates to the official `@devcontainers/cli`)
- **MCP servers** — Declare Postgres, Playwright, GitHub, etc. servers in `config.yaml`; only the Worker receives them, the Planner and Reviewer stay clean
- **Model-agnostic** — Pluggable provider interface (Claude CLI for MVP, extensible to OpenAI, Ollama)
- **Professional TUI** — Vertical layout, soft palette, every visible key wired (help `?`, filter `/`, detail `↵`, pause `p`, skip `s`, retry `r`, logs `l`)
- **Git Checkpointing** — Automatic commits after each task for crash recovery
- **Custom Hooks** — `pre-task`, `post-task`, `on-success`, `on-failure` (per task) and `post-run` (once when a run finishes) shell scripts. `post-run` gets `CORVEX_PROJECT` and `CORVEX_STATUS` (passed|partial) — wire it to emit the run's migrations as SQL, open a PR, notify, etc.
- **Agent Routing** — Map task types to specialized agent prompts

## Installation

### From source (requires Go 1.24+)

```bash
go install github.com/giovannialves/corvex@latest
```

### From GitHub Releases

Download the latest binary for your platform from the [Releases](https://github.com/giovannialves/corvex/releases) page.

```bash
# macOS / Linux
tar xzf corvex_*_$(uname -s)_$(uname -m).tar.gz
sudo mv corvex /usr/local/bin/
```

### Homebrew (macOS)

```bash
brew install giovannialves/tap/corvex
```

## Quickstart

```bash
# 1. Initialize a Corvex project
corvex init

# 2. Create your project specification
mkdir -p .corvex/tasks/my-feature
cat > .corvex/tasks/my-feature/spec.md << 'EOF'
# My Feature

## Objective
Implement user authentication with JWT tokens.

## Requirements
- Login endpoint with email/password
- JWT token generation and validation
- Protected route middleware

## Validation
- All tests pass
- API responds correctly
EOF

# 3. (Optional) Interview the spec to resolve ambiguities before planning
corvex grill my-feature

# 4. Generate task plan from the spec (uses decisions.md if grill was run)
corvex plan my-feature

# 5. Execute all tasks
corvex run my-feature

# 6. Check progress
corvex status my-feature
```

### Grill: refine the spec before you commit to a plan

`corvex grill` puts the AI in interviewer mode: it reads the spec, explores the
codebase, and asks you one high-impact design question at a time with a
recommended answer. Your responses persist in `decisions.md` next to the spec
and feed into the next `corvex plan`. Cheaper to resolve a question for $0.05
during grill than to discover it in a $0.30 worker retry.

```bash
corvex grill my-feature
# 🔍 Should tokens persist across restarts?
# 💡 Recommended: Yes — store hashed tokens in the existing sessions table
#    why: matches the rotation pattern already in `auth/session.ts`
# Your answer (Enter to accept recommendation, /skip to skip, /done to finish): _
```

## Commands

| Command | Description |
|---------|-------------|
| `corvex init` | Scaffold `.corvex/` directory with default configuration |
| `corvex start` | Single entry point for a new feature (brainstorm → grill → plan) |
| `corvex grill <project>` | Interview to resolve spec ambiguities (writes `decisions.md`) |
| `corvex plan <project>` | Generate `tasks.md` from the project specification |
| `corvex run <project>` | Execute pending tasks with the orchestration loop |
| `corvex run <project> --task S03` | Execute a specific task |
| `corvex run <project> --single` | Execute only the next pending task |
| `corvex run <project> --dry-run` | Show execution plan without running |
| `corvex run <project> --plain` | Disable TUI, use plain log output |
| `corvex run <project> --task S03 --ab sonnet,opus` | A/B-compare two models on one task |
| `corvex status <project>` | Display DAG with task statuses |
| `corvex logs <project> [task]` | Show logs for a task |
| `corvex reset <project> <task>` | Mark a task as PENDING |
| `corvex review [project]` | List pending escalations awaiting human review |
| `corvex validate <project>` | Run integration validation against the live stack |
| `corvex list` | List all projects |
| `corvex doctor` | Validate config + environment before a run (preflight checks) |
| `corvex recipe <name>` | Compile a declarative recipe into a runnable task DAG (no AI planning) |
| `corvex version` | Print the corvex version (same as `--version`) |
| `corvex completion <shell>` | Emit a shell completion script (bash/zsh/fish/powershell) |

### Global flags

Available on every command:

- `--no-color` — disable colored output (also honored via the `NO_COLOR` env var).
- `-q, --quiet` — with `--plain`, print only failures/errors and the final summary.
- `--json` — on `list`, `status`, `inspect`, and `doctor`: emit a single
  machine-readable JSON document instead of human output (great for scripts/CI).

### `corvex run` flags

Beyond `--task`, `--single`, `--dry-run`, `--plain`, `--ab`:

- `-y, --yes` — skip the cost-preview confirmation prompt (CI/scripts; non-TTY
  runs auto-proceed).
- `--skip-doctor` — skip the pre-run config checks.
- `--no-replan` — fail if `spec.md` drifted instead of auto-regenerating tasks.md.
  When the drift is benign (an edit that doesn't change the task breakdown),
  accept it without a replan via `corvex plan <project> --reanchor`, which just
  re-records the spec hash and leaves tasks.md untouched.
- `--force` — run on a dirty working tree, discarding uncommitted changes first.
- `--here` — run from the current directory even when a worktree exists for this
  project. See [Worktrees](#worktrees) below.

Before running, `corvex run` prints a cost preview (pending task count + the
configured ceilings) and, on an interactive terminal, asks for confirmation. It
also runs the `doctor` checks and refuses to start on a failing config.

### Shell completion

```bash
# zsh (add to ~/.zshrc, or load once)
source <(corvex completion zsh)

# bash
source <(corvex completion bash)
```

The `<project>` argument of `run`, `status`, `logs`, and `reset` completes to
your real project names.

### Recipes — deterministic pipelines

When you already know the exact pipeline, skip AI planning with a declarative
recipe at `.corvex/recipes/<name>.yaml`:

```yaml
name: ship-endpoint
description: scaffold → implement → review
stages:
  - id: S01
    title: Scaffold the handler
    type: backend
    description: create the route skeleton and wiring
    criteria: [it builds]
  - id: S02
    title: Implement the logic
    type: backend
    depends_on: [S01]
  - id: S03
    title: Review
    type: review
    depends_on: [S02]
```

```bash
corvex recipe ship-endpoint   # compiles → .corvex/tasks/ship-endpoint/tasks.md
corvex run ship-endpoint      # executes the fixed DAG (no Planner)
```

Each stage has a `kind`:

- `code` (default) — an AI worker task (planned/implemented/reviewed as usual).
- `tool` — a fixed-contract operation that changes the world (open a PR, push a
  tag, run a migration): runs `command:` as a shell step, no LLM, no cost.
- `test` — like `tool`, but only *observes* the world (e.g. `go test`/lint),
  never changes it.
- `repro` — deterministic with a temporal verdict: `command:` must fail before
  the fix and pass after it (see `fixed_by:` below).

The pre-F2 spellings `task`, `command` and `human-gate` still work (mapped
respectively onto `code`, `tool` and a `tool` stage with a human gate) — new
recipes should use the four above.

A `tool`/`test` stage can also **loop with a policy** — repeat until a condition
holds, up to a cap:

```yaml
  - id: S02
    kind: tool
    command: ./flaky-step.sh        # the work, re-run each iteration
    loop:
      until: go test ./...          # optional; success when this exits 0
      max: 5                        # iteration cap (default 3)
```

With `until` omitted, the command's own exit code is the loop condition (i.e.
retry the command until it succeeds, up to `max`).

Human approval is a **gate**, not a stage kind — attach it to any stage:

```yaml
  - id: S03
    kind: tool
    command: ./deploy.sh
    gates:
      - nature: human
        when: before                # before: guards the work; after: judges the result
        prompt: "Deploy to prod?"
```

A `human` gate parks the run — it does not print a re-run hint, because there
is nothing to re-run. Someone else, from another process, resolves it:

```bash
corvex gate list                                       # runs waiting on a decision
corvex gate show <run-id> --step S03                   # the gate's prompt and evidence
corvex gate approve <run-id> --step S03 --ack "Deploy"  # unblocks the run
```

`corvex run <name> --approve-gates` auto-approves every human gate instead —
the CI path, for pipelines that must not stop for a person.

A stage may hold **one** gate that waits on a person — one `human` or one
`question`, never two. That gate is a file, one per (run id, step id), so a
second one on the same stage opens a path that already exists and kills the run
*after* somebody has already approved the first; `recipe validate` refuses such
a recipe rather than letting it die halfway. Two decisions means two stages, the
second `depends_on` the first.

Gates come in four natures — `computational` (a shell command's exit code),
`inferential` (an independent agent call, never the stage's own worker),
`human` (above), and `policy` (a runner rule: attempt cap, cost ceiling,
protected branch):

```yaml
    gates:
      - nature: computational
        command: go vet ./...
      - nature: inferential
        reviewer: security-review   # repo skill guiding the judgment
      - nature: policy
        max_attempts: 2
        branch_not: [main]
```

A stage can hand its gate **evidence** — what a reviewer or approver sees.
`required_reading: true` arms the approval lock: `gate approve` refuses until
every such item has been acknowledged by label (`gate ack ... --ack "<label>"`):

```yaml
    evidence:
      - kind: diff
        label: "schema migration"
        from: git diff --stat $CORVEX_RUN_BASE -- db/migrations   # or `content:` inline
        required_reading: true
```

Evidence is collected only by a gate that parks the run on a person — `nature:
human` or `nature: question`. Declaring it under a computational, inferential or
policy gate would promise a lock nobody arms, so `recipe validate` refuses that
recipe instead of letting it run.

`from:` runs when the gate opens, with `$CORVEX_RUN_BASE` set to the commit the
run started from. Anchor diffs to it rather than to `HEAD`: with `auto_commit`
on, every step checkpoints, so by the time a later step's gate opens `HEAD` is a
bookkeeping commit and a `HEAD`-anchored diff shows the approver nothing.

A stage can also **fan out** over items discovered at run time — one template
expanded into N instances, up to a cap:

```yaml
  - id: S04
    produces: items              # this stage's output feeds a fanout
  - id: S05
    fanout:
      over: S04
      max_items: 20              # default 50; each item can be an LLM call
      template:
        - id: fix
          kind: code
          description: "fix {{item}}"
```

`requires:` declares what the run needs on the machine *before* spending a
token — a missing `bin:` (a name on PATH, or a path relative to the run's
workDir) or `env:` (a variable set on the runner) fails the run up front
instead of mid-pipeline:

```yaml
requires:
  - bin: az                      # a CLI, looked up on PATH
    why: "az is how the ship stage opens the PR"
  - bin: scripts/deploy.sh       # a repo script, resolved against the workDir
    why: "renaming it should fail here, not in the last stage"
```

A `bin:` with a separator is checked as a file — the same file the stage's
command will resolve, since stages run with the workDir as their working
directory — and the check also distinguishes "not there" from "there but not
executable", because one of those is fixed with `chmod`.

`timeout:` on a stage overrides `execution.task_timeout_minutes` for that one
step (`"45m"`, `"2h"`) — use it for a stage that's known to run long or short.

### Skills (repo-local)

Drop [Claude Code skills](https://docs.claude.com) the Worker can invoke under
`.corvex/skills/<name>/SKILL.md`. Before each run, Corvex symlinks them into
`.claude/skills/` (the location the Claude CLI auto-discovers, including in the
headless mode the Worker uses), and removes the links when the run ends. They
travel with the repo and into the Docker sandbox (both dirs live under the
bind-mounted root).

```
.corvex/skills/
  my-convention/
    SKILL.md      # name + description frontmatter; the Worker uses it when relevant
```

The **Worker** can invoke skills; the **Reviewer** also gets the skill routed to
the `review` type (so it reviews against your house rules). The Planner runs
without skills. An existing `.claude/skills/<name>` is never overwritten.
`corvex doctor` lists the repo skills available to the Worker.

To make skill use **intentional** rather than opportunistic, route a task type
to a skill in `config.yaml`:

```yaml
skill_routing:
  frontend: my-ui-conventions   # frontend tasks are told to use this skill
  database: sql-review
```

The Worker prompt for a routed task type instructs it to invoke that skill, and
the orchestrator **Reviewer** uses the skill routed to `review` to judge every
task against it. `corvex doctor` warns if a routed skill isn't available under
`.corvex/skills/`.

### Pulling external context into planning

The Planner is read-only (it can't run shell tools), so it can't reach an issue
tracker or API on its own. Set `plan.context_command` to a shell command whose
stdout is injected into the Planner prompt as authoritative context — the DAG is
then built to match it:

```yaml
plan:
  # e.g. pull an Azure DevOps feature + its stories/tasks via a skill-aware call
  context_command: 'claude -p "use the az skill: dump Feature 59888 and its Stories/Tasks"'
  # or raw: 'az boards query --wiql "SELECT ... WHERE [System.Parent]=59888"'
```

A failing command is non-fatal — planning proceeds without the context.

## Configuration

After `corvex init`, edit `.corvex/config.yaml`:

```yaml
project:
  name: my-project
  description: "Project description"

provider:
  default: claude-cli
  models:
    planner: opus        # Capable model for planning
    worker: sonnet       # Fast model for execution
    reviewer: sonnet     # Fast model for review

sandbox:
  type: docker           # "docker" or "local"
  image: node:20-slim
  mount: ./:/app
  workdir: /app
  worker_extra_args:     # Optional: CLI flags applied only to the Worker
    - "--dangerously-skip-permissions"
  env_allowlist:         # Optional: extra host env prefixes forwarded to the sandbox
    - AZURE_             # e.g. AZURE_DEVOPS_EXT_PAT for the `az` CLI
    - GH_TOKEN

execution:
  max_retries: 2                 # Retry failed tasks
  auto_commit: true              # Git commit after each task
  parallel: true                 # Run independent DAG levels concurrently
  max_parallel: 4                # Max tasks running at once when parallel (0 → 4)
  max_cost_usd: 25               # Abort the run past this cumulative LLM spend (0 → no cap)
  max_cost_per_task_usd: 5       # Abort a task past this spend (0 → no cap)
  task_warn_minutes: 5           # Warn when a task runs longer than this
  task_timeout_minutes: 20       # Hard wall-clock ceiling per attempt; cancels a stuck task (0 → off)
  stream_idle_timeout_seconds: 180 # Cancel an attempt with no provider output for this long (0 → off)

review:
  # Escalate after repeated rejections of the same category (Reviewer emits
  # `CATEGORY:` alongside `VERDICT: FAIL`). Actions: upgrade-model,
  # spawn-investigation, human-prompt.
  escalation:
    wrong-approach:    { after: 2, action: upgrade-model, to: opus }
    flaky-test:        { after: 3, action: human-prompt }
    missing-edge-case: { after: 2, action: spawn-investigation }

context:
  always_include:
    - .corvex/context/*.md

agent_routing:
  database: .corvex/agents/dba.md
  backend: .corvex/agents/backend.md
  frontend: .corvex/agents/frontend.md
```

### Worktrees

`corvex start <project>` creates a sibling git worktree at `<repo>-<project>` and
checks out a fresh feature branch. All of `corvex plan`/`run` for that project is
meant to happen **inside** the worktree so generated code lands on the right
branch.

To guard against the easy mistake of running from the main checkout while a
worktree exists, `corvex plan` and `corvex run` refuse to proceed and point you
at the worktree:

```
worktree for project "feat59440" exists at /repo-feat59440, but you are running from /repo.
→ cd /repo-feat59440 && corvex run feat59440
```

Pass `--here` to override (rare — e.g. the worktree is a leftover you intend to
ignore).

A worktree is a clean checkout, so gitignored paths the build needs — installed
dependencies, local dotenv files — are absent. List them under `worktree.link`
and `corvex start` symlinks each from the main repo into the worktree
(idempotent: missing sources and already-present destinations are skipped):

```yaml
worktree:
  link:
    - node_modules
    - .env
    - backend/.env-stg
```

### Sandbox and Worker Isolation

The Worker executes the Claude CLI **inside** the configured sandbox environment. The Planner (read-only) and Reviewer (read+test) always run on the host since they present low risk.

#### Sandbox types

- `sandbox.type: docker` — the Worker CLI runs inside a container with the repo bind-mounted as a volume.
- `sandbox.type: local` — the Worker runs directly on the host. This is the default and the fallback when Docker is not reachable.

#### Profiles

For repos that already declare their dev environment, set `sandbox.profile` to inherit it instead of configuring Corvex's own image. Profile takes precedence over `type` when set:

```yaml
sandbox:
  profile: nix          # reads flake.nix at the repo root
  # profile: devcontainer  # reads .devcontainer/devcontainer.json
```

- `profile: nix` — the Worker command is wrapped with `nix develop --command <cmd>`, so it runs inside the flake's devShell. The Claude CLI must be reachable from the resolved PATH (either declared in the flake or kept on the host PATH, which is appended after the Nix shell environment). Corvex falls back to local execution if `nix` is not installed.
- `profile: devcontainer` — Corvex delegates lifecycle to the official `devcontainer` CLI (`devcontainer up` then `devcontainer exec`). Requires [`@devcontainers/cli`](https://github.com/devcontainers/cli) on the host PATH. Corvex falls back to local execution if it is not installed.

#### MCP servers (Worker only)

Declare MCP servers in `config.yaml` to expose extra tools to the Worker — databases, browsers, GitHub APIs, etc. Corvex materialises the config as `.corvex/mcp.json` before each Worker invocation and passes it through `claude --mcp-config`:

```yaml
sandbox:
  mcp_servers:
    - name: postgres
      command: npx
      args: ["-y", "@modelcontextprotocol/server-postgres", "postgres://localhost/db"]
    - name: playwright
      command: npx
      args: ["-y", "@modelcontextprotocol/server-playwright"]
      env:
        DEBUG: "1"
```

Only the Worker receives MCP servers. The Planner (read-only) and Reviewer (read+test) run without them. Add `.corvex/mcp.json` to `.gitignore` — it is regenerated on each run.

#### A/B run

Pit two models against the same task to learn which serves it better:

```bash
corvex run my-feature --task S03 --ab sonnet,opus
```

Each model executes in its own git worktree under `.corvex/worktrees/`. The Reviewer judges each side independently; the winner's branch is merged back into HEAD with a `corvex: merge a/b winner <branch>` commit, the loser worktree is removed. Outcomes accumulate in `.corvex/ab-stats.json` (per task type), which is the basis for future automatic model routing. A/B currently bypasses container sandboxes — each side runs directly in the host's filesystem inside its worktree.

#### Environment and worker flags

**Environment variables** for authentication are forwarded from the host process into the sandbox by name prefix — secrets are never stored in `config.yaml`. Built in, and always present: `ANTHROPIC_`, `CLAUDE_`, `AWS_ACCESS_KEY`, `AWS_SECRET_ACCESS`, `AWS_SESSION_TOKEN`, `AWS_DEFAULT_REGION`, `AWS_REGION`, `AWS_PROFILE`, `OPENAI_`, `CORVEX_`.

Anything else your repo's tooling needs goes in `sandbox.env_allowlist`:

```yaml
sandbox:
  env_allowlist:
    - AZURE_          # az / Azure DevOps CLI: AZURE_DEVOPS_EXT_PAT, AZURE_TENANT_ID, ...
    - GH_TOKEN
    - VAULT_
```

The list **adds to** the built-in prefixes — it never replaces them, so no entry here can lock the Worker out of its own model credentials. Only names are matched; the values stay in your shell and never reach the YAML, the logs or the ledger. Granting a new credential is a config edit, not a new build.

**Worker extra args** (`sandbox.worker_extra_args`) allow flags like `--dangerously-skip-permissions` that skip interactive tool confirmations. These are only safe inside Docker isolation — using them with `type: local` is at your own risk.

## Architecture

```
┌────────────────────────────────────────────────────────────────────┐
│                       CLI + TUI (Bubbletea)                         │
│  init · start · grill · plan · run · status · logs · review        │
│  reset · validate · list                                            │
├────────────────────────────────────────────────────────────────────┤
│                       CORE (Orchestrator)                           │
│                                                                     │
│     Planner ──→ Worker ──→ Reviewer ──→ Escalation engine          │
│     READ-ONLY    ALL tools   READ+TEST    (upgrade-model /         │
│                      │                     spawn-investigation /   │
│                      │                     human-prompt)           │
│                                                                     │
│     Anchor Manager   ·   DAG Engine   ·   A/B runner (worktrees)   │
├────────────────────────────────────────────────────────────────────┤
│   PROVIDERS                  │         SANDBOX                      │
│   Claude CLI (MVP)           │   docker · local · nix ·            │
│   + MCP servers (Worker)     │   devcontainer (with fallback)      │
└────────────────────────────────────────────────────────────────────┘
```

The **Planner** reads the spec and generates a task DAG (read-only tools only). The **Worker** executes each task with full tool access inside the configured **sandbox**. The **Reviewer** independently verifies success criteria and emits a `CATEGORY:` alongside any `FAIL`. The **escalation engine** counts categories per task and reacts according to `review.escalation` policies: upgrade the model for the next retry, spawn an investigation, or surface a structured note for a human via `corvex review`. The **A/B runner** can fan one task across two worktrees with different models and merge the winner. Separation between planner and worker is enforced at the infrastructure level, not only by prompt.

## TUI

`corvex run` opens an interactive terminal UI by default (use `--plain` to fall back to log lines). The layout is vertical:

```
 corvex · my-feature · 3/8 · $2.14
 ○ S01  Setup database schema                          pending
 ✓ S02  Add migration tooling                           1m12s
 ● S03  Implement auth endpoints              running · 18s
 ○ S04  Add JWT middleware                             pending
 ─────────────────────────────────────────────────────────────
 worker · S03
   › Bash    $ npm test -- auth
   ↳ done    7 passed, 0 failed
   › Edit    internal/auth/handler.go:42
 tokens 4.2k↑ 823↓ · turn 6 · 12m04s    ? · / · ↵ · p · q quit
```

Status glyphs in the DAG: `○` pending · `●` running · `✓` passed · `✗` failed · `→` skipped.

### Keyboard reference

| Key | Action |
|---|---|
| `j` `k` `↑` `↓` | Navigate the DAG |
| `↵` (Enter) | Open the task detail modal |
| `?` | Toggle the help overlay |
| `/` | Filter the DAG by ID or title |
| `Esc` | Close a modal or leave filter mode |
| `l` | Open the selected task's logs via `$PAGER` (`corvex logs <project> <task>`) |
| `p` | Toggle pause — the orchestrator waits before starting the next task |
| `s` | Skip the selected `PENDING` task (marks it `SKIPPED`) |
| `r` | Retry the selected `FAILED` task (resets to `PENDING`) |
| `q` `ctrl+c` | Quit |

Pause, skip, and retry travel from the TUI to the orchestrator over a `Commands` channel and take effect between tasks.

## Project Structure

```
.corvex/
├── config.yaml              # Project configuration
├── mcp.json                 # Generated MCP config passed to the Worker (gitignored)
├── ab-stats.json            # Accumulated A/B run outcomes per task type
├── agents/                  # Custom agent prompts by role
│   ├── dba.md
│   ├── backend.md
│   └── reviewer.md
├── context/                 # Docs injected into every task
│   ├── architecture.md
│   └── conventions.md
├── hooks/                   # Lifecycle scripts
│   ├── pre-task.sh
│   ├── post-task.sh
│   ├── on-success.sh
│   └── on-failure.sh
├── escalations/             # Markdown notes when the Reviewer escalates to human review
│   └── my-feature-S03.md
├── worktrees/               # Ephemeral git worktrees for A/B runs (auto-cleaned)
│   └── S03-a/
└── tasks/                   # Task manifests per project
    └── my-feature/
        ├── spec.md          # Specification (Planner input)
        ├── decisions.md     # Answers produced by `corvex grill` (optional)
        ├── tasks.md         # Task DAG (Planner output)
        └── anchor.yaml      # Accumulated context (auto-generated)
```

## Prerequisites

- **Go 1.24+** (for building from source)
- **Claude CLI** installed and authenticated (for the default provider)
- **Docker** (optional, for sandboxed execution)
- **Git** (for checkpointing and recovery)

## Shell completion

Corvex ships with shell completion via cobra's built-in `completion` command.

```bash
# Bash (add to ~/.bashrc)
source <(corvex completion bash)

# Zsh (add to ~/.zshrc)
source <(corvex completion zsh)

# Fish (add to ~/.config/fish/completions/corvex.fish)
corvex completion fish | source

# PowerShell (add to $PROFILE)
corvex completion powershell | Out-String | Invoke-Expression
```

## License

MIT — see [LICENSE](LICENSE).
