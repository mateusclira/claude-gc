<div align="center">

# claude-gc

**A garbage collector for Claude Code sessions.**

Finds the `claude` processes that the VS Code extension spawns and forgets,
reaps the ones you stopped using days ago, and keeps doing it once a day in
the background. macOS and Linux. One static binary, no dependencies.

[![CI](https://github.com/mateusclira/claude-gc/actions/workflows/ci.yml/badge.svg)](https://github.com/mateusclira/claude-gc/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mateusclira/claude-gc?display_name=tag&sort=semver)](https://github.com/mateusclira/claude-gc/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/mateusclira/claude-gc)](https://goreportcard.com/report/github.com/mateusclira/claude-gc)
[![Platforms](https://img.shields.io/badge/platforms-macOS%20%7C%20Linux-5b6b7a)](#install)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

</div>

---

## The problem

Every Claude Code chat tab in VS Code starts a `claude` process. Closing the
tab does not stop it. In practice, the extension host keeps those children
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

Each pass, claude-gc:

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

## When it runs

The background service runs one pass a day, and only once the computer has
been up for a full day:

| Time since the last restart | What happens |
| --- | --- |
| Less than 24 hours | Nothing. A restart ends every Claude Code process, so there is nothing to collect yet. |
| 24 hours | The first pass. |
| Every 24 hours after that | Another pass, for as long as the computer stays up. |

- **Sleep counts as uptime.** Only a restart resets the clock. A laptop that
  sleeps overnight still gets its daily pass, a few minutes after it wakes.
- **Restarting the daemon does not cause an extra pass.** It remembers its
  last pass, so logging out and back in leaves the schedule alone.
- **Installing on a computer that has been up for more than a day runs a
  pass right away**, then one every 24 hours.
- **`claude-gc run` ignores the schedule.** It always runs, right now.

Both numbers are configurable. `-fq` sets how often passes run and
`-min-uptime` how long to wait after a restart, with `0` turning the wait off.
Unless you set it yourself, the wait never outlasts `-life`, so a short life
such as `-life 2h` is honored right after a restart too.

## Install

**Install script** (downloads the latest release to `~/.local/bin` and verifies its checksum)

```sh
curl -fsSL https://raw.githubusercontent.com/mateusclira/claude-gc/main/scripts/install.sh | sh
```

**Go** (macOS and Linux)

```sh
go install github.com/mateusclira/claude-gc/cmd/claude-gc@latest
```

**Binaries** for macOS and Linux on amd64 and arm64 are on the
[releases page](https://github.com/mateusclira/claude-gc/releases/latest).
A Homebrew tap is on the way.

## Quick start

```sh
claude-gc status            # what is running and what would be reaped; changes nothing
claude-gc run -dry-run      # preview one pass
claude-gc run               # reap now
claude-gc service install   # keep it tidy automatically
```

`service install` registers a per-user background service: a launchd agent on
macOS, a systemd user unit on Linux. It starts right away and on every login.

Two flags shape what it does: `-fq` is how often it runs, and `-life` is how
long a session may stay unused before it is reaped. The defaults are
`-fq 24h -life 3d`. To check every two hours instead:

```sh
claude-gc service install -fq 2h -life 3d
```

```
$ claude-gc service install
Installed ~/Library/LaunchAgents/io.github.mateusclira.claude-gc.plist
Service:    running (pid 51234)
Schedule:   once a day, after 24 hours of uptime
Reaps:      claude-vscode sessions idle for longer than 3d
Uptime:     82d 3h
Last pass:  none yet
Next pass:  now
Logs:       ~/Library/Logs/claude-gc/claude-gc.log
```

## How it decides

| Session state | Action |
| --- | --- |
| Transcript untouched for longer than `-life` | **reap** |
| No transcript on disk and started longer than `-life` ago | **reap** |
| Used within `-life` | keep |
| The session claude-gc itself is running inside | skip |
| Entrypoint other than `claude-vscode`, such as `claude` in a terminal | skip, unless listed in `-entrypoints` |
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
- **`status` and `run -dry-run` never change anything**, so you can always
  look before you leap.

## Configuration

Every setting is a flag, with an environment variable as fallback. The two
that matter most are `-fq` and `-life`.

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `-life` | `CLAUDE_GC_LIFE` | `3d` | How long a session may stay unused before it is reaped |
| `-fq` | `CLAUDE_GC_FQ` | `24h` | How often the daemon runs a pass |
| `-min-uptime` | `CLAUDE_GC_MIN_UPTIME` | `24h` | No pass until the computer has been up this long. Shrinks to `-life` when that is shorter; `0` disables |
| `-entrypoints` | `CLAUDE_GC_ENTRYPOINTS` | `claude-vscode` | Comma-separated entrypoints to reap; `*` for all |
| `-grace` | `CLAUDE_GC_GRACE` | `5s` | Wait after SIGTERM before SIGKILL |
| `-config-dir` | `CLAUDE_CONFIG_DIR` | `~/.claude` | Where Claude Code keeps its state |
| `-json` | | | Machine-readable output for `status` and `run` |
| `-verbose` | | | Debug logging |

`-fq` and `-min-uptime` apply to `daemon` and `service install`. The others
apply to every command.

Durations use `d`, `h`, `m` and `s`, such as `3d`, `24h`, `2h`, `30m` or
`1d12h`, and `0`. Flags take one dash or two, so `-fq 2h`, `--fq 2h` and
`-fq=2h` are the same. The names from before 0.3.0, `-interval` and
`-max-idle`, still work.

Examples:

```sh
claude-gc status -life 2h                        # what would a 2-hour life reap? changes nothing
claude-gc run -entrypoints 'claude-vscode,cli'   # also reap idle terminal sessions
claude-gc service install -fq 2h                 # check every 2 hours instead of once a day
claude-gc service install -fq 30m -life 2h       # strict: anything unused for 2 hours goes
claude-gc service install -life 7d               # gentle: only sessions unused for a week
```

To change the settings of an installed service, run `service install` again
with the new flags.

## The background service

| | macOS | Linux |
| --- | --- | --- |
| Manager | launchd (per-user agent) | systemd (`--user` unit) |
| Unit file | `~/Library/LaunchAgents/io.github.mateusclira.claude-gc.plist` | `~/.config/systemd/user/claude-gc.service` |
| Logs | `~/Library/Logs/claude-gc/claude-gc.log` | `journalctl --user -u claude-gc` |
| State | `~/Library/Application Support/claude-gc/state.json` | `~/.local/state/claude-gc/state.json` |

```sh
claude-gc service status      # running? when was the last pass, when is the next?
claude-gc service logs -f     # follow the log
claude-gc service uninstall   # stop and remove
```

`service status` reads the schedule back from the daemon. Here, a computer
restarted three hours ago:

```
$ claude-gc service status
Service:    running (pid 812)
Schedule:   once a day, after 24 hours of uptime
Reaps:      claude-vscode sessions idle for longer than 3d
Uptime:     3h 12m
Last pass:  2d 6h ago, reaped 4 sessions and freed 912 MB, before the last restart
Next pass:  in 20h 48m (Sat Nov 28 09:49), once uptime reaches 24 hours
Unit:       ~/Library/LaunchAgents/io.github.mateusclira.claude-gc.plist
Logs:       ~/Library/Logs/claude-gc/claude-gc.log
```

On Linux, user services stop at logout unless lingering is enabled:
`loginctl enable-linger $USER`. Systems without systemd can run
`claude-gc daemon` under any supervisor, or `claude-gc run` from a daily cron
job.

The daemon is a single Go process using about 7 MB of memory. Between passes
it wakes every few minutes only to look at the clock, which is how it notices
a pass that came due while the computer slept.

## Scripting

`status -json` and `run -json` print stable JSON. For example, memory held
by sessions that would be reaped:

```sh
claude-gc status -json | jq '.summary.reapableKB / 1024 | floor'
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
existed". Tune it with `-life`.

**Does `-life` count from when a session started?**
No, from its last message. A session you are using is never cut off, however
old it is. Only sessions whose transcript is gone, which Claude Code deletes
after a month of inactivity, count from when they started.

**Why once a day?**
Sessions become eligible after three idle days, so a daily pass reaps each
one within a day of that, and the daemon stays out of the way the rest of the
time. Use `-fq` to run more often, for example `-fq 2h`.

**Why does nothing happen right after a restart?**
There is nothing to collect. A restart ends every Claude Code process, and no
session started since then can have been idle for three days. The daemon
waits a full day of uptime before its first pass. `-min-uptime 0` turns this
off, and a `-life` shorter than a day shortens the wait to match.

**What about sessions started from a terminal?**
Skipped by default, because a terminal tab is something you chose to keep
open. `-entrypoints '*'` or `-entrypoints claude-vscode,cli` opts them in.
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
