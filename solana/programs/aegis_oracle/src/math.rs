use crate::state::BPS_DENOMINATOR;

pub fn has_quorum(count: u64, eligible: u64, quorum_bps: u64, min_quorum_nodes: u64) -> bool {
    if count < min_quorum_nodes {
        return false;
    }
    (count as u128) * (BPS_DENOMINATOR as u128) >= (eligible as u128) * (quorum_bps as u128)
}

// For an even count, the floor of the two middle values' average, computed without overflow: the
// same rule as the Arbitrum contract's Math.average, so both chains settle identical inputs alike.
pub fn median(values: &[u128]) -> Option<u128> {
    if values.is_empty() {
        return None;
    }
    let mut sorted = values.to_vec();
    sorted.sort_unstable();
    let mid = sorted.len() / 2;
    if sorted.len() % 2 == 1 {
        return Some(sorted[mid]);
    }
    let (a, b) = (sorted[mid - 1], sorted[mid]);
    Some((a & b) + ((a ^ b) >> 1))
}

pub fn slash_cap(stake: u64, max_slash_bps: u64) -> u64 {
    ((stake as u128) * (max_slash_bps as u128) / (BPS_DENOMINATOR as u128)) as u64
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn quorum_needs_both_the_floor_and_the_share() {
        assert!(!has_quorum(2, 3, 5_000, 3));
        assert!(has_quorum(3, 5, 6_000, 3));
        assert!(!has_quorum(3, 6, 6_000, 3));
        assert!(has_quorum(u8::MAX as u64, u8::MAX as u64, 10_000, 1));
    }

    #[test]
    fn median_of_odd_and_even_counts() {
        assert_eq!(median(&[]), None);
        assert_eq!(median(&[7]), Some(7));
        assert_eq!(median(&[9, 1, 5]), Some(5));
        assert_eq!(median(&[4, 1, 3, 2]), Some(2));
        assert_eq!(median(&[4, 1, 3, 2, 8, 6]), Some(3));
        assert_eq!(median(&[u128::MAX, u128::MAX - 1]), Some(u128::MAX - 1));
        assert_eq!(median(&[u128::MAX, 0, u128::MAX]), Some(u128::MAX));
    }

    #[test]
    fn the_slash_cap_rounds_down_and_never_overflows() {
        assert_eq!(slash_cap(1_000, 1_000), 100);
        assert_eq!(slash_cap(999, 1_000), 99);
        assert_eq!(slash_cap(u64::MAX, 10_000), u64::MAX);
    }
}
