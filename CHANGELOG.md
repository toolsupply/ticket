# Changelog

All notable user-facing changes to `ticket` are documented here.

## [0.1.2] - 2026-09-16

### Added

- Added `ticket actor` to report the effective actor identity and its source.
- Added append-only Work log messages for workflow transitions, separate from
  replaceable Handoff context.
- Added explicit implementation and review work queues for `next` and `wait`,
  including atomic claim/resume behavior.
- Added tag-based filtering for work selection and specialized workers.
- Added per-ticket `attachments/` directories for supplementary files, with
  safe cwd-relative discovery through `ticket show -j`.

### Removed

- Removed the built-in `ticket upgrade` command.

### Changed

- Improved human-oriented command output, help text, and workflow guidance.

## [0.1.0] - 2026-09-15

### Added

- Initial release

[0.1.2]: https://github.com/toolsupply/ticket/releases/tag/v0.1.2
[0.1.0]: https://github.com/toolsupply/ticket/releases/tag/v0.1.0
