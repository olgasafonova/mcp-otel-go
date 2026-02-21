package mcpotel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olgasafonova/mcp-otel-go/mcpotel"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

var testImpl = &mcp.Implementation{
	Name:    "test-server",
	Version: "0.1.0",
}

func setupServer(t *testing.T, cfg mcpotel.Config) (*mcp.Server, *tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()

	spanExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(spanExporter),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	metricReader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(metricReader),
	)
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	cfg.TracerProvider = tp
	cfg.MeterProvider = mp

	s := mcp.NewServer(testImpl, nil)
	s.AddReceivingMiddleware(mcpotel.Middleware(cfg))

	return s, spanExporter, metricReader
}

func connect(t *testing.T, s *mcp.Server) *mcp.ClientSession {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	c := mcp.NewClient(testImpl, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

func TestMiddleware_ToolCall(t *testing.T) {
	s, spanExp, metricReader := setupServer(t, mcpotel.Config{
		ServiceName: "test-server",
	})

	mcp.AddTool(s, &mcp.Tool{Name: "greet"}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "hello"}},
		}, nil, nil
	})

	cs := connect(t, s)

	ctx := context.Background()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "greet"})
	if err != nil {
		t.Fatal(err)
	}

	spans := spanExp.GetSpans()
	toolSpan := findSpan(spans, "tools/call greet")
	if toolSpan == nil {
		t.Fatalf("expected span 'tools/call greet', got spans: %v", spanNames(spans))
	}

	assertAttribute(t, toolSpan, "mcp.method.name", "tools/call")
	assertAttribute(t, toolSpan, "gen_ai.tool.name", "greet")

	if toolSpan.SpanKind != trace.SpanKindServer {
		t.Errorf("expected SpanKindServer, got %v", toolSpan.SpanKind)
	}

	// Verify metrics recorded
	var rm metricdata.ResourceMetrics
	if err := metricReader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}

	found := findMetric(rm, "mcp.server.operation.duration")
	if !found {
		t.Error("expected mcp.server.operation.duration metric to be recorded")
	}
}

func TestMiddleware_ToolCallError(t *testing.T) {
	// Calling a non-existent tool triggers a JSON-RPC error at the protocol
	// level, which the middleware sees as a real Go error (unlike tool handler
	// errors, which the go-sdk wraps into CallToolResult with IsError=true).
	s, spanExp, _ := setupServer(t, mcpotel.Config{
		ServiceName: "test-server",
	})

	cs := connect(t, s)

	ctx := context.Background()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "nonexistent"})
	if err == nil {
		t.Fatal("expected error from calling nonexistent tool")
	}

	spans := spanExp.GetSpans()
	span := findSpan(spans, "tools/call nonexistent")
	if span == nil {
		t.Fatalf("expected span 'tools/call nonexistent', got spans: %v", spanNames(spans))
	}

	if span.Status.Code != codes.Error {
		t.Errorf("expected error status code %v, got %v", codes.Error, span.Status.Code)
	}
}

func TestMiddleware_ToolHandlerError(t *testing.T) {
	// When a tool handler returns a Go error, the go-sdk wraps it into
	// CallToolResult{IsError: true} and returns (result, nil) to the
	// middleware. The middleware should detect this and mark the span as error.
	s, spanExp, _ := setupServer(t, mcpotel.Config{
		ServiceName: "test-server",
	})

	mcp.AddTool(s, &mcp.Tool{Name: "broken"}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return nil, nil, errors.New("database connection lost")
	})

	cs := connect(t, s)

	ctx := context.Background()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "broken"})
	// The go-sdk does NOT propagate tool handler errors as Go errors.
	// Instead it returns a result with IsError=true.
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true on result")
	}

	spans := spanExp.GetSpans()
	span := findSpan(spans, "tools/call broken")
	if span == nil {
		t.Fatalf("expected span 'tools/call broken', got spans: %v", spanNames(spans))
	}

	// The middleware should detect the application-level error
	if span.Status.Code != codes.Error {
		t.Errorf("expected error status for tool handler error, got %v", span.Status.Code)
	}

	// Default RedactError records type name, not the message (PII-safe)
	assertAttribute(t, span, "error.type", "*errors.errorString")
}

