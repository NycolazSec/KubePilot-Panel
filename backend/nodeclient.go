package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// ---------- Panel side: SSH connections to the node agents (see nodeagent.go)

var errNoAgent = fmt.Errorf("no node agent")

type nodePool struct {
	k       *Kube
	ns      string // namespace of the agents (the panel's release)
	sel     string // label selector of the agents' pods
	token   func() (string, error)
	dial    func(ctx context.Context, addr string) (net.Conn, error)
	resolve func(ctx context.Context, node string) (addr string, pod types.UID, err error) // the agent of a node

	mu      sync.Mutex
	conns   map[string]*nodeConn // by node name
	cur     string               // token the keys come from
	signer  ssh.Signer
	hostKey ssh.PublicKey
}

type nodeConn struct {
	c   *ssh.Client
	pod types.UID
}

func newNodePool(k *Kube, ns, sel string, token func() (string, error)) (*nodePool, error) {
	d := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
	p := &nodePool{k: k, ns: ns, sel: sel, token: token, dial: func(ctx context.Context, addr string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp", addr)
	}, conns: map[string]*nodeConn{}}
	p.resolve = func(ctx context.Context, node string) (string, types.UID, error) {
		pod, err := p.agentPod(ctx, node)
		if err != nil {
			return "", "", err
		}
		return net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(nodeAgentPort)), pod.UID, nil
	}
	if _, _, err := p.keys(); err != nil {
		return nil, err
	}
	return p, nil
}

// keys: the panel's client key and the agents' host key, from the current token.
func (p *nodePool) keys() (ssh.Signer, ssh.PublicKey, error) {
	t, err := p.token()
	if err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if t != p.cur || p.signer == nil {
		hostKey, clientKey := nodeKeys(t)
		signer, err := ssh.NewSignerFromKey(clientKey)
		if err != nil {
			return nil, nil, err
		}
		hk, err := ssh.NewPublicKey(hostKey.Public())
		if err != nil {
			return nil, nil, err
		}
		p.cur, p.signer, p.hostKey = t, signer, hk
	}
	return p.signer, p.hostKey, nil
}

// agentPod finds the running agent of a node.
func (p *nodePool) agentPod(ctx context.Context, node string) (*corev1.Pod, error) {
	if p.k == nil {
		return nil, errNoAgent
	}
	pods, err := p.k.cs.CoreV1().Pods(p.ns).List(ctx, metav1.ListOptions{LabelSelector: p.sel, FieldSelector: "spec.nodeName=" + node})
	if err != nil {
		return nil, err
	}
	for i := range pods.Items {
		if pod := &pods.Items[i]; podReady(pod) {
			return pod, nil
		}
	}
	return nil, errNoAgent
}

// podReady: running, with an address, and its readiness probe passed (the agent accepts connections).
func podReady(p *corev1.Pod) bool {
	if p.Status.Phase != corev1.PodRunning || p.Status.PodIP == "" || p.DeletionTimestamp != nil {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func (p *nodePool) client(ctx context.Context, node string) (*ssh.Client, error) {
	p.mu.Lock()
	nc := p.conns[node]
	p.mu.Unlock()
	if nc != nil {
		return nc.c, nil
	}
	addr, pod, err := p.resolve(ctx, node)
	if err != nil {
		return nil, err
	}
	return p.connect(ctx, node, addr, pod)
}

func (p *nodePool) connect(ctx context.Context, node, addr string, pod types.UID) (*ssh.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := p.dial(ctx, addr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(dl)
	}
	signer, hostKey, err := p.keys()
	if err != nil {
		raw.Close()
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User: nodeAgentUser, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.FixedHostKey(hostKey), ClientVersion: "SSH-2.0-KubePilot-panel", Timeout: 10 * time.Second,
	}
	sc, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		raw.Close()
		return nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	c := ssh.NewClient(sc, chans, reqs)
	nc := &nodeConn{c: c, pod: pod}
	p.mu.Lock()
	if old := p.conns[node]; old != nil { // another request connected first: keep one
		p.mu.Unlock()
		c.Close()
		return old.c, nil
	}
	p.conns[node] = nc
	p.mu.Unlock()
	go p.keepalive(node, nc)
	return c, nil
}

// keepalive drops a connection that stops answering (node down, agent restarted): pending operations fail
// instead of hanging, and the next one reconnects.
func (p *nodePool) keepalive(node string, nc *nodeConn) {
	done := make(chan struct{})
	go func() { _ = nc.c.Wait(); close(done) }()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			p.drop(node, nc)
			return
		case <-t.C:
		}
		ok := make(chan bool, 1)
		go func() {
			_, _, err := nc.c.SendRequest("keepalive@kubepilot", true, nil)
			ok <- err == nil
		}()
		select {
		case good := <-ok:
			if good {
				continue
			}
		case <-time.After(10 * time.Second):
		}
		nc.c.Close()
		p.drop(node, nc)
		return
	}
}

