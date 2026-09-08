-- Vault metadata, completing the pattern assets and oracle_feeds already follow.
--
-- A share is not a token: its scale is the asset's decimals plus the vault's virtual-shares offset,
-- and that offset is a property of the deployed contract. The API previously served a share amount
-- with no scale at all, because the backend had no way to know it. This is that way.

CREATE TABLE vaults (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    address       TEXT NOT NULL,
    asset_address TEXT NOT NULL,
    share_offset  SMALLINT NOT NULL CHECK (share_offset >= 0 AND share_offset <= 38),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, address),
    FOREIGN KEY (chain_id, asset_address) REFERENCES assets(chain_id, address)
);

CREATE TRIGGER vaults_set_updated_at
    BEFORE UPDATE ON vaults
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Shares get the same guarantee amounts have: they cannot be recorded without the metadata that
-- makes them readable.
ALTER TABLE vault_deposits
    ADD CONSTRAINT vault_deposits_vault_fkey
    FOREIGN KEY (chain_id, vault_address) REFERENCES vaults(chain_id, address);

ALTER TABLE vault_withdrawals
    ADD CONSTRAINT vault_withdrawals_vault_fkey
    FOREIGN KEY (chain_id, vault_address) REFERENCES vaults(chain_id, address);

CREATE INDEX idx_vaults_asset ON vaults(chain_id, asset_address);
