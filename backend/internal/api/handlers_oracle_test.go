package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	testChainID  = int64(31337)
	testFeedID   = "0xabcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	testNodeAddr = "0x70997970c51812dc3a010c7d01b50e0d17dc79c8"
)

type stubOracle struct {
	feeds       []types.OracleFeed
	feed        types.OracleFeed
	rounds      []types.OracleRound
	round       types.OracleRound
	submissions []types.OracleSubmission
	nodes       []types.OracleNode
	node        types.OracleNode
	err         error

	gotLimit  int
	gotOffset int
	gotFeedID string
	gotRound  string
	gotNode   string
}

func (s *stubOracle) Feeds(_ context.Context, limit, offset int) ([]types.OracleFeed, error) {
	s.gotLimit, s.gotOffset = limit, offset
	return s.feeds, s.err
}

func (s *stubOracle) Feed(_ context.Context, feedID string) (types.OracleFeed, error) {
	s.gotFeedID = feedID
	return s.feed, s.err
}

func (s *stubOracle) Rounds(_ context.Context, feedID string, limit, offset int) ([]types.OracleRound, error) {
	s.gotFeedID, s.gotLimit, s.gotOffset = feedID, limit, offset
	return s.rounds, s.err
}

func (s *stubOracle) Round(_ context.Context, roundID types.Raw) (types.OracleRound, error) {
	s.gotRound = roundID.String()
	return s.round, s.err
}

func (s *stubOracle) Submissions(_ context.Context, roundID types.Raw, limit, offset int) ([]types.OracleSubmission, error) {
	s.gotRound, s.gotLimit, s.gotOffset = roundID.String(), limit, offset
	return s.submissions, s.err
}

func (s *stubOracle) Nodes(_ context.Context, limit, offset int) ([]types.OracleNode, error) {
	s.gotLimit, s.gotOffset = limit, offset
	return s.nodes, s.err
}

func (s *stubOracle) Node(_ context.Context, address string) (types.OracleNode, error) {
	s.gotNode = address
	return s.node, s.err
}

// chainStub implements only IdentityCodec — the whole of what the handlers need from a chain.
type chainStub struct{}

func (chainStub) EncodeIdentity(id types.Identity) string         { return id.EVMHex() }
func (chainStub) DecodeIdentity(s string) (types.Identity, error) { return types.IdentityFromEVMHex(s) }

func newTestServer(t *testing.T, stub *stubOracle) http.Handler {
	t.Helper()

	cfg := &config.Config{}
	cfg.API.MaxPageSize = 100
	cfg.API.WriteTimeout = 5 * time.Second
	cfg.Chain.ChainID = testChainID

	h := &handlers{
		oracle:      stub,
		chainClient: chainStub{},
		chainID:     testChainID,
		maxPageSize: cfg.API.MaxPageSize,
		log:         zerolog.New(io.Discard),
	}
	return routes(cfg, h, zerolog.New(io.Discard))
}

func get(t *testing.T, srv http.Handler, path string) (int, map[string]any) {
	t.Helper()

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

	var body map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v\nbody: %s", path, err, rec.Body.String())
		}
	}
	return rec.Code, body
}

func raw(t *testing.T, s string) types.Raw {
	t.Helper()
	v, err := types.ParseRaw(s)
	if err != nil {
		t.Fatalf("parse raw %q: %v", s, err)
	}
	return v
}

// --- scale serialisation ---

// A uint256 must reach the client as a string with its scale beside it. Serialised as a JSON
// number it silently loses precision above 2^53, which is well inside the range of a settled
// 18-decimal price.
func TestRoundSerialisesRawValueAsStringWithScale(t *testing.T) {
	const settled = "3000000000000000000000"

	stub := &stubOracle{round: types.OracleRound{
		ChainID:         testChainID,
		RoundID:         raw(t, "7"),
		FeedID:          testFeedID,
		FeedName:        "ETH/USD",
		State:           "settled",
		AggregatedValue: raw(t, settled),
		Decimals:        18,
		EligibleCount:   5,
		NodeSetVersion:  raw(t, "5"),
		SubmissionCount: 4,
	}}
	srv := newTestServer(t, stub)

	status, body := get(t, srv, "/v1/oracle/rounds/7")
	if status != http.StatusOK {
		t.Fatalf("status %d, body %v", status, body)
	}

	if got := body["aggregatedValue"]; got != settled {
		t.Errorf("aggregatedValue = %v (%T), want the string %q", got, got, settled)
	}
	if got := body["decimals"]; got != float64(18) {
		t.Errorf("decimals = %v, want 18 travelling with the value", got)
	}
}

