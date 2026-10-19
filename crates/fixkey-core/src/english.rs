//! English validation over a bundled SCOWL/ESDB-derived lexicon with
//! inflection rules.
//!
//! Comparison is case-insensitive. Bundled validation uses each dictionary
//! entry's affix flags and the supplied rule conditions. The [`Lexicon`]
//! fixture interface also provides a smaller class-based inflection rule set.

use crate::decision::{LanguageValidator, Validation};

/// SCOWL/ESDB Hunspell validator. Affix flags belong to each dictionary
/// entry; a listed noun does not automatically acquire verb inflections.
pub struct HunspellValidator {
    dictionary: spellbook::Dictionary,
}

impl HunspellValidator {
    /// Loads the pinned US English dictionary compiled into the application.
    ///
    /// # Errors
    ///
    /// Returns the parser's error if the bundled dictionary cannot be loaded.
    pub fn bundled() -> Result<Self, spellbook::ParseDictionaryError> {
        Self::from_hunspell(
            include_str!("../assets/en-us/en_US.aff"),
            include_str!("../assets/en-us/en_US.dic"),
        )
    }

    /// Loads a Hunspell dictionary with case-insensitive entry comparison.
    /// Original affix flags and rule conditions are preserved.
    ///
    /// # Errors
    ///
    /// Returns the parser's error for invalid dictionary or affix data.
    pub fn from_hunspell(
        affixes: &str,
        entries: &str,
    ) -> Result<Self, spellbook::ParseDictionaryError> {
        // Lowercase only the entry, never its case-sensitive affix flags or
        // morphology fields. This also handles mixed-case listed names.
        let mut folded = String::with_capacity(entries.len());
        for line in entries.lines() {
            let split = line.find(['/', '\t', ' ']).unwrap_or(line.len());
            folded.push_str(&line[..split].to_lowercase());
            folded.push_str(&line[split..]);
            folded.push('\n');
        }
        spellbook::Dictionary::new(affixes, &folded).map(|dictionary| Self { dictionary })
    }
}

impl LanguageValidator for HunspellValidator {
    fn validate(&self, candidate: &str) -> Validation {
        if !candidate.is_empty() && self.dictionary.check(&candidate.to_lowercase()) {
            Validation::Accepted
        } else {
            Validation::Rejected
        }
    }
}

/// Lexicon entry class.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EntryClass {
    /// Ordinary word that may inflect.
    Common,
    /// Proper noun; accepted as listed and with a possessive.
    ProperNoun,
    /// Abbreviation; accepted only as listed.
    Abbreviation,
}

/// Lexicon lookup by lowercase word.
pub trait Lexicon {
    /// Returns the entry class of an exact lowercase `word`, if listed.
    fn lookup(&self, word: &str) -> Option<EntryClass>;
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
        .or_else(|| lower.strip_suffix("s'"))
    {
        return matches!(
            lexicon.lookup(base),
            Some(EntryClass::Common | EntryClass::ProperNoun)
        ) || is_inflected_common(lexicon, base);
    }
    is_inflected_common(lexicon, &lower)
}

fn common(lexicon: &dyn Lexicon, word: &str) -> bool {
    !word.is_empty() && lexicon.lookup(word) == Some(EntryClass::Common)
}

fn undoubled(stem: &str) -> Option<&str> {
    let mut chars = stem.chars().rev();
    let (last, previous) = (chars.next()?, chars.next()?);
    (last == previous && last.is_ascii_alphabetic()).then(|| &stem[..stem.len() - last.len_utf8()])
}

/// Applies `-s/-es/-ies`, `-ed/-ied`, `-ing`, `-er/-ier` and `-est/-iest`.
fn is_inflected_common(lexicon: &dyn Lexicon, word: &str) -> bool {
    let stems = |suffix: &str| word.strip_suffix(suffix).filter(|stem| stem.len() >= 2);
    if let Some(stem) = stems("ies")
        && common(lexicon, &format!("{stem}y"))
    {
        return true;
    }
    if let Some(stem) = stems("es")
        && common(lexicon, stem)
        && ["s", "x", "z", "ch", "sh", "o"]
            .iter()
            .any(|e| stem.ends_with(e))
    {
        return true;
    }
    if let Some(stem) = stems("s")
        && !stem.ends_with('s')
        && common(lexicon, stem)
    {
        return true;
    }
    if let Some(stem) = stems("ied")
        && common(lexicon, &format!("{stem}y"))
    {
        return true;
    }
    for suffix in ["ed", "ing", "er", "est"] {
        let Some(stem) = stems(suffix) else { continue };
        if common(lexicon, stem)
            || common(lexicon, &format!("{stem}e"))
            || undoubled(stem).is_some_and(|base| common(lexicon, base))
        {
            return true;
        }
    }
    for suffix in ["ier", "iest"] {
        if let Some(stem) = stems(suffix)
            && common(lexicon, &format!("{stem}y"))
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

    struct Fixture(HashMap<&'static str, EntryClass>);

    impl Lexicon for Fixture {
        fn lookup(&self, word: &str) -> Option<EntryClass> {
            self.0.get(word).copied()
        }
    }

    fn lexicon() -> Fixture {
        Fixture(HashMap::from([
            ("keyboard", EntryClass::Common),
            ("try", EntryClass::Common),
            ("stop", EntryClass::Common),
            ("make", EntryClass::Common),
            ("box", EntryClass::Common),
            ("happy", EntryClass::Common),
            ("big", EntryClass::Common),
            ("london", EntryClass::ProperNoun),
            ("nasa", EntryClass::Abbreviation),
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
    fn inflections_follow_the_rule_set() {
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
        assert!(!accepted("keyboardx"));
    }

    #[test]
    fn possessives_apply_to_common_words_and_proper_nouns() {
        assert!(accepted("keyboard's"));
        assert!(accepted("keyboards'"));
        assert!(accepted("London's"));
    }

    #[test]
    fn proper_nouns_and_abbreviations_require_a_listing() {
        assert!(accepted("London"));
        assert!(accepted("NASA"));
        assert!(!accepted("Paris"));
        // Abbreviations and proper nouns do not take common inflections.
        assert!(!accepted("nasas"));
        assert!(!accepted("londons"));
    }

    #[test]
    fn unlisted_and_degenerate_words_are_rejected() {
        assert!(!accepted("rpthrgo"));
        assert!(!accepted(""));
        assert!(!accepted("s"));
        assert!(!accepted("es"));
    }
}
