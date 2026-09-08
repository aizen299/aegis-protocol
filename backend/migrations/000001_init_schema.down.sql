DROP INDEX IF EXISTS idx_vault_withdrawals_vault;
DROP INDEX IF EXISTS idx_vault_withdrawals_block;
DROP INDEX IF EXISTS idx_vault_withdrawals_user;
DROP INDEX IF EXISTS idx_vault_deposits_vault;
DROP INDEX IF EXISTS idx_vault_deposits_block;
DROP INDEX IF EXISTS idx_vault_deposits_user;

DROP TABLE IF EXISTS vault_withdrawals;
DROP TABLE IF EXISTS vault_deposits;

DROP TRIGGER IF EXISTS assets_set_updated_at ON assets;
DROP TABLE IF EXISTS assets;

DROP TRIGGER IF EXISTS indexer_cursors_set_updated_at ON indexer_cursors;
DROP TABLE IF EXISTS indexer_cursors;

DROP FUNCTION IF EXISTS set_updated_at();
