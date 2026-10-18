# Mock Integration Server DSL

The DSL is intended to describe integration stubs without YAML. The structure is specified by curly braces, lists by square brackets, and properties are separated by colons. **Indentations do not affect the meaning of the file**. Line breaks are only needed for ease of reading.

## 1. Files and downloads

- `config/server.toml` — server parameters.
- `scenarios/*.mock` — routes and scenarios HTTP/WebSocket.
- `scenario_files` lists DSL files. Paths are considered relative to the `server.toml` directory.
- Old YAML configurations are still supported for backward compatibility. For new configurations, use TOML + `.mock`.

Running from the project root:

```powershell
go run ./cmd/mockserver -config ./config/server.toml
```

Checking configuration without running listeners:

```powershell
go run ./cmd/mockserver -config ./config/server.toml -check-config
```

## 2. Syntax

```text
route "route-id" {
  protocol: http
  path: "/api/orders"
  methods: [POST]
  scenarios: ["create-order"]
}

scenario "create-order" {
  priority: 100
  when: {
    headers: {
      "Content-Type": { regex: "(?i)application/json" }
    }
  }
  actions: [
    respond {
      status: 201
      json: { accepted: true }
    }
  ]
}
```

Vocabulary rules:

- Strings can be written in double or single quotes. In double quotes, `\\`, `\"`, `\n`, `\r`, `\t` work.
- Property names may not be quoted if it is a simple identifier. It is more convenient to enclose JSONPath paths, HTTP header names with hyphens, and strings with special characters in quotes.
- Simple values `true`, `false`, `null`, numbers and unquoted words (`http`, `GET`, `array`) are recognized automatically. Unknown words are considered strings.
- Commas between properties and list elements are optional. For clarity, it is recommended to use commas in short single-line blocks; in the multiline examples below they are omitted.
- Comments begin with `#` or `//` outside of lines.
- A duplicate property in the same block and an unknown property give a configuration error.
- The order of the keys in the blocks is not important. The order of the elements in `actions` and `responses` is important.

## 3. Server settings in TOML

Example `config/server.toml`:

```toml
version = 1
scenario_files = ["../scenarios/orders.mock", "../scenarios/files.mock"]

[server]
http_listen = "127.0.0.1:8090"
admin_listen = "127.0.0.1:8091"
fixtures_dir = "../fixtures"
read_timeout = "30s"
write_timeout = "30s"
idle_timeout = "60s"
shutdown_timeout = "10s"
max_body_bytes = 52428800

[defaults]
unmatched_status = 404
strict = false

[admin]
enabled = true

[logging]
level = "info"
format = "text"
log_bodies = false
```

Supported options:

| Parameter | Destination |
|---|---|
| `version` | Configuration format version, currently `1` |
| `scenario_files` | Array of paths to `.mock` files relative to TOML |
| `server.http_listen` | HTTP/WebSocket listener address |
| `server.admin_listen` | Administrative API address; empty string disables listener |
| `server.fixtures_dir` | File response root; relative path is calculated from TOML |
| `server.read_timeout` | HTTP request read timeout, Go duration (`30s`, `1m`) |
| `server.write_timeout` | HTTP response recording timeout |
| `server.idle_timeout` | Keep-alive connection timeout |
| `server.shutdown_timeout` | Time for a graceful shutdown |
| `server.max_body_bytes` | Maximum request body size in bytes |
| `defaults.unmatched_status` | HTTP status when script not found |
| `defaults.strict` | Strict mode for unfound stubs |
| `admin.enabled` | Includes administrative API |
| `logging.level` | `debug`, `info`, `warn` or `error` |
| `logging.format` | `text` or `json` |
| `logging.log_bodies` | Reserved logging settings parameter; actual logging of bodies depends on the request handler |

The CLI overrides file values  such as `-listen`, `-admin-listen off`, `-log-level debug`, `-log-format json`, `-log-file ./logs/mockserver.jsonl`, `-shutdown-after 30m` and `-shutdown-timeout 10s`.

TOML is parsed by the standard TOML library. Once parsed, unknown settings options are rejected rather than ignored.

## 4. Routes

Route announcement:

```text
route "create-order" {
  protocol: http
  path: "/api/orders"
  methods: [POST]
  scenarios: ["create-valid", "create-invalid"]
}
```

Fields:

- `protocol`: `http` or `websocket`.
- `path`: path starting with `/`. Path parameters are specified in curly braces, for example `/api/orders/{orderId}`.
- `methods`: optional array of HTTP methods. If omitted, all methods are suitable.
- `scenarios`: IDs of scenarios that can process the route.

