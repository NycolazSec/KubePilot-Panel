package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// sftpPipe serves dir over SFTP in memory, as one session of server s.
func sftpPipe(t *testing.T, a *App, s *Server, dir string) *sftp.Client {
	t.Helper()
	root, err := openLocalVol(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := &sftpFS{a: a, srv: s, root: root, uid: os.Getuid()}
	c1, c2 := net.Pipe()
	rs := sftp.NewRequestServer(c1, sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h})
	go rs.Serve()
	cl, err := sftp.NewClientPipe(c2, c2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close(); rs.Close(); root.Close() })
	return cl
}

func TestSFTPFileSystem(t *testing.T) {
	a, _ := newTestApp(t, false)
	s := &Server{ID: "srvtest1", DiskMB: 1}
	dir, outside := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("top secret"), 0o600)
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "escape"))
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	cl := sftpPipe(t, a, s, dir)

	f, err := cl.Create("/plugins.yml")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("hello"))
	f.Close()
	if b, _ := os.ReadFile(filepath.Join(dir, "plugins.yml")); string(b) != "hello" {
		t.Fatalf("upload: %q", b)
	}
	if fi, err := cl.Stat("/plugins.yml"); err != nil || fi.Size() != 5 {
		t.Fatalf("stat: %v %v", fi, err)
	}
	if a.diskUsed(s.ID) != 5 {
		t.Errorf("uploaded bytes must be counted at once: %d", a.diskUsed(s.ID))
	}

	// a symlink planted by the game can't lead outside the volume, and none can be created
	if _, err := cl.Open("/escape"); err == nil {
		t.Error("following a symlink out of the volume must fail")
	}
	if _, err := cl.Open("/../../../../etc/passwd"); err == nil {
		t.Error("'..' must stay in the volume")
	}
	if err := cl.Symlink("/etc/passwd", "/link"); err == nil {
		t.Error("creating symlinks must be refused")
	}
	// a FIFO must neither block the session nor be read
	done := make(chan error, 1)
	go func() { _, err := cl.Open("/fifo"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a FIFO must not be opened")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a FIFO blocked the session")
	}

	// disk quota (1 MB): the second megabyte is refused, deleting gives the space back
	big, _ := cl.Create("/big.bin")
	_, err = big.Write(make([]byte, 2<<20))
	big.Close()
	if err == nil || !strings.Contains(err.Error(), "quota") {
		t.Errorf("writing past the quota must fail with a clear message: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "big.bin")); err == nil {
		t.Error("a new file cut by the quota must be removed")
	}
	if used := a.diskUsed(s.ID); used > 5 {
		t.Errorf("deleting must give the space back: %d", used)
	}
	// sparse writes far past the end are counted too
	sp, _ := cl.OpenFile("/sparse", os.O_WRONLY|os.O_CREATE)
	if _, err := sp.WriteAt([]byte("x"), 1<<40); err == nil {
		t.Error("a sparse write 1 TB away must not bypass the quota")
	}
	sp.Close()

	// renames: v3 never overwrites, POSIX does; the root can't be removed
	cl.Mkdir("/world")
	if err := cl.Rename("/plugins.yml", "/world"); err == nil {
		t.Error("Rename must not overwrite")
	}
	if err := cl.PosixRename("/plugins.yml", "/world/p.yml"); err != nil {
		t.Errorf("PosixRename: %v", err)
	}
	if err := cl.RemoveDirectory("/"); err == nil {
		t.Error("the root folder can't be removed")
	}
	if list, err := cl.ReadDir("/world"); err != nil || len(list) != 1 || list[0].Name() != "p.yml" {
		t.Errorf("list: %v %v", list, err)
	}
}

