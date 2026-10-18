// Package openapi converts Swagger/OpenAPI descriptions into Mock Integration Server DSL.
package openapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Report struct {
	Version    string
	Operations int
	Warnings   []string
}

type document struct {
	root    map[string]any
	version string
	family  string
}

var operationMethods = []string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}
var validStatus = regexp.MustCompile(`^[1-5][0-9][0-9]$`)
var invalidID = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// Generate converts JSON or YAML Swagger/OpenAPI input into DSL. requestedVersion
// accepts "auto", "2.0", "3.0", "3.1", or "3.2".
func Generate(input []byte, source, requestedVersion string) ([]byte, Report, error) {
	root := map[string]any{}
	if err := yaml.Unmarshal(input, &root); err != nil {
		return nil, Report{}, fmt.Errorf("parse OpenAPI document %s: %w", source, err)
	}
	if root == nil {
		return nil, Report{}, fmt.Errorf("OpenAPI document %s is empty or not an object", source)
	}
	doc, err := detectVersion(root, requestedVersion)
	if err != nil {
		return nil, Report{}, fmt.Errorf("OpenAPI document %s: %w", source, err)
	}
	paths, ok := asMap(root["paths"])
	if !ok || len(paths) == 0 {
		return nil, Report{}, fmt.Errorf("OpenAPI document %s: paths must be a non-empty object", source)
	}
	g := generator{doc: doc, report: Report{Version: doc.version}}
	g.writeHeader(source)
	pathNames := sortedKeys(paths)
	basePath := ""
	if doc.family == "2" {
		basePath, _ = root["basePath"].(string)
		if basePath != "" && !strings.HasPrefix(basePath, "/") {
			basePath = "/" + basePath
		}
		basePath = strings.TrimSuffix(basePath, "/")
	}
	for _, p := range pathNames {
		pathItem, ok := asMap(paths[p])
		if !ok {
			g.report.Warnings = append(g.report.Warnings, fmt.Sprintf("path %q is not an object and was skipped", p))
			continue
		}
		if ref, ok := pathItem["$ref"].(string); ok && ref != "" {
			g.report.Warnings = append(g.report.Warnings, fmt.Sprintf("path %q uses $ref %q; referenced Path Item objects are not expanded and were skipped", p, ref))
			continue
		}
		for _, method := range operationMethods {
			op, ok := asMap(pathItem[method])
			if !ok {
				continue
			}
			fullPath := p
			if basePath != "" {
				fullPath = basePath + "/" + strings.TrimPrefix(p, "/")
			}
			g.operation(fullPath, strings.ToUpper(method), op)
		}
	}
	if g.report.Operations == 0 {
		return nil, g.report, fmt.Errorf("OpenAPI document %s contains no supported HTTP operations", source)
	}
	g.report.Warnings = append(g.report.Warnings, "generated scenarios are response stubs: request parameters/body validation, authentication/security rules, callbacks, webhooks, and server URL prefixes are not converted to DSL match conditions")
	return []byte(g.out.String()), g.report, nil
}

