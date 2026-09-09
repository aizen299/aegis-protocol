package api

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const testVoterAddr = "0x70997970c51812dc3a010c7d01b50e0d17dc79c8"

type stubGovernance struct {
	proposals []types.Proposal
	proposal  types.Proposal
	votes     []types.Vote
	governor  types.GovernorMetadata
	err       error

	gotState    string
	gotLimit    int
	gotOffset   int
	gotProposal string
	gotVoter    string
}

func (s *stubGovernance) Proposals(_ context.Context, state string, limit, offset int) ([]types.Proposal, error) {
	s.gotState, s.gotLimit, s.gotOffset = state, limit, offset
	return s.proposals, s.err
}

func (s *stubGovernance) Proposal(_ context.Context, proposalID types.Raw) (types.Proposal, error) {
	s.gotProposal = proposalID.String()
	return s.proposal, s.err
}

func (s *stubGovernance) ProposalVotes(_ context.Context, proposalID types.Raw, limit, offset int) ([]types.Vote, error) {
	s.gotProposal, s.gotLimit, s.gotOffset = proposalID.String(), limit, offset
	return s.votes, s.err
}

func (s *stubGovernance) VotesByVoter(_ context.Context, voter string, limit, offset int) ([]types.Vote, error) {
	s.gotVoter, s.gotLimit, s.gotOffset = voter, limit, offset
	return s.votes, s.err
}

func (s *stubGovernance) Governor(context.Context) (types.GovernorMetadata, error) {
	return s.governor, s.err
}

func newGovernanceServer(t *testing.T, stub *stubGovernance) http.Handler {
	t.Helper()

	cfg := &config.Config{}
	cfg.API.MaxPageSize = 100
	cfg.API.WriteTimeout = 5 * time.Second
	cfg.Chain.ChainID = testChainID

	h := &handlers{
		governance:  stub,
		chainClient: chainStub{},
		chainID:     testChainID,
		maxPageSize: cfg.API.MaxPageSize,
		log:         zerolog.New(io.Discard),
	}
	return routes(cfg, h, zerolog.New(io.Discard))
}

// --- scale serialisation ---

// A vote weight is a uint256. Serialised as a JSON number it silently loses precision above 2^53,
// which a governance token's supply exceeds by many orders of magnitude, and the decimals that make
// it readable must travel with it.
func TestVoteWeightIsAStringWithItsScale(t *testing.T) {
	const weight = "12345678901234567890123456"

	stub := &stubGovernance{votes: []types.Vote{{
		ChainID:      testChainID,
		ProposalID:   raw(t, "1"),
		Voter:        testVoterAddr,
		Support:      types.SupportFor,
		Weight:       raw(t, weight),
		VoteDecimals: 18,
		VotedAt:      time.Unix(1735689600, 0).UTC(),
	}}}

	code, body := get(t, newGovernanceServer(t, stub), "/v1/governance/proposals/1/votes")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}

	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", body["items"])
	}
	vote := items[0].(map[string]any)

	got, ok := vote["weight"].(string)
	if !ok {
		t.Fatalf("weight is %T, want string — a uint256 must not be a JSON number", vote["weight"])
	}
	if got != weight {
		t.Errorf("weight = %s, want %s", got, weight)
	}
	if vote["voteDecimals"] != float64(18) {
		t.Errorf("voteDecimals = %v, want 18", vote["voteDecimals"])
	}
}

// Tallies are uint256 sums and carry the same requirement.
func TestProposalTalliesAreStringsWithTheirScale(t *testing.T) {
	stub := &stubGovernance{proposal: types.Proposal{
		ChainID:      testChainID,
		ProposalID:   raw(t, "1"),
		State:        types.ProposalStateActive,
		VotesFor:     raw(t, "99999999999999999999999999"),
		VotesAgainst: raw(t, "1"),
		VotesAbstain: raw(t, "0"),
		VoteDecimals: 18,
	}}

	code, body := get(t, newGovernanceServer(t, stub), "/v1/governance/proposals/1")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}

	for _, field := range []string{"votesFor", "votesAgainst", "votesAbstain"} {
		if _, ok := body[field].(string); !ok {
			t.Errorf("%s is %T, want string", field, body[field])
		}
	}
	if body["voteDecimals"] != float64(18) {
		t.Errorf("voteDecimals = %v, want 18", body["voteDecimals"])
	}
}

