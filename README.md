# mcp-otel-go

[![Go Report Card](https://goreportcard.com/badge/github.com/olgasafonova/mcp-otel-go)](https://goreportcard.com/report/github.com/olgasafonova/mcp-otel-go)
[![CI](https://github.com/olgasafonova/mcp-otel-go/actions/workflows/ci.yml/badge.svg)](https://github.com/olgasafonova/mcp-otel-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/olgasafonova/mcp-otel-go/mcpotel.svg)](https://pkg.go.dev/github.com/olgasafonova/mcp-otel-go/mcpotel)
[![codecov](https://codecov.io/gh/olgasafonova/mcp-otel-go/branch/main/graph/badge.svg)](https://codecov.io/gh/olgasafonova/mcp-otel-go)

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
| Resource URI | `mcp.resource.uri = "miro://board/123"` |
| Prompt name | `gen_ai.prompt.name = "summarize"` |
| Session ID | `mcp.session.id = "abc123"` |
| Error type (both surfaces) | `error.type = "*errors.errorString"` |
| Duration histogram | `mcp.server.operation.duration` (seconds) |

All attribute names follow the [OTel semantic conventions for MCP](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/).

## What does NOT get collected

Privacy-safe by default. The middleware never records:

- Tool arguments or return values
- Resource content
- Environment variables or file paths
- IP addresses or user-identifiable information
- Full error messages (only Go type names like `*json.SyntaxError`, not the message text)

Only method names, tool names, timing, error type names, and session IDs. Resource URIs are recorded by default but can be redacted (see below).

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

### URI redaction (opt-in)

Resource URIs are recorded in full by default. If your URIs contain user-identifiable paths, enable scheme-only recording:

```go
mcpotel.Middleware(mcpotel.Config{
    ServiceName: "my-server",
    RedactURI:   mcpotel.URISchemeOnly, // "file:///home/john/secret.txt" → "file://"
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
    RedactURI      func(uri string) string   // Optional. Nil = full URI recorded
}
```

### Filtering methods

Skip instrumentation for noisy methods:

```go
mcpotel.Middleware(mcpotel.Config{
    ServiceName: "my-server",
    Filter: func(method string) bool {
        return method != "notifications/initialized"
    },
})
```

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

## Quick start with otel-tui

[otel-tui](https://github.com/ymtdzzz/otel-tui) is a terminal UI that receives OTLP and displays traces and metrics. No Prometheus, Grafana, or Docker required.

**Install:**

```bash
brew install ymtdzzz/tap/otel-tui   # macOS
# or: go install github.com/ymtdzzz/otel-tui@latest
```

**Run (two terminals):**

```bash
# Terminal 1 — start the collector UI
otel-tui

# Terminal 2 — run the OTLP example server
cd mcp-otel-go
go run ./examples/otlp
```

Then use [MCP Inspector](https://github.com/modelcontextprotocol/inspector) to call tools:

```bash
npx @modelcontextprotocol/inspector go run ./examples/otlp
```

Call the `greet` tool a few times. Switch to the otel-tui terminal:

- **Traces tab** shows `tools/call greet` spans with `mcp.method.name`, `gen_ai.tool.name`, and `mcp.session.id` attributes
- **Metrics tab** shows the `mcp.server.operation.duration` histogram

The OTLP example exports both traces and metrics over gRPC to `localhost:4317`. Set `OTEL_EXPORTER_OTLP_ENDPOINT` to point elsewhere.

### Alternative: Jaeger

For a richer web UI with trace waterfall diagrams, use [Jaeger](https://www.jaegertracing.io/) (requires Docker):

```bash
docker run -d -p 16686:16686 -p 4317:4317 jaegertracing/jaeger:latest
```

Open `http://localhost:16686`, then run the OTLP example the same way. No code changes needed — same OTLP endpoint.

## Dependencies

- `github.com/modelcontextprotocol/go-sdk` v1.3.0+
- `go.opentelemetry.io/otel` v1.34.0+
- No exporter dependencies. You bring your own.

## License

MIT
