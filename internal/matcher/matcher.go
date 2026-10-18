package matcher

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"mock-integration-server/internal/config"
)

type Request struct {
	Method     string
	URL        *url.URL
	Header     http.Header
	Body       []byte
	PathParams map[string]string
	TestRunID  string
}
type Result struct {
	Route    config.Route
	Scenario config.Scenario
}
type Matcher struct{ cfg *config.Config }

func New(c *config.Config) *Matcher { return &Matcher{cfg: c} }
func (m *Matcher) Match(req Request, protocol string) (*Result, error) {
	var candidates []Result
	for _, r := range m.cfg.Routes {
		if r.Protocol != protocol {
			continue
		}
		params, pathOK := extractPathParams(r.Path, req.URL.Path)
		if !pathOK {
			continue
		}
		localReq := req
		localReq.PathParams = params
		if len(r.Methods) > 0 && !containsFold(r.Methods, req.Method) {
			continue
		}
		for _, id := range r.Scenarios {
			for _, s := range m.cfg.Scenarios {
				if s.ID != id {
					continue
				}
				ok, err := matchScenario(s.Match, localReq)
				if err != nil {
					return nil, fmt.Errorf("scenario %s: %w", s.ID, err)
				}
				if ok {
					candidates = append(candidates, Result{r, s})
				}
			}
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Scenario.Priority > candidates[j].Scenario.Priority })
	if len(candidates) > 1 && candidates[0].Scenario.Priority == candidates[1].Scenario.Priority {
		return nil, fmt.Errorf("ambiguous scenarios %q and %q", candidates[0].Scenario.ID, candidates[1].Scenario.ID)
	}
	return &candidates[0], nil
}
func pathMatch(pattern, path string) bool { _, ok := extractPathParams(pattern, path); return ok }
func extractPathParams(pattern, path string) (map[string]string, bool) {
	pp := strings.Split(strings.Trim(pattern, "/"), "/")
	ap := strings.Split(strings.Trim(path, "/"), "/")
	if len(pp) != len(ap) {
		return nil, false
	}
	params := map[string]string{}
	for i, p := range pp {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			params[strings.TrimSuffix(strings.TrimPrefix(p, "{"), "}")] = ap[i]
			continue
		}
		if p != ap[i] {
			return nil, false
		}
	}
	return params, true
}
func containsFold(a []string, s string) bool {
	for _, v := range a {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
func matchScenario(m config.Match, r Request) (bool, error) {
	for k, c := range m.PathParams {
		v, ok := r.PathParams[k]
		if c.Exists != nil && ok != *c.Exists {
			return false, nil
		}
		if (c.EqualsSet || c.Equals != "") && (!ok || v != c.Equals) {
			return false, nil
		}
		if (c.NotEqualsSet || c.NotEquals != "") && ok && v == c.NotEquals {
			return false, nil
		}
		if c.Contains != "" && (!ok || !strings.Contains(v, c.Contains)) {
			return false, nil
		}
		if c.Regex != "" {
			re, err := regexp.Compile(c.Regex)
			if err != nil {
				return false, err
			}
			if !ok || !re.MatchString(v) {
				return false, nil
			}
		}
		if len(c.OneOf) > 0 && (!ok || !contains(c.OneOf, v)) {
			return false, nil
		}
	}
	for k, c := range m.Headers {
		vals, ok := r.Header[http.CanonicalHeaderKey(k)]
		if c.Exists != nil && ok != *c.Exists {
			return false, nil
		}
		if (c.EqualsSet || c.Equals != "") && (!ok || !containsFold(vals, c.Equals)) {
			return false, nil
		}
		if (c.NotEqualsSet || c.NotEquals != "") && ok && containsFold(vals, c.NotEquals) {
			return false, nil
		}
		if c.Contains != "" {
			good := false
			for _, v := range vals {
				if strings.Contains(v, c.Contains) {
					good = true
				}
			}
			if !good {
				return false, nil
			}
		}
		if c.Regex != "" {
			re, e := regexp.Compile(c.Regex)
			if e != nil {
				return false, e
			}
			if !ok || len(vals) == 0 || !re.MatchString(vals[0]) {
				return false, nil
			}
		}
		if len(c.OneOf) > 0 {
			good := false
			for _, v := range vals {
				for _, x := range c.OneOf {
					if v == x {
						good = true
					}
				}
			}
			if !good {
				return false, nil
			}
		}
	}
	q := r.URL.Query()
	for k, c := range m.Query {
		vals, ok := q[k]
		if c.Exists != nil && ok != *c.Exists {
			return false, nil
		}
		if (c.EqualsSet || c.Equals != "") && (!ok || !contains(vals, c.Equals)) {
			return false, nil
		}
		if (c.NotEqualsSet || c.NotEquals != "") && ok && contains(vals, c.NotEquals) {
			return false, nil
		}
		if c.Contains != "" {
			good := false
			for _, v := range vals {
				if strings.Contains(v, c.Contains) {
					good = true
				}
			}
			if !good {
				return false, nil
			}
		}
		if c.Regex != "" {
			re, err := regexp.Compile(c.Regex)
			if err != nil {
				return false, err
			}
			good := false
			for _, v := range vals {
				if re.MatchString(v) {
					good = true
				}
			}
			if !good {
				return false, nil
			}
		}
		if len(c.OneOf) > 0 {
			good := false
			for _, v := range vals {
				if contains(c.OneOf, v) {
					good = true
				}
			}
			if !good {
				return false, nil
			}
		}
	}
	if m.BodySize != nil && int64(len(r.Body)) != *m.BodySize {
		return false, nil
	}
	if m.BodySHA256 != "" {
		sum := sha256.Sum256(r.Body)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), m.BodySHA256) {
			return false, nil
		}
	}
	if m.BodyEquals != "" && string(r.Body) != m.BodyEquals {
		return false, nil
	}
	if m.BodyRegex != "" {
		re, err := regexp.Compile(m.BodyRegex)
		if err != nil {
			return false, err
		}
		if !re.Match(r.Body) {
			return false, nil
		}
	}
	if len(m.JSON) > 0 {
		var root any
		d := json.NewDecoder(bytes.NewReader(r.Body))
		d.UseNumber()
		if err := d.Decode(&root); err != nil {
			return false, nil
		}
		for p, c := range m.JSON {
			v, ok := jsonPath(root, p)
			if c.Exists != nil && ok != *c.Exists {
				return false, nil
			}
			if !ok {
				if c.EqualsSet || c.Equals != nil || c.NotEqualsSet || c.NotEquals != nil || c.Type != "" || c.Regex != "" || c.Contains != nil || c.GreaterThan != nil || c.GreaterEqual != nil || c.LessThan != nil || c.LessEqual != nil {
					return false, nil
				}
				continue
			}
			if c.Type != "" && jsonType(v) != c.Type {
				return false, nil
			}
			if (c.EqualsSet || c.Equals != nil) && !equalJSON(v, c.Equals) {
				return false, nil
			}
			if (c.NotEqualsSet || c.NotEquals != nil) && equalJSON(v, c.NotEquals) {
				return false, nil
			}
			if c.Regex != "" {
				re, err := regexp.Compile(c.Regex)
				if err != nil {
					return false, err
				}
				if !re.MatchString(fmt.Sprint(v)) {
					return false, nil
				}
			}
			if c.Contains != nil && !containsJSON(v, c.Contains) {
				return false, nil
			}
			if c.GreaterThan != nil || c.GreaterEqual != nil || c.LessThan != nil || c.LessEqual != nil {
				n, ok := number(v)
				if !ok {
					return false, nil
				}
				if c.GreaterThan != nil && !(n > *c.GreaterThan) {
					return false, nil
				}
				if c.GreaterEqual != nil && !(n >= *c.GreaterEqual) {
					return false, nil
				}
				if c.LessThan != nil && !(n < *c.LessThan) {
					return false, nil
				}
				if c.LessEqual != nil && !(n <= *c.LessEqual) {
					return false, nil
				}
			}
		}
	}
	if len(m.XML) > 0 {
		for path, c := range m.XML {
			v, ok, err := XMLPathValue(r.Body, path)
			if err != nil {
				return false, fmt.Errorf("XPath %q: %w", path, err)
			}
			if c.Exists != nil && ok != *c.Exists {
				return false, nil
			}
			if !ok {
				if c.EqualsSet || c.Equals != nil || c.NotEqualsSet || c.NotEquals != nil || c.Type != "" || c.Regex != "" || c.Contains != nil || c.GreaterThan != nil || c.GreaterEqual != nil || c.LessThan != nil || c.LessEqual != nil {
					return false, nil
				}
				continue
			}
			if c.Type != "" && c.Type != "string" {
				return false, nil
			}
			if (c.EqualsSet || c.Equals != nil) && fmt.Sprint(v) != fmt.Sprint(c.Equals) {
				return false, nil
			}
			if (c.NotEqualsSet || c.NotEquals != nil) && fmt.Sprint(v) == fmt.Sprint(c.NotEquals) {
				return false, nil
			}
			if c.Contains != nil && !strings.Contains(fmt.Sprint(v), fmt.Sprint(c.Contains)) {
				return false, nil
			}
			if c.Regex != "" {
				re, e := regexp.Compile(c.Regex)
				if e != nil {
					return false, e
				}
				if !re.MatchString(fmt.Sprint(v)) {
					return false, nil
				}
			}
			if c.GreaterThan != nil || c.GreaterEqual != nil || c.LessThan != nil || c.LessEqual != nil {
				n, e := strconv.ParseFloat(fmt.Sprint(v), 64)
				if e != nil {
					return false, nil
				}
				if c.GreaterThan != nil && !(n > *c.GreaterThan) || c.GreaterEqual != nil && !(n >= *c.GreaterEqual) || c.LessThan != nil && !(n < *c.LessThan) || c.LessEqual != nil && !(n <= *c.LessEqual) {
					return false, nil
				}
			}
		}
	}
	return true, nil
}
func contains(a []string, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		n, err := x.Float64()
		return n, err == nil
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		n, err := strconv.ParseFloat(x, 64)
		return n, err == nil
	default:
		return 0, false
	}
}
func containsJSON(value, expected any) bool {
	switch x := value.(type) {
	case string:
		return strings.Contains(x, fmt.Sprint(expected))
	case []any:
		for _, item := range x {
			if equalJSON(item, expected) {
				return true
			}
		}
	case map[string]any:
		key := fmt.Sprint(expected)
		_, ok := x[key]
		return ok
	}
	return false
}