func detectVersion(root map[string]any, requested string) (document, error) {
	swagger, _ := root["swagger"].(string)
	openapi, _ := root["openapi"].(string)
	actual := ""
	family := ""
	switch {
	case swagger == "2.0":
		actual, family = "2.0", "2"
	case swagger != "":
		return document{}, fmt.Errorf("unsupported Swagger version %q (supported: 2.0)", swagger)
	case strings.HasPrefix(openapi, "3.0."):
		actual, family = openapi, "3.0"
	case strings.HasPrefix(openapi, "3.1."):
		actual, family = openapi, "3.1"
	case strings.HasPrefix(openapi, "3.2."):
		actual, family = openapi, "3.2"
	case openapi != "":
		return document{}, fmt.Errorf("unsupported OpenAPI version %q (supported: 3.0.x, 3.1.x, 3.2.x)", openapi)
	default:
		return document{}, fmt.Errorf("cannot detect version: expected swagger: '2.0' or openapi: '3.x.y'")
	}
	requested = strings.TrimSpace(strings.ToLower(requested))
	if requested == "" || requested == "auto" {
		return document{root: root, version: actual, family: family}, nil
	}
	requested = strings.TrimPrefix(requested, "openapi-")
	requested = strings.TrimPrefix(requested, "swagger-")
	compatible := false
	switch requested {
	case "2", "2.0":
		compatible = family == "2"
	case "3.0":
		compatible = family == "3.0"
	case "3.1":
		compatible = family == "3.1"
	case "3.2":
		compatible = family == "3.2"
	default:
		return document{}, fmt.Errorf("invalid -openapi-version %q (use auto, 2.0, 3.0, 3.1, or 3.2)", requested)
	}
	if !compatible {
		return document{}, fmt.Errorf("requested version family %q does not match document version %q", requested, actual)
	}
	return document{root: root, version: actual, family: family}, nil
}

type generator struct {
	doc    document
	out    strings.Builder
	report Report
}

func (g *generator) writeHeader(source string) {
	g.out.WriteString("# Generated from OpenAPI/Swagger by Mock Integration Server.\n")
	g.out.WriteString("# Source: " + oneLine(source) + "\n")
	g.out.WriteString("# Specification version: " + g.doc.version + "\n")
	g.out.WriteString("# Review generated examples and enrich request matching as needed.\n\n")
}

func (g *generator) operation(routePath, method string, op map[string]any) {
	operationID, _ := op["operationId"].(string)
	base := operationID
	if base == "" {
		base = strings.ToLower(method) + "_" + strings.Trim(routePath, "/")
	}
	base = invalidID.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-_ ")
	if base == "" {
		base = strings.ToLower(method) + "-operation"
	}
	id := base
	// Route IDs must be unique even when operationId is duplicated.
	if g.hasID(id) {
		id = fmt.Sprintf("%s-%d", base, g.report.Operations+1)
	}
	scenarioID := "openapi-" + id
	if summary, ok := op["summary"].(string); ok && strings.TrimSpace(summary) != "" {
		g.out.WriteString("# " + oneLine(summary) + "\n")
	}
	g.out.WriteString("route " + quote(id) + " {\n")
	g.out.WriteString("  protocol: http\n")
	g.out.WriteString("  path: " + quote(openAPIPath(routePath)) + "\n")
	g.out.WriteString("  methods: [" + method + "]\n")
	g.out.WriteString("  scenarios: [" + quote(scenarioID) + "]\n")
	g.out.WriteString("}\n\n")

	status, response, found := g.chooseResponse(op)
	g.out.WriteString("scenario " + quote(scenarioID) + " {\n")
	g.out.WriteString("  priority: 0\n")
	g.out.WriteString("  actions: [\n")
	if !found {
		g.report.Warnings = append(g.report.Warnings, fmt.Sprintf("%s %s: no responses declared; generated empty 200 response", method, routePath))
		status, response = "200", map[string]any{}
	}
	g.writeResponseAction(status, response, op)
	g.out.WriteString("  ]\n")
	g.out.WriteString("}\n\n")
	g.report.Operations++
}

func (g *generator) hasID(id string) bool {
	return strings.Contains(g.out.String(), "route \""+id+"\" {")
}

func (g *generator) chooseResponse(op map[string]any) (string, map[string]any, bool) {
	responses, ok := asMap(op["responses"])
	if !ok || len(responses) == 0 {
		return "", nil, false
	}
	keys := sortedKeys(responses)
	// Prefer common success codes, then any explicit 2xx, then default/first response.
	for _, preferred := range []string{"200", "201", "202", "204"} {
		if v, ok := g.resolveObject(responses[preferred]); ok {
			return preferred, v, true
		}
	}
	for _, k := range keys {
		if validStatus.MatchString(k) && strings.HasPrefix(k, "2") {
			if v, ok := g.resolveObject(responses[k]); ok {
				return k, v, true
			}
		}
	}
	for _, k := range keys {
		if v, ok := g.resolveObject(responses[k]); ok {
			if k == "default" {
				return "200", v, true
			}
			if validStatus.MatchString(k) {
				return k, v, true
			}
		}
	}
	return "", nil, false
}

