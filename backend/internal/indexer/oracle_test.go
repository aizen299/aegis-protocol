package indexer

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	roundsHex = "0x610178da211fef7d417bc0e6fed39f05609ad788"
	nodeHex   = "0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc"
	stakeHex  = "0xe7f1725e7734ce288f8367e1bb143e90bb3f0512"
)

var feedID = [32]byte{0xab, 0xcd}

type fakeOracleStore struct {
	feeds        []db.Feed
	ensured      []string
	deactivated  []string
	rounds       []db.Round
	submissions  []db.Submission
	settled      map[string]types.Raw
	failed       []string
	quorumMet    []string
	nodes        []db.NodeStakeEvent
	slashes      []db.Slash
	assets       []types.AssetMetadata
	stakeUpdates map[string]types.Raw
	activeFlags  map[string]bool
	unstakes     map[string]types.Raw
}

func newFakeOracleStore() *fakeOracleStore {
	return &fakeOracleStore{
		settled:      map[string]types.Raw{},
		stakeUpdates: map[string]types.Raw{},
		activeFlags:  map[string]bool{},
		unstakes:     map[string]types.Raw{},
	}
}

func (f *fakeOracleStore) UpsertOracleFeed(_ context.Context, feed db.Feed) error {
	f.feeds = append(f.feeds, feed)
	return nil
}

func (f *fakeOracleStore) EnsureOracleFeed(_ context.Context, _ int64, feedID string, _ uint8) error {
	f.ensured = append(f.ensured, feedID)
	return nil
}

func (f *fakeOracleStore) DeactivateOracleFeed(_ context.Context, _ int64, feedID string) error {
	f.deactivated = append(f.deactivated, feedID)
	return nil
}

func (f *fakeOracleStore) InsertOracleRound(_ context.Context, r db.Round) error {
	f.rounds = append(f.rounds, r)
	return nil
}

func (f *fakeOracleStore) MarkOracleRoundQuorumMet(_ context.Context, _ int64, roundID types.Raw, _ int32) error {
	f.quorumMet = append(f.quorumMet, roundID.String())
	return nil
}

func (f *fakeOracleStore) SettleOracleRound(_ context.Context, _ int64, roundID, value types.Raw, _ int32, _ time.Time) error {
	f.settled[roundID.String()] = value
	return nil
}

func (f *fakeOracleStore) FailOracleRound(_ context.Context, _ int64, roundID types.Raw, _ int32, _ time.Time) error {
	f.failed = append(f.failed, roundID.String())
	return nil
}

func (f *fakeOracleStore) InsertOracleSubmission(_ context.Context, sub db.Submission) error {
	f.submissions = append(f.submissions, sub)
	return nil
}

func (f *fakeOracleStore) UpsertAsset(_ context.Context, a types.AssetMetadata) error {
	f.assets = append(f.assets, a)
	return nil
}

func (f *fakeOracleStore) UpsertOracleNode(_ context.Context, e db.NodeStakeEvent) error {
	f.nodes = append(f.nodes, e)
	return nil
}

func (f *fakeOracleStore) SetOracleNodeStake(_ context.Context, _ int64, node string, stake types.Raw) error {
	f.stakeUpdates[node] = stake
	return nil
}

func (f *fakeOracleStore) SetOracleNodeUnstake(_ context.Context, _ int64, node string, pending types.Raw, _ *time.Time, active bool) error {
	f.unstakes[node] = pending
	f.activeFlags[node] = active
	return nil
}

func (f *fakeOracleStore) SetOracleNodeActive(_ context.Context, _ int64, node string, active bool) error {
	f.activeFlags[node] = active
	return nil
}

func (f *fakeOracleStore) RecordOracleSlash(_ context.Context, sl db.Slash) error {
	f.slashes = append(f.slashes, sl)
	return nil
}

