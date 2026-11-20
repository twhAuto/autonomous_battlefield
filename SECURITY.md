# Security policy

OrdinalFrontier is a personal project maintained by one person. Reports are welcome
and are handled on a best-effort basis; there is no bug bounty.

## Reporting a vulnerability

Report privately through GitHub: open the repository's **Security** tab and choose
**Report a vulnerability**. Do not open a public issue or pull request for a
suspected vulnerability.

Include the affected file, route or component, the steps to reproduce, and the
impact you expect. The maintainer aims to reply within a week.

## Scope

- The code in this repository on the `main` branch.
- The hosted application at `play.ordinalfrontier.com`, for accounts you were
  invited to use.

When testing the hosted application, use only your own account and data, and do not
run load tests, automated scanners or anything that degrades the service for other
players. Findings in third-party dependencies belong with their maintainers unless
this project's use of the dependency makes them exploitable.

The [system architecture](docs/architecture/system-architecture.md) describes the
security boundaries and the limitations the project accepts.
