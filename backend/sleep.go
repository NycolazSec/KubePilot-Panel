package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Auto-sleep: a Minecraft server that nobody plays on stops by itself, and starts again when a player joins.
//
//   - Going to sleep is the panel's job: every 30 s it asks each running server how many players are online
//     (Server List Ping, so it also sees the players who joined through the node port), and stops the ones that
//     stayed empty for their delay. Before handing the server over, it writes the configuration files and the pod
//     spec exactly as a normal start would, so the wake-up needs nothing from the panel.
//   - Waking up is mc-router's: the Service of a sleeping server carries autoScaleUp=true, so a player joining
//     <hostname>.<domain> makes mc-router scale the StatefulSet from 0 to 1 (it may only change the replica
//     count: see the chart's RBAC). The player is told to come back in a minute, and the server list shows that
//     the server is sleeping or starting.
//
// A server stopped any other way (button, schedule, quota, suspension) is not "asleep" and stays off.

const (
	specHashAnnotation = "kubepilot.io/spec-hash"
	mcAutoScaleUp      = "mc-router.itzg.me/autoScaleUp"
	mcAutoScaleDown    = "mc-router.itzg.me/autoScaleDown"
	mcAutoScaleWait    = "mc-router.itzg.me/autoScaleWaitTimeout"

	sleepCheckEvery = 30 * time.Second
	wakeWaitTimeout = "5m" // how long mc-router shows "starting" in the server list after waking a server
	minSleepMinutes = 5
	maxSleepMinutes = 1440
)

// routerAnnotationKeys are the Service annotations the panel owns (removed, then set again, on every update).
var routerAnnotationKeys = []string{mcRouterAnnotation, mcAutoScaleUp, mcAutoScaleDown, mcAutoScaleWait}

// routerAnnotations tells mc-router how to route the server and whether it may start it. It never lets mc-router
// stop a server: mc-router only sees its own connections, not the players who join through the node port.
func routerAnnotations(sp Spec) map[string]string {
	out := map[string]string{}
	if sp.Routed == "" {
		return out
	}
	out[mcRouterAnnotation] = sp.Routed
	out[mcAutoScaleUp] = strconv.FormatBool(sp.Wakeable)
	out[mcAutoScaleDown] = "false"
	if sp.Wakeable {
		out[mcAutoScaleWait] = wakeWaitTimeout
	}
	return out
}

// hash identifies what a start writes (pod spec and configuration files): a sleeping server whose settings
// changed since it fell asleep is prepared again, so that mc-router never starts it with a stale spec.
func (sp Spec) hash() string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode(sp.podTemplate())
	_ = enc.Encode(sp.Template.ConfigFiles)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// autoSleepAvailable: the router can wake this server up (router deployed with auto-sleep, Minecraft Java).
func (a *App) autoSleepAvailable(t *Template, st Settings) bool {
	return a.cfg.AutoSleep && a.routerAvailable(t, st)
}

// wakeable: mc-router may start this server when a player joins.
func (a *App) wakeable(s *Server, routed string) bool {
	return a.cfg.AutoSleep && routed != "" && s.Asleep && s.AutoSleep > 0 && !s.Suspended && !overQuota(a.diskUsed(s.ID), s.DiskMB)
}

// ---------- Sleeping servers, in memory for the status shown every second

func (a *App) isAsleep(id string) bool {
	a.sleepMu.RLock()
	defer a.sleepMu.RUnlock()
	return a.asleep[id]
}

func (a *App) setAsleep(id string, asleep bool) error {
	if err := a.db.SetAsleep(id, asleep); err != nil {
		return err
	}
	a.sleepMu.Lock()
	defer a.sleepMu.Unlock()
	if asleep {
		a.asleep[id] = true
	} else {
		delete(a.asleep, id)
	}
	return nil
}

func (a *App) loadAsleep(servers []*Server) {
	a.sleepMu.Lock()
	defer a.sleepMu.Unlock()
	for _, s := range servers {
		if s.Asleep {
			a.asleep[s.ID] = true
		}
	}
}

