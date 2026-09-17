pub const WINDOW_ENTRIES: usize = 16;

/// Decides whether an outflow fits a rolling cap, given the last outflows and their times.
///
/// Exact over the window: every recorded outflow newer than `now - window` counts. A refilling bucket
/// would let twice the cap through across a window boundary. When every slot holds an outflow still
/// inside the window, a further outflow is refused rather than evicting one that still counts.
pub fn record(
    times: &mut [i64; WINDOW_ENTRIES],
    amounts: &mut [u64; WINDOW_ENTRIES],
    now: i64,
    window: i64,
    rolling_cap: u64,
    outflow: u64,
) -> Result<(), WindowError> {
    let since = now.saturating_sub(window);
    let mut in_window: u128 = 0;
    let mut free = None;
    for i in 0..WINDOW_ENTRIES {
        if amounts[i] > 0 && times[i] > since {
            in_window += amounts[i] as u128;
        } else if free.is_none() {
            free = Some(i);
        }
    }
    if outflow == 0 {
        return Ok(());
    }
    if in_window + outflow as u128 > rolling_cap as u128 {
        return Err(WindowError::CapExceeded);
    }
    let slot = free.ok_or(WindowError::WindowFull)?;
    times[slot] = now;
    amounts[slot] = outflow;
    Ok(())
}

#[derive(Debug, PartialEq, Eq)]
pub enum WindowError {
    CapExceeded,
    WindowFull,
}

#[cfg(test)]
mod tests {
    use super::*;

    const DAY: i64 = 86_400;

    fn empty() -> ([i64; WINDOW_ENTRIES], [u64; WINDOW_ENTRIES]) {
        ([0; WINDOW_ENTRIES], [0; WINDOW_ENTRIES])
    }

    #[test]
    fn outflows_inside_the_window_add_up_to_the_cap() {
        let (mut t, mut a) = empty();
        assert!(record(&mut t, &mut a, 1_000, DAY, 100, 60).is_ok());
        assert!(record(&mut t, &mut a, 1_010, DAY, 100, 40).is_ok());
        assert_eq!(
            record(&mut t, &mut a, 1_020, DAY, 100, 1),
            Err(WindowError::CapExceeded)
        );
    }

    // The boundary a refilling bucket gets wrong: the whole cap spent just before a window ends, then
    // the whole cap again just after, is refused until the first outflow is a full window old.
    #[test]
    fn the_cap_holds_across_a_window_boundary() {
        let (mut t, mut a) = empty();
        let start = 10 * DAY;
        record(&mut t, &mut a, start, DAY, 100, 100).unwrap();
        assert_eq!(
            record(&mut t, &mut a, start + DAY - 1, DAY, 100, 100),
            Err(WindowError::CapExceeded)
        );
        assert!(
            record(&mut t, &mut a, start + DAY, DAY, 100, 100).is_ok(),
            "a full window later"
        );
    }

    #[test]
    fn a_full_window_refuses_rather_than_forgetting_an_outflow_that_still_counts() {
        let (mut t, mut a) = empty();
        for i in 0..WINDOW_ENTRIES as i64 {
            record(&mut t, &mut a, 1_000 + i, DAY, 1_000_000, 1).unwrap();
        }
        assert_eq!(
            record(&mut t, &mut a, 2_000, DAY, 1_000_000, 1),
            Err(WindowError::WindowFull)
        );
        assert!(
            record(&mut t, &mut a, 1_000 + DAY + 1, DAY, 1_000_000, 1).is_ok(),
            "the oldest has expired"
        );
    }

    #[test]
    fn a_zero_outflow_records_nothing_and_always_fits() {
        let (mut t, mut a) = empty();
        record(&mut t, &mut a, 5, DAY, 10, 10).unwrap();
        assert!(record(&mut t, &mut a, 6, DAY, 10, 0).is_ok());
        assert_eq!(a.iter().filter(|x| **x > 0).count(), 1);
    }
}
