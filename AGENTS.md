# AGENTS.md

## Project intent

`ticket` is a small, repository-local task manager for humans and coding agents.

Keep it simple:

* plain Markdown is authoritative;
* one static Go binary;
* Go standard library only;
* no database, daemon, service, index, or background state;
* filesystem operations must remain deterministic and conservative.

Prefer small, explicit features over abstractions or speculative flexibility.

## Architecture contracts

Preserve these invariants:

* `tickets/<id>/TASK.md` is authoritative ticket state.
* Ticket IDs are canonicalized before constructing filesystem paths.
* One ticket-root lock serializes authoritative mutations.
* Writes replace complete files atomically; do not truncate managed files in place.
* `.local` is implementation state and must not be edited externally.
* SCM integration is transport/persistence around the same filesystem model, not a second source of truth.
* Human mode may use current-ticket convenience; JSON workflows should use explicit ticket IDs.
* `TICKET_ACTOR` is the normal worker identity. `--actor` is an explicit override.
* Work selection belongs to `next` / `wait`; do not reconstruct scheduler logic with `list`.
* Review selection belongs to the review queue; reviewers do not close tickets.
* Handoff is replaceable current context. Work log is append-only history.
* Persistent JSON mode (`-i -j`) is a strict request/response protocol:
  one nonblank JSON request line produces one JSON response, with no unsolicited output.
* Preserve framing recovery, bounded request-local stdin, and mutation uncertainty metadata unless
  explicitly asked to work on the protocol with regressions accepted.
* Clients establish stream readiness with a successful `version` request; startup errors before that are startup failures.

Do not weaken path, symlink, traversal, locking, or atomic-write checks for convenience.

## Attachments

Attachments live under:

```text
tickets/<id>/attachments/
```

`ticket` does not manage attachment contents.

`ticket show -j` may expose an `attachment_path` only when it can safely provide a cwd-relative lexical path.

Do not:

* enumerate attachment contents inside `ticket`;
* pipe attachment bytes through `ticket`;
* add attachment read/write/delete commands;
* resolve symlinks merely to authorize access.

The calling agent/harness should use its normal filesystem tools so its own path, size, binary-file, and access safeguards remain effective.

## Documentation policy

Keep documentation layered:

### README

Document user-facing behavior only:

* what the tool does;
* normal commands and workflows;
* installation/configuration;
* concise examples.

Do not put parser internals, path-containment algorithms, locking implementation, symlink mechanics, or agent-specific safety rationale in README unless users need them to operate the tool.

### CLI help

Document exact command syntax and observable behavior.

Help text is part of the interface. Keep it concise and consistent.

### Skill

Document agent workflow rules that humans should not need to know, including:

* authoritative work-selection commands;
* actor discovery;
* Handoff vs Work log usage;
* review behavior;
* attachment access through harness filesystem tools;
* anti-patterns for agents.

### Code/tests

Implementation contracts and security details belong in code comments and regression tests.

If a behavior exists mainly to prevent traversal, symlink escape, malformed Markdown, race conditions, or agent misuse, test it rather than explaining it at length in README.

## Change discipline

Before adding a new command or concept, check whether an existing primitive already owns that responsibility.

Avoid duplicated mechanisms such as:

* CLI functionality that bypasses harness safeguards;
* multiple ways to mutate the same managed state;
* separate scheduler logic in agents;
* duplicated format/schema descriptions across README, config, Skill, and code.

When fixing a bug, add the smallest regression test that proves the invariant.

Do not broaden scope while doing release-polish work.

## Validation

For normal ticket work, run the fast repository checks before considering the
change complete:

```sh
go fmt ./...
go test ./...
go vet ./...
```

For concurrency-sensitive changes, run a focused race check for the affected
package(s) when practical. The complete uncached race suite is a release gate,
not a per-ticket local requirement; the release workflow runs:

```sh
go test -race -count=1 ./...
```

Also preserve the standard-library-only dependency policy and existing
release/build checks.
