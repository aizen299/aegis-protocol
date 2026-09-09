DROP INDEX IF EXISTS idx_oracle_slashings_one_per_round;
DROP INDEX IF EXISTS idx_oracle_slashings_unconfirmed;
DROP INDEX IF EXISTS idx_oracle_slashings_claimable;

ALTER TABLE oracle_slashings DROP COLUMN IF EXISTS abandoned_reason;
ALTER TABLE oracle_slashings DROP COLUMN IF EXISTS abandoned_at;
ALTER TABLE oracle_slashings DROP COLUMN IF EXISTS attempts;
ALTER TABLE oracle_slashings DROP COLUMN IF EXISTS submitted_at;
