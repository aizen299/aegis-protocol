package oraclenode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

type stubSource struct {
	name     string
	value    string
	err      error
	failures int32
	calls    int32
	delay    time.Duration
}

func (s *stubSource) Name() string { return s.name }

func (s *stubSource) Fetch(ctx context.Context, decimals uint8) (types.Raw, error) {
	atomic.AddInt32(&s.calls, 1)
	if s.delay > 0 {
		select {
		case <-ctx.Done():
			return types.Raw{}, ctx.Err()
		case <-time.After(s.delay):
		}
	}
	if atomic.LoadInt32(&s.failures) > 0 {
		atomic.AddInt32(&s.failures, -1)
		return types.Raw{}, errors.New("transient")
	}
	if s.err != nil {
		return types.Raw{}, s.err
	}
	return ParseScaled(s.value, decimals)
}

func newFetcher(t *testing.T, sources []Source, opts FetcherOptions) *Fetcher {
	t.Helper()
	if opts.BaseDelay == 0 {
		opts.BaseDelay = time.Millisecond
	}
	return NewFetcher(sources, zerolog.New(io.Discard), opts)
}

// The median across sources is what protects this node from a single compromised or broken feed.
// The contract's median protects the network from a bad node; this one protects the node.
func TestPriceIsTheMedianAcrossSources(t *testing.T) {
	sources := []Source{
		&stubSource{name: "a", value: "3000"},
		&stubSource{name: "b", value: "3010"},
		&stubSource{name: "c", value: "9999"}, // compromised
	}

	price, result, err := newFetcher(t, sources, FetcherOptions{MinSources: 3}).Price(context.Background(), 18)
	if err != nil {
		t.Fatalf("price: %v", err)
	}
	if FormatScaled(price, 18) != "3010" {
		t.Fatalf("price = %s, want the median 3010, unmoved by the outlier", FormatScaled(price, 18))
	}
	if len(result.Values) != 3 {
		t.Fatalf("values = %v", result.Values)
	}
}

// A missed round costs 0.5% of stake; a wrong submission costs 1% and pollutes the median everyone
// else relies on. Below the source floor the node stays silent.
func TestTooFewSourcesRefusesToProduceAPrice(t *testing.T) {
	sources := []Source{
		&stubSource{name: "a", value: "3000"},
		&stubSource{name: "b", err: errors.New("503")},
		&stubSource{name: "c", err: errors.New("timeout")},
	}

	_, result, err := newFetcher(t, sources, FetcherOptions{MinSources: 3, MaxRetries: 1}).
		Price(context.Background(), 18)

	if !errors.Is(err, ErrTooFewSources) {
		t.Fatalf("err = %v, want ErrTooFewSources rather than a price from one source", err)
	}
	if len(result.Failed) != 2 {
		t.Errorf("failures = %v; they are kept so a consistently dead source is visible", result.Failed)
	}
}

func TestOneFailingSourceStillProducesAPrice(t *testing.T) {
	sources := []Source{
		&stubSource{name: "a", value: "3000"},
		&stubSource{name: "b", value: "3000"},
		&stubSource{name: "c", value: "3000"},
		&stubSource{name: "d", err: errors.New("503")},
	}

	price, result, err := newFetcher(t, sources, FetcherOptions{MinSources: 3, MaxRetries: 1}).
		Price(context.Background(), 18)
	if err != nil {
		t.Fatalf("price: %v", err)
	}
	if FormatScaled(price, 18) != "3000" {
		t.Fatalf("price = %s", FormatScaled(price, 18))
	}
	if len(result.Failed) != 1 {
		t.Errorf("the failed source was not recorded: %v", result.Failed)
	}
}

// A transient failure must not cost the round when a retry would have worked.
func TestTransientFailureIsRetried(t *testing.T) {
	flaky := &stubSource{name: "flaky", value: "3000", failures: 2}
	sources := []Source{
		flaky,
		&stubSource{name: "b", value: "3000"},
		&stubSource{name: "c", value: "3000"},
	}

	if _, _, err := newFetcher(t, sources, FetcherOptions{MinSources: 3, MaxRetries: 5}).
		Price(context.Background(), 18); err != nil {
		t.Fatalf("price: %v", err)
	}
	if calls := atomic.LoadInt32(&flaky.calls); calls != 3 {
		t.Fatalf("flaky source called %d times, want 3 (two failures then success)", calls)
	}
}