// The action's target is 32 bytes and its destination chain is separate from the chain the proposal
// lives on. Both must survive to the client, or a remote proposal is indistinguishable from a local
// one.
func TestProposalActionCarriesTargetChainAndFullTarget(t *testing.T) {
	const target = "0xffeedd0000000000000000000000000000000000000000000000000000000000"

	stub := &stubGovernance{proposal: types.Proposal{
		ChainID:    testChainID,
		ProposalID: raw(t, "1"),
		State:      types.ProposalStateQueued,
		Action: types.ProposalAction{
			TargetChainID: testChainID + 1,
			Target:        target,
			Value:         raw(t, "0"),
			Calldata:      "0xdead",
		},
		VotesFor:     raw(t, "0"),
		VotesAgainst: raw(t, "0"),
		VotesAbstain: raw(t, "0"),
	}}

	code, body := get(t, newGovernanceServer(t, stub), "/v1/governance/proposals/1")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}

	action, ok := body["action"].(map[string]any)
	if !ok {
		t.Fatalf("action = %#v", body["action"])
	}
	if action["target"] != target {
		t.Errorf("target = %v, want the full 32 bytes %s", action["target"], target)
	}
	if action["targetChainId"] != float64(testChainID+1) {
		t.Errorf("targetChainId = %v, want %d", action["targetChainId"], testChainID+1)
	}
}

// --- validation ---

func TestUnknownProposalStateIsRejected(t *testing.T) {
	stub := &stubGovernance{}
	code, body := get(t, newGovernanceServer(t, stub), "/v1/governance/proposals?state=nonsense")

	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if body["code"] != "INVALID_STATE" {
		t.Errorf("code = %v", body["code"])
	}
	// A typo that quietly widens a filter is worse than an error.
	if stub.gotState != "" {
		t.Errorf("the store was queried with %q despite the rejection", stub.gotState)
	}
}

func TestKnownProposalStateReachesTheStore(t *testing.T) {
	stub := &stubGovernance{}
	code, _ := get(t, newGovernanceServer(t, stub), "/v1/governance/proposals?state=queued")

	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if stub.gotState != types.ProposalStateQueued {
		t.Errorf("state = %q, want queued", stub.gotState)
	}
}

func TestNonNumericProposalIDIsRejected(t *testing.T) {
	stub := &stubGovernance{}
	code, body := get(t, newGovernanceServer(t, stub), "/v1/governance/proposals/abc")

	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if body["code"] != "INVALID_PROPOSAL_ID" {
		t.Errorf("code = %v", body["code"])
	}
}

func TestMalformedVoterAddressIsRejected(t *testing.T) {
	stub := &stubGovernance{}
	code, body := get(t, newGovernanceServer(t, stub), "/v1/governance/voters/not-an-address/votes")

	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if body["code"] != "INVALID_ADDRESS" {
		t.Errorf("code = %v", body["code"])
	}
}

func TestVoterAddressIsCanonicalisedBeforeTheQuery(t *testing.T) {
	stub := &stubGovernance{}
	mixedCase := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"

	if code, _ := get(t, newGovernanceServer(t, stub), "/v1/governance/voters/"+mixedCase+"/votes"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if stub.gotVoter != testVoterAddr {
		t.Errorf("voter = %s, want the canonical lowercase form %s", stub.gotVoter, testVoterAddr)
	}
}

// --- error mapping ---

func TestMissingProposalIsNotFound(t *testing.T) {
	stub := &stubGovernance{err: db.ErrNotFound}
	code, _ := get(t, newGovernanceServer(t, stub), "/v1/governance/proposals/42")

	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestMissingGovernorIsNotFound(t *testing.T) {
	stub := &stubGovernance{err: db.ErrNotFound}
	code, _ := get(t, newGovernanceServer(t, stub), "/v1/governance/governor")

	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestGovernorMetadataIsServed(t *testing.T) {
	stub := &stubGovernance{governor: types.GovernorMetadata{
		ChainID:         testChainID,
		Address:         "0x8a791620dd6260079bf849dc5567adc3f2fdc318",
		TokenAddress:    "0xa513e6e4b8f2a923d98304ec87f64353c4d5c853",
		TimelockAddress: "0x2279b7a0a67db372996a5fab50d91eaa73d2ebe6",
		TokenDecimals:   18,
	}}

	code, body := get(t, newGovernanceServer(t, stub), "/v1/governance/governor")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body["tokenDecimals"] != float64(18) {
		t.Errorf("tokenDecimals = %v, want 18", body["tokenDecimals"])
	}
	if body["timelock"] != stub.governor.TimelockAddress {
		t.Errorf("timelock = %v", body["timelock"])
	}
}

// An oversized limit is clamped rather than rejected, which is the behaviour every other endpoint
// already has. A limit that cannot mean anything is refused.
func TestGovernancePaginationIsClampedAndValidated(t *testing.T) {
	stub := &stubGovernance{}
	srv := newGovernanceServer(t, stub)

	if code, _ := get(t, srv, "/v1/governance/proposals?limit=99999"); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if stub.gotLimit != 100 {
		t.Errorf("limit = %d, want it clamped to the configured maximum of 100", stub.gotLimit)
	}

	for _, bad := range []string{"limit=0", "limit=-1", "limit=abc", "offset=-1"} {
		code, body := get(t, srv, "/v1/governance/proposals?"+bad)
		if code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", bad, code)
			continue
		}
		if body["code"] != "INVALID_PAGINATION" {
			t.Errorf("%s: code = %v", bad, body["code"])
		}
	}
}
