package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- Prometheus metrics
//
// /metrics on a listener of its own (KUBEPILOT_METRICS_LISTEN, :9090 in the chart): never through the ingress,
// since server names and player counts aren't for the internet, and out of reach of the game servers (their
// NetworkPolicy blocks the cluster network). Text exposition format, written by hand: no client library.

type promWriter struct {
	b    strings.Builder
	seen map[string]bool
}

// family declares a metric (HELP and TYPE once, before its first sample).
func (p *promWriter) family(name, typ, help string) {
	if p.seen == nil {
		p.seen = map[string]bool{}
	}
	if p.seen[name] {
		return
	}
	p.seen[name] = true
	fmt.Fprintf(&p.b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

var promEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// sample writes one value; labels are name, value pairs.
func (p *promWriter) sample(name string, value float64, labels ...string) {
	p.b.WriteString(name)
	if len(labels) > 0 {
		p.b.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				p.b.WriteByte(',')
			}
			fmt.Fprintf(&p.b, `%s="%s"`, labels[i], promEscaper.Replace(labels[i+1]))
		}
		p.b.WriteByte('}')
	}
	p.b.WriteByte(' ')
	switch {
	case math.IsNaN(value):
		p.b.WriteString("NaN")
	case value == math.Trunc(value) && math.Abs(value) < 1e15:
		p.b.WriteString(strconv.FormatInt(int64(value), 10))
	default:
		p.b.WriteString(strconv.FormatFloat(value, 'g', -1, 64))
	}
	p.b.WriteByte('\n')
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// ---------- Players of Minecraft servers, pinged at most every 30 s

type playerCount struct {
	online, max int
	ok          bool
	at          time.Time
}

type playerCache struct {
	mu sync.Mutex
	m  map[string]playerCount
}

// players pings the running Minecraft servers whose count is older than 30 s (in parallel, bounded) and returns
// the counts it knows.
func (a *App) players(ctx context.Context, servers []*Server) map[string]playerCount {
	type job struct {
		s    *Server
		kind string
		ip   string
	}
	var jobs []job
	a.playerCache.mu.Lock()
	if a.playerCache.m == nil {
		a.playerCache.m = map[string]playerCount{}
	}
	for _, s := range servers {
		if time.Since(a.playerCache.m[s.ID].at) < 30*time.Second {
			continue
		}
		kind := a.mcKind(s)
		ip := a.kube.PodIP(s.ID)
		if kind == "" || ip == "" || a.kube.State(s.ID).Status != "running" {
			delete(a.playerCache.m, s.ID)
			continue
		}
		jobs = append(jobs, job{s, kind, ip})
	}
	a.playerCache.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			var st *mcStatus
			var err error
			if j.kind == featureMinecraftJava {
				st, err = javaPing(pctx, j.ip, a.gamePort(j.s, 25565))
			} else {
				st, err = bedrockPing(pctx, j.ip, a.gamePort(j.s, 19132))
			}
			pc := playerCount{at: time.Now(), ok: err == nil}
			if err == nil {
				pc.online, pc.max = st.PlayersOnline, st.PlayersMax
			}
			a.playerCache.mu.Lock()
			a.playerCache.m[j.s.ID] = pc
			a.playerCache.mu.Unlock()
		}()
	}
	wg.Wait()
	out := map[string]playerCount{}
	a.playerCache.mu.Lock()
	for k, v := range a.playerCache.m {
		out[k] = v
	}
	a.playerCache.mu.Unlock()
	return out
}

// ---------- The metrics

var serverStates = []string{"offline", "installing", "starting", "running", "stopping", "crashed", "sleeping"}

func (a *App) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if tok := a.cfg.MetricsToken; tok != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(a.metrics(r.Context())))
}

