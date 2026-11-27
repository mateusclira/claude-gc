<div align="center">

# claude-gc

**A garbage collector for Claude Code sessions.**

Finds the `claude` processes that the VS Code extension spawns and forgets,
reaps the ones you stopped using days ago, and keeps doing it quietly in the
background. macOS and Linux. One static binary, no dependencies.

[![CI](https://github.com/mateusclira/claude-gc/actions/workflows/ci.yml/badge.svg)](https://github.com/mateusclira/claude-gc/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mateusclira/claude-gc?display_name=tag&sort=semver)](https://github.com/mateusclira/claude-gc/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/mateusclira/claude-gc)](https://goreportcard.com/report/github.com/mateusclira/claude-gc)
[![Platforms](https://img.shields.io/badge/platforms-macOS%20%7C%20Linux-5b6b7a)](#install)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

</div>

---

## The problem

Every Claude Code chat tab in VS Code starts a `claude` process. Closing the
tab does not stop it. In practice the extension host keeps those children
alive until VS Code itself quits, so a machine that stays up for weeks
accumulates dozens of idle sessions, each holding 150 to 300 MB of memory and
polling in the background.

This is what that looked like on the laptop this tool was written on:

```
$ claude-gc status
PID    NAME     PROJECT       RSS     STARTED      LAST ACTIVITY  ACTION
39953  api-0d   ~/code/api    184 MB  65d 2h ago   none           reap
94772  api-54   ~/code/api    208 MB  64d 22h ago  none           reap
61669  api-52   ~/code/api    175 MB  64d 21h ago  none           reap
  ⋮
45382  api-15   ~/code/api    226 MB  3d 18h ago   3d 18h ago     reap
82118  api-10   ~/code/api    231 MB  1d 17h ago   1d 17h ago     keep
67205  api-ce   ~/code/api    272 MB  2d 18h ago   18h 7m ago     keep
83446  api-72   ~/code/api    243 MB  47m ago      38m ago        keep
95113  gc-99    ~/code/gc     267 MB  33m ago      just now       skip (current session)

42 sessions · 8.4 GB resident · 38 to reap (7.4 GB, idle > 3d) · 3 active · 1 skipped
26 stale socket files left behind by exited sessions.
```

Forty-two processes, 8.4 GB of RAM, the oldest started 65 days earlier. Three
of them were actually in use.

## What it does

On every pass (every 30 minutes by default) claude-gc:

1. **Reads Claude Code's own session registry.** Claude Code writes one record
   per running session to `~/.claude/sessions/<pid>.json`, naming its process,
   project, start time and transcript.
2. **Checks when each session was last used.** That is the modification time
   of its transcript, the file Claude Code appends to on every message. No
   transcript on disk means Claude Code already cleaned it up for inactivity,
   so the session's age is used instead.
3. **Terminates sessions idle for longer than 3 days.** SIGTERM first, a
   5-second grace period, then SIGKILL.
4. **Removes what exited sessions leave behind:** stale records in
   `~/.claude/sessions` and dead sockets in `/tmp/cc-socks`.

Reaped sessions stay resumable. The transcript is never touched, so the
conversation still shows up in Claude Code's history and `claude --resume`
works as before.

## Install

**Homebrew** (macOS)

```sh
brew install mateusclira/tap/claude-gc
```

**Go** (macOS and Linux)

```sh
go install github.com/mateusclira/claude-gc/cmd/claude-gc@latest
```

**Install script** (downloads the latest release to `~/.local/bin` and verifies its checksum)

```sh
curl -fsSL https://raw.githubusercontent.com/mateusclira/claude-gc/main/scripts/install.sh | sh
```

**Binaries** for macOS and Linux on amd64 and arm64 are on the
[releases page](https://github.com/mateusclira/claude-gc/releases/latest).

## Quick start

```sh
claude-gc status            # what is running and what would be reaped; changes nothing
claude-gc run --dry-run     # preview one pass
claude-gc run               # reap now
claude-gc service install   # keep it tidy automatically
```

`service install` registers a per-user background service: a launchd agent on
macOS, a systemd user unit on Linux. It starts right away and on every login.

```
$ claude-gc service install
Installed ~/Library/LaunchAgents/io.github.mateusclira.claude-gc.plist
claude-gc daemon is running (pid 51234). Every 30m it reaps claude-vscode sessions idle for longer than 3d.
Logs: ~/Library/Logs/claude-gc/claude-gc.log
```

## How it decides

| Session state | Action |
| --- | --- |
| Transcript untouched for longer than `--max-idle` | **reap** |
| No transcript on disk and started longer than `--max-idle` ago | **reap** |
| Used within `--max-idle` | keep |
| The session claude-gc itself is running inside | skip |
| Entrypoint other than `claude-vscode`, such as `claude` in a terminal | skip, unless listed in `--entrypoints` |
| Record whose process already exited | clean up the record only |
| Live process that cannot be verified as the recorded session | skip |

## Safety

claude-gc kills processes, so it is deliberately conservative:

- **It only acts on sessions Claude Code itself registered.** A process is
  reaped only if its PID has a session record, its command line is a Claude
  Code binary, and its start time matches the one in the record. A recycled
  PID can never be mistaken for an old session.
- **It never kills itself.** The session claude-gc runs inside, and every
  ancestor of it, is always skipped.
- **Terminal sessions are left alone by default.** Only `claude-vscode`
  sessions (the ones the extension spawns) are eligible unless you opt in.
- **It stops gracefully.** SIGTERM lets Claude Code flush its transcript;
  SIGKILL is a last resort after the grace period.
- **It never deletes transcripts.** Nothing under `~/.claude/projects` is
  touched. Claude Code prunes old transcripts itself; see
  [`cleanupPeriodDays`](https://docs.anthropic.com/en/docs/claude-code/settings).
- **`status` and `run --dry-run` never change anything**, so you can always
  look before you leap.

## Configuration

Every tunable is a flag, with an environment variable as fallback. Durations
accept `d`, `h`, `m` and `s`: `3d`, `36h`, `1d12h`, `30m`.

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `--max-idle` | `CLAUDE_GC_MAX_IDLE` | `3d` | Reap sessions idle for longer than this |
| `--interval` | `CLAUDE_GC_INTERVAL` | `30m` | Time between passes (`daemon`, `service install`) |
| `--entrypoints` | `CLAUDE_GC_ENTRYPOINTS` | `claude-vscode` | Comma-separated entrypoints to reap; `*` for all |
| `--grace` | `CLAUDE_GC_GRACE` | `5s` | Wait after SIGTERM before SIGKILL |
| `--config-dir` | `CLAUDE_CONFIG_DIR` | `~/.claude` | Where Claude Code keeps its state |
| `--json` | | | Machine-readable output for `status` and `run` |
| `--verbose` | | | Debug logging |

Examples:

```sh
claude-gc status --max-idle 1d                      # tighter threshold, just looking
claude-gc run --entrypoints 'claude-vscode,cli'     # also reap idle terminal sessions
claude-gc service install --max-idle 7d --interval 1h
```

To change the settings of an installed service, run `service install` again
with the new flags.

## The background service

| | macOS | Linux |
| --- | --- | --- |
| Manager | launchd (per-user agent) | systemd (`--user` unit) |
| Unit file | `~/Library/LaunchAgents/io.github.mateusclira.claude-gc.plist` | `~/.config/systemd/user/claude-gc.service` |
| Logs | `~/Library/Logs/claude-gc/claude-gc.log` | `journalctl --user -u claude-gc` |

```sh
claude-gc service status      # installed? running?
claude-gc service logs -f     # follow the log
claude-gc service uninstall   # stop and remove
```

On Linux, user services stop at logout unless lingering is enabled:
`loginctl enable-linger $USER`. Systems without systemd can run
`claude-gc daemon` under any supervisor, or `claude-gc run` from cron.

The daemon itself is a single Go process that sleeps between passes; it uses
a few megabytes of memory and no CPU while idle.

## Scripting

`status --json` and `run --json` print stable JSON. For example, memory held
by sessions that would be reaped:

```sh
claude-gc status --json | jq '.summary.reapableKB / 1024 | floor'
```

## FAQ

**Isn't this treating the symptom?**
Yes. The leak is the extension not ending child processes when a tab closes,
and quitting VS Code entirely (not just reloading the window) clears them all
at once. Until that is fixed upstream, claude-gc keeps the damage bounded.

**Will I lose a conversation?**
No. The transcript stays on disk and the session remains in Claude Code's
history. Reaping only ends the idle process.

**Why three days?**
It is the gap between "I stepped away for the weekend" and "I forgot this
existed". Tune it with `--max-idle`.

**What about sessions started from a terminal?**
Skipped by default, because a terminal tab is something you chose to keep
open. `--entrypoints '*'` or `--entrypoints claude-vscode,cli` opts them in.
`claude-gc status` shows every session's entrypoint decision, so you can see
what would change.

**Does it need root?**
No. It runs as you and only sees your own sessions.

**Windows?**
Not supported. WSL2 with systemd enabled works like any other Linux.

## Contributing

Bug reports, ideas and pull requests are welcome. Start with
[CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, project layout
and how releases work. Everyone participating is expected to follow the
[Code of Conduct](CODE_OF_CONDUCT.md). Security issues go through
[SECURITY.md](SECURITY.md), not the public tracker.

## License

[MIT](LICENSE). claude-gc is an independent community project and is not
affiliated with or endorsed by Anthropic. Claude and Claude Code are
trademarks of Anthropic, PBC.
