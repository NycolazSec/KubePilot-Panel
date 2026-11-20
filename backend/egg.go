package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Pterodactyl / Pelican eggs (PTDL_v1, PTDL_v2) → KubePilot templates. Thousands of games and apps are
// described as eggs by the community (github.com/pelican-eggs); the panel converts one into a template draft
// that the admin reviews before saving. Their install scripts run as root (see Template.InstallAsRoot), the
// game itself never does.

type eggVariable struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	EnvVariable  string          `json:"env_variable"`
	DefaultValue string          `json:"default_value"`
	UserViewable bool            `json:"user_viewable"`
	UserEditable bool            `json:"user_editable"`
	Rules        json.RawMessage `json:"rules"` // "required|string" (PTDL) or ["required", "string"] (PLCN_v3)
}

func (v eggVariable) rules() []string {
	var list []string
	if json.Unmarshal(v.Rules, &list) == nil {
		return list
	}
	var s string
	_ = json.Unmarshal(v.Rules, &s)
	return strings.Split(s, "|")
}

type egg struct {
	Meta struct {
		Version string `json:"version"`
	} `json:"meta"`
	Name         string          `json:"name"`
	Author       string          `json:"author"`
	Description  string          `json:"description"`
	Features     []string        `json:"features"`
	Tags         []string        `json:"tags"`
	DockerImages json.RawMessage `json:"docker_images"` // PTDL_v2: {"label": "image"} (ordered)
	Images       []string        `json:"images"`        // PTDL_v1
	Startup      string          `json:"startup"`
	StartupCmds  json.RawMessage `json:"startup_commands"` // PLCN_v3: {"label": "command"}, the first is the default
	Config       struct {
		Files string `json:"files"`
		Stop  string `json:"stop"`
	} `json:"config"`
	Scripts struct {
		Installation struct {
			Script     string `json:"script"`
			Container  string `json:"container"`
			Entrypoint string `json:"entrypoint"`
		} `json:"installation"`
	} `json:"scripts"`
	Variables []eggVariable `json:"variables"`
}

// orderedImages decodes {"label": "value", …} (images, PLCN_v3 startup commands) keeping the egg's order:
// the first one is the default.
func orderedImages(raw json.RawMessage) []ImageOption {
	out := []ImageOption{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return out
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			break
		}
		var v string
		if dec.Decode(&v) != nil {
			break
		}
		if label, ok := k.(string); ok && strings.TrimSpace(v) != "" {
			out = append(out, ImageOption{Name: truncate(label, 60), Image: strings.TrimSpace(v)})
		}
	}
	return out
}