func oracleEvent(t *testing.T, contract string, name string, payload map[string]any) chain.Event {
	t.Helper()
	return chain.Event{
		ChainID:     testChainID,
		BlockNumber: 100,
		BlockTime:   1735689600,
		TxHash:      [32]byte{0x01, 0x02},
		LogIndex:    7,
		Contract:    mustID(t, contract),
		Name:        name,
		Payload:     payload,
	}
}

func newRoundsHandler(t *testing.T, store OracleRoundStore) *OracleRoundsHandler {
	t.Helper()
	return &OracleRoundsHandler{
		contract: mustID(t, roundsHex),
		encode:   func(id types.Identity) string { return id.EVMHex() },
		store:    store,
		chainID:  testChainID,
	}
}

// A settled value can be any uint256 the contract produced. Nothing may rescale it on the way in.
func TestOracleSettlementPreservesRawValue(t *testing.T) {
	store := newFakeOracleStore()
	h := newRoundsHandler(t, store)

	const huge = "115792089237316195423570985008687907853269984665640564039457584007913129639935"
	ev := oracleEvent(t, roundsHex, eventRoundSettled, map[string]any{
		"roundId":         big.NewInt(7),
		"feedId":          feedID,
		"aggregatedValue": mustBigInt(t, huge),
		"submissionCount": big.NewInt(3),
	})

	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got := store.settled["7"].String(); got != huge {
		t.Fatalf("aggregated value = %s, want the full uint256 unmodified", got)
	}
}

// A round references a feed by foreign key. An indexer started after the feed was registered must
// still be able to record the round rather than stalling on a row it cannot attribute.
func TestRoundStartedEnsuresFeedExists(t *testing.T) {
	store := newFakeOracleStore()
	h := newRoundsHandler(t, store)

	ev := oracleEvent(t, roundsHex, eventRoundStarted, map[string]any{
		"roundId":        big.NewInt(1),
		"feedId":         feedID,
		"openedAt":       big.NewInt(1735689600),
		"deadline":       big.NewInt(1735689900),
		"eligibleCount":  big.NewInt(5),
		"nodeSetVersion": big.NewInt(5),
	})

	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(store.ensured) != 1 {
		t.Fatalf("feed was not ensured before the round: %v", store.ensured)
	}
	if len(store.rounds) != 1 {
		t.Fatal("round was not recorded")
	}

	round := store.rounds[0]
	if round.EligibleCount != 5 || round.NodeSetVersion.String() != "5" {
		t.Errorf("snapshot not recorded: eligible=%d version=%s", round.EligibleCount, round.NodeSetVersion)
	}
	if round.OpenedAt.Unix() != 1735689600 || round.Deadline.Unix() != 1735689900 {
		t.Errorf("round window wrong: %v..%v", round.OpenedAt, round.Deadline)
	}
	if round.LogIndex != 7 {
		t.Errorf("log_index = %d, want 7", round.LogIndex)
	}
}

func TestFeedRegisteredRecordsNameAndScale(t *testing.T) {
	store := newFakeOracleStore()
	h := newRoundsHandler(t, store)

	ev := oracleEvent(t, roundsHex, eventFeedRegistered, map[string]any{
		"feedId":   feedID,
		"name":     "ETH/USD",
		"decimals": uint8(18),
	})
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(store.feeds) != 1 {
		t.Fatal("feed not recorded")
	}
	if store.feeds[0].Name != "ETH/USD" || store.feeds[0].Decimals != 18 {
		t.Fatalf("feed = %+v", store.feeds[0])
	}
}

// The scale comes from the event. Hardcoding 18 in Go was correct for every feed built so far and
// silently wrong for the first one that is not — the same mistake as the hardcoded token decimals.
func TestFeedScaleComesFromTheEventNotAConstant(t *testing.T) {
	store := newFakeOracleStore()
	h := newRoundsHandler(t, store)

	ev := oracleEvent(t, roundsHex, eventFeedRegistered, map[string]any{
		"feedId":   feedID,
		"name":     "SOFR",
		"decimals": uint8(8),
	})
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if store.feeds[0].Decimals != 8 {
		t.Fatalf("decimals = %d, want 8 as declared on chain", store.feeds[0].Decimals)
	}
}

