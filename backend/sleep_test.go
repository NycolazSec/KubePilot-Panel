package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRouterAnnotations(t *testing.T) {
	if got := routerAnnotations(Spec{}); len(got) != 0 {
		t.Errorf("a server that isn't routed gets no mc-router annotation, got %v", got)
	}
	awake := routerAnnotations(Spec{Routed: "survie.mc.example.com"})
	if awake[mcRouterAnnotation] != "survie.mc.example.com" || awake[mcAutoScaleUp] != "false" || awake[mcAutoScaleDown] != "false" {
		t.Errorf("a routed server that isn't asleep must not be started by mc-router: %v", awake)
	}
	if _, ok := awake[mcAutoScaleWait]; ok {
		t.Error("no wake timeout on a server that can't be woken up")
	}
	asleep := routerAnnotations(Spec{Routed: "survie.mc.example.com", Wakeable: true})
	if asleep[mcAutoScaleUp] != "true" || asleep[mcAutoScaleDown] != "false" || asleep[mcAutoScaleWait] != wakeWaitTimeout {
		t.Errorf("a sleeping server is started by mc-router, never stopped by it: %v", asleep)
	}
	for k := range asleep {
		found := false
		for _, owned := range routerAnnotationKeys {
			found = found || owned == k
		}
		if !found {
			t.Errorf("annotation %s isn't removed when the server's Service is updated", k)
		}
	}
}

func TestWakeable(t *testing.T) {
	app, _ := newTestApp(t, false)
	app.cfg.AutoSleep = true
	base := Server{ID: "aaaa1111", Asleep: true, AutoSleep: 10, DiskMB: 1024}
	if !app.wakeable(&base, "survie.mc.example.com") {
		t.Fatal("an asleep server with auto-sleep on can be woken up")
	}
	cases := map[string]func(s *Server) string{
		"not asleep (stopped by hand)": func(s *Server) string { s.Asleep = false; return "x.mc.example.com" },
		"auto-sleep turned off":        func(s *Server) string { s.AutoSleep = 0; return "x.mc.example.com" },
		"suspended":                    func(s *Server) string { s.Suspended = true; return "x.mc.example.com" },
		"not routed":                   func(s *Server) string { return "" },
	}
	for name, change := range cases {
		s := base
		if routed := change(&s); app.wakeable(&s, routed) {
			t.Errorf("%s: must not be woken up", name)
		}
	}
	app.disk["aaaa1111"] = 2048 << 20 // twice its limit
	if app.wakeable(&base, "survie.mc.example.com") {
		t.Error("a server over its disk quota must not be woken up")
	}
	delete(app.disk, "aaaa1111")
	app.cfg.AutoSleep = false
	if app.wakeable(&base, "survie.mc.example.com") {
		t.Error("without auto-sleep in the chart, mc-router can't start servers")
	}
}

func TestIdleTracker(t *testing.T) {
	tr := idleTracker{since: map[string]time.Time{}}
	t0 := time.Unix(1_800_000_000, 0)
	after := 10 * time.Minute
	if tr.observe("s", 0, true, t0, after) {
		t.Fatal("the first empty check only starts the countdown")
	}
	if tr.observe("s", 0, true, t0.Add(9*time.Minute), after) {
		t.Fatal("asleep too early")
	}
	if !tr.observe("s", 0, true, t0.Add(10*time.Minute), after) {
		t.Fatal("empty for the whole delay: goes to sleep")
	}
	// A player in the middle restarts the countdown.
	tr.forget("s")
	tr.observe("s", 0, true, t0, after)
	tr.observe("s", 2, true, t0.Add(5*time.Minute), after)
	if tr.observe("s", 0, true, t0.Add(11*time.Minute), after) || tr.observe("s", 0, true, t0.Add(20*time.Minute), after) {
		t.Fatal("the countdown restarts when a player was seen")
	}
	if !tr.observe("s", 0, true, t0.Add(21*time.Minute), after) {
		t.Fatal("empty for 10 minutes after the last player")
	}
	// A server that doesn't answer is never considered empty.
	tr.forget("s")
	tr.observe("s", 0, true, t0, after)
	if tr.observe("s", 0, false, t0.Add(30*time.Minute), after) || tr.emptyFor("s", t0.Add(30*time.Minute)) != 0 {
		t.Fatal("an unanswered ping restarts the countdown")
	}
}

func TestSleepingState(t *testing.T) {
	if got := sleepingState(State{Status: "offline"}, true).Status; got != "sleeping" {
		t.Errorf("stopped by auto-sleep: %q", got)
	}
	for _, st := range []string{"starting", "running", "stopping", "crashed", "installing"} {
		if got := sleepingState(State{Status: st}, true).Status; got != st {
			t.Errorf("%s must stay %s, got %q", st, st, got)
		}
	}
	if got := sleepingState(State{Status: "offline"}, false).Status; got != "offline" {
		t.Errorf("stopped by hand: %q", got)
	}
}

