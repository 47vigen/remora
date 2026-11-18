// Package apispec embeds the management API contract, including hand-mounted operations.
package apispec

import _ "embed"

// OpenAPI is the source contract in YAML.
//
//go:embed openapi.yaml
var OpenAPI string
