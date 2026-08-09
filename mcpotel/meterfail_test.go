package mcpotel_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/olgasafonova/mcp-otel-go/mcpotel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/embedded"
)

// failingMeterProvider returns a Meter whose instrument constructors always
// error. Used to exercise the metric-registration failure path in Middleware().
type failingMeterProvider struct {
	embedded.MeterProvider
}

func (failingMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return failingMeter{}
}

func brokenMeterErr() error {
	return errors.New("meter is broken")
}

type failingMeter struct {
	embedded.Meter
}

func (failingMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Int64UpDownCounter(string, ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Int64Histogram(string, ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Int64Gauge(string, ...metric.Int64GaugeOption) (metric.Int64Gauge, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Float64Counter(string, ...metric.Float64CounterOption) (metric.Float64Counter, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Float64UpDownCounter(string, ...metric.Float64UpDownCounterOption) (metric.Float64UpDownCounter, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Float64Histogram(string, ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Float64Gauge(string, ...metric.Float64GaugeOption) (metric.Float64Gauge, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Int64ObservableCounter(string, ...metric.Int64ObservableCounterOption) (metric.Int64ObservableCounter, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Int64ObservableUpDownCounter(string, ...metric.Int64ObservableUpDownCounterOption) (metric.Int64ObservableUpDownCounter, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Int64ObservableGauge(string, ...metric.Int64ObservableGaugeOption) (metric.Int64ObservableGauge, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Float64ObservableCounter(string, ...metric.Float64ObservableCounterOption) (metric.Float64ObservableCounter, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Float64ObservableUpDownCounter(string, ...metric.Float64ObservableUpDownCounterOption) (metric.Float64ObservableUpDownCounter, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) Float64ObservableGauge(string, ...metric.Float64ObservableGaugeOption) (metric.Float64ObservableGauge, error) {
	return nil, brokenMeterErr()
}

func (failingMeter) RegisterCallback(metric.Callback, ...metric.Observable) (metric.Registration, error) {
	return nil, brokenMeterErr()
}

func TestMiddleware_MeterRegistrationFailureSurfacesViaSlog(t *testing.T) {
	// Capture slog output for the duration of this test.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Construct middleware with a meter provider that fails on instrument
	// creation. The middleware should not panic and should not return an
	// error to its caller; it should emit a slog.Error and continue.
	_ = mcpotel.Middleware(mcpotel.Config{
		ServiceName:   "test-server-meter-fail",
		MeterProvider: failingMeterProvider{},
	})

	logOutput := buf.String()
	if !strings.Contains(logOutput, "metric registration failed") {
		t.Errorf("expected slog output to mention 'metric registration failed', got: %q", logOutput)
	}
	if !strings.Contains(logOutput, "level=ERROR") {
		t.Errorf("expected ERROR-level log, got: %q", logOutput)
	}
	if !strings.Contains(logOutput, "test-server-meter-fail") {
		t.Errorf("expected service name in log line, got: %q", logOutput)
	}
	if !strings.Contains(logOutput, "meter is broken") {
		t.Errorf("expected underlying error message in log line, got: %q", logOutput)
	}
}