// JSONPathValue exposes the supported deterministic JSONPath subset ($.a.b and array indices).
func JSONPathValue(root any, path string) (any, bool) { return jsonPath(root, path) }

// MatchJSONConditions checks JSON body fields using the same operators as HTTP scenario matching.
func MatchJSONConditions(data []byte, conditions map[string]config.ValueCondition) (bool, error) {
	var root any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&root); err != nil {
		return false, err
	}
	for path, c := range conditions {
		v, ok := jsonPath(root, path)
		if c.Exists != nil && ok != *c.Exists {
			return false, nil
		}
		if !ok {
			if c.EqualsSet || c.Equals != nil || c.NotEqualsSet || c.NotEquals != nil || c.Type != "" || c.Regex != "" || c.Contains != nil || c.GreaterThan != nil || c.GreaterEqual != nil || c.LessThan != nil || c.LessEqual != nil {
				return false, nil
			}
			continue
		}
		if c.Type != "" && jsonType(v) != c.Type {
			return false, nil
		}
		if (c.EqualsSet || c.Equals != nil) && !equalJSON(v, c.Equals) {
			return false, nil
		}
		if (c.NotEqualsSet || c.NotEquals != nil) && equalJSON(v, c.NotEquals) {
			return false, nil
		}
		if c.Regex != "" {
			re, err := regexp.Compile(c.Regex)
			if err != nil {
				return false, err
			}
			if !re.MatchString(fmt.Sprint(v)) {
				return false, nil
			}
		}
		if c.Contains != nil && !containsJSON(v, c.Contains) {
			return false, nil
		}
		if c.GreaterThan != nil || c.GreaterEqual != nil || c.LessThan != nil || c.LessEqual != nil {
			n, ok := number(v)
			if !ok {
				return false, nil
			}
			if c.GreaterThan != nil && !(n > *c.GreaterThan) {
				return false, nil
			}
			if c.GreaterEqual != nil && !(n >= *c.GreaterEqual) {
				return false, nil
			}
			if c.LessThan != nil && !(n < *c.LessThan) {
				return false, nil
			}
			if c.LessEqual != nil && !(n <= *c.LessEqual) {
				return false, nil
			}
		}
	}
	return true, nil
}

