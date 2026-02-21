# mcp-otel-go

OpenTelemetry tracing and metrics for Go MCP servers. One function call instruments every method in a [go-sdk](https://github.com/modelcontextprotocol/go-sdk) server, following the [OTel semantic conventions for MCP](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/).

The go-sdk doesn't include observability out of the box, and existing OTel integrations for MCP ([MCPcat](https://github.com/MCPCat/mcp-cat), [Shinzo Labs](https://github.com/shinzo-labs/otel-mcp)) are TypeScript-only. This is the Go equivalent.

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
| Error type (both surfaces) | `error.type = "database connection lost"` |
| Duration histogram | `mcp.server.operation.duration` (seconds) |

All attribute names follow the [OTel semantic conventions for MCP](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/).

## What does NOT get collected

Privacy-safe by design. The middleware never records:

- Tool arguments or return values
- Resource content
- Environment variables or file paths
- IP addresses or user-identifiable information

Only method names, tool names, resource URIs, timing, error types, and session IDs.

## Config

```go
type Config struct {
    ServiceName    string                // Required. OTel service.name
    ServiceVersion string                // Optional. service.version
    TracerProvider trace.TracerProvider   // Optional. Defaults to otel.GetTracerProvider()
    MeterProvider  metric.MeterProvider  // Optional. Defaults to otel.GetMeterProvider()
    Filter         func(method string) bool  // Optional. Return false to skip a method
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

## Dependencies

- `github.com/modelcontextprotocol/go-sdk` v1.3.0+
- `go.opentelemetry.io/otel` v1.34.0+
- No exporter dependencies. You bring your own.

## License

MIT
