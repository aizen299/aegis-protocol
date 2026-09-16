use anchor_lang::prelude::*;

pub const CONFIG_SEED: &[u8] = b"config";
pub const STAKE_VAULT_SEED: &[u8] = b"stake_vault";
pub const NODE_SEED: &[u8] = b"node";
pub const FEED_SEED: &[u8] = b"feed";
pub const ROUND_SEED: &[u8] = b"round";
pub const SUBMISSION_SEED: &[u8] = b"submission";
pub const SLASH_SEED: &[u8] = b"slash";

pub const ACCOUNT_VERSION: u8 = 1;
pub const MAX_NODES: usize = 32;
pub const BPS_DENOMINATOR: u64 = 10_000;
pub const MIN_UNBONDING_PERIOD: i64 = 24 * 60 * 60;
pub const MAX_FEED_DECIMALS: u8 = 38;
pub const MAX_FEED_NAME_LEN: usize = 64;

pub const ROUND_OPEN: u8 = 1;
pub const ROUND_QUORUM_MET: u8 = 2;
pub const ROUND_SETTLED: u8 = 3;
pub const ROUND_FAILED: u8 = 4;

// Same bytes as the Arbitrum contract's bytes32 string literals: left-aligned ASCII, zero-padded.
pub const fn reason(s: &[u8]) -> [u8; 32] {
    let mut out = [0u8; 32];
    let mut i = 0;
    while i < s.len() {
        out[i] = s[i];
        i += 1;
    }
    out
}
pub const REASON_UNSTAKE_REQUESTED: [u8; 32] = reason(b"UNSTAKE_REQUESTED");
pub const REASON_BELOW_STAKE_FLOOR: [u8; 32] = reason(b"BELOW_STAKE_FLOOR");

// Append-only: new fields are carved from `reserved`. Checked by `make solana-layout-check`.
#[account]
#[derive(InitSpace)]
pub struct Config {
    pub version: u8,
    pub bump: u8,
    pub stake_vault_bump: u8,
    pub paused: bool,
    pub admin: Pubkey,
    pub pending_admin: Pubkey,
    pub oracle_manager: Pubkey,
    pub pauser: Pubkey,
    pub slasher: Pubkey,
    pub stake_mint: Pubkey,
    pub chain_id: i64,
    pub minimum_stake: u64,
    pub min_stake_floor: u64,
    pub unbonding_period: i64,
    pub max_slash_bps: u64,
    pub max_nodes: u8,
    pub active_node_count: u8,
    pub min_quorum_nodes: u8,
    pub quorum_bps: u64,
    pub round_duration: i64,
    pub node_set_version: u64,
    pub next_round_id: u64,
    pub reserved: [u8; 128],
}

#[account]
#[derive(InitSpace)]
pub struct Node {
    pub version: u8,
    pub bump: u8,
    pub active: bool,
    pub node: Pubkey,
    pub stake: u64,
    pub pending_unstake: u64,
    pub claimable_at: i64,
    pub slashed_total: u64,
    pub activated_at_version: u64,
    pub nonce: u64,
    pub reserved: [u8; 64],
}

#[account]
#[derive(InitSpace)]
pub struct Feed {
    pub version: u8,
    pub bump: u8,
    pub registered: bool,
    pub decimals: u8,
    pub feed_id: [u8; 32],
    pub current_round_id: u64,
    pub last_settled_round_id: u64,
    pub last_value: u128,
    pub last_settled_at: i64,
    pub reserved: [u8; 64],
}

#[account]
#[derive(InitSpace)]
pub struct Round {
    pub version: u8,
    pub bump: u8,
    pub state: u8,
    pub eligible_count: u8,
    pub submission_count: u8,
    pub round_id: u64,
    pub feed_id: [u8; 32],
    pub opened_at: i64,
    pub deadline: i64,
    pub settled_at: i64,
    pub node_set_version: u64,
    pub aggregated_value: u128,
    pub values: [u128; MAX_NODES],
    pub reserved: [u8; 32],
}

#[account]
#[derive(InitSpace)]
pub struct Submission {
    pub version: u8,
    pub bump: u8,
    pub round_id: u64,
    pub node: Pubkey,
    pub value: u128,
    pub reserved: [u8; 16],
}

#[account]
#[derive(InitSpace)]
pub struct SlashRecord {
    pub version: u8,
    pub bump: u8,
    pub round_id: u64,
    pub node: Pubkey,
    pub amount: u64,
    pub reserved: [u8; 16],
}

impl Round {
    pub fn is_live(&self) -> bool {
        self.state == ROUND_OPEN || self.state == ROUND_QUORUM_MET
    }
}

impl Node {
    // The Arbitrum rule: active now, and already active when the round froze its set.
    pub fn eligible_at(&self, version: u64) -> bool {
        self.active && self.activated_at_version <= version
    }
}
