use anchor_lang::prelude::*;

#[event]
pub struct ReceiverInitialized {
    pub chain_id: u64,
    pub emitter_chain: u16,
    pub emitter_address: [u8; 32],
    pub guardian: Pubkey,
    pub delay: i64,
    pub window: i64,
    pub authority: Pubkey,
}

#[event]
pub struct MessageReceived {
    pub emitter_chain: u16,
    pub sequence: u64,
    pub source_chain_id: u64,
    pub operation_id: u64,
    pub target: Pubkey,
    pub declared_value: [u8; 32],
    pub accounts_hash: [u8; 32],
    pub executable_at: i64,
}

#[event]
pub struct MessageExecuted {
    pub emitter_chain: u16,
    pub sequence: u64,
    pub source_chain_id: u64,
    pub operation_id: u64,
    pub executor: Pubkey,
}

#[event]
pub struct MessageCancelled {
    pub emitter_chain: u16,
    pub sequence: u64,
    pub source_chain_id: u64,
    pub operation_id: u64,
    pub by: Pubkey,
}

#[event]
pub struct TreasuryOutflow {
    pub mint: Pubkey,
    pub sequence: u64,
    pub amount: u64,
}

#[event]
pub struct PausedSet {
    pub paused: bool,
    pub by: Pubkey,
}

#[event]
pub struct DelayUpdated {
    pub delay: i64,
}

#[event]
pub struct WindowUpdated {
    pub window: i64,
}

#[event]
pub struct EmitterUpdated {
    pub emitter_chain: u16,
    pub emitter_address: [u8; 32],
}

#[event]
pub struct GuardianUpdated {
    pub guardian: Pubkey,
}

#[event]
pub struct ProgramAllowed {
    pub program: Pubkey,
    pub allowed: bool,
}

#[event]
pub struct TreasuryRegistered {
    pub mint: Pubkey,
    pub account: Pubkey,
    pub per_message_cap: u64,
    pub rolling_cap: u64,
}

#[event]
pub struct TreasuryDeregistered {
    pub mint: Pubkey,
}

#[event]
pub struct BootstrapEnded {}