func jsonPath(root any, p string) (any, bool) {
	parts, ok := tokenizeJSONPath(p)
	if !ok {
		return nil, false
	}
	values := []any{root}
	for _, part := range parts {
		next := make([]any, 0)
		if strings.HasPrefix(part, "..") {
			key := strings.TrimPrefix(part, "..")
			for _, value := range values {
				recursiveJSONFind(value, key, &next)
			}
		} else {
			for _, value := range values {
				switch x := value.(type) {
				case map[string]any:
					if part == "*" {
						keys := make([]string, 0, len(x))
						for k := range x {
							keys = append(keys, k)
						}
						sort.Strings(keys)
						for _, k := range keys {
							next = append(next, x[k])
						}
					} else if v, exists := x[part]; exists {
						next = append(next, v)
					}
				case []any:
					if part == "*" {
						next = append(next, x...)
					} else if strings.HasPrefix(part, "?(") && strings.HasSuffix(part, ")") {
						for _, item := range x {
							if matchJSONFilter(item, strings.TrimSuffix(strings.TrimPrefix(part, "?("), ")")) {
								next = append(next, item)
							}
						}
					} else if strings.Contains(part, ":") {
						next = append(next, applyJSONSlice(x, part)...)
					} else if i, err := strconv.Atoi(part); err == nil {
						if i < 0 {
							i = len(x) + i
						}
						if i >= 0 && i < len(x) {
							next = append(next, x[i])
						}
					}
				}
			}
		}
		if len(next) == 0 {
			return nil, false
		}
		values = next
	}
	if len(values) == 1 {
		return values[0], true
	}
	return values, true
}

