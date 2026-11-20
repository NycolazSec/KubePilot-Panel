package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func reps(t *testing.T, pairs ...string) []replacement {
	t.Helper()
	out := []replacement{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, replacement{match: pairs[i], value: pairs[i+1]})
	}
	return out
}

func TestEditProperties(t *testing.T) {
	in := "#Minecraft server properties\nserver-ip=\nserver-port=25565\nmotd=A Minecraft Server\nquery.port = 25565\n"
	got := string(editProperties([]byte(in), reps(t, "server-ip", "0.0.0.0", "server-port", "30101", "query.port", "30101", "enable-query", "true")))
	want := "#Minecraft server properties\nserver-ip=0.0.0.0\nserver-port=30101\nmotd=A Minecraft Server\nquery.port=30101\nenable-query=true\n"
	if got != want {
		t.Errorf("properties:\n%s\nwant:\n%s", got, want)
	}
}

func TestEditLines(t *testing.T) {
	in := "name = 'x'\nhost = '127.0.0.1'\nport = 7788\n"
	got := string(editLines([]byte(in), reps(t, "host", "host = '0.0.0.0'", "port", "port = 30101", "missing", "never added")))
	if got != "name = 'x'\nhost = '0.0.0.0'\nport = 30101\n" {
		t.Errorf("file parser: %q", got)
	}
}

func TestEditINI(t *testing.T) {
	in := "; comment\nglobal=1\n\n[server]\nname = Old\nport=1\n\n[other]\nport=2\n"
	got := string(editINI([]byte(in), reps(t, "server.name", "New name", "server.port", "30101", "server.max", "8", "global", "2", "new.key", "v")))
	want := "; comment\nglobal=2\n\n[server]\nname = New name\nport=30101\nmax=8\n\n[other]\nport=2\n\n[new]\nkey=v\n"
	if got != want {
		t.Errorf("ini:\n%s\nwant:\n%s", got, want)
	}
}

