package main

import (
	"context"
	"net/http"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// ---------- Several nodes
//
// A server lives on one node: its volume (local-path) is a folder of that node, and its game port answers on
// that node's address (externalTrafficPolicy: Local). New servers go to the node with the most free memory,
// among the nodes that accept new servers, unless the administrator picks one. The panel reaches the files
// of the other nodes through their agents (nodeagent.go).

const nodeSchema = `
CREATE TABLE IF NOT EXISTS nodes (
  name TEXT PRIMARY KEY,
  public_host TEXT NOT NULL DEFAULT '',
  accept_new INTEGER NOT NULL DEFAULT 1
);
`

// NodeSetting is what the panel stores about a node.
type NodeSetting struct {
	PublicHost string `json:"publicHost"` // address players use for the servers of this node ("" = its IP)
	AcceptNew  bool   `json:"acceptNew"`  // automatic placement may put new servers there
}

func (db *DB) NodeSettings() map[string]NodeSetting {
	out := map[string]NodeSetting{}
	rows, err := db.Query(`SELECT name, public_host, accept_new FROM nodes`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var ns NodeSetting
		if rows.Scan(&name, &ns.PublicHost, &ns.AcceptNew) == nil {
			out[name] = ns
		}
	}
	return out
}

func (db *DB) nodeSetting(name string) NodeSetting {
	if ns, ok := db.NodeSettings()[name]; ok {
		return ns
	}
	return NodeSetting{AcceptNew: true}
}

func (db *DB) SaveNodeSetting(name string, ns NodeSetting) error {
	_, err := db.Exec(`INSERT INTO nodes(name, public_host, accept_new) VALUES(?,?,?)
		ON CONFLICT(name) DO UPDATE SET public_host=excluded.public_host, accept_new=excluded.accept_new`, name, ns.PublicHost, ns.AcceptNew)
	return err
}

// ---------- The cluster's nodes, cached (addresses are needed for every server view)

type nodeCache struct {
	mu    sync.Mutex
	at    time.Time
	nodes []corev1.Node
}

func (k *Kube) clusterNodes(ctx context.Context) ([]corev1.Node, error) {
	k.nodeCache.mu.Lock()
	defer k.nodeCache.mu.Unlock()
	if time.Since(k.nodeCache.at) < 15*time.Second && k.nodeCache.nodes != nil {
		return k.nodeCache.nodes, nil
	}
	list, err := k.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		if k.nodeCache.nodes != nil {
			return k.nodeCache.nodes, nil // stale beats nothing
		}
		return nil, err
	}
	k.nodeCache.nodes, k.nodeCache.at = list.Items, time.Now()
	return list.Items, nil
}

func nodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// nodeIP is the address of a node players can reach: its external IP when the node has one.
func nodeIP(n *corev1.Node) string {
	var internal string
	for _, a := range n.Status.Addresses {
		switch a.Type {
		case corev1.NodeExternalIP:
			return a.Address
		case corev1.NodeInternalIP:
			if internal == "" {
				internal = a.Address
			}
		}
	}
	return internal
}

// cachedVolNode is the node of a server's volume if it's already known (no API call).
func (k *Kube) cachedVolNode(id string) string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.vols[id].Node
}

// agentNodes: the nodes whose agent is running.
func (k *Kube) agentNodes(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	if k.nodes == nil {
		return out
	}
	pods, err := k.cs.CoreV1().Pods(k.nodes.ns).List(ctx, metav1.ListOptions{LabelSelector: k.nodes.sel})
	if err != nil {
		return out
	}
	for i := range pods.Items {
		if podReady(&pods.Items[i]) {
			out[pods.Items[i].Spec.NodeName] = true
		}
	}
	return out
}

// ---------- Where a server is, and its address

// serverNode is the node a server lives on ("" when unknown: no volume yet on an unpinned server).
func (a *App) serverNode(s *Server) string {
	if s.Node != "" {
		return s.Node
	}
	if a.kube == nil {
		return ""
	}
	return a.kube.cachedVolNode(s.ID)
}

