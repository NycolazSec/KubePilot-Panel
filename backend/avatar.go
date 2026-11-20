package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Player heads: the panel fetches the skin from Mojang, cuts the face (with its hat layer) and serves a
// 64×64 PNG from its own origin — browsers never call a third party and the CSP stays 'self'. Players
// without a Mojang skin (offline-mode servers, Bedrock) get a pixel face generated from their ID.

const (
	avatarTTL      = 6 * time.Hour
	avatarRetryTTL = 10 * time.Minute // after a Mojang error
	avatarCacheMax = 2000
)

type avatarEntry struct {
	png     []byte
	expires time.Time
}

var (
	avatarMu    sync.Mutex
	avatarCache = map[string]avatarEntry{}
	uuid32Re    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	textureRe   = regexp.MustCompile(`^/texture/[0-9a-f]{16,128}$`)
	mojangHTTP  = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	errNoSkin   = internalErr("no skin")
)

// GET /api/minecraft/avatar/{uuid}
func (a *App) handleAvatar(w http.ResponseWriter, r *http.Request, _ *User) {
	id := strings.ToLower(strings.ReplaceAll(r.PathValue("uuid"), "-", ""))
	if !uuid32Re.MatchString(id) {
		writeErr(w, r, errCode(400, "Invalid UUID"))
		return
	}
	avatarMu.Lock()
	e, ok := avatarCache[id]
	avatarMu.Unlock()
	if !ok || time.Now().After(e.expires) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		img, err := fetchHead(ctx, id)
		cancel()
		ttl := avatarTTL
		if err != nil {
			if !errors.Is(err, errNoSkin) {
				ttl = avatarRetryTTL
			}
			img = generatedHead(id)
		}
		e = avatarEntry{png: img, expires: time.Now().Add(ttl)}
		avatarMu.Lock()
		if len(avatarCache) >= avatarCacheMax {
			for k, v := range avatarCache {
				if time.Now().After(v.expires) || len(avatarCache) >= avatarCacheMax {
					delete(avatarCache, k)
				}
			}
		}
		avatarCache[id] = e
		avatarMu.Unlock()
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=3600") // overrides the API's no-store: a face isn't secret
	_, _ = w.Write(e.png)
}

func getLimited(ctx context.Context, u string, limit int64) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "KubePilot/"+version)
	res, err := mojangHTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, res.StatusCode, err
	}
	if int64(len(b)) > limit {
		return nil, res.StatusCode, internalErr("response too large")
	}
	return b, res.StatusCode, nil
}

func fetchHead(ctx context.Context, id string) ([]byte, error) {
	b, code, err := getLimited(ctx, "https://sessionserver.mojang.com/session/minecraft/profile/"+id, 64<<10)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNoContent || code == http.StatusNotFound || code == http.StatusBadRequest {
		return nil, errNoSkin
	}
	if code != http.StatusOK {
		return nil, internalErr("mojang: " + http.StatusText(code))
	}
	var prof struct {
		Properties []struct{ Name, Value string } `json:"properties"`
	}
	if err := json.Unmarshal(b, &prof); err != nil {
		return nil, err
	}
	skinURL := ""
	for _, p := range prof.Properties {
		if p.Name != "textures" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(p.Value)
		if err != nil {
			return nil, err
		}
		var tx struct {
			Textures struct {
				Skin struct{ URL string } `json:"SKIN"`
			} `json:"textures"`
		}
		if err := json.Unmarshal(raw, &tx); err != nil {
			return nil, err
		}
		skinURL = tx.Textures.Skin.URL
	}
	// only ever download from Mojang's texture server (no SSRF through a crafted profile)
	u, err := url.Parse(skinURL)
	if skinURL == "" || err != nil || u.Host != "textures.minecraft.net" || !textureRe.MatchString(u.Path) {
		return nil, errNoSkin
	}
	u.Scheme, u.RawQuery, u.Fragment = "https", "", ""
	b, code, err = getLimited(ctx, u.String(), 256<<10)
	if err != nil || code != http.StatusOK {
		return nil, internalErr("skin download failed")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width != 64 || (cfg.Height != 64 && cfg.Height != 32) {
		return nil, errNoSkin
	}
	skin, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, errNoSkin
	}
	return renderHead(skin), nil
}

// renderHead cuts the 8×8 face (and the hat layer drawn over it) and scales it to 64×64.
func renderHead(skin image.Image) []byte {
	px := func(x, y int) color.NRGBA {
		face := color.NRGBAModel.Convert(skin.At(8+x, 8+y)).(color.NRGBA)
		hat := color.NRGBAModel.Convert(skin.At(40+x, 8+y)).(color.NRGBA)
		face.A = 255
		if hat.A == 0 {
			return face
		}
		mix := func(f, h uint8) uint8 { return uint8((uint16(h)*uint16(hat.A) + uint16(f)*uint16(255-hat.A)) / 255) }
		return color.NRGBA{mix(face.R, hat.R), mix(face.G, hat.G), mix(face.B, hat.B), 255}
	}
	return scaleFace(px)
}

func scaleFace(px func(x, y int) color.NRGBA) []byte {
	out := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			out.SetNRGBA(x, y, px(x/8, y/8))
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, out)
	return buf.Bytes()
}

// generatedHead draws a deterministic pixel face for players without a Mojang skin.
func generatedHead(id string) []byte {
	h := sha256.Sum256([]byte(id))
	skins := []color.NRGBA{{0xf2, 0xc9, 0xa5, 255}, {0xe0, 0xac, 0x86, 255}, {0xc6, 0x8c, 0x63, 255}, {0x9c, 0x66, 0x43, 255}, {0x6f, 0x46, 0x2c, 255}}
	hairs := []color.NRGBA{{0x2b, 0x1d, 0x14, 255}, {0x5a, 0x3a, 0x22, 255}, {0x9c, 0x6b, 0x30, 255}, {0xd9, 0xb3, 0x5f, 255}, {0x1f, 0x1f, 0x24, 255}, {0x7d, 0x2f, 0x1b, 255}, {0x55, 0x5f, 0x9e, 255}, {0x3f, 0x8f, 0x6b, 255}}
	eyes := []color.NRGBA{{0x3a, 0x6e, 0xc9, 255}, {0x4f, 0x8a, 0x3c, 255}, {0x5b, 0x3a, 0x22, 255}, {0x6b, 0x4f, 0xc9, 255}}
	skin, hair, eye := skins[int(h[0])%len(skins)], hairs[int(h[1])%len(hairs)], eyes[int(h[2])%len(eyes)]
	white, mouth := color.NRGBA{0xf4, 0xf4, 0xf4, 255}, color.NRGBA{skin.R / 2, skin.G / 3, skin.B / 3, 255}
	fringe := int(h[3]) % 3 // how far the hair comes down on the sides
	return scaleFace(func(x, y int) color.NRGBA {
		switch {
		case y < 2, y == 2 && (x == 0 || x == 7 || int(h[4]>>uint(x))&1 == 1), y < 2+fringe && (x == 0 || x == 7):
			return hair
		case y == 4 && (x == 1 || x == 5):
			return white
		case y == 4 && (x == 2 || x == 6):
			return eye
		case y == 6 && x >= 3 && x <= 4:
			return mouth
		}
		return skin
	})
}
