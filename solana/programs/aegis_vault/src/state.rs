use anchor_lang::prelude::*;

pub const VAULT_SEED: &[u8] = b"vault";
pub const TOKENS_SEED: &[u8] = b"tokens";
pub const POSITION_SEED: &[u8] = b"position";

pub const VAULT_VERSION: u8 = 1;
pub const POSITION_VERSION: u8 = 1;

// Append-only: new fields are carved from `reserved`, never inserted. Checked against
// solana/layouts/ by `make solana-layout-check`.
#[account]
#[derive(InitSpace)]
pub struct Vault {
    pub version: u8,
    pub bump: u8,
    pub tokens_bump: u8,
    pub mint: Pubkey,
    pub admin: Pubkey,
    // Pubkey::default() when none: an Option would make every later field's offset depend on it.
    pub pending_admin: Pubkey,
    pub manager: Pubkey,
    pub pauser: Pubkey,
    pub total_shares: u128,
    pub deposit_cap: u64,
    pub min_deposit: u64,
    pub paused: bool,
    pub withdrawals_frozen: bool,
    pub reserved: [u8; 128],
}

#[account]
#[derive(InitSpace)]
pub struct Position {
    pub version: u8,
    pub bump: u8,
    pub vault: Pubkey,
    pub owner: Pubkey,
    pub shares: u128,
    pub reserved: [u8; 32],
}