func recursiveJSONFind(value any, key string, out *[]any) {
	switch x := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := x[k]
			if key == "*" || k == key {
				*out = append(*out, v)
			}
			recursiveJSONFind(v, key, out)
		}
	case []any:
		for _, v := range x {
			recursiveJSONFind(v, key, out)
		}
	}
}

// tokenizeJSONPath supports deterministic JSONPath selectors: $, dot properties,
// quoted bracket properties, array indexes, wildcards and recursive descent.
func tokenizeJSONPath(path string) ([]string, bool) {
	p := strings.TrimSpace(path)
	if p == "" || p == "$" {
		return nil, true
	}
	if !strings.HasPrefix(p, "$") {
		return nil, false
	}
	p = p[1:]
	var out []string
	for i := 0; i < len(p); {
		switch p[i] {
		case '.':
			if i+1 < len(p) && p[i+1] == '.' {
				i += 2
				start := i
				for i < len(p) && p[i] != '.' && p[i] != '[' {
					i++
				}
				if start == i {
					return nil, false
				}
				out = append(out, ".."+p[start:i])
			} else {
				i++
				if i < len(p) && p[i] == '*' {
					out = append(out, "*")
					i++
				}
			}
		case '[':
			i++
			if i >= len(p) {
				return nil, false
			}
			if p[i] == '\'' || p[i] == '"' {
				quote := p[i]
				i++
				var b strings.Builder
				for i < len(p) && p[i] != quote {
					if p[i] == '\\' && i+1 < len(p) {
						i++
						b.WriteByte(p[i])
						i++
					} else {
						b.WriteByte(p[i])
						i++
					}
				}
				if i >= len(p) {
					return nil, false
				}
				i++
				if i >= len(p) || p[i] != ']' {
					return nil, false
				}
				i++
				out = append(out, b.String())
			} else {
				start := i
				for i < len(p) && p[i] != ']' {
					i++
				}
				if i >= len(p) {
					return nil, false
				}
				token := strings.TrimSpace(p[start:i])
				i++
				if token == "" {
					return nil, false
				}
				if token != "*" && !strings.HasPrefix(token, "?") && !strings.Contains(token, ":") {
					if _, err := strconv.Atoi(token); err != nil {
						return nil, false
					}
				}
				out = append(out, token)
			}
		default:
			start := i
			for i < len(p) && p[i] != '.' && p[i] != '[' {
				i++
			}
			if start == i {
				return nil, false
			}
			out = append(out, p[start:i])
		}
	}
	return out, true
}

