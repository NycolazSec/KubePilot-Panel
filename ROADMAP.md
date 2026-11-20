# Roadmap

KubePilot's goal: **the best game server panel, built for Kubernetes** — everything Pterodactyl does, without Wings,
plus what only an orchestrator makes possible.

Ideas and votes are welcome in the [issues](https://github.com/NycolazSec/KubePilot-Panel/issues).

## v0.1 — First official release

- [x] Game servers as StatefulSets: console, files, backups, schedules, sub-users, templates
- [x] Hardened by default: Pod Security `restricted`, NetworkPolicy, least-privilege RBAC
- [x] Open-source license (AGPL-3.0), contribution guide, security policy
- [x] Multi-arch container image (`amd64`, `arm64`) published to GHCR on every release
- [x] One-command install with a Helm chart, on any Kubernetes distribution
- [x] English and French interface, ready for community translations
- [ ] Two-factor authentication (TOTP) with recovery codes
- [ ] Personal API keys
- [ ] Backend test suite in CI

## v0.2 — Pterodactyl parity

- [ ] Import Pterodactyl / Pelican **eggs** — hundreds of games out of the box
- [ ] Built-in **SFTP** server
- [ ] Documented public REST API (OpenAPI) for bots and billing systems
- [ ] MySQL / MariaDB databases per server
- [ ] Backups to S3-compatible storage
- [ ] Password reset and notifications by email

## v0.3 and beyond — what Pterodactyl can't do

- [ ] **Multi-node**: add a machine by joining it to the cluster; servers are scheduled where there is room
- [ ] One-click **server transfer** between nodes
- [ ] Standard game ports (e.g. `25565`) instead of NodePorts
- [ ] **Auto-sleep**: empty servers stop by themselves and wake up when a player connects
- [ ] Prometheus metrics, Grafana dashboard, Discord alerts
- [ ] Native integration with the [KubePilot Discord bot](https://github.com/NycolazSec/KubePilot)
