//! English validation over a SCOWL/ESDB-derived lexicon with entry-specific
//! inflection rules.
//!
//! Comparison is case-insensitive. Proper nouns and abbreviations are accepted
//! only when the lexicon lists them, and only common words inflect.

use crate::decision::{LanguageValidator, Validation};

/// Lexicon entry class.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EntryClass {
    /// Ordinary word that may have permitted inflections.
    Common,
    /// Proper noun; accepted as listed and with a possessive.
    ProperNoun,
    /// Abbreviation; accepted only as listed.
    Abbreviation,
}

/// An entry's permitted suffix and spelling transformation.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum InflectionRule {
    PluralS,
    PluralEs,
    PluralIes,
    PastEd,
    PastD,
    PastIed,
    PastDoubleEd,
    ProgressiveIng,
    ProgressiveDropE,
    ProgressiveDoubleIng,
    ComparativeEr,
    ComparativeR,
    ComparativeIer,
    ComparativeDoubleEr,
    SuperlativeEst,
    SuperlativeSt,
    SuperlativeIest,
    SuperlativeDoubleEst,
}

/// Exact dictionary entry and its permitted rules. An empty rule list permits
/// the listed word and its possessive but no derived suffix forms.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct LexiconEntry {
    pub class: EntryClass,
    pub rules: &'static [InflectionRule],
}

/// Lexicon lookup by lowercase word.
pub trait Lexicon {
    /// Returns the exact lowercase entry, if listed. Rules must come from the
    /// selected dictionary entry; a common-word class grants no suffixes.
    fn lookup(&self, word: &str) -> Option<LexiconEntry>;
}

/// Returns whether `word` is a listed word or a valid inflection of one.
#[must_use]
pub fn is_known_word(lexicon: &dyn Lexicon, word: &str) -> bool {
    let lower = word.to_lowercase();
    if lower.is_empty() {
        return false;
    }
    if lexicon.lookup(&lower).is_some() {
        return true;
    }
    if let Some(base) = lower
        .strip_suffix("'s")
        .or_else(|| lower.strip_suffix("\u{2019}s"))
        .or_else(|| lower.strip_suffix('\'').filter(|base| base.ends_with('s')))
    {
        return matches!(
            lexicon.lookup(base),
            Some(LexiconEntry {
                class: EntryClass::Common | EntryClass::ProperNoun,
                ..
            })
        ) || is_inflected_common(lexicon, base);
    }
    is_inflected_common(lexicon, &lower)
}

fn consonant_y(base: &str) -> Option<&str> {
    let stem = base.strip_suffix('y')?;
    stem.chars()
        .last()
        .filter(|c| c.is_ascii_alphabetic() && !"aeiou".contains(*c))?;
    Some(stem)
}

fn doubles_final_consonant(base: &str) -> Option<char> {
    let mut chars = base.chars().rev();
    let (last, middle, first) = (chars.next()?, chars.next()?, chars.next()?);
    (last.is_ascii_alphabetic()
        && !"aeiouwxy".contains(last)
        && "aeiou".contains(middle)
        && first.is_ascii_alphabetic()
        && !"aeiou".contains(first))
    .then_some(last)
}

