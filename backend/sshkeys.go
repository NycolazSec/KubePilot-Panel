package main

import (
	"crypto/rsa"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// SSH keys of an account, for SFTP. Only the public key is stored; the fingerprint (SHA256) identifies it.

const sshKeySchema = `
CREATE TABLE IF NOT EXISTS ssh_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  public_key TEXT NOT NULL,
  last_used_at INTEGER NOT NULL DEFAULT 0,
  last_ip TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  UNIQUE(user_id, fingerprint)
);`

const maxSSHKeys = 20

type SSHKey struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Type        string `json:"type"`
	LastUsedAt  int64  `json:"lastUsedAt"`
	LastIP      string `json:"lastIp"`
	CreatedAt   int64  `json:"createdAt"`
}

func (db *DB) HasSSHKey(userID int64, fingerprint string) bool {
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM ssh_keys WHERE user_id=? AND fingerprint=?`, userID, fingerprint).Scan(&n)
	return n > 0
}

func (db *DB) TouchSSHKey(userID int64, fingerprint, ip string) {
	_, _ = db.Exec(`UPDATE ssh_keys SET last_used_at=?, last_ip=? WHERE user_id=? AND fingerprint=?`, now(), ip, userID, fingerprint)
}

func (db *DB) SSHKeys(userID int64) ([]SSHKey, error) {
	rows, err := db.Query(`SELECT id,name,fingerprint,public_key,last_used_at,last_ip,created_at FROM ssh_keys WHERE user_id=? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SSHKey{}
	for rows.Next() {
		var k SSHKey
		var pub string
		if err := rows.Scan(&k.ID, &k.Name, &k.Fingerprint, &pub, &k.LastUsedAt, &k.LastIP, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.Type, _, _ = strings.Cut(pub, " ")
		out = append(out, k)
	}
	return out, rows.Err()
}

// parseSSHKey accepts one OpenSSH public key line: ed25519, ECDSA, security keys, RSA of 3072 bits at least.
func parseSSHKey(line string) (ssh.PublicKey, error) {
	key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(line)))
	if err != nil || len(strings.TrimSpace(string(rest))) > 0 {
		return nil, errCode(400, "Paste one public key in OpenSSH format (ssh-ed25519 AAAA…)")
	}
	switch key.Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoSKED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoSKECDSA256:
	case ssh.KeyAlgoRSA:
		ck, ok := key.(ssh.CryptoPublicKey)
		if !ok {
			return nil, errCode(400, "Unsupported key type")
		}
		if rk, ok := ck.CryptoPublicKey().(*rsa.PublicKey); !ok || rk.N.BitLen() < 3072 {
			return nil, errCode(400, "RSA keys need 3072 bits at least: prefer an ed25519 key (ssh-keygen -t ed25519)")
		}
	default:
		return nil, errCode(400, "Unsupported key type")
	}
	return key, nil
}

// GET /api/account/ssh-keys
func (a *App) handleSSHKeys(w http.ResponseWriter, r *http.Request, u *User) {
	keys, err := a.db.SSHKeys(u.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, keys)
}

// POST /api/account/ssh-keys {name, publicKey, password}
func (a *App) handleSSHKeyAdd(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct {
		Name      string `json:"name"`
		PublicKey string `json:"publicKey"`
		Password  string `json:"password"`
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
	key, err := parseSSHKey(in.PublicKey)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	fp := ssh.FingerprintSHA256(key)
	if a.db.HasSSHKey(u.ID, fp) {
		writeErr(w, r, errCode(409, "This key is already added"))
		return
	}
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM ssh_keys WHERE user_id=?`, u.ID).Scan(&n)
	if n >= maxSSHKeys {
		writeErr(w, r, errCode(400, "%d keys at most", maxSSHKeys))
		return
	}
	pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	if _, err := a.db.Exec(`INSERT INTO ssh_keys(user_id,name,fingerprint,public_key,created_at) VALUES(?,?,?,?,?)`, u.ID, in.Name, fp, pub, now()); err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "account.sshkey.add", in.Name+" "+fp)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// DELETE /api/account/ssh-keys/{kid}
func (a *App) handleSSHKeyDelete(w http.ResponseWriter, r *http.Request, u *User) {
	id, _ := strconv.ParseInt(r.PathValue("kid"), 10, 64)
	var name, fp string
	if err := a.db.QueryRow(`SELECT name, fingerprint FROM ssh_keys WHERE id=? AND user_id=?`, id, u.ID).Scan(&name, &fp); err != nil {
		writeErr(w, r, ErrNotFound)
		return
	}
	if _, err := a.db.Exec(`DELETE FROM ssh_keys WHERE id=? AND user_id=?`, id, u.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "account.sshkey.delete", name+" "+fp)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// GET /api/servers/{id}/sftp: how to connect.
func (a *App) handleSFTPInfo(w http.ResponseWriter, r *http.Request, c *Ctx) {
	if a.sftp == nil {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, 200, map[string]any{
		"enabled": true, "host": a.publicHost(r), "port": a.cfg.SFTPPort,
		"username": c.User.Username + "." + c.Server.ID, "fingerprint": a.sftp.fingerprint,
		"passwordAllowed": !c.User.TwoFactor && !a.twoFactorRequired(c.User),
	})
}
