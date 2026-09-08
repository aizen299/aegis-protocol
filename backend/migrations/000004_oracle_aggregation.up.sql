-- Marks a round as analysed by the aggregation service, so the poller does not reprocess it and an
-- unanalysed settled round is visible as a backlog rather than being silently skipped.
ALTER TABLE oracle_rounds ADD COLUMN aggregated_at TIMESTAMPTZ;

-- What the service computed independently of the contract. Stored rather than discarded because a
-- disagreement is the signal that something is wrong with one of the two, and diagnosing it later
-- needs both numbers.
ALTER TABLE oracle_rounds ADD COLUMN service_value NUMERIC(78, 0)
    CHECK (service_value IS NULL OR service_value >= 0);

-- Set when the service's median differs from the settled value. An alert, never a correction: the
-- chain is authoritative, and a backend that overrode a settled price would be claiming authority
-- the staking and slashing design exists to deny it.
ALTER TABLE oracle_rounds ADD COLUMN value_mismatch BOOLEAN NOT NULL DEFAULT FALSE;

-- Records why a submission was rejected off-chain. A signature the contract accepted but the
-- service could not verify is a discrepancy worth keeping, not a boolean.
ALTER TABLE oracle_submissions ADD COLUMN signature_valid BOOLEAN;

CREATE INDEX idx_oracle_rounds_unaggregated ON oracle_rounds(chain_id, settled_at)
    WHERE aggregated_at IS NULL AND state IN ('settled', 'failed');

CREATE INDEX idx_oracle_rounds_mismatch ON oracle_rounds(chain_id, round_id)
    WHERE value_mismatch;
