package main

import (
	"strings"
	"testing"
)

func TestSoftwareFor(t *testing.T) {
	cases := map[string]string{"PAPER": "plugin:plugins:paper", "purpur": "plugin:plugins:purpur", "FABRIC": "mod:mods:fabric", "NEOFORGE": "mod:mods:neoforge"}
	for typ, want := range cases {
		s, ok := softwareFor(typ)
		if got := s.Kind + ":" + s.Folder + ":" + s.Loaders[0]; !ok || got != want {
			t.Errorf("%s → %s, want %s", typ, got, want)
		}
	}
	if _, ok := softwareFor("VANILLA"); ok {
		t.Error("vanilla has no plugins")
	}
}

func TestPluginFileNames(t *testing.T) {
	for _, ok := range []string{"LuckPerms-Bukkit-5.4.jar", "worldedit 7.3 (1).jar", "EssentialsX.jar.disabled"} {
		if _, valid := pluginFile(ok); !valid {
			t.Errorf("%q must be accepted", ok)
		}
	}
	for _, bad := range []string{"../server.properties", "plugins/x.jar", "x.sh", ".jar", "evil.jar/../../a.jar", strings.Repeat("a", 130) + ".jar"} {
		if _, valid := pluginFile(bad); valid {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestRasterOnlyIcons(t *testing.T) {
	if rasterType([]byte("\x89PNG\r\n\x1a\nxxxx")) != "image/png" || rasterType([]byte("RIFF\x00\x00\x00\x00WEBPVP8 ")) != "image/webp" {
		t.Error("raster images must be recognized")
	}
	if rasterType([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)) != "" {
		t.Error("SVG (scripts) must never be served from the panel's origin")
	}
}
