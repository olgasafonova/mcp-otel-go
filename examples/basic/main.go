// Command basic demonstrates using mcpotel middleware with an MCP server.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olgasafonova/mcp-otel-go/mcpotel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func main() {
	ctx := context.Background()

	// Set up a stdout trace exporter for demonstration.
	// In production, use OTLP exporter pointing to Jaeger, Grafana Tempo, etc.
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		log.Fatal(err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
	)
	defer tp.Shutdown(ctx)
	otel.SetTracerProvider(tp)

	// Create the MCP server
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "example-server",
		Version: "0.1.0",
	}, nil)

	// Add OpenTelemetry middleware — one line
	server.AddReceivingMiddleware(mcpotel.Middleware(mcpotel.Config{
		ServiceName:    "example-server",
		ServiceVersion: "0.1.0",
	}))

	// Register a tool
	mcp.AddTool(server, &mcp.Tool{
		Name:        "greet",
		Description: "Returns a greeting",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		name, _ := args["name"].(string)
		if name == "" {
			name = "world"
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Hello, %s!", name)}},
		}, nil, nil
	})

	// Run the server over stdio
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
