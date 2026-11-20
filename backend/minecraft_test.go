package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"strings"
	"testing"
)

func TestVarIntRoundTrip(t *testing.T) {
	for _, v := range []int32{0, 1, 127, 128, 25565, 2097151, -1} {
		var b bytes.Buffer
		writeVarInt(&b, v)
		got, err := readVarInt(bufio.NewReader(&b))
		if err != nil || got != v {
			t.Errorf("varint %d → %d (%v)", v, got, err)
		}
	}
}

// fakeJava answers one Server List Ping with the given JSON.
func fakeJava(t *testing.T, status string) (string, int) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		for i := 0; i < 2; i++ { // handshake + status request
			n, _ := readVarInt(br)
			_, _ = io.CopyN(io.Discard, br, int64(n))
		}
		var body bytes.Buffer
		writeVarInt(&body, int32(len(status)))
		body.WriteString(status)
		_, _ = c.Write(mcPacket(0, body.Bytes()))
	}()
	a := ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

func TestJavaPing(t *testing.T) {
	host, port := fakeJava(t, `{"version":{"name":"Paper 1.21.4","protocol":769},
		"players":{"max":20,"online":3,"sample":[{"name":"Notch","id":"069a79f4-44e9-4726-a5be-fca90e38aaf5"},
		{"name":"§6Welcome to the server!","id":"00000000-0000-0000-0000-000000000000"}]},
		"description":{"text":"A ","extra":[{"text":"KubePilot","color":"gold"}]},"favicon":"data:image/png;base64,iVBORw0KGgo="}`)
	st, err := javaPing(context.Background(), host, port)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != "Paper 1.21.4" || st.PlayersOnline != 3 || st.PlayersMax != 20 || st.Favicon == "" {
		t.Fatalf("status: %+v", st)
	}
	if len(st.Players) != 1 || st.Players[0].Name != "Notch" {
		t.Fatalf("fake sample lines must be dropped, real players kept: %+v", st.Players)
	}
	if !strings.Contains(string(st.MOTD), "KubePilot") {
		t.Fatalf("motd: %s", st.MOTD)
	}
}

func TestJavaPingRefusesNonPNGFavicon(t *testing.T) {
	host, port := fakeJava(t, `{"version":{"name":"x"},"players":{"max":1,"online":0},"description":"hi","favicon":"data:text/html;base64,PHNjcmlwdD4="}`)
	st, err := javaPing(context.Background(), host, port)
	if err != nil || st.Favicon != "" {
		t.Fatalf("only PNG favicons may reach the browser: %+v %v", st, err)
	}
}

func TestRCON(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		id, pass, _ := rconRead(c)
		if pass != "s3cret" {
			id = -1
		}
		_, _ = c.Write(rconPacket(id, 2, ""))
		id, cmd, _ := rconRead(c)
		_, _ = c.Write(rconPacket(id, 0, "There are 2 of a max of 20 players online: Notch (069a79f4-44e9-4726-a5be-fca90e38aaf5), jeb_ (853c80ef-3c37-49fd-aa49-938b674adae6) cmd="+cmd))
	}()
	out, err := rconCommand(context.Background(), ln.Addr().String(), "s3cret", "list uuids")
	if err != nil {
		t.Fatal(err)
	}
	m := listUUIDsRe.FindAllStringSubmatch(out, -1)
	if len(m) != 2 || m[0][1] != "Notch" || m[1][1] != "jeb_" || !strings.Contains(out, "cmd=list uuids") {
		t.Fatalf("rcon list: %q", out)
	}
}

func TestBedrockPing(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 1500)
		_, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		payload := "MCPE;Dedicated Server;766;1.21.50;2;10;123;Bedrock level;Survival;1;19132;19133;"
		var b bytes.Buffer
		b.WriteByte(0x1c)
		b.Write(make([]byte, 16)) // time + server guid
		b.Write(raknetMagic)
		_ = binary.Write(&b, binary.BigEndian, uint16(len(payload)))
		b.WriteString(payload)
		_, _ = pc.WriteTo(b.Bytes(), from)
	}()
	a := pc.LocalAddr().(*net.UDPAddr)
	st, err := bedrockPing(context.Background(), a.IP.String(), a.Port)
	if err != nil {
		t.Fatal(err)
	}
	var motd string
	_ = json.Unmarshal(st.MOTD, &motd)
	if st.Version != "1.21.50" || st.PlayersOnline != 2 || st.PlayersMax != 10 || motd != "Dedicated Server" {
		t.Fatalf("bedrock: %+v %q", st, motd)
	}
}

