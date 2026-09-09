//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/api"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	governancesvc "github.com/aizen299/aegis-protocol/backend/internal/governance"
	"github.com/aizen299/aegis-protocol/backend/internal/indexer"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/governance"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// The governance handlers were written against fakes whose payloads this file did not produce.
// Real logs split fields between topics and data, and a bytes32 target, a uint256 weight, and a
// string reason each normalise differently coming off the wire than they do in a hand-built map.
// That seam is where every real defect in v0.1 and v0.2 was found.
type governanceStack struct {
	deployment governanceDeployment
	store      *db.Store
	client     *evm.Client
	indexer    *indexer.Indexer
}

func setupGovernanceStack(t *testing.T, d governanceDeployment) *governanceStack {
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
	truncateGovernance(t, cfg.DB.DSN)

	governorID, err := types.IdentityFromEVMHex(d.Governor)
	if err != nil {
		t.Fatalf("governor address: %v", err)
	}

	client, err := evm.New(ctx, evm.Options{
		RPCURL:            anvilRPC,
		ChainID:           chainID,
		ConfirmationDepth: 1,
		Contracts:         []evm.Registration{{Address: governorID, ABI: governance.GovernorABI()}},
	})
	if err != nil {
		t.Fatalf("evm client: %v", err)
	}
	t.Cleanup(client.Close)

	log := zerolog.New(io.Discard)
	idx := indexer.New(client, store, log, indexer.Options{
		ServiceName: "e2e-governance",
		StartBlock:  d.DeployedAtBlock,
		BatchSize:   500,
	},
		indexer.NewGovernanceHandler(store, client, evm.NewGovernanceReader(client), governorID),
	)
	if err := idx.Restore(ctx); err != nil {
		t.Fatalf("restore cursor: %v", err)
	}

	return &governanceStack{deployment: d, store: store, client: client, indexer: idx}
}

func (s *governanceStack) indexToHead(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	mineBlocks(t, 2)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("governance indexer stalled")
		}
		advanced, err := s.indexer.Step(ctx)
		if err != nil {
			t.Fatalf("governance indexer step: %v", err)
		}
		if !advanced {
			return
		}
	}
}

func (s *governanceStack) apiHandler(t *testing.T) http.Handler {
	t.Helper()

	cfg := &config.Config{}
	cfg.API.MaxPageSize = 100
	cfg.API.WriteTimeout = 30 * time.Second
	cfg.Chain.ChainID = chainID

	log := zerolog.New(io.Discard)
	srv := api.NewServer(cfg, log, api.Deps{
		Store:      s.store,
		Governance: governancesvc.NewService(s.store, nil, log, chainID),
		Chain:      s.client,
		ChainID:    chainID,
	})
	return srv.Handler()
}

func truncateGovernance(t *testing.T, dsn string) {
	t.Helper()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect for truncate: %v", err)
	}
	defer conn.Close(ctx)

	const q = `TRUNCATE governance_votes, governance_proposals, governors, vaults, assets,
	           indexer_cursors RESTART IDENTITY CASCADE`
	if _, err := conn.Exec(ctx, q); err != nil {
		t.Fatalf("truncate governance tables: %v", err)
	}
}

func governanceQueryString(t *testing.T, s *governanceStack, sql string, args ...any) string {
	t.Helper()
	var out string
	if err := s.store.QueryRowForTest(context.Background(), sql, args...).Scan(&out); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return out
}

func governanceQueryInt(t *testing.T, s *governanceStack, sql string, args ...any) int64 {
	t.Helper()
	var out int64
	if err := s.store.QueryRowForTest(context.Background(), sql, args...).Scan(&out); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return out
}

// proposeAndPass drives a local proposal to executed on chain, so the indexer sees the whole
// lifecycle in real logs.
func (s *governanceStack) proposeAndPass(t *testing.T, newValue int, remote bool) string {
	t.Helper()
	d := s.deployment

	send(t, deployerKey, d.AegisToken, "delegate(address)", deployerAddr)

	targetChain := chainID
	if remote {
		targetChain = chainID + 1
	}
	payload := cast(t, "calldata", "setValue(uint256)", fmt.Sprint(newValue))

	send(t, deployerKey, d.Governor,
		"propose((uint256,bytes32,uint256,bytes),string,string)",
		action(targetChain, d.GovernedTarget, "0", payload),
		"Set the value", "end to end through the indexer")

	proposalID := "1"
	advanceTime(t, votingDelaySeconds+1)
	send(t, deployerKey, d.Governor, "castVote(uint256,uint8,string)", proposalID, "1", "aye")
	advanceTime(t, votingPeriodSeconds+1)
	send(t, deployerKey, d.Governor, "queue(uint256)", proposalID)

	return proposalID
}

