package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const qZkGate = `
	SELECT chain_id, address, tree_address, verifier_address
	FROM zk_gates
	WHERE chain_id = $1
	ORDER BY address
	LIMIT 1
`

// The leaf count is the anonymity set. It is served rather than left to be inferred, because
// docs/v0.4-zk-plan.md §4 records the bound as unresolved and commits to surfacing it.
const qAnonymitySet = `
	SELECT COUNT(*),
	       COALESCE((
	           SELECT root_after FROM zk_commitments
	           WHERE chain_id = $1 AND tree_address = $2
	           ORDER BY leaf_index DESC LIMIT 1
	       ), '')
	FROM zk_commitments
	WHERE chain_id = $1 AND tree_address = $2
`

// Ordered by leaf index because that order is what a Merkle path is built from; any other ordering
// produces a different tree.
const qListCommitments = `
	SELECT chain_id, tree_address, leaf_index, commitment, root_after,
	       tx_hash, log_index, block_number, inserted_at
	FROM zk_commitments
	WHERE chain_id = $1 AND tree_address = $2
	ORDER BY leaf_index ASC
	LIMIT $3 OFFSET $4
`

const qListPrivateActions = `
	SELECT chain_id, gate_address, nullifier, action_id, root,
	       tx_hash, log_index, block_number, executed_at
	FROM zk_private_actions
	WHERE chain_id = $1 AND gate_address = $2
	ORDER BY block_number DESC, log_index DESC
	LIMIT $3 OFFSET $4
`

const qNullifierSpent = `
	SELECT EXISTS (
	    SELECT 1 FROM zk_private_actions
	    WHERE chain_id = $1 AND gate_address = $2 AND nullifier = $3
	)
`

const qListZkActions = `
	SELECT chain_id, gate_address, action_id, name, registered, block_number
	FROM zk_actions
	WHERE chain_id = $1 AND gate_address = $2
	ORDER BY name
	LIMIT $3 OFFSET $4
`

func (s *Store) ZkGate(ctx context.Context, chainID int64) (types.ZkGateMetadata, error) {
	var g types.ZkGateMetadata
	err := s.pool.QueryRow(ctx, qZkGate, chainID).
		Scan(&g.ChainID, &g.Address, &g.TreeAddress, &g.VerifierAddress)
	if errors.Is(err, pgx.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, fmt.Errorf("get zk gate: %w", err)
	}
	return g, nil
}

func (s *Store) AnonymitySet(ctx context.Context, chainID int64, tree string) (types.AnonymitySet, error) {
	set := types.AnonymitySet{ChainID: chainID, TreeAddress: tree}

	if err := s.pool.QueryRow(ctx, qAnonymitySet, chainID, tree).
		Scan(&set.LeafCount, &set.CurrentRoot); err != nil {
		return set, fmt.Errorf("anonymity set: %w", err)
	}
	return set, nil
}

func (s *Store) ListCommitments(ctx context.Context, chainID int64, tree string, limit, offset int) ([]types.Commitment, error) {
	rows, err := s.pool.Query(ctx, qListCommitments, chainID, tree, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list commitments: %w", err)
	}
	defer rows.Close()

	out := make([]types.Commitment, 0, limit)
	for rows.Next() {
		var c types.Commitment
		if err := rows.Scan(&c.ChainID, &c.TreeAddress, &c.LeafIndex, &c.Commitment, &c.RootAfter,
			&c.TxHash, &c.LogIndex, &c.BlockNumber, &c.InsertedAt); err != nil {
			return nil, fmt.Errorf("scan commitment: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListPrivateActions(ctx context.Context, chainID int64, gate string, limit, offset int) ([]types.PrivateAction, error) {
	rows, err := s.pool.Query(ctx, qListPrivateActions, chainID, gate, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list private actions: %w", err)
	}
	defer rows.Close()

	out := make([]types.PrivateAction, 0, limit)
	for rows.Next() {
		var a types.PrivateAction
		if err := rows.Scan(&a.ChainID, &a.GateAddress, &a.Nullifier, &a.ActionID, &a.Root,
			&a.TxHash, &a.LogIndex, &a.BlockNumber, &a.ExecutedAt); err != nil {
			return nil, fmt.Errorf("scan private action: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// NullifierSpent answers the one question a client needs before paying to generate a proof.
func (s *Store) NullifierSpent(ctx context.Context, chainID int64, gate, nullifier string) (bool, error) {
	var spent bool
	if err := s.pool.QueryRow(ctx, qNullifierSpent, chainID, gate, nullifier).Scan(&spent); err != nil {
		return false, fmt.Errorf("nullifier spent: %w", err)
	}
	return spent, nil
}

func (s *Store) ListZkActions(ctx context.Context, chainID int64, gate string, limit, offset int) ([]types.ZkAction, error) {
	rows, err := s.pool.Query(ctx, qListZkActions, chainID, gate, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list zk actions: %w", err)
	}
	defer rows.Close()

	out := make([]types.ZkAction, 0, limit)
	for rows.Next() {
		var a types.ZkAction
		if err := rows.Scan(&a.ChainID, &a.GateAddress, &a.ActionID, &a.Name, &a.Registered,
			&a.BlockNumber); err != nil {
			return nil, fmt.Errorf("scan zk action: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