func (p *nodePool) drop(node string, nc *nodeConn) {
	p.mu.Lock()
	if p.conns[node] == nc {
		delete(p.conns, node)
	}
	p.mu.Unlock()
}

// session opens a session running one agent operation; a broken cached connection is replaced once.
func (p *nodePool) session(ctx context.Context, node string, op agentOp) (*ssh.Session, io.WriteCloser, io.Reader, *bytes.Buffer, error) {
	cmd, _ := json.Marshal(op)
	for attempt := 0; ; attempt++ {
		c, err := p.client(ctx, node)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		s, err := newSession(ctx, c)
		if err == nil {
			in, _ := s.StdinPipe()
			out, _ := s.StdoutPipe()
			var stderr bytes.Buffer
			s.Stderr = &stderr
			if err = s.Start(string(cmd)); err == nil {
				return s, in, out, &stderr, nil
			}
			s.Close()
		}
		c.Close()
		p.mu.Lock()
		if nc := p.conns[node]; nc != nil && nc.c == c {
			delete(p.conns, node)
		}
		p.mu.Unlock()
		if attempt > 0 {
			return nil, nil, nil, nil, err
		}
	}
}

// newSession opens a session without waiting forever on a connection whose peer vanished (the keepalive
// would only notice it later): past the deadline the connection is closed, and the caller reconnects.
func newSession(ctx context.Context, c *ssh.Client) (*ssh.Session, error) {
	type res struct {
		s   *ssh.Session
		err error
	}
	ch := make(chan res, 1)
	go func() { s, err := c.NewSession(); ch <- res{s, err} }()
	t := time.NewTimer(10 * time.Second)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.s, r.err
	case <-ctx.Done():
	case <-t.C:
	}
	c.Close()
	go func() {
		if r := <-ch; r.s != nil {
			r.s.Close()
		}
	}()
	return nil, fmt.Errorf("the node agent doesn't answer")
}

// run executes a short operation and returns its output.
func (p *nodePool) run(ctx context.Context, node string, op agentOp) (string, error) {
	s, in, out, stderr, err := p.session(ctx, node, op)
	if err != nil {
		return "", err
	}
	defer s.Close()
	in.Close()
	stop := context.AfterFunc(ctx, func() { s.Close() })
	defer stop()
	b, _ := io.ReadAll(io.LimitReader(out, 1<<20))
	if err := s.Wait(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", agentError(msg)
		}
		return "", err
	}
	return string(b), nil
}

func (p *nodePool) ping(ctx context.Context, node string) error {
	out, err := p.run(ctx, node, agentOp{Op: "ping"})
	if err == nil && out != "ok" {
		err = fmt.Errorf("unexpected answer")
	}
	return err
}

func (p *nodePool) du(ctx context.Context, node, vol string) (int64, error) {
	out, err := p.run(ctx, node, agentOp{Op: "du", Vol: vol})
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

// openVol opens a volume of another node.
func (p *nodePool) openVol(ctx context.Context, node, vol string) (Vol, error) {
	s, in, out, _, err := p.session(ctx, node, agentOp{Op: "sftp", Vol: vol})
	if err != nil {
		return nil, err
	}
	c, err := sftp.NewClientPipe(out, in, sftp.UseConcurrentWrites(true), sftp.UseConcurrentReads(true))
	if err != nil {
		s.Close()
		return nil, err
	}
	return &remoteVol{c: c, s: s, rmall: func(name string) error {
		_, err := p.run(context.Background(), node, agentOp{Op: "rmall", Vol: vol, Path: name})
		return err
	}}, nil
}

// agentError rebuilds an error the agent sent as text (see errno in nodeagent.go).
func agentError(msg string) error {
	for _, e := range agentErrnos {
		if msg == e.Error() {
			return e
		}
	}
	return errors.New(msg)
}

var agentErrnos = []syscall.Errno{syscall.ENOENT, syscall.EEXIST, syscall.ENOTEMPTY, syscall.ENOTDIR, syscall.EISDIR,
	syscall.ELOOP, syscall.EINVAL, syscall.ENOSPC, syscall.EDQUOT, syscall.EXDEV, syscall.ENAMETOOLONG, syscall.EBUSY,
	syscall.ETXTBSY, syscall.EROFS, syscall.EMLINK, syscall.EACCES, syscall.EPERM, syscall.EIO, syscall.ENXIO}

// remoteErr gives SFTP errors the shape os.Root's have (*fs.PathError around an errno), which the
// handlers turn into messages.
func remoteErr(op, name string, err error) error {
	if err == nil {
		return nil
	}
	var se *sftp.StatusError
	var e error
	switch {
	case errors.As(err, &se):
		switch se.FxCode() {
		case sftp.ErrSSHFxNoSuchFile:
			e = syscall.ENOENT
		case sftp.ErrSSHFxPermissionDenied:
			e = syscall.EACCES
		case sftp.ErrSSHFxEOF:
			return io.EOF
		default:
			e = fmt.Errorf("remote failure")
			for _, n := range agentErrnos {
				if strings.Contains(se.Error(), strconv.Quote(n.Error())) {
					e = n
					break
				}
			}
			if errors.Is(e, syscall.EINVAL) || e.Error() == "remote failure" {
				if strings.Contains(se.Error(), `"path escapes from parent"`) {
					e = fmt.Errorf("path escapes from parent")
				} else if strings.Contains(se.Error(), strconv.Quote(errNotRegular.Error())) {
					e = errNotRegular
				}
			}
		}
	case errors.Is(err, io.EOF):
		return io.EOF
	case errors.Is(err, os.ErrNotExist):
		e = syscall.ENOENT
	case errors.Is(err, os.ErrPermission):
		e = syscall.EACCES
	default:
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return err
		}
		e = err
	}
	return &fs.PathError{Op: op, Path: name, Err: e}
}