// The snapshot is what quorum was judged against. Serving the live node set instead would make a
// settled round unauditable.
func TestRoundExposesFrozenSnapshot(t *testing.T) {
	stub := &stubOracle{round: types.OracleRound{
		RoundID:         raw(t, "7"),
		EligibleCount:   6,
		NodeSetVersion:  raw(t, "11"),
		SubmissionCount: 4,
		AggregatedValue: raw(t, "1"),
	}}
	srv := newTestServer(t, stub)

	_, body := get(t, srv, "/v1/oracle/rounds/7")
	if got := body["eligibleCount"]; got != float64(6) {
		t.Errorf("eligibleCount = %v, want the frozen 6", got)
	}
	if got := body["nodeSetVersion"]; got != "11" {
		t.Errorf("nodeSetVersion = %v, want the frozen \"11\"", got)
	}
}

func TestNodeSerialisesStakeWithStakeAssetScale(t *testing.T) {
	stub := &stubOracle{node: types.OracleNode{
		Address:      testNodeAddr,
		StakeAsset:   "0xe7f1725e7734ce288f8367e1bb143e90bb3f0512",
		StakedAmount: raw(t, "10000000000"),
		SlashedTotal: raw(t, "0"),
		Decimals:     6,
		Active:       true,
	}}
	srv := newTestServer(t, stub)

	status, body := get(t, srv, "/v1/oracle/nodes/"+testNodeAddr)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if got := body["stakedAmount"]; got != "10000000000" {
		t.Errorf("stakedAmount = %v, want the raw string", got)
	}
	if got := body["decimals"]; got != float64(6) {
		t.Errorf("decimals = %v, want the stake asset's 6", got)
	}
}

func TestSubmissionsCarryScale(t *testing.T) {
	stub := &stubOracle{submissions: []types.OracleSubmission{{
		RoundID:  raw(t, "7"),
		Node:     testNodeAddr,
		Value:    raw(t, "3000000000000000000000"),
		Decimals: 18,
	}}}
	srv := newTestServer(t, stub)

	_, body := get(t, srv, "/v1/oracle/rounds/7/submissions")
	items := body["items"].([]any)
	first := items[0].(map[string]any)

	if first["value"] != "3000000000000000000000" {
		t.Errorf("value = %v, want a raw string", first["value"])
	}
	if first["decimals"] != float64(18) {
		t.Errorf("decimals = %v", first["decimals"])
	}
}

// --- validation ---

func TestFeedIDValidation(t *testing.T) {
	cases := map[string]int{
		testFeedID: http.StatusOK,
		"0xABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789": http.StatusOK,
		"0xdeadbeef":                 http.StatusBadRequest,
		"not-hex":                    http.StatusBadRequest,
		"0x" + "zz" + testFeedID[4:]: http.StatusBadRequest,
	}

	for input, want := range cases {
		stub := &stubOracle{feed: types.OracleFeed{FeedID: testFeedID}}
		srv := newTestServer(t, stub)

		status, _ := get(t, srv, "/v1/oracle/feeds/"+input)
		if status != want {
			t.Errorf("feed id %q: status %d, want %d", input, status, want)
		}
	}
}

// An uppercase feed id must reach the same row as the lowercase one it was stored as, exactly as
// a checksummed address does.
func TestFeedIDIsCanonicalisedBeforeLookup(t *testing.T) {
	stub := &stubOracle{feed: types.OracleFeed{FeedID: testFeedID}}
	srv := newTestServer(t, stub)

	upper := "0xABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"
	if status, _ := get(t, srv, "/v1/oracle/feeds/"+upper); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if stub.gotFeedID != testFeedID {
		t.Fatalf("looked up %q, want the canonical lowercase form", stub.gotFeedID)
	}
}

func TestRoundIDValidation(t *testing.T) {
	cases := map[string]int{
		"7":    http.StatusOK,
		"0":    http.StatusOK,
		"1e18": http.StatusBadRequest,
		"0x10": http.StatusBadRequest,
		"-1":   http.StatusBadRequest,
		"abc":  http.StatusBadRequest,
	}

	for input, want := range cases {
		stub := &stubOracle{round: types.OracleRound{AggregatedValue: raw(t, "1")}}
		srv := newTestServer(t, stub)

		status, _ := get(t, srv, "/v1/oracle/rounds/"+input)
		if status != want {
			t.Errorf("round id %q: status %d, want %d", input, status, want)
		}
	}
}

