# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| latest  | Yes                |

## Reporting a Vulnerability

If you discover a security vulnerability in Watchfire, please report it
responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please send an email to **security@watchfire.io** with:

- A description of the vulnerability
- Steps to reproduce the issue
- Any potential impact you've identified
- Suggested fix (if you have one)

## Response Timeline

- **Acknowledgment**: Within 48 hours of your report
- **Initial assessment**: Within 1 week
- **Fix and disclosure**: We aim to release a fix within 30 days of a confirmed
  vulnerability, depending on complexity

## Disclosure Policy

- We will coordinate disclosure with you
- We will credit reporters in our release notes (unless you prefer anonymity)
- We ask that you give us reasonable time to address the issue before public
  disclosure

## Security Best Practices

Watchfire runs coding agents in sandboxed environments. Key security
considerations:

- The daemon uses macOS `sandbox-exec` to restrict agent access
- Agents cannot access `~/.ssh`, `~/.aws`, `~/.gnupg`, or `.env` files
- `.git/hooks` are blocked to prevent hook injection
- All agent sessions run in isolated git worktrees

If you find a way to bypass these protections, we consider that a critical
security issue and ask that you report it immediately.
