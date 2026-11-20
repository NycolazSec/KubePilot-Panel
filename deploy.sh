#!/bin/sh
# Build the image from source, import it into k3s and (re)deploy KubePilot with its Helm chart.
# For a normal install, use the published chart instead (see README).
set -e
cd "$(dirname "$0")"
export KUBECONFIG=${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}
[ -f deploy/config.env ] || { echo "Missing deploy/config.env (copy deploy/config.env.example)"; exit 1; }
. deploy/config.env
: "${KUBEPILOT_HOST:?}" "${KUBEPILOT_NODE:=}" "${KUBEPILOT_ISSUER:=}" "${KUBEPILOT_INGRESS_CLASS:=}" "${KUBEPILOT_CLOUDFLARE_ONLY:=false}" "${KUBEPILOT_MINECRAFT_ROUTER:=false}"
VERSION=$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
TAG=$(date +%Y%m%d%H%M%S)
docker build -q --build-arg VERSION="$VERSION" -t kubepilot:$TAG . >/dev/null
docker save kubepilot:$TAG | k3s ctr -n k8s.io images import - >/dev/null
helm upgrade --install kubepilot charts/kubepilot --namespace kubepilot-system --create-namespace \
  --set image.repository=docker.io/library/kubepilot --set image.tag="$TAG" --set image.pullPolicy=Never \
  --set ingress.host="$KUBEPILOT_HOST" --set ingress.clusterIssuer="$KUBEPILOT_ISSUER" \
  --set ingress.className="$KUBEPILOT_INGRESS_CLASS" --set storage.node="$KUBEPILOT_NODE" \
  --set persistence.storageClass=local-path --set ingress.cloudflareOnly="$KUBEPILOT_CLOUDFLARE_ONLY" --set minecraftRouter.enabled="$KUBEPILOT_MINECRAFT_ROUTER" \
  --wait --timeout 5m >/dev/null
echo "Deployed: KubePilot $VERSION (kubepilot:$TAG) → https://$KUBEPILOT_HOST"
