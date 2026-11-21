package main

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// ---------- Node agent: the game volumes of one node, served to the panel
//
// With several nodes, a server's volume lives on the node where it was created, and the panel (on another
// node) can't open it. One agent per node (DaemonSet `kubepilot node`) serves the volumes of its node over
// SSH: SFTP for the files, plus a few commands that would be slow file by file (disk usage, recursive
// delete). Both ends derive their keys from one shared secret, so the panel checks the agent's host key
// and the agent accepts no other client. An agent has no Kubernetes credentials at all.

const (
	nodeAgentPort   = 7070
	nodeAgentUser   = "kubepilot"
	nodeAgentBanner = "SSH-2.0-KubePilot-node"
)

// nodeKeys derives the agents' host key and the panel's client key from the shared token.
func nodeKeys(token string) (host, client ed25519.PrivateKey) {
	derive := func(label string) ed25519.PrivateKey {
		m := hmac.New(sha256.New, []byte(token))
		m.Write([]byte(label))
		return ed25519.NewKeyFromSeed(m.Sum(nil))
	}
	return derive("kubepilot node host key v1"), derive("kubepilot node client key v1")
}

// agentOp is the command of one SSH session (the exec request's payload).
type agentOp struct {
	Op   string `json:"op"`             // sftp, du, rmall, ping
	Vol  string `json:"vol,omitempty"`  // directory of the volume, below the storage root
	Path string `json:"path,omitempty"` // rmall: path inside the volume
}

// validVolDir accepts only the folder of a game volume: <claim> (isolated StorageClass) or
// pvc-<uid>_<namespace>_<claim> (plain local-path), the claim being srv-<id>-data.
func validVolDir(dir, ns string) bool {
	if dir == "" || dir != filepath.Base(dir) || strings.Contains(dir, "..") {
		return false
	}
	re := `^(pvc-[0-9a-f-]{36}_` + regexp.QuoteMeta(ns) + `_)?srv-[a-z0-9]{1,32}-data$`
	return regexp.MustCompile(re).MatchString(dir)
}

type nodeAgent struct {
	root  string // folder of the game volumes on this node
	ns    string // the servers' namespace
	token func() (string, error)

	mu  sync.Mutex
	cur string // token the configuration was built from
	cfg *ssh.ServerConfig
}

// nodeTokenSource reads the shared token from a file (a mounted Secret: a new token reaches running pods
// without a restart) or from KUBEPILOT_NODE_TOKEN.
func nodeTokenSource() func() (string, error) {
	file := os.Getenv("KUBEPILOT_NODE_TOKEN_FILE")
	return func() (string, error) {
		t := os.Getenv("KUBEPILOT_NODE_TOKEN")
		if file != "" {
			b, err := os.ReadFile(file)
			if err != nil {
				return "", err
			}
			t = strings.TrimSpace(string(b))
		}
		if len(t) < 32 {
			return "", fmt.Errorf("node token: at least 32 characters required")
		}
		return t, nil
	}
}

// config is the SSH configuration for the current token.
func (ag *nodeAgent) config() (*ssh.ServerConfig, error) {
	t, err := ag.token()
	if err != nil {
		return nil, err
	}
	ag.mu.Lock()
	defer ag.mu.Unlock()
	if t != ag.cur || ag.cfg == nil {
		cfg, err := nodeAgentConfig(t)
		if err != nil {
			return nil, err
		}
		ag.cur, ag.cfg = t, cfg
	}
	return ag.cfg, nil
}

func runNodeAgent() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("node: ")
	ag := &nodeAgent{root: filepath.Clean(env("KUBEPILOT_STORAGE_ROOT", "/var/lib/rancher/k3s/storage")), ns: env("KUBEPILOT_NAMESPACE", "kubepilot-servers"),
		token: nodeTokenSource()}
	if _, err := ag.config(); err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", env("KUBEPILOT_NODE_LISTEN", ":"+strconv.Itoa(nodeAgentPort)))
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() { <-ctx.Done(); ln.Close() }()
	log.Printf("serving %s on %s", ag.root, ln.Addr())
	ag.serve(ln)
}

