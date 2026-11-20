package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"rsc.io/qr"
)

// Two-factor authentication: TOTP (RFC 6238, SHA-1, 6 digits, 30 s — what every authenticator app supports)
// plus single-use recovery codes.

const (
	totpPeriod        = 30
	totpDigits        = 6
	recoveryCodeCount = 10
	loginTicketTTL    = 5 * time.Minute
	loginTicketTries  = 5
	recoveryCodeCost  = 10 // bcrypt cost of recovery codes (checked rarely, up to 10 per attempt)
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	h := m.Sum(nil)
	off := h[len(h)-1] & 0x0f
	v := (uint32(h[off])&0x7f)<<24 | uint32(h[off+1])<<16 | uint32(h[off+2])<<8 | uint32(h[off+3])
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000)
}

// matchTOTP checks a code against the current time step ±1 (clock drift) and returns the matching step.
// Steps up to lastStep were already used and are refused, so a code can't be replayed.
func matchTOTP(secretB32, code string, at time.Time, lastStep int64) (int64, bool) {
	secret, err := b32.DecodeString(secretB32)
	if err != nil || len(code) != totpDigits {
		return 0, false
	}
	cur := at.Unix() / totpPeriod
	for _, step := range []int64{cur - 1, cur, cur + 1} {
		if step <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

func normalizeCode(code string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code)))
}

func newRecoveryCode() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no 0/o, 1/l/i
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:5]) + "-" + string(b[5:])
}

// ---------- Storage

const totpSchema = `
CREATE TABLE IF NOT EXISTS user_totp (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  secret TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 0,
  last_step INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS recovery_codes (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash TEXT NOT NULL,
  PRIMARY KEY (user_id, code_hash)
);`

// BeginTOTP stores a new pending secret (not active until confirmed with a code).
func (db *DB) BeginTOTP(userID int64) (string, error) {
	raw := make([]byte, 20)
	_, _ = rand.Read(raw)
	secret := b32.EncodeToString(raw)
	_, err := db.Exec(`INSERT INTO user_totp(user_id,secret,enabled,last_step,created_at) VALUES(?,?,0,0,?)
		ON CONFLICT(user_id) DO UPDATE SET secret=excluded.secret, enabled=0, last_step=0, created_at=excluded.created_at
		WHERE user_totp.enabled=0`, userID, secret, now())
	return secret, err
}

// EnableTOTP confirms the pending secret with a code from the app, and returns fresh recovery codes.
func (db *DB) EnableTOTP(userID int64, code string) ([]string, error) {
	var secret string
	var enabled bool
	err := db.QueryRow(`SELECT secret, enabled FROM user_totp WHERE user_id=?`, userID).Scan(&secret, &enabled)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && enabled) {
		return nil, errCode(409, "Start the setup again")
	} else if err != nil {
		return nil, err
	}
	step, ok := matchTOTP(secret, normalizeCode(code), time.Now(), 0)
	if !ok {
		return nil, errCode(400, "Invalid code: check that your device's clock is correct")
	}
	if _, err := db.Exec(`UPDATE user_totp SET enabled=1, last_step=? WHERE user_id=?`, step, userID); err != nil {
		return nil, err
	}
	return db.NewRecoveryCodes(userID)
}

func (db *DB) DisableTOTP(userID int64) error {
	if _, err := db.Exec(`DELETE FROM user_totp WHERE user_id=?`, userID); err != nil {
		return err
	}
	_, err := db.Exec(`DELETE FROM recovery_codes WHERE user_id=?`, userID)
	return err
}

