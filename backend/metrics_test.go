package main

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// a line of the text exposition format: a comment, or name{labels} value
var promLine = regexp.MustCompile(`^(# (HELP|TYPE) [a-z_]+ .+|[a-z_]+(\{[a-z_]+="([^"\\]|\\.)*"(,[a-z_]+="([^"\\]|\\.)*")*\})? -?[0-9.e+-]+|NaN)$`)

func TestMetricsFormat(t *testing.T) {
	app, db := newTestApp(t, false)
	app.cfg.MetricsToken = "s3cret"
	if _, err := db.CreateUser("owner", "owner@example.com", "correct-horse-1", false); err != nil {
		t.Fatal(err)
	}
	u, _ := db.UserByLogin("owner")
	if err := app.seedTemplates(); err != nil {
		t.Fatal(err)
	}
	tpls, _ := db.Templates()
	srv := &Server{ID: "abc123", Name: `Survie "entre potes"` + "\n" + `\o/`, OwnerID: u.ID, TemplateID: tpls[0].ID, Env: map[string]string{},
		MemoryMB: 2048, CPUMillis: 1500, DiskMB: 10240, CreatedAt: now()}
	if err := db.InsertServer(srv); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAllocation(&Allocation{Port: 30100, ServerID: "abc123", Protocol: "tcp", TargetPort: 25565, IsPrimary: true}); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	app.handleMetrics(w, r)
	if w.Code != 401 {
		t.Fatalf("a token is set: %d", w.Code)
	}
	r.Header.Set("Authorization", "Bearer s3cret")
	w = httptest.NewRecorder()
	app.handleMetrics(w, r)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatalf("metrics: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	out := w.Body.String()
	types := map[string]int{}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if !promLine.MatchString(line) {
			t.Errorf("not exposition format: %q", line)
		}
		if strings.HasPrefix(line, "# TYPE ") {
			types[strings.Fields(line)[2]]++
		}
	}
	for name, n := range types {
		if n != 1 {
			t.Errorf("%s declared %d times", name, n)
		}
	}
	for _, want := range []string{
		`kubepilot_info{version="dev"} 1`,
		`kubepilot_users 1`,
		`kubepilot_servers{state="offline"} 1`,
		`kubepilot_server_info{server="abc123",name="Survie \"entre potes\"\n\\o/",owner="owner",template="` + tpls[0].Slug + `",node=""} 1`,
		`kubepilot_server_state{server="abc123",name="Survie \"entre potes\"\n\\o/",state="offline"} 1`,
		`kubepilot_server_memory_limit_bytes{server="abc123",name="Survie \"entre potes\"\n\\o/"} 2147483648`,
		`kubepilot_server_cpu_limit_millicores{server="abc123",name="Survie \"entre potes\"\n\\o/"} 1500`,
		`kubepilot_ports_used{kind="nodeport"} 1`,
		`kubepilot_ports_available{kind="nodeport"} 200`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(out, "kubepilot_server_restarts{") {
		t.Error("no restart count for a server that isn't running")
	}

	p := &promWriter{}
	p.sample("x", 0.25)
	p.sample("y", 1e20)
	p.sample("z", -3)
	if got := p.b.String(); got != "x 0.25\ny 1e+20\nz -3\n" {
		t.Fatalf("values: %q", got)
	}
}
