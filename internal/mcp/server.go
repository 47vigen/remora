// Package mcp serves remora's management tools over the Model Context Protocol: at /mcp on the main listener and
// over stdio by `remora mcp`. Every tool calls the management API through the generated client, so a tool call gets
// exactly the API's authentication, validation and errors.
package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/version"
)

// New returns the MCP server with every tool, each calling c.
func New(c *gen.ClientWithResponses) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "remora", Version: version.Version}, nil)
	addOverview(s, c)
	addRead(s, c)
	return s
}

// add registers a tool whose handler sees only its input, with the caller's Authorization (if any) in ctx.
func add[In, Out any](s *sdk.Server, t *sdk.Tool, h func(ctx context.Context, in In) (Out, error)) {
	sdk.AddTool(s, t, func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, Out, error) {
		if req.Extra != nil {
			if v := req.Extra.Header.Get("Authorization"); v != "" {
				ctx = WithAuthorization(ctx, v)
			}
		}
		out, err := h(ctx, in)
		return nil, out, err
	})
}

// check returns a generated response's success body, else an error carrying remora's message:
// "<message> (HTTP <status>)", or "HTTP <status>" when the body has none.
func check[T any](status int, ok *T, e *gen.Error, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	if ok != nil {
		return ok, nil
	}
	if e != nil && e.Error.Message != "" {
		return nil, fmt.Errorf("%s (HTTP %d)", e.Error.Message, status)
	}
	return nil, fmt.Errorf("HTTP %d", status)
}

// toolError is an error whose text is a sentence for the agent, capitalised and punctuated.
type toolError string

func (e toolError) Error() string { return string(e) }

// response is what every generated response has.
type response interface {
	StatusCode() int
	GetJSONDefault() *gen.Error
}

// ok200 is check for an operation whose success is 200 with a JSON body.
func ok200[T any](r interface {
	response
	GetJSON200() *T
}, err error,
) (*T, error) {
	if err != nil {
		return nil, err
	}
	return check(r.StatusCode(), r.GetJSON200(), r.GetJSONDefault(), nil)
}

var readOnly = &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: new(false), OpenWorldHint: new(false)}
