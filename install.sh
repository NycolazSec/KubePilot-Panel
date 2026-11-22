#!/bin/sh
# KubePilot installer: https://github.com/NycolazSec/KubePilot-Panel
#
#   curl -sfL https://raw.githubusercontent.com/NycolazSec/KubePilot-Panel/main/install.sh | sudo sh
#
# On a server without Kubernetes it installs k3s (a light Kubernetes made for single servers); with a cluster
# already configured for kubectl it uses that one. Then it installs Helm, cert-manager with a Let's Encrypt
# issuer, KubePilot, and creates the first administrator. It asks its questions first (every answer can be given
# in advance, see --help), then works on its own. Run it again to update KubePilot.
set -eu

HELM_VERSION=v4.3.1
HELM_SHA256_AMD64=0403a88ae20913d11b10a2467b50087721fea7581736da6b9d3992f5357fe54b
HELM_SHA256_ARM64=8cf8f66ecbc252ac8f7f06cca68aba88508ddfde3a6a34feb1678ad1694b4f88
CERT_MANAGER_VERSION=v1.21.3
LOCAL_PATH_VERSION=v0.0.31

DOMAIN=${KUBEPILOT_DOMAIN:-}
EMAIL=${KUBEPILOT_EMAIL:-}
ADMIN_USER=${KUBEPILOT_ADMIN_USER:-}
MC_ROUTER=${KUBEPILOT_MINECRAFT_ROUTER:-}
SHIELD=${KUBEPILOT_SHIELD:-}
VERSION=${KUBEPILOT_VERSION:-}
CHART=${KUBEPILOT_CHART:-oci://ghcr.io/nycolazsec/charts/kubepilot}
NAMESPACE=${KUBEPILOT_NAMESPACE:-kubepilot-system}
ISSUER=${KUBEPILOT_ISSUER:-letsencrypt-prod}
ACME_SERVER=${KUBEPILOT_ACME_SERVER:-https://acme-v02.api.letsencrypt.org/directory} # staging: https://acme-staging-v02.api.letsencrypt.org/directory
HELM_EXTRA=${KUBEPILOT_HELM_ARGS:-} # extra helm arguments (e.g. a local image to test the installer)
YES=${KUBEPILOT_YES:-false}
CERT_WAIT=${KUBEPILOT_CERT_WAIT:-180}
LOG=${KUBEPILOT_LOG:-/var/log/kubepilot-install.log}
RELEASE=kubepilot
INSTALL_URL=${KUBEPILOT_INSTALL_URL:-https://raw.githubusercontent.com/NycolazSec/KubePilot-Panel/main/install.sh}
MODE=install # install (or update), add-node (main machine: invitation for a new one), join (new machine)
SERVER=${KUBEPILOT_SERVER:-}         # join: https://<main machine>:6443
TOKEN=${KUBEPILOT_TOKEN:-}           # join: the invitation printed by add-node
NODE_NAME=${KUBEPILOT_NODE_NAME:-}   # join: name of the new machine in the cluster (default: its hostname)
SERVER_ADDRESS=${KUBEPILOT_SERVER_ADDRESS:-} # add-node: address the new machine reaches this one at (default: its IP)
ALLOW_FROM=${KUBEPILOT_ALLOW_FROM:-}         # add-node: IP of the new machine (ufw opens the cluster's ports for it only)
K3S_VERSION=${KUBEPILOT_K3S_VERSION:-}       # join: the k3s version of the main machine (a node can't be newer)

# ---------- Output, in French or English

case ${KUBEPILOT_LANG:-${LC_ALL:-${LC_MESSAGES:-${LANG:-}}}} in fr*) L=fr ;; *) L=en ;; esac
for a in "$@"; do case $a in --lang=fr) L=fr ;; --lang=en) L=en ;; esac; done

if [ -t 1 ]; then
	B=$(printf '\033[1m') D=$(printf '\033[2m') G=$(printf '\033[32m') Y=$(printf '\033[33m') R=$(printf '\033[31m') P=$(printf '\033[35m') N=$(printf '\033[0m')
else
	B='' D='' G='' Y='' R='' P='' N=''
fi
tr2() { if [ "$L" = fr ]; then printf '%s' "$2"; else printf '%s' "$1"; fi; }
say() { printf '  %s\n' "$(tr2 "$1" "$2")"; }
step() { printf '\n%s==>%s %s%s%s\n' "$P" "$N" "$B" "$(tr2 "$1" "$2")" "$N"; }
ok() { printf '  %s✓%s %s\n' "$G" "$N" "$(tr2 "$1" "$2")"; }
warn() { printf '  %s!%s %s\n' "$Y" "$N" "$(tr2 "$1" "$2")" >&2; }
die() {
	printf '\n%s✗ %s%s\n' "$R" "$(tr2 "$1" "$2")" "$N" >&2
	[ -s "$LOG" ] && printf '  %s\n' "$(tr2 "Details: $LOG" "Détails : $LOG")" >&2
	exit 1
}
stop() { # the user said no: nothing to look at in the log
	printf '\n  %s\n' "$(tr2 "$1" "$2")" >&2
	exit 1
}
yn() { if [ "$1" = true ]; then tr2 yes oui; else tr2 no non; fi; }
run() { "$@" >>"$LOG" 2>&1; } # long commands write to the log, not the screen

