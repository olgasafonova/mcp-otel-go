# mcp-otel-go

OpenTelemetry middleware for Go MCP servers built with the [official go-sdk](https://github.com/modelcontextprotocol/go-sdk).

One function call adds tracing and metrics to every MCP method. Follows the [OTel semantic conventions for MCP](https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/).

## Install

```bash
go get github.com/olgasafonova/mcp-otel-go/mcpotel
```

## Usage

```go
import "github.com/olgasafonova/mcp-otel-go/mcpotel"

server := mcp.NewServer(impl, opts)
server.AddReceivingMiddleware(mcpotel.Middleware(mcpotel.Config{
    ServiceName:    "my-mcp-server",
    ServiceVersion: "1.0.0",
}))
```

That's it. Every incoming MCP method call now produces an OTel span and a duration histogram.

## What gets collected

| Data | Example |
|------|---------|
| Span per method call | `tools/call miro_create_sticky` |
| Method name attribute | `mcp.method.name = "tools/call"` |
| Tool name (for tools/call) | `gen_ai.tool.name = "miro_create_sticky"` |
| Resource URI (for resources/read) | `mcp.resource.uri = "miro://board/123"` |
| Prompt name (for prompts/get) | `gen_ai.prompt.name = "summarize"` |
| Session ID | `mcp.session.id = "abc123"` |
| Error status + type | `error.type = "unknown tool \"foo\""` |
| Duration histogram | `mcp.server.operation.duration` (seconds) |

## What does NOT get collected

The middleware is privacy-safe by design:

- No tool arguments or return values
- No resource content
- No environment variables or file paths
- No IP addresses or user-identifiable information

Only: method names, tool names, resource URIs, timing, error types, session IDs.

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

This package has zero opinions on where telemetry goes. Configure your `TracerProvider` and `MeterProvider` at app startup as usual:

```go
// OTLP to Jaeger/Tempo/etc.
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
