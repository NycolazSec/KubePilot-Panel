package main

import (
	"database/sql"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"regexp"
	"strings"
	"syscall"
)

// ---------- Files planted by game servers
//
// A game server controls the content of its volume: besides symlinks (handled by os.Root) it can create
// FIFOs or device nodes. Opening a FIFO blocks until a writer shows up, so a planted one could hang panel
// requests forever. Every open made by the panel is non-blocking and refuses anything but regular files.

const maxArchiveEntries = 100_000

func openRegular(root *os.Root, name string) (*os.File, fs.FileInfo, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !st.Mode().IsRegular() && !st.IsDir() {
		f.Close()
		return nil, nil, errCode(400, "Not a regular file")
	}
	return f, st, nil
}

// createRegular opens a file for writing (created if needed, truncated), refusing FIFOs and devices.
func createRegular(root *os.Root, name string, perm fs.FileMode) (*os.File, error) {
	if st, err := root.Stat(name); err == nil && !st.Mode().IsRegular() {
		return nil, errCode(400, "Not a regular file")
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NONBLOCK, perm)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, errCode(400, "Not a regular file")
	}
	return f, nil
}

// ---------- Disk quota
//
// local-path volumes have no size enforcement: without these checks one server could fill the node's
// disk and take down every other application on it.

func errQuota() error { return errCode(413, "Disk quota reached: free some space first") }

// diskRemaining returns how many bytes the server may still write.
func (a *App) diskRemaining(s *Server) int64 {
	if s.DiskMB <= 0 {
		return math.MaxInt64
	}
	return int64(s.DiskMB)<<20 - a.diskUsed(s.ID)
}

// quotaWriter fails once more than `left` bytes have been written.
type quotaWriter struct {
	w    io.Writer
	left int64
}

func (q *quotaWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > q.left {
		return 0, errQuota()
	}
	n, err := q.w.Write(p)
	q.left -= int64(n)
	return n, err
}

// overQuota tells whether a server is far enough above its disk limit to be stopped:
// 10 % of tolerance (at least 256 MB) so a short spike doesn't kill a game.
func overQuota(used int64, diskMB int) bool {
	if diskMB <= 0 {
		return false
	}
	limit := int64(diskMB) << 20
	margin := max(limit/10, 256<<20)
	return used > limit+margin
}

// ---------- HTTP

// securityHeaders are sent with every response.
func securityHeaders(h http.Header, r *http.Request, secure bool) {
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("X-Permitted-Cross-Domain-Policies", "none")
	connect := "'self'"
	if hostRe.MatchString(r.Host) {
		connect += " wss://" + r.Host // console WebSocket (older browsers don't match wss: with 'self')
	}
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; object-src 'none'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; connect-src "+connect+"; worker-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if secure {
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		h.Set("Cache-Control", "no-store") // account data, files and tokens must never sit in a cache
	}
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9.-]+(:[0-9]{1,5})?$`)

// cookieName: with HTTPS the "__Host-" prefix makes the browser refuse a session cookie set by another
// subdomain or without Secure, so a sibling site can't plant a session (cookie tossing / fixation).
func (a *App) cookieName() string {
	if a.cfg.SecureCookies {
		return "__Host-kubepilot_session"
	}
	return "kubepilot_session"
}

// knownIP tells whether this user already has a session from this IP: such sign-ins are exempt from the
// per-account limit, so an attacker spraying wrong passwords can't lock the real user out.
func (db *DB) knownIP(userID int64, ip string) bool {
	var one int
	return db.QueryRow(`SELECT 1 FROM sessions WHERE user_id=? AND ip=? AND expires_at>? LIMIT 1`, userID, ip, now()).Scan(&one) == nil
}

// affected returns the number of rows changed by an Exec, 0 on error.
func affected(res sql.Result, err error) int64 {
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

// ---------- Mandatory two-factor authentication

const (
	twoFactorOptional = ""
	twoFactorAdmins   = "admins"
	twoFactorEveryone = "everyone"
)

func (a *App) twoFactorRequired(u *User) bool {
	if u.TwoFactor {
		return false
	}
	switch a.db.Settings().TwoFactorPolicy {
	case twoFactorEveryone:
		return true
	case twoFactorAdmins:
		return u.IsAdmin
	}
	return false
}

// Without the required second factor, a signed-in user can only reach what's needed to set it up.
func twoFactorSetupRoute(r *http.Request) bool {
	p := r.URL.Path
	return p == "/api/auth/me" || p == "/api/auth/logout" || p == "/api/account/password" || strings.HasPrefix(p, "/api/account/2fa")
}