// ---------- The remote volume

type remoteVol struct {
	c     *sftp.Client
	s     *ssh.Session
	rmall func(name string) error
}

// sftp paths are absolute: "plugins/a.yml" → "/plugins/a.yml"
func rpath(name string) string { return path.Clean("/" + name) }

func (v *remoteVol) Close() error {
	err := v.c.Close()
	v.s.Close()
	return err
}

func (v *remoteVol) Stat(name string) (fs.FileInfo, error) {
	st, err := v.c.Stat(rpath(name))
	return st, remoteErr("stat", name, err)
}

func (v *remoteVol) Lstat(name string) (fs.FileInfo, error) {
	st, err := v.c.Lstat(rpath(name))
	return st, remoteErr("lstat", name, err)
}

func (v *remoteVol) Open(name string) (File, error) { return v.OpenFile(name, os.O_RDONLY, 0) }

func (v *remoteVol) OpenFile(name string, flag int, perm fs.FileMode) (File, error) {
	flag &^= syscall.O_NONBLOCK | syscall.O_CLOEXEC | os.O_SYNC // the agent opens everything non-blocking
	if flag&(os.O_WRONLY|os.O_RDWR) == 0 && flag&os.O_CREATE == 0 {
		st, err := v.c.Stat(rpath(name))
		if err != nil {
			return nil, remoteErr("open", name, err)
		}
		if st.IsDir() { // SFTP reads directories with opendir, not open
			return &remoteDir{v: v, name: name, st: st}, nil
		}
	}
	f, err := v.c.OpenFile(rpath(name), flag)
	if err != nil {
		return nil, remoteErr("open", name, err)
	}
	if p := perm.Perm(); flag&os.O_CREATE != 0 && p != 0 && p != 0o644 {
		_ = f.Chmod(p)
	}
	return &remoteFile{File: f, name: name}, nil
}

func (v *remoteVol) Mkdir(name string, perm fs.FileMode) error {
	if err := v.c.Mkdir(rpath(name)); err != nil {
		return remoteErr("mkdir", name, err)
	}
	if p := perm.Perm(); p != 0 && p != 0o755 {
		_ = v.c.Chmod(rpath(name), p)
	}
	return nil
}

// MkdirAll stops at the first error that isn't "missing", like os.Root's: a symlink leaving the volume
// is reported as such.
func (v *remoteVol) MkdirAll(name string, perm fs.FileMode) error {
	p := rel(name)
	if p == "." {
		return nil
	}
	st, err := v.Stat(p)
	if err == nil {
		if st.IsDir() {
			return nil
		}
		return &fs.PathError{Op: "mkdir", Path: name, Err: syscall.ENOTDIR}
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if parent := path.Dir(p); parent != "." {
		if err := v.MkdirAll(parent, perm); err != nil {
			return err
		}
	}
	err = v.Mkdir(p, perm)
	if errors.Is(err, fs.ErrExist) {
		if st, e := v.Lstat(p); e == nil && st.IsDir() {
			return nil
		}
	}
	return err
}

func (v *remoteVol) Remove(name string) error {
	return remoteErr("remove", name, v.c.Remove(rpath(name)))
}

func (v *remoteVol) RemoveAll(name string) error {
	if rel(name) == "." {
		return &fs.PathError{Op: "removeall", Path: name, Err: syscall.EINVAL}
	}
	if _, err := v.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return nil // like os.RemoveAll
	}
	if err := v.rmall(name); err != nil {
		return &fs.PathError{Op: "removeall", Path: name, Err: err}
	}
	return nil
}

