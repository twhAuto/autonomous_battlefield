package apicontract

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

var httpMethods = []string{"get", "put", "post", "delete", "patch", "head", "options"}

// specification is the parsed document, kept generic so the test does not need an
// OpenAPI library and never pins more of the structure than it checks.
type specification map[string]any

func loadSpecification(t *testing.T) specification {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "docs", "reference", "openapi.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read OpenAPI document: %v", err)
	}
	// Decode into a plain map: yaml.v3 gives nested mappings the destination's named
	// type, which would defeat the map[string]any assertions below.
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse OpenAPI document: %v", err)
	}
	// Guard against a parse that silently yields nothing: every check below iterates
	// over operations and would otherwise pass vacuously.
	if len(specification(document).operations()) == 0 {
		t.Fatal("OpenAPI document parsed but contains no operations")
	}
	return specification(document)
}

func asMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

// operations yields every "METHOD /path" in the document with its operation object.
func (s specification) operations() map[string]map[string]any {
	result := map[string]map[string]any{}
	for path, item := range asMap(s["paths"]) {
		for _, method := range httpMethods {
			if operation := asMap(asMap(item)[method]); operation != nil {
				result[strings.ToUpper(method)+" "+path] = operation
			}
		}
	}
	return result
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// The document must list exactly the routes RoutePolicy serves. A route added to
// the code without the spec, or documented but never served, fails here.
func TestSpecificationMatchesTheServedRoutes(t *testing.T) {
	documented := loadSpecification(t).operations()
	served := map[string]bool{}
	for _, route := range authn.PublicAPIRoutes() {
		served[route] = true
	}

	var missing, stale []string
	for route := range served {
		if _, ok := documented[route]; !ok {
			missing = append(missing, route)
		}
	}
	for route := range documented {
		if !served[route] {
			stale = append(stale, route)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("served but not documented in openapi.yaml:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("documented in openapi.yaml but not served:\n  %s", strings.Join(stale, "\n  "))
	}
}

func TestOperationIdentifiersAreUnique(t *testing.T) {
	seen := map[string]string{}
	operations := loadSpecification(t).operations()
	for _, route := range sortedKeys(operations) {
		id, _ := operations[route]["operationId"].(string)
		if id == "" {
			t.Errorf("%s has no operationId", route)
			continue
		}
		if previous, ok := seen[id]; ok {
			t.Errorf("operationId %q is used by both %s and %s", id, previous, route)
		}
		seen[id] = route
	}
}

// Every path template parameter must be declared at the path or operation level.
func TestPathParametersAreDeclared(t *testing.T) {
	document := loadSpecification(t)
	placeholder := regexp.MustCompile(`\{([^}]+)\}`)
	for _, path := range sortedKeys(asMap(document["paths"])) {
		item := asMap(asMap(document["paths"])[path])
		for _, method := range httpMethods {
			operation := asMap(item[method])
			if operation == nil {
				continue
			}
			declared := document.declaredPathParameters(t, item, operation)
			for _, match := range placeholder.FindAllStringSubmatch(path, -1) {
				if !declared[match[1]] {
					t.Errorf("%s %s: path parameter {%s} is not declared", strings.ToUpper(method), path, match[1])
				}
			}
		}
	}
}

// declaredPathParameters names the path parameters declared on the path item or the
// operation, following references.
func (s specification) declaredPathParameters(t *testing.T, item, operation map[string]any) map[string]bool {
	t.Helper()
	declared := map[string]bool{}
	for _, source := range []any{item["parameters"], operation["parameters"]} {
		list, _ := source.([]any)
		for _, entry := range list {
			parameter := s.resolve(t, asMap(entry))
			if parameter["in"] == "path" {
				declared[parameter["name"].(string)] = true
			}
		}
	}
	return declared
}

// resolve follows a local $ref, failing the test when the target does not exist.
func (s specification) resolve(t *testing.T, node map[string]any) map[string]any {
	t.Helper()
	reference, ok := node["$ref"].(string)
	if !ok {
		return node
	}
	target := s.lookup(reference)
	if target == nil {
		t.Fatalf("unresolved reference %s", reference)
	}
	return target
}

func (s specification) lookup(reference string) map[string]any {
	if !strings.HasPrefix(reference, "#/") {
		return nil
	}
	var current any = map[string]any(s)
	for _, part := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		current = asMap(current)[part]
	}
	return asMap(current)
}

// Every local $ref anywhere in the document must point at something.
func TestAllReferencesResolve(t *testing.T) {
	document := loadSpecification(t)
	var walk func(any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if reference, ok := value["$ref"].(string); ok && document.lookup(reference) == nil {
				t.Errorf("unresolved reference %s", reference)
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(map[string]any(document))
}

// Clients branch on the envelope's code, so every documented error response must use
// it. The unauthenticated session read is the one deliberate exception: its 401
// returns a session body so clients can learn the authentication mode.
func TestErrorResponsesUseTheEnvelope(t *testing.T) {
	document := loadSpecification(t)
	operations := document.operations()
	for _, route := range sortedKeys(operations) {
		responses := asMap(operations[route]["responses"])
		successful := false
		for _, status := range sortedKeys(responses) {
			if strings.HasPrefix(status, "2") {
				successful = true
				continue
			}
			if route == "GET /v1/auth/session" && status == "401" {
				continue
			}
			response := document.resolve(t, asMap(responses[status]))
			schema := asMap(asMap(asMap(response["content"])["application/json"])["schema"])
			if !usesEnvelope(schema) {
				t.Errorf("%s %s: error response does not use ErrorEnvelope", route, status)
			}
		}
		if !successful {
			t.Errorf("%s declares no 2xx response", route)
		}
	}
}

func usesEnvelope(schema map[string]any) bool {
	if schema["$ref"] == "#/components/schemas/ErrorEnvelope" {
		return true
	}
	parts, _ := schema["allOf"].([]any)
	for _, part := range parts {
		if usesEnvelope(asMap(part)) {
			return true
		}
	}
	return false
}

// Strict decoding rejects unknown fields, so request schemas must say the same.
func TestRequestBodiesRejectUnknownFields(t *testing.T) {
	document := loadSpecification(t)
	operations := document.operations()
	for _, route := range sortedKeys(operations) {
		body := asMap(operations[route]["requestBody"])
		if body == nil {
			continue
		}
		schema := document.resolve(t, asMap(asMap(asMap(body["content"])["application/json"])["schema"]))
		if schema["additionalProperties"] != false {
			t.Errorf("%s: request schema must set additionalProperties: false", route)
		}
	}
}

// BrowserProtection rejects a mutation without the configured Origin, so every
// mutating operation must tell clients to send it.
func TestMutationsDeclareTheOriginHeader(t *testing.T) {
	document := loadSpecification(t)
	operations := document.operations()
	for _, route := range sortedKeys(operations) {
		if strings.HasPrefix(route, "GET ") {
			continue
		}
		declared := false
		parameters, _ := operations[route]["parameters"].([]any)
		for _, entry := range parameters {
			parameter := document.resolve(t, asMap(entry))
			if parameter["in"] == "header" && parameter["name"] == "Origin" {
				declared = true
			}
		}
		if !declared {
			t.Errorf("%s: mutating operation does not declare the Origin header", route)
		}
	}
}
