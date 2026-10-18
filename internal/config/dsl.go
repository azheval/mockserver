package config

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// LoadDSL parses the Mock Integration Server scenario language. The grammar is
// intentionally independent from YAML and does not use indentation as syntax.
func LoadDSL(data []byte, source string) ([]Route, []Scenario, error) {
	p := dslParser{src: string(data), source: source, line: 1, col: 1}
	p.next()
	var routes []Route
	var scenarios []Scenario
	var bindings = map[string]string{}
	for p.tok.kind != tokEOF {
		kind := p.tok.text
		if kind != "route" && kind != "scenario" {
			return nil, nil, p.errorf("expected 'route' or 'scenario', got %q", kind)
		}
		p.next()
		if p.tok.kind != tokString && p.tok.kind != tokIdent {
			return nil, nil, p.errorf("expected declaration name after %s", kind)
		}
		name := p.tok.text
		p.next()
		v, err := p.parseObject()
		if err != nil {
			return nil, nil, err
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, nil, p.errorf("%s body must be an object", kind)
		}
		if kind == "route" {
			if err := ensureDSLKeys(m, "protocol", "path", "methods", "scenarios"); err != nil {
				return nil, nil, fmt.Errorf("%s: route %q: %w", source, name, err)
			}
			m["id"] = name
			b, _ := yaml.Marshal(m)
			var r Route
			if err := yaml.Unmarshal(b, &r); err != nil {
				return nil, nil, fmt.Errorf("%s: route %q: %w", source, name, err)
			}
			routes = append(routes, r)
		} else {
			if err := ensureDSLKeys(m, "route", "priority", "when", "match", "actions", "state"); err != nil {
				return nil, nil, fmt.Errorf("%s: scenario %q: %w", source, name, err)
			}
			if rawRoute, exists := m["route"]; exists {
				route, ok := rawRoute.(string)
				if !ok || route == "" {
					return nil, nil, fmt.Errorf("%s: scenario %q: route must be a non-empty string", source, name)
				}
				bindings[name] = route
				delete(m, "route")
			}
			if when, ok := m["when"]; ok {
				if _, already := m["match"]; already {
					return nil, nil, fmt.Errorf("%s: scenario %q cannot define both when and match", source, name)
				}
				m["match"] = when
				delete(m, "when")
			}
			if match, ok := m["match"].(map[string]any); ok {
				if err := ensureDSLKeys(match, "headers", "query", "path_params", "json", "xml", "body_sha256", "body_size", "body_regex", "body_equals"); err != nil {
					return nil, nil, fmt.Errorf("%s: scenario %q match: %w", source, name, err)
				}
				for _, field := range []string{"headers", "query", "path_params", "json", "xml"} {
					if group, ok := match[field].(map[string]any); ok {
						for key, cond := range group {
							if cm, ok := cond.(map[string]any); ok {
								if err := ensureDSLKeys(cm, "equals", "not_equals", "contains", "exists", "regex", "one_of", "type", "gt", "gte", "lt", "lte"); err != nil {
									return nil, nil, fmt.Errorf("%s: scenario %q match %s %q: %w", source, name, field, key, err)
								}
							}
						}
					}
				}
			}
			if actions, ok := m["actions"].([]any); ok {
				out := make([]any, 0, len(actions))
				for i, action := range actions {
					a, err := normalizeDSLAction(action)
					if err != nil {
						return nil, nil, fmt.Errorf("%s: scenario %q action %d: %w", source, name, i+1, err)
					}
					out = append(out, a)
				}
				m["actions"] = out
			}
			m["id"] = name
			b, _ := yaml.Marshal(m)
			var s Scenario
			if err := yaml.Unmarshal(b, &s); err != nil {
				return nil, nil, fmt.Errorf("%s: scenario %q: %w", source, name, err)
			}
			scenarios = append(scenarios, s)
		}
	}
	for sid, rid := range bindings {
		found := false
		for i := range routes {
			if routes[i].ID == rid {
				routes[i].Scenarios = append(routes[i].Scenarios, sid)
				found = true
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("%s: scenario %q references unknown route %q", source, sid, rid)
		}
	}
	return routes, scenarios, nil
}

func ensureDSLKeys(m map[string]any, allowed ...string) error {
	set := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		set[k] = true
	}
	for k := range m {
		if !set[k] {
			return fmt.Errorf("unknown property %q", k)
		}
	}
	return nil
}

