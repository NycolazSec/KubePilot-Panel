package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------- Adding a machine from the panel
//
// The admin clicks "Add a machine" and gets one command to paste on the new machine: the panel creates the k3s
// invitation itself (a bootstrap token, like `k3s token create`), so nobody has to log in to the main machine.
// The command carries the main machine's address, the token (with the cluster CA's hash: the new machine checks
// it talks to the right cluster) and the k3s version (a node newer than the server isn't supported).

const (
	inviteTTL       = 24 * time.Hour
	installerURL    = "https://raw.githubusercontent.com/NycolazSec/KubePilot-Panel/main/install.sh"
	k3sNodeGroup    = "system:bootstrappers:k3s:default-node-token" // what k3s token create sets
	bootstrapPrefix = "bootstrap-token-"
)

type Invitation struct {
	Command    string `json:"command"`
	Server     string `json:"server"`
	K3sVersion string `json:"k3sVersion"`
	Expires    int64  `json:"expires"`
}

// joinToken builds k3s' "secure" token format: K10<sha256 of the cluster CA bundle>::<id>.<secret>.
func joinToken(caBundle []byte, id, secret string) string {
	sum := sha256.Sum256(caBundle)
	return "K10" + hex.EncodeToString(sum[:]) + "::" + id + "." + secret
}

// joinCommand is what the admin pastes on the new machine.
func joinCommand(server, token, version string) string {
	cmd := "curl -sfL " + installerURL + " | sudo sh -s -- join --server https://" + server + ":6443 --token " + token
	if version != "" {
		cmd += " --k3s-version " + version
	}
	return cmd
}

// mainNode: the control plane (where new machines connect), else the panel's node.
func mainNode(nodes []corev1.Node, panel string) *corev1.Node {
	var fallback *corev1.Node
	for i := range nodes {
		n := &nodes[i]
		if _, ok := n.Labels["node-role.kubernetes.io/control-plane"]; ok {
			return n
		}
		if n.Name == panel || fallback == nil {
			fallback = n
		}
	}
	return fallback
}

// createInvitation creates a k3s bootstrap token valid 24 hours and returns the command for the new machine.
func (k *Kube) createInvitation(ctx context.Context, address, by string) (*Invitation, error) {
	nodes, err := k.clusterNodes(ctx)
	if err != nil {
		return nil, err
	}
	main := mainNode(nodes, k.nodeName)
	if main == nil {
		return nil, errCode(500, "No node found")
	}
	version := main.Status.NodeInfo.KubeletVersion
	if !strings.Contains(version, "+k3s") {
		return nil, errCode(400, "Adding a machine from the panel needs k3s: join the node to your cluster the way your Kubernetes does it")
	}
	if address == "" {
		address = nodeIP(main)
	}
	if !validHost(address) {
		return nil, errCode(400, "Invalid address: a domain name or an IP address")
	}
	ca, err := k.cs.CoreV1().RESTClient().Get().AbsPath("/cacerts").DoRaw(ctx)
	if err != nil || !strings.Contains(string(ca), "BEGIN CERTIFICATE") {
		return nil, errCode(502, "Can't read the cluster's certificate authority (/cacerts)")
	}
	id, secret := randomID(6), randomID(16) // [a-z0-9], what bootstrap tokens require
	expires := time.Now().Add(inviteTTL).UTC()
	_, err = k.cs.CoreV1().Secrets("kube-system").Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: bootstrapPrefix + id, Labels: map[string]string{lblManaged: managedBy}},
		Type:       corev1.SecretTypeBootstrapToken,
		StringData: map[string]string{
			"token-id": id, "token-secret": secret, "expiration": expires.Format(time.RFC3339),
			"usage-bootstrap-authentication": "true", "usage-bootstrap-signing": "true",
			"auth-extra-groups": k3sNodeGroup, "description": truncate("KubePilot: new machine, invited by "+by, 120),
		},
	}, metav1.CreateOptions{})
	if apierrors.IsForbidden(err) {
		return nil, errCode(403, "The panel isn't allowed to create invitations (nodeInvites.enabled=false): run install.sh add-node on the main machine")
	} else if err != nil {
		return nil, err
	}
	token := joinToken(ca, id, secret)
	return &Invitation{Command: joinCommand(address, token, version), Server: address, K3sVersion: version, Expires: expires.Unix()}, nil
}

// POST /api/admin/nodes/invite {address}
func (a *App) handleAdminNodeInvite(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct {
		Address string `json:"address"`
	}
	if r.ContentLength > 0 {
		if err := readJSON(r, &in); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	inv, err := a.kube.createInvitation(r.Context(), strings.ToLower(strings.TrimSpace(in.Address)), u.Username)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "admin.node.invite", inv.Server)
	writeJSON(w, 200, inv)
}
