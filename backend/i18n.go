package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Messages are written in English in the code. Each file in locales/ maps an English message
// (a fmt format string) to its translation: add a language by adding a file.
//
//go:embed locales/*.json
var localeFS embed.FS

var catalogs = loadCatalogs()

func loadCatalogs() map[string]map[string]string {
	out := map[string]map[string]string{}
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		b, err := localeFS.ReadFile("locales/" + e.Name())
		if err != nil {
			panic(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			panic(fmt.Sprintf("locales/%s: %v", e.Name(), err))
		}
		out[strings.TrimSuffix(e.Name(), ".json")] = m
	}
	return out
}

// supportedLang normalizes "fr-FR", "fr;q=0.9"… and returns "" for unsupported languages.
func supportedLang(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if i := strings.IndexAny(l, "-_;"); i >= 0 {
		l = l[:i]
	}
	if l == "en" {
		return l
	}
	if _, ok := catalogs[l]; ok {
		return l
	}
	return ""
}

// langOf returns the language of a request: the interface's explicit choice (X-KubePilot-Lang header,
// or ?lang= for WebSockets, which can't send headers), then the browser's Accept-Language, then English.
func langOf(r *http.Request) string {
	if l := supportedLang(r.Header.Get("X-KubePilot-Lang")); l != "" {
		return l
	}
	if l := supportedLang(r.URL.Query().Get("lang")); l != "" {
		return l
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		if l := supportedLang(part); l != "" {
			return l
		}
	}
	return "en"
}

// tr translates an English message (format string) and formats it.
func tr(lang, msg string, args ...any) string {
	if t := catalogs[lang][msg]; t != "" {
		msg = t
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

// trErr returns the message of an error in the user's language.
func trErr(lang string, err error) string {
	var ae *apiError
	if errors.As(err, &ae) {
		return tr(lang, ae.msg, ae.args...)
	}
	return tr(lang, err.Error())
}

// ---------- Console messages
//
// Panel messages in a server console are shared by every viewer and kept in the scrollback, so they are
// stored untranslated (marker + English message + arguments) and rendered for each viewer's language.

const consoleMark = "\x00kp:"

type consoleMsg struct {
	Msg  string   `json:"m"`
	Args []string `json:"a,omitempty"`
	Err  bool     `json:"e,omitempty"`
}

func encodeConsoleMsg(isErr bool, msg string, args ...string) string {
	b, _ := json.Marshal(consoleMsg{Msg: msg, Args: args, Err: isErr})
	return consoleMark + string(b)
}

// bytesArg passes a size as a console message argument, formatted in the viewer's language.
func bytesArg(n int64) string { return "\x01" + strconv.FormatInt(n, 10) }

func renderConsoleLine(lang, line string) string {
	if !strings.HasPrefix(line, consoleMark) {
		return line
	}
	var m consoleMsg
	if json.Unmarshal([]byte(line[len(consoleMark):]), &m) != nil {
		return line
	}
	args := make([]any, len(m.Args))
	for i, a := range m.Args {
		if n, err := strconv.ParseInt(strings.TrimPrefix(a, "\x01"), 10, 64); err == nil && strings.HasPrefix(a, "\x01") {
			args[i] = humanBytes(lang, n)
		} else {
			args[i] = a
		}
	}
	txt := tr(lang, m.Msg, args...)
	if m.Err {
		return "\x1b[38;5;203m[KubePilot] " + txt + "\x1b[0m"
	}
	return "\x1b[38;5;104m[KubePilot]\x1b[0m \x1b[38;5;250m" + txt + "\x1b[0m"
}

func humanBytes(lang string, n int64) string {
	units := []string{"%.1f B", "%.1f KB", "%.1f MB", "%.1f GB", "%.1f TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return tr(lang, units[i], f)
}
