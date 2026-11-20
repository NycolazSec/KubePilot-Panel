package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Minecraft pages: live status (Server List Ping / RakNet), players with RCON, whitelist / ops / bans read
// from the server's files, and player actions. Everything talks to the pod from inside the cluster: nothing
// more is exposed to the Internet. Data coming from the game server is untrusted and size-limited.

const (
	featureMinecraftJava    = "minecraft-java"
	featureMinecraftBedrock = "minecraft-bedrock"
)

var knownFeatures = []string{featureMinecraftJava, featureMinecraftBedrock}

// internalErr is for protocol errors that are logged or swallowed, never shown to users (not translated).
func internalErr(msg string) error { return errors.New(msg) }

// templateFeatures returns the game-specific pages of a template. Templates created before features
// existed are recognized from their slug or image.
func templateFeatures(t *Template) []string {
	if t == nil {
		return []string{}
	}
	if len(t.Features) > 0 {
		return t.Features
	}
	imgs := ""
	for _, im := range t.Images {
		imgs += " " + im.Image
	}
	switch {
	case t.Slug == "minecraft-java" || strings.Contains(imgs, "itzg/minecraft-server"):
		return []string{featureMinecraftJava}
	case t.Slug == "minecraft-bedrock" || strings.Contains(imgs, "minecraft-bedrock-server"):
		return []string{featureMinecraftBedrock}
	}
	return []string{}
}

type mcPlayer struct {
	Name string `json:"name"`
	UUID string `json:"uuid,omitempty"`
}

type mcStatus struct {
	Version       string          `json:"version,omitempty"`
	MOTD          json.RawMessage `json:"motd,omitempty"` // text with § codes, or a chat component
	Favicon       string          `json:"favicon,omitempty"`
	PlayersOnline int             `json:"playersOnline"`
	PlayersMax    int             `json:"playersMax"`
	Players       []mcPlayer      `json:"players"`
	LatencyMs     int64           `json:"latencyMs"`
}

var (
	javaNameRe    = regexp.MustCompile(`^[.*]?[A-Za-z0-9_]{1,16}$`) // "." / "*" = Bedrock players through Geyser/Floodgate
	bedrockNameRe = regexp.MustCompile(`^[A-Za-z0-9_ ]{1,16}$`)
	uuidRe        = regexp.MustCompile(`^[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$`)
)

// ---------- Java: Server List Ping (https://minecraft.wiki/w/Java_Edition_protocol/Server_List_Ping)

func writeVarInt(w *bytes.Buffer, v int32) {
	u := uint32(v)
	for {
		if u&^0x7f == 0 {
			w.WriteByte(byte(u))
			return
		}
		w.WriteByte(byte(u&0x7f | 0x80))
		u >>= 7
	}
}

func readVarInt(r io.ByteReader) (int32, error) {
	var v uint32
	for i := 0; i < 5; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v |= uint32(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return int32(v), nil
		}
	}
	return 0, internalErr("varint too long")
}

func mcPacket(id int32, body []byte) []byte {
	var p bytes.Buffer
	writeVarInt(&p, id)
	p.Write(body)
	var out bytes.Buffer
	writeVarInt(&out, int32(p.Len()))
	out.Write(p.Bytes())
	return out.Bytes()
}

