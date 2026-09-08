DROP INDEX IF EXISTS idx_vaults_asset;

ALTER TABLE vault_withdrawals DROP CONSTRAINT IF EXISTS vault_withdrawals_vault_fkey;
ALTER TABLE vault_deposits DROP CONSTRAINT IF EXISTS vault_deposits_vault_fkey;

DROP TRIGGER IF EXISTS vaults_set_updated_at ON vaults;
DROP TABLE IF EXISTS vaults;
