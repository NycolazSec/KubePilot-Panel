package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// userMessages returns every message literal that can reach a user: errCode(code, "…"), daemon("…"),
// daemonErr("…"), tr(lang, "…") and errors.New("…") (except in the admin CLI).
func userMessages(t *testing.T) map[string]string {
	t.Helper()
	files, _ := filepath.Glob("*.go")
	msgs := map[string]string{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "main.go" {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			arg := -1
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				switch fn.Name {
				case "errCode", "tr":
					arg = 1
				case "warn":
					arg = 0
				}
			case *ast.SelectorExpr:
				switch {
				case fn.Sel.Name == "daemon" || fn.Sel.Name == "daemonErr":
					arg = 0
				case fn.Sel.Name == "New" && isIdent(fn.X, "errors"):
					arg = 0
				}
			}
			if arg < 0 || len(call.Args) <= arg {
				return true
			}
			if lit, ok := call.Args[arg].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s, err := strconv.Unquote(lit.Value)
				if err == nil {
					msgs[s] = fset.Position(lit.Pos()).String()
				}
			}
			return true
		})
	}
	return msgs
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

var verbRe = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

func TestEveryMessageIsTranslated(t *testing.T) {
	msgs := userMessages(t)
	if len(msgs) < 50 {
		t.Fatalf("only %d messages found: is the parser still matching the helpers?", len(msgs))
	}
	for lang, cat := range catalogs {
		for msg, pos := range msgs {
			if cat[msg] == "" {
				t.Errorf("%s: no %q translation for %q", pos, lang, msg)
			}
		}
	}
}

func TestTranslationsKeepFormatVerbs(t *testing.T) {
	for lang, cat := range catalogs {
		for en, tr := range cat {
			if a, b := verbRe.FindAllString(en, -1), verbRe.FindAllString(tr, -1); !slices.Equal(a, b) {
				t.Errorf("%s: %q has verbs %v but its translation %q has %v", lang, en, a, tr, b)
			}
		}
	}
}

func TestLangOf(t *testing.T) {
	cases := []struct {
		header, query, accept, want string
	}{
		{"", "", "", "en"},
		{"fr", "", "en-US", "fr"},
		{"", "fr", "", "fr"},
		{"", "", "fr-FR,fr;q=0.9,en;q=0.8", "fr"},
		{"", "", "de-DE,de;q=0.9,fr;q=0.5", "fr"},
		{"", "", "de-DE", "en"},
		{"xx", "", "fr", "fr"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/?lang="+c.query, nil)
		if c.header != "" {
			r.Header.Set("X-KubePilot-Lang", c.header)
		}
		if c.accept != "" {
			r.Header.Set("Accept-Language", c.accept)
		}
		if got := langOf(r); got != c.want {
			t.Errorf("langOf(header=%q, query=%q, accept=%q) = %q, want %q", c.header, c.query, c.accept, got, c.want)
		}
	}
}

func TestConsoleMessages(t *testing.T) {
	line := encodeConsoleMsg(false, "Backup completed ✔ (%s)", bytesArg(3<<20))
	if got := renderConsoleLine("en", line); !strings.Contains(got, "Backup completed ✔ (3.0 MB)") {
		t.Errorf("en: %q", got)
	}
	if got := renderConsoleLine("fr", line); !strings.Contains(got, "Sauvegarde terminée ✔ (3.0 Mo)") {
		t.Errorf("fr: %q", got)
	}
	if got := renderConsoleLine("fr", "plain game output"); got != "plain game output" {
		t.Errorf("game output must be left untouched, got %q", got)
	}
	errLine := renderConsoleLine("fr", encodeConsoleMsg(true, "The server crashed: %s", "OOMKilled"))
	if !strings.Contains(errLine, "Le serveur a planté : OOMKilled") || !strings.Contains(errLine, "38;5;203m") {
		t.Errorf("error line: %q", errLine)
	}
}

func TestTranslatedAPIError(t *testing.T) {
	err := errCode(409, "Backup limit of %d reached: delete one first", 3)
	if got := trErr("fr", err); got != "Limite de 3 sauvegardes atteinte : supprimes-en une" {
		t.Errorf("fr: %q", got)
	}
	if got := err.Error(); got != "Backup limit of 3 reached: delete one first" {
		t.Errorf("Error(): %q", got)
	}
}