func TestFeedRegisteredRejectsMissingDecimals(t *testing.T) {
	store := newFakeOracleStore()
	h := newRoundsHandler(t, store)

	ev := oracleEvent(t, roundsHex, eventFeedRegistered, map[string]any{
		"feedId": feedID,
		"name":   "ETH/USD",
	})
	if err := h.Handle(context.Background(), ev); err == nil {
		t.Fatal("expected an error rather than a defaulted scale")
	}
}

func TestSubmissionRecordsRawValueAndSignature(t *testing.T) {
	store := newFakeOracleStore()
	h := newRoundsHandler(t, store)

	signature := []byte{0xde, 0xad, 0xbe, 0xef}
	ev := oracleEvent(t, roundsHex, eventSubmission, map[string]any{
		"roundId":         big.NewInt(3),
		"node":            mustID(t, nodeHex),
		"value":           mustBigInt(t, "3000000000000000000000"),
		"submissionCount": big.NewInt(1),
		"signature":       signature,
	})

	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	sub := store.submissions[0]
	if sub.Value.String() != "3000000000000000000000" {
		t.Errorf("value = %s, want the raw submission", sub.Value)
	}
	if sub.Node != nodeHex {
		t.Errorf("node = %s", sub.Node)
	}
	if string(sub.Signature) != string(signature) {
		t.Error("signature was not preserved; the aggregator verifies it off-chain")
	}
}

func TestRoundFailedIsRecorded(t *testing.T) {
	store := newFakeOracleStore()
	h := newRoundsHandler(t, store)

	ev := oracleEvent(t, roundsHex, eventRoundFailed, map[string]any{
		"roundId":         big.NewInt(9),
		"feedId":          feedID,
		"submissionCount": big.NewInt(2),
		"eligibleCount":   big.NewInt(6),
	})
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(store.failed) != 1 || store.failed[0] != "9" {
		t.Fatalf("failed rounds = %v", store.failed)
	}
}

func TestRoundsHandlerIgnoresOtherContracts(t *testing.T) {
	store := newFakeOracleStore()
	h := newRoundsHandler(t, store)

	ev := oracleEvent(t, nodeHex, eventRoundSettled, map[string]any{
		"roundId":         big.NewInt(1),
		"feedId":          feedID,
		"aggregatedValue": big.NewInt(1),
		"submissionCount": big.NewInt(1),
	})
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(store.settled) != 0 {
		t.Fatal("an event from another address was indexed")
	}
}

func TestOracleRoundsHandlerRejectsMissingField(t *testing.T) {
	store := newFakeOracleStore()
	h := newRoundsHandler(t, store)

	ev := oracleEvent(t, roundsHex, eventRoundSettled, map[string]any{
		"roundId":         big.NewInt(1),
		"feedId":          feedID,
		"submissionCount": big.NewInt(1),
	})
	if err := h.Handle(context.Background(), ev); err == nil {
		t.Fatal("expected an error for a settled event without aggregatedValue")
	}
}

// --- staking handler ---

func newStakingHandler(t *testing.T, store OracleNodeStore, resolver AssetResolver) *OracleStakingHandler {
	t.Helper()
	return &OracleStakingHandler{
		contract:   mustID(t, roundsHex),
		stakeAsset: mustID(t, stakeHex),
		encode:     func(id types.Identity) string { return id.EVMHex() },
		resolver:   resolver,
		store:      store,
		chainID:    testChainID,
	}
}

func TestNodeRegistrationResolvesStakeAssetDecimals(t *testing.T) {
	store := newFakeOracleStore()
	h := newStakingHandler(t, store, &fakeResolver{meta: chain.TokenMeta{Decimals: 6, Symbol: "aSTK"}})

	ev := oracleEvent(t, roundsHex, eventNodeRegistered, map[string]any{
		"node":  mustID(t, nodeHex),
		"stake": mustBigInt(t, "10000000000"),
	})
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(store.assets) != 1 || store.assets[0].Decimals != 6 {
		t.Fatalf("stake asset decimals not recorded: %+v", store.assets)
	}
	if store.nodes[0].StakedAmount.String() != "10000000000" {
		t.Errorf("stake = %s, want the raw amount", store.nodes[0].StakedAmount)
	}
}

