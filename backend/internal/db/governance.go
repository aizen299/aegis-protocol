package db

import (
	"context"
	"fmt"
	"time"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const qUpsertGovernor = `
	INSERT INTO governors (chain_id, address, token_address, timelock_address)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (chain_id, address)
	DO UPDATE SET token_address = EXCLUDED.token_address,
	              timelock_address = EXCLUDED.timelock_address
`

const qInsertProposal = `
	INSERT INTO governance_proposals
		(chain_id, governor_address, proposal_id, proposer, title, description, target,
		 target_chain_id, action_value, calldata, state, vote_start, vote_end,
		 tx_hash, log_index, block_number)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending', $11, $12, $13, $14, $15)
	ON CONFLICT (chain_id, proposal_id) DO NOTHING
`

const qInsertVote = `
	INSERT INTO governance_votes
		(chain_id, proposal_id, voter, support, weight, reason, tx_hash, log_index, block_number, voted_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	ON CONFLICT (chain_id, tx_hash, log_index) DO NOTHING
`

// Tallies are recomputed from the votes table rather than accumulated, so a replayed batch cannot
// double-count. The votes table is idempotent on (chain_id, tx_hash, log_index); a sum over it is
// therefore idempotent too, which an incrementing UPDATE would not be.
const qRetallyProposal = `
	UPDATE governance_proposals p
	SET votes_for = t.for_votes, votes_against = t.against_votes, votes_abstain = t.abstain_votes
	FROM (
		SELECT
			COALESCE(SUM(weight) FILTER (WHERE support = 1), 0) AS for_votes,
			COALESCE(SUM(weight) FILTER (WHERE support = 0), 0) AS against_votes,
			COALESCE(SUM(weight) FILTER (WHERE support = 2), 0) AS abstain_votes
		FROM governance_votes
		WHERE chain_id = $1 AND proposal_id = $2
	) t
	WHERE p.chain_id = $1 AND p.proposal_id = $2
`

// Every transition is guarded on the states it may legally leave, so a replayed or out-of-order
// batch cannot walk a proposal backwards out of a terminal state.
const qMarkProposalQueued = `
	UPDATE governance_proposals
	SET state = 'queued', operation_id = $3, executable_at = $4, queued_at = $5
	WHERE chain_id = $1 AND proposal_id = $2
	  AND state IN ('pending', 'active', 'succeeded')
`

// Reachable only once a destination chain exists. `dispatched` is not terminal: the indexer
// resolves it on confirmation from that chain, and an unresolved one is an operational alert.
const qMarkProposalDispatched = `
	UPDATE governance_proposals
	SET state = 'dispatched', dispatched_at = $3
	WHERE chain_id = $1 AND proposal_id = $2 AND state = 'queued'
`

const qMarkProposalExecuted = `
	UPDATE governance_proposals
	SET state = 'executed', executed_at = $3
	WHERE chain_id = $1 AND proposal_id = $2 AND state IN ('queued', 'dispatched')
`

const qMarkProposalCancelled = `
	UPDATE governance_proposals
	SET state = 'cancelled', cancelled_at = $3
	WHERE chain_id = $1 AND proposal_id = $2
	  AND state NOT IN ('executed', 'cancelled')
`

func (s *Store) UpsertGovernor(ctx context.Context, g types.GovernorMetadata) error {
	_, err := s.pool.Exec(ctx, qUpsertGovernor, g.ChainID, g.Address, g.TokenAddress, g.TimelockAddress)
	if err != nil {
		return fmt.Errorf("upsert governor %s: %w", g.Address, err)
	}
	return nil
}

// GovernanceProposal is the write-side row a ProposalCreated event becomes.
type GovernanceProposal struct {
	ChainID         int64
	GovernorAddress string
	ProposalID      types.Raw
	Proposer        string
	Title           string
	Description     string
	Target          string
	TargetChainID   int64
	ActionValue     types.Raw
	Calldata        []byte
	VoteStart       int64
	VoteEnd         int64
	TxHash          string
	LogIndex        uint
	BlockNumber     uint64
}

func (s *Store) InsertGovernanceProposal(ctx context.Context, p GovernanceProposal) error {
	_, err := s.pool.Exec(ctx, qInsertProposal,
		p.ChainID, p.GovernorAddress, p.ProposalID, p.Proposer, p.Title, p.Description,
		p.Target, p.TargetChainID, p.ActionValue, p.Calldata,
		p.VoteStart, p.VoteEnd, p.TxHash, p.LogIndex, p.BlockNumber)
	if err != nil {
		return fmt.Errorf("insert proposal %s: %w", p.ProposalID, err)
	}
	return nil
}

// GovernanceVote is the write-side row a VoteCast event becomes.
type GovernanceVote struct {
	ChainID     int64
	ProposalID  types.Raw
	Voter       string
	Support     uint8
	Weight      types.Raw
	Reason      string
	TxHash      string
	LogIndex    uint
	BlockNumber uint64
	VotedAt     time.Time
}

// InsertGovernanceVote writes the vote and re-derives the proposal's tallies from the votes table.
// Both statements run in one transaction: a vote whose tally never landed would understate the
// result, and the API reads the tally.
func (s *Store) InsertGovernanceVote(ctx context.Context, v GovernanceVote) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin vote tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, qInsertVote, v.ChainID, v.ProposalID, v.Voter, v.Support,
		v.Weight, v.Reason, v.TxHash, v.LogIndex, v.BlockNumber, v.VotedAt); err != nil {
		return fmt.Errorf("insert vote by %s: %w", v.Voter, err)
	}

	if _, err := tx.Exec(ctx, qRetallyProposal, v.ChainID, v.ProposalID); err != nil {
		return fmt.Errorf("retally proposal %s: %w", v.ProposalID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit vote tx: %w", err)
	}
	return nil
}

func (s *Store) MarkProposalQueued(ctx context.Context, chainID int64, proposalID, operationID types.Raw, executableAt, at time.Time) error {
	_, err := s.pool.Exec(ctx, qMarkProposalQueued, chainID, proposalID, operationID, executableAt, at)
	if err != nil {
		return fmt.Errorf("mark proposal %s queued: %w", proposalID, err)
	}
	return nil
}

func (s *Store) MarkProposalExecuted(ctx context.Context, chainID int64, proposalID types.Raw, at time.Time) error {
	if _, err := s.pool.Exec(ctx, qMarkProposalExecuted, chainID, proposalID, at); err != nil {
		return fmt.Errorf("mark proposal %s executed: %w", proposalID, err)
	}
	return nil
}

func (s *Store) MarkProposalDispatched(ctx context.Context, chainID int64, proposalID types.Raw, at time.Time) error {
	if _, err := s.pool.Exec(ctx, qMarkProposalDispatched, chainID, proposalID, at); err != nil {
		return fmt.Errorf("mark proposal %s dispatched: %w", proposalID, err)
	}
	return nil
}

func (s *Store) MarkProposalCancelled(ctx context.Context, chainID int64, proposalID types.Raw, at time.Time) error {
	if _, err := s.pool.Exec(ctx, qMarkProposalCancelled, chainID, proposalID, at); err != nil {
		return fmt.Errorf("mark proposal %s cancelled: %w", proposalID, err)
	}
	return nil
}