impl InflectionRule {
    fn surface(self, base: &str) -> Option<String> {
        let plural_es = ["s", "x", "z", "ch", "sh", "o"]
            .iter()
            .any(|ending| base.ends_with(ending));
        let doubled = doubles_final_consonant(base);
        let surface = match self {
            Self::PluralS if !plural_es && consonant_y(base).is_none() => format!("{base}s"),
            Self::PluralEs if plural_es => format!("{base}es"),
            Self::PluralIes => format!("{}ies", consonant_y(base)?),
            Self::PastEd
                if !base.ends_with('e') && consonant_y(base).is_none() && doubled.is_none() =>
            {
                format!("{base}ed")
            }
            Self::PastD if base.ends_with('e') => format!("{base}d"),
            Self::PastIed => format!("{}ied", consonant_y(base)?),
            Self::PastDoubleEd => format!("{base}{}ed", doubled?),
            Self::ProgressiveIng if !base.ends_with('e') && doubled.is_none() => {
                format!("{base}ing")
            }
            Self::ProgressiveDropE if base.ends_with('e') && !base.ends_with("ee") => {
                format!("{}ing", base.strip_suffix('e')?)
            }
            Self::ProgressiveDoubleIng => format!("{base}{}ing", doubled?),
            Self::ComparativeEr
                if !base.ends_with('e') && consonant_y(base).is_none() && doubled.is_none() =>
            {
                format!("{base}er")
            }
            Self::ComparativeR if base.ends_with('e') => format!("{base}r"),
            Self::ComparativeIer => format!("{}ier", consonant_y(base)?),
            Self::ComparativeDoubleEr => format!("{base}{}er", doubled?),
            Self::SuperlativeEst
                if !base.ends_with('e') && consonant_y(base).is_none() && doubled.is_none() =>
            {
                format!("{base}est")
            }
            Self::SuperlativeSt if base.ends_with('e') => format!("{base}st"),
            Self::SuperlativeIest => format!("{}iest", consonant_y(base)?),
            Self::SuperlativeDoubleEst => format!("{base}{}est", doubled?),
            _ => return None,
        };
        Some(surface)
    }
}

fn undoubled(stem: &str) -> Option<&str> {
    let mut chars = stem.chars().rev();
    let (last, previous) = (chars.next()?, chars.next()?);
    (last == previous && last.is_ascii_alphabetic()).then(|| &stem[..stem.len() - last.len_utf8()])
}

fn matches_entry(lexicon: &dyn Lexicon, base: &str, word: &str) -> bool {
    if base.is_empty() {
        return false;
    }
    let Some(entry) = lexicon.lookup(base) else {
        return false;
    };
    entry.class == EntryClass::Common
        && entry
            .rules
            .iter()
            .any(|rule| rule.surface(base).is_some_and(|surface| surface == word))
}

/// Checks only the spelling transformations permitted by the base entry.
fn is_inflected_common(lexicon: &dyn Lexicon, word: &str) -> bool {
    for suffix in [
        "ies", "ied", "ing", "iest", "est", "ier", "er", "ed", "es", "s", "d", "r", "st",
    ] {
        let Some(stem) = word.strip_suffix(suffix).filter(|stem| stem.len() >= 2) else {
            continue;
        };
        if matches_entry(lexicon, stem, word)
            || matches_entry(lexicon, &format!("{stem}e"), word)
            || matches_entry(lexicon, &format!("{stem}y"), word)
            || undoubled(stem).is_some_and(|base| matches_entry(lexicon, base, word))
        {
            return true;
        }
    }
    false
}

/// English validator over a lexicon. A missing lexicon is modeled by
/// [`crate::decision::MissingValidator`] instead.
pub struct EnglishValidator<L> {
    lexicon: L,
}

impl<L: Lexicon> EnglishValidator<L> {
    /// Creates a validator.
    pub const fn new(lexicon: L) -> Self {
        Self { lexicon }
    }
}

impl<L: Lexicon> LanguageValidator for EnglishValidator<L> {
    fn validate(&self, candidate: &str) -> Validation {
        if is_known_word(&self.lexicon, candidate) {
            Validation::Accepted
        } else {
            Validation::Rejected
        }
    }
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use super::*;
    use crate::{
        decision::{Validators, decide},
        layout,
        types::{CorrectionOutcome, Language, LanguageSet},
    };