func (g *generator) resolveObject(value any) (map[string]any, bool) {
	m, ok := asMap(value)
	if !ok {
		return nil, false
	}
	if ref, ok := m["$ref"].(string); ok {
		resolved, found := g.resolveRef(ref)
		if !found {
			g.report.Warnings = append(g.report.Warnings, "unresolved response reference "+ref)
			return nil, false
		}
		return resolved, true
	}
	return m, true
}

func (g *generator) writeResponseAction(status string, response map[string]any, operation map[string]any) {
	code, err := strconv.Atoi(status)
	if err != nil || code < 100 || code > 599 {
		code = 200
	}
	media, value, hasBody := g.responseBody(response, operation)
	if code == 204 || code == 304 {
		hasBody = false
	}
	headers := g.responseHeaders(response)
	if hasBody && media != "" {
		if headers == nil {
			headers = map[string]string{}
		}
		if _, exists := headers["Content-Type"]; !exists {
			headers["Content-Type"] = media
		}
	}
	g.out.WriteString("    respond {\n")
	g.out.WriteString(fmt.Sprintf("      status: %d\n", code))
	if len(headers) > 0 {
		g.out.WriteString("      headers: ")
		g.writeValue(headers, 6)
		g.out.WriteString("\n")
	}
	if !hasBody {
		g.out.WriteString("      empty: true\n")
	} else if isJSONMedia(media) || (media == "" && (isObjectOrArray(value))) {
		g.out.WriteString("      json: ")
		g.writeValue(value, 6)
		g.out.WriteString("\n")
	} else if text, ok := value.(string); ok {
		g.out.WriteString("      text: ")
		g.writeValue(text, 6)
		g.out.WriteString("\n")
	} else {
		g.report.Warnings = append(g.report.Warnings, fmt.Sprintf("response media type %q has a structured schema but no textual example; generated an empty body", media))
		g.out.WriteString("      empty: true\n")
	}
	g.out.WriteString("    }\n")
}

func isObjectOrArray(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}

