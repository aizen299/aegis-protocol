use anchor_lang::prelude::*;

use crate::message::MAX_INSTRUCTION_DATA;
use crate::window::WINDOW_ENTRIES;

pub const CONFIG_SEED: &[u8] = b"config";
pub const AUTHORITY_SEED: &[u8] = b"authority";
pub const MESSAGE_SEED: &[u8] = b"message";
pub const ALLOWED_SEED: &[u8] = b"allowed";
pub const TREASURY_SEED: &[u8] = b"treasury";
pub const WINDOW_SEED: &[u8] = b"window";

pub const ACCOUNT_VERSION: u8 = 1;
#[cfg(not(feature = "localnet"))]
pub const MIN_DELAY: i64 = 60 * 60;
// The local validator's clock follows wall time, so an end-to-end test cannot wait out an hour.
#[cfg(feature = "localnet")]
pub const MIN_DELAY: i64 = 5;
pub const MAX_TREASURIES: u8 = 8;
/// The mint a treasury uses for the authority's own lamports.
pub const LAMPORTS_MINT: Pubkey = Pubkey::new_from_array([0; 32]);

pub const MESSAGE_PENDING: u8 = 1;
pub const MESSAGE_EXECUTED: u8 = 2;
pub const MESSAGE_CANCELLED: u8 = 3;

// Append-only: new fields are carved from `reserved`. Checked by `make solana-layout-check`.
#[account]
#[derive(InitSpace)]
pub struct Config {
    pub version: u8,
    pub bump: u8,
    pub authority_bump: u8,
    pub paused: bool,
    pub bootstrapping: bool,
    pub bootstrap_admin: Pubkey,
    pub guardian: Pubkey,
    pub chain_id: u64,
    pub emitter_chain: u16,
    pub emitter_address: [u8; 32],
    pub delay: i64,
    pub window: i64,
    pub treasury_count: u8,
    pub reserved: [u8; 128],
}

#[account]
#[derive(InitSpace)]
pub struct InboundMessage {
    pub version: u8,
    pub bump: u8,
    pub state: u8,
    pub emitter_chain: u16,
    pub sequence: u64,
    pub source_chain_id: u64,
    pub operation_id: u64,
    pub target: Pubkey,
    pub declared_value: [u8; 32],
    pub accounts_hash: [u8; 32],
    pub received_at: i64,
    pub executable_at: i64,
    pub closed_at: i64,
    pub data_len: u16,
    pub data: [u8; MAX_INSTRUCTION_DATA],
    pub reserved: [u8; 32],
}

#[account]
#[derive(InitSpace)]
pub struct Allowed {
    pub version: u8,
    pub bump: u8,
    pub allowed: bool,
    pub program: Pubkey,
    pub reserved: [u8; 32],
}

#[account]
#[derive(InitSpace)]
pub struct Treasury {
    pub version: u8,
    pub bump: u8,
    pub mint: Pubkey,
    pub account: Pubkey,
    pub per_message_cap: u64,
    pub rolling_cap: u64,
    pub reserved: [u8; 64],
}

// Kept apart from Treasury so that execution, which writes the window, never writes the caps a
// governance message may have changed during that same execution.
#[account]
#[derive(InitSpace)]
pub struct Window {
    pub version: u8,
    pub bump: u8,
    pub mint: Pubkey,
    pub times: [i64; WINDOW_ENTRIES],
    pub amounts: [u64; WINDOW_ENTRIES],
    pub reserved: [u8; 32],
}
