use anchor_lang::prelude::*;

use crate::error::ReceiverError;

const TOKEN_ACCOUNT_LEN: usize = 165;

/// What a registered treasury holds, as far as governance still controls it.
///
/// A token account that was closed, handed to another owner, or turned into another mint's account
/// holds nothing for governance: all of it has left. A delegate or close authority would let tokens
/// leave later, outside any execution, so one is refused outright.
pub fn token_balance(
    data: &[u8],
    owner_program_ok: bool,
    mint: &Pubkey,
    authority: &Pubkey,
) -> Result<u64> {
    if !owner_program_ok || data.len() < TOKEN_ACCOUNT_LEN {
        return Ok(0);
    }
    if data[0..32] != mint.to_bytes() || data[32..64] != authority.to_bytes() {
        return Ok(0);
    }
    require!(
        data[72..76] == [0; 4] && data[129..133] == [0; 4],
        ReceiverError::TreasuryDelegated
    );
    Ok(u64::from_le_bytes(data[64..72].try_into().unwrap()))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn account(mint: &Pubkey, owner: &Pubkey, amount: u64) -> Vec<u8> {
        let mut data = vec![0u8; TOKEN_ACCOUNT_LEN];
        data[0..32].copy_from_slice(mint.as_ref());
        data[32..64].copy_from_slice(owner.as_ref());
        data[64..72].copy_from_slice(&amount.to_le_bytes());
        data[108] = 1;
        data
    }

    #[test]
    fn a_held_token_account_reports_its_amount() {
        let (mint, authority) = (Pubkey::new_unique(), Pubkey::new_unique());
        assert_eq!(
            token_balance(&account(&mint, &authority, 77), true, &mint, &authority).unwrap(),
            77
        );
    }

    #[test]
    fn an_account_governance_no_longer_holds_counts_as_empty() {
        let (mint, authority) = (Pubkey::new_unique(), Pubkey::new_unique());
        let data = account(&mint, &authority, 77);
        assert_eq!(
            token_balance(&data, false, &mint, &authority).unwrap(),
            0,
            "closed or reassigned"
        );
        assert_eq!(
            token_balance(&[], true, &mint, &authority).unwrap(),
            0,
            "no data"
        );
        assert_eq!(
            token_balance(
                &account(&mint, &Pubkey::new_unique(), 77),
                true,
                &mint,
                &authority
            )
            .unwrap(),
            0,
            "new owner"
        );
        assert_eq!(
            token_balance(
                &account(&Pubkey::new_unique(), &authority, 77),
                true,
                &mint,
                &authority
            )
            .unwrap(),
            0,
            "other mint"
        );
    }

    #[test]
    fn a_delegate_or_close_authority_is_refused() {
        let (mint, authority) = (Pubkey::new_unique(), Pubkey::new_unique());
        let mut delegated = account(&mint, &authority, 77);
        delegated[72] = 1;
        assert!(
            token_balance(&delegated, true, &mint, &authority).is_err(),
            "delegate"
        );
        let mut closable = account(&mint, &authority, 77);
        closable[129] = 1;
        assert!(
            token_balance(&closable, true, &mint, &authority).is_err(),
            "close authority"
        );
    }
}
