package mcpotel_test

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olgasafonova/mcp-otel-go/mcpotel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func BenchmarkMiddleware_ToolCall(b *testing.B) {
	spanExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(spanExporter),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	metricReader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(metricReader),
	)
	defer func() { _ = mp.Shutdown(context.Background()) }()

	s := mcp.NewServer(testImpl, nil)
	s.AddReceivingMiddleware(mcpotel.Middleware(mcpotel.Config{
		ServiceName:    "bench-server",
		TracerProvider: tp,
		MeterProvider:  mp,
	}))

	mcp.AddTool(s, &mcp.Tool{Name: "nop"}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = ss.Close() }()

	c := mcp.NewClient(testImpl, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "nop"})
		if err != nil {
			b.Fatal(err)
		}
		spanExporter.Reset()
	}
}
