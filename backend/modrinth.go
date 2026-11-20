package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Plugins & mods for Minecraft Java servers, from Modrinth (https://docs.modrinth.com/api/): search filtered by
// the server's software and Minecraft version, installed jars recognized by their SHA-1, updates, one-click
// install with required dependencies. Files are only downloaded from Modrinth's CDN and checked against the
// SHA-512 Modrinth publishes; they land in the server's own volume, within its disk quota.

const (
	modrinthAPI     = "https://api.modrinth.com/v2"
	maxPluginSize   = 100 << 20
	maxPluginsShown = 300
)

var (
	modrinthHTTP = &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	mrIDRe       = regexp.MustCompile(`^[A-Za-z0-9]{8}$`)
	jarNameRe    = regexp.MustCompile(`^[A-Za-z0-9 ._+()\[\]-]{1,120}\.jar$`)
	mcVersionRe  = regexp.MustCompile(`\b(\d+\.\d+(?:\.\d+)?)\b`)
)

// mrSoftware describes where extensions go for a server type, and which Modrinth loaders fit it.
type mrSoftware struct {
	Kind    string   `json:"kind"` // "plugin" or "mod"
	Folder  string   `json:"folder"`
	Loaders []string `json:"loaders"`
}

func softwareFor(typ string) (mrSoftware, bool) {
	switch strings.ToUpper(typ) {
	case "PAPER":
		return mrSoftware{"plugin", "plugins", []string{"paper", "spigot", "bukkit"}}, true
	case "PURPUR":
		return mrSoftware{"plugin", "plugins", []string{"purpur", "paper", "spigot", "bukkit"}}, true
	case "FOLIA":
		return mrSoftware{"plugin", "plugins", []string{"folia"}}, true
	case "SPIGOT", "BUKKIT":
		return mrSoftware{"plugin", "plugins", []string{"spigot", "bukkit"}}, true
	case "FABRIC":
		return mrSoftware{"mod", "mods", []string{"fabric"}}, true
	case "QUILT":
		return mrSoftware{"mod", "mods", []string{"quilt", "fabric"}}, true
	case "FORGE":
		return mrSoftware{"mod", "mods", []string{"forge"}}, true
	case "NEOFORGE":
		return mrSoftware{"mod", "mods", []string{"neoforge"}}, true
	}
	return mrSoftware{}, false
}

func mrGet(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	return mrDo(req, out)
}

func mrPost(ctx context.Context, u string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return mrDo(req, out)
}

func mrDo(req *http.Request, out any) error {
	req.Header.Set("User-Agent", "NycolazSec/KubePilot-Panel/"+version+" (github.com/NycolazSec/KubePilot-Panel)")
	res, err := modrinthHTTP.Do(req)
	if err != nil {
		return errCode(502, "Modrinth can't be reached right now")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return errCode(502, "Modrinth can't be reached right now")
	}
	return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out)
}

type mrFile struct {
	URL      string            `json:"url"`
	Filename string            `json:"filename"`
	Primary  bool              `json:"primary"`
	Size     int64             `json:"size"`
	Hashes   map[string]string `json:"hashes"`
}

type mrVersion struct {
	ID            string   `json:"id"`
	ProjectID     string   `json:"project_id"`
	VersionNumber string   `json:"version_number"`
	VersionType   string   `json:"version_type"`
	GameVersions  []string `json:"game_versions"`
	Loaders       []string `json:"loaders"`
	Files         []mrFile `json:"files"`
	Dependencies  []struct {
		ProjectID      string `json:"project_id"`
		DependencyType string `json:"dependency_type"`
	} `json:"dependencies"`
}

func (v *mrVersion) primary() *mrFile {
	for i := range v.Files {
		if v.Files[i].Primary {
			return &v.Files[i]
		}
	}
	if len(v.Files) > 0 {
		return &v.Files[0]
	}
	return nil
}

type mrProject struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
}

// ---------- Server context

type pluginCtx struct {
	soft    mrSoftware
	version string // Minecraft version, "" if unknown
}

