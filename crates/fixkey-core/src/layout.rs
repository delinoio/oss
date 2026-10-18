//! Layout reconstruction between Korean Dubeolsik and US English QWERTY.
//!
//! Both layouts share the same physical keys, so a typed word is modeled as a
//! *key sequence* expressed with the characters the US QWERTY layout produces
//! (shift is implied by case). Any word can be turned into its key sequence
//! under the layout it currently appears in and re-interpreted under every
//! enabled layout. Text-only reverse inference is available for manual
//! candidates; automatic correction requires the recorded physical keys.

use crate::types::{Language, LanguageSet};

/// Single-key Dubeolsik jamo, as `(key, compatibility jamo)`.
const KEY_JAMO: &[(char, char)] = &[
    ('r', 'ㄱ'),
    ('R', 'ㄲ'),
    ('s', 'ㄴ'),
    ('e', 'ㄷ'),
    ('E', 'ㄸ'),
    ('f', 'ㄹ'),
    ('a', 'ㅁ'),
    ('q', 'ㅂ'),
    ('Q', 'ㅃ'),
    ('t', 'ㅅ'),
    ('T', 'ㅆ'),
    ('d', 'ㅇ'),
    ('w', 'ㅈ'),
    ('W', 'ㅉ'),
    ('c', 'ㅊ'),
    ('z', 'ㅋ'),
    ('x', 'ㅌ'),
    ('v', 'ㅍ'),
    ('g', 'ㅎ'),
    ('k', 'ㅏ'),
    ('o', 'ㅐ'),
    ('i', 'ㅑ'),
    ('O', 'ㅒ'),
    ('j', 'ㅓ'),
    ('p', 'ㅔ'),
    ('u', 'ㅕ'),
    ('P', 'ㅖ'),
    ('h', 'ㅗ'),
    ('y', 'ㅛ'),
    ('n', 'ㅜ'),
    ('b', 'ㅠ'),
    ('m', 'ㅡ'),
    ('l', 'ㅣ'),
    // Keys without a distinct shifted jamo fall back to their lowercase key;
    // see `jamo_for_key`. These placeholders keep the table length explicit.
    ('K', 'ㅏ'),
    ('I', 'ㅑ'),
    ('J', 'ㅓ'),
    ('U', 'ㅕ'),
    ('H', 'ㅗ'),
    ('Y', 'ㅛ'),
    ('N', 'ㅜ'),
    ('B', 'ㅠ'),
    ('M', 'ㅡ'),
    ('L', 'ㅣ'),
    ('S', 'ㄴ'),
    ('F', 'ㄹ'),
    ('A', 'ㅁ'),
    ('D', 'ㅇ'),
    ('G', 'ㅎ'),
    ('Z', 'ㅋ'),
    ('X', 'ㅌ'),
    ('V', 'ㅍ'),
    ('C', 'ㅊ'),
];

/// Compound jamo and the key sequence that types them.
const COMPOUND_KEYS: [(char, &str); 18] = [
    ('ㅘ', "hk"),
    ('ㅙ', "ho"),
    ('ㅚ', "hl"),
    ('ㅝ', "nj"),
    ('ㅞ', "np"),
    ('ㅟ', "nl"),
    ('ㅢ', "ml"),
    ('ㄳ', "rt"),
    ('ㄵ', "sw"),
    ('ㄶ', "sg"),
    ('ㄺ', "fr"),
    ('ㄻ', "fa"),
    ('ㄼ', "fq"),
    ('ㄽ', "ft"),
    ('ㄾ', "fx"),
    ('ㄿ', "fv"),
    ('ㅀ', "fg"),
    ('ㅄ', "qt"),
];

/// `(first vowel, second vowel, compound vowel)`.
const VOWEL_PAIRS: [(char, char, char); 7] = [
    ('ㅗ', 'ㅏ', 'ㅘ'),
    ('ㅗ', 'ㅐ', 'ㅙ'),
    ('ㅗ', 'ㅣ', 'ㅚ'),
    ('ㅜ', 'ㅓ', 'ㅝ'),
    ('ㅜ', 'ㅔ', 'ㅞ'),
    ('ㅜ', 'ㅣ', 'ㅟ'),
    ('ㅡ', 'ㅣ', 'ㅢ'),
];

