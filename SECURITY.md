# Security policy

This file covers reporting vulnerabilities in Lumek. The protections the framework applies to your application are documented in [docs/security.md](docs/security.md).

## Supported versions

| Version | Supported |
| --- | --- |
| Latest `v0.x` minor | ✅ |
| Older `v0.x` | ❌ |

Lumek is pre-1.0: only the latest minor release receives fixes. A long-term support policy will be published with v1.0.0.

## Reporting a vulnerability

**Do not open a public issue.**

Report privately through GitHub: **Security → Report a vulnerability** on <https://github.com/AlexGalhardo/golang-framework/security/advisories/new>.

Please include:

- Affected version or commit
- A description of the issue and its impact
- Steps or code to reproduce
- Any suggested fix

## What to expect

| Step | Target time |
| --- | --- |
| Acknowledgement | 3 business days |
| Initial assessment | 7 days |
| Fix released for confirmed issues | 30 days, faster for critical issues |

Fixes are published as GitHub Security Advisories, which also feed the [Go vulnerability database](https://pkg.go.dev/vuln/) so `govulncheck` users are notified. Reporters are credited unless they prefer otherwise.
