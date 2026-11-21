package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Built-in SFTP server. Users sign in as <username>.<server id> with their panel password, or with an SSH key
// from My account (required when two-factor authentication is on: a password alone must not bypass it).
// A session only sees that server's volume through os.Root, with the web file manager's rules: no symlinks
// created, no FIFO or device opened, writes counted against the disk quota.

const (
	sftpMaxPerIP     = 16
	sftpMaxTotal     = 256
	sftpHandshake    = 30 * time.Second
	sftpIdle         = 15 * time.Minute
	sftpLoggedChange = 20 // paths kept for the activity log of a session
)

var errSFTPDenied = internalErr("sftp: access denied") // never shown: SSH only says "denied"

const nonBlock = syscall.O_NONBLOCK // a FIFO planted by the game must not block the session

type sftpServer struct {
	a           *App
	config      *ssh.ServerConfig
	fingerprint string
	openRoot    func(*Server) (Vol, int, error) // the server's volume (replaced in tests)

	mu    sync.Mutex
	perIP map[string]int
	total int
}

// loadHostKey reads the server's ed25519 host key, creating it on first start.
func loadHostKey(file string) (ssh.Signer, error) {
	if b, err := os.ReadFile(file); err == nil {
		return ssh.ParsePrivateKey(b)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "kubepilot sftp")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(file, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(priv)
}

func newSFTPServer(a *App, hostKeyFile string) (*sftpServer, error) {
	key, err := loadHostKey(hostKeyFile)
	if err != nil {
		return nil, fmt.Errorf("sftp host key: %w", err)
	}
	s := &sftpServer{a: a, perIP: map[string]int{}, fingerprint: ssh.FingerprintSHA256(key.PublicKey()),
		openRoot: func(srv *Server) (Vol, int, error) { return a.openRoot(a.ctx, srv) }}
	s.config = &ssh.ServerConfig{
		ServerVersion:     "SSH-2.0-KubePilot",
		MaxAuthTries:      6,
		PasswordCallback:  s.password,
		PublicKeyCallback: s.publicKey,
	}
	s.config.AddHostKey(key)
	return s, nil
}

func (s *sftpServer) serve(ctx context.Context, ln net.Listener) {
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go s.handle(nc)
	}
}

func hostOf(addr net.Addr) string {
	h, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return h
}

func (s *sftpServer) acquire(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.total >= sftpMaxTotal || s.perIP[ip] >= sftpMaxPerIP {
		return false
	}
	s.total++
	s.perIP[ip]++
	return true
}

func (s *sftpServer) release(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total--
	if s.perIP[ip]--; s.perIP[ip] <= 0 {
		delete(s.perIP, ip)
	}
}

// splitSFTPUser: "alice.ab12cd34" → ("alice", "ab12cd34"). Usernames may contain dots, server IDs don't.
func splitSFTPUser(name string) (login, serverID string, ok bool) {
	i := strings.LastIndex(name, ".")
	if i <= 0 || i == len(name)-1 {
		return "", "", false
	}
	return strings.ToLower(name[:i]), name[i+1:], true
}

// access checks that the user may open this server over SFTP.
func (s *sftpServer) access(u *User, serverID string) (*Server, bool) {
	srv, err := s.a.db.Server(serverID)
	if err != nil {
		return nil, false
	}
	perms, ok := s.a.permsFor(u, srv)
	if !ok || !slices.Contains(perms, "sftp") || (srv.Suspended && !u.IsAdmin) {
		return nil, false
	}
	return srv, true
}

func sftpPerms(u *User, srv *Server, method string) *ssh.Permissions {
	return &ssh.Permissions{Extensions: map[string]string{"user": fmt.Sprint(u.ID), "server": srv.ID, "method": method}}
}

func (s *sftpServer) password(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
	a, ip := s.a, hostOf(c.RemoteAddr())
	if !a.limiter.allow(ip) {
		return nil, errSFTPDenied
	}
	login, sid, ok := splitSFTPUser(c.User())
	u, err := a.db.UserByLogin(login)
	if (!ok || err != nil || !a.db.knownIP(u.ID, ip)) && !a.accounts.allow(login) {
		return nil, errSFTPDenied
	}
	if !ok || err != nil {
		(&User{hash: dummyHash()}).CheckPassword(string(pass))
		return nil, errSFTPDenied
	}
	if !u.CheckPassword(string(pass)) {
		a.db.LogActivity("", u, ip, "auth.fail", "SFTP")
		return nil, errSFTPDenied
	}
	if u.TwoFactor || a.twoFactorRequired(u) {
		a.db.LogActivity("", u, ip, "auth.fail", "SFTP: two-factor account, SSH key required")
		return nil, errSFTPDenied
	}
	srv, ok := s.access(u, sid)
	if !ok {
		return nil, errSFTPDenied
	}
	a.limiter.reset(ip)
	return sftpPerms(u, srv, "password"), nil
}

