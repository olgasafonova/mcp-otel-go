package mcpotel

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Config controls the behavior of the OpenTelemetry middleware.
type Config struct {
	// ServiceName is used as the OTel service.name resource attribute.
	// Required.
	ServiceName string

	// ServiceVersion is used as the service.version resource attribute.
	// Optional.
	ServiceVersion string

	// TracerProvider supplies the tracer. Defaults to otel.GetTracerProvider().
	TracerProvider trace.TracerProvider

	// MeterProvider supplies the meter. Defaults to otel.GetMeterProvider().
	MeterProvider metric.MeterProvider

	// Filter returns false for methods that should not be instrumented.
	// When nil, all methods are instrumented.
	Filter func(method string) bool
}

// Middleware returns an MCP middleware that instruments every incoming method
// call with OpenTelemetry spans and metrics.
//
// Usage:
//
//	server := mcp.NewServer(impl, opts)
//	server.AddReceivingMiddleware(mcpotel.Middleware(mcpotel.Config{
//	    ServiceName: "my-mcp-server",
//	}))
func Middleware(cfg Config) mcp.Middleware {
	tp := cfg.TracerProvider
	if tp == nil {
		tp = otel.GetTracerProvider()
	}

	mp := cfg.MeterProvider
	if mp == nil {
		mp = otel.GetMeterProvider()
	}

	tracer := tp.Tracer(
		instrumentationName,
		trace.WithInstrumentationVersion("0.1.0"),
	)

	m, err := newMeters(mp)
	if err != nil {
		// Metric registration should not fail in practice. If it does, the
		// middleware still works — it just won't record metrics.
		m = nil
	}

	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if cfg.Filter != nil && !cfg.Filter(method) {
				return next(ctx, method, req)
			}

			target := extractTarget(method, req)
			name := spanName(method, target)

			attrs := []attribute.KeyValue{
				AttrMCPMethodName.String(method),
			}

			if session := req.GetSession(); session != nil {
				if id := session.ID(); id != "" {
					attrs = append(attrs, AttrMCPSessionID.String(id))
				}
			}

			attrs = append(attrs, targetAttributes(method, target)...)

			ctx, span := tracer.Start(ctx, name,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attrs...),
			)
			defer span.End()

			start := time.Now()
			result, err := next(ctx, method, req)
			duration := time.Since(start)

			if err != nil {
				// Protocol-level error (tool not found, invalid params, etc.)
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
				errAttr := AttrErrorType.String(errorType(err))
				span.SetAttributes(errAttr)
				attrs = append(attrs, errAttr)
			} else if toolErr := extractToolError(result); toolErr != "" {
				// Application-level tool error: the go-sdk wraps tool handler
				// errors into CallToolResult with IsError=true instead of
				// returning a Go error. Without this check, failing tools
				// would appear as successful in traces.
				span.SetStatus(codes.Error, toolErr)
				errAttr := AttrErrorType.String(toolErr)
				span.SetAttributes(errAttr)
				attrs = append(attrs, errAttr)
			} else {
				span.SetStatus(codes.Ok, "")
			}

			if m != nil {
				m.recordDuration(ctx, duration, attrs)
			}

			return result, err
		}
	}
}

// errorType returns a short classification string for the error.
func errorType(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
