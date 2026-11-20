package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Ctx struct {
	User     *User
	Server   *Server
	Perms    []string
	IP       string
	ReadOnly bool // read-only API key: no console command or power action over the WebSocket either
}

func (c *Ctx) Can(p string) bool { return slices.Contains(c.Perms, p) }

func (c *Ctx) log(a *App, action, meta string) {
	a.db.LogActivity(c.Server.ID, c.User, c.IP, action, meta)
}

func (a *App) permsFor(u *User, s *Server) ([]string, bool) {
	if u.IsAdmin || s.OwnerID == u.ID {
		return append(slices.Clone(AllPerms), "users", "settings"), true
	}
	return a.db.SubUserPerms(s.ID, u.ID)
}

type srvHandler func(w http.ResponseWriter, r *http.Request, c *Ctx)

func (a *App) srv(perm string, h srvHandler) http.HandlerFunc {
	return a.authed(func(w http.ResponseWriter, r *http.Request, u *User) {
		s, err := a.db.Server(r.PathValue("id"))
		if err != nil {
			writeErr(w, r, err)
			return
		}
		perms, ok := a.permsFor(u, s)
		if !ok {
			writeErr(w, r, ErrNotFound) // don't reveal existence
			return
		}
		if s.Suspended && !u.IsAdmin {
			writeErr(w, r, errCode(403, "This server is suspended"))
			return
		}
		k := keyFrom(r)
		c := &Ctx{User: u, Server: s, Perms: keyPerms(perms, k), IP: clientIP(r), ReadOnly: k != nil && k.ReadOnly}
		if perm != "" && !c.Can(perm) {
			writeErr(w, r, errCode(403, "Missing permission: %s", perm))
			return
		}
		h(w, r, c)
	})
}

func (a *App) spec(s *Server) (Spec, error) {
	t, err := a.db.Template(s.TemplateID)
	if err != nil {
		return Spec{}, fmt.Errorf("template: %w", err)
	}
	allocs, err := a.db.Allocations(s.ID)
	if err != nil {
		return Spec{}, err
	}
	return Spec{Server: s, Template: t, Allocs: allocs, Settings: a.db.Settings()}, nil
}

func (a *App) publicHost(r *http.Request) string {
	if h := a.db.Settings().PublicHost; h != "" {
		return h
	}
	if nodes, err := a.kube.Nodes(r.Context()); err == nil {
		for _, n := range nodes {
			if n.Ready {
				return n.Address
			}
		}
	}
	return "?"
}

// ---------- Views

type ServerView struct {
	*Server
	Template     string        `json:"template"`
	TemplateSlug string        `json:"templateSlug"`
	ConsoleInput bool          `json:"consoleInput"`
	Owner        string        `json:"owner"`
	State        State         `json:"state"`
	DiskUsed     int64         `json:"diskUsed"`
	Address      string        `json:"address"`
	Allocations  []*Allocation `json:"allocations"`
	Permissions  []string      `json:"permissions"`
	Features     []string      `json:"features"` // game-specific pages (see templateFeatures)
}

func (a *App) view(s *Server, perms []string, host string, tpls map[int64]*Template, users map[int64]string) ServerView {
	v := ServerView{Server: s, State: a.kube.State(s.ID), Permissions: perms, Owner: users[s.OwnerID]}
	if t := tpls[s.TemplateID]; t != nil {
		v.Template, v.TemplateSlug, v.ConsoleInput = t.Name, t.Slug, t.ConsoleInput
	}
	v.Features = templateFeatures(tpls[s.TemplateID])
	a.diskMu.RLock()
	v.DiskUsed = a.disk[s.ID]
	a.diskMu.RUnlock()
	v.Allocations, _ = a.db.Allocations(s.ID)
	for _, al := range v.Allocations {
		if al.IsPrimary {
			v.Address = fmt.Sprintf("%s:%d", host, al.Port)
		}
	}
	if !slices.Contains(perms, "settings") {
		v.Server = &Server{ID: s.ID, Name: s.Name, Description: s.Description, OwnerID: s.OwnerID, TemplateID: s.TemplateID,
			Image: s.Image, MemoryMB: s.MemoryMB, CPUMillis: s.CPUMillis, DiskMB: s.DiskMB, BackupLimit: s.BackupLimit,
			AllocationLimit: s.AllocationLimit, Suspended: s.Suspended, CreatedAt: s.CreatedAt, Env: map[string]string{}}
	}
	return v
}

