ALTER TABLE oracle_nodes ADD COLUMN missed_rounds INT NOT NULL DEFAULT 0;
DROP INDEX IF EXISTS oracle_rounds_unjudged;
ALTER TABLE oracle_rounds DROP COLUMN IF EXISTS misses_judged_at;
DROP TABLE IF EXISTS oracle_round_outcomes;
