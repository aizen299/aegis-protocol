use anchor_lang::prelude::*;
use anchor_spl::token::{self, Mint, Token, TokenAccount, Transfer};

pub mod error;
pub mod events;
pub mod math;
pub mod signature;
pub mod state;

use error::OracleError;
use events::*;
use state::*;

declare_id!("6GcRqNzxauK3JLP1EUMMTTQ9ja8qcgHVAznS1nKZjnc7");

#[derive(AnchorSerialize, AnchorDeserialize, Clone)]
pub struct InitializeArgs {
    pub chain_id: i64,
    pub oracle_manager: Pubkey,
    pub pauser: Pubkey,
    pub slasher: Pubkey,
    pub minimum_stake: u64,
    pub min_stake_floor: u64,
    pub unbonding_period: i64,
    pub max_slash_bps: u64,
    pub max_nodes: u8,
    pub round_duration: i64,
    pub quorum_bps: u64,
    pub min_quorum_nodes: u8,
}

#[derive(AnchorSerialize, AnchorDeserialize, Clone, Copy, Debug, PartialEq, Eq)]
pub struct ValueView {
    pub value: u128,
    pub settled_at: i64,
    pub round_id: u64,
}

#[program]
pub mod aegis_oracle {
    use super::*;

    pub fn initialize(ctx: Context<Initialize>, args: InitializeArgs) -> Result<()> {
        for key in [args.oracle_manager, args.pauser, args.slasher] {
            require_keys_neq!(key, Pubkey::default(), OracleError::ZeroAddress);
        }
        require!(
            args.max_slash_bps > 0 && args.max_slash_bps <= BPS_DENOMINATOR,
            OracleError::InvalidBps
        );
        require!(
            args.quorum_bps > 0 && args.quorum_bps <= BPS_DENOMINATOR,
            OracleError::InvalidBps
        );
        require!(
            args.unbonding_period >= MIN_UNBONDING_PERIOD,
            OracleError::UnbondingPeriodTooShort
        );
        require!(
            args.min_stake_floor <= args.minimum_stake,
            OracleError::StakeBelowMinimum
        );
        require!(
            args.max_nodes > 0 && args.max_nodes as usize <= MAX_NODES,
            OracleError::InvalidParameter
        );
        require!(
            args.round_duration > 0 && args.min_quorum_nodes > 0,
            OracleError::ZeroValue
        );

        let config = &mut ctx.accounts.config;
        config.version = ACCOUNT_VERSION;
        config.bump = ctx.bumps.config;
        config.stake_vault_bump = ctx.bumps.stake_vault;
        config.paused = false;
        config.admin = ctx.accounts.admin.key();
        config.pending_admin = Pubkey::default();
        config.oracle_manager = args.oracle_manager;
        config.pauser = args.pauser;
        config.slasher = args.slasher;
        config.stake_mint = ctx.accounts.stake_mint.key();
        config.chain_id = args.chain_id;
        config.minimum_stake = args.minimum_stake;
        config.min_stake_floor = args.min_stake_floor;
        config.unbonding_period = args.unbonding_period;
        config.max_slash_bps = args.max_slash_bps;
        config.max_nodes = args.max_nodes;
        config.active_node_count = 0;
        config.min_quorum_nodes = args.min_quorum_nodes;
        config.quorum_bps = args.quorum_bps;
        config.round_duration = args.round_duration;
        config.node_set_version = 0;
        config.next_round_id = 1;
        Ok(())
    }

    // --- feeds ---

    pub fn register_feed(
        ctx: Context<RegisterFeed>,
        feed_id: [u8; 32],
        name: String,
        decimals: u8,
    ) -> Result<()> {
        require_keys_eq!(
            ctx.accounts.manager.key(),
            ctx.accounts.config.oracle_manager,
            OracleError::Unauthorized
        );
        require!(
            name.len() <= MAX_FEED_NAME_LEN,
            OracleError::FeedNameTooLong
        );
        require!(decimals <= MAX_FEED_DECIMALS, OracleError::InvalidDecimals);

        let feed = &mut ctx.accounts.feed;
        require!(!feed.registered, OracleError::FeedAlreadyRegistered);
        if feed.version == 0 {
            feed.version = ACCOUNT_VERSION;
            feed.bump = ctx.bumps.feed;
            feed.feed_id = feed_id;
        }
        feed.registered = true;
        feed.decimals = decimals;
        emit_cpi!(FeedRegistered {
            feed_id,
            name,
            decimals
        });
        Ok(())
    }