/// `(first final, second final, compound final)`.
const TAIL_PAIRS: [(char, char, char); 11] = [
    ('ㄱ', 'ㅅ', 'ㄳ'),
    ('ㄴ', 'ㅈ', 'ㄵ'),
    ('ㄴ', 'ㅎ', 'ㄶ'),
    ('ㄹ', 'ㄱ', 'ㄺ'),
    ('ㄹ', 'ㅁ', 'ㄻ'),
    ('ㄹ', 'ㅂ', 'ㄼ'),
    ('ㄹ', 'ㅅ', 'ㄽ'),
    ('ㄹ', 'ㅌ', 'ㄾ'),
    ('ㄹ', 'ㅍ', 'ㄿ'),
    ('ㄹ', 'ㅎ', 'ㅀ'),
    ('ㅂ', 'ㅅ', 'ㅄ'),
];

const LEADS: [char; 19] = [
    'ㄱ', 'ㄲ', 'ㄴ', 'ㄷ', 'ㄸ', 'ㄹ', 'ㅁ', 'ㅂ', 'ㅃ', 'ㅅ', 'ㅆ', 'ㅇ', 'ㅈ', 'ㅉ', 'ㅊ', 'ㅋ',
    'ㅌ', 'ㅍ', 'ㅎ',
];
const VOWELS: [char; 21] = [
    'ㅏ', 'ㅐ', 'ㅑ', 'ㅒ', 'ㅓ', 'ㅔ', 'ㅕ', 'ㅖ', 'ㅗ', 'ㅘ', 'ㅙ', 'ㅚ', 'ㅛ', 'ㅜ', 'ㅝ', 'ㅞ',
    'ㅟ', 'ㅠ', 'ㅡ', 'ㅢ', 'ㅣ',
];
/// Index 0 is "no final consonant".
const TAILS: [Option<char>; 28] = [
    None,
    Some('ㄱ'),
    Some('ㄲ'),
    Some('ㄳ'),
    Some('ㄴ'),
    Some('ㄵ'),
    Some('ㄶ'),
    Some('ㄷ'),
    Some('ㄹ'),
    Some('ㄺ'),
    Some('ㄻ'),
    Some('ㄼ'),
    Some('ㄽ'),
    Some('ㄾ'),
    Some('ㄿ'),
    Some('ㅀ'),
    Some('ㅁ'),
    Some('ㅂ'),
    Some('ㅄ'),
    Some('ㅅ'),
    Some('ㅆ'),
    Some('ㅇ'),
    Some('ㅈ'),
    Some('ㅊ'),
    Some('ㅋ'),
    Some('ㅌ'),
    Some('ㅍ'),
    Some('ㅎ'),
];

const SYLLABLE_BASE: u32 = 0xac00;
const SYLLABLE_LAST: u32 = 0xd7a3;
const VOWEL_COUNT: u32 = 21;
const TAIL_COUNT: u32 = 28;

fn is_vowel(jamo: char) -> bool {
    VOWELS.contains(&jamo)
}

fn index_of<T: PartialEq + Copy>(table: &[T], value: T) -> Option<usize> {
    table.iter().position(|candidate| *candidate == value)
}

/// Jamo typed by one Dubeolsik key. Shifted keys without a distinct shifted
/// jamo type the same jamo as their unshifted key.
fn jamo_for_key(key: char) -> Option<char> {
    KEY_JAMO
        .iter()
        .find(|(candidate, _)| *candidate == key)
        .map(|(_, jamo)| *jamo)
}

/// Keys that type `jamo`, including compound jamo.
fn keys_for_jamo(jamo: char) -> Option<Vec<char>> {
    if let Some((_, keys)) = COMPOUND_KEYS.iter().find(|(c, _)| *c == jamo) {
        return Some(keys.chars().collect());
    }
    // Prefer the first entry, which is the canonical (shift-aware) key.
    KEY_JAMO
        .iter()
        .find(|(_, candidate)| *candidate == jamo)
        .map(|(key, _)| vec![*key])
}