    struct Fixture(HashMap<&'static str, LexiconEntry>);

    impl Lexicon for Fixture {
        fn lookup(&self, word: &str) -> Option<LexiconEntry> {
            self.0.get(word).copied()
        }
    }

    const fn common(rules: &'static [InflectionRule]) -> LexiconEntry {
        LexiconEntry {
            class: EntryClass::Common,
            rules,
        }
    }

    fn lexicon() -> Fixture {
        use InflectionRule as Rule;
        Fixture(HashMap::from([
            ("keyboard", common(&[Rule::PluralS])),
            (
                "try",
                common(&[Rule::PluralIes, Rule::PastIed, Rule::ProgressiveIng]),
            ),
            (
                "stop",
                common(&[Rule::PastDoubleEd, Rule::ProgressiveDoubleIng]),
            ),
            (
                "make",
                common(&[Rule::ProgressiveDropE, Rule::ComparativeR]),
            ),
            ("box", common(&[Rule::PluralEs])),
            (
                "happy",
                common(&[Rule::ComparativeIer, Rule::SuperlativeIest]),
            ),
            (
                "big",
                common(&[Rule::ComparativeDoubleEr, Rule::SuperlativeDoubleEst]),
            ),
            (
                "london",
                LexiconEntry {
                    class: EntryClass::ProperNoun,
                    rules: &[],
                },
            ),
            (
                "nasa",
                LexiconEntry {
                    class: EntryClass::Abbreviation,
                    rules: &[],
                },
            ),
        ]))
    }

    fn accepted(word: &str) -> bool {
        EnglishValidator::new(lexicon()).validate(word) == Validation::Accepted
    }

    #[test]
    fn listed_words_match_case_insensitively() {
        assert!(accepted("keyboard"));
        assert!(accepted("Keyboard"));
        assert!(accepted("KEYBOARD"));
    }

    #[test]
    fn inflections_follow_each_entry_rule() {
        for word in [
            "keyboards",
            "tries",
            "tried",
            "trying",
            "stopped",
            "stopping",
            "making",
            "boxes",
            "happier",
            "happiest",
            "bigger",
            "biggest",
            "maker",
        ] {
            assert!(accepted(word), "{word}");
        }
        for word in [
            "tryed",
            "makeed",
            "makeing",
            "stoped",
            "stoping",
            "trys",
            "boxs",
            "keyboarded",
            "keyboardx",
            "biger",
            "biggestest",
        ] {
            assert!(!accepted(word), "{word}");
        }
    }

    #[test]
    fn listed_forms_and_possessives_remain_valid() {
        let mut dictionary = lexicon();
        dictionary.0.insert("made", common(&[]));
        assert!(is_known_word(&dictionary, "MADE"));
        assert!(!is_known_word(&dictionary, "mades"));
        assert!(accepted("keyboard's"));
        assert!(accepted("keyboards'"));
        assert!(accepted("London's"));
        assert!(!accepted("londons'"));
    }

    #[test]
    fn proper_nouns_and_abbreviations_require_a_listing() {
        assert!(accepted("London"));
        assert!(accepted("NASA"));
        assert!(!accepted("Paris"));
        // Abbreviations and proper nouns do not take common inflections.
        assert!(!accepted("nasas"));
        assert!(!accepted("londons"));
        assert!(!accepted("NASA's"));
    }

    #[test]
    fn unlisted_and_degenerate_words_are_rejected() {
        assert!(!accepted("rpthrgo"));
        assert!(!accepted(""));
        assert!(!accepted("s"));
        assert!(!accepted("es"));
    }

    struct Reject;

    impl LanguageValidator for Reject {
        fn validate(&self, _: &str) -> Validation {
            Validation::Rejected
        }
    }

    #[test]
    fn malformed_sole_candidate_does_not_replace() {
        let english = EnglishValidator::new(lexicon());
        let validators = Validators {
            korean: &Reject,
            english: &english,
        };
        for word in ["tryed", "makeed"] {
            let keys = word.chars().collect::<Vec<_>>();
            let typed = layout::text_from_keys(&keys, Language::KoDubeolsik).unwrap();
            let decision = decide(&typed, LanguageSet::default(), &validators);
            assert_eq!(
                decision.outcome,
                CorrectionOutcome::PreservedNoMatch,
                "{word}"
            );
            assert_eq!(decision.replacement, None);
        }
        let keys = "tried".chars().collect::<Vec<_>>();
        let typed = layout::text_from_keys(&keys, Language::KoDubeolsik).unwrap();
        let decision = decide(&typed, LanguageSet::default(), &validators);
        assert_eq!(decision.outcome, CorrectionOutcome::Replaced);
        assert_eq!(decision.replacement.as_deref(), Some("tried"));
    }
}
