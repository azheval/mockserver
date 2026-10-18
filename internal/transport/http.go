package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mock-integration-server/internal/config"
	"mock-integration-server/internal/engine"
	"mock-integration-server/internal/journal"
	"mock-integration-server/internal/matcher"
)

type HTTPHandler struct {
	cfg     *config.Config
	match   *matcher.Matcher
	engine  *engine.Engine
	journal *journal.Journal
	logger  *slog.Logger
}

func NewHTTPHandler(c *config.Config, e *engine.Engine, j *journal.Journal, l *slog.Logger) *HTTPHandler {
	return &HTTPHandler{cfg: c, match: matcher.New(c), engine: e, journal: j, logger: l}
}
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	id := requestID()
	w.Header().Set("X-Request-Id", id)
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, h.cfg.Server.MaxBodyBytes)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
		h.record(journal.Event{Time: start, RequestID: id, Protocol: "http", Method: r.Method, Path: r.URL.RequestURI(), Status: 413, DurationMS: time.Since(start).Milliseconds(), Error: err.Error()})
		return
	}
	protocol := "http"
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		protocol = "websocket"
	}
	runID := strings.TrimSpace(r.Header.Get("X-Mock-Test-Run"))
	if runID == "" {
		runID = "default"
	}
	if len(runID) > 128 {
		runID = runID[:128]
	}
	req := matcher.Request{Method: r.Method, URL: r.URL, Header: r.Header.Clone(), Body: body, PathParams: map[string]string{}, TestRunID: runID}
	found, matchErr := h.match.Match(req, protocol)
	if matchErr != nil {
		h.logger.Error("scenario matching failed", "request_id", id, "error", matchErr)
		http.Error(w, "ambiguous or invalid scenario match", http.StatusInternalServerError)
		h.record(journal.Event{Time: start, RequestID: id, Protocol: protocol, Method: r.Method, Path: r.URL.RequestURI(), Status: 500, DurationMS: time.Since(start).Milliseconds(), Error: matchErr.Error()})
		return
	}
	if protocol == "websocket" {
		h.serveWebSocket(w, r, req, found, id, start)
		return
	}
	res, err := h.engine.Execute(r.Context(), req, found)
	if err != nil {
		h.logger.Error("scenario execution failed", "request_id", id, "error", err)
		http.Error(w, "scenario execution failed", http.StatusInternalServerError)
		h.record(journal.Event{Time: start, RequestID: id, Protocol: protocol, Method: r.Method, Path: r.URL.RequestURI(), Status: 500, DurationMS: time.Since(start).Milliseconds(), Error: err.Error()})
		return
	}
	if res.Aborted {
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, e := hj.Hijack()
			if e == nil {
				_ = conn.Close()
			}
		}
		h.record(journal.Event{Time: start, RequestID: id, Protocol: protocol, Method: r.Method, Path: r.URL.RequestURI(), RouteID: res.RouteID, ScenarioID: res.ScenarioID, DurationMS: time.Since(start).Milliseconds(), Error: "connection aborted by scenario"})
		return
	}
	for k, vs := range res.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(res.Status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(res.Body)
	}
	e := journal.Event{Time: start, RequestID: id, Protocol: protocol, Method: r.Method, Path: r.URL.RequestURI(), Status: res.Status, DurationMS: time.Since(start).Milliseconds(), RouteID: res.RouteID, ScenarioID: res.ScenarioID, Unmatched: found == nil}
	if found == nil {
		e.Error = "no matching scenario"
		if h.cfg.Defaults.Strict {
			h.logger.Error("strict mode: unmatched request", "request_id", id, "method", r.Method, "path", r.URL.Path)
		}
	}
	h.record(e)
}
func (h *HTTPHandler) record(e journal.Event) {
	h.journal.Add(e)
	if e.Error != "" || e.Unmatched {
		h.logger.Warn("request", "request_id", e.RequestID, "protocol", e.Protocol, "method", e.Method, "path", e.Path, "route", e.RouteID, "scenario", e.ScenarioID, "status", e.Status, "duration_ms", e.DurationMS, "unmatched", e.Unmatched, "error", e.Error)
	} else {
		h.logger.Info("request", "request_id", e.RequestID, "protocol", e.Protocol, "method", e.Method, "path", e.Path, "route", e.RouteID, "scenario", e.ScenarioID, "status", e.Status, "duration_ms", e.DurationMS, "unmatched", e.Unmatched)
	}
}
func requestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().Format("20060102150405.000000")
	}
	return hex.EncodeToString(b[:])
}
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var wsUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func (h *HTTPHandler) serveWebSocket(w http.ResponseWriter, r *http.Request, req matcher.Request, found *matcher.Result, id string, start time.Time) {
	if found == nil {
		http.Error(w, "No matching WebSocket stub", h.cfg.Defaults.UnmatchedStatus)
		h.record(journal.Event{Time: start, RequestID: id, Protocol: "websocket", Method: r.Method, Path: r.URL.RequestURI(), Status: h.cfg.Defaults.UnmatchedStatus, DurationMS: time.Since(start).Milliseconds(), Unmatched: true, Error: "no matching WebSocket scenario"})
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		h.record(journal.Event{Time: start, RequestID: id, Protocol: "websocket", Method: r.Method, Path: r.URL.RequestURI(), RouteID: found.Route.ID, ScenarioID: found.Scenario.ID, DurationMS: time.Since(start).Milliseconds(), Error: err.Error()})
		return
	}
	wsCtx, wsCancel := context.WithCancel(r.Context())
	var wsWG sync.WaitGroup
	var writeMu sync.Mutex
	defer func() {
		wsCancel()
		_ = conn.Close() // unblock pending writes before waiting for scheduled senders
		wsWG.Wait()
	}()
	status := 101
	for _, a := range found.Scenario.Actions {
		if a.Delay > 0 && !(a.Type == "ws_send" && a.Async) {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Duration(a.Delay)):
			}
		}
		switch a.Type {
		case "ws_expect":
			timeout := time.Duration(a.Timeout)
			if timeout <= 0 {
				timeout = 10 * time.Second
			}
			_ = conn.SetReadDeadline(time.Now().Add(timeout))
			mt, data, e := conn.ReadMessage()
			if e != nil {
				h.logger.Warn("WebSocket read failed", "request_id", id, "error", e)
				h.record(journal.Event{Time: start, RequestID: id, Protocol: "websocket", Method: r.Method, Path: r.URL.RequestURI(), RouteID: found.Route.ID, ScenarioID: found.Scenario.ID, DurationMS: time.Since(start).Milliseconds(), Error: e.Error()})
				return
			}
			if a.Message.Type != "" && a.Message.Type != "any" && a.Message.Type != "text" && a.Message.Type != "binary" {
				h.logger.Warn("unknown ws_expect message type", "type", a.Message.Type)
			}
			if a.Message.Type == "text" && mt != websocket.TextMessage {
				h.wsExpectationFailed(start, id, r, found, "expected text WebSocket frame")
				return
			}
			if a.Message.Type == "binary" && mt != websocket.BinaryMessage {
				h.wsExpectationFailed(start, id, r, found, "expected binary WebSocket frame")
				return
			}
			if a.Message.Template != "" && string(data) != h.engine.RenderTemplate(a.Message.Template, req) {
				h.wsExpectationFailed(start, id, r, found, "WebSocket message did not match template")
				return
			}
			if a.Message.Value != nil {
				expected, _ := json.Marshal(a.Message.Value)
				if !bytes.Equal(bytes.TrimSpace(data), bytes.TrimSpace(expected)) && string(data) != fmt.Sprint(a.Message.Value) {
					h.wsExpectationFailed(start, id, r, found, "WebSocket message did not match expected value")
					return
				}
			}
			if len(a.Message.Match) > 0 {
				ok, matchErr := matcher.MatchJSONConditions(data, a.Message.Match)
				if matchErr != nil || !ok {
					reason := "WebSocket JSON message did not match conditions"
					if matchErr != nil {
						reason += ": " + matchErr.Error()
					}
					h.wsExpectationFailed(start, id, r, found, reason)
					return
				}
			}
		case "ws_send":
			mt := websocket.TextMessage
			if a.Message.Type == "binary" || a.Message.Type == "file" || a.Message.Type == "base64" {
				mt = websocket.BinaryMessage
			}
			var data []byte
			var buildErr error
			if a.Message.Type == "json" {
				if a.Message.Template != "" {
					data = []byte(h.engine.RenderTemplate(a.Message.Template, req))
				} else {
					data, buildErr = json.Marshal(a.Message.Value)
				}
			} else if a.Message.Type == "file" {
				data, buildErr = readFixture(h.cfg.Server.FixturesDir, a.Message.Path)
			} else if a.Message.Type == "binary" {
				if a.Message.Path != "" {
					data, buildErr = readFixture(h.cfg.Server.FixturesDir, a.Message.Path)
				} else {
					data, buildErr = h.engine.BuildBody(config.Body{Type: "base64", Value: a.Message.Value}, req)
				}
			} else {
				data, buildErr = h.engine.BuildBody(a.Message, req)
			}
			if buildErr != nil {
				h.logger.Error("build WebSocket message", "request_id", id, "error", buildErr)
				h.wsExpectationFailed(start, id, r, found, buildErr.Error())
				return
			}
			if a.Async {
				payload := append([]byte(nil), data...)
				delay := time.Duration(a.Delay)
				wsWG.Add(1)
				go func() {
					defer wsWG.Done()
					if delay > 0 {
						timer := time.NewTimer(delay)
						defer timer.Stop()
						select {
						case <-wsCtx.Done():
							return
						case <-timer.C:
						}
					}
					select {
					case <-wsCtx.Done():
						return
					default:
					}
					writeMu.Lock()
					defer writeMu.Unlock()
					if err := conn.WriteMessage(mt, payload); err != nil && wsCtx.Err() == nil {
						h.logger.Error("asynchronous WebSocket write failed", "request_id", id, "error", err)
					}
				}()
				continue
			}
			writeMu.Lock()
			writeErr := conn.WriteMessage(mt, data)
			writeMu.Unlock()
			if writeErr != nil {
				h.logger.Error("WebSocket write failed", "request_id", id, "error", writeErr)
				return
			}
		case "ws_close":
			code := a.Code
			if code == 0 {
				code = websocket.CloseNormalClosure
			}
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, a.Reason), time.Now().Add(time.Second))
			h.record(journal.Event{Time: start, RequestID: id, Protocol: "websocket", Method: r.Method, Path: r.URL.RequestURI(), RouteID: found.Route.ID, ScenarioID: found.Scenario.ID, Status: status, DurationMS: time.Since(start).Milliseconds()})
			return
		case "delay":
		case "respond":
		default:
			h.logger.Warn("action unsupported for WebSocket", "action", a.Type, "scenario", found.Scenario.ID)
		}
	}
	h.record(journal.Event{Time: start, RequestID: id, Protocol: "websocket", Method: r.Method, Path: r.URL.RequestURI(), RouteID: found.Route.ID, ScenarioID: found.Scenario.ID, Status: status, DurationMS: time.Since(start).Milliseconds()})
}

func (h *HTTPHandler) wsExpectationFailed(start time.Time, id string, r *http.Request, found *matcher.Result, reason string) {
	h.logger.Warn("WebSocket scenario expectation failed", "request_id", id, "scenario", found.Scenario.ID, "error", reason)
	h.record(journal.Event{Time: start, RequestID: id, Protocol: "websocket", Method: r.Method, Path: r.URL.RequestURI(), RouteID: found.Route.ID, ScenarioID: found.Scenario.ID, DurationMS: time.Since(start).Milliseconds(), Error: reason})
}

func readFixture(root, name string) ([]byte, error) {
	if name == "" || filepath.IsAbs(name) {
		return nil, fmt.Errorf("fixture path must be relative")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("fixture path escapes fixture root")
	}
	p := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("fixture path escapes fixture root")
	}
	cur := root
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		st, e := os.Lstat(cur)
		if e != nil {
			return nil, e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink fixtures are not allowed")
		}
	}
	return os.ReadFile(p)
}
