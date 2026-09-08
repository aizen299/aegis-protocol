DROP INDEX IF EXISTS idx_oracle_slashings_unexecuted;
DROP INDEX IF EXISTS idx_oracle_slashings_node;
DROP INDEX IF EXISTS idx_oracle_submissions_node;
DROP INDEX IF EXISTS idx_oracle_submissions_round;
DROP INDEX IF EXISTS idx_oracle_rounds_block;
DROP INDEX IF EXISTS idx_oracle_rounds_feed_state;
DROP INDEX IF EXISTS idx_oracle_nodes_active;
DROP INDEX IF EXISTS idx_oracle_feeds_active;

DROP TABLE IF EXISTS oracle_slashings;
DROP TABLE IF EXISTS oracle_submissions;

DROP TRIGGER IF EXISTS oracle_rounds_set_updated_at ON oracle_rounds;
DROP TABLE IF EXISTS oracle_rounds;

DROP TRIGGER IF EXISTS oracle_nodes_set_updated_at ON oracle_nodes;
DROP TABLE IF EXISTS oracle_nodes;

DROP TRIGGER IF EXISTS oracle_feeds_set_updated_at ON oracle_feeds;
DROP TABLE IF EXISTS oracle_feeds;
