# KubePilot API

Everything the panel does goes through this REST API, so scripts, bots and other apps can drive your servers too:
start / stop them, send console commands, follow the console live, manage files and backups, or automate the admin
area.

- Base URL: `https://<your panel>/api`
- Requests and responses are JSON (`Content-Type: application/json`), except file contents and downloads.
- Error messages come as `{"error": "…"}` with a meaningful HTTP status, in English or in the language of the
  `Accept-Language` header (`fr` supported).

## Authentication

Create a key in **My account → API keys** and send it in the `Authorization` header:

```sh
curl -H "Authorization: Bearer kp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" https://panel.example.com/api/servers
```

A key acts **as you**, narrowed down by its own settings:

| Setting | Effect |
|---|---|
| Permissions | On each server, the key gets the intersection of *your* permissions and the key's: `console`, `power`, `files`, `backups`, `schedules`, `startup`, `network`, `activity`, `users` (sub-users), `settings` (server settings), `admin` (admin area, admins only). |
| Read-only | Only `GET` requests; no command or power action over the console WebSocket either. |
| Allowed IPs | Optional list of IPs / networks the key may be used from. |
| Expiry | Optional: 30, 90 or 365 days. |

Rules that always apply:

- Keys are shown **once**, stored hashed, and only accepted in the `Authorization` header (never in the URL).
- A key can't manage the account: `/api/account/*` (password, two-factor authentication, API keys) needs a browser session.
- **240 requests per minute** per key (`429` beyond). Unknown keys count against the same per-IP budget as sign-ins.
- If two-factor authentication is mandatory on the panel and you haven't enabled it, your keys are refused (`403`).
- Every action is in the activity log with the name of the key.
- No anti-CSRF header is needed with a key (it is required for browser sessions only).
- Behind Cloudflare, the default User-Agent of Python's `urllib` is blocked (error `1010`): set your own
  `User-Agent`, or use `requests`, `aiohttp` or `websockets`, which go through.

## Servers

| Method | Path | Permission | Description |
|---|---|---|---|
| `GET` | `/servers` | — | Servers you can access (`?all=1` for every server, admins) |
| `GET` | `/servers/{id}` | — | One server: resources, state, address, your permissions |
| `POST` | `/servers/{id}/power` | `power` | `{"action": "start" \| "stop" \| "restart" \| "kill"}` |
| `POST` | `/servers/{id}/command` | `console` | `{"command": "say Hello"}` — the server must be running |
| `GET` | `/servers/{id}/console` | `console` | Last console lines: `?lines=100` (max 1000), `&ansi=1` to keep colors |
| `GET` | `/servers/{id}/ws` | `console` | Live console (WebSocket, see below) |
| `GET` | `/servers/{id}/activity` | `activity` | Activity log of the server |
| `PUT` | `/servers/{id}/settings` | `settings` | `{"name": "…", "description": "…"}` |
| `POST` | `/servers/{id}/reinstall` | `settings` | Run the template's install script again (server stopped) |

A server looks like this (`GET /servers/{id}`):

```json
{
  "id": "k3x9q2m1", "name": "Survival", "template": "Minecraft Java", "templateSlug": "minecraft-java",
  "memory": 4096, "cpu": 2000, "disk": 20480,
  "state": { "status": "running", "startedAt": 1795180000, "restarts": 0, "cpu": 812.5, "memory": 2684354560 },
  "diskUsed": 1873215488, "address": "play.example.com:30101", "permissions": ["console", "power", "files"]
}
```

`state.status` is one of `offline`, `installing`, `starting`, `running`, `stopping`, `crashed` (with `state.reason`).
`state.cpu` is in millicores, `state.memory` and `diskUsed` in bytes, `memory` / `disk` limits in MB, `cpu` limit in
millicores (`0` = unlimited).

### Live console (WebSocket)

`GET /api/servers/{id}/ws` with the same `Authorization` header. The panel sends JSON messages:

| `type` | Content |
|---|---|
| `history` | `lines`: the recent console output, sent once on connection |
| `line` | `data`: a new console line (with ANSI colors) |
| `status` | `state`: the server changed state |
| `stats` | `state` and `disk`: resources, every 2 seconds |
| `error` | `data`: why a command or power action was refused |

Send `{"type": "command", "data": "say Hello"}` or `{"type": "power", "data": "restart"}`.

## Minecraft (`console`)

For servers whose template has the Minecraft Java or Bedrock panel.

| Method | Path | Description |
|---|---|---|
| `GET` | `/servers/{id}/minecraft` | Live status (version, MOTD, icon, players with UUIDs, latency), whitelist / allowlist, operators, bans, main `server.properties` values |
| `POST` | `/servers/{id}/minecraft/action` | `{"action": "kick" \| "ban" \| "pardon" \| "pardon-ip" \| "op" \| "deop" \| "whitelist-add" \| "whitelist-remove" \| "whitelist-on" \| "whitelist-off", "player": "Notch", "reason": "…"}` → `{"output": "Made Notch a server operator"}` (server running) |
| `GET` | `/minecraft/avatar/{uuid}` | 64×64 PNG of a player's head, from their Mojang skin (any signed-in user) |

