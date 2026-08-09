package mcpotel

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
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
	// When nil, defaults to URISchemeOnly: only the scheme is recorded
	// (e.g., "file://", "user://"). This is the privacy-safe default and
	// keeps the library's posture consistent with RedactError.
	//
	// To record full URIs verbatim, set this explicitly to URIFull. Only do
	// this when you control the URI namespace and are confident the paths
	// contain no user-identifiable data.
	RedactURI func(uri string) string

	// Propagator extracts inbound trace context from the request's `_meta`,
	// linking this server's spans to the caller's trace. Defaults to
	// otel.GetTextMapPropagator().
	//
	// MCP revision 2026-07-28 documents the OpenTelemetry convention for
	// `_meta` (SEP-414) using the unprefixed W3C key names `traceparent`,
	// `tracestate` and `baggage`, which is exactly what the standard
	// propagators read. Note that the OTel global default is a NO-OP
	// propagator: an application that never calls otel.SetTextMapPropagator
	// gets no propagation, here or anywhere else in its stack. Set one, or set
	// this field, to opt in.
	Propagator propagation.TextMapPropagator
}

// resolved holds the immutable, pre-computed state for the middleware.
// Created once during Middleware() and captured by the closure.
type resolved struct {
	tracer     trace.Tracer
	meters     *meters
	redactErr  func(error) string
	redactURI  func(string) string
	filter     func(string) bool
	propagator propagation.TextMapPropagator
}

func resolve(cfg Config) resolved {
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
	if redactURI == nil {
		redactURI = URISchemeOnly
	}

	propagator := cfg.Propagator
	if propagator == nil {
		propagator = otel.GetTextMapPropagator()
	}

	tracer := tp.Tracer(
		instrumentationName,
		trace.WithInstrumentationVersion("0.1.0"),
	)

	m, err := newMeters(mp)
	if err != nil {
		// Metric registration should not fail in practice. If it does, the
		// middleware still works (spans continue to be recorded), but the
		// duration histogram will be silently absent from the metric
		// pipeline. Surface the failure so operators notice before the
		// dashboard goes empty.
		slog.Error(
			"mcpotel: metric registration failed; continuing without metrics",
			"err", err,
			"service", cfg.ServiceName,
		)
		m = nil
	}

	return resolved{
		tracer:     tracer,
		meters:     m,
		redactErr:  redactErr,
		redactURI:  redactURI,
		filter:     cfg.Filter,
		propagator: propagator,
	}
}

// recordError sets the span error status and appends the error attribute
// for both span and metric recording.
func recordError(span trace.Span, attrs *[]attribute.KeyValue, errMsg string) {
	span.SetStatus(codes.Error, errMsg)
	errAttr := AttrErrorType.String(errMsg)
	span.SetAttributes(errAttr)
	*attrs = append(*attrs, errAttr)
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
	r := resolve(cfg)

	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if r.filter != nil && !r.filter(method) {
				return next(ctx, method, req)
			}

			displayTarget := displayedTarget(method, extractTarget(method, req), r.redactURI)
			attrs := requestAttrs(method, displayTarget, req)

			// SEP-414: link this span to the caller's trace by extracting W3C
			// trace context from the request's `_meta` BEFORE starting the
			// span. Extracting afterwards would produce a root span and lose
			// the parent link, which is the whole point of propagation.
			//
			// Extract against a request with no `_meta` is a no-op that
			// returns ctx unchanged, so an uninstrumented caller still gets a
			// normal root span here.
			ctx = r.propagator.Extract(ctx, carrierFor(req))

			ctx, span := r.tracer.Start(ctx, spanName(method, displayTarget),
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attrs...),
			)
			defer span.End()

			start := time.Now()
			result, err := next(ctx, method, req)
			duration := time.Since(start)

			if errMsg := callErrMsg(result, err, r.redactErr); errMsg != "" {
				recordError(span, &attrs, errMsg)
			} else {
				span.SetStatus(codes.Ok, "")
			}

			if r.meters != nil {
				r.meters.recordDuration(ctx, duration, attrs)
			}

			return result, err
		}
	}
}

