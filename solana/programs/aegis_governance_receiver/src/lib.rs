use anchor_lang::prelude::*;
use anchor_lang::solana_program::instruction::{
    get_stack_height, Instruction, TRANSACTION_LEVEL_STACK_HEIGHT,
};
use anchor_lang::solana_program::program::{invoke, invoke_signed};
use anchor_spl::{token, token_2022};

pub mod error;
pub mod events;
pub mod message;
pub mod outflow;
pub mod state;
pub mod window;

use error::ReceiverError;
use events::*;
use message::{accounts_hash, parse_message, parse_vaa_body, vaa_digest};
use state::*;

declare_id!("GJB8hFodVCW6cBWiiaYurMdKJk98Q4SkM3AbrC3GNZL5");

/// Wormhole's verify-VAA shim, and the core bridge whose guardian set it checks. The committed
/// binaries in solana/external are the mainnet programs at these addresses. §6.
pub const WORMHOLE_VERIFY_VAA_SHIM: Pubkey =
    pubkey!("EFaNWErqAtVWufdNb7yofSHHfWFos843DFpu4JBw24at");
pub const WORMHOLE_CORE_BRIDGE: Pubkey = pubkey!("worm2ZoG2kUd4vFXhvjh93UUH596ayRfgQ2MgjNMTth");

#[derive(AnchorSerialize, AnchorDeserialize, Clone)]
pub struct InitializeArgs {
    pub chain_id: u64,
    pub emitter_chain: u16,
    pub emitter_address: [u8; 32],
    pub guardian: Pubkey,
    pub delay: i64,
    pub window: i64,
}

#[program]
pub mod aegis_governance_receiver {
    use super::*;

    pub fn initialize(ctx: Context<Initialize>, args: InitializeArgs) -> Result<()> {
        require_keys_neq!(args.guardian, Pubkey::default(), ReceiverError::ZeroAddress);
        require!(
            args.emitter_address != [0; 32] && args.chain_id != 0,
            ReceiverError::InvalidParameter
        );
        require!(args.delay >= MIN_DELAY, ReceiverError::DelayTooShort);
        require!(args.window > 0, ReceiverError::InvalidParameter);

        let config = &mut ctx.accounts.config;
        config.version = ACCOUNT_VERSION;
        config.bump = ctx.bumps.config;
        config.authority_bump = ctx.bumps.authority;
        config.paused = false;
        config.bootstrapping = true;
        config.bootstrap_admin = ctx.accounts.admin.key();
        config.guardian = args.guardian;
        config.chain_id = args.chain_id;
        config.emitter_chain = args.emitter_chain;
        config.emitter_address = args.emitter_address;
        config.delay = args.delay;
        config.window = args.window;
        config.treasury_count = 0;
        emit_cpi!(ReceiverInitialized {
            chain_id: args.chain_id,
            emitter_chain: args.emitter_chain,
            emitter_address: args.emitter_address,
            guardian: args.guardian,
            delay: args.delay,
            window: args.window,
            authority: ctx.accounts.authority.key(),
        });
        Ok(())
    }

    // --- messages ---

