package main

import (
	"bufio"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeSMTP is a minimal SMTP server that records what it receives.
type fakeSMTP struct {
	addr     string
	starttls bool // advertise STARTTLS (never actually done)
	got      chan smtpMail
}

type smtpMail struct {
	from, to string
	data     string
}

func newFakeSMTP(t *testing.T, starttls bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeSMTP{addr: ln.Addr().String(), starttls: starttls, got: make(chan smtpMail, 10)}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
	say("220 fake ESMTP")
	var m smtpMail
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			if f.starttls {
				say("250-fake")
				say("250 STARTTLS")
			} else {
				say("250 fake")
			}
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			m = smtpMail{from: strings.TrimSpace(line[10:])}
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			m.to = strings.TrimSpace(line[8:])
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			m.data = b.String()
			f.got <- m
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (f *fakeSMTP) mailer(t *testing.T, tlsMode string) *mailer {
	t.Helper()
	host, port, _ := net.SplitHostPort(f.addr)
	m, err := newMailer(MailConfig{Host: host, Port: atoiOr(port, 0), TLS: tlsMode, From: "KubePilot <panel@example.com>"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *fakeSMTP) next(t *testing.T) smtpMail {
	t.Helper()
	select {
	case m := <-f.got:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no email received")
		return smtpMail{}
	}
}

func (f *fakeSMTP) none(t *testing.T) {
	t.Helper()
	select {
	case m := <-f.got:
		t.Fatalf("unexpected email to %s", m.to)
	case <-time.After(300 * time.Millisecond):
	}
}

// bodies decodes the text and HTML parts of a raw email.
func bodies(t *testing.T, raw string) (hdr mail.Header, text, html string) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		p, err := mr.NextPart() // decodes quoted-printable
		if err != nil {
			break
		}
		b, _ := io.ReadAll(p)
		if strings.HasPrefix(p.Header.Get("Content-Type"), "text/html") {
			html = string(b)
		} else {
			text = string(b)
		}
	}
	return msg.Header, text, html
}

func TestBuildMessage(t *testing.T) {
	from, _ := mail.ParseAddress("Panneau Été <panel@example.com>")
	to, _ := mail.ParseAddress("steve@example.com")
	content := mailContent{Panel: "KubePilot", Title: "Bonjour <b>Steve</b>", Paragraphs: []string{"Ligne avec é & <script>"},
		Button: "Go", URL: "https://panel.example.com/reset-password#abc"}
	msg, err := content.render("Réinitialise ton mot de passe\r\nBcc: victim@example.com")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := buildMessage(from, to, msg, time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	hdr, text, html := bodies(t, string(raw))
	if hdr.Get("Bcc") != "" || strings.Contains(string(raw), "\r\nBcc:") {
		t.Fatal("a line break in the subject must not add a header")
	}
	if subj, _ := new(mime.WordDecoder).DecodeHeader(hdr.Get("Subject")); subj != "Réinitialise ton mot de passe Bcc: victim@example.com" {
		t.Errorf("subject: %q", subj)
	}
	if hdr.Get("Auto-Submitted") != "auto-generated" || hdr.Get("Message-Id") == "" || !strings.HasSuffix(hdr.Get("Message-Id"), "@example.com>") {
		t.Errorf("headers: %v", hdr)
	}
	if a, err := hdr.AddressList("From"); err != nil || a[0].Name != "Panneau Été" || a[0].Address != "panel@example.com" {
		t.Errorf("from: %v %v", a, err)
	}
	if !strings.Contains(text, "Bonjour <b>Steve</b>") || !strings.Contains(text, "https://panel.example.com/reset-password#abc") {
		t.Errorf("text part: %q", text)
	}
	if strings.Contains(html, "<script>") || strings.Contains(html, "<b>Steve") || !strings.Contains(html, "&lt;script&gt;") {
		t.Error("the HTML part must escape the text")
	}
	if !strings.Contains(html, `href="https://panel.example.com/reset-password#abc"`) {
		t.Errorf("button link missing: %s", html)
	}
}

func TestMailerConfig(t *testing.T) {
	if m, err := newMailer(MailConfig{}); m != nil || err != nil {
		t.Error("no host: emails off, without error")
	}
	if _, err := newMailer(MailConfig{Host: "smtp.example.com", From: "not an address"}); err == nil {
		t.Error("an invalid sender must be refused")
	}
	if _, err := newMailer(MailConfig{Host: "smtp.example.com", From: "a@example.com", TLS: "ssl3"}); err == nil {
		t.Error("an unknown TLS mode must be refused")
	}
	for mode, port := range map[string]int{"": 587, "starttls": 587, "tls": 465, "none": 25} {
		m, err := newMailer(MailConfig{Host: "smtp.example.com", From: "a@example.com", TLS: mode})
		if err != nil || m.cfg.Port != port {
			t.Errorf("%q: port %d, err %v", mode, m.cfg.Port, err)
		}
	}
}

func TestMailerSend(t *testing.T) {
	f := newFakeSMTP(t, false)
	msg, _ := mailContent{Panel: "KubePilot", Title: "Test"}.render("Test")
	if err := f.mailer(t, "none").send(context.Background(), "steve@example.com", msg); err != nil {
		t.Fatal(err)
	}
	got := f.next(t)
	if got.from != "<panel@example.com>" || got.to != "<steve@example.com>" || !strings.Contains(got.data, "Subject: Test") {
		t.Errorf("got %+v", got)
	}
	// starttls: a server that doesn't offer it is an error, never a fallback to plain text
	if err := f.mailer(t, "starttls").send(context.Background(), "steve@example.com", msg); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("expected a STARTTLS error, got %v", err)
	}
	f.none(t)
	// credentials are never sent over a plain connection (the localhost exception of PlainAuth aside)
	m := f.mailer(t, "none")
	m.cfg.Host, m.cfg.Username, m.cfg.Password = "smtp.example.com", "user", "secret"
	if err := m.sendTo(context.Background(), f.addr, "steve@example.com", msg); err == nil || !strings.Contains(err.Error(), "unencrypted") {
		t.Errorf("PLAIN auth over an unencrypted connection must be refused, got %v", err)
	}
	f.none(t)
}

var resetLinkRe = regexp.MustCompile(`https://panel\.example\.com/reset-password#([0-9a-f]{64})`)

func TestPasswordReset(t *testing.T) {
	app, db := newTestApp(t, false)
	f := newFakeSMTP(t, false)
	h := app.routes()
	call := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-KubePilot", "1")
		r.Header.Set("X-KubePilot-Lang", "fr")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/api/auth/forgot", `{"login":"steve"}`, nil); w.Code != 404 {
		t.Fatalf("no SMTP: reset not offered, got %d", w.Code)
	}
	if w := call("GET", "/api/auth/options", "", nil); !strings.Contains(w.Body.String(), `"passwordReset":false`) {
		t.Fatalf("options: %s", w.Body.String())
	}
	app.mail, app.cfg.PublicURL = f.mailer(t, "none"), "https://panel.example.com"
	if w := call("GET", "/api/auth/options", "", nil); !strings.Contains(w.Body.String(), `"passwordReset":true`) {
		t.Fatalf("options: %s", w.Body.String())
	}
	u, err := db.CreateUser("steve", "steve@example.com", "correct-horse", false)
	if err != nil {
		t.Fatal(err)
	}
	cookie := sessionCookie(t, app, "steve", "correct-horse")

	// unknown account: same answer, no email
	if w := call("POST", "/api/auth/forgot", `{"login":"nobody@example.com"}`, nil); w.Code != 200 {
		t.Fatalf("unknown login: %d", w.Code)
	}
	f.none(t)

	if w := call("POST", "/api/auth/forgot", `{"login":"Steve@Example.com"}`, nil); w.Code != 200 {
		t.Fatalf("forgot: %d %s", w.Code, w.Body.String())
	}
	got := f.next(t)
	_, text, _ := bodies(t, got.data)
	m := resetLinkRe.FindStringSubmatch(text)
	if got.to != "<steve@example.com>" || m == nil || !strings.Contains(text, "Bonjour steve") {
		t.Fatalf("reset email: to %s\n%s", got.to, text)
	}
	tok := m[1]
	var stored string
	_ = db.QueryRow(`SELECT token_hash FROM password_resets WHERE user_id=?`, u.ID).Scan(&stored)
	if stored == "" || stored == tok || stored != sha(tok) {
		t.Fatal("only the token's hash is stored")
	}

	if w := call("POST", "/api/auth/reset", `{"token":"`+tok+`","password":"short"}`, nil); w.Code != 400 {
		t.Fatalf("weak password: %d", w.Code)
	}
	if w := call("POST", "/api/auth/reset", `{"token":"`+strings.Repeat("0", 64)+`","password":"a-much-better-one"}`, nil); w.Code != 400 {
		t.Fatalf("wrong token: %d", w.Code)
	}
	if w := call("POST", "/api/auth/reset", `{"token":"`+tok+`","password":"a-much-better-one"}`, nil); w.Code != 200 {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	if _, text, _ := bodies(t, f.next(t).data); !strings.Contains(text, "vient d'être réinitialisé") {
		t.Errorf("confirmation email: %s", text)
	}
	if w := call("GET", "/api/auth/me", "", cookie); w.Code != 401 {
		t.Error("a reset signs the account out everywhere")
	}
	if w := call("POST", "/api/auth/reset", `{"token":"`+tok+`","password":"yet-another-one"}`, nil); w.Code != 400 {
		t.Error("a link works only once")
	}
	sessionCookie(t, app, "steve", "a-much-better-one")

	// expired link
	call("POST", "/api/auth/forgot", `{"login":"steve"}`, nil)
	tok = resetLinkRe.FindStringSubmatch(func() string { _, tx, _ := bodies(t, f.next(t).data); return tx }())[1]
	_, _ = db.Exec(`UPDATE password_resets SET expires_at=? WHERE user_id=?`, now()-1, u.ID)
	if w := call("POST", "/api/auth/reset", `{"token":"`+tok+`","password":"a-much-better-two"}`, nil); w.Code != 400 {
		t.Error("an expired link must be refused")
	}

	// at most 3 emails per account and per hour: the initial one, the expired one, this one
	call("POST", "/api/auth/forgot", `{"login":"steve"}`, nil)
	f.next(t)
	if w := call("POST", "/api/auth/forgot", `{"login":"steve"}`, nil); w.Code != 200 {
		t.Fatalf("4th request: %d", w.Code)
	}
	f.none(t)
}
