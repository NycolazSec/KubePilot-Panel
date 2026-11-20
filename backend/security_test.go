package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A FIFO planted by a game server must never block the panel.
func TestPlantedFIFODoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "trap"), 0o644); err != nil {
		t.Skip("mkfifo unsupported:", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	done := make(chan [2]error, 1)
	go func() {
		_, _, rerr := openRegular(root, "trap")
		_, werr := createRegular(root, "trap", 0o644)
		done <- [2]error{rerr, werr}
	}()
	select {
	case errs := <-done:
		if errs[0] == nil || errs[1] == nil {
			t.Fatalf("a FIFO must be refused, got %v", errs)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("opening a FIFO blocked the panel")
	}
}

func TestQuotaWriterAndOverQuota(t *testing.T) {
	var buf bytes.Buffer
	q := &quotaWriter{w: &buf, left: 10}
	if _, err := q.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Write([]byte("123456")); err == nil {
		t.Fatal("writing past the quota must fail")
	}
	if overQuota(1100<<20, 1000) || !overQuota(1300<<20, 1000) || overQuota(1<<40, 0) {
		t.Fatal("overQuota: 10 % (min 256 MB) of tolerance, 0 = unlimited")
	}
}

func newTestApp(t *testing.T, secure bool) (*App, *DB) {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	return newApp(Config{SecureCookies: secure}, db, nil), db
}

func TestSecurityHeadersAndHostCookie(t *testing.T) {
	app, db := newTestApp(t, true)
	if _, err := db.CreateUser("bob", "bob@example.com", "correct-horse", false); err != nil {
		t.Fatal(err)
	}
	h := app.routes()
	r := httptest.NewRequest("POST", "https://panel.example.com/api/auth/login", strings.NewReader(`{"login":"bob","password":"correct-horse"}`))
	r.Header.Set("X-KubePilot", "1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("login: %d", w.Code)
	}
	for k, want := range map[string]string{
		"Cross-Origin-Opener-Policy": "same-origin", "X-Frame-Options": "DENY", "Cache-Control": "no-store",
		"Strict-Transport-Security": "max-age=63072000; includeSubDomains",
	} {
		if got := w.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "object-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("weak CSP: %s", csp)
	}
	ck := w.Result().Cookies()
	if len(ck) != 1 || ck[0].Name != "__Host-kubepilot_session" || !ck[0].Secure || !ck[0].HttpOnly || ck[0].Path != "/" || ck[0].Domain != "" {
		t.Fatalf("session cookie must be __Host-, Secure, HttpOnly, Path=/: %+v", ck)
	}
}

func TestMandatoryTwoFactor(t *testing.T) {
	app, db := newTestApp(t, false)
	if _, err := db.CreateUser("root", "root@example.com", "correct-horse", true); err != nil {
		t.Fatal(err)
	}
	c := &testClient{t: t, h: app.routes()}
	c.do("POST", "/api/auth/login", map[string]string{"login": "root", "password": "correct-horse"})
	// an admin can't make 2FA mandatory without having it (would lock them out)
	st := db.Settings()
	st.TwoFactorPolicy = twoFactorAdmins
	if code, _ := c.do("PUT", "/api/admin/settings", st); code != 400 {
		t.Fatalf("policy without own 2FA: %d", code)
	}
	if err := db.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if code, _ := c.do("GET", "/api/admin/users", nil); code != 403 {
		t.Fatalf("admin without 2FA must be blocked when it's mandatory: %d", code)
	}
	if code, _ := c.do("GET", "/api/servers", nil); code != 403 {
		t.Fatalf("server list must be blocked too: %d", code)
	}
	code, me := c.do("GET", "/api/auth/me", nil)
	if code != 200 || me["twoFactorRequired"] != true {
		t.Fatalf("me must stay reachable and say 2FA is required: %d %v", code, me)
	}
	if code, _ := c.do("POST", "/api/account/2fa/setup", map[string]string{"password": "correct-horse"}); code != 200 {
		t.Fatalf("2FA setup must stay reachable: %d", code)
	}
}

func TestPerAccountLimitSparesKnownIPs(t *testing.T) {
	app, db := newTestApp(t, false)
	if _, err := db.CreateUser("carol", "carol@example.com", "correct-horse", false); err != nil {
		t.Fatal(err)
	}
	home := &testClient{t: t, h: app.routes(), ip: "198.51.100.1"}
	if code, _ := home.do("POST", "/api/auth/login", map[string]string{"login": "carol", "password": "correct-horse"}); code != 200 {
		t.Fatalf("first login: %d", code)
	}
	// password spraying from many IPs (each under the per-IP limit)
	last := 0
	for i := 0; i < 25; i++ {
		atk := &testClient{t: t, h: app.routes(), ip: fmt.Sprintf("192.0.2.%d", i+1)}
		last, _ = atk.do("POST", "/api/auth/login", map[string]string{"login": "carol", "password": "wrong"})
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("spraying must hit the per-account limit, last answer %d", last)
	}
	// the real user, from an IP they already signed in from, is not locked out
	home.cookie = ""
	if code, _ := home.do("POST", "/api/auth/login", map[string]string{"login": "carol", "password": "correct-horse"}); code != 200 {
		t.Fatalf("known IP must not be locked out: %d", code)
	}
}