Java servers are queried with the Server List Ping protocol and RCON (the panel reads the password from
`.rcon-cli.env`, written by the `itzg/minecraft-server` image, or from the `RCON_PASSWORD` variable); Bedrock servers
with a RakNet ping. Player names are validated and reasons cleaned before anything reaches the console.

## Plugins & mods (`files`, Minecraft Java)

| Method | Path | Description |
|---|---|---|
| `GET` | `/servers/{id}/plugins` | Software, Minecraft version, folder, installed jars (Modrinth project, version, available update, missing dependencies) |
| `GET` | `/servers/{id}/plugins/search?q=…&offset=…` | Modrinth search, filtered for this server |
| `POST` | `/servers/{id}/plugins/install` | `{"projectId": "1u6JkXh5"}` → `{"installed": […], "missing": […]}` (with dependencies) |
| `POST` | `/servers/{id}/plugins/update` | `{"file": "worldedit-bukkit-7.4.5.jar"}` |
| `POST` | `/servers/{id}/plugins/toggle` | `{"file": "…"}` — `.jar` ⇄ `.jar.disabled` |
| `POST` | `/servers/{id}/plugins/delete` | `{"file": "…"}` |

## Files (`files`)

Paths are relative to the server's data folder (`/` = its root) and can never leave it.

| Method | Path | Body / query |
|---|---|---|
| `GET` | `/servers/{id}/files/list?path=/plugins` | — |
| `GET` | `/servers/{id}/files/content?path=/server.properties` | returns the text (8 MB max) |
| `PUT` | `/servers/{id}/files/content?path=/server.properties` | raw text body |
| `POST` | `/servers/{id}/files/mkdir` | `{"path": "/plugins/config"}` |
| `POST` | `/servers/{id}/files/rename` | `{"from": "/a.txt", "to": "/old/a.txt"}` |
| `POST` | `/servers/{id}/files/delete` | `{"paths": ["/logs", "/crash.txt"]}` |
| `POST` | `/servers/{id}/files/upload?path=/plugins` | `multipart/form-data`, field `files` (2 GB max, disk quota) |
| `GET` | `/servers/{id}/files/download?path=/world` | file, or a folder as `.zip` |
| `POST` | `/servers/{id}/files/compress` | `{"dir": "/", "paths": ["/world"]}` → `{"name": "archive-….tar.gz"}` |
| `POST` | `/servers/{id}/files/decompress` | `{"path": "/modpack.zip"}` (`.zip`, `.tar`, `.tar.gz`, `.tgz`) |

### Databases (`databases`)

| Method | Path | Description |
|---|---|---|
| `GET` | `/servers/{id}/databases` | `{"enabled", "host", "port", "limit", "databases": [{"id", "name", "username", "createdAt"}]}` |
| `POST` | `/servers/{id}/databases` | `{"name": "luckperms"}` → `{"database", "password"}` (the password is only returned here) |
| `POST` | `/servers/{id}/databases/{did}/rotate` | New password → `{"password"}` |
| `DELETE` | `/servers/{id}/databases/{did}` | Deletes the database and its user |

### SFTP (`sftp`)

`GET /servers/{id}/sftp` → `{"enabled", "host", "port", "username", "fingerprint", "passwordAllowed"}`: how to connect
with an SFTP client (`<username>.<server id>`, panel password, or an SSH key added in *My account* — required for
accounts with two-factor authentication). API keys can't be used over SFTP.

## Backups (`backups`)

| Method | Path | Body |
|---|---|---|
| `GET` | `/servers/{id}/backups` | → `{"backups": […], "limit": 3}` |
| `POST` | `/servers/{id}/backups` | `{"name": "Before 1.22"}` (optional name) |
| `GET` | `/servers/{id}/backups/{bid}/download` | `.tar.gz` |
| `POST` | `/servers/{id}/backups/{bid}/restore` | `{"truncate": false}` — server stopped; `true` deletes current files first |
| `POST` | `/servers/{id}/backups/{bid}/lock` | toggles the lock |
| `DELETE` | `/servers/{id}/backups/{bid}` | |

## Schedules, startup, network, sub-users

