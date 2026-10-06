package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dataKeyed lists objects keyed by lender code rather than schema field name.
var dataKeyed = map[string]bool{"deals_by_donor": true, "active_by_donor": true, "inquiries_by_org": true}

// collectKeys adds every object key in a decoded JSON value to keys.
func collectKeys(v any, keys map[string]bool) {
	switch v := v.(type) {
	case map[string]any:
		for k, child := range v {
			keys[k] = true
			if !dataKeyed[k] {
				collectKeys(child, keys)
			}
		}
	case []any:
		for _, child := range v {
			collectKeys(child, keys)
		}
	}
}

// The OpenAPI schema must document every key in the golden payloads and every API error
// code.
func TestOpenAPIDocumentsTheWholeContract(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	documented := make(map[string]bool)
	for _, m := range regexp.MustCompile(`(?m)^\s+([a-z0-9_]+):`).FindAllStringSubmatch(string(spec), -1) {
		documented[m[1]] = true
	}

	goldens, err := filepath.Glob(filepath.Join("..", "..", "rating", "testdata", "golden", "*.json"))
	if err != nil || len(goldens) == 0 {
		t.Fatalf("no golden payloads found: %v", err)
	}
	keys := make(map[string]bool)
	for _, path := range goldens {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var payload any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		collectKeys(payload, keys)
	}
	for key := range keys {
		if !documented[key] {
			t.Errorf("the payload carries %q, which api/openapi.yaml does not document", key)
		}
	}

	for _, code := range []string{
		"invalid_request", "missing_report", "invalid_report",
		"method_not_allowed", "report_too_large", "unsupported_media_type",
	} {
		if !strings.Contains(string(spec), code) {
			t.Errorf("error code %q is not documented", code)
		}
	}
}