// The whole lifecycle out of real logs: the proposal with its full action, the vote with its raw
// weight, the queue with its timelock operation, and the execution.
func TestGovernanceLifecycleIsIndexedFromRealLogs(t *testing.T) {
	requireDeps(t)
	d := deployGovernance(t)
	s := setupGovernanceStack(t, d)

	proposalID := s.proposeAndPass(t, 42, false)
	advanceTime(t, timelockSeconds+1)
	send(t, deployerKey, d.Governor, "execute(uint256)", proposalID)

	s.indexToHead(t)

	state := governanceQueryString(t, s,
		`SELECT state FROM governance_proposals WHERE chain_id = $1 AND proposal_id = $2`,
		chainID, proposalID)
	if state != types.ProposalStateExecuted {
		t.Fatalf("state = %s, want executed", state)
	}

	title := governanceQueryString(t, s,
		`SELECT title FROM governance_proposals WHERE chain_id = $1 AND proposal_id = $2`,
		chainID, proposalID)
	if title != "Set the value" {
		t.Errorf("title = %q — the event's string fields did not survive decoding", title)
	}

	// The operation id came from the event, not a contract read.
	operationID := governanceQueryString(t, s,
		`SELECT operation_id::text FROM governance_proposals WHERE chain_id = $1 AND proposal_id = $2`,
		chainID, proposalID)
	if operationID != "1" {
		t.Errorf("operation id = %s, want 1", operationID)
	}

	votes := governanceQueryInt(t, s,
		`SELECT count(*) FROM governance_votes WHERE chain_id = $1 AND proposal_id = $2`,
		chainID, proposalID)
	if votes != 1 {
		t.Fatalf("votes = %d, want 1", votes)
	}

	// The tally is derived from the votes table, and the weight is the raw uint256 supply.
	const fullSupply = "100000000000000000000000000"
	tally := governanceQueryString(t, s,
		`SELECT votes_for::text FROM governance_proposals WHERE chain_id = $1 AND proposal_id = $2`,
		chainID, proposalID)
	if tally != fullSupply {
		t.Errorf("votes_for = %s, want the raw weight %s", tally, fullSupply)
	}
}

// The vote token has to be discovered from the governor and recorded, or a weight has no scale.
func TestGovernorAndVoteTokenAreIndexed(t *testing.T) {
	requireDeps(t)
	d := deployGovernance(t)
	s := setupGovernanceStack(t, d)

	s.proposeAndPass(t, 7, false)
	s.indexToHead(t)

	token := governanceQueryString(t, s,
		`SELECT token_address FROM governors WHERE chain_id = $1 AND address = $2`,
		chainID, d.Governor)
	if token != d.AegisToken {
		t.Fatalf("vote token = %s, want %s", token, d.AegisToken)
	}

	timelock := governanceQueryString(t, s,
		`SELECT timelock_address FROM governors WHERE chain_id = $1 AND address = $2`,
		chainID, d.Governor)
	if timelock != d.Timelock {
		t.Errorf("timelock = %s, want %s", timelock, d.Timelock)
	}

	decimals := governanceQueryInt(t, s,
		`SELECT decimals FROM assets WHERE chain_id = $1 AND address = $2`, chainID, d.AegisToken)
	if decimals != 18 {
		t.Errorf("vote token decimals = %d, want 18 read from the token", decimals)
	}
}

// A remote proposal must survive indexing with its destination intact and its 32-byte target
// untruncated. Narrowing it to an address here would lose exactly what §7 requires be kept.
func TestRemoteProposalKeepsItsDestinationThroughIndexing(t *testing.T) {
	requireDeps(t)
	d := deployGovernance(t)
	s := setupGovernanceStack(t, d)

	proposalID := s.proposeAndPass(t, 99, true)
	s.indexToHead(t)

	targetChain := governanceQueryInt(t, s,
		`SELECT target_chain_id FROM governance_proposals WHERE chain_id = $1 AND proposal_id = $2`,
		chainID, proposalID)
	if targetChain != chainID+1 {
		t.Fatalf("target chain = %d, want %d", targetChain, chainID+1)
	}

	target := governanceQueryString(t, s,
		`SELECT target FROM governance_proposals WHERE chain_id = $1 AND proposal_id = $2`,
		chainID, proposalID)
	if len(target) != 66 {
		t.Errorf("target = %s, want a 32-byte value, not a narrowed address", target)
	}
	if target != addressAsBytes32(d.GovernedTarget) {
		t.Errorf("target = %s, want %s", target, addressAsBytes32(d.GovernedTarget))
	}
}

// A cancelled proposal must read as cancelled, not linger as queued in the API an operator watches.
func TestCancelledProposalIsIndexedAsCancelled(t *testing.T) {
	requireDeps(t)
	d := deployGovernance(t)
	s := setupGovernanceStack(t, d)

	proposalID := s.proposeAndPass(t, 7, false)
	send(t, deployerKey, d.Governor, "cancel(uint256)", proposalID)
	s.indexToHead(t)

	state := governanceQueryString(t, s,
		`SELECT state FROM governance_proposals WHERE chain_id = $1 AND proposal_id = $2`,
		chainID, proposalID)
	if state != types.ProposalStateCancelled {
		t.Fatalf("state = %s, want cancelled", state)
	}
}

