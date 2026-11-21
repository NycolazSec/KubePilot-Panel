# Changelog

All notable changes to KubePilot are documented in this file.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- **Several machines**: `install.sh add-node` on the main machine prints an invitation (valid 24 hours) and the
  command to run on a new machine, `install.sh join` installs k3s there and joins the cluster. New servers go to the
  machine with the most free memory among those that accept new servers, or to the one the admin picks; a server
  stays on its machine (node affinity), and its address is that machine's.
- *Admin* → *Machines*: each machine's state, CPU and memory, the servers and resources KubePilot placed there, its
  agent, a public address per machine, and whether automatic placement may use it.
- **Node agent** (`nodeAgent.enabled`, on by default): one small pod per node serves the files of the servers of
  its node to the panel (file manager, SFTP, backups, plugins, configuration files, disk usage). SFTP over SSH, with
  keys derived on both ends from a Secret the chart creates; a NetworkPolicy lets only the panel in; no Kubernetes
  credentials. On the panel's own node nothing changes: the volumes are still opened directly.
- Fresh k3s installs use WireGuard between machines (`--flannel-backend=wireguard-native`) when the kernel has it.
- **Standard game ports** (with the firewall agents): a pool of ports in *Admin* → *Settings* (`25565-25600,
  19132-19140, 27015-27030`…), and a *Standard ports* option per server. A server gets the game's usual port when
  it's free on its machine (else another one of the pool); a port number is unique per machine, not in the whole
  cluster. The firewall agent of the machine forwards the port to the server's pod (nftables DNAT, players'
  addresses kept, protected like the other game ports); game pods stay Pod Security `restricted`. Ports something
  already answers on are skipped. Switching a stopped server between NodePorts and standard ports gives it new ports.

### Upgrading

- The panel gets the right to list the pods of its own namespace (to find the node agents), and its pod mounts the
  agents' token. Single-node installs behave as before: servers aren't pinned to a node until there are several.
- The ports table of the database is rebuilt on the first start (every port kept), so that a standard port can
  be used once per machine.

## [0.3.0] - 2026-11-21

**Easy to install, protected, and lighter on resources**: a one-command installer and a first start from the
browser, an optional firewall and anti-DDoS on the game ports, Minecraft servers that sleep when nobody plays, and
password reset by email.

### Upgrading from 0.2.0

- Nothing changes until you turn the new options on: `shield.enabled` (firewall and anti-DDoS, off by default: its
  agents need the host network and `NET_ADMIN`) and `email.smtp.*` (off without a host).
- With the Minecraft router, `minecraftRouter.autoSleep.enabled` is on: mc-router gets the right to change the
  replica count of game StatefulSets (`statefulsets/scale` only), so owners can turn auto-sleep on.
- The panel may now read and write ConfigMaps in the servers' namespace (the firewall configuration), and game
  servers may reach the cluster's own websites on the ingress controller's HTTP/HTTPS ports
  (`serversNamespace.ingressController`, Traefik by default).
- With Helm 4, an upgrade may stop on a conflict with fields last written by `kubectl` (for instance a Role edited
  by hand): run it again with `--force-conflicts`.
- Or simply run the installer again: on a server where KubePilot is installed, it updates it with the same settings.

### Added

- **One-command installer**: `curl -sfL https://raw.githubusercontent.com/NycolazSec/KubePilot-Panel/main/install.sh | sudo sh`.
  On a bare Linux server it installs k3s; with a cluster `kubectl` already reaches, it uses it. Then Helm (pinned
  version, checksum verified), cert-manager with a Let's Encrypt issuer, KubePilot, and the first administrator (its
  password goes through stdin and is shown once). It asks its questions first, in English or French, then works on
  its own; every answer is also an option (`--domain`, `--email`, `--yes`…). It checks the server (root, arch, RAM,
  ports 80/443, DNS pointing here), finds the local-path folder and the ingress class, opens the ports in ufw when
  it's active, and waits for the certificate. Run again, it updates KubePilot with the same settings.
- **First start from the browser**: a panel without any account prints a one-time setup token in its logs and asks
  for it at `/setup` to create the administrator — no `kubectl exec` needed after a Helm install, and nobody without
  access to the cluster can claim a fresh panel. The token lives in memory only and dies with the first account.
- CLI: `user:create` and `user:password` take `--password-stdin` (no password in a command line), new `user:list`.

