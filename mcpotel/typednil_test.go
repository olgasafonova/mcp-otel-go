package mcpotel_test

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olgasafonova/mcp-otel-go/mcpotel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// MCP makes `params` OPTIONAL on notifications, so a spec-compliant peer may
// send `{"jsonrpc":"2.0","method":"notifications/initialized"}` with no params
// member at all. The go-sdk represents that as ServerRequest[P]{Params: nil},
// and GetParams returns the typed P field verbatim: a NON-nil Params interface
// wrapping a nil pointer.
//
// Every accessor on those param types is promoted from an embedded Meta VALUE,
// so calling one dereferences the nil pointer. An `x == nil` interface compare
// is false against a typed nil, so the guards this package used to carry did
// not stop it. Observed as SIGSEGV in miro-mcp-server v1.22.0 against
// mcp-otel-go v0.2.0.
//
// These tests drive the middleware's own handler rather than a live server,
// because the go-sdk client always populates params and therefore cannot
// produce the shape a hand-written or third-party client does.

// nilParamsHandler builds the instrumented handler under test. The inner
// handler is a no-op: the assertion is that nothing panics on the way to it.
func nilParamsHandler(t *testing.T) mcp.MethodHandler {
	t.Helper()

	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	mw := mcpotel.Middleware(mcpotel.Config{
		ServiceName:    "test-server",
		TracerProvider: tp,
		Propagator:     propagation.TraceContext{},
	})

	return mw(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.ListToolsResult{}, nil
	})
}

// TestMiddleware_TypedNilParams covers every params type that can reach the
// server with no params member. The notification cases are the ones a real
// peer produces; the request cases pin extractTarget, which reads concrete
// fields off the params and would panic the same way.
func TestMiddleware_TypedNilParams(t *testing.T) {
	cases := []struct {
		method string
		req    mcp.Request
	}{
		// Notifications: params is optional per spec, so these are the
		// shapes a compliant client actually sends.
		{"notifications/initialized", &mcp.ServerRequest[*mcp.InitializedParams]{}},
		{"notifications/cancelled", &mcp.ServerRequest[*mcp.CancelledParams]{}},
		{"notifications/progress", &mcp.ServerRequest[*mcp.ProgressNotificationParams]{}},
		// ClientRequest is the other implementation of mcp.Request, with the
		// same verbatim-field GetParams and a *ClientSession that is a typed
		// nil in the same way.
		{"notifications/resources/updated", &mcp.ClientRequest[*mcp.ResourceUpdatedNotificationParams]{}},

		// Requests whose params feed extractTarget's switch. These reach
		// the concrete-field reads that the nil check must shield.
		{"tools/call", &mcp.ServerRequest[*mcp.CallToolParamsRaw]{}},
		{"resources/read", &mcp.ServerRequest[*mcp.ReadResourceParams]{}},
		{"prompts/get", &mcp.ServerRequest[*mcp.GetPromptParams]{}},

		// List requests, which take the no-target path.
		{"tools/list", &mcp.ServerRequest[*mcp.ListToolsParams]{}},
		{"resources/list", &mcp.ServerRequest[*mcp.ListResourcesParams]{}},
		{"prompts/list", &mcp.ServerRequest[*mcp.ListPromptsParams]{}},
	}

	handler := nilParamsHandler(t)

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			if got := tc.req.GetParams(); got == nil {
				t.Fatalf("test is not exercising the bug: GetParams() returned an untyped nil")
			}

			if _, err := handler(context.Background(), tc.method, tc.req); err != nil {
				t.Fatalf("handler returned error: %v", err)
			}
		})
	}
}

// TestMiddleware_TypedNilSession pins the same defect on the other interface
// the middleware reads. ServerRequest.GetSession returns its *ServerSession
// field verbatim, so an unpopulated session is a typed nil, and ID() reads a
// field off the nil receiver.
func TestMiddleware_TypedNilSession(t *testing.T) {
	req := &mcp.ServerRequest[*mcp.ListToolsParams]{Params: &mcp.ListToolsParams{}}

	if got := req.GetSession(); got == nil {
		t.Fatal("test is not exercising the bug: GetSession() returned an untyped nil")
	}

	handler := nilParamsHandler(t)
	if _, err := handler(context.Background(), "tools/list", req); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
}
