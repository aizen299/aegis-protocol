DROP INDEX IF EXISTS idx_oracle_rounds_mismatch;
DROP INDEX IF EXISTS idx_oracle_rounds_unaggregated;

ALTER TABLE oracle_submissions DROP COLUMN IF EXISTS signature_valid;

ALTER TABLE oracle_rounds DROP COLUMN IF EXISTS value_mismatch;
ALTER TABLE oracle_rounds DROP COLUMN IF EXISTS service_value;
ALTER TABLE oracle_rounds DROP COLUMN IF EXISTS aggregated_at;
