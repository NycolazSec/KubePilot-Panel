# Changelog

All notable changes to KubePilot are documented in this file.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- "Internal only" servers (*Network* → *Reachable from the Internet* off): no public port at all, only the owner's
  other servers reach them — for a Lavalink, a database… Switching keeps the internal address and live connections.

- Internal network between the servers of one owner (a bot and its Lavalink, a game and its database…): each
  server shows its internal address in *Network*; other users' servers stay unreachable.

- Plugins & mods for Minecraft Java servers, from Modrinth: search filtered by the server's software (Paper, Purpur,
  Folia, Fabric, Quilt, Forge, NeoForge) and Minecraft version, one-click install with dependencies (declared on
  Modrinth **and** read from the plugin's own `plugin.yml` / `paper-plugin.yml`), installed jars recognized by their
  SHA-1 with updates, enable / disable, delete, missing dependencies flagged. Files only come from Modrinth's CDN,
  are checked against their SHA-512 and stay within the disk quota; icons are proxied (raster only).

- Minecraft router (`minecraftRouter.enabled`, itzg/mc-router): Java servers get `<hostname>.<domain>` on the standard
  port 25565 — no port to type for players. Hostname picked from the server name, editable in *Network*; domain in the
  admin settings (wildcard DNS record, DNS only). The NodePort address keeps working.

## [0.1.0] - 2026-11-20

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
- Minecraft panel (*Players* tab of Minecraft servers): live status with the server icon and colored MOTD, online
  players with their skin heads (fetched and cached by the panel, no third-party requests from browsers), whitelist,
  operators and bans with heads, kick / ban / op / whitelist in one click through RCON (persistent connection, no
  console spam). Bedrock: status and allowlist. Templates get a "game panel" setting.
- REST API with personal API keys (*My account → API keys*): permissions per key, read-only mode, IP allow-list,
  expiry, 240 requests / minute, shown once and stored hashed, never able to manage the account; actions are logged
  with the key's name. New `GET /api/servers/{id}/console` endpoint and WebSocket console for scripts.
  Documentation in `docs/API.md`.
- English and French interface: the language follows the browser and can be changed from the account page or the
  sign-in page. Server errors and console messages are translated for each viewer. New installs get English templates.

### Security

- 2FA recovery codes are hashed with bcrypt (they're short enough to be brute-forced from a stolen database
  with a fast hash); codes created before keep working.
- CI: govulncheck, npm audit and CodeQL (security-extended) on every pull request; GitHub Actions pinned to commit SHAs,
  read-only workflow token by default.
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

[Unreleased]: https://github.com/NycolazSec/KubePilot-Panel/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/NycolazSec/KubePilot-Panel/releases/tag/v0.1.0
