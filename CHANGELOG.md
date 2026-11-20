# Changelog

All notable changes to KubePilot are documented in this file.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

First public version of KubePilot, the Kubernetes-native game server panel.

### Added

- Game servers run as Kubernetes StatefulSets with their own volume and NodePort service — no Wings, no Docker socket.
- Real-time console over WebSocket, power actions, crash detection (OOMKilled, exit codes, image errors).
- File manager with code editor, drag & drop upload, zip download, archive / extract.
- Backups with restore, download, lock and automatic rotation; cron schedules for commands, power actions and backups.
- Startup variables, image selection, extra ports, sub-users with fine-grained permissions, per-server activity log.
- Admin area: overview, servers, users, editable templates with JSON import/export, settings, global activity log.
- Built-in templates: Minecraft Java & Bedrock, Node.js, Python, custom Docker image.
- Security: Pod Security `restricted`, NetworkPolicy isolation, least-privilege RBAC, `os.Root` file access, CSRF
  protection, strict CSP, login rate limiting.
- Open-source license (AGPL-3.0), contribution guide, security policy and public roadmap.
- Helm chart published to `oci://ghcr.io/nycolazsec/charts/kubepilot`: one-command install on any Kubernetes cluster.
- Multi-arch image (`linux/amd64`, `linux/arm64`) `ghcr.io/nycolazsec/kubepilot-panel`, signed with cosign, with SBOM
  and build provenance.
- Configurable StorageClass for game volumes (`KUBEPILOT_STORAGE_CLASS`).
- Two-factor authentication (TOTP, works with any authenticator app) with 10 single-use recovery codes: enable it
  from *My account*; codes can't be replayed, sign-in tickets are bound to the IP and limited to 5 attempts. Admins can
  reset it for a user who lost their phone (also `kubepilot user:2fa-reset --username …`).
- English and French interface: the language follows the browser and can be changed from the account page or the
  sign-in page. Server errors and console messages are translated for each viewer. New installs get English templates.

### Security

- Disk quotas are enforced: uploads, the editor, compression and extraction stop at the server's disk limit, and a
  running server writing far beyond it (10 %, at least 256 MB) is stopped — one server can no longer fill the node.
- FIFOs and device files planted by a game server are refused instead of hanging panel requests.
- Archives are limited to 100,000 entries.
- Optional mandatory two-factor authentication, for administrators or for everyone (admin settings).
- Per-account sign-in limit against password spraying from many IPs (IPs the user already signed in from are exempt).
- `__Host-` session cookie over HTTPS; stricter headers (CSP `object-src 'none'`, `base-uri 'none'`, COOP, CORP,
  HSTS 2 years, `Cache-Control: no-store` on the API); HTTP idle timeout and header size limit.
- React Router 7 (fixes CVE-2025-68470 and a related advisory).
- Game volumes get their own folder through a dedicated `kubepilot-local` StorageClass and the panel only mounts that folder,
  never the volumes of the other applications of the cluster.
- Optional Cloudflare-only origin (`ingress.cloudflareOnly`, Traefik), seccomp `RuntimeDefault` on the panel pod.

### Fixed

- Long dialogs (new template, new server…) no longer get their top cut off on small screens: the dialog stays within
  the screen, its content scrolls and the title and buttons stay visible.