func javaPing(ctx context.Context, host string, port int) (*mcStatus, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	var hs bytes.Buffer
	writeVarInt(&hs, -1) // protocol version: unknown, the server answers with its own
	writeVarInt(&hs, int32(len(host)))
	hs.WriteString(host)
	_ = binary.Write(&hs, binary.BigEndian, uint16(port))
	writeVarInt(&hs, 1) // next state: status
	start := time.Now()
	if _, err := conn.Write(append(mcPacket(0, hs.Bytes()), mcPacket(0, nil)...)); err != nil {
		return nil, err
	}
	br := bufio.NewReader(conn)
	size, err := readVarInt(br)
	if err != nil || size <= 0 || size > 1<<20 {
		return nil, internalErr("bad status response")
	}
	lr := bufio.NewReader(io.LimitReader(br, int64(size)))
	if id, err := readVarInt(lr); err != nil || id != 0 {
		return nil, internalErr("bad status packet")
	}
	n, err := readVarInt(lr)
	if err != nil || n < 0 || n > 1<<20 {
		return nil, internalErr("bad status length")
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(lr, raw); err != nil {
		return nil, err
	}
	var js struct {
		Version struct {
			Name string `json:"name"`
		} `json:"version"`
		Players struct {
			Max, Online int
			Sample      []struct{ Name, ID string }
		} `json:"players"`
		Description json.RawMessage `json:"description"`
		Favicon     string          `json:"favicon"`
	}
	if err := json.Unmarshal(raw, &js); err != nil {
		return nil, err
	}
	st := &mcStatus{Version: truncate(js.Version.Name, 80), PlayersOnline: js.Players.Online, PlayersMax: js.Players.Max,
		Players: []mcPlayer{}, LatencyMs: time.Since(start).Milliseconds()}
	if len(js.Description) <= 8192 {
		st.MOTD = js.Description
	}
	if strings.HasPrefix(js.Favicon, "data:image/png;base64,") && len(js.Favicon) < 64<<10 {
		st.Favicon = js.Favicon
	}
	for _, p := range js.Players.Sample { // servers can put fake lines in the sample: keep real players only
		if javaNameRe.MatchString(p.Name) && uuidRe.MatchString(p.ID) && !strings.HasPrefix(p.ID, "00000000-0000-0000") {
			st.Players = append(st.Players, mcPlayer{Name: p.Name, UUID: p.ID})
		}
	}
	return st, nil
}

// ---------- RCON (https://minecraft.wiki/w/RCON)

func rconPacket(id, typ int32, body string) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, int32(len(body)+10))
	_ = binary.Write(&b, binary.LittleEndian, id)
	_ = binary.Write(&b, binary.LittleEndian, typ)
	b.WriteString(body)
	b.Write([]byte{0, 0})
	return b.Bytes()
}

func rconRead(r io.Reader) (int32, string, error) {
	var size, id, typ int32
	if err := binary.Read(r, binary.LittleEndian, &size); err != nil {
		return 0, "", err
	}
	if size < 10 || size > 1<<16 {
		return 0, "", internalErr("bad rcon packet")
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, "", err
	}
	id = int32(binary.LittleEndian.Uint32(buf[0:4]))
	typ = int32(binary.LittleEndian.Uint32(buf[4:8]))
	_ = typ
	return id, string(bytes.TrimRight(buf[8:], "\x00")), nil
}

