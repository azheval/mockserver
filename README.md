# Mock Integration Server

Configurable HTTP and WebSocket mock server for testing external integrations with 1C and other systems. Routes and scenario behavior are declared outside Go source code.

## Quick start

```powershell
go run ./cmd/mockserver -config ./config/server.toml
```

Validate the configuration without opening listeners:

```powershell
go run ./cmd/mockserver -config ./config/server.toml -check-config
```

The default configuration uses TOML for server settings and the `.mock` DSL for routes and scenarios. Legacy YAML configuration remains supported for compatibility.

## Key features

- HTTP and WebSocket routes and stub scenarios.
- Match conditions for headers, query parameters, path parameters, JSONPath, XML paths, and request bodies.
- Response bodies from JSON, text, files, Base64, or empty content.
- Response sequences, delays, state capture/assertions, and asynchronous WebSocket messages.
- Per-test-run state isolation using `X-Mock-Test-Run`.
- `slog` logging to stdout and optional file using `-log-file`.
- Admin API for health, statistics, journal, reset, reload, and graceful shutdown.
- Runtime configuration validation with `-check-config`.

## Configuration files

- `config/server.toml` — listener, timeout, fixture, logging, and scenario file settings.
- `scenarios/*.mock` — route and scenario definitions.
- `fixtures/` — files returned by file-based responses.

Full DSL reference with examples: [`docs/dsl-reference.md`](docs/dsl-reference.md).

## Run with file logging

```powershell
go run ./cmd/mockserver `
  -config ./config/server.toml `
  -log-level debug `
  -log-format json `
  -log-file ./logs/mockserver.jsonl
```

The `logs` directory must exist. The file is opened in append mode; log records are also written to stdout.

## Admin API

The default admin listener is `127.0.0.1:8091`. Endpoints include `/health`, `/stats`, `/journal`, `/reset`, `/reload`, and `/shutdown`. The admin API has no built-in authentication; do not expose it to untrusted networks.

## Build and test

```powershell
go test ./...
go test -race ./...
go vet ./...
go build -o mockserver.exe ./cmd/mockserver
```

See [`docs/cli.md`](docs/cli.md), [`docs/architecture.md`](docs/architecture.md), and [`docs/dsl-reference.md`](docs/dsl-reference.md).