    /// Stores a governance message after Wormhole's guardians have signed it. The message account's
    /// address is derived from the emitter chain and sequence, so the same VAA cannot be stored twice.
    pub fn receive_message(
        ctx: Context<ReceiveMessage>,
        emitter_chain: u16,
        sequence: u64,
        guardian_set_bump: u8,
        body: Vec<u8>,
    ) -> Result<()> {
        let config = &ctx.accounts.config;
        let vaa = parse_vaa_body(&body)?;
        require!(
            vaa.emitter_chain == emitter_chain && vaa.sequence == sequence,
            ReceiverError::VaaMismatch
        );
        require!(
            vaa.emitter_chain == config.emitter_chain
                && vaa.emitter_address == config.emitter_address,
            ReceiverError::UnknownEmitter
        );
        let parsed = parse_message(vaa.payload)?;
        require!(
            parsed.target_chain_id == config.chain_id,
            ReceiverError::WrongTargetChain
        );
        require!(
            !config.paused || is_unpause(&parsed.target, parsed.instruction_data),
            ReceiverError::Paused
        );

        let mut data = anchor_discriminator("verify_hash").to_vec();
        data.push(guardian_set_bump);
        data.extend_from_slice(&vaa_digest(&body));
        invoke(
            &Instruction {
                program_id: WORMHOLE_VERIFY_VAA_SHIM,
                accounts: vec![
                    AccountMeta::new_readonly(ctx.accounts.guardian_set.key(), false),
                    AccountMeta::new_readonly(ctx.accounts.guardian_signatures.key(), false),
                ],
                data,
            },
            &[
                ctx.accounts.guardian_set.to_account_info(),
                ctx.accounts.guardian_signatures.to_account_info(),
            ],
        )?;

        let now = Clock::get()?.unix_timestamp;
        let executable_at = now
            .checked_add(config.delay)
            .ok_or(ReceiverError::InvalidParameter)?;
        let message = &mut ctx.accounts.message;
        message.version = ACCOUNT_VERSION;
        message.bump = ctx.bumps.message;
        message.state = MESSAGE_PENDING;
        message.emitter_chain = emitter_chain;
        message.sequence = sequence;
        message.source_chain_id = parsed.source_chain_id;
        message.operation_id = parsed.operation_id;
        message.target = parsed.target;
        message.declared_value = parsed.declared_value;
        message.accounts_hash = parsed.accounts_hash;
        message.received_at = now;
        message.executable_at = executable_at;
        message.closed_at = 0;
        message.data_len = parsed.instruction_data.len() as u16;
        message.data[..parsed.instruction_data.len()].copy_from_slice(parsed.instruction_data);

        emit_cpi!(MessageReceived {
            emitter_chain,
            sequence,
            source_chain_id: parsed.source_chain_id,
            operation_id: parsed.operation_id,
            target: parsed.target,
            declared_value: parsed.declared_value,
            accounts_hash: parsed.accounts_hash,
            executable_at,
        });
        Ok(())
    }

    /// Performs a message's instruction as the authority, once its delay has passed.
    ///
    /// Remaining accounts: for each registered treasury, in increasing mint order, its Treasury,
    /// Window, and watched account; then the instruction's accounts, exactly as voted.
    pub fn execute<'info>(ctx: Context<'info, Execute<'info>>) -> Result<()> {
        // A message whose instruction called execute again would run inside this one, around the
        // outflow measurement. Only a transaction-level call is accepted.
        require!(
            get_stack_height() == TRANSACTION_LEVEL_STACK_HEIGHT,
            ReceiverError::NotTopLevel
        );

        let config = &ctx.accounts.config;
        require!(
            ctx.accounts.allowed.allowed,
            ReceiverError::ProgramNotAllowed
        );
        let now = Clock::get()?.unix_timestamp;
        {
            let message = &ctx.accounts.message;
            require!(
                message.state == MESSAGE_PENDING,
                ReceiverError::MessageNotPending
            );
            require!(now >= message.executable_at, ReceiverError::DelayNotElapsed);
            require!(
                !config.paused
                    || is_unpause(&message.target, &message.data[..message.data_len as usize]),
                ReceiverError::Paused
            );
        }

        let authority = ctx.accounts.authority.key();
        let treasury_accounts = config.treasury_count as usize * 3;
        require!(
            ctx.remaining_accounts.len() >= treasury_accounts,
            ReceiverError::TreasuryListIncomplete
        );
        let (watched, instruction_accounts) = ctx.remaining_accounts.split_at(treasury_accounts);

        let mut treasuries = Vec::with_capacity(config.treasury_count as usize);
        let mut previous_mint: Option<Pubkey> = None;
        for triple in watched.chunks(3) {
            let treasury = Account::<Treasury>::try_from(&triple[0])
                .map_err(|_| ReceiverError::TreasuryMismatch)?;
            let window = Account::<Window>::try_from(&triple[1])
                .map_err(|_| ReceiverError::TreasuryMismatch)?;
            require_keys_eq!(
                triple[0].key(),
                Pubkey::create_program_address(
                    &[TREASURY_SEED, treasury.mint.as_ref(), &[treasury.bump]],
                    &crate::ID
                )
                .map_err(|_| ReceiverError::TreasuryMismatch)?,
                ReceiverError::TreasuryMismatch
            );
            require_keys_eq!(
                triple[1].key(),
                Pubkey::create_program_address(
                    &[WINDOW_SEED, treasury.mint.as_ref(), &[window.bump]],
                    &crate::ID
                )
                .map_err(|_| ReceiverError::TreasuryMismatch)?,
                ReceiverError::TreasuryMismatch
            );
            require!(triple[1].is_writable, ReceiverError::TreasuryMismatch);
            require_keys_eq!(
                triple[2].key(),
                treasury.account,
                ReceiverError::TreasuryMismatch
            );
            // Strictly increasing mints make the treasuries distinct, so none is counted twice or skipped.
            require!(
                previous_mint.is_none_or(|p| p < treasury.mint),
                ReceiverError::TreasuryMismatch
            );
            previous_mint = Some(treasury.mint);
            let before = balance(&treasury, &triple[2], &authority)?;
            treasuries.push((treasury, window, &triple[2], before));
        }