// publicKey may be called for keys that are only offered, not proven: nothing is logged here, the session
// is logged once the handshake succeeded, with the permissions of the key that was really used.
func (s *sftpServer) publicKey(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	login, sid, ok := splitSFTPUser(c.User())
	if !ok {
		return nil, errSFTPDenied
	}
	u, err := s.a.db.UserByLogin(login)
	if err != nil {
		return nil, errSFTPDenied
	}
	fp := ssh.FingerprintSHA256(key)
	if !s.a.db.HasSSHKey(u.ID, fp) {
		return nil, errSFTPDenied
	}
	srv, ok := s.access(u, sid)
	if !ok {
		return nil, errSFTPDenied
	}
	return sftpPerms(u, srv, "key "+fp), nil
}

// idleConn closes connections that stay silent for too long.
type idleConn struct{ net.Conn }

func (c idleConn) Read(b []byte) (int, error) {
	_ = c.Conn.SetDeadline(time.Now().Add(sftpIdle))
	return c.Conn.Read(b)
}

func (c idleConn) Write(b []byte) (int, error) {
	_ = c.Conn.SetDeadline(time.Now().Add(sftpIdle))
	return c.Conn.Write(b)
}

func (s *sftpServer) handle(nc net.Conn) {
	ip := hostOf(nc.RemoteAddr())
	if !s.acquire(ip) {
		nc.Close()
		return
	}
	defer s.release(ip)
	defer nc.Close()
	timer := time.AfterFunc(sftpHandshake, func() { nc.Close() })
	conn, chans, reqs, err := ssh.NewServerConn(idleConn{nc}, s.config)
	timer.Stop()
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs) // no port forwarding, no agent
	ext := conn.Permissions.Extensions
	uid, _ := strconv.ParseInt(ext["user"], 10, 64)
	u, err1 := s.a.db.UserByID(uid)
	srv, err2 := s.a.db.Server(ext["server"])
	if err1 != nil || err2 != nil {
		return
	}
	if strings.HasPrefix(ext["method"], "key ") {
		s.a.db.TouchSSHKey(u.ID, strings.TrimPrefix(ext["method"], "key "), ip)
	}
	s.a.db.LogActivity(srv.ID, u, ip, "server.sftp.login", ext["method"])
	for nch := range chans {
		if nch.ChannelType() != "session" {
			_ = nch.Reject(ssh.UnknownChannelType, "only SFTP is available")
			continue
		}
		ch, chReqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go s.session(ch, chReqs, u, srv, ip)
	}
}

// session only accepts the "sftp" subsystem: no shell, no command, no terminal.
func (s *sftpServer) session(ch ssh.Channel, reqs <-chan *ssh.Request, u *User, srv *Server, ip string) {
	defer ch.Close()
	started := false
	for req := range reqs {
		ok := !started && req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp"
		if req.WantReply {
			_ = req.Reply(ok, nil)
		}
		if !ok {
			continue
		}
		started = true
		go func() {
			defer ch.Close()
			if err := s.serveSFTP(ch, u, srv, ip); err != nil && !errors.Is(err, io.EOF) {
				log.Printf("sftp %s/%s: %v", u.Username, srv.ID, err)
			}
		}()
	}
}

func (s *sftpServer) serveSFTP(ch ssh.Channel, u *User, srv *Server, ip string) error {
	a := s.a
	root, uid, err := s.openRoot(srv)
	if err != nil {
		return err
	}
	defer root.Close()
	h := &sftpFS{a: a, srv: srv, root: root, uid: uid}
	rs := sftp.NewRequestServer(ch, sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h})
	err = rs.Serve()
	rs.Close()
	if h.changed() {
		if a.kube != nil {
			go a.refreshDisk(a.ctx, srv.ID)
		}
		a.db.LogActivity(srv.ID, u, ip, "server.sftp.changes", h.summary())
	}
	return err
}

// ---------- File system of one session

type sftpFS struct {
	a    *App
	srv  *Server
	root Vol
	uid  int

	mu                               sync.Mutex
	uploads, deletes, renames, mkdir int
	written                          int64
	paths                            []string
}

