package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------- Exposure: the cluster's own ports, and what the Internet sees of each machine
//
// Both live in the shield (shield.go, shieldagent.go).
//
// Cluster port guard (setting clusterGuard, off by default). Kubernetes listens on every address of every machine:
// the API (6443), the kubelet (10250), k3s' image mirror (5001), etcd (2379-2380) and kube-proxy's health checks of
// LoadBalancer services. All of them ask for credentials, but none has to be reachable from the Internet: only the
// machines of the cluster talk to them. With the guard, every agent accepts these ports only from the nodes' own
// addresses, the cluster's private networks and the machine itself, over IPv4 and IPv6. While an invitation to add a
// machine is open (24 hours at most, or until a new machine has joined), 6443 stays open to everyone: the new machine
// connects to it before it is a node.
//
// Exposure audit (Admin → Firewall). Every agent reports the TCP ports its machine listens on and its public
// addresses; on request, every agent checks the other machines from the outside with tcpcat (bundled in the image).
// IPv4 probes are SYNs from a reserved source port (auditSourcePort), which the guard treats like any Internet
// address, so what the audit finds open is what the Internet finds open. IPv6 needs no trick: the nodes talk over
// IPv4, so no IPv6 address is trusted. Only ports something listens on, plus the servers' and a few usual ones, are
// probed: a few dozen packets per machine, not a scan of the whole range, which a host's anti-abuse could flag.

const (
	// auditSourcePort is outside Linux's ephemeral range (32768-60999): no ordinary connection uses it.
	auditSourcePort = 61999
	auditTimeout    = 2 * time.Minute
	auditMaxPorts   = 1024
	tcpcatBinary    = "/usr/local/bin/tcpcat"
	nodeInviteKey   = "nodeInvite"
)

// clusterPorts: what Kubernetes (k3s) listens on that only the machines of the cluster need. kube-proxy's health
// check ports are added per Service (healthCheckNodePort).
var clusterPorts = []int{2379, 2380, 5001, 6443, 10250}

// usualPorts: always probed by the audit, listening or not (a port forwarded by NAT has no socket of its own).
var usualPorts = []int{22, 80, 443}

// ClusterNode: a machine of the cluster as the shield sees it, by name, with the addresses Kubernetes knows it by.
type ClusterNode struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"`
}

// cleanAddrs keeps valid, specific IP addresses (IPv4 or IPv6), canonical and without duplicates.
func cleanAddrs(in []string) []string {
	out := []string{}
	for _, s := range in {
		ip, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil || ip.IsUnspecified() || ip.Zone() != "" {
			continue
		}
		out = append(out, ip.Unmap().String())
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// nodeAddresses: the addresses Kubernetes knows a node by (internal and external).
func nodeAddresses(n *corev1.Node) []string {
	var out []string
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP || a.Type == corev1.NodeExternalIP {
			out = append(out, a.Address)
		}
	}
	return cleanAddrs(out)
}

// splitAddrs separates IPv4 and IPv6 addresses (for the nftables sets).
func splitAddrs(nodes []ClusterNode) (v4, v6 []string) {
	for _, n := range nodes {
		for _, s := range n.Addrs {
			if ip, err := netip.ParseAddr(s); err == nil && ip.Is4() {
				v4 = append(v4, s)
			} else if err == nil {
				v6 = append(v6, s)
			}
		}
	}
	slices.Sort(v4)
	slices.Sort(v6)
	return slices.Compact(v4), slices.Compact(v6)
}

// ---------- Invitations: 6443 open while a new machine may be joining

type nodeInvite struct {
	Until int64 `json:"until"` // the invitation's expiry
	Nodes int   `json:"nodes"` // machines when it was created: one more and it has been used
}

func (db *DB) saveNodeInvite(inv nodeInvite) error {
	b, _ := json.Marshal(inv)
	_, err := db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, nodeInviteKey, string(b))
	return err
}