- **Firewall and anti-DDoS** (optional, chart value `shield.enabled`). One agent per node (`kubepilot shield`, a
  DaemonSet with the host network and the `NET_ADMIN` capability only) filters the game ports with an nftables table
  of its own: the servers' ports and the Minecraft router, never SSH, the panel, the Kubernetes API or private
  networks. *Admin* → *Firewall*: limits per source IP (new TCP connections per second, simultaneous connections,
  UDP packets per second) with automatic bans, an "under attack" mode (limits three times tighter, bans six times
  longer), packets dropped by reason, banned IPs (unban, or ban by hand on every node), and rules for every server
  (block, or allow: never limited). *Server* → *Network* → *Firewall*: the server's own rules (`network`
  permission); one allow rule turns them into an allow list. The panel publishes the configuration in the ConfigMap
  `kubepilot-shield`, the agents report in `kubepilot-shield-status` (the only two objects they may touch). Bans
  survive an agent restart; the table is removed when the agent stops. Filtering happens on the node: an attack
  bigger than its link still has to be stopped upstream (the host's anti-DDoS, or a provider such as path.net).
  The image now includes `nft`.

- **Password reset by email** ("Forgot your password?" on the sign-in page) once an SMTP server is set in the chart
  (`email.smtp.*`, `email.from`; password in a Secret). The link carries a 256-bit token in its `#fragment` (never in
  server logs), stored hashed, valid once for one hour; a reset signs the account out everywhere and sends a
  confirmation email. Two-factor authentication still applies. Rate-limited per IP and per account, and the answer
  never tells whether an account exists. STARTTLS is mandatory in `starttls` mode and no password is ever sent over
  an unencrypted connection. *Admin* → *Settings* shows the email setup and sends a test email.
- `publicURL` (default `https://<ingress.host>`): the panel's address, for links in emails.

- **Auto-sleep** for Minecraft Java servers with a router address (*Settings* → *Auto-sleep*, or
  `PUT /servers/{id}/auto-sleep`): after 5 to 1440 minutes without players, the server stops by itself, and the next
  player who joins `<hostname>.<domain>` starts it again. The panel counts the players itself (Server List Ping, so
  players who joined through the IP and port count too), then writes the configuration files and pod spec before
  handing the server to mc-router — suspension, disk quota and the eggs' `config.files` still apply. In the server
  list, players see that the server is sleeping or starting; the first join attempt is answered with
  `minecraftRouter.autoSleep.wakeMessage`. New server status `sleeping`.
- mc-router may now change the replica count of the game StatefulSets (`statefulsets/scale` only — never a pod spec)
  when `minecraftRouter.autoSleep.enabled` (default `true`). It logs a warning that `patch` is forbidden, then uses
  the scale subresource.

### Fixed

- Game servers can reach the websites of their own cluster through their public address (a Discord bot whose music
  player fetches tracks from the bot's own dashboard, for instance). kube-proxy sends those connections straight to the
  ingress controller's pods, which the network isolation refused ("connection refused"). The isolation now opens
  only the controller's HTTP and HTTPS entry points — new chart value `serversNamespace.ingressController` (default:
  Traefik on k3s; `namespace: ""` keeps it closed). The node's own IPs and the rest of the cluster stay blocked.

## [0.2.0] - 2026-11-20

**Pterodactyl parity**: hundreds of games through Pterodactyl / Pelican eggs, a built-in SFTP server, MySQL databases
per server and backups on S3 — plus the Minecraft router, a plugin manager and an internal network between servers.

### Upgrading from 0.1.0

- `sftp.enabled` is on and opens **port 2022 on the panel's node** (`sftp.hostPort`). If Pterodactyl Wings runs on
  the same node, set `sftp.port` to another port (e.g. 2222).
- `databases.enabled` is on: the chart deploys a MariaDB (10 Gi volume) next to the game servers. Existing servers
  get a database limit of 0: raise it in *Admin* → *Servers*.
- `serversNamespace.rootInstall` is on: the servers' namespace is enforced at Pod Security `baseline` (games still
  run non-root; `restricted` stays audited). Set it to `false` to keep `restricted` if you don't use eggs.
- With Helm 4, the first upgrade of a namespace created by `kubectl` may need `--force-conflicts`.

### Added

