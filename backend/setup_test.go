package main

import "testing"

func TestFirstStartSetup(t *testing.T) {
	app, db := newTestApp(t, false)
	h := app.routes()
	c := &testClient{t: t, h: h}

	if code, opts := c.do("GET", "/api/auth/options", nil); code != 200 || opts["setup"] != false {
		t.Fatalf("no token yet, no setup offered: %d %v", code, opts)
	}
	tok := app.initSetup()
	if len(tok) != 32 {
		t.Fatalf("token: %q", tok)
	}
	if _, opts := c.do("GET", "/api/auth/options", nil); opts["setup"] != true {
		t.Fatalf("setup must be offered: %v", opts)
	}
	in := map[string]string{"token": "wrong", "username": "owner", "email": "owner@example.com", "password": "correct-horse-1"}
	if code, _ := c.do("POST", "/api/setup", in); code != 403 {
		t.Fatalf("wrong token: %d", code)
	}
	in["token"], in["password"] = tok, "short"
	if code, _ := c.do("POST", "/api/setup", in); code != 400 || !app.setupNeeded() {
		t.Fatalf("weak password: refused, and the token still works: %d", code)
	}
	in["password"] = "correct-horse-1"
	if code, out := c.do("POST", "/api/setup", in); code != 200 || out["username"] != "owner" {
		t.Fatalf("setup: %d %v", code, out)
	}
	u, err := db.UserByLogin("owner")
	if err != nil || !u.IsAdmin {
		t.Fatalf("the first account is an administrator: %+v %v", u, err)
	}
	if app.setupNeeded() {
		t.Fatal("setup is over")
	}
	in["username"], in["email"] = "intruder", "intruder@example.com"
	if code, _ := c.do("POST", "/api/setup", in); code != 409 {
		t.Fatalf("the token works once: %d", code)
	}
	if code, _ := c.do("POST", "/api/auth/login", map[string]string{"login": "owner", "password": "correct-horse-1"}); code != 200 {
		t.Fatalf("sign in with the new account: %d", code)
	}
	if app.initSetup() != "" {
		t.Fatal("a panel with an account prints no token")
	}

	// an account created another way (CLI) ends the setup too
	app2, db2 := newTestApp(t, false)
	if app2.initSetup() == "" {
		t.Fatal("token expected")
	}
	if _, err := db2.CreateUser("cli", "cli@example.com", "correct-horse-1", true); err != nil {
		t.Fatal(err)
	}
	if app2.setupNeeded() {
		t.Fatal("an account exists: no more setup")
	}
}