    pub fn deregister_feed(ctx: Context<DeregisterFeed>) -> Result<()> {
        require_keys_eq!(
            ctx.accounts.manager.key(),
            ctx.accounts.config.oracle_manager,
            OracleError::Unauthorized
        );
        let feed = &mut ctx.accounts.feed;
        require!(feed.registered, OracleError::FeedNotRegistered);
        feed.registered = false;
        emit_cpi!(FeedDeregistered {
            feed_id: feed.feed_id
        });
        Ok(())
    }

    // --- staking ---

    pub fn register(ctx: Context<Register>, amount: u64) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require!(
            amount >= config.minimum_stake && amount > 0,
            OracleError::StakeBelowMinimum
        );
        require!(
            config.active_node_count < config.max_nodes,
            OracleError::NodeSetFull
        );

        transfer_in(
            &ctx.accounts.token_program,
            &ctx.accounts.node_tokens,
            &ctx.accounts.stake_vault,
            &ctx.accounts.signer,
            amount,
        )?;

        let node = &mut ctx.accounts.node;
        node.version = ACCOUNT_VERSION;
        node.bump = ctx.bumps.node;
        node.node = ctx.accounts.signer.key();
        node.stake = amount;
        let version = activate(config, node)?;

        // The order the Arbitrum contract emits them in: activation happens first.
        emit_cpi!(NodeReactivated {
            node: node.node,
            node_set_version: version
        });
        emit_cpi!(NodeRegistered {
            node: node.node,
            stake: amount
        });
        emit_cpi!(NodeStaked {
            node: node.node,
            amount,
            total_stake: amount
        });
        Ok(())
    }

    pub fn stake(ctx: Context<StakeMore>, amount: u64) -> Result<()> {
        require!(amount > 0, OracleError::ZeroValue);
        transfer_in(
            &ctx.accounts.token_program,
            &ctx.accounts.node_tokens,
            &ctx.accounts.stake_vault,
            &ctx.accounts.signer,
            amount,
        )?;

        let config = &mut ctx.accounts.config;
        let node = &mut ctx.accounts.node;
        node.stake = node
            .stake
            .checked_add(amount)
            .ok_or(OracleError::MathOverflow)?;
        let key = node.node;
        let total_stake = node.stake;

        // Topping back above the minimum reactivates a node under the floor, if there is room.
        let mut reactivated = None;
        if !node.active
            && node.pending_unstake == 0
            && node.stake >= config.minimum_stake
            && config.active_node_count < config.max_nodes
        {
            reactivated = Some(activate(config, node)?);
        }

        emit_cpi!(NodeStaked {
            node: key,
            amount,
            total_stake
        });
        if let Some(version) = reactivated {
            emit_cpi!(NodeReactivated {
                node: key,
                node_set_version: version
            });
        }
        Ok(())
    }

    pub fn request_unstake(ctx: Context<NodeAction>, amount: u64) -> Result<()> {
        let config = &mut ctx.accounts.config;
        let node = &mut ctx.accounts.node;
        require!(amount > 0, OracleError::ZeroValue);
        require!(
            node.pending_unstake == 0,
            OracleError::UnstakeAlreadyRequested
        );
        require!(amount <= node.stake, OracleError::InsufficientStake);

        node.pending_unstake = amount;
        node.claimable_at = now()?
            .checked_add(config.unbonding_period)
            .ok_or(OracleError::MathOverflow)?;
        let key = node.node;
        let claimable_at = node.claimable_at;

        let mut deactivated = None;
        if node.active {
            deactivated = Some(deactivate_node(config, node)?);
        }
        if let Some(version) = deactivated {
            emit_cpi!(NodeDeactivated {
                node: key,
                reason: REASON_UNSTAKE_REQUESTED,
                node_set_version: version
            });
        }
        emit_cpi!(UnstakeRequested {
            node: key,
            amount,
            claimable_at
        });
        Ok(())
    }

    pub fn cancel_unstake(ctx: Context<NodeAction>) -> Result<()> {
        let config = &mut ctx.accounts.config;
        let node = &mut ctx.accounts.node;
        require!(node.pending_unstake > 0, OracleError::NoUnstakeRequested);

        let cancelled = node.pending_unstake;
        node.pending_unstake = 0;
        node.claimable_at = 0;
        let key = node.node;

        let mut reactivated = None;
        if !node.active
            && node.stake >= config.minimum_stake
            && config.active_node_count < config.max_nodes
        {
            reactivated = Some(activate(config, node)?);
        }
        if let Some(version) = reactivated {
            emit_cpi!(NodeReactivated {
                node: key,
                node_set_version: version
            });
        }
        emit_cpi!(UnstakeCancelled {
            node: key,
            amount: cancelled
        });
        Ok(())
    }

    // A slash during unbonding can leave less than was requested; only what remains is released.
    pub fn complete_unstake(ctx: Context<CompleteUnstake>) -> Result<()> {
        let node = &mut ctx.accounts.node;
        require!(node.pending_unstake > 0, OracleError::NoUnstakeRequested);
        require!(
            now()? >= node.claimable_at,
            OracleError::UnbondingNotElapsed
        );

        let amount = node.pending_unstake.min(node.stake);
        node.pending_unstake = 0;
        node.claimable_at = 0;
        node.stake -= amount;
        let key = node.node;
        let remaining_stake = node.stake;

        if amount > 0 {
            let config = &ctx.accounts.config;
            let seeds: &[&[u8]] = &[CONFIG_SEED, &[config.bump]];
            token::transfer(
                CpiContext::new_with_signer(
                    ctx.accounts.token_program.key(),
                    Transfer {
                        from: ctx.accounts.stake_vault.to_account_info(),
                        to: ctx.accounts.node_tokens.to_account_info(),
                        authority: ctx.accounts.config.to_account_info(),
                    },
                    &[seeds],
                ),
                amount,
            )?;
        }
        emit_cpi!(NodeUnstaked {
            node: key,
            amount,
            remaining_stake
        });
        Ok(())
    }

    // --- rounds ---

    pub fn open_round(ctx: Context<OpenRound>) -> Result<()> {
        let previous_live = ctx.accounts.feed.current_round_id != 0 && ctx.accounts.round_is_live();
        let config = &mut ctx.accounts.config;
        let feed = &mut ctx.accounts.feed;
        require!(!config.paused, OracleError::Paused);
        require!(feed.registered, OracleError::FeedNotRegistered);
        require!(!previous_live, OracleError::RoundAlreadyOpen);
        require!(
            config.active_node_count >= config.min_quorum_nodes,
            OracleError::NotEnoughEligibleNodes
        );

        let round_id = config.next_round_id;
        config.next_round_id = round_id.checked_add(1).ok_or(OracleError::MathOverflow)?;
        let opened_at = now()?;
        let deadline = opened_at
            .checked_add(config.round_duration)
            .ok_or(OracleError::MathOverflow)?;

        let round = &mut ctx.accounts.round;
        round.version = ACCOUNT_VERSION;
        round.bump = ctx.bumps.round;
        round.state = ROUND_OPEN;
        round.eligible_count = config.active_node_count;
        round.submission_count = 0;
        round.round_id = round_id;
        round.feed_id = feed.feed_id;
        round.opened_at = opened_at;
        round.deadline = deadline;
        round.node_set_version = config.node_set_version;
        feed.current_round_id = round_id;

        emit_cpi!(RoundStarted {
            round_id,
            feed_id: feed.feed_id,
            opened_at,
            deadline,
            eligible_count: config.active_node_count as u64,
            node_set_version: config.node_set_version,
        });
        Ok(())
    }

    pub fn submit(ctx: Context<Submit>, value: u128, nonce: u64) -> Result<()> {
        let config = &ctx.accounts.config;
        let round = &ctx.accounts.round;
        let node = &ctx.accounts.node;
        require!(!config.paused, OracleError::Paused);
        require!(
            round.is_live() && now()? <= round.deadline,
            OracleError::RoundNotOpen
        );
        require!(value > 0, OracleError::ZeroValue);
        // Judged against the frozen set: a node that joined after the round opened is not in the
        // denominator and must not add to the numerator.
        require!(
            node.eligible_at(round.node_set_version),
            OracleError::NotEligible
        );
        require!(nonce == node.nonce, OracleError::InvalidNonce);
        require!(
            (round.submission_count as usize) < MAX_NODES,
            OracleError::TooManySubmissions
        );

        let message = signature::submission_message(
            &crate::ID,
            config.chain_id,
            round.round_id,
            &round.feed_id,
            value,
            &node.node,
            nonce,
        );
        let sig = verify_previous_ed25519(&ctx.accounts.instructions, &node.node, &message)?;

        let node = &mut ctx.accounts.node;
        node.nonce = nonce.checked_add(1).ok_or(OracleError::MathOverflow)?;

        let submission = &mut ctx.accounts.submission;
        submission.version = ACCOUNT_VERSION;
        submission.bump = ctx.bumps.submission;
        submission.round_id = ctx.accounts.round.round_id;
        submission.node = node.node;
        submission.value = value;

        let config = &ctx.accounts.config;
        let round = &mut ctx.accounts.round;
        let slot = round.submission_count as usize;
        round.values[slot] = value;
        round.submission_count += 1;
        let count = round.submission_count as u64;

        emit_cpi!(SubmissionReceived {
            round_id: round.round_id,
            node: node.node,
            value,
            nonce,
            submission_count: count,
            signature: sig,
        });

        if round.state == ROUND_OPEN
            && math::has_quorum(
                count,
                round.eligible_count as u64,
                config.quorum_bps,
                config.min_quorum_nodes as u64,
            )
        {
            round.state = ROUND_QUORUM_MET;
            emit_cpi!(RoundQuorumMet {
                round_id: round.round_id,
                submission_count: count,
                eligible_count: round.eligible_count as u64
            });
        }
        Ok(())
    }

    // Permissionless. A round with quorum settles at any time; one without waits for its deadline.
    pub fn settle_round(ctx: Context<SettleRound>) -> Result<()> {
        let config = &ctx.accounts.config;
        let round = &mut ctx.accounts.round;
        let feed = &mut ctx.accounts.feed;
        require!(round.is_live(), OracleError::RoundNotOpen);

        let count = round.submission_count as u64;
        let eligible = round.eligible_count as u64;
        let now = now()?;

        if !math::has_quorum(
            count,
            eligible,
            config.quorum_bps,
            config.min_quorum_nodes as u64,
        ) {
            require!(now > round.deadline, OracleError::RoundStillOpen);
            round.state = ROUND_FAILED;
            round.settled_at = now;
            emit_cpi!(RoundFailed {
                round_id: round.round_id,
                feed_id: round.feed_id,
                submission_count: count,
                eligible_count: eligible
            });
            return Ok(());
        }

        let value = math::median(&round.values[..count as usize]).ok_or(OracleError::ZeroValue)?;
        round.state = ROUND_SETTLED;
        round.settled_at = now;
        round.aggregated_value = value;
        feed.last_settled_round_id = round.round_id;
        feed.last_value = value;
        feed.last_settled_at = now;

        emit_cpi!(RoundSettled {
            round_id: round.round_id,
            feed_id: round.feed_id,
            aggregated_value: value,
            submission_count: count
        });
        Ok(())
    }

    // There is no latest-answer call: a reader states how old is too old. §12.6.
    pub fn get_value(ctx: Context<GetValue>, max_staleness: i64) -> Result<ValueView> {
        let feed = &ctx.accounts.feed;
        require!(feed.registered, OracleError::FeedNotRegistered);
        require!(feed.last_settled_round_id != 0, OracleError::NoSettledRound);
        require!(max_staleness >= 0, OracleError::InvalidParameter);
        let age = now()?
            .checked_sub(feed.last_settled_at)
            .ok_or(OracleError::MathOverflow)?;
        require!(age <= max_staleness, OracleError::StaleValue);
        Ok(ValueView {
            value: feed.last_value,
            settled_at: feed.last_settled_at,
            round_id: feed.last_settled_round_id,
        })
    }

    // --- slashing ---

    // The backend decides whether to slash; the program decides what is survivable. The slash record
    // makes a second penalty for the same round fail, so a retry after a lost confirmation is safe.
    pub fn slash(ctx: Context<Slash>, round_id: u64, amount: u64, reason: [u8; 32]) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_keys_eq!(
            ctx.accounts.slasher.key(),
            config.slasher,
            OracleError::Unauthorized
        );
        require!(amount > 0, OracleError::ZeroValue);
        require!(round_id > 0, OracleError::InvalidRound);

        let node = &mut ctx.accounts.node;
        let cap = math::slash_cap(node.stake, config.max_slash_bps);
        require!(amount <= cap, OracleError::SlashExceedsCap);

        let slashed = amount.min(node.stake);
        node.stake -= slashed;
        node.slashed_total = node
            .slashed_total
            .checked_add(slashed)
            .ok_or(OracleError::MathOverflow)?;
        let key = node.node;
        let remaining_stake = node.stake;

        let record = &mut ctx.accounts.slash_record;
        record.version = ACCOUNT_VERSION;
        record.bump = ctx.bumps.slash_record;
        record.round_id = round_id;
        record.node = key;
        record.amount = slashed;

        let mut deactivated = None;
        if node.active && node.stake < config.min_stake_floor {
            deactivated = Some(deactivate_node(config, node)?);
        }
        if let Some(version) = deactivated {
            emit_cpi!(NodeDeactivated {
                node: key,
                reason: REASON_BELOW_STAKE_FLOOR,
                node_set_version: version
            });
        }
        emit_cpi!(NodeSlashed {
            node: key,
            round_id,
            amount: slashed,
            reason,
            remaining_stake
        });
        Ok(())
    }

    // --- admin ---

    pub fn deactivate(ctx: Context<AdminNode>, reason: [u8; 32]) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            config.admin,
            OracleError::Unauthorized
        );
        let node = &mut ctx.accounts.node;
        require!(node.active, OracleError::NodeInactive);
        let key = node.node;
        let version = deactivate_node(config, node)?;
        emit_cpi!(NodeDeactivated {
            node: key,
            reason,
            node_set_version: version
        });
        Ok(())
    }

    pub fn set_paused(ctx: Context<Manage>, paused: bool) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            config.pauser,
            OracleError::Unauthorized
        );
        config.paused = paused;
        emit_cpi!(PausedSet { paused });
        Ok(())
    }

    pub fn set_role(ctx: Context<Manage>, role: Role, account: Pubkey) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            config.admin,
            OracleError::Unauthorized
        );
        require_keys_neq!(account, Pubkey::default(), OracleError::ZeroAddress);
        match role {
            Role::OracleManager => config.oracle_manager = account,
            Role::Pauser => config.pauser = account,
            Role::Slasher => config.slasher = account,
            Role::Admin => return err!(OracleError::Unauthorized),
        }
        emit_cpi!(RoleUpdated { role, account });
        Ok(())
    }

    pub fn propose_admin(ctx: Context<Manage>, proposed: Pubkey) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            config.admin,
            OracleError::Unauthorized
        );
        require_keys_neq!(proposed, Pubkey::default(), OracleError::ZeroAddress);
        config.pending_admin = proposed;
        emit_cpi!(AdminProposed { proposed });
        Ok(())
    }

    pub fn accept_admin(ctx: Context<Manage>) -> Result<()> {
        let config = &mut ctx.accounts.config;
        let pending = config.pending_admin;
        require_keys_neq!(pending, Pubkey::default(), OracleError::NoPendingAdmin);
        require_keys_eq!(
            ctx.accounts.signer.key(),
            pending,
            OracleError::Unauthorized
        );
        config.admin = pending;
        config.pending_admin = Pubkey::default();
        emit_cpi!(RoleUpdated {
            role: Role::Admin,
            account: pending
        });
        Ok(())
    }

    pub fn set_minimum_stake(ctx: Context<Manage>, value: u64) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            config.admin,
            OracleError::Unauthorized
        );
        require!(
            value >= config.min_stake_floor,
            OracleError::StakeBelowMinimum
        );
        let previous_value = std::mem::replace(&mut config.minimum_stake, value);
        emit_cpi!(MinimumStakeUpdated {
            previous_value,
            new_value: value
        });
        Ok(())
    }

    pub fn set_min_stake_floor(ctx: Context<Manage>, value: u64) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            config.admin,
            OracleError::Unauthorized
        );
        require!(
            value <= config.minimum_stake,
            OracleError::StakeBelowMinimum
        );
        let previous_value = std::mem::replace(&mut config.min_stake_floor, value);
        emit_cpi!(MinStakeFloorUpdated {
            previous_value,
            new_value: value
        });
        Ok(())
    }

    pub fn set_unbonding_period(ctx: Context<Manage>, value: i64) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            config.admin,
            OracleError::Unauthorized
        );
        require!(
            value >= MIN_UNBONDING_PERIOD,
            OracleError::UnbondingPeriodTooShort
        );
        let previous_value = std::mem::replace(&mut config.unbonding_period, value);
        emit_cpi!(UnbondingPeriodUpdated {
            previous_value: previous_value as u64,
            new_value: value as u64
        });
        Ok(())
    }

    pub fn set_max_nodes(ctx: Context<Manage>, value: u8) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            config.admin,
            OracleError::Unauthorized
        );
        require!(
            value > 0 && value as usize <= MAX_NODES,
            OracleError::InvalidParameter
        );
        let previous_value = std::mem::replace(&mut config.max_nodes, value);
        emit_cpi!(MaxNodesUpdated {
            previous_value: previous_value as u64,
            new_value: value as u64
        });
        Ok(())
    }

    pub fn set_round_duration(ctx: Context<Manage>, value: i64) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            config.oracle_manager,
            OracleError::Unauthorized
        );
        require!(value > 0, OracleError::ZeroValue);
        let previous_value = std::mem::replace(&mut config.round_duration, value);
        emit_cpi!(RoundDurationUpdated {
            previous_value: previous_value as u64,
            new_value: value as u64
        });
        Ok(())
    }

    pub fn set_quorum_bps(ctx: Context<Manage>, value: u64) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            config.oracle_manager,
            OracleError::Unauthorized
        );
        require!(
            value > 0 && value <= BPS_DENOMINATOR,
            OracleError::InvalidBps
        );
        let previous_value = std::mem::replace(&mut config.quorum_bps, value);
        emit_cpi!(QuorumBpsUpdated {
            previous_value,
            new_value: value
        });
        Ok(())
    }

    pub fn set_min_quorum_nodes(ctx: Context<Manage>, value: u8) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            config.oracle_manager,
            OracleError::Unauthorized
        );
        require!(value > 0, OracleError::ZeroValue);
        let previous_value = std::mem::replace(&mut config.min_quorum_nodes, value);
        emit_cpi!(MinQuorumNodesUpdated {
            previous_value: previous_value as u64,
            new_value: value as u64
        });
        Ok(())
    }
}

