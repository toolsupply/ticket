# ticket

[![CI](https://github.com/toolsupply/ticket/actions/workflows/ci.yml/badge.svg)](https://github.com/toolsupply/ticket/actions/workflows/ci.yml)
[![GitHub License](https://img.shields.io/badge/license-MIT-blue)](https://github.com/toolsupply/ticket/LICENSE)
[![Go version](https://img.shields.io/github/go-mod/go-version/toolsupply/ticket)](https://github.com/toolsupply/ticket/blob/main/go.mod)
[![Dependencies](https://img.shields.io/badge/dependencies-stdlib_only-blue)](https://github.com/toolsupply/ticket/blob/main/go.mod)

A repository-local task manager and issue tracker for humans and coding agents.

`ticket` manages work as Markdown tickets stored alongside your source code. Tickets stay with the repository, remain human-readable, and can be managed through a single CLI by humans, scripts, and autonomous coding agents.

One statically compiled binary, no server, no database, and no third-party dependencies.

Key features:

- Markdown tickets stored in your code repository
- Human-readable, Markdown, and JSON output
- Dependencies, parent/child work, priorities, and ownership
- Explicit implementation, review, and human-signoff workflow
- Deterministic work selection for coding agents
- Optional `ticket-tasks` Skill for agent workflows
- Optional Git and SVN synchronization
- Single Linux, macOS, or Windows binary
- MIT licensed

`ticket` can be used to manage work of a single developer, a human team, a single coding agent, or a horde of autonomous workers.

Using tickets to hand work to an agent separates task coordination from the working context. You can inspect progress, reprioritize work, and queue additional tasks without interrupting the current agent session.

## Installation

### Linux

The latest Linux amd64 release can be installed into `~/.local/bin` like so:

```sh
mkdir -p "$HOME/.local/bin"
curl -fsSL "https://github.com/toolsupply/ticket/releases/latest/download/ticket-linux-amd64.tar.gz" | tar -xz -C "$HOME/.local/bin" ticket
```

Alternatively install for all users:

```sh
curl -fsSL "https://github.com/toolsupply/ticket/releases/latest/download/ticket-linux-amd64.tar.gz" | tar -xz -O ticket | sudo install -m 0755 /dev/stdin /usr/local/bin/ticket
```

To update the binary, rerun the installation command for your platform.

### Manual installation

Releases contain one archive per supported platform:

| Platform | Archive |
| --- | --- |
| Linux x86-64 | [ticket-linux-amd64.tar.gz](https://github.com/toolsupply/ticket/releases/latest/download/ticket-linux-amd64.tar.gz) |
| Linux ARM64 | [ticket-linux-arm64.tar.gz](https://github.com/toolsupply/ticket/releases/latest/download/ticket-linux-arm64.tar.gz) |
| macOS Intel | [ticket-darwin-amd64.tar.gz](https://github.com/toolsupply/ticket/releases/latest/download/ticket-darwin-amd64.tar.gz) |
| macOS Apple Silicon | [ticket-darwin-arm64.tar.gz](https://github.com/toolsupply/ticket/releases/latest/download/ticket-darwin-arm64.tar.gz) |
| Windows x86-64 | [ticket-windows-amd64.zip](https://github.com/toolsupply/ticket/releases/latest/download/ticket-windows-amd64.zip) |

Download `SHA256SUMS` with the archive and verify the checksum before
extracting it. Each archive contains only the executable: `ticket` on Unix
systems and `ticket.exe` on Windows. Place it somewhere on `PATH`.

GitHub Releases are the authoritative source for updates. All commits and release
tags are signed by the same public key. Release archives include GitHub artifact attestations:

```sh
gh attestation verify ticket-linux-amd64.tar.gz -R toolsupply/ticket
```

### Codex Skill

The repository includes an optional `ticket-tasks` skill for Codex CLI and
compatible Agent Skills clients. In Codex, install it directly from GitHub:

```text
$skill-installer install https://github.com/toolsupply/ticket/tree/main/skills/ticket-tasks
```

For other harnesses, download [ticket-tasks.zip](https://github.com/toolsupply/ticket/releases/latest/download/ticket-tasks.zip) and extract the `ticket-tasks` directory into the harness's skill directory.

The skill teaches agents to discover, claim, update, and submit tickets while preserving the repository's ticket safety rules.

## Quick start

Initialize a ticket repository:

```sh
ticket init
```

### Human workflow

Create some tickets and list all active (non-closed and non-rejected) tickets:

```sh
ticket new "Fix flaky retry test" "Make the retry test deterministic."
ticket new "Another ticket with long objective" -e
ticket list
```

Inspect the last created ticket:

```sh
ticket status
ticket show
```

Edit it in editor:

```sh
ticket edit
```

When the work is ready for review:

```sh
ticket submit -m "Fixed the race and added coverage for timeout retries."
```

After review and signoff:

```sh
ticket close
```

The submit and review lifecycle states are optional; tickets can be closed or rejected from any state.

### Querying tickets with TQL

Ticket Query Language (TQL) is the preferred shell-friendly way to select
tickets. It uses separate argv words, so ordinary Bash, POSIX shell,
PowerShell, and `cmd.exe` invocations do not need expression quoting:

```sh
ticket list state:open assignee:coder
ticket list state:open priority le P2
ticket list state:closed
```

For scripts, `-q` / `--query` makes the query boundary explicit and consumes
all remaining argv tokens, so command options must come first:

```sh
ticket close -m "Accepted" -q state:signoff tag:release
ticket approve -q state:review unclaimed
ticket graph -q not terminal
```

TQL queries can be composed directly with an in-process consumer:

```sh
ticket query state:open ready :: graph
ticket query state:signoff :: close -m "Accepted"
ticket query state:review unclaimed :: approve
ticket query state:open :: list --deps --fields id,title
ticket query terminal :: archive
```

TQL `or` has lower precedence than the implicit `and`:

```sh
ticket query state:open tag:backend or state:review tag:backend
```

### Hints

- `ticket -i` is a simple shell mode where human does not have to type ticket in front of every command.
- Use shell command execution in your harness to control ticket, for example `!ticket list open`.
- Use #tags suffix in title for quick auto-taggig.
- Use `ticket ready` to check if there are actionable tickets or `ticket list review` to check if there is anything to reivew.
- Instruct reviewer agent to close tickets once happy to bypass human signoff, example prompt also trigging Codex goals:

```sh
review all reviewable tickets; close if satified; set goal
```

## How it works

The `ticket` CLI program manages ticket state, ownership, dependencies, parent/child relationships, work selection, and optional source-control synchronization.

`ticket` uses the local filesystem as its database. Tickets are Markdown files that can live directly alongside the source code they describe. A `ticket` repository is a directory of plain files:

```text
tickets/
├─ config.json
├─ 20260920-12345/
│  └─ TASK.md
├─ 20260920-23456/
│  ├─ TASK.md
│  └─ attachments/
│     └─ design.png
└─ archive/
   └─ 20260919-34567/
      └─ TASK.md
```

The `TASK.md` files contain all ticket metadata, objectives, handoffs, and work logs.

The files can be edited directly in a text editor, but when there may be concurrent access, both humans and agents should perform ticket management operations through the `ticket` program.

A ticket repository has no fixed place on the filesystem. It can live inside the source tree it describes or in a separate working tree.

### Attachments

Supplementary material can be stored under a ticket's `attachments/`
directory. The ticket manager leaves those files opaque; humans and agents
can use their normal filesystem tools to organize, read, and update them.
`TASK.md` remains the ticket manager's authoritative file.

### Archive

Closed and rejected, unassigned tickets can be moved out of the active
queue with `ticket archive ID`. The ticket keeps its lifecycle state and
history; only its location changes to `tickets/archive/ID/`. Archived tickets
are read-only until returned with `ticket unarchive ID`.

The `list`, `ready`, `next`, and `wait` commands use the active queue.

Use `ticket list --archived` for archived tickets or `ticket list --all` for
both namespaces. `show`, explicit ID lookup, and relationship references also
work for archived tickets.

### Dependencies and parent tickets

Tickets can depend on other tickets and can be grouped under parent work.

`ticket` takes the relationship graph into account when determining which work is actionable. Agents and scripts therefore do not need to independently reason about whether dependencies or child work currently block a ticket.

Inspect dependency relationships:

```sh
ticket list --graph
ticket graph ID
ticket graph ID --reverse
ticket graph ID --depth 2 --ascii
ticket graph --all
```

### Ticket lifecycle

The normal workflow is:

```text
hold ─open─> open ─submit─> review ─approve─> signoff ─close─> closed
```

| State | Purpose | Normal transition |
|---|---|---|
| `hold` | Drafted or intentionally paused | `open` |
| `open` | Available for implementation | `submit` → `review` |
| `review` | Awaiting independent review | `approve` → `signoff` |
| `signoff` | Awaiting human acceptance | `close` → `closed` |
| `closed` | Accepted and finished | — |
| `rejected` | Abandoned or declined | — |

`open` can return a ticket from any state to implementation work. `reject` abandons work rather than requesting changes.

## Environment

| Variable | Purpose |
|---|---|
| `TICKET_REPOSITORY` | Ticket repository path. Relative paths are resolved from the current working directory. |
| `TICKET_ACTOR` | Actor identity used when claiming and working tickets. |
| `TICKET_SCOPE` | Named user configuration scope. |
| `TICKET_CREATE_TAGS` | Comma-separated tags added to newly created tickets. Explicit `--tag` values are additive. |
| `TICKET_WORK_TAGS` | Comma-separated tags required by `next` and `wait`; explicit `--tag` values are additive. |
| `TICKET_EDITOR` | Editor command for interactive create/edit operations; falls back to `config.editor`, `VISUAL`, `EDITOR`, then the platform default. |
| `TICKET_CURRENT` | Override the current ticket used by interactive single-ticket commands. |
| `TICKET_DECORATOR` | Executable used to render Markdown output for interactive use. |
| `TICKET_SCM` | Source-control backend: `none`, `git`, or `svn`. |
| `TICKET_SCM_MODE` | Source-control synchronization mode. |

For example, configure an editor that waits for completion:

```sh
export TICKET_EDITOR="code --wait"
```

### Configuration file

An optional `~/.ticket/config.json` can define named scopes for users who work
with more than one ticket repository or routing group.

```json
{
  "editor": "code --wait",
  "scopes": {
    "windows": {
      "repository": "/work/project/tickets",
      "create_tags": ["windows"],
      "work_tags": ["windows"]
    }
  }
}
```

The repository path may be absolute or relative to the directory containing the config
file. Environment variables and explicit command-line options override scope
defaults.

### Named scopes

Select the scope for a session with `TICKET_SCOPE`, or for one command with `--scope`:

```sh
export TICKET_SCOPE=windows
export TICKET_ACTOR=coder-01
ticket next --claim
```

## Selecting work

To select the next ticket available for implementation and claim it:

```sh
ticket next --claim
```

Selection logic considers current actor, ticket state, ownership, dependencies, child
work, and any supplied filters such as tags. Blocked or otherwise ineligible
tickets are skipped.

Reviewers use the explicit review queue. Review selection does not use
implementation dependency or child-work blockers:

```sh
ticket next review --claim
```

## Coding agents

`ticket` is a coordination primitive, not an agent runtime.

It manages work selection, state, ownership, dependencies, and synchronization. An optional Skill teaches compatible coding agents how to follow the workflow, while external tools remain responsible for launching and supervising agent processes.

Project- or harness-level instructions decide how aggressively agents should turn user requests into tickets, how work should be decomposed, and when native agent planning should be used instead.

This keeps the responsibilities separate:

- `ticket` coordinates persistent work
- the Skill teaches agents the ticket protocol
- project or harness instructions define planning policy
- the supervisor launches and restarts agents

### Agent Skill

The repository includes an optional `ticket-tasks` Skill that teaches compatible coding agents how to use the ticket workflow consistently.

The Skill covers conventions such as:

- selecting, claiming, and resuming implementation or review work
- working one ticket at a time
- using `TICKET_ACTOR` as a stable worker identity
- using tags and filters for specialized workers
- keeping Handoff and Work log context distinct
- submitting implementation for independent review
- reviewing work and approving it for human signoff
- creating or decomposing independently useful work when appropriate
- avoiding direct mutation of managed ticket files
- using JSON output for deterministic agent interaction

### Coding workflow

Once the Skill is installed, an agent should use the `ticket` CLI when the current project contains a ticket repository or when asked to work from tickets.

A coding worker will normally:

```text
select or resume implementation work
inspect the ticket
implement its Objective and Acceptance criteria
record useful transition context
submit it for review
```

A review worker will normally:

```text
select or resume review work
inspect the ticket, implementation, and relevant tests
approve correct work for human signoff
or return it to open with concrete review findings
```

Implementation workers stop at review. Reviewers stop at signoff. Final acceptance remains a human lifecycle action.

### Agent instructions

The bundled `ticket-tasks` Skill defines how agents interact with the ticket system. Individual repositories can add project-specific guidance, for example in `AGENTS.md`, to decide what kinds of work should become persistent tickets.

A project that wants tickets to be its primary coordination surface might use:

```markdown
Use tickets as the primary persistent coordination surface for non-trivial work.

Create or update tickets for work that should be independently tracked,
reviewed, resumed, or handed to another agent. Decompose broad requests when
the resulting pieces are independently actionable or reviewable.

Keep ticket state and handoff information current at meaningful workflow
transitions. Use the agent's native task planning for short-lived implementation
steps that do not need persistent coordination.
```

A project that prefers lighter-weight use might instead say:

```markdown
Use tickets for work that should survive the current session, be reviewed
independently, or be handed between agents.

Keep ordinary implementation planning in the agent's native task UI unless
there is a reason to persist it as a ticket.
```

This keeps the shared Skill focused on ticket mechanics while allowing each repository to choose how much work becomes persistent ticket state.

### Orchestration

Agent orchestration is deliberately not built into `ticket`. For ticket-based orchestration consider [ticket-orc](https://github.com/toolsupply/ticket-orc).

## Source-control synchronization

`ticket` uses the local filesystem by default. Set `TICKET_SCM` to optionally synchronize ticket changes through source control.

For Git:

```sh
export TICKET_SCM=git
export TICKET_SCM_MODE=sync
```

With Git synchronization enabled, `ticket` updates before reading and commits and pushes ticket mutations using the current branch and its configured upstream.

An explicit push destination does not isolate history: pushing a branch necessarily publishes any unpublished ancestor commits required by that branch. Use a dedicated Ticket branch or worktree when that isolation matters.

`ticket` never creates or switches branches or worktrees. It uses the checkout containing `TICKET_REPOSITORY` exactly as configured.

### Sparse ticket worktree

For concurrent coding and review agents, a dedicated sparse worktree can be used for the ticket repository.

All workers coordinate through the same authoritative ticket repository. The repository lock serializes cooperating operations; Git handles update and publication.

Create a ticket worktree:

```sh
git worktree add --no-checkout -b ticket-state ../project-tickets origin/main
cd ../project-tickets

git sparse-checkout init --cone
git sparse-checkout set tickets
git checkout

git push -u origin ticket-state
```

Configure `ticket` to use it:

```sh
export TICKET_REPOSITORY="$PWD/tickets"
export TICKET_SCM=git
export TICKET_SCM_MODE=sync
```

The ticket-state branch acts as the coordination branch for ticket state; coding agents may use separate source branches and worktrees.

### Shared repository

For simpler setups, a separate worktree is not required:

```sh
export TICKET_REPOSITORY="$PWD/tickets"
export TICKET_SCM=git
export TICKET_SCM_MODE=sync
```

This is suitable when one coding agent works in the checkout and other participants, such as review agents, do not modify source concurrently.

In this configuration, Git synchronization performed by `ticket` advances the same branch and working tree used for normal development.

### Security and operational boundaries

`ticket` does not contain access controls of any kind; access to tickets is
controlled by filesystem permissions. `TICKET_ACTOR` and `--actor` identify a
worker but neither grant nor enforce permissions. Independent review and human
closure are process boundaries, not substitutes for repository access controls.

Configured editors, decorators, SCM hooks, and Git or SVN commands are external
programs. `ticket` does not add a sandbox around them; they run within the
environment and permissions of the process invoking `ticket`.

An explicit Git push destination narrows what `ticket` publishes, but it cannot
isolate a shared branch from its required unpublished ancestor commits. Use a
dedicated branch or worktree when history isolation matters.

## Building

Toolchain: Go `1.26.8`. No CGO or third-party Go dependencies are required.

```sh
make build && ./bin/ticket version
```

## Tests

```sh
go test ./... && go vet ./...
```

## Inspiration

Ideas and influences:

- Various issue trackers and ticket management systems
- https://github.com/tsoding/tatr
- https://github.com/mattpocock/skills/blob/main/docs/engineering/wayfinder.md
- https://orgmode.org/

## Disclaimer

100% vibe generated, no code was touched by humans.

## License

MIT