// serverHost is the address players type for a server's ports: the address of its node.
func (a *App) serverHost(ctx context.Context, s *Server, global string) string {
	node := a.serverNode(s)
	if a.kube == nil || node == "" || node == a.kube.nodeName {
		if ns := a.db.nodeSetting(node); node != "" && ns.PublicHost != "" {
			return ns.PublicHost
		}
		return global
	}
	if ns := a.db.nodeSetting(node); ns.PublicHost != "" {
		return ns.PublicHost
	}
	nodes, err := a.kube.clusterNodes(ctx)
	if err != nil {
		return global
	}
	if len(nodes) == 1 { // a single node: the panel's address setting applies
		return global
	}
	for i := range nodes {
		if nodes[i].Name == node {
			if ip := nodeIP(&nodes[i]); ip != "" {
				return ip
			}
		}
	}
	return global
}

// ---------- Placement

// nodeLoad adds up what KubePilot's servers reserve on each node.
type nodeLoad struct {
	servers       int
	mem, cpu, dsk int64 // MB, millicores, MB
}

func (a *App) nodeLoads() map[string]*nodeLoad {
	out := map[string]*nodeLoad{}
	servers, _ := a.db.AllServers()
	for _, s := range servers {
		n := a.serverNode(s)
		if n == "" && a.kube != nil {
			n = a.kube.nodeName // unpinned servers of a single-node install
		}
		l := out[n]
		if l == nil {
			l = &nodeLoad{}
			out[n] = l
		}
		l.servers++
		l.mem += int64(s.MemoryMB)
		l.cpu += int64(s.CPUMillis)
		l.dsk += int64(s.DiskMB)
	}
	return out
}

// usableNode: a node that can run game servers now, and whose files the panel can reach.
func usableNode(n *corev1.Node, local func(string) bool, agents map[string]bool) error {
	switch {
	case !nodeReady(n):
		return errCode(409, "The node %s isn't ready", n.Name)
	case n.Spec.Unschedulable:
		return errCode(409, "The node %s is cordoned (maintenance)", n.Name)
	case !local(n.Name) && !agents[n.Name]:
		return errCode(409, "The KubePilot agent of the node %s isn't running: its files would be out of reach", n.Name)
	}
	for _, t := range n.Spec.Taints {
		if t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute {
			return errCode(409, "The node %s doesn't accept workloads (taint %s)", n.Name, t.Key)
		}
	}
	return nil
}

// placeServer picks the node of a new server (see pickNode).
func (a *App) placeServer(ctx context.Context, want string, memMB int) (string, error) {
	nodes, err := a.kube.clusterNodes(ctx)
	if err != nil {
		return "", err
	}
	if want == "" && len(nodes) <= 1 {
		return "", nil
	}
	return pickNode(nodes, a.kube.local, a.kube.agentNodes(ctx), a.nodeLoads(), a.db.NodeSettings(), want, memMB)
}

// pickNode: the node asked for, or the one with the most free memory among the nodes that accept new servers.
// A cluster of one node pins nothing (servers stay where their volume is, as before several nodes existed).
func pickNode(nodes []corev1.Node, local func(string) bool, agents map[string]bool, loads map[string]*nodeLoad,
	settings map[string]NodeSetting, want string, memMB int) (string, error) {
	if want == "" && len(nodes) <= 1 {
		return "", nil
	}
	if want != "" {
		for i := range nodes {
			if nodes[i].Name == want {
				if err := usableNode(&nodes[i], local, agents); err != nil {
					return "", err
				}
				return want, nil
			}
		}
		return "", errCode(400, "Unknown node: %s", want)
	}
	type cand struct {
		name    string
		free    int64
		servers int
	}
	var cands []cand
	for i := range nodes {
		n := &nodes[i]
		if ns, ok := settings[n.Name]; (ok && !ns.AcceptNew) || usableNode(n, local, agents) != nil {
			continue
		}
		c := cand{name: n.Name}
		if q, ok := n.Status.Allocatable[corev1.ResourceMemory]; ok {
			c.free = q.Value() >> 20
		}
		if l := loads[n.Name]; l != nil {
			c.free -= l.mem
			c.servers = l.servers
		}
		cands = append(cands, c)
	}
	if len(cands) == 0 {
		return "", errCode(409, "No node can take a new server: every node is full, not ready, or closed to new servers")
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].free != cands[j].free {
			return cands[i].free > cands[j].free
		}
		if cands[i].servers != cands[j].servers {
			return cands[i].servers < cands[j].servers
		}
		return cands[i].name < cands[j].name
	})
	if cands[0].free < int64(memMB)/2 { // below the pod's memory request: it would never be scheduled
		return "", errCode(409, "No node has enough free memory for this server (%s MB free at most)", strconv.FormatInt(max(cands[0].free, 0), 10))
	}
	return cands[0].name, nil
}

