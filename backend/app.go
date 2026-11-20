package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type App struct {
	cfg  Config
	db   *DB
	kube *Kube
	ctx  context.Context

	hubsMu sync.Mutex
	hubs   map[string]*Hub

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex

	diskMu sync.RWMutex
	disk   map[string]int64

	allocMu sync.Mutex

	limiter  *rateLimiter // sign-in attempts per IP
	accounts *rateLimiter // sign-in attempts per account, from IPs the account never signed in from
	keyRate  *rateLimiter // requests per API key
	sftp     *sftpServer  // nil when SFTP is off
	dbHost   dbHost       // MariaDB for the servers' databases (empty address: off)
	tickets  loginTickets // sign-ins waiting for their second factor
}

func newApp(cfg Config, db *DB, kube *Kube) *App {
	return &App{
		cfg: cfg, db: db, kube: kube, ctx: context.Background(),
		hubs: map[string]*Hub{}, locks: map[string]*sync.Mutex{}, disk: map[string]int64{},
		dbHost:  dbHost{addr: cfg.DatabaseAddr, pass: cfg.DatabasePassword},
		limiter: newRateLimiter(10, 10*time.Minute), accounts: newRateLimiter(20, 15*time.Minute), keyRate: newRateLimiter(apiRatePerMin, time.Minute),
	}
}

func (a *App) startBackground(ctx context.Context) {
	a.ctx = ctx
	servers, err := a.db.AllServers()
	if err == nil {
		for _, s := range servers {
			a.hub(s.ID)
		}
	}
	go a.syncRoutes(ctx) // router annotations follow the current settings
	go a.diskLoop(ctx)
	go a.scheduleLoop(ctx)
	go a.sessionCleanup(ctx)
}

func (a *App) serverLock(id string) *sync.Mutex {
	a.locksMu.Lock()
	defer a.locksMu.Unlock()
	m, ok := a.locks[id]
	if !ok {
		m = &sync.Mutex{}
		a.locks[id] = m
	}
	return m
}

func (a *App) sessionCleanup(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now())
		_, _ = a.db.Exec(`DELETE FROM activity WHERE created_at < ?`, time.Now().Add(-90*24*time.Hour).Unix())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---------- HTTP helpers

// apiError is an error shown to the user: msg is an English format string, translated by writeErr.
type apiError struct {
	code int
	msg  string
	args []any
}

func (e *apiError) Error() string {
	if len(e.args) > 0 {
		return fmt.Sprintf(e.msg, e.args...)
	}
	return e.msg
}

func errCode(code int, msg string, args ...any) error { return &apiError{code, msg, args} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr answers with an error message in the user's language.
func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	lang := langOf(r)
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		writeJSON(w, ae.code, map[string]string{"error": trErr(lang, ae)})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, 404, map[string]string{"error": tr(lang, "Not found")})
	case errors.Is(err, ErrNoVolume):
		writeJSON(w, 409, map[string]string{"error": trErr(lang, err)})
	default:
		writeJSON(w, 400, map[string]string{"error": trErr(lang, err)})
	}
}

func readJSON(r *http.Request, v any) error {
	body := io.LimitReader(r.Body, 2<<20)
	if err := json.NewDecoder(body).Decode(v); err != nil {
		return errCode(400, "Invalid JSON")
	}
	return nil
}

// Cloudflare edge ranges (https://www.cloudflare.com/ips/). CF-Connecting-IP is only trusted
// when the request really comes from one of them, so it can't be spoofed by hitting the origin directly.
var cloudflareNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range strings.Fields(`173.245.48.0/20 103.21.244.0/22 103.22.200.0/22 103.31.4.0/22 141.101.64.0/18
		108.162.192.0/18 190.93.240.0/20 188.114.96.0/20 197.234.240.0/22 198.41.128.0/17 162.158.0.0/15 104.16.0.0/13
		104.24.0.0/14 172.64.0.0/13 131.0.72.0/22 2400:cb00::/32 2606:4700::/32 2803:f800::/32 2405:b500::/32
		2405:8100::/32 2a06:98c0::/29 2c0f:f248::/32`) {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

func fromCloudflare(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range cloudflareNets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

func clientIP(r *http.Request) string {
	// Traffic always arrives through the ingress controller, which appends the peer it saw last.
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		ip = strings.TrimSpace(parts[len(parts)-1])
	}
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" && fromCloudflare(ip) && net.ParseIP(cf) != nil {
		return cf
	}
	return ip
}

