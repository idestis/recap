# Security

recap records terminal commands, notes and links while a session runs, so it handles sensitive text by design. What it does about that:

- Secrets are masked locally before any model call, session naming or Git write.
- Raw timelines, screenshots and files stay in `~/.recap` on your machine; only reviewed recipes and short session summaries are written to Git.
- Model API keys live in the macOS Keychain, never in config files or logs.
- recap makes no network requests of its own except to the model endpoint you configure and the Git remote you publish to.

## Reporting a vulnerability

Please report privately through [GitHub security advisories](https://github.com/idestis/recap/security/advisories/new), not in a public issue. Include steps to reproduce and the recap version (`recap version`). You can expect a reply within a week.

A missed secret pattern in redaction counts as a security issue.
