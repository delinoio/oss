package contracts

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestRefreshLatencyMetricHasOnlyRedactedClosedFields(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	metrics := LogRefreshMetrics{
		Logger: slog.New(slog.NewJSONHandler(&output, nil)),
	}
	metrics.ObserveRefresh(RefreshMetricProviderSuccess, 17*time.Millisecond)
	line := output.String()
	if !strings.Contains(line, `"event":"deck_refresh_latency"`) ||
		!strings.Contains(line, `"latency_ms":17`) ||
		!strings.Contains(line, `"outcome":2`) {
		t.Fatalf("metric = %s", line)
	}
	for _, forbidden := range []string{
		"repository", "query", "title", "subject", "token", "url", "SLO",
	} {
		if strings.Contains(strings.ToLower(line), strings.ToLower(forbidden)) {
			t.Fatalf("metric contains %q: %s", forbidden, line)
		}
	}
}