usage() {
	if [ "$L" = fr ]; then
		cat <<'EOF'
Installateur de KubePilot

  curl -sfL https://raw.githubusercontent.com/NycolazSec/KubePilot-Panel/main/install.sh | sudo sh -s -- [options]

  --domain panel.example.com   adresse du panel (un enregistrement DNS doit pointer vers ce serveur)
  --email toi@example.com      compte Let's Encrypt et email de l'administrateur
  --admin-user admin           nom de l'administrateur
  --minecraft-router           serveurs Minecraft à <nom>.mc.<domaine> sur le port 25565
  --shield / --no-shield       pare-feu et anti-DDoS sur les ports de jeu (activé par défaut)
  --version 0.3.0              version de KubePilot (par défaut : la dernière)
  --yes                        aucune question : valeurs par défaut pour le reste
  --lang fr|en

Chaque option existe aussi en variable d'environnement : KUBEPILOT_DOMAIN, KUBEPILOT_EMAIL,
KUBEPILOT_ADMIN_USER, KUBEPILOT_MINECRAFT_ROUTER=true, KUBEPILOT_SHIELD=false, KUBEPILOT_VERSION, KUBEPILOT_YES=true.
Relancé sur un serveur où KubePilot est installé, le script le met à jour.

Ajouter une machine au cluster :
  … | sudo sh -s -- add-node     sur la machine principale : affiche la commande pour la nouvelle machine
  … | sudo sh -s -- add-node --allow-from <IP de la nouvelle machine>   (ufw : ports du cluster pour elle seule)
  … | sudo sh -s -- join --server https://<machine principale>:6443 --token <invitation> [--k3s-version v…] [--node-name nom]
                                 sur la nouvelle machine : installe k3s et rejoint le cluster
EOF
	else
		cat <<'EOF'
KubePilot installer

  curl -sfL https://raw.githubusercontent.com/NycolazSec/KubePilot-Panel/main/install.sh | sudo sh -s -- [options]

  --domain panel.example.com   address of the panel (a DNS record must point at this server)
  --email you@example.com      Let's Encrypt account and the administrator's email
  --admin-user admin           administrator's username
  --minecraft-router           Minecraft servers at <name>.mc.<domain> on port 25565
  --shield / --no-shield       firewall and anti-DDoS on the game ports (on by default)
  --version 0.3.0              KubePilot version (default: the latest)
  --yes                        no question: defaults for everything not given
  --lang fr|en

Every option is also an environment variable: KUBEPILOT_DOMAIN, KUBEPILOT_EMAIL, KUBEPILOT_ADMIN_USER,
KUBEPILOT_MINECRAFT_ROUTER=true, KUBEPILOT_SHIELD=false, KUBEPILOT_VERSION, KUBEPILOT_YES=true.
Run again on a server where KubePilot is installed, the script updates it.

Add a machine to the cluster:
  … | sudo sh -s -- add-node     on the main machine: prints the command for the new machine
  … | sudo sh -s -- add-node --allow-from <IP of the new machine>   (ufw: the cluster's ports for it only)
  … | sudo sh -s -- join --server https://<main machine>:6443 --token <invitation> [--k3s-version v…] [--node-name name]
                                 on the new machine: installs k3s and joins the cluster
EOF
	fi
}

while [ $# -gt 0 ]; do
	case $1 in
	--domain=*) DOMAIN=${1#*=} ;;
	--domain) DOMAIN=${2:-} && shift ;;
	--email=*) EMAIL=${1#*=} ;;
	--email) EMAIL=${2:-} && shift ;;
	--admin-user=*) ADMIN_USER=${1#*=} ;;
	--admin-user) ADMIN_USER=${2:-} && shift ;;
	--version=*) VERSION=${1#*=} ;;
	--version) VERSION=${2:-} && shift ;;
	--minecraft-router) MC_ROUTER=true ;;
	--no-minecraft-router) MC_ROUTER=false ;;
	--shield) SHIELD=true ;;
	--no-shield) SHIELD=false ;;
	-y | --yes) YES=true ;;
	--lang=*) L=${1#*=} ;;
	--lang) L=${2:-en} && shift ;;
	add-node) MODE="add-node" ;;
	join) MODE="join" ;;
	--server=*) SERVER=${1#*=} ;;
	--server) SERVER=${2:-} && shift ;;
	--token=*) TOKEN=${1#*=} ;;
	--token) TOKEN=${2:-} && shift ;;
	--node-name=*) NODE_NAME=${1#*=} ;;
	--node-name) NODE_NAME=${2:-} && shift ;;
	--allow-from=*) ALLOW_FROM=${1#*=} ;;
	--allow-from) ALLOW_FROM=${2:-} && shift ;;
	--k3s-version=*) K3S_VERSION=${1#*=} ;;
	--k3s-version) K3S_VERSION=${2:-} && shift ;;
	-h | --help) usage && exit 0 ;;
	*) die "Unknown option: $1 (see --help)" "Option inconnue : $1 (voir --help)" ;;
	esac
	shift
done

# Questions are read from the terminal, also when the script itself comes from a pipe (curl | sh).
INTERACTIVE=false
if [ "$YES" != true ] && (: </dev/tty) 2>/dev/null; then INTERACTIVE=true; fi

ask() { # ask "question" "question en français" default → the answer
	if [ "$INTERACTIVE" = true ]; then
		if [ -n "$3" ]; then
			printf '  %s %s[%s]%s%s ' "$(tr2 "$1" "$2")" "$D" "$3" "$N" "$(tr2 : ' :')" >/dev/tty
		else
			printf '  %s%s ' "$(tr2 "$1" "$2")" "$(tr2 : ' :')" >/dev/tty
		fi
		a=
		IFS= read -r a </dev/tty || a=
		printf '%s' "${a:-$3}"
	else
		printf '%s' "$3"
	fi
}
confirm() { # confirm "question" "question en français" y|n → success when yes
	if [ "$INTERACTIVE" != true ]; then [ "$3" = y ] && return 0 || return 1; fi
	if [ "$3" = y ]; then h=$(tr2 Y/n O/n); else h=$(tr2 y/N o/N); fi
	printf '  %s %s[%s]%s ' "$(tr2 "$1" "$2")" "$D" "$h" "$N" >/dev/tty
	a=
	IFS= read -r a </dev/tty || a=
	case ${a:-$3} in y | Y | yes | Yes | o | O | oui | Oui) return 0 ;; *) return 1 ;; esac
}

port_busy() { ss -ltnH 2>/dev/null | awk '{print $4}' | grep -Eq "[:.]$1\$"; }
is_private() { case $1 in 10.* | 192.168.* | 127.* | 100.6[4-9].* | 100.[7-9][0-9].* | 100.1[01][0-9].* | 100.12[0-7].* | 172.1[6-9].* | 172.2[0-9].* | 172.3[01].* | "") return 0 ;; *) return 1 ;; esac; }