var (
	eggPlaceholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.]+)\s*\}\}`)
	eggRegexRuleRe   = regexp.MustCompile(`regex:/(.*)/[a-zA-Z]*$`)
)

// eggStartup turns Pterodactyl's {{VAR}} placeholders into shell variables, and Wings' own ones
// ({{server.build.…}}) into the variables KubePilot sets. The image's entrypoint runs it (StartupEnv).
func eggStartup(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	s = eggPlaceholderRe.ReplaceAllStringFunc(s, func(m string) string {
		name := eggPlaceholderRe.FindStringSubmatch(m)[1]
		switch {
		case name == "server.build.default.port":
			return "${SERVER_PORT}"
		case name == "server.build.memory":
			return "${SERVER_MEMORY}"
		case name == "server.build.default.ip":
			return "${SERVER_IP}"
		case strings.HasPrefix(name, "server.build.env."):
			return "${" + strings.TrimPrefix(name, "server.build.env.") + "}"
		case strings.Contains(name, "."):
			return m // unknown Wings placeholder: left as is
		}
		return "${" + name + "}"
	})
	return s
}

// eggVar maps an egg variable and its Laravel validation rules ("required|string|in:a,b|regex:/…/").
func eggVar(v eggVariable) (TemplateVar, bool) {
	key := strings.TrimSpace(v.EnvVariable)
	if !envKeyRe.MatchString(key) {
		return TemplateVar{}, false
	}
	def := strings.ReplaceAll(v.DefaultValue, "$(", "$$(") // no Kubernetes $(VAR) expansion in egg values
	tv := TemplateVar{Key: key, Name: truncate(strings.TrimSpace(v.Name), 60), Description: truncate(strings.TrimSpace(strings.ReplaceAll(v.Description, "\r", "")), 300),
		Default: def, UserViewable: v.UserViewable, UserEditable: v.UserEditable}
	if tv.Name == "" {
		tv.Name = key
	}
	required := false
	for _, r := range v.rules() {
		r = strings.TrimSpace(r)
		switch {
		case r == "required":
			required = true
		case strings.HasPrefix(r, "in:"):
			tv.Options = strings.Split(strings.TrimPrefix(r, "in:"), ",")
		case strings.HasPrefix(r, "regex:"):
			if m := eggRegexRuleRe.FindStringSubmatch(r); m != nil {
				if _, err := regexp.Compile(m[1]); err == nil {
					tv.Pattern = m[1]
				}
			}
		case r == "boolean" && tv.Pattern == "":
			tv.Pattern = `^(0|1|true|false)$`
		case (r == "numeric" || r == "integer") && tv.Pattern == "":
			tv.Pattern = `^-?[0-9]+$`
		}
	}
	if len(tv.Options) > 0 {
		tv.Pattern = ""
		if !required && !containsFold(tv.Options, "") {
			tv.Options = append([]string{""}, tv.Options...)
		}
	} else if tv.Pattern != "" && !required {
		tv.Pattern = `^$|` + tv.Pattern // optional: empty is fine too
	}
	return tv, true
}

func slugify(s string, max int) string {
	s = strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > max {
		s = strings.Trim(s[:max], "-")
	}
	if len(s) < 2 {
		s = "egg-" + s
	}
	return s
}

// warn marks a message shown to the admin (translated like errors).
func warn(msg string) string { return msg }

// convertEgg turns an egg into a template draft, with warnings about what KubePilot doesn't do like Wings.
func convertEgg(raw []byte) (*Template, []string, error) {
	var e egg
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, nil, errCode(400, "This is not a valid egg (JSON)")
	}
	if !(strings.HasPrefix(e.Meta.Version, "PTDL_") || strings.HasPrefix(e.Meta.Version, "PLCN_")) || strings.TrimSpace(e.Name) == "" {
		return nil, nil, errCode(400, "This is not a Pterodactyl / Pelican egg (PTDL_v1 or PTDL_v2)")
	}
	if strings.TrimSpace(e.Startup) == "" { // PLCN_v3
		if cmds := orderedImages(e.StartupCmds); len(cmds) > 0 {
			e.Startup = cmds[0].Image
		}
	}
	images := orderedImages(e.DockerImages)
	for _, im := range e.Images {
		images = append(images, ImageOption{Name: path.Base(im), Image: im})
	}
	// "Java 8, Java 11 … Java 21": the newest first, it's what current game versions need
	javaRe := regexp.MustCompile(`(?i)^java\s*([0-9]+)$`)
	allJava := len(images) > 1
	for _, im := range images {
		allJava = allJava && javaRe.MatchString(strings.TrimSpace(im.Name))
	}
	if allJava {
		sort.SliceStable(images, func(i, j int) bool {
			a, _ := strconv.Atoi(javaRe.FindStringSubmatch(strings.TrimSpace(images[i].Name))[1])
			b, _ := strconv.Atoi(javaRe.FindStringSubmatch(strings.TrimSpace(images[j].Name))[1])
			return a > b
		})
	}
	if len(images) == 0 {
		return nil, nil, errCode(400, "This egg has no Docker image")
	}
	all := strings.ToLower(e.Name + " " + e.Description + " " + strings.Join(e.Tags, " "))
	isMinecraft := strings.Contains(all, "minecraft") || containsFold(e.Features, "eula") // only Minecraft eggs ask for the EULA
	inst := e.Scripts.Installation
	steam := strings.Contains(all, "steamcmd") || strings.Contains(strings.ToLower(inst.Script+inst.Container), "steamcmd")
	t := &Template{
		Slug: slugify(e.Name, 40), Name: truncate(strings.TrimSpace(e.Name), 60), Category: "Eggs",
		Description: truncate(strings.TrimSpace(strings.ReplaceAll(e.Description, "\r", "")), 300),
		Images:      images, Startup: eggStartup(e.Startup), DataPath: "/home/container", RunAsUser: 1000,
		Ports: []TemplatePort{{Name: "game", Port: 27015, Protocol: "tcp+udp"}}, StopGrace: 60, ConsoleInput: true,
		InstallImage: strings.TrimSpace(inst.Container), InstallScript: strings.ReplaceAll(inst.Script, "\r\n", "\n"),
		InstallAsRoot: true, InstallShell: "bash", InstallPath: "/mnt/server", StartupEnv: true,
		StopCommand: truncate(strings.TrimSpace(e.Config.Stop), 100), Source: truncate("pterodactyl-egg "+e.Author, 100),
		DefMemory: 2048, DefCPU: 2000, DefDisk: 10240,
	}
	if steam {
		t.DefDisk = 30720
	}
	switch path.Base(strings.TrimSpace(inst.Entrypoint)) {
	case "ash", "sh":
		t.InstallShell = path.Base(strings.TrimSpace(inst.Entrypoint))
	}
	if t.InstallImage == "" {
		t.InstallImage, t.InstallShell, t.InstallScript = "busybox:1.36", "sh", "echo 'Nothing to install.'"
	}
	if isMinecraft {
		t.Ports = []TemplatePort{{Name: "minecraft", Port: 25565, Protocol: "tcp"}}
		if strings.Contains(all, "bedrock") {
			t.Ports[0].Port, t.Ports[0].Protocol, t.Features = 19132, "udp", []string{featureMinecraftBedrock}
		} else if !strings.Contains(all, "proxy") && !strings.Contains(all, "velocity") && !strings.Contains(all, "bungee") && !strings.Contains(all, "waterfall") {
			t.Features = []string{featureMinecraftJava}
		}
	}
	seen := map[string]bool{}
	for _, v := range e.Variables {
		if tv, ok := eggVar(v); ok && !seen[tv.Key] {
			seen[tv.Key] = true
			t.Vars = append(t.Vars, tv)
		}
	}
	warnings := []string{}
	if f := strings.TrimSpace(e.Config.Files); f != "" && f != "{}" && f != "[]" {
		warnings = append(warnings, warn("This egg has Wings edit configuration files on start (server port, IP…): check them in the file manager after the first start."))
	}
	for _, f := range e.Features {
		if f == "eula" {
			warnings = append(warnings, warn("Minecraft asks to accept its EULA: set eula=true in eula.txt after the install."))
		}
	}
	if t.StopCommand == "" {
		warnings = append(warnings, warn("No stop command: the game is stopped with a signal."))
	}
	return t, warnings, t.Validate()
}

// ---------- Fetching eggs

var eggHosts = map[string]bool{"raw.githubusercontent.com": true, "gist.githubusercontent.com": true, "api.github.com": true}

// eggHTTP only talks to GitHub, redirects included.
var eggHTTP = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 || req.URL.Scheme != "https" || !eggHosts[req.URL.Host] {
		return http.ErrUseLastResponse
	}
	return nil
}}

// eggURL only accepts GitHub (raw files, blob pages converted to raw, gists): no request to arbitrary hosts.
func eggURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return "", errCode(400, "Give an https link to a GitHub egg file")
	}
	p := strings.TrimPrefix(u.Path, "/")
	// The URL is rebuilt from a fixed origin: only the path comes from the admin.
	switch u.Host {
	case "raw.githubusercontent.com":
		return "https://raw.githubusercontent.com/" + escapePath(p), nil
	case "gist.githubusercontent.com":
		return "https://gist.githubusercontent.com/" + escapePath(p), nil
	case "github.com":
		parts := strings.SplitN(p, "/", 5) // owner/repo/blob/branch/path
		if len(parts) == 5 && parts[2] == "blob" {
			return "https://raw.githubusercontent.com/" + escapePath(parts[0]+"/"+parts[1]+"/"+parts[3]+"/"+parts[4]), nil
		}
	}
	return "", errCode(400, "Give an https link to a GitHub egg file")
}

func escapePath(p string) string { return (&url.URL{Path: p}).EscapedPath() }

func fetchEgg(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "NycolazSec/KubePilot-Panel/"+version)
	res, err := eggHTTP.Do(req)
	if err != nil {
		return nil, errCode(502, "GitHub can't be reached right now")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errCode(404, "Egg not found at this address")
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(http.MaxBytesReader(nil, res.Body, 2<<20)); err != nil {
		return nil, errCode(400, "This is not a valid egg (JSON)")
	}
	return buf.Bytes(), nil
}

// POST /api/admin/templates/egg {egg: "<json>"} or {url: "https://…"} → {template, warnings} (not saved)
func (a *App) handleEggConvert(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct {
		Egg json.RawMessage `json:"egg"`
		URL string          `json:"url"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	raw := []byte(in.Egg)
	if in.URL != "" {
		eu, err := eggURL(in.URL)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if raw, err = fetchEgg(ctx, eu); err != nil {
			writeErr(w, r, err)
			return
		}
	} else if len(raw) > 0 && raw[0] == '"' { // the egg file's content as a JSON string
		var s string
		_ = json.Unmarshal(raw, &s)
		raw = []byte(s)
	}
	t, warnings, err := convertEgg(raw)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	lang := langOf(r)
	for i := range warnings {
		warnings[i] = tr(lang, warnings[i])
	}
	writeJSON(w, 200, map[string]any{"template": t, "warnings": warnings})
}