func TestSpecHash(t *testing.T) {
	tpl := &Template{Slug: "minecraft-java", DataPath: "/data", RunAsUser: 1000, Vars: []TemplateVar{{Key: "VERSION", Default: "LATEST"}, {Key: "TYPE", Default: "PAPER"}}}
	srv := &Server{ID: "aaaa1111", Image: "itzg/minecraft-server", MemoryMB: 2048, CPUMillis: 1000, Env: map[string]string{"VERSION": "1.21.4"}}
	sp := Spec{Server: srv, Template: tpl, Allocs: []*Allocation{{Port: 30000, TargetPort: 25565, Protocol: "tcp", IsPrimary: true}}}
	h := sp.hash()
	if h == "" || h != sp.hash() {
		t.Fatal("the hash must be stable")
	}
	srv.Env["VERSION"] = "1.21.5"
	if sp.hash() == h {
		t.Error("changing a variable while asleep must prepare the server again")
	}
	srv.Env["VERSION"] = "1.21.4"
	tpl.ConfigFiles = []ConfigFile{{File: "server.properties", Parser: "properties"}}
	if sp.hash() == h {
		t.Error("the template's configuration files are part of what a start writes")
	}
}

func TestAutoSleepEndpoint(t *testing.T) {
	app, db := newTestApp(t, false)
	app.cfg.MinecraftRouter, app.cfg.AutoSleep = true, true
	owner, err := db.CreateUser("steve", "steve@example.com", "correct-horse", false)
	if err != nil {
		t.Fatal(err)
	}
	java := &Template{Slug: "minecraft-java", Name: "MC", Images: []ImageOption{{Image: "itzg/minecraft-server"}}, DataPath: "/data", RunAsUser: 1000}
	node := &Template{Slug: "nodejs", Name: "Node", Images: []ImageOption{{Image: "node:22"}}, DataPath: "/app", RunAsUser: 1000}
	for _, tpl := range []*Template{java, node} {
		if err := db.SaveTemplate(tpl); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SaveSettings(Settings{MinecraftDomain: "mc.example.com"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Server{
		{ID: "aaaa1111", Name: "Survie", OwnerID: owner.ID, TemplateID: java.ID, Image: "x", Env: map[string]string{}, Hostname: "survie"},
		{ID: "bbbb2222", Name: "Bot", OwnerID: owner.ID, TemplateID: node.ID, Image: "x", Env: map[string]string{}},
	} {
		if err := db.InsertServer(s); err != nil {
			t.Fatal(err)
		}
	}
	cookie := sessionCookie(t, app, "steve", "correct-horse")
	put := func(id, body string) int {
		r := httptest.NewRequest("PUT", "/api/servers/"+id+"/auto-sleep", strings.NewReader(body))
		r.Header.Set("X-KubePilot", "1")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		app.routes().ServeHTTP(w, r)
		return w.Code
	}
	for body, want := range map[string]int{`{"minutes":3}`: 400, `{"minutes":2000}`: 400, `{"minutes":-1}`: 400, `{"minutes":15}`: 200} {
		if got := put("aaaa1111", body); got != want {
			t.Errorf("%s: %d, want %d", body, got, want)
		}
	}
	if s, _ := db.Server("aaaa1111"); s.AutoSleep != 15 {
		t.Errorf("auto-sleep not saved: %d", s.AutoSleep)
	}
	if got := put("bbbb2222", `{"minutes":15}`); got != 400 {
		t.Errorf("a server that mc-router can't wake up can't sleep: %d", got)
	}
	if err := db.SetAsleep("aaaa1111", true); err != nil {
		t.Fatal(err)
	}
	app.loadAsleep([]*Server{{ID: "aaaa1111", Asleep: true}})
	if got := put("aaaa1111", `{"minutes":0}`); got != 200 {
		t.Fatalf("turning auto-sleep off: %d", got)
	}
	if s, _ := db.Server("aaaa1111"); s.AutoSleep != 0 || s.Asleep || app.isAsleep("aaaa1111") {
		t.Error("turning auto-sleep off must also cancel the wake-up of a sleeping server")
	}
}

// sessionCookie signs in and returns the session cookie.
func sessionCookie(t *testing.T, app *App, login, password string) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"login":"`+login+`","password":"`+password+`"}`))
	r.Header.Set("X-KubePilot", "1")
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if strings.Contains(c.Name, "kubepilot_session") {
			return c
		}
	}
	t.Fatalf("sign-in failed: %d %s", w.Code, w.Body.String())
	return nil
}
