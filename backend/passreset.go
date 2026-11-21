package main

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Password reset by email. The link carries a random token (256 bits) in its #fragment, so it never reaches
// a server log or a Referer header; the panel only stores its SHA-256. A token lives one hour, works once, and
// a successful reset signs the account out everywhere. Two-factor authentication still applies afterwards: a
// mailbox alone doesn't open an account protected by 2FA.

const mailSchema = `
CREATE TABLE IF NOT EXISTS password_resets (
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  ip TEXT NOT NULL DEFAULT ''
);
`

const resetTTL = time.Hour

// passwordResetAvailable: emails can be sent, and the panel knows its own address for the link.
func (a *App) passwordResetAvailable() bool {
	return a.mail != nil && a.cfg.PublicURL != ""
}

// GET /api/auth/options: what the sign-in page may offer (public).
func (a *App) handleAuthOptions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"passwordReset": a.passwordResetAvailable(), "panelName": a.db.Settings().PanelName, "setup": a.setupNeeded()})
}

// POST /api/auth/forgot {login}: always answers the same way, whether the account exists or not.
func (a *App) handleForgot(w http.ResponseWriter, r *http.Request) {
	if !a.passwordResetAvailable() {
		writeErr(w, r, errCode(404, "Password reset by email isn't set up on this panel: ask an administrator"))
		return
	}
	var in struct{ Login string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	ip := clientIP(r)
	if !a.resetLimiter.allow(ip) {
		writeErr(w, r, errCode(429, "Too many attempts, try again in a few minutes"))
		return
	}
	login := strings.ToLower(strings.TrimSpace(in.Login))
	if u, err := a.db.UserByLogin(login); err == nil && login != "" && a.resetPerAccount.allow(strconv.FormatInt(u.ID, 10)) {
		tok := randomToken()
		_, _ = a.db.Exec(`DELETE FROM password_resets WHERE user_id=?`, u.ID) // only the latest link works
		if _, err := a.db.Exec(`INSERT INTO password_resets(token_hash,user_id,created_at,expires_at,ip) VALUES(?,?,?,?,?)`,
			sha(tok), u.ID, now(), time.Now().Add(resetTTL).Unix(), ip); err == nil {
			a.db.LogActivity("", u, ip, "auth.reset.request", "")
			lang := langOf(r)
			go a.mailUser(u, func(panel string) mailContent { // in the background: the answer takes the same time
				return mailContent{
					Panel: panel,
					Title: tr(lang, "Reset your password"),
					Paragraphs: []string{
						tr(lang, "Hello %s,", u.Username),
						tr(lang, "Someone (hopefully you) asked to reset the password of your %s account, from the IP address %s.", panel, ip),
						tr(lang, "This link works once, for one hour. If you didn't ask for it, ignore this email: your password stays the same."),
					},
					Button: tr(lang, "Choose a new password"),
					URL:    a.cfg.PublicURL + "/reset-password#" + tok,
					Footer: tr(lang, "You receive this email because an account on %s uses this address.", panel),
				}
			}, tr(lang, "Reset your %s password", a.db.Settings().PanelName))
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// POST /api/auth/reset {token, password}
func (a *App) handleReset(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Password string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	ip := clientIP(r)
	if !a.resetLimiter.allow(ip) {
		writeErr(w, r, errCode(429, "Too many attempts, try again in a few minutes"))
		return
	}
	var uid int64
	if err := a.db.QueryRow(`SELECT user_id FROM password_resets WHERE token_hash=? AND expires_at>?`, sha(strings.TrimSpace(in.Token)), now()).Scan(&uid); err != nil {
		writeErr(w, r, errCode(400, "This link is invalid or has expired: ask for a new one"))
		return
	}
	if err := a.db.SetPassword(uid, in.Password); err != nil {
		writeErr(w, r, err) // too weak: the link keeps working
		return
	}
	// one use only, and every session ends (someone else may have been signed in)
	_, _ = a.db.Exec(`DELETE FROM password_resets WHERE user_id=?`, uid)
	_, _ = a.db.Exec(`DELETE FROM sessions WHERE user_id=?`, uid)
	u, err := a.db.UserByID(uid)
	if err == nil {
		a.db.LogActivity("", u, ip, "auth.reset", "")
		a.limiter.reset(ip)
		lang := langOf(r)
		go a.mailUser(u, func(panel string) mailContent {
			return mailContent{
				Panel: panel,
				Title: tr(lang, "Your password was changed"),
				Paragraphs: []string{
					tr(lang, "Hello %s,", u.Username),
					tr(lang, "The password of your %s account was just reset with an email link, from the IP address %s. All your sessions were signed out.", panel, ip),
					tr(lang, "If it wasn't you, reset it again right away and tell an administrator."),
				},
				Button: tr(lang, "Sign in"),
				URL:    a.cfg.PublicURL + "/",
			}
		}, tr(lang, "Your %s password was changed", a.db.Settings().PanelName))
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// mailUser renders and sends an email to a user, logging failures (nobody waits for the result).
func (a *App) mailUser(u *User, content func(panel string) mailContent, subject string) {
	if a.mail == nil {
		return
	}
	msg, err := content(a.db.Settings().PanelName).render(subject)
	if err == nil {
		ctx, cancel := context.WithTimeout(a.ctx, mailTimeout+15*time.Second)
		defer cancel()
		err = a.mail.send(ctx, u.Email, msg)
	}
	if err != nil {
		log.Printf("email to user %d: %v", u.ID, err)
	}
}

// GET /api/admin/mail: how emails are sent (no password).
func (a *App) handleAdminMail(w http.ResponseWriter, r *http.Request, u *User) {
	if a.mail == nil {
		writeJSON(w, 200, map[string]any{"enabled": false, "publicURL": a.cfg.PublicURL})
		return
	}
	c := a.mail.cfg
	writeJSON(w, 200, map[string]any{"enabled": true, "host": c.Host, "port": c.Port, "tls": c.TLS, "from": a.mail.from.String(),
		"auth": c.Username != "", "publicURL": a.cfg.PublicURL, "passwordReset": a.passwordResetAvailable()})
}

// POST /api/admin/mail/test: sends a test email to the administrator, and reports the SMTP error if any.
func (a *App) handleAdminMailTest(w http.ResponseWriter, r *http.Request, u *User) {
	if a.mail == nil {
		writeErr(w, r, errCode(400, "Emails aren't set up: fill in email.smtp in the Helm chart's values"))
		return
	}
	if !a.resetLimiter.allow("mail-test:" + strconv.FormatInt(u.ID, 10)) {
		writeErr(w, r, errCode(429, "Too many attempts, try again in a few minutes"))
		return
	}
	lang := langOf(r)
	panel := a.db.Settings().PanelName
	msg, err := mailContent{
		Panel:      panel,
		Title:      tr(lang, "Emails work!"),
		Paragraphs: []string{tr(lang, "This test email was sent by %s through %s.", panel, a.mail.cfg.Host)},
		URL:        a.cfg.PublicURL,
		Button:     tr(lang, "Open the panel"),
	}.render(tr(lang, "%s: test email", panel))
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), mailTimeout+15*time.Second)
		defer cancel()
		err = a.mail.send(ctx, u.Email, msg)
	}
	if err != nil {
		writeErr(w, r, errCode(502, "The email couldn't be sent: %s", err.Error()))
		return
	}
	a.db.LogActivity("", u, clientIP(r), "admin.mail.test", u.Email)
	writeJSON(w, 200, map[string]string{"sentTo": u.Email})
}
