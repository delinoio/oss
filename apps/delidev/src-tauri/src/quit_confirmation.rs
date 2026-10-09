// SPDX-License-Identifier: Apache-2.0
//! Bounded original-scope Quit observation decisions; no runtime authority.
use std::collections::BTreeMap;
#[derive(Debug, PartialEq, Eq)]
pub struct Summary {
    pub count: u128,
    pub unknown: bool,
}
impl Summary {
    pub fn verified_zero(&self) -> bool {
        !self.unknown && self.count == 0
    }
}
pub fn summarize(counts: impl IntoIterator<Item = Option<u64>>, complete: bool) -> Summary {
    let mut result = Summary {
        count: 0,
        unknown: !complete,
    };
    for count in counts {
        match count {
            Some(value) => result.count += u128::from(value),
            None => result.unknown = true,
        }
    }
    result
}
pub fn observers(
    scopes: impl IntoIterator<Item = (String, Option<String>, bool)>,
) -> (BTreeMap<String, String>, bool) {
    let mut selected = BTreeMap::new();
    let mut unknown = false;
    for (label, server, ready) in scopes {
        if !ready {
            unknown = true;
            continue;
        }
        if let Some(server) = server {
            selected.entry(server).or_insert(label);
        } else {
            unknown = true;
        }
    }
    (selected, unknown)
}
pub fn parse_count(value: &str) -> Result<u64, ()> {
    if value.is_empty()
        || value.len() > 20
        || !value.bytes().all(|v| v.is_ascii_digit())
        || value.len() > 1 && value.starts_with('0')
    {
        return Err(());
    }
    value.parse().map_err(|_| ())
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn only_complete_fresh_zero_admits_without_warning() {
        assert!(summarize([Some(0), Some(0)], true).verified_zero());
        for summary in [
            summarize([Some(0)], false),
            summarize([None], true),
            summarize([Some(1)], true),
        ] {
            assert!(!summary.verified_zero());
        }
    }
    #[test]
    fn positive_unknown_preserves_exact_partial_count() {
        assert_eq!(
            summarize([Some(u64::MAX), Some(1), None], true),
            Summary {
                count: u128::from(u64::MAX) + 1,
                unknown: true
            }
        );
    }
    #[test]
    fn duplicate_servers_have_one_original_observer() {
        let (selected, unknown) = observers([
            ("a".into(), Some("server".into()), true),
            ("b".into(), Some("server".into()), true),
            ("c".into(), Some("other".into()), true),
        ]);
        assert!(!unknown);
        assert_eq!(selected.len(), 2);
        assert_eq!(selected["server"], "a");
    }
    #[test]
    fn missing_and_preparing_scopes_are_unknown() {
        let (_, unknown) = observers([
            ("a".into(), None, true),
            ("b".into(), Some("server".into()), false),
        ]);
        assert!(unknown);
    }
    #[test]
    fn counts_reject_noncanonical_or_overflowing_numbers() {
        for value in ["", "-1", "+1", "01", "18446744073709551616"] {
            assert_eq!(parse_count(value), Err(()));
        }
        assert_eq!(parse_count("18446744073709551615"), Ok(u64::MAX));
    }
}
