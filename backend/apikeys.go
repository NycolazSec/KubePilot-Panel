package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Personal API keys let scripts, bots and other tools drive the panel with the rights of their owner,
// narrowed down by the key itself: permissions, read-only mode, allowed IPs and expiry. A key can never
// manage the account (password, 2FA, other keys) and is only accepted in the Authorization header.

const (
	apiKeyPrefix   = "kp_"
	apiKeyLen      = 40 // random base62 characters (~238 bits)
	maxAPIKeys     = 25
	apiRatePerMin  = 240
	keyTouchPeriod = 60 // seconds between "last used" updates
)

// Scopes a key can carry: the server permissions, the owner-only ones and the admin area.
// "sftp" is left out: SFTP never uses API keys.
var apiKeyScopes = append(slices.DeleteFunc(slices.Clone(AllPerms), func(p string) bool { return p == "sftp" }), "users", "settings", "admin")

const apiKeySchema = `
CREATE TABLE IF NOT EXISTS api_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  prefix TEXT NOT NULL,
  permissions TEXT NOT NULL DEFAULT '',
  read_only INTEGER NOT NULL DEFAULT 0,
  allowed_ips TEXT NOT NULL DEFAULT '',
  expires_at INTEGER NOT NULL DEFAULT 0,
  last_used_at INTEGER NOT NULL DEFAULT 0,
  last_ip TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS api_keys_user ON api_keys(user_id);`

type APIKey struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Prefix      string   `json:"prefix"`
	Permissions []string `json:"permissions"`
	ReadOnly    bool     `json:"readOnly"`
	AllowedIPs  []string `json:"allowedIps"`
	ExpiresAt   int64    `json:"expiresAt"`
	LastUsedAt  int64    `json:"lastUsedAt"`
	LastIP      string   `json:"lastIp"`
	CreatedAt   int64    `json:"createdAt"`
	userID      int64
}

func (k *APIKey) can(scope string) bool { return slices.Contains(k.Permissions, scope) }

func newAPIToken() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 0, apiKeyLen)
	buf := make([]byte, 64)
	for len(out) < apiKeyLen {
		_, _ = rand.Read(buf)
		for _, b := range buf {
			if b < 248 && len(out) < apiKeyLen { // 248 = 4×62: no modulo bias
				out = append(out, alphabet[int(b)%len(alphabet)])
			}
		}
	}
	return apiKeyPrefix + string(out)
}

// parseNets validates an allow-list of IPs / CIDR networks and returns it normalized.
func parseNets(in []string) ([]string, error) {
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			ip := net.ParseIP(s)
			if ip == nil {
				return nil, errCode(400, "Invalid IP address or network: %s", s)
			}
			if ip.To4() != nil {
				s += "/32"
			} else {
				s += "/128"
			}
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, errCode(400, "Invalid IP address or network: %s", s)
		}
		out = append(out, n.String())
	}
	if len(out) > 20 {
		return nil, errCode(400, "20 IP addresses or networks maximum")
	}
	return out, nil
}

func ipAllowed(ip string, nets []string) bool {
	if len(nets) == 0 {
		return true
	}
	parsed := net.ParseIP(ip)
	for _, c := range nets {
		if _, n, err := net.ParseCIDR(c); err == nil && parsed != nil && n.Contains(parsed) {
			return true
		}
	}
	return false
}

