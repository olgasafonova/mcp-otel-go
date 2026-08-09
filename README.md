# mcp-otel-go

[![CI](https://github.com/olgasafonova/mcp-otel-go/actions/workflows/ci.yml/badge.svg)](https://github.com/olgasafonova/mcp-otel-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/olgasafonova/mcp-otel-go/mcpotel.svg)](https://pkg.go.dev/github.com/olgasafonova/mcp-otel-go/mcpotel)
[![codecov](https://codecov.io/gh/olgasafonova/mcp-otel-go/branch/main/graph/badge.svg)](https://codecov.io/gh/olgasafonova/mcp-otel-go)
[![CodeScene Average Code Health](https://codescene.io/projects/83411/status-badges/average-code-health)](https://codescene.io/projects/83411)
[![Release](https://img.shields.io/github/v/tag/olgasafonova/mcp-otel-go?sort=semver&label=release)](https://github.com/olgasafonova/mcp-otel-go/tags)


OpenTelemetry (OTel) tracing and metrics for Go MCP servers. One function call instruments every method in a [go-sdk](https://github.com/modelcontextprotocol/go-sdk) server, following the [OTel semantic conventions for MCP](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/).

The go-sdk doesn't include observability out of the box, and existing OpenTelemetry integrations for MCP ([MCPcat](https://github.com/MCPCat/mcp-cat), [Shinzo Labs](https://github.com/shinzo-labs/otel-mcp)) are TypeScript-only. This is the Go equivalent.

## Who is this for?

You're building MCP servers in Go with the official go-sdk. You already have OTel infrastructure (Jaeger, Grafana Tempo, Prometheus, Datadog) and you want your MCP servers reporting into it. You shouldn't have to write custom instrumentation for every tool handler. Nothing else exists for Go today.

## Install

```bash
go get github.com/olgasafonova/mcp-otel-go/mcpotel
```

## Usage

```go
server := mcp.NewServer(impl, opts)
server.AddReceivingMiddleware(mcpotel.Middleware(mcpotel.Config{
    ServiceName:    "my-mcp-server",
    ServiceVersion: "1.0.0",
}))
```

Three lines. Every incoming MCP method call now produces an OTel span and a duration histogram.

## Two error surfaces, both covered

MCP tool errors split into two categories, and most instrumentation only catches one.

**Protocol errors** happen when the tool doesn't exist or params are invalid. The go-sdk returns these as normal Go errors. Easy to catch.

**Application errors** happen when your tool handler returns an error (database down, API timeout, bad input). The go-sdk wraps these into `CallToolResult{IsError: true}` and returns `nil` for the error. Your middleware sees a "successful" call. Your dashboard shows green. Your users see failures.

This middleware catches both. It inspects `CallToolResult.IsError` after every `tools/call` and marks the span as an error with the original error message.

## What gets collected

| Data | Example |
|------|---------|
| Span per method call | `tools/call miro_create_sticky` |
| Method name | `mcp.method.name = "tools/call"` |
| Tool name | `gen_ai.tool.name = "miro_create_sticky"` |
| Resource URI scheme | `mcp.resource.uri = "miro://"` (scheme-only by default; opt in to full URIs with `RedactURI: mcpotel.URIFull`) |
| Prompt name | `gen_ai.prompt.name = "summarize"` |
| Session ID | `mcp.session.id = "abc123"` |
| Error type (both surfaces) | `error.type = "*errors.errorString"` |
| Duration histogram | `mcp.server.operation.duration` (seconds) |

All attribute names follow the [OTel semantic conventions for MCP](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/).

## Distributed tracing across the MCP boundary

Spans emitted here join the caller's trace when the request carries W3C trace
context. MCP revision `2026-07-28` documents the convention (SEP-414): the
context travels in the request's `_meta` under the **unprefixed** W3C key names
`traceparent`, `tracestate` and `baggage`. They carry no
`io.modelcontextprotocol/` namespace, unlike `protocolVersion` or `clientInfo`,
which is what lets the standard OTel propagators read them with no translation
layer.

The middleware extracts that context before it starts its span, so the span
becomes a child of the caller's span rather than a new root.

**One gotcha, and it is the reason this can silently do nothing.** OTel's global
default is a **no-op** propagator. An application that never calls
`otel.SetTextMapPropagator` gets no propagation anywhere in its stack, including
here. Opt in explicitly, either globally:

```go
otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
    propagation.TraceContext{},
    propagation.Baggage{},
))
```

or per-middleware via `Config.Propagator`.

A request with no trace context still produces a normal root span, and a
malformed or non-string `traceparent` degrades to a root span rather than
failing the request.

## What does NOT get collected

Privacy-safe by default. The middleware never records:

- Tool arguments or return values
- Resource content
- Environment variables or file paths
- IP addresses or user-identifiable information
- Full error messages (only Go type names like `*json.SyntaxError`, not the message text)
- Full resource URI paths (only the scheme, e.g., `file://` not `file:///home/john/secret.txt`)

Only method names, tool names, timing, error type names, session IDs, and URI schemes.

## Privacy controls

Error messages from tool handlers can contain PII (e.g., `"user john@example.com not found"`). Resource URIs can contain user-identifiable paths (e.g., `"user://john.doe/profile"`). The middleware provides two redaction hooks to control what reaches your telemetry backend.

### Error redaction (on by default)

By default, only the Go error type name is recorded (e.g., `*json.SyntaxError`), not the full error message. This is safe because type names are developer-defined and never contain user data.

```go
// Default behavior: records "*json.SyntaxError", not "invalid field: email john@example.com"
mcpotel.Middleware(mcpotel.Config{
    ServiceName: "my-server",
})
```

Opt in to full error messages only if your errors are known to be PII-free:

```go
mcpotel.Middleware(mcpotel.Config{
    ServiceName: "my-server",
    RedactError: mcpotel.ErrorMessageFull,
})
```

Or provide your own classifier:

```go
mcpotel.Middleware(mcpotel.Config{
    ServiceName: "my-server",
    RedactError: func(err error) string {
        // Classify by error type, strip PII, or return a fixed string
        return "internal_error"
    },
})
```

### URI redaction (on by default)

By default, only the URI scheme is recorded (e.g., `file://`, `miro://`), not the full path. This is safe because URI schemes are server-defined and never contain user data.

```go
// Default behavior: records "file://", not "file:///home/john/secret.txt"
mcpotel.Middleware(mcpotel.Config{
    ServiceName: "my-server",
})
```

Opt in to full URIs only when you control the URI namespace and the paths contain no PII (e.g., opaque IDs from your own system):

```go
mcpotel.Middleware(mcpotel.Config{
    ServiceName: "my-server",
    RedactURI:   mcpotel.URIFull, // record complete URI verbatim
})
```

Or provide your own redactor:

```go
mcpotel.Middleware(mcpotel.Config{
    ServiceName: "my-server",
    RedactURI: func(uri string) string {
        // Hash, classify, or strip per-component
        return classifyURI(uri)
    },
})
```

### Data controller responsibility

This middleware is a data processor. You, as the MCP server operator, are the data controller. You decide:

- Which telemetry backend receives the data
- How long spans and metrics are retained
- Whether error messages or URIs need redaction for your use case
- Compliance with GDPR, CCPA, or other applicable regulations

Session IDs are random protocol identifiers, not user identifiers. They become pseudonymous data only if your telemetry backend correlates them with user identity through other means.

## Config

```go
type Config struct {
    ServiceName    string                    // Required. OTel service.name
    ServiceVersion string                    // Optional. service.version
    TracerProvider trace.TracerProvider       // Optional. Defaults to otel.GetTracerProvider()
    MeterProvider  metric.MeterProvider      // Optional. Defaults to otel.GetMeterProvider()
    Filter         func(method string) bool  // Optional. Return false to skip a method
    RedactError    func(err error) string    // Optional. Defaults to Go type name only
    RedactURI      func(uri string) string   // Optional. Defaults to URISchemeOnly
    Propagator     propagation.TextMapPropagator // Optional. Defaults to otel.GetTextMapPropagator()
}
```

### Filtering methods

By default, every MCP method is instrumented, including protocol housekeeping like `notifications/initialized`, `ping`, and `tools/list`. On high-traffic servers this can dominate telemetry without carrying diagnostic value.

Use the built-in `DefaultProtocolFilter` to drop the well-known chatter while keeping `tools/call`, `resources/read`, `prompts/get`, and `server/discover`:

```go
mcpotel.Middleware(mcpotel.Config{
    ServiceName: "my-server",
    Filter:      mcpotel.DefaultProtocolFilter,
})
```

`DefaultProtocolFilter` currently drops: `notifications/initialized`, `notifications/cancelled`, `notifications/progress`, `notifications/roots/list_changed`, `ping`, `tools/list`, `resources/list`, `resources/templates/list`, `prompts/list`.

Or compose your own:

```go
mcpotel.Middleware(mcpotel.Config{
    ServiceName: "my-server",
    Filter: func(method string) bool {
        if !mcpotel.DefaultProtocolFilter(method) {
            return false
        }
        return method != "server/discover" // also drop the discovery call
    },
})
```

`DefaultProtocolFilter` is not the default `Filter`. The default (`nil`) is to instrument everything, so existing setups keep recording the same methods they always did. Opt in when you decide the noise outweighs the signal.

## Bring your own exporter

No opinions on where telemetry goes. Configure your providers at startup as usual:

```go
exporter, _ := otlptracegrpc.New(ctx)
tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
otel.SetTracerProvider(tp)

// The middleware picks up the global provider automatically
server.AddReceivingMiddleware(mcpotel.Middleware(mcpotel.Config{
    ServiceName: "my-server",
}))
```

Or pass providers explicitly:

```go
server.AddReceivingMiddleware(mcpotel.Middleware(mcpotel.Config{
    ServiceName:    "my-server",
    TracerProvider: myCustomTP,
    MeterProvider:  myCustomMP,
}))
```

## Try it locally

The `examples/otlp/` demo exports traces and metrics over gRPC to `localhost:4317`. Point any OTLP-compatible backend at it — no code changes needed.

**Generate telemetry** (same for every backend):

```bash
# Terminal — run the OTLP example via MCP Inspector
npx @modelcontextprotocol/inspector go run ./examples/otlp
```

Connect in the Inspector UI, call the `greet` tool a few times. Then check your backend for:

- `tools/call greet` spans with `mcp.method.name`, `gen_ai.tool.name`, `mcp.session.id` attributes
- `mcp.server.operation.duration` histogram

Set `OTEL_EXPORTER_OTLP_ENDPOINT` to override the default `localhost:4317`.

### otel-tui (no Docker, no setup)

Terminal UI that receives OTLP directly. Fastest way to see traces.

```bash
brew install ymtdzzz/tap/otel-tui   # macOS
# or: go install github.com/ymtdzzz/otel-tui@latest
otel-tui                             # listens on :4317
```

### Jaeger (traces)

Web UI with trace waterfall diagrams and dependency graphs.

```bash
docker run -d -p 16686:16686 -p 4317:4317 jaegertracing/jaeger:latest
```

Open `http://localhost:16686`. Select the `example-server` service to see traces.

### Grafana + Tempo + Prometheus (traces + metrics)

Full observability stack with dashboards. Create a `docker-compose.yml`:

```yaml
services:
  tempo:
    image: grafana/tempo:latest
    command: ["-config.file=/etc/tempo.yaml"]
    volumes:
      - ./tempo.yaml:/etc/tempo.yaml
    ports:
      - "4317:4317"

  prometheus:
    image: prom/prometheus:latest
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
    ports:
      - "9090:9090"

  grafana:
    image: grafana/grafana:latest
    ports:
      - "3000:3000"
    environment:
      - GF_AUTH_ANONYMOUS_ENABLED=true
      - GF_AUTH_ANONYMOUS_ORG_ROLE=Admin
```

Add a minimal `tempo.yaml`:

```yaml
server:
  http_listen_port: 3200

distributor:
  receivers:
    otlp:
      protocols:
        grpc:
          endpoint: "0.0.0.0:4317"

storage:
  trace:
    backend: local
    local:
      path: /tmp/tempo/blocks
```

```bash
docker compose up -d
```

Open `http://localhost:3000`, add Tempo as a data source (`http://tempo:3200`), and explore traces.

### Datadog

Set your API key and site, then use the Datadog Agent as the OTLP collector:

```bash
docker run -d \
  -e DD_API_KEY=<your-api-key> \
  -e DD_SITE=datadoghq.com \
  -e DD_OTLP_CONFIG_RECEIVER_PROTOCOLS_GRPC_ENDPOINT=0.0.0.0:4317 \
  -p 4317:4317 \
  gcr.io/datadoghq/agent:latest
```

Traces and metrics appear in the [Datadog APM](https://app.datadoghq.com/apm) dashboard.

### Honeycomb

Cloud-native observability with a [generous free tier](https://www.honeycomb.io/pricing). No Docker needed — send OTLP directly:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=https://api.honeycomb.io \
OTEL_EXPORTER_OTLP_HEADERS="x-honeycomb-team=<your-api-key>" \
go run ./examples/otlp
```

### Grafana Cloud

[Free tier](https://grafana.com/pricing/) includes traces and metrics. Get your OTLP endpoint and token from the Grafana Cloud portal:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp-gateway-<zone>.grafana.net/otlp \
OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic <base64-encoded-credentials>" \
go run ./examples/otlp
```

## Dependencies

- `github.com/modelcontextprotocol/go-sdk` v1.3.0+
- `go.opentelemetry.io/otel` v1.34.0+
- No exporter dependencies. You bring your own.

## License

MIT
