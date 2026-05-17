# Corvex

> AI-powered development orchestrator — decompose specs into tasks, execute with AI agents, validate automatically.

Corvex is an open-source CLI tool written in Go that orchestrates AI agents to execute complex software development tasks autonomously. You define a specification, Corvex decomposes it into a DAG of tasks, executes each one in a fresh context, validates the result with an independent reviewer, and advances automatically.

## Features

- **Task Pipeline with DAG** — Topological sort, dependency resolution, and anchored summarization between tasks
- **Planner → Worker → Reviewer** — Infrastructure-enforced separation between orchestration and execution
- **Model-agnostic** — Pluggable provider interface (Claude CLI for MVP, extensible to OpenAI, Ollama)
- **Docker Sandbox** — Isolated execution with local fallback when Docker is unavailable
- **Interactive TUI** — Real-time DAG panel, worker stream, and status bar (Bubbletea)
- **Git Checkpointing** — Automatic commits after each task for crash recovery
- **Custom Hooks** — `pre-task`, `post-task`, `on-success`, `on-failure` shell scripts
- **Agent Routing** — Map task types to specialized agent prompts

## Installation

### From source (requires Go 1.22+)

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
| `corvex list` | List all projects |

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

execution:
  max_retries: 2         # Retry failed tasks
  auto_commit: true      # Git commit after each task
  parallel: true         # Run independent tasks in parallel

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

### Sandbox and Worker Isolation

The Worker executes the Claude CLI **inside** the configured sandbox environment. When `sandbox.type` is `docker`, the CLI runs inside a container with the repo bind-mounted as a volume. When `local`, it runs directly on the host (the default).

#### Profiles

For repos that already declare their dev environment, set `sandbox.profile` to inherit it instead of configuring Corvex's own image:

```yaml
sandbox:
  profile: nix          # reads flake.nix at the repo root
  # profile: devcontainer  # reads .devcontainer/devcontainer.json
```

- `profile: nix` — the Worker command is wrapped with `nix develop --command <cmd>`, so it runs inside the flake's devShell. The Claude CLI must be reachable from the resolved PATH (either declared in the flake or kept on the host PATH, which is appended after the Nix shell environment). Corvex falls back to local execution if `nix` is not installed.
- `profile: devcontainer` — Corvex delegates lifecycle to the official `devcontainer` CLI (`devcontainer up` then `devcontainer exec`). Requires [`@devcontainers/cli`](https://github.com/devcontainers/cli) on the host PATH. Corvex falls back to local execution if it is not installed.

Planner (read-only) and Reviewer (read+test) always run on the host since they present low risk.

**Environment variables** for authentication (`ANTHROPIC_API_KEY`, `CLAUDE_*`, `AWS_*`, etc.) are automatically forwarded from the host process to the sandbox — secrets are never stored in `config.yaml`.

**Worker extra args** (`sandbox.worker_extra_args`) allow flags like `--dangerously-skip-permissions` that skip interactive tool confirmations. These are only safe inside Docker isolation — using them with `type: local` is at your own risk.

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

## Architecture

```
┌──────────────────────────────────────────────────────┐
│                   CLI + TUI (Bubbletea)               │
│  corvex init | run | plan | status | logs | reset    │
├──────────────────────────────────────────────────────┤
│                   CORE (Orchestrator)                 │
│                                                       │
│   Planner ──→ Worker ──→ Reviewer                    │
│   READ-ONLY    ALL tools   READ+TEST                 │
│                    │                                  │
│               Anchor Manager                         │
│               DAG Engine                             │
├──────────────────────────────────────────────────────┤
│   PROVIDERS          │         SANDBOX               │
│   Claude CLI (MVP)   │   Docker / Local fallback     │
└──────────────────────────────────────────────────────┘
```

The **Planner** reads the spec and generates a task DAG (read-only tools only). The **Worker** executes each task with full tool access inside the configured **sandbox** (Docker or local). The **Reviewer** independently verifies success criteria. Separation between planner and worker is enforced at the infrastructure level, not only by prompt.

## Project Structure

```
.corvex/
├── config.yaml              # Project configuration
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
└── tasks/                   # Task manifests per project
    └── my-feature/
        ├── spec.md          # Specification (Planner input)
        ├── tasks.md         # Task DAG (Planner output)
        └── anchor.yaml      # Accumulated context (auto-generated)
```

## Prerequisites

- **Go 1.22+** (for building from source)
- **Claude CLI** installed and authenticated (for the default provider)
- **Docker** (optional, for sandboxed execution)
- **Git** (for checkpointing and recovery)

## License

MIT — see [LICENSE](LICENSE).