func splitList(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

const apiKeyCols = `id, user_id, name, prefix, permissions, read_only, allowed_ips, expires_at, last_used_at, last_ip, created_at`

func scanAPIKey(row interface{ Scan(...any) error }) (*APIKey, error) {
	k := &APIKey{}
	var perms, ips string
	if err := row.Scan(&k.ID, &k.userID, &k.Name, &k.Prefix, &perms, &k.ReadOnly, &ips, &k.ExpiresAt, &k.LastUsedAt, &k.LastIP, &k.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	k.Permissions, k.AllowedIPs = splitList(perms), splitList(ips)
	return k, nil
}

func (db *DB) APIKeys(userID int64) ([]*APIKey, error) {
	rows, err := db.Query(`SELECT `+apiKeyCols+` FROM api_keys WHERE user_id=? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (db *DB) APIKeyByToken(tok string) (*APIKey, error) {
	return scanAPIKey(db.QueryRow(`SELECT `+apiKeyCols+` FROM api_keys WHERE token_hash=?`, sha(tok)))
}

// ---------- Authentication

type apiKeyCtxKey struct{}

func keyFrom(r *http.Request) *APIKey {
	k, _ := r.Context().Value(apiKeyCtxKey{}).(*APIKey)
	return k
}

var bearerRe = regexp.MustCompile(`^Bearer (kp_[A-Za-z0-9]{40})$`)

// bearerToken returns the API key sent in the Authorization header, if any.
func bearerToken(r *http.Request) string {
	if m := bearerRe.FindStringSubmatch(r.Header.Get("Authorization")); m != nil {
		return m[1]
	}
	return ""
}

// authKey authenticates a request made with an API key. On failure it answers and returns nil.
func (a *App) authKey(w http.ResponseWriter, r *http.Request, tok string) (*User, *http.Request) {
	ip := clientIP(r)
	k, err := a.db.APIKeyByToken(tok)
	if err != nil {
		if !a.limiter.allow("key:" + ip) { // guessing keys: same budget as sign-ins
			writeErr(w, r, errCode(429, "Too many attempts, try again in a few minutes"))
			return nil, nil
		}
		writeErr(w, r, errCode(401, "Invalid API key"))
		return nil, nil
	}
	switch {
	case k.ExpiresAt > 0 && now() > k.ExpiresAt:
		writeErr(w, r, errCode(401, "This API key has expired"))
		return nil, nil
	case !ipAllowed(ip, k.AllowedIPs):
		writeErr(w, r, errCode(403, "This API key can't be used from this IP address"))
		return nil, nil
	case !a.keyRate.allow(strconv.FormatInt(k.ID, 10)):
		writeErr(w, r, errCode(429, "API rate limit reached (%d requests per minute)", apiRatePerMin))
		return nil, nil
	case strings.HasPrefix(r.URL.Path, "/api/account/"):
		writeErr(w, r, errCode(403, "API keys can't be used to manage the account"))
		return nil, nil
	case k.ReadOnly && r.Method != http.MethodGet && r.Method != http.MethodHead:
		writeErr(w, r, errCode(403, "This API key is read-only"))
		return nil, nil
	}
	u, err := a.db.UserByID(k.userID)
	if err != nil {
		writeErr(w, r, errCode(401, "Invalid API key"))
		return nil, nil
	}
	if now()-k.LastUsedAt >= keyTouchPeriod || k.LastIP != ip {
		_, _ = a.db.Exec(`UPDATE api_keys SET last_used_at=?, last_ip=? WHERE id=?`, now(), ip, k.ID)
	}
	u.viaKey = k.Name
	return u, r.WithContext(context.WithValue(r.Context(), apiKeyCtxKey{}, k))
}

// keyPerms narrows a user's permissions on a server down to the key's scopes.
func keyPerms(perms []string, k *APIKey) []string {
	if k == nil {
		return perms
	}
	out := []string{}
	for _, p := range perms {
		if k.can(p) {
			out = append(out, p)
		}
	}
	return out
}

// Scripts have no Origin header: for API keys (not cookies) there is no cross-site risk to guard against.
var apiUpgrader = websocket.Upgrader{
	ReadBufferSize: 4096, WriteBufferSize: 16384,
	CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" || upgrader.CheckOrigin(r) },
}

// ---------- Account endpoints (session only)

func (a *App) handleAPIKeys(w http.ResponseWriter, r *http.Request, u *User) {
	keys, err := a.db.APIKeys(u.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	scopes := slices.Clone(apiKeyScopes)
	if !u.IsAdmin {
		scopes = slices.DeleteFunc(scopes, func(s string) bool { return s == "admin" })
	}
	writeJSON(w, 200, map[string]any{"keys": keys, "scopes": scopes, "rateLimit": apiRatePerMin})
}

// POST /api/account/api-keys {name, permissions, readOnly, allowedIps, expiresInDays, password}
// → the token, shown once.
func (a *App) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct {
		Name          string   `json:"name"`
		Permissions   []string `json:"permissions"`
		ReadOnly      bool     `json:"readOnly"`
		AllowedIPs    []string `json:"allowedIps"`
		ExpiresInDays int      `json:"expiresInDays"`
		Password      string   `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if !u.CheckPassword(in.Password) {
		writeErr(w, r, errCode(400, "Current password is incorrect"))
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 60 {
		writeErr(w, r, errCode(400, "Name required (60 characters max)"))
		return
	}
	perms := []string{}
	for _, s := range apiKeyScopes {
		if slices.Contains(in.Permissions, s) && (s != "admin" || u.IsAdmin) {
			perms = append(perms, s)
		}
	}
	if len(perms) == 0 {
		writeErr(w, r, errCode(400, "Choose at least one permission"))
		return
	}
	nets, err := parseNets(in.AllowedIPs)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var expires int64
	if in.ExpiresInDays < 0 || in.ExpiresInDays > 3650 {
		writeErr(w, r, errCode(400, "Invalid expiry"))
		return
	} else if in.ExpiresInDays > 0 {
		expires = time.Now().AddDate(0, 0, in.ExpiresInDays).Unix()
	}
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id=?`, u.ID).Scan(&n)
	if n >= maxAPIKeys {
		writeErr(w, r, errCode(409, "%d API keys maximum per account", maxAPIKeys))
		return
	}
	tok := newAPIToken()
	res, err := a.db.Exec(`INSERT INTO api_keys(user_id,name,token_hash,prefix,permissions,read_only,allowed_ips,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		u.ID, in.Name, sha(tok), tok[:len(apiKeyPrefix)+6], strings.Join(perms, ","), in.ReadOnly, strings.Join(nets, ","), expires, now())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	id, _ := res.LastInsertId()
	k, err := scanAPIKey(a.db.QueryRow(`SELECT `+apiKeyCols+` FROM api_keys WHERE id=?`, id))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "account.apikey.create", fmt.Sprintf("%s (%s%s)", k.Name, strings.Join(perms, ", "), map[bool]string{true: ", read-only"}[in.ReadOnly]))
	writeJSON(w, 200, map[string]any{"key": k, "token": tok})
}

func (a *App) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request, u *User) {
	id, _ := strconv.ParseInt(r.PathValue("kid"), 10, 64)
	var name string
	if err := a.db.QueryRow(`SELECT name FROM api_keys WHERE id=? AND user_id=?`, id, u.ID).Scan(&name); err != nil {
		writeErr(w, r, ErrNotFound)
		return
	}
	if _, err := a.db.Exec(`DELETE FROM api_keys WHERE id=? AND user_id=?`, id, u.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "account.apikey.delete", name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- Console history for scripts

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// GET /api/servers/{id}/console?lines=100&ansi=1 → the last console lines (ANSI colors stripped by default)
func (a *App) handleConsoleHistory(w http.ResponseWriter, r *http.Request, c *Ctx) {
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if n <= 0 || n > hubHistory {
		n = min(100, hubHistory)
	}
	lines := a.hub(c.Server.ID).snapshot()
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	lang, ansi := langOf(r), r.URL.Query().Get("ansi") == "1"
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = renderConsoleLine(lang, l)
		if !ansi {
			out[i] = ansiRe.ReplaceAllString(out[i], "")
		}
	}
	writeJSON(w, 200, map[string]any{"lines": out, "status": a.state(c.Server.ID).Status})
}