func nodeAgentConfig(token string) (*ssh.ServerConfig, error) {
	hostKey, clientKey := nodeKeys(token)
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		return nil, err
	}
	allowed, err := ssh.NewPublicKey(clientKey.Public())
	if err != nil {
		return nil, err
	}
	want := allowed.Marshal()
	cfg := &ssh.ServerConfig{
		ServerVersion: nodeAgentBanner,
		MaxAuthTries:  2,
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if meta.User() == nodeAgentUser && subtle.ConstantTimeCompare(key.Marshal(), want) == 1 {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown key")
		},
	}
	cfg.AddHostKey(signer)
	return cfg, nil
}

func (ag *nodeAgent) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("accept: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go ag.handleConn(c)
	}
}

func (ag *nodeAgent) handleConn(c net.Conn) {
	cfg, err := ag.config()
	if err != nil {
		log.Printf("refusing connections: %v", err)
		c.Close()
		return
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return // probes, or a client without the key: nothing to log
	}
	_ = c.SetDeadline(time.Time{})
	defer conn.Close()
	go func() { // keepalives from the panel
		for r := range reqs {
			if r.WantReply {
				_ = r.Reply(r.Type == "keepalive@kubepilot", nil)
			}
		}
	}()
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go ag.session(ch, creqs)
	}
}

func (ag *nodeAgent) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	var op agentOp
	for r := range reqs {
		if r.Type != "exec" || len(r.Payload) < 4 {
			if r.WantReply {
				_ = r.Reply(false, nil)
			}
			continue
		}
		n := binary.BigEndian.Uint32(r.Payload)
		if int(n) != len(r.Payload)-4 || json.Unmarshal(r.Payload[4:], &op) != nil {
			_ = r.Reply(false, nil)
			return
		}
		_ = r.Reply(true, nil)
		go func() { // later requests (none expected) are refused
			for r := range reqs {
				if r.WantReply {
					_ = r.Reply(false, nil)
				}
			}
		}()
		break
	}
	if op.Op == "" {
		return
	}
	err := ag.run(ch, op)
	status := uint32(0)
	if err != nil {
		status = 1
		fmt.Fprint(ch.Stderr(), err.Error())
	}
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
}

func (ag *nodeAgent) run(ch ssh.Channel, op agentOp) error {
	if op.Op == "ping" {
		_, err := io.WriteString(ch, "ok")
		return err
	}
	if !validVolDir(op.Vol, ag.ns) {
		return fmt.Errorf("invalid volume")
	}
	root, err := os.OpenRoot(filepath.Join(ag.root, op.Vol))
	if err != nil {
		return errno(err)
	}
	defer root.Close()
	switch op.Op {
	case "sftp":
		h := &agentFS{root: root}
		rs := sftp.NewRequestServer(ch, sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h})
		err := rs.Serve()
		rs.Close()
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	case "du":
		_, err := io.WriteString(ch, strconv.FormatInt(dirSizeRoot(root), 10))
		return err
	case "rmall":
		p := rel(op.Path)
		if p == "." {
			return fmt.Errorf("refusing to remove the volume itself")
		}
		return errno(root.RemoveAll(p))
	}
	return fmt.Errorf("unknown operation")
}

// dirSizeRoot adds up the regular files of a volume.
func dirSizeRoot(root *os.Root) int64 {
	var total int64
	_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// errno keeps only the error number (or os.Root's own message): the panel rebuilds the error from it,
// and the node's paths never leave the agent.
func errno(err error) error {
	if err == nil {
		return nil
	}
	var e syscall.Errno
	if errors.As(err, &e) {
		return e
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return errors.New(pe.Err.Error())
	}
	return err
}

// ---------- The volume over SFTP: the panel's operations, inside an os.Root

type agentFS struct{ root *os.Root }

// sftp paths are absolute ("/plugins/a.yml"): back to root-relative ones
func agentPath(p string) string { return rel(p) }

var errNotRegular = fmt.Errorf("not a regular file")

// openRegularAt opens a file without following it into a FIFO or a device (see openRegular).
func (h *agentFS) openRegularAt(p string, flags int) (*os.File, error) {
	f, err := h.root.OpenFile(p, flags|syscall.O_NONBLOCK, 0o644)
	if err != nil {
		return nil, errno(err)
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		f.Close()
		if err == nil && st.IsDir() {
			return nil, syscall.EISDIR
		}
		return nil, errNotRegular
	}
	return f, nil
}

func (h *agentFS) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	return h.openRegularAt(agentPath(r.Filepath), os.O_RDONLY)
}

