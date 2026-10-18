package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDSLScenarioAndActions(t *testing.T) {
	source := []byte(`
route "orders" {
  protocol: http
  path: "/api/orders/{id}"
  methods: [GET]
  scenarios: ["order-status"]
}
scenario "order-status" {
  priority: 10
  when: { path_params: { id: { regex: "^ORD-[0-9]+$" } } }
  actions: [
    respond { status: 200, json: { id: "{{pathParam.id}}", status: "ready" } }
    delay { duration: "10ms" }
  ]
}
`)
	routes, scenarios, err := LoadDSL(source, "test.mock")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].ID != "orders" || len(routes[0].Scenarios) != 1 {
		t.Fatalf("unexpected routes: %#v", routes)
	}
	if len(scenarios) != 1 || scenarios[0].ID != "order-status" || len(scenarios[0].Actions) != 2 {
		t.Fatalf("unexpected scenarios: %#v", scenarios)
	}
	if scenarios[0].Actions[0].Type != "respond" || scenarios[0].Actions[0].Body.Type != "json" {
		t.Fatalf("unexpected response action: %#v", scenarios[0].Actions[0])
	}
}

func TestLoadTOMLAndScenarioFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scenarios"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.toml"), []byte(`version = 1
scenario_files = ["scenarios/api.mock"]
[server]
http_listen = "127.0.0.1:8090"
fixtures_dir = "fixtures"
[logging]
level = "debug"
format = "json"
[defaults]
strict = true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scenarios", "api.mock"), []byte(`route "health" { protocol: http, path: "/health", methods: [GET], scenarios: ["ok"] }
scenario "ok" { actions: [respond { status: 200, json: { ok: true } }] }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(filepath.Join(dir, "server.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Logging.Level != "debug" || cfg.Logging.Format != "json" {
		t.Fatalf("logging settings not loaded: %#v", cfg.Logging)
	}
	if len(cfg.Routes) != 1 || len(cfg.Scenarios) != 1 {
		t.Fatalf("scenario files not loaded: routes=%d scenarios=%d", len(cfg.Routes), len(cfg.Scenarios))
	}
}

func TestDSLReportsDuplicateProperties(t *testing.T) {
	_, _, err := LoadDSL([]byte(`route "x" { path: "/x", path: "/y" }`), "duplicate.mock")
	if err == nil {
		t.Fatal("expected duplicate property error")
	}
}
