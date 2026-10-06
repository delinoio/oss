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
    let mut parts = text(key).split("{{");
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
}
