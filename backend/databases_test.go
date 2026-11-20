package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestDatabaseNamesAreSafe(t *testing.T) {
	for _, bad := range []string{"a`b", "a'b", "a;DROP", "A", "", strings.Repeat("a", 65), "a b", "a-b", "é"} {
		if _, err := quoteIdent(bad); err == nil {
			t.Errorf("identifier %q must be refused", bad)
		}
	}
	for _, bad := range []string{"", "a b", "x;", "UPPER", strings.Repeat("a", 33)} {
		if dbSuffixRe.MatchString(bad) {
			t.Errorf("database name %q must be refused", bad)
		}
	}
	if p := randomAlnum(24); !safePassRe.MatchString(p) {
		t.Errorf("generated passwords must be safe to quote: %q", p)
	}
	ctx := context.Background()
	if createDatabase(ctx, nil, "ok_name", "user'x", "Password1234567890ab") == nil || createDatabase(ctx, nil, "ok", "u", "short") == nil {
		t.Error("unsafe users and passwords must be refused before any query")
	}
}

// Needs a MariaDB: KUBEPILOT_TEST_MARIADB=127.0.0.1:3399 with root password KUBEPILOT_TEST_MARIADB_PASSWORD.
func TestDatabasesOnMariaDB(t *testing.T) {
	addr := os.Getenv("KUBEPILOT_TEST_MARIADB")
	if addr == "" {
		t.Skip("set KUBEPILOT_TEST_MARIADB to run against a real MariaDB")
	}
	a, _ := newTestApp(t, false)
	a.dbHost = dbHost{addr: addr, pass: os.Getenv("KUBEPILOT_TEST_MARIADB_PASSWORD")}
	root := a.mariadb()
	ctx := context.Background()
	open := func(user, pass, db string) *sql.DB {
		cfg := mysql.NewConfig()
		cfg.User, cfg.Passwd, cfg.Net, cfg.Addr, cfg.DBName, cfg.Timeout = user, pass, "tcp", addr, db, 5*time.Second
		c, _ := mysql.NewConnector(cfg)
		return sql.OpenDB(c)
	}
	p1, p2 := randomAlnum(24), randomAlnum(24)
	if err := createDatabase(ctx, root, "sab12cd34_main", "uab12cd34_aaaaaa", p1); err != nil {
		t.Fatal(err)
	}
	if err := createDatabase(ctx, root, "szz99yy88_main", "uzz99yy88_bbbbbb", p2); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dropDatabase(ctx, root, "sab12cd34_main", "uab12cd34_aaaaaa")
		dropDatabase(ctx, root, "szz99yy88_main", "uzz99yy88_bbbbbb")
	})
	u1 := open("uab12cd34_aaaaaa", p1, "sab12cd34_main")
	defer u1.Close()
	if _, err := u1.Exec("CREATE TABLE players (uuid CHAR(36) PRIMARY KEY, name VARCHAR(16))"); err != nil {
		t.Fatalf("a user works in its own database: %v", err)
	}
	if _, err := u1.Exec("INSERT INTO players VALUES ('1', 'Steve')"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"SELECT 1 FROM szz99yy88_main.x", "CREATE DATABASE other", "SELECT user FROM mysql.user",
		"GRANT ALL ON sab12cd34_main.* TO 'uzz99yy88_bbbbbb'@'%'", "CREATE USER 'evil'@'%'"} {
		if _, err := u1.Exec(q); err == nil {
			t.Errorf("%q must be refused to a database user", q)
		}
	}
	var file sql.NullString
	if u1.QueryRow("SELECT LOAD_FILE('/etc/passwd')").Scan(&file); file.Valid {
		t.Error("a database user must not read the server's files")
	}
	var dbs []string
	rows, _ := u1.Query("SHOW DATABASES")
	for rows.Next() {
		var n string
		rows.Scan(&n)
		dbs = append(dbs, n)
	}
	rows.Close()
	if strings.Contains(strings.Join(dbs, ","), "szz99yy88") {
		t.Errorf("other servers' databases must not even be listed: %v", dbs)
	}
	if err := open("uab12cd34_aaaaaa", p2, "sab12cd34_main").Ping(); err == nil {
		t.Error("a wrong password must be refused")
	}
	// rotation: the old password stops working
	if _, err := root.Exec("ALTER USER 'uab12cd34_aaaaaa'@'%' IDENTIFIED BY '" + p2 + "'"); err != nil {
		t.Fatal(err)
	}
	if err := open("uab12cd34_aaaaaa", p1, "sab12cd34_main").Ping(); err == nil {
		t.Error("the old password must stop working after a rotation")
	}
	if err := dropDatabase(ctx, root, "sab12cd34_main", "uab12cd34_aaaaaa"); err != nil {
		t.Fatal(err)
	}
	var n int
	root.QueryRow("SELECT COUNT(*) FROM mysql.user WHERE user='uab12cd34_aaaaaa'").Scan(&n)
	if n != 0 {
		t.Error("deleting a database removes its user")
	}
}