fn now() -> Result<i64> {
    Ok(Clock::get()?.unix_timestamp)
}

// Every activation checks the cap, including reactivations: the Round's fixed value array holds
// MAX_NODES entries, and an activation past max_nodes would let a round overflow it.
fn activate(config: &mut Config, node: &mut Node) -> Result<u64> {
    require!(
        config.active_node_count < config.max_nodes,
        OracleError::NodeSetFull
    );
    config.active_node_count += 1;
    config.node_set_version = config
        .node_set_version
        .checked_add(1)
        .ok_or(OracleError::MathOverflow)?;
    node.active = true;
    node.activated_at_version = config.node_set_version;
    Ok(config.node_set_version)
}

fn deactivate_node(config: &mut Config, node: &mut Node) -> Result<u64> {
    config.active_node_count -= 1;
    config.node_set_version = config
        .node_set_version
        .checked_add(1)
        .ok_or(OracleError::MathOverflow)?;
    node.active = false;
    Ok(config.node_set_version)
}

fn transfer_in<'info>(
    token_program: &Program<'info, Token>,
    from: &Account<'info, TokenAccount>,
    to: &Account<'info, TokenAccount>,
    authority: &Signer<'info>,
    amount: u64,
) -> Result<()> {
    token::transfer(
        CpiContext::new(
            token_program.key(),
            Transfer {
                from: from.to_account_info(),
                to: to.to_account_info(),
                authority: authority.to_account_info(),
            },
        ),
        amount,
    )
}

