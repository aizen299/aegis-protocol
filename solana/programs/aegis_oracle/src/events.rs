use anchor_lang::prelude::*;

// Names and fields follow contracts/src/oracle/interfaces, so the indexer's oracle handlers take
// both chains' events. docs/v2.0-solana-plan.md §12.7.

#[event]
pub struct FeedRegistered {
    pub feed_id: [u8; 32],
    pub name: String,
    pub decimals: u8,
}

#[event]
pub struct FeedDeregistered {
    pub feed_id: [u8; 32],
}

#[event]
pub struct RoundStarted {
    pub round_id: u64,
    pub feed_id: [u8; 32],
    pub opened_at: i64,
    pub deadline: i64,
    pub eligible_count: u64,
    pub node_set_version: u64,
}

#[event]
pub struct SubmissionReceived {
    pub round_id: u64,
    pub node: Pubkey,
    pub value: u128,
    pub nonce: u64,
    pub submission_count: u64,
    pub signature: [u8; 64],
}

#[event]
pub struct RoundQuorumMet {
    pub round_id: u64,
    pub submission_count: u64,
    pub eligible_count: u64,
}

#[event]
pub struct RoundSettled {
    pub round_id: u64,
    pub feed_id: [u8; 32],
    pub aggregated_value: u128,
    pub submission_count: u64,
}

#[event]
pub struct RoundFailed {
    pub round_id: u64,
    pub feed_id: [u8; 32],
    pub submission_count: u64,
    pub eligible_count: u64,
}

#[event]
pub struct NodeRegistered {
    pub node: Pubkey,
    pub stake: u64,
}

#[event]
pub struct NodeStaked {
    pub node: Pubkey,
    pub amount: u64,
    pub total_stake: u64,
}

#[event]
pub struct UnstakeRequested {
    pub node: Pubkey,
    pub amount: u64,
    pub claimable_at: i64,
}

#[event]
pub struct UnstakeCancelled {
    pub node: Pubkey,
    pub amount: u64,
}

#[event]
pub struct NodeUnstaked {
    pub node: Pubkey,
    pub amount: u64,
    pub remaining_stake: u64,
}

#[event]
pub struct NodeSlashed {
    pub node: Pubkey,
    pub round_id: u64,
    pub amount: u64,
    pub reason: [u8; 32],
    pub remaining_stake: u64,
}

#[event]
pub struct NodeDeactivated {
    pub node: Pubkey,
    pub reason: [u8; 32],
    pub node_set_version: u64,
}

#[event]
pub struct NodeReactivated {
    pub node: Pubkey,
    pub node_set_version: u64,
}

#[event]
pub struct MinimumStakeUpdated {
    pub previous_value: u64,
    pub new_value: u64,
}

#[event]
pub struct MinStakeFloorUpdated {
    pub previous_value: u64,
    pub new_value: u64,
}

#[event]
pub struct UnbondingPeriodUpdated {
    pub previous_value: u64,
    pub new_value: u64,
}

#[event]
pub struct MaxNodesUpdated {
    pub previous_value: u64,
    pub new_value: u64,
}

#[event]
pub struct RoundDurationUpdated {
    pub previous_value: u64,
    pub new_value: u64,
}

#[event]
pub struct QuorumBpsUpdated {
    pub previous_value: u64,
    pub new_value: u64,
}

#[event]
pub struct MinQuorumNodesUpdated {
    pub previous_value: u64,
    pub new_value: u64,
}

#[event]
pub struct RoleUpdated {
    pub role: Role,
    pub account: Pubkey,
}

#[event]
pub struct AdminProposed {
    pub proposed: Pubkey,
}

#[event]
pub struct PausedSet {
    pub paused: bool,
}

#[derive(AnchorSerialize, AnchorDeserialize, Clone, Copy, PartialEq, Eq, Debug)]
pub enum Role {
    Admin,
    OracleManager,
    Pauser,
    Slasher,
}
