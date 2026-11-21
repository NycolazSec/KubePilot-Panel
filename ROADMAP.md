# Roadmap

KubePilot's goal: **the best game server panel, built for Kubernetes** — everything Pterodactyl does, without Wings,
plus what only an orchestrator makes possible.

Ideas and votes are welcome in the [issues](https://github.com/NycolazSec/KubePilot-Panel/issues).

## v0.1 — First official release ✅

- [x] Game servers as StatefulSets: console, files, backups, schedules, sub-users, templates
- [x] Hardened by default: Pod Security `restricted`, NetworkPolicy, least-privilege RBAC
- [x] Open-source license (AGPL-3.0), contribution guide, security policy
- [x] Multi-arch container image (`amd64`, `arm64`) published to GHCR on every release
- [x] One-command install with a Helm chart, on any Kubernetes distribution
- [x] English and French interface, ready for community translations
- [x] Two-factor authentication (TOTP) with recovery codes
- [x] Personal API keys (permissions, read-only, IP allow-list, expiry) and a documented REST API ([docs/API.md](docs/API.md))
- [x] Backend test suite in CI, plus govulncheck, npm audit and CodeQL

## v0.2 — Pterodactyl parity ✅

- [x] Import Pterodactyl / Pelican **eggs** — hundreds of games out of the box (with their `config.files`)
- [x] Built-in **SFTP** server (password or SSH keys, 2FA accounts key-only)
- [x] Documented public REST API for bots and billing systems ([docs/API.md](docs/API.md))
- [x] MySQL / MariaDB databases per server (MariaDB deployed by the chart)
- [x] Backups to S3-compatible storage (optionally encrypted with age)

## v0.3 — Easy, protected, lighter ✅

- [x] **One-command installer** (k3s, Helm, cert-manager, KubePilot, first administrator) and first start from the browser
- [x] **Auto-sleep**: empty servers stop by themselves and wake up when a player connects (Minecraft Java, via the router)
- [x] **Firewall and anti-DDoS** (optional): per-server and global rules, limits per source IP, automatic bans, "under attack" mode (nftables, one agent per node)
- [x] Password reset by email (SMTP)

## v0.4 and beyond — what Pterodactyl can't do

- [ ] Notifications by email
- [x] **Multi-node**: add a machine by joining it to the cluster; servers are scheduled where there is room
- [ ] One-click **server transfer** between nodes
- [ ] Standard game ports (e.g. `25565`) instead of NodePorts
- [ ] Prometheus metrics, Grafana dashboard, Discord alerts
- [ ] Native integration with the [KubePilot Discord bot](https://github.com/NycolazSec/KubePilot)