        let metas: Vec<AccountMeta> = instruction_accounts
            .iter()
            .map(|a| AccountMeta {
                pubkey: a.key(),
                is_signer: a.is_signer || a.key() == authority,
                is_writable: a.is_writable,
            })
            .collect();
        require!(
            accounts_hash(
                metas
                    .iter()
                    .map(|m| (&m.pubkey, m.is_signer, m.is_writable))
            ) == ctx.accounts.message.accounts_hash,
            ReceiverError::AccountsMismatch
        );

        let message = &mut ctx.accounts.message;
        message.state = MESSAGE_EXECUTED;
        message.closed_at = now;
        let data = message.data[..message.data_len as usize].to_vec();
        let (emitter_chain, sequence) = (message.emitter_chain, message.sequence);
        let (source_chain_id, operation_id) = (message.source_chain_id, message.operation_id);
        // Written before the call, so the call finds the message already executed.
        message.exit(&crate::ID)?;

        let mut infos = instruction_accounts.to_vec();
        infos.push(ctx.accounts.target_program.to_account_info());
        invoke_signed(
            &Instruction {
                program_id: ctx.accounts.target_program.key(),
                accounts: metas,
                data,
            },
            &infos,
            &[&[AUTHORITY_SEED, &[config.authority_bump]]],
        )?;

        for (treasury, mut window, info, before) in treasuries {
            let after = balance(&treasury, info, &authority)?;
            let outflow = before.saturating_sub(after);
            if outflow == 0 {
                continue;
            }
            require!(
                outflow <= treasury.per_message_cap,
                ReceiverError::PerMessageCapExceeded
            );
            let w = &mut *window;
            window::record(
                &mut w.times,
                &mut w.amounts,
                now,
                config.window,
                treasury.rolling_cap,
                outflow,
            )
            .map_err(|e| match e {
                window::WindowError::CapExceeded => ReceiverError::RollingCapExceeded,
                window::WindowError::WindowFull => ReceiverError::WindowFull,
            })?;
            window.exit(&crate::ID)?;
            emit_cpi!(TreasuryOutflow {
                mint: treasury.mint,
                sequence,
                amount: outflow
            });
        }

