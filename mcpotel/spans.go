// Package mcpotel provides OpenTelemetry instrumentation middleware for MCP servers
// built with the official go-sdk.
//
// It follows the OpenTelemetry semantic conventions for MCP:
// https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/
package mcpotel

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

// Attribute keys following OTel MCP semantic conventions.
const (
	// AttrMCPMethodName is the MCP method being called (e.g., "tools/call").
	AttrMCPMethodName = attribute.Key("mcp.method.name")

	// AttrMCPSessionID identifies the MCP session.
	AttrMCPSessionID = attribute.Key("mcp.session.id")

	// AttrMCPResourceURI is the URI of the resource being read.
	AttrMCPResourceURI = attribute.Key("mcp.resource.uri")

	// AttrGenAIToolName is the name of the tool being called.
	AttrGenAIToolName = attribute.Key("gen_ai.tool.name")

	// AttrGenAIPromptName is the name of the prompt being retrieved.
	AttrGenAIPromptName = attribute.Key("gen_ai.prompt.name")

	// AttrErrorType classifies the error.
	AttrErrorType = attribute.Key("error.type")
)

// instrumentationName is the OTel instrumentation scope name for this package.
const instrumentationName = "github.com/olgasafonova/mcp-otel-go/mcpotel"

// extractTarget returns the target identifier for a given MCP method based on
// the request parameters. For tools/call it returns the tool name, for
// resources/read the URI, and for prompts/get the prompt name.
func extractTarget(method string, req mcp.Request) string {
	params := req.GetParams()
	if params == nil {
		return ""
	}

	switch method {
	case "tools/call":
		// The server-side middleware receives CallToolParamsRaw (raw JSON arguments),
		// while client-side middleware receives CallToolParams. Both have a Name field.
		switch p := params.(type) {
		case *mcp.CallToolParamsRaw:
			return p.Name
		case *mcp.CallToolParams:
			return p.Name
		}
	case "resources/read":
		if p, ok := params.(*mcp.ReadResourceParams); ok {
			return p.URI
		}
	case "prompts/get":
		if p, ok := params.(*mcp.GetPromptParams); ok {
			return p.Name
		}
	}
	return ""
}

// spanName builds the span name following the convention: "{method} {target}".
// If no target is available, the span name is just the method.
func spanName(method, target string) string {
	if target == "" {
		return method
	}
	return method + " " + target
}

// extractToolError checks if the result is a CallToolResult with IsError set.
// The go-sdk converts tool handler errors into CallToolResult{IsError: true}
// instead of propagating them as Go errors. GetError() returns the original
// error on the server side; we fall back to "tool_error" if unavailable.
//
// The redact function controls how error messages are recorded. It is never
// nil when called from the middleware (defaults to errorTypeName).
func extractToolError(result mcp.Result, redact func(error) string) string {
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || !ctr.IsError {
		return ""
	}
	if err := ctr.GetError(); err != nil {
		return redact(err)
	}
	return "tool_error"
}

// targetAttributes returns the method-specific OTel attributes for a request.
func targetAttributes(method, target string) []attribute.KeyValue {
	if target == "" {
		return nil
	}

	switch method {
	case "tools/call":
		return []attribute.KeyValue{AttrGenAIToolName.String(target)}
	case "resources/read":
		return []attribute.KeyValue{AttrMCPResourceURI.String(target)}
	case "prompts/get":
		return []attribute.KeyValue{AttrGenAIPromptName.String(target)}
	default:
		return nil
	}
}