# ---------- 1. This server

printf '\n%sKubePilot%s %s\n' "$B" "$N" "$(tr2 "· game server panel for Kubernetes" "· panel de serveurs de jeu pour Kubernetes")"
step "Checking this server" "Vérification du serveur"
[ "$(uname -s)" = Linux ] || die "KubePilot installs on Linux." "KubePilot s'installe sur Linux."
[ "$(id -u)" = 0 ] || die "Run it as root: curl -sfL … | sudo sh" "Lance-le en root : curl -sfL … | sudo sh"
case $(uname -m) in
x86_64 | amd64) ARCH=amd64 HELM_SHA256=$HELM_SHA256_AMD64 ;;
aarch64 | arm64) ARCH=arm64 HELM_SHA256=$HELM_SHA256_ARM64 ;;
*) die "Unsupported processor: $(uname -m) (amd64 or arm64)" "Processeur non pris en charge : $(uname -m) (amd64 ou arm64)" ;;
esac
printf '%s' "$ISSUER" | grep -Eq '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$' || die "Invalid issuer name: $ISSUER" "Nom d'émetteur invalide : $ISSUER"
for c in curl tar sha256sum awk; do
	command -v "$c" >/dev/null 2>&1 || die "$c is missing: install it first" "$c est absent : installe-le d'abord"
done
(umask 077 && : >>"$LOG") || die "Can't write $LOG" "Impossible d'écrire $LOG"
printf '\n=== %s\n' "$(date)" >>"$LOG"
MEM=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
ok "Linux $ARCH, $MEM MB RAM" "Linux $ARCH, $MEM Mo de RAM"
[ "$MEM" -ge 1800 ] || warn "Less than 2 GB of RAM: enough for the panel, short for game servers." "Moins de 2 Go de RAM : assez pour le panel, juste pour des serveurs de jeu."
IP=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") {print $(i + 1); exit}}' || true)

ufw_active() { command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q 'Status: active'; }
is_ipv4() { printf '%s' "$1" | grep -Eq '^([0-9]{1,3}\.){3}[0-9]{1,3}$'; }

# ---------- add-node: on the main machine, an invitation for a new machine

if [ "$MODE" = add-node ]; then
	step "Invitation for a new machine" "Invitation pour une nouvelle machine"
	{ command -v k3s >/dev/null 2>&1 && [ -f /etc/rancher/k3s/k3s.yaml ]; } ||
		die "Run add-node on the main machine of the cluster (the k3s server KubePilot was installed on)." \
			"Lance add-node sur la machine principale du cluster (le serveur k3s où KubePilot est installé)."
	KUBECONFIG=/etc/rancher/k3s/k3s.yaml
	export KUBECONFIG
	kubectl get nodes --request-timeout=10s >/dev/null 2>&1 || die "k3s doesn't answer (systemctl status k3s)." "k3s ne répond pas (systemctl status k3s)."
	ADDR=${SERVER_ADDRESS:-$IP}
	[ -n "$ADDR" ] || die "Can't find this machine's address: give it with KUBEPILOT_SERVER_ADDRESS=<ip>." \
		"Impossible de trouver l'adresse de cette machine : donne-la avec KUBEPILOT_SERVER_ADDRESS=<ip>."
	JOIN=$(k3s token create --ttl 24h --description "KubePilot: new machine" 2>>"$LOG") && [ -n "$JOIN" ] ||
		die "Can't create the invitation (k3s token create, see $LOG)." "Impossible de créer l'invitation (k3s token create, voir $LOG)."
	ok "Invitation created, valid for 24 hours" "Invitation créée, valable 24 heures"
	BACKEND=$(kubectl get nodes -o jsonpath='{.items[0].metadata.annotations.flannel\.alpha\.coreos\.com/backend-type}' 2>/dev/null || true)
	case $BACKEND in
	wireguard*) UDP=51820 ;; # flannel names it "wireguard"
	*)
		UDP=8472
		warn "On this cluster the traffic between machines isn't encrypted (flannel ${BACKEND:-vxlan}): only add machines of the same private network, or switch k3s to --flannel-backend=wireguard-native first." \
			"Sur ce cluster, le trafic entre les machines n'est pas chiffré (flannel ${BACKEND:-vxlan}) : n'ajoute que des machines du même réseau privé, ou passe d'abord k3s en --flannel-backend=wireguard-native."
		;;
	esac
	if ufw_active; then
		[ -n "$ALLOW_FROM" ] || ALLOW_FROM=$(ask "ufw is active: public IP of the new machine (the cluster's ports open for it only)" \
			"ufw est actif : IP publique de la nouvelle machine (les ports du cluster ne s'ouvrent que pour elle)" "")
		if [ -n "$ALLOW_FROM" ]; then
			is_ipv4 "$ALLOW_FROM" || die "Invalid IP: $ALLOW_FROM" "IP invalide : $ALLOW_FROM"
			for p in 6443/tcp 10250/tcp "$UDP/udp"; do run ufw allow from "$ALLOW_FROM" to any port "${p%/*}" proto "${p#*/}"; done
			ok "Ports 6443/tcp, 10250/tcp and $UDP/udp opened for $ALLOW_FROM" "Ports 6443/tcp, 10250/tcp et $UDP/udp ouverts pour $ALLOW_FROM"
		else
			warn "ufw is active: open 6443/tcp, 10250/tcp and $UDP/udp for the new machine (ufw allow from <its IP> to any port <port> proto <tcp|udp>)." \
				"ufw est actif : ouvre 6443/tcp, 10250/tcp et $UDP/udp pour la nouvelle machine (ufw allow from <son IP> to any port <port> proto <tcp|udp>)."
		fi
	fi
	K3SV=$(k3s --version 2>/dev/null | sed -n 's/^k3s version \([^ ]*\).*/\1/p')
	printf '\n  %s\n\n    %scurl -sfL %s | sudo sh -s -- join --server https://%s:6443 --token %s%s%s\n\n' \
		"$(tr2 "On the new machine (a fresh Linux server), run:" "Sur la nouvelle machine (un serveur Linux neuf), lance :")" "$B" "$INSTALL_URL" "$ADDR" "$JOIN" "${K3SV:+ --k3s-version $K3SV}" "$N"
	say "It must reach this machine on 6443/tcp; the machines talk to each other on 10250/tcp and $UDP/udp." \
		"Elle doit joindre cette machine sur 6443/tcp ; les machines se parlent sur 10250/tcp et $UDP/udp."
	say "Keep the invitation secret: it lets a machine join the cluster." "Garde l'invitation secrète : elle permet à une machine de rejoindre le cluster."
	printf '\n'
	exit 0
