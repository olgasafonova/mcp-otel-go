package mcpotel_test

import (
	"testing"

	"github.com/olgasafonova/mcp-otel-go/mcpotel"
)

func TestURISchemeOnly_NoScheme(t *testing.T) {
	result := mcpotel.URISchemeOnly("just-a-path")
	if result != "unknown://" {
		t.Errorf("URISchemeOnly(%q) = %q, want %q", "just-a-path", result, "unknown://")
	}
}