Scripts can also be bound from the script side using `route: "create-order"`; the route and script must be in the same `.mock` file. If both methods are used at the same time, do not duplicate the same identifier.

## 5. Scenarios, priority and conditions

```text
scenario "create-valid" {
  priority: 100
  when: {
    headers: {
      "Content-Type": { regex: "(?i)application/json" }
    }
    query: {
      source: { one_of: ["erp", "portal"] }
    }
    path_params: {
      orderId: { regex: "^ORD-[0-9]+$" }
    }
    json: {
      "$.order.id": { exists: true }
      "$.order.amount": { gt: 0, lte: 100000 }
      "$.order.items": { type: array }
    }
    xml: {
      "/Order/Number": { equals: "A-100" }
    }
    body_regex: "(?s).*<Order>.*"
  }
  actions: [
    respond { status: 201, json: { accepted: true } }
  ]
}
```

`when` — short name for the `match` block. Supported:

- `headers`: conditions for HTTP headers.
- `query`: conditions based on query parameters.
- `path_params`: conditions for path parameters.
- `json`: conditions by JSONPath in the JSON body of the request.
- `xml`: conditions for XPath-like paths in the XML body.
- `body_sha256`, `body_size`, `body_regex`, `body_equals`: conditions for the whole body.

Conditions by lines: `equals`, `not_equals`, `contains`, `exists`, `regex`, `one_of`.
Conditions on JSON/XML values ​​additionally support `type`, `gt`, `gte`, `lt`, `lte`. For numeric comparisons, the value must be a number.

Supported types in `type`: `string`, `number`, `integer`, `boolean`, `object`, `array`, `null`.

If several scenarios match, the scenario with the highest `priority` is selected. If the top two matches have the same priority, the server returns an ambiguity error. Set different priorities for the primary and backup scenarios.

### JSONPath

Supports dot and parenthesis properties, quoted keys, array indexes, wildcard, recursive search, filters, and index ranges. Examples:

```text
"$.order.id"
"$['external-code']"
"$.items[0].sku"
"$.items[*].sku"
"$..id"
"$.items[?(@.price >= 10)].sku"
"$.items[1:3]"
```

Filters and slicers are a subset of JSONPath and not an implementation of the full standard. Check the project tests if you use complex expressions.

### XML/XPath

Absolute and descendant paths, element indexes, attributes, and `text()` are supported, for example `"/Order/Line[2]/@sku"` or `"//Message/text()"`. This is not full XPath 1.0: arbitrary predicates, namespaces, and the full set of functions are not supported.

## 6. Actions

Actions are recorded in the `actions` array and executed from top to bottom.

### `respond` — HTTP response

```text
respond {
  status: 201
  headers: { "Content-Type": "application/json", "X-Stub": "create-order" }
  json: {
    id: "EXT-1001"
    accepted: true
    receivedOrderId: "{{json.$.order.id}}"
    source: "{{query.source}}"
  }
}
```

The body is specified in exactly one way:

- `json: { ... }` — a JSON object/array/value; strings inside are treated as templates.
- `text: "..."` — a text response; templates are supported.
- `file: "files/invoice.pdf"` — a file relative to `server.fixtures_dir`.
- `base64: "SGVsbG8="` — a binary body encoded in Base64.
- `empty: true` — an empty body.

Instead of the shorthand fields, you can pass `body: { type: "json", value: {...} }` or `body: { type: "file", path: "files/invoice.pdf" }` for compatibility with the internal model.

### Templates

The following substitutions are available:

- `{{method}}`, `{{path}}`.
- `{{query.name}}` — the first query parameter with the specified name.
- `{{pathParam.orderId}}` — a path parameter.
- `{{header.Content-Type}}` — a request header.
- `{{json.$.order.id}}` — a value by JSONPath in the JSON body.
- `{{state.externalOrderId}}` — state saved by the `set_state` or `capture` action.

Insertions in JSON strings are performed as text substitution. Do not use a template to insert an arbitrary object into JSON unless you control the correctness of serialization.

### `delay` — delay

```text
delay { duration: "250ms" }
```

Accepts a Go duration (`100ms`, `2s`, `1m`). The delay is interrupted when the request is canceled or the server stops.

### `sequence` — response sequence

```text
sequence {
  cycle: false
  responses: [
    respond { status: 202, json: { status: "processing" } }
    respond { status: 200, json: { status: "completed" } }
  ]
}
```

The first call receives the first response, the second receives the second. After the sequence is exhausted, the last response is returned. With `cycle: true`, the sequence starts over. Counters are separated by `X-Mock-Test-Run` and the scenario ID.

