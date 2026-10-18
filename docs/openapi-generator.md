# Generating DSL from Swagger / OpenAPI

Mock Integration Server can convert an API description into `.mock` scenarios. The input document can be JSON or YAML. The generator does not start the server and does not fetch remote `$ref` values: it works with the local file and internal `#/...` references.

## Running

```powershell
# Automatically detect the version and create an api.mock file next to the input
go run ./cmd/mockserver -generate-openapi .\openapi.yaml

# Set the output path explicitly
go run ./cmd/mockserver -generate-openapi .\openapi.yaml -generate-openapi-out .\scenarios\generated.mock

# Generate to stdout (convenient for pipelines)
go run ./cmd/mockserver -generate-openapi .\openapi.json -generate-openapi-out -

# Additionally verify the specification version family
go run ./cmd/mockserver -generate-openapi .\swagger.yaml -openapi-version 2.0
```

`-openapi-version` accepts `auto`, `2.0`, `3.0`, `3.1`, or `3.2`. The default is `auto`: the version is taken from the `swagger` or `openapi` field. If the specified version does not match the document, generation stops with an error. The parameter sets the version family; it does not replace the check of the version field inside the file.

If `-generate-openapi-out` is not specified, the output name is obtained by replacing the input file extension with `.mock`. The output directory must exist.

## Supported families

| Specification | Version detection | Constructs handled |
|---|---|---|
| Swagger 2.0 | `swagger: "2.0"` | `paths`, HTTP operations, `basePath`, `responses`, `schema`, `examples`, `produces`, response headers, `definitions`, and local `$ref` |
| OpenAPI 3.0.x | `openapi: 3.0.x` | `paths`, operations, `responses`, `content`, `example`, `examples`, `schema`, `components/schemas`, local `$ref` |
| OpenAPI 3.1.x | `openapi: 3.1.x` | Common OpenAPI 3.x constructs and common JSON Schema schemas, including `type` as an array, `enum`, `default`, `example`, `properties`, `items`, `allOf`, `oneOf`, `anyOf` |
| OpenAPI 3.2.x | `openapi: 3.2.x` | The common OpenAPI 3.x constructs listed above. New features specific to OpenAPI 3.2 that do not affect the listed fields are not interpreted as scenario logic |

Versions are detected by family: Swagger 2.0, OpenAPI 3.0, 3.1, and 3.2. An unknown version is not handled silently — the generator reports an error.

## What is created

For each supported operation `GET`, `PUT`, `POST`, `DELETE`, `PATCH`, `HEAD`, `OPTIONS`, or `TRACE`, the following are created:

1. a `route` with a path, method, and a reference to a scenario;
2. a `scenario` with a `respond` action;
3. the response code and, when possible, the `Content-Type`;
4. the response body from the specification example or an automatically built schema example.

The identifier is taken from `operationId` if it is set. Otherwise it is built from the method and path. Duplicate identifiers receive a suffix.

### Response selection

The generator selects one response for the initial stub: first `200`, `201`, `202`, `204`, then another explicit `2xx`, then the first explicit response code. If only `default` is described in the specification, status `200` is used. Codes `204` and `304` are always generated without a body.

### Example response from a schema

Input OpenAPI 3.x:

```yaml
openapi: 3.1.0
info: { title: Orders, version: '1.0' }
paths:
  /orders/{id}:
    get:
      operationId: getOrder
      responses:
        '200':
          description: Order
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Order'
components:
  schemas:
    Order:
      type: object
      properties:
        id: { type: string, example: ORD-42 }
        amount: { type: number, default: 12.5 }
        active: { type: boolean }
```

The generated DSL will contain the route `/orders/{id}` and a JSON response roughly like this:

```text
route "getOrder" {
  protocol: http
  path: "/orders/{id}"
  methods: [GET]
  scenarios: ["openapi-getOrder"]
}

scenario "openapi-getOrder" {
  priority: 0
  actions: [
    respond {
      status: 200
      headers: { "Content-Type": "application/json" }
      json: {
        "active": true
        "amount": 12.5
        "id": "ORD-42"
      }
    }
  ]
}
```

The order of properties in the generated object is stable so that file changes are easy to track in Git.

## Example schemas without an explicit example

If the specification has no `example`/`default`, the generator tries to build an example from the schema:

- `enum` — the first element;
- `object` — properties in alphabetical order (up to 20 properties);
- `array` — one example element;
- `string` — a placeholder string or a typical value for `date`, `date-time`, `uuid`, `email`, `uri`;
- `integer` — `1`, `number` — `1`, `boolean` — `true`;
- `allOf` — merging the properties of the object parts; `oneOf`/`anyOf` — an example of the first alternative;
- local `$ref` — following to the described schema with recursion protection.

These are demonstration data, not a guaranteed valid business example. Mandatory business rules, field interdependencies, and constraints may require manual configuration.

## Warnings and limitations

The generator intentionally creates initial **response stubs**, not a full contract validator. It does not convert the following into `when` conditions:

- `parameters`, `requestBody`, and incoming body schemas;
- authorization rules from `security`;
- callbacks and webhooks;
- OpenAPI 3.x server URLs (routes are created from `paths` keys); for Swagger 2.0, `basePath` is taken into account.

Local `$ref` values for schemas and responses are supported. External `$ref` and `$ref` for a Path Item are not loaded; unsupported references are output as warnings. For response content with an XML/other non-JSON type, an explicit text example is used if present; if only an object schema without a text example is available, an empty body is created with a warning. A full XML serializer from JSON Schema is not performed.

After generation, it is recommended to:

1. check the generator warnings;
2. add `when` conditions for parameters, headers, and the request body;
3. if necessary, create separate error scenarios (`400`, `401`, `404`, `500`) and response sequences;
4. include the created file in `scenario_files` in `config/server.toml`;
5. run `-check-config` to validate the entire server configuration.

## Connecting to the server

In `config/server.toml`:

```toml
scenario_files = [
  "../scenarios/generated.mock",
  "../scenarios/orders.mock",
]
```

Then:

```powershell
go run ./cmd/mockserver -config .\config\server.toml -check-config
go run ./cmd/mockserver -config .\config\server.toml
```
