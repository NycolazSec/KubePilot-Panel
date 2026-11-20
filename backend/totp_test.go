package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTOTPMatchesRFC6238(t *testing.T) {
	secret := []byte("12345678901234567890") // RFC 6238 appendix B, SHA-1
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		if got := totpCode(secret, ts/totpPeriod); got != want {
			t.Errorf("T=%d: got %s, want %s", ts, got, want)
		}
	}
}

func TestMatchTOTPDriftAndReplay(t *testing.T) {
	secretB32 := b32.EncodeToString([]byte("12345678901234567890"))
	secret, _ := b32.DecodeString(secretB32)
	now := time.Unix(1_700_000_000, 0)
	cur := now.Unix() / totpPeriod
	for _, d := range []int64{-1, 0, 1} {
		if _, ok := matchTOTP(secretB32, totpCode(secret, cur+d), now, 0); !ok {
			t.Errorf("code of step %+d should be accepted (clock drift)", d)
		}
	}
	if _, ok := matchTOTP(secretB32, totpCode(secret, cur+2), now, 0); ok {
		t.Error("a code two steps ahead must be refused")
	}
	if _, ok := matchTOTP(secretB32, totpCode(secret, cur), now, cur); ok {
		t.Error("an already used step must be refused (replay)")
	}
}

// testClient talks to a real KubePilot HTTP handler (no Kubernetes needed for accounts).
type testClient struct {
	t      *testing.T
	h      http.Handler
	cookie string
}

func (c *testClient) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.RemoteAddr = "203.0.113.7:1234"
	r.Header.Set("X-KubePilot", "1")
	r.Header.Set("Content-Type", "application/json")
	if c.cookie != "" {
		r.Header.Set("Cookie", c.cookie)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	for _, ck := range w.Result().Cookies() {
		if ck.Name == cookieName {
			c.cookie = ck.Name + "=" + ck.Value
		}
	}
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestTwoFactorFlow(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser("alice", "alice@example.com", "correct-horse", false); err != nil {
		t.Fatal(err)
	}
	app := newApp(Config{}, db, nil)
	c := &testClient{t: t, h: app.routes()}
	login := func() (int, map[string]any) {
		c.cookie = ""
		return c.do("POST", "/api/auth/login", map[string]string{"login": "alice", "password": "correct-horse"})
	}

	if code, _ := login(); code != 200 || c.cookie == "" {
		t.Fatalf("login without 2FA: %d", code)
	}
	if code, _ := c.do("POST", "/api/account/2fa/setup", map[string]string{"password": "wrong"}); code != 400 {
		t.Fatalf("setup must require the password, got %d", code)
	}
	code, setup := c.do("POST", "/api/account/2fa/setup", map[string]string{"password": "correct-horse"})
	secretB32, _ := setup["secret"].(string)
	if code != 200 || secretB32 == "" || !strings.HasPrefix(setup["qr"].(string), "data:image/png;base64,") {
		t.Fatalf("setup: %d %v", code, setup)
	}
	secret, _ := b32.DecodeString(secretB32)
	cur := time.Now().Unix() / totpPeriod
	if code, _ := c.do("POST", "/api/account/2fa/enable", map[string]string{"code": "000000"}); code != 400 {
		t.Fatalf("enable with a wrong code: %d", code)
	}
	code, en := c.do("POST", "/api/account/2fa/enable", map[string]string{"code": totpCode(secret, cur)})
	recovery, _ := en["recoveryCodes"].([]any)
	if code != 200 || len(recovery) != recoveryCodeCount {
		t.Fatalf("enable: %d %v", code, en)
	}

	// password alone no longer opens a session
	code, res := login()
	ticket, _ := res["ticket"].(string)
	if code != 200 || res["twoFactor"] != true || ticket == "" || c.cookie != "" {
		t.Fatalf("login with 2FA must return a ticket and no session: %d %v cookie=%q", code, res, c.cookie)
	}
	if code, _ := c.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": ticket, "code": totpCode(secret, cur)}); code != 401 {
		t.Fatalf("the code used to enable 2FA must not be accepted again (replay), got %d", code)
	}
	if code, _ := c.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": ticket, "code": totpCode(secret, cur+1)}); code != 200 || c.cookie == "" {
		t.Fatalf("login with a fresh TOTP code: %d", code)
	}
	if code, me := c.do("GET", "/api/auth/me", nil); code != 200 || me["user"].(map[string]any)["twoFactor"] != true {
		t.Fatalf("me: %d %v", code, me)
	}

	// recovery codes work once, with or without the dash
	rc := recovery[0].(string)
	_, res = login()
	if code, _ := c.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": res["ticket"].(string), "code": strings.ToUpper(strings.ReplaceAll(rc, "-", ""))}); code != 200 {
		t.Fatalf("login with a recovery code: %d", code)
	}
	_, res = login()
	if code, _ := c.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": res["ticket"].(string), "code": rc}); code != 401 {
		t.Fatalf("a recovery code must be single-use, got %d", code)
	}
	if n := db.RecoveryCodesLeft(1); n != recoveryCodeCount-1 {
		t.Fatalf("recovery codes left: %d", n)
	}

	// a ticket is bound to its IP and dies after too many attempts
	_, res = login()
	ticket = res["ticket"].(string)
	for i := 0; i < loginTicketTries; i++ {
		c.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": ticket, "code": "000000"})
	}
	if code, _ := c.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": ticket, "code": recovery[1].(string)}); code != 410 {
		t.Fatalf("ticket must be dead (410) after %d wrong codes, got %d", loginTicketTries, code)
	}

	// disabling needs the password and a second factor
	app.limiter.reset("203.0.113.7")
	_, res = login()
	c.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": res["ticket"].(string), "code": recovery[2].(string)})
	if code, _ := c.do("POST", "/api/account/2fa/disable", map[string]string{"password": "correct-horse", "code": "000000"}); code != 400 {
		t.Fatalf("disable without a valid code: %d", code)
	}
	if code, _ := c.do("POST", "/api/account/2fa/disable", map[string]string{"password": "correct-horse", "code": recovery[3].(string)}); code != 200 {
		t.Fatalf("disable: %d", code)
	}
	if code, res := login(); code != 200 || res["twoFactor"] == true || c.cookie == "" {
		t.Fatalf("after disabling, the password is enough again: %d %v", code, res)
	}
}
