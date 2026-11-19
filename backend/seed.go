package main

const gitPrelude = `if [ -n "$GIT_REPO" ] && [ ! -d .git ]; then
  echo "[KubePilot] Clonage de $GIT_REPO…"
  rm -rf /tmp/repo
  git clone --depth 1 ${GIT_BRANCH:+-b "$GIT_BRANCH"} "$GIT_REPO" /tmp/repo
  cp -a /tmp/repo/. . && rm -rf /tmp/repo
elif [ -d .git ] && [ "$AUTO_UPDATE" = "TRUE" ]; then
  echo "[KubePilot] Mise à jour du dépôt (git pull)…"
  git pull --ff-only || echo "[KubePilot] git pull a échoué, on continue."
fi
`

func boolOpts() []string { return []string{"TRUE", "FALSE"} }

func defaultTemplates() []*Template {
	return []*Template{
		{
			Slug: "minecraft-java", Name: "Minecraft Java", Category: "Minecraft",
			Description: "Paper, Purpur, Vanilla, Fabric, Forge, NeoForge… Téléchargement et mises à jour automatiques (itzg/minecraft-server).",
			Images: []ImageOption{
				{Name: "Java recommandé (auto)", Image: "itzg/minecraft-server:latest"},
				{Name: "Java 25", Image: "itzg/minecraft-server:java25"},
				{Name: "Java 21", Image: "itzg/minecraft-server:java21"},
				{Name: "Java 17", Image: "itzg/minecraft-server:java17"},
				{Name: "Java 8 (anciennes versions)", Image: "itzg/minecraft-server:java8"},
			},
			DataPath: "/data",
			Vars: []TemplateVar{
				{Key: "EULA", Name: "Accepter l'EULA Minecraft", Description: "Obligatoire pour lancer le serveur : https://aka.ms/MinecraftEULA", Default: "TRUE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "TYPE", Name: "Type de serveur", Description: "Logiciel serveur à télécharger.", Default: "PAPER", Options: []string{"PAPER", "PURPUR", "VANILLA", "FABRIC", "QUILT", "FORGE", "NEOFORGE", "FOLIA", "SPIGOT"}, UserViewable: true, UserEditable: true},
				{Key: "VERSION", Name: "Version de Minecraft", Description: "LATEST ou une version précise (ex : 1.21.4).", Default: "LATEST", Pattern: `^[A-Za-z0-9._-]{1,32}$`, UserViewable: true, UserEditable: true},
				{Key: "MOTD", Name: "Message du jour (MOTD)", Description: "Texte affiché dans la liste des serveurs.", Default: "Un serveur propulsé par KubePilot", UserViewable: true, UserEditable: true},
				{Key: "DIFFICULTY", Name: "Difficulté", Default: "normal", Options: []string{"peaceful", "easy", "normal", "hard"}, UserViewable: true, UserEditable: true},
				{Key: "MODE", Name: "Mode de jeu", Default: "survival", Options: []string{"survival", "creative", "adventure", "spectator"}, UserViewable: true, UserEditable: true},
				{Key: "MAX_PLAYERS", Name: "Joueurs max", Default: "20", Pattern: `^[0-9]{1,4}$`, UserViewable: true, UserEditable: true},
				{Key: "ONLINE_MODE", Name: "Mode en ligne", Description: "Vérifie les comptes Mojang (désactive seulement si tu sais pourquoi).", Default: "TRUE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "VIEW_DISTANCE", Name: "Distance de vue", Default: "10", Pattern: `^([2-9]|[12][0-9]|3[0-2])$`, UserViewable: true, UserEditable: true},
				{Key: "OPS", Name: "Opérateurs", Description: "Pseudos séparés par des virgules.", Default: "", Pattern: `^[A-Za-z0-9_,]*$`, UserViewable: true, UserEditable: true},
				{Key: "WHITELIST", Name: "Liste blanche", Description: "Pseudos autorisés, séparés par des virgules (vide = ouvert à tous).", Default: "", Pattern: `^[A-Za-z0-9_,]*$`, UserViewable: true, UserEditable: true},
				{Key: "SEED", Name: "Graine du monde", Default: "", UserViewable: true, UserEditable: true},
				{Key: "USE_AIKAR_FLAGS", Name: "Flags JVM optimisés (Aikar)", Default: "TRUE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "MEMORY", Name: "Mémoire JVM", Description: "Calculée automatiquement depuis la limite mémoire.", Default: "$(SERVER_JVM_MEMORY)M"},
				{Key: "STOP_SERVER_ANNOUNCE_DELAY", Name: "Délai d'annonce d'arrêt", Default: "0"},
			},
			Ports:     []TemplatePort{{Name: "minecraft", Port: 25565, Protocol: "tcp"}},
			StopGrace: 90, ReadyProbe: true, RunAsUser: 1000, ConsoleInput: true,
			InstallImage: "busybox:1.36", InstallScript: "echo 'Volume prêt. Minecraft sera téléchargé au premier démarrage.'",
			DefMemory: 4096, DefCPU: 2000, DefDisk: 10240,
		},
		{
			Slug: "minecraft-bedrock", Name: "Minecraft Bedrock", Category: "Minecraft",
			Description: "Serveur officiel Bedrock (mobile, consoles, Windows). Mises à jour automatiques.",
			Images:      []ImageOption{{Name: "Bedrock", Image: "itzg/minecraft-bedrock-server:latest"}},
			DataPath:    "/data",
			Vars: []TemplateVar{
				{Key: "EULA", Name: "Accepter l'EULA Minecraft", Description: "https://aka.ms/MinecraftEULA", Default: "TRUE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "VERSION", Name: "Version", Default: "LATEST", Pattern: `^[A-Za-z0-9._-]{1,32}$`, UserViewable: true, UserEditable: true},
				{Key: "SERVER_NAME", Name: "Nom du serveur", Default: "Serveur KubePilot", UserViewable: true, UserEditable: true},
				{Key: "GAMEMODE", Name: "Mode de jeu", Default: "survival", Options: []string{"survival", "creative", "adventure"}, UserViewable: true, UserEditable: true},
				{Key: "DIFFICULTY", Name: "Difficulté", Default: "normal", Options: []string{"peaceful", "easy", "normal", "hard"}, UserViewable: true, UserEditable: true},
				{Key: "MAX_PLAYERS", Name: "Joueurs max", Default: "10", Pattern: `^[0-9]{1,4}$`, UserViewable: true, UserEditable: true},
				{Key: "ALLOW_CHEATS", Name: "Autoriser les triches", Default: "false", Options: []string{"false", "true"}, UserViewable: true, UserEditable: true},
				{Key: "ONLINE_MODE", Name: "Mode en ligne (Xbox Live)", Default: "true", Options: []string{"true", "false"}, UserViewable: true, UserEditable: true},
				{Key: "LEVEL_NAME", Name: "Nom du monde", Default: "Bedrock level", UserViewable: true, UserEditable: true},
			},
			Ports:     []TemplatePort{{Name: "bedrock", Port: 19132, Protocol: "udp"}},
			StopGrace: 30, ReadyProbe: false, RunAsUser: 1000, ConsoleInput: true,
			InstallImage: "busybox:1.36", InstallScript: "echo 'Volume prêt. Bedrock sera téléchargé au premier démarrage.'",
			DefMemory: 2048, DefCPU: 1000, DefDisk: 5120,
		},
		{
			Slug: "nodejs", Name: "Node.js", Category: "Bots & applications",
			Description: "Bot Discord, API, site… Clone un dépôt Git (optionnel), npm install puis lance ton fichier.",
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
				{Key: "MAIN_FILE", Name: "Fichier principal", Default: "index.js", Pattern: `^[A-Za-z0-9._/-]{1,200}$`, UserViewable: true, UserEditable: true},
				{Key: "GIT_REPO", Name: "Dépôt Git (optionnel)", Description: "URL https d'un dépôt public, cloné au premier démarrage.", Default: "", Pattern: `^(https://[^\s]+)?$`, UserViewable: true, UserEditable: true},
				{Key: "GIT_BRANCH", Name: "Branche Git", Default: "", Pattern: `^[A-Za-z0-9._/-]*$`, UserViewable: true, UserEditable: true},
				{Key: "AUTO_UPDATE", Name: "git pull à chaque démarrage", Default: "FALSE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "NPM_INSTALL", Name: "npm install au démarrage", Default: "TRUE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "START_COMMAND", Name: "Commande perso (optionnel)", Description: "Remplace « node fichier », ex : npm start", Default: "", UserViewable: true, UserEditable: true},
				{Key: "NODE_ENV", Name: "NODE_ENV", Default: "production", UserViewable: true, UserEditable: true},
				{Key: "PORT", Name: "Port d'écoute", Default: "$(SERVER_PORT)"},
			},
			Ports:     []TemplatePort{{Name: "http", Port: 8080, Protocol: "tcp"}},
			StopGrace: 15, RunAsUser: 1000, ConsoleInput: true,
			InstallImage: "busybox:1.36", InstallScript: "echo 'Volume prêt. Dépose tes fichiers ou configure un dépôt Git.'",
			DefMemory: 1024, DefCPU: 1000, DefDisk: 5120,
		},
		{
			Slug: "python", Name: "Python", Category: "Bots & applications",
			Description: "Bot Discord, script, API… Clone un dépôt Git (optionnel), installe requirements.txt puis lance ton fichier.",
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
				{Key: "MAIN_FILE", Name: "Fichier principal", Default: "main.py", Pattern: `^[A-Za-z0-9._/-]{1,200}$`, UserViewable: true, UserEditable: true},
				{Key: "GIT_REPO", Name: "Dépôt Git (optionnel)", Description: "URL https d'un dépôt public, cloné au premier démarrage.", Default: "", Pattern: `^(https://[^\s]+)?$`, UserViewable: true, UserEditable: true},
				{Key: "GIT_BRANCH", Name: "Branche Git", Default: "", Pattern: `^[A-Za-z0-9._/-]*$`, UserViewable: true, UserEditable: true},
				{Key: "AUTO_UPDATE", Name: "git pull à chaque démarrage", Default: "FALSE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "PIP_INSTALL", Name: "pip install au démarrage", Default: "TRUE", Options: boolOpts(), UserViewable: true, UserEditable: true},
				{Key: "PYTHONUSERBASE", Name: "PYTHONUSERBASE", Default: "/home/container/.local"},
				{Key: "PYTHONUNBUFFERED", Name: "PYTHONUNBUFFERED", Default: "1"},
				{Key: "PORT", Name: "Port d'écoute", Default: "$(SERVER_PORT)"},
			},
			Ports:     []TemplatePort{{Name: "http", Port: 8080, Protocol: "tcp"}},
			StopGrace: 15, RunAsUser: 1000, ConsoleInput: true,
			InstallImage: "busybox:1.36", InstallScript: "echo 'Volume prêt. Dépose tes fichiers ou configure un dépôt Git.'",
			DefMemory: 1024, DefCPU: 1000, DefDisk: 5120,
		},
		{
			Slug: "custom", Name: "Image Docker personnalisée", Category: "Avancé",
			Description: "N'importe quelle image Docker. L'admin choisit l'image et la commande de démarrage.",
			Images:      []ImageOption{{Name: "Alpine", Image: "alpine:3.20"}},
			DataPath:    "/home/container",
			Startup:     "echo 'Configure la commande de démarrage dans l onglet Démarrage.'; exec tail -f /dev/null",
			Vars:        []TemplateVar{},
			Ports:       []TemplatePort{{Name: "app", Port: 8080, Protocol: "tcp"}},
			StopGrace:   15, RunAsUser: 1000, ConsoleInput: false,
			InstallImage: "busybox:1.36",
			DefMemory:   512, DefCPU: 500, DefDisk: 2048,
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
