#!/bin/sh
# Build, import the image into k3s and (re)deploy KubePilot.
set -e
cd "$(dirname "$0")"
export KUBECONFIG=${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}
[ -f deploy/config.env ] || { echo "Missing deploy/config.env (copy deploy/config.env.example)"; exit 1; }
. deploy/config.env
: "${KUBEPILOT_HOST:?}" "${KUBEPILOT_NODE:?}" "${KUBEPILOT_ISSUER:=letsencrypt-prod}"
./build.sh
TAG=$(date +%Y%m%d%H%M%S)
cp kubepilot deploy/kubepilot
docker build -q -t kubepilot:$TAG -t kubepilot:latest deploy >/dev/null
rm deploy/kubepilot
docker save kubepilot:$TAG | k3s ctr -n k8s.io images import - >/dev/null
sed -e "s#image: kubepilot:latest#image: docker.io/library/kubepilot:$TAG#" \
    -e "s#__HOST__#$KUBEPILOT_HOST#g" -e "s#__NODE__#$KUBEPILOT_NODE#g" -e "s#__ISSUER__#$KUBEPILOT_ISSUER#g" \
    deploy/k8s.yaml | kubectl apply -f - >/dev/null
kubectl -n kubepilot-system rollout status deploy/kubepilot --timeout=120s
echo "Deployed: kubepilot:$TAG → https://$KUBEPILOT_HOST"
