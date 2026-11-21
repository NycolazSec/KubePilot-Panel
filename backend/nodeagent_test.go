package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

const testNodeToken = "0123456789abcdef0123456789abcdef-test"

// startAgent runs a node agent on localhost, serving the volumes found in root.
func startAgent(t *testing.T, root string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go (&nodeAgent{root: root, ns: "kp-servers", token: staticToken(testNodeToken)}).serve(ln)
	return ln.Addr().String()
}

func staticToken(t string) func() (string, error) { return func() (string, error) { return t, nil } }

func testPool(t *testing.T, addr, token string) *nodePool {
	t.Helper()
	p, err := newNodePool(nil, "", "", staticToken(token))
	if err != nil {
		t.Fatal(err)
	}
	p.resolve = func(context.Context, string) (string, types.UID, error) { return addr, "agent-pod", nil }
	return p
}

// kind names an outcome the same way for both implementations.
func kind(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, fs.ErrNotExist):
		return "notexist"
	case errors.Is(err, syscall.ENOTEMPTY):
		return "notempty"
	case errors.Is(err, fs.ErrExist):
		return "exist"
	case errors.Is(err, syscall.EISDIR):
		return "isdir"
	case errors.Is(err, syscall.ENOTDIR):
		return "notdir"
	case strings.Contains(err.Error(), "path escapes from parent"):
		return "escape"
	case strings.Contains(err.Error(), "Not a regular file") || strings.Contains(err.Error(), "not a regular file"):
		return "notregular"
	case errors.Is(err, fs.ErrPermission):
		return "permission"
	case strings.Contains(err.Error(), "not a small file"):
		return "notsmall"
	}
	return "other: " + err.Error()
}

// volScript runs the panel's file operations on a volume and logs what happened.
func volScript(t *testing.T, v Vol, dir string) []string {
	t.Helper()
	var out []string
	log := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }

	log("mkdirall %s", kind(v.MkdirAll("a/b/c", 0o755)))
	log("mkdir %s", kind(v.Mkdir("a/x", 0o755)))
	log("mkdir again %s", kind(v.Mkdir("a/x", 0o755)))
	f, err := createRegular(v, "a/b/f.txt", 0o644)
	log("create %s", kind(err))
	if err == nil {
		_, err = io.WriteString(f, "hello world")
		log("write %s", kind(err))
		log("close %s", kind(f.Close()))
	}
	f, st, err := openRegular(v, "a/b/f.txt")
	log("open %s", kind(err))
	if err == nil {
		b, _ := io.ReadAll(f)
		buf := make([]byte, 5)
		n, _ := f.ReadAt(buf, 6)
		pos, _ := f.Seek(2, io.SeekStart)
		b2 := make([]byte, 3)
		_, _ = io.ReadFull(f, b2)
		log("read %q at6=%q seek=%d next=%q size=%d regular=%v", b, buf[:n], pos, b2, st.Size(), st.Mode().IsRegular())
		f.Close()
	}
	_, err = v.OpenFile("a/b/f.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	log("excl %s", kind(err))
	f, err = v.OpenFile("a/b/f.txt", os.O_RDWR, 0)
	log("rdwr %s", kind(err))
	if err == nil {
		_, err = f.WriteAt([]byte("HE"), 0)
		log("writeat %s", kind(err))
		log("truncate %s", kind(f.Truncate(8)))
		st, _ := f.Stat()
		log("size after truncate %d", st.Size())
		f.Close()
	}
	b, _ := readSmall(v, "a/b/f.txt", 100)
	log("content %q", b)
	log("readsmall dir %s", kind(func() error { _, err := readSmall(v, "a", 100); return err }()))

	log("rename %s", kind(v.Rename("a/b/f.txt", "a/g.txt")))
	_, err = v.Lstat("a/b/f.txt")
	log("old gone %s", kind(err))
	log("rename over %s", kind(func() error {
		f, _ := createRegular(v, "a/h.txt", 0o644)
		f.Close()
		return v.Rename("a/h.txt", "a/g.txt")
	}()))

	d, err := v.Open("a")
	log("open dir %s", kind(err))
	if err == nil {
		ents, err := d.ReadDir(-1)
		names := []string{}
		for _, e := range ents {
			names = append(names, fmt.Sprintf("%s:%v", e.Name(), e.IsDir()))
		}
		slices.Sort(names)
		log("readdir %s %v", kind(err), names)
		st, _ := d.Stat()
		log("dir stat %v", st.IsDir())
		_, err = d.Read(make([]byte, 1))
		log("read dir fails %v", err != nil) // EISDIR or EINVAL, depending on the file system
		d.Close()
	}
	ents, err := fs.ReadDir(v.FS(), "a")
	log("fs.ReadDir %s %d", kind(err), len(ents))

	// what a game can plant in its volume
	_ = os.Symlink("/etc", filepath.Join(dir, "esc"))
	_ = os.Symlink("a", filepath.Join(dir, "inside"))
	_ = syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644)
	target, err := v.Readlink("esc")
	log("readlink %s %s", kind(err), target)
	_, err = v.Stat("esc/passwd")
	log("escape stat %s", kind(err))
	_, _, err = openRegular(v, "esc/passwd")
	log("escape open %s", kind(err))
	_, err = createRegular(v, "esc/x", 0o644)
	log("escape create %s", kind(err))
	log("escape mkdir %s", kind(v.MkdirAll("esc/y", 0o755)))
	st, err = v.Stat("inside")
	log("inside link %s dir=%v", kind(err), err == nil && st.IsDir())
	st, err = v.Lstat("inside")
	log("lstat link %s symlink=%v", kind(err), err == nil && st.Mode()&fs.ModeSymlink != 0)
	done := make(chan string, 1)
	go func() {
		_, _, err := openRegular(v, "fifo")
		e2 := func() error { _, err := createRegular(v, "fifo", 0o644); return err }()
		done <- kind(err) + " " + kind(e2)
	}()
	select {
	case r := <-done:
		log("fifo %s", r)
	case <-time.After(5 * time.Second):
		t.Fatal("a FIFO blocked the panel")
	}

	walked := []string{}
	err = fs.WalkDir(v.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		walked = append(walked, fmt.Sprintf("%s:%v", p, d.Type()))
		return nil
	})
	log("walk %s %v", kind(err), walked)

	log("lchown %s", kind(v.Lchown("a/g.txt", os.Getuid(), os.Getgid())))
	mt := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	log("chtimes %s", kind(v.Chtimes("a/g.txt", mt, mt)))
	st, _ = v.Stat("a/g.txt")
	log("mtime %v", st.ModTime().UTC())
	f, err = createRegular(v, "run.sh", 0o755)
	if err == nil {
		f.Close()
	}
	st, _ = v.Stat("run.sh")
	log("archive mode %v", st.Mode().Perm())

	log("remove nonempty %s", kind(v.Remove("a")))
	log("remove file %s", kind(v.Remove("a/g.txt")))
	log("removeall %s", kind(v.RemoveAll("a")))
	log("removeall missing %s", kind(v.RemoveAll("a")))
	_, err = v.Stat("a")
	log("gone %s", kind(err))
	return out
}

