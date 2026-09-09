-- Execution state for a slash decision, separate from the decision itself.
--
-- submitted_at and executed_at are distinct on purpose, for the same reason governance_proposals
-- separates dispatched from executed: a transaction that was sent is not a transaction that
-- landed. The executor sets submitted_at; the indexer sets executed_at when it sees NodeSlashed.
-- A decision stuck between them is an operational alert, not a resting state.
ALTER TABLE oracle_slashings ADD COLUMN submitted_at TIMESTAMPTZ;
ALTER TABLE oracle_slashings ADD COLUMN attempts INT NOT NULL DEFAULT 0;
ALTER TABLE oracle_slashings ADD COLUMN abandoned_at TIMESTAMPTZ;
ALTER TABLE oracle_slashings ADD COLUMN abandoned_reason TEXT;

-- Only decisions that are neither in flight nor finished are eligible for submission.
CREATE INDEX idx_oracle_slashings_claimable ON oracle_slashings(chain_id, decided_at)
    WHERE executed_at IS NULL AND submitted_at IS NULL AND abandoned_at IS NULL;

-- A submitted penalty that never confirmed. What an operator is paged for.
CREATE INDEX idx_oracle_slashings_unconfirmed ON oracle_slashings(chain_id, submitted_at)
    WHERE submitted_at IS NOT NULL AND executed_at IS NULL;

-- The contract permits one penalty per node per round, so the audit trail should not hold two
-- either. Without this a duplicate decision would be submitted, revert, and be abandoned — noise
-- for something the schema can simply prevent.
CREATE UNIQUE INDEX idx_oracle_slashings_one_per_round
    ON oracle_slashings(chain_id, node_address, round_id)
    WHERE round_id IS NOT NULL;