// Reprocessing the same logs must not duplicate a vote or double the tally. The tally is a sum over
// an idempotent table for exactly this reason.
func TestGovernanceReindexingIsIdempotent(t *testing.T) {
	requireDeps(t)
	d := deployGovernance(t)
	s := setupGovernanceStack(t, d)

	proposalID := s.proposeAndPass(t, 42, false)
	s.indexToHead(t)

	before := governanceQueryString(t, s,
		`SELECT votes_for::text FROM governance_proposals WHERE chain_id = $1 AND proposal_id = $2`,
		chainID, proposalID)

	// Rewind the cursor and replay every log the indexer has already seen. The cursor is asserted
	// on both sides: a rewind that silently failed would make this test prove nothing at all.
	head := s.indexer.Cursor()
	if head <= d.DeployedAtBlock {
		t.Fatalf("cursor never advanced past deployment (%d), so there is nothing to replay", head)
	}

	if err := s.store.RewindCursorForTest(context.Background(), "e2e-governance", chainID, d.DeployedAtBlock); err != nil {
		t.Fatalf("rewind cursor: %v", err)
	}
	if err := s.indexer.Restore(context.Background()); err != nil {
		t.Fatalf("restore cursor: %v", err)
	}
	if got := s.indexer.Cursor(); got != d.DeployedAtBlock {
		t.Fatalf("cursor = %d after the rewind, want %d — the replay never happened", got, d.DeployedAtBlock)
	}

	s.indexToHead(t)

	if got := s.indexer.Cursor(); got < head {
		t.Fatalf("cursor = %d after the replay, want it back at or past %d", got, head)
	}

	votes := governanceQueryInt(t, s,
		`SELECT count(*) FROM governance_votes WHERE chain_id = $1 AND proposal_id = $2`,
		chainID, proposalID)
	if votes != 1 {
		t.Fatalf("votes = %d after a replay, want 1", votes)
	}

	after := governanceQueryString(t, s,
		`SELECT votes_for::text FROM governance_proposals WHERE chain_id = $1 AND proposal_id = $2`,
		chainID, proposalID)
	if after != before {
		t.Fatalf("tally moved on replay: %s -> %s", before, after)
	}
}

// The API over real indexed rows: raw values as strings, every weight with its scale, and the
// queued view an operator relies on to see what is about to execute.
func TestGovernanceAPIServesIndexedRows(t *testing.T) {
	requireDeps(t)
	d := deployGovernance(t)
	s := setupGovernanceStack(t, d)

	proposalID := s.proposeAndPass(t, 42, false)
	s.indexToHead(t)

	srv := s.apiHandler(t)

	status, body := apiGet(t, srv, "/v1/governance/governor")
	if status != http.StatusOK {
		t.Fatalf("governor: status %d, body %v", status, body)
	}
	if got := body["tokenDecimals"]; got != float64(18) {
		t.Errorf("tokenDecimals = %v, want 18 read from the token", got)
	}
	if got := body["timelock"]; got != d.Timelock {
		t.Errorf("timelock = %v, want %s", got, d.Timelock)
	}

	status, body = apiGet(t, srv, "/v1/governance/proposals/"+proposalID)
	if status != http.StatusOK {
		t.Fatalf("proposal: status %d, body %v", status, body)
	}
	if got := body["state"]; got != types.ProposalStateQueued {
		t.Errorf("state = %v, want queued", got)
	}

	const fullSupply = "100000000000000000000000000"
	if got := body["votesFor"]; got != fullSupply {
		t.Errorf("votesFor = %v, want the raw weight as a string %s", got, fullSupply)
	}
	if got := body["voteDecimals"]; got != float64(18) {
		t.Errorf("voteDecimals = %v, want the vote token's scale", got)
	}
	if body["executableAt"] == nil {
		t.Error("executableAt is absent — an operator cannot tell when this executes")
	}
	if got := body["operationId"]; got != "1" {
		t.Errorf("operationId = %v, want 1", got)
	}

	// The queued view is the reason the timelock is observable at all.
	status, body = apiGet(t, srv, "/v1/governance/proposals?state=queued")
	if status != http.StatusOK {
		t.Fatalf("queued proposals: status %d", status)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("queued items = %#v", body["items"])
	}

	status, body = apiGet(t, srv, "/v1/governance/proposals/"+proposalID+"/votes")
	if status != http.StatusOK {
		t.Fatalf("votes: status %d", status)
	}
	items, ok = body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("vote items = %#v", body["items"])
	}
	vote := items[0].(map[string]any)
	if got := vote["weight"]; got != fullSupply {
		t.Errorf("weight = %v, want the raw uint256 as a string", got)
	}
	if got := vote["reason"]; got != "aye" {
		t.Errorf("reason = %v, want the string from the log", got)
	}

	status, body = apiGet(t, srv, "/v1/governance/voters/"+deployerAddr+"/votes")
	if status != http.StatusOK {
		t.Fatalf("voter votes: status %d", status)
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 1 {
		t.Fatalf("voter items = %#v", body["items"])
	}
}
