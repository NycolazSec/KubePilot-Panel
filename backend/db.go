package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type DB struct{ *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  email TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  is_admin INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  ip TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS templates (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  slug TEXT NOT NULL UNIQUE,
  data TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS servers (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  owner_id INTEGER NOT NULL REFERENCES users(id),
  template_id INTEGER NOT NULL REFERENCES templates(id),
  image TEXT NOT NULL,
  startup TEXT NOT NULL DEFAULT '',
  env TEXT NOT NULL DEFAULT '{}',
  memory_mb INTEGER NOT NULL,
  cpu_millis INTEGER NOT NULL,
  disk_mb INTEGER NOT NULL,
  backup_limit INTEGER NOT NULL DEFAULT 3,
  allocation_limit INTEGER NOT NULL DEFAULT 0,
  suspended INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS allocations (
  port INTEGER PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  protocol TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  target_port INTEGER NOT NULL,
  is_primary INTEGER NOT NULL DEFAULT 0,
  notes TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS server_users (
  server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  permissions TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (server_id, user_id)
);
CREATE TABLE IF NOT EXISTS backups (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  size INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  locked INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  completed_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS schedules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  cron TEXT NOT NULL,
  action TEXT NOT NULL,
  payload TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  only_online INTEGER NOT NULL DEFAULT 0,
  last_run INTEGER NOT NULL DEFAULT 0,
  next_run INTEGER NOT NULL DEFAULT 0,
  last_result TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS activity (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id TEXT NOT NULL DEFAULT '',
  user_id INTEGER NOT NULL DEFAULT 0,
  username TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  meta TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS activity_server ON activity(server_id, id);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

func openDB(path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	// A single connection: SQLite serialises writes anyway and this avoids SQLITE_BUSY.
	// Rule: never run a query while iterating another result set.
	sqldb.SetMaxOpenConns(1)
	if _, err := sqldb.Exec(schema + totpSchema + apiKeySchema + sshKeySchema); err != nil {
		return nil, err
	}
	db := &DB{sqldb}
	// columns added after the first release
	if err := db.addColumn("activity", "api_key", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return nil, err
	}
	if err := db.addColumn("servers", "hostname", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return nil, err
	}
	if err := db.addColumn("servers", "internal_only", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS servers_hostname ON servers(hostname) WHERE hostname<>''`); err != nil {
		return nil, err
	}
	return db, nil
}

// addColumn adds a column to an existing table if it isn't there yet (simple forward-only migration).
func (db *DB) addColumn(table, column, def string) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, def))
	return err
}

func now() int64 { return time.Now().Unix() }

func randomID(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sha hashes the random tokens generated by the panel (sessions, API keys, sign-in tickets: 238 to 256 bits
// of entropy). A fast hash is the right tool for those: they can't be brute-forced, and every request
// checks one. Passwords and recovery codes, which a human can guess or are short, use bcrypt instead.
func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ---------- Users

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	IsAdmin   bool   `json:"isAdmin"`
	TwoFactor bool   `json:"twoFactor"`
	CreatedAt int64  `json:"createdAt"`
	hash      string
	viaKey    string // name of the API key used for this request, if any (activity log)
}

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

func validatePassword(p string) error {
	if len(p) < 8 {
		return errors.New("The password must be at least 8 characters long")
	}
	if len(p) > 72 {
		return errors.New("Password too long (72 characters max)")
	}
	return nil
}

func (db *DB) CreateUser(username, email, password string, admin bool) (*User, error) {
	username = strings.TrimSpace(username)
	email = strings.TrimSpace(strings.ToLower(email))
	if !usernameRe.MatchString(username) {
		return nil, errors.New("Invalid username (3-32 characters: letters, digits, _ . -)")
	}
	if !strings.Contains(email, "@") || len(email) > 190 {
		return nil, errors.New("Invalid email")
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}
	res, err := db.Exec(`INSERT INTO users(username,email,password_hash,is_admin,created_at) VALUES(?,?,?,?,?)`,
		username, email, string(h), admin, now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, errors.New("This username or email already exists")
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.UserByID(id)
}

const userCols = `id, username, email, is_admin, created_at, password_hash,
	EXISTS(SELECT 1 FROM user_totp t WHERE t.user_id=users.id AND t.enabled=1)`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.hash, &u.TwoFactor); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return u, nil
}

func (db *DB) UserByID(id int64) (*User, error) {
	return scanUser(db.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, id))
}

func (db *DB) UserByLogin(login string) (*User, error) {
	return scanUser(db.QueryRow(`SELECT `+userCols+` FROM users WHERE username=? OR email=?`, login, strings.ToLower(login)))
}

func (db *DB) Users() ([]*User, error) {
	rows, err := db.Query(`SELECT ` + userCols + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (db *DB) SetPassword(id int64, password string) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE users SET password_hash=? WHERE id=?`, string(h), id)
	return err
}

func (u *User) CheckPassword(p string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.hash), []byte(p)) == nil
}

// ---------- Sessions

const sessionTTL = 14 * 24 * time.Hour

func (db *DB) CreateSession(userID int64, ip, ua string) (string, error) {
	tok := randomToken()
	if len(ua) > 250 {
		ua = ua[:250]
	}
	_, err := db.Exec(`INSERT INTO sessions(token_hash,user_id,created_at,expires_at,ip,user_agent) VALUES(?,?,?,?,?,?)`,
		sha(tok), userID, now(), time.Now().Add(sessionTTL).Unix(), ip, ua)
	return tok, err
}

func (db *DB) SessionUser(tok string) (*User, error) {
	if tok == "" {
		return nil, ErrNotFound
	}
	return scanUser(db.QueryRow(`SELECT u.id, u.username, u.email, u.is_admin, u.created_at, u.password_hash,
		EXISTS(SELECT 1 FROM user_totp t WHERE t.user_id=u.id AND t.enabled=1)
		FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?`, sha(tok), now()))
}

func (db *DB) DeleteSession(tok string) {
	_, _ = db.Exec(`DELETE FROM sessions WHERE token_hash=?`, sha(tok))
}

func (db *DB) DeleteOtherSessions(userID int64, keepTok string) {
	_, _ = db.Exec(`DELETE FROM sessions WHERE user_id=? AND token_hash<>?`, userID, sha(keepTok))
}

// ---------- Settings

type Settings struct {
	PanelName  string `json:"panelName"`
	PublicHost string `json:"publicHost"`
	PortMin    int    `json:"portMin"`
	PortMax    int    `json:"portMax"`
	Timezone   string `json:"timezone"`
	// TwoFactorPolicy: "" (optional), "admins" or "everyone" must use two-factor authentication
	TwoFactorPolicy string `json:"twoFactorPolicy"`
	// MinecraftDomain: with the Minecraft router, Java servers are reachable at <hostname>.<domain>
	MinecraftDomain string `json:"minecraftDomain"`
}

func (db *DB) Settings() Settings {
	s := Settings{PanelName: "KubePilot", PublicHost: "", PortMin: 30100, PortMax: 30299, Timezone: "Europe/Paris"}
	var raw string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='panel'`).Scan(&raw); err == nil {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	return s
}

func (db *DB) SaveSettings(s Settings) error {
	b, _ := json.Marshal(s)
	_, err := db.Exec(`INSERT INTO settings(key,value) VALUES('panel',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(b))
	return err
}

// ---------- Templates

type TemplateVar struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Default      string   `json:"default"`
	UserViewable bool     `json:"userViewable"`
	UserEditable bool     `json:"userEditable"`
	Options      []string `json:"options,omitempty"`
	Pattern      string   `json:"pattern,omitempty"`
}

type TemplatePort struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // tcp, udp, tcp+udp
}

type ImageOption struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

type Template struct {
	ID            int64          `json:"id"`
	Slug          string         `json:"slug"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Category      string         `json:"category"`
	Images        []ImageOption  `json:"images"`
	Startup       string         `json:"startup"`
	DataPath      string         `json:"dataPath"`
	Vars          []TemplateVar  `json:"vars"`
	Ports         []TemplatePort `json:"ports"`
	StopGrace     int            `json:"stopGrace"`
	ReadyProbe    bool           `json:"readyProbe"`
	RunAsUser     int64          `json:"runAsUser"`
	ConsoleInput  bool           `json:"consoleInput"`
	InstallImage  string         `json:"installImage"`
	InstallScript string         `json:"installScript"`
	DefMemory     int            `json:"defaultMemory"`
	DefCPU        int            `json:"defaultCpu"`
	DefDisk       int            `json:"defaultDisk"`
	// Features turn on game-specific pages in the panel: "minecraft-java", "minecraft-bedrock"
	Features []string `json:"features,omitempty"`
	// Pterodactyl / Pelican eggs: their install scripts expect root, a given shell and /mnt/server;
	// the game itself always runs as RunAsUser. StopCommand is typed in the console to stop the game cleanly.
	InstallAsRoot bool `json:"installAsRoot,omitempty"`
	// StartupEnv hands the startup command to the image's own entrypoint in $STARTUP (Pterodactyl images
	// run it with bash eval, after their own steps) instead of running it with sh -c.
	StartupEnv   bool   `json:"startupEnv,omitempty"`
	InstallShell string `json:"installShell,omitempty"`
	InstallPath  string `json:"installPath,omitempty"`
	StopCommand  string `json:"stopCommand,omitempty"`
	Source       string `json:"source,omitempty"`
	// ConfigFiles are edited before each start from the server's variables (eggs' "config.files")
	ConfigFiles []ConfigFile `json:"configFiles,omitempty"`
	UpdatedAt   int64        `json:"updatedAt"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,40}$`)
var envKeyRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

func (t *Template) Validate() error {
	t.Slug = strings.TrimSpace(t.Slug)
	if !slugRe.MatchString(t.Slug) {
		return errors.New("Invalid slug (lowercase letters, digits, dashes)")
	}
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("Name required")
	}
	if len(t.Images) == 0 || strings.TrimSpace(t.Images[0].Image) == "" {
		return errors.New("At least one Docker image is required")
	}
	if !strings.HasPrefix(t.DataPath, "/") || strings.Contains(t.DataPath, "..") {
		return errors.New("Invalid data path")
	}
	seen := map[string]bool{}
	for _, v := range t.Vars {
		if !envKeyRe.MatchString(v.Key) {
			return errCode(400, "Invalid variable key: %q", v.Key)
		}
		if seen[v.Key] {
			return errCode(400, "Duplicate variable: %s", v.Key)
		}
		seen[v.Key] = true
		if v.Pattern != "" {
			if _, err := regexp.Compile(v.Pattern); err != nil {
				return errCode(400, "Invalid regex for %s", v.Key)
			}
		}
	}
	switch t.InstallShell {
	case "", "sh", "bash", "ash":
	default:
		return errCode(400, "Install shell: sh, bash or ash")
	}
	if t.InstallPath != "" && (!strings.HasPrefix(t.InstallPath, "/") || strings.Contains(t.InstallPath, "..")) {
		return errCode(400, "Invalid install path")
	}
	if len(t.StopCommand) > 100 || strings.ContainsAny(t.StopCommand, "\r\n") {
		return errCode(400, "Invalid stop command")
	}
	for _, f := range t.Features {
		if !slices.Contains(knownFeatures, f) {
			return errCode(400, "Unknown feature: %s", f)
		}
	}
	for i, p := range t.Ports {
		if p.Port < 1 || p.Port > 65535 {
			return errors.New("Invalid port")
		}
		switch p.Protocol {
		case "tcp", "udp", "tcp+udp":
		default:
			t.Ports[i].Protocol = "tcp"
		}
	}
	if t.RunAsUser <= 0 {
		t.RunAsUser = 1000
	}
	if t.StopGrace <= 0 {
		t.StopGrace = 30
	}
	if t.StopGrace > 600 {
		t.StopGrace = 600
	}
	if t.InstallImage == "" {
		t.InstallImage = "busybox:1.36"
	}
	return validateConfigFiles(t.ConfigFiles)
}

func (db *DB) Templates() ([]*Template, error) {
	rows, err := db.Query(`SELECT id, data, updated_at FROM templates ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Template{}
	for rows.Next() {
		var id, upd int64
		var data string
		if err := rows.Scan(&id, &data, &upd); err != nil {
			return nil, err
		}
		t := &Template{}
		if err := json.Unmarshal([]byte(data), t); err != nil {
			return nil, err
		}
		t.ID, t.UpdatedAt = id, upd
		out = append(out, t)
	}
	return out, rows.Err()
}

func (db *DB) Template(id int64) (*Template, error) {
	var data string
	var upd int64
	if err := db.QueryRow(`SELECT data, updated_at FROM templates WHERE id=?`, id).Scan(&data, &upd); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t := &Template{}
	if err := json.Unmarshal([]byte(data), t); err != nil {
		return nil, err
	}
	t.ID, t.UpdatedAt = id, upd
	return t, nil
}

func (db *DB) SaveTemplate(t *Template) error {
	if err := t.Validate(); err != nil {
		return err
	}
	data, _ := json.Marshal(t)
	if t.ID == 0 {
		res, err := db.Exec(`INSERT INTO templates(slug,data,created_at,updated_at) VALUES(?,?,?,?)`, t.Slug, string(data), now(), now())
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return errors.New("This slug already exists")
			}
			return err
		}
		t.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := db.Exec(`UPDATE templates SET slug=?, data=?, updated_at=? WHERE id=?`, t.Slug, string(data), now(), t.ID)
	return err
}

// ---------- Servers

type Server struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	OwnerID         int64             `json:"ownerId"`
	TemplateID      int64             `json:"templateId"`
	Image           string            `json:"image"`
	Startup         string            `json:"startup"`
	Env             map[string]string `json:"env"`
	MemoryMB        int               `json:"memory"`
	CPUMillis       int               `json:"cpu"`
	DiskMB          int               `json:"disk"`
	BackupLimit     int               `json:"backupLimit"`
	AllocationLimit int               `json:"allocationLimit"`
	Suspended       bool              `json:"suspended"`
	CreatedAt       int64             `json:"createdAt"`
	Hostname        string            `json:"hostname"`     // Minecraft router: <hostname>.<minecraftDomain>
	InternalOnly    bool              `json:"internalOnly"` // no public port: only the owner's other servers reach it
}

const serverCols = `id, name, description, owner_id, template_id, image, startup, env, memory_mb, cpu_millis, disk_mb, backup_limit, allocation_limit, suspended, created_at, hostname, internal_only`

func scanServer(row interface{ Scan(...any) error }) (*Server, error) {
	s := &Server{}
	var env string
	if err := row.Scan(&s.ID, &s.Name, &s.Description, &s.OwnerID, &s.TemplateID, &s.Image, &s.Startup, &env,
		&s.MemoryMB, &s.CPUMillis, &s.DiskMB, &s.BackupLimit, &s.AllocationLimit, &s.Suspended, &s.CreatedAt, &s.Hostname, &s.InternalOnly); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.Env = map[string]string{}
	_ = json.Unmarshal([]byte(env), &s.Env)
	return s, nil
}

