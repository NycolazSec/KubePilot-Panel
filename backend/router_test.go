package main

import "testing"

func TestHostnames(t *testing.T) {
	app, db := newTestApp(t, false)
	app.cfg.MinecraftRouter = true
	java := &Template{Slug: "minecraft-java"}
	st := Settings{MinecraftDomain: "mc.example.com"}
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil { // bare server rows, without owner nor template
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO servers(id,name,owner_id,template_id,image,memory_mb,cpu_millis,disk_mb,created_at,hostname) VALUES('aaaa1111','Survie',0,0,'x',1,1,1,0,'survie')`); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"Survie":                "bbbb2222", // taken by another server → its ID
		"Créatif entre potes !": "cr-atif-entre-potes",
		"Skyblock":              "skyblock",
		"---":                   "bbbb2222",
		"a very very long name for a minecraft server": "a-very-very-long-name-for-a-mine",
	} {
		if got := app.freeHostname(name, "bbbb2222"); got != want {
			t.Errorf("freeHostname(%q) = %q, want %q", name, got, want)
		}
	}
	if got := app.freeHostname("Survie", "aaaa1111"); got != "survie" {
		t.Errorf("a server keeps its own hostname, got %q", got)
	}
	s := &Server{ID: "aaaa1111", Hostname: "survie"}
	if got := app.routedHost(s, java, st); got != "survie.mc.example.com" {
		t.Errorf("routedHost = %q", got)
	}
	if app.routedHost(s, &Template{Slug: "nodejs"}, st) != "" || app.routedHost(s, java, Settings{}) != "" {
		t.Error("only Minecraft Java servers with a domain are routed")
	}
	app.cfg.MinecraftRouter = false
	if app.routedHost(s, java, st) != "" {
		t.Error("no router, no routed address")
	}
	for _, d := range []string{"mc.example.com", "play.nycolazsec.com"} {
		if !domainRe.MatchString(d) {
			t.Errorf("%s must be a valid domain", d)
		}
	}
	for _, d := range []string{"localhost", "-bad.com", "a..b.com", "evil.com/x", "Mc.Example.Com"} {
		if domainRe.MatchString(d) {
			t.Errorf("%s must be refused", d)
		}
	}
	if _, err := db.Exec(`INSERT INTO servers(id,name,owner_id,template_id,image,memory_mb,cpu_millis,disk_mb,created_at,hostname) VALUES('cccc3333','x',0,0,'x',1,1,1,0,'survie')`); err == nil {
		t.Error("two servers can't share a hostname (unique index)")
	}
}

func TestAssignMissingHostnames(t *testing.T) {
	app, db := newTestApp(t, false)
	app.cfg.MinecraftRouter = true
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	java := &Template{Slug: "minecraft-java", Name: "MC", Images: []ImageOption{{Image: "itzg/minecraft-server"}}, DataPath: "/data", RunAsUser: 1000}
	node := &Template{Slug: "nodejs", Name: "Node", Images: []ImageOption{{Image: "node:22"}}, DataPath: "/app", RunAsUser: 1000}
	for _, tpl := range []*Template{java, node} {
		if err := db.SaveTemplate(tpl); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range [][4]any{{"aaaa1111", "Survie", java.ID, ""}, {"bbbb2222", "Bot", node.ID, ""}, {"cccc3333", "Créa", java.ID, "crea-custom"}} {
		if _, err := db.Exec(`INSERT INTO servers(id,name,owner_id,template_id,image,memory_mb,cpu_millis,disk_mb,created_at,hostname) VALUES(?,?,0,?,'x',1,1,1,0,?)`, s[0], s[1], s[2], s[3]); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SaveSettings(Settings{MinecraftDomain: "mc.example.com"}); err != nil {
		t.Fatal(err)
	}
	app.assignMissingHostnames()
	want := map[string]string{"aaaa1111": "survie", "bbbb2222": "", "cccc3333": "crea-custom"}
	for id, h := range want {
		var got string
		_ = db.QueryRow(`SELECT hostname FROM servers WHERE id=?`, id).Scan(&got)
		if got != h {
			t.Errorf("%s: hostname %q, want %q", id, got, h)
		}
	}
}

func TestInternalOnlyIsNeverRouted(t *testing.T) {
	app, _ := newTestApp(t, false)
	app.cfg.MinecraftRouter = true
	s := &Server{ID: "aaaa1111", Hostname: "survie", InternalOnly: true}
	if got := app.routedHost(s, &Template{Slug: "minecraft-java"}, Settings{MinecraftDomain: "mc.example.com"}); got != "" {
		t.Errorf("an internal-only server must not get a public address, got %q", got)
	}
}