// ---------- Admin: nodes

type NodeView struct {
	NodeInfo
	InternalIP   string `json:"internalIP"`
	ExternalIP   string `json:"externalIP"`
	ControlPlane bool   `json:"controlPlane"`
	Cordoned     bool   `json:"cordoned"`
	Panel        bool   `json:"panel"` // the panel runs there (its volumes are mounted directly)
	Agent        bool   `json:"agent"` // its agent is running
	Arch         string `json:"arch"`
	Servers      int    `json:"servers"`
	MemAlloc     int64  `json:"memoryAllocated"` // MB, what KubePilot servers there may use
	CPUAlloc     int64  `json:"cpuAllocated"`
	DiskAlloc    int64  `json:"diskAllocated"`
	PublicHost   string `json:"publicHost"`
	Address      string `json:"address"` // what players get for the servers there
	AcceptNew    bool   `json:"acceptNew"`
	Problem      string `json:"problem,omitempty"` // why automatic placement skips it
	CreatedAt    int64  `json:"createdAt"`
}

func (a *App) nodeViews(r *http.Request) ([]NodeView, error) {
	ctx := r.Context()
	infos, err := a.kube.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	nodes, _ := a.kube.clusterNodes(ctx)
	byName := map[string]*corev1.Node{}
	for i := range nodes {
		byName[nodes[i].Name] = &nodes[i]
	}
	agents, loads, settings := a.kube.agentNodes(ctx), a.nodeLoads(), a.db.NodeSettings()
	global := a.publicHost(r)
	out := []NodeView{}
	for _, info := range infos {
		v := NodeView{NodeInfo: info, Panel: info.Name == a.kube.nodeName || (a.kube.nodeName == "" && len(infos) == 1), Agent: agents[info.Name], AcceptNew: true}
		if ns, ok := settings[info.Name]; ok {
			v.PublicHost, v.AcceptNew = ns.PublicHost, ns.AcceptNew
		}
		if l := loads[info.Name]; l != nil {
			v.Servers, v.MemAlloc, v.CPUAlloc, v.DiskAlloc = l.servers, l.mem, l.cpu, l.dsk
		}
		if n := byName[info.Name]; n != nil {
			for _, ad := range n.Status.Addresses {
				switch ad.Type {
				case corev1.NodeInternalIP:
					if v.InternalIP == "" {
						v.InternalIP = ad.Address
					}
				case corev1.NodeExternalIP:
					if v.ExternalIP == "" {
						v.ExternalIP = ad.Address
					}
				}
			}
			_, v.ControlPlane = n.Labels["node-role.kubernetes.io/control-plane"]
			v.Cordoned = n.Spec.Unschedulable
			v.Arch = n.Status.NodeInfo.Architecture
			v.CreatedAt = n.CreationTimestamp.Unix()
			if err := usableNode(n, a.kube.local, agents); err != nil {
				v.Problem = trErr(langOf(r), err)
			}
		}
		v.Address = a.serverHost(ctx, &Server{Node: info.Name}, global)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Panel != out[j].Panel {
			return out[i].Panel
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// GET /api/admin/nodes
func (a *App) handleAdminNodes(w http.ResponseWriter, r *http.Request, u *User) {
	list, err := a.nodeViews(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"nodes": list, "agents": a.kube.nodes != nil})
}

// PUT /api/admin/nodes/{name} {publicHost, acceptNew}
func (a *App) handleAdminNodeSave(w http.ResponseWriter, r *http.Request, u *User) {
	name := r.PathValue("name")
	nodes, err := a.kube.clusterNodes(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if !slices.ContainsFunc(nodes, func(n corev1.Node) bool { return n.Name == name }) {
		writeErr(w, r, errCode(404, "Unknown node: %s", name))
		return
	}
	var in NodeSetting
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	in.PublicHost = strings.ToLower(strings.TrimSpace(in.PublicHost))
	if in.PublicHost != "" && !validHost(in.PublicHost) {
		writeErr(w, r, errCode(400, "Invalid address: a domain name or an IP address"))
		return
	}
	if err := a.db.SaveNodeSetting(name, in); err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "admin.node.update", name)
	writeJSON(w, 200, in)
}

// validHost: a DNS name or an IP address (no port, no scheme).
func validHost(h string) bool {
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	return len(h) <= 253 && len(validation.IsDNS1123Subdomain(h)) == 0
}
