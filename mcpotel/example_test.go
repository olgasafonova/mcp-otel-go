package mcpotel_test

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olgasafonova/mcp-otel-go/mcpotel"
)

// --- examples (appear on pkg.go.dev) ---

func ExampleMiddleware() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "my-server",
		Version: "1.0.0",
	}, nil)

	// Add OpenTelemetry middleware — picks up the global TracerProvider
	// and MeterProvider by default.
	server.AddReceivingMiddleware(mcpotel.Middleware(mcpotel.Config{
		ServiceName:    "my-server",
		ServiceVersion: "1.0.0",
	}))

	// Register tools, prompts, resources as usual.
	// Every incoming MCP method call now produces an OTel span and
	// a duration histogram automatically.
}

func ExampleMiddleware_withFilter() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "my-server",
		Version: "1.0.0",
	}, nil)

	server.AddReceivingMiddleware(mcpotel.Middleware(mcpotel.Config{
		ServiceName: "my-server",
		Filter: func(method string) bool {
			// Only instrument tool calls, skip noisy methods
			return method == "tools/call"
		},
	}))
}

func ExampleMiddleware_withRedaction() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "my-server",
		Version: "1.0.0",
	}, nil)

	server.AddReceivingMiddleware(mcpotel.Middleware(mcpotel.Config{
		ServiceName: "my-server",
		RedactError: mcpotel.ErrorMessageFull, // opt-in to full error messages
		RedactURI:   mcpotel.URIFull,          // opt-in to full URI paths
	}))
}
