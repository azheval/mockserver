package transport

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"mock-integration-server/internal/config"
	"mock-integration-server/internal/engine"
	"mock-integration-server/internal/journal"
)

func TestConcurrentHTTPAndWebSocketRequests(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{FixturesDir: t.TempDir(), MaxBodyBytes: 1 << 20},
		Defaults: config.DefaultsConfig{UnmatchedStatus: 404},
		Routes: []config.Route{
			{ID: "http", Protocol: "http", Path: "/api/ping", Methods: []string{"GET"}, Scenarios: []string{"ping"}},
			{ID: "ws", Protocol: "websocket", Path: "/ws", Methods: []string{"GET"}, Scenarios: []string{"notify"}},
		},
		Scenarios: []config.Scenario{
			{ID: "ping", Actions: []config.Action{{Type: "respond", Status: 200, Body: config.Body{Type: "text", Value: "pong"}}}},
			{ID: "notify", Actions: []config.Action{
				{Type: "ws_send", Async: true, Delay: config.Duration(5 * time.Millisecond), Message: config.Body{Type: "text", Value: "notice"}},
				{Type: "delay", Delay: config.Duration(30 * time.Millisecond)},
				{Type: "ws_close"},
			}},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHTTPHandler(cfg, engine.New(cfg), journal.New(1000), logger)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	const httpRequests = 24
	const wsRequests = 8
	var wg sync.WaitGroup
	errCh := make(chan error, httpRequests+wsRequests)
	for i := 0; i < httpRequests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/ping", nil)
			req.Header.Set("X-Mock-Test-Run", fmt.Sprintf("parallel-%d", i%3))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errCh <- err
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK || string(body) != "pong" {
				errCh <- fmt.Errorf("HTTP response: status=%d body=%q", resp.StatusCode, body)
			}
		}(i)
	}
	for i := 0; i < wsRequests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
			conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{"X-Mock-Test-Run": []string{fmt.Sprintf("ws-%d", i)}})
			if err != nil {
				errCh <- err
				return
			}
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, message, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if string(message) != "notice" {
				errCh <- fmt.Errorf("WebSocket message = %q", message)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
