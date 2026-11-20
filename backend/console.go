package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const hubHistory = 1000

type WSMsg struct {
	Type  string   `json:"type"`
	Data  string   `json:"data,omitempty"`
	Lines []string `json:"lines,omitempty"`
	State *State   `json:"state,omitempty"`
	Disk  int64    `json:"disk,omitempty"`
}

type Hub struct {
	id     string
	mu     sync.Mutex
	lines  []string
	subs   map[chan WSMsg]struct{}
	cancel context.CancelFunc
}

func (a *App) hub(id string) *Hub {
	a.hubsMu.Lock()
	defer a.hubsMu.Unlock()
	if h, ok := a.hubs[id]; ok {
		return h
	}
	ctx, cancel := context.WithCancel(a.ctx)
	h := &Hub{id: id, subs: map[chan WSMsg]struct{}{}, cancel: cancel}
	a.hubs[id] = h
	go a.followLogs(ctx, h)
	go a.watchState(ctx, h)
	return h
}

func (a *App) dropHub(id string) {
	a.hubsMu.Lock()
	defer a.hubsMu.Unlock()
	if h, ok := a.hubs[id]; ok {
		h.cancel()
		delete(a.hubs, id)
	}
}

func (h *Hub) broadcast(m WSMsg) {
	for ch := range h.subs {
		select {
		case ch <- m:
		default: // slow client: drop
		}
	}
}

func (h *Hub) push(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lines = append(h.lines, line)
	if len(h.lines) > hubHistory {
		h.lines = append([]string(nil), h.lines[len(h.lines)-hubHistory:]...)
	}
	h.broadcast(WSMsg{Type: "line", Data: line})
}

// daemon writes a panel message in the console (English format string, translated for each viewer).
func (h *Hub) daemon(msg string, args ...string) { h.push(encodeConsoleMsg(false, msg, args...)) }

// daemonErr writes a panel error in the console.
func (h *Hub) daemonErr(msg string, args ...string) { h.push(encodeConsoleMsg(true, msg, args...)) }

func (h *Hub) subscribe() (chan WSMsg, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan WSMsg, 512)
	h.subs[ch] = struct{}{}
	return ch, append([]string(nil), h.lines...)
}

// snapshot returns a copy of the console history.
func (h *Hub) snapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.lines...)
}

func (h *Hub) unsubscribe(ch chan WSMsg) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

var statusText = map[string]string{
	"starting":   "Starting the server…",
	"running":    "Server online ✔",
	"stopping":   "Stopping the server…",
	"offline":    "Server stopped.",
	"installing": "Installing…",
}

func (a *App) watchState(ctx context.Context, h *Hub) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	last := a.kube.State(h.id)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st := a.kube.State(h.id)
		if st.Status != last.Status || (st.Status == "crashed" && st.Restarts != last.Restarts) {
			if st.Status == "crashed" {
				h.daemonErr("The server crashed: %s", st.Reason)
			} else if last.Status == "installing" && st.InstallError {
				h.daemonErr("Installation failed. See the lines above.")
			} else if last.Status == "installing" {
				h.daemon("Installation completed ✔")
			} else if txt, ok := statusText[st.Status]; ok {
				h.daemon(txt)
			}
			h.mu.Lock()
			h.broadcast(WSMsg{Type: "status", State: &st})
			h.mu.Unlock()
			last = st
		}
	}
}

func splitTimestamp(line string) (time.Time, string) {
	if i := strings.IndexByte(line, ' '); i > 0 {
		if ts, err := time.Parse(time.RFC3339Nano, line[:i]); err == nil {
			return ts, line[i+1:]
		}
	}
	return time.Time{}, line
}