// The attestation must be verified by the Ed25519 program in the instruction immediately before this
// one. Reading it back through the instructions sysvar is where Solana programs have been exploited,
// so every way the check could read the wrong bytes is refused. §12.3.
fn verify_previous_ed25519(
    instructions: &UncheckedAccount,
    signer: &Pubkey,
    message: &[u8],
) -> Result<[u8; 64]> {
    let info = instructions.to_account_info();
    let current = solana_instructions_sysvar::load_current_index_checked(&info)?;
    require!(current > 0, OracleError::InvalidSignatureInstruction);
    let previous =
        solana_instructions_sysvar::load_instruction_at_checked(current as usize - 1, &info)?;
    signature::verified_signature(
        &previous.program_id,
        previous.accounts.len(),
        &previous.data,
        signer,
        message,
    )
}

#[derive(Accounts)]
pub struct Initialize<'info> {
    #[account(mut)]
    pub admin: Signer<'info>,

    #[account(init, payer = admin, space = 8 + Config::INIT_SPACE, seeds = [CONFIG_SEED], bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(
        init,
        payer = admin,
        seeds = [STAKE_VAULT_SEED],
        bump,
        token::mint = stake_mint,
        token::authority = config,
    )]
    pub stake_vault: Account<'info, TokenAccount>,

    // Account<Mint> checks the classic token program owns it, which refuses Token-2022.
    pub stake_mint: Account<'info, Mint>,

    #[account(constraint = this_program.programdata_address()? == Some(program_data.key()) @ OracleError::NotUpgradeAuthority)]
    pub this_program: Program<'info, crate::program::AegisOracle>,
    #[account(constraint = program_data.upgrade_authority_address == Some(admin.key()) @ OracleError::NotUpgradeAuthority)]
    pub program_data: Account<'info, ProgramData>,

    pub token_program: Program<'info, Token>,
    pub system_program: Program<'info, System>,
}