// ---------- Rate limiter (login)

type rateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-l.window)
	h := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			h = append(h, t)
		}
	}
	if len(h) >= l.max {
		l.hits[key] = h
		return false
	}
	l.hits[key] = append(h, time.Now())
	return true
}

func (l *rateLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}

// ---------- Auth

type ctxKey int

const userKey ctxKey = 1

func (a *App) currentUser(r *http.Request) *User {
	c, err := r.Cookie(a.cookieName())
	if err != nil {
		return nil
	}
	u, err := a.db.SessionUser(c.Value)
	if err != nil {
		return nil
	}
	return u
}

type authedHandler func(w http.ResponseWriter, r *http.Request, u *User)

func (a *App) authed(h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var u *User
		if tok := bearerToken(r); tok != "" {
			// API key: never mixed with a browser session
			if u, r = a.authKey(w, r, tok); u == nil {
				return
			}
		} else if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			writeErr(w, r, errCode(401, "Invalid API key"))
			return
		} else if u = a.currentUser(r); u == nil {
			writeErr(w, r, errCode(401, "Not signed in"))
			return
		}
		if a.twoFactorRequired(u) && !twoFactorSetupRoute(r) {
			writeErr(w, r, errCode(403, "Two-factor authentication is required: enable it in My account"))
			return
		}
		h(w, r, u)
	}
}

func (a *App) admin(h authedHandler) http.HandlerFunc {
	return a.authed(func(w http.ResponseWriter, r *http.Request, u *User) {
		if !u.IsAdmin {
			writeErr(w, r, errCode(403, "Administrators only"))
			return
		}
		if k := keyFrom(r); k != nil && !k.can("admin") {
			writeErr(w, r, errCode(403, "This API key doesn't have the admin permission"))
			return
		}
		h(w, r, u)
	})
}

// dummy hash so failed lookups take as long as real ones (no user enumeration by timing)
var dummyHash = sync.OnceValue(func() string {
	h, _ := bcrypt.GenerateFromPassword([]byte("kubepilot-dummy-password"), 12)
	return string(h)
})

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Login, Password string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	ip := clientIP(r)
	if !a.limiter.allow(ip) {
		writeErr(w, r, errCode(429, "Too many attempts, try again in a few minutes"))
		return
	}
	login := strings.ToLower(strings.TrimSpace(in.Login))
	u, err := a.db.UserByLogin(login)
	// per-account limit against password spraying from many IPs; IPs the user already signed in from are
	// exempt so nobody can lock the real user out. Unknown logins are counted the same way (no enumeration).
	if (err != nil || !a.db.knownIP(u.ID, ip)) && !a.accounts.allow(login) {
		writeErr(w, r, errCode(429, "Too many attempts, try again in a few minutes"))
		return
	}
	if err != nil {
		(&User{hash: dummyHash()}).CheckPassword(in.Password)
		writeErr(w, r, errCode(401, "Invalid credentials"))
		return
	}
	if !u.CheckPassword(in.Password) {
		a.db.LogActivity("", u, ip, "auth.fail", "")
		writeErr(w, r, errCode(401, "Invalid credentials"))
		return
	}
	if u.TwoFactor {
		// password OK: the session is only created once the second factor is checked (the rate limit keeps running)
		writeJSON(w, 200, map[string]any{"twoFactor": true, "ticket": a.tickets.create(u.ID, ip)})
		return
	}
	a.limiter.reset(ip)
	a.startSession(w, r, u)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(a.cookieName()); err == nil {
		a.db.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) meView(u *User) map[string]any {
	st := a.db.Settings()
	return map[string]any{"user": u, "panelName": st.PanelName, "version": version, "twoFactorRequired": a.twoFactorRequired(u)}
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request, u *User) {
	writeJSON(w, 200, a.meView(u))
}

