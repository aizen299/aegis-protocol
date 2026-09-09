package db

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// Every read joins the vote token's decimals in. A weight without its scale is uninterpretable, and
// the caller must never have to guess it.

const qProposalColumns = `
	p.chain_id, p.governor_address, p.proposal_id, p.proposer, p.title, COALESCE(p.description, ''),
	p.target, p.target_chain_id, p.action_value, p.calldata, p.state, p.vote_start, p.vote_end,
	p.votes_for, p.votes_against, p.votes_abstain, a.decimals,
	p.operation_id, p.executable_at, p.queued_at, p.dispatched_at, p.executed_at, p.cancelled_at,
	p.tx_hash, p.log_index, p.block_number
`

const qProposalJoin = `
	FROM governance_proposals p
	JOIN governors g ON g.chain_id = p.chain_id AND g.address = p.governor_address
	JOIN assets a ON a.chain_id = g.chain_id AND a.address = g.token_address
`

const qListProposals = `
	SELECT ` + qProposalColumns + qProposalJoin + `
	WHERE p.chain_id = $1
	ORDER BY p.proposal_id DESC
	LIMIT $2 OFFSET $3
`

const qListProposalsByState = `
	SELECT ` + qProposalColumns + qProposalJoin + `
	WHERE p.chain_id = $1 AND p.state = $2
	ORDER BY p.proposal_id DESC
	LIMIT $3 OFFSET $4
`

const qGetProposal = `
	SELECT ` + qProposalColumns + qProposalJoin + `
	WHERE p.chain_id = $1 AND p.proposal_id = $2
`

// Queued proposals, soonest-executable first. This is the query the timelock exists to make
// answerable: a delay only protects anyone if what is waiting can be seen.
const qListQueuedProposals = `
	SELECT ` + qProposalColumns + qProposalJoin + `
	WHERE p.chain_id = $1 AND p.state = 'queued'
	ORDER BY p.executable_at ASC
	LIMIT $2 OFFSET $3
`

const qVoteColumns = `
	v.chain_id, v.proposal_id, v.voter, v.support, v.weight, a.decimals, COALESCE(v.reason, ''),
	v.tx_hash, v.log_index, v.block_number, v.voted_at
`

const qVoteJoin = `
	FROM governance_votes v
	JOIN governance_proposals p ON p.chain_id = v.chain_id AND p.proposal_id = v.proposal_id
	JOIN governors g ON g.chain_id = p.chain_id AND g.address = p.governor_address
	JOIN assets a ON a.chain_id = g.chain_id AND a.address = g.token_address
`

const qListVotesByProposal = `
	SELECT ` + qVoteColumns + qVoteJoin + `
	WHERE v.chain_id = $1 AND v.proposal_id = $2
	ORDER BY v.weight DESC, v.voter
	LIMIT $3 OFFSET $4
`

const qListVotesByVoter = `
	SELECT ` + qVoteColumns + qVoteJoin + `
	WHERE v.chain_id = $1 AND v.voter = $2
	ORDER BY v.proposal_id DESC
	LIMIT $3 OFFSET $4
`

const qGetGovernor = `
	SELECT g.chain_id, g.address, g.token_address, g.timelock_address, a.decimals
	FROM governors g
	JOIN assets a ON a.chain_id = g.chain_id AND a.address = g.token_address
	WHERE g.chain_id = $1
	ORDER BY g.address
	LIMIT 1
`

func (s *Store) ListProposals(ctx context.Context, chainID int64, state string, limit, offset int) ([]types.Proposal, error) {
	var rows pgx.Rows
	var err error

	switch state {
	case "":
		rows, err = s.pool.Query(ctx, qListProposals, chainID, limit, offset)
	case types.ProposalStateQueued:
		rows, err = s.pool.Query(ctx, qListQueuedProposals, chainID, limit, offset)
	default:
		rows, err = s.pool.Query(ctx, qListProposalsByState, chainID, state, limit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("list proposals: %w", err)
	}
	defer rows.Close()

	out := make([]types.Proposal, 0, limit)
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Proposal(ctx context.Context, chainID int64, proposalID types.Raw) (types.Proposal, error) {
	p, err := scanProposal(s.pool.QueryRow(ctx, qGetProposal, chainID, proposalID))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Store) ListProposalVotes(ctx context.Context, chainID int64, proposalID types.Raw, limit, offset int) ([]types.Vote, error) {
	return s.listVotes(ctx, qListVotesByProposal, chainID, proposalID, limit, offset)
}

func (s *Store) ListVotesByVoter(ctx context.Context, chainID int64, voter string, limit, offset int) ([]types.Vote, error) {
	return s.listVotes(ctx, qListVotesByVoter, chainID, voter, limit, offset)
}

func (s *Store) Governor(ctx context.Context, chainID int64) (types.GovernorMetadata, error) {
	var g types.GovernorMetadata
	err := s.pool.QueryRow(ctx, qGetGovernor, chainID).Scan(
		&g.ChainID, &g.Address, &g.TokenAddress, &g.TimelockAddress, &g.TokenDecimals)
	if errors.Is(err, pgx.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, fmt.Errorf("get governor: %w", err)
	}
	return g, nil
}

func (s *Store) listVotes(ctx context.Context, query string, chainID int64, key any, limit, offset int) ([]types.Vote, error) {
	rows, err := s.pool.Query(ctx, query, chainID, key, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list votes: %w", err)
	}
	defer rows.Close()

	out := make([]types.Vote, 0, limit)
	for rows.Next() {
		var v types.Vote
		if err := rows.Scan(&v.ChainID, &v.ProposalID, &v.Voter, &v.Support, &v.Weight,
			&v.VoteDecimals, &v.Reason, &v.TxHash, &v.LogIndex, &v.BlockNumber, &v.VotedAt); err != nil {
			return nil, fmt.Errorf("scan vote: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func scanProposal(row scanner) (types.Proposal, error) {
	var p types.Proposal
	var calldata []byte

	err := row.Scan(
		&p.ChainID, &p.GovernorAddress, &p.ProposalID, &p.Proposer, &p.Title, &p.Description,
		&p.Action.Target, &p.Action.TargetChainID, &p.Action.Value, &calldata, &p.State,
		&p.VoteStart, &p.VoteEnd,
		&p.VotesFor, &p.VotesAgainst, &p.VotesAbstain, &p.VoteDecimals,
		&p.OperationID, &p.ExecutableAt, &p.QueuedAt, &p.DispatchedAt, &p.ExecutedAt, &p.CancelledAt,
		&p.TxHash, &p.LogIndex, &p.BlockNumber)
	if err != nil {
		return p, err
	}

	p.Action.Calldata = "0x" + hex.EncodeToString(calldata)
	return p, nil
}