func TestMinecraftCommandsCantBeInjected(t *testing.T) {
	ok := map[[3]string]string{
		{"kick", "Notch", "go away"}:         "kick Notch go away",
		{"ban", "Notch", "cheat\nop Evil"}:   "ban Notch cheatop Evil", // one line: a newline can't start a second command
		{"whitelist-add", ".BedrockGuy", ""}: "whitelist add .BedrockGuy",
		{"pardon-ip", "203.0.113.4", ""}:     "pardon-ip 203.0.113.4",
		{"op", "jeb_", ""}:                   "op jeb_",
		{"whitelist-on", "", ""}:             "whitelist on",
		{"kick", "Notch", "§k§lspam"}:        "kick Notch klspam",
	}
	for in, want := range ok {
		got, err := mcCommand(featureMinecraftJava, in[0], in[1], in[2])
		if err != nil || got != want {
			t.Errorf("%v → %q (%v), want %q", in, got, err, want)
		}
	}
	for _, bad := range [][2]string{{"op", "Notch op Evil"}, {"kick", "a;b"}, {"op", ""}, {"pardon-ip", "1.2.3"}, {"stop", "Notch"}, {"op", strings.Repeat("a", 17)}} {
		if got, err := mcCommand(featureMinecraftJava, bad[0], bad[1], ""); err == nil {
			t.Errorf("%v must be refused, got %q", bad, got)
		}
	}
	if got, _ := mcCommand(featureMinecraftBedrock, "whitelist-add", "Steve Gamer", ""); got != "allowlist add Steve Gamer" {
		t.Errorf("bedrock allowlist: %q", got)
	}
}

func TestRenderHead(t *testing.T) {
	skin := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 8; y < 16; y++ {
		for x := 8; x < 16; x++ {
			skin.SetNRGBA(x, y, color.NRGBA{200, 0, 0, 255}) // face: red
		}
	}
	skin.SetNRGBA(40, 8, color.NRGBA{0, 0, 255, 255}) // hat pixel over the top-left of the face: blue
	img, err := png.Decode(bytes.NewReader(renderHead(skin)))
	if err != nil || img.Bounds().Dx() != 64 {
		t.Fatal("head must be a 64×64 PNG", err)
	}
	if c := color.NRGBAModel.Convert(img.At(3, 3)).(color.NRGBA); c.B != 255 || c.R != 0 {
		t.Errorf("hat layer must cover the face: %v", c)
	}
	if c := color.NRGBAModel.Convert(img.At(30, 30)).(color.NRGBA); c.R != 200 {
		t.Errorf("face pixel: %v", c)
	}
	a, b := generatedHead("069a79f444e94726a5befca90e38aaf5"), generatedHead("069a79f444e94726a5befca90e38aaf5")
	if !bytes.Equal(a, b) || bytes.Equal(a, generatedHead("853c80ef3c3749fdaa49938b674adae6")) {
		t.Error("generated heads must be deterministic and differ between players")
	}
}

func TestTemplateFeatures(t *testing.T) {
	cases := map[string]*Template{
		featureMinecraftJava:    {Slug: "my-paper", Images: []ImageOption{{Image: "itzg/minecraft-server:java21"}}},
		featureMinecraftBedrock: {Slug: "minecraft-bedrock"},
		"":                      {Slug: "nodejs", Images: []ImageOption{{Image: "node:22"}}},
	}
	for want, tpl := range cases {
		got := strings.Join(templateFeatures(tpl), ",")
		if got != want {
			t.Errorf("%s: got %q, want %q", tpl.Slug, got, want)
		}
	}
	if f := templateFeatures(&Template{Slug: "x", Features: []string{featureMinecraftBedrock}}); f[0] != featureMinecraftBedrock {
		t.Error("explicit features win")
	}
}
