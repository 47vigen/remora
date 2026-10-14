package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// schemaJSON is the remora.yaml JSON Schema, a copy of the published api/remora.schema.json (a test keeps the two
// equal; go:embed cannot reach outside this package's directory).
//
//go:embed remora.schema.json
var schemaJSON []byte

const schemaURL = "https://github.com/47vigen/remora/api/remora.schema.json"

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaURL, doc); err != nil {
		return nil, err
	}
	return c.Compile(schemaURL)
})

// checkSchema validates a YAML document against the remora.yaml schema. Each error names the value's path, such as
// "providers[0].kind".
func checkSchema(b []byte) error {
	sch, err := compiledSchema()
	if err != nil {
		return fmt.Errorf("remora.yaml: schema: %w", err)
	}
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("remora.yaml: %w", err)
	}
	if v == nil {
		v = map[string]any{} // an empty document is an empty File
	}
	// Round-trip through JSON so the validator sees JSON values (numbers, string-keyed objects).
	j, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("remora.yaml: %w", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(j))
	if err != nil {
		return fmt.Errorf("remora.yaml: %w", err)
	}
	err = sch.Validate(inst)
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return err
	}
	var errs []error
	for _, leaf := range leaves(ve) {
		msg := "invalid"
		if u := leaf.BasicOutput(); u.Error != nil {
			msg = u.Error.String()
		}
		e := fmt.Errorf("remora.yaml: %s: %s", instancePath(leaf.InstanceLocation), msg)
		if !slices.ContainsFunc(errs, func(x error) bool { return x.Error() == e.Error() }) {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}

// leaves returns the errors without causes: the failed keywords themselves, not the schemas that contain them.
func leaves(e *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(e.Causes) == 0 {
		return []*jsonschema.ValidationError{e}
	}
	var out []*jsonschema.ValidationError
	for _, c := range e.Causes {
		out = append(out, leaves(c)...)
	}
	return out
}

// instancePath turns a JSON pointer's tokens ("providers", "0", "kind") into the path Validate uses
// ("providers[0].kind").
func instancePath(tokens []string) string {
	if len(tokens) == 0 {
		return "(document)"
	}
	var b strings.Builder
	for _, tok := range tokens {
		if _, err := strconv.Atoi(tok); err == nil {
			b.WriteString("[" + tok + "]")
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(tok)
	}
	return b.String()
}