func (a *App) lookups() (map[int64]*Template, map[int64]string) {
	tpls := map[int64]*Template{}
	if ts, err := a.db.Templates(); err == nil {
		for _, t := range ts {
			tpls[t.ID] = t
		}
	}
	users := map[int64]string{}
	if us, err := a.db.Users(); err == nil {
		for _, u := range us {
			users[u.ID] = u.Username
		}
	}
	return tpls, users
}

func (a *App) handleServers(w http.ResponseWriter, r *http.Request, u *User) {
	var list []*Server
	var err error
	if u.IsAdmin && r.URL.Query().Get("all") == "1" {
		list, err = a.db.AllServers()
	} else {
		list, err = a.db.ServersForUser(u.ID)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	host := a.publicHost(r)
	tpls, users := a.lookups()
	out := []ServerView{}
	for _, s := range list {
		perms, _ := a.permsFor(u, s)
		out = append(out, a.view(s, perms, host, tpls, users))
	}
	writeJSON(w, 200, out)
}

func (a *App) handleServer(w http.ResponseWriter, r *http.Request, c *Ctx) {
	tpls, users := a.lookups()
	writeJSON(w, 200, a.view(c.Server, c.Perms, a.publicHost(r), tpls, users))
}

// ---------- Power

func (a *App) diskUsed(id string) int64 {
	a.diskMu.RLock()
	defer a.diskMu.RUnlock()
	return a.disk[id]
}

func (a *App) startServer(ctx context.Context, s *Server) error {
	if s.Suspended {
		return errCode(403, "Server suspended")
	}
	if used := a.diskUsed(s.ID); s.DiskMB > 0 && used > int64(s.DiskMB)*1024*1024 {
		return errCode(409, "Disk limit exceeded: free some space before starting")
	}
	sp, err := a.spec(s)
	if err != nil {
		return err
	}
	if err := a.kube.EnsurePVC(ctx, s); err != nil {
		return err
	}
	if err := a.kube.EnsureService(ctx, sp); err != nil {
		return err
	}
	go a.refreshDisk(a.ctx, s.ID)
	return a.kube.ApplyStatefulSet(ctx, sp, 1)
}

func (a *App) waitPodGone(ctx context.Context, id string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !a.kube.PodExists(id) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
	return false
}

// power runs a power action. Restart (and start on a crashed server) continues in the background.
func (a *App) power(s *Server, action string) error {
	lock := a.serverLock(s.ID)
	if !lock.TryLock() {
		return errCode(409, "Another action is already running on this server")
	}
	st := a.kube.State(s.ID)
	if st.Status == "installing" {
		lock.Unlock()
		return errCode(409, "Installation in progress, please wait")
	}
	ctx := a.ctx
	switch action {
	case "start":
		if st.Status == "running" || st.Status == "starting" {
			lock.Unlock()
			return nil
		}
		if st.Status == "crashed" || st.Status == "stopping" {
			action = "restart"
			break
		}
		defer lock.Unlock()
		a.hub(s.ID).daemon("Start requested…")
		return a.startServer(ctx, s)
	case "stop":
		defer lock.Unlock()
		a.hub(s.ID).daemon("Stop requested…")
		return a.kube.SetReplicas(ctx, s.ID, 0)
	case "kill":
		defer lock.Unlock()
		a.hub(s.ID).daemon("Forced stop (kill)…")
		if err := a.kube.SetReplicas(ctx, s.ID, 0); err != nil {
			return err
		}
		return a.kube.KillPod(ctx, s.ID)
	case "restart":
	default:
		lock.Unlock()
		return errCode(400, "Unknown action")
	}
	// restart
	if s.Suspended {
		lock.Unlock()
		return errCode(403, "Server suspended")
	}
	a.hub(s.ID).daemon("Restart requested…")
	go func() {
		defer lock.Unlock()
		if err := a.kube.SetReplicas(ctx, s.ID, 0); err != nil {
			a.hub(s.ID).daemon("Erreur : " + err.Error())
			return
		}
		grace := 60 * time.Second
		if t, err := a.db.Template(s.TemplateID); err == nil {
			grace = time.Duration(t.StopGrace+20) * time.Second
		}
		if !a.waitPodGone(ctx, s.ID, grace) {
			_ = a.kube.KillPod(ctx, s.ID)
			a.waitPodGone(ctx, s.ID, 20*time.Second)
		}
		fresh, err := a.db.Server(s.ID)
		if err != nil {
			return
		}
		if err := a.startServer(ctx, fresh); err != nil {
			a.hub(s.ID).daemonErr("Failed to start: %s", err.Error())
		}
	}()
	return nil
}

func (a *App) handlePower(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ Action string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if err := a.power(c.Server, in.Action); err != nil {
		writeErr(w, r, err)
		return
	}
	c.log(a, "server.power."+in.Action, "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) sendCommand(s *Server, cmd string) error {
	cmd = strings.TrimRight(cmd, "\r\n")
	if cmd == "" || len(cmd) > 2000 || strings.ContainsAny(cmd, "\n\r") {
		return errCode(400, "Invalid command")
	}
	t, err := a.db.Template(s.TemplateID)
	if err != nil {
		return err
	}
	if !t.ConsoleInput {
		return errCode(400, "This type of server doesn't accept console commands")
	}
	if err := a.kube.SendStdin(s.ID, cmd); err != nil {
		return errCode(409, err.Error())
	}
	return nil
}

func (a *App) handleCommand(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ Command string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if err := a.sendCommand(c.Server, in.Command); err != nil {
		writeErr(w, r, err)
		return
	}
	c.log(a, "server.console.command", in.Command)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- Startup / variables

type VarView struct {
	TemplateVar
	Value string `json:"value"`
}

func (a *App) handleStartup(w http.ResponseWriter, r *http.Request, c *Ctx) {
	t, err := a.db.Template(c.Server.TemplateID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	vals := EffectiveVars(c.Server, t)
	vars := []VarView{}
	for _, v := range t.Vars {
		if !v.UserViewable && !c.User.IsAdmin {
			continue
		}
		if !c.User.IsAdmin && !v.UserEditable {
			// viewable but read-only
		}
		vars = append(vars, VarView{TemplateVar: v, Value: vals[v.Key]})
	}
	writeJSON(w, 200, map[string]any{
		"startup": c.Server.Startup, "image": c.Server.Image, "images": t.Images, "vars": vars,
		"dataPath": t.DataPath, "canEditAll": c.User.IsAdmin,
	})
}

func (a *App) handleStartupSave(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct {
		Env     map[string]string `json:"env"`
		Image   *string           `json:"image"`
		Startup *string           `json:"startup"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	t, err := a.db.Template(c.Server.TemplateID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s := c.Server
	changed := []string{}
	for key, val := range in.Env {
		var tv *TemplateVar
		for i := range t.Vars {
			if t.Vars[i].Key == key {
				tv = &t.Vars[i]
			}
		}
		if tv == nil {
			continue
		}
		if !c.User.IsAdmin && (!tv.UserEditable || !tv.UserViewable) {
			continue
		}
		if len(val) > 4096 {
			writeErr(w, r, errCode(400, "%s: value too long", tv.Name))
			return
		}
		if len(tv.Options) > 0 && !slices.Contains(tv.Options, val) {
			writeErr(w, r, errCode(400, "%s: value not allowed", tv.Name))
			return
		}
		if tv.Pattern != "" {
			if ok, _ := regexp.MatchString(tv.Pattern, val); !ok {
				writeErr(w, r, errCode(400, "%s: invalid format", tv.Name))
				return
			}
		}
		if EffectiveVars(s, t)[key] != val {
			changed = append(changed, key)
		}
		if val == tv.Default {
			delete(s.Env, key)
		} else {
			s.Env[key] = val
		}
	}
	if in.Image != nil && *in.Image != s.Image {
		ok := c.User.IsAdmin
		for _, im := range t.Images {
			if im.Image == *in.Image {
				ok = true
			}
		}
		if !ok || strings.TrimSpace(*in.Image) == "" {
			writeErr(w, r, errCode(400, "Image not allowed"))
			return
		}
		s.Image = strings.TrimSpace(*in.Image)
		changed = append(changed, "image")
	}
	if in.Startup != nil && c.User.IsAdmin && *in.Startup != s.Startup {
		s.Startup = *in.Startup
		changed = append(changed, "startup")
	}
	if err := a.db.UpdateServer(s); err != nil {
		writeErr(w, r, err)
		return
	}
	c.log(a, "server.startup.update", strings.Join(changed, ", "))
	writeJSON(w, 200, map[string]any{"ok": true, "restartRequired": len(changed) > 0})
}

// ---------- Network / allocations

func (a *App) allocatePort(ctx context.Context) (int, error) {
	st := a.db.Settings()
	used, err := a.kube.UsedNodePorts(ctx)
	if err != nil {
		return 0, err
	}
	allocs, err := a.db.Allocations("")
	if err != nil {
		return 0, err
	}
	for _, al := range allocs {
		used[al.Port] = true
	}
	for p := st.PortMin; p <= st.PortMax; p++ {
		if !used[p] {
			return p, nil
		}
	}
	return 0, errCode(409, "No free port left in the range %d-%d", st.PortMin, st.PortMax)
}

func (a *App) handleNetwork(w http.ResponseWriter, r *http.Request, c *Ctx) {
	allocs, err := a.db.Allocations(c.Server.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"host": a.publicHost(r), "allocations": allocs, "limit": c.Server.AllocationLimit})
}

func (a *App) handleAllocAdd(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ Protocol, Notes string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if in.Protocol != "udp" && in.Protocol != "tcp+udp" {
		in.Protocol = "tcp"
	}
	a.allocMu.Lock()
	defer a.allocMu.Unlock()
	allocs, _ := a.db.Allocations(c.Server.ID)
	extra := 0
	for _, al := range allocs {
		if !al.IsPrimary {
			extra++
		}
	}
	if !c.User.IsAdmin && extra >= c.Server.AllocationLimit {
		writeErr(w, r, errCode(403, "Extra port limit reached (%d)", c.Server.AllocationLimit))
		return
	}
	port, err := a.allocatePort(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	al := &Allocation{Port: port, ServerID: c.Server.ID, Protocol: in.Protocol, Name: "extra", TargetPort: port, IsPrimary: len(allocs) == 0, Notes: truncate(in.Notes, 100)}
	if err := a.db.InsertAllocation(al); err != nil {
		writeErr(w, r, err)
		return
	}
	if sp, err := a.spec(c.Server); err == nil {
		_ = a.kube.EnsureService(r.Context(), sp)
	}
	c.log(a, "server.allocation.create", fmt.Sprintf("%d/%s", port, in.Protocol))
	writeJSON(w, 200, al)
}

func (a *App) handleAllocUpdate(w http.ResponseWriter, r *http.Request, c *Ctx) {
	port, _ := strconv.Atoi(r.PathValue("port"))
	var in struct {
		Notes   *string `json:"notes"`
		Primary bool    `json:"primary"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	allocs, _ := a.db.Allocations(c.Server.ID)
	var target *Allocation
	for _, al := range allocs {
		if al.Port == port {
			target = al
		}
	}
	if target == nil {
		writeErr(w, r, ErrNotFound)
		return
	}
	if in.Notes != nil {
		_, _ = a.db.Exec(`UPDATE allocations SET notes=? WHERE port=?`, truncate(*in.Notes, 100), port)
	}
	if in.Primary && !target.IsPrimary {
		_, _ = a.db.Exec(`UPDATE allocations SET is_primary=(port=?) WHERE server_id=?`, port, c.Server.ID)
		c.log(a, "server.allocation.primary", strconv.Itoa(port))
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) handleAllocDelete(w http.ResponseWriter, r *http.Request, c *Ctx) {
	port, _ := strconv.Atoi(r.PathValue("port"))
	res, err := a.db.Exec(`DELETE FROM allocations WHERE port=? AND server_id=? AND is_primary=0`, port, c.Server.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, r, errCode(400, "Port not found, or primary port (can't be removed)"))
		return
	}
	if sp, err := a.spec(c.Server); err == nil {
		_ = a.kube.EnsureService(r.Context(), sp)
	}
	c.log(a, "server.allocation.delete", strconv.Itoa(port))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ---------- Sub-users

func (a *App) handleSubUsers(w http.ResponseWriter, r *http.Request, c *Ctx) {
	list, err := a.db.SubUsers(c.Server.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"users": list, "permissions": AllPerms})
}

func (a *App) handleSubUserSave(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct {
		Username    string   `json:"username"`
		Permissions []string `json:"permissions"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	perms := cleanPerms(in.Permissions)
	if uidStr := r.PathValue("uid"); uidStr != "" {
		uid, _ := strconv.ParseInt(uidStr, 10, 64)
		if affected(a.db.Exec(`UPDATE server_users SET permissions=? WHERE server_id=? AND user_id=?`, perms, c.Server.ID, uid)) == 0 {
			writeErr(w, r, ErrNotFound)
			return
		}
		c.log(a, "server.subuser.update", fmt.Sprintf("#%d : %s", uid, perms))
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	u, err := a.db.UserByLogin(strings.TrimSpace(in.Username))
	if err != nil {
		writeErr(w, r, errCode(404, "No user with this username or email"))
		return
	}
	if u.ID == c.Server.OwnerID {
		writeErr(w, r, errCode(400, "This user is already the owner"))
		return
	}
	if _, err := a.db.Exec(`INSERT INTO server_users(server_id,user_id,permissions,created_at) VALUES(?,?,?,?)`, c.Server.ID, u.ID, perms, now()); err != nil {
		writeErr(w, r, errCode(400, "This user already has access"))
		return
	}
	c.log(a, "server.subuser.create", u.Username+" : "+perms)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) handleSubUserDelete(w http.ResponseWriter, r *http.Request, c *Ctx) {
	uid, _ := strconv.ParseInt(r.PathValue("uid"), 10, 64)
	_, _ = a.db.Exec(`DELETE FROM server_users WHERE server_id=? AND user_id=?`, c.Server.ID, uid)
	c.log(a, "server.subuser.delete", fmt.Sprintf("#%d", uid))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- Activity / settings / reinstall

func (a *App) handleServerActivity(w http.ResponseWriter, r *http.Request, c *Ctx) {
	list, err := a.db.ActivityList(c.Server.ID, 200)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if !c.User.IsAdmin {
		for _, it := range list {
			if it.UserID != c.User.ID {
				it.IP = ""
			}
		}
	}
	writeJSON(w, 200, list)
}

func (a *App) handleServerSettings(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ Name, Description string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 60 {
		writeErr(w, r, errCode(400, "Name required (60 characters max)"))
		return
	}
	c.Server.Name, c.Server.Description = in.Name, truncate(strings.TrimSpace(in.Description), 300)
	if err := a.db.UpdateServer(c.Server); err != nil {
		writeErr(w, r, err)
		return
	}
	c.log(a, "server.settings.rename", in.Name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) install(ctx context.Context, s *Server) error {
	sp, err := a.spec(s)
	if err != nil {
		return err
	}
	if err := a.kube.EnsurePVC(ctx, s); err != nil {
		return err
	}
	if err := a.kube.EnsureService(ctx, sp); err != nil {
		return err
	}
	a.hub(s.ID).daemon("Starting the installation…")
	return a.kube.RunInstall(ctx, sp)
}

func (a *App) handleReinstall(w http.ResponseWriter, r *http.Request, c *Ctx) {
	st := a.kube.State(c.Server.ID)
	if st.Status != "offline" && st.Status != "crashed" {
		writeErr(w, r, errCode(409, "Stop the server before reinstalling it"))
		return
	}
	if st.Status == "crashed" {
		_ = a.kube.SetReplicas(r.Context(), c.Server.ID, 0)
		a.waitPodGone(r.Context(), c.Server.ID, 30*time.Second)
	}
	if err := a.install(r.Context(), c.Server); err != nil {
		writeErr(w, r, err)
		return
	}
	c.log(a, "server.reinstall", "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- Admin: servers

type serverInput struct {
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	OwnerID         int64             `json:"ownerId"`
	TemplateID      int64             `json:"templateId"`
	Image           string            `json:"image"`
	Startup         *string           `json:"startup"`
	Memory          int               `json:"memory"`
	CPU             int               `json:"cpu"`
	Disk            int               `json:"disk"`
	BackupLimit     int               `json:"backupLimit"`
	AllocationLimit int               `json:"allocationLimit"`
	Suspended       bool              `json:"suspended"`
	Env             map[string]string `json:"env"`
	StartAfter      bool              `json:"startAfterInstall"`
}

func (in *serverInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "" || len(in.Name) > 60:
		return errCode(400, "Name required (60 characters max)")
	case in.Memory < 128 || in.Memory > 262144:
		return errCode(400, "Memory must be between 128 MB and 256 GB")
	case in.CPU != 0 && (in.CPU < 100 || in.CPU > 128000):
		return errCode(400, "CPU: 0 (unlimited) or between 100 and 128000 millicores")
	case in.Disk < 256 || in.Disk > 4194304:
		return errCode(400, "Disk must be between 256 MB and 4 TB")
	case in.BackupLimit < 0 || in.BackupLimit > 100 || in.AllocationLimit < 0 || in.AllocationLimit > 50:
		return errCode(400, "Invalid limits")
	}
	return nil
}

func (a *App) handleAdminServerCreate(w http.ResponseWriter, r *http.Request, u *User) {
	var in serverInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, r, err)
		return
	}
	if _, err := a.db.UserByID(in.OwnerID); err != nil {
		writeErr(w, r, errCode(400, "Owner not found"))
		return
	}
	t, err := a.db.Template(in.TemplateID)
	if err != nil {
		writeErr(w, r, errCode(400, "Template not found"))
		return
	}
	s := &Server{
		ID: randomID(8), Name: in.Name, Description: truncate(in.Description, 300), OwnerID: in.OwnerID, TemplateID: t.ID,
		Image: t.Images[0].Image, Startup: t.Startup, Env: map[string]string{}, MemoryMB: in.Memory, CPUMillis: in.CPU,
		DiskMB: in.Disk, BackupLimit: in.BackupLimit, AllocationLimit: in.AllocationLimit, CreatedAt: now(),
	}
	if strings.TrimSpace(in.Image) != "" {
		s.Image = strings.TrimSpace(in.Image)
	}
	for _, v := range t.Vars {
		if val, ok := in.Env[v.Key]; ok && val != v.Default {
			s.Env[v.Key] = val
		}
	}

	a.allocMu.Lock()
	if err := a.db.InsertServer(s); err != nil {
		a.allocMu.Unlock()
		writeErr(w, r, err)
		return
	}
	for i, tp := range t.Ports {
		port, err := a.allocatePort(r.Context())
		if err == nil {
			err = a.db.InsertAllocation(&Allocation{Port: port, ServerID: s.ID, Protocol: tp.Protocol, Name: tp.Name, TargetPort: tp.Port, IsPrimary: i == 0})
		}
		if err != nil {
			a.allocMu.Unlock()
			_, _ = a.db.Exec(`DELETE FROM servers WHERE id=?`, s.ID)
			writeErr(w, r, err)
			return
		}
	}
	a.allocMu.Unlock()

	if err := a.install(r.Context(), s); err != nil {
		writeErr(w, r, errCode(500, "Server created, but the installation failed: %s", err.Error()))
		return
	}
	a.db.LogActivity(s.ID, u, clientIP(r), "server.create", s.Name)
	if in.StartAfter {
		go a.startAfterInstall(s.ID)
	}
	writeJSON(w, 200, s)
}

func (a *App) startAfterInstall(id string) {
	time.Sleep(3 * time.Second)
	deadline := time.Now().Add(35 * time.Minute)
	for time.Now().Before(deadline) {
		st := a.kube.State(id)
		if st.Status != "installing" {
			if st.InstallError {
				a.hub(id).daemonErr("The installation failed: automatic start cancelled.")
				return
			}
			if s, err := a.db.Server(id); err == nil {
				if err := a.power(s, "start"); err != nil {
					a.hub(id).daemonErr("Automatic start failed: %s", err.Error())
				}
			}
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func (a *App) handleAdminServerUpdate(w http.ResponseWriter, r *http.Request, u *User) {
	s, err := a.db.Server(r.PathValue("id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in serverInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, r, err)
		return
	}
	if _, err := a.db.UserByID(in.OwnerID); err != nil {
		writeErr(w, r, errCode(400, "Owner not found"))
		return
	}
	wasSuspended := s.Suspended
	s.Name, s.Description, s.OwnerID = in.Name, truncate(in.Description, 300), in.OwnerID
	s.MemoryMB, s.CPUMillis, s.DiskMB = in.Memory, in.CPU, in.Disk
	s.BackupLimit, s.AllocationLimit, s.Suspended = in.BackupLimit, in.AllocationLimit, in.Suspended
	if strings.TrimSpace(in.Image) != "" {
		s.Image = strings.TrimSpace(in.Image)
	}
	if in.Startup != nil {
		s.Startup = *in.Startup
	}
	if err := a.db.UpdateServer(s); err != nil {
		writeErr(w, r, err)
		return
	}
	if s.Suspended && !wasSuspended {
		_ = a.kube.SetReplicas(r.Context(), s.ID, 0)
		a.hub(s.ID).daemon("Server suspended by an administrator.")
	}
	a.db.LogActivity(s.ID, u, clientIP(r), "admin.server.update", s.Name)
	writeJSON(w, 200, s)
}

func (a *App) handleAdminServerDelete(w http.ResponseWriter, r *http.Request, u *User) {
	s, err := a.db.Server(r.PathValue("id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := a.kube.DeleteServer(r.Context(), s.ID); err != nil {
		writeErr(w, r, errCode(500, "Kubernetes deletion failed: %s", err.Error()))
		return
	}
	_ = os.RemoveAll(filepath.Join(a.cfg.DataDir, "backups", s.ID))
	if _, err := a.db.Exec(`DELETE FROM servers WHERE id=?`, s.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	a.dropHub(s.ID)
	a.diskMu.Lock()
	delete(a.disk, s.ID)
	a.diskMu.Unlock()
	a.db.LogActivity("", u, clientIP(r), "admin.server.delete", s.Name+" ("+s.ID+")")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- Admin: users

func (a *App) handleAdminUsers(w http.ResponseWriter, r *http.Request, u *User) {
	users, err := a.db.Users()
	if err != nil {
		writeErr(w, r, err)
		return
	}
	counts := map[int64]int{}
	if servers, err := a.db.AllServers(); err == nil {
		for _, s := range servers {
			counts[s.OwnerID]++
		}
	}
	type uv struct {
		*User
		Servers int `json:"servers"`
	}
	out := []uv{}
	for _, x := range users {
		out = append(out, uv{x, counts[x.ID]})
	}
	writeJSON(w, 200, out)
}

func (a *App) handleAdminUserSave(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct {
		Username, Email, Password string
		IsAdmin                   bool `json:"isAdmin"`
		Reset2FA                  bool `json:"reset2fa"` // for a user who lost their device and recovery codes
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if uidStr := r.PathValue("uid"); uidStr != "" {
		uid, _ := strconv.ParseInt(uidStr, 10, 64)
		target, err := a.db.UserByID(uid)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		if target.ID == u.ID && !in.IsAdmin {
			writeErr(w, r, errCode(400, "You can't remove your own admin role"))
			return
		}
		if !usernameRe.MatchString(in.Username) || !strings.Contains(in.Email, "@") {
			writeErr(w, r, errCode(400, "Invalid username or email"))
			return
		}
		if _, err := a.db.Exec(`UPDATE users SET username=?, email=?, is_admin=? WHERE id=?`, in.Username, strings.ToLower(in.Email), in.IsAdmin, uid); err != nil {
			writeErr(w, r, errCode(400, "Username or email already in use"))
			return
		}
		if in.Password != "" {
			if err := a.db.SetPassword(uid, in.Password); err != nil {
				writeErr(w, r, err)
				return
			}
			_, _ = a.db.Exec(`DELETE FROM sessions WHERE user_id=?`, uid)
		}
		if in.Reset2FA && target.TwoFactor {
			if err := a.db.DisableTOTP(uid); err != nil {
				writeErr(w, r, err)
				return
			}
			a.db.LogActivity("", u, clientIP(r), "admin.user.2fa.reset", in.Username)
		}
		a.db.LogActivity("", u, clientIP(r), "admin.user.update", in.Username)
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	nu, err := a.db.CreateUser(in.Username, in.Email, in.Password, in.IsAdmin)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "admin.user.create", nu.Username)
	writeJSON(w, 200, nu)
}

func (a *App) handleAdminUserDelete(w http.ResponseWriter, r *http.Request, u *User) {
	uid, _ := strconv.ParseInt(r.PathValue("uid"), 10, 64)
	if uid == u.ID {
		writeErr(w, r, errCode(400, "You can't delete your own account"))
		return
	}
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE owner_id=?`, uid).Scan(&n)
	if n > 0 {
		writeErr(w, r, errCode(400, "This user owns %d server(s): transfer or delete them first", n))
		return
	}
	if _, err := a.db.Exec(`DELETE FROM users WHERE id=?`, uid); err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "admin.user.delete", fmt.Sprintf("#%d", uid))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- Admin: templates

func (a *App) handleAdminTemplates(w http.ResponseWriter, r *http.Request, u *User) {
	list, err := a.db.Templates()
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, list)
}

func (a *App) handleAdminTemplateSave(w http.ResponseWriter, r *http.Request, u *User) {
	var t Template
	if err := readJSON(r, &t); err != nil {
		writeErr(w, r, err)
		return
	}
	t.ID = 0
	if tid := r.PathValue("tid"); tid != "" {
		t.ID, _ = strconv.ParseInt(tid, 10, 64)
		if _, err := a.db.Template(t.ID); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	if err := a.db.SaveTemplate(&t); err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "admin.template.save", t.Name)
	writeJSON(w, 200, t)
}

func (a *App) handleAdminTemplateDelete(w http.ResponseWriter, r *http.Request, u *User) {
	tid, _ := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE template_id=?`, tid).Scan(&n)
	if n > 0 {
		writeErr(w, r, errCode(400, "%d server(s) use this template", n))
		return
	}
	_, _ = a.db.Exec(`DELETE FROM templates WHERE id=?`, tid)
	a.db.LogActivity("", u, clientIP(r), "admin.template.delete", fmt.Sprintf("#%d", tid))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- Admin: settings / overview / activity

func (a *App) handleAdminSettings(w http.ResponseWriter, r *http.Request, u *User) {
	writeJSON(w, 200, a.db.Settings())
}

func (a *App) handleAdminSettingsSave(w http.ResponseWriter, r *http.Request, u *User) {
	var s Settings
	if err := readJSON(r, &s); err != nil {
		writeErr(w, r, err)
		return
	}
	s.PanelName = strings.TrimSpace(s.PanelName)
	if s.PanelName == "" || len(s.PanelName) > 40 {
		writeErr(w, r, errCode(400, "Panel name required (40 characters max)"))
		return
	}
	if s.PortMin < 30000 || s.PortMax > 32767 || s.PortMin > s.PortMax {
		writeErr(w, r, errCode(400, "The port range must be within 30000-32767 (Kubernetes NodePorts)"))
		return
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		writeErr(w, r, errCode(400, "Invalid time zone"))
		return
	}
	s.PublicHost = strings.TrimSpace(s.PublicHost)
	switch s.TwoFactorPolicy {
	case twoFactorOptional:
	case twoFactorAdmins, twoFactorEveryone:
		if !u.TwoFactor { // never let an admin lock themselves out
			writeErr(w, r, errCode(400, "Enable two-factor authentication on your own account first"))
			return
		}
	default:
		writeErr(w, r, errCode(400, "Invalid two-factor authentication policy"))
		return
	}
	if err := a.db.SaveSettings(s); err != nil {
		writeErr(w, r, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "admin.settings.update", "")
	writeJSON(w, 200, s)
}

func (a *App) handleAdminOverview(w http.ResponseWriter, r *http.Request, u *User) {
	nodes, err := a.kube.Nodes(r.Context())
	if err != nil {
		nodes = []NodeInfo{}
	}
	servers, _ := a.db.AllServers()
	users, _ := a.db.Users()
	counts := map[string]int{}
	var memAlloc, cpuAlloc, diskAlloc, diskUsed int64
	for _, s := range servers {
		counts[a.kube.State(s.ID).Status]++
		memAlloc += int64(s.MemoryMB)
		cpuAlloc += int64(s.CPUMillis)
		diskAlloc += int64(s.DiskMB)
		diskUsed += a.diskUsed(s.ID)
	}
	var backupBytes int64
	_ = a.db.QueryRow(`SELECT COALESCE(SUM(size),0) FROM backups`).Scan(&backupBytes)
	st := a.db.Settings()
	allocs, _ := a.db.Allocations("")
	writeJSON(w, 200, map[string]any{
		"nodes": nodes, "servers": len(servers), "users": len(users), "statuses": counts,
		"memoryAllocated": memAlloc, "cpuAllocated": cpuAlloc, "diskAllocated": diskAlloc, "diskUsed": diskUsed,
		"backupBytes": backupBytes, "portsUsed": len(allocs), "portsTotal": st.PortMax - st.PortMin + 1,
		"namespace": a.kube.ns, "version": version,
	})
}

func (a *App) handleAdminActivity(w http.ResponseWriter, r *http.Request, u *User) {
	list, err := a.db.ActivityList("", 300)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, list)
}

var _ = errors.New
