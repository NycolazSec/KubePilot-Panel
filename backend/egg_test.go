package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testEgg = `{
  "meta": {"version": "PTDL_v2"},
  "name": "Paper (test)", "author": "tester@example.com", "description": "A test egg\r\nfor Minecraft",
  "features": ["eula"],
  "docker_images": {"Java 17": "ghcr.io/pterodactyl/yolks:java_17", "Java 21": "ghcr.io/pterodactyl/yolks:java_21"},
  "startup": "java -Xms128M -Xmx{{SERVER_MEMORY}}M -Dterminal.jline=false -jar {{SERVER_JARFILE}}",
  "config": {"files": "{\"server.properties\":{\"parser\":\"properties\"}}", "startup": "{}", "logs": "{}", "stop": "stop"},
  "scripts": {"installation": {"script": "#!/bin/ash\r\napk add curl\r\ncurl -o server.jar https://example.com/paper.jar", "container": "ghcr.io/pterodactyl/installers:alpine", "entrypoint": "ash"}},
  "variables": [
    {"name": "Server Jar File", "env_variable": "SERVER_JARFILE", "default_value": "server.jar", "user_viewable": true, "user_editable": true, "rules": "required|regex:/^([\\w\\d._-]+)(\\.jar)$/"},
    {"name": "Version", "env_variable": "MC_VERSION", "default_value": "latest", "user_viewable": true, "user_editable": true, "rules": "nullable|string|max:20"},
    {"name": "Type", "env_variable": "BUILD_TYPE", "default_value": "release", "user_viewable": true, "user_editable": false, "rules": "required|string|in:release,snapshot"},
    {"name": "Players", "env_variable": "MAX_PLAYERS", "default_value": "20", "user_viewable": true, "user_editable": true, "rules": "nullable|numeric"},
    {"name": "Bad", "env_variable": "bad key", "default_value": "", "user_viewable": true, "user_editable": true, "rules": ""}
  ]
}`

