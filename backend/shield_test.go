package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestParseCIDR4(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":       "203.0.113.7/32",
		" 198.51.100.9/24 ": "198.51.100.0/24",
		"10.0.0.0/8":        "10.0.0.0/8",
	} {
		if got, err := parseCIDR4(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "2001:db8::1", "::ffff:1.2.3.4", "1.2.3.4/33", "0.0.0.0/0", "1.0.0.0/7", "example.com", "1.2.3"} {
		if _, err := parseCIDR4(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestShieldSettings(t *testing.T) {
	d := defaultShield()
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ShieldSettings{
		{Mode: "max", TCPRate: 15, Conns: 30, UDPRate: 1500, BanMinutes: 10},
		{Mode: "normal", TCPRate: 0, Conns: 30, UDPRate: 1500, BanMinutes: 10},
		{Mode: "normal", TCPRate: 15, Conns: 0, UDPRate: 1500, BanMinutes: 10},
		{Mode: "normal", TCPRate: 15, Conns: 30, UDPRate: 5, BanMinutes: 10},
		{Mode: "normal", TCPRate: 15, Conns: 30, UDPRate: 1500, BanMinutes: 0},
	} {
		if bad.validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if tcp, conns, udp, ban := d.effective(); tcp != 15 || conns != 30 || udp != 1500 || ban != 600 {
		t.Fatalf("normal: %d %d %d %d", tcp, conns, udp, ban)
	}
	d.Mode = "strict"
	if tcp, conns, udp, ban := d.effective(); tcp != 5 || conns != 10 || udp != 750 || ban != 3600 {
		t.Fatalf("strict: three times tighter, bans six times longer: %d %d %d %d", tcp, conns, udp, ban)
	}
	d.BanMinutes = 10080
	if _, _, _, ban := d.effective(); ban != shieldMaxBanSeconds {
		t.Fatalf("bans are capped at 7 days: %d", ban)
	}
}

func TestBuildShieldConfig(t *testing.T) {
	allocs := []*Allocation{
		{Port: 30100, ServerID: "aaaa1111", Protocol: "tcp"},
		{Port: 30101, ServerID: "aaaa1111", Protocol: "tcp+udp"},
		{Port: 30102, ServerID: "bbbb2222", Protocol: "udp"},
		{Port: 30103, ServerID: "cccc3333", Protocol: "tcp"},
	}
	rules := []*FirewallRule{
		{CIDR: "198.51.100.0/24", Action: "deny"},
		{CIDR: "198.51.100.7/32", Action: "deny"}, // already covered by the /24
		{CIDR: "203.0.113.10/32", Action: "allow"},
		{ServerID: "aaaa1111", CIDR: "192.0.2.0/24", Action: "allow"},
		{ServerID: "aaaa1111", CIDR: "192.0.2.66/32", Action: "deny"},
		{ServerID: "bbbb2222", CIDR: "192.0.2.99/32", Action: "deny"},
		{ServerID: "dddd4444", CIDR: "192.0.2.1/32", Action: "deny"}, // no port: nothing to filter
	}
	c := buildShieldConfig(defaultShield(), allocs, rules, 25565, nil, nil)
	if fmt.Sprint(c.TCPPorts) != "[25565 30100 30101 30103]" || fmt.Sprint(c.UDPPorts) != "[30101 30102]" {
		t.Fatalf("ports: tcp %v udp %v", c.TCPPorts, c.UDPPorts)
	}
	if fmt.Sprint(c.Deny) != "[198.51.100.0/24]" || fmt.Sprint(c.Allow) != "[203.0.113.10/32]" {
		t.Fatalf("global lists: deny %v allow %v", c.Deny, c.Allow)
	}
	if len(c.Servers) != 2 || c.Servers[0].ID != "aaaa1111" || c.Servers[1].ID != "bbbb2222" {
		t.Fatalf("only servers with rules and ports: %+v", c.Servers)
	}
	a := c.Servers[0]
	if fmt.Sprint(a.TCP, a.UDP, a.Allow, a.Deny) != "[30100 30101] [30101] [192.0.2.0/24] [192.0.2.66/32]" {
		t.Fatalf("server a: %+v", a)
	}
	if c.TCPRate != 15 || c.TCPBurst != 30 || c.Conns != 30 || c.UDPRate != 1500 || c.UDPBurst != 3000 || c.BanSeconds != 600 || c.Rev == "" {
		t.Fatalf("limits: %+v", c)
	}
	if again := buildShieldConfig(defaultShield(), allocs, rules, 25565, nil, nil); again.Rev != c.Rev {
		t.Fatal("same inputs, same revision")
	}
	if other := buildShieldConfig(defaultShield(), allocs, rules, 0, nil, nil); other.Rev == c.Rev || len(other.TCPPorts) != 3 {
		t.Fatal("without the router, port 25565 isn't protected and the revision changes")
	}
}

func TestShieldSanitize(t *testing.T) {
	c := &ShieldConfig{Mode: "turbo", TCPRate: -5, Conns: 1 << 30, UDPRate: 3, BanSeconds: 1,
		TCPPorts: []int{0, 80, 80, 70000}, Allow: []string{"1.2.3.4/32", "x; flush ruleset", "::1/128", "0.0.0.0/0"},
		Servers: []ShieldServer{
			{ID: "ok123456", TCP: []int{30100}, Deny: []string{"1.2.3.0/24", "1.2.3.4/32"}},
			{ID: "bad id\" drop", TCP: []int{30100}, Deny: []string{"1.2.3.4/32"}},
			{ID: "noports1", Deny: []string{"1.2.3.4/32"}},
		},
		Actions: []ShieldAction{{ID: 1, Op: "ban", IP: "198.51.100.1", Seconds: 60}, {ID: 2, Op: "rm -rf", IP: "198.51.100.1"}, {ID: 3, Op: "ban", IP: "1.2.3.4 }"}},
	}
	c.sanitize()
	if c.Mode != "off" || c.TCPRate != 1 || c.Conns != 10000 || c.UDPRate != 10 || c.BanSeconds != 60 {
		t.Fatalf("limits: %+v", c)
	}
	if fmt.Sprint(c.TCPPorts) != "[80]" || fmt.Sprint(c.Allow) != "[1.2.3.4/32]" {
		t.Fatalf("ports %v allow %v", c.TCPPorts, c.Allow)
	}
	if len(c.Servers) != 1 || fmt.Sprint(c.Servers[0].Deny) != "[1.2.3.0/24]" {
		t.Fatalf("servers: %+v", c.Servers)
	}
	if len(c.Actions) != 1 || c.Actions[0].ID != 1 {
		t.Fatalf("actions: %+v", c.Actions)
	}
}

func sampleShieldConfig(mode string) *ShieldConfig {
	st := defaultShield()
	st.Mode = mode
	return buildShieldConfig(st,
		[]*Allocation{{Port: 30117, ServerID: "aaaa1111", Protocol: "tcp+udp"}, {Port: 30118, ServerID: "bbbb2222", Protocol: "tcp"}},
		[]*FirewallRule{{CIDR: "198.51.100.0/24", Action: "deny"}, {CIDR: "203.0.113.10", Action: "allow"},
			{ServerID: "aaaa1111", CIDR: "192.0.2.0/24", Action: "allow"}, {ServerID: "bbbb2222", CIDR: "192.0.2.66/32", Action: "deny"}},
		25565, nil, []ShieldForward{{Node: "node-a", Proto: "tcp", Port: 25566, Addr: "10.42.0.12", Target: 25565},
			{Node: "node-a", Proto: "udp", Port: 19132, Addr: "10.42.0.13", Target: 19132}, {Node: "bad node", Proto: "tcp", Port: 1, Addr: "x", Target: 1}})
}

func TestNftScript(t *testing.T) {
	c := sampleShieldConfig("normal")
	if len(c.Forwards) != 2 {
		t.Fatalf("an invalid forward is dropped: %+v", c.Forwards)
	}
	for _, want := range []string{"tcp dport 25566 dnat ip to 10.42.0.12:25565", "udp dport 19132 dnat ip to 10.42.0.13:19132"} {
		if !strings.Contains(nftScript("kubepilot", c, nil), want) {
			t.Errorf("forward %q missing", want)
		}
	}
	other := *c
	other.Forwards = slices.Clone(c.Forwards)
	other.forNode("node-b")
	if len(other.Forwards) != 0 || strings.Contains(nftScript("kubepilot", &other, nil), "dnat") {
		t.Fatal("another node forwards nothing")
	}
	s := nftScript("kubepilot", sampleShieldConfig("normal"), []string{"srv_gone0000"})
	for _, want := range []string{
		"add chain inet kubepilot pre { type filter hook prerouting priority -150; policy accept; }",
		"add element inet kubepilot trusted { 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.168.0.0/16, 203.0.113.10/32 }",
		"add element inet kubepilot blocked { 198.51.100.0/24 }",
		"add element inet kubepilot tcp_ports { 25565, 30117, 30118 }",
		"add element inet kubepilot udp_ports { 30117 }",
		"add rule inet kubepilot pre ip saddr @trusted accept",
		`add rule inet kubepilot game_tcp tcp dport { 30117 } ip saddr != { 192.0.2.0/24 } counter name "srv_aaaa1111" drop`,
		`add rule inet kubepilot game_udp udp dport { 30117 } ip saddr != { 192.0.2.0/24 } counter name "srv_aaaa1111" drop`,
		`add rule inet kubepilot game_tcp tcp dport { 30118 } ip saddr { 192.0.2.66/32 } counter name "srv_bbbb2222" drop`,
		`update @rl_tcp { ip saddr limit rate over 15/second burst 30 packets } add @banned { ip saddr timeout 600s }`,
		`add @conns { ip saddr ct count over 30 }`,
		`update @rl_udp { ip saddr limit rate over 1500/second burst 3000 packets }`,
		"delete counter inet kubepilot srv_gone0000",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing: %s", want)
		}
	}
	if strings.Contains(s, "srv_bbbb2222\" drop\nadd rule inet kubepilot game_udp udp dport") {
		t.Error("a TCP-only server gets no UDP rule")
	}
	// flushes come before the counter deletions, which come before the rules
	if strings.Index(s, "flush chain inet kubepilot game_udp") > strings.Index(s, "delete counter") || strings.Index(s, "delete counter") > strings.Index(s, "add rule") {
		t.Error("order: flush the chains, then delete stale counters, then add rules")
	}
	off := nftScript("kubepilot", sampleShieldConfig("off"), nil)
	if strings.Contains(off, "rl_tcp {") && strings.Contains(off, "limit rate") {
		t.Error("mode off: firewall rules only, no rate limit")
	}
	if !strings.Contains(off, `ip saddr @blocked counter name "blocked" drop`) || !strings.Contains(off, `counter name "srv_aaaa1111" drop`) {
		t.Error("mode off keeps the firewall rules")
	}

	// the real nft accepts the script (dry run: nothing is applied), when the test can use it
	if _, err := exec.LookPath("nft"); err != nil || os.Geteuid() != 0 {
		t.Log("nft not available (or not root): syntax not checked by nft")
		return
	}
	for _, mode := range []string{"normal", "strict", "off"} {
		script := nftScript("kp_test_dryrun", sampleShieldConfig(mode), nil)
		cmd := exec.Command("nft", "-c", "-f", "-")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("nft -c (%s): %v\n%s\n%s", mode, err, out, script)
		}
	}
}

func TestParseNftBans(t *testing.T) {
	raw := `{"nftables": [{"metainfo": {"version": "1.1.3"}}, {"set": {"family": "inet", "name": "banned", "table": "kubepilot", "type": "ipv4_addr",
		"elem": [{"elem": {"val": "192.0.2.67", "timeout": 120, "expires": 119}}, {"elem": {"val": "192.0.2.66", "timeout": 3600, "expires": 3599}}, "192.0.2.70"]}}]}`
	bans := parseNftBans(raw)
	if len(bans) != 3 || bans[0].IP != "192.0.2.66" || bans[0].Expires != 3599 || bans[1].IP != "192.0.2.67" || bans[2].IP != "192.0.2.70" {
		t.Fatalf("bans: %+v", bans)
	}
	if len(parseNftBans("not json")) != 0 {
		t.Fatal("garbage: no ban")
	}
}

// fakeNft records the scripts the agent runs.
type fakeNft struct {
	scripts []string
	fail    func(stdin string, args []string) bool
}

func (f *fakeNft) run(_ context.Context, stdin string, args ...string) (string, error) {
	if f.fail != nil && f.fail(stdin, args) {
		return "", errors.New("nft: failed")
	}
	if len(args) > 0 && args[0] == "-j" {
		return `{"nftables": [{"counter": {"name": "srv_gone0000", "packets": 3}}, {"counter": {"name": "blocked", "packets": 9}}]}`, nil
	}
	f.scripts = append(f.scripts, strings.TrimSpace(stdin+" "+strings.Join(args, " ")))
	return "", nil
}

func TestShieldAgentApply(t *testing.T) {
	f := &fakeNft{}
	ag := &shieldAgent{table: "kubepilot", nft: f.run}
	c := sampleShieldConfig("normal")
	c.Actions = []ShieldAction{
		{ID: 2, Op: "unban", IP: "192.0.2.5", Seconds: 1, At: now()},
		{ID: 1, Op: "ban", IP: "192.0.2.5", Seconds: 3600, At: now() - 600},
		{ID: 3, Op: "ban", IP: "192.0.2.6", Seconds: 60, At: now() - 120}, // already over
	}
	if err := ag.apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(f.scripts, "\n")
	if !strings.Contains(f.scripts[0], "delete counter inet kubepilot srv_gone0000") {
		t.Fatalf("the counter of a server without rules is removed:\n%s", f.scripts[0])
	}
	// bans run in order: ban (with the time left), then unban; the expired ban isn't added again
	iBan := strings.Index(all, "add element inet kubepilot banned { 192.0.2.5 timeout 3000s }")
	iUnban := strings.LastIndex(all, "delete element inet kubepilot banned { 192.0.2.5 }")
	if iBan < 0 || iUnban < iBan || strings.Contains(all, "add element inet kubepilot banned { 192.0.2.6") {
		t.Fatalf("actions:\n%s", all)
	}
	if ag.lastAction != 3 || !ag.applied {
		t.Fatalf("lastAction %d applied %v", ag.lastAction, ag.applied)
	}
	// the same actions aren't run twice
	f.scripts = nil
	if err := ag.apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(f.scripts) != 1 {
		t.Fatalf("only the configuration again: %v", f.scripts)
	}

	// a script refused (sets of an older version): the table is deleted and created again
	f2 := &fakeNft{}
	tries := 0
	f2.fail = func(stdin string, args []string) bool {
		if strings.HasPrefix(stdin, "add table") {
			tries++
			return tries == 1
		}
		return false
	}
	ag2 := &shieldAgent{table: "kubepilot", nft: f2.run}
	if err := ag2.apply(context.Background(), sampleShieldConfig("normal")); err != nil {
		t.Fatal(err)
	}
	if tries != 2 || !strings.Contains(strings.Join(f2.scripts, "\n"), "delete table inet kubepilot") {
		t.Fatalf("recreate: tries %d, %v", tries, f2.scripts)
	}
}

func TestShieldAPI(t *testing.T) {
	app, db := newTestApp(t, false)
	app.cfg.Shield = true
	admin, _ := db.CreateUser("root", "root@example.com", "root-password-1", true)
	owner, _ := db.CreateUser("alice", "alice@example.com", "alice-password-1", false)
	helper, _ := db.CreateUser("bob", "bob@example.com", "bob-password-123", false)
	_ = admin
	if err := app.seedTemplates(); err != nil {
		t.Fatal(err)
	}
	var tid int64
	db.QueryRow(`SELECT id FROM templates LIMIT 1`).Scan(&tid)
	srv := &Server{ID: "fw12ab34", Name: "s", OwnerID: owner.ID, TemplateID: tid, Env: map[string]string{}, CreatedAt: now()}
	if err := db.InsertServer(srv); err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO server_users(server_id,user_id,permissions,created_at) VALUES(?,?,?,?)`, srv.ID, helper.ID, "files", now())

	h := app.routes()
	root := &testClient{t: t, h: h}
	root.do("POST", "/api/auth/login", map[string]string{"login": "root", "password": "root-password-1"})
	alice := &testClient{t: t, h: h, ip: "203.0.113.8"}
	alice.do("POST", "/api/auth/login", map[string]string{"login": "alice", "password": "alice-password-1"})
	bob := &testClient{t: t, h: h, ip: "203.0.113.9"}
	bob.do("POST", "/api/auth/login", map[string]string{"login": "bob", "password": "bob-password-123"})

	code, g := root.do("POST", "/api/admin/firewall/rules", map[string]string{"cidr": "198.51.100.0/24", "action": "deny", "note": "botnet"})
	if code != 200 || g["cidr"] != "198.51.100.0/24" {
		t.Fatalf("global rule: %d %v", code, g)
	}
	if code, _ := root.do("POST", "/api/admin/firewall/rules", map[string]string{"cidr": "198.51.100.9/24", "action": "deny"}); code != 409 {
		t.Fatalf("same range written differently is a duplicate: %d", code)
	}
	for _, bad := range []map[string]string{{"cidr": "2001:db8::/32", "action": "deny"}, {"cidr": "1.0.0.0/4", "action": "deny"}, {"cidr": "1.2.3.4", "action": "reject"}} {
		if code, _ := root.do("POST", "/api/admin/firewall/rules", bad); code != 400 {
			t.Fatalf("%v: %d", bad, code)
		}
	}
	if code, _ := alice.do("POST", "/api/admin/firewall/rules", map[string]string{"cidr": "192.0.2.1", "action": "allow"}); code != 403 {
		t.Fatalf("a user can't add global rules: %d", code)
	}

	code, r := alice.do("POST", "/api/servers/fw12ab34/firewall", map[string]string{"cidr": "192.0.2.0/24", "action": "allow", "note": "friends"})
	if code != 200 {
		t.Fatalf("owner rule: %d %v", code, r)
	}
	if code, _ := bob.do("POST", "/api/servers/fw12ab34/firewall", map[string]string{"cidr": "192.0.2.9", "action": "deny"}); code != 403 {
		t.Fatalf("sub-user without the network permission: %d", code)
	}
	if code, _ := alice.do("DELETE", fmt.Sprintf("/api/servers/fw12ab34/firewall/%v", g["id"]), nil); code != 404 {
		t.Fatalf("a server route can't delete a global rule: %d", code)
	}
	code, list := alice.do("GET", "/api/servers/fw12ab34/firewall", nil)
	if code != 200 || list["available"] != true || len(list["rules"].([]any)) != 1 {
		t.Fatalf("server rules: %d %v", code, list)
	}

	if code, _ := root.do("PUT", "/api/admin/firewall", map[string]any{"mode": "panic", "tcpRate": 15, "conns": 30, "udpRate": 1500, "banMinutes": 10}); code != 400 {
		t.Fatalf("bad mode: %d", code)
	}
	if code, _ := root.do("PUT", "/api/admin/firewall", map[string]any{"mode": "strict", "tcpRate": 15, "conns": 30, "udpRate": 1500, "banMinutes": 10}); code != 200 {
		t.Fatalf("save: %d", code)
	}
	if db.ShieldSettings().Mode != "strict" {
		t.Fatal("settings not saved")
	}

	if code, _ := root.do("POST", "/api/admin/firewall/bans", map[string]any{"ip": "10.1.2.3", "minutes": 60}); code != 400 {
		t.Fatalf("banning a private address does nothing: %d", code)
	}
	if code, _ := root.do("POST", "/api/admin/firewall/bans", map[string]any{"ip": "192.0.2.66", "minutes": 60}); code != 200 {
		t.Fatalf("ban: %d", code)
	}
	if code, _ := root.do("DELETE", "/api/admin/firewall/bans/192.0.2.66", nil); code != 200 {
		t.Fatalf("unban: %d", code)
	}
	acts := app.shieldActions()
	if len(acts) != 2 || acts[0].Op != "ban" || acts[0].Seconds != 3600 || acts[1].Op != "unban" || acts[1].ID <= acts[0].ID {
		t.Fatalf("actions: %+v", acts)
	}

	// the configuration the agents get
	allocs := []*Allocation{{Port: 30150, ServerID: "fw12ab34", Protocol: "tcp"}}
	rules, _ := db.FirewallRules("*")
	c := buildShieldConfig(db.ShieldSettings(), allocs, rules, 0, acts, nil)
	if c.Mode != "strict" || c.TCPRate != 5 || fmt.Sprint(c.Deny) != "[198.51.100.0/24]" || len(c.Servers) != 1 || len(c.Actions) != 2 {
		t.Fatalf("config: %+v", c)
	}

	// deleting the server deletes its rules
	if _, err := db.Exec(`DELETE FROM servers WHERE id=?`, srv.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := db.FirewallRules("fw12ab34"); len(left) != 0 {
		t.Fatalf("rules of a deleted server: %v", left)
	}

	// without the chart option, server owners can't add rules
	app.cfg.Shield = false
	if code, _ := root.do("POST", "/api/admin/firewall/bans", map[string]any{"ip": "192.0.2.66", "minutes": 60}); code != 400 {
		t.Fatalf("bans need the agents: %d", code)
	}
}