// Retrying is bounded: a round has a deadline, and retrying past it turns one slow source into a
// missed round for all of them.
func TestRetriesAreBounded(t *testing.T) {
	broken := &stubSource{name: "broken", err: errors.New("down")}

	_, _, _ = newFetcher(t, []Source{broken}, FetcherOptions{MinSources: 1, MaxRetries: 4}).
		Price(context.Background(), 18)

	if calls := atomic.LoadInt32(&broken.calls); calls != 4 {
		t.Fatalf("called %d times, want exactly 4", calls)
	}
}

// A source reporting zero is a broken source, not a free price.
func TestZeroFromASourceIsTreatedAsAFailure(t *testing.T) {
	sources := []Source{
		&stubSource{name: "zero", value: "0"},
		&stubSource{name: "b", value: "3000"},
		&stubSource{name: "c", value: "3000"},
	}

	_, result, err := newFetcher(t, sources, FetcherOptions{MinSources: 3, MaxRetries: 2}).
		Price(context.Background(), 18)

	if err == nil {
		t.Fatal("a zero reading was counted as a valid source")
	}
	if _, failed := result.Failed["zero"]; !failed {
		t.Errorf("the zero source was not recorded as failed: %v", result.Failed)
	}
}

// A hung source must not hold the round open past its deadline.
func TestSlowSourceIsCutOffByContext(t *testing.T) {
	sources := []Source{
		&stubSource{name: "slow", value: "3000", delay: time.Hour},
		&stubSource{name: "b", value: "3000"},
		&stubSource{name: "c", value: "3000"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_, _, _ = newFetcher(t, sources, FetcherOptions{MinSources: 3, MaxRetries: 2}).Price(ctx, 18)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a hung source held the fetch open")
	}
}

// --- HTTP source ---

func TestHTTPSourceReadsANestedPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"amount":"3000.42","currency":"USD"}}`)
	}))
	defer server.Close()

	source := &HTTPSource{SourceName: "test", URL: server.URL, Path: "data.amount"}
	value, err := source.Fetch(context.Background(), 18)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if value.String() != "3000420000000000000000" {
		t.Fatalf("value = %s", value)
	}
}

// A JSON number must reach the parser without a float round trip in between.
func TestHTTPSourceHandlesBareNumbers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"price":3000.5}`)
	}))
	defer server.Close()

	source := &HTTPSource{SourceName: "test", URL: server.URL, Path: "price"}
	value, err := source.Fetch(context.Background(), 6)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if value.String() != "3000500000" {
		t.Fatalf("value = %s, want 3000.5 at six decimals", value)
	}
}

func TestHTTPSourceRejectsBadResponses(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"non-200":      func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) },
		"invalid json": func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "not json") },
		"missing path": func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"other":1}`) },
		"wrong type":   func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"price":{"a":1}}`) },
	}

	for name, handler := range cases {
		server := httptest.NewServer(handler)
		source := &HTTPSource{SourceName: name, URL: server.URL, Path: "price"}

		if _, err := source.Fetch(context.Background(), 18); err == nil {
			t.Errorf("%s: fetch succeeded", name)
		}
		server.Close()
	}
}

func TestHTTPSourceBoundsTheResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Far larger than the reader's limit, and never valid JSON.
		for i := 0; i < 200_000; i++ {
			_, _ = io.WriteString(w, "aaaaaaaaaa")
		}
	}))
	defer server.Close()

	source := &HTTPSource{SourceName: "flood", URL: server.URL, Path: "price", Timeout: 5 * time.Second}
	if _, err := source.Fetch(context.Background(), 18); err == nil {
		t.Fatal("an unbounded body was accepted")
	}
}

func TestExtractPathHandlesJSONNumberType(t *testing.T) {
	decoder := json.NewDecoder(io.NopCloser(io.Reader(nil)))
	_ = decoder

	got, err := extractPath([]byte(`{"a":{"b":"12.5"}}`), "a.b")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got != "12.5" {
		t.Fatalf("got %q", got)
	}
}
