// Checks spans record errors without database connection identities.

package tracing

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestEndSpanRecordsRedactedErrors(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	_, span := provider.Tracer("test").Start(t.Context(), "database")
	EndSpan(span, fmt.Errorf("ping: %w", &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 5432}, Err: errors.New("connect: connection refused")}))

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d", len(ended))
	}
	recorded := ended[0]
	if recorded.Status().Description != "ping: dial tcp: connect: connection refused" {
		t.Fatalf("status = %q", recorded.Status().Description)
	}
	for _, event := range recorded.Events() {
		for _, attribute := range event.Attributes {
			if strings.Contains(attribute.Value.Emit(), "10.0.0.5") {
				t.Fatalf("span event leaked the address: %v", attribute)
			}
		}
	}
	RecordError(span, nil) // A nil error records nothing.
}
