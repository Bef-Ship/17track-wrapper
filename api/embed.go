// Package api holds the HTTP contract for upstream services.
package api

import _ "embed"

// OpenAPI is openapi.yaml, served at GET /openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte
