package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

// MySQL / MariaDB databases per server, on the MariaDB the chart deploys next to the game servers. Each
// database gets its own user, limited to it; only the server's game pods (and the panel) can reach MariaDB.
// Passwords are shown once and never stored: they can be rotated.

const databaseSchema = `
CREATE TABLE IF NOT EXISTS server_databases (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  name TEXT NOT NULL UNIQUE,
  username TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS server_databases_server ON server_databases(server_id);`

type ServerDatabase struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Username  string `json:"username"`
	CreatedAt int64  `json:"createdAt"`
}

// What a database user may do on its own database (no GRANT OPTION, nothing on other databases).
const dbGrants = "SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, ALTER, INDEX, REFERENCES, CREATE TEMPORARY TABLES, LOCK TABLES, " +
	"EXECUTE, CREATE VIEW, SHOW VIEW, CREATE ROUTINE, ALTER ROUTINE, EVENT, TRIGGER"

const dbMaxUserConnections = 30

var dbSuffixRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

type dbHost struct {
	once sync.Once
	addr string // host:port the panel uses
	pass string
	db   *sql.DB
}

func (a *App) mariadb() *sql.DB {
	h := &a.dbHost
	if h.addr == "" || h.pass == "" {
		return nil
	}
	h.once.Do(func() {
		cfg := mysql.NewConfig()
		cfg.User, cfg.Passwd, cfg.Net, cfg.Addr = "root", h.pass, "tcp", h.addr
		cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 5*time.Second, 30*time.Second, 30*time.Second
		cfg.InterpolateParams = true
		conn, err := mysql.NewConnector(cfg)
		if err != nil {
			return
		}
		h.db = sql.OpenDB(conn)
		h.db.SetMaxOpenConns(4)
		h.db.SetConnMaxIdleTime(5 * time.Minute)
	})
	return h.db
}

func randomAlnum(n int) string {
	const chars = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	for i := range b {
		x, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[x.Int64()]
	}
	return string(b)
}

// quoteIdent / quoteString: names and passwords are generated from safe characters and checked again here.
var safeIdentRe = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
var safePassRe = regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`)

func quoteIdent(s string) (string, error) {
	if !safeIdentRe.MatchString(s) {
		return "", internalErr("unsafe identifier")
	}
	return "`" + s + "`", nil
}

func (db *DB) ServerDatabases(serverID string) ([]ServerDatabase, error) {
	rows, err := db.Query(`SELECT id, name, username, created_at FROM server_databases WHERE server_id=? ORDER BY id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerDatabase{}
	for rows.Next() {
		var d ServerDatabase
		if err := rows.Scan(&d.ID, &d.Name, &d.Username, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (db *DB) ServerDatabase(serverID string, id int64) (*ServerDatabase, error) {
	var d ServerDatabase
	err := db.QueryRow(`SELECT id, name, username, created_at FROM server_databases WHERE server_id=? AND id=?`, serverID, id).
		Scan(&d.ID, &d.Name, &d.Username, &d.CreatedAt)
	if err != nil {
		return nil, ErrNotFound
	}
	return &d, nil
}

func createDatabase(ctx context.Context, my *sql.DB, name, user, pass string) error {
	qn, err := quoteIdent(name)
	if err != nil {
		return err
	}
	if !safeIdentRe.MatchString(user) || !safePassRe.MatchString(pass) {
		return internalErr("unsafe user")
	}
	stmts := []string{
		"CREATE DATABASE " + qn + " CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		fmt.Sprintf("CREATE USER '%s'@'%%' IDENTIFIED BY '%s' WITH MAX_USER_CONNECTIONS %d", user, pass, dbMaxUserConnections),
		fmt.Sprintf("GRANT %s ON %s.* TO '%s'@'%%'", dbGrants, qn, user),
	}
	for _, q := range stmts {
		if _, err := my.ExecContext(ctx, q); err != nil {
			_ = dropDatabase(ctx, my, name, user)
			return err
		}
	}
	return nil
}

func dropDatabase(ctx context.Context, my *sql.DB, name, user string) error {
	qn, err := quoteIdent(name)
	if err != nil {
		return err
	}
	if !safeIdentRe.MatchString(user) {
		return internalErr("unsafe user")
	}
	if _, err := my.ExecContext(ctx, "DROP DATABASE IF EXISTS "+qn); err != nil {
		return err
	}
	_, err = my.ExecContext(ctx, fmt.Sprintf("DROP USER IF EXISTS '%s'@'%%'", user))
	return err
}

// dropServerDatabases removes a server's databases from MariaDB (before the server itself is deleted).
func (a *App) dropServerDatabases(ctx context.Context, serverID string) error {
	dbs, err := a.db.ServerDatabases(serverID)
	if err != nil || len(dbs) == 0 {
		return err
	}
	my := a.mariadb()
	if my == nil {
		return internalErr("MariaDB is not configured")
	}
	for _, d := range dbs {
		if err := dropDatabase(ctx, my, d.Name, d.Username); err != nil {
			return err
		}
	}
	return nil
}

func dbUnavailable(err error) error {
	log.Printf("mariadb: %v", err)
	return errCode(503, "The database server can't be reached right now")
}

func (a *App) databaseAddress() (string, int) {
	host, port, err := net.SplitHostPort(a.dbHost.addr)
	if err != nil {
		return a.dbHost.addr, 3306
	}
	p, _ := strconv.Atoi(port)
	return host, p
}

// GET /api/servers/{id}/databases
func (a *App) handleDatabases(w http.ResponseWriter, r *http.Request, c *Ctx) {
	dbs, err := a.db.ServerDatabases(c.Server.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	host, port := a.databaseAddress()
	writeJSON(w, 200, map[string]any{"enabled": a.mariadb() != nil, "host": host, "port": port,
		"limit": c.Server.DatabaseLimit, "databases": dbs})
}

// POST /api/servers/{id}/databases {name} → {database, password}
func (a *App) handleDatabaseCreate(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	my := a.mariadb()
	if my == nil {
		writeErr(w, r, errCode(400, "Databases are not enabled on this panel"))
		return
	}
	suffix := strings.ToLower(strings.TrimSpace(in.Name))
	if !dbSuffixRe.MatchString(suffix) {
		writeErr(w, r, errCode(400, "Database name: 1 to 32 lowercase letters, digits or _"))
		return
	}
	lock := a.serverLock("db:" + c.Server.ID)
	lock.Lock()
	defer lock.Unlock()
	dbs, err := a.db.ServerDatabases(c.Server.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if len(dbs) >= c.Server.DatabaseLimit {
		writeErr(w, r, errCode(400, "Database limit reached (%d)", c.Server.DatabaseLimit))
		return
	}
	name := "s" + c.Server.ID + "_" + suffix
	user := "u" + c.Server.ID + "_" + strings.ToLower(randomAlnum(6))
	pass := randomAlnum(24)
	for _, d := range dbs {
		if d.Name == name {
			writeErr(w, r, errCode(409, "This database already exists"))
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := a.db.Exec(`INSERT INTO server_databases(server_id, name, username, created_at) VALUES(?,?,?,?)`, c.Server.ID, name, user, now())
	if err != nil {
		writeErr(w, r, errCode(409, "This database already exists"))
		return
	}
	id, _ := res.LastInsertId()
	if err := createDatabase(ctx, my, name, user, pass); err != nil {
		_, _ = a.db.Exec(`DELETE FROM server_databases WHERE id=?`, id)
		writeErr(w, r, dbUnavailable(err))
		return
	}
	d, _ := a.db.ServerDatabase(c.Server.ID, id)
	c.log(a, "server.database.create", name)
	writeJSON(w, 200, map[string]any{"database": d, "password": pass})
}

// POST /api/servers/{id}/databases/{did}/rotate → {password}
func (a *App) handleDatabaseRotate(w http.ResponseWriter, r *http.Request, c *Ctx) {
	id, _ := strconv.ParseInt(r.PathValue("did"), 10, 64)
	d, err := a.db.ServerDatabase(c.Server.ID, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	my := a.mariadb()
	if my == nil || !safeIdentRe.MatchString(d.Username) {
		writeErr(w, r, errCode(400, "Databases are not enabled on this panel"))
		return
	}
	pass := randomAlnum(24)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := my.ExecContext(ctx, fmt.Sprintf("ALTER USER '%s'@'%%' IDENTIFIED BY '%s'", d.Username, pass)); err != nil {
		writeErr(w, r, dbUnavailable(err))
		return
	}
	c.log(a, "server.database.rotate", d.Name)
	writeJSON(w, 200, map[string]string{"password": pass})
}

// DELETE /api/servers/{id}/databases/{did}
func (a *App) handleDatabaseDelete(w http.ResponseWriter, r *http.Request, c *Ctx) {
	id, _ := strconv.ParseInt(r.PathValue("did"), 10, 64)
	d, err := a.db.ServerDatabase(c.Server.ID, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	my := a.mariadb()
	if my == nil {
		writeErr(w, r, errCode(400, "Databases are not enabled on this panel"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := dropDatabase(ctx, my, d.Name, d.Username); err != nil {
		writeErr(w, r, dbUnavailable(err))
		return
	}
	_, _ = a.db.Exec(`DELETE FROM server_databases WHERE id=?`, d.ID)
	c.log(a, "server.database.delete", d.Name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