func normalizeDSLAction(raw any) (map[string]any, error) {
	m, ok := raw.(map[string]any)
	if !ok || len(m) != 1 {
		return nil, fmt.Errorf("each action must be written as one action block, e.g. respond { status: 200 }")
	}
	for name, val := range m {
		args, ok := val.(map[string]any)
		if !ok {
			args = map[string]any{"value": val}
		}
		var allowed []string
		switch name {
		case "respond":
			allowed = []string{"status", "headers", "json", "text", "file", "base64", "empty", "body"}
		case "delay":
			allowed = []string{"duration", "value"}
		case "abort":
			allowed = []string{}
		case "set_state", "capture", "assert":
			allowed = []string{"key", "value"}
		case "sequence":
			allowed = []string{"cycle", "responses"}
		case "ws_expect", "ws_send":
			allowed = []string{"delay", "timeout", "async", "json", "text", "file", "base64", "empty", "body"}
		case "ws_close":
			allowed = []string{"code", "reason"}
		default:
			return nil, fmt.Errorf("unknown action %q", name)
		}
		if err := ensureDSLKeys(args, allowed...); err != nil {
			return nil, fmt.Errorf("action %s: %w", name, err)
		}
		out := map[string]any{"type": name}
		switch name {
		case "respond":
			copyIf(args, out, "status", "headers")
			body, err := dslBody(args)
			if err != nil {
				return nil, err
			}
			if body != nil {
				out["body"] = body
			}
		case "delay":
			v := args["duration"]
			if v == nil {
				v = args["value"]
			}
			if v == nil {
				return nil, fmt.Errorf("delay requires duration, e.g. delay { duration: \"250ms\" }")
			}
			out["delay"] = v
		case "abort":
		case "set_state", "capture", "assert":
			copyIf(args, out, "key", "value")
		case "sequence":
			copyIf(args, out, "cycle")
			steps, _ := args["responses"].([]any)
			var responses []any
			for _, step := range steps {
				sm, ok := step.(map[string]any)
				if !ok || len(sm) != 1 || sm["respond"] == nil {
					return nil, fmt.Errorf("sequence responses must use respond blocks")
				}
				rm, ok := sm["respond"].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("respond step must be an object")
				}
				resp := map[string]any{}
				copyIf(rm, resp, "status", "headers")
				body, err := dslBody(rm)
				if err != nil {
					return nil, err
				}
				if body != nil {
					resp["body"] = body
				}
				responses = append(responses, resp)
			}
			out["responses"] = responses
		case "ws_expect", "ws_send":
			copyIf(args, out, "delay", "timeout", "async")
			msg, err := dslBody(args)
			if err != nil {
				return nil, err
			}
			if msg != nil {
				out["message"] = msg
			}
		case "ws_close":
			copyIf(args, out, "code", "reason")
		default:
			return nil, fmt.Errorf("unknown action %q", name)
		}
		return out, nil
	}
	return nil, fmt.Errorf("empty action")
}

func dslBody(m map[string]any) (map[string]any, error) {
	found := 0
	for _, typ := range []string{"json", "text", "file", "base64", "empty", "body"} {
		if _, ok := m[typ]; ok {
			found++
		}
	}
	if found > 1 {
		return nil, fmt.Errorf("specify only one of json, text, file, base64, empty, or body")
	}
	for _, typ := range []string{"json", "text", "file", "base64", "empty"} {
		v, exists := m[typ]
		if !exists {
			continue
		}
		body := map[string]any{"type": typ}
		switch typ {
		case "json":
			body["value"] = v
		case "text":
			if vm, ok := v.(map[string]any); ok {
				copyIf(vm, body, "value", "template")
			} else {
				body["template"] = v
			}
		case "file":
			body["path"] = v
		case "base64":
			body["value"] = v
		case "empty":
		}
		return body, nil
	}
	if b, ok := m["body"].(map[string]any); ok {
		return b, nil
	}
	return nil, nil
}
func copyIf(from, to map[string]any, keys ...string) {
	for _, k := range keys {
		if v, ok := from[k]; ok {
			to[k] = v
		}
	}
}

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString
	tokNumber
	tokPunct
)

