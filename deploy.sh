#!/bin/sh
# Build the image from source, import it into k3s and (re)deploy KubePilot with its Helm chart.
# For a normal install, use the published chart instead (see README).
set -e
cd "$(dirname "$0")"
export KUBECONFIG=${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}
[ -f deploy/config.env ] || { echo "Missing deploy/config.env (copy deploy/config.env.example)"; exit 1; }
. deploy/config.env
: "${KUBEPILOT_HOST:?}" "${KUBEPILOT_NODE:=}" "${KUBEPILOT_ISSUER:=}" "${KUBEPILOT_INGRESS_CLASS:=}" "${KUBEPILOT_CLOUDFLARE_ONLY:=false}" "${KUBEPILOT_MINECRAFT_ROUTER:=false}" "${KUBEPILOT_SFTP_PORT:=2022}"
: "${KUBEPILOT_SHIELD:=false}"
: "${KUBEPILOT_SMTP_HOST:=}" "${KUBEPILOT_SMTP_PORT:=0}" "${KUBEPILOT_SMTP_TLS:=starttls}" "${KUBEPILOT_SMTP_USERNAME:=}" "${KUBEPILOT_MAIL_FROM:=}"
# Emails: the SMTP password stays in the Secret kubepilot-smtp (key "password"), created once by hand:
#   kubectl -n kubepilot-system create secret generic kubepilot-smtp --from-file=password=<file>
set -- --set email.smtp.host="$KUBEPILOT_SMTP_HOST"
if [ -n "$KUBEPILOT_SMTP_HOST" ]; then
  set -- "$@" --set email.smtp.port="$KUBEPILOT_SMTP_PORT" --set email.smtp.tls="$KUBEPILOT_SMTP_TLS" \
    --set email.smtp.username="$KUBEPILOT_SMTP_USERNAME" --set-string email.from="$KUBEPILOT_MAIL_FROM" --set email.smtp.existingSecret=kubepilot-smtp
fi
VERSION=$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
TAG=$(date +%Y%m%d%H%M%S)
docker build -q --build-arg VERSION="$VERSION" -t kubepilot:$TAG . >/dev/null
docker save kubepilot:$TAG | k3s ctr -n k8s.io images import - >/dev/null
helm upgrade --install kubepilot charts/kubepilot --namespace kubepilot-system --create-namespace \
  --set image.repository=docker.io/library/kubepilot --set image.tag="$TAG" --set image.pullPolicy=Never \
  --set ingress.host="$KUBEPILOT_HOST" --set ingress.clusterIssuer="$KUBEPILOT_ISSUER" \
  --set ingress.className="$KUBEPILOT_INGRESS_CLASS" --set storage.node="$KUBEPILOT_NODE" \
  --set persistence.storageClass=local-path --set ingress.cloudflareOnly="$KUBEPILOT_CLOUDFLARE_ONLY" --set minecraftRouter.enabled="$KUBEPILOT_MINECRAFT_ROUTER" \
  --set sftp.port="$KUBEPILOT_SFTP_PORT" --set shield.enabled="$KUBEPILOT_SHIELD" "$@" \
  --wait --timeout 5m >/dev/null
echo "Deployed: KubePilot $VERSION (kubepilot:$TAG) → https://$KUBEPILOT_HOST"
