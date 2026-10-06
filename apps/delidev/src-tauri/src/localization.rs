// SPDX-License-Identifier: Apache-2.0
use std::{collections::BTreeMap, sync::OnceLock};

use crate::language::{SupportedLanguage, active};
#[path = "localization_keys.rs"]
mod keys;
pub use keys::Message;

fn catalog(language: SupportedLanguage) -> &'static BTreeMap<String, String> {
    static ENGLISH: OnceLock<BTreeMap<String, String>> = OnceLock::new();
    static KOREAN: OnceLock<BTreeMap<String, String>> = OnceLock::new();
    match language {
        SupportedLanguage::English => ENGLISH.get_or_init(|| {
            serde_json::from_str(include_str!("../../locales/native/en.json"))
                .expect("checked English catalog")
        }),
        SupportedLanguage::Korean => KOREAN.get_or_init(|| {
            serde_json::from_str(include_str!("../../locales/native/ko.json"))
                .expect("checked Korean catalog")
        }),
    }
}
pub fn text(key: Message) -> &'static str {
    text_in(key, active())
}
pub fn text_in(key: Message, language: SupportedLanguage) -> &'static str {
    catalog(language)
        .get(key.key())
        .expect("checked native key")
}
pub fn format(key: Message, values: &[(&str, &str)]) -> String {
    format_in(key, values, active())
}
fn format_in(key: Message, values: &[(&str, &str)], language: SupportedLanguage) -> String {
    let mut parts = text_in(key, language).split("{{");
    let mut result = parts.next().unwrap_or_default().to_owned();
    for part in parts {
        if let Some((name, rest)) = part.split_once("}}") {
            if let Some((_, value)) = values.iter().find(|(key, _)| *key == name) {
                result.push_str(value);
            } else {
                result.push_str("{{");
                result.push_str(name);
                result.push_str("}}");
            }
            result.push_str(rest);
        } else {
            result.push_str("{{");
            result.push_str(part);
        }
    }
    result
}
pub fn date(value: &str) -> String {
    date_in(value, active())
}
fn date_in(value: &str, language: SupportedLanguage) -> String {
    // Retain the observation's offset and fractional precision. Presentation
    // must not turn a source timestamp into a different local wall-clock time.
    if !value.is_ascii() || value.len() < 20 || value.len() > 64 {
        return value.to_owned();
    }
    let Some((calendar, time)) = value.split_once('T') else {
        return value.to_owned();
    };
    let fields = calendar.split('-').collect::<Vec<_>>();
    if fields.len() != 3
        || fields[0].len() != 4
        || fields[1].len() != 2
        || fields[2].len() != 2
        || fields
            .iter()
            .any(|field| !field.bytes().all(|byte| byte.is_ascii_digit()))
    {
        return value.to_owned();
    }
    let Ok(month) = fields[1].parse::<usize>() else {
        return value.to_owned();
    };
    let months = [
        Message::Month1,
        Message::Month2,
        Message::Month3,
        Message::Month4,
        Message::Month5,
        Message::Month6,
        Message::Month7,
        Message::Month8,
        Message::Month9,
        Message::Month10,
        Message::Month11,
        Message::Month12,
    ];
    let Some(month_key) = month.checked_sub(1).and_then(|index| months.get(index)) else {
        return value.to_owned();
    };
    let (clock, zone) = if let Some(clock) = time.strip_suffix('Z') {
        (clock, "UTC".to_owned())
    } else if time.len() >= 14 && matches!(time.as_bytes()[time.len() - 6], b'+' | b'-') {
        (
            &time[..time.len() - 6],
            format!("UTC{}", &time[time.len() - 6..]),
        )
    } else {
        return value.to_owned();
    };
    format_in(
        Message::Date,
        &[
            ("year", fields[0]),
            ("month", text_in(*month_key, language)),
            ("day", fields[2]),
            ("clock", clock),
            ("zone", &zone),
        ],
        language,
    )
}
/// Both supported locales group decimal digits with commas. Keep every digit,
/// including the original fraction, without converting through floating point.
pub fn number(value: &str) -> String {
    let (integer, fraction) = value
        .split_once('.')
        .map_or((value, None), |(a, b)| (a, Some(b)));
    let mut result = String::new();
    for (index, byte) in integer.bytes().enumerate() {
        if index > 0 && (integer.len() - index).is_multiple_of(3) {
            result.push(',');
        }
        result.push(char::from(byte));
    }
    if let Some(fraction) = fraction {
        result.push('.');
        result.push_str(fraction);
    }
    result
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn catalogs_and_exact_numbers_keep_data_separate() {
        assert_eq!(
            catalog(SupportedLanguage::English)
                .keys()
                .collect::<Vec<_>>(),
            catalog(SupportedLanguage::Korean)
                .keys()
                .collect::<Vec<_>>()
        );
        assert!(
            catalog(SupportedLanguage::Korean)
                .values()
                .all(|value| !value.trim().is_empty())
        );
        assert_eq!(
            number("90071992547409930000.000000009"),
            "90,071,992,547,409,930,000.000000009"
        );
        assert_eq!(
            text_in(Message::Sessions, SupportedLanguage::Korean),
            "세션"
        );
        assert!(text_in(Message::InstallBody, SupportedLanguage::Korean).contains("{{version}}"));
    }
    #[test]
    fn dates_keep_the_source_offset_and_fraction() {
        assert_eq!(
            date_in(
                "2026-09-27T00:01:02.123456789+09:00",
                SupportedLanguage::Korean
            ),
            "2026년 9월 27일 00:01:02.123456789 UTC+09:00"
        );
        assert_eq!(
            date_in("2026-09-27T00:01:02Z", SupportedLanguage::English),
            "September 27, 2026, 00:01:02 UTC"
        );
        assert_eq!(
            date_in("unavailable", SupportedLanguage::Korean),
            "unavailable"
        );
    }
}
