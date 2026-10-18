package matcher

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"mock-integration-server/internal/config"
)

func TestMatchPathParamsAndPriority(t *testing.T) {
	c := &config.Config{
		Routes: []config.Route{{ID: "r", Protocol: "http", Path: "/api/files/{fileId}", Methods: []string{"GET"}, Scenarios: []string{"low", "high"}}},
		Scenarios: []config.Scenario{
			{ID: "low", Priority: 1, Match: config.Match{}, Actions: []config.Action{{Type: "respond", Status: 200}}},
			{ID: "high", Priority: 10, Match: config.Match{PathParams: map[string]config.StringCondition{"fileId": {Equals: "invoice-1"}}}, Actions: []config.Action{{Type: "respond", Status: 200}}},
		},
	}
	u, _ := url.Parse("/api/files/invoice-1")
	got, err := New(c).Match(Request{Method: http.MethodGet, URL: u, Header: http.Header{}}, "http")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Scenario.ID != "high" {
		t.Fatalf("expected high priority matching scenario, got %#v", got)
	}
}

func TestAmbiguousScenariosFail(t *testing.T) {
	c := &config.Config{Routes: []config.Route{{ID: "r", Protocol: "http", Path: "/x", Scenarios: []string{"a", "b"}}}, Scenarios: []config.Scenario{{ID: "a", Priority: 5}, {ID: "b", Priority: 5}}}
	u, _ := url.Parse("/x")
	if _, err := New(c).Match(Request{Method: "GET", URL: u, Header: http.Header{}}, "http"); err == nil {
		t.Fatal("expected ambiguity error")
	}
}

func TestJSONPathArrayIndexAndQuotedKey(t *testing.T) {
	var root any
	if err := json.Unmarshal([]byte(`{"items":[{"sku":"A-1"}],"odd.key":7}`), &root); err != nil {
		t.Fatal(err)
	}
	if got, ok := JSONPathValue(root, "$.items[0].sku"); !ok || got != "A-1" {
		t.Fatalf("array path got %v, %v", got, ok)
	}
	if got, ok := JSONPathValue(root, `$['odd.key']`); !ok || got != float64(7) {
		t.Fatalf("quoted path got %v, %v", got, ok)
	}
}

func TestMatchJSONConditionsOperators(t *testing.T) {
	gt := 4.0
	ok, err := MatchJSONConditions([]byte(`{"name":"order-created","amount":12,"tags":["urgent","api"]}`), map[string]config.ValueCondition{
		"$.name":   {Contains: "created"},
		"$.amount": {GreaterThan: &gt},
		"$.tags":   {Contains: "api", Type: "array"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected JSON conditions to match")
	}
}

func TestJSONNullAndEmptyStringCanBeMatched(t *testing.T) {
	var root any
	if err := json.Unmarshal([]byte(`{"nullable":null,"empty":""}`), &root); err != nil {
		t.Fatal(err)
	}
	ok, err := MatchJSONConditions([]byte(`{"nullable":null,"empty":""}`), map[string]config.ValueCondition{
		"$.nullable": {EqualsSet: true, Equals: nil},
		"$.empty":    {Equals: "", EqualsSet: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected null and empty string conditions to match")
	}
}

func TestJSONPathWildcardAndRecursiveDescent(t *testing.T) {
	root := map[string]any{
		"items":  []any{map[string]any{"sku": "A"}, map[string]any{"sku": "B"}},
		"nested": map[string]any{"id": "C"},
	}
	got, ok := JSONPathValue(root, "$.items[*].sku")
	if !ok {
		t.Fatal("wildcard path not found")
	}
	values, ok := got.([]any)
	if !ok || len(values) != 2 || values[0] != "A" || values[1] != "B" {
		t.Fatalf("unexpected wildcard result: %#v", got)
	}
	got, ok = JSONPathValue(root, "$..id")
	if !ok || got != "C" {
		t.Fatalf("unexpected recursive result: %#v, %v", got, ok)
	}
}

func TestJSONPathFilterAndSlice(t *testing.T) {
	root := map[string]any{"items": []any{map[string]any{"price": float64(3)}, map[string]any{"price": float64(12)}, map[string]any{"price": float64(20)}}}
	got, ok := JSONPathValue(root, "$.items[?(@.price >= 10)].price")
	if !ok {
		t.Fatal("filter did not match")
	}
	values, ok := got.([]any)
	if !ok || len(values) != 2 || values[0] != float64(12) || values[1] != float64(20) {
		t.Fatalf("unexpected filter result: %#v", got)
	}
	got, ok = JSONPathValue(root, "$.items[1:3].price")
	values, ok = got.([]any)
	if !ok || len(values) != 2 || values[0] != float64(12) || values[1] != float64(20) {
		t.Fatalf("unexpected slice result: %#v", got)
	}
}

func TestXMLPathValue(t *testing.T) {
	xml := []byte(`<order><item id="a">one</item><item id="b">two</item></order>`)
	if got, ok, err := XMLPathValue(xml, "/order/item[2]/@id"); err != nil || !ok || got != "b" {
		t.Fatalf("attribute XPath = %q, %v, %v", got, ok, err)
	}
	if got, ok, err := XMLPathValue(xml, "//item[2]/text()"); err != nil || !ok || got != "two" {
		t.Fatalf("text XPath = %q, %v, %v", got, ok, err)
	}
}
