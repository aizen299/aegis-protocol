package db

import (
	"context"
	"fmt"
	"time"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const qUpsertZkGate = `
	INSERT INTO zk_gates (chain_id, address, tree_address, verifier_address)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (chain_id, address)
	DO UPDATE SET tree_address = EXCLUDED.tree_address,
	              verifier_address = EXCLUDED.verifier_address
`

const qInsertCommitment = `
	INSERT INTO zk_commitments
		(chain_id, tree_address, leaf_index, commitment, root_after,
		 tx_hash, log_index, block_number, inserted_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	ON CONFLICT (chain_id, tx_hash, log_index) DO NOTHING
`

const qUpsertZkAction = `
	INSERT INTO zk_actions
		(chain_id, gate_address, action_id, name, registered, tx_hash, log_index, block_number)
	VALUES ($1, $2, $3, $4, TRUE, $5, $6, $7)
	ON CONFLICT (chain_id, gate_address, action_id)
	DO UPDATE SET name = EXCLUDED.name, registered = TRUE
`

const qDeregisterZkAction = `
	UPDATE zk_actions SET registered = FALSE
	WHERE chain_id = $1 AND gate_address = $2 AND action_id = $3
`

const qInsertPrivateAction = `
	INSERT INTO zk_private_actions
		(chain_id, gate_address, nullifier, action_id, root,
		 tx_hash, log_index, block_number, executed_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	ON CONFLICT (chain_id, tx_hash, log_index) DO NOTHING
`

func (s *Store) UpsertZkGate(ctx context.Context, g types.ZkGateMetadata) error {
	_, err := s.pool.Exec(ctx, qUpsertZkGate, g.ChainID, g.Address, g.TreeAddress, g.VerifierAddress)
	if err != nil {
		return fmt.Errorf("upsert zk gate %s: %w", g.Address, err)
	}
	return nil
}

// ZkCommitment is the write-side row a CommitmentInserted event becomes.
type ZkCommitment struct {
	ChainID     int64
	TreeAddress string
	LeafIndex   uint64
	Commitment  string
	RootAfter   string
	TxHash      string
	LogIndex    uint
	BlockNumber uint64
	InsertedAt  time.Time
}

func (s *Store) InsertZkCommitment(ctx context.Context, c ZkCommitment) error {
	_, err := s.pool.Exec(ctx, qInsertCommitment,
		c.ChainID, c.TreeAddress, int64(c.LeafIndex), c.Commitment, c.RootAfter,
		c.TxHash, c.LogIndex, c.BlockNumber, c.InsertedAt)
	if err != nil {
		return fmt.Errorf("insert commitment at leaf %d: %w", c.LeafIndex, err)
	}
	return nil
}

// ZkActionRow is the write-side row an ActionRegistered event becomes.
type ZkActionRow struct {
	ChainID     int64
	GateAddress string
	ActionID    string
	Name        string
	TxHash      string
	LogIndex    uint
	BlockNumber uint64
}

func (s *Store) UpsertZkAction(ctx context.Context, a ZkActionRow) error {
	_, err := s.pool.Exec(ctx, qUpsertZkAction,
		a.ChainID, a.GateAddress, a.ActionID, a.Name, a.TxHash, a.LogIndex, a.BlockNumber)
	if err != nil {
		return fmt.Errorf("upsert zk action %s: %w", a.ActionID, err)
	}
	return nil
}

func (s *Store) DeregisterZkAction(ctx context.Context, chainID int64, gate, actionID string) error {
	if _, err := s.pool.Exec(ctx, qDeregisterZkAction, chainID, gate, actionID); err != nil {
		return fmt.Errorf("deregister zk action %s: %w", actionID, err)
	}
	return nil
}

// ZkPrivateAction is the write-side row a PrivateActionExecuted event becomes.
//
// It carries no sender. The chain reveals one, but recording it beside the commitment table would
// make correlation a join rather than an investigation — see migration 000008.
type ZkPrivateAction struct {
	ChainID     int64
	GateAddress string
	Nullifier   string
	ActionID    string
	Root        string
	TxHash      string
	LogIndex    uint
	BlockNumber uint64
	ExecutedAt  time.Time
}

func (s *Store) InsertZkPrivateAction(ctx context.Context, a ZkPrivateAction) error {
	_, err := s.pool.Exec(ctx, qInsertPrivateAction,
		a.ChainID, a.GateAddress, a.Nullifier, a.ActionID, a.Root,
		a.TxHash, a.LogIndex, a.BlockNumber, a.ExecutedAt)
	if err != nil {
		return fmt.Errorf("insert private action %s: %w", a.Nullifier, err)
	}
	return nil
}