fi

# ---------- join: on a new machine, join the cluster of the main one

if [ "$MODE" = join ]; then
	step "Joining the cluster" "Connexion au cluster"
	{ [ -n "$SERVER" ] && [ -n "$TOKEN" ]; } || die "join needs --server and --token: run add-node on the main machine to get them." \
		"join a besoin de --server et --token : lance add-node sur la machine principale pour les obtenir."
	case $SERVER in https://?*) ;; *) die "--server: https://<main machine>:6443 expected" "--server : https://<machine principale>:6443 attendu" ;; esac
	SERVER=${SERVER%/}
	if command -v k3s >/dev/null 2>&1 || [ -e /etc/systemd/system/k3s.service ] || [ -e /etc/systemd/system/k3s-agent.service ] || [ -d /var/lib/rancher/k3s/data ]; then
		die "k3s is already installed on this machine: join a fresh one, or uninstall k3s first (k3s-uninstall.sh or k3s-agent-uninstall.sh)." \
			"k3s est déjà installé sur cette machine : utilise une machine neuve, ou désinstalle d'abord k3s (k3s-uninstall.sh ou k3s-agent-uninstall.sh)."
	fi
	if [ -n "$K3S_VERSION" ]; then # the version of the main machine: a node newer than the server isn't supported
		printf '%s' "$K3S_VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+\+k3s[0-9]+$' || die "Invalid k3s version: $K3S_VERSION" "Version de k3s invalide : $K3S_VERSION"
		INSTALL_K3S_VERSION=$K3S_VERSION
		export INSTALL_K3S_VERSION
	fi
	if [ -n "$NODE_NAME" ]; then
		printf '%s' "$NODE_NAME" | grep -Eq '^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$' || die "Invalid machine name: $NODE_NAME (lowercase letters, digits, - and .)" \
			"Nom de machine invalide : $NODE_NAME (minuscules, chiffres, - et .)"
	fi
	curl -sk --max-time 10 "$SERVER/cacerts" >/dev/null 2>&1 ||
		die "Can't reach $SERVER: open port 6443/tcp on the main machine (its firewall, and the provider's) and check the address." \
			"Impossible de joindre $SERVER : ouvre le port 6443/tcp sur la machine principale (son pare-feu, et celui de l'hébergeur) et vérifie l'adresse."
	ok "Main machine reachable: $SERVER" "Machine principale joignable : $SERVER"
	if port_busy 10250; then
		die "Port 10250 is already used by another program: the cluster needs it on this machine." "Le port 10250 est déjà utilisé par un autre programme : le cluster en a besoin sur cette machine."
	fi
	say "Installing k3s (agent)…" "Installation de k3s (agent)…"
	curl -sfL https://get.k3s.io -o /tmp/kubepilot-k3s-install.sh || die "Can't download k3s (https://get.k3s.io)." "Impossible de télécharger k3s (https://get.k3s.io)."
	K3S_URL=$SERVER
	K3S_TOKEN=$TOKEN
	export K3S_URL K3S_TOKEN
	set -- agent
	[ -z "$NODE_NAME" ] || set -- "$@" --node-name "$NODE_NAME"
	run sh /tmp/kubepilot-k3s-install.sh "$@" || die "The k3s installation failed (see $LOG)." "L'installation de k3s a échoué (voir $LOG)."
	rm -f /tmp/kubepilot-k3s-install.sh
	i=0
	until [ -f /var/lib/rancher/k3s/agent/client-kubelet.crt ] && systemctl is-active --quiet k3s-agent; do
		i=$((i + 1))
		[ $i -lt 60 ] || die "The machine doesn't join the cluster (journalctl -u k3s-agent): expired invitation, or the same name as another machine (use --node-name)?" \
			"La machine ne rejoint pas le cluster (journalctl -u k3s-agent) : invitation expirée, ou le même nom qu'une autre machine (utilise --node-name) ?"
		sleep 3
	done
	ok "Joined the cluster as ${NODE_NAME:-$(hostname)}" "Cluster rejoint sous le nom ${NODE_NAME:-$(hostname)}"
	if ufw_active; then
		# the cluster's ports for the main machine only, the game ports for everyone
		MAIN=${SERVER#https://}
		MAIN=${MAIN%%/*}
		MAIN=${MAIN%:*}
		is_ipv4 "$MAIN" || MAIN=$(getent ahostsv4 "$MAIN" 2>/dev/null | awk 'NR == 1 {print $1}')
		if confirm "ufw is active: open the cluster's ports for ${MAIN:-the main machine} and the game ports 30100-30299 for everyone?" \
			"ufw est actif : ouvrir les ports du cluster pour ${MAIN:-la machine principale} et les ports de jeu 30100-30299 pour tout le monde ?" y; then
			for p in 10250/tcp 51820/udp 8472/udp; do
				if [ -n "$MAIN" ]; then run ufw allow from "$MAIN" to any port "${p%/*}" proto "${p#*/}"; else run ufw allow "$p"; fi
			done
			run ufw allow 30100:30299/tcp
			run ufw allow 30100:30299/udp
			ok "Ports opened" "Ports ouverts"
		fi
	fi
	printf '\n%s%s%s\n\n' "$G$B" "$(tr2 "This machine is part of the cluster!" "Cette machine fait partie du cluster !")" "$N"
	say "In the panel, Admin → Machines: it shows up within a minute with its KubePilot agent, and new servers can go there." \
		"Dans le panel, Admin → Machines : elle apparaît dans la minute avec son agent KubePilot, et les nouveaux serveurs peuvent y aller."
	say "Players reach the servers of this machine at ${IP:-its address}." "Les joueurs rejoignent les serveurs de cette machine à ${IP:-son adresse}."
	printf '\n'
	exit 0
fi

# ---------- 2. Kubernetes

if [ -z "${KUBECONFIG:-}" ]; then
	if [ -f /etc/rancher/k3s/k3s.yaml ]; then
		KUBECONFIG=/etc/rancher/k3s/k3s.yaml
	elif [ -f "${HOME:-/root}/.kube/config" ]; then
		KUBECONFIG=${HOME:-/root}/.kube/config
	fi
	[ -z "${KUBECONFIG:-}" ] || export KUBECONFIG
fi
CLUSTER=false
if command -v kubectl >/dev/null 2>&1 && kubectl get nodes --request-timeout=10s >/dev/null 2>&1; then CLUSTER=true; fi

UPDATE=false
if [ "$CLUSTER" = true ] && command -v helm >/dev/null 2>&1 && helm status "$RELEASE" -n "$NAMESPACE" >/dev/null 2>&1; then UPDATE=true; fi

if [ "$UPDATE" = true ]; then
	# ---------- Update: same settings, new version (plus the options given on the command line)
	step "Updating KubePilot" "Mise à jour de KubePilot"
	# the release's own settings, on top of the new chart's defaults (new options get their default value)
	if helm upgrade --help 2>/dev/null | grep -q -- '--reset-then-reuse-values'; then set -- --reset-then-reuse-values; else set -- --reuse-values; fi
	[ -z "$VERSION" ] || set -- "$@" --version "$VERSION"
	[ -z "$MC_ROUTER" ] || set -- "$@" --set minecraftRouter.enabled="$MC_ROUTER"
	[ -z "$SHIELD" ] || set -- "$@" --set shield.enabled="$SHIELD"
	[ -z "$DOMAIN" ] || set -- "$@" --set ingress.host="$DOMAIN"
	BEFORE=$(helm list -n "$NAMESPACE" -f "^$RELEASE\$" -o json | sed -n 's/.*"app_version":"\([^"]*\)".*/\1/p')
	# shellcheck disable=SC2086 # HELM_EXTRA holds several arguments
	run helm upgrade "$RELEASE" "$CHART" -n "$NAMESPACE" "$@" $HELM_EXTRA --wait --timeout 10m ||
		die "The update failed." "La mise à jour a échoué."
	AFTER=$(helm list -n "$NAMESPACE" -f "^$RELEASE\$" -o json | sed -n 's/.*"app_version":"\([^"]*\)".*/\1/p')
	HOST=$(kubectl -n "$NAMESPACE" get ingress -o jsonpath='{.items[0].spec.rules[0].host}' 2>/dev/null || true)
	ok "KubePilot $BEFORE → $AFTER" "KubePilot $BEFORE → $AFTER"
	printf '\n  %s%s%s https://%s\n\n' "$B" "$(tr2 "Panel:" "Panel :")" "$N" "$HOST"
	exit 0
fi

step "Kubernetes" "Kubernetes"
if [ "$CLUSTER" = true ]; then
	ok "Cluster found: $(kubectl get nodes --no-headers | wc -l | tr -d ' ') node(s), $(kubectl version 2>/dev/null | sed -n 's/^Server Version: //p')" \
		"Cluster trouvé : $(kubectl get nodes --no-headers | wc -l | tr -d ' ') nœud(s), $(kubectl version 2>/dev/null | sed -n 's/^Server Version: //p')"
else
	say "No Kubernetes cluster here: KubePilot can install k3s, a light Kubernetes made for single servers." \
		"Aucun cluster Kubernetes ici : KubePilot peut installer k3s, un Kubernetes léger fait pour un seul serveur."
	confirm "Install k3s on this server?" "Installer k3s sur ce serveur ?" y ||
		stop "KubePilot needs a Kubernetes cluster. Configure kubectl for yours, or let the script install k3s." \
			"KubePilot a besoin d'un cluster Kubernetes. Configure kubectl pour le tien, ou laisse le script installer k3s."
	for p in 80 443 6443; do
		if port_busy "$p"; then
			warn "Port $p is already used by another program: k3s needs it (Traefik serves the panel on 80 and 443)." \
				"Le port $p est déjà utilisé par un autre programme : k3s en a besoin (Traefik sert le panel sur 80 et 443)."
			confirm "Continue anyway?" "Continuer quand même ?" n || stop "Stopped: free port $p, then run the script again." "Arrêt : libère le port $p, puis relance le script."
		fi
	done
fi

# ---------- 3. Questions (all of them now, then the script works on its own)

step "Your panel" "Ton panel"
DEFAULT_DOMAIN=
if ! is_private "$IP"; then DEFAULT_DOMAIN=$(printf '%s' "$IP" | tr . -).sslip.io; fi
[ -n "$DOMAIN" ] || {
	[ "$INTERACTIVE" != true ] || [ -z "$DEFAULT_DOMAIN" ] || say "No domain? $DEFAULT_DOMAIN already points at this server (sslip.io)." \
		"Pas de domaine ? $DEFAULT_DOMAIN pointe déjà vers ce serveur (sslip.io)."
	DOMAIN=$(ask "Address of the panel (a DNS record must point at this server)" "Adresse du panel (un enregistrement DNS doit pointer vers ce serveur)" "$DEFAULT_DOMAIN")
}
DOMAIN=$(printf '%s' "$DOMAIN" | tr '[:upper:]' '[:lower:]')
printf '%s' "$DOMAIN" | grep -Eq '^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$' ||
	die "Invalid address: \"$DOMAIN\" (expected panel.example.com)" "Adresse invalide : « $DOMAIN » (attendu : panel.example.com)"
while ! printf '%s' "$EMAIL" | grep -Eq '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$'; do
	[ "$INTERACTIVE" = true ] || die "An email is needed (--email): Let's Encrypt sends certificate warnings there." \
		"Il faut un email (--email) : Let's Encrypt y envoie ses avertissements de certificat."
	EMAIL=$(ask "Your email (Let's Encrypt and the administrator account)" "Ton email (Let's Encrypt et compte administrateur)" "")
done
[ -n "$ADMIN_USER" ] || ADMIN_USER=$(ask "Administrator's username" "Nom de l'administrateur" "admin")
printf '%s' "$ADMIN_USER" | grep -Eq '^[A-Za-z0-9_.-]{3,32}$' ||
	die "Invalid username (3-32 characters: letters, digits, _ . -)" "Nom invalide (3 à 32 caractères : lettres, chiffres, _ . -)"
if [ -z "$MC_ROUTER" ]; then
	if confirm "Minecraft servers at <name>.mc.$DOMAIN on port 25565? (needs a DNS record *.mc.$DOMAIN)" \
		"Serveurs Minecraft à <nom>.mc.$DOMAIN sur le port 25565 ? (il faut un enregistrement DNS *.mc.$DOMAIN)" n; then MC_ROUTER=true; else MC_ROUTER=false; fi
fi
if [ -z "$SHIELD" ]; then
	if confirm "Firewall and anti-DDoS on the game ports?" "Pare-feu et anti-DDoS sur les ports de jeu ?" y; then SHIELD=true; else SHIELD=false; fi
fi

RESOLVED=$(getent ahostsv4 "$DOMAIN" 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ' || true)
if [ -n "$IP" ] && ! printf ' %s' "$RESOLVED" | grep -q " $IP "; then
	SHOWN=$RESOLVED
	[ -n "$SHOWN" ] || SHOWN=$(tr2 "nothing yet" "rien pour l'instant")
	warn "$DOMAIN points to $SHOWN, not to this server ($IP). The HTTPS certificate will come once the DNS record is right." \
		"$DOMAIN pointe vers $SHOWN, pas vers ce serveur ($IP). Le certificat HTTPS arrivera quand l'enregistrement DNS sera bon."
fi

if [ "$INTERACTIVE" = true ]; then
	printf '\n'
	printf '  %-22s %s\n' "Panel" "https://$DOMAIN"
	printf '  %-22s %s\n' "$(tr2 Administrator Administrateur)" "$ADMIN_USER <$EMAIL>"
	printf '  %-22s %s\n' "$(tr2 "Minecraft router" "Routeur Minecraft")" "$(yn "$MC_ROUTER")"
	printf '  %-22s %s\n' "$(tr2 "Firewall / anti-DDoS" "Pare-feu / anti-DDoS")" "$(yn "$SHIELD")"
	[ "$CLUSTER" = true ] || printf '  %-22s %s\n' "Kubernetes" "$(tr2 "k3s, installed on this server" "k3s, installé sur ce serveur")"
	printf '\n'
	confirm "Install now? (a few minutes)" "Installer maintenant ? (quelques minutes)" y || stop "Nothing was changed." "Rien n'a été modifié."
fi

# ---------- 4. k3s

if [ "$CLUSTER" != true ]; then
	step "Installing k3s" "Installation de k3s"
	curl -sfL https://get.k3s.io -o /tmp/kubepilot-k3s-install.sh || die "Can't download k3s (https://get.k3s.io)." "Impossible de télécharger k3s (https://get.k3s.io)."
	# WireGuard between machines when the kernel has it: machines added later talk over an encrypted network
	set --
	if [ -e /sys/module/wireguard ] || modprobe wireguard >/dev/null 2>&1; then set -- --flannel-backend=wireguard-native; fi
	run sh /tmp/kubepilot-k3s-install.sh "$@" || die "The k3s installation failed." "L'installation de k3s a échoué."
	rm -f /tmp/kubepilot-k3s-install.sh
	KUBECONFIG=/etc/rancher/k3s/k3s.yaml
	export KUBECONFIG
	i=0
	until kubectl get nodes --no-headers 2>/dev/null | grep -q ' Ready'; do
		i=$((i + 1))
		[ $i -lt 60 ] || die "k3s doesn't start (journalctl -u k3s)." "k3s ne démarre pas (journalctl -u k3s)."
		sleep 3
	done
	ok "k3s $(k3s --version | sed -n 's/^k3s version \([^ ]*\).*/\1/p')" "k3s $(k3s --version | sed -n 's/^k3s version \([^ ]*\).*/\1/p')"
	say "Waiting for its ingress controller and storage…" "Attente de son contrôleur d'ingress et de son stockage…"
	i=0
	until kubectl get ingressclass traefik >/dev/null 2>&1 && kubectl get storageclass local-path >/dev/null 2>&1; do
		i=$((i + 1))
		[ $i -lt 100 ] || die "Traefik or local-path didn't appear (kubectl -n kube-system get pods)." "Traefik ou local-path n'est pas apparu (kubectl -n kube-system get pods)."
		sleep 3
	done
fi

# ---------- 5. What the cluster must provide

step "Cluster components" "Composants du cluster"
LP_PROVISIONER=$(kubectl get storageclass -o jsonpath='{range .items[*]}{.provisioner}{"\n"}{end}' | grep -x rancher.io/local-path || true)
if [ -z "$LP_PROVISIONER" ]; then
	warn "No local-path storage: KubePilot keeps game files on the nodes with it." "Pas de stockage local-path : KubePilot garde les fichiers des jeux sur les nœuds avec."
	confirm "Install local-path-provisioner $LOCAL_PATH_VERSION?" "Installer local-path-provisioner $LOCAL_PATH_VERSION ?" y ||
		die "KubePilot needs local-path storage." "KubePilot a besoin du stockage local-path."
	run kubectl apply -f "https://raw.githubusercontent.com/rancher/local-path-provisioner/$LOCAL_PATH_VERSION/deploy/local-path-storage.yaml" ||
		die "local-path-provisioner couldn't be installed." "local-path-provisioner n'a pas pu être installé."
fi
# where local-path creates the volumes (k3s: /var/lib/rancher/k3s/storage, upstream: /opt/local-path-provisioner)
LP_PATH=
i=0
while [ -z "$LP_PATH" ] && [ $i -lt 20 ]; do
	LP_PATH=$(kubectl get configmap -A --field-selector metadata.name=local-path-config -o jsonpath='{.items[0].data.config\.json}' 2>/dev/null |
		tr -d ' \n' | sed -n 's/.*"paths":\["\([^"]*\)".*/\1/p')
	[ -n "$LP_PATH" ] || sleep 3
	i=$((i + 1))
done
[ -n "$LP_PATH" ] || die "Can't read the local-path configuration (ConfigMap local-path-config)." "Impossible de lire la configuration local-path (ConfigMap local-path-config)."
ok "local-path storage ($LP_PATH)" "Stockage local-path ($LP_PATH)"

INGRESS_CLASS=$(kubectl get ingressclass -o jsonpath='{range .items[?(@.metadata.annotations.ingressclass\.kubernetes\.io/is-default-class=="true")]}{.metadata.name}{"\n"}{end}' | head -n 1)
[ -n "$INGRESS_CLASS" ] || INGRESS_CLASS=$(kubectl get ingressclass -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
[ -n "$INGRESS_CLASS" ] || die "No ingress controller: install one (k3s ships Traefik, otherwise ingress-nginx)." \
	"Pas de contrôleur d'ingress : installe-en un (k3s fournit Traefik, sinon ingress-nginx)."
ok "Ingress: $INGRESS_CLASS" "Ingress : $INGRESS_CLASS"
if kubectl get apiservice v1beta1.metrics.k8s.io >/dev/null 2>&1; then
	ok "metrics-server" "metrics-server"
else
	warn "No metrics-server: the panel won't show CPU and RAM usage." "Pas de metrics-server : le panel n'affichera pas l'usage CPU et RAM."
fi
NODES=$(kubectl get nodes --no-headers | wc -l | tr -d ' ')
STORAGE_NODE=
if [ "$NODES" -gt 1 ]; then
	STORAGE_NODE=$(kubectl get nodes -o name | sed 's|node/||' | grep -x "$(hostname)" || kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
	warn "$NODES nodes: game servers and the panel go on $STORAGE_NODE (their files stay on one node)." \
		"$NODES nœuds : les serveurs de jeu et le panel vont sur $STORAGE_NODE (leurs fichiers restent sur un nœud)."
fi

# ---------- 6. Helm

step "Helm" "Helm"
if command -v helm >/dev/null 2>&1 && helm version --short 2>/dev/null | grep -Eq '^v(3\.(1[4-9]|[2-9][0-9])|[4-9])\.'; then
	ok "Helm $(helm version --short)" "Helm $(helm version --short)"
else
	TMP=$(mktemp -d)
	curl -sfL -o "$TMP/helm.tgz" "https://get.helm.sh/helm-$HELM_VERSION-linux-$ARCH.tar.gz" || die "Can't download Helm." "Impossible de télécharger Helm."
	printf '%s  %s\n' "$HELM_SHA256" "$TMP/helm.tgz" | sha256sum -c - >/dev/null 2>&1 ||
		die "The Helm download doesn't match its checksum: stopped." "Le téléchargement de Helm ne correspond pas à son empreinte : arrêt."
	tar -xzf "$TMP/helm.tgz" -C "$TMP"
	install -m 0755 "$TMP/linux-$ARCH/helm" /usr/local/bin/helm
	rm -rf "$TMP"
	ok "Helm $HELM_VERSION installed (checksum verified)" "Helm $HELM_VERSION installé (empreinte vérifiée)"
fi

# ---------- 7. HTTPS: cert-manager and Let's Encrypt

step "HTTPS certificates" "Certificats HTTPS"
if kubectl get crd clusterissuers.cert-manager.io >/dev/null 2>&1; then
	ok "cert-manager already installed" "cert-manager déjà installé"
else
	say "Installing cert-manager $CERT_MANAGER_VERSION…" "Installation de cert-manager $CERT_MANAGER_VERSION…"
	run helm upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager --version "$CERT_MANAGER_VERSION" \
		-n cert-manager --create-namespace --set crds.enabled=true --wait --timeout 10m ||
		die "cert-manager couldn't be installed." "cert-manager n'a pas pu être installé."
	ok "cert-manager $CERT_MANAGER_VERSION" "cert-manager $CERT_MANAGER_VERSION"
fi
if kubectl get clusterissuer "$ISSUER" >/dev/null 2>&1; then
	ok "Certificate issuer $ISSUER already present" "Émetteur de certificats $ISSUER déjà présent"
else
	i=0
	# right after its installation, cert-manager's webhook may need a few seconds
	until kubectl apply -f - >>"$LOG" 2>&1 <<EOF; do
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: $ISSUER
spec:
  acme:
    server: $ACME_SERVER
    email: $EMAIL
    privateKeySecretRef:
      name: $ISSUER-account
    solvers:
      - http01:
          ingress:
            ingressClassName: $INGRESS_CLASS
EOF
		i=$((i + 1))
		[ $i -lt 20 ] || die "The Let's Encrypt issuer couldn't be created." "L'émetteur Let's Encrypt n'a pas pu être créé."
		sleep 5
	done
	ok "Let's Encrypt issuer $ISSUER ($EMAIL)" "Émetteur Let's Encrypt $ISSUER ($EMAIL)"
fi

# ---------- 8. KubePilot

step "Installing KubePilot" "Installation de KubePilot"
set -- --set ingress.host="$DOMAIN" --set ingress.clusterIssuer="$ISSUER" --set ingress.className="$INGRESS_CLASS" \
	--set storage.hostPath="$LP_PATH" --set storage.node="$STORAGE_NODE" \
	--set minecraftRouter.enabled="$MC_ROUTER" --set shield.enabled="$SHIELD"
[ -z "$VERSION" ] || set -- "$@" --version "$VERSION"
SFTP_PORT=2022
if port_busy 2022; then
	SFTP_PORT=2222
	set -- "$@" --set sftp.port=2222
	warn "Port 2022 is taken (Pterodactyl Wings?): SFTP will use port 2222." "Le port 2022 est pris (Pterodactyl Wings ?) : le SFTP utilisera le port 2222."
fi
# shellcheck disable=SC2086 # HELM_EXTRA holds several arguments
run helm upgrade --install "$RELEASE" "$CHART" -n "$NAMESPACE" --create-namespace "$@" $HELM_EXTRA --wait --timeout 10m ||
	die "KubePilot couldn't be installed." "KubePilot n'a pas pu être installé."
ok "KubePilot $(helm list -n "$NAMESPACE" -f "^$RELEASE\$" -o json | sed -n 's/.*"app_version":"\([^"]*\)".*/\1/p')" \
	"KubePilot $(helm list -n "$NAMESPACE" -f "^$RELEASE\$" -o json | sed -n 's/.*"app_version":"\([^"]*\)".*/\1/p')"

# ---------- 9. First administrator

step "Administrator account" "Compte administrateur"
PASS=
USERS=$(kubectl -n "$NAMESPACE" exec deploy/kubepilot -- kubepilot user:list 2>/dev/null || true)
if [ -n "$USERS" ]; then
	ok "The panel already has accounts: none created" "Le panel a déjà des comptes : aucun créé"
else
	PASS=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 20)
	# the password goes through stdin, never in a command line; older versions only take --password
	if ! printf '%s\n' "$PASS" | kubectl -n "$NAMESPACE" exec -i deploy/kubepilot -- \
		kubepilot user:create --username "$ADMIN_USER" --email "$EMAIL" --password-stdin --admin >>"$LOG" 2>&1; then
		OUT=$(kubectl -n "$NAMESPACE" exec deploy/kubepilot -- kubepilot user:create --username "$ADMIN_USER" --email "$EMAIL" --password "$PASS" --admin 2>&1 || true)
		case $OUT in
		*"User created"*) ;;
		*"already exists"*) PASS= && ok "The account $ADMIN_USER already exists" "Le compte $ADMIN_USER existe déjà" ;;
		*) printf '%s\n' "$OUT" >>"$LOG" && die "The administrator couldn't be created." "L'administrateur n'a pas pu être créé." ;;
		esac
	fi
	[ -z "$PASS" ] || ok "Administrator $ADMIN_USER created" "Administrateur $ADMIN_USER créé"