func (h *sftpFS) note(kind *int, p string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	*kind++
	if len(h.paths) < sftpLoggedChange && !slices.Contains(h.paths, "/"+p) {
		h.paths = append(h.paths, "/"+p)
	}
}

func (h *sftpFS) changed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.uploads+h.deletes+h.renames+h.mkdir > 0
}

func (h *sftpFS) summary() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := fmt.Sprintf("%d uploaded (%s), %d deleted, %d renamed, %d folders: %s", h.uploads, humanBytes("en", h.written),
		h.deletes, h.renames, h.mkdir, strings.Join(h.paths, ", "))
	return truncate(s, 900)
}

// sftpPath maps a client path to a root-relative one; the root itself can't be written, renamed or deleted.
func sftpPath(p string, allowRoot bool) (string, error) {
	r := rel(p)
	if r == "." && !allowRoot {
		return "", os.ErrPermission
	}
	return r, nil
}

// Fileread: downloads (regular files only).
func (h *sftpFS) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	p, err := sftpPath(r.Filepath, false)
	if err != nil {
		return nil, err
	}
	f, st, err := openRegular(h.root, p)
	if err != nil {
		return nil, sftpErr(err)
	}
	if st.IsDir() {
		f.Close()
		return nil, sftp.ErrSSHFxFailure
	}
	return f, nil
}

// sftpFile is an upload handle: every byte past the end of the file is reserved on the server's quota,
// shared with the other sessions and the web file manager.
type sftpFile struct {
	h    *sftpFS
	f    File
	path string

	mu       sync.Mutex
	size     int64
	added    int64
	created  bool // the file didn't exist before this upload
	overflow bool // a write hit the quota
}

func (w *sftpFile) WriteAt(b []byte, off int64) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if off < 0 {
		return 0, sftp.ErrSSHFxFailure
	}
	if end := off + int64(len(b)); end > w.size {
		if !w.h.a.reserveDisk(w.h.srv, end-w.size) {
			w.overflow = true
			return 0, errQuota()
		}
		w.added += end - w.size
		w.size = end
	}
	return w.f.WriteAt(b, off)
}

func (w *sftpFile) ReadAt(b []byte, off int64) (int, error) { return w.f.ReadAt(b, off) }

func (w *sftpFile) Close() error {
	err := w.f.Close()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.overflow && w.created { // like the web upload: a file cut by the quota is removed, not left full
		if w.h.root.Remove(w.path) == nil {
			w.h.a.releaseDisk(w.h.srv.ID, w.size)
		}
		return err
	}
	chownNew(w.h.root, w.path, w.h.uid)
	w.h.mu.Lock()
	w.h.written += w.added
	w.h.mu.Unlock()
	return err
}

func (h *sftpFS) Filewrite(r *sftp.Request) (io.WriterAt, error) { return h.open(r) }

func (h *sftpFS) OpenFile(r *sftp.Request) (sftp.WriterAtReaderAt, error) { return h.open(r) }

func (h *sftpFS) open(r *sftp.Request) (*sftpFile, error) {
	p, err := sftpPath(r.Filepath, false)
	if err != nil {
		return nil, err
	}
	pf := r.Pflags()
	flags := os.O_WRONLY | nonBlock // never O_APPEND: the client writes at explicit offsets
	if pf.Read {
		flags = os.O_RDWR | nonBlock
	}
	if pf.Creat {
		flags |= os.O_CREATE
	}
	if pf.Excl {
		flags |= os.O_EXCL
	}
	if pf.Trunc {
		flags |= os.O_TRUNC
	}
	var before int64
	existed := false
	if st, err := h.root.Lstat(p); err == nil {
		existed = true
		if !st.Mode().IsRegular() && st.Mode()&fs.ModeSymlink == 0 {
			return nil, os.ErrPermission
		}
		before = st.Size()
	}
	f, err := h.root.OpenFile(p, flags, 0o644)
	if err != nil {
		return nil, sftpErr(err)
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, os.ErrPermission
	}
	if pf.Trunc && before > st.Size() {
		h.a.releaseDisk(h.srv.ID, before-st.Size())
	}
	h.note(&h.uploads, p)
	return &sftpFile{h: h, f: f, path: p, size: st.Size(), created: !existed}, nil
}