// pluginContext finds the server's software and Minecraft version (variable, live status, Paper's
// version_history.json, else Modrinth's latest release for "LATEST").
func (a *App) pluginContext(ctx context.Context, s *Server) (*pluginCtx, error) {
	t, err := a.db.Template(s.TemplateID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(templateFeatures(t), featureMinecraftJava) {
		return nil, errCode(404, "This is not a Minecraft server")
	}
	vars := EffectiveVars(s, t)
	soft, ok := softwareFor(vars["TYPE"])
	if !ok {
		return nil, errCode(400, "This server type doesn't support plugins or mods (choose Paper, Purpur, Fabric, Forge… in Startup)")
	}
	pc := &pluginCtx{soft: soft}
	if v := vars["VERSION"]; v != "" && mcVersionRe.MatchString(v) && !strings.EqualFold(v, "LATEST") {
		pc.version = mcVersionRe.FindString(v)
		return pc, nil
	}
	if ip := a.kube.PodIP(s.ID); ip != "" {
		pctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		st, err := javaPing(pctx, ip, a.gamePort(s, 25565))
		cancel()
		if err == nil {
			if m := mcVersionRe.FindAllString(st.Version, -1); len(m) > 0 {
				pc.version = m[len(m)-1]
				return pc, nil
			}
		}
	}
	if root, _, err := a.openRoot(ctx, s); err == nil {
		b, err := readSmall(root, "version_history.json", 64<<10)
		root.Close()
		if err == nil {
			if m := regexp.MustCompile(`MC: ?(\d+\.\d+(?:\.\d+)?)`).FindSubmatch(b); m != nil {
				pc.version = string(m[1])
				return pc, nil
			}
		}
	}
	var tags []struct{ Version, VersionType string } // "LATEST" before the first start
	if err := mrGet(ctx, modrinthAPI+"/tag/game_version", &tags); err == nil {
		for _, tg := range tags {
			if tg.VersionType == "release" {
				pc.version = tg.Version
				break
			}
		}
	}
	return pc, nil
}

func jsonParam(v any) string { b, _ := json.Marshal(v); return url.QueryEscape(string(b)) }

// ---------- Installed extensions

type installedPlugin struct {
	MissingDeps []string   `json:"missingDeps,omitempty"` // declared by the jar, provided by no installed plugin
	File        string     `json:"file"`
	Size        int64      `json:"size"`
	Disabled    bool       `json:"disabled"`
	Project     *mrProject `json:"project,omitempty"`
	Version     string     `json:"version,omitempty"`
	Update      string     `json:"update,omitempty"` // newer compatible version, if any
}

func sha1File(root *os.Root, name string) (string, error) {
	f, _, err := openRegular(root, name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxPluginSize)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// GET /api/servers/{id}/plugins
func (a *App) handlePlugins(w http.ResponseWriter, r *http.Request, c *Ctx) {
	pc, err := a.pluginContext(r.Context(), c.Server)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := []*installedPlugin{}
	byHash := map[string]*installedPlugin{}
	var names map[string]bool
	root, _, err := a.openRoot(r.Context(), c.Server)
	if err == nil {
		if f, err := root.Open(pc.soft.Folder); err == nil {
			ents, _ := f.ReadDir(-1)
			f.Close()
			for _, e := range ents {
				name := e.Name()
				if e.IsDir() || !(strings.HasSuffix(name, ".jar") || strings.HasSuffix(name, ".jar.disabled")) || len(out) >= maxPluginsShown {
					continue
				}
				info, err := e.Info()
				if err != nil || !info.Mode().IsRegular() {
					continue
				}
				p := &installedPlugin{File: name, Size: info.Size(), Disabled: strings.HasSuffix(name, ".disabled")}
				out = append(out, p)
				if pc.soft.Kind == "plugin" && !p.Disabled {
					if names == nil {
						names = installedPluginNames(root, pc.soft.Folder)
					}
					if m := readPluginMeta(root, path.Join(pc.soft.Folder, name)); m != nil {
						for _, d := range m.Depends {
							if !names[strings.ToLower(d)] {
								p.MissingDeps = append(p.MissingDeps, d)
							}
						}
					}
				}
				if h, err := sha1File(root, path.Join(pc.soft.Folder, name)); err == nil {
					byHash[h] = p
				}
			}
		}
		root.Close()
	}
	if len(byHash) > 0 {
		hashes := make([]string, 0, len(byHash))
		for h := range byHash {
			hashes = append(hashes, h)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		var known map[string]mrVersion
		if mrPost(ctx, modrinthAPI+"/version_files", map[string]any{"hashes": hashes, "algorithm": "sha1"}, &known) == nil && len(known) > 0 {
			ids := []string{}
			for h, v := range known {
				if p := byHash[h]; p != nil {
					p.Version = truncate(v.VersionNumber, 40)
					ids = append(ids, v.ProjectID)
				}
			}
			var projects []mrProject
			if mrGet(ctx, modrinthAPI+"/projects?ids="+jsonParam(ids), &projects) == nil {
				byID := map[string]*mrProject{}
				for i := range projects {
					byID[projects[i].ID] = &projects[i]
				}
				for h, v := range known {
					if p := byHash[h]; p != nil {
						p.Project = byID[v.ProjectID]
					}
				}
			}
			upd := map[string]any{"hashes": hashes, "algorithm": "sha1", "loaders": pc.soft.Loaders}
			if pc.version != "" {
				upd["game_versions"] = []string{pc.version}
			}
			var latest map[string]mrVersion
			if mrPost(ctx, modrinthAPI+"/version_files/update", upd, &latest) == nil {
				for h, v := range latest {
					if p, cur := byHash[h], known[h]; p != nil && cur.ID != "" && v.ID != cur.ID {
						p.Update = truncate(v.VersionNumber, 40)
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].File) < strings.ToLower(out[j].File) })
	writeJSON(w, 200, map[string]any{"kind": pc.soft.Kind, "folder": pc.soft.Folder, "loaders": pc.soft.Loaders, "gameVersion": pc.version, "installed": out})
}

// GET /api/servers/{id}/plugins/search?q=…&offset=…
func (a *App) handlePluginSearch(w http.ResponseWriter, r *http.Request, c *Ctx) {
	pc, err := a.pluginContext(r.Context(), c.Server)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	facets := [][]string{{"project_type:" + pc.soft.Kind}}
	cats := []string{}
	for _, l := range pc.soft.Loaders {
		cats = append(cats, "categories:"+l)
	}
	facets = append(facets, cats)
	if pc.version != "" {
		facets = append(facets, []string{"versions:" + pc.version})
	}
	if pc.soft.Kind == "mod" {
		facets = append(facets, []string{"server_side:required", "server_side:optional"})
	}
	q := truncate(strings.TrimSpace(r.URL.Query().Get("q")), 100)
	offset := 0
	fmt.Sscanf(r.URL.Query().Get("offset"), "%d", &offset)
	offset = max(0, min(offset, 1000))
	u := fmt.Sprintf("%s/search?limit=20&offset=%d&index=%s&query=%s&facets=%s", modrinthAPI, offset,
		map[bool]string{true: "relevance", false: "downloads"}[q != ""], url.QueryEscape(q), jsonParam(facets))
	var res struct {
		Hits []struct {
			ProjectID   string `json:"project_id"`
			Slug        string `json:"slug"`
			Title       string `json:"title"`
			Description string `json:"description"`
			IconURL     string `json:"icon_url"`
			Downloads   int64  `json:"downloads"`
			Author      string `json:"author"`
		} `json:"hits"`
		TotalHits int `json:"total_hits"`
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := mrGet(ctx, u, &res); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, res)
}

// ---------- Install / update / toggle / delete

// latestVersion returns the newest version of a project that fits the server.
func latestVersion(ctx context.Context, projectID string, pc *pluginCtx) (*mrVersion, error) {
	u := fmt.Sprintf("%s/project/%s/version?loaders=%s", modrinthAPI, projectID, jsonParam(pc.soft.Loaders))
	if pc.version != "" {
		u += "&game_versions=" + jsonParam([]string{pc.version})
	}
	var versions []mrVersion
	if err := mrGet(ctx, u, &versions); err != nil {
		return nil, err
	}
	for i := range versions { // newest first: prefer a release, else the newest beta / alpha
		if versions[i].VersionType == "release" {
			return &versions[i], nil
		}
	}
	if len(versions) > 0 {
		return &versions[0], nil
	}
	return nil, errCode(404, "No version of this project fits this server (software or Minecraft version)")
}

// download fetches a Modrinth file into the server's extension folder, checking its SHA-512 and the quota.
func (a *App) download(ctx context.Context, c *Ctx, root *os.Root, uid int, folder string, f *mrFile) (string, error) {
	u, err := url.Parse(f.URL)
	if err != nil || u.Scheme != "https" || u.Host != "cdn.modrinth.com" {
		return "", errCode(502, "Unexpected download address")
	}
	name := path.Base(f.Filename)
	if !jarNameRe.MatchString(name) {
		return "", errCode(400, "Unexpected file name: %s", truncate(f.Filename, 60))
	}
	want := strings.ToLower(f.Hashes["sha512"])
	if len(want) != 128 {
		return "", errCode(502, "Modrinth didn't give a checksum for this file")
	}
	if f.Size > maxPluginSize || f.Size > a.diskRemaining(c.Server) {
		return "", errQuota()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "NycolazSec/KubePilot-Panel/"+version)
	res, err := modrinthHTTP.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		if res != nil {
			res.Body.Close()
		}
		return "", errCode(502, "Modrinth can't be reached right now")
	}
	defer res.Body.Close()
	_ = root.MkdirAll(folder, 0o755)
	tmp := path.Join(folder, ".kubepilot-"+randomID(8)+".part")
	out, err := createRegular(root, tmp, 0o644)
	if err != nil {
		return "", err
	}
	h := sha512.New()
	_, err = io.Copy(io.MultiWriter(&quotaWriter{w: out, left: min(a.diskRemaining(c.Server), maxPluginSize)}, h), io.LimitReader(res.Body, maxPluginSize+1))
	out.Close()
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = errCode(502, "The downloaded file doesn't match its checksum: not installed")
	}
	if err != nil {
		_ = root.Remove(tmp)
		return "", err
	}
	if err := root.Rename(tmp, path.Join(folder, name)); err != nil {
		_ = root.Remove(tmp)
		return "", err
	}
	chownNew(root, path.Join(folder, name), uid)
	chownNew(root, folder, uid)
	return name, nil
}

var installMu sync.Map // one install at a time per server

// POST /api/servers/{id}/plugins/install {projectId}
func (a *App) handlePluginInstall(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct {
		ProjectID string `json:"projectId"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if !mrIDRe.MatchString(in.ProjectID) {
		writeErr(w, r, errCode(400, "Invalid project"))
		return
	}
	mu, _ := installMu.LoadOrStore(c.Server.ID, &sync.Mutex{})
	if !mu.(*sync.Mutex).TryLock() {
		writeErr(w, r, errCode(409, "Another install is running on this server"))
		return
	}
	defer mu.(*sync.Mutex).Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	pc, err := a.pluginContext(ctx, c.Server)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	root, uid, err := a.openRoot(ctx, c.Server)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	defer root.Close()
	installed, missing := []string{}, []string{}
	seen := map[string]bool{}
	var install func(id string, depth int) error
	install = func(id string, depth int) error {
		if seen[id] || depth > 3 {
			return nil
		}
		seen[id] = true
		v, err := latestVersion(ctx, id, pc)
		if err != nil {
			return err
		}
		f := v.primary()
		if f == nil {
			return errCode(404, "No version of this project fits this server (software or Minecraft version)")
		}
		file := path.Base(f.Filename)
		if _, err := root.Stat(path.Join(pc.soft.Folder, file)); err != nil {
			name, err := a.download(ctx, c, root, uid, pc.soft.Folder, f)
			if err != nil {
				return err
			}
			installed, file = append(installed, name), name
		}
		if pc.soft.Kind == "plugin" { // hard dependencies the jar declares itself
			if m := readPluginMeta(root, path.Join(pc.soft.Folder, file)); m != nil && len(m.Depends) > 0 {
				have := installedPluginNames(root, pc.soft.Folder)
				for _, d := range m.Depends {
					if have[strings.ToLower(d)] {
						continue
					}
					if dep := findProjectByName(ctx, d, pc); dep != "" && install(dep, depth+1) == nil {
						continue
					}
					if !containsFold(missing, d) {
						missing = append(missing, d)
					}
				}
			}
		}
		for _, d := range v.Dependencies { // required dependencies come along; their failure doesn't undo the rest
			if d.DependencyType == "required" && mrIDRe.MatchString(d.ProjectID) {
				_ = install(d.ProjectID, depth+1)
			}
		}
		return nil
	}
	if err := install(in.ProjectID, 0); err != nil {
		writeErr(w, r, err)
		return
	}
	go a.refreshDisk(a.ctx, c.Server.ID)
	c.log(a, "server.plugin.install", strings.Join(installed, ", "))
	writeJSON(w, 200, map[string]any{"installed": installed, "missing": missing})
}

// pluginFile validates a file name of the extension folder.
func pluginFile(name string) (string, bool) {
	base := strings.TrimSuffix(name, ".disabled")
	return name, name == path.Base(name) && jarNameRe.MatchString(base)
}

// POST /api/servers/{id}/plugins/update {file}
func (a *App) handlePluginUpdate(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ File string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	file, ok := pluginFile(in.File)
	if !ok {
		writeErr(w, r, errCode(400, "Invalid path"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	pc, err := a.pluginContext(ctx, c.Server)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	root, uid, err := a.openRoot(ctx, c.Server)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	defer root.Close()
	old := path.Join(pc.soft.Folder, file)
	h, err := sha1File(root, old)
	if err != nil {
		writeErr(w, r, errCode(404, "File or folder not found"))
		return
	}
	body := map[string]any{"hashes": []string{h}, "algorithm": "sha1", "loaders": pc.soft.Loaders}
	if pc.version != "" {
		body["game_versions"] = []string{pc.version}
	}
	var latest map[string]mrVersion
	if err := mrPost(ctx, modrinthAPI+"/version_files/update", body, &latest); err != nil {
		writeErr(w, r, err)
		return
	}
	v, ok := latest[h]
	f := v.primary()
	if !ok || f == nil {
		writeErr(w, r, errCode(404, "No update found for this file"))
		return
	}
	name, err := a.download(ctx, c, root, uid, pc.soft.Folder, f)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if name != file {
		_ = root.Remove(old)
	}
	go a.refreshDisk(a.ctx, c.Server.ID)
	c.log(a, "server.plugin.update", file+" → "+name)
	writeJSON(w, 200, map[string]string{"file": name})
}

// POST /api/servers/{id}/plugins/toggle {file} — .jar ⇄ .jar.disabled
// POST /api/servers/{id}/plugins/delete {file}
func (a *App) handlePluginFile(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ File string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	file, ok := pluginFile(in.File)
	if !ok {
		writeErr(w, r, errCode(400, "Invalid path"))
		return
	}
	pc, err := a.pluginContext(r.Context(), c.Server)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	a.withRoot(w, r, c, func(root *os.Root, _ int) error {
		p := path.Join(pc.soft.Folder, file)
		if strings.HasSuffix(r.URL.Path, "/delete") {
			if err := root.Remove(p); err != nil {
				return err
			}
			c.log(a, "server.plugin.delete", file)
			go a.refreshDisk(a.ctx, c.Server.ID)
			writeJSON(w, 200, map[string]bool{"ok": true})
			return nil
		}
		to := file + ".disabled"
		if strings.HasSuffix(file, ".disabled") {
			to = strings.TrimSuffix(file, ".disabled")
		}
		if err := root.Rename(p, path.Join(pc.soft.Folder, to)); err != nil {
			return err
		}
		c.log(a, "server.plugin.toggle", file+" → "+to)
		writeJSON(w, 200, map[string]string{"file": to})
		return nil
	})
}

// ---------- Icons
//
// Project icons are proxied like player heads: the browser never calls a third party and the CSP stays 'self'.
// Only raster images are served (an SVG served from our origin could run scripts).

var (
	iconMu    sync.Mutex
	iconCache = map[string]avatarEntry{}
	iconURLRe = regexp.MustCompile(`^/data/[A-Za-z0-9]{8}/[A-Za-z0-9._/-]{1,200}$`)
)

func rasterType(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("GIF8")):
		return "image/gif"
	case len(b) > 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

// GET /api/modrinth/icon?u=https://cdn.modrinth.com/data/…
func (a *App) handleModrinthIcon(w http.ResponseWriter, r *http.Request, _ *User) {
	u, err := url.Parse(r.URL.Query().Get("u"))
	if err != nil || u.Scheme != "https" || u.Host != "cdn.modrinth.com" || !iconURLRe.MatchString(u.Path) {
		writeErr(w, r, errCode(400, "Invalid address"))
		return
	}
	key := u.Path
	iconMu.Lock()
	e, ok := iconCache[key]
	iconMu.Unlock()
	if !ok || time.Now().After(e.expires) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		b, code, err := getLimited(ctx, "https://cdn.modrinth.com"+u.Path, 512<<10)
		cancel()
		if err != nil || code != http.StatusOK || rasterType(b) == "" {
			writeErr(w, r, ErrNotFound)
			return
		}
		e = avatarEntry{png: b, expires: time.Now().Add(24 * time.Hour)}
		iconMu.Lock()
		if len(iconCache) > 1000 {
			clear(iconCache)
		}
		iconCache[key] = e
		iconMu.Unlock()
	}
	w.Header().Set("Content-Type", rasterType(e.png))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(e.png)
}
