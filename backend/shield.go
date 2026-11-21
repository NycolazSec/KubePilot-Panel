package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Shield: firewall and anti-DDoS, optional (chart value shield.enabled).
//
// The panel keeps the rules and settings in its database and publishes what they mean (ports to protect,
// limits, lists) in the ConfigMap kubepilot-shield of the servers' namespace. A DaemonSet runs `kubepilot shield`
// on every node (shieldagent.go): it turns that configuration into its own nftables table and reports its counters
// and bans in the ConfigMap kubepilot-shield-status.
//
// Only the game ports are filtered — the servers' NodePorts and the Minecraft router — never SSH, the panel or the
// Kubernetes API, so no rule can lock the administrator out. Private networks (the cluster itself) are never
// filtered. This protects against what reaches the node; an attack bigger than the node's link has to be stopped
// upstream (the host's anti-DDoS, or a provider such as path.net).

const (
	shieldConfigMap      = "kubepilot-shield"
	shieldStatusMap      = "kubepilot-shield-status"
	shieldMaxServerRules = 100
	shieldMaxGlobalRules = 1000
	shieldMaxBanSeconds  = 7 * 24 * 3600
	shieldStaleAfter     = 60 // seconds without news from an agent
)

const shieldSchema = `
CREATE TABLE IF NOT EXISTS firewall_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id TEXT REFERENCES servers(id) ON DELETE CASCADE, -- NULL: every server of the panel
  cidr TEXT NOT NULL,
  action TEXT NOT NULL, -- allow, deny
  note TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS firewall_rules_server ON firewall_rules(server_id);
`

// ---------- Settings

// ShieldSettings: the anti-DDoS limits, per source IP and per node.
type ShieldSettings struct {
	Mode       string `json:"mode"`       // off (firewall rules only), normal, strict (under attack: tighter limits)
	TCPRate    int    `json:"tcpRate"`    // new TCP connections per second
	Conns      int    `json:"conns"`      // simultaneous TCP connections
	UDPRate    int    `json:"udpRate"`    // UDP packets per second
	BanMinutes int    `json:"banMinutes"` // an IP over a limit is banned this long
}

func defaultShield() ShieldSettings {
	// A Minecraft client opens 1 or 2 connections (ping + game); a game client sends well under 200 UDP packets/s.
	return ShieldSettings{Mode: "normal", TCPRate: 15, Conns: 30, UDPRate: 1500, BanMinutes: 10}
}

func (s ShieldSettings) validate() error {
	switch {
	case s.Mode != "off" && s.Mode != "normal" && s.Mode != "strict":
		return errCode(400, "Mode: off, normal or strict")
	case s.TCPRate < 1 || s.TCPRate > 1000:
		return errCode(400, "New TCP connections per second: between 1 and 1000")
	case s.Conns < 1 || s.Conns > 10000:
		return errCode(400, "Simultaneous connections per IP: between 1 and 10000")
	case s.UDPRate < 10 || s.UDPRate > 100000:
		return errCode(400, "UDP packets per second: between 10 and 100000")
	case s.BanMinutes < 1 || s.BanMinutes > 10080:
		return errCode(400, "Ban duration: between 1 and 10080 minutes")
	}
	return nil
}

// effective: the limits really applied; under attack (strict), three times tighter and bans six times longer.
func (s ShieldSettings) effective() (tcp, conns, udp, banSeconds int) {
	tcp, conns, udp, banSeconds = s.TCPRate, s.Conns, s.UDPRate, s.BanMinutes*60
	if s.Mode == "strict" {
		tcp, conns, udp, banSeconds = max(1, tcp/3), max(2, conns/3), max(10, udp/2), banSeconds*6
	}
	return tcp, conns, udp, min(banSeconds, shieldMaxBanSeconds)
}

func (db *DB) ShieldSettings() ShieldSettings {
	s := defaultShield()
	var raw string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='shield'`).Scan(&raw); err == nil {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	return s
}

func (db *DB) SaveShieldSettings(s ShieldSettings) error {
	b, _ := json.Marshal(s)
	_, err := db.Exec(`INSERT INTO settings(key,value) VALUES('shield',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(b))
	return err
}