// followLogs streams the logs of the current pod (install job or server) into the hub.
func (a *App) followLogs(ctx context.Context, h *Hub) {
	var lastTs time.Time
	var lastUID types.UID
	idle := time.Second
	sleep := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	pods := a.kube.cs.CoreV1().Pods(a.kube.ns)
	for ctx.Err() == nil {
		pod, ctr, uid, ok := a.kube.LogTarget(h.id)
		if !ok {
			if !sleep(time.Second) {
				return
			}
			continue
		}
		opts := &corev1.PodLogOptions{Container: ctr, Follow: true, Timestamps: true}
		if lastTs.IsZero() {
			tail := int64(300)
			opts.TailLines = &tail
		} else if uid == lastUID {
			t := metav1.NewTime(lastTs.Add(-time.Second))
			opts.SinceTime = &t
		} else {
			t := metav1.NewTime(lastTs.Add(-time.Second))
			opts.SinceTime = &t
		}
		lastUID = uid
		stream, err := pods.GetLogs(pod, opts).Stream(ctx)
		if err != nil {
			if !sleep(2 * time.Second) {
				return
			}
			continue
		}
		got := 0
		r := bufio.NewReaderSize(stream, 64*1024)
		for {
			line, err := r.ReadString('\n')
			if line != "" {
				ts, text := splitTimestamp(strings.TrimRight(line, "\r\n"))
				if !ts.IsZero() && !ts.After(lastTs) {
					continue
				}
				if !ts.IsZero() {
					lastTs = ts
				}
				got++
				if len(text) > 8192 {
					text = text[:8192] + "…"
				}
				h.push(text)
			}
			if err != nil {
				break
			}
		}
		stream.Close()
		if got == 0 {
			if idle < 10*time.Second {
				idle *= 2
			}
		} else {
			idle = time.Second
		}
		if !sleep(idle) {
			return
		}
	}
}

// ---------- WebSocket

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 16384,
	CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		if o == "" {
			return false
		}
		u, err := url.Parse(o)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	},
}

func (a *App) handleWS(w http.ResponseWriter, r *http.Request, c *Ctx) {
	up := &upgrader
	if keyFrom(r) != nil {
		up = &apiUpgrader
	}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(8192)
	tok := ""
	if ck, err := r.Cookie(a.cookieName()); err == nil {
		tok = ck.Value
	}

	h := a.hub(c.Server.ID)
	ch, history := h.subscribe()
	defer h.unsubscribe(ch)

	lang := langOf(r)
	write := func(m WSMsg) error {
		// panel messages are rendered in this viewer's language
		switch m.Type {
		case "line":
			m.Data = renderConsoleLine(lang, m.Data)
		case "history":
			lines := make([]string, len(m.Lines))
			for i, l := range m.Lines {
				lines[i] = renderConsoleLine(lang, l)
			}
			m.Lines = lines
		}
		_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		return conn.WriteJSON(m)
	}
	st := a.kube.State(c.Server.ID)
	if write(WSMsg{Type: "history", Lines: history}) != nil || write(WSMsg{Type: "status", State: &st, Disk: a.diskUsed(c.Server.ID)}) != nil {
		return
	}

	out := make(chan WSMsg, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var in struct{ Type, Data string }
			if err := conn.ReadJSON(&in); err != nil {
				return
			}
			var err error
			switch in.Type {
			case "command":
				if c.ReadOnly {
					err = errCode(403, "This API key is read-only")
				} else if !c.Can("console") {
					err = errCode(403, "Missing permission: console")
				} else if err = a.sendCommand(c.Server, in.Data); err == nil {
					c.log(a, "server.console.command", in.Data)
				}
			case "power":
				if c.ReadOnly {
					err = errCode(403, "This API key is read-only")
				} else if !c.Can("power") {
					err = errCode(403, "Missing permission: power")
				} else if err = a.power(c.Server, in.Data); err == nil {
					c.log(a, "server.power."+in.Data, "")
				}
			}
			if err != nil {
				select {
				case out <- WSMsg{Type: "error", Data: trErr(lang, err)}:
				default:
				}
			}
		}
	}()

	stats := time.NewTicker(2 * time.Second)
	defer stats.Stop()
	recheck := time.NewTicker(time.Minute)
	defer recheck.Stop()
	if err == nil {
		for {
			select {
			case <-done:
				return
			case m := <-ch:
				if write(m) != nil {
					return
				}
			case m := <-out:
				if write(m) != nil {
					return
				}
			case <-stats.C:
				st := a.kube.State(c.Server.ID)
				if write(WSMsg{Type: "stats", State: &st, Disk: a.diskUsed(c.Server.ID)}) != nil {
					return
				}
			case <-recheck.C:
				if _, err := a.db.SessionUser(tok); err != nil {
					_ = write(WSMsg{Type: "error", Data: tr(lang, "Session expired")})
					return
				}
			}
		}
	}
}

var _ = io.EOF