func (a *App) metrics(ctx context.Context) string {
	p := &promWriter{}
	p.family("kubepilot_info", "gauge", "KubePilot version.")
	p.sample("kubepilot_info", 1, "version", version)

	servers, _ := a.db.AllServers()
	tpls, users := a.lookups()
	var userCount int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount)
	p.family("kubepilot_users", "gauge", "User accounts.")
	p.sample("kubepilot_users", float64(userCount))

	players := map[string]playerCount{}
	if a.kube != nil {
		players = a.players(ctx, servers)
	}
	type backupStat struct {
		n     int
		bytes int64
	}
	backups := map[string]backupStat{}
	if rows, err := a.db.Query(`SELECT server_id, COUNT(*), COALESCE(SUM(size),0) FROM backups WHERE status='completed' GROUP BY server_id`); err == nil {
		for rows.Next() {
			var id string
			var bs backupStat
			if rows.Scan(&id, &bs.n, &bs.bytes) == nil {
				backups[id] = bs
			}
		}
		rows.Close()
	}

	byState := map[string]int{}
	type row struct {
		s      *Server
		labels []string
		st     State
	}
	var rows []row
	for _, s := range servers {
		st := State{Status: "offline"}
		if a.kube != nil {
			st = sleepingState(a.kube.State(s.ID), s.Asleep)
		}
		byState[st.Status]++
		rows = append(rows, row{s, []string{"server", s.ID, "name", s.Name}, st})
	}

	p.family("kubepilot_servers", "gauge", "Game servers, by state.")
	for _, state := range serverStates {
		p.sample("kubepilot_servers", float64(byState[state]), "state", state)
	}
	p.family("kubepilot_server_info", "gauge", "One series per game server, with its owner, template and node.")
	for _, r := range rows {
		slug := ""
		if t := tpls[r.s.TemplateID]; t != nil {
			slug = t.Slug
		}
		p.sample("kubepilot_server_info", 1, append(r.labels, "owner", users[r.s.OwnerID], "template", slug, "node", a.serverNode(r.s))...)
	}
	p.family("kubepilot_server_state", "gauge", "1 for the current state of each game server.")
	for _, r := range rows {
		for _, state := range serverStates {
			p.sample("kubepilot_server_state", b2f(r.st.Status == state), append(r.labels, "state", state)...)
		}
	}
	gauge := func(name, help string, value func(r row) (float64, bool)) {
		p.family(name, "gauge", help)
		for _, r := range rows {
			if v, ok := value(r); ok {
				p.sample(name, v, r.labels...)
			}
		}
	}
	gauge("kubepilot_server_up", "1 when the game server is running.", func(r row) (float64, bool) { return b2f(r.st.Status == "running"), true })
	gauge("kubepilot_server_cpu_millicores", "CPU used by the game server, in millicores.", func(r row) (float64, bool) { return r.st.CPU, true })
	gauge("kubepilot_server_cpu_limit_millicores", "CPU limit of the game server, in millicores (0: unlimited).", func(r row) (float64, bool) {
		return float64(r.s.CPUMillis), true
	})
	gauge("kubepilot_server_memory_bytes", "Memory used by the game server.", func(r row) (float64, bool) { return float64(r.st.Memory), true })
	gauge("kubepilot_server_memory_limit_bytes", "Memory limit of the game server.", func(r row) (float64, bool) {
		return float64(int64(r.s.MemoryMB) << 20), true
	})
	gauge("kubepilot_server_disk_bytes", "Disk used by the game server's files.", func(r row) (float64, bool) {
		return float64(a.diskUsed(r.s.ID)), true
	})
	gauge("kubepilot_server_disk_limit_bytes", "Disk limit of the game server.", func(r row) (float64, bool) {
		return float64(int64(r.s.DiskMB) << 20), true
	})
	gauge("kubepilot_server_restarts", "Restarts of the game server's container since its pod started.", func(r row) (float64, bool) {
		return float64(r.st.Restarts), r.st.Status != "offline"
	})
	gauge("kubepilot_server_start_time_seconds", "When the game server's container started (Unix time).", func(r row) (float64, bool) {
		return float64(r.st.StartedAt), r.st.StartedAt > 0
	})
	gauge("kubepilot_server_suspended", "1 when an administrator suspended the game server.", func(r row) (float64, bool) { return b2f(r.s.Suspended), true })
	gauge("kubepilot_server_players", "Players online (Minecraft servers, refreshed at most every 30 s).", func(r row) (float64, bool) {
		pc, ok := players[r.s.ID]
		return float64(pc.online), ok && pc.ok
	})
	gauge("kubepilot_server_players_max", "Player slots (Minecraft servers).", func(r row) (float64, bool) {
		pc, ok := players[r.s.ID]
		return float64(pc.max), ok && pc.ok
	})
	gauge("kubepilot_server_backups", "Completed backups of the game server.", func(r row) (float64, bool) { return float64(backups[r.s.ID].n), true })
	gauge("kubepilot_server_backups_bytes", "Size of the game server's completed backups.", func(r row) (float64, bool) {
		return float64(backups[r.s.ID].bytes), true
	})

	// ports
	allocs, _ := a.db.Allocations("")
	var nodePorts, hostPorts int
	for _, al := range allocs {
		if al.Host {
			hostPorts++
		} else {
			nodePorts++
		}
	}
	st := a.db.Settings()
	pool, _ := parsePortPool(st.HostPorts, a.routerPort(), a.cfg.SFTPPort)
	p.family("kubepilot_ports_used", "gauge", "Game ports given to servers: NodePorts, and standard ports (counted once per node).")
	p.sample("kubepilot_ports_used", float64(nodePorts), "kind", "nodeport")
	p.sample("kubepilot_ports_used", float64(hostPorts), "kind", "standard")
	p.family("kubepilot_ports_available", "gauge", "Size of the NodePort range, and of the standard ports pool (per node).")
	p.sample("kubepilot_ports_available", float64(st.PortMax-st.PortMin+1), "kind", "nodeport")
	p.sample("kubepilot_ports_available", float64(poolSize(pool)), "kind", "standard")

	if a.kube != nil {
		a.nodeMetrics(ctx, p)
	}
	if a.cfg.Shield && a.kube != nil {
		a.shieldMetrics(ctx, p)
	}
	return p.b.String()
}

