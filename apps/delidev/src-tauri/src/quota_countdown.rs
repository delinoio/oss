// SPDX-License-Identifier: Apache-2.0
//! Quota reset presentation only. No observation or account authority.
use std::time::{SystemTime, UNIX_EPOCH};

use crate::{
    language::{SupportedLanguage, active},
    localization::{Message, format_in},
};

pub fn now_millis() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|value| value.as_millis() as i64)
        .unwrap_or_else(|error| -(error.duration().as_millis() as i64))
}

/// Validate the original RFC3339 wall date before computing elapsed time.
/// Parsing here must not normalize impossible dates into countdown evidence.
fn instant(value: &str) -> Option<i64> {
    if !value.is_ascii() || !(20..=35).contains(&value.len()) {
        return None;
    }
    let bytes = value.as_bytes();
    if bytes[4] != b'-'
        || bytes[7] != b'-'
        || bytes[10] != b'T'
        || bytes[13] != b':'
        || bytes[16] != b':'
    {
        return None;
    }
    let number = |start: usize, end: usize| -> Option<i64> {
        let part = value.get(start..end)?;
        part.bytes()
            .all(|byte| byte.is_ascii_digit())
            .then(|| part.parse().ok())
            .flatten()
    };
    let year = number(0, 4)?;
    let month = number(5, 7)?;
    let day = number(8, 10)?;
    let hour = number(11, 13)?;
    let minute = number(14, 16)?;
    let second = number(17, 19)?;
    let leap = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);
    let month_days = match month {
        1 | 3 | 5 | 7 | 8 | 10 | 12 => 31,
        4 | 6 | 9 | 11 => 30,
        2 => {
            if leap {
                29
            } else {
                28
            }
        }
        _ => return None,
    };
    if day < 1 || day > month_days || hour > 23 || minute > 59 || second > 59 {
        return None;
    }
    let mut end = 19;
    let mut millis = 0;
    if bytes.get(end) == Some(&b'.') {
        end += 1;
        let start = end;
        while bytes.get(end).is_some_and(u8::is_ascii_digit) {
            end += 1;
        }
        if !(1..=9).contains(&(end - start)) {
            return None;
        }
        let fraction = &value[start..end];
        millis = format!("{fraction:0<3}")[..3].parse().ok()?;
    }
    let offset = match value.get(end..)? {
        "Z" => 0,
        zone if zone.len() == 6
            && matches!(zone.as_bytes()[0], b'+' | b'-')
            && zone.as_bytes()[3] == b':' =>
        {
            let hours = number(end + 1, end + 3)?;
            let minutes = number(end + 4, end + 6)?;
            if hours > 23 || minutes > 59 {
                return None;
            }
            (hours * 60 + minutes) * 60 * if zone.as_bytes()[0] == b'+' { 1 } else { -1 }
        }
        _ => return None,
    };
    // Gregorian days from civil date, including year zero, relative to Unix.
    let adjusted_year = year - i64::from(month <= 2);
    let era = adjusted_year.div_euclid(400);
    let yoe = adjusted_year - era * 400;
    let shifted_month = month + if month > 2 { -3 } else { 9 };
    let doy = (153 * shifted_month + 2) / 5 + day - 1;
    let days = era * 146097 + yoe * 365 + yoe / 4 - yoe / 100 + doy - 719468;
    Some((days * 86400 + hour * 3600 + minute * 60 + second - offset) * 1000 + millis)
}