fn compose_pair(table: &[(char, char, char)], first: char, second: char) -> Option<char> {
    table
        .iter()
        .find(|(a, b, _)| *a == first && *b == second)
        .map(|(_, _, combined)| *combined)
}

/// Splits a compound final into `(kept final, moved consonant)`.
fn split_tail(tail: char) -> Option<(char, char)> {
    TAIL_PAIRS
        .iter()
        .find(|(_, _, combined)| *combined == tail)
        .map(|(first, second, _)| (*first, *second))
}

/// Incremental Hangul syllable composer for the Dubeolsik automaton.
#[derive(Default)]
struct Composer {
    out: String,
    lead: Option<char>,
    vowel: Option<char>,
    tail: Option<char>,
}

impl Composer {
    fn flush(&mut self) {
        match (self.lead.take(), self.vowel.take(), self.tail.take()) {
            (Some(lead), Some(vowel), tail) => {
                if let Some(syllable) = compose_syllable(lead, vowel, tail) {
                    self.out.push(syllable);
                } else {
                    self.out.push(lead);
                    self.out.push(vowel);
                    if let Some(tail) = tail {
                        self.out.push(tail);
                    }
                }
            }
            (lead, vowel, tail) => {
                self.out.extend(lead);
                self.out.extend(vowel);
                self.out.extend(tail);
            }
        }
    }

    fn push(&mut self, jamo: char) {
        if is_vowel(jamo) {
            self.push_vowel(jamo);
        } else {
            self.push_consonant(jamo);
        }
    }

    fn push_vowel(&mut self, vowel: char) {
        match (self.lead, self.vowel, self.tail) {
            (Some(_), None, _) => self.vowel = Some(vowel),
            (Some(_), Some(current), None) => {
                if let Some(combined) = compose_pair(&VOWEL_PAIRS, current, vowel) {
                    self.vowel = Some(combined);
                } else {
                    self.flush();
                    self.out.push(vowel);
                }
            }
            (Some(_), Some(_), Some(tail)) => {
                // The final consonant moves to the next syllable as its lead.
                let (kept, moved) =
                    split_tail(tail).map_or((None, tail), |(kept, moved)| (Some(kept), moved));
                self.tail = kept;
                self.flush();
                self.lead = Some(moved);
                self.vowel = Some(vowel);
            }
            (None, _, _) => {
                self.flush();
                self.out.push(vowel);
            }
        }
    }

    fn push_consonant(&mut self, consonant: char) {
        match (self.lead, self.vowel, self.tail) {
            (Some(_), Some(_), None) if TAILS.contains(&Some(consonant)) => {
                self.tail = Some(consonant);
            }
            (Some(_), Some(_), Some(tail)) => {
                if let Some(combined) = compose_pair(&TAIL_PAIRS, tail, consonant) {
                    self.tail = Some(combined);
                } else {
                    self.flush();
                    self.lead = Some(consonant);
                }
            }
            _ => {
                self.flush();
                self.lead = Some(consonant);
            }
        }
    }

    fn finish(mut self) -> String {
        self.flush();
        self.out
    }
}

fn compose_syllable(lead: char, vowel: char, tail: Option<char>) -> Option<char> {
    let lead_index = index_of(&LEADS, lead)?;
    let vowel_index = index_of(&VOWELS, vowel)?;
    let tail_index = match tail {
        None => 0,
        Some(tail) => index_of(&TAILS, Some(tail))?,
    };
    let code = SYLLABLE_BASE
        + (u32::try_from(lead_index).ok()? * VOWEL_COUNT + u32::try_from(vowel_index).ok()?)
            * TAIL_COUNT
        + u32::try_from(tail_index).ok()?;
    char::from_u32(code)
}

