package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mock-integration-server/internal/config"
	"mock-integration-server/internal/fixtures"
	"mock-integration-server/internal/matcher"
)

type Result struct {
	Status     int
	Header     http.Header
	Body       []byte
	Aborted    bool
	ScenarioID string
	RouteID    string
}
type Engine struct {
	cfg    *config.Config
	files  *fixtures.Store
	mu     sync.Mutex
	counts map[string]int
	state  map[string]string
}

func New(c *config.Config) *Engine {
	return &Engine{cfg: c, files: fixtures.New(c.Server.FixturesDir), counts: map[string]int{}, state: map[string]string{}}
}
func (e *Engine) Execute(ctx context.Context, req matcher.Request, found *matcher.Result) (*Result, error) {
	if found == nil {
		return &Result{Status: e.cfg.Defaults.UnmatchedStatus, Header: http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}}, Body: []byte("No matching stub")}, nil
	}
	r := &Result{Status: 200, Header: make(http.Header), RouteID: found.Route.ID, ScenarioID: found.Scenario.ID}
	runID := req.TestRunID
	if runID == "" {
		runID = "default"
	}
	countKey := scopedKey(runID, found.Scenario.ID)
	e.mu.Lock()
	e.counts[countKey]++
	callNo := e.counts[countKey]
	e.mu.Unlock()
	for _, a := range found.Scenario.Actions {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		switch a.Type {
		case "delay":
			if a.Delay > 0 {
				t := time.NewTimer(time.Duration(a.Delay))
				select {
				case <-ctx.Done():
					t.Stop()
					return nil, ctx.Err()
				case <-t.C:
				}
			}
		case "respond":
			if a.Status > 0 {
				r.Status = a.Status
			}
			for k, v := range a.Headers {
				r.Header.Set(k, e.RenderTemplate(v, req))
			}
			b, err := e.body(a.Body, req)
			if err != nil {
				return nil, err
			}
			r.Body = b
			setDefaultContentType(r.Header, a.Body)
		case "abort":
			r.Aborted = true
			return r, nil
		case "set_state":
			stateValue := e.RenderTemplate(a.Value, req)
			e.mu.Lock()
			e.state[scopedKey(runID, a.Key)] = stateValue
			e.mu.Unlock()
		case "capture":
			v, err := e.capture(a.Value, req)
			if err != nil {
				return nil, err
			}
			e.mu.Lock()
			e.state[scopedKey(runID, a.Key)] = v
			e.mu.Unlock()
		case "sequence":
			if len(a.Responses) > 0 {
				idx := callNo - 1
				if a.Cycle {
					idx = idx % len(a.Responses)
				}
				if idx >= len(a.Responses) {
					idx = len(a.Responses) - 1
				}
				step := a.Responses[idx]
				if step.Status > 0 {
					r.Status = step.Status
				}
				for k, v := range step.Headers {
					r.Header.Set(k, e.RenderTemplate(v, req))
				}
				b, err := e.body(step.Body, req)
				if err != nil {
					return nil, err
				}
				r.Body = b
				setDefaultContentType(r.Header, step.Body)
			} else {
				// Backward compatibility with the original sequence action.
				if a.Status > 0 {
					r.Status = a.Status
				}
				for k, v := range a.Headers {
					r.Header.Set(k, v)
				}
				b, err := e.body(a.Body, req)
				if err != nil {
					return nil, err
				}
				r.Body = b
				setDefaultContentType(r.Header, a.Body)
			}
			r.Header.Set("X-Mock-Sequence-Call", fmt.Sprint(callNo))
		case "assert":
			e.mu.Lock()
			v, ok := e.state[scopedKey(runID, a.Key)]
			e.mu.Unlock()
			if !ok || v != a.Value {
				return nil, fmt.Errorf("scenario assertion failed: state[%q]=%q, expected %q", a.Key, v, a.Value)
			}
		case "ws_expect", "ws_send", "ws_close":
			return nil, fmt.Errorf("action %q requires WebSocket transport; WebSocket scenario execution is not yet available", a.Type)
		default:
			return nil, fmt.Errorf("unsupported action %q", a.Type)
		}
	}
	if r.Header.Get("Content-Type") == "" && len(r.Body) > 0 {
		r.Header.Set("Content-Type", "application/octet-stream")
	}
	return r, nil
}
func setDefaultContentType(headers http.Header, body config.Body) {
	if headers.Get("Content-Type") != "" {
		return
	}
	switch body.Type {
	case "json":
		headers.Set("Content-Type", "application/json")
	case "text", "":
		headers.Set("Content-Type", "text/plain; charset=utf-8")
	case "file":
		if typ := mime.TypeByExtension(filepath.Ext(body.Path)); typ != "" {
			headers.Set("Content-Type", typ)
		} else {
			headers.Set("Content-Type", "application/octet-stream")
		}
	case "base64":
		headers.Set("Content-Type", "application/octet-stream")
	}
}

