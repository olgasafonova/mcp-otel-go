package mcpotel

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/propagation"
)

// metaCarrier adapts an MCP request's `_meta` map to the OpenTelemetry
// TextMapCarrier interface, so a standard TextMapPropagator can read trace
// context straight out of it.
//
// MCP revision 2026-07-28 documents the OpenTelemetry trace-context convention
// for `_meta` (SEP-414) using the W3C key names verbatim and UNPREFIXED:
// `traceparent`, `tracestate`, `baggage`. They deliberately carry no
// `io.modelcontextprotocol/` namespace, unlike protocolVersion or clientInfo.
// That is what makes this adapter a pure type shim: the W3C TraceContext and
// Baggage propagators already read and write exactly those keys, so no key
// translation is needed or wanted. Introducing one would put this
// implementation off-spec.
//
// `_meta` is map[string]any while a carrier is string-keyed and string-valued,
// so Get coerces and non-string values read as absent rather than panicking. A
// peer that sent a non-string traceparent is malformed; dropping the trace link
// is the right response, not failing the request.
type metaCarrier map[string]any

// Get returns the string value for key, or "" if absent or not a string.
func (c metaCarrier) Get(key string) string {
	v, ok := c[key].(string)
	if !ok {
		return ""
	}
	return v
}

// Set writes a string value into the underlying `_meta` map. It is a no-op on a
// nil map, which is the shape a request with no `_meta` produces.
func (c metaCarrier) Set(key, value string) {
	if c == nil {
		return
	}
	c[key] = value
}

// Keys returns the keys carrying string values. Non-string entries are omitted
// because they cannot be part of a text map.
func (c metaCarrier) Keys() []string {
	if len(c) == 0 {
		return nil
	}
	keys := make([]string, 0, len(c))
	for k, v := range c {
		if _, ok := v.(string); ok {
			keys = append(keys, k)
		}
	}
	return keys
}

// carrierFor returns a TextMapCarrier over the request's `_meta` map. A request
// with no params or no `_meta` yields an empty carrier, and Extract against an
// empty carrier is a no-op that leaves the context unchanged, so the caller
// needs no special case.
func carrierFor(req mcp.Request) propagation.TextMapCarrier {
	params := req.GetParams()
	if params == nil {
		return metaCarrier(nil)
	}
	return metaCarrier(params.GetMeta())
}

// compile-time assertion that the adapter satisfies the carrier contract.
var _ propagation.TextMapCarrier = metaCarrier(nil)
