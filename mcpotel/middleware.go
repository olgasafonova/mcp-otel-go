package mcpotel

import (
	"context"
	"fmt"
	"net/url"
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

	// RedactError controls how error messages are recorded in spans and metrics.
	// Error messages from tool handlers may contain PII (e.g., user emails,
	// file paths). This function lets you sanitize or classify them.
	//
	// When nil, defaults to recording the Go error type name only (e.g.,
	// "*json.SyntaxError"), not the full message. Set to ErrorMessageFull
	// to record complete error messages if your errors are known to be PII-free.
	RedactError func(err error) string

	// RedactURI controls how resource URIs are recorded in spans and metrics.
	// URIs may contain user-identifiable paths or query parameters.
	//
	// When nil, defaults to recording the full URI. Set to URISchemeOnly
	// to record only the scheme (e.g., "file://", "user://").
	RedactURI func(uri string) string
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

	redactErr := cfg.RedactError
	if redactErr == nil {
		redactErr = errorTypeName
	}

	redactURI := cfg.RedactURI

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

			// Apply URI redaction for resource reads.
			displayTarget := target
			if method == "resources/read" && redactURI != nil && target != "" {
				displayTarget = redactURI(target)
			}

			name := spanName(method, displayTarget)

			// Pre-allocate for the common case: method + session + target + error.
			attrs := make([]attribute.KeyValue, 0, 4)
			attrs = append(attrs, AttrMCPMethodName.String(method))

			if session := req.GetSession(); session != nil {
				if id := session.ID(); id != "" {
					attrs = append(attrs, AttrMCPSessionID.String(id))
				}
			}

			appendTargetAttrs(&attrs, method, displayTarget)

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
				redacted := redactErr(err)
				span.SetStatus(codes.Error, redacted)
				errAttr := AttrErrorType.String(redacted)
				span.SetAttributes(errAttr)
				attrs = append(attrs, errAttr)
			} else if toolErr := extractToolError(result, redactErr); toolErr != "" {
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

// --- Built-in redaction functions ---

// ErrorMessageFull records the complete error message. Use this only when
// you are confident your error messages never contain PII.
func ErrorMessageFull(err error) string {
	return err.Error()
}

// errorTypeName returns the Go type name of the error (e.g., "*json.SyntaxError").
// This is the default RedactError behavior: safe because type names are
// developer-defined and never contain user data.
func errorTypeName(err error) string {
	return fmt.Sprintf("%T", err)
}

// URISchemeOnly records only the URI scheme (e.g., "file://", "miro://").
// Use this when resource URIs may contain user-identifiable paths.
func URISchemeOnly(uri string) string {
	if u, err := url.Parse(uri); err == nil && u.Scheme != "" {
		return u.Scheme + "://"
	}
	return "unknown://"
}