// displayedTarget applies URI redaction for resource reads. The redact
// function is never nil: resolve() defaults it to URISchemeOnly.
func displayedTarget(method, target string, redact func(string) string) string {
	if method == "resources/read" && target != "" {
		return redact(target)
	}
	return target
}

// requestAttrs builds the span and metric attributes for an incoming call.
// Pre-allocates for the common case: method + session + target + error.
func requestAttrs(method, target string, req mcp.Request) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 4)
	attrs = append(attrs, AttrMCPMethodName.String(method))

	if session := req.GetSession(); session != nil {
		if id := session.ID(); id != "" {
			attrs = append(attrs, AttrMCPSessionID.String(id))
		}
	}

	appendTargetAttrs(&attrs, method, target)
	return attrs
}

// callErrMsg determines the error message from either surface: a Go error
// from the handler chain, or a tool result carrying IsError.
func callErrMsg(result mcp.Result, err error, redact func(error) string) string {
	if err != nil {
		return redact(err)
	}
	return extractToolError(result, redact)
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
// This is the default RedactURI behavior. Use it explicitly to document intent.
func URISchemeOnly(uri string) string {
	if u, err := url.Parse(uri); err == nil && u.Scheme != "" {
		return u.Scheme + "://"
	}
	return "unknown://"
}

// URIFull records the complete URI verbatim. Use this only when you control
// the URI namespace and are confident the paths contain no PII (e.g., opaque
// IDs from your own system).
//
// Setting RedactURI: URIFull is the explicit opt-out from the privacy-safe
// default (URISchemeOnly).
func URIFull(uri string) string {
	return uri
}

// --- Built-in filters ---

// defaultProtocolChatterMethods enumerates the MCP protocol housekeeping
// methods that DefaultProtocolFilter drops. Keep this list narrow: each
// addition silently removes signal from telemetry, which is harder to debug
// than the noise it removes.
var defaultProtocolChatterMethods = map[string]struct{}{
	"notifications/initialized":        {},
	"notifications/cancelled":          {},
	"notifications/progress":           {},
	"notifications/roots/list_changed": {},
	"ping":                             {},
	"tools/list":                       {},
	"resources/list":                   {},
	"resources/templates/list":         {},
	"prompts/list":                     {},
}

// DefaultProtocolFilter returns false for MCP protocol housekeeping methods
// that tend to dominate telemetry without carrying diagnostic value. It
// returns true for everything else, including tools/call, resources/read,
// prompts/get, and server/discover.
//
// ping stays in the drop list even though protocol revision 2026-07-28 removed
// the method: the SDK negotiates versions, so an older client can still send
// one. server/discover is deliberately NOT dropped. It replaced initialize as
// the connection-time call, and connection establishment is diagnostic signal
// rather than chatter.
//
// Pass it to Config.Filter to reduce metric cardinality and span volume on
// high-traffic servers:
//
//	mcpotel.Middleware(mcpotel.Config{
//	    ServiceName: "my-server",
//	    Filter:      mcpotel.DefaultProtocolFilter,
//	})
//
// Currently filtered methods:
//
//	notifications/initialized
//	notifications/cancelled
//	notifications/progress
//	notifications/roots/list_changed
//	ping
//	tools/list
//	resources/list
//	resources/templates/list
//	prompts/list
//
// This list is intentionally conservative. To filter additional methods,
// compose your own filter:
//
//	Filter: func(method string) bool {
//	    if !mcpotel.DefaultProtocolFilter(method) {
//	        return false
//	    }
//	    return method != "server/discover" // also drop the discovery call
//	}
func DefaultProtocolFilter(method string) bool {
	_, chatter := defaultProtocolChatterMethods[method]
	return !chatter
}
