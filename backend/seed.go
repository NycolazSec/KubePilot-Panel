package main

const gitPrelude = `if [ -n "$GIT_REPO" ] && [ ! -d .git ]; then
  echo "[KubePilot] Cloning $GIT_REPO…"
  rm -rf /tmp/repo
  git clone --depth 1 ${GIT_BRANCH:+-b "$GIT_BRANCH"} "$GIT_REPO" /tmp/repo
  cp -a /tmp/repo/. . && rm -rf /tmp/repo
elif [ -d .git ] && [ "$AUTO_UPDATE" = "TRUE" ]; then
  echo "[KubePilot] Updating the repository (git pull)…"
  git pull --ff-only || echo "[KubePilot] git pull failed, continuing."
fi
`

func boolOpts() []string { return []string{"TRUE", "FALSE"} }

func defaultTemplates() []*Template {
	return []*Template{
		{
			Slug: "minecraft-java", Name: "Minecraft Java", Category: "Minecraft",
			Description: "Paper, Purpur, Vanilla, Fabric, Forge, NeoForge… Automatic download and updates (itzg/minecraft-server).",
			Images: []ImageOption{
				{Name: "Recommended Java (auto)", Image: "itzg/minecraft-server:latest"},
				{Name: "Java 25", Image: "itzg/minecraft-server:java25"},
				{Name: "Java 21", Image: "itzg/minecraft-server:java21"},
				{Name: "Java 17", Image: "itzg/minecraft-server:java17"},
				{Name: "Java 8 (old versions)", Image: "itzg/minecraft-server:java8"},
			},
			DataPath: "/data",
			Vars: []TemplateVar{
				{Key: "EULA", Name: "Accept the Minecraft EULA", Description: "Required to start the server: https://aka.ms/MinecraftEULA", Default: "TRUE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "TYPE", Name: "Server type", Description: "Server software to download.", Default: "PAPER", Options: []string{"PAPER", "PURPUR", "VANILLA", "FABRIC", "QUILT", "FORGE", "NEOFORGE", "FOLIA", "SPIGOT"}, UserViewable: true, UserEditable: true},
				{Key: "VERSION", Name: "Minecraft version", Description: "LATEST or a specific version (e.g. 1.21.4).", Default: "LATEST", Pattern: `^[A-Za-z0-9._-]{1,32}$`, UserViewable: true, UserEditable: true},
				{Key: "MOTD", Name: "Message of the day (MOTD)", Description: "Text shown in the server list.", Default: "A server powered by KubePilot", UserViewable: true, UserEditable: true},
				{Key: "DIFFICULTY", Name: "Difficulty", Default: "normal", Options: []string{"peaceful", "easy", "normal", "hard"}, UserViewable: true, UserEditable: true},
				{Key: "MODE", Name: "Game mode", Default: "survival", Options: []string{"survival", "creative", "adventure", "spectator"}, UserViewable: true, UserEditable: true},
				{Key: "MAX_PLAYERS", Name: "Max players", Default: "20", Pattern: `^[0-9]{1,4}$`, UserViewable: true, UserEditable: true},
				{Key: "ONLINE_MODE", Name: "Online mode", Description: "Checks Mojang accounts (only disable it if you know why).", Default: "TRUE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "VIEW_DISTANCE", Name: "View distance", Default: "10", Pattern: `^([2-9]|[12][0-9]|3[0-2])$`, UserViewable: true, UserEditable: true},
				{Key: "OPS", Name: "Operators", Description: "Comma-separated player names.", Default: "", Pattern: `^[A-Za-z0-9_,]*$`, UserViewable: true, UserEditable: true},
				{Key: "WHITELIST", Name: "Whitelist", Description: "Allowed player names, comma-separated (empty = open to everyone).", Default: "", Pattern: `^[A-Za-z0-9_,]*$`, UserViewable: true, UserEditable: true},
				{Key: "SEED", Name: "World seed", Default: "", UserViewable: true, UserEditable: true},
				{Key: "USE_AIKAR_FLAGS", Name: "Optimized JVM flags (Aikar)", Default: "TRUE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "MEMORY", Name: "JVM memory", Description: "Computed automatically from the memory limit.", Default: "$(SERVER_JVM_MEMORY)M"},
				{Key: "STOP_SERVER_ANNOUNCE_DELAY", Name: "Stop announcement delay", Default: "0"},
			},
			Ports:     []TemplatePort{{Name: "minecraft", Port: 25565, Protocol: "tcp"}},
			StopGrace: 90, ReadyProbe: true, RunAsUser: 1000, ConsoleInput: true,
			InstallImage: "busybox:1.36", InstallScript: "echo 'Volume ready. Minecraft will be downloaded on first start.'",
			DefMemory: 4096, DefCPU: 2000, DefDisk: 10240,
		},
		{
			Slug: "minecraft-bedrock", Name: "Minecraft Bedrock", Category: "Minecraft",
			Description: "Official Bedrock server (mobile, consoles, Windows). Automatic updates.",
			Images:      []ImageOption{{Name: "Bedrock", Image: "itzg/minecraft-bedrock-server:latest"}},
			DataPath:    "/data",
			Vars: []TemplateVar{
				{Key: "EULA", Name: "Accept the Minecraft EULA", Description: "https://aka.ms/MinecraftEULA", Default: "TRUE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "VERSION", Name: "Version", Default: "LATEST", Pattern: `^[A-Za-z0-9._-]{1,32}$`, UserViewable: true, UserEditable: true},
				{Key: "SERVER_NAME", Name: "Server name", Default: "KubePilot server", UserViewable: true, UserEditable: true},
				{Key: "GAMEMODE", Name: "Game mode", Default: "survival", Options: []string{"survival", "creative", "adventure"}, UserViewable: true, UserEditable: true},
				{Key: "DIFFICULTY", Name: "Difficulty", Default: "normal", Options: []string{"peaceful", "easy", "normal", "hard"}, UserViewable: true, UserEditable: true},
				{Key: "MAX_PLAYERS", Name: "Max players", Default: "10", Pattern: `^[0-9]{1,4}$`, UserViewable: true, UserEditable: true},
				{Key: "ALLOW_CHEATS", Name: "Allow cheats", Default: "false", Options: []string{"false", "true"}, UserViewable: true, UserEditable: true},
				{Key: "ONLINE_MODE", Name: "Online mode (Xbox Live)", Default: "true", Options: []string{"true", "false"}, UserViewable: true, UserEditable: true},
				{Key: "LEVEL_NAME", Name: "World name", Default: "Bedrock level", UserViewable: true, UserEditable: true},
			},
			Ports:     []TemplatePort{{Name: "bedrock", Port: 19132, Protocol: "udp"}},
			StopGrace: 30, ReadyProbe: false, RunAsUser: 1000, ConsoleInput: true,
			InstallImage: "busybox:1.36", InstallScript: "echo 'Volume ready. Bedrock will be downloaded on first start.'",
			DefMemory: 2048, DefCPU: 1000, DefDisk: 5120,
		},
		{
			Slug: "nodejs", Name: "Node.js", Category: "Bots & applications",
			Description: "Discord bot, API, website… Clones a Git repository (optional), runs npm install, then starts your file.",
			Images: []ImageOption{
				{Name: "Node 22 LTS", Image: "node:22-bookworm"},
				{Name: "Node 24", Image: "node:24-bookworm"},
				{Name: "Node 20", Image: "node:20-bookworm"},
			},
			DataPath: "/home/container",
			Startup: gitPrelude + `if [ -f package.json ] && [ "$NPM_INSTALL" = "TRUE" ]; then
  echo "[KubePilot] npm install…"
  npm install --omit=dev --no-audit --no-fund
fi
if [ -n "$START_COMMAND" ]; then exec sh -c "$START_COMMAND"; fi
exec node "$MAIN_FILE"`,
			Vars: []TemplateVar{
				{Key: "MAIN_FILE", Name: "Main file", Default: "index.js", Pattern: `^[A-Za-z0-9._/-]{1,200}$`, UserViewable: true, UserEditable: true},
				{Key: "GIT_REPO", Name: "Git repository (optional)", Description: "https URL of a public repository, cloned on first start.", Default: "", Pattern: `^(https://[^\s]+)?$`, UserViewable: true, UserEditable: true},
				{Key: "GIT_BRANCH", Name: "Git branch", Default: "", Pattern: `^[A-Za-z0-9._/-]*$`, UserViewable: true, UserEditable: true},
				{Key: "AUTO_UPDATE", Name: "git pull on every start", Default: "FALSE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "NPM_INSTALL", Name: "npm install on start", Default: "TRUE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "START_COMMAND", Name: "Custom command (optional)", Description: "Replaces “node file”, e.g. npm start", Default: "", UserViewable: true, UserEditable: true},
				{Key: "NODE_ENV", Name: "NODE_ENV", Default: "production", UserViewable: true, UserEditable: true},
				{Key: "PORT", Name: "Listening port", Default: "$(SERVER_PORT)"},
			},
			Ports:     []TemplatePort{{Name: "http", Port: 8080, Protocol: "tcp"}},
			StopGrace: 15, RunAsUser: 1000, ConsoleInput: true,
			InstallImage: "busybox:1.36", InstallScript: "echo 'Volume ready. Upload your files or configure a Git repository.'",
			DefMemory: 1024, DefCPU: 1000, DefDisk: 5120,
		},
		{
			Slug: "python", Name: "Python", Category: "Bots & applications",
			Description: "Discord bot, script, API… Clones a Git repository (optional), installs requirements.txt, then starts your file.",
			Images: []ImageOption{
				{Name: "Python 3.12", Image: "python:3.12-bookworm"},
				{Name: "Python 3.13", Image: "python:3.13-bookworm"},
				{Name: "Python 3.11", Image: "python:3.11-bookworm"},
			},
			DataPath: "/home/container",
			Startup: gitPrelude + `if [ -f requirements.txt ] && [ "$PIP_INSTALL" = "TRUE" ]; then
  echo "[KubePilot] pip install -r requirements.txt…"
  pip install --user --no-warn-script-location --disable-pip-version-check -r requirements.txt
fi
exec python -u "$MAIN_FILE"`,
			Vars: []TemplateVar{
				{Key: "MAIN_FILE", Name: "Main file", Default: "main.py", Pattern: `^[A-Za-z0-9._/-]{1,200}$`, UserViewable: true, UserEditable: true},
				{Key: "GIT_REPO", Name: "Git repository (optional)", Description: "https URL of a public repository, cloned on first start.", Default: "", Pattern: `^(https://[^\s]+)?$`, UserViewable: true, UserEditable: true},
				{Key: "GIT_BRANCH", Name: "Git branch", Default: "", Pattern: `^[A-Za-z0-9._/-]*$`, UserViewable: true, UserEditable: true},
				{Key: "AUTO_UPDATE", Name: "git pull on every start", Default: "FALSE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "PIP_INSTALL", Name: "pip install on start", Default: "TRUE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "PYTHONUSERBASE", Name: "PYTHONUSERBASE", Default: "/home/container/.local"},
				{Key: "PYTHONUNBUFFERED", Name: "PYTHONUNBUFFERED", Default: "1"},
				{Key: "PORT", Name: "Listening port", Default: "$(SERVER_PORT)"},
			},
			Ports:     []TemplatePort{{Name: "http", Port: 8080, Protocol: "tcp"}},
			StopGrace: 15, RunAsUser: 1000, ConsoleInput: true,
			InstallImage: "busybox:1.36", InstallScript: "echo 'Volume ready. Upload your files or configure a Git repository.'",
			DefMemory: 1024, DefCPU: 1000, DefDisk: 5120,
		},
		{
			Slug: "custom", Name: "Custom Docker image", Category: "Advanced",
			Description: "Any Docker image. The admin picks the image and the startup command.",
			Images:      []ImageOption{{Name: "Alpine", Image: "alpine:3.20"}},
			DataPath:    "/home/container",
			Startup:     "echo 'Set the startup command in the Startup tab.'; exec tail -f /dev/null",
			Vars:        []TemplateVar{},
			Ports:       []TemplatePort{{Name: "app", Port: 8080, Protocol: "tcp"}},
			StopGrace:   15, RunAsUser: 1000, ConsoleInput: false,
			InstallImage: "busybox:1.36",
			DefMemory:    512, DefCPU: 500, DefDisk: 2048,
		},
	}
}

// seedTemplates inserts default templates whose slug doesn't exist yet (admin edits are kept).
func (a *App) seedTemplates() error {
	existing, err := a.db.Templates()
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, t := range existing {
		have[t.Slug] = true
	}
	for _, t := range defaultTemplates() {
		if !have[t.Slug] {
			if err := a.db.SaveTemplate(t); err != nil {
				return err
			}
		}
	}
	return nil
}
