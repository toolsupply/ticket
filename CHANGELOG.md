# Changelog

All notable user-facing changes to `ticket` are documented here.

## [0.1.7] - 2026-09-21

### Added

- Added queue-aware `ticket ready [open|review]` inspection
- Added terminal title control in 'ticket -i' shell mode
- Added optional name property to repository config.json

## [0.1.6] - 2026-09-20

### Added

- Added the `ticket -i` line-oriented shell mode
- Added promptless NDJSON transport with `ticket -j -i`.
- Added `ticket add` as an alias for the create/new commands.

### Changed

- Made `.local/current` exclusively human convenience state
- Made editor-backed create and edit flows session-aware
- Made Git-backed ticket repositories work without a upstream
- Fixed interactive handling of prompt interrupts, interrupted waits, final
  input without a newline, and invalid editor drafts
- Switched to go 1.26.8

## [0.1.4] - 2026-09-18

### Added

- Added explicit `ticket state` command

### Changed

- Improved CLI dispatch with support for more fluent syntax aliases and positional arguments.

## [0.1.3] - 2026-09-17

### Added

- Added `TICKET_CREATE_TAGS` for default tags on newly created tickets.
- Added `TICKET_WORK_TAGS` for implicit tag filters on `next` and `wait`.
- Added `TICKET_REPOSITORY` for selecting the ticket repository.
- Added scope concept and ~/.ticket/config.json configuration file.

### Changed

- Strengthened ticket ownership enforcement for concurrent workers
- Tuned execution bias in SKILL.md
- Condensed SKILL.md

## [0.1.2] - 2026-09-16

### Added

- Added tag-based filtering for work selection and specialized workers.
- Added per-ticket `attachments/` directories for supplementary files.

### Removed

- Removed the built-in `ticket upgrade` command.

### Changed

- Improved human-oriented command output, help text, and workflow guidance.

## [0.1.0] - 2026-09-15

### Added

- Initial release

[0.1.7]: https://github.com/toolsupply/ticket/releases/tag/v0.1.7
[0.1.6]: https://github.com/toolsupply/ticket/releases/tag/v0.1.6
[0.1.4]: https://github.com/toolsupply/ticket/releases/tag/v0.1.4
[0.1.3]: https://github.com/toolsupply/ticket/releases/tag/v0.1.3
[0.1.2]: https://github.com/toolsupply/ticket/releases/tag/v0.1.2
[0.1.0]: https://github.com/toolsupply/ticket/releases/tag/v0.1.0
