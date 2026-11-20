package main

import (
	"context"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// Minecraft router: when the chart deploys mc-router (minecraftRouter.enabled), every Java server can get
// <hostname>.<minecraftDomain> on the standard port 25565. Minecraft clients send the hostname they typed in
// their handshake, and mc-router forwards them to the right server — found through an annotation that the
// panel puts on the server's Service. Players don't have to type a port any more.

const mcRouterAnnotation = "mc-router.itzg.me/externalServerName"

var (
	hostLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	domainRe    = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// routerAvailable: this server's template can be routed (router deployed, domain set, Minecraft Java).
func (a *App) routerAvailable(t *Template, st Settings) bool {
	return a.cfg.MinecraftRouter && st.MinecraftDomain != "" && slices.Contains(templateFeatures(t), featureMinecraftJava)
}

// routedHost is the full address of a routed server, "" when it isn't routed.
func (a *App) routedHost(s *Server, t *Template, st Settings) string {
	if s.Hostname == "" || !a.routerAvailable(t, st) {
		return ""
	}
	return s.Hostname + "." + st.MinecraftDomain
}

// freeHostname picks a default hostname from the server's name, or its ID if taken.
func (a *App) freeHostname(name, id string) string {
	label := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(label) > 32 {
		label = strings.Trim(label[:32], "-")
	}
	if !hostLabelRe.MatchString(label) || a.hostnameTaken(label, id) {
		return id
	}
	return label
}

func (a *App) hostnameTaken(label, id string) bool {
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE hostname=? AND id<>?`, label, id).Scan(&n)
	return n > 0
}

// PUT /api/servers/{id}/hostname {hostname}
func (a *App) handleHostname(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ Hostname string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	t, err := a.db.Template(c.Server.TemplateID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	st := a.db.Settings()
	if !a.routerAvailable(t, st) {
		writeErr(w, r, errCode(400, "Custom addresses need the Minecraft router and a Minecraft domain in the admin settings"))
		return
	}
	label := strings.ToLower(strings.TrimSpace(in.Hostname))
	if label != "" && !hostLabelRe.MatchString(label) {
		writeErr(w, r, errCode(400, "Invalid address: lowercase letters, digits and dashes, 32 characters max"))
		return
	}
	if label != "" && a.hostnameTaken(label, c.Server.ID) {
		writeErr(w, r, errCode(409, "This address is already used by another server"))
		return
	}
	if _, err := a.db.Exec(`UPDATE servers SET hostname=? WHERE id=?`, label, c.Server.ID); err != nil {
		writeErr(w, r, errCode(409, "This address is already used by another server"))
		return
	}
	c.Server.Hostname = label
	sp, err := a.spec(c.Server)
	if err == nil {
		err = a.kube.EnsureService(r.Context(), sp)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	c.log(a, "server.hostname", sp.Routed)
	writeJSON(w, 200, map[string]string{"address": sp.Routed})
}

// assignMissingHostnames gives an address to the Minecraft servers that have none (servers created before
// the router or the domain existed). Called when the Minecraft domain is set.
func (a *App) assignMissingHostnames() {
	servers, err := a.db.AllServers()
	if err != nil {
		return
	}
	st := a.db.Settings()
	for _, s := range servers {
		if s.Hostname != "" {
			continue
		}
		if t, err := a.db.Template(s.TemplateID); err == nil && a.routerAvailable(t, st) {
			_, _ = a.db.Exec(`UPDATE servers SET hostname=? WHERE id=? AND hostname=''`, a.freeHostname(s.Name, s.ID), s.ID)
		}
	}
}

// syncRoutes re-applies every server's Service, so router annotations follow the settings (domain changed,
// router enabled or disabled).
func (a *App) syncRoutes(ctx context.Context) {
	servers, err := a.db.AllServers()
	if err != nil {
		return
	}
	for _, s := range servers {
		allocs, _ := a.db.Allocations(s.ID)
		if len(allocs) == 0 {
			continue
		}
		if sp, err := a.spec(s); err == nil {
			if err := a.kube.EnsureService(ctx, sp); err != nil {
				log.Printf("router: service of %s: %v", s.ID, err)
			}
		}
	}
}