func (e *Engine) body(b config.Body, req matcher.Request) ([]byte, error) {
	switch b.Type {
	case "", "text":
		if b.Value == nil {
			return []byte(e.RenderTemplate(b.Template, req)), nil
		}
		return []byte(e.RenderTemplate(fmt.Sprint(b.Value), req)), nil
	case "json":
		if b.Template != "" {
			return []byte(e.RenderTemplate(b.Template, req)), nil
		}
		v := b.Value
		if v == nil {
			v = map[string]any{}
		}
		return json.Marshal(e.renderValue(v, req))
	case "file":
		f, err := e.files.Open(b.Path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(f)
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(fmt.Sprint(b.Value))
		if err != nil {
			return nil, fmt.Errorf("invalid base64 body: %w", err)
		}
		return decoded, nil
	case "empty":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported body type %q", b.Type)
	}
}
func (e *Engine) renderValue(v any, req matcher.Request) any {
	switch x := v.(type) {
	case string:
		return e.RenderTemplate(x, req)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, value := range x {
			out[k] = e.renderValue(value, req)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, value := range x {
			out[fmt.Sprint(k)] = e.renderValue(value, req)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			out[i] = e.renderValue(value, req)
		}
		return out
	default:
		return v
	}
}

func (e *Engine) BuildBody(body config.Body, req matcher.Request) ([]byte, error) {
	return e.body(body, req)
}

func (e *Engine) RenderTemplate(s string, req matcher.Request) string {
	replacements := map[string]string{"{{method}}": req.Method, "{{path}}": req.URL.Path}
	for k, vals := range req.URL.Query() {
		if len(vals) > 0 {
			replacements["{{query."+k+"}}"] = vals[0]
		}
	}
	for k, v := range req.PathParams {
		replacements["{{pathParam."+k+"}}"] = v
	}
	for k, vals := range req.Header {
		if len(vals) > 0 {
			replacements["{{header."+k+"}}"] = vals[0]
			replacements["{{header."+strings.ToLower(k)+"}}"] = vals[0]
		}
	}
	for k, v := range e.stateSnapshot(req.TestRunID) {
		replacements["{{state."+k+"}}"] = v
	}
	var root any
	if len(req.Body) > 0 && json.Unmarshal(req.Body, &root) == nil {
		for _, p := range templateJSONPaths(s) {
			if v, ok := matcher.JSONPathValue(root, p); ok {
				replacements["{{json."+p+"}}"] = fmt.Sprint(v)
			}
		}
	}
	for k, v := range replacements {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}
func templateJSONPaths(s string) []string {
	var out []string
	for pos := 0; pos < len(s); {
		i := strings.Index(s[pos:], "{{json.")
		if i < 0 {
			break
		}
		start := pos + i + 7
		end := strings.Index(s[start:], "}}")
		if end < 0 {
			break
		}
		out = append(out, s[start:start+end])
		pos = start + end + 2
	}
	return out
}
func scopedKey(runID, key string) string { return runID + "\x00" + key }

func (e *Engine) stateSnapshot(runID string) map[string]string {
	if runID == "" {
		runID = "default"
	}
	prefix := runID + "\x00"
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]string, len(e.state))
	for k, v := range e.state {
		if strings.HasPrefix(k, prefix) {
			out[strings.TrimPrefix(k, prefix)] = v
		}
	}
	return out
}
func (e *Engine) capture(source string, req matcher.Request) (string, error) {
	switch {
	case source == "method":
		return req.Method, nil
	case source == "path":
		return req.URL.Path, nil
	case source == "body":
		return string(req.Body), nil
	case strings.HasPrefix(source, "header:"):
		return req.Header.Get(strings.TrimPrefix(source, "header:")), nil
	case strings.HasPrefix(source, "query:"):
		return req.URL.Query().Get(strings.TrimPrefix(source, "query:")), nil
	case strings.HasPrefix(source, "pathParam:"):
		return req.PathParams[strings.TrimPrefix(source, "pathParam:")], nil
	case strings.HasPrefix(source, "json:"):
		var root any
		if err := json.Unmarshal(req.Body, &root); err != nil {
			return "", fmt.Errorf("capture %q: request body is not valid JSON: %w", source, err)
		}
		v, ok := matcher.JSONPathValue(root, strings.TrimPrefix(source, "json:"))
		if !ok {
			return "", nil
		}
		return fmt.Sprint(v), nil
	default:
		return req.URL.Query().Get(source), nil
	}
}
func (e *Engine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.counts = map[string]int{}
	e.state = map[string]string{}
}
func (e *Engine) Stats() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	counts := map[string]int{}
	for k, v := range e.counts {
		parts := strings.SplitN(k, "\x00", 2)
		if len(parts) == 2 && parts[0] != "default" {
			counts[parts[0]+"/"+parts[1]] = v
		} else if len(parts) == 2 {
			counts[parts[1]] = v
		} else {
			counts[k] = v
		}
	}
	return map[string]any{"scenario_calls": counts, "state_keys": len(e.state)}
}