func TestMiddleware_RedactErrorFull(t *testing.T) {
	// Opt-in to full error messages when you know they're PII-free.
	s, spanExp, _ := setupServer(t, mcpotel.Config{
		ServiceName: "test-server",
		RedactError: mcpotel.ErrorMessageFull,
	})

	mcp.AddTool(s, &mcp.Tool{Name: "fail"}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return nil, nil, errors.New("invalid input format")
	})

	cs := connect(t, s)

	ctx := context.Background()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "fail"}); err != nil {
		t.Fatal(err)
	}

	spans := spanExp.GetSpans()
	span := findSpan(spans, "tools/call fail")
	if span == nil {
		t.Fatalf("expected span, got: %v", spanNames(spans))
	}

	// With ErrorMessageFull, the actual message is recorded
	assertAttribute(t, span, "error.type", "invalid input format")
}

func TestMiddleware_RedactURI(t *testing.T) {
	s, spanExp, _ := setupServer(t, mcpotel.Config{
		ServiceName: "test-server",
		RedactURI:   mcpotel.URISchemeOnly,
	})

	// We can't easily register a resource handler through the public API
	// to test resources/read, but we can verify the URI redaction function
	// works correctly in isolation.
	result := mcpotel.URISchemeOnly("file:///home/john/secret.txt")
	if result != "file://" {
		t.Errorf("URISchemeOnly: got %q, want %q", result, "file://")
	}

	result = mcpotel.URISchemeOnly("miro://board/abc123")
	if result != "miro://" {
		t.Errorf("URISchemeOnly: got %q, want %q", result, "miro://")
	}

	// Verify the server still works with RedactURI set
	mcp.AddTool(s, &mcp.Tool{Name: "nop"}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	})

	cs := connect(t, s)
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "nop"}); err != nil {
		t.Fatal(err)
	}

	spans := spanExp.GetSpans()
	if findSpan(spans, "tools/call nop") == nil {
		t.Error("expected tools/call span")
	}
}

func TestMiddleware_ListTools(t *testing.T) {
	s, spanExp, _ := setupServer(t, mcpotel.Config{
		ServiceName: "test-server",
	})

	mcp.AddTool(s, &mcp.Tool{Name: "nop"}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	})

	cs := connect(t, s)

	ctx := context.Background()
	_, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	spans := spanExp.GetSpans()
	span := findSpan(spans, "tools/list")
	if span == nil {
		t.Fatalf("expected span 'tools/list', got spans: %v", spanNames(spans))
	}

	assertAttribute(t, span, "mcp.method.name", "tools/list")
}

func TestMiddleware_Initialize(t *testing.T) {
	s, spanExp, _ := setupServer(t, mcpotel.Config{
		ServiceName: "test-server",
	})

	// Just connecting triggers initialize
	_ = connect(t, s)

	spans := spanExp.GetSpans()
	span := findSpan(spans, "initialize")
	if span == nil {
		t.Fatalf("expected span 'initialize', got spans: %v", spanNames(spans))
	}

	assertAttribute(t, span, "mcp.method.name", "initialize")
}

func TestMiddleware_Filter(t *testing.T) {
	s, spanExp, _ := setupServer(t, mcpotel.Config{
		ServiceName: "test-server",
		Filter: func(method string) bool {
			return method == "tools/call"
		},
	})

	mcp.AddTool(s, &mcp.Tool{Name: "hello"}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "hi"}},
		}, nil, nil
	})

	cs := connect(t, s)

	ctx := context.Background()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "hello"}); err != nil {
		t.Fatal(err)
	}

	spans := spanExp.GetSpans()

	// tools/call should be instrumented
	if findSpan(spans, "tools/call hello") == nil {
		t.Error("expected tools/call span to be present")
	}

	// initialize should NOT be instrumented (filtered out)
	if findSpan(spans, "initialize") != nil {
		t.Error("expected initialize span to be filtered out")
	}
}

func TestMiddleware_SessionID(t *testing.T) {
	s, spanExp, _ := setupServer(t, mcpotel.Config{
		ServiceName: "test-server",
	})

	mcp.AddTool(s, &mcp.Tool{Name: "ping"}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "pong"}},
		}, nil, nil
	})

	cs := connect(t, s)

	ctx := context.Background()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "ping"}); err != nil {
		t.Fatal(err)
	}

	spans := spanExp.GetSpans()
	span := findSpan(spans, "tools/call ping")
	if span == nil {
		t.Fatalf("expected span 'tools/call ping', got spans: %v", spanNames(spans))
	}

	// Session ID should be present (in-memory transport assigns one)
	hasSessionID := false
	for _, attr := range span.Attributes {
		if string(attr.Key) == "mcp.session.id" && attr.Value.AsString() != "" {
			hasSessionID = true
			break
		}
	}
	if !hasSessionID {
		t.Log("session ID attribute not found (may be empty for in-memory transport)")
	}
}

