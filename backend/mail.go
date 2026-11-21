package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Emails: password reset links and notifications, sent through the SMTP server given to the chart
// (email.smtp.*). Without it, everything that needs an email is simply not offered.

// MailConfig is the SMTP server the panel sends through.
type MailConfig struct {
	Host     string
	Port     int
	TLS      string // starttls (default), tls (implicit TLS, usually port 465), none (trusted relay only)
	Username string
	Password string
	From     string // "KubePilot <panel@example.com>"
}

type mailer struct {
	cfg  MailConfig
	from *mail.Address
}

const mailTimeout = 30 * time.Second

func newMailer(cfg MailConfig) (*mailer, error) {
	if cfg.Host == "" {
		return nil, nil
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("email: invalid sender %q: %w", cfg.From, err)
	}
	switch cfg.TLS {
	case "":
		cfg.TLS = "starttls"
	case "starttls", "tls", "none":
	default:
		return nil, fmt.Errorf("email: unknown TLS mode %q (starttls, tls or none)", cfg.TLS)
	}
	if cfg.Port == 0 {
		cfg.Port = map[string]int{"starttls": 587, "tls": 465, "none": 25}[cfg.TLS]
	}
	return &mailer{cfg: cfg, from: from}, nil
}

// mailMsg is one email, in plain text and HTML.
type mailMsg struct {
	Subject string
	Text    string
	HTML    string
}

// send delivers msg to one address. It refuses to send credentials over an unencrypted connection: with
// starttls, a server that doesn't offer STARTTLS is an error (no silent downgrade).
func (m *mailer) send(ctx context.Context, to string, msg mailMsg) error {
	return m.sendTo(ctx, net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port)), to, msg)
}

// sendTo talks to the SMTP server at addr (the TLS certificate and authentication are checked against cfg.Host).
func (m *mailer) sendTo(ctx context.Context, addr, to string, msg mailMsg) error {
	rcpt, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	data, err := buildMessage(m.from, rcpt, msg, time.Now())
	if err != nil {
		return err
	}
	tlsCfg := &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}
	d := net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if m.cfg.TLS == "tls" {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(mailTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Hello(heloName(m.from.Address)); err != nil {
		return err
	}
	if m.cfg.TLS == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the SMTP server doesn't offer STARTTLS")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if m.cfg.Username != "" {
		// PlainAuth itself refuses to send the password over a connection that isn't encrypted
		if err := c.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(m.from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(rcpt.Address); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// heloName is the name the panel gives in HELO: the domain of its sender address.
func heloName(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 && i < len(addr)-1 {
		return addr[i+1:]
	}
	return "localhost"
}

// oneLine removes line breaks, so no text can add headers to an email.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\r' || r == '\n' }), " ")
}

func buildMessage(from, to *mail.Address, msg mailMsg, at time.Time) ([]byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	h := []struct{ k, v string }{
		{"From", from.String()},
		{"To", to.String()},
		{"Subject", mime.QEncoding.Encode("utf-8", oneLine(msg.Subject))},
		{"Date", at.Format(time.RFC1123Z)},
		{"Message-ID", "<" + hex.EncodeToString(id) + "@" + heloName(from.Address) + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", `multipart/alternative; boundary="` + mw.Boundary() + `"`},
		{"Auto-Submitted", "auto-generated"}, // RFC 3834: no vacation replies
	}
	var head bytes.Buffer
	for _, kv := range h {
		head.WriteString(kv.k + ": " + oneLine(kv.v) + "\r\n")
	}
	head.WriteString("\r\n")
	for _, part := range []struct{ typ, body string }{{"text/plain", msg.Text}, {"text/html", msg.HTML}} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.typ + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		qp := quotedprintable.NewWriter(pw)
		if _, err := qp.Write([]byte(strings.ReplaceAll(part.body, "\n", "\r\n"))); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return append(head.Bytes(), buf.Bytes()...), nil
}

// ---------- Content

// mailContent is what an email says; render turns it into text and HTML in the panel's style.
type mailContent struct {
	Panel      string   // panel name, in the header
	Title      string   // first line, in bold
	Paragraphs []string // plain text, escaped in HTML
	Button     string   // call to action (optional)
	URL        string
	Footer     string
}

// html writes the email's HTML: a simple table layout that mail clients render. Every text is escaped.
// (No html/template: its reflection keeps every method of every type in the binary — +25 MB with client-go.)
func (c mailContent) html() string {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html><body style="margin:0;padding:24px;background:#f4f4f7;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#1c1d23">
<table role="presentation" width="100%" cellspacing="0" cellpadding="0"><tr><td align="center">
<table role="presentation" width="560" cellspacing="0" cellpadding="0" style="max-width:560px;width:100%;background:#ffffff;border-radius:10px;border:1px solid #e4e4ea">
<tr><td style="padding:20px 28px;border-bottom:1px solid #ececf1;font-weight:600;font-size:15px;color:#5546c4">` + e(c.Panel) + `</td></tr>
<tr><td style="padding:28px">
<p style="margin:0 0 16px;font-size:17px;font-weight:600">` + e(c.Title) + `</p>
`)
	for _, p := range c.Paragraphs {
		b.WriteString(`<p style="margin:0 0 14px;font-size:14px;line-height:1.55;color:#30323b">` + e(p) + "</p>\n")
	}
	if c.URL != "" {
		b.WriteString(`<p style="margin:22px 0"><a href="` + e(c.URL) + `" style="display:inline-block;background:#6656e3;color:#ffffff;text-decoration:none;padding:10px 18px;border-radius:7px;font-size:14px;font-weight:600">` + e(c.Button) + `</a></p>
<p style="margin:0;font-size:12px;line-height:1.5;color:#7c7f8c;word-break:break-all">` + e(c.URL) + "</p>\n")
	}
	b.WriteString("</td></tr>\n")
	if c.Footer != "" {
		b.WriteString(`<tr><td style="padding:16px 28px;border-top:1px solid #ececf1;font-size:12px;line-height:1.5;color:#7c7f8c">` + e(c.Footer) + "</td></tr>\n")
	}
	b.WriteString("</table></td></tr></table></body></html>\n")
	return b.String()
}

func (c mailContent) render(subject string) (mailMsg, error) {
	var text strings.Builder
	text.WriteString(c.Title + "\n\n")
	for _, p := range c.Paragraphs {
		text.WriteString(p + "\n\n")
	}
	if c.URL != "" {
		text.WriteString(c.Button + ":\n" + c.URL + "\n\n")
	}
	if c.Footer != "" {
		text.WriteString("-- \n" + c.Footer + "\n")
	}
	return mailMsg{Subject: subject, Text: text.String(), HTML: c.html()}, nil
}
