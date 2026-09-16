use anchor_lang::prelude::*;

#[event]
pub struct VaultInitialized {
    pub vault: Pubkey,
    pub asset: Pubkey,
    pub admin: Pubkey,
    pub manager: Pubkey,
    pub pauser: Pubkey,
    pub deposit_cap: u64,
    pub min_deposit: u64,
}

#[event]
pub struct Deposited {
    pub user: Pubkey,
    pub asset: Pubkey,
    pub amount: u64,
    pub shares: u128,
}

#[event]
pub struct Withdrawn {
    pub user: Pubkey,
    pub asset: Pubkey,
    pub amount: u64,
    pub shares: u128,
}

#[event]
pub struct DepositCapUpdated {
    pub asset: Pubkey,
    pub previous_cap: u64,
    pub new_cap: u64,
}

#[event]
pub struct MinDepositUpdated {
    pub asset: Pubkey,
    pub previous_min: u64,
    pub new_min: u64,
}

#[event]
pub struct PausedSet {
    pub asset: Pubkey,
    pub paused: bool,
}

#[event]
pub struct WithdrawalsFrozenSet {
    pub asset: Pubkey,
    pub frozen: bool,
}

#[event]
pub struct RoleUpdated {
    pub asset: Pubkey,
    pub role: Role,
    pub account: Pubkey,
}

#[event]
pub struct AdminProposed {
    pub asset: Pubkey,
    pub proposed: Pubkey,
}

#[derive(AnchorSerialize, AnchorDeserialize, Clone, Copy, PartialEq, Eq, Debug)]
pub enum Role {
    Admin,
    Manager,
    Pauser,
}