### `capture`, `set_state`, `assert`

```text
capture { key: "orderId", value: "json:$.order.id" }
set_state { key: "externalId", value: "EXT-{{state.orderId}}" }
assert { key: "orderId", value: "ORD-100" }
```

`capture.value` supports `method`, `path`, `body`, `header:Name`, `query:name`, `pathParam:name`, `json:JSONPath`. An unknown prefix is treated as a query parameter name. State is isolated by `X-Mock-Test-Run`.

### `abort`

```text
abort {}
```

Interrupts the scenario without generating a normal response.

### WebSocket: `ws_expect`, `ws_send`, `ws_close`

```text
ws_expect { text: "{\"type\":\"subscribe\"}", timeout: "3s" }
ws_send { json: { type: "connected", subscriptionId: "sub-001" } }
ws_send { async: true, delay: "2s", json: { type: "notification", text: "Order ready" } }
ws_close { code: 1000, reason: "Scenario completed" }
```

`ws_expect` waits for a client message, `ws_send` sends a text/JSON/file/Base64/empty message, `ws_close` closes the connection. `async: true` starts the send independently of subsequent scenario actions; `delay` postpones the send. WebSocket support depends on the transport handler and does not automatically turn an HTTP route into a WebSocket — the route must have `protocol: websocket`.

## 7. Isolating parallel tests

Pass a unique `X-Mock-Test-Run` header for each parallel test or test suite:

```powershell
Invoke-RestMethod -Uri "http://127.0.0.1:8090/api/orders" -Method Post -Headers @{ "X-Mock-Test-Run" = "ut-run-042" } -ContentType "application/json" -Body '{"orderId":"ORD-42", "items":[]}'
```

Sequence counters and state keys are separated by this identifier. If the header is not passed, the shared `default` namespace is used. The admin `/reset` resets server state.

## 8. Complete example: 1C integration

```text
route "erp-order" {
  protocol: http
  path: "/integration/orders"
  methods: [POST]
  scenarios: ["erp-order-created", "erp-order-invalid"]
}

scenario "erp-order-created" {
  priority: 100
  when: {
    headers: { "Content-Type": { regex: "(?i)application/json" } }
    json: {
      "$.DocumentNumber": { exists: true }
      "$.Organization.Code": { exists: true }
      "$.Total": { gt: 0 }
    }
  }
  actions: [
    capture { key: "documentNumber", value: "json:$.DocumentNumber" }
    set_state { key: "externalId", value: "EXT-{{state.documentNumber}}" }
    respond {
      status: 200
      json: {
        Success: true
        DocumentNumber: "{{state.documentNumber}}"
        ExternalId: "{{state.externalId}}"
      }
    }
  ]
}

scenario "erp-order-invalid" {
  priority: 10
  when: { json: { "$.DocumentNumber": { exists: false } } }
  actions: [
    respond { status: 400, json: { Error: "DOCUMENT_NUMBER_REQUIRED" } }
  ]
}
```

## 9. Preparing for generation from OpenAPI

The DSL deliberately separates the transport route and the scenario:

- An OpenAPI `paths` + HTTP method can be converted into a `route`.
- `operationId` is suitable for a stable route or scenario name.
- `parameters` (path/query/header) map to `when`.
- `requestBody` and JSON Schema can be used to generate basic `when.json` conditions.
- `responses` can be converted into `respond` with the corresponding statuses, headers, and bodies.
- Several response statuses can become separate scenarios with priorities or `sequence` steps, depending on the desired behavior.

For predictable generation, use stable `operationId`s, do not duplicate IDs, and keep generated files separate from manually maintained scenarios. The OpenAPI generator should produce `.mock` as an artifact, not change the DSL syntax.

## 10. Diagnostics

- `unknown property`: a typo in a field/action name.
- `duplicate property`: the same property is set twice in one block.
- `unknown route`: `scenario route:` references a route that does not exist in this `.mock` file.
- `unknown scenario`: an ID in `route.scenarios` is not declared.
- `ambiguous scenarios`: two scenarios with the same highest priority matched.
- `No matching stub`: no route and scenario matched the request.

Recommended development cycle: edit the `.mock` → run `-check-config` → start the server → make a request → check the `/journal` and logs.

## Generating from Swagger / OpenAPI

To create initial `.mock` scenarios from an API description, see the [OpenAPI generator guide](openapi-generator.md). The generator supports Swagger 2.0 and the OpenAPI 3.0, 3.1, 3.2 families, automatically detects the version, and produces routes and responses. Request validation conditions can be added manually after generation.