func TestConvertEgg(t *testing.T) {
	tpl, warnings, err := convertEgg([]byte(testEgg))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Slug != "paper-test" || tpl.Images[0].Name != "Java 21" || tpl.Images[1].Name != "Java 17" || len(tpl.Images) != 2 {
		t.Errorf("slug/images: %q %+v", tpl.Slug, tpl.Images)
	}
	if tpl.Startup != "java -Xms128M -Xmx${SERVER_MEMORY}M -Dterminal.jline=false -jar ${SERVER_JARFILE}" || !tpl.StartupEnv {
		t.Errorf("startup: %q", tpl.Startup)
	}
	if !tpl.InstallAsRoot || tpl.InstallShell != "ash" || tpl.InstallPath != "/mnt/server" || strings.Contains(tpl.InstallScript, "\r") {
		t.Errorf("install: root=%v shell=%q path=%q", tpl.InstallAsRoot, tpl.InstallShell, tpl.InstallPath)
	}
	if tpl.StopCommand != "stop" || tpl.Features[0] != featureMinecraftJava || tpl.Ports[0].Port != 25565 {
		t.Errorf("stop/features/ports: %q %v %+v", tpl.StopCommand, tpl.Features, tpl.Ports)
	}
	if len(tpl.Vars) != 4 {
		t.Fatalf("invalid env keys must be dropped: %+v", tpl.Vars)
	}
	byKey := map[string]TemplateVar{}
	for _, v := range tpl.Vars {
		byKey[v.Key] = v
	}
	if p := byKey["SERVER_JARFILE"].Pattern; p != `^([\w\d._-]+)(\.jar)$` {
		t.Errorf("regex rule: %q", p)
	}
	if o := byKey["BUILD_TYPE"].Options; strings.Join(o, ",") != "release,snapshot" {
		t.Errorf("in: rule: %v", o)
	}
	if p := byKey["MAX_PLAYERS"].Pattern; p != `^$|^-?[0-9]+$` {
		t.Errorf("optional numeric: %q", p)
	}
	if len(warnings) != 2 {
		t.Errorf("config files + EULA warnings expected: %v", warnings)
	}
	for _, bad := range []string{`{}`, `{"meta":{"version":"PTDL_v2"},"name":"x"}`, `not json`} {
		if _, _, err := convertEgg([]byte(bad)); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestEggStartup(t *testing.T) {
	cases := map[string]string{
		"./server -port {{SERVER_PORT}}":                   "./server -port ${SERVER_PORT}",
		"./a && ./b {{server.build.default.port}}":         "./a && ./b ${SERVER_PORT}",
		"java -Xmx{{server.build.memory}}M -jar x.jar":     "java -Xmx${SERVER_MEMORY}M -jar x.jar",
		"./srv +ip {{server.build.default.ip}}":            "./srv +ip ${SERVER_IP}",
		"./srv {{server.build.env.WORLD}} {{weird.thing}}": "./srv ${WORLD} {{weird.thing}}",
	}
	for in, want := range cases {
		if got := eggStartup(in); got != want {
			t.Errorf("eggStartup(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEggURL(t *testing.T) {
	ok := map[string]string{
		"https://raw.githubusercontent.com/pelican-eggs/minecraft/main/java/paper/egg-paper.json": "https://raw.githubusercontent.com/pelican-eggs/minecraft/main/java/paper/egg-paper.json",
		"https://github.com/pelican-eggs/minecraft/blob/main/java/paper/egg-paper.json":           "https://raw.githubusercontent.com/pelican-eggs/minecraft/main/java/paper/egg-paper.json",
	}
	for in, want := range ok {
		if got, err := eggURL(in); err != nil || got != want {
			t.Errorf("eggURL(%q) = %q, %v", in, got, err)
		}
	}
	if got, _ := eggURL("https://raw.githubusercontent.com/a/b/main/my egg.json?token=x#f"); got != "https://raw.githubusercontent.com/a/b/main/my%20egg.json" {
		t.Errorf("query and fragment must be dropped, path escaped: %q", got)
	}
	for _, bad := range []string{"http://raw.githubusercontent.com/a/b/c/d.json", "https://evil.com/egg.json", "https://169.254.169.254/latest", "https://github.com/a/b",
		"file:///etc/passwd", "https://raw.githubusercontent.com:8443/a/b/c/d.json", "https://user@raw.githubusercontent.com/a/b/c/d.json", "https://raw.githubusercontent.com.evil.com/a"} {
		if _, err := eggURL(bad); err == nil {
			t.Errorf("%q must be refused (only GitHub over https)", bad)
		}
	}
}

func TestStopScript(t *testing.T) {
	if s := stopScript("stop"); !strings.Contains(s, "printf '%s\\n' 'stop' > /proc/1/fd/0") || !strings.Contains(s, "while kill -0 1") {
		t.Errorf("stop command: %q", s)
	}
	if s := stopScript("^C"); !strings.Contains(s, "kill -INT 1") {
		t.Errorf("^C must be SIGINT: %q", s)
	}
	if s := stopScript("say bye'; rm -rf /; echo '"); !strings.Contains(s, `'say bye'\''; rm -rf /; echo '\'''`) {
		t.Errorf("the stop command must be quoted, never interpreted: %q", s)
	}
}

func TestConvertPelicanV3Egg(t *testing.T) {
	v3 := `{"meta": {"version": "PLCN_v3"}, "name": "AlwaysAuth", "description": "Minecraft proxy", "tags": ["minecraft", "proxy"],
	  "docker_images": {"Java 21": "ghcr.io/pelican-eggs/yolks:java_21"}, "startup": null,
	  "startup_commands": {"Default": "java -Xmx{{SERVER_MEMORY}}M -jar {{JARFILE}}", "Other": "java -jar x.jar"},
	  "config": {"stop": "end"}, "scripts": {"installation": {"script": "echo hi", "container": "ghcr.io/pelican-eggs/installers:debian", "entrypoint": "bash"}},
	  "variables": [{"name": "Jar", "env_variable": "JARFILE", "default_value": "a.jar", "user_viewable": true, "user_editable": true, "rules": ["required", "string", "in:a.jar,b.jar"]}]}`
	tpl, _, err := convertEgg([]byte(v3))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Startup != "java -Xmx${SERVER_MEMORY}M -jar ${JARFILE}" || strings.Join(tpl.Vars[0].Options, ",") != "a.jar,b.jar" {
		t.Errorf("PLCN_v3: startup %q, options %v", tpl.Startup, tpl.Vars[0].Options)
	}
	if len(tpl.Features) != 0 || tpl.Ports[0].Port != 25565 {
		t.Errorf("a Minecraft proxy gets the Minecraft port but not the players page: %v %+v", tpl.Features, tpl.Ports)
	}
}

func TestEggOverhead(t *testing.T) {
	for mb, want := range map[int]int{1024: 256, 2048: 307, 4096: 409, 8192: 409} {
		if got := eggOverhead(mb); got != want {
			t.Errorf("eggOverhead(%d) = %d, want %d", mb, got, want)
		}
	}
}

func TestEggPodSpec(t *testing.T) {
	tpl, _, err := convertEgg([]byte(testEgg))
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{ID: "abc12345", Startup: tpl.Startup + " $(id)", MemoryMB: 2048, Env: map[string]string{}}
	sp := Spec{Server: srv, Template: tpl, Settings: Settings{Timezone: "UTC"}}
	pod := sp.podTemplate()
	c := pod.Spec.Containers[0]
	if len(c.Command) != 0 {
		t.Errorf("egg images run the startup from their own entrypoint, the command must not be overridden: %v", c.Command)
	}
	env := map[string]string{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}
	if env["STARTUP"] != strings.ReplaceAll(srv.Startup, "$", "$$") || env["P_SERVER_UUID"] != srv.ID {
		t.Errorf("STARTUP must be set literally (no Kubernetes $(VAR) expansion): %q", env["STARTUP"])
	}
	if c.Lifecycle == nil || c.Lifecycle.PreStop == nil || !strings.Contains(c.Lifecycle.PreStop.Exec.Command[2], "'stop'") {
		t.Error("the egg's stop command must be typed before the pod stops")
	}
	if got := c.Resources.Limits.Memory().Value() >> 20; got != 2048+307 {
		t.Errorf("eggs get Wings' memory headroom: %d MiB", got)
	}
	if sc := pod.Spec.SecurityContext; sc.RunAsUser == nil || *sc.RunAsUser == 0 {
		t.Error("the game itself never runs as root, even from an egg")
	}
}

func TestPropertiesOf(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.properties"), []byte("#Minecraft\nenable-rcon=true\nrcon.password=p\\:a\\\\ss\nrcon.port = 25580\nmotd=hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	p := propertiesOf(root, "enable-rcon", "rcon.password", "rcon.port")
	if p["enable-rcon"] != "true" || p["rcon.password"] != `p:a\ss` || p["rcon.port"] != "25580" || p["motd"] != "" {
		t.Errorf("properties: %v", p)
	}
}

func TestEggRedirectsStayOnGitHub(t *testing.T) {
	for _, u := range []string{"https://raw.githubusercontent.com/a/b/c.json", "https://api.github.com/repos/x"} {
		req, _ := http.NewRequest("GET", u, nil)
		if err := eggHTTP.CheckRedirect(req, nil); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"https://evil.example/egg.json", "http://raw.githubusercontent.com/a", "https://169.254.169.254/"} {
		req, _ := http.NewRequest("GET", u, nil)
		if eggHTTP.CheckRedirect(req, nil) == nil {
			t.Errorf("redirect to %s must not be followed", u)
		}
	}
}
