package telemetry

import (
	"context"
	"testing"
)

func TestTraceExportConfiguredForGenericAndSignalSpecificEndpoints(t *testing.T) {
	for name, endpoints := range map[string]struct {
		generic string
		traces  string
		want    bool
	}{
		"none":            {},
		"generic":         {generic: "https://collector.example/v1/traces", want: true},
		"trace specific":  {traces: "https://traces.example/v1/traces", want: true},
		"both configured": {generic: "https://collector.example", traces: "https://traces.example/v1/traces", want: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoints.generic)
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", endpoints.traces)
			if got := traceExportConfigured(); got != endpoints.want {
				t.Fatalf("traceExportConfigured() = %v, want %v", got, endpoints.want)
			}
		})
	}
}

func TestNewUsesSDKSemanticConventionSchema(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")

	providers, err := New(context.Background(), "devhud-api", "test")
	if err != nil {
		t.Fatalf("create telemetry providers: %v", err)
	}
	if err := providers.Shutdown(context.Background()); err != nil {
		t.Fatalf("shut down telemetry providers: %v", err)
	}
}