type dslToken struct {
	kind      tokenKind
	text      string
	line, col int
}
type dslParser struct {
	src, source    string
	pos, line, col int
	tok            dslToken
}

func (p *dslParser) next() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '\n' {
			p.pos++
			p.line++
			p.col = 1
			continue
		}
		if unicode.IsSpace(rune(c)) {
			p.pos++
			p.col++
			continue
		}
		if c == '#' || (c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '/') {
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
				p.col++
			}
			continue
		}
		break
	}
	if p.pos >= len(p.src) {
		p.tok = dslToken{kind: tokEOF, line: p.line, col: p.col}
		return
	}
	start, line, col := p.pos, p.line, p.col
	c := p.src[p.pos]
	if c == '"' || c == '\'' {
		quote := c
		p.pos++
		p.col++
		var b strings.Builder
		for p.pos < len(p.src) {
			ch := p.src[p.pos]
			p.pos++
			p.col++
			if ch == quote {
				p.tok = dslToken{tokString, b.String(), line, col}
				return
			}
			if ch == '\\' && p.pos < len(p.src) {
				n := p.src[p.pos]
				p.pos++
				p.col++
				switch n {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(n)
				}
			} else {
				b.WriteByte(ch)
			}
		}
		p.tok = dslToken{tokString, p.src[start+1:], line, col}
		return
	}
	if strings.ContainsRune("{}[]:,", rune(c)) {
		p.pos++
		p.col++
		p.tok = dslToken{tokPunct, string(c), line, col}
		return
	}
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if unicode.IsSpace(rune(ch)) || strings.ContainsRune("{}[]:,\"'", rune(ch)) || ch == '#' {
			break
		}
		p.pos++
		p.col++
	}
	word := p.src[start:p.pos]
	if _, err := strconv.ParseFloat(word, 64); err == nil {
		p.tok = dslToken{tokNumber, word, line, col}
	} else {
		p.tok = dslToken{tokIdent, word, line, col}
	}
}
func (p *dslParser) errorf(f string, a ...any) error {
	return fmt.Errorf("%s:%d:%d: %s", p.source, p.tok.line, p.tok.col, fmt.Sprintf(f, a...))
}
func (p *dslParser) expect(s string) error {
	if p.tok.text != s {
		return p.errorf("expected %q, got %q", s, p.tok.text)
	}
	p.next()
	return nil
}
func (p *dslParser) parseObject() (any, error) {
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	m := map[string]any{}
	for p.tok.kind != tokEOF && p.tok.text != "}" {
		if p.tok.kind != tokIdent && p.tok.kind != tokString {
			return nil, p.errorf("expected property name, got %q", p.tok.text)
		}
		key := p.tok.text
		p.next()
		if err := p.expect(":"); err != nil {
			return nil, err
		}
		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		if _, exists := m[key]; exists {
			return nil, p.errorf("duplicate property %q", key)
		}
		m[key] = v
		if p.tok.text == "," {
			p.next()
		}
	}
	if err := p.expect("}"); err != nil {
		return nil, err
	}
	return m, nil
}
func (p *dslParser) parseValue() (any, error) {
	switch p.tok.kind {
	case tokString:
		v := p.tok.text
		p.next()
		return v, nil
	case tokNumber:
		v, err := strconv.ParseFloat(p.tok.text, 64)
		p.next()
		if err != nil {
			return nil, err
		}
		if v == float64(int64(v)) {
			return int64(v), nil
		}
		return v, nil
	case tokPunct:
		switch p.tok.text {
		case "{":
			return p.parseObject()
		case "[":
			p.next()
			var a []any
			for p.tok.kind != tokEOF && p.tok.text != "]" {
				v, err := p.parseValue()
				if err != nil {
					return nil, err
				}
				a = append(a, v)
				if p.tok.text == "," {
					p.next()
				} else if p.tok.text != "]" && p.tok.kind == tokEOF {
					return nil, p.errorf("unterminated array")
				}
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			return a, nil
		}
	case tokIdent:
		word := p.tok.text
		p.next()
		switch word {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		}
		if p.tok.text == "{" {
			v, err := p.parseObject()
			if err != nil {
				return nil, err
			}
			return map[string]any{word: v}, nil
		}
		return word, nil
	}
	return nil, p.errorf("expected a value, got %q", p.tok.text)
}