#[event_cpi]
#[derive(Accounts)]
#[instruction(feed_id: [u8; 32])]
pub struct RegisterFeed<'info> {
    #[account(mut)]
    pub manager: Signer<'info>,

    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(
        init_if_needed,
        payer = manager,
        space = 8 + Feed::INIT_SPACE,
        seeds = [FEED_SEED, feed_id.as_ref()],
        bump,
    )]
    pub feed: Account<'info, Feed>,

    pub system_program: Program<'info, System>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct DeregisterFeed<'info> {
    pub manager: Signer<'info>,

    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(mut, seeds = [FEED_SEED, feed.feed_id.as_ref()], bump = feed.bump)]
    pub feed: Account<'info, Feed>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct Register<'info> {
    #[account(mut)]
    pub signer: Signer<'info>,

    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(init, payer = signer, space = 8 + Node::INIT_SPACE, seeds = [NODE_SEED, signer.key().as_ref()], bump)]
    pub node: Account<'info, Node>,

    #[account(mut, token::mint = config.stake_mint, token::authority = signer)]
    pub node_tokens: Account<'info, TokenAccount>,

    #[account(mut, seeds = [STAKE_VAULT_SEED], bump = config.stake_vault_bump)]
    pub stake_vault: Account<'info, TokenAccount>,

    pub token_program: Program<'info, Token>,
    pub system_program: Program<'info, System>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct StakeMore<'info> {
    pub signer: Signer<'info>,

    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(mut, seeds = [NODE_SEED, signer.key().as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,

    #[account(mut, token::mint = config.stake_mint, token::authority = signer)]
    pub node_tokens: Account<'info, TokenAccount>,

    #[account(mut, seeds = [STAKE_VAULT_SEED], bump = config.stake_vault_bump)]
    pub stake_vault: Account<'info, TokenAccount>,

    pub token_program: Program<'info, Token>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct NodeAction<'info> {
    pub signer: Signer<'info>,

    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(mut, seeds = [NODE_SEED, signer.key().as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct CompleteUnstake<'info> {
    pub signer: Signer<'info>,

    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(mut, seeds = [NODE_SEED, signer.key().as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,

    #[account(mut, token::mint = config.stake_mint)]
    pub node_tokens: Account<'info, TokenAccount>,

    #[account(mut, seeds = [STAKE_VAULT_SEED], bump = config.stake_vault_bump)]
    pub stake_vault: Account<'info, TokenAccount>,

    pub token_program: Program<'info, Token>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct OpenRound<'info> {
    #[account(mut)]
    pub payer: Signer<'info>,

    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(mut, seeds = [FEED_SEED, feed.feed_id.as_ref()], bump = feed.bump)]
    pub feed: Account<'info, Feed>,

    /// CHECK: the feed's current round, read only for liveness. Its address is derived from the
    /// feed's own record, so no other account can stand in for it.
    #[account(seeds = [ROUND_SEED, feed.current_round_id.to_le_bytes().as_ref()], bump)]
    pub current_round: UncheckedAccount<'info>,

    #[account(
        init,
        payer = payer,
        space = 8 + Round::INIT_SPACE,
        seeds = [ROUND_SEED, config.next_round_id.to_le_bytes().as_ref()],
        bump,
    )]
    pub round: Box<Account<'info, Round>>,

    pub system_program: Program<'info, System>,
}

impl OpenRound<'_> {
    fn round_is_live(&self) -> bool {
        let data = self.current_round.try_borrow_data();
        match data {
            Ok(data) => Round::try_deserialize(&mut &data[..])
                .map(|r| r.is_live())
                .unwrap_or(true),
            Err(_) => true,
        }
    }
}

#[event_cpi]
#[derive(Accounts)]
pub struct Submit<'info> {
    #[account(mut)]
    pub signer: Signer<'info>,

    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(mut, seeds = [NODE_SEED, signer.key().as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,

    #[account(mut, seeds = [ROUND_SEED, round.round_id.to_le_bytes().as_ref()], bump = round.bump)]
    pub round: Box<Account<'info, Round>>,

    #[account(
        init,
        payer = signer,
        space = 8 + Submission::INIT_SPACE,
        seeds = [SUBMISSION_SEED, round.round_id.to_le_bytes().as_ref(), signer.key().as_ref()],
        bump,
    )]
    pub submission: Account<'info, Submission>,

    /// CHECK: the load_*_checked calls verify this is the instructions sysvar.
    #[account(address = solana_sdk_ids::sysvar::instructions::ID)]
    pub instructions: UncheckedAccount<'info>,

    pub system_program: Program<'info, System>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct SettleRound<'info> {
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(mut, seeds = [ROUND_SEED, round.round_id.to_le_bytes().as_ref()], bump = round.bump)]
    pub round: Box<Account<'info, Round>>,

    #[account(mut, seeds = [FEED_SEED, round.feed_id.as_ref()], bump = feed.bump)]
    pub feed: Account<'info, Feed>,
}

#[derive(Accounts)]
pub struct GetValue<'info> {
    #[account(seeds = [FEED_SEED, feed.feed_id.as_ref()], bump = feed.bump)]
    pub feed: Account<'info, Feed>,
}

#[event_cpi]
#[derive(Accounts)]
#[instruction(round_id: u64)]
pub struct Slash<'info> {
    #[account(mut)]
    pub slasher: Signer<'info>,

    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(mut, seeds = [NODE_SEED, node.node.as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,

    #[account(
        init,
        payer = slasher,
        space = 8 + SlashRecord::INIT_SPACE,
        seeds = [SLASH_SEED, round_id.to_le_bytes().as_ref(), node.node.as_ref()],
        bump,
    )]
    pub slash_record: Account<'info, SlashRecord>,

    pub system_program: Program<'info, System>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct AdminNode<'info> {
    pub signer: Signer<'info>,

    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(mut, seeds = [NODE_SEED, node.node.as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct Manage<'info> {
    pub signer: Signer<'info>,

    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,
}
