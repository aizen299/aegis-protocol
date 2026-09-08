package indexer

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// A feed's scale is declared at registration and carried in FeedRegistered, so nothing here
// assumes it. An earlier revision hardcoded 18 in Go — the same mistake as the hardcoded token
// decimals, one level up: correct for every feed built so far and silently wrong for the first
// one that is not.
//
// fallbackFeedDecimals applies only when a round is seen for a feed whose registration event was
// never indexed, which happens when the indexer starts after deployment. The placeholder row is
// corrected if the registration is later backfilled.
const fallbackFeedDecimals uint8 = 18

const (
	eventFeedRegistered   = "FeedRegistered"
	eventFeedDeregistered = "FeedDeregistered"
	eventRoundStarted     = "RoundStarted"
	eventRoundQuorumMet   = "RoundQuorumMet"
	eventRoundSettled     = "RoundSettled"
	eventRoundFailed      = "RoundFailed"
	eventSubmission       = "SubmissionReceived"

	eventNodeRegistered  = "NodeRegistered"
	eventNodeStaked      = "NodeStaked"
	eventUnstakeRequest  = "UnstakeRequested"
	eventUnstakeCancel   = "UnstakeCancelled"
	eventNodeUnstaked    = "NodeUnstaked"
	eventNodeSlashed     = "NodeSlashed"
	eventNodeDeactivated = "NodeDeactivated"
	eventNodeReactivated = "NodeReactivated"
)

// OracleRoundStore is the write surface the rounds handler needs.
type OracleRoundStore interface {
	UpsertOracleFeed(ctx context.Context, f db.Feed) error
	EnsureOracleFeed(ctx context.Context, chainID int64, feedID string, decimals uint8) error
	DeactivateOracleFeed(ctx context.Context, chainID int64, feedID string) error
	InsertOracleRound(ctx context.Context, r db.Round) error
	MarkOracleRoundQuorumMet(ctx context.Context, chainID int64, roundID types.Raw, count int32) error
	SettleOracleRound(ctx context.Context, chainID int64, roundID, value types.Raw, count int32, at time.Time) error
	FailOracleRound(ctx context.Context, chainID int64, roundID types.Raw, count int32, at time.Time) error
	InsertOracleSubmission(ctx context.Context, sub db.Submission) error
}

// OracleRoundsHandler turns OracleRounds events into rows.
type OracleRoundsHandler struct {
	contract types.Identity
	encode   func(types.Identity) string
	store    OracleRoundStore
	chainID  int64
}

func NewOracleRoundsHandler(store OracleRoundStore, client chain.Client, contract types.Identity) *OracleRoundsHandler {
	return &OracleRoundsHandler{
		contract: contract,
		encode:   client.EncodeIdentity,
		store:    store,
		chainID:  client.ChainID(),
	}
}

func (h *OracleRoundsHandler) Filters() []chain.Filter {
	return []chain.Filter{{
		Contract: h.contract,
		Names: []string{
			eventFeedRegistered, eventFeedDeregistered, eventRoundStarted,
			eventRoundQuorumMet, eventRoundSettled, eventRoundFailed, eventSubmission,
		},
	}}
}

func (h *OracleRoundsHandler) Handle(ctx context.Context, ev chain.Event) error {
	if ev.Contract != h.contract {
		return nil
	}

	switch ev.Name {
	case eventFeedRegistered:
		return h.handleFeedRegistered(ctx, ev)
	case eventFeedDeregistered:
		feedID, err := bytes32Field(ev, "feedId")
		if err != nil {
			return err
		}
		return h.store.DeactivateOracleFeed(ctx, ev.ChainID, hexBytes32(feedID))
	case eventRoundStarted:
		return h.handleRoundStarted(ctx, ev)
	case eventRoundQuorumMet:
		return h.handleQuorumMet(ctx, ev)
	case eventRoundSettled:
		return h.handleSettled(ctx, ev)
	case eventRoundFailed:
		return h.handleFailed(ctx, ev)
	case eventSubmission:
		return h.handleSubmission(ctx, ev)
	default:
		return nil
	}
}

func (h *OracleRoundsHandler) handleFeedRegistered(ctx context.Context, ev chain.Event) error {
	feedID, err := bytes32Field(ev, "feedId")
	if err != nil {
		return err
	}
	name, err := stringField(ev, "name")
	if err != nil {
		return err
	}

	decimals, err := uint8Field(ev, "decimals")
	if err != nil {
		return err
	}

	return h.store.UpsertOracleFeed(ctx, db.Feed{
		ChainID:  ev.ChainID,
		FeedID:   hexBytes32(feedID),
		Name:     name,
		Decimals: decimals,
	})
}