func TestNodeAddressValidation(t *testing.T) {
	stub := &stubOracle{node: types.OracleNode{Address: testNodeAddr, StakedAmount: raw(t, "1")}}
	srv := newTestServer(t, stub)

	if status, _ := get(t, srv, "/v1/oracle/nodes/not-an-address"); status != http.StatusBadRequest {
		t.Errorf("malformed address: status %d, want 400", status)
	}
	if status, _ := get(t, srv, "/v1/oracle/nodes/"+testNodeAddr); status != http.StatusOK {
		t.Errorf("valid address: status %d, want 200", status)
	}
}

// --- pagination ---

func TestPaginationIsCappedAtMaxPageSize(t *testing.T) {
	stub := &stubOracle{}
	srv := newTestServer(t, stub)

	if status, _ := get(t, srv, "/v1/oracle/feeds?limit=100000"); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if stub.gotLimit != 100 {
		t.Fatalf("limit reached the store as %d, want it capped at 100", stub.gotLimit)
	}
}

func TestPaginationRejectsNonsense(t *testing.T) {
	for _, query := range []string{"?limit=0", "?limit=-1", "?limit=abc", "?offset=-1", "?offset=x"} {
		srv := newTestServer(t, &stubOracle{})
		if status, _ := get(t, srv, "/v1/oracle/feeds"+query); status != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", query, status)
		}
	}
}

func TestListResponseCarriesPaginationEnvelope(t *testing.T) {
	stub := &stubOracle{feeds: []types.OracleFeed{{FeedID: testFeedID, Name: "ETH/USD", Decimals: 18}}}
	srv := newTestServer(t, stub)

	_, body := get(t, srv, "/v1/oracle/feeds?limit=25&offset=50")
	if body["limit"] != float64(25) || body["offset"] != float64(50) {
		t.Errorf("envelope = %v", body)
	}
	if body["count"] != float64(1) {
		t.Errorf("count = %v, want 1", body["count"])
	}
	if body["chainId"] != float64(testChainID) {
		t.Errorf("chainId = %v", body["chainId"])
	}
}

// --- error mapping ---

// A caller has to be able to tell "this does not exist" from "the database is down"; they retry
// differently.
func TestMissingRowMapsToNotFound(t *testing.T) {
	paths := []string{
		"/v1/oracle/feeds/" + testFeedID,
		"/v1/oracle/rounds/7",
		"/v1/oracle/nodes/" + testNodeAddr,
	}

	for _, path := range paths {
		srv := newTestServer(t, &stubOracle{err: db.ErrNotFound})
		status, body := get(t, srv, path)

		if status != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, status)
		}
		if body["code"] != "NOT_FOUND" {
			t.Errorf("%s: code = %v, want NOT_FOUND", path, body["code"])
		}
	}
}

func TestStoreFailureMapsToInternalError(t *testing.T) {
	srv := newTestServer(t, &stubOracle{err: errors.New("connection refused")})

	status, body := get(t, srv, "/v1/oracle/rounds/7")
	if status != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", status)
	}
	if body["code"] != "INTERNAL" {
		t.Errorf("code = %v, want INTERNAL", body["code"])
	}
}

// An internal error must not leak the underlying failure to the caller. Connection strings and
// query text end up in those messages.
func TestInternalErrorsDoNotLeakDetail(t *testing.T) {
	srv := newTestServer(t, &stubOracle{
		err: errors.New("dial tcp 10.0.1.5:5432: connect: connection refused"),
	})

	_, body := get(t, srv, "/v1/oracle/feeds")
	message, _ := body["error"].(string)

	for _, leaked := range []string{"10.0.1.5", "5432", "dial tcp"} {
		if contains(message, leaked) {
			t.Fatalf("response leaked %q: %s", leaked, message)
		}
	}
}

func TestEmptyListSerialisesAsArrayNotNull(t *testing.T) {
	srv := newTestServer(t, &stubOracle{feeds: []types.OracleFeed{}})

	_, body := get(t, srv, "/v1/oracle/feeds")
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("items = %v (%T), want an array — null breaks clients that iterate", body["items"], body["items"])
	}
	if len(items) != 0 {
		t.Fatalf("items = %v", items)
	}
}

// A round id larger than any native integer must round-trip through the URL and the store.
func TestRoundIDAcceptsFullUint256(t *testing.T) {
	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).String()

	stub := &stubOracle{round: types.OracleRound{AggregatedValue: raw(t, "1")}}
	srv := newTestServer(t, stub)

	if status, _ := get(t, srv, "/v1/oracle/rounds/"+maxUint256); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if stub.gotRound != maxUint256 {
		t.Fatalf("round id reached the store as %s", stub.gotRound)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