func (v *remoteVol) Rename(o, n string) error {
	return remoteErr("rename", o, v.c.PosixRename(rpath(o), rpath(n)))
}

func (v *remoteVol) Lchown(name string, uid, gid int) error {
	return remoteErr("lchown", name, v.c.Chown(rpath(name), uid, gid))
}

func (v *remoteVol) Chtimes(name string, a, m time.Time) error {
	return remoteErr("chtimes", name, v.c.Chtimes(rpath(name), a, m))
}

func (v *remoteVol) Readlink(name string) (string, error) {
	t, err := v.c.ReadLink(rpath(name))
	return t, remoteErr("readlink", name, err)
}

func (v *remoteVol) readDir(name string) ([]fs.DirEntry, error) {
	infos, err := v.c.ReadDir(rpath(name))
	if err != nil {
		return nil, remoteErr("readdir", name, err)
	}
	out := make([]fs.DirEntry, 0, len(infos))
	for _, i := range infos {
		out = append(out, fs.FileInfoToDirEntry(i))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (v *remoteVol) FS() fs.FS { return remoteFS{v} }

type remoteFile struct {
	*sftp.File
	name string
}

func (f *remoteFile) Read(b []byte) (int, error) {
	n, err := f.File.Read(b)
	if err != nil && !errors.Is(err, io.EOF) {
		err = remoteErr("read", f.name, err)
	}
	return n, err
}

func (f *remoteFile) Write(b []byte) (int, error) {
	n, err := f.File.Write(b)
	return n, remoteErr("write", f.name, err)
}

func (f *remoteFile) WriteAt(b []byte, off int64) (int, error) {
	n, err := f.File.WriteAt(b, off)
	return n, remoteErr("write", f.name, err)
}

func (f *remoteFile) ReadDir(int) ([]fs.DirEntry, error) {
	return nil, &fs.PathError{Op: "readdir", Path: f.name, Err: syscall.ENOTDIR}
}

func (f *remoteFile) Truncate(size int64) error {
	return remoteErr("truncate", f.name, f.File.Truncate(size))
}

// remoteDir is an open directory: only Stat and ReadDir work, like a directory *os.File.
type remoteDir struct {
	v       *remoteVol
	name    string
	st      fs.FileInfo
	entries []fs.DirEntry
	read    bool
}

func (d *remoteDir) isDir(op string) error {
	return &fs.PathError{Op: op, Path: d.name, Err: syscall.EISDIR}
}
func (d *remoteDir) Read([]byte) (int, error)             { return 0, d.isDir("read") }
func (d *remoteDir) Write([]byte) (int, error)            { return 0, d.isDir("write") }
func (d *remoteDir) ReadAt([]byte, int64) (int, error)    { return 0, d.isDir("read") }
func (d *remoteDir) WriteAt([]byte, int64) (int, error)   { return 0, d.isDir("write") }
func (d *remoteDir) Seek(int64, int) (int64, error)       { return 0, d.isDir("seek") }
func (d *remoteDir) Truncate(int64) error                 { return d.isDir("truncate") }
func (d *remoteDir) Close() error                         { return nil }
func (d *remoteDir) Stat() (fs.FileInfo, error)           { return d.st, nil }
func (d *remoteDir) ReadDir(n int) ([]fs.DirEntry, error) { return d.readDirN(n) }

func (d *remoteDir) readDirN(n int) ([]fs.DirEntry, error) {
	if !d.read {
		e, err := d.v.readDir(d.name)
		if err != nil {
			return nil, err
		}
		d.entries, d.read = e, true
	}
	if n <= 0 {
		out := d.entries
		d.entries = nil
		return out, nil
	}
	if len(d.entries) == 0 {
		return nil, io.EOF
	}
	if n > len(d.entries) {
		n = len(d.entries)
	}
	out := d.entries[:n]
	d.entries = d.entries[n:]
	return out, nil
}

// remoteFS is the volume as an fs.FS (fs.WalkDir, fs.ReadDir).
type remoteFS struct{ v *remoteVol }

func (f remoteFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	return f.v.Open(name)
}

func (f remoteFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	return f.v.readDir(name)
}

func (f remoteFS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	return f.v.Stat(name)
}
