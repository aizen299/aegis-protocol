pub const VIRTUAL_SHARES_OFFSET: u8 = 3;
pub const VIRTUAL_SHARES: u128 = 1_000;
pub const VIRTUAL_ASSETS: u128 = 1;

// Floor in both directions: a depositor never receives a share the vault did not get paid for, and a
// withdrawer never receives an asset their shares do not cover.
pub fn convert_to_shares(assets: u64, total_assets: u64, total_shares: u128) -> Option<u128> {
    mul_div_floor(
        assets as u128,
        total_shares.checked_add(VIRTUAL_SHARES)?,
        (total_assets as u128).checked_add(VIRTUAL_ASSETS)?,
    )
}

pub fn convert_to_assets(shares: u128, total_assets: u64, total_shares: u128) -> Option<u64> {
    let assets = mul_div_floor(
        shares,
        (total_assets as u128).checked_add(VIRTUAL_ASSETS)?,
        total_shares.checked_add(VIRTUAL_SHARES)?,
    )?;
    u64::try_from(assets).ok()
}

// Exact floor(a*b/d). The product can exceed u128 — shares near u64::MAX * 1000 against a large
// balance — and refusing then would lock a legitimate withdrawal.
fn mul_div_floor(a: u128, b: u128, d: u128) -> Option<u128> {
    if d == 0 {
        return None;
    }
    if let Some(p) = a.checked_mul(b) {
        return Some(p / d);
    }
    mul_div_wide(a, b, d)
}

fn mul_div_wide(a: u128, b: u128, d: u128) -> Option<u128> {
    let (hi, lo) = widening_mul(a, b);
    if hi >= d {
        return None;
    }
    let mut rem = hi;
    let mut quo: u128 = 0;
    for i in (0..128).rev() {
        let carry = rem >> 127;
        rem = (rem << 1) | ((lo >> i) & 1);
        quo <<= 1;
        if carry == 1 || rem >= d {
            rem = rem.wrapping_sub(d);
            quo |= 1;
        }
    }
    Some(quo)
}

fn widening_mul(a: u128, b: u128) -> (u128, u128) {
    const MASK: u128 = u64::MAX as u128;
    let (a_hi, a_lo) = (a >> 64, a & MASK);
    let (b_hi, b_lo) = (b >> 64, b & MASK);
    let ll = a_lo * b_lo;
    let lh = a_lo * b_hi;
    let hl = a_hi * b_lo;
    let hh = a_hi * b_hi;
    let mid = (ll >> 64) + (lh & MASK) + (hl & MASK);
    let lo = (ll & MASK) | ((mid & MASK) << 64);
    let hi = hh + (lh >> 64) + (hl >> 64) + (mid >> 64);
    (hi, lo)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn first_deposit_mints_at_the_offset() {
        assert_eq!(convert_to_shares(1_000_000, 0, 0), Some(1_000_000_000));
    }

    #[test]
    fn a_round_trip_never_returns_more_than_was_deposited() {
        let cases = [
            (1u64, 0u64, 0u128),
            (7, 1_000, 999_000),
            (123_456, 10_000_001, 9_999_999_000),
            (u64::MAX / 2, u64::MAX / 3, 1),
        ];
        for (assets, total_assets, total_shares) in cases {
            let shares = convert_to_shares(assets, total_assets, total_shares).unwrap();
            let back =
                convert_to_assets(shares, total_assets + assets, total_shares + shares).unwrap();
            assert!(back <= assets, "deposit {assets} returned {back}");
        }
    }

    // The attack: deposit 1, donate a large balance directly, and let the next depositor round to
    // zero shares. The virtual shares make the donation cost the attacker more than it takes.
    #[test]
    fn a_donation_does_not_round_the_next_depositor_to_nothing() {
        let attacker_shares = convert_to_shares(1, 0, 0).unwrap();
        let donated = 1_000_000u64;
        let victim_shares = convert_to_shares(1_000_000, 1 + donated, attacker_shares).unwrap();
        assert!(victim_shares > 0);

        let total_assets = 1 + donated + 1_000_000;
        let total_shares = attacker_shares + victim_shares;
        let attacker_out = convert_to_assets(attacker_shares, total_assets, total_shares).unwrap();
        assert!(
            attacker_out < 1 + donated,
            "attacker recovered {attacker_out} of {}",
            1 + donated
        );
    }

    #[test]
    fn a_full_u64_deposit_does_not_overflow() {
        let shares = convert_to_shares(u64::MAX, 0, 0).unwrap();
        assert_eq!(shares, u64::MAX as u128 * 1_000);
        assert_eq!(convert_to_assets(shares, u64::MAX, shares), Some(u64::MAX));
    }

    // Checked against arbitrary-precision results computed independently.
    #[test]
    fn full_width_division_is_exact() {
        let m = u128::MAX;
        assert_eq!(mul_div_floor(m, 2, 4), Some(m / 2));
        assert_eq!(mul_div_floor(m, m, m), Some(m));
        assert_eq!(mul_div_floor(m, m - 1, m), Some(m - 1));
        assert_eq!(mul_div_floor(1 << 100, 1 << 100, 1 << 80), Some(1 << 120));
        assert_eq!(
            mul_div_floor(3 << 126, 5, 7),
            Some(182294125136217033998236396838447256137)
        );
        assert_eq!(mul_div_floor(m, m, 1), None);
        assert_eq!(mul_div_floor(1, 1, 0), None);
    }

    #[test]
    fn the_wide_path_agrees_with_plain_arithmetic() {
        let mut x: u64 = 0x9E37_79B9_7F4A_7C15;
        let mut next = || {
            x ^= x << 13;
            x ^= x >> 7;
            x ^= x << 17;
            x
        };
        for _ in 0..20_000 {
            let a = next() as u128;
            let b = (next() >> (next() % 64)) as u128;
            let d = ((next() >> (next() % 64)) as u128).max(1);
            assert_eq!(mul_div_wide(a, b, d), Some(a * b / d), "a={a} b={b} d={d}");
        }
    }

    #[test]
    fn products_beyond_u128_still_divide() {
        assert_eq!(mul_div_floor(u128::MAX, 2, 4), Some(u128::MAX / 2));
        assert_eq!(
            mul_div_floor(u128::MAX, u128::MAX, u128::MAX),
            Some(u128::MAX)
        );
        assert_eq!(mul_div_floor(1, 1, 0), None);
    }

    #[test]
    fn assets_that_do_not_fit_u64_are_refused() {
        assert_eq!(convert_to_assets(u128::MAX, u64::MAX, 0), None);
    }
}
