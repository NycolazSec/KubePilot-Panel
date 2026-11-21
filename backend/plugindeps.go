package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// Bukkit / Paper plugins declare their hard dependencies in their own jar (plugin.yml "depend",
// paper-plugin.yml "dependencies.server.<name>.required"). Modrinth often doesn't list them (e.g. WorldGuard →
// WorldEdit), so the panel reads them itself: missing ones are installed when Modrinth has an exact match,
// and reported otherwise.

var pluginNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

type pluginMeta struct {
	Name    string
	Depends []string
}

// readPluginMeta reads plugin.yml / paper-plugin.yml from a plugin jar of the server's volume.
func readPluginMeta(root Vol, p string) *pluginMeta {
	f, st, err := openRegular(root, p)
	if err != nil {
		return nil
	}
	defer f.Close()
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return nil
	}
	meta := &pluginMeta{}
	found := false
	for _, zf := range zr.File {
		if zf.Name != "plugin.yml" && zf.Name != "paper-plugin.yml" {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(rc, 64<<10))
		rc.Close()
		if err != nil {
			continue
		}
		var y struct {
			Name         string          `json:"name"`
			Depend       json.RawMessage `json:"depend"`
			Dependencies struct {
				Server map[string]struct {
					Required *bool `json:"required"`
				} `json:"server"`
			} `json:"dependencies"`
		}
		if yaml.Unmarshal(b, &y) != nil {
			continue
		}
		found = true
		if pluginNameRe.MatchString(y.Name) {
			meta.Name = y.Name
		}
		var list []string
		if json.Unmarshal(y.Depend, &list) != nil {
			var one string
			if json.Unmarshal(y.Depend, &one) == nil && one != "" {
				list = []string{one}
			}
		}
		for name, d := range y.Dependencies.Server {
			if d.Required == nil || *d.Required { // paper-plugin.yml: required unless said otherwise
				list = append(list, name)
			}
		}
		for _, d := range list {
			if pluginNameRe.MatchString(d) && !strings.EqualFold(d, meta.Name) && !containsFold(meta.Depends, d) {
				meta.Depends = append(meta.Depends, d)
			}
		}
	}
	if !found {
		return nil
	}
	return meta
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// installedPluginNames returns the plugin names declared by the enabled jars of the folder.
func installedPluginNames(root Vol, folder string) map[string]bool {
	names := map[string]bool{}
	f, err := root.Open(folder)
	if err != nil {
		return names
	}
	ents, _ := f.ReadDir(-1)
	f.Close()
	for i, e := range ents {
		if i >= maxPluginsShown || e.IsDir() || !strings.HasSuffix(e.Name(), ".jar") {
			continue
		}
		if m := readPluginMeta(root, path.Join(folder, e.Name())); m != nil && m.Name != "" {
			names[strings.ToLower(m.Name)] = true
		}
	}
	return names
}

func normName(s string) string {
	return strings.NewReplacer(" ", "", "-", "", "_", "", ".", "").Replace(strings.ToLower(s))
}

type mrHit struct {
	ProjectID string `json:"project_id"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
}

// findProjectByName looks a plugin name up on Modrinth and only accepts an exact title or slug match.
func findProjectByName(ctx context.Context, name string, pc *pluginCtx) string {
	facets := [][]string{{"project_type:plugin"}}
	cats := []string{}
	for _, l := range pc.soft.Loaders {
		cats = append(cats, "categories:"+l)
	}
	facets = append(facets, cats)
	var res struct {
		Hits []mrHit `json:"hits"`
	}
	if mrGet(ctx, modrinthAPI+"/search?limit=10&query="+url.QueryEscape(name)+"&facets="+jsonParam(facets), &res) != nil {
		return ""
	}
	for _, h := range res.Hits {
		if normName(h.Title) == normName(name) || normName(h.Slug) == normName(name) {
			return h.ProjectID
		}
	}
	return ""
}