        emit_cpi!(MessageExecuted {
            emitter_chain,
            sequence,
            source_chain_id,
            operation_id,
            executor: ctx.accounts.executor.key(),
        });
        Ok(())
    }

    /// Stops a pending message for good. The guardian can, so a forged message waiting out its delay
    /// in public can be stopped rather than only delayed.
    pub fn cancel(ctx: Context<Cancel>) -> Result<()> {
        let by = ctx.accounts.signer.key();
        require!(
            by == ctx.accounts.config.guardian
                || governed(
                    &ctx.accounts.config,
                    &by,
                    ctx.accounts.config.authority_bump
                ),
            ReceiverError::Unauthorized
        );
        let message = &mut ctx.accounts.message;
        require!(
            message.state == MESSAGE_PENDING,
            ReceiverError::MessageNotPending
        );
        message.state = MESSAGE_CANCELLED;
        message.closed_at = Clock::get()?.unix_timestamp;
        emit_cpi!(MessageCancelled {
            emitter_chain: message.emitter_chain,
            sequence: message.sequence,
            source_chain_id: message.source_chain_id,
            operation_id: message.operation_id,
            by,
        });
        Ok(())
    }

    // --- pause ---

    pub fn pause(ctx: Context<Govern>) -> Result<()> {
        let by = ctx.accounts.controller.key();
        let config = &mut ctx.accounts.config;
        require!(
            by == config.guardian || governed(config, &by, config.authority_bump),
            ReceiverError::Unauthorized
        );
        require!(!config.paused, ReceiverError::Paused);
        config.paused = true;
        emit_cpi!(PausedSet { paused: true, by });
        Ok(())
    }

    // Unpausing is governance's alone: a stolen guardian key must not be able to undo a pause.
    pub fn unpause(ctx: Context<Govern>) -> Result<()> {
        let by = ctx.accounts.controller.key();
        let config = &mut ctx.accounts.config;
        require_governed(config, &by)?;
        require!(config.paused, ReceiverError::NotPaused);
        config.paused = false;
        emit_cpi!(PausedSet { paused: false, by });
        Ok(())
    }

    // --- parameters: the authority, or the upgrade authority until bootstrap ends ---

    pub fn set_delay(ctx: Context<Govern>, delay: i64) -> Result<()> {
        require_governed(&ctx.accounts.config, &ctx.accounts.controller.key())?;
        require!(delay >= MIN_DELAY, ReceiverError::DelayTooShort);
        ctx.accounts.config.delay = delay;
        emit_cpi!(DelayUpdated { delay });
        Ok(())
    }

    pub fn set_window(ctx: Context<Govern>, window: i64) -> Result<()> {
        require_governed(&ctx.accounts.config, &ctx.accounts.controller.key())?;
        require!(window > 0, ReceiverError::InvalidParameter);
        ctx.accounts.config.window = window;
        emit_cpi!(WindowUpdated { window });
        Ok(())
    }

    pub fn set_emitter(
        ctx: Context<Govern>,
        emitter_chain: u16,
        emitter_address: [u8; 32],
    ) -> Result<()> {
        require_governed(&ctx.accounts.config, &ctx.accounts.controller.key())?;
        require!(emitter_address != [0; 32], ReceiverError::InvalidParameter);
        ctx.accounts.config.emitter_chain = emitter_chain;
        ctx.accounts.config.emitter_address = emitter_address;
        emit_cpi!(EmitterUpdated {
            emitter_chain,
            emitter_address
        });
        Ok(())
    }

    pub fn set_guardian(ctx: Context<Govern>, guardian: Pubkey) -> Result<()> {
        require_governed(&ctx.accounts.config, &ctx.accounts.controller.key())?;
        require_keys_neq!(guardian, Pubkey::default(), ReceiverError::ZeroAddress);
        ctx.accounts.config.guardian = guardian;
        emit_cpi!(GuardianUpdated { guardian });
        Ok(())
    }

    pub fn set_allowed(ctx: Context<SetAllowed>, target: Pubkey, allowed: bool) -> Result<()> {
        require_governed(&ctx.accounts.config, &ctx.accounts.controller.key())?;
        let entry = &mut ctx.accounts.allowed;
        entry.version = ACCOUNT_VERSION;
        entry.bump = ctx.bumps.allowed;
        entry.program = target;
        entry.allowed = allowed;
        emit_cpi!(ProgramAllowed {
            program: target,
            allowed
        });
        Ok(())
    }

    /// Registers the account whose outflow is measured for a mint: a token account the authority
    /// owns, or, for LAMPORTS_MINT, the authority itself.
    pub fn register_treasury(
        ctx: Context<RegisterTreasury>,
        mint: Pubkey,
        per_message_cap: u64,
        rolling_cap: u64,
    ) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_governed(config, &ctx.accounts.controller.key())?;
        require!(
            config.treasury_count < MAX_TREASURIES,
            ReceiverError::InvalidParameter
        );
        require!(
            per_message_cap <= rolling_cap,
            ReceiverError::InvalidParameter
        );
        let authority = ctx.accounts.authority.key();
        let account = &ctx.accounts.account;
        if mint == LAMPORTS_MINT {
            require_keys_eq!(
                account.key(),
                authority,
                ReceiverError::InvalidTreasuryAccount
            );
        } else {
            require!(
                *account.owner == token::ID || *account.owner == token_2022::ID,
                ReceiverError::InvalidTreasuryAccount
            );
            let data = account.try_borrow_data()?;
            require!(
                data.len() >= 165
                    && data[0..32] == mint.to_bytes()
                    && data[32..64] == authority.to_bytes(),
                ReceiverError::InvalidTreasuryAccount
            );
            outflow::token_balance(&data, true, &mint, &authority)?;
        }
        config.treasury_count += 1;

        let treasury = &mut ctx.accounts.treasury;
        treasury.version = ACCOUNT_VERSION;
        treasury.bump = ctx.bumps.treasury;
        treasury.mint = mint;
        treasury.account = account.key();
        treasury.per_message_cap = per_message_cap;
        treasury.rolling_cap = rolling_cap;
        let window = &mut ctx.accounts.window;
        window.version = ACCOUNT_VERSION;
        window.bump = ctx.bumps.window;
        window.mint = mint;
        emit_cpi!(TreasuryRegistered {
            mint,
            account: account.key(),
            per_message_cap,
            rolling_cap
        });
        Ok(())
    }

    pub fn set_treasury_caps(
        ctx: Context<SetTreasuryCaps>,
        per_message_cap: u64,
        rolling_cap: u64,
    ) -> Result<()> {
        require_governed(&ctx.accounts.config, &ctx.accounts.controller.key())?;
        require!(
            per_message_cap <= rolling_cap,
            ReceiverError::InvalidParameter
        );
        let treasury = &mut ctx.accounts.treasury;
        treasury.per_message_cap = per_message_cap;
        treasury.rolling_cap = rolling_cap;
        emit_cpi!(TreasuryRegistered {
            mint: treasury.mint,
            account: treasury.account,
            per_message_cap,
            rolling_cap
        });
        Ok(())
    }

    pub fn deregister_treasury(ctx: Context<DeregisterTreasury>) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require_governed(config, &ctx.accounts.controller.key())?;
        config.treasury_count -= 1;
        emit_cpi!(TreasuryDeregistered {
            mint: ctx.accounts.treasury.mint
        });
        Ok(())
    }

    /// Ends the upgrade authority's setup powers, for good. From here only governance messages change
    /// the receiver.
    pub fn finish_bootstrap(ctx: Context<Govern>) -> Result<()> {
        let config = &mut ctx.accounts.config;
        require!(config.bootstrapping, ReceiverError::BootstrapFinished);
        require_keys_eq!(
            ctx.accounts.controller.key(),
            config.bootstrap_admin,
            ReceiverError::Unauthorized
        );
        config.bootstrapping = false;
        config.bootstrap_admin = Pubkey::default();
        emit_cpi!(BootstrapEnded {});
        Ok(())
    }
}

