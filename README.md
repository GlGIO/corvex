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

# 3. Generate task plan from the spec
corvex plan my-feature

# 4. Execute all tasks
corvex run my-feature

# 5. Check progress
corvex status my-feature
```

## Commands

| Command | Description |
|---------|-------------|
| `corvex init` | Scaffold `.corvex/` directory with default configuration |
| `corvex plan <project>` | Generate `tasks.md` from the project specification |
| `corvex run <project>` | Execute pending tasks with the orchestration loop |
| `corvex run <project> --task S03` | Execute a specific task |
| `corvex run <project> --single` | Execute only the next pending task |
| `corvex run <project> --dry-run` | Show execution plan without running |
| `corvex run <project> --plain` | Disable TUI, use plain log output |
| `corvex status <project>` | Display DAG with task statuses |
| `corvex logs <project> [task]` | Show logs for a task |
| `corvex reset <project> <task>` | Mark a task as PENDING |
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

Planner (read-only) and Reviewer (read+test) always run on the host since they present low risk.

**Environment variables** for authentication (`ANTHROPIC_API_KEY`, `CLAUDE_*`, `AWS_*`, etc.) are automatically forwarded from the host process to the sandbox — secrets are never stored in `config.yaml`.

**Worker extra args** (`sandbox.worker_extra_args`) allow flags like `--dangerously-skip-permissions` that skip interactive tool confirmations. These are only safe inside Docker isolation — using them with `type: local` is at your own risk.

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
