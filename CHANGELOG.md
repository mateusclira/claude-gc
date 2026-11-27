# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org).

## [Unreleased]

## [0.2.0] - 2026-11-27

### Changed

- The daemon runs once a day instead of every 30 minutes: `--interval` now
  defaults to `24h`.
- The daemon does nothing until the computer has been up for 24 hours. A
  restart ends every Claude Code session, so there is nothing to collect
  right after one. Set with `--min-uptime` or `CLAUDE_GC_MIN_UPTIME`; `0`
  turns the rule off.
- The schedule follows the wall clock, so time asleep counts. A pass that
  comes due while the computer sleeps runs a few minutes after it wakes.

### Added

- The daemon keeps its schedule in a state file, so restarting it, or
  logging out and back in, does not cause an extra pass.
- `claude-gc service status` and `service install` show the schedule,
  uptime, the last pass and when the next one is due.
- Durations accept a bare `0`.

### Upgrading

Services installed by 0.1.x have `--interval 30m` written into their unit
file. After upgrading the binary, run `claude-gc service install` again to
switch to the new schedule.

## [0.1.1] - 2026-11-27

### Fixed

- `claude-gc version` now reports the module version and commit for
  binaries built with `go install` or from a checkout, instead of `dev`.

## [0.1.0] - 2026-11-27

First release.

### Added

- `claude-gc status`: lists every Claude Code session with its process,
  memory, age, last activity and the action claude-gc would take.
- `claude-gc run`: reaps sessions idle for longer than `--max-idle`
  (default 3 days) with SIGTERM, a grace period and SIGKILL, then removes
  stale session records and sockets. `--dry-run` previews a pass.
- `claude-gc daemon`: runs passes on an interval; SIGHUP triggers one now.
- `claude-gc service install|uninstall|status|logs`: manages a launchd agent
  on macOS or a systemd user unit on Linux.
- `--json` output for `status` and `run`.
- Process identity verification against the session record (command line
  and start time), self-protection for the current session, and an
  entrypoint allow-list defaulting to `claude-vscode`.
- Release binaries for macOS and Linux on amd64 and arm64, `go install`
  support, an install script, and a GoReleaser cask definition for a
  Homebrew tap.

[Unreleased]: https://github.com/mateusclira/claude-gc/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/mateusclira/claude-gc/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/mateusclira/claude-gc/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/mateusclira/claude-gc/releases/tag/v0.1.0