- **Backups on S3-compatible storage** (`backups.s3.*`: AWS, Backblaze B2, OVH, Scaleway, Cloudflare R2, MinIO,
  SeaweedFS…): archives are streamed to a multipart upload (16 MiB parts, no temporary file, up to 160 GB), each part
  checked by the server (`Content-MD5`); downloads support range requests, restores stream from the bucket.
  Optional **encryption before upload** with an [age](https://age-encryption.org) key (`encryptionKey`): the provider
  only sees ciphertext, and `age -d -i key.txt` restores an archive without the panel. Each backup remembers where it
  is, so local backups made before keep working; an interrupted upload leaves nothing; a server's backups are deleted
  from the bucket with it. *Admin* → *Settings* shows the storage, its status and whether it's encrypted —
  credentials only live in a Kubernetes Secret.

- **MySQL / MariaDB databases per server** (*Databases* tab): one click creates a database and its own user
  (LuckPerms, CoreProtect, bots…), with the host, a JDBC URL and a `mysql://` URL to copy. The chart deploys a MariaDB
  next to the game servers (`databases.*`, Pod Security `restricted`, read-only root filesystem); only game servers and
  the panel can reach it, and it reaches nothing. Each user only has rights on its own database (no `GRANT`, no
  `FILE`, other databases not even listed), 30 connections at most. Passwords are shown once and never stored, and can
  be rotated. A server's databases are deleted with it. New per-server limit *Max. databases* (default 1 for new
  servers, 0 for existing ones) and sub-user permission *Databases*.

- **Egg configuration files** (`config.files`): before each start, the game's own files get the server's port, IP,
  name, password… from its variables, like Wings — the 6 parsers (`properties`, `ini`, `json`, `yaml`, `xml`,
  `file`), dot paths with `[0]` indexes and `*` wildcards, conditional replacements, all of Wings' placeholders
  (`{{server.build.default.port}}`, `{{server.build.env.X}}`, `{{env.X}}`, `{{server.environment.X}}`…). Better than
  Wings: comments and key order are kept (YAML, JSON), existing value types too, and a list is never invented.
  Flat files (`properties`, `ini`) are created if missing; the others are edited once the game wrote them. 171 of the
  172 catalog eggs that use it are converted (values pointing to Wings' Docker network are left out, with a warning).
  Shown and editable (JSON) in the template editor; the console says which files were updated.

- **Built-in SFTP server** (FileZilla, WinSCP, `sftp`): sign in as `<username>.<server id>` with the panel password or an
  SSH key added in *My account* (ed25519, ECDSA, security keys, RSA ≥ 3072 bits). Accounts with two-factor
  authentication must use a key: a password alone can't bypass 2FA. Same rules as the web file manager — `os.Root`
  (no escape, even through symlinks planted by a game), no symlink created, FIFOs refused, writes counted at once on a
  disk quota shared by parallel transfers (a new file cut by the quota is removed), files owned by the game's user.
  Only the `sftp` subsystem: no shell, command or port forwarding. Sign-in limits per IP and per account, 16
  connections per IP, idle sessions closed after 15 min, every sign-in and a summary of the changes in the activity
  log. New sub-user permission *SFTP access*; *Files* → **SFTP** shows the address, username and host key
  fingerprint. Chart: `sftp.enabled`, `sftp.port` (default 2022), `sftp.hostPort` (default `true`: the port is
  opened on the panel's node so the panel sees real client IPs — k3s ServiceLB masquerades them), or a Service
  (`sftp.service.*`) with `sftp.hostPort=false`.

- **Pterodactyl / Pelican eggs**: *Admin* → *Templates* → *Import an egg*, from the community catalog
  (~300 games and apps of github.com/pelican-eggs, searchable), a GitHub link or a `.json` file. PTDL_v1, PTDL_v2
  and PLCN_v3 are supported. The egg becomes a template draft to review: images (newest Java first), startup command
  handed to the image's own entrypoint like Wings does, variables with their validation rules (`in:`, `regex:`,
  numeric, boolean), stop command, Minecraft features detected. The install script runs as root — without
  privilege escalation, with seccomp and only the capabilities package managers need — and its files are handed back
  to the game's user; the game itself never runs as root. New chart value `serversNamespace.rootInstall` (default
  `true`: the namespace is enforced at Pod Security `baseline`, with `restricted` still audited and warned about).
- Stop command per template (`stop`, `shutdown`, `^C`…): typed in the console before any stop, restart or node
  drain, so the game saves cleanly.
- Minecraft servers that enable RCON in `server.properties` (eggs, other images) get the player actions too.

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

### Changed

- Templates imported from eggs get Wings' memory headroom above the server's memory (eggs size the JVM from it).

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

[Unreleased]: https://github.com/NycolazSec/KubePilot-Panel/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/NycolazSec/KubePilot-Panel/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/NycolazSec/KubePilot-Panel/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/NycolazSec/KubePilot-Panel/releases/tag/v0.1.0