fn decompose_syllable(syllable: char) -> Option<[Option<char>; 3]> {
    let code = u32::from(syllable);
    if !(SYLLABLE_BASE..=SYLLABLE_LAST).contains(&code) {
        return None;
    }
    let offset = code - SYLLABLE_BASE;
    let tail_index = usize::try_from(offset % TAIL_COUNT).ok()?;
    let vowel_index = usize::try_from((offset / TAIL_COUNT) % VOWEL_COUNT).ok()?;
    let lead_index = usize::try_from(offset / (TAIL_COUNT * VOWEL_COUNT)).ok()?;
    Some([
        LEADS.get(lead_index).copied(),
        VOWELS.get(vowel_index).copied(),
        TAILS.get(tail_index).copied().flatten(),
    ])
}

const fn is_english_word_char(c: char) -> bool {
    c.is_ascii_alphabetic() || c == '\'' || c == '-'
}

const fn is_layout_separator(c: char) -> bool {
    c == '\'' || c == '-'
}

fn is_word_with_internal_separators(
    chars: impl IntoIterator<Item = char>,
    is_letter: impl Fn(char) -> bool,
) -> bool {
    let mut previous_was_letter = false;
    for c in chars {
        if is_letter(c) {
            previous_was_letter = true;
        } else if is_layout_separator(c) && previous_was_letter {
            previous_was_letter = false;
        } else {
            return false;
        }
    }
    previous_was_letter
}

fn is_hangul(c: char) -> bool {
    let code = u32::from(c);
    (SYLLABLE_BASE..=SYLLABLE_LAST).contains(&code) || (0x3131..=0x3163).contains(&code)
}

/// Returns the language whose layout `word` is currently written in, or
/// `None` for empty, mixed-script or unsupported text.
#[must_use]
pub fn detect_language(word: &str) -> Option<Language> {
    let mut chars = word.chars().peekable();
    chars.peek()?;
    if is_word_with_internal_separators(word.chars(), is_hangul) {
        Some(Language::KoDubeolsik)
    } else if word.chars().all(is_english_word_char)
        && word.chars().any(|c| c.is_ascii_alphabetic())
    {
        Some(Language::EnUsQwerty)
    } else {
        None
    }
}

/// Converts `text` written in `language` into the key sequence that types it.
///
/// Returns `None` when `text` contains characters the layout cannot type, or
/// standalone compound compatibility jamo that cannot round-trip unchanged.
#[must_use]
pub fn keys_from_text(text: &str, language: Language) -> Option<Vec<char>> {
    if text.is_empty() {
        return None;
    }
    match language {
        Language::EnUsQwerty => text
            .chars()
            .all(is_english_word_char)
            .then(|| text.chars().collect()),
        Language::KoDubeolsik => {
            if !is_word_with_internal_separators(text.chars(), is_hangul) {
                return None;
            }
            let mut keys = Vec::new();
            for c in text.chars() {
                if let Some(parts) = decompose_syllable(c) {
                    for jamo in parts.into_iter().flatten() {
                        keys.extend(keys_for_jamo(jamo)?);
                    }
                } else if is_layout_separator(c) {
                    keys.push(c);
                } else if is_hangul(c) {
                    if COMPOUND_KEYS.iter().any(|(compound, _)| *compound == c) {
                        return None;
                    }
                    keys.extend(keys_for_jamo(c)?);
                } else {
                    return None;
                }
            }
            Some(keys)
        }
    }
}

/// Interprets a key sequence under `language`'s layout.
///
/// Returns `None` when a key has no meaning in that layout. Apostrophes and
/// hyphens are preserved when they occur between letter keys.
#[must_use]
pub fn text_from_keys(keys: &[char], language: Language) -> Option<String> {
    if keys.is_empty() {
        return None;
    }
    match language {
        Language::EnUsQwerty => keys
            .iter()
            .all(|key| is_english_word_char(*key))
            .then(|| keys.iter().collect()),
        Language::KoDubeolsik => {
            if !is_word_with_internal_separators(keys.iter().copied(), |c| c.is_ascii_alphabetic())
            {
                return None;
            }
            let mut composer = Composer::default();
            for key in keys {
                if is_layout_separator(*key) {
                    composer.flush();
                    composer.out.push(*key);
                } else {
                    composer.push(jamo_for_key(*key)?);
                }
            }
            Some(composer.finish())
        }
    }
}

