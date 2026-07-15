# Security policy

## Supported versions

Until v1.0, security fixes are provided for the latest prerelease and `main`;
older prerelease lines are not backported. Starting with v1.0, the newest patch
of the current stable minor line receives all security fixes. The immediately
previous stable minor line receives Critical and High severity fixes for 90
days after the next minor release. This limited overlap gives operators time to
upgrade without creating an open-ended backport obligation for the maintainer.

| Version | Supported |
| --- | --- |
| Latest stable minor, newest patch | Yes |
| Previous stable minor | Critical/High for 90 days |
| Latest prerelease before v1 | Best effort |
| `main` | Yes |
| Older releases | No |

Support means that a validated vulnerability can receive a private fix,
coordinated disclosure, and a signed patch release. It does not guarantee an
SLA. Unsupported releases may receive public mitigation advice but should be
upgraded before a fix is expected. Platform support also requires the native
release-commit evidence listed in the [platform capability record].

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Prefer a private
[GitHub security advisory] so the report, discussion, and coordinated fix remain
confidential. If that is not possible, email Ryan Wallace at
<ryancswallace@gmail.com>.

Include the affected version, reproduction steps or a proof of concept,
potential impact, and any known mitigation. Remove unrelated credentials and
personal data.

You should receive an acknowledgement within seven days. The maintainer will
coordinate validation, remediation, release timing, and disclosure with the
reporter. Please allow a reasonable remediation period before publishing
details.

[GitHub security advisory]: https://github.com/ryancswallace/jobman/security/advisories/new
[platform capability record]: docs/design/PLATFORM_CAPABILITIES.md