func (db *DB) NewRecoveryCodes(userID int64) ([]string, error) {
	if _, err := db.Exec(`DELETE FROM recovery_codes WHERE user_id=?`, userID); err != nil {
		return nil, err
	}
	codes := make([]string, recoveryCodeCount)
	for i := range codes {
		codes[i] = newRecoveryCode()
		// ~50 bits of entropy: hashed like a password (bcrypt), so a stolen database can't be brute-forced
		h, err := bcrypt.GenerateFromPassword([]byte(normalizeCode(codes[i])), recoveryCodeCost)
		if err != nil {
			return nil, err
		}
		if _, err := db.Exec(`INSERT INTO recovery_codes(user_id,code_hash) VALUES(?,?)`, userID, string(h)); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

func (db *DB) RecoveryCodesLeft(userID int64) int {
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE user_id=?`, userID).Scan(&n)
	return n
}

// CheckSecondFactor accepts a code from the authenticator app or an unused recovery code (which is then
// consumed). It returns "totp" or "recovery".
func (db *DB) CheckSecondFactor(userID int64, code string) (string, bool) {
	code = normalizeCode(code)
	var secret string
	var lastStep int64
	if err := db.QueryRow(`SELECT secret, last_step FROM user_totp WHERE user_id=? AND enabled=1`, userID).Scan(&secret, &lastStep); err != nil {
		return "", false
	}
	if step, ok := matchTOTP(secret, code, time.Now(), lastStep); ok {
		// conditional update: two concurrent requests can't both use the same code
		if res, err := db.Exec(`UPDATE user_totp SET last_step=? WHERE user_id=? AND last_step<?`, step, userID, step); err == nil {
			if n, _ := res.RowsAffected(); n == 1 {
				return "totp", true
			}
		}
		return "", false
	}
	if len(code) == 10 {
		var hashes []string
		if rows, err := db.Query(`SELECT code_hash FROM recovery_codes WHERE user_id=?`, userID); err == nil {
			for rows.Next() {
				var h string
				if rows.Scan(&h) == nil {
					hashes = append(hashes, h)
				}
			}
			rows.Close()
		}
		for _, h := range hashes {
			match := false
			if strings.HasPrefix(h, "$2") {
				match = bcrypt.CompareHashAndPassword([]byte(h), []byte(code)) == nil
			} else { // codes generated before bcrypt: SHA-256
				match = subtle.ConstantTimeCompare([]byte(h), []byte(sha(code))) == 1
			}
			// conditional delete: a code can't be used twice, even by concurrent requests
			if match && affected(db.Exec(`DELETE FROM recovery_codes WHERE user_id=? AND code_hash=?`, userID, h)) == 1 {
				return "recovery", true
			}
		}
	}
	return "", false
}

// ---------- Login tickets: password checked, second factor pending

type loginTicket struct {
	userID  int64
	ip      string
	expires time.Time
	tries   int
}

type loginTickets struct {
	mu sync.Mutex
	m  map[string]*loginTicket
}

func (lt *loginTickets) create(userID int64, ip string) string {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	if lt.m == nil {
		lt.m = map[string]*loginTicket{}
	}
	for k, t := range lt.m {
		if time.Now().After(t.expires) {
			delete(lt.m, k)
		}
	}
	tok := randomToken()
	lt.m[sha(tok)] = &loginTicket{userID: userID, ip: ip, expires: time.Now().Add(loginTicketTTL)}
	return tok
}

// use returns the ticket's user if the ticket is valid for this IP, counting one attempt.
func (lt *loginTickets) use(tok, ip string) (int64, bool) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	t := lt.m[sha(tok)]
	if t == nil || t.ip != ip || time.Now().After(t.expires) || t.tries >= loginTicketTries {
		return 0, false
	}
	t.tries++
	return t.userID, true
}

func (lt *loginTickets) done(tok string) {
	lt.mu.Lock()
	delete(lt.m, sha(tok))
	lt.mu.Unlock()
}

// ---------- HTTP

func (a *App) startSession(w http.ResponseWriter, r *http.Request, u *User) {
	tok, err := a.db.CreateSession(u.ID, clientIP(r), r.UserAgent())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: a.cookieName(), Value: tok, Path: "/", HttpOnly: true, Secure: a.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds()),
	})
	a.accounts.reset(strings.ToLower(u.Username))
	a.accounts.reset(strings.ToLower(u.Email))
	a.db.LogActivity("", u, clientIP(r), "auth.login", "")
	writeJSON(w, 200, a.meView(u))
}

// POST /api/auth/login/2fa {ticket, code}
func (a *App) handleLogin2FA(w http.ResponseWriter, r *http.Request) {
	var in struct{ Ticket, Code string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	ip := clientIP(r)
	if !a.limiter.allow(ip) {
		writeErr(w, r, errCode(429, "Too many attempts, try again in a few minutes"))
		return
	}
	uid, ok := a.tickets.use(in.Ticket, ip)
	if !ok {
		writeErr(w, r, errCode(410, "Sign-in expired: enter your password again"))
		return
	}
	u, err := a.db.UserByID(uid)
	if err != nil {
		writeErr(w, r, errCode(410, "Sign-in expired: enter your password again"))
		return
	}
	method, ok := a.db.CheckSecondFactor(uid, in.Code)
	if !ok {
		a.db.LogActivity("", u, ip, "auth.2fa.fail", "")
		writeErr(w, r, errCode(401, "Invalid code"))
		return
	}
	a.tickets.done(in.Ticket)
	a.limiter.reset(ip)
	if method == "recovery" {
		a.db.LogActivity("", u, ip, "auth.2fa.recovery", fmt.Sprintf("%d", a.db.RecoveryCodesLeft(uid)))
	}
	a.startSession(w, r, u)
}

// GET /api/account/2fa
func (a *App) handle2FAStatus(w http.ResponseWriter, r *http.Request, u *User) {
	writeJSON(w, 200, map[string]any{"enabled": u.TwoFactor, "recoveryCodes": a.db.RecoveryCodesLeft(u.ID)})
}

// POST /api/account/2fa/setup {password} → secret + QR code of a pending setup
func (a *App) handle2FASetup(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct{ Password string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if !u.CheckPassword(in.Password) {
		writeErr(w, r, errCode(400, "Current password is incorrect"))
		return
	}
	if u.TwoFactor {
		writeErr(w, r, errCode(409, "Two-factor authentication is already enabled"))
		return
	}
	secret, err := a.db.BeginTOTP(u.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	issuer := a.db.Settings().PanelName
	label := url.PathEscape(issuer + ":" + u.Username)
	uri := fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d", label, secret, url.QueryEscape(issuer), totpDigits, totpPeriod)
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	code.Scale = 6
	writeJSON(w, 200, map[string]string{
		"secret": secret, "uri": uri,
		"qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG()),
	})
}

// POST /api/account/2fa/enable {code} → recovery codes, shown once
func (a *App) handle2FAEnable(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct{ Code string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	codes, err := a.db.EnableTOTP(u.ID, in.Code)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "account.2fa.enable", "")
	writeJSON(w, 200, map[string]any{"recoveryCodes": codes})
}

// requireSecondFactor checks the password and a current second factor, for sensitive 2FA changes.
func (a *App) requireSecondFactor(w http.ResponseWriter, r *http.Request, u *User, password, code string) bool {
	if !u.CheckPassword(password) {
		writeErr(w, r, errCode(400, "Current password is incorrect"))
		return false
	}
	if _, ok := a.db.CheckSecondFactor(u.ID, code); !ok {
		writeErr(w, r, errCode(400, "Invalid code"))
		return false
	}
	return true
}

// POST /api/account/2fa/disable {password, code}
func (a *App) handle2FADisable(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct{ Password, Code string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if !a.requireSecondFactor(w, r, u, in.Password, in.Code) {
		return
	}
	if err := a.db.DisableTOTP(u.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "account.2fa.disable", "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// POST /api/account/2fa/recovery {password, code} → new recovery codes (the old ones stop working)
func (a *App) handle2FARecovery(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct{ Password, Code string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if !a.requireSecondFactor(w, r, u, in.Password, in.Code) {
		return
	}
	codes, err := a.db.NewRecoveryCodes(u.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "account.2fa.recovery", "")
	writeJSON(w, 200, map[string]any{"recoveryCodes": codes})
}