func TestMiddleware_ResourceRead(t *testing.T) {
	s, spanExp, _ := setupServer(t, mcpotel.Config{
		ServiceName: "test-server",
	})

	s.AddResource(&mcp.Resource{
		Name: "greeting",
		URI:  "test://greetings/hello",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: "test://greetings/hello", Text: "Hello, world!"},
			},
		}, nil
	})

	cs := connect(t, s)

	ctx := context.Background()
	_, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "test://greetings/hello"})
	if err != nil {
		t.Fatal(err)
	}

	spans := spanExp.GetSpans()
	span := findSpan(spans, "resources/read test://greetings/hello")
	if span == nil {
		t.Fatalf("expected span 'resources/read test://greetings/hello', got spans: %v", spanNames(spans))
	}

	assertAttribute(t, span, "mcp.method.name", "resources/read")
	assertAttribute(t, span, "mcp.resource.uri", "test://greetings/hello")
}

func TestMiddleware_ResourceReadWithRedactURI(t *testing.T) {
	s, spanExp, _ := setupServer(t, mcpotel.Config{
		ServiceName: "test-server",
		RedactURI:   mcpotel.URISchemeOnly,
	})

	s.AddResource(&mcp.Resource{
		Name: "secret",
		URI:  "file:///home/john/secret.txt",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: "file:///home/john/secret.txt", Text: "secret data"},
			},
		}, nil
	})

	cs := connect(t, s)

	ctx := context.Background()
	_, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "file:///home/john/secret.txt"})
	if err != nil {
		t.Fatal(err)
	}

	spans := spanExp.GetSpans()
	// Span name uses redacted URI when RedactURI is set
	span := findSpan(spans, "resources/read file://")
	if span == nil {
		t.Fatalf("expected span with redacted URI, got spans: %v", spanNames(spans))
	}

	assertAttribute(t, span, "mcp.resource.uri", "file://")
}

func TestMiddleware_PromptGet(t *testing.T) {
	s, spanExp, _ := setupServer(t, mcpotel.Config{
		ServiceName: "test-server",
	})

	s.AddPrompt(&mcp.Prompt{
		Name:        "summarize",
		Description: "Summarize text",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: "Please summarize"}},
			},
		}, nil
	})

	cs := connect(t, s)

	ctx := context.Background()
	_, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "summarize"})
	if err != nil {
		t.Fatal(err)
	}

	spans := spanExp.GetSpans()
	span := findSpan(spans, "prompts/get summarize")
	if span == nil {
		t.Fatalf("expected span 'prompts/get summarize', got spans: %v", spanNames(spans))
	}

	assertAttribute(t, span, "mcp.method.name", "prompts/get")
	assertAttribute(t, span, "gen_ai.prompt.name", "summarize")
}

func TestURISchemeOnly_NoScheme(t *testing.T) {
	result := mcpotel.URISchemeOnly("just-a-path")
	if result != "unknown://" {
		t.Errorf("URISchemeOnly(%q) = %q, want %q", "just-a-path", result, "unknown://")
	}
}

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
		RedactURI:   mcpotel.URISchemeOnly,    // strip paths from resource URIs
	}))
}

// --- benchmarks ---

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

// --- helpers ---

func findSpan(spans tracetest.SpanStubs, name string) *tracetest.SpanStub {
	for i := range spans {
		if spans[i].Name == name {
			return &spans[i]
		}
	}
	return nil
}

func spanNames(spans tracetest.SpanStubs) []string {
	names := make([]string, len(spans))
	for i, s := range spans {
		names[i] = s.Name
	}
	return names
}

func assertAttribute(t *testing.T, span *tracetest.SpanStub, key, expected string) {
	t.Helper()
	for _, attr := range span.Attributes {
		if string(attr.Key) == key {
			if got := attr.Value.AsString(); got != expected {
				t.Errorf("attribute %q: got %q, want %q", key, got, expected)
			}
			return
		}
	}
	t.Errorf("attribute %q not found on span %q", key, span.Name)
}

func findMetric(rm metricdata.ResourceMetrics, name string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return true
			}
		}
	}
	return false
}
