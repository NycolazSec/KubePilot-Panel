package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	_ "modernc.org/sqlite"
)

func TestAllocationsMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	// a database of the previous version: the port is the primary key
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE servers (id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE allocations (port INTEGER PRIMARY KEY, server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			protocol TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', target_port INTEGER NOT NULL, is_primary INTEGER NOT NULL DEFAULT 0,
			notes TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO servers(id, name) VALUES ('aaa', 'A'), ('bbb', 'B')`,
		`INSERT INTO allocations VALUES (30100, 'aaa', 'tcp', 'game', 25565, 1, ''), (30101, 'aaa', 'udp', 'extra', 30101, 0, 'voice'), (30102, 'bbb', 'tcp', 'game', 8080, 1, '')`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	all, err := db.Allocations("")
	if err != nil || len(all) != 3 {
		t.Fatalf("rows kept: %v %v", all, err)
	}
	if a := all[1]; a.Port != 30101 || a.Protocol != "udp" || a.Notes != "voice" || a.Host || a.ServerID != "aaa" {
		t.Fatalf("row content kept: %+v", a)
	}
	ins := func(port int, srv string, host bool, node string) error {
		return db.InsertAllocation(&Allocation{Port: port, ServerID: srv, Protocol: "tcp", TargetPort: port, Host: host, Node: node})
	}
	if ins(30100, "bbb", false, "") == nil {
		t.Fatal("a NodePort is unique in the cluster")
	}
	if err := ins(25565, "aaa", true, "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := ins(25565, "bbb", true, "node-b"); err != nil {
		t.Fatalf("the same standard port on another node: %v", err)
	}
	if ins(25565, "bbb", true, "node-a") == nil {
		t.Fatal("a standard port is unique on its node")
	}
	if _, err := db.Exec(`DELETE FROM servers WHERE id='aaa'`); err != nil {
		t.Fatal(err)
	}
	if left, _ := db.Allocations("aaa"); len(left) != 0 {
		t.Fatal("deleting a server still frees its ports")
	}
	db.Close()
	// a second start doesn't migrate again
	db2, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if all, _ := db2.Allocations(""); len(all) != 2 {
		t.Fatalf("reopened: %v", all)
	}
	db2.Close()
}

func TestPortPool(t *testing.T) {
	pool, err := parsePortPool(" 27015-27030, 25565-25600;7777 ", 0, 2022)
	if err != nil {
		t.Fatal(err)
	}
	if got := formatPortPool(pool); got != "7777, 25565-25600, 27015-27030" || poolSize(pool) != 53 {
		t.Fatalf("pool: %q %d", got, poolSize(pool))
	}
	for in, want := range map[string]string{
		"80":                   "Invalid",
		"30000-30100":          "reserved",
		"25560-25570":          "reserved", // the router's port
		"2000-2100":            "reserved", // SFTP
		"6443":                 "reserved",
		"25565-25500":          "Invalid",
		"abc":                  "Invalid",
		"500":                  "Invalid",
		"70000":                "Invalid",
		"7000-7100, 7050-7060": "overlap",
		"3000-5100":            "2000",
	} {
		_, err := parsePortPool(in, 25565, 2022)
		if err == nil || !strings.Contains(err.Error()+trErr("en", err), want) {
			t.Errorf("%q: want %q, got %v", in, want, err)
		}
	}
	if p, _ := parsePortPool("", 25565, 2022); len(p) != 0 {
		t.Fatal("empty pool")
	}

	used := map[int]bool{25565: true}
	if p, ok := pickHostPort(pool, used, 27015, nil); !ok || p != 27015 {
		t.Fatalf("the game's usual port first: %d", p)
	}
	if p, ok := pickHostPort(pool, used, 25565, nil); !ok || p != 7777 {
		t.Fatalf("taken: the first free port: %d", p)
	}
	if p, ok := pickHostPort(pool, used, 19132, func(p int) bool { return p == 7777 }); !ok || p != 25566 {
		t.Fatalf("not in the pool, and 7777 busy on the node: %d", p)
	}
	if _, ok := pickHostPort([]portRange{{7777, 7777}}, map[int]bool{7777: true}, 0, nil); ok {
		t.Fatal("pool exhausted")
	}
}

func TestStandardPortsInPodAndService(t *testing.T) {
	sp := Spec{Server: &Server{ID: "x", HostPorts: true}, Template: &Template{DataPath: "/data"}, Allocs: []*Allocation{
		{Port: 25566, TargetPort: 25565, Protocol: "tcp", IsPrimary: true, Host: true, Node: "n1"},
		{Port: 24454, TargetPort: 24454, Protocol: "udp", Host: true, Node: "n1"},
	}}
	ports := sp.podTemplate().Spec.Containers[0].Ports
	if len(ports) != 2 || ports[0].ContainerPort != 25565 || ports[1].Protocol != corev1.ProtocolUDP {
		t.Fatalf("container ports: %+v", ports)
	}
	for _, p := range ports {
		if p.HostPort != 0 {
			t.Fatal("never a hostPort: Pod Security forbids it, the node's agent forwards the port")
		}
	}
	svc, nodePorts := sp.servicePorts()
	if nodePorts || len(svc) != 2 || svc[0].NodePort != 0 || svc[0].Port != 25566 || svc[0].TargetPort.IntValue() != 25565 {
		t.Fatalf("standard ports: a ClusterIP Service only: %v %+v", nodePorts, svc)
	}
	// NodePorts, as before
	sp = Spec{Server: &Server{ID: "y"}, Template: &Template{DataPath: "/data"}, Allocs: []*Allocation{{Port: 30100, TargetPort: 25565, Protocol: "tcp+udp", IsPrimary: true}}}
	svc, nodePorts = sp.servicePorts()
	if !nodePorts || len(svc) != 2 || svc[0].NodePort != 30100 || svc[1].NodePort != 30100 {
		t.Fatalf("NodePorts: %v %+v", nodePorts, svc)
	}
}

func TestShieldRulesFollowStandardPorts(t *testing.T) {
	allocs := []*Allocation{
		{Port: 25565, ServerID: "aaa", Protocol: "tcp", Host: true, Node: "node-a"},
		{Port: 25565, ServerID: "bbb", Protocol: "tcp", Host: true, Node: "node-b"},
		{Port: 30100, ServerID: "ccc", Protocol: "tcp"},
	}
	rules := []*FirewallRule{
		{ServerID: "aaa", CIDR: "203.0.113.0/24", Action: "deny"},
		{ServerID: "ccc", CIDR: "198.51.100.7/32", Action: "deny"},
	}
	c := buildShieldConfig(ShieldSettings{Mode: "normal", TCPRate: 15, Conns: 30, UDPRate: 1500, BanMinutes: 10}, allocs, rules, 0, nil, nil)
	if len(c.Servers) != 2 || c.Servers[0].Node != "node-a" || c.Servers[1].Node != "" {
		t.Fatalf("servers: %+v", c.Servers)
	}
	b := *c
	b.Servers = append([]ShieldServer(nil), c.Servers...)
	b.forNode("node-b")
	if len(b.Servers) != 1 || b.Servers[0].ID != "ccc" {
		t.Fatalf("node-b must not block 25565 for the server of node-a: %+v", b.Servers)
	}
	c.forNode("node-a")
	if len(c.Servers) != 2 {
		t.Fatalf("node-a applies both: %+v", c.Servers)
	}
}