fn countdown(remaining: i64, language: SupportedLanguage) -> String {
    let days = remaining / 86400000;
    let hours = remaining / 3600000 % 24;
    let minutes = remaining / 60000 % 60;
    let key = if days > 0 {
        match (days == 1, hours) {
            (true, 0) => Message::ResetDay,
            (false, 0) => Message::ResetDays,
            (true, 1) => Message::ResetDayHour,
            (true, _) => Message::ResetDayHours,
            (false, 1) => Message::ResetDaysHour,
            (false, _) => Message::ResetDaysHours,
        }
    } else if hours > 0 {
        match (hours == 1, minutes) {
            (true, 0) => Message::ResetHour,
            (false, 0) => Message::ResetHours,
            (true, 1) => Message::ResetHourMinute,
            (true, _) => Message::ResetHourMinutes,
            (false, 1) => Message::ResetHoursMinute,
            (false, _) => Message::ResetHoursMinutes,
        }
    } else {
        match minutes {
            0 => Message::ResetSoon,
            1 => Message::ResetMinute,
            _ => Message::ResetMinutes,
        }
    };
    format_in(
        key,
        &[
            ("days", &days.to_string()),
            ("hours", &hours.to_string()),
            ("minutes", &minutes.to_string()),
        ],
        language,
    )
}

pub fn reset(value: &str, now: i64) -> String {
    reset_in(value, now, active())
}
fn reset_in(value: &str, now: i64, language: SupportedLanguage) -> String {
    if let Some(remaining) = instant(value)
        .map(|instant| instant - now)
        .filter(|remaining| *remaining > 0)
    {
        return countdown(remaining, language);
    }
    // Expiry does not prove quota recovery. Retain the original date fallback.
    let at = crate::localization::date_in(value, language);
    format_in(Message::ResetAt, &[("at", &at)], language)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn thresholds_plurals_and_flooring_match_both_catalogs() {
        for (seconds, en, ko) in [
            (529200, "Resets in 6 days 3 hours", "6일 3시간 뒤 리셋"),
            (86400, "Resets in 1 day", "1일 뒤 리셋"),
            (
                86340,
                "Resets in 23 hours 59 minutes",
                "23시간 59분 뒤 리셋",
            ),
            (12000, "Resets in 3 hours 20 minutes", "3시간 20분 뒤 리셋"),
            (3540, "Resets in 59 minutes", "59분 뒤 리셋"),
            (60, "Resets in 1 minute", "1분 뒤 리셋"),
            (59, "Resets soon", "곧 리셋"),
            (3600, "Resets in 1 hour", "1시간 뒤 리셋"),
            (3660, "Resets in 1 hour 1 minute", "1시간 1분 뒤 리셋"),
            (90000, "Resets in 1 day 1 hour", "1일 1시간 뒤 리셋"),
            (176400, "Resets in 2 days 1 hour", "2일 1시간 뒤 리셋"),
            (7320, "Resets in 2 hours 2 minutes", "2시간 2분 뒤 리셋"),
        ] {
            assert_eq!(
                countdown(seconds * 1000 + 999, SupportedLanguage::English),
                en
            );
            assert_eq!(
                countdown(seconds * 1000 + 999, SupportedLanguage::Korean),
                ko
            );
        }
    }
    #[test]
    fn original_instants_validate_without_timezone_or_calendar_inference() {
        let now = instant("2026-10-08T00:00:00Z").unwrap();
        assert_eq!(
            reset_in("2026-10-14T03:00:00Z", now, SupportedLanguage::English),
            "Resets in 6 days 3 hours"
        );
        assert_eq!(
            instant("2026-10-14T03:00:00Z"),
            instant("2026-10-14T12:00:00+09:00")
        );
        assert_eq!(
            instant("2026-11-01T02:00:00-05:00").unwrap()
                - instant("2026-11-01T01:00:00-04:00").unwrap(),
            7200000
        );
        for value in [
            "invalid",
            "2026-02-30T00:00:00Z",
            "2026-01-01T25:00:00Z",
            "2026-01-01T00:00:00+24:00",
            "2026-01-01T00:00:00.1234567890Z",
        ] {
            assert!(instant(value).is_none());
            assert!(!reset_in(value, now, SupportedLanguage::English).contains("Resets in"));
        }
        assert!(
            !reset_in("2026-10-08T00:00:00Z", now, SupportedLanguage::English)
                .contains("Resets in")
        );
        assert_eq!(instant("1970-01-01T00:00:00Z"), Some(0));
        assert_eq!(instant("2026-10-08T00:00:00.123456789Z"), Some(now + 123));
    }
}
