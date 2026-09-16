use anchor_lang::prelude::*;

#[error_code]
pub enum VaultError {
    ZeroAmount,
    DepositBelowMinimum,
    DepositCapExceeded,
    SlippageExceeded,
    InsufficientShares,
    Paused,
    WithdrawalsFrozen,
    Unauthorized,
    NoPendingAdmin,
    PositionNotEmpty,
    MathOverflow,
    NotUpgradeAuthority,
    ZeroAddress,
}