| Method | Path | Permission | Body |
|---|---|---|---|
| `GET` / `POST` | `/servers/{id}/schedules` | `schedules` | `{"name", "cron": "0 4 * * *", "action": "command \| start \| stop \| restart \| backup", "payload", "enabled", "onlyOnline"}` |
| `PUT` / `DELETE` | `/servers/{id}/schedules/{sid}` | `schedules` | same body for `PUT` |
| `POST` | `/servers/{id}/schedules/{sid}/run` | `schedules` | run now → `{"result": "OK"}` |
| `GET` / `PUT` | `/servers/{id}/startup` | `startup` | `{"env": {"MOTD": "Hi"}, "image": "…"}` (only editable variables, allowed images) |
| `GET` / `POST` | `/servers/{id}/network` | `network` | `POST {"protocol": "tcp" \| "udp" \| "tcp+udp"}` adds a port |
| `PUT` / `DELETE` | `/servers/{id}/network/{port}` | `network` | `PUT {"primary": true}` or `{"notes": "Dynmap"}` |
| `PUT` | `/servers/{id}/exposure` | `network` | `{"internalOnly": true}` → no public port, reachable by the owner's other servers only |
| `PUT` | `/servers/{id}/hostname` | `network` | `{"hostname": "survival"}` → `survival.<Minecraft domain>` on port 25565 (Minecraft router; `""` removes it) |
| `GET` / `POST` | `/servers/{id}/users` | `users` | `POST {"username": "alice", "permissions": ["console", "power"]}` |
| `PUT` / `DELETE` | `/servers/{id}/users/{uid}` | `users` | `PUT {"permissions": […]}` |

## Admin area (`admin` permission, administrators)

| Method | Path | Description |
|---|---|---|
| `GET` | `/admin/backup-storage` | Where new backups go: `{"storage": "local"}` or `{"storage": "s3", "endpoint", "bucket", "prefix", "encrypted", "status"}` |
| `GET` | `/admin/overview` | Nodes, allocated resources, ports |
| `GET` / `POST` | `/admin/users` | List / create `{"username", "email", "password", "isAdmin"}` |
| `PUT` / `DELETE` | `/admin/users/{uid}` | Update (also `"reset2fa": true`) / delete |
| `GET` / `POST` | `/admin/templates` | List / create |
| `PUT` / `DELETE` | `/admin/templates/{tid}` | Update / delete |
| `POST` | `/admin/templates/egg` | Convert a Pterodactyl / Pelican egg `{"url"}` (GitHub only) or `{"egg": <egg JSON>}` into a template draft → `{"template", "warnings"}` (not saved) |
| `GET` | `/admin/eggs/catalog` | Community eggs of github.com/pelican-eggs `[{"repo", "name", "group", "url"}]` (cached 6 h) |
| `POST` | `/admin/servers` | Create `{"name", "ownerId", "templateId", "image", "memory", "cpu", "disk", "backupLimit", "databaseLimit", "allocationLimit", "env", "startAfterInstall"}` |
| `PUT` / `DELETE` | `/admin/servers/{id}` | Update / delete (volume and backups included) |
| `GET` / `PUT` | `/admin/settings` | Panel settings |
| `GET` | `/admin/activity` | Global activity log |

## Examples

### Shell

```sh
export KP=https://panel.example.com/api KEY=kp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

curl -s -H "Authorization: Bearer $KEY" $KP/servers | jq '.[] | {id, name, status: .state.status}'
curl -s -H "Authorization: Bearer $KEY" -X POST $KP/servers/k3x9q2m1/power -d '{"action":"restart"}'
curl -s -H "Authorization: Bearer $KEY" -X POST $KP/servers/k3x9q2m1/command -d '{"command":"say Restart in 5 minutes"}'
curl -s -H "Authorization: Bearer $KEY" "$KP/servers/k3x9q2m1/console?lines=20" | jq -r '.lines[]'
```

### Python (e.g. a Discord bot)

```python
import requests

API = "https://panel.example.com/api"
s = requests.Session()
s.headers["Authorization"] = "Bearer kp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

def status(server_id):
    srv = s.get(f"{API}/servers/{server_id}", timeout=10).json()
    st = srv["state"]
    return f"{srv['name']}: {st['status']}, {st['memory'] / 2**20:.0f} MB RAM, {srv['address']}"

def power(server_id, action):  # start, stop, restart, kill
    r = s.post(f"{API}/servers/{server_id}/power", json={"action": action}, timeout=10)
    if not r.ok:
        raise RuntimeError(r.json()["error"])
```

### Live console

```python
import asyncio, json, websockets  # pip install websockets

async def follow(server_id, key):
    url = f"wss://panel.example.com/api/servers/{server_id}/ws"
    async with websockets.connect(url, additional_headers={"Authorization": f"Bearer {key}"}) as ws:
        await ws.send(json.dumps({"type": "command", "data": "list"}))
        async for raw in ws:
            msg = json.loads(raw)
            if msg["type"] == "line":
                print(msg["data"])
```

Or from a terminal: `websocat -H "Authorization: Bearer $KEY" wss://panel.example.com/api/servers/k3x9q2m1/ws`.