func (db *DB) Server(id string) (*Server, error) {
	return scanServer(db.QueryRow(`SELECT `+serverCols+` FROM servers WHERE id=?`, id))
}

func (db *DB) queryServers(q string, args ...any) ([]*Server, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Server{}
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (db *DB) AllServers() ([]*Server, error) {
	return db.queryServers(`SELECT ` + serverCols + ` FROM servers ORDER BY created_at`)
}

func (db *DB) ServersForUser(uid int64) ([]*Server, error) {
	return db.queryServers(`SELECT `+serverCols+` FROM servers WHERE owner_id=? OR id IN (SELECT server_id FROM server_users WHERE user_id=?) ORDER BY created_at`, uid, uid)
}

func (db *DB) InsertServer(s *Server) error {
	env, _ := json.Marshal(s.Env)
	_, err := db.Exec(`INSERT INTO servers(`+serverCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.ID, s.Name, s.Description, s.OwnerID, s.TemplateID, s.Image, s.Startup, string(env),
		s.MemoryMB, s.CPUMillis, s.DiskMB, s.BackupLimit, s.AllocationLimit, s.Suspended, s.CreatedAt, s.Hostname, s.InternalOnly)
	return err
}

func (db *DB) UpdateServer(s *Server) error {
	env, _ := json.Marshal(s.Env)
	_, err := db.Exec(`UPDATE servers SET name=?, description=?, owner_id=?, template_id=?, image=?, startup=?, env=?,
		memory_mb=?, cpu_millis=?, disk_mb=?, backup_limit=?, allocation_limit=?, suspended=? WHERE id=?`,
		s.Name, s.Description, s.OwnerID, s.TemplateID, s.Image, s.Startup, string(env),
		s.MemoryMB, s.CPUMillis, s.DiskMB, s.BackupLimit, s.AllocationLimit, s.Suspended, s.ID)
	return err
}

// ---------- Allocations

type Allocation struct {
	Port       int    `json:"port"`
	ServerID   string `json:"serverId"`
	Protocol   string `json:"protocol"`
	Name       string `json:"name"`
	TargetPort int    `json:"targetPort"`
	IsPrimary  bool   `json:"isPrimary"`
	Notes      string `json:"notes"`
}

func (db *DB) Allocations(serverID string) ([]*Allocation, error) {
	q := `SELECT port, server_id, protocol, name, target_port, is_primary, notes FROM allocations`
	var rows *sql.Rows
	var err error
	if serverID == "" {
		rows, err = db.Query(q + ` ORDER BY port`)
	} else {
		rows, err = db.Query(q+` WHERE server_id=? ORDER BY is_primary DESC, port`, serverID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Allocation{}
	for rows.Next() {
		a := &Allocation{}
		if err := rows.Scan(&a.Port, &a.ServerID, &a.Protocol, &a.Name, &a.TargetPort, &a.IsPrimary, &a.Notes); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (db *DB) InsertAllocation(a *Allocation) error {
	_, err := db.Exec(`INSERT INTO allocations(port,server_id,protocol,name,target_port,is_primary,notes) VALUES(?,?,?,?,?,?,?)`,
		a.Port, a.ServerID, a.Protocol, a.Name, a.TargetPort, a.IsPrimary, a.Notes)
	return err
}

// ---------- Subusers

var AllPerms = []string{"console", "power", "files", "sftp", "backups", "schedules", "startup", "network", "activity"}

type SubUser struct {
	UserID      int64    `json:"userId"`
	Username    string   `json:"username"`
	Email       string   `json:"email"`
	Permissions []string `json:"permissions"`
	CreatedAt   int64    `json:"createdAt"`
}

func (db *DB) SubUsers(serverID string) ([]*SubUser, error) {
	rows, err := db.Query(`SELECT su.user_id, u.username, u.email, su.permissions, su.created_at FROM server_users su
		JOIN users u ON u.id=su.user_id WHERE su.server_id=? ORDER BY su.created_at`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SubUser{}
	for rows.Next() {
		s := &SubUser{}
		var perms string
		if err := rows.Scan(&s.UserID, &s.Username, &s.Email, &perms, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.Permissions = splitPerms(perms)
		out = append(out, s)
	}
	return out, rows.Err()
}

func splitPerms(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cleanPerms(in []string) string {
	keep := []string{}
	for _, p := range AllPerms {
		for _, q := range in {
			if p == q {
				keep = append(keep, p)
				break
			}
		}
	}
	return strings.Join(keep, ",")
}

func (db *DB) SubUserPerms(serverID string, userID int64) ([]string, bool) {
	var perms string
	if err := db.QueryRow(`SELECT permissions FROM server_users WHERE server_id=? AND user_id=?`, serverID, userID).Scan(&perms); err != nil {
		return nil, false
	}
	return splitPerms(perms), true
}

// ---------- Activity

type Activity struct {
	ID        int64  `json:"id"`
	ServerID  string `json:"serverId"`
	UserID    int64  `json:"userId"`
	Username  string `json:"username"`
	Action    string `json:"action"`
	Meta      string `json:"meta"`
	IP        string `json:"ip"`
	APIKey    string `json:"apiKey,omitempty"` // set when the action was done with an API key
	CreatedAt int64  `json:"createdAt"`
}

func (db *DB) LogActivity(serverID string, u *User, ip, action, meta string) {
	var uid int64
	name, key := "system", ""
	if u != nil {
		uid, name, key = u.ID, u.Username, u.viaKey
	}
	if len(meta) > 500 {
		meta = meta[:500] + "…"
	}
	_, _ = db.Exec(`INSERT INTO activity(server_id,user_id,username,action,meta,ip,api_key,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		serverID, uid, name, action, meta, ip, key, now())
}

func (db *DB) ActivityList(serverID string, limit int) ([]*Activity, error) {
	var rows *sql.Rows
	var err error
	q := `SELECT id, server_id, user_id, username, action, meta, ip, api_key, created_at FROM activity`
	if serverID == "" {
		rows, err = db.Query(q+` ORDER BY id DESC LIMIT ?`, limit)
	} else {
		rows, err = db.Query(q+` WHERE server_id=? ORDER BY id DESC LIMIT ?`, serverID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Activity{}
	for rows.Next() {
		a := &Activity{}
		if err := rows.Scan(&a.ID, &a.ServerID, &a.UserID, &a.Username, &a.Action, &a.Meta, &a.IP, &a.APIKey, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
