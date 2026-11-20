# Security policy

KubePilot runs untrusted game servers and holds credentials to a Kubernetes cluster, so we take security reports
seriously.

## Reporting a vulnerability

**Please do not open a public issue.** Report it privately through GitHub:
[**Report a vulnerability**](https://github.com/NycolazSec/KubePilot-Panel/security/advisories/new).

Include as much as you can: affected version, configuration, steps to reproduce and the impact you observed.
You will get an acknowledgement within a few days. Once a fix is released, the advisory is published and you are
credited, unless you prefer to stay anonymous.

## Supported versions

Until 1.0, only the **latest release** receives security fixes. Always run the latest version.

## Scope

Examples of what we consider vulnerabilities:

- a game server escaping its pod, its volume or its network isolation;
- a user reaching a server, file or action they were not granted;
- the panel's ServiceAccount being abused to act outside `kubepilot-servers`;
- authentication, session, CSRF or XSS issues in the panel.

Running KubePilot with a configuration that the documentation marks as unsafe is out of scope.
