package api

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/chain/svm"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const solanaNode = "8FUGbGRf4CSGZotCfE5krXSosJKaXPBVhLy8Lw78cQx6"

func twoChainServer(t *testing.T, anvil, solana *stubOracle) http.Handler {
	t.Helper()
	cfg := &config.Config{}
	cfg.API.MaxPageSize = 100
	cfg.API.WriteTimeout = 5 * time.Second
	chains := []ChainDeps{
		{ID: types.ChainIDAnvil, Codec: chainStub{}, Oracle: anvil},
		{ID: types.ChainIDSolanaLocalnet, Codec: svm.Codec{}},
	}
	if solana != nil {
		chains[1].Oracle = solana
	}
	h := &handlers{chains: chainMap(chains), maxPageSize: 100, log: zerolog.New(io.Discard)}
	return routes(cfg, h, zerolog.New(io.Discard))
}

// With more than one chain, a request that names none has no single right answer.
func TestSeveralChainsRequireTheRequestToNameOne(t *testing.T) {
	srv := twoChainServer(t, &stubOracle{}, &stubOracle{})
	status, body := get(t, srv, "/v1/oracle/nodes")
	if status != http.StatusBadRequest || body["code"] != "CHAIN_REQUIRED" {
		t.Fatalf("status = %d, body = %v", status, body)
	}
}

func TestAChainIsSelectedOnlyByAServedRegistryName(t *testing.T) {
	srv := twoChainServer(t, &stubOracle{}, &stubOracle{})
	cases := []struct {
		query  string
		status int
		code   string
	}{
		{"?chain=nonexistent", http.StatusBadRequest, "UNKNOWN_CHAIN"},
		{"?chain=31337", http.StatusBadRequest, "UNKNOWN_CHAIN"},
		{"?chain=arbitrum-one", http.StatusNotFound, "CHAIN_NOT_SERVED"},
		{"?chain=anvil&chain=solana-localnet", http.StatusBadRequest, "INVALID_CHAIN"},
	}
	for _, tc := range cases {
		status, body := get(t, srv, "/v1/oracle/nodes"+tc.query)
		if status != tc.status || body["code"] != tc.code {
			t.Errorf("%s: status = %d, code = %v; want %d %s", tc.query, status, body["code"], tc.status, tc.code)
		}
	}
}

// An address is valid only in its own chain's encoding, and the lookup goes to that chain's service.
func TestEachChainUsesItsOwnEncodingAndServices(t *testing.T) {
	anvil, solana := &stubOracle{}, &stubOracle{}
	srv := twoChainServer(t, anvil, solana)

	if status, _ := get(t, srv, "/v1/oracle/nodes/"+solanaNode+"?chain=solana-localnet"); status != http.StatusOK {
		t.Fatalf("solana address on solana: status %d", status)
	}
	if solana.gotNode != solanaNode || anvil.gotNode != "" {
		t.Fatalf("solana service got %q, anvil service got %q", solana.gotNode, anvil.gotNode)
	}

	if status, body := get(t, srv, "/v1/oracle/nodes/"+solanaNode+"?chain=anvil"); status != http.StatusBadRequest || body["code"] != "INVALID_ADDRESS" {
		t.Fatalf("solana address on anvil: status %d, body %v", status, body)
	}
	if status, _ := get(t, srv, "/v1/oracle/nodes/"+testNodeAddr+"?chain=anvil"); status != http.StatusOK || anvil.gotNode != testNodeAddr {
		t.Fatalf("anvil address on anvil: status %d, got %q", status, anvil.gotNode)
	}
}

// A module a chain does not have is absent, not empty: an empty oracle list would claim a
// deployment that nothing indexes.
func TestAModuleAChainLacksAnswersNotFound(t *testing.T) {
	srv := twoChainServer(t, &stubOracle{}, nil)
	status, body := get(t, srv, "/v1/oracle/nodes?chain=solana-localnet")
	if status != http.StatusNotFound || body["code"] != "MODULE_NOT_ON_CHAIN" {
		t.Fatalf("status = %d, body = %v", status, body)
	}
}

func TestASingleChainNeedsNoParameter(t *testing.T) {
	status, _ := get(t, newTestServer(t, &stubOracle{}), "/v1/oracle/nodes")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
}
