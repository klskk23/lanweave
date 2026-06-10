package api

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"lanweave/internal/server/api/docs"
)

// knownErrorCodes is the full set of machine codes the server can emit
// (every WriteJSONError callsite). Documented codes must be a subset.
var knownErrorCodes = map[string]bool{
	"validation_error": true, "unauthorized": true, "forbidden": true,
	"not_found": true, "method_not_allowed": true, "rate_limited": true,
	"internal_error": true, "invalid_credentials": true, "invalid_refresh_token": true,
	"invite_invalid": true, "username_taken": true, "pubkey_taken": true,
	"node_name_taken": true, "zone_name_taken": true, "invalid_zone_or_password": true,
	"device_limit_reached": true, "zone_limit_reached": true, "pool_exhausted": true,
	"last_admin": true, "cannot_delete_self": true,
	// Subnet announcements (feature 030).
	"platform_unsupported": true, "announce_disabled": true, "subnet_invalid": true,
	"subnet_overlap": true, "announce_limit_reached": true, "synthetic_pool_exhausted": true,
}

// routeOperations derives the documented-comparable (METHOD path) set from the
// live route table. A pattern without a method prefix (healthz's historical
// method-agnostic registration) normalizes to GET.
func routeOperations(t *testing.T) map[string]bool {
	t.Helper()
	ops := map[string]bool{}
	// Zero-value handlers/Options (nil JWT, nil store) are safe here because the
	// route table is only enumerated for its patterns; no handler is ever invoked.
	for _, rt := range routes(&handlers{}, Options{}) {
		method, path, found := strings.Cut(rt.pattern, " ")
		if !found {
			method, path = "GET", rt.pattern
		}
		ops[method+" "+path] = true
	}
	return ops
}

// specOperations extracts the (METHOD path) set from the embedded OpenAPI
// document's paths object.
func specOperations(t *testing.T) map[string]bool {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(docs.SpecYAML(), &doc); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	httpMethods := map[string]bool{
		"get": true, "post": true, "put": true, "patch": true,
		"delete": true, "head": true, "options": true, "trace": true,
	}
	ops := map[string]bool{}
	for path, item := range doc.Paths {
		for key := range item {
			if httpMethods[key] {
				ops[strings.ToUpper(key)+" "+path] = true
			}
		}
	}
	return ops
}

// TestOpenAPIMatchesRouteTable is the drift sentinel (FR-010): the documented
// operation set and the registered route table must be equal in both
// directions. Adding or removing an endpoint without touching openapi.yaml
// fails here.
func TestOpenAPIMatchesRouteTable(t *testing.T) {
	routeOps := routeOperations(t)
	specOps := specOperations(t)

	var missing, extra []string
	for op := range routeOps {
		if !specOps[op] {
			missing = append(missing, op)
		}
	}
	for op := range specOps {
		if !routeOps[op] {
			extra = append(extra, op)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("registered but undocumented operations (add them to openapi.yaml):\n  %s",
			strings.Join(missing, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("documented but unregistered operations (remove them from openapi.yaml):\n  %s",
			strings.Join(extra, "\n  "))
	}
}

// TestDocumentedErrorCodesAreKnown walks the whole document and asserts every
// x-error-codes entry names a code the server actually emits — a typo or a
// stale code in the docs fails here.
func TestDocumentedErrorCodesAreKnown(t *testing.T) {
	var doc any
	if err := yaml.Unmarshal(docs.SpecYAML(), &doc); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	codes := map[string]bool{}
	collectErrorCodes(t, doc, codes)
	if len(codes) == 0 {
		t.Fatal("no x-error-codes found in openapi.yaml; the error documentation went missing")
	}
	for code := range codes {
		if !knownErrorCodes[code] {
			t.Errorf("documented error code %q is not emitted by the server", code)
		}
	}
}

func collectErrorCodes(t *testing.T, node any, out map[string]bool) {
	t.Helper()
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "x-error-codes" {
				list, ok := child.([]any)
				if !ok {
					t.Errorf("x-error-codes is %T, want a list", child)
					continue
				}
				for _, item := range list {
					out[fmt.Sprint(item)] = true
				}
				continue
			}
			collectErrorCodes(t, child, out)
		}
	case []any:
		for _, child := range v {
			collectErrorCodes(t, child, out)
		}
	}
}
