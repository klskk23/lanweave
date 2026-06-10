// Package docs embeds the hand-written OpenAPI document and the vendored
// Swagger UI assets served under /api/docs/. It only exposes bytes and content
// types; routing (and the 404 shape for unknown names) stays in package api.
package docs

import _ "embed"

//go:embed openapi.yaml
var specYAML []byte

//go:embed assets/index.html
var indexHTML []byte

//go:embed assets/swagger-ui.css
var swaggerCSS []byte

//go:embed assets/swagger-ui-bundle.js
var swaggerJS []byte

// SpecYAML returns the embedded OpenAPI document. The consistency test in
// package api compares its paths against the live route table.
func SpecYAML() []byte { return specYAML }

// File returns the artifact served at /api/docs/<name> ("" is the index page)
// with its Content-Type. ok is false for unknown names so the caller can answer
// with the API-wide 404.
func File(name string) (body []byte, contentType string, ok bool) {
	switch name {
	case "":
		return indexHTML, "text/html; charset=utf-8", true
	case "openapi.yaml":
		return specYAML, "application/yaml", true
	case "swagger-ui.css":
		return swaggerCSS, "text/css; charset=utf-8", true
	case "swagger-ui-bundle.js":
		return swaggerJS, "text/javascript; charset=utf-8", true
	default:
		return nil, "", false
	}
}
