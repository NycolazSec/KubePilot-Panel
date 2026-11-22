package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func guardConfig() *ShieldConfig {
	c := sampleShieldConfig("normal")
	c.Guard = []int{6443, 10250, 5001, 30117} // 30117 is a game port: never closed
	c.Nodes = []ClusterNode{
		{Name: "node-a", Addrs: []string{"192.0.2.10", "2001:db8::10"}},
		{Name: "node-b", Addrs: []string{"198.51.100.20", "not an ip"}},
	}
	c.seal()
	return c
}

func TestShieldGuardSanitize(t *testing.T) {
	c := guardConfig()
	if fmt.Sprint(c.Guard) != "[5001 6443 10250]" {
		t.Fatalf("guard ports (a game port is never closed): %v", c.Guard)
	}
	if fmt.Sprint(c.Nodes[1].Addrs) != "[198.51.100.20]" {
		t.Fatalf("invalid addresses dropped: %+v", c.Nodes)
	}
	bad := sampleShieldConfig("normal")
	bad.Guard = []int{6443}
	bad.Nodes = []ClusterNode{{Name: "Bad Name!", Addrs: []string{"192.0.2.1"}}, {Name: "node-c", Addrs: []string{"0.0.0.0"}}}
	bad.seal()
	if len(bad.Guard) != 0 {
		t.Fatalf("no node address to let in: no guard, or the cluster would be cut off: %+v", bad)
	}
	if again := guardConfig(); again.Rev != c.Rev {
		t.Fatal("same configuration, same revision")
	}
	withAudit := guardConfig()
	withAudit.Audit = 42
	withAudit.seal()
	if withAudit.Rev == c.Rev {
		t.Fatal("an audit request changes the revision, so the agents see it")
	}
}

func TestNftScriptGuard(t *testing.T) {
	s := nftScript("kubepilot", guardConfig(), nil)
	for _, want := range []string{
		"add set inet kubepilot guard_ports { type inet_service; }",
		"add element inet kubepilot guard_ports { 5001, 6443, 10250 }",
		"add element inet kubepilot nodes { 192.0.2.10, 198.51.100.20 }",
		"add element inet kubepilot nodes6 { 2001:db8::10 }",
		`add rule inet kubepilot pre tcp dport @guard_ports iif "lo" accept`,
		"add rule inet kubepilot pre tcp dport @guard_ports ip saddr @nodes tcp sport != 61999 accept",
		"add rule inet kubepilot pre tcp dport @guard_ports ip6 saddr @nodes6 accept",
		`add rule inet kubepilot pre tcp dport @guard_ports counter name "cluster" drop`,
		"add counter inet kubepilot cluster",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing: %s", want)
		}
	}
	// after the replies and the private networks, before the game ports
	iTrusted := strings.Index(s, "pre ip saddr @trusted accept")
	iDrop := strings.Index(s, `counter name "cluster" drop`)
	iGame := strings.Index(s, "jump game_tcp")
	if iTrusted > iDrop || iDrop > iGame {
		t.Error("order: trusted networks, then the guard, then the game ports")
	}
	// without the guard: the sets exist (stable table) but no rule uses them
	off := nftScript("kubepilot", sampleShieldConfig("normal"), nil)
	if !strings.Contains(off, "add set inet kubepilot guard_ports") || strings.Contains(off, "dport @guard_ports") {
		t.Error("guard off: sets declared, no rule")
	}
	if _, err := exec.LookPath("nft"); err != nil || os.Geteuid() != 0 {
		t.Log("nft not available (or not root): syntax not checked by nft")
		return
	}
	cmd := exec.Command("nft", "-c", "-f", "-")
	cmd.Stdin = strings.NewReader(nftScript("kp_test_dryrun", guardConfig(), nil))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nft -c: %v\n%s", err, out)
	}
}

