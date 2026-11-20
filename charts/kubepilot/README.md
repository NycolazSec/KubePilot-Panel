# KubePilot Helm chart

Installs [KubePilot](https://github.com/NycolazSec/KubePilot-Panel), the Kubernetes-native game server panel.

```sh
helm install kubepilot oci://ghcr.io/nycolazsec/charts/kubepilot \
  --namespace kubepilot-system --create-namespace \
  --set ingress.host=panel.example.com \
  --set ingress.clusterIssuer=letsencrypt-prod

# create the first admin account
kubectl -n kubepilot-system exec deploy/kubepilot -- \
  kubepilot user:create --username admin --email you@example.com --password 'change-me-please' --admin
```

Upgrade with `helm upgrade kubepilot oci://ghcr.io/nycolazsec/charts/kubepilot -n kubepilot-system --reuse-values`.

`helm uninstall` removes the panel but **keeps** the game servers namespace and the panel's data volume, so no game
data is ever lost by accident. Delete them by hand if you really want to.

## Requirements

- Kubernetes ≥ 1.29 with a **hostPath-based storage provisioner** for game volumes
  (k3s ships `local-path`; elsewhere install [local-path-provisioner](https://github.com/rancher/local-path-provisioner)
  and set `storage.hostPath=/opt/local-path-provisioner`)
- [metrics-server](https://github.com/kubernetes-sigs/metrics-server) for CPU / RAM graphs
- An ingress controller and, for HTTPS, [cert-manager](https://cert-manager.io) with a `ClusterIssuer`

## Values

| Key | Default | Description |
|---|---|---|
| `image.repository` | `ghcr.io/nycolazsec/kubepilot-panel` | Panel image |
| `image.tag` | chart `appVersion` | Image tag |
| `serversNamespace.name` | `kubepilot-servers` | Namespace of the game servers (kept on uninstall) |
| `serversNamespace.create` | `true` | Create that namespace (Pod Security `restricted` audited) |
| `sftp.enabled` | `true` | Built-in SFTP server (`<username>.<server id>`, panel password or SSH key) |
| `sftp.port` | `2022` | SFTP port (use another one if Pterodactyl Wings runs on the nodes) |
| `sftp.hostPort` | `true` | Open `sftp.port` on the panel's node, keeping client IPs (k3s ServiceLB would hide them) |
| `sftp.service.type` | `LoadBalancer` | With `sftp.hostPort=false`: SFTP Service type (`externalTrafficPolicy: Local`) |
| `serversNamespace.rootInstall` | `true` | Let egg install scripts run as root (namespace enforced at `baseline`; games stay non-root). `false` enforces `restricted` |
| `storage.isolated` | `true` | Create a StorageClass that keeps game volumes in their own folder; the panel mounts only that folder, never other apps' volumes (local-path-provisioner ≥ v0.0.27) |
| `storage.className` | `kubepilot-local` | StorageClass of game volumes (created when `isolated`, else an existing hostPath-based one) |
| `storage.provisioner` | `rancher.io/local-path` | Provisioner of the created StorageClass |
| `storage.hostPath` | `/var/lib/rancher/k3s/storage` | Directory where that provisioner creates volumes on the node |
| `storage.node` | `""` | Node holding the volumes, where the panel runs. Empty on a single-node cluster |
| `persistence.size` | `20Gi` | Panel data volume (database + backups) |
| `persistence.storageClass` | `""` | StorageClass of the panel data volume (empty = default) |
| `persistence.existingClaim` | `""` | Use an existing PVC for panel data |
| `ingress.enabled` | `true` | Expose the panel through an Ingress |
| `ingress.host` | `""` | **Required** when the ingress is enabled: public domain of the panel |
| `ingress.className` | `""` | IngressClass (empty = cluster default) |
| `ingress.clusterIssuer` | `""` | cert-manager ClusterIssuer for the TLS certificate |
| `ingress.annotations` | `{}` | Extra Ingress annotations |
| `ingress.tls.enabled` | `true` | Serve the panel over HTTPS |
| `ingress.tls.secretName` | `kubepilot-tls` | Secret holding the certificate |
| `ingress.cloudflareOnly` | `false` | Traefik: only accept requests from Cloudflare's ranges (domain proxied by Cloudflare) — nobody can bypass Cloudflare by hitting the node directly |
| `ingress.sourceRanges` | `[]` | Traefik: other networks allowed to reach the panel |
| `service.type` / `service.port` | `ClusterIP` / `80` | Panel Service |
| `minecraftRouter.enabled` | `false` | Deploy mc-router: Java servers get `<hostname>.<domain>` on port 25565 (set the domain in the admin settings, wildcard DNS record, DNS only) |
| `minecraftRouter.service.type` / `.port` | `LoadBalancer` / `25565` | How the router is exposed |
| `secureCookies` | `true` | Send cookies over HTTPS only. Set `false` only to test over plain HTTP |
| `resources` | 50m / 64Mi → 2 CPU / 1Gi | Panel resources |
| `extraEnv`, `podAnnotations`, `tolerations` | empty | Extra pod settings |