/// One interpretation of a word under an enabled layout.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Reconstruction {
    /// Layout the key sequence was interpreted under.
    pub language: Language,
    /// Resulting text, equal to the input for the word's own language.
    pub text: String,
}

/// Reconstructs a directly typed word from its recorded physical keys.
///
/// The caller must supply keys observed for this word in the same input
/// context. Empty, unsupported or mismatched keys return `None`; inferred
/// keys must not be passed here as input provenance. The rendered word is
/// checked separately so its original representation remains authoritative.
#[must_use]
pub fn reconstruct_from_keys(
    word: &str,
    recorded_keys: &[char],
    enabled: LanguageSet,
) -> Option<Vec<Reconstruction>> {
    let current = detect_language(word)?;
    // Preserve the text-only decoder's rejection of unsupported Korean input,
    // including standalone compound compatibility jamo.
    keys_from_text(word, current)?;
    if !is_word_with_internal_separators(recorded_keys.iter().copied(), |c| c.is_ascii_alphabetic())
        || text_from_keys(recorded_keys, current).as_deref() != Some(word)
    {
        return None;
    }
    Some(
        enabled
            .enabled()
            .filter_map(|language| {
                text_from_keys(recorded_keys, language)
                    .map(|text| Reconstruction { language, text })
            })
            .collect(),
    )
}