func TestRemoteVolumeBehavesLikeLocal(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"srv-local-data", "srv-remote-data"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pool := testPool(t, startAgent(t, root), testNodeToken)
	ctx := context.Background()

	lv, err := openLocalVol(filepath.Join(root, "srv-local-data"))
	if err != nil {
		t.Fatal(err)
	}
	defer lv.Close()
	rv, err := pool.openVol(ctx, "node-b", "srv-remote-data")
	if err != nil {
		t.Fatal(err)
	}
	defer rv.Close()

	want := volScript(t, lv, filepath.Join(root, "srv-local-data"))
	got := volScript(t, rv, filepath.Join(root, "srv-remote-data"))
	for i := range max(len(want), len(got)) {
		var w, g string
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if w != g {
			t.Errorf("step %d\n local: %s\nremote: %s", i, w, g)
		}
	}
	for _, line := range want {
		if testing.Verbose() {
			t.Log(line)
		}
		if strings.Contains(line, "other:") {
			t.Errorf("unexpected error: %s", line)
		}
	}

	// disk usage, computed by the agent
	if err := os.WriteFile(filepath.Join(root, "srv-remote-data", "big"), make([]byte, 12345), 0o644); err != nil {
		t.Fatal(err)
	}
	size, err := pool.du(ctx, "node-b", "srv-remote-data")
	if err != nil || size != dirSize(filepath.Join(root, "srv-remote-data")) || size < 12345 {
		t.Fatalf("du: %d %v", size, err)
	}
	if err := pool.ping(ctx, "node-b"); err != nil {
		t.Fatal(err)
	}
	// several volumes at once on one connection
	rv2, err := pool.openVol(ctx, "node-b", "srv-local-data")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rv2.Stat("run.sh"); err != nil {
		t.Fatal(err)
	}
	rv2.Close()
	if _, err := rv.Stat("big"); err != nil {
		t.Fatalf("the first volume still works: %v", err)
	}
}