// ---------- Rules

type FirewallRule struct {
	ID        int64  `json:"id"`
	ServerID  string `json:"serverId,omitempty"` // empty: every server
	CIDR      string `json:"cidr"`
	Action    string `json:"action"` // allow, deny
	Note      string `json:"note"`
	CreatedAt int64  `json:"createdAt"`
}

// parseCIDR4 accepts an IPv4 address or range and returns it in canonical form ("203.0.113.7/32").
// Ranges wider than /8 are refused: one rule must not be able to block (or exempt) a large part of the Internet.
func parseCIDR4(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "/") {
		s += "/32"
	}
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.Addr().Is4() {
		return "", errCode(400, "Firewall rules take IPv4 addresses or ranges, like 203.0.113.7 or 198.51.100.0/24")
	}
	if p.Bits() < 8 {
		return "", errCode(400, "This range is too wide: use /8 or narrower")
	}
	return p.Masked().String(), nil
}

func (db *DB) FirewallRules(serverID string) ([]*FirewallRule, error) {
	q := `SELECT id, COALESCE(server_id,''), cidr, action, note, created_at FROM firewall_rules`
	var args []any
	switch serverID {
	case "*":
	case "":
		q += ` WHERE server_id IS NULL`
	default:
		q += ` WHERE server_id=?`
		args = append(args, serverID)
	}
	rows, err := db.Query(q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*FirewallRule{}
	for rows.Next() {
		r := &FirewallRule{}
		if err := rows.Scan(&r.ID, &r.ServerID, &r.CIDR, &r.Action, &r.Note, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (db *DB) AddFirewallRule(r *FirewallRule) error {
	cidr, err := parseCIDR4(r.CIDR)
	if err != nil {
		return err
	}
	if r.Action != "allow" && r.Action != "deny" {
		return errCode(400, "Action: allow or deny")
	}
	r.CIDR, r.Note, r.CreatedAt = cidr, truncate(strings.TrimSpace(r.Note), 100), now()
	existing, err := db.FirewallRules(r.ServerID)
	if err != nil {
		return err
	}
	limit := shieldMaxGlobalRules
	if r.ServerID != "" {
		limit = shieldMaxServerRules
	}
	if len(existing) >= limit {
		return errCode(400, "Too many firewall rules (%d at most)", limit)
	}
	for _, e := range existing {
		if e.CIDR == r.CIDR && e.Action == r.Action {
			return errCode(409, "This rule already exists")
		}
	}
	var sid any
	if r.ServerID != "" {
		sid = r.ServerID
	}
	res, err := db.Exec(`INSERT INTO firewall_rules(server_id,cidr,action,note,created_at) VALUES(?,?,?,?,?)`, sid, r.CIDR, r.Action, r.Note, r.CreatedAt)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

// DeleteFirewallRule deletes a rule of this server ("" = a global rule); ErrNotFound if it has no such rule.
func (db *DB) DeleteFirewallRule(id int64, serverID string) (*FirewallRule, error) {
	rules, err := db.FirewallRules(serverID)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if r.ID == id {
			_, err := db.Exec(`DELETE FROM firewall_rules WHERE id=?`, id)
			return r, err
		}
	}
	return nil, ErrNotFound
}

// ---------- Configuration given to the agents

// ShieldConfig is what every node applies (ConfigMap kubepilot-shield, key config.json).
type ShieldConfig struct {
	Rev        string         `json:"rev"` // hash of everything below: the agents report the one they applied
	Mode       string         `json:"mode"`
	TCPRate    int            `json:"tcpRate"`
	TCPBurst   int            `json:"tcpBurst"`
	Conns      int            `json:"conns"`
	UDPRate    int            `json:"udpRate"`
	UDPBurst   int            `json:"udpBurst"`
	BanSeconds int            `json:"banSeconds"`
	TCPPorts   []int          `json:"tcpPorts"`
	UDPPorts   []int          `json:"udpPorts"`
	Allow      []string       `json:"allow"` // never filtered
	Deny       []string       `json:"deny"`  // always blocked
	Servers    []ShieldServer `json:"servers"`
	Actions    []ShieldAction `json:"actions"` // manual bans and unbans, applied once by each agent
}

// ShieldServer: the firewall rules of one server, on its own ports. With an allow list, only those IPs get in.
type ShieldServer struct {
	ID    string   `json:"id"`
	TCP   []int    `json:"tcp"`
	UDP   []int    `json:"udp"`
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

type ShieldAction struct {
	ID      int64  `json:"id"`
	Op      string `json:"op"` // ban, unban
	IP      string `json:"ip"`
	Seconds int    `json:"seconds"`
	At      int64  `json:"at"`
}

// buildShieldConfig turns the panel's data into the agents' configuration. routerPort: 0 without the router.
func buildShieldConfig(st ShieldSettings, allocs []*Allocation, rules []*FirewallRule, routerPort int, actions []ShieldAction) *ShieldConfig {
	tcp, conns, udp, ban := st.effective()
	c := &ShieldConfig{Mode: st.Mode, TCPRate: tcp, TCPBurst: tcp * 2, Conns: conns, UDPRate: udp, UDPBurst: udp * 2, BanSeconds: ban,
		Allow: []string{}, Deny: []string{}, Servers: []ShieldServer{}, Actions: actions}
	if c.Actions == nil {
		c.Actions = []ShieldAction{}
	}
	tcpPorts, udpPorts := map[int]bool{}, map[int]bool{}
	if routerPort > 0 {
		tcpPorts[routerPort] = true
	}
	servers := map[string]*ShieldServer{}
	srv := func(id string) *ShieldServer {
		if servers[id] == nil {
			servers[id] = &ShieldServer{ID: id, TCP: []int{}, UDP: []int{}, Allow: []string{}, Deny: []string{}}
		}
		return servers[id]
	}
	for _, a := range allocs {
		for _, p := range protocols(a.Protocol) {
			if p == corev1.ProtocolUDP {
				udpPorts[a.Port] = true
				srv(a.ServerID).UDP = append(srv(a.ServerID).UDP, a.Port)
			} else {
				tcpPorts[a.Port] = true
				srv(a.ServerID).TCP = append(srv(a.ServerID).TCP, a.Port)
			}
		}
	}
	for _, r := range rules {
		if r.ServerID == "" {
			if r.Action == "allow" {
				c.Allow = append(c.Allow, r.CIDR)
			} else {
				c.Deny = append(c.Deny, r.CIDR)
			}
		} else if s := servers[r.ServerID]; s != nil { // a server without ports has nothing to filter
			if r.Action == "allow" {
				s.Allow = append(s.Allow, r.CIDR)
			} else {
				s.Deny = append(s.Deny, r.CIDR)
			}
		}
	}
	c.TCPPorts, c.UDPPorts = sortedKeys(tcpPorts), sortedKeys(udpPorts)
	for _, s := range servers {
		if len(s.Allow)+len(s.Deny) > 0 {
			sort.Ints(s.TCP)
			sort.Ints(s.UDP)
			c.Servers = append(c.Servers, *s)
		}
	}
	sort.Slice(c.Servers, func(i, j int) bool { return c.Servers[i].ID < c.Servers[j].ID })
	c.sanitize()
	b, _ := json.Marshal(c)
	c.Rev = sha(string(b))[:16]
	return c
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

var (
	shieldTableRe    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)
	shieldServerIDRe = regexp.MustCompile(`^[a-z0-9]{1,32}$`)
)

// sanitize keeps only what can safely go into an nftables script: the agent doesn't trust the ConfigMap blindly.
func (c *ShieldConfig) sanitize() {
	if c.Mode != "normal" && c.Mode != "strict" {
		c.Mode = "off"
	}
	c.TCPRate, c.TCPBurst = clampInt(c.TCPRate, 1, 1000), clampInt(c.TCPBurst, 1, 2000)
	c.Conns = clampInt(c.Conns, 1, 10000)
	c.UDPRate, c.UDPBurst = clampInt(c.UDPRate, 10, 100000), clampInt(c.UDPBurst, 10, 200000)
	c.BanSeconds = clampInt(c.BanSeconds, 60, shieldMaxBanSeconds)
	c.TCPPorts, c.UDPPorts = cleanPorts(c.TCPPorts), cleanPorts(c.UDPPorts)
	c.Allow, c.Deny = cleanCIDRs(c.Allow), cleanCIDRs(c.Deny)
	servers := c.Servers[:0]
	for _, s := range c.Servers {
		if !shieldServerIDRe.MatchString(s.ID) {
			continue
		}
		s.TCP, s.UDP, s.Allow, s.Deny = cleanPorts(s.TCP), cleanPorts(s.UDP), cleanCIDRs(s.Allow), cleanCIDRs(s.Deny)
		if len(s.TCP)+len(s.UDP) > 0 && len(s.Allow)+len(s.Deny) > 0 {
			servers = append(servers, s)
		}
	}
	c.Servers = servers
	actions := c.Actions[:0]
	for _, x := range c.Actions {
		ip, err := netip.ParseAddr(x.IP)
		if err != nil || !ip.Is4() || (x.Op != "ban" && x.Op != "unban") {
			continue
		}
		x.IP, x.Seconds = ip.String(), clampInt(x.Seconds, 1, shieldMaxBanSeconds)
		actions = append(actions, x)
	}
	c.Actions = actions
}

func clampInt(v, lo, hi int) int { return max(lo, min(hi, v)) }

func cleanPorts(in []int) []int {
	out := []int{}
	for _, p := range in {
		if p >= 1 && p <= 65535 && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

// cleanCIDRs parses, deduplicates and drops the ranges another one of the list already covers (nftables refuses
// overlapping intervals in a set).
func cleanCIDRs(in []string) []string {
	var ps []netip.Prefix
	for _, s := range in {
		if !strings.Contains(s, "/") {
			s += "/32"
		}
		if p, err := netip.ParsePrefix(s); err == nil && p.Addr().Is4() && p.Bits() >= 8 {
			ps = append(ps, p.Masked())
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Bits() != ps[j].Bits() {
			return ps[i].Bits() < ps[j].Bits()
		}
		return ps[i].Addr().Less(ps[j].Addr())
	})
	var kept []netip.Prefix
	for _, p := range ps {
		covered := false
		for _, k := range kept {
			if k.Contains(p.Addr()) {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, p)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Addr().Less(kept[j].Addr()) })
	out := make([]string, len(kept))
	for i, p := range kept {
		out[i] = p.String()
	}
	return out
}

// trustedRanges are never filtered: the cluster itself (pods, nodes, probes) and the loopback.
var trustedRanges = []string{"10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16"}

func isTrusted(ip netip.Addr, allow []string) bool {
	for _, s := range append(slices.Clone(trustedRanges), allow...) {
		if p, err := netip.ParsePrefix(s); err == nil && p.Contains(ip) {
			return true
		}
	}
	return false
}

// shieldCounters: packets dropped, by reason (plus srv_<id> for each server's own rules).
var shieldCounters = []string{"blocked", "banned", "rate_tcp", "conns", "rate_udp", "invalid"}

// nftScript is the nftables transaction that brings the table to the configuration. It is applied atomically
// (nft -f): sets of bans and rate meters are kept between two configurations, every rule is rewritten.
// stale: per-server counters to delete (servers whose rules are gone).
func nftScript(table string, c *ShieldConfig, stale []string) string {
	var b strings.Builder
	T := "inet " + table
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("add table %s", T)
	w("add set %s trusted { type ipv4_addr; flags interval; auto-merge; }", T)
	w("add set %s blocked { type ipv4_addr; flags interval; auto-merge; }", T)
	w("add set %s banned { type ipv4_addr; flags dynamic,timeout; size 65536; }", T)
	w("add set %s tcp_ports { type inet_service; }", T)
	w("add set %s udp_ports { type inet_service; }", T)
	w("add set %s rl_tcp { type ipv4_addr; flags dynamic,timeout; timeout 1m; size 65536; }", T)
	w("add set %s rl_udp { type ipv4_addr; flags dynamic,timeout; timeout 1m; size 65536; }", T)
	w("add set %s conns { type ipv4_addr; flags dynamic; size 65536; }", T)
	for _, n := range shieldCounters {
		w("add counter %s %s", T, n)
	}
	for _, s := range c.Servers {
		w("add counter %s srv_%s", T, s.ID)
	}
	// before the cluster's NAT (dstnat, -100): the destination port is still the one the player connected to
	w("add chain %s pre { type filter hook prerouting priority -150; policy accept; }", T)
	w("add chain %s game_tcp", T)
	w("add chain %s game_udp", T)
	for _, ch := range []string{"pre", "game_tcp", "game_udp"} {
		w("flush chain %s %s", T, ch)
	}
	for _, n := range stale {
		w("delete counter %s %s", T, n)
	}
	fill := func(set string, elems []string) {
		w("flush set %s %s", T, set)
		if len(elems) > 0 {
			w("add element %s %s { %s }", T, set, strings.Join(elems, ", "))
		}
	}
	fill("trusted", cleanCIDRs(append(slices.Clone(trustedRanges), c.Allow...)))
	fill("blocked", c.Deny)
	fill("tcp_ports", intStrings(c.TCPPorts))
	fill("udp_ports", intStrings(c.UDPPorts))

	w("add rule %s pre ct direction reply accept", T) // answers to connections made from inside
	w("add rule %s pre ip saddr @trusted accept", T)
	w("add rule %s pre meta nfproto ipv4 tcp dport @tcp_ports jump game_tcp", T)
	w("add rule %s pre meta nfproto ipv4 udp dport @udp_ports jump game_udp", T)
	for _, proto := range []string{"tcp", "udp"} {
		ch := "game_" + proto
		w(`add rule %s %s ip saddr @blocked counter name "blocked" drop`, T, ch)
		w(`add rule %s %s ip saddr @banned counter name "banned" drop`, T, ch)
		for _, s := range c.Servers {
			ports := s.TCP
			if proto == "udp" {
				ports = s.UDP
			}
			if len(ports) == 0 {
				continue
			}
			p := strings.Join(intStrings(ports), ", ")
			if len(s.Deny) > 0 {
				w(`add rule %s %s %s dport { %s } ip saddr { %s } counter name "srv_%s" drop`, T, ch, proto, p, strings.Join(s.Deny, ", "), s.ID)
			}
			if len(s.Allow) > 0 { // allow list: everyone else is refused
				w(`add rule %s %s %s dport { %s } ip saddr != { %s } counter name "srv_%s" drop`, T, ch, proto, p, strings.Join(s.Allow, ", "), s.ID)
			}
		}
		if c.Mode == "off" {
			continue
		}
		if proto == "tcp" {
			w(`add rule %s game_tcp ct state invalid counter name "invalid" drop`, T)
			w(`add rule %s game_tcp tcp flags & (fin|syn|rst|ack) != syn ct state new counter name "invalid" drop`, T)
			w(`add rule %s game_tcp ct state new update @rl_tcp { ip saddr limit rate over %d/second burst %d packets } add @banned { ip saddr timeout %ds } counter name "rate_tcp" drop`,
				T, c.TCPRate, c.TCPBurst, c.BanSeconds)
			w(`add rule %s game_tcp ct state new add @conns { ip saddr ct count over %d } counter name "conns" drop`, T, c.Conns)
		} else {
			w(`add rule %s game_udp update @rl_udp { ip saddr limit rate over %d/second burst %d packets } add @banned { ip saddr timeout %ds } counter name "rate_udp" drop`,
				T, c.UDPRate, c.UDPBurst, c.BanSeconds)
		}
	}
	return b.String()
}

func intStrings(in []int) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = strconv.Itoa(v)
	}
	return out
}

// ---------- Status reported by the agents

type shieldNodeStatus struct {
	Node     string            `json:"node"`
	Updated  int64             `json:"updated"`
	Since    int64             `json:"since"` // counters count from there
	Version  string            `json:"version"`
	Rev      string            `json:"rev"` // configuration applied
	Error    string            `json:"error"`
	Counters map[string]uint64 `json:"counters"`
	Servers  map[string]uint64 `json:"servers"`
	Bans     []shieldBan       `json:"bans"`
	BanCount int               `json:"banCount"`
}

type shieldBan struct {
	IP      string `json:"ip"`
	Expires int    `json:"expires"` // seconds left
}

// ---------- Panel side

type shieldState struct {
	mu      sync.Mutex
	actions []ShieldAction
	lastID  int64
	rev     string // last configuration published
	last    string
	kick    chan struct{}
}

func (a *App) shieldChanged() {
	select {
	case a.shield.kick <- struct{}{}:
	default:
	}
}

// shieldLoop publishes the configuration when something changed (rules, settings, ports of the servers).
func (a *App) shieldLoop(ctx context.Context) {
	if !a.cfg.Shield {
		return
	}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		if err := a.publishShield(ctx); err != nil {
			log.Printf("shield: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-a.shield.kick:
		}
	}
}

func (a *App) shieldActions() []ShieldAction {
	a.shield.mu.Lock()
	defer a.shield.mu.Unlock()
	t := now()
	keep := a.shield.actions[:0]
	for _, x := range a.shield.actions {
		// a ban stays listed while it lasts (for nodes that join meanwhile); an unban for ten minutes
		if (x.Op == "ban" && x.At+int64(x.Seconds) > t) || (x.Op == "unban" && x.At+600 > t) {
			keep = append(keep, x)
		}
	}
	if len(keep) > 200 {
		keep = keep[len(keep)-200:]
	}
	a.shield.actions = keep
	return slices.Clone(keep)
}

func (a *App) addShieldAction(op, ip string, seconds int) {
	a.shield.mu.Lock()
	id := time.Now().UnixNano()
	if id <= a.shield.lastID {
		id = a.shield.lastID + 1
	}
	a.shield.lastID = id
	a.shield.actions = append(a.shield.actions, ShieldAction{ID: id, Op: op, IP: ip, Seconds: seconds, At: now()})
	a.shield.mu.Unlock()
	a.shieldChanged()
}

func (a *App) publishShield(ctx context.Context) error {
	allocs, err := a.db.Allocations("")
	if err != nil {
		return err
	}
	rules, err := a.db.FirewallRules("*")
	if err != nil {
		return err
	}
	router := 0
	if a.cfg.MinecraftRouter {
		router = a.cfg.RouterPort
	}
	c := buildShieldConfig(a.db.ShieldSettings(), allocs, rules, router, a.shieldActions())
	b, _ := json.Marshal(c)
	a.shield.mu.Lock()
	same := string(b) == a.shield.last
	a.shield.mu.Unlock()
	if same {
		return nil
	}
	if err := a.kube.putConfigMap(ctx, shieldConfigMap, map[string]string{"config.json": string(b)}); err != nil {
		return err
	}
	if err := a.kube.ensureConfigMap(ctx, shieldStatusMap); err != nil {
		return err
	}
	a.shield.mu.Lock()
	a.shield.last, a.shield.rev = string(b), c.Rev
	a.shield.mu.Unlock()
	return nil
}

// shieldStatus reads what the agents reported, one entry per node.
func (a *App) shieldStatus(ctx context.Context) []shieldNodeStatus {
	out := []shieldNodeStatus{}
	if a.kube == nil {
		return out
	}
	data, err := a.kube.configMapData(ctx, shieldStatusMap)
	if err != nil {
		return out
	}
	for node, raw := range data {
		var st shieldNodeStatus
		if json.Unmarshal([]byte(raw), &st) == nil {
			st.Node = node
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}

func (k *Kube) configMapData(ctx context.Context, name string) (map[string]string, error) {
	cm, err := k.cs.CoreV1().ConfigMaps(k.ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return cm.Data, nil
}

func (k *Kube) putConfigMap(ctx context.Context, name string, data map[string]string) error {
	api := k.cs.CoreV1().ConfigMaps(k.ns)
	cur, err := api.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: k.ns, Labels: map[string]string{lblManaged: managedBy}}, Data: data}, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	}
	cur.Data = data
	_, err = api.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

// ensureConfigMap creates an empty ConfigMap if there is none (the agents may only update it).
func (k *Kube) ensureConfigMap(ctx context.Context, name string) error {
	api := k.cs.CoreV1().ConfigMaps(k.ns)
	if _, err := api.Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		return err
	}
	_, err := api.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: k.ns, Labels: map[string]string{lblManaged: managedBy}}}, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (a *App) requireShield() error {
	if !a.cfg.Shield {
		return errCode(400, "The firewall and anti-DDoS protection aren't enabled: set shield.enabled in the Helm chart")
	}
	return nil
}

// GET /api/admin/firewall: settings, global rules, and what every node reports.
func (a *App) handleAdminShield(w http.ResponseWriter, r *http.Request, u *User) {
	st := a.db.ShieldSettings()
	rules, err := a.db.FirewallRules("")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	tcp, conns, udp, ban := st.effective()
	out := map[string]any{"available": a.cfg.Shield, "settings": st, "defaults": defaultShield(), "rules": rules,
		"effective": map[string]int{"tcpRate": tcp, "conns": conns, "udpRate": udp, "banMinutes": ban / 60}}
	if a.cfg.Shield {
		a.shield.mu.Lock()
		rev := a.shield.rev
		a.shield.mu.Unlock()
		type nodeView struct {
			Node    string `json:"node"`
			Updated int64  `json:"updated"`
			Since   int64  `json:"since"`
			Version string `json:"version"`
			Error   string `json:"error"`
			Stale   bool   `json:"stale"`
			Synced  bool   `json:"synced"` // the node applies the latest configuration
		}
		nodes := []nodeView{}
		counters := map[string]uint64{}
		bans := map[string]int{}
		banCount := 0
		var since int64
		var serverDrops uint64
		for _, n := range a.shieldStatus(r.Context()) {
			nodes = append(nodes, nodeView{Node: n.Node, Updated: n.Updated, Since: n.Since, Version: n.Version, Error: n.Error,
				Stale: now()-n.Updated > shieldStaleAfter, Synced: rev != "" && n.Rev == rev})
			for k, v := range n.Counters {
				counters[k] += v
			}
			for _, v := range n.Servers {
				serverDrops += v
			}
			for _, b := range n.Bans {
				bans[b.IP] = max(bans[b.IP], b.Expires)
			}
			banCount += n.BanCount
			if since == 0 || (n.Since > 0 && n.Since < since) {
				since = n.Since
			}
		}
		counters["servers"] = serverDrops
		banList := make([]shieldBan, 0, len(bans))
		for ip, e := range bans {
			banList = append(banList, shieldBan{IP: ip, Expires: e})
		}
		sort.Slice(banList, func(i, j int) bool { return banList[i].Expires > banList[j].Expires })
		allocs, _ := a.db.Allocations("")
		out["nodes"], out["counters"], out["bans"], out["banCount"], out["since"], out["ports"] = nodes, counters, banList, banCount, since, len(allocs)
		out["router"] = a.cfg.MinecraftRouter
	}
	writeJSON(w, 200, out)
}

// PUT /api/admin/firewall {mode, tcpRate, conns, udpRate, banMinutes}
func (a *App) handleAdminShieldSave(w http.ResponseWriter, r *http.Request, u *User) {
	var in ShieldSettings
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, r, err)
		return
	}
	if err := a.db.SaveShieldSettings(in); err != nil {
		writeErr(w, r, err)
		return
	}
	a.shieldChanged()
	a.db.LogActivity("", u, clientIP(r), "admin.firewall.update", in.Mode)
	writeJSON(w, 200, in)
}

// POST /api/admin/firewall/rules {cidr, action, note}
func (a *App) handleAdminShieldRuleAdd(w http.ResponseWriter, r *http.Request, u *User) {
	var in FirewallRule
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	in.ServerID = ""
	if err := a.db.AddFirewallRule(&in); err != nil {
		writeErr(w, r, err)
		return
	}
	a.shieldChanged()
	a.db.LogActivity("", u, clientIP(r), "admin.firewall.rule.add", in.Action+" "+in.CIDR)
	writeJSON(w, 200, in)
}

// DELETE /api/admin/firewall/rules/{rid}
func (a *App) handleAdminShieldRuleDelete(w http.ResponseWriter, r *http.Request, u *User) {
	id, _ := strconv.ParseInt(r.PathValue("rid"), 10, 64)
	rule, err := a.db.DeleteFirewallRule(id, "")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	a.shieldChanged()
	a.db.LogActivity("", u, clientIP(r), "admin.firewall.rule.delete", rule.Action+" "+rule.CIDR)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// POST /api/admin/firewall/bans {ip, minutes}: bans an IP on every node, for a while.
func (a *App) handleAdminShieldBan(w http.ResponseWriter, r *http.Request, u *User) {
	if err := a.requireShield(); err != nil {
		writeErr(w, r, err)
		return
	}
	var in struct {
		IP      string `json:"ip"`
		Minutes int    `json:"minutes"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(in.IP))
	if err != nil || !ip.Is4() {
		writeErr(w, r, errCode(400, "Invalid IPv4 address"))
		return
	}
	if in.Minutes < 1 || in.Minutes > 10080 {
		writeErr(w, r, errCode(400, "Ban duration: between 1 and 10080 minutes"))
		return
	}
	global, _ := a.db.FirewallRules("")
	var allow []string
	for _, g := range global {
		if g.Action == "allow" {
			allow = append(allow, g.CIDR)
		}
	}
	if isTrusted(ip, allow) {
		writeErr(w, r, errCode(400, "This address is never filtered (private network or allow list): a ban would do nothing"))
		return
	}
	a.addShieldAction("ban", ip.String(), in.Minutes*60)
	a.db.LogActivity("", u, clientIP(r), "admin.firewall.ban", ip.String())
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// DELETE /api/admin/firewall/bans/{ip}
func (a *App) handleAdminShieldUnban(w http.ResponseWriter, r *http.Request, u *User) {
	if err := a.requireShield(); err != nil {
		writeErr(w, r, err)
		return
	}
	ip, err := netip.ParseAddr(r.PathValue("ip"))
	if err != nil || !ip.Is4() {
		writeErr(w, r, errCode(400, "Invalid IPv4 address"))
		return
	}
	a.addShieldAction("unban", ip.String(), 1)
	a.db.LogActivity("", u, clientIP(r), "admin.firewall.unban", ip.String())
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// GET /api/servers/{id}/firewall: the server's own rules, and how many packets they dropped.
func (a *App) handleServerFirewall(w http.ResponseWriter, r *http.Request, c *Ctx) {
	rules, err := a.db.FirewallRules(c.Server.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var blocked uint64
	if a.cfg.Shield {
		for _, n := range a.shieldStatus(r.Context()) {
			blocked += n.Servers[c.Server.ID]
		}
	}
	writeJSON(w, 200, map[string]any{"available": a.cfg.Shield, "rules": rules, "blocked": blocked, "limit": shieldMaxServerRules,
		"mode": a.db.ShieldSettings().Mode})
}

// POST /api/servers/{id}/firewall {cidr, action, note}
func (a *App) handleServerFirewallAdd(w http.ResponseWriter, r *http.Request, c *Ctx) {
	if err := a.requireShield(); err != nil {
		writeErr(w, r, err)
		return
	}
	var in FirewallRule
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	in.ServerID = c.Server.ID
	if err := a.db.AddFirewallRule(&in); err != nil {
		writeErr(w, r, err)
		return
	}
	a.shieldChanged()
	c.log(a, "server.firewall.rule.add", in.Action+" "+in.CIDR)
	writeJSON(w, 200, in)
}

// DELETE /api/servers/{id}/firewall/{rid}
func (a *App) handleServerFirewallDelete(w http.ResponseWriter, r *http.Request, c *Ctx) {
	id, _ := strconv.ParseInt(r.PathValue("rid"), 10, 64)
	rule, err := a.db.DeleteFirewallRule(id, c.Server.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	a.shieldChanged()
	c.log(a, "server.firewall.rule.delete", rule.Action+" "+rule.CIDR)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
