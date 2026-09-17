package db

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// RemoteActionReceived is the row a receiver's MessageReceived event becomes.
type RemoteActionReceived struct {
	ChainID       int64
	Receiver      string
	EmitterChain  uint16
	Sequence      types.Raw
	SourceChainID int64
	OperationID   types.Raw
	Target        string
	DeclaredValue types.Raw
	AccountsHash  [32]byte
	ExecutableAt  time.Time
	ReceivedAt    time.Time
	TxHash        string
}

const qInsertRemoteAction = `
	INSERT INTO governance_remote_actions
		(chain_id, receiver, emitter_chain, sequence, source_chain_id, operation_id, target,
		 declared_value, accounts_hash, status, executable_at, received_at, received_tx)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'pending', $10, $11, $12)
	ON CONFLICT (chain_id, emitter_chain, sequence) DO NOTHING
`

// Only a pending action closes, so a replayed batch cannot move an executed one to cancelled.
const qCloseRemoteAction = `
	UPDATE governance_remote_actions
	SET status = $4, closed_at = $5, closed_tx = $6, closed_by = $7
	WHERE chain_id = $1 AND emitter_chain = $2 AND sequence = $3 AND status = 'pending'
`

func (s *Store) InsertRemoteAction(ctx context.Context, a RemoteActionReceived) error {
	_, err := s.pool.Exec(ctx, qInsertRemoteAction,
		a.ChainID, a.Receiver, int32(a.EmitterChain), a.Sequence, a.SourceChainID, a.OperationID, a.Target,
		a.DeclaredValue, a.AccountsHash[:], a.ExecutableAt, a.ReceivedAt, a.TxHash)
	if err != nil {
		return fmt.Errorf("insert remote action %d/%s: %w", a.EmitterChain, a.Sequence, err)
	}
	return nil
}

// CloseRemoteAction records an execution or cancellation. The received row must already exist: both
// events come from the same program, in order, so a missing row is an indexing fault, not a race.
func (s *Store) CloseRemoteAction(ctx context.Context, chainID int64, emitterChain uint16, sequence types.Raw, status string, at time.Time, txHash, by string) error {
	tag, err := s.pool.Exec(ctx, qCloseRemoteAction, chainID, int32(emitterChain), sequence, status, at, txHash, by)
	if err != nil {
		return fmt.Errorf("close remote action %d/%s: %w", emitterChain, sequence, err)
	}
	if tag.RowsAffected() == 0 {
		var current string
		err := s.pool.QueryRow(ctx,
			`SELECT status FROM governance_remote_actions WHERE chain_id = $1 AND emitter_chain = $2 AND sequence = $3`,
			chainID, int32(emitterChain), sequence).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("close remote action %d/%s: never received", emitterChain, sequence)
		}
		if err != nil {
			return fmt.Errorf("close remote action %d/%s: %w", emitterChain, sequence, err)
		}
	}
	return nil
}

const qRemoteActionColumns = `
	chain_id, receiver, emitter_chain, sequence, source_chain_id, operation_id, target, declared_value,
	accounts_hash, status, executable_at, received_at, received_tx, closed_at, COALESCE(closed_tx, ''),
	COALESCE(closed_by, '')
`

const qListRemoteActions = `
	SELECT ` + qRemoteActionColumns + `
	FROM governance_remote_actions
	WHERE chain_id = $1 AND ($2 = '' OR status = $2)
	ORDER BY received_at DESC, emitter_chain, sequence DESC
	LIMIT $3 OFFSET $4
`

// A dispatched operation is published once, so it has at most one receipt per destination. The first
// received is taken should that ever not hold.
const qRemoteActionsForOperations = `
	SELECT DISTINCT ON (chain_id, operation_id) ` + qRemoteActionColumns + `
	FROM governance_remote_actions
	WHERE source_chain_id = $1 AND operation_id = ANY($2::NUMERIC[])
	ORDER BY chain_id, operation_id, received_at, sequence
`

func (s *Store) ListRemoteActions(ctx context.Context, chainID int64, status string, limit, offset int) ([]types.RemoteAction, error) {
	rows, err := s.pool.Query(ctx, qListRemoteActions, chainID, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list remote actions: %w", err)
	}
	return collectRemoteActions(rows, limit)
}

func collectRemoteActions(rows pgx.Rows, capacity int) ([]types.RemoteAction, error) {
	defer rows.Close()
	out := make([]types.RemoteAction, 0, capacity)
	for rows.Next() {
		var a types.RemoteAction
		var emitterChain int32
		var hash []byte
		if err := rows.Scan(&a.ChainID, &a.Receiver, &emitterChain, &a.Sequence, &a.SourceChainID, &a.OperationID,
			&a.Target, &a.DeclaredValue, &hash, &a.Status, &a.ExecutableAt, &a.ReceivedAt, &a.ReceivedTx,
			&a.ClosedAt, &a.ClosedTx, &a.ClosedBy); err != nil {
			return nil, err
		}
		a.EmitterChain = uint16(emitterChain)
		a.AccountsHash = "0x" + hex.EncodeToString(hash)
		out = append(out, a)
	}
	return out, rows.Err()
}

// attachRemoteActions fills in each dispatched proposal's receipt on its destination chain.
func (s *Store) attachRemoteActions(ctx context.Context, chainID int64, proposals []types.Proposal) error {
	ids := make([]string, 0, len(proposals))
	for _, p := range proposals {
		if p.OperationID != nil && p.Action.TargetChainID != chainID {
			ids = append(ids, p.OperationID.String())
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, qRemoteActionsForOperations, chainID, ids)
	if err != nil {
		return fmt.Errorf("remote actions for proposals: %w", err)
	}
	actions, err := collectRemoteActions(rows, len(ids))
	if err != nil {
		return fmt.Errorf("remote actions for proposals: %w", err)
	}
	for i := range proposals {
		p := &proposals[i]
		if p.OperationID == nil {
			continue
		}
		for j := range actions {
			if actions[j].ChainID == p.Action.TargetChainID && actions[j].OperationID.String() == p.OperationID.String() {
				p.Remote = &actions[j]
			}
		}
	}
	return nil
}