func applyJSONSlice(values []any, selector string) []any {
	parts := strings.Split(selector, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return nil
	}
	parseBound := func(s string, fallback int) int {
		if strings.TrimSpace(s) == "" {
			return fallback
		}
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return fallback
		}
		if n < 0 {
			n = len(values) + n
		}
		return n
	}
	start := parseBound(parts[0], 0)
	end := parseBound(parts[1], len(values))
	step := 1
	if len(parts) == 3 {
		if n, err := strconv.Atoi(strings.TrimSpace(parts[2])); err == nil && n != 0 {
			step = n
		}
	}
	if start < 0 {
		start = 0
	}
	if start > len(values) {
		start = len(values)
	}
	if end < 0 {
		end = 0
	}
	if end > len(values) {
		end = len(values)
	}
	out := []any{}
	if step > 0 {
		for i := start; i < end; i += step {
			out = append(out, values[i])
		}
	} else {
		if start >= len(values) {
			start = len(values) - 1
		}
		for i := start; i > end && i >= 0; i += step {
			out = append(out, values[i])
		}
	}
	return out
}

func matchJSONFilter(value any, expression string) bool {
	ops := []string{"==", "!=", ">=", "<=", ">", "<"}
	for _, op := range ops {
		if i := strings.Index(expression, op); i > 0 {
			left := strings.TrimSpace(expression[:i])
			right := strings.TrimSpace(expression[i+len(op):])
			if !strings.HasPrefix(left, "@") {
				return false
			}
			var actual any = value
			if left != "@" {
				var ok bool
				actual, ok = jsonPath(value, "$"+strings.TrimPrefix(left, "@"))
				if !ok {
					return false
				}
			}
			expected, ok := parseJSONFilterLiteral(right)
			if !ok {
				return false
			}
			if an, aok := number(actual); aok {
				if en, eok := number(expected); eok {
					switch op {
					case "==":
						return an == en
					case "!=":
						return an != en
					case ">":
						return an > en
					case ">=":
						return an >= en
					case "<":
						return an < en
					case "<=":
						return an <= en
					}
				}
			}
			as, es := fmt.Sprint(actual), fmt.Sprint(expected)
			switch op {
			case "==":
				return as == es
			case "!=":
				return as != es
			case ">":
				return as > es
			case ">=":
				return as >= es
			case "<":
				return as < es
			case "<=":
				return as <= es
			}
		}
	}
	// Existence filter: [?(@.field)]
	if strings.HasPrefix(strings.TrimSpace(expression), "@") {
		_, ok := jsonPath(value, "$"+strings.TrimPrefix(strings.TrimSpace(expression), "@"))
		return ok
	}
	return false
}

func parseJSONFilterLiteral(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"')) {
		return s[1 : len(s)-1], true
	}
	if s == "true" {
		return true, true
	}
	if s == "false" {
		return false, true
	}
	if s == "null" {
		return nil, true
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return n, true
	}
	return nil, false
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number, float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}
func equalJSON(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if bytes.Equal(ab, bb) {
		return true
	}
	if an, ok := a.(json.Number); ok {
		switch bn := b.(type) {
		case int:
			return an.String() == strconv.Itoa(bn)
		case int64:
			return an.String() == strconv.FormatInt(bn, 10)
		case float64:
			return an.String() == strconv.FormatFloat(bn, 'f', -1, 64)
		}
	}
	return false
}
