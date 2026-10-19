# SECURITY.md — ouija

## Reporting a vulnerability

Please **do not** open a public issue for a security vulnerability.

- Use [GitHub Security Advisories → Report a vulnerability](https://github.com/discorgento/ouija/security/advisories/new)
  (private, reaches the maintainers, not visible to anyone else).
- If that is unavailable, contact the maintainers through
  [discorgento.com](https://discorgento.com/) and mark the subject
  `SECURITY — ouija`.

You will get an acknowledgement as soon as possible, a confirmation once the
issue is reproduced, and credit in the fix release if you want it.

## Scope

ouija is an AI harness: it executes model-proposed tool calls (file reads and
writes, `bash`) inside a project directory. In scope:

- Escaping the filesystem sandbox (`SafeJoin`, symlink resolution, `SafeResolve`)
- Bypassing a hard rule: the bash denylist, protected-path policy, or an
  environment sanitization
- Reading or exfiltrating secrets (config files with `0600`, credential-like
  env vars) through the tools, the MCP server, traces, or logs
- Remote code execution or request smuggling through a provider adapter or the
  MCP transport

Out of scope (accepted risks, documented in
[docs/explanation/security.md](docs/explanation/security.md)):

- Prompt injection steering the *content* of an analysis
- A model doing something unwanted **inside** an approval the user granted
- Denial of service caused by model behaviour (budget ceilings bound the cost)
- Vulnerabilities in dependencies

## Principles the code must uphold

These are invariants, not features. A change that weakens any of them is a
security change and needs tests proving the previous behaviour was wrong:

1. **Fail closed.** An inconclusive check means *deny* — never *allow*.
2. **Hard rules outrank consent.** The denylist and protected paths run before
   any approval prompt; a user "yes" cannot override them.
3. **Approvals are scoped to a target**, not to a tool name (one path, one
   exact command), and only **Session** persists them.
4. **No secrets in logs, traces, or stdout.** The MCP server keeps protocol
   messages on stdout and diagnostics on stderr.
5. **The sandbox is shared.** Tools, `doc` output, and MCP resources all go
   through the same `SafeJoin`/`SafeResolve` — never a re-implementation.

## Supported version

This project ships from `main`. There are no long-term-support branches: use
the latest release (see [CHANGELOG.md](CHANGELOG.md)).
