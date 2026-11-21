package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"log"
	"net/http"
	"strings"
	"sync"
)

// First start: while the panel has no account, it prints a one-time setup token in its logs, and whoever has it
// creates the administrator from the browser (/setup). Without the token nobody can claim a fresh panel, even
// if they find it before its owner. The token is only kept in memory: a restart prints a new one.

type setupState struct {
	mu   sync.Mutex
	hash string // sha256 of the token; empty once an account exists
}

func (db *DB) UserCount() int {
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	return n
}

// initSetup prepares the setup token when the panel has no account yet; it returns the token ("" otherwise).
func (a *App) initSetup() string {
	if a.db.UserCount() > 0 {
		return ""
	}
	b := make([]byte, 20) // 160 bits
	_, _ = rand.Read(b)
	tok := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
	a.setup.mu.Lock()
	a.setup.hash = sha(tok)
	a.setup.mu.Unlock()
	where := "/setup"
	if a.cfg.PublicURL != "" {
		where = a.cfg.PublicURL + "/setup"
	}
	log.Printf("First start, no account yet: create the administrator at %s#%s", where, tok)
	log.Printf("Setup token: %s (valid until the first account exists, or the next restart)", tok)
	return tok
}

// setupNeeded: the panel has no account and is waiting for its administrator.
func (a *App) setupNeeded() bool {
	a.setup.mu.Lock()
	pending := a.setup.hash != ""
	a.setup.mu.Unlock()
	return pending && a.db.UserCount() == 0
}

// POST /api/setup {token, username, email, password}: creates the first administrator.
func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Username, Email, Password string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	ip := clientIP(r)
	if !a.resetLimiter.allow("setup:" + ip) {
		writeErr(w, r, errCode(429, "Too many attempts, try again in a few minutes"))
		return
	}
	a.setup.mu.Lock()
	defer a.setup.mu.Unlock() // one setup at a time
	if a.setup.hash == "" || a.db.UserCount() > 0 {
		writeErr(w, r, errCode(409, "This panel already has an account: sign in"))
		return
	}
	if subtle.ConstantTimeCompare([]byte(sha(strings.TrimSpace(in.Token))), []byte(a.setup.hash)) != 1 {
		writeErr(w, r, errCode(403, "Invalid setup token: copy it from the panel's logs"))
		return
	}
	u, err := a.db.CreateUser(in.Username, in.Email, in.Password, true)
	if err != nil {
		writeErr(w, r, err) // invalid username, email or password: translated
		return
	}
	a.setup.hash = ""
	a.db.LogActivity("", u, ip, "auth.setup", u.Username)
	log.Printf("First administrator created from the browser: %s", u.Username)
	writeJSON(w, 200, map[string]string{"username": u.Username})
}
