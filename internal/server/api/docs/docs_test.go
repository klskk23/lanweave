package docs

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSpecYAMLIsValidOpenAPI proves the embedded document is parseable YAML
// with the OpenAPI 3 shape (US3: standard tooling can consume it).
func TestSpecYAMLIsValidOpenAPI(t *testing.T) {
	var doc struct {
		OpenAPI string `yaml:"openapi"`
		Info    struct {
			Title   string `yaml:"title"`
			Version string `yaml:"version"`
		} `yaml:"info"`
		Paths map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(SpecYAML(), &doc); err != nil {
		t.Fatalf("openapi.yaml does not parse: %v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		t.Errorf("openapi version = %q, want 3.x", doc.OpenAPI)
	}
	if doc.Info.Title == "" || doc.Info.Version == "" {
		t.Errorf("info.title/info.version must be set, got %q / %q", doc.Info.Title, doc.Info.Version)
	}
	if len(doc.Paths) == 0 {
		t.Error("paths is empty")
	}
}

// TestSpecYAMLContainsNoSecretShapes is a cheap tripwire for FR-009: example
// values must be obviously fictitious, never real key or token material.
func TestSpecYAMLContainsNoSecretShapes(t *testing.T) {
	spec := string(SpecYAML())
	for _, needle := range []string{
		"BEGIN ",      // PEM blocks (private keys, certificates)
		"eyJ",         // base64url '{"' — the start of every real JWT
		"PRIVATE KEY", // belt and braces
	} {
		if strings.Contains(spec, needle) {
			t.Errorf("openapi.yaml contains secret-shaped content %q", needle)
		}
	}
}

// TestFile pins the served artifact set: the four known names and the not-ok
// fallback for anything else.
func TestFile(t *testing.T) {
	for name, wantCT := range map[string]string{
		"":                     "text/html; charset=utf-8",
		"openapi.yaml":         "application/yaml",
		"swagger-ui.css":       "text/css; charset=utf-8",
		"swagger-ui-bundle.js": "text/javascript; charset=utf-8",
	} {
		body, ct, ok := File(name)
		if !ok {
			t.Errorf("File(%q) not ok", name)
			continue
		}
		if len(body) == 0 {
			t.Errorf("File(%q) returned empty body", name)
		}
		if ct != wantCT {
			t.Errorf("File(%q) Content-Type = %q, want %q", name, ct, wantCT)
		}
	}
	if _, _, ok := File("no-such-asset.js"); ok {
		t.Error("File(unknown) must not be ok")
	}
}