// A transient RPC failure must not poison every later batch: sync.Once fires once, so the handler
// has to reset itself or the stake asset is never resolved again.
func TestStakeAssetResolutionRetriesAfterFailure(t *testing.T) {
	store := newFakeOracleStore()
	resolver := &fakeResolver{err: errors.New("rpc down")}
	h := newStakingHandler(t, store, resolver)

	ev := oracleEvent(t, roundsHex, eventNodeRegistered, map[string]any{
		"node":  mustID(t, nodeHex),
		"stake": big.NewInt(1),
	})
	if err := h.Handle(context.Background(), ev); err == nil {
		t.Fatal("expected the batch to fail while the asset is unresolvable")
	}

	resolver.err = nil
	resolver.meta = chain.TokenMeta{Decimals: 18}
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("retry should succeed once the endpoint recovers: %v", err)
	}
	if len(store.assets) != 1 {
		t.Fatal("asset was never resolved after the failure cleared")
	}
}

func TestUnstakeRequestDeactivatesNode(t *testing.T) {
	store := newFakeOracleStore()
	h := newStakingHandler(t, store, &fakeResolver{meta: chain.TokenMeta{Decimals: 18}})

	ev := oracleEvent(t, roundsHex, eventUnstakeRequest, map[string]any{
		"node":        mustID(t, nodeHex),
		"amount":      mustBigInt(t, "10000000000000000000000"),
		"claimableAt": big.NewInt(1736294400),
	})
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if store.activeFlags[nodeHex] {
		t.Error("a node that asked to leave must not stay active, or it counts toward quorum")
	}
	if store.unstakes[nodeHex].String() != "10000000000000000000000" {
		t.Errorf("pending unstake = %s", store.unstakes[nodeHex])
	}
}

func TestSlashRecordsAuditRowWithReadableReason(t *testing.T) {
	store := newFakeOracleStore()
	h := newStakingHandler(t, store, &fakeResolver{meta: chain.TokenMeta{Decimals: 18}})

	var reason [32]byte
	copy(reason[:], "OUTLIER_SUBMISSION")

	ev := oracleEvent(t, roundsHex, eventNodeSlashed, map[string]any{
		"node":           mustID(t, nodeHex),
		"amount":         mustBigInt(t, "1000000000000000000000"),
		"reason":         reason,
		"remainingStake": mustBigInt(t, "9000000000000000000000"),
	})
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}

	sl := store.slashes[0]
	if sl.Reason != "OUTLIER_SUBMISSION" {
		t.Errorf("reason = %q, want the trailing zero padding stripped", sl.Reason)
	}
	if sl.Amount.String() != "1000000000000000000000" || sl.RemainingStake.String() != "9000000000000000000000" {
		t.Errorf("slash amounts wrong: %+v", sl)
	}
}

func TestNodeReactivationTogglesActiveFlag(t *testing.T) {
	store := newFakeOracleStore()
	h := newStakingHandler(t, store, &fakeResolver{meta: chain.TokenMeta{Decimals: 18}})

	deactivate := oracleEvent(t, roundsHex, eventNodeDeactivated, map[string]any{
		"node":   mustID(t, nodeHex),
		"reason": [32]byte{},
	})
	reactivate := oracleEvent(t, roundsHex, eventNodeReactivated, map[string]any{
		"node": mustID(t, nodeHex),
	})

	if err := h.Handle(context.Background(), deactivate); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if store.activeFlags[nodeHex] {
		t.Fatal("node still active after deactivation")
	}

	if err := h.Handle(context.Background(), reactivate); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if !store.activeFlags[nodeHex] {
		t.Fatal("node not active after reactivation")
	}
}
