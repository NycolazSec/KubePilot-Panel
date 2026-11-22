package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// The shield agent (`kubepilot shield`): one per node (DaemonSet, host network, NET_ADMIN only). It applies the
// configuration the panel publishes (see shield.go) as an nftables table of its own, and reports counters and bans.
// On exit it removes its table: turning the option off, or uninstalling, leaves no rule behind.

type shieldAgent struct {
	cs    kubernetes.Interface
	ns    string
	node  string
	table string
	since int64
	// nft runs the nft command (replaced in tests)
	nft func(ctx context.Context, stdin string, args ...string) (string, error)
	// probe checks an address's ports from here with tcpcat (exposure.go; replaced in tests)
	probe func(ctx context.Context, addr string, ports []int) ([]int, error)

	rv         string // ConfigMap version applied
	rev        string
	applied    bool
	lastAction int64
	err        string

	mu      sync.Mutex   // the exposure audit runs on its own goroutine (exposure.go)
	audit   *auditReport // the last one, reported in the status
	auditID int64        // the request it answered
}

func runShieldAgent() {
	node := os.Getenv("NODE_NAME")
	table := env("KUBEPILOT_SHIELD_TABLE", "kubepilot")
	if node == "" {
		log.Fatal("shield: NODE_NAME is required (downward API: spec.nodeName)")
	}
	if !shieldTableRe.MatchString(table) {
		log.Fatalf("shield: invalid table name %q (a-z, 0-9 and _)", table)
	}
	var rc *rest.Config
	var err error
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		rc, err = clientcmd.BuildConfigFromFlags("", kc)
	} else {
		rc, err = rest.InClusterConfig()
	}
	if err != nil {
		log.Fatalf("shield: %v", err)
	}
	cs, err := kubernetes.NewForConfig(rc)
	if err != nil {
		log.Fatalf("shield: %v", err)
	}
	ag := &shieldAgent{cs: cs, ns: env("KUBEPILOT_NAMESPACE", "kubepilot-servers"), node: node, table: table, since: now(), nft: runNft, probe: runTcpcat}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("shield %s: node %s, table inet %s", version, node, table)
	ag.loop(ctx)
	ag.removeTable(context.Background())
	log.Printf("shield: stopped, table inet %s removed", table)
}

func runNft(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "nft", args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	s := strings.TrimSpace(out.String())
	if err != nil {
		if len(s) > 600 {
			s = s[:600] + "…"
		}
		return s, fmt.Errorf("nft %s: %v: %s", strings.Join(args, " "), err, s)
	}
	return s, nil
}

