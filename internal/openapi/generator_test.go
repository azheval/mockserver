package openapi

import (
	"strings"
	"testing"

	"mock-integration-server/internal/config"
)

func TestGenerateOpenAPI31AndParseGeneratedDSL(t *testing.T) {
	input := []byte(`openapi: 3.1.0
info:
  title: Orders API
  version: 1.0.0
paths:
  /orders/{orderId}:
    get:
      operationId: getOrder
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Order'
        '404':
          description: missing
components:
  schemas:
    Order:
      type: object
      required: [id, amount]
      properties:
        id:
          type: string
          example: ORD-100
        amount:
          type: number
          minimum: 1
        status:
          type: string
          enum: [new, done]
`)
	out, report, err := Generate(input, "orders.yaml", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if report.Version != "3.1.0" || report.Operations != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	text := string(out)
	for _, expected := range []string{`path: "/orders/{orderId}"`, `methods: [GET]`, `status: 200`, `"id": "ORD-100"`, `"amount": 1`, `"status": "new"`} {
		if !strings.Contains(text, expected) {
			t.Errorf("generated DSL does not contain %q:\n%s", expected, text)
		}
	}
	routes, scenarios, err := config.LoadDSL(out, "generated.mock")
	if err != nil {
		t.Fatalf("generated DSL is invalid: %v\n%s", err, out)
	}
	if len(routes) != 1 || len(scenarios) != 1 || scenarios[0].Actions[0].Status != 200 {
		t.Fatalf("unexpected parsed result: %#v %#v", routes, scenarios)
	}
}

func TestGenerateSwagger20(t *testing.T) {
	input := []byte(`swagger: '2.0'
info: {title: Legacy, version: '1'}
basePath: /v1
produces: [application/json]
paths:
  /health:
    get:
      operationId: health
      responses:
        '200':
          description: healthy
          schema:
            type: object
            properties:
              ok: {type: boolean, example: true}
`)
	out, report, err := Generate(input, "swagger.yaml", "2.0")
	if err != nil {
		t.Fatal(err)
	}
	if report.Version != "2.0" || report.Operations != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if !strings.Contains(string(out), `"ok": true`) {
		t.Fatalf("expected schema example in output:\n%s", out)
	}
	if !strings.Contains(string(out), `path: "/v1/health"`) {
		t.Fatalf("expected Swagger basePath to be applied:\n%s", out)
	}
	if !strings.Contains(string(out), `"Content-Type": "application/json"`) {
		t.Fatalf("expected response Content-Type header:\n%s", out)
	}
	if _, _, err := config.LoadDSL(out, "generated.mock"); err != nil {
		t.Fatalf("generated DSL invalid: %v\n%s", err, out)
	}
}

func TestVersionMismatchAndUnsupportedVersion(t *testing.T) {
	_, _, err := Generate([]byte("openapi: 3.1.0\npaths: {/x: {get: {responses: {'200': {description: ok}}}}}"), "api.yaml", "3.0")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected version mismatch, got %v", err)
	}
	_, _, err = Generate([]byte("openapi: 4.0.0\npaths: {}"), "api.yaml", "auto")
	if err == nil || !strings.Contains(err.Error(), "unsupported OpenAPI version") {
		t.Fatalf("expected unsupported version, got %v", err)
	}
}

func TestGenerateUsesResponseExampleAndEscapesDSLStrings(t *testing.T) {
	input := []byte(`openapi: 3.0.3
info: {title: T, version: '1'}
paths:
  /message:
    post:
      responses:
        '201':
          description: created
          content:
            application/json:
              example:
                message: "Привет\\nмир"
`)
	out, _, err := Generate(input, "api.yaml", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := config.LoadDSL(out, "generated.mock"); err != nil {
		t.Fatalf("generated DSL invalid: %v\n%s", err, out)
	}
}
