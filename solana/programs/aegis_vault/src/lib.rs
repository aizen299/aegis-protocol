use anchor_lang::prelude::*;
use anchor_spl::token::{self, Mint, Token, TokenAccount, Transfer};

pub mod error;
pub mod events;
pub mod math;
pub mod state;

use error::VaultError;
use events::*;
use state::*;

declare_id!("h5VEwZjPpX6zug14r44i4QzpPzYBKeAHQYa5QZdzyGo");

#[program]
pub mod aegis_vault {
    use super::*;

    pub fn initialize_vault(
        ctx: Context<InitializeVault>,
        manager: Pubkey,
        pauser: Pubkey,
        deposit_cap: u64,
        min_deposit: u64,
    ) -> Result<()> {
        require_keys_neq!(manager, Pubkey::default(), VaultError::ZeroAddress);
        require_keys_neq!(pauser, Pubkey::default(), VaultError::ZeroAddress);
        let vault = &mut ctx.accounts.vault;
        vault.version = VAULT_VERSION;
        vault.bump = ctx.bumps.vault;
        vault.tokens_bump = ctx.bumps.vault_tokens;
        vault.mint = ctx.accounts.mint.key();
        vault.admin = ctx.accounts.admin.key();
        vault.pending_admin = Pubkey::default();
        vault.manager = manager;
        vault.pauser = pauser;
        vault.total_shares = 0;
        vault.deposit_cap = deposit_cap;
        vault.min_deposit = min_deposit;
        vault.paused = false;
        vault.withdrawals_frozen = false;

        emit_cpi!(VaultInitialized {
            vault: vault.key(),
            asset: vault.mint,
            admin: vault.admin,
            manager,
            pauser,
            deposit_cap,
            min_deposit,
        });
        Ok(())
    }

    // `min_shares` is the caller's floor on the exchange rate. See docs/v1.2-vault-slippage-plan.md.
    pub fn deposit(ctx: Context<Deposit>, amount: u64, min_shares: u128) -> Result<()> {
        let vault = &ctx.accounts.vault;
        require_keys_neq!(
            ctx.accounts.receiver.key(),
            Pubkey::default(),
            VaultError::ZeroAddress
        );
        require!(!vault.paused, VaultError::Paused);
        require!(amount > 0, VaultError::ZeroAmount);
        require!(amount >= vault.min_deposit, VaultError::DepositBelowMinimum);

        let assets_before = ctx.accounts.vault_tokens.amount;
        let after = assets_before
            .checked_add(amount)
            .ok_or(VaultError::MathOverflow)?;
        require!(
            vault.deposit_cap == 0 || after <= vault.deposit_cap,
            VaultError::DepositCapExceeded
        );

        let shares = math::convert_to_shares(amount, assets_before, vault.total_shares)
            .ok_or(VaultError::MathOverflow)?;
        require!(shares > 0, VaultError::ZeroAmount);
        require!(shares >= min_shares, VaultError::SlippageExceeded);

        token::transfer(
            CpiContext::new(
                ctx.accounts.token_program.key(),
                Transfer {
                    from: ctx.accounts.depositor_tokens.to_account_info(),
                    to: ctx.accounts.vault_tokens.to_account_info(),
                    authority: ctx.accounts.depositor.to_account_info(),
                },
            ),
            amount,
        )?;

        let position = &mut ctx.accounts.position;
        if position.version == 0 {
            position.version = POSITION_VERSION;
            position.bump = ctx.bumps.position;
            position.vault = ctx.accounts.vault.key();
            position.owner = ctx.accounts.receiver.key();
        }
        position.shares = position
            .shares
            .checked_add(shares)
            .ok_or(VaultError::MathOverflow)?;

        let vault = &mut ctx.accounts.vault;
        vault.total_shares = vault
            .total_shares
            .checked_add(shares)
            .ok_or(VaultError::MathOverflow)?;

        emit_cpi!(Deposited {
            user: position.owner,
            asset: vault.mint,
            amount,
            shares,
        });
        Ok(())
    }