func TestSFTPAuth(t *testing.T) {
	a, db := newTestApp(t, false)
	owner, _ := db.CreateUser("alice", "alice@example.com", "alice-password-1", false)
	helper, _ := db.CreateUser("bob.smith", "bob@example.com", "bob-password-123", false)
	stranger, _ := db.CreateUser("eve", "eve@example.com", "eve-password-123", false)
	secure, _ := db.CreateUser("carol", "carol@example.com", "carol-password-1", false)
	if err := a.seedTemplates(); err != nil {
		t.Fatal(err)
	}
	var tid int64
	db.QueryRow(`SELECT id FROM templates LIMIT 1`).Scan(&tid)
	srv := &Server{ID: "ab12cd34", Name: "s", OwnerID: owner.ID, TemplateID: tid, Env: map[string]string{}, CreatedAt: now()}
	if err := db.InsertServer(srv); err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO server_users(server_id,user_id,permissions,created_at) VALUES(?,?,?,?)`, srv.ID, helper.ID, "files,sftp", now())
	db.Exec(`INSERT INTO server_users(server_id,user_id,permissions,created_at) VALUES(?,?,?,?)`, srv.ID, stranger.ID, "files", now())
	db.Exec(`INSERT INTO server_users(server_id,user_id,permissions,created_at) VALUES(?,?,?,?)`, srv.ID, secure.ID, "sftp", now())
	db.Exec(`INSERT INTO user_totp(user_id,secret,enabled,created_at) VALUES(?,?,1,?)`, secure.ID, "JBSWY3DPEHPK3PXP", now())

	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	db.Exec(`INSERT INTO ssh_keys(user_id,name,fingerprint,public_key,created_at) VALUES(?,?,?,?,?)`, secure.ID, "laptop",
		ssh.FingerprintSHA256(signer.PublicKey()), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), now())

	s, err := newSFTPServer(a, filepath.Join(t.TempDir(), "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, "server.properties"), []byte("motd=hi\n"), 0o644)
	s.openRoot = func(*Server) (Vol, int, error) { r, err := openLocalVol(data); return r, os.Getuid(), err }
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go s.serve(a.ctx, ln)
	t.Cleanup(func() { ln.Close() })

	dial := func(user string, auth ssh.AuthMethod) (*sftp.Client, error) {
		c, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{auth},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
		if err != nil {
			return nil, err
		}
		cl, err := sftp.NewClient(c)
		if err != nil {
			c.Close()
			return nil, err
		}
		t.Cleanup(func() { cl.Close(); c.Close() })
		return cl, nil
	}
	read := func(cl *sftp.Client) string {
		f, err := cl.Open("/server.properties")
		if err != nil {
			return err.Error()
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		return string(b)
	}

	if cl, err := dial("alice.ab12cd34", ssh.Password("alice-password-1")); err != nil || read(cl) != "motd=hi\n" {
		t.Fatalf("owner with password: %v", err)
	}
	if cl, err := dial("bob.smith.ab12cd34", ssh.Password("bob-password-123")); err != nil || read(cl) != "motd=hi\n" {
		t.Fatalf("sub-user with the sftp permission (dotted username): %v", err)
	}
	for name, c := range map[string]struct{ user, pass string }{
		"wrong password":             {"alice.ab12cd34", "nope"},
		"no sftp permission":         {"eve.ab12cd34", "eve-password-123"},
		"unknown server":             {"alice.zzzzzzzz", "alice-password-1"},
		"no server in the username":  {"alice", "alice-password-1"},
		"2FA account, password only": {"carol.ab12cd34", "carol-password-1"},
	} {
		if _, err := dial(c.user, ssh.Password(c.pass)); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	if cl, err := dial("carol.ab12cd34", ssh.PublicKeys(signer)); err != nil || read(cl) != "motd=hi\n" {
		t.Fatalf("2FA account with its SSH key: %v", err)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(other)
	if _, err := dial("carol.ab12cd34", ssh.PublicKeys(otherSigner)); err == nil {
		t.Error("an unknown key must be refused")
	}
	if _, err := dial("alice.ab12cd34", ssh.PublicKeys(signer)); err == nil {
		t.Error("someone else's key must be refused")
	}

	// no shell, no command: only the sftp subsystem
	c, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{User: "alice.ab12cd34", Auth: []ssh.AuthMethod{ssh.Password("alice-password-1")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sess, _ := c.NewSession()
	if out, err := sess.CombinedOutput("id"); err == nil {
		t.Errorf("commands must be refused: %q", out)
	}
	if _, err := c.Dial("tcp", "127.0.0.1:22"); err == nil {
		t.Error("port forwarding must be refused")
	}

	// brute force: the IP gets blocked
	for range 12 {
		dial("alice.ab12cd34", ssh.Password("guess"))
	}
	if _, err := dial("alice.ab12cd34", ssh.Password("alice-password-1")); err == nil {
		t.Error("after many failures the IP must be blocked for a while")
	}

	var n int
	db.QueryRow(`SELECT COUNT(*) FROM activity WHERE action='server.sftp.login'`).Scan(&n)
	if n < 3 {
		t.Errorf("SFTP logins must be in the activity log: %d", n)
	}
	var used int64
	db.QueryRow(`SELECT last_used_at FROM ssh_keys WHERE user_id=?`, secure.ID).Scan(&used)
	if used == 0 {
		t.Error("the key's last use must be recorded")
	}
}

func TestParseSSHKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	line := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	if _, err := parseSSHKey(strings.TrimSpace(line) + " me@laptop"); err != nil {
		t.Errorf("ed25519 with a comment: %v", err)
	}
	for _, bad := range []string{"", "hello", line + line, "ssh-dss AAAAB3NzaC1kc3MAAACBAP", "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQC"} {
		if _, err := parseSSHKey(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
