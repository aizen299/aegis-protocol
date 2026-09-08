-- v0.1 schema: indexer state, asset metadata, and vault event history.
-- Oracle (v0.2), governance (v0.3), and zk (v0.4) tables land with their own migrations.
--
-- Every chain-derived row carries chain_id and every uniqueness constraint includes it: a tx hash
-- is unique within a chain, not across chains. See docs/database.md "Multi-Chain Readiness".

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE indexer_cursors (
    id            BIGSERIAL PRIMARY KEY,
    service_name  TEXT NOT NULL,
    chain_id      BIGINT NOT NULL,
    last_block    BIGINT NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (service_name, chain_id)
);

CREATE TRIGGER indexer_cursors_set_updated_at
    BEFORE UPDATE ON indexer_cursors
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Decimals are a property of the asset, not of the protocol. Amounts elsewhere are stored in raw
-- base units exactly as the chain reported them; this table is what makes them interpretable.
-- An amount is meaningless without its asset row, which is why the vault tables carry a foreign
-- key to it rather than a plain address column.
CREATE TABLE assets (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    address       TEXT NOT NULL,
    decimals      SMALLINT NOT NULL CHECK (decimals >= 0 AND decimals <= 38),
    symbol        TEXT,
    name          TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, address)
);

CREATE TRIGGER assets_set_updated_at
    BEFORE UPDATE ON assets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Identity columns are TEXT holding the chain's canonical encoding (0x-hex on EVM, base58 on
-- Solana). Not BYTEA(20), and no CHECK on hex length.
--
-- amount and shares are NUMERIC(78,0): a uint256 in raw base units, unscaled. Scaling at write
-- time would bake one token's decimals into every row and silently corrupt any asset that does
-- not match. Callers scale on read using assets.decimals.
CREATE TABLE vault_deposits (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    user_address  TEXT NOT NULL,
    asset_address TEXT NOT NULL,
    vault_address TEXT NOT NULL,
    amount        NUMERIC(78, 0) NOT NULL CHECK (amount >= 0),
    shares        NUMERIC(78, 0) NOT NULL CHECK (shares >= 0),
    tx_hash       TEXT NOT NULL,
    log_index     INT NOT NULL,
    block_number  BIGINT NOT NULL,
    deposited_at  TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- One transaction can emit several Deposited events (a router depositing for multiple users,
    -- or two vaults touched in one call), so log_index is part of the identity of a row.
    UNIQUE (chain_id, tx_hash, log_index),
    FOREIGN KEY (chain_id, asset_address) REFERENCES assets(chain_id, address)
);

CREATE TABLE vault_withdrawals (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    user_address  TEXT NOT NULL,
    asset_address TEXT NOT NULL,
    vault_address TEXT NOT NULL,
    amount        NUMERIC(78, 0) NOT NULL CHECK (amount >= 0),
    shares        NUMERIC(78, 0) NOT NULL CHECK (shares >= 0),
    tx_hash       TEXT NOT NULL,
    log_index     INT NOT NULL,
    block_number  BIGINT NOT NULL,
    withdrawn_at  TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, tx_hash, log_index),
    FOREIGN KEY (chain_id, asset_address) REFERENCES assets(chain_id, address)
);

-- chain_id leads every index on chain-derived data: queries are always chain-scoped, and a leading
-- chain_id keeps them selective once a second chain is indexed.
CREATE INDEX idx_vault_deposits_user ON vault_deposits(chain_id, user_address);
CREATE INDEX idx_vault_deposits_block ON vault_deposits(chain_id, block_number);
CREATE INDEX idx_vault_deposits_vault ON vault_deposits(chain_id, vault_address);
CREATE INDEX idx_vault_withdrawals_user ON vault_withdrawals(chain_id, user_address);
CREATE INDEX idx_vault_withdrawals_block ON vault_withdrawals(chain_id, block_number);
CREATE INDEX idx_vault_withdrawals_vault ON vault_withdrawals(chain_id, vault_address);