func TestNodeAgentRefusesStrangers(t *testing.T) {
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, "srv-abc-data"), 0o755)
	_ = os.Mkdir(filepath.Join(root, "other-app"), 0o755)
	addr := startAgent(t, root)
	ctx := context.Background()

	if _, err := testPool(t, addr, "another-token-another-token-another").openVol(ctx, "n", "srv-abc-data"); err == nil {
		t.Fatal("a client without the token's key must be refused")
	}
	pool := testPool(t, addr, testNodeToken)
	for _, vol := range []string{"other-app", "..", "../srv-abc-data", "srv-abc-data/..", "", "/etc", "pvc-00000000-0000-0000-0000-000000000000_default_srv-abc-data"} {
		if v, err := pool.openVol(ctx, "n", vol); err == nil {
			if _, err := v.Stat("."); err == nil {
				t.Errorf("volume %q must be refused", vol)
			}
			v.Close()
		}
		if _, err := pool.du(ctx, "n", vol); err == nil {
			t.Errorf("du %q must be refused", vol)
		}
	}
	if _, err := pool.run(ctx, "n", agentOp{Op: "rmall", Vol: "srv-abc-data", Path: "/"}); err == nil {
		t.Fatal("the volume itself can't be removed")
	}
	if _, err := os.Stat(filepath.Join(root, "srv-abc-data")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.run(ctx, "n", agentOp{Op: "rmall", Vol: "srv-abc-data", Path: "../other-app"}); err == nil {
		if _, err := os.Stat(filepath.Join(root, "other-app")); err != nil {
			t.Fatal("rmall escaped the volume")
		}
	}

	// the host key is checked: an agent with another token is not trusted
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go (&nodeAgent{root: root, ns: "kp-servers", token: staticToken("another-token-another-token-another")}).serve(ln)
	if _, err := testPool(t, ln.Addr().String(), testNodeToken).openVol(ctx, "n", "srv-abc-data"); err == nil {
		t.Fatal("an agent with an unknown host key must be refused")
	}
}

func TestValidVolDir(t *testing.T) {
	for dir, ok := range map[string]bool{
		"srv-abc123-data": true,
		"pvc-0a1b2c3d-0000-4000-8000-123456789abc_kp-servers_srv-abc-data": true,
		"pvc-0a1b2c3d-0000-4000-8000-123456789abc_default_srv-abc-data":    false,
		"srv-abc-data/../x": false, "srv--data": false, "srv-ABC-data": false, "x": false, "srv-abc-data-2": false,
	} {
		if validVolDir(dir, "kp-servers") != ok {
			t.Errorf("%q: want %v", dir, ok)
		}
	}
}

func TestRemoteErrorShape(t *testing.T) {
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, "srv-abc-data"), 0o755)
	v, err := testPool(t, startAgent(t, root), testNodeToken).openVol(context.Background(), "n", "srv-abc-data")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	_, err = v.Stat("missing/file")
	var pe *fs.PathError
	if !errors.As(err, &pe) || !errors.Is(err, fs.ErrNotExist) || pe.Path != "missing/file" {
		t.Fatalf("an os.Root-like error is expected: %#v", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("the node's paths must not leak: %v", err)
	}
}

func TestNodeTokenRotation(t *testing.T) {
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, "srv-abc-data"), 0o755)
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte(testNodeToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBEPILOT_NODE_TOKEN_FILE", file)
	src := nodeTokenSource()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go (&nodeAgent{root: root, ns: "kp-servers", token: src}).serve(ln)
	p, err := newNodePool(nil, "", "", src)
	if err != nil {
		t.Fatal(err)
	}
	p.resolve = func(context.Context, string) (string, types.UID, error) { return ln.Addr().String(), "x", nil }
	if err := p.ping(context.Background(), "n"); err != nil {
		t.Fatal(err)
	}
	// a new Secret: both ends switch to the new keys, without a restart
	if err := os.WriteFile(file, []byte("a-brand-new-token-a-brand-new-token-0001"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	for n, c := range p.conns {
		c.c.Close()
		delete(p.conns, n)
	}
	p.mu.Unlock()
	if err := p.ping(context.Background(), "n"); err != nil {
		t.Fatalf("after rotation: %v", err)
	}
	if err := os.WriteFile(file, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := src(); err == nil {
		t.Fatal("a short token is refused")
	}
}
