package main

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- Standard game ports
//
// By default a server's ports are NodePorts (30100-30299): unique in the whole cluster, open on every node.
// A server with standard ports instead gets ports of the admin's pool (25565, 27015, 7777…) opened as host
// ports on its own node: players type the game's usual port, a port number can be used once per node, and the
// game sees the players' real addresses. The ports must be free on the machines: the panel skips the ones
// something already answers on, but the pool is the admin's promise.

// The allocations table: id as primary key, NodePorts unique in the cluster, host ports unique per node.
const allocationsSchema = `
CREATE TABLE allocations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  port INTEGER NOT NULL,
  server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  protocol TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  target_port INTEGER NOT NULL,
  is_primary INTEGER NOT NULL DEFAULT 0,
  notes TEXT NOT NULL DEFAULT '',
  host INTEGER NOT NULL DEFAULT 0,
  node TEXT NOT NULL DEFAULT ''
)`

const allocationsIndexes = `
CREATE UNIQUE INDEX IF NOT EXISTS allocations_nodeport ON allocations(port) WHERE host=0;
CREATE UNIQUE INDEX IF NOT EXISTS allocations_hostport ON allocations(node, port) WHERE host=1;
CREATE UNIQUE INDEX IF NOT EXISTS allocations_server_port ON allocations(server_id, port);
`

