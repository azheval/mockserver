# Command-line reference

```text
mockserver [flags]
  -config string            TOML config path (default "config/server.toml"); legacy YAML is supported
  -listen string            Override HTTP listen address
  -admin-listen string      Override admin listen address; use `off` to disable
  -fixtures string          Override fixtures root
  -log-level string         debug|info|warn|error
  -log-format string        text|json
  -log-file string          Append logs to this file as well as stdout
  -strict                   Mark unmatched requests as errors in the journal
  -no-strict                Disable strict mode
  -max-body-bytes int       Maximum request body size
  -read-timeout duration    HTTP read timeout (e.g. 30s)
  -write-timeout duration   HTTP write timeout (e.g. 30s)
  -idle-timeout duration    HTTP idle timeout (e.g. 60s)
  -shutdown-timeout duration Graceful shutdown timeout
  -shutdown-after duration  Automatically stop after duration (e.g. 30m; 0 disables)
  -check-config             Validate configuration and exit
  -version                  Print version
```

Examples (PowerShell):

```powershell
go run ./cmd/mockserver -config .\config\server.toml -listen 127.0.0.1:8090 -admin-listen 127.0.0.1:8091 -log-level debug -log-file .\logs\mockserver.log

go run ./cmd/mockserver -config .\config\server.toml -check-config
```

`logs` must exist before starting with `-log-file`. The log file is opened in append mode; output continues to stdout. The selected format (`text` or `json`) and level apply to both outputs.

Admin endpoints on the separate admin listener: `GET /health`, `GET /stats`, `GET /journal`, `POST /reset`, `POST /reload`, `POST /shutdown`. Do not expose the admin listener to untrusted networks.

## Command-line precedence

Values explicitly supplied on the command line override settings in TOML. Omitted flags preserve file values. Use `-admin-listen off` to disable the admin listener even if it is enabled in the file; use `-no-strict` to turn strict mode off. Duration values use Go duration syntax (`250ms`, `5s`, `2m`).

## Graceful shutdown

Stop after a duration:

```powershell
go run ./cmd/mockserver -config .\config\server.toml -shutdown-after 30m -shutdown-timeout 10s
```

Request shutdown through the admin API:

```powershell
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8091/shutdown
```

The endpoint returns HTTP 202 and graceful shutdown starts shortly afterwards. The server stops accepting new requests and waits up to `-shutdown-timeout` for active HTTP requests. The API must only be exposed on a trusted network because it can stop the server.

## Reload configuration

After editing `server.toml` or a `.mock` scenario file, send:

```powershell
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8091/reload
```

Reload validates the complete configuration before switching the HTTP handler. If validation fails, the current configuration remains active. Listener addresses cannot be changed by reload; restart to change listener addresses or server-level socket timeouts. Reload creates a fresh scenario engine, so counters and in-memory state reset. Existing WebSocket sessions continue with the handler/scenario selected when their handshake was processed.

## Per-test isolation

Pass `X-Mock-Test-Run` on requests to isolate sequence counters and scenario state per test run. Without the header, the shared `default` scope is used. `POST /reset` resets all counters and in-memory state.

## Configuration references

- New format and full DSL: [`dsl-reference.md`](dsl-reference.md)
- Legacy YAML reference: [`configuration-guide.md`](configuration-guide.md)

## Generate DSL from Swagger / OpenAPI

```powershell
go run ./cmd/mockserver -generate-openapi .\openapi.yaml
go run ./cmd/mockserver -generate-openapi .\openapi.json -generate-openapi-out .\scenarios\generated.mock
go run ./cmd/mockserver -generate-openapi .\swagger.yaml -openapi-version 2.0
```

`-generate-openapi-out -` see: [docs/openapi-generator.md](openapi-generator.md).