// state is the server's state as users see it: a stopped server that auto-sleep put to sleep is "sleeping".
func (a *App) state(id string) State {
	return sleepingState(a.kube.State(id), a.isAsleep(id))
}

func sleepingState(st State, asleep bool) State {
	if asleep && st.Status == "offline" {
		st.Status = "sleeping"
	}
	return st
}

// clearAsleep: the server is no longer waiting for a player (started or stopped by hand, suspended, auto-sleep
// turned off…). mc-router loses the right to start it.
func (a *App) clearAsleep(ctx context.Context, s *Server) {
	if !s.Asleep && !a.isAsleep(s.ID) {
		return
	}
	if err := a.setAsleep(s.ID, false); err != nil {
		log.Printf("auto-sleep: %s: %v", s.ID, err)
	}
	s.Asleep = false
	a.syncService(ctx, s)
}

// syncService writes the server's Service again (router annotations), when it has ports.
func (a *App) syncService(ctx context.Context, s *Server) {
	sp, err := a.spec(s)
	if err != nil || len(sp.Allocs) == 0 {
		return
	}
	if err := a.kube.EnsureService(ctx, sp); err != nil {
		log.Printf("auto-sleep: service of %s: %v", s.ID, err)
	}
}

// ---------- Idle detection

// idleTracker remembers since when each running server has had no players.
type idleTracker struct {
	mu    sync.Mutex
	since map[string]time.Time
}

// observe records a player count (ok=false: the server didn't answer) and reports whether the server has now
// been empty for at least `after`. An unanswered ping restarts the count: a server is only put to sleep when
// it said, for the whole delay, that nobody was online.
func (t *idleTracker) observe(id string, players int, ok bool, now time.Time, after time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !ok || players > 0 {
		delete(t.since, id)
		return false
	}
	first, seen := t.since[id]
	if !seen {
		t.since[id] = now
		return false
	}
	return now.Sub(first) >= after
}

func (t *idleTracker) forget(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.since, id)
}

// emptyFor: how long the server has been seen empty (0 if it isn't).
func (t *idleTracker) emptyFor(id string, now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if first, ok := t.since[id]; ok {
		return now.Sub(first)
	}
	return 0
}

func (a *App) sleepLoop(ctx context.Context) {
	if !a.cfg.AutoSleep {
		return
	}
	t := time.NewTicker(sleepCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.checkSleep(ctx)
	}
}

func (a *App) checkSleep(ctx context.Context) {
	servers, err := a.db.AllServers()
	if err != nil {
		return
	}
	st := a.db.Settings()
	for _, s := range servers {
		if s.Asleep {
			a.idle.forget(s.ID)
			a.checkAsleep(ctx, s)
			continue
		}
		t, err := a.db.Template(s.TemplateID)
		if s.AutoSleep <= 0 || s.Suspended || err != nil || a.routedHost(s, t, st) == "" || !a.autoSleepAvailable(t, st) ||
			a.kube.State(s.ID).Status != "running" {
			a.idle.forget(s.ID)
			continue
		}
		players, ok := a.playersOnline(ctx, s)
		if a.idle.observe(s.ID, players, ok, time.Now(), time.Duration(s.AutoSleep)*time.Minute) {
			a.idle.forget(s.ID)
			a.goToSleep(ctx, s)
		}
	}
}