func (a *App) handlePassword(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct{ Current, New string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if !u.CheckPassword(in.Current) {
		writeErr(w, r, errCode(400, "Current password is incorrect"))
		return
	}
	if err := a.db.SetPassword(u.ID, in.New); err != nil {
		writeErr(w, r, err)
		return
	}
	if c, err := r.Cookie(a.cookieName()); err == nil {
		a.db.DeleteOtherSessions(u.ID, c.Value)
	}
	a.db.LogActivity("", u, clientIP(r), "account.password", "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- Router

func (a *App) routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /api/auth/login", a.handleLogin)
	m.HandleFunc("POST /api/auth/logout", a.handleLogout)
	m.HandleFunc("GET /api/auth/me", a.authed(a.handleMe))
	m.HandleFunc("PUT /api/account/password", a.authed(a.handlePassword))
	m.HandleFunc("POST /api/auth/login/2fa", a.handleLogin2FA)
	m.HandleFunc("GET /api/account/2fa", a.authed(a.handle2FAStatus))
	m.HandleFunc("POST /api/account/2fa/setup", a.authed(a.handle2FASetup))
	m.HandleFunc("POST /api/account/2fa/enable", a.authed(a.handle2FAEnable))
	m.HandleFunc("POST /api/account/2fa/disable", a.authed(a.handle2FADisable))
	m.HandleFunc("POST /api/account/2fa/recovery", a.authed(a.handle2FARecovery))
	m.HandleFunc("GET /api/account/api-keys", a.authed(a.handleAPIKeys))
	m.HandleFunc("POST /api/account/api-keys", a.authed(a.handleAPIKeyCreate))
	m.HandleFunc("DELETE /api/account/api-keys/{kid}", a.authed(a.handleAPIKeyDelete))
	m.HandleFunc("GET /api/account/ssh-keys", a.authed(a.handleSSHKeys))
	m.HandleFunc("POST /api/account/ssh-keys", a.authed(a.handleSSHKeyAdd))
	m.HandleFunc("DELETE /api/account/ssh-keys/{kid}", a.authed(a.handleSSHKeyDelete))
	m.HandleFunc("GET /api/servers/{id}/sftp", a.srv("sftp", a.handleSFTPInfo))
	m.HandleFunc("GET /api/servers/{id}/databases", a.srv("databases", a.handleDatabases))
	m.HandleFunc("POST /api/servers/{id}/databases", a.srv("databases", a.handleDatabaseCreate))
	m.HandleFunc("POST /api/servers/{id}/databases/{did}/rotate", a.srv("databases", a.handleDatabaseRotate))
	m.HandleFunc("DELETE /api/servers/{id}/databases/{did}", a.srv("databases", a.handleDatabaseDelete))

	m.HandleFunc("GET /api/servers", a.authed(a.handleServers))
	m.HandleFunc("GET /api/servers/{id}", a.srv("", a.handleServer))
	m.HandleFunc("POST /api/servers/{id}/power", a.srv("power", a.handlePower))
	m.HandleFunc("POST /api/servers/{id}/command", a.srv("console", a.handleCommand))
	m.HandleFunc("GET /api/servers/{id}/ws", a.srv("console", a.handleWS))
	m.HandleFunc("GET /api/servers/{id}/console", a.srv("console", a.handleConsoleHistory))
	m.HandleFunc("GET /api/servers/{id}/minecraft", a.srv("console", a.handleMinecraft))
	m.HandleFunc("POST /api/servers/{id}/minecraft/action", a.srv("console", a.handleMinecraftAction))
	m.HandleFunc("GET /api/minecraft/avatar/{uuid}", a.authed(a.handleAvatar))
	m.HandleFunc("GET /api/servers/{id}/plugins", a.srv("files", a.handlePlugins))
	m.HandleFunc("GET /api/servers/{id}/plugins/search", a.srv("files", a.handlePluginSearch))
	m.HandleFunc("POST /api/servers/{id}/plugins/install", a.srv("files", a.handlePluginInstall))
	m.HandleFunc("POST /api/servers/{id}/plugins/update", a.srv("files", a.handlePluginUpdate))
	m.HandleFunc("POST /api/servers/{id}/plugins/toggle", a.srv("files", a.handlePluginFile))
	m.HandleFunc("POST /api/servers/{id}/plugins/delete", a.srv("files", a.handlePluginFile))
	m.HandleFunc("GET /api/modrinth/icon", a.authed(a.handleModrinthIcon))

	m.HandleFunc("GET /api/servers/{id}/files/list", a.srv("files", a.handleFileList))
	m.HandleFunc("GET /api/servers/{id}/files/content", a.srv("files", a.handleFileRead))
	m.HandleFunc("PUT /api/servers/{id}/files/content", a.srv("files", a.handleFileWrite))
	m.HandleFunc("POST /api/servers/{id}/files/mkdir", a.srv("files", a.handleMkdir))
	m.HandleFunc("POST /api/servers/{id}/files/rename", a.srv("files", a.handleRename))
	m.HandleFunc("POST /api/servers/{id}/files/delete", a.srv("files", a.handleDelete))
	m.HandleFunc("POST /api/servers/{id}/files/upload", a.srv("files", a.handleUpload))
	m.HandleFunc("GET /api/servers/{id}/files/download", a.srv("files", a.handleDownload))
	m.HandleFunc("POST /api/servers/{id}/files/compress", a.srv("files", a.handleCompress))
	m.HandleFunc("POST /api/servers/{id}/files/decompress", a.srv("files", a.handleDecompress))

	m.HandleFunc("GET /api/servers/{id}/backups", a.srv("backups", a.handleBackups))
	m.HandleFunc("POST /api/servers/{id}/backups", a.srv("backups", a.handleBackupCreate))
	m.HandleFunc("GET /api/servers/{id}/backups/{bid}/download", a.srv("backups", a.handleBackupDownload))
	m.HandleFunc("POST /api/servers/{id}/backups/{bid}/restore", a.srv("backups", a.handleBackupRestore))
	m.HandleFunc("POST /api/servers/{id}/backups/{bid}/lock", a.srv("backups", a.handleBackupLock))
	m.HandleFunc("DELETE /api/servers/{id}/backups/{bid}", a.srv("backups", a.handleBackupDelete))

	m.HandleFunc("GET /api/servers/{id}/schedules", a.srv("schedules", a.handleSchedules))
	m.HandleFunc("POST /api/servers/{id}/schedules", a.srv("schedules", a.handleScheduleSave))
	m.HandleFunc("PUT /api/servers/{id}/schedules/{sid}", a.srv("schedules", a.handleScheduleSave))
	m.HandleFunc("DELETE /api/servers/{id}/schedules/{sid}", a.srv("schedules", a.handleScheduleDelete))
	m.HandleFunc("POST /api/servers/{id}/schedules/{sid}/run", a.srv("schedules", a.handleScheduleRun))

	m.HandleFunc("GET /api/servers/{id}/startup", a.srv("startup", a.handleStartup))
	m.HandleFunc("PUT /api/servers/{id}/startup", a.srv("startup", a.handleStartupSave))

	m.HandleFunc("GET /api/servers/{id}/network", a.srv("network", a.handleNetwork))
	m.HandleFunc("POST /api/servers/{id}/network", a.srv("network", a.handleAllocAdd))
	m.HandleFunc("PUT /api/servers/{id}/network/{port}", a.srv("network", a.handleAllocUpdate))
	m.HandleFunc("PUT /api/servers/{id}/hostname", a.srv("network", a.handleHostname))
	m.HandleFunc("PUT /api/servers/{id}/exposure", a.srv("network", a.handleExposure))
	m.HandleFunc("DELETE /api/servers/{id}/network/{port}", a.srv("network", a.handleAllocDelete))

	m.HandleFunc("GET /api/servers/{id}/users", a.srv("users", a.handleSubUsers))
	m.HandleFunc("POST /api/servers/{id}/users", a.srv("users", a.handleSubUserSave))
	m.HandleFunc("PUT /api/servers/{id}/users/{uid}", a.srv("users", a.handleSubUserSave))
	m.HandleFunc("DELETE /api/servers/{id}/users/{uid}", a.srv("users", a.handleSubUserDelete))

	m.HandleFunc("GET /api/servers/{id}/activity", a.srv("activity", a.handleServerActivity))
	m.HandleFunc("PUT /api/servers/{id}/settings", a.srv("settings", a.handleServerSettings))
	m.HandleFunc("POST /api/servers/{id}/reinstall", a.srv("settings", a.handleReinstall))

	m.HandleFunc("GET /api/admin/overview", a.admin(a.handleAdminOverview))
	m.HandleFunc("GET /api/admin/users", a.admin(a.handleAdminUsers))
	m.HandleFunc("POST /api/admin/users", a.admin(a.handleAdminUserSave))
	m.HandleFunc("PUT /api/admin/users/{uid}", a.admin(a.handleAdminUserSave))
	m.HandleFunc("DELETE /api/admin/users/{uid}", a.admin(a.handleAdminUserDelete))
	m.HandleFunc("GET /api/admin/templates", a.admin(a.handleAdminTemplates))
	m.HandleFunc("POST /api/admin/templates", a.admin(a.handleAdminTemplateSave))
	m.HandleFunc("PUT /api/admin/templates/{tid}", a.admin(a.handleAdminTemplateSave))
	m.HandleFunc("DELETE /api/admin/templates/{tid}", a.admin(a.handleAdminTemplateDelete))
	m.HandleFunc("POST /api/admin/templates/egg", a.admin(a.handleEggConvert))
	m.HandleFunc("GET /api/admin/eggs/catalog", a.admin(a.handleEggCatalog))
	m.HandleFunc("POST /api/admin/servers", a.admin(a.handleAdminServerCreate))
	m.HandleFunc("PUT /api/admin/servers/{id}", a.admin(a.handleAdminServerUpdate))
	m.HandleFunc("DELETE /api/admin/servers/{id}", a.admin(a.handleAdminServerDelete))
	m.HandleFunc("GET /api/admin/settings", a.admin(a.handleAdminSettings))
	m.HandleFunc("PUT /api/admin/settings", a.admin(a.handleAdminSettingsSave))
	m.HandleFunc("GET /api/admin/activity", a.admin(a.handleAdminActivity))

	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	m.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, r, errCode(404, "Unknown route"))
	}))
	m.Handle("/", a.spa())
	return a.secure(m)
}

func (a *App) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w.Header(), r, a.cfg.SecureCookies)
		// CSRF: state-changing API calls must carry a custom header (forces a CORS preflight cross-site).
		// API keys travel in the Authorization header, which a cross-site page can't set: no CSRF risk.
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Method != http.MethodHead && bearerToken(r) == "" {
			if r.Header.Get("X-KubePilot") != "1" {
				writeErr(w, r, errCode(403, "Request refused (CSRF)"))
				return
			}
		}
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic %s %s: %v", r.Method, r.URL.Path, rec)
				writeErr(w, r, errCode(500, "Internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *App) spa() http.Handler {
	sub, _ := fs.Sub(webFS, "web")
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" {
			if f, err := sub.Open(p); err == nil {
				st, _ := f.Stat()
				f.Close()
				if !st.IsDir() {
					if strings.HasPrefix(p, "assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					files.ServeHTTP(w, r)
					return
				}
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		b, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "frontend not built", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
}