/// Infers keys from rendered `word` for manual candidate exploration.
///
/// This path cannot recover physical Shift state from Korean text and must not
/// be used for automatic correction. It includes the current representation.
///
/// Returns an empty list when `word` is not written in a supported layout;
/// languages whose layout cannot represent the key sequence are omitted.
#[must_use]
pub fn reconstruct(word: &str, enabled: LanguageSet) -> Vec<Reconstruction> {
    let Some(current) = detect_language(word) else {
        return Vec::new();
    };
    let Some(keys) = keys_from_text(word, current) else {
        return Vec::new();
    };
    enabled
        .enabled()
        .filter_map(|language| {
            text_from_keys(&keys, language).map(|text| Reconstruction { language, text })
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ko(keys: &str) -> String {
        text_from_keys(&keys.chars().collect::<Vec<_>>(), Language::KoDubeolsik).unwrap()
    }

    fn keys(text: &str, language: Language) -> String {
        keys_from_text(text, language)
            .unwrap()
            .into_iter()
            .collect()
    }

    #[test]
    fn recorded_keys_preserve_shifts_lost_by_korean_reverse_inference() {
        let both = LanguageSet::default();
        for typed in ["NASA", "nasa", "NaSa"] {
            let recorded: Vec<char> = typed.chars().collect();
            let rendered = text_from_keys(&recorded, Language::KoDubeolsik).unwrap();
            assert_eq!(rendered, "ㅜㅁㄴㅁ");
            let candidates = reconstruct_from_keys(&rendered, &recorded, both).unwrap();
            assert_eq!(candidates[0].text, rendered);
            assert_eq!(candidates[1].text, typed);
        }
        for typed in ["rK", "rk"] {
            let recorded: Vec<char> = typed.chars().collect();
            assert_eq!(
                text_from_keys(&recorded, Language::KoDubeolsik),
                Some("가".into())
            );
            assert_eq!(
                reconstruct_from_keys("가", &recorded, both).unwrap()[1].text,
                typed
            );
        }
    }

    #[test]
    fn recorded_keys_reject_missing_mismatched_and_unsupported_provenance() {
        let both = LanguageSet::default();
        for recorded in ["", "xxxx", "na-sa", "nasa'"] {
            assert!(
                reconstruct_from_keys("ㅜㅁㄴㅁ", &recorded.chars().collect::<Vec<_>>(), both)
                    .is_none()
            );
        }
        assert!(reconstruct_from_keys("NASA", &"nasa".chars().collect::<Vec<_>>(), both).is_none());
        assert!(reconstruct_from_keys("a가", &"nasa".chars().collect::<Vec<_>>(), both).is_none());
        assert!(reconstruct_from_keys("ㅘ", &"hk".chars().collect::<Vec<_>>(), both).is_none());
        assert!(reconstruct_from_keys("가", &"rK".chars().collect::<Vec<_>>(), both).is_some());
    }

    #[test]
    fn required_examples_convert_from_english_keys() {
        assert_eq!(ko("rPthrgo"), "계속해");
        assert_eq!(ko("zlqhemrk"), "키보드가");
    }

    #[test]
    fn reverse_conversion_returns_the_key_sequence() {
        assert_eq!(keys("계속해", Language::KoDubeolsik), "rPthrgo");
        assert_eq!(keys("키보드가", Language::KoDubeolsik), "zlqhemrk");
    }

    #[test]
    fn shifted_keys_and_casing_are_preserved() {
        assert_eq!(ko("Rk"), "까");
        assert_eq!(ko("oO"), "ㅐㅒ");
        assert_eq!(keys("깨", Language::KoDubeolsik), "Ro");
        assert_eq!(keys("Hello", Language::EnUsQwerty), "Hello");
        // Shifted keys with no distinct jamo type the unshifted jamo.
        assert_eq!(ko("K"), "ㅏ");
    }

    #[test]
    fn compound_vowels_and_finals_compose_and_decompose() {
        assert_eq!(ko("dhk"), "와");
        assert_eq!(keys("와", Language::KoDubeolsik), "dhk");
        assert_eq!(ko("dkfr"), "앍");
        assert_eq!(keys("앍", Language::KoDubeolsik), "dkfr");
        assert_eq!(ko("rkqt"), "값");
        assert_eq!(keys("값", Language::KoDubeolsik), "rkqt");
    }

    #[test]
    fn final_consonant_moves_to_the_next_syllable_before_a_vowel() {
        assert_eq!(ko("rkr"), "각");
        assert_eq!(ko("rkrk"), "가가");
        // A compound final splits: its second consonant becomes the lead.
        assert_eq!(ko("dkfrk"), "알가");
    }

    #[test]
    fn lone_jamo_remain_standalone() {
        assert_eq!(ko("r"), "ㄱ");
        assert_eq!(ko("rr"), "ㄱㄱ");
        assert_eq!(ko("kk"), "ㅏㅏ");
        for (text, expected_keys) in [("ㅗㅏ", "hk"), ("ㄱㅅ", "rt"), ("ㄱ", "r"), ("ㅏ", "k")]
        {
            assert_eq!(keys(text, Language::KoDubeolsik), expected_keys);
            assert_eq!(ko(expected_keys), text);
        }
    }

    #[test]
    fn standalone_compound_jamo_cannot_change_the_original_candidate() {
        for compound in "ㅘㅙㅚㅝㅞㅟㅢㄳㄵㄶㄺㄻㄼㄽㄾㄿㅀㅄ".chars() {
            for text in [compound.to_string(), format!("가{compound}나")] {
                assert_eq!(detect_language(&text), Some(Language::KoDubeolsik));
                assert_eq!(keys_from_text(&text, Language::KoDubeolsik), None, "{text}");
                assert!(
                    reconstruct(&text, LanguageSet::default()).is_empty(),
                    "{text}"
                );
            }
        }
    }

    #[test]
    fn round_trip_holds_for_composed_words() {
        for word in ["안녕하세요", "한국어", "값", "읊다", "닭", "의사"] {
            let typed = keys(word, Language::KoDubeolsik);
            assert_eq!(ko(&typed), word, "typed as {typed}");
        }
    }

    #[test]
    fn unsupported_characters_have_no_key_sequence() {
        assert_eq!(keys_from_text("가a", Language::KoDubeolsik), None);
        assert_eq!(keys_from_text("hi!", Language::EnUsQwerty), None);
        assert_eq!(keys_from_text("", Language::EnUsQwerty), None);
        assert_eq!(text_from_keys(&['\''], Language::KoDubeolsik), None);
        assert_eq!(text_from_keys(&['-'], Language::KoDubeolsik), None);
        assert_eq!(text_from_keys(&[], Language::EnUsQwerty), None);
    }

    #[test]
    fn language_detection_requires_a_single_script() {
        assert_eq!(detect_language("rPthrgo"), Some(Language::EnUsQwerty));
        assert_eq!(detect_language("계속해"), Some(Language::KoDubeolsik));
        assert_eq!(detect_language("don't"), Some(Language::EnUsQwerty));
        assert_eq!(detect_language("a가"), None);
        assert_eq!(detect_language("'-"), None);
        assert_eq!(detect_language(""), None);
    }

    #[test]
    fn reconstruct_covers_every_enabled_language_in_both_directions() {
        let both = LanguageSet::default();
        let from_english = reconstruct("rPthrgo", both);
        assert_eq!(
            from_english,
            vec![
                Reconstruction {
                    language: Language::KoDubeolsik,
                    text: "계속해".into()
                },
                Reconstruction {
                    language: Language::EnUsQwerty,
                    text: "rPthrgo".into()
                },
            ]
        );
        let from_korean = reconstruct("키보드가", both);
        assert_eq!(from_korean[0].text, "키보드가");
        assert_eq!(from_korean[1].text, "zlqhemrk");
    }

    #[test]
    fn disabled_languages_are_not_reconstructed() {
        let english_only = LanguageSet::default().with(Language::KoDubeolsik, false);
        let result = reconstruct("rPthrgo", english_only);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].language, Language::EnUsQwerty);
        assert_eq!(reconstruct("a가", LanguageSet::default()), vec![]);
    }

    #[test]
    fn internal_apostrophes_and_hyphens_round_trip_in_both_layouts() {
        let both = LanguageSet::default();
        for (english, korean) in [
            ("well-being", "ㅈ디ㅣ-ㅠ댜ㅜㅎ"),
            ("don't", "애ㅜ'ㅅ"),
            ("Rk-Rk", "까-까"),
            ("a-b-c", "ㅁ-ㅠ-ㅊ"),
        ] {
            assert_eq!(detect_language(english), Some(Language::EnUsQwerty));
            assert_eq!(ko(english), korean);
            assert_eq!(detect_language(korean), Some(Language::KoDubeolsik));
            assert_eq!(keys(korean, Language::KoDubeolsik), english);
            let recorded: Vec<char> = english.chars().collect();
            for rendered in [english, korean] {
                let checked = reconstruct_from_keys(rendered, &recorded, both).unwrap();
                assert_eq!(checked[0].text, korean);
                assert_eq!(checked[1].text, english);
            }
            assert_eq!(
                reconstruct(english, both),
                vec![
                    Reconstruction {
                        language: Language::KoDubeolsik,
                        text: korean.into()
                    },
                    Reconstruction {
                        language: Language::EnUsQwerty,
                        text: english.into()
                    },
                ]
            );
            assert_eq!(
                reconstruct(korean, both),
                vec![
                    Reconstruction {
                        language: Language::KoDubeolsik,
                        text: korean.into()
                    },
                    Reconstruction {
                        language: Language::EnUsQwerty,
                        text: english.into()
                    },
                ]
            );
        }

        assert_eq!(ko("wellbeing"), "ㅈ디ㅣㅠ댜ㅜㅎ");
        assert_eq!(ko("dont"), "애ㅜㅅ");
    }

    #[test]
    fn separators_stay_internal_and_enabled_languages_are_filtered() {
        let both = LanguageSet::default();
        for text in [
            "가-", "-가", "가--나", "가'나-", "가-a", "가@나", "가1나", "'-",
        ] {
            assert_eq!(detect_language(text), None, "{text}");
            assert!(reconstruct(text, both).is_empty(), "{text}");
        }
        assert_eq!(keys_from_text("가-", Language::KoDubeolsik), None);
        assert_eq!(
            text_from_keys(&['r', 'k', '-'], Language::KoDubeolsik),
            None
        );

        let korean_only = both.with(Language::EnUsQwerty, false);
        let english_only = both.with(Language::KoDubeolsik, false);
        assert_eq!(reconstruct("애ㅜ'ㅅ", korean_only)[0].text, "애ㅜ'ㅅ");
        assert_eq!(reconstruct("애ㅜ'ㅅ", english_only)[0].text, "don't");
    }
}