pub fn anchor_discriminator(name: &str) -> [u8; 8] {
    solana_sha256_hasher::hash(format!("global:{name}").as_bytes()).to_bytes()[..8]
        .try_into()
        .unwrap()
}

pub fn authority_address(bump: u8) -> Pubkey {
    Pubkey::create_program_address(&[AUTHORITY_SEED, &[bump]], &crate::ID).unwrap()
}

// Unpausing is governance's, so its message must pass through the pause it lifts. It still waits out
// the delay, and the guardian can still cancel it.
fn is_unpause(target: &Pubkey, data: &[u8]) -> bool {
    *target == crate::ID && data == anchor_discriminator("unpause")
}

fn governed(config: &Config, signer: &Pubkey, authority_bump: u8) -> bool {
    *signer == authority_address(authority_bump)
        || (config.bootstrapping && *signer == config.bootstrap_admin)
}

fn require_governed(config: &Config, signer: &Pubkey) -> Result<()> {
    require!(
        governed(config, signer, config.authority_bump),
        ReceiverError::Unauthorized
    );
    Ok(())
}

fn balance(treasury: &Treasury, info: &AccountInfo, authority: &Pubkey) -> Result<u64> {
    if treasury.mint == LAMPORTS_MINT {
        return Ok(info.lamports());
    }
    let held = *info.owner == token::ID || *info.owner == token_2022::ID;
    outflow::token_balance(&info.try_borrow_data()?, held, &treasury.mint, authority)
}

#[event_cpi]
#[derive(Accounts)]
pub struct Initialize<'info> {
    #[account(mut)]
    pub admin: Signer<'info>,

    #[account(init, payer = admin, space = 8 + Config::INIT_SPACE, seeds = [CONFIG_SEED], bump)]
    pub config: Box<Account<'info, Config>>,

    /// CHECK: the authority holds no data; it is only ever a signer, derived here for its bump.
    #[account(seeds = [AUTHORITY_SEED], bump)]
    pub authority: UncheckedAccount<'info>,

    #[account(constraint = this_program.programdata_address()? == Some(program_data.key()) @ ReceiverError::NotUpgradeAuthority)]
    pub this_program: Program<'info, crate::program::AegisGovernanceReceiver>,
    #[account(constraint = program_data.upgrade_authority_address == Some(admin.key()) @ ReceiverError::NotUpgradeAuthority)]
    pub program_data: Account<'info, ProgramData>,

    pub system_program: Program<'info, System>,
}