func (db *DB) nodeInvite() nodeInvite {
	var inv nodeInvite
	var raw string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key=?`, nodeInviteKey).Scan(&raw); err == nil {
		_ = json.Unmarshal([]byte(raw), &inv)
	}
	return inv
}

// joinOpen: an invitation may still be used (not expired, and no machine has joined since).
func (inv nodeInvite) joinOpen(nodes int, at int64) bool {
	return inv.Until > at && nodes <= inv.Nodes
}

// ---------- Panel side

// guardPorts: the ports the guard closes to the Internet now, or nil when it is off or can't be applied safely.
func (a *App) guardPorts(ctx context.Context, st ShieldSettings, nodes []ClusterNode) []int {
	if !st.ClusterGuard || len(nodes) == 0 {
		return nil
	}
	for _, n := range nodes {
		if len(n.Addrs) == 0 { // a machine we can't allow would be cut off from the cluster
			return nil
		}
	}
	ports := slices.Clone(clusterPorts)
	if a.kube != nil && a.kube.cs != nil {
		if list, err := a.kube.cs.CoreV1().Services("").List(ctx, metav1.ListOptions{}); err == nil {
			for _, s := range list.Items {
				if s.Spec.HealthCheckNodePort > 0 {
					ports = append(ports, int(s.Spec.HealthCheckNodePort))
				}
			}
		}
	}
	if a.db.nodeInvite().joinOpen(len(nodes), now()) {
		ports = slices.DeleteFunc(ports, func(p int) bool { return p == 6443 })
	}
	slices.Sort(ports)
	return slices.Compact(ports)
}

// clusterNodeList: every machine with its addresses (nil without Kubernetes).
func (a *App) clusterNodeList(ctx context.Context) []ClusterNode {
	if a.kube == nil {
		return nil
	}
	list, err := a.kube.clusterNodes(ctx)
	if err != nil {
		return nil
	}
	out := make([]ClusterNode, 0, len(list))
	for i := range list {
		out = append(out, ClusterNode{Name: list[i].Name, Addrs: nodeAddresses(&list[i])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// POST /api/admin/firewall/audit: every agent checks the other machines from the outside, once.
func (a *App) handleAdminShieldAudit(w http.ResponseWriter, r *http.Request, u *User) {
	if err := a.requireShield(); err != nil {
		writeErr(w, r, err)
		return
	}
	nodes := a.clusterNodeList(r.Context())
	if len(nodes) < 2 {
		writeErr(w, r, errCode(400, "The audit needs at least two machines: each one checks the others from the outside"))
		return
	}
	a.shield.mu.Lock()
	a.shield.audit = time.Now().UnixMilli()
	id := a.shield.audit
	a.shield.mu.Unlock()
	a.shieldChanged()
	a.db.LogActivity("", u, clientIP(r), "admin.firewall.audit", "")
	writeJSON(w, 202, map[string]int64{"audit": id})
}

// exposedPort: an open port the audit found, and what it is.
type exposedPort struct {
	Port int    `json:"port"`
	Kind string `json:"kind"` // game, panel, sftp, ssh, cluster (shouldn't be reachable), other
}

type exposureView struct {
	Node     string        `json:"node"`
	Addr     string        `json:"addr"`
	From     string        `json:"from"`
	At       int64         `json:"at"`
	Probed   int           `json:"probed"`
	Open     []exposedPort `json:"open"`
	Error    string        `json:"error,omitempty"`
	Problems int           `json:"problems"` // ports that shouldn't be open
}

// portKind says what an open port is, from what the panel knows it runs.
func portKind(p int, game map[int]bool, sftp int, guard map[int]bool) string {
	switch {
	case game[p]:
		return "game"
	case p == 80 || p == 443:
		return "panel"
	case sftp > 0 && p == sftp:
		return "sftp"
	case p == 22:
		return "ssh"
	case guard[p] || slices.Contains(clusterPorts, p):
		return "cluster"
	}
	return "other"
}

// exposure gathers the latest audit every agent ran, with each open port explained.
func (a *App) exposure(statuses []shieldNodeStatus, requested int64, guard []int) (views []exposureView, running bool) {
	game := map[int]bool{}
	if allocs, err := a.db.Allocations(""); err == nil {
		for _, al := range allocs {
			game[al.Port] = true
		}
	}
	if a.cfg.MinecraftRouter && a.cfg.RouterPort > 0 {
		game[a.cfg.RouterPort] = true
	}
	g := map[int]bool{}
	for _, p := range guard {
		g[p] = true
	}
	views = []exposureView{}
	for _, st := range statuses {
		if st.Audit == nil {
			if requested > 0 {
				running = true
			}
			continue
		}
		if st.Audit.ID < requested || st.Audit.Finished == 0 {
			running = true
		}
		for _, res := range st.Audit.Results {
			v := exposureView{Node: res.Node, Addr: res.Addr, From: st.Node, At: st.Audit.Finished, Probed: res.Probed, Error: res.Error, Open: []exposedPort{}}
			for _, p := range res.Open {
				k := portKind(p, game, a.cfg.SFTPPort, g)
				if k == "cluster" || k == "other" {
					v.Problems++
				}
				v.Open = append(v.Open, exposedPort{Port: p, Kind: k})
			}
			views = append(views, v)
		}
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Node != views[j].Node {
			return views[i].Node < views[j].Node
		}
		return views[i].Addr < views[j].Addr
	})
	return views, running
}

// ---------- Agent side

// auditReport: the last audit an agent ran, reported in its status.
type auditReport struct {
	ID       int64         `json:"id"`
	Started  int64         `json:"started"`
	Finished int64         `json:"finished"` // 0 while it runs
	Results  []auditResult `json:"results"`
}

type auditResult struct {
	Node   string `json:"node"`
	Addr   string `json:"addr"`
	Probed int    `json:"probed"`
	Open   []int  `json:"open"`
	Error  string `json:"error,omitempty"`
}

// listeningPorts reads /proc/net/tcp and tcp6 (the agent shares the node's network): the TCP ports something
// listens on, on every address or a public one. A socket on the loopback or a private address isn't reachable from
// outside.
func listeningPorts(readers ...io.Reader) []int {
	seen := map[int]bool{}
	for _, r := range readers {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) < 4 || f[3] != "0A" { // 0A: LISTEN
				continue
			}
			host, port, ok := strings.Cut(f[1], ":")
			if !ok {
				continue
			}
			p, err := strconv.ParseUint(port, 16, 16)
			ip, ok2 := procNetAddr(host)
			if err != nil || p == 0 || !ok2 {
				continue
			}
			if ip.IsUnspecified() || publicAddr(ip) {
				seen[int(p)] = true
			}
		}
	}
	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Ints(out)
	if len(out) > auditMaxPorts {
		out = out[:auditMaxPorts]
	}
	return out
}

// procNetAddr decodes an address of /proc/net/tcp*: 8 hex digits (IPv4) or 32 (IPv6), each 32-bit word in host
// (little-endian) order.
func procNetAddr(h string) (netip.Addr, bool) {
	b, err := hex.DecodeString(h)
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return netip.Addr{}, false
	}
	for i := 0; i+4 <= len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	ip, ok := netip.AddrFromSlice(b)
	return ip.Unmap(), ok
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// publicAddr: an address the Internet can reach (not loopback, link-local, private or carrier-grade NAT).
func publicAddr(ip netip.Addr) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnat.Contains(ip.Unmap())
}

// publicAddrs: this machine's public addresses, the ones the other agents audit.
func publicAddrs() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && publicAddr(p.Addr()) {
			out = append(out, p.Addr().Unmap().String())
		}
	}
	return cleanAddrs(out)
}

// auditPorts: what to probe on a machine: what it listens on, the servers', the guard's and the usual ports.
func auditPorts(listening []int, c *ShieldConfig, node string) []int {
	ports := slices.Concat(listening, c.TCPPorts, c.Guard, clusterPorts, usualPorts)
	for _, f := range c.Forwards {
		if f.Proto == "tcp" && f.Node == node {
			ports = append(ports, f.Port)
		}
	}
	ports = cleanPorts(ports)
	if len(ports) > auditMaxPorts {
		ports = ports[:auditMaxPorts]
	}
	return ports
}

// tcpcatArgs: a SYN probe from the audit's source port over IPv4; a connect probe over IPv6 (tcpcat's raw engine is
// IPv4 only, and no IPv6 address is trusted anyway).
func tcpcatArgs(addr string, ports []int, out string) []string {
	list := strings.Join(intStrings(ports), ",")
	scan := []string{"-sS", "-g", strconv.Itoa(auditSourcePort)}
	if ip, err := netip.ParseAddr(addr); err == nil && ip.Is6() {
		scan = []string{"-sT"}
	}
	return slices.Concat(scan, []string{"-Pn", "--open", "-T", "4", "--rate", "200", "-p", list, "-j", out, addr})
}

// parseTcpcatOpen reads the open ports of tcpcat's JSON report.
func parseTcpcatOpen(raw []byte) ([]int, error) {
	var rep struct {
		Results []struct {
			Port  int    `json:"port"`
			State string `json:"state"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("tcpcat's report: %v", err)
	}
	open := []int{}
	for _, r := range rep.Results {
		if strings.EqualFold(r.State, "open") {
			open = append(open, r.Port)
		}
	}
	return cleanPorts(open), nil
}