    pub fn withdraw(ctx: Context<Withdraw>, shares: u128, min_amount: u64) -> Result<()> {
        let vault = &ctx.accounts.vault;
        require!(!vault.withdrawals_frozen, VaultError::WithdrawalsFrozen);
        require!(shares > 0, VaultError::ZeroAmount);
        require!(
            ctx.accounts.position.shares >= shares,
            VaultError::InsufficientShares
        );

        let amount =
            math::convert_to_assets(shares, ctx.accounts.vault_tokens.amount, vault.total_shares)
                .ok_or(VaultError::MathOverflow)?;
        require!(amount > 0, VaultError::ZeroAmount);
        require!(amount >= min_amount, VaultError::SlippageExceeded);

        ctx.accounts.position.shares -= shares;
        let vault = &mut ctx.accounts.vault;
        vault.total_shares = vault
            .total_shares
            .checked_sub(shares)
            .ok_or(VaultError::MathOverflow)?;

        let mint = vault.mint;
        let seeds: &[&[u8]] = &[VAULT_SEED, mint.as_ref(), &[vault.bump]];
        token::transfer(
            CpiContext::new_with_signer(
                ctx.accounts.token_program.key(),
                Transfer {
                    from: ctx.accounts.vault_tokens.to_account_info(),
                    to: ctx.accounts.receiver_tokens.to_account_info(),
                    authority: ctx.accounts.vault.to_account_info(),
                },
                &[seeds],
            ),
            amount,
        )?;

        emit_cpi!(Withdrawn {
            user: ctx.accounts.owner.key(),
            asset: mint,
            amount,
            shares,
        });
        Ok(())
    }

    pub fn close_position(ctx: Context<ClosePosition>) -> Result<()> {
        require!(
            ctx.accounts.position.shares == 0,
            VaultError::PositionNotEmpty
        );
        Ok(())
    }

    pub fn set_deposit_cap(ctx: Context<Manage>, new_cap: u64) -> Result<()> {
        let vault = &mut ctx.accounts.vault;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            vault.manager,
            VaultError::Unauthorized
        );
        let previous_cap = vault.deposit_cap;
        vault.deposit_cap = new_cap;
        emit_cpi!(DepositCapUpdated {
            asset: vault.mint,
            previous_cap,
            new_cap
        });
        Ok(())
    }

    pub fn set_min_deposit(ctx: Context<Manage>, new_min: u64) -> Result<()> {
        let vault = &mut ctx.accounts.vault;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            vault.manager,
            VaultError::Unauthorized
        );
        let previous_min = vault.min_deposit;
        vault.min_deposit = new_min;
        emit_cpi!(MinDepositUpdated {
            asset: vault.mint,
            previous_min,
            new_min
        });
        Ok(())
    }

    pub fn set_paused(ctx: Context<Manage>, paused: bool) -> Result<()> {
        let vault = &mut ctx.accounts.vault;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            vault.pauser,
            VaultError::Unauthorized
        );
        vault.paused = paused;
        emit_cpi!(PausedSet {
            asset: vault.mint,
            paused
        });
        Ok(())
    }

    pub fn set_withdrawals_frozen(ctx: Context<Manage>, frozen: bool) -> Result<()> {
        let vault = &mut ctx.accounts.vault;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            vault.admin,
            VaultError::Unauthorized
        );
        vault.withdrawals_frozen = frozen;
        emit_cpi!(WithdrawalsFrozenSet {
            asset: vault.mint,
            frozen
        });
        Ok(())
    }

    pub fn set_role(ctx: Context<Manage>, role: Role, account: Pubkey) -> Result<()> {
        let vault = &mut ctx.accounts.vault;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            vault.admin,
            VaultError::Unauthorized
        );
        require_keys_neq!(account, Pubkey::default(), VaultError::ZeroAddress);
        match role {
            Role::Manager => vault.manager = account,
            Role::Pauser => vault.pauser = account,
            // The admin changes hands only through propose and accept.
            Role::Admin => return err!(VaultError::Unauthorized),
        }
        emit_cpi!(RoleUpdated {
            asset: vault.mint,
            role,
            account
        });
        Ok(())
    }

    pub fn propose_admin(ctx: Context<Manage>, proposed: Pubkey) -> Result<()> {
        let vault = &mut ctx.accounts.vault;
        require_keys_eq!(
            ctx.accounts.signer.key(),
            vault.admin,
            VaultError::Unauthorized
        );
        require_keys_neq!(proposed, Pubkey::default(), VaultError::ZeroAddress);
        vault.pending_admin = proposed;
        emit_cpi!(AdminProposed {
            asset: vault.mint,
            proposed
        });
        Ok(())
    }

    pub fn accept_admin(ctx: Context<Manage>) -> Result<()> {
        let vault = &mut ctx.accounts.vault;
        let pending = vault.pending_admin;
        require_keys_neq!(pending, Pubkey::default(), VaultError::NoPendingAdmin);
        require_keys_eq!(ctx.accounts.signer.key(), pending, VaultError::Unauthorized);
        vault.admin = pending;
        vault.pending_admin = Pubkey::default();
        emit_cpi!(RoleUpdated {
            asset: vault.mint,
            role: Role::Admin,
            account: pending
        });
        Ok(())
    }
}

