package mcpotel_test

import (
	"testing"

	"github.com/olgasafonova/mcp-otel-go/mcpotel"
)

func TestDefaultProtocolFilter_DropsChatterKeepsWork(t *testing.T) {
	cases := []struct {
		method string
		keep   bool
	}{
		{"notifications/initialized", false},
		{"notifications/cancelled", false},
		{"notifications/progress", false},
		{"notifications/roots/list_changed", false},
		{"ping", false},
		{"tools/list", false},
		{"resources/list", false},
		{"resources/templates/list", false},
		{"prompts/list", false},
		{"tools/call", true},
		{"resources/read", true},
		{"prompts/get", true},
		{"server/discover", true},
		{"completion/complete", true},
		{"sampling/createMessage", true},
		{"some/unknown/method", true},
	}
	for _, tc := range cases {
		if got := mcpotel.DefaultProtocolFilter(tc.method); got != tc.keep {
			t.Errorf("DefaultProtocolFilter(%q) = %v, want %v", tc.method, got, tc.keep)
		}
	}
}