func (h *agentFS) Filewrite(r *sftp.Request) (io.WriterAt, error) { return h.OpenFile(r) }

func (h *agentFS) OpenFile(r *sftp.Request) (sftp.WriterAtReaderAt, error) {
	pf := r.Pflags()
	flags := os.O_WRONLY // never O_APPEND: the client writes at explicit offsets
	if pf.Read {
		flags = os.O_RDWR
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
	p := agentPath(r.Filepath)
	if st, err := h.root.Lstat(p); err == nil && !st.Mode().IsRegular() && st.Mode()&fs.ModeSymlink == 0 {
		if st.IsDir() {
			return nil, syscall.EISDIR
		}
		return nil, errNotRegular // never truncate or write a FIFO or a device
	}
	return h.openRegularAt(p, flags)
}

func (h *agentFS) Filecmd(r *sftp.Request) error {
	p := agentPath(r.Filepath)
	switch r.Method {
	case "Setstat":
		return h.setstat(p, r)
	case "Rename", "PosixRename":
		return errno(h.root.Rename(p, agentPath(r.Target)))
	case "Rmdir", "Remove":
		return errno(h.root.Remove(p))
	case "Mkdir":
		return errno(h.root.Mkdir(p, 0o755))
	case "Link", "Symlink": // the panel never creates links: a link could point a later read at another file
		return syscall.EPERM
	}
	return sftp.ErrSSHFxOpUnsupported
}

func (h *agentFS) PosixRename(r *sftp.Request) error { return h.Filecmd(r) }

// setstat: owner (never through a symlink), mode, times, size.
func (h *agentFS) setstat(p string, r *sftp.Request) error {
	flags, attrs := r.AttrFlags(), r.Attributes()
	if flags.UidGid {
		if err := h.root.Lchown(p, int(attrs.UID), int(attrs.GID)); err != nil {
			return errno(err)
		}
	}
	if flags.Permissions {
		if err := h.root.Chmod(p, attrs.FileMode().Perm()); err != nil {
			return errno(err)
		}
	}
	if flags.Acmodtime {
		if err := h.root.Chtimes(p, time.Unix(int64(attrs.Atime), 0), time.Unix(int64(attrs.Mtime), 0)); err != nil {
			return errno(err)
		}
	}
	if flags.Size {
		f, err := h.openRegularAt(p, os.O_WRONLY)
		if err != nil {
			return err
		}
		defer f.Close()
		return errno(f.Truncate(int64(attrs.Size)))
	}
	return nil
}

func (h *agentFS) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	p := agentPath(r.Filepath)
	switch r.Method {
	case "List":
		d, err := h.root.Open(p)
		if err != nil {
			return nil, errno(err)
		}
		defer d.Close()
		entries, err := d.ReadDir(-1)
		if err != nil {
			return nil, errno(err)
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
			return nil, errno(err)
		}
		return listerAt{st}, nil
	}
	return nil, sftp.ErrSSHFxOpUnsupported
}

func (h *agentFS) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	st, err := h.root.Lstat(agentPath(r.Filepath))
	if err != nil {
		return nil, errno(err)
	}
	return listerAt{st}, nil
}

func (h *agentFS) Readlink(p string) (string, error) {
	t, err := h.root.Readlink(agentPath(p))
	return t, errno(err)
}

func (h *agentFS) RealPath(p string) (string, error) { return path.Clean("/" + p), nil }