#[event_cpi]
#[derive(Accounts)]
pub struct InitializeVault<'info> {
    #[account(mut)]
    pub admin: Signer<'info>,

    #[account(
        init,
        payer = admin,
        space = 8 + Vault::INIT_SPACE,
        seeds = [VAULT_SEED, mint.key().as_ref()],
        bump,
    )]
    pub vault: Account<'info, Vault>,

    #[account(
        init,
        payer = admin,
        seeds = [TOKENS_SEED, vault.key().as_ref()],
        bump,
        token::mint = mint,
        token::authority = vault,
    )]
    pub vault_tokens: Account<'info, TokenAccount>,

    // Account<Mint> checks the mint is owned by the classic token program, which refuses Token-2022.
    pub mint: Account<'info, Mint>,

    // A vault created by anyone else could name its own admin before the real one is set up.
    #[account(constraint = this_program.programdata_address()? == Some(program_data.key()) @ VaultError::NotUpgradeAuthority)]
    pub this_program: Program<'info, crate::program::AegisVault>,
    #[account(constraint = program_data.upgrade_authority_address == Some(admin.key()) @ VaultError::NotUpgradeAuthority)]
    pub program_data: Account<'info, ProgramData>,

    pub token_program: Program<'info, Token>,
    pub system_program: Program<'info, System>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct Deposit<'info> {
    #[account(mut)]
    pub depositor: Signer<'info>,

    /// CHECK: any account may receive shares; it is only a seed and the recorded owner.
    pub receiver: UncheckedAccount<'info>,

    #[account(mut, seeds = [VAULT_SEED, vault.mint.as_ref()], bump = vault.bump)]
    pub vault: Account<'info, Vault>,

    #[account(mut, seeds = [TOKENS_SEED, vault.key().as_ref()], bump = vault.tokens_bump)]
    pub vault_tokens: Account<'info, TokenAccount>,

    #[account(mut, token::mint = vault.mint, token::authority = depositor)]
    pub depositor_tokens: Account<'info, TokenAccount>,

    #[account(
        init_if_needed,
        payer = depositor,
        space = 8 + Position::INIT_SPACE,
        seeds = [POSITION_SEED, vault.key().as_ref(), receiver.key().as_ref()],
        bump,
    )]
    pub position: Account<'info, Position>,

    pub token_program: Program<'info, Token>,
    pub system_program: Program<'info, System>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct Withdraw<'info> {
    pub owner: Signer<'info>,

    #[account(mut, seeds = [VAULT_SEED, vault.mint.as_ref()], bump = vault.bump)]
    pub vault: Account<'info, Vault>,

    #[account(mut, seeds = [TOKENS_SEED, vault.key().as_ref()], bump = vault.tokens_bump)]
    pub vault_tokens: Account<'info, TokenAccount>,

    #[account(mut, token::mint = vault.mint)]
    pub receiver_tokens: Account<'info, TokenAccount>,

    #[account(
        mut,
        seeds = [POSITION_SEED, vault.key().as_ref(), owner.key().as_ref()],
        bump = position.bump,
        has_one = owner,
        has_one = vault,
    )]
    pub position: Account<'info, Position>,

    pub token_program: Program<'info, Token>,
}

#[derive(Accounts)]
pub struct ClosePosition<'info> {
    #[account(mut)]
    pub owner: Signer<'info>,

    #[account(seeds = [VAULT_SEED, vault.mint.as_ref()], bump = vault.bump)]
    pub vault: Account<'info, Vault>,

    #[account(
        mut,
        close = owner,
        seeds = [POSITION_SEED, vault.key().as_ref(), owner.key().as_ref()],
        bump = position.bump,
        has_one = owner,
        has_one = vault,
    )]
    pub position: Account<'info, Position>,
}

#[event_cpi]
#[derive(Accounts)]
pub struct Manage<'info> {
    pub signer: Signer<'info>,

    #[account(mut, seeds = [VAULT_SEED, vault.mint.as_ref()], bump = vault.bump)]
    pub vault: Account<'info, Vault>,
}
