package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/observability"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
)

func newMeteredServer(t *testing.T, out *bytes.Buffer, stub *stubOracle) http.Handler {
	t.Helper()

	cfg := &config.Config{}
	cfg.API.MaxPageSize = 100
	cfg.API.WriteTimeout = 5 * time.Second
	cfg.Chain.ChainID = testChainID

	h := &handlers{
		chains:      chainMap([]ChainDeps{{ID: testChainID, Codec: chainStub{}, Oracle: stub}}),
		maxPageSize: cfg.API.MaxPageSize,
		log:         zerolog.New(io.Discard),
		metrics:     observability.NewMetricsTo(out, "api", "staging"),
	}
	return routes(cfg, h, zerolog.New(io.Discard))
}

func emittedLines(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()

	var parsed []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("emitted a line that is not JSON: %v\n%s", err, line)
		}
		parsed = append(parsed, entry)
	}
	return parsed
}

// The three measurements the required alarms are built from. Latency feeds the p99 alarm; the two
// counts feed the error-rate alarm, which divides sums over its own window.
func TestARequestEmitsLatencyAndCounts(t *testing.T) {
	var out bytes.Buffer
	srv := newMeteredServer(t, &out, &stubOracle{})

	if code, _ := get(t, srv, "/v1/oracle/feeds"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}

	lines := emittedLines(t, &out)
	if len(lines) != 1 {
		t.Fatalf("emitted %d lines, want 1", len(lines))
	}

	line := lines[0]
	for _, name := range []string{
		observability.MetricAPILatencyMillis,
		observability.MetricAPIRequests,
		observability.MetricAPIServerErrors,
	} {
		if _, present := line[name]; !present {
			t.Errorf("%s was not emitted", name)
		}
	}
	if line[observability.MetricAPIRequests] != float64(1) {
		t.Errorf("request count = %v, want 1", line[observability.MetricAPIRequests])
	}
	if line[observability.MetricAPIServerErrors] != float64(0) {
		t.Errorf("a 200 counted as a server error")
	}
}

// A 5xx must count. Without this the error-rate alarm watches a metric that is always zero and
// reads as healthy through an outage.
func TestAServerErrorIsCounted(t *testing.T) {
	var out bytes.Buffer
	stub := &stubOracle{err: errors.New("boom")}
	srv := newMeteredServer(t, &out, stub)

	if code, _ := get(t, srv, "/v1/oracle/feeds"); code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", code)
	}

	lines := emittedLines(t, &out)
	if len(lines) != 1 {
		t.Fatalf("emitted %d lines, want 1", len(lines))
	}
	if lines[0][observability.MetricAPIServerErrors] != float64(1) {
		t.Errorf("server errors = %v, want 1", lines[0][observability.MetricAPIServerErrors])
	}
}

// A 400 is the caller's fault, not the service's. Counting it would make a scanner probing bad
// input look like an outage.
func TestAClientErrorIsNotAServerError(t *testing.T) {
	var out bytes.Buffer
	srv := newMeteredServer(t, &out, &stubOracle{})

	if code, _ := get(t, srv, "/v1/oracle/feeds?limit=0"); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}

	lines := emittedLines(t, &out)
	if len(lines) != 1 {
		t.Fatalf("emitted %d lines, want 1", len(lines))
	}
	if lines[0][observability.MetricAPIServerErrors] != float64(0) {
		t.Errorf("a 400 counted as a server error")
	}
	if lines[0][observability.MetricAPIRequests] != float64(1) {
		t.Errorf("a 400 was not counted as a request")
	}
}

// The load balancer polls these far more often than any real endpoint. Including them would dilute
// the latency percentile and the error rate until neither described user traffic.
func TestHealthAndReadinessAreNotMetered(t *testing.T) {
	var out bytes.Buffer
	srv := newMeteredServer(t, &out, &stubOracle{})

	for _, path := range []string{"/health", "/ready"} {
		get(t, srv, path)
	}

	if out.Len() != 0 {
		t.Errorf("probes were metered: %s", out.String())
	}
}

// A nil metrics client is the disabled configuration and must not panic a request.
func TestRequestsSucceedWithMetricsDisabled(t *testing.T) {
	srv := newTestServer(t, &stubOracle{})

	if code, _ := get(t, srv, "/v1/oracle/feeds"); code != http.StatusOK {
		t.Fatalf("status = %d with metrics disabled", code)
	}
}
