package engine

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"mock-integration-server/internal/config"
	"mock-integration-server/internal/matcher"
)

func TestSequenceReturnsDifferentResponsesByCall(t *testing.T) {
	c := &config.Config{Server: config.ServerConfig{FixturesDir: t.TempDir()}, Defaults: config.DefaultsConfig{UnmatchedStatus: 404}, Scenarios: []config.Scenario{{ID: "sequence", Actions: []config.Action{{Type: "sequence", Responses: []config.ResponseStep{
		{Status: 202, Headers: map[string]string{"Content-Type": "text/plain"}, Body: config.Body{Type: "text", Value: "accepted"}},
		{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: config.Body{Type: "json", Value: map[string]any{"state": "ready"}}},
	}}}}}}
	e := New(c)
	u, _ := url.Parse("/api/status")
	req := matcher.Request{Method: http.MethodGet, URL: u, Header: http.Header{}, Body: []byte(`{}`)}
	found := &matcher.Result{Route: config.Route{ID: "r"}, Scenario: c.Scenarios[0]}
	first, err := e.Execute(context.Background(), req, found)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != 202 || string(first.Body) != "accepted" {
		t.Fatalf("unexpected first response: %#v", first)
	}
	second, err := e.Execute(context.Background(), req, found)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != 200 || string(second.Body) != `{"state":"ready"}` {
		t.Fatalf("unexpected second response: %#v", second)
	}
}

func TestTemplateUsesJSONAndState(t *testing.T) {
	c := &config.Config{Server: config.ServerConfig{FixturesDir: t.TempDir()}}
	e := New(c)
	e.state[scopedKey("default", "token")] = "T-100"
	u, _ := url.Parse("/orders/12?source=1c")
	req := matcher.Request{Method: "POST", URL: u, Header: http.Header{"X-Client": []string{"1C"}}, Body: []byte(`{"order":{"id":42}}`), PathParams: map[string]string{"id": "12"}}
	got := e.RenderTemplate(`{{method}} {{path}} {{query.source}} {{pathParam.id}} {{header.X-Client}} {{json.$.order.id}} {{state.token}}`, req)
	want := `POST /orders/12 1c 12 1C 42 T-100`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestJSONBodyRendersTemplateValuesSafely(t *testing.T) {
	c := &config.Config{Server: config.ServerConfig{FixturesDir: t.TempDir()}}
	e := New(c)
	u, _ := url.Parse("/orders?source=1c")
	req := matcher.Request{Method: "POST", URL: u, Header: http.Header{}, Body: []byte(`{"name":"A\"B"}`)}
	body, err := e.BuildBody(config.Body{Type: "json", Value: map[string]any{"name": "{{json.$.name}}", "source": "{{query.source}}"}}, req)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"A\"B","source":"1c"}`
	if string(body) != want {
		t.Fatalf("got %s, want %s", body, want)
	}
}

func TestTestRunStateAndSequencesAreIsolated(t *testing.T) {
	c := &config.Config{Server: config.ServerConfig{FixturesDir: t.TempDir()}, Scenarios: []config.Scenario{{ID: "s", Actions: []config.Action{{Type: "sequence", Responses: []config.ResponseStep{{Status: 200, Body: config.Body{Type: "text", Value: "first"}}, {Status: 200, Body: config.Body{Type: "text", Value: "second"}}}}}}}}
	e := New(c)
	u, _ := url.Parse("/x")
	found := &matcher.Result{Route: config.Route{ID: "r"}, Scenario: c.Scenarios[0]}
	call := func(run string) string {
		t.Helper()
		res, err := e.Execute(context.Background(), matcher.Request{Method: "GET", URL: u, Header: http.Header{}, TestRunID: run}, found)
		if err != nil {
			t.Fatal(err)
		}
		return string(res.Body)
	}
	if got := call("run-a"); got != "first" {
		t.Fatalf("run-a first call = %q", got)
	}
	if got := call("run-a"); got != "second" {
		t.Fatalf("run-a second call = %q", got)
	}
	if got := call("run-b"); got != "first" {
		t.Fatalf("run-b should start from first response, got %q", got)
	}
}