func (h *sftpFS) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Link", "Symlink": // a link could point the game, or a later download, at another file of the volume
		return os.ErrPermission
	case "Setstat":
		return h.setstat(r)
	}
	p, err := sftpPath(r.Filepath, false)
	if err != nil {
		return err
	}
	switch r.Method {
	case "Mkdir":
		if err := h.root.Mkdir(p, 0o755); err != nil {
			return sftpErr(err)
		}
		chownNew(h.root, p, h.uid)
		h.note(&h.mkdir, p)
	case "Rmdir":
		st, err := h.root.Lstat(p)
		if err != nil {
			return sftpErr(err)
		}
		if !st.IsDir() {
			return sftp.ErrSSHFxFailure
		}
		if err := h.root.Remove(p); err != nil {
			return sftpErr(err)
		}
		h.note(&h.deletes, p)
	case "Remove":
		st, err := h.root.Lstat(p)
		if err != nil {
			return sftpErr(err)
		}
		if st.IsDir() {
			return sftp.ErrSSHFxFailure
		}
		if err := h.root.Remove(p); err != nil {
			return sftpErr(err)
		}
		if st.Mode().IsRegular() {
			h.a.releaseDisk(h.srv.ID, st.Size())
		}
		h.note(&h.deletes, p)
	case "Rename", "PosixRename":
		to, err := sftpPath(r.Target, false)
		if err != nil {
			return err
		}
		if r.Method == "Rename" { // SFTP v3: never overwrite
			if _, err := h.root.Lstat(to); err == nil {
				return sftp.ErrSSHFxFailure
			}
		}
		if err := h.root.Rename(p, to); err != nil {
			return sftpErr(err)
		}
		h.note(&h.renames, to)
	default:
		return sftp.ErrSSHFxOpUnsupported
	}
	return nil
}

func (h *sftpFS) PosixRename(r *sftp.Request) error { return h.Filecmd(r) }

// setstat: size (truncate / extend, counted on the quota) and times; owners and modes stay the panel's.
func (h *sftpFS) setstat(r *sftp.Request) error {
	p, err := sftpPath(r.Filepath, true)
	if err != nil {
		return err
	}
	flags, attrs := r.AttrFlags(), r.Attributes()
	if flags.Size && p != "." {
		f, err := h.root.OpenFile(p, os.O_WRONLY|nonBlock, 0)
		if err != nil {
			return sftpErr(err)
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			return os.ErrPermission
		}
		size := int64(attrs.Size)
		if grow := size - st.Size(); grow > 0 && !h.a.reserveDisk(h.srv, grow) {
			return errQuota()
		} else if grow < 0 {
			h.a.releaseDisk(h.srv.ID, -grow)
		}
		if err := f.Truncate(size); err != nil {
			return sftpErr(err)
		}
	}
	if flags.Acmodtime {
		_ = h.root.Chtimes(p, time.Unix(int64(attrs.Atime), 0), time.Unix(int64(attrs.Mtime), 0))
	}
	return nil
}

type listerAt []os.FileInfo

func (l listerAt) ListAt(out []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(out, l[off:])
	if n < len(out) {
		return n, io.EOF
	}
	return n, nil
}

func (h *sftpFS) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	p, _ := sftpPath(r.Filepath, true)
	switch r.Method {
	case "List":
		d, err := h.root.Open(p)
		if err != nil {
			return nil, sftpErr(err)
		}
		defer d.Close()
		entries, err := d.ReadDir(-1)
		if err != nil {
			return nil, sftpErr(err)
		}
		out := make(listerAt, 0, len(entries))
		for _, e := range entries {
			if info, err := e.Info(); err == nil {
				out = append(out, info)
			}
		}
		return out, nil
	case "Stat":
		st, err := h.root.Stat(p)
		if err != nil {
			return nil, sftpErr(err)
		}
		return listerAt{st}, nil
	}
	return nil, sftp.ErrSSHFxOpUnsupported
}

func (h *sftpFS) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	p, _ := sftpPath(r.Filepath, true)
	st, err := h.root.Lstat(p)
	if err != nil {
		return nil, sftpErr(err)
	}
	return listerAt{st}, nil
}

func (h *sftpFS) Readlink(p string) (string, error) {
	r, _ := sftpPath(p, true)
	target, err := h.root.Readlink(r)
	if err != nil {
		return "", sftpErr(err)
	}
	return target, nil
}

func (h *sftpFS) RealPath(p string) (string, error) { return path.Clean("/" + p), nil }

// sftpErr keeps the useful SFTP codes and hides the server's own paths from the client.
func sftpErr(err error) error {
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		return err
	case errors.Is(err, fs.ErrNotExist):
		return os.ErrNotExist
	case errors.Is(err, fs.ErrExist):
		return sftp.ErrSSHFxFailure
	case errors.Is(err, fs.ErrPermission):
		return os.ErrPermission
	}
	return sftp.ErrSSHFxFailure
}