func TestEditJSON(t *testing.T) {
	in := "{\n    \"zeta\": 1,\n    \"apps\": {\"http\": {\"servers\": {\"srv0\": {\"listen\": [\":80\"]}}}},\n    \"maxPlayers\": 10,\n    \"name\": \"a <b>\",\n    \"list\": [{\"addr\": \"localhost:1\"}, {\"addr\": \"x\"}]\n}\n"
	r := reps(t, "apps.http.servers.srv0.listen.0", ":30101", "maxPlayers", "32", "name", "1234", "new.nested", "true", "list[*].addr", "ignored", "made.0.up", "x")
	r[4].ifValue, r[4].value = "localhost", "srv"
	out, err := editJSON([]byte(in), r)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{`"zeta": 1,`, `"listen": [`, `":30101"`, `"maxPlayers": 32,`, `"name": "1234"`, `"nested": true`, `"addr": "srv:1"`, `"addr": "x"`, "\n    \"apps\""} {
		if !strings.Contains(got, want) {
			t.Errorf("json: missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "made") {
		t.Error("json: a list must never be invented")
	}
	if strings.Index(got, "zeta") > strings.Index(got, "apps") {
		t.Error("json: key order must be kept")
	}
	var v any
	if json.Unmarshal(out, &v) != nil {
		t.Error("json: output must stay valid")
	}
}

func TestEditYAML(t *testing.T) {
	in := "# BungeeCord config\nlisteners:\n- host: 0.0.0.0:25577 # the port\n  motd: hi\nservers:\n  lobby:\n    address: localhost:25565\n  pvp:\n    address: 10.0.0.2:25565\nonline_mode: true\nplayer_limit: -1\n"
	r := []replacement{{match: "listeners[0].host", value: "0.0.0.0:30101"}, {match: "servers.*.address", ifValue: "localhost", value: "srv-a"},
		{match: "player_limit", value: "20"}, {match: "online_mode", value: "false"}, {match: "new.key", value: "on"}}
	out, err := editYAML([]byte(in), r)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"# BungeeCord config", "host: 0.0.0.0:30101", "# the port", "address: srv-a:25565", "address: 10.0.0.2:25565", "online_mode: false", "player_limit: 20", "new:\n  key: \"on\""} {
		if !strings.Contains(got, want) {
			t.Errorf("yaml: missing %q in\n%s", want, got)
		}
	}
}

func TestEditXML(t *testing.T) {
	in := `<?xml version="1.0"?>
<MyConfigDedicated xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <SessionSettings>
    <GameMode>Creative</GameMode>
    <MaxPlayers>4</MaxPlayers>
    <OnlineMode/>
  </SessionSettings>
  <ServerPort>27016</ServerPort>
</MyConfigDedicated>
`
	out, err := editXML([]byte(in), reps(t, "MyConfigDedicated.SessionSettings.GameMode", "Survival", "MyConfigDedicated.ServerPort", "30101",
		"MyConfigDedicated.SessionSettings.OnlineMode", "PUBLIC & <safe>", "MyConfigDedicated.Missing", "x", "MyConfigDedicated.SessionSettings", "not a leaf"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("<GameMode>Creative<", "<GameMode>Survival<", "<ServerPort>27016<", "<ServerPort>30101<",
		"<OnlineMode/>", "<OnlineMode>PUBLIC &amp; &lt;safe&gt;</OnlineMode>").Replace(in)
	if string(out) != want {
		t.Errorf("xml:\n%s\nwant:\n%s", out, want)
	}
}

func TestResolveConfigValue(t *testing.T) {
	vars := map[string]string{"SERVER_PORT": "30101", "SERVER_MEMORY": "2048", "SRV_NAME": "My server"}
	for in, want := range map[string]string{
		"{{server.build.default.port}}":                    "30101",
		"0.0.0.0:{{server.allocations.default.port}}":      "0.0.0.0:30101",
		"{{server.build.env.SRV_NAME}} / {{env.SRV_NAME}}": "My server / My server",
		"{{server.environment.SRV_NAME}}":                  "My server",
		"{{ server.build.default.ip }}":                    "0.0.0.0",
	} {
		if got, ok := resolveConfigValue(in, vars); !ok || got != want {
			t.Errorf("%q → %q (%v), want %q", in, got, ok, want)
		}
	}
	if _, ok := resolveConfigValue("{{server.build.env.UNKNOWN}}", vars); ok {
		t.Error("an unknown variable must not be written as an empty value")
	}
}

func TestEggConfigFiles(t *testing.T) {
	raw := `{"config.yml": {"parser": "yaml", "find": {"listeners[0].host": "0.0.0.0:{{server.build.default.port}}",
	  "servers.*.address": {"127.0.0.1": "{{config.docker.interface}}", "localhost": "{{config.docker.interface}}"}}},
	  "/server.properties": {"parser": "properties", "find": {"server-ip": "0.0.0.0", "server-port": "{{server.build.default.port}}", "max": 20}},
	  "../evil": {"parser": "file", "find": {"a": "b"}}, "x.bin": {"parser": "exe", "find": {"a": "b"}}}`
	files, skipped := eggConfigFiles(raw)
	if len(files) != 2 || files[0].File != "config.yml" || files[1].File != "server.properties" {
		t.Fatalf("files: %+v", files)
	}
	if len(files[0].Find) != 1 || len(skipped) != 2 {
		t.Errorf("Docker network values must be left out: %+v %v", files[0].Find, skipped)
	}
	if f := files[1].Find; len(f) != 3 || f[0].Match != "server-ip" || f[2].Match != "max" || string(f[2].Value) != "20" {
		t.Errorf("order and literal values: %+v", f)
	}
}

func TestApplyConfigFiles(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(outside, "server.properties"), []byte("server-port=1\n"), 0o644)
	os.Symlink(filepath.Join(outside, "server.properties"), filepath.Join(dir, "link.properties"))
	syscall.Mkfifo(filepath.Join(dir, "fifo.json"), 0o644)
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	val := func(s string) json.RawMessage { b, _ := json.Marshal(s); return b }
	files := []ConfigFile{
		{File: "server.properties", Parser: "properties", Find: []ConfigReplace{{Match: "server-port", Value: val("{{server.build.default.port}}")}}},
		{File: "config/game.ini", Parser: "ini", Find: []ConfigReplace{{Match: "net.port", Value: val("{{server.build.default.port}}")}, {Match: "net.secret", Value: val("{{env.NOPE}}")}}},
		{File: "later.yml", Parser: "yaml", Find: []ConfigReplace{{Match: "listeners[0].host", Value: val("0.0.0.0")}}},
		{File: "absent.xml", Parser: "xml", Find: []ConfigReplace{{Match: "a.b", Value: val("x")}}},
		{File: "link.properties", Parser: "properties", Find: []ConfigReplace{{Match: "server-port", Value: val("666")}}},
		{File: "fifo.json", Parser: "json", Find: []ConfigReplace{{Match: "a", Value: val("b")}}},
	}
	changed, errs := applyConfigFiles(root, os.Getuid(), files, map[string]string{"SERVER_PORT": "30101"})
	if strings.Join(changed, ",") != "server.properties,config/game.ini" {
		t.Errorf("changed: %v (errors %v)", changed, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "server.properties")); string(b) != "server-port=30101\n" {
		t.Errorf("a missing properties file is created: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "config/game.ini")); string(b) != "[net]\nport=30101\n" {
		t.Errorf("ini created with known values only: %q", b)
	}
	for _, f := range []string{"absent.xml", "later.yml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s: structured files are left for the game to create", f)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "server.properties")); string(b) != "server-port=1\n" {
		t.Error("a symlink must not lead the edit outside the volume")
	}
	if len(errs) != 2 {
		t.Errorf("symlink and FIFO must be reported: %v", errs)
	}
	if again, _ := applyConfigFiles(root, os.Getuid(), files[:3], map[string]string{"SERVER_PORT": "30101"}); len(again) != 0 {
		t.Errorf("nothing changes the second time: %v", again)
	}
	if validateConfigFiles([]ConfigFile{{File: "../x", Parser: "file", Find: files[0].Find}}) == nil ||
		validateConfigFiles([]ConfigFile{{File: "/etc/x", Parser: "file", Find: files[0].Find}}) == nil {
		t.Error("paths must stay in the volume")
	}
}
