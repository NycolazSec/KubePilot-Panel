package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestAPIKeys(t *testing.T) {
	app, db := newTestApp(t, false)
	if _, err := db.CreateUser("admin", "admin@example.com", "correct-horse", true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser("dave", "dave@example.com", "correct-horse", false); err != nil {
		t.Fatal(err)
	}
	h := app.routes()
	session := func(login string) *testClient {
		c := &testClient{t: t, h: h}
		if code, _ := c.do("POST", "/api/auth/login", map[string]string{"login": login, "password": "correct-horse"}); code != 200 {
			t.Fatalf("login %s: %d", login, code)
		}
		return c
	}
	newKey := func(c *testClient, body map[string]any) (string, map[string]any) {
		t.Helper()
		body["password"] = "correct-horse"
		code, res := c.do("POST", "/api/account/api-keys", body)
		if code != 200 {
			t.Fatalf("create key %v: %d %v", body["name"], code, res)
		}
		return res["token"].(string), res["key"].(map[string]any)
	}
	key := func(tok string) *testClient { return &testClient{t: t, h: h, auth: "Bearer " + tok} }

	admin := session("admin")
	if code, _ := admin.do("POST", "/api/account/api-keys", map[string]any{"name": "x", "permissions": []string{"console"}, "password": "wrong"}); code != 400 {
		t.Fatalf("creating a key must require the password: %d", code)
	}
	full, k := newKey(admin, map[string]any{"name": "bot", "permissions": []string{"console", "power", "admin"}})
	if !strings.HasPrefix(full, "kp_") || len(full) != 43 || k["prefix"] != full[:9] {
		t.Fatalf("token format: %q, prefix %v", full, k["prefix"])
	}
	_, list := admin.do("GET", "/api/account/api-keys", nil)
	if strings.Contains(fmt.Sprint(list), full) {
		t.Fatal("the token must never be listed again")
	}

	// a key works without cookie nor CSRF header, and acts as its owner
	if code, me := key(full).do("GET", "/api/auth/me", nil); code != 200 || me["user"].(map[string]any)["username"] != "admin" {
		t.Fatalf("me with a key: %d %v", code, me)
	}
	if code, _ := key(full).do("POST", "/api/admin/users", map[string]any{"username": "erin", "email": "erin@example.com", "password": "correct-horse"}); code != 200 {
		t.Fatalf("admin action with an admin key: %d", code)
	}
	acts, _ := db.ActivityList("", 5)
	if acts[0].Action != "admin.user.create" || acts[0].APIKey != "bot" {
		t.Fatalf("the activity log must record the key: %+v", acts[0])
	}

	// what a key can never do
	if code, _ := key(full).do("GET", "/api/account/api-keys", nil); code != 403 {
		t.Fatalf("a key must not manage keys: %d", code)
	}
	if code, _ := key(full).do("POST", "/api/account/2fa/setup", map[string]string{"password": "correct-horse"}); code != 403 {
		t.Fatalf("a key must not touch 2FA: %d", code)
	}
	if code, _ := key("kp_"+strings.Repeat("A", 40)).do("GET", "/api/servers", nil); code != 401 {
		t.Fatalf("unknown key: %d", code)
	}
	if code, _ := key("not-a-key").do("GET", "/api/servers", nil); code != 401 {
		t.Fatalf("malformed key: %d", code)
	}

	// scopes: no "admin" scope → no admin area, even for an admin
	consoleOnly, _ := newKey(admin, map[string]any{"name": "console", "permissions": []string{"console"}})
	if code, _ := key(consoleOnly).do("GET", "/api/admin/users", nil); code != 403 {
		t.Fatalf("key without admin scope: %d", code)
	}
	if got := keyPerms([]string{"console", "power", "files"}, &APIKey{Permissions: []string{"console", "backups"}}); fmt.Sprint(got) != "[console]" {
		t.Fatalf("a key never adds rights: %v", got)
	}

	// read-only
	ro, _ := newKey(admin, map[string]any{"name": "ro", "permissions": []string{"admin"}, "readOnly": true})
	if code, _ := key(ro).do("GET", "/api/admin/users", nil); code != 200 {
		t.Fatalf("read-only key, GET: %d", code)
	}
	if code, _ := key(ro).do("POST", "/api/admin/users", map[string]any{"username": "frank", "email": "f@example.com", "password": "correct-horse"}); code != 403 {
		t.Fatalf("read-only key, POST: %d", code)
	}

	// IP allow-list and expiry
	pinned, _ := newKey(admin, map[string]any{"name": "pinned", "permissions": []string{"console"}, "allowedIps": []string{"198.51.100.0/24"}})
	if code, _ := key(pinned).do("GET", "/api/servers", nil); code != 403 {
		t.Fatalf("key used from a non-allowed IP: %d", code)
	}
	from := key(pinned)
	from.ip = "198.51.100.9"
	if code, _ := from.do("GET", "/api/auth/me", nil); code != 200 {
		t.Fatalf("key used from an allowed IP: %d", code)
	}
	if code, _ := admin.do("POST", "/api/account/api-keys", map[string]any{"name": "bad", "permissions": []string{"console"}, "allowedIps": []string{"nope"}, "password": "correct-horse"}); code != 400 {
		t.Fatalf("invalid IP must be refused: %d", code)
	}
	if _, err := db.Exec(`UPDATE api_keys SET expires_at=1 WHERE name='console'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := key(consoleOnly).do("GET", "/api/servers", nil); code != 401 {
		t.Fatalf("expired key: %d", code)
	}

	// a regular user can't give a key the admin scope
	_, dk := newKey(session("dave"), map[string]any{"name": "mine", "permissions": []string{"console", "admin"}})
	if fmt.Sprint(dk["permissions"]) != "[console]" {
		t.Fatalf("admin scope must be dropped for non-admins: %v", dk["permissions"])
	}

	// rate limit per key
	last := 0
	for i := 0; i <= apiRatePerMin; i++ {
		last, _ = key(full).do("GET", "/api/auth/me", nil)
	}
	if last != 429 {
		t.Fatalf("request %d must be rate limited, got %d", apiRatePerMin+1, last)
	}

	// revoking a key
	id := fmt.Sprint(int(k["id"].(float64)))
	if code, _ := admin.do("DELETE", "/api/account/api-keys/"+id, nil); code != 200 {
		t.Fatalf("delete key: %d", code)
	}
	app.keyRate.reset(id)
	if code, _ := key(full).do("GET", "/api/auth/me", nil); code != 401 {
		t.Fatalf("a revoked key must stop working: %d", code)
	}
}
