# Security Policy

claude-gc terminates processes and deletes files, so bugs in it can have real
consequences. Please report anything that could make it act on the wrong
process or the wrong file.

## Supported versions

Only the latest release receives fixes. Please upgrade before reporting.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting:
**Security → Report a vulnerability** on
https://github.com/mateusclira/claude-gc/security/advisories/new.

Please do not open a public issue for security problems. Include the version
(`claude-gc version`), your operating system, and the steps or session state
that trigger the problem. You should hear back within a week; fixes are
released as soon as they are ready and credited to you unless you prefer
otherwise.

## Scope

In scope: anything that leads claude-gc to signal a process that is not an
abandoned Claude Code session, to remove files it should not, to run with
more privilege than the invoking user, or to be influenced by data it reads
from disk in a way that is not intended.

Out of scope: the behaviour of Claude Code or the VS Code extension
themselves.