fi

# ---------- 10. Firewall of the server

if ufw_active; then
	step "Firewall (ufw)" "Pare-feu (ufw)"
	# also the cluster's ports, so that adding a machine later works from the panel (k3s API and kubelet need
	# their certificates; WireGuard its keys)
	PORTS="80/tcp 443/tcp 30100:30299/tcp 30100:30299/udp $SFTP_PORT/tcp 6443/tcp 10250/tcp 51820/udp"
	[ "$MC_ROUTER" != true ] || PORTS="$PORTS 25565/tcp"
	if confirm "ufw is active: open $PORTS?" "ufw est actif : ouvrir $PORTS ?" y; then
		for p in $PORTS; do run ufw allow "$p"; done
		ok "Ports opened" "Ports ouverts"
	else
		warn "Open them yourself, or players and Let's Encrypt won't get in." "Ouvre-les toi-même, sinon ni les joueurs ni Let's Encrypt ne pourront entrer."
	fi
fi

# ---------- 11. Certificate

step "HTTPS certificate" "Certificat HTTPS"
i=0
READY=
while [ $((i * 5)) -lt "$CERT_WAIT" ]; do
	READY=$(kubectl -n "$NAMESPACE" get certificate -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
	[ "$READY" != True ] || break
	i=$((i + 1))
	sleep 5
done
if [ "$READY" = True ]; then
	ok "Certificate issued by Let's Encrypt" "Certificat délivré par Let's Encrypt"
else
	warn "No certificate yet. It comes on its own once $DOMAIN points at this server and port 80 is open to the Internet." \
		"Pas encore de certificat. Il arrivera tout seul dès que $DOMAIN pointera vers ce serveur et que le port 80 sera ouvert sur Internet."
	say "Follow it with: kubectl -n $NAMESPACE describe certificate" "Pour suivre : kubectl -n $NAMESPACE describe certificate"
fi

# ---------- Done

printf '\n%s%s%s\n\n' "$G$B" "$(tr2 "KubePilot is ready!" "KubePilot est prêt !")" "$N"
printf '  %-15s %shttps://%s%s\n' "$(tr2 "Panel" "Panel")" "$B" "$DOMAIN" "$N"
if [ -n "$PASS" ]; then
	printf '  %-15s %s\n' "$(tr2 "Username" "Utilisateur")" "$ADMIN_USER"
	printf '  %-15s %s%s%s   %s\n' "$(tr2 "Password" "Mot de passe")" "$B" "$PASS" "$N" "$D$(tr2 "shown once: write it down" "affiché une seule fois : note-le")$N"
fi
printf '\n'
say "Next:" "Ensuite :"
[ -z "$PASS" ] || say "· turn on two-factor authentication in My account" "· active la double authentification dans Mon compte"
if [ "$MC_ROUTER" = true ]; then
	say "· add a DNS record *.mc.$DOMAIN → ${IP:-this server}, then set the Minecraft domain mc.$DOMAIN in Admin → Settings" \
		"· ajoute un enregistrement DNS *.mc.$DOMAIN → ${IP:-ce serveur}, puis le domaine Minecraft mc.$DOMAIN dans Admin → Réglages"
fi
say "· players join ${IP:-<server>}:<port>; game ports are 30100-30299 (open them if your provider has a firewall)" \
	"· les joueurs rejoignent ${IP:-<serveur>}:<port> ; les ports de jeu sont 30100-30299 (ouvre-les si ton hébergeur a un pare-feu)"
say "· update KubePilot: run this command again" "· mettre à jour KubePilot : relance cette commande"
say "· more machines: Admin → Machines → Add a machine" "· plus de machines : Admin → Machines → Ajouter une machine"
say "· uninstall: helm uninstall $RELEASE -n $NAMESPACE (game servers and their files are kept)" \
	"· désinstaller : helm uninstall $RELEASE -n $NAMESPACE (les serveurs de jeu et leurs fichiers sont gardés)"
printf '\n'
