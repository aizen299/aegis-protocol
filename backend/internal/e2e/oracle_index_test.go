//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/api"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/internal/indexer"
	oraclesvc "github.com/aizen299/aegis-protocol/backend/internal/oracle"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/oracle"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// The oracle handlers were written against fakes whose payloads this file did not produce. Real
// logs split fields between topics and data, and a wrong field name or a mistaken assumption about
// how an indexed bytes32 normalises passes every unit test and fails only here. This is the same
// seam that hid three defects in v0.1 until an end-to-end test existed.
type oracleStack struct {
	deployment oracleDeployment
	store      *db.Store
	client     *evm.Client
	indexer    *indexer.Indexer
}

func setupOracleStack(t *testing.T, d oracleDeployment) *oracleStack {
	t.Helper()
	ctx := context.Background()

	cfg := &config.Config{}
	cfg.DB.DSN = envOr("DB_DSN", "postgres://pb:pb_local@localhost:5432/aegis?sslmode=disable")
	cfg.DB.MaxOpenConns = 8
	cfg.DB.MinConns = 1

	store, err := db.New(ctx, cfg)
	if err != nil {
		t.Skipf("postgres unavailable: %v (run `make e2e`)", err)
	}
	t.Cleanup(store.Close)
	truncateOracle(t, cfg.DB.DSN)

	roundsID, err := types.IdentityFromEVMHex(d.OracleRounds)
	if err != nil {
		t.Fatalf("rounds address: %v", err)
	}
	stakingID, err := types.IdentityFromEVMHex(d.OracleStaking)
	if err != nil {
		t.Fatalf("staking address: %v", err)
	}
	stakeID, err := types.IdentityFromEVMHex(d.StakeToken)
	if err != nil {
		t.Fatalf("stake token: %v", err)
	}

	client, err := evm.New(ctx, evm.Options{
		RPCURL:            anvilRPC,
		ChainID:           chainID,
		ConfirmationDepth: 1,
		Contracts: []evm.Registration{
			{Address: roundsID, ABI: oracle.RoundsABI()},
			{Address: stakingID, ABI: oracle.StakingABI()},
		},
	})
	if err != nil {
		t.Fatalf("evm client: %v", err)
	}
	t.Cleanup(client.Close)

	log := zerolog.New(io.Discard)
	idx := indexer.New(client, store, log, indexer.Options{
		ServiceName: "e2e-oracle",
		StartBlock:  d.DeployedAtBlock,
		BatchSize:   500,
	},
		indexer.NewOracleRoundsHandler(store, client, roundsID),
		indexer.NewOracleStakingHandler(store, client, stakingID, stakeID),
	)
	if err := idx.Restore(ctx); err != nil {
		t.Fatalf("restore cursor: %v", err)
	}

	return &oracleStack{deployment: d, store: store, client: client, indexer: idx}
}

func (s *oracleStack) indexToHead(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	mineBlocks(t, 2)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("oracle indexer stalled")
		}
		advanced, err := s.indexer.Step(ctx)
		if err != nil {
			t.Fatalf("oracle indexer step: %v", err)
		}
		if !advanced {
			return
		}
	}
}

// A full round indexed from real logs: feed, node registry, round with its frozen snapshot,
// submissions, and the settled median.
func TestOracleEventsAreIndexedFromRealLogs(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 3)
	s := setupOracleStack(t, d)

	send(t, deployerKey, d.OracleRounds, "openRound(bytes32)", feedIDHex())
	roundID := castCallUint(t, d.OracleRounds, "currentRoundId(bytes32)(uint256)", feedIDHex()).Int64()

	submitSigned(t, d, nodes[0], roundID, ether(3100))
	submitSigned(t, d, nodes[1], roundID, ether(2900))
	submitSigned(t, d, nodes[2], roundID, ether(3000))
	send(t, deployerKey, d.OracleRounds, "settleRound(uint256)", fmt64(roundID))

	s.indexToHead(t)

	assertFeed(t, s, "ETH/USD", 18)
	assertRound(t, s, roundID, "settled", ether(3000), 3, 3)
	assertSubmissionCount(t, s, roundID, 3)
	assertNodeCount(t, s, 3)
}

// The node registry has to reflect a departing node, or an aggregation service would count it
// toward quorum.
func TestOracleUnstakeIsIndexed(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 3)
	s := setupOracleStack(t, d)

	send(t, nodes[0].keyHex, d.OracleStaking, "requestUnstake(uint256)", minStake)
	s.indexToHead(t)

	active := queryInt(t, s, `SELECT count(*) FROM oracle_nodes WHERE chain_id = $1 AND active`, chainID)
	if active != 2 {
		t.Fatalf("active nodes = %d, want 2 after one requested an exit", active)
	}

	pending := queryString(t, s,
		`SELECT pending_unstake::text FROM oracle_nodes WHERE chain_id = $1 AND address = $2`,
		chainID, nodes[0].address)
	if pending != minStake {
		t.Fatalf("pending unstake = %s, want %s", pending, minStake)
	}
}

// A slash writes an audit row and the balance change together.
func TestOracleSlashIsIndexed(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 3)
	s := setupOracleStack(t, d)

	// 10% of stake: exactly the on-chain cap.
	send(t, deployerKey, d.OracleStaking, "slash(address,uint256,uint256,bytes32)",
		nodes[0].address, "7", "1000000000000000000000", reasonHex("OUTLIER_SUBMISSION"))
	s.indexToHead(t)

	reason := queryString(t, s,
		`SELECT reason FROM oracle_slashings WHERE chain_id = $1 AND node_address = $2`,
		chainID, nodes[0].address)
	if reason != "OUTLIER_SUBMISSION" {
		t.Errorf("reason = %q, want the padding stripped", reason)
	}

	stake := queryString(t, s,
		`SELECT staked_amount::text FROM oracle_nodes WHERE chain_id = $1 AND address = $2`,
		chainID, nodes[0].address)
	if stake != "9000000000000000000000" {
		t.Errorf("stake = %s, want 9000e18 after a 10%% slash", stake)
	}

	round := queryString(t, s,
		`SELECT round_id::text FROM oracle_slashings WHERE chain_id = $1 AND node_address = $2`,
		chainID, nodes[0].address)
	if round != "7" {
		t.Errorf("round = %s, want 7 — a penalty must be traceable to what caused it", round)
	}
}

// The executor cannot know whether a transaction it lost track of landed, so a retry must revert
// rather than take the stake twice. Verified against a real chain, not just the unit test.
func TestOracleDoubleSlashIsRejectedOnChain(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 3)
	s := setupOracleStack(t, d)

	send(t, deployerKey, d.OracleStaking, "slash(address,uint256,uint256,bytes32)",
		nodes[0].address, "7", "1000000000000000000000", reasonHex("OUTLIER_SUBMISSION"))

	out, err := castErr(t, "send", "--rpc-url", anvilRPC, "--private-key", deployerKey,
		d.OracleStaking, "slash(address,uint256,uint256,bytes32)",
		nodes[0].address, "7", "1000000000000000000000", reasonHex("OUTLIER_SUBMISSION"))
	if err == nil {
		t.Fatalf("a second slash for the same round succeeded: %s", out)
	}

	s.indexToHead(t)

	stake := queryString(t, s,
		`SELECT staked_amount::text FROM oracle_nodes WHERE chain_id = $1 AND address = $2`,
		chainID, nodes[0].address)
	if stake != "9000000000000000000000" {
		t.Fatalf("stake = %s, want the single 10%% penalty applied once", stake)
	}
}

// A round that misses quorum must be recorded as failed, not left open or silently settled.
func TestOracleFailedRoundIsIndexed(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 6)
	s := setupOracleStack(t, d)

	send(t, deployerKey, d.OracleRounds, "openRound(bytes32)", feedIDHex())
	roundID := castCallUint(t, d.OracleRounds, "currentRoundId(bytes32)(uint256)", feedIDHex()).Int64()
	submitSigned(t, d, nodes[0], roundID, ether(3000))

	cast(t, "rpc", "--rpc-url", anvilRPC, "evm_increaseTime", "400")
	mineBlocks(t, 1)
	send(t, deployerKey, d.OracleRounds, "settleRound(uint256)", fmt64(roundID))

	s.indexToHead(t)
	assertRound(t, s, roundID, "failed", nil, 1, 6)
}

func assertFeed(t *testing.T, s *oracleStack, name string, decimals int) {
	t.Helper()

	gotName := queryString(t, s, `SELECT name FROM oracle_feeds WHERE chain_id = $1`, chainID)
	if gotName != name {
		t.Errorf("feed name = %q, want %q", gotName, name)
	}
	gotDecimals := queryInt(t, s, `SELECT decimals FROM oracle_feeds WHERE chain_id = $1`, chainID)
	if gotDecimals != int64(decimals) {
		t.Errorf("feed decimals = %d, want %d, read from the event", gotDecimals, decimals)
	}
}

func assertRound(t *testing.T, s *oracleStack, roundID int64, state string, value *big.Int, submissions, eligible int) {
	t.Helper()

	gotState := queryString(t, s,
		`SELECT state FROM oracle_rounds WHERE chain_id = $1 AND round_id = $2`, chainID, roundID)
	if gotState != state {
		t.Fatalf("round state = %q, want %q", gotState, state)
	}

	gotEligible := queryInt(t, s,
		`SELECT eligible_count FROM oracle_rounds WHERE chain_id = $1 AND round_id = $2`, chainID, roundID)
	if gotEligible != int64(eligible) {
		t.Errorf("eligible_count = %d, want the snapshot value %d", gotEligible, eligible)
	}

	gotSubmissions := queryInt(t, s,
		`SELECT submission_count FROM oracle_rounds WHERE chain_id = $1 AND round_id = $2`, chainID, roundID)
	if gotSubmissions != int64(submissions) {
		t.Errorf("submission_count = %d, want %d", gotSubmissions, submissions)
	}

	if value == nil {
		return
	}
	gotValue := queryString(t, s,
		`SELECT aggregated_value::text FROM oracle_rounds WHERE chain_id = $1 AND round_id = $2`,
		chainID, roundID)
	if gotValue != value.String() {
		t.Errorf("aggregated_value = %s, want the raw median %s", gotValue, value)
	}
}