// migrateAllocations creates the table, or rebuilds the first one (the port as primary key: one owner per port
// number in the whole cluster) into the current one, rows kept.
func (db *DB) migrateAllocations() error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='allocations'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := db.Exec(allocationsSchema); err != nil {
			return err
		}
	} else if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('allocations') WHERE name='host'`).Scan(&n); err != nil {
		return err
	} else if n == 0 {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, q := range []string{
			`ALTER TABLE allocations RENAME TO allocations_v1`,
			allocationsSchema,
			`INSERT INTO allocations(port, server_id, protocol, name, target_port, is_primary, notes)
			 SELECT port, server_id, protocol, name, target_port, is_primary, notes FROM allocations_v1 ORDER BY port`,
			`DROP TABLE allocations_v1`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return fmt.Errorf("allocations migration: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	_, err := db.Exec(allocationsIndexes)
	return err
}

// ---------- The admin's pool: "25565-25600, 27015-27030, 7777"

type portRange struct{ lo, hi int }

// reservedPort: ports a game server must never take on a node — the cluster's own, the panel's and the
// usual services, and the NodePort range (kube-proxy owns it).
func reservedPort(p, routerPort, sftpPort int) string {
	switch {
	case p >= 30000 && p <= 32767:
		return "Kubernetes NodePorts"
	case p == routerPort && routerPort > 0:
		return "Minecraft router"
	case p == sftpPort && sftpPort > 0:
		return "SFTP"
	}
	switch p {
	case 22:
		return "SSH"
	case 53:
		return "DNS"
	case 80, 443:
		return "HTTP(S)"
	case 2379, 2380, 6443, 6444, 10248, 10249, 10250, 10256, 10257, 10259:
		return "Kubernetes"
	case 8472, 51820, 51821:
		return "cluster network"
	}
	return ""
}

// parsePortPool reads a pool of ports ("25565-25600, 27015"); at most 2000 ports in all.
func parsePortPool(s string, routerPort, sftpPort int) ([]portRange, error) {
	var out []portRange
	total := 0
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' }) {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.Atoi(strings.TrimSpace(hi))
		}
		if err1 != nil || err2 != nil || a < 1024 || b > 65535 || a > b {
			return nil, errCode(400, "Invalid port range: %s (ports 1024-65535, e.g. 25565-25600)", part)
		}
		for p := a; p <= b; p++ {
			if why := reservedPort(p, routerPort, sftpPort); why != "" {
				return nil, errCode(400, "Port %s is reserved (%s): leave it out of the standard ports", strconv.Itoa(p), why)
			}
		}
		total += b - a + 1
		out = append(out, portRange{a, b})
	}
	if total > 2000 {
		return nil, errCode(400, "At most 2000 standard ports")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].lo < out[j].lo })
	for i := 1; i < len(out); i++ {
		if out[i].lo <= out[i-1].hi {
			return nil, errCode(400, "The port ranges overlap")
		}
	}
	return out, nil
}

func formatPortPool(pool []portRange) string {
	parts := make([]string, 0, len(pool))
	for _, r := range pool {
		if r.lo == r.hi {
			parts = append(parts, strconv.Itoa(r.lo))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", r.lo, r.hi))
		}
	}
	return strings.Join(parts, ", ")
}

func poolContains(pool []portRange, p int) bool {
	for _, r := range pool {
		if p >= r.lo && p <= r.hi {
			return true
		}
	}
	return false
}

func poolSize(pool []portRange) int {
	n := 0
	for _, r := range pool {
		n += r.hi - r.lo + 1
	}
	return n
}

// ---------- Choosing a port

// pickHostPort: the preferred port (the game's usual one) when it's in the pool and free on the node, else the
// first free port of the pool. busy reports a port something already answers on, on the node.
func pickHostPort(pool []portRange, used map[int]bool, prefer int, busy func(int) bool) (int, bool) {
	try := func(p int) bool { return !used[p] && (busy == nil || !busy(p)) }
	if prefer > 0 && poolContains(pool, prefer) && try(prefer) {
		return prefer, true
	}
	for _, r := range pool {
		for p := r.lo; p <= r.hi; p++ {
			if p != prefer && try(p) {
				return p, true
			}
		}
	}
	return 0, false
}

// allocNode is the node a server's host ports are counted on: the node it was placed on, or where its volume is;
// "" on a single node (every unpinned server shares it).
func (a *App) allocNode(ctx context.Context, s *Server) string {
	if s.Node != "" {
		return s.Node
	}
	if a.kube != nil {
		if ref, err := a.kube.Volume(ctx, s.ID); err == nil && ref.Node != "" {
			if nodes, err := a.kube.clusterNodes(ctx); err == nil && len(nodes) > 1 {
				return ref.Node
			}
		}
	}
	return ""
}

// newAllocation reserves a port for a server: from the NodePort range, or from the standard ports of its node.
// prefer: the port the game uses by default (standard ports only). Callers hold allocMu.
func (a *App) newAllocation(ctx context.Context, s *Server, prefer int) (*Allocation, error) {
	if !s.HostPorts {
		port, err := a.allocatePort(ctx)
		if err != nil {
			return nil, err
		}
		return &Allocation{Port: port, ServerID: s.ID}, nil
	}
	st := a.db.Settings()
	pool, err := parsePortPool(st.HostPorts, a.routerPort(), a.cfg.SFTPPort)
	if err != nil || len(pool) == 0 {
		return nil, errCode(409, "No standard ports are set up: add a range in Admin → Settings")
	}
	node := a.allocNode(ctx, s)
	used := map[int]bool{}
	rows, err := a.db.Query(`SELECT port FROM allocations WHERE host=1 AND node=?`, node)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p int
		if rows.Scan(&p) == nil {
			used[p] = true
		}
	}
	rows.Close()
	port, ok := pickHostPort(pool, used, prefer, a.portBusy(ctx, node))
	if !ok {
		return nil, errCode(409, "No free standard port left on this machine (%s ports in the pool)", strconv.Itoa(poolSize(pool)))
	}
	return &Allocation{Port: port, ServerID: s.ID, Host: true, Node: node}, nil
}

// routerPort: the port mc-router takes on every node, when it's deployed.
func (a *App) routerPort() int {
	if a.cfg.MinecraftRouter {
		return a.cfg.RouterPort
	}
	return 0
}

// portBusy probes a node's address: a TCP port something answers on (another program, a Pterodactyl server…)
// is skipped. UDP can't be probed: the pool is the admin's promise for it.
func (a *App) portBusy(ctx context.Context, node string) func(int) bool {
	ip := ""
	if a.kube != nil {
		if nodes, err := a.kube.clusterNodes(ctx); err == nil {
			for i := range nodes {
				if nodes[i].Name == node || (node == "" && len(nodes) == 1) {
					ip = nodeIP(&nodes[i])
				}
			}
		}
	}
	if ip == "" {
		return nil
	}
	d := net.Dialer{Timeout: 400 * time.Millisecond}
	return func(p int) bool {
		c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(p)))
		if err != nil {
			return false
		}
		c.Close()
		return true
	}
}

// reallocate gives a stopped server new ports after it switched between NodePorts and standard ports: same
// names, target ports, protocols and primary flag; the game's usual port first.
func (a *App) reallocate(ctx context.Context, s *Server) error {
	a.allocMu.Lock()
	defer a.allocMu.Unlock()
	old, err := a.db.Allocations(s.ID)
	if err != nil {
		return err
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM allocations WHERE server_id=?`, s.ID); err != nil {
		return err
	}
	// the new ports are chosen with the old ones already released: commit first, then insert one by one
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, o := range old {
		prefer := o.TargetPort
		al, err := a.newAllocation(ctx, s, prefer)
		if err == nil {
			al.Protocol, al.Name, al.TargetPort, al.IsPrimary, al.Notes = o.Protocol, o.Name, o.TargetPort, o.IsPrimary, o.Notes
			if o.Name == "extra" && o.TargetPort == o.Port { // an extra port: the game listens on the public one
				al.TargetPort = al.Port
			}
			err = a.db.InsertAllocation(al)
		}
		if err != nil { // put the old ports back: the server keeps working as before
			_, _ = a.db.Exec(`DELETE FROM allocations WHERE server_id=?`, s.ID)
			for _, o := range old {
				_ = a.db.InsertAllocation(o)
			}
			return err
		}
	}
	return nil
}
