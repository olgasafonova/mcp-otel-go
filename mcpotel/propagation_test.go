package mcpotel_test

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olgasafonova/mcp-otel-go/mcpotel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// A well-formed W3C traceparent: version-traceid-spanid-flags, sampled.
const (
	inboundTraceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	inboundTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	inboundSpanID      = "00f067aa0ba902b7"
)

// setupPropagatingServer mirrors setupServer but pins an explicit TraceContext
// propagator. Pinned rather than relying on the global because OTel's global
// default is a NO-OP propagator, so a test leaning on it would pass for the
// wrong reason.
func setupPropagatingServer(t *testing.T) (*mcp.Server, *tracetest.InMemoryExporter) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	s := mcp.NewServer(testImpl, nil)
	s.AddReceivingMiddleware(mcpotel.Middleware(mcpotel.Config{
		ServiceName:    "test-server",
		TracerProvider: tp,
		Propagator:     propagation.TraceContext{},
	}))

	return s, exporter
}

// listToolsWithMeta drives one tools/list carrying the supplied `_meta`.
func listToolsWithMeta(t *testing.T, s *mcp.Server, meta map[string]any) {
	t.Helper()

	cs := connect(t, s)
	params := &mcp.ListToolsParams{}
	params.SetMeta(meta)
	if _, err := cs.ListTools(context.Background(), params); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
}

// TestMiddleware_ExtractsInboundTraceContext is the assertion that SEP-414
// propagation actually works: an inbound traceparent in `_meta` must become the
// PARENT of the span this server emits, joining the caller's trace.
//
// Asserting that Extract was called, or that the carrier was read, would pass
// with the propagator wired to nothing. Only the parent linkage proves it.
//
// Ablation: remove the r.propagator.Extract line in Middleware and this fails
// with a fresh trace ID and an invalid parent span ID.
func TestMiddleware_ExtractsInboundTraceContext(t *testing.T) {
	s, exporter := setupPropagatingServer(t)

	listToolsWithMeta(t, s, map[string]any{"traceparent": inboundTraceParent})

	span := findSpan(exporter.GetSpans(), "tools/list")
	if span == nil {
		t.Fatalf("expected span 'tools/list', got: %v", spanNames(exporter.GetSpans()))
	}

	if got := span.SpanContext.TraceID().String(); got != inboundTraceID {
		t.Errorf("trace ID = %s, want %s (span did not join the inbound trace)", got, inboundTraceID)
	}
	if got := span.Parent.SpanID().String(); got != inboundSpanID {
		t.Errorf("parent span ID = %s, want %s (span is not a child of the caller)", got, inboundSpanID)
	}
	if !span.Parent.IsRemote() {
		t.Error("parent span context is not marked remote; it did not come from the wire")
	}
}

// TestMiddleware_NoInboundTraceContextStillSpans pins the other half of the
// contract. A caller that sends no trace context must still be instrumented,
// as a root span rather than an error or a dropped request.
func TestMiddleware_NoInboundTraceContextStillSpans(t *testing.T) {
	s, exporter := setupPropagatingServer(t)

	listToolsWithMeta(t, s, nil)

	span := findSpan(exporter.GetSpans(), "tools/list")
	if span == nil {
		t.Fatalf("expected span 'tools/list', got: %v", spanNames(exporter.GetSpans()))
	}
	if span.Parent.IsValid() {
		t.Errorf("expected a root span, got parent %s", span.Parent.SpanID())
	}
}

// TestMiddleware_MalformedTraceParentIsIgnored covers the hostile case. `_meta`
// is map[string]any and reaches this server from a peer, so a non-string or
// unparseable traceparent must degrade to a root span rather than panic or
// fail the request.
func TestMiddleware_MalformedTraceParentIsIgnored(t *testing.T) {
	for name, meta := range map[string]map[string]any{
		"non-string value": {"traceparent": 12345},
		"unparseable":      {"traceparent": "not-a-traceparent"},
		"empty string":     {"traceparent": ""},
	} {
		t.Run(name, func(t *testing.T) {
			s, exporter := setupPropagatingServer(t)

			listToolsWithMeta(t, s, meta)

			span := findSpan(exporter.GetSpans(), "tools/list")
			if span == nil {
				t.Fatalf("request did not complete; expected a root span")
			}
			if span.Parent.IsValid() {
				t.Errorf("malformed traceparent produced a parent link: %s", span.Parent.SpanID())
			}
		})
	}
}
