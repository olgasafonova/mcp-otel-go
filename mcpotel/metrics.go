package mcpotel

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// meters holds the registered OTel metric instruments.
type meters struct {
	operationDuration metric.Float64Histogram
}

// newMeters creates the MCP metric instruments from the given MeterProvider.
func newMeters(mp metric.MeterProvider) (*meters, error) {
	meter := mp.Meter(instrumentationName)

	opDuration, err := meter.Float64Histogram(
		"mcp.server.operation.duration",
		metric.WithDescription("Duration of MCP server operations"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	return &meters{
		operationDuration: opDuration,
	}, nil
}

// recordDuration records the operation duration with method and error attributes.
func (m *meters) recordDuration(ctx context.Context, duration time.Duration, attrs []attribute.KeyValue) {
	m.operationDuration.Record(
		ctx,
		duration.Seconds(),
		metric.WithAttributes(attrs...),
	)
}
