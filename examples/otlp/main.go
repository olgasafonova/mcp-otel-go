// Command otlp demonstrates mcpotel with OTLP exporters for use with otel-tui,
// Jaeger, Grafana Tempo, or any OTLP-compatible collector.
//
// Usage:
//
//	Terminal 1: otel-tui                           # listens on :4317
//	Terminal 2: go run ./examples/otlp             # sends traces + metrics
//	Terminal 3: npx @modelcontextprotocol/inspector go run ./examples/otlp
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olgasafonova/mcp-otel-go/mcpotel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
)

func main() {
	ctx := context.Background()

	shutdown, err := setupOTel(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = shutdown(ctx) }()

	// Create the MCP server
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "example-server",
		Version: "0.1.0",
	}, nil)

	// Add OpenTelemetry middleware
	server.AddReceivingMiddleware(mcpotel.Middleware(mcpotel.Config{
		ServiceName:    "example-server",
		ServiceVersion: "0.1.0",
	}))

	// Register a sample tool to generate telemetry
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

// setupOTel configures OTLP gRPC exporters for both traces and metrics.
// By default they connect to localhost:4317 (the standard OTLP gRPC port).
// Set OTEL_EXPORTER_OTLP_ENDPOINT to override.
func setupOTel(ctx context.Context) (func(context.Context) error, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("example-server"),
			semconv.ServiceVersion("0.1.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("creating resource: %w", err)
	}

	// Trace exporter → OTLP gRPC (insecure for local dev)
	traceExp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("creating trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	// Metric exporter → OTLP gRPC (insecure for local dev)
	metricExp, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("creating metric exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
			sdkmetric.WithInterval(10*time.Second),
		)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	// Return a combined shutdown function
	return func(ctx context.Context) error {
		if err := tp.Shutdown(ctx); err != nil {
			return err
		}
		return mp.Shutdown(ctx)
	}, nil
}
