# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org).

## [Unreleased]

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
- Release binaries for macOS and Linux on amd64 and arm64, a Homebrew tap
  for macOS, `go install` support and an install script.

[Unreleased]: https://github.com/mateusclira/claude-gc/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/mateusclira/claude-gc/releases/tag/v0.1.0