func rconDial(ctx context.Context, addr, password string) (net.Conn, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(rconPacket(1, 3, password)); err != nil {
		conn.Close()
		return nil, err
	}
	if id, _, err := rconRead(conn); err != nil || id != 1 {
		conn.Close()
		return nil, internalErr("rcon authentication failed")
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func rconSend(conn net.Conn, id int32, command string) (string, error) {
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetDeadline(time.Time{})
	if _, err := conn.Write(rconPacket(id, 2, command)); err != nil {
		return "", err
	}
	got, out, err := rconRead(conn)
	if err == nil && got != id {
		err = internalErr("rcon: unexpected response")
	}
	return out, err
}

// rconCommand runs one command on a new connection.
func rconCommand(ctx context.Context, addr, password, command string) (string, error) {
	conn, err := rconDial(ctx, addr, password)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	return rconSend(conn, 2, command)
}

// One RCON connection per server, kept open: servers log every new RCON client, so reconnecting on each
// refresh would flood their console.
type rconConn struct {
	mu   sync.Mutex
	conn net.Conn
	id   int32
}

var (
	rconMu    sync.Mutex
	rconConns = map[string]*rconConn{}
)

func (a *App) rconExec(ctx context.Context, s *Server, command string) (string, error) {
	rconMu.Lock()
	c := rconConns[s.ID]
	if c == nil {
		c = &rconConn{}
		rconConns[s.ID] = c
	}
	rconMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if c.conn == nil { // (re)connect: first use, server restarted, new pod IP…
			addr, pass := a.rconTarget(ctx, s, a.kube.PodIP(s.ID))
			if addr == "" {
				return "", internalErr("rcon unavailable")
			}
			conn, err := rconDial(ctx, addr, pass)
			if err != nil {
				return "", err
			}
			c.conn = conn
		}
		c.id++
		if c.id > 1<<30 {
			c.id = 2
		}
		out, err := rconSend(c.conn, c.id+1, command)
		if err == nil {
			return out, nil
		}
		c.conn.Close()
		c.conn = nil
	}
	return "", internalErr("rcon failed")
}

// rconTarget finds how to reach the RCON of a Java server: the itzg image enables it by default and writes
// its password to .rcon-cli.env / .rcon-cli.yaml in the data folder. The host is always the pod's IP.
func (a *App) rconTarget(ctx context.Context, s *Server, podIP string) (string, string) {
	if podIP == "" {
		return "", ""
	}
	port, pass := "25575", s.Env["RCON_PASSWORD"]
	if p := s.Env["RCON_PORT"]; p != "" {
		port = p
	}
	if pass == "" {
		root, _, err := a.openRoot(ctx, s)
		if err != nil {
			return "", ""
		}
		defer root.Close()
		for _, name := range []string{".rcon-cli.env", ".rcon-cli.yaml"} {
			b, err := readSmall(root, name, 4096)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(b), "\n") {
				k, v, ok := strings.Cut(line, "=")
				if !ok {
					k, v, ok = strings.Cut(line, ":")
				}
				if !ok {
					continue
				}
				k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
				switch k {
				case "password":
					pass = v
				case "port":
					if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
						port = v
					}
				}
			}
			if pass != "" {
				break
			}
		}
		if pass == "" { // vanilla configuration (eggs, other images): RCON enabled in server.properties
			if p := propertiesOf(root, "enable-rcon", "rcon.password", "rcon.port"); p["enable-rcon"] == "true" && p["rcon.password"] != "" {
				pass = p["rcon.password"]
				if n, err := strconv.Atoi(p["rcon.port"]); err == nil && n > 0 && n < 65536 {
					port = p["rcon.port"]
				}
			}
		}
	}
	if pass == "" {
		return "", ""
	}
	return net.JoinHostPort(podIP, port), pass
}

var listUUIDsRe = regexp.MustCompile(`([.*]?[A-Za-z0-9_]{1,16}) \(([0-9a-fA-F-]{32,36})\)`)

// ---------- Bedrock: RakNet unconnected ping

var raknetMagic = []byte{0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78}

func bedrockPing(ctx context.Context, host string, port int) (*mcStatus, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	var b bytes.Buffer
	b.WriteByte(0x01)
	_ = binary.Write(&b, binary.BigEndian, time.Now().UnixMilli())
	b.Write(raknetMagic)
	guid := make([]byte, 8)
	_, _ = rand.Read(guid)
	b.Write(guid)
	start := time.Now()
	if _, err := conn.Write(b.Bytes()); err != nil {
		return nil, err
	}
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	if n < 35 || buf[0] != 0x1c {
		return nil, internalErr("bad pong")
	}
	l := int(binary.BigEndian.Uint16(buf[33:35]))
	if 35+l > n {
		return nil, internalErr("bad pong length")
	}
	// MCPE;motd;protocol;version;online;max;serverId;motd2;gamemode;…
	f := strings.Split(string(buf[35:35+l]), ";")
	if len(f) < 6 {
		return nil, internalErr("bad pong payload")
	}
	online, _ := strconv.Atoi(f[4])
	maxp, _ := strconv.Atoi(f[5])
	motd, _ := json.Marshal(truncate(f[1], 200))
	return &mcStatus{Version: truncate(f[3], 40), MOTD: motd, PlayersOnline: online, PlayersMax: maxp, Players: []mcPlayer{},
		LatencyMs: time.Since(start).Milliseconds()}, nil
}

// ---------- Files of the server

