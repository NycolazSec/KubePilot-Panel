#!/bin/sh
# Build, import the image into k3s and (re)deploy KubePilot.
set -e
cd "$(dirname "$0")"
export KUBECONFIG=${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}
[ -f deploy/config.env ] || { echo "Missing deploy/config.env (copy deploy/config.env.example)"; exit 1; }
. deploy/config.env
: "${KUBEPILOT_HOST:?}" "${KUBEPILOT_NODE:?}" "${KUBEPILOT_ISSUER:=letsencrypt-prod}" "${KUBEPILOT_OPS_NAMESPACES:=}"
./build.sh
TAG=$(date +%Y%m%d%H%M%S)
cp kubepilot deploy/kubepilot
docker build -q -t kubepilot:$TAG -t kubepilot:latest deploy >/dev/null
rm deploy/kubepilot
docker save kubepilot:$TAG | k3s ctr -n k8s.io images import - >/dev/null
{
  sed -e "s#image: kubepilot:latest#image: docker.io/library/kubepilot:$TAG#" \
      -e "s#__HOST__#$KUBEPILOT_HOST#g" -e "s#__NODE__#$KUBEPILOT_NODE#g" -e "s#__ISSUER__#$KUBEPILOT_ISSUER#g" \
      deploy/k8s.yaml
  # actions autorisées (supprimer un pod / répliques) uniquement dans ces namespaces
  for ns in $KUBEPILOT_OPS_NAMESPACES; do
    printf -- '---\napiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nmetadata:\n  name: kubepilot-ops\n  namespace: %s\n  labels: { kubepilot.io/ops: "true" }\nroleRef: { apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: kubepilot-ops }\nsubjects: [{ kind: ServiceAccount, name: kubepilot, namespace: kubepilot-system }]\n' "$ns"
  done
} | kubectl apply -f - >/dev/null
# retire les autorisations des namespaces sortis de la liste
for rb in $(kubectl get rolebinding -A -l kubepilot.io/ops=true -o jsonpath='{range .items[*]}{.metadata.namespace}{"\n"}{end}'); do
  case " $KUBEPILOT_OPS_NAMESPACES " in *" $rb "*) ;; *) kubectl -n "$rb" delete rolebinding kubepilot-ops >/dev/null ;; esac
done
kubectl -n kubepilot-system rollout status deploy/kubepilot --timeout=120s
echo "Deployed: kubepilot:$TAG → https://$KUBEPILOT_HOST"