func (h *OracleRoundsHandler) handleRoundStarted(ctx context.Context, ev chain.Event) error {
	roundID, err := rawField(ev, "roundId")
	if err != nil {
		return err
	}
	feedID, err := bytes32Field(ev, "feedId")
	if err != nil {
		return err
	}
	openedAt, err := unixField(ev, "openedAt")
	if err != nil {
		return err
	}
	deadline, err := unixField(ev, "deadline")
	if err != nil {
		return err
	}
	eligible, err := rawField(ev, "eligibleCount")
	if err != nil {
		return err
	}
	version, err := rawField(ev, "nodeSetVersion")
	if err != nil {
		return err
	}

	feed := hexBytes32(feedID)
	// The round's foreign key requires the feed row. An indexer started after the feed was
	// registered would otherwise stall on a round it can see but cannot attribute.
	if err := h.store.EnsureOracleFeed(ctx, ev.ChainID, feed, fallbackFeedDecimals); err != nil {
		return err
	}

	return h.store.InsertOracleRound(ctx, db.Round{
		ChainID:        ev.ChainID,
		RoundID:        roundID,
		FeedID:         feed,
		OpenedAt:       openedAt,
		Deadline:       deadline,
		EligibleCount:  int32(eligible.Big().Int64()),
		NodeSetVersion: version,
		TxHash:         txHashHex(ev.TxHash),
		LogIndex:       ev.LogIndex,
		BlockNumber:    ev.BlockNumber,
	})
}

func (h *OracleRoundsHandler) handleQuorumMet(ctx context.Context, ev chain.Event) error {
	roundID, err := rawField(ev, "roundId")
	if err != nil {
		return err
	}
	count, err := rawField(ev, "submissionCount")
	if err != nil {
		return err
	}
	return h.store.MarkOracleRoundQuorumMet(ctx, ev.ChainID, roundID, int32(count.Big().Int64()))
}

func (h *OracleRoundsHandler) handleSettled(ctx context.Context, ev chain.Event) error {
	roundID, err := rawField(ev, "roundId")
	if err != nil {
		return err
	}
	value, err := rawField(ev, "aggregatedValue")
	if err != nil {
		return err
	}
	count, err := rawField(ev, "submissionCount")
	if err != nil {
		return err
	}
	return h.store.SettleOracleRound(ctx, ev.ChainID, roundID, value,
		int32(count.Big().Int64()), blockTime(ev))
}

func (h *OracleRoundsHandler) handleFailed(ctx context.Context, ev chain.Event) error {
	roundID, err := rawField(ev, "roundId")
	if err != nil {
		return err
	}
	count, err := rawField(ev, "submissionCount")
	if err != nil {
		return err
	}
	return h.store.FailOracleRound(ctx, ev.ChainID, roundID, int32(count.Big().Int64()), blockTime(ev))
}

func (h *OracleRoundsHandler) handleSubmission(ctx context.Context, ev chain.Event) error {
	roundID, err := rawField(ev, "roundId")
	if err != nil {
		return err
	}
	node, err := identityField(ev, "node")
	if err != nil {
		return err
	}
	value, err := rawField(ev, "value")
	if err != nil {
		return err
	}
	signature, err := bytesField(ev, "signature")
	if err != nil {
		return err
	}

	return h.store.InsertOracleSubmission(ctx, db.Submission{
		ChainID:     ev.ChainID,
		RoundID:     roundID,
		Node:        h.encode(node),
		Value:       value,
		Signature:   signature,
		TxHash:      txHashHex(ev.TxHash),
		LogIndex:    ev.LogIndex,
		BlockNumber: ev.BlockNumber,
		SubmittedAt: blockTime(ev),
	})
}

// --- field decoding shared with the vault handler ---

func bytes32Field(ev chain.Event, key string) ([32]byte, error) {
	raw, ok := ev.Payload[key]
	if !ok {
		return [32]byte{}, fmt.Errorf("event %s: missing field %q", ev.Name, key)
	}
	v, ok := raw.([32]byte)
	if !ok {
		return [32]byte{}, fmt.Errorf("event %s: field %q is %T, want [32]byte", ev.Name, key, raw)
	}
	return v, nil
}

func uint8Field(ev chain.Event, key string) (uint8, error) {
	raw, ok := ev.Payload[key]
	if !ok {
		return 0, fmt.Errorf("event %s: missing field %q", ev.Name, key)
	}
	v, ok := raw.(uint8)
	if !ok {
		return 0, fmt.Errorf("event %s: field %q is %T, want uint8", ev.Name, key, raw)
	}
	return v, nil
}

func stringField(ev chain.Event, key string) (string, error) {
	raw, ok := ev.Payload[key]
	if !ok {
		return "", fmt.Errorf("event %s: missing field %q", ev.Name, key)
	}
	v, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("event %s: field %q is %T, want string", ev.Name, key, raw)
	}
	return v, nil
}

func bytesField(ev chain.Event, key string) ([]byte, error) {
	raw, ok := ev.Payload[key]
	if !ok {
		return nil, fmt.Errorf("event %s: missing field %q", ev.Name, key)
	}
	v, ok := raw.([]byte)
	if !ok {
		return nil, fmt.Errorf("event %s: field %q is %T, want []byte", ev.Name, key, raw)
	}
	return v, nil
}

func unixField(ev chain.Event, key string) (time.Time, error) {
	raw, err := rawField(ev, key)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(raw.Big().Int64(), 0).UTC(), nil
}

func blockTime(ev chain.Event) time.Time {
	return time.Unix(ev.BlockTime, 0).UTC()
}

func hexBytes32(b [32]byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 2+len(b)*2)
	out[0], out[1] = '0', 'x'
	for i, v := range b {
		out[2+i*2] = hexDigits[v>>4]
		out[3+i*2] = hexDigits[v&0x0f]
	}
	return string(out)
}

// bytes32 reason codes are right-padded with zeros on chain; the database stores the readable form.
func trimBytes32(b [32]byte) string {
	return string(bytes.TrimRight(b[:], "\x00"))
}