func (g *generator) responseHeaders(response map[string]any) map[string]string {
	headers := map[string]string{}
	if g.doc.family == "2" {
		if raw, ok := asMap(response["headers"]); ok {
			for k, v := range raw {
				if vm, ok := asMap(v); ok {
					if ex, exists := vm["example"]; exists {
						headers[k] = fmt.Sprint(ex)
					} else if d, exists := vm["default"]; exists {
						headers[k] = fmt.Sprint(d)
					}
				}
			}
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func (g *generator) responseBody(response map[string]any, operation map[string]any) (string, any, bool) {
	if g.doc.family == "2" {
		if examples, ok := asMap(response["examples"]); ok {
			for _, media := range sortedKeys(examples) {
				return media, examples[media], true
			}
		}
		schema, ok := asMap(response["schema"])
		if !ok {
			return "", nil, false
		}
		media := ""
		if operation != nil {
			if produces, ok := operation["produces"].([]any); ok && len(produces) > 0 {
				media, _ = produces[0].(string)
			}
		}
		if media == "" {
			if produces, ok := g.doc.root["produces"].([]any); ok && len(produces) > 0 {
				media, _ = produces[0].(string)
			}
		}
		if media == "" {
			media = "application/json"
		}
		return media, g.exampleForSchema(schema, map[string]bool{}), true
	}
	content, ok := asMap(response["content"])
	if !ok || len(content) == 0 {
		return "", nil, false
	}
	mediaTypes := sortedKeys(content)
	// Prefer JSON, then structured +json media types, then the first documented type.
	chosen := mediaTypes[0]
	for _, media := range mediaTypes {
		if strings.EqualFold(media, "application/json") {
			chosen = media
			break
		}
	}
	if chosen != "application/json" {
		for _, media := range mediaTypes {
			if strings.HasSuffix(strings.ToLower(media), "+json") {
				chosen = media
				break
			}
		}
	}
	item, _ := asMap(content[chosen])
	if example, ok := item["example"]; ok {
		return chosen, example, true
	}
	if examples, ok := asMap(item["examples"]); ok && len(examples) > 0 {
		for _, name := range sortedKeys(examples) {
			if ex, ok := asMap(examples[name]); ok {
				if v, exists := ex["value"]; exists {
					return chosen, v, true
				}
				if ref, exists := ex["$ref"]; exists {
					g.report.Warnings = append(g.report.Warnings, fmt.Sprintf("response example %q is a $ref and was skipped", ref))
				}
			}
		}
	}
	if schema, ok := asMap(item["schema"]); ok {
		return chosen, g.exampleForSchema(schema, map[string]bool{}), true
	}
	return chosen, nil, false
}

func (g *generator) exampleForSchema(schema map[string]any, stack map[string]bool) any {
	if ref, ok := schema["$ref"].(string); ok {
		if stack[ref] {
			return map[string]any{}
		}
		resolved, found := g.resolveRef(ref)
		if !found {
			g.report.Warnings = append(g.report.Warnings, "unresolved schema reference "+ref)
			return map[string]any{}
		}
		stack[ref] = true
		value := g.exampleForSchema(resolved, stack)
		delete(stack, ref)
		return value
	}
	for _, key := range []string{"example", "default"} {
		if v, ok := schema[key]; ok {
			return v
		}
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	for _, combiner := range []string{"oneOf", "anyOf", "allOf"} {
		if list, ok := schema[combiner].([]any); ok && len(list) > 0 {
			if combiner == "allOf" {
				merged := map[string]any{}
				for _, item := range list {
					if m, ok := asMap(item); ok {
						if obj, ok := g.exampleForSchema(m, stack).(map[string]any); ok {
							for k, v := range obj {
								merged[k] = v
							}
						}
					}
				}
				return merged
			}
			if m, ok := asMap(list[0]); ok {
				return g.exampleForSchema(m, stack)
			}
		}
	}
	typ, _ := schema["type"].(string)
	if typeList, ok := schema["type"].([]any); ok {
		for _, candidate := range typeList {
			if candidate != "null" {
				if s, ok := candidate.(string); ok {
					typ = s
					break
				}
			}
		}
	}
	if typ == "" {
		if _, ok := schema["properties"]; ok {
			typ = "object"
		}
		if _, ok := schema["items"]; ok {
			typ = "array"
		}
	}
	switch typ {
	case "object":
		result := map[string]any{}
		props, _ := asMap(schema["properties"])
		for _, key := range sortedKeys(props) {
			if len(result) >= 20 {
				break
			}
			if child, ok := asMap(props[key]); ok {
				result[key] = g.exampleForSchema(child, stack)
			}
		}
		return result
	case "array":
		items, ok := asMap(schema["items"])
		if !ok {
			return []any{}
		}
		return []any{g.exampleForSchema(items, stack)}
	case "string":
		format, _ := schema["format"].(string)
		switch format {
		case "date":
			return "2026-01-01"
		case "date-time":
			return "2026-01-01T00:00:00Z"
		case "uuid":
			return "00000000-0000-4000-8000-000000000001"
		case "email":
			return "user@example.com"
		case "uri", "url":
			return "https://example.com/resource"
		case "binary", "byte":
			return ""
		default:
			return "string"
		}
	case "integer":
		return int64(1)
	case "number":
		return float64(1)
	case "boolean":
		return true
	case "null":
		return nil
	default:
		return map[string]any{}
	}
}

func (g *generator) resolveRef(ref string) (map[string]any, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	var current any = g.doc.root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := asMap(current)
		if !ok {
			return nil, false
		}
		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	m, ok := asMap(current)
	return m, ok
}

func (g *generator) writeValue(value any, indent int) {
	pad := strings.Repeat(" ", indent)
	switch v := value.(type) {
	case nil:
		g.out.WriteString("null")
	case string:
		g.out.WriteString(quote(v))
	case bool:
		if v {
			g.out.WriteString("true")
		} else {
			g.out.WriteString("false")
		}
	case int:
		g.out.WriteString(strconv.Itoa(v))
	case int64:
		g.out.WriteString(strconv.FormatInt(v, 10))
	case float64:
		g.out.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
	case float32:
		g.out.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	case map[string]string:
		if len(v) == 0 {
			g.out.WriteString("{}")
			return
		}
		g.out.WriteString("{\n")
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			g.out.WriteString(pad + "  " + quote(key) + ": " + quote(v[key]) + "\n")
		}
		g.out.WriteString(pad + "}")
	case map[string]any:
		if len(v) == 0 {
			g.out.WriteString("{}")
			return
		}
		g.out.WriteString("{\n")
		for _, key := range sortedKeys(v) {
			g.out.WriteString(pad + "  " + quote(key) + ": ")
			g.writeValue(v[key], indent+2)
			g.out.WriteString("\n")
		}
		g.out.WriteString(pad + "}")
	case []any:
		if len(v) == 0 {
			g.out.WriteString("[]")
			return
		}
		g.out.WriteString("[\n")
		for _, item := range v {
			g.out.WriteString(pad + "  ")
			g.writeValue(item, indent+2)
			g.out.WriteString("\n")
		}
		g.out.WriteString(pad + "]")
	default:
		g.out.WriteString(quote(fmt.Sprint(v)))
	}
}

func openAPIPath(p string) string {
	// OpenAPI uses {parameter}, which is already the route-template syntax.
	if p == "" {
		return "/"
	}
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}
func isJSONMedia(media string) bool {
	m := strings.ToLower(strings.TrimSpace(strings.Split(media, ";")[0]))
	return m == "application/json" || strings.HasSuffix(m, "+json") || m == "application/problem+json"
}
func quote(s string) string {
	// The DSL lexer understands these escapes; avoid JSON's \uXXXX escape form.
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
func oneLine(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ") }
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func asMap(v any) (map[string]any, bool) {
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	if m, ok := v.(map[any]any); ok {
		out := make(map[string]any, len(m))
		for k, val := range m {
			if s, ok := k.(string); ok {
				out[s] = val
			}
		}
		return out, true
	}
	return nil, false
}

// GenerateFile is a convenience helper used by the CLI.
func GenerateFile(inputPath, outputPath, requestedVersion string) (Report, error) {
	input, err := os.ReadFile(inputPath)
	if err != nil {
		return Report{}, fmt.Errorf("read OpenAPI input: %w", err)
	}
	result, report, err := Generate(input, inputPath, requestedVersion)
	if err != nil {
		return report, err
	}
	if outputPath == "-" {
		_, err = io.Copy(os.Stdout, bytes.NewReader(result))
		if err != nil {
			return report, fmt.Errorf("write generated DSL to stdout: %w", err)
		}
		return report, nil
	}
	if outputPath == "" {
		return report, fmt.Errorf("output path is required")
	}
	if err := os.WriteFile(outputPath, result, 0o644); err != nil {
		return report, fmt.Errorf("write generated DSL %q: %w", outputPath, err)
	}
	return report, nil
}

// VersionOf returns the detected Swagger/OpenAPI version without generating output.
func VersionOf(input []byte) (string, error) {
	root := map[string]any{}
	if err := yaml.Unmarshal(input, &root); err != nil {
		return "", err
	}
	doc, err := detectVersion(root, "auto")
	return doc.version, err
}

// MarshalJSON is exposed for tests/tools that need stable JSON examples.
func MarshalJSON(v any) ([]byte, error) { return json.Marshal(v) }