func assertSubmissionCount(t *testing.T, s *oracleStack, roundID int64, want int) {
	t.Helper()

	got := queryInt(t, s,
		`SELECT count(*) FROM oracle_submissions WHERE chain_id = $1 AND round_id = $2`, chainID, roundID)
	if got != int64(want) {
		t.Fatalf("indexed %d submissions, want %d", got, want)
	}

	signatures := queryInt(t, s,
		`SELECT count(*) FROM oracle_submissions WHERE chain_id = $1 AND round_id = $2 AND length(signature) = 65`,
		chainID, roundID)
	if signatures != int64(want) {
		t.Errorf("%d submissions carry a 65-byte signature, want %d — the aggregator verifies these",
			signatures, want)
	}
}

func assertNodeCount(t *testing.T, s *oracleStack, want int) {
	t.Helper()

	got := queryInt(t, s, `SELECT count(*) FROM oracle_nodes WHERE chain_id = $1`, chainID)
	if got != int64(want) {
		t.Fatalf("indexed %d nodes, want %d", got, want)
	}
}

// The handler tests drive stubs. This drives the real service over real indexed rows, which is the
// only place a query, its joins, and the JSON shape are checked together.
func TestOracleAPIServesIndexedRows(t *testing.T) {
	requireDeps(t)
	d := deployOracle(t)
	nodes := registerNodes(t, d, 3)
	s := setupOracleStack(t, d)

	send(t, deployerKey, d.OracleRounds, "openRound(bytes32)", feedIDHex())
	roundID := castCallUint(t, d.OracleRounds, "currentRoundId(bytes32)(uint256)", feedIDHex()).Int64()
	for _, node := range nodes {
		submitSigned(t, d, node, roundID, ether(3000))
	}
	send(t, deployerKey, d.OracleRounds, "settleRound(uint256)", fmt64(roundID))
	s.indexToHead(t)

	srv := s.apiHandler(t)

	status, body := apiGet(t, srv, "/v1/oracle/feeds")
	if status != http.StatusOK {
		t.Fatalf("feeds: status %d, body %v", status, body)
	}
	feeds := body["items"].([]any)
	if len(feeds) != 1 {
		t.Fatalf("feeds = %v", feeds)
	}
	if got := feeds[0].(map[string]any)["decimals"]; got != float64(18) {
		t.Errorf("feed decimals = %v, want 18 as registered on chain", got)
	}

	status, body = apiGet(t, srv, fmt.Sprintf("/v1/oracle/rounds/%d", roundID))
	if status != http.StatusOK {
		t.Fatalf("round: status %d, body %v", status, body)
	}
	if got := body["aggregatedValue"]; got != ether(3000).String() {
		t.Errorf("aggregatedValue = %v, want the raw median as a string", got)
	}
	if got := body["eligibleCount"]; got != float64(3) {
		t.Errorf("eligibleCount = %v, want the frozen snapshot", got)
	}
	if got := body["state"]; got != "settled" {
		t.Errorf("state = %v", got)
	}

	status, body = apiGet(t, srv, fmt.Sprintf("/v1/oracle/rounds/%d/submissions", roundID))
	if status != http.StatusOK {
		t.Fatalf("submissions: status %d", status)
	}
	if got := body["count"]; got != float64(3) {
		t.Errorf("submissions count = %v, want 3", got)
	}

	status, body = apiGet(t, srv, "/v1/oracle/nodes/"+nodes[0].address)
	if status != http.StatusOK {
		t.Fatalf("node: status %d, body %v", status, body)
	}
	if got := body["stakedAmount"]; got != minStake {
		t.Errorf("stakedAmount = %v, want the raw stake", got)
	}
	if got := body["decimals"]; got != float64(18) {
		t.Errorf("node decimals = %v, want the stake asset's scale", got)
	}

	if status, _ := apiGet(t, srv, "/v1/oracle/rounds/999999"); status != http.StatusNotFound {
		t.Errorf("unknown round: status %d, want 404", status)
	}
}

// apiHandler builds the real API over the same store the indexer just wrote to. The cache is nil:
// these assertions are about the query and its joins, and a cache hit would hide both.
func (s *oracleStack) apiHandler(t *testing.T) http.Handler {
	t.Helper()

	cfg := &config.Config{}
	cfg.API.MaxPageSize = 100
	cfg.API.WriteTimeout = 30 * time.Second
	cfg.Chain.ChainID = chainID

	log := zerolog.New(io.Discard)
	srv := api.NewServer(cfg, log, api.Deps{
		Store:   s.store,
		Oracle:  oraclesvc.NewService(s.store, nil, log, chainID),
		Chain:   s.client,
		ChainID: chainID,
	})
	return srv.Handler()
}

func apiGet(t *testing.T, srv http.Handler, path string) (int, map[string]any) {
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