func (ag *shieldAgent) loop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var reported time.Time
	for {
		ag.sync(ctx)
		if time.Since(reported) >= 10*time.Second {
			ag.report(ctx)
			reported = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// sync applies the panel's configuration when it changed.
func (ag *shieldAgent) sync(ctx context.Context) {
	cm, err := ag.cs.CoreV1().ConfigMaps(ag.ns).Get(ctx, shieldConfigMap, metav1.GetOptions{})
	if apierrors.IsNotFound(err) { // the panel publishes nothing: no filtering
		if ag.applied {
			ag.removeTable(ctx)
		}
		ag.err = ""
		return
	}
	if err != nil {
		ag.err = err.Error()
		return
	}
	if ag.applied && cm.ResourceVersion == ag.rv {
		ag.err = "" // the API answers again: an earlier failure to read it is over
		return
	}
	var c ShieldConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &c); err != nil {
		ag.err = "configuration: " + err.Error()
		return
	}
	if err := ag.apply(ctx, &c); err != nil {
		ag.err = err.Error()
		log.Printf("shield: %v", err)
		return
	}
	ag.rv, ag.rev, ag.err = cm.ResourceVersion, c.Rev, ""
	// an exposure audit requested since the last one: run it once, on the whole configuration (apply kept only
	// what this node applies)
	if c.Audit > 0 && c.Audit != ag.auditID {
		ag.auditID = c.Audit
		var full ShieldConfig
		if json.Unmarshal([]byte(cm.Data["config.json"]), &full) == nil {
			full.sanitize()
			go ag.runAudit(ctx, &full)
		}
	}
}

func (ag *shieldAgent) apply(ctx context.Context, c *ShieldConfig) error {
	c.sanitize()
	c.forNode(ag.node)
	stale := []string{}
	keep := map[string]bool{}
	for _, s := range c.Servers {
		keep["srv_"+s.ID] = true
	}
	for name := range ag.counters(ctx) {
		if strings.HasPrefix(name, "srv_") && !keep[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	script := nftScript(ag.table, c, stale)
	if _, err := ag.nft(ctx, script, "-f", "-"); err != nil {
		// a table left by another version whose sets differ: start again from a clean one (bans are lost)
		log.Printf("shield: %v; recreating the table", err)
		ag.removeTable(ctx)
		if _, err := ag.nft(ctx, nftScript(ag.table, c, nil), "-f", "-"); err != nil {
			return err
		}
	}
	if !ag.applied {
		log.Printf("shield: protecting %d TCP and %d UDP ports (mode %s)", len(c.TCPPorts), len(c.UDPPorts), c.Mode)
	}
	ag.applied = true
	ag.applyActions(ctx, c.Actions)
	return nil
}

// applyActions runs the manual bans and unbans this agent hasn't run yet.
func (ag *shieldAgent) applyActions(ctx context.Context, actions []ShieldAction) {
	sort.Slice(actions, func(i, j int) bool { return actions[i].ID < actions[j].ID })
	T := "inet " + ag.table
	for _, x := range actions {
		if x.ID <= ag.lastAction {
			continue
		}
		ag.lastAction = x.ID
		// deleting first refreshes the timeout of an IP already banned; an IP that isn't there is an error, ignored
		_, _ = ag.nft(ctx, fmt.Sprintf("delete element %s banned { %s }\n", T, x.IP), "-f", "-")
		if x.Op != "ban" {
			continue
		}
		if left := x.Seconds - int(now()-x.At); left > 0 {
			if _, err := ag.nft(ctx, fmt.Sprintf("add element %s banned { %s timeout %ds }\n", T, x.IP, left), "-f", "-"); err != nil {
				log.Printf("shield: ban %s: %v", x.IP, err)
			}
		}
	}
}

func (ag *shieldAgent) removeTable(ctx context.Context) {
	if _, err := ag.nft(ctx, "", "delete", "table", "inet", ag.table); err != nil && !strings.Contains(err.Error(), "No such file") {
		log.Printf("shield: %v", err)
	}
	ag.applied, ag.rv, ag.rev, ag.lastAction = false, "", "", 0
}

type nftList struct {
	Nftables []map[string]json.RawMessage `json:"nftables"`
}

// counters returns the packets each named counter of the table dropped (empty without a table).
func (ag *shieldAgent) counters(ctx context.Context) map[string]uint64 {
	out := map[string]uint64{}
	raw, err := ag.nft(ctx, "", "-j", "list", "counters", "table", "inet", ag.table)
	if err != nil {
		return out
	}
	var l nftList
	if json.Unmarshal([]byte(raw), &l) != nil {
		return out
	}
	for _, o := range l.Nftables {
		if c, ok := o["counter"]; ok {
			var v struct {
				Name    string `json:"name"`
				Packets uint64 `json:"packets"`
			}
			if json.Unmarshal(c, &v) == nil {
				out[v.Name] = v.Packets
			}
		}
	}
	return out
}

// bans lists the banned IPs with the seconds left.
func (ag *shieldAgent) bans(ctx context.Context) []shieldBan {
	out := []shieldBan{}
	raw, err := ag.nft(ctx, "", "-j", "list", "set", "inet", ag.table, "banned")
	if err != nil {
		return out
	}
	return parseNftBans(raw)
}

func parseNftBans(raw string) []shieldBan {
	out := []shieldBan{}
	var l nftList
	if json.Unmarshal([]byte(raw), &l) != nil {
		return out
	}
	for _, o := range l.Nftables {
		s, ok := o["set"]
		if !ok {
			continue
		}
		var set struct {
			Elem []json.RawMessage `json:"elem"`
		}
		if json.Unmarshal(s, &set) != nil {
			continue
		}
		for _, e := range set.Elem {
			var ip string
			if json.Unmarshal(e, &ip) == nil {
				out = append(out, shieldBan{IP: ip})
				continue
			}
			var v struct {
				Elem struct {
					Val     string `json:"val"`
					Expires int    `json:"expires"`
				} `json:"elem"`
			}
			if json.Unmarshal(e, &v) == nil && v.Elem.Val != "" {
				out = append(out, shieldBan{IP: v.Elem.Val, Expires: v.Elem.Expires})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Expires > out[j].Expires })
	return out
}

// report writes this node's status in the status ConfigMap (one key per node).
func (ag *shieldAgent) report(ctx context.Context) {
	st := shieldNodeStatus{Node: ag.node, Updated: now(), Since: ag.since, Version: version, Rev: ag.rev, Error: ag.err,
		Counters: map[string]uint64{}, Servers: map[string]uint64{}, Bans: []shieldBan{},
		Public: publicAddrs(), Audit: ag.lastAudit()}
	var tables []io.Reader
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if fh, err := os.Open(f); err == nil {
			defer fh.Close()
			tables = append(tables, fh)
		}
	}
	st.Listening = listeningPorts(tables...)
	if ag.applied {
		for name, v := range ag.counters(ctx) {
			if id, ok := strings.CutPrefix(name, "srv_"); ok {
				st.Servers[id] = v
			} else {
				st.Counters[name] = v
			}
		}
		st.Bans = ag.bans(ctx)
		st.BanCount = len(st.Bans)
		if len(st.Bans) > 500 {
			st.Bans = st.Bans[:500]
		}
	}
	b, _ := json.Marshal(st)
	patch, _ := json.Marshal(map[string]any{"data": map[string]string{ag.node: string(b)}})
	_, err := ag.cs.CoreV1().ConfigMaps(ag.ns).Patch(ctx, shieldStatusMap, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil && !errors.Is(err, context.Canceled) && !apierrors.IsNotFound(err) {
		log.Printf("shield: status: %v", err)
	}
}