// ---------- Catalog of the community eggs (github.com/pelican-eggs)

type eggEntry struct {
	Repo  string `json:"repo"`
	Name  string `json:"name"`
	Group string `json:"group"`
	URL   string `json:"url"`
}

var eggRepos = []string{"games-steamcmd", "games-standalone", "minecraft", "voice", "chatbots", "software", "database", "monitoring", "storage", "generic"}

var eggCatalog struct {
	sync.Mutex
	list    []eggEntry
	expires time.Time
}

func humanize(s string) string {
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(s))
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// GET /api/admin/eggs/catalog
func (a *App) handleEggCatalog(w http.ResponseWriter, r *http.Request, u *User) {
	eggCatalog.Lock()
	defer eggCatalog.Unlock()
	if eggCatalog.list != nil && time.Now().Before(eggCatalog.expires) {
		writeJSON(w, 200, eggCatalog.list)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	list, complete := []eggEntry{}, true
	for _, repo := range eggRepos {
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://api.github.com/repos/pelican-eggs/%s/git/trees/main?recursive=1", repo), nil)
		req.Header.Set("User-Agent", "NycolazSec/KubePilot-Panel/"+version)
		req.Header.Set("Accept", "application/vnd.github+json")
		res, err := eggHTTP.Do(req)
		if err != nil {
			complete = false
			continue
		}
		var tree struct {
			Tree []struct{ Path, Type string } `json:"tree"`
		}
		ok := res.StatusCode == http.StatusOK && json.NewDecoder(http.MaxBytesReader(nil, res.Body, 16<<20)).Decode(&tree) == nil
		res.Body.Close()
		if !ok {
			complete = false
			continue
		}
		byDir := map[string]string{} // one egg per folder, the Pelican one rather than the "pterodactyl" copy
		for _, n := range tree.Tree {
			base := path.Base(n.Path)
			if n.Type != "blob" || !strings.HasPrefix(base, "egg-") || !strings.HasSuffix(base, ".json") {
				continue
			}
			dir := path.Dir(n.Path)
			if cur, ok := byDir[dir]; !ok || (strings.HasPrefix(path.Base(cur), "egg-pterodactyl-") && !strings.HasPrefix(base, "egg-pterodactyl-")) {
				byDir[dir] = n.Path
			}
		}
		for dir, p := range byDir {
			parts := strings.Split(dir, "/")
			group := humanize(repo)
			if len(parts) > 1 {
				group += " · " + humanize(parts[0])
			}
			raw := url.URL{Scheme: "https", Host: "raw.githubusercontent.com", Path: "/pelican-eggs/" + repo + "/main/" + p}
			list = append(list, eggEntry{Repo: repo, Name: humanize(parts[len(parts)-1]), Group: group, URL: raw.String()})
		}
	}
	if len(list) == 0 {
		writeErr(w, r, errCode(502, "GitHub can't be reached right now"))
		return
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	ttl := 6 * time.Hour
	if !complete { // GitHub rate limit or outage: try again soon
		ttl = 10 * time.Minute
	}
	eggCatalog.list, eggCatalog.expires = list, time.Now().Add(ttl)
	writeJSON(w, 200, list)
}