func (a *App) playersOnline(ctx context.Context, s *Server) (int, bool) {
	ip := a.kube.PodIP(s.ID)
	if ip == "" {
		return 0, false
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := javaPing(pctx, ip, a.gamePort(s, 25565))
	if err != nil {
		return 0, false
	}
	return st.PlayersOnline, true
}

// goToSleep stops an empty server, then prepares it for mc-router once its pod is gone.
func (a *App) goToSleep(ctx context.Context, s *Server) {
	lock := a.serverLock(s.ID)
	if !lock.TryLock() {
		return // a power action is running: try again at the next check
	}
	h := a.hub(s.ID)
	h.daemon("Nobody played for %s minutes: the server goes to sleep. It will start again when a player joins.", strconv.Itoa(s.AutoSleep))
	if err := a.kube.SetReplicas(ctx, s.ID, 0); err != nil {
		lock.Unlock()
		h.daemonErr("Auto-sleep failed: %s", err.Error())
		return
	}
	if err := a.setAsleep(s.ID, true); err != nil {
		log.Printf("auto-sleep: %s: %v", s.ID, err)
	}
	a.db.LogActivity(s.ID, nil, "", "server.sleep", strconv.Itoa(s.AutoSleep)+" min")
	go func() {
		defer lock.Unlock()
		grace := 60 * time.Second
		if t, err := a.db.Template(s.TemplateID); err == nil {
			grace = time.Duration(t.StopGrace+20) * time.Second
		}
		if !a.waitPodGone(ctx, s.ID, grace) {
			_ = a.kube.KillPod(ctx, s.ID)
			a.waitPodGone(ctx, s.ID, 20*time.Second)
		}
		if fresh, err := a.db.Server(s.ID); err == nil && fresh.Asleep {
			a.prepareWake(ctx, fresh)
		}
	}()
}

// prepareWake writes what a start would write (configuration files, pod spec with 0 replicas), then lets
// mc-router start the server.
func (a *App) prepareWake(ctx context.Context, s *Server) {
	sp, err := a.spec(s)
	if err != nil {
		return
	}
	a.applyServerConfig(ctx, sp)
	if err := a.kube.ApplyStatefulSet(ctx, sp, 0); err != nil {
		log.Printf("auto-sleep: statefulset of %s: %v", s.ID, err)
		return
	}
	a.syncService(ctx, s)
}

// checkAsleep follows a sleeping server: woken up by mc-router, no longer allowed to wake up, or changed since
// it fell asleep.
func (a *App) checkAsleep(ctx context.Context, s *Server) {
	lock := a.serverLock(s.ID)
	if !lock.TryLock() {
		return // going to sleep right now, or a power action is running
	}
	defer lock.Unlock()
	if a.kube.Replicas(s.ID) > 0 { // mc-router started it: a player is joining
		if err := a.setAsleep(s.ID, false); err != nil {
			log.Printf("auto-sleep: %s: %v", s.ID, err)
			return
		}
		s.Asleep = false
		a.hub(s.ID).daemon("A player is joining: the server wakes up…")
		a.db.LogActivity(s.ID, nil, "", "server.wake", "")
		a.syncService(ctx, s)
		go a.refreshDisk(a.ctx, s.ID)
		return
	}
	sp, err := a.spec(s)
	if err != nil {
		return
	}
	if !sp.Wakeable { // suspended, over its disk quota, auto-sleep or the router turned off: it stays off
		a.clearAsleep(ctx, s)
		return
	}
	if a.kube.SpecHash(s.ID) != sp.hash() {
		a.prepareWake(ctx, s)
	}
}

// PUT /api/servers/{id}/auto-sleep {minutes} (0 = off)
func (a *App) handleAutoSleep(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct {
		Minutes int `json:"minutes"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if in.Minutes != 0 && (in.Minutes < minSleepMinutes || in.Minutes > maxSleepMinutes) {
		writeErr(w, r, errCode(400, "The delay must be between %s and %s minutes (0 turns auto-sleep off)", strconv.Itoa(minSleepMinutes), strconv.Itoa(maxSleepMinutes)))
		return
	}
	if in.Minutes > 0 {
		t, err := a.db.Template(c.Server.TemplateID)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		st := a.db.Settings()
		if !a.autoSleepAvailable(t, st) || a.routedHost(c.Server, t, st) == "" {
			writeErr(w, r, errCode(400, "Auto-sleep needs the Minecraft router and an address for this server (Network tab)"))
			return
		}
	}
	if _, err := a.db.Exec(`UPDATE servers SET auto_sleep=? WHERE id=?`, in.Minutes, c.Server.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	c.Server.AutoSleep = in.Minutes
	a.idle.forget(c.Server.ID)
	if in.Minutes == 0 {
		a.clearAsleep(r.Context(), c.Server)
	}
	c.log(a, "server.autosleep", strconv.Itoa(in.Minutes))
	writeJSON(w, 200, map[string]int{"autoSleep": in.Minutes})
}