func (a *App) nodeMetrics(ctx context.Context, p *promWriter) {
	infos, err := a.kube.Nodes(ctx)
	if err != nil {
		return
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	agents, loads := a.kube.agentNodes(ctx), a.nodeLoads()
	type m struct{ name, typ, help string }
	defs := []m{
		{"kubepilot_node_ready", "gauge", "1 when the node is ready."},
		{"kubepilot_node_cpu_millicores", "gauge", "CPU used on the node, in millicores."},
		{"kubepilot_node_cpu_capacity_millicores", "gauge", "Allocatable CPU of the node, in millicores."},
		{"kubepilot_node_memory_bytes", "gauge", "Memory used on the node."},
		{"kubepilot_node_memory_capacity_bytes", "gauge", "Allocatable memory of the node."},
		{"kubepilot_node_servers", "gauge", "Game servers placed on the node."},
		{"kubepilot_node_memory_reserved_bytes", "gauge", "Memory the game servers of the node may use (sum of their limits)."},
	}
	for _, d := range defs {
		p.family(d.name, d.typ, d.help)
		for _, n := range infos {
			l := []string{"node", n.Name}
			load := loads[n.Name]
			if load == nil {
				load = &nodeLoad{}
			}
			switch d.name {
			case "kubepilot_node_ready":
				p.sample(d.name, b2f(n.Ready), l...)
			case "kubepilot_node_cpu_millicores":
				p.sample(d.name, n.CPU, l...)
			case "kubepilot_node_cpu_capacity_millicores":
				p.sample(d.name, n.CPUCap, l...)
			case "kubepilot_node_memory_bytes":
				p.sample(d.name, float64(n.Mem), l...)
			case "kubepilot_node_memory_capacity_bytes":
				p.sample(d.name, float64(n.MemCap), l...)
			case "kubepilot_node_servers":
				p.sample(d.name, float64(load.servers), l...)
			case "kubepilot_node_memory_reserved_bytes":
				p.sample(d.name, float64(load.mem<<20), l...)
			}
		}
	}
	if a.kube.nodes != nil {
		p.family("kubepilot_node_agent_up", "gauge", "1 when the node's KubePilot agent is running (it serves the node's game files).")
		for _, n := range infos {
			p.sample("kubepilot_node_agent_up", b2f(agents[n.Name]), "node", n.Name)
		}
	}
}

func (a *App) shieldMetrics(ctx context.Context, p *promWriter) {
	nodes := a.shieldStatus(ctx)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Node < nodes[j].Node })
	p.family("kubepilot_shield_dropped_packets_total", "counter", "Packets the firewall dropped on the game ports, by reason (since the node's table was created).")
	for _, n := range nodes {
		for _, reason := range shieldCounters {
			p.sample("kubepilot_shield_dropped_packets_total", float64(n.Counters[reason]), "node", n.Node, "reason", reason)
		}
		var rules uint64
		for _, v := range n.Servers {
			rules += v
		}
		p.sample("kubepilot_shield_dropped_packets_total", float64(rules), "node", n.Node, "reason", "server_rules")
	}
	p.family("kubepilot_shield_banned_ips", "gauge", "IP addresses banned right now, per node.")
	for _, n := range nodes {
		p.sample("kubepilot_shield_banned_ips", float64(max(n.BanCount, len(n.Bans))), "node", n.Node)
	}
	a.shield.mu.Lock()
	rev := a.shield.rev
	a.shield.mu.Unlock()
	p.family("kubepilot_shield_up", "gauge", "1 when the node's firewall agent reported recently and applied the current configuration.")
	for _, n := range nodes {
		fresh := now()-n.Updated <= shieldStaleAfter
		p.sample("kubepilot_shield_up", b2f(fresh && rev != "" && n.Rev == rev && n.Error == ""), "node", n.Node)
	}
}