#[event_cpi]
#[derive(Accounts)]
#[instruction(emitter_chain: u16, sequence: u64)]
pub struct ReceiveMessage<'info> {
    #[account(mut)]
    pub payer: Signer<'info>,

    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(
        init,
        payer = payer,
        space = 8 + InboundMessage::INIT_SPACE,
        seeds = [MESSAGE_SEED, &emitter_chain.to_be_bytes(), &sequence.to_be_bytes()],
        bump,
    )]
    pub message: Box<Account<'info, InboundMessage>>,

    /// CHECK: the shim checks it is the core bridge's guardian set for the signatures' index.
    pub guardian_set: UncheckedAccount<'info>,
    /// CHECK: owned and checked by the shim.
    pub guardian_signatures: UncheckedAccount<'info>,
    /// CHECK: the address is the check.
    #[account(address = WORMHOLE_VERIFY_VAA_SHIM)]
    pub verify_vaa_shim: UncheckedAccount<'info>,

    pub system_program: Program<'info, System>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct Execute<'info> {
    pub executor: Signer<'info>,

    // Read-only: a message may change the configuration, and this copy must not be written back over it.
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(
        mut,
        seeds = [MESSAGE_SEED, &message.emitter_chain.to_be_bytes(), &message.sequence.to_be_bytes()],
        bump = message.bump,
    )]
    pub message: Box<Account<'info, InboundMessage>>,

    #[account(seeds = [ALLOWED_SEED, message.target.as_ref()], bump = allowed.bump)]
    pub allowed: Box<Account<'info, Allowed>>,

    /// CHECK: the address is the check.
    #[account(address = message.target, executable)]
    pub target_program: UncheckedAccount<'info>,

    /// CHECK: a PDA with no data, only ever a signer.
    #[account(seeds = [AUTHORITY_SEED], bump = config.authority_bump)]
    pub authority: UncheckedAccount<'info>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct Cancel<'info> {
    pub signer: Signer<'info>,

    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(
        mut,
        seeds = [MESSAGE_SEED, &message.emitter_chain.to_be_bytes(), &message.sequence.to_be_bytes()],
        bump = message.bump,
    )]
    pub message: Box<Account<'info, InboundMessage>>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct Govern<'info> {
    pub controller: Signer<'info>,

    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,
}

#[event_cpi]
#[derive(Accounts)]
#[instruction(target: Pubkey)]
pub struct SetAllowed<'info> {
    #[account(mut)]
    pub controller: Signer<'info>,

    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(
        init_if_needed,
        payer = controller,
        space = 8 + Allowed::INIT_SPACE,
        seeds = [ALLOWED_SEED, target.as_ref()],
        bump,
    )]
    pub allowed: Box<Account<'info, Allowed>>,

    pub system_program: Program<'info, System>,
}

#[event_cpi]
#[derive(Accounts)]
#[instruction(mint: Pubkey)]
pub struct RegisterTreasury<'info> {
    #[account(mut)]
    pub controller: Signer<'info>,

    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(
        init,
        payer = controller,
        space = 8 + Treasury::INIT_SPACE,
        seeds = [TREASURY_SEED, mint.as_ref()],
        bump,
    )]
    pub treasury: Box<Account<'info, Treasury>>,

    #[account(
        init,
        payer = controller,
        space = 8 + Window::INIT_SPACE,
        seeds = [WINDOW_SEED, mint.as_ref()],
        bump,
    )]
    pub window: Box<Account<'info, Window>>,

    /// CHECK: validated in the handler as the authority or a token account it owns.
    pub account: UncheckedAccount<'info>,

    /// CHECK: a PDA with no data.
    #[account(seeds = [AUTHORITY_SEED], bump = config.authority_bump)]
    pub authority: UncheckedAccount<'info>,

    pub system_program: Program<'info, System>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct SetTreasuryCaps<'info> {
    pub controller: Signer<'info>,

    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(mut, seeds = [TREASURY_SEED, treasury.mint.as_ref()], bump = treasury.bump)]
    pub treasury: Box<Account<'info, Treasury>>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct DeregisterTreasury<'info> {
    #[account(mut)]
    pub controller: Signer<'info>,

    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,

    #[account(mut, close = controller, seeds = [TREASURY_SEED, treasury.mint.as_ref()], bump = treasury.bump)]
    pub treasury: Box<Account<'info, Treasury>>,

    #[account(mut, close = controller, seeds = [WINDOW_SEED, treasury.mint.as_ref()], bump = window.bump)]
    pub window: Box<Account<'info, Window>>,
}