func TestListeningPorts(t *testing.T) {
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1
   1: 0100007F:192C 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2 1
   2: 01002A0A:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3 1
   3: 0A0200C0:1389 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 4 1
   4: 0A0200C0:0050 0B0200C0:D431 01 00000000:00000000 00:00000000 00000000     0        0 5 1
`
	tcp6 := `  sl  local_address                         remote_address                        st
   0: 00000000000000000000000000000000:192B 00000000000000000000000000000000:0000 0A
   1: 00000000000000000000000001000000:1F40 00000000000000000000000000000000:0000 0A
`
	got := listeningPorts(strings.NewReader(tcp), strings.NewReader(tcp6))
	// 22 on every address, 5001 on a public address, 6443 on every IPv6 address; not the loopback (6444, 8000),
	// a private address (8080) or an established connection (80)
	if fmt.Sprint(got) != "[22 5001 6443]" {
		t.Fatalf("listening: %v", got)
	}
}

func TestProcNetAddr(t *testing.T) {
	for in, want := range map[string]string{
		"0100007F":                         "127.0.0.1",
		"0A0200C0":                         "192.0.2.10",
		"00000000000000000000000001000000": "::1",
		"B80D0120000000000000000010000000": "2001:db8::10",
	} {
		ip, ok := procNetAddr(in)
		if !ok || ip.String() != want {
			t.Errorf("%s: %v %v, want %s", in, ip, ok, want)
		}
	}
	if _, ok := procNetAddr("zz"); ok {
		t.Error("garbage")
	}
}

func TestTcpcatArgsAndReport(t *testing.T) {
	v4 := strings.Join(tcpcatArgs("198.51.100.20", []int{22, 6443}, "/tmp/r.json"), " ")
	if v4 != "-sS -g 61999 -Pn --open -T 4 --rate 200 -p 22,6443 -j /tmp/r.json 198.51.100.20" {
		t.Fatalf("IPv4: %s", v4)
	}
	if v6 := tcpcatArgs("2001:db8::20", []int{22}, "r"); v6[0] != "-sT" || slices.Contains(v6, "-g") {
		t.Fatalf("IPv6: connect probe: %v", v6)
	}
	open, err := parseTcpcatOpen([]byte(`{"target":"x","results":[{"ip":"198.51.100.20","port":6443,"state":"OPEN"},{"port":22,"state":"open"},{"port":80,"state":"FILTERED"}]}`))
	if err != nil || fmt.Sprint(open) != "[22 6443]" {
		t.Fatalf("open: %v %v", open, err)
	}
	if _, err := parseTcpcatOpen([]byte("nope")); err == nil {
		t.Fatal("garbage report")
	}
}

func TestAuditPorts(t *testing.T) {
	c := guardConfig()
	got := auditPorts([]int{8080, 22}, c, "node-a")
	for _, p := range []int{22, 80, 443, 8080, 6443, 10250, 2379, 25565, 30117, 25566} {
		if !slices.Contains(got, p) {
			t.Errorf("missing %d in %v", p, got)
		}
	}
	if slices.Contains(auditPorts(nil, c, "node-b"), 25566) {
		t.Error("a standard port forwarded on node-a isn't probed on node-b")
	}
}

func TestNodeInviteJoinOpen(t *testing.T) {
	inv := nodeInvite{Until: 1000, Nodes: 1}
	switch {
	case !inv.joinOpen(1, 500):
		t.Error("valid and not used: open")
	case inv.joinOpen(2, 500):
		t.Error("a machine joined: closed")
	case inv.joinOpen(1, 1000):
		t.Error("expired: closed")
	}
}

func TestGuardPorts(t *testing.T) {
	app, db := newTestApp(t, false)
	nodes := []ClusterNode{{Name: "a", Addrs: []string{"192.0.2.10"}}, {Name: "b", Addrs: []string{"198.51.100.20"}}}
	st := defaultShield()
	if app.guardPorts(context.Background(), st, nodes) != nil {
		t.Fatal("off by default")
	}
	st.ClusterGuard = true
	if got := app.guardPorts(context.Background(), st, nodes); fmt.Sprint(got) != "[2379 2380 5001 6443 10250]" {
		t.Fatalf("on: %v", got)
	}
	if app.guardPorts(context.Background(), st, append(nodes, ClusterNode{Name: "c"})) != nil {
		t.Fatal("a machine without an address: no guard")
	}
	if err := db.saveNodeInvite(nodeInvite{Until: now() + 3600, Nodes: 2}); err != nil {
		t.Fatal(err)
	}
	if got := app.guardPorts(context.Background(), st, nodes); slices.Contains(got, 6443) || len(got) != 4 {
		t.Fatalf("invitation open: 6443 stays open: %v", got)
	}
	if got := app.guardPorts(context.Background(), st, append(nodes, ClusterNode{Name: "c", Addrs: []string{"203.0.113.5"}})); !slices.Contains(got, 6443) {
		t.Fatalf("the new machine joined: 6443 closes again: %v", got)
	}
}

func TestRunAudit(t *testing.T) {
	status := func(node string, st shieldNodeStatus) string { b, _ := json.Marshal(st); return string(b) }
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: shieldStatusMap, Namespace: "srv"},
		Data: map[string]string{
			"node-a": status("node-a", shieldNodeStatus{Listening: []int{22}}),
			"node-b": status("node-b", shieldNodeStatus{Listening: []int{22, 9000}, Public: []string{"198.51.100.20", "2001:db8::20"}}),
		},
	})
	probed := map[string][]int{}
	ag := &shieldAgent{cs: cs, ns: "srv", node: "node-a", probe: func(_ context.Context, addr string, ports []int) ([]int, error) {
		probed[addr] = ports
		if addr == "2001:db8::20" {
			return nil, errors.New("tcpcat: no answer")
		}
		return []int{22, 9000}, nil
	}}
	c := guardConfig()
	c.Audit = 7
	ag.runAudit(context.Background(), c)
	rep := ag.lastAudit()
	if rep == nil || rep.ID != 7 || rep.Finished == 0 || len(rep.Results) != 2 {
		t.Fatalf("report: %+v", rep)
	}
	if _, self := probed["192.0.2.10"]; self || !slices.Contains(probed["198.51.100.20"], 9000) {
		t.Fatalf("only the other machine, on what it listens on: %v", probed)
	}
	if r := rep.Results[0]; r.Node != "node-b" || r.Addr != "198.51.100.20" || fmt.Sprint(r.Open) != "[22 9000]" {
		t.Fatalf("IPv4 result: %+v", r)
	}
	if r := rep.Results[1]; r.Error == "" || len(r.Open) != 0 {
		t.Fatalf("a failed probe is reported, not read as closed: %+v", r)
	}
}

func TestExposureView(t *testing.T) {
	app, _ := newTestApp(t, false)
	app.cfg.SFTPPort = 2022
	statuses := []shieldNodeStatus{
		{Node: "node-a", Audit: &auditReport{ID: 5, Finished: 100, Results: []auditResult{
			{Node: "node-b", Addr: "198.51.100.20", Probed: 8, Open: []int{22, 443, 2022, 6443, 9000}},
		}}},
		{Node: "node-b", Audit: &auditReport{ID: 4, Finished: 90}}, // an older request
	}
	views, running := app.exposure(statuses, 5, []int{6443})
	if !running {
		t.Error("node-b hasn't answered the latest request yet")
	}
	if len(views) != 1 || views[0].From != "node-a" || views[0].Problems != 2 {
		t.Fatalf("views: %+v", views)
	}
	kinds := map[int]string{}
	for _, p := range views[0].Open {
		kinds[p.Port] = p.Kind
	}
	if fmt.Sprint(kinds) != "map[22:ssh 443:panel 2022:sftp 6443:cluster 9000:other]" {
		t.Fatalf("kinds: %v", kinds)
	}
}
