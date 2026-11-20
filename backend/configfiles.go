package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// Configuration files edited before each start, like Wings does for eggs ("config.files"): the server's
// port, IP, name, password… are written into the game's own files from the server's variables.

type ConfigFile struct {
	File   string          `json:"file"`   // relative to the data folder
	Parser string          `json:"parser"` // properties, ini, json, yaml, xml, file
	Find   []ConfigReplace `json:"find"`
}

type ConfigReplace struct {
	Match string `json:"match"` // key (dot path for json/yaml/xml, "section.key" for ini, line prefix for file)
	// Value: a string with {{placeholders}}, or a JSON number / boolean
	Value json.RawMessage `json:"value"`
	// IfValue: only when the current value contains this text, which is then replaced by Value
	IfValue string `json:"ifValue,omitempty"`
}

var configParsers = []string{"properties", "ini", "json", "yaml", "xml", "file"}

const maxConfigFileSize = 4 << 20

func validateConfigFiles(files []ConfigFile) error {
	if len(files) > 20 {
		return errCode(400, "20 configuration files at most")
	}
	for _, f := range files {
		if f.File == "" || path.IsAbs(f.File) || rel(f.File) != path.Clean(f.File) || strings.HasPrefix(path.Clean(f.File), "..") || rel(f.File) == "." {
			return errCode(400, "Invalid configuration file: %s", f.File)
		}
		if !slices.Contains(configParsers, f.Parser) {
			return errCode(400, "Unknown parser for %s: %s", f.File, f.Parser)
		}
		if len(f.Find) == 0 || len(f.Find) > 100 {
			return errCode(400, "Invalid configuration file: %s", f.File)
		}
		for _, r := range f.Find {
			if r.Match == "" || len(r.Match) > 200 || len(r.Value) > 1000 || len(r.IfValue) > 200 || !json.Valid(r.Value) {
				return errCode(400, "Invalid configuration file: %s", f.File)
			}
		}
	}
	return nil
}

// ---------- Placeholders

var cfgPlaceholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.\-]+)\s*\}\}`)

// resolveConfigValue fills the Wings placeholders from the server's variables; ok is false when one is unknown.
func resolveConfigValue(s string, vars map[string]string) (string, bool) {
	ok := true
	out := cfgPlaceholderRe.ReplaceAllStringFunc(s, func(m string) string {
		name := cfgPlaceholderRe.FindStringSubmatch(m)[1]
		var v string
		var found bool
		switch {
		case name == "server.build.default.port" || name == "server.allocations.default.port":
			v, found = vars["SERVER_PORT"]
		case name == "server.build.default.ip" || name == "server.allocations.default.ip":
			v, found = "0.0.0.0", true
		case name == "server.build.memory":
			v, found = vars["SERVER_MEMORY"]
		case strings.HasPrefix(name, "server.build.env."):
			v, found = vars[strings.TrimPrefix(name, "server.build.env.")]
		case strings.HasPrefix(name, "server.environment."):
			v, found = vars[strings.TrimPrefix(name, "server.environment.")]
		case strings.HasPrefix(name, "env."):
			v, found = vars[strings.TrimPrefix(name, "env.")]
		case !strings.Contains(name, "."):
			v, found = vars[name]
		}
		if !found {
			ok = false
		}
		return v
	})
	return out, ok
}

// replacement is one resolved edit.
type replacement struct {
	match, ifValue string
	value          string
	literal        any // json.Number or bool when the egg gave a non-string value
}

func resolveReplacements(f ConfigFile, vars map[string]string) ([]replacement, []string) {
	out, skipped := []replacement{}, []string{}
	for _, r := range f.Find {
		var lit any
		var s string
		if err := json.Unmarshal(r.Value, &s); err != nil {
			dec := json.NewDecoder(bytes.NewReader(r.Value))
			dec.UseNumber()
			if dec.Decode(&lit) != nil {
				skipped = append(skipped, r.Match)
				continue
			}
			s = fmt.Sprint(lit)
		}
		v, ok := resolveConfigValue(s, vars)
		if !ok {
			skipped = append(skipped, r.Match)
			continue
		}
		out = append(out, replacement{match: r.Match, ifValue: r.IfValue, value: v, literal: lit})
	}
	return out, skipped
}

// ---------- Applying

// applyConfigFiles edits the files and returns the ones that changed.
func applyConfigFiles(root *os.Root, uid int, files []ConfigFile, vars map[string]string) (changed []string, errs []error) {
	for _, f := range files {
		p := rel(f.File)
		reps, _ := resolveReplacements(f, vars)
		if len(reps) == 0 {
			continue
		}
		var old []byte
		if fh, st, err := openRegular(root, p); err == nil {
			if st.IsDir() || st.Size() > maxConfigFileSize {
				fh.Close()
				errs = append(errs, fmt.Errorf("%s: not a configuration file", f.File))
				continue
			}
			old, err = io.ReadAll(fh)
			fh.Close()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", f.File, err))
				continue
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("%s: %w", f.File, err))
			continue
		} else if f.Parser != "properties" && f.Parser != "ini" {
			// structured files (json, yaml, xml) and line files are written by the game on its first start: building
			// one from a few keys could give it a wrong structure (a list made an object). Edited from the next start.
			continue
		}
		updated, err := editConfig(f.Parser, old, reps)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.File, err))
			continue
		}
		if bytes.Equal(updated, old) {
			continue
		}
		if dir := path.Dir(p); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", f.File, err))
				continue
			}
		}
		fh, err := createRegular(root, p, 0o644)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.File, err))
			continue
		}
		_, err = fh.Write(updated)
		if cerr := fh.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.File, err))
			continue
		}
		chownNew(root, p, uid)
		changed = append(changed, f.File)
	}
	return changed, errs
}

func editConfig(parser string, b []byte, reps []replacement) ([]byte, error) {
	switch parser {
	case "properties":
		return editProperties(b, reps), nil
	case "ini":
		return editINI(b, reps), nil
	case "file":
		return editLines(b, reps), nil
	case "json":
		return editJSON(b, reps)
	case "yaml":
		return editYAML(b, reps)
	case "xml":
		return editXML(b, reps)
	}
	return nil, fmt.Errorf("unknown parser %q", parser)
}

// newValue applies IfValue (substring replacement); ok is false when the condition doesn't hold.
func newValue(cur string, exists bool, r replacement) (string, bool) {
	if r.ifValue == "" {
		return r.value, true
	}
	if !exists || !strings.Contains(cur, r.ifValue) {
		return "", false
	}
	return strings.ReplaceAll(cur, r.ifValue, r.value), true
}

func splitLines(b []byte) ([]string, string) {
	nl := "\n"
	if bytes.Contains(b, []byte("\r\n")) {
		nl = "\r\n"
	}
	s := strings.TrimSuffix(string(b), nl)
	if s == "" {
		return []string{}, nl
	}
	return strings.Split(s, nl), nl
}

func joinLines(lines []string, nl string) []byte {
	if len(lines) == 0 {
		return []byte{}
	}
	return []byte(strings.Join(lines, nl) + nl)
}

// ---------- properties (key=value, key: value), comments kept, missing keys appended

func editProperties(b []byte, reps []replacement) []byte {
	lines, nl := splitLines(b)
	for _, r := range reps {
		found := false
		for i, line := range lines {
			t := strings.TrimLeft(line, " \t")
			if t == "" || t[0] == '#' || t[0] == '!' {
				continue
			}
			end := strings.IndexAny(t, "=:")
			if end < 0 || strings.TrimSpace(t[:end]) != r.match {
				continue
			}
			found = true
			v, ok := newValue(strings.TrimSpace(t[end+1:]), true, r)
			if ok {
				lines[i] = r.match + "=" + v
			}
		}
		if !found && r.ifValue == "" {
			lines = append(lines, r.match+"="+r.value)
		}
	}
	return joinLines(lines, nl)
}

// ---------- file: a line starting with the match is replaced as a whole (Wings' "file" parser)

func editLines(b []byte, reps []replacement) []byte {
	lines, nl := splitLines(b)
	for i, line := range lines {
		for _, r := range reps {
			if strings.HasPrefix(line, r.match) {
				lines[i] = r.value
			}
		}
	}
	return joinLines(lines, nl)
}

// ---------- ini: "section.key" (no dot = before the first section), layout and comments kept

func editINI(b []byte, reps []replacement) []byte {
	lines, nl := splitLines(b)
	for _, r := range reps {
		section, key := "", r.match
		if s, k, ok := strings.Cut(r.match, "."); ok {
			section, key = s, k
		}
		cur, found, last := "", false, -1 // last: index after which a missing key goes
		inSection := section == ""
		if inSection {
			last = -1
		}
		for i, line := range lines {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
				cur = strings.TrimSpace(t[1 : len(t)-1])
				inSection = cur == section
				if inSection {
					last = i
				}
				continue
			}
			if !inSection || t == "" || t[0] == ';' || t[0] == '#' {
				continue
			}
			k, v, ok := strings.Cut(t, "=")
			if !ok {
				continue
			}
			last = i
			if strings.TrimSpace(k) != key {
				continue
			}
			found = true
			if nv, ok := newValue(strings.TrimSpace(v), true, r); ok {
				sep := "="
				if strings.Contains(line, " = ") {
					sep = " = "
				}
				indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				lines[i] = indent + key + sep + nv
			}
		}
		if found || r.ifValue != "" {
			continue
		}
		entry := key + "=" + r.value
		switch {
		case section != "" && last < 0: // the section doesn't exist yet
			if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
				lines = append(lines, "")
			}
			lines = append(lines, "["+section+"]", entry)
		default:
			lines = slices.Insert(lines, last+1, entry)
		}
	}
	return joinLines(lines, nl)
}

// ---------- json / yaml paths: "a.b.0.c", "a[0].c", "a.*.c"

func configPath(match string) []string {
	m := regexp.MustCompile(`\[(\d+|\*)\]`).ReplaceAllString(match, ".$1")
	return strings.Split(strings.Trim(m, "."), ".")
}

func isIndex(seg string) bool {
	_, err := strconv.Atoi(seg)
	return err == nil || seg == "*"
}

// typed returns the value to write in json/yaml, keeping the type of the current value when there is one.
func typed(v string, r replacement, cur any, exists bool) any {
	if r.literal != nil && r.ifValue == "" {
		return r.literal
	}
	if exists {
		switch cur.(type) {
		case string:
			return v
		case bool:
			if b, err := strconv.ParseBool(v); err == nil {
				return b
			}
		case json.Number:
			if _, err := strconv.ParseFloat(v, 64); err == nil {
				return json.Number(v)
			}
		}
		return v
	}
	if b, err := strconv.ParseBool(v); err == nil && (v == "true" || v == "false") {
		return b
	}
	if regexp.MustCompile(`^-?(0|[1-9][0-9]{0,15})$`).MatchString(v) {
		return json.Number(v)
	}
	return v
}

// ---------- json (key order kept)

type jsonObj struct {
	keys []string
	m    map[string]any
}

func decodeJSONValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &jsonObj{m: map[string]any{}}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				v, err := decodeJSONValue(dec)
				if err != nil {
					return nil, err
				}
				if _, dup := o.m[k]; !dup {
					o.keys = append(o.keys, k)
				}
				o.m[k] = v
			}
			_, err := dec.Token()
			return o, err
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeJSONValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			_, err := dec.Token()
			return &arr, err
		}
	}
	return tok, nil
}

func encodeJSONValue(buf *bytes.Buffer, v any) {
	switch t := v.(type) {
	case *jsonObj:
		buf.WriteByte('{')
		for i, k := range t.keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			encodeJSONValue(buf, k)
			buf.WriteByte(':')
			encodeJSONValue(buf, t.m[k])
		}
		buf.WriteByte('}')
	case *[]any:
		buf.WriteByte('[')
		for i, e := range *t {
			if i > 0 {
				buf.WriteByte(',')
			}
			encodeJSONValue(buf, e)
		}
		buf.WriteByte(']')
	default:
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(t)
		buf.Truncate(buf.Len() - 1) // Encode's newline
	}
}

func setJSONPath(node any, segs []string, r replacement) any {
	if len(segs) == 0 {
		cur, isStr := node.(string)
		if !isStr {
			cur = fmt.Sprint(node)
		}
		v, ok := newValue(cur, node != nil, r)
		if !ok {
			return node
		}
		return typed(v, r, node, node != nil)
	}
	seg, rest := segs[0], segs[1:]
	switch t := node.(type) {
	case *jsonObj:
		if seg == "*" {
			for _, k := range t.keys {
				t.m[k] = setJSONPath(t.m[k], rest, r)
			}
			return t
		}
		child, ok := t.m[seg]
		if !ok {
			if r.ifValue != "" || (len(rest) > 0 && isIndex(rest[0])) {
				return t // never invent a list: its items are the game's to create
			}
			t.keys = append(t.keys, seg)
			if len(rest) > 0 {
				child = &jsonObj{m: map[string]any{}}
			}
		}
		t.m[seg] = setJSONPath(child, rest, r)
		return t
	case *[]any:
		if seg == "*" {
			for i := range *t {
				(*t)[i] = setJSONPath((*t)[i], rest, r)
			}
		} else if i, err := strconv.Atoi(seg); err == nil && i >= 0 && i < len(*t) {
			(*t)[i] = setJSONPath((*t)[i], rest, r)
		}
		return t
	case nil:
		if r.ifValue != "" || seg == "*" {
			return nil
		}
		o := &jsonObj{m: map[string]any{}}
		return setJSONPath(o, segs, r)
	}
	return node // a scalar on the way: leave it
}

func editJSON(b []byte, reps []replacement) ([]byte, error) {
	var root any = &jsonObj{m: map[string]any{}}
	if len(bytes.TrimSpace(b)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		v, err := decodeJSONValue(dec)
		if err != nil {
			return nil, fmt.Errorf("invalid JSON")
		}
		root = v
	}
	for _, r := range reps {
		root = setJSONPath(root, configPath(r.match), r)
	}
	var raw, out bytes.Buffer
	encodeJSONValue(&raw, root)
	indent := "  "
	if m := regexp.MustCompile(`\n([ \t]+)\S`).FindSubmatch(b); m != nil {
		indent = string(m[1])
	} else if len(bytes.TrimSpace(b)) > 0 && !bytes.Contains(bytes.TrimSpace(b), []byte("\n")) {
		return append(raw.Bytes(), '\n'), nil // the file was on one line
	}
	if err := json.Indent(&out, raw.Bytes(), "", indent); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// ---------- yaml (comments and order kept)

var yaml11Bool = regexp.MustCompile(`(?i)^(y|n|yes|no|on|off)$`)

func yamlScalar(node *yaml.Node, v string, r replacement) {
	var cur any
	exists := node.Kind == yaml.ScalarNode && node.Tag != "" && node.Tag != "!!null"
	if exists {
		switch node.Tag {
		case "!!int", "!!float":
			cur = json.Number(node.Value)
		case "!!bool":
			cur, _ = strconv.ParseBool(node.Value)
		default:
			cur = node.Value
		}
	}
	val := typed(v, r, cur, exists)
	node.Kind, node.Content, node.Alias = yaml.ScalarNode, nil, nil
	switch t := val.(type) {
	case bool:
		node.Tag, node.Value, node.Style = "!!bool", strconv.FormatBool(t), 0
	case json.Number:
		node.Tag, node.Value, node.Style = "!!int", t.String(), 0
		if strings.ContainsAny(t.String(), ".eE") {
			node.Tag = "!!float"
		}
	default:
		node.Tag, node.Value = "!!str", fmt.Sprint(t)
		if node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			node.Style = 0
		}
		// YAML 1.1 readers (SnakeYAML in Java games) take on/off/yes/no/y/n for booleans
		if yaml11Bool.MatchString(node.Value) {
			node.Style = yaml.DoubleQuotedStyle
		}
	}
}

func setYAMLPath(node *yaml.Node, segs []string, r replacement) {
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		setYAMLPath(node.Content[0], segs, r)
		return
	}
	if len(segs) == 0 {
		cur := node.Value
		v, ok := newValue(cur, node.Kind == yaml.ScalarNode && node.Tag != "!!null", r)
		if ok {
			yamlScalar(node, v, r)
		}
		return
	}
	seg, rest := segs[0], segs[1:]
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if seg == "*" || node.Content[i].Value == seg {
				setYAMLPath(node.Content[i+1], rest, r)
				if seg != "*" {
					return
				}
			}
		}
		if seg == "*" || r.ifValue != "" || (len(rest) > 0 && isIndex(rest[0])) {
			return
		}
		child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if len(rest) == 0 {
			child = &yaml.Node{Kind: yaml.ScalarNode}
		}
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seg}, child)
		setYAMLPath(child, rest, r)
	case yaml.SequenceNode:
		if seg == "*" {
			for _, c := range node.Content {
				setYAMLPath(c, rest, r)
			}
		} else if i, err := strconv.Atoi(seg); err == nil && i >= 0 && i < len(node.Content) {
			setYAMLPath(node.Content[i], rest, r)
		}
	}
}

func editYAML(b []byte, reps []replacement) ([]byte, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(b)) > 0 {
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("invalid YAML")
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	for _, r := range reps {
		setYAMLPath(&doc, configPath(r.match), r)
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	enc.Close()
	return out.Bytes(), nil
}

// ---------- xml: element text by path ("Root.Child.Leaf"), edited in place (the rest of the file is untouched)

func editXML(b []byte, reps []replacement) ([]byte, error) {
	type splice struct {
		from, to int
		text     string
	}
	type open struct {
		start, afterStart int
		children          bool
		text              string
	}
	var splices []splice
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	var stack []string
	var opens []*open
	for {
		before := int(dec.InputOffset())
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid XML")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(opens) > 0 {
				opens[len(opens)-1].children = true
			}
			stack = append(stack, t.Name.Local)
			opens = append(opens, &open{start: before, afterStart: int(dec.InputOffset())})
		case xml.CharData:
			if len(opens) > 0 {
				opens[len(opens)-1].text += string(t)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("invalid XML")
			}
			o := opens[len(opens)-1]
			p := strings.Join(stack, ".")
			for _, r := range reps {
				if r.match != p || o.children {
					continue
				}
				v, ok := newValue(strings.TrimSpace(o.text), true, r)
				if !ok {
					continue
				}
				var esc bytes.Buffer
				_ = xml.EscapeText(&esc, []byte(v))
				if selfClosing := o.afterStart >= 2 && string(b[o.afterStart-2:o.afterStart]) == "/>"; selfClosing {
					name := t.Name.Local
					if t.Name.Space != "" {
						name = t.Name.Space + ":" + name
					}
					tag := strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(string(b[o.start:o.afterStart]), "/>")), "/")
					splices = append(splices, splice{o.start, o.afterStart, tag + ">" + esc.String() + "</" + name + ">"})
				} else {
					splices = append(splices, splice{o.afterStart, before, esc.String()})
				}
			}
			stack, opens = stack[:len(stack)-1], opens[:len(opens)-1]
		}
	}
	out := slices.Clone(b)
	for i := len(splices) - 1; i >= 0; i-- {
		s := splices[i]
		out = slices.Concat(out[:s.from], []byte(s.text), out[s.to:])
	}
	return out, nil
}

// ---------- Before each start

// applyServerConfig writes the template's configuration files from the server's current variables.
func (a *App) applyServerConfig(ctx context.Context, sp Spec) {
	if len(sp.Template.ConfigFiles) == 0 {
		return
	}
	root, uid, err := a.openRoot(ctx, sp.Server)
	if err != nil {
		return // no volume yet: the files will be written at the next start
	}
	defer root.Close()
	vars := map[string]string{}
	for _, e := range sp.env() {
		vars[e.Name] = strings.ReplaceAll(e.Value, "$$", "$")
	}
	changed, errs := applyConfigFiles(root, uid, sp.Template.ConfigFiles, vars)
	h := a.hub(sp.Server.ID)
	if len(changed) > 0 {
		h.daemon("Configuration updated: %s", strings.Join(changed, ", "))
	}
	for _, err := range errs {
		h.daemonErr("Configuration file not updated: %s", err.Error())
	}
}
