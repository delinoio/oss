// SPDX-License-Identifier: Apache-2.0
//! Closed numeric intent policy. External CEF pages receive no product bridge.
pub fn numeric_intent(
    key: i32,
    modifiers: u32,
    mac: bool,
    raw: bool,
    composing: bool,
) -> Option<u8> {
    const SHIFT: u32 = 2;
    const CONTROL: u32 = 4;
    const ALT: u32 = 8;
    const COMMAND: u32 = 128;
    const REPEAT: u32 = 8192;
    if !raw || composing || modifiers & (SHIFT | ALT | REPEAT) != 0 {
        return None;
    }
    let primary = if mac { COMMAND } else { CONTROL };
    let other = if mac { CONTROL } else { COMMAND };
    if modifiers & primary == 0 || modifiers & other != 0 || !(49..=57).contains(&key) {
        return None;
    }
    Some((key - 48) as u8)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn exact_primary_positions() {
        for key in 49..=57 {
            assert_eq!(
                numeric_intent(key, 128, true, true, false),
                Some((key - 48) as u8)
            );
            assert_eq!(
                numeric_intent(key, 4, false, true, false),
                Some((key - 48) as u8)
            );
        }
        for flags in [0, 4, 128 | 4, 128 | 2, 128 | 8, 128 | 8192] {
            assert_eq!(numeric_intent(49, flags, true, true, false), None);
        }
        assert_eq!(numeric_intent(49, 128, true, false, false), None);
        assert_eq!(numeric_intent(49, 128, true, true, true), None);
        assert_eq!(numeric_intent(48, 128, true, true, false), None);
    }
}