// readSmall reads a small regular file of the server's volume.
func readSmall(root *os.Root, name string, limit int64) ([]byte, error) {
	f, st, err := openRegular(root, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st.IsDir() || st.Size() > limit {
		return nil, internalErr("not a small file")
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

type mcListEntry struct {
	Name    string `json:"name,omitempty"`
	UUID    string `json:"uuid,omitempty"`
	XUID    string `json:"xuid,omitempty"`
	IP      string `json:"ip,omitempty"`
	Level   int    `json:"level,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Source  string `json:"source,omitempty"`
	Created string `json:"created,omitempty"`
	Expires string `json:"expires,omitempty"`
}

func readMCList(root *os.Root, name string) []mcListEntry {
	out := []mcListEntry{}
	b, err := readSmall(root, name, 1<<20)
	if err != nil {
		return out
	}
	var raw []mcListEntry
	if json.Unmarshal(b, &raw) != nil {
		return out
	}
	for _, e := range raw {
		if len(out) >= 1000 {
			break
		}
		e.Name, e.Reason, e.Source = truncate(e.Name, 32), truncate(e.Reason, 200), truncate(e.Source, 64)
		if e.UUID != "" && !uuidRe.MatchString(e.UUID) {
			e.UUID = ""
		}
		out = append(out, e)
	}
	return out
}

var shownProperties = []string{"white-list", "enforce-whitelist", "online-mode", "max-players", "gamemode", "difficulty", "pvp", "level-name", "server-port"}

func readProperties(root *os.Root) map[string]string {
	out := propertiesOf(root, shownProperties...)
	for k, v := range out {
		out[k] = truncate(v, 100)
	}
	return out
}

// propertiesOf reads these keys from server.properties (Java properties: "\x" escapes x).
func propertiesOf(root *os.Root, keys ...string) map[string]string {
	out := map[string]string{}
	b, err := readSmall(root, "server.properties", 256<<10)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if k = strings.TrimSpace(k); ok && slices.Contains(keys, k) {
			out[k] = propUnescape.Replace(strings.TrimSpace(v))
		}
	}
	return out
}

var propUnescape = strings.NewReplacer(`\\`, `\`, `\:`, ":", `\=`, "=", `\!`, "!", `\#`, "#", `\ `, " ")

// ---------- HTTP

type mcView struct {
	Kind       string            `json:"kind"`
	Running    bool              `json:"running"`
	Status     *mcStatus         `json:"status"`
	RCON       bool              `json:"rcon"`
	Whitelist  []mcListEntry     `json:"whitelist"`
	Ops        []mcListEntry     `json:"ops"`
	Bans       []mcListEntry     `json:"bans"`
	IPBans     []mcListEntry     `json:"ipBans"`
	Properties map[string]string `json:"properties"`
}

func (a *App) mcKind(s *Server) string {
	t, err := a.db.Template(s.TemplateID)
	if err != nil {
		return ""
	}
	for _, f := range templateFeatures(t) {
		if f == featureMinecraftJava || f == featureMinecraftBedrock {
			return f
		}
	}
	return ""
}

// gamePort is the container port of the server's primary allocation.
func (a *App) gamePort(s *Server, def int) int {
	allocs, _ := a.db.Allocations(s.ID)
	for _, al := range allocs {
		if al.IsPrimary && al.TargetPort > 0 {
			return al.TargetPort
		}
	}
	return def
}

// GET /api/servers/{id}/minecraft
func (a *App) handleMinecraft(w http.ResponseWriter, r *http.Request, c *Ctx) {
	kind := a.mcKind(c.Server)
	if kind == "" {
		writeErr(w, r, errCode(404, "This is not a Minecraft server"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	v := mcView{Kind: kind, Whitelist: []mcListEntry{}, Ops: []mcListEntry{}, Bans: []mcListEntry{}, IPBans: []mcListEntry{}, Properties: map[string]string{}}
	ip := a.kube.PodIP(c.Server.ID)
	v.Running = ip != "" && a.kube.State(c.Server.ID).Status == "running"
	if v.Running {
		if kind == featureMinecraftJava {
			v.Status, _ = javaPing(ctx, ip, a.gamePort(c.Server, 25565))
			if out, err := a.rconExec(ctx, c.Server, "list uuids"); err == nil {
				v.RCON = true
				if v.Status != nil {
					v.Status.Players = []mcPlayer{}
					for _, m := range listUUIDsRe.FindAllStringSubmatch(out, 200) {
						v.Status.Players = append(v.Status.Players, mcPlayer{Name: m[1], UUID: m[2]})
					}
				}
			}
		} else {
			v.Status, _ = bedrockPing(ctx, ip, a.gamePort(c.Server, 19132))
		}
	}
	if root, _, err := a.openRoot(ctx, c.Server); err == nil {
		if kind == featureMinecraftJava {
			v.Whitelist, v.Ops = readMCList(root, "whitelist.json"), readMCList(root, "ops.json")
			v.Bans, v.IPBans = readMCList(root, "banned-players.json"), readMCList(root, "banned-ips.json")
		} else {
			v.Whitelist = readMCList(root, "allowlist.json")
		}
		v.Properties = readProperties(root)
		root.Close()
	}
	writeJSON(w, 200, v)
}

// POST /api/servers/{id}/minecraft/action {action, player, reason}
func (a *App) handleMinecraftAction(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ Action, Player, Reason string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	kind := a.mcKind(c.Server)
	if kind == "" {
		writeErr(w, r, errCode(404, "This is not a Minecraft server"))
		return
	}
	cmd, err := mcCommand(kind, in.Action, in.Player, in.Reason)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	player := strings.TrimSpace(in.Player)
	if a.kube.State(c.Server.ID).Status != "running" {
		writeErr(w, r, errCode(409, "Start the server to manage its players"))
		return
	}
	// RCON returns the server's answer; the console (stdin) is the fallback
	output, sent := "", false
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	if kind == featureMinecraftJava {
		if out, err := a.rconExec(ctx, c.Server, cmd); err == nil {
			output, sent = truncate(strings.TrimSpace(out), 300), true
			a.hub(c.Server.ID).push("\x1b[38;5;245m> " + cmd + "\x1b[0m") // keep the console history complete
		}
	}
	if !sent {
		if err := a.sendCommand(c.Server, cmd); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	c.log(a, "server.minecraft."+in.Action, player)
	writeJSON(w, 200, map[string]any{"ok": true, "output": output})
}

// mcCommand builds the console command of a player action. Player names are validated against the game's
// rules and the reason is cleaned, so nothing can be smuggled into the console.
func mcCommand(kind, action, player, reasonIn string) (string, error) {
	player = strings.TrimSpace(player)
	nameRe := javaNameRe
	if kind == featureMinecraftBedrock {
		nameRe = bedrockNameRe
	}
	// keep the reason on one line, without anything a console could interpret
	reason := truncate(strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '§' {
			return -1
		}
		return r
	}, reasonIn)), 120)
	okName := nameRe.MatchString(player)
	java := kind == featureMinecraftJava
	var cmd string
	switch action {
	case "kick":
		if okName {
			cmd = strings.TrimSpace("kick " + player + " " + reason)
		}
	case "ban":
		if okName && java {
			cmd = strings.TrimSpace("ban " + player + " " + reason)
		}
	case "pardon":
		if okName && java {
			cmd = "pardon " + player
		}
	case "pardon-ip":
		if net.ParseIP(player) != nil && java {
			cmd = "pardon-ip " + player
		}
	case "op", "deop":
		if okName {
			cmd = action + " " + player
		}
	case "whitelist-add", "whitelist-remove":
		if okName {
			list := "whitelist"
			if !java {
				list = "allowlist"
			}
			cmd = list + " " + strings.TrimPrefix(action, "whitelist-") + " " + player
		}
	case "whitelist-on", "whitelist-off":
		if java {
			cmd = "whitelist " + strings.TrimPrefix(action, "whitelist-")
		}
	default:
		return "", errCode(400, "Unknown action")
	}
	if cmd == "" {
		return "", errCode(400, "Invalid player name")
	}
	return cmd, nil
}