// runTcpcat probes one address and returns its open ports.
func runTcpcat(ctx context.Context, addr string, ports []int) ([]int, error) {
	dir, err := os.MkdirTemp("", "kp-audit-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := dir + "/report.json"
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tcpcatBinary, tcpcatArgs(addr, ports, out)...)
	cmd.Dir, cmd.Env = dir, []string{"HOME=" + dir, "PATH=/usr/local/bin:/usr/bin:/bin", "NO_COLOR=1"}
	var tail tailBuffer
	cmd.Stdout, cmd.Stderr = &tail, &tail
	if err := cmd.Run(); err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("tcpcat: no answer within %s", auditTimeout)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("tcpcat wrote no report: %s", strings.TrimSpace(tail.String()))
	}
	return parseTcpcatOpen(raw)
}

// tailBuffer keeps the end of a command's output (for an error message).
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 400 {
		t.b = t.b[len(t.b)-400:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.b) }

// runAudit checks every other machine from here: their listening ports (from their status) on their public
// addresses (from their status too, else the ones Kubernetes knows).
func (ag *shieldAgent) runAudit(ctx context.Context, c *ShieldConfig) {
	rep := &auditReport{ID: c.Audit, Started: now(), Results: []auditResult{}}
	ag.setAudit(rep)
	statuses := map[string]shieldNodeStatus{}
	if cm, err := ag.cs.CoreV1().ConfigMaps(ag.ns).Get(ctx, shieldStatusMap, metav1.GetOptions{}); err == nil {
		for node, raw := range cm.Data {
			var st shieldNodeStatus
			if json.Unmarshal([]byte(raw), &st) == nil {
				statuses[node] = st
			}
		}
	}
	probe := ag.probe
	if probe == nil {
		probe = runTcpcat
	}
	results := []auditResult{}
	for _, n := range c.Nodes {
		if n.Name == ag.node {
			continue
		}
		st := statuses[n.Name]
		addrs := cleanAddrs(st.Public)
		if len(addrs) == 0 {
			addrs = n.Addrs
		}
		ports := auditPorts(st.Listening, c, n.Name)
		for _, addr := range addrs {
			res := auditResult{Node: n.Name, Addr: addr, Probed: len(ports), Open: []int{}}
			if open, err := probe(ctx, addr, ports); err != nil {
				res.Error = err.Error()
			} else {
				res.Open = open
			}
			results = append(results, res)
		}
	}
	ag.setAudit(&auditReport{ID: c.Audit, Started: rep.Started, Finished: now(), Results: results})
	ag.report(ctx) // don't make the admin wait for the next report
}

func (ag *shieldAgent) setAudit(r *auditReport) {
	ag.mu.Lock()
	ag.audit = r
	ag.mu.Unlock()
}

func (ag *shieldAgent) lastAudit() *auditReport {
	ag.mu.Lock()
	defer ag.mu.Unlock()
	return ag.audit
}
