# Contributing to claude-gc

Thanks for helping. This document covers how to set up a development
environment, how the code is organised, and what a good pull request looks
like.

## Development setup

You need Go 1.24 or newer. Everything else is optional:

- [golangci-lint](https://golangci-lint.run) v2 for `make lint`
- [GoReleaser](https://goreleaser.com) v2 for `make snapshot`
- [shellcheck](https://www.shellcheck.net) for the install script

```sh
git clone https://github.com/mateusclira/claude-gc
cd claude-gc
make build          # bin/claude-gc
make test           # go test -race ./...
make lint           # golangci-lint run
./bin/claude-gc status
```

`claude-gc status` and `claude-gc run -dry-run` never change anything, so
they are safe to run against your real Claude Code state while developing.
When you want to exercise the real thing, point `-config-dir` at a scratch
directory with hand-made session records instead of your own.

## Project layout

```
cmd/claude-gc/        main package; only wires build info into the CLI
internal/cli/         commands, flags, human and JSON output
internal/reaper/      the decision rules (Scan) and the actions (Apply)
internal/daemon/      the schedule (once a day, after a day of uptime) and its state file
internal/claude/      where Claude Code keeps its state and how to read it
internal/proc/        process table via ps(1), identity checks, signalling
internal/service/     launchd and systemd units and their lifecycle
internal/humanize/    durations and sizes, parsed and formatted for people
scripts/install.sh    curl-pipe-sh installer for release binaries
```

Two rules keep the tool trustworthy, and pull requests are reviewed against
them:

1. **`Scan` is read-only and `Apply` is the only thing that acts.** Every
   decision is made from a snapshot and recorded with a reason, so `status`,
   `run -dry-run` and the daemon always agree.
2. **A process is only ever signalled after its identity has been verified**
   against the session record (command line and start time), and never when
   it is an ancestor of the current process.

## Testing

The decision logic is tested with a fake process table and a temporary config
directory, so the interesting cases (recycled PIDs, missing transcripts, the
current session, unknown entrypoints) run everywhere without touching real
processes. A couple of tests spawn a `sleep` child and reap it through the
real `ps` and `kill` to cover the live path.

The daemon's schedule is tested against a simulated clock, so days of
operation, a laptop sleeping overnight, reboots and daemon restarts run in
milliseconds. Anything that depends on time should be written the same way:
take the clock as a parameter and compare wall-clock times, never monotonic
ones, because monotonic time stops while the computer sleeps.

CI runs the suite on macOS and Linux. If you only have one of them, say so in
the pull request and CI will cover the other.

## Pull requests

- Open an issue first for anything beyond a small fix, so the approach can be
  agreed before you spend time on it.
- Keep changes focused. Separate refactors from behaviour changes.
- Add or update tests for behaviour you change.
- Run `make test` and `make lint` before pushing.
- Add a line under **Unreleased** in [CHANGELOG.md](CHANGELOG.md).
- Use [Conventional Commits](https://www.conventionalcommits.org) for commit
  and pull request titles: `feat:`, `fix:`, `docs:`, `ci:`, `chore:`,
  `refactor:`, `test:`. The release notes are generated from them.

`main` is protected, for maintainers too: every change lands through a pull
request whose CI checks pass, and pull requests are squash-merged with their
title as the commit message. That title is what appears in the release notes,
so it is the one that has to follow Conventional Commits.

## Releasing (maintainers)

Releases are built by GitHub Actions with GoReleaser when a tag is pushed.
Open a pull request that moves the **Unreleased** section of the changelog
under the new version, merge it, then tag the merge commit:

```sh
git switch main && git pull
git tag -a v0.4.0 -m "v0.4.0"
git push origin v0.4.0
```

That produces the GitHub release with binaries and checksums for macOS and
Linux. The Homebrew cask in
[mateusclira/homebrew-tap](https://github.com/mateusclira/homebrew-tap) is
updated automatically when the `HOMEBREW_TAP_GITHUB_TOKEN` repository secret
(a fine-grained token with *contents: write* on the tap) is present;
otherwise that step is skipped and the cask can be updated by hand from the
`checksums.txt` release asset.

Release tags cannot be moved or deleted once pushed. The Go module proxy
caches a version the first time anyone fetches it, so a moved tag would break
`go install` for everyone who already has the old one. If a release goes
wrong, fix it in the next patch version.

The repository rules live under **Settings → Rules → Rulesets**. The checks
that `main` requires are matched by CI job name, so a pull request that
renames a job in `.github/workflows/ci.yml` must also update the ruleset.
