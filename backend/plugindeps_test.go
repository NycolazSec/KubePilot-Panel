package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeJar(t *testing.T, dir, name, file, content string) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create(file)
	_, _ = w.Write([]byte(content))
	_ = zw.Close()
	_ = f.Close()
}

func TestPluginDependencies(t *testing.T) {
	dir := t.TempDir()
	writeJar(t, dir, "worldguard.jar", "plugin.yml", "name: WorldGuard\nmain: x\nversion: 7\ndepend: [WorldEdit]\nsoftdepend: [Vault]\n")
	writeJar(t, dir, "block.jar", "plugin.yml", "name: Shop\ndepend:\n  - Vault\n  - WorldEdit\n")
	writeJar(t, dir, "single.jar", "plugin.yml", "name: Single\ndepend: ProtocolLib\n")
	writeJar(t, dir, "paper.jar", "paper-plugin.yml", "name: Modern\ndependencies:\n  server:\n    LuckPerms:\n      load: BEFORE\n    Optional:\n      required: false\n")
	writeJar(t, dir, "worldedit.jar", "plugin.yml", "name: WorldEdit\n")
	writeJar(t, dir, "evil.jar", "plugin.yml", "name: Evil\ndepend: ['../../etc', 'x; rm -rf /']\n")
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	want := map[string]string{"worldguard.jar": "WorldEdit", "block.jar": "Vault,WorldEdit", "single.jar": "ProtocolLib", "paper.jar": "LuckPerms", "worldedit.jar": "", "evil.jar": ""}
	for jar, deps := range want {
		m := readPluginMeta(root, jar)
		if m == nil {
			t.Fatalf("%s: no metadata", jar)
		}
		if got := strings.Join(m.Depends, ","); got != deps {
			t.Errorf("%s: depends %q, want %q", jar, got, deps)
		}
	}
	names := installedPluginNames(root, ".")
	if !names["worldedit"] || !names["worldguard"] || names["vault"] {
		t.Errorf("installed names: %v", names)
	}
	if normName("World Edit") != normName("worldedit") || normName("LuckPerms") == normName("LuckPermsAddon") {
		t.Error("name matching")
	}
}

func TestSearchHitDecoding(t *testing.T) {
	var res struct {
		Hits []mrHit `json:"hits"`
	}
	if err := json.Unmarshal([]byte(`{"hits":[{"project_id":"1u6JkXh5","slug":"worldedit","title":"WorldEdit"}]}`), &res); err != nil || res.Hits[0].ProjectID != "1u6JkXh5" {
		t.Fatalf("project_id must be decoded: %+v %v", res, err)
	}
}
