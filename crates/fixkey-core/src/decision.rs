//! Language validators and the automatic replacement decision.

use crate::{
    layout::{Reconstruction, detect_language, keys_from_text, reconstruct, reconstruct_from_keys},
    types::{CandidateValidation, CorrectionOutcome, Language, LanguageSet},
};

/// Result of validating one candidate.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Validation {
    /// The language accepts the candidate.
    Accepted,
    /// The language definitively rejects the candidate.
    Rejected,
    /// The validator is unavailable or failed; this is not a rejection.
    Unavailable,
}

/// Validates candidate text for one language.
pub trait LanguageValidator {
    /// Validates `candidate`.
    fn validate(&self, candidate: &str) -> Validation;
}

/// Validator used when a required dictionary is missing.
#[derive(Debug, Default, Clone, Copy)]
pub struct MissingValidator;

impl LanguageValidator for MissingValidator {
    fn validate(&self, _candidate: &str) -> Validation {
        Validation::Unavailable
    }
}

/// The validators for every supported language.
pub struct Validators<'a> {
    /// Korean validator.
    pub korean: &'a dyn LanguageValidator,
    /// English validator.
    pub english: &'a dyn LanguageValidator,
}

impl Validators<'_> {
    fn validate(&self, language: Language, candidate: &str) -> Validation {
        match language {
            Language::KoDubeolsik => self.korean.validate(candidate),
            Language::EnUsQwerty => self.english.validate(candidate),
        }
    }
}

/// One validated interpretation of a word.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Candidate {
    /// Layout the candidate was reconstructed under.
    pub language: Language,
    /// Candidate text.
    pub text: String,
    /// Validation result.
    pub validation: Validation,
}

/// The decision for one completed word.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Decision {
    /// Outcome category.
    pub outcome: CorrectionOutcome,
    /// Replacement text when [`CorrectionOutcome::Replaced`].
    pub replacement: Option<String>,
    /// Language that supplied the replacement.
    pub language: Option<Language>,
}

impl Decision {
    const fn preserved(outcome: CorrectionOutcome) -> Self {
        Self {
            outcome,
            replacement: None,
            language: None,
        }
    }
}

fn validate_all(
    reconstructions: Vec<Reconstruction>,
    validators: &Validators<'_>,
) -> Vec<Candidate> {
    reconstructions
        .into_iter()
        .map(|reconstruction| Candidate {
            validation: validators.validate(reconstruction.language, &reconstruction.text),
            language: reconstruction.language,
            text: reconstruction.text,
        })
        .collect()
}

/// Decides whether rendered `word` is replaced using its recorded physical
/// keys.
///
/// `recorded_keys` must come from direct key events for this word and context.
/// Missing, unsupported or mismatched keys skip correction. Text inferred from
/// the rendered word is never treated as physical-key provenance.
///
/// Replaces only when exactly one enabled language accepts its candidate and
/// that candidate differs from `word`. If any required enabled-language
/// validator is unavailable the word is preserved, because an unavailable
/// validator is not an invalid result.
#[must_use]
pub fn decide(
    word: &str,
    recorded_keys: &[char],
    enabled: LanguageSet,
    validators: &Validators<'_>,
) -> Decision {
    let Some(reconstructions) = reconstruct_from_keys(word, recorded_keys, enabled) else {
        return Decision::preserved(CorrectionOutcome::PreservedSkipped);
    };
    let candidates = validate_all(reconstructions, validators);
    if candidates.is_empty() {
        return Decision::preserved(CorrectionOutcome::PreservedNoMatch);
    }
    if candidates
        .iter()
        .any(|candidate| candidate.validation == Validation::Unavailable)
    {
        return Decision::preserved(CorrectionOutcome::PreservedValidatorUnavailable);
    }
    let mut accepted = candidates
        .into_iter()
        .filter(|candidate| candidate.validation == Validation::Accepted);
    match (accepted.next(), accepted.next()) {
        (None, _) => Decision::preserved(CorrectionOutcome::PreservedNoMatch),
        (Some(_), Some(_)) => Decision::preserved(CorrectionOutcome::PreservedAmbiguous),
        (Some(winner), None) if winner.text == word => {
            Decision::preserved(CorrectionOutcome::PreservedUnchanged)
        }
        (Some(winner), None) => Decision {
            outcome: CorrectionOutcome::Replaced,
            replacement: Some(winner.text),
            language: Some(winner.language),
        },
    }
}

/// One entry of the manual candidate list.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ManualCandidate {
    /// Layout the candidate was reconstructed under.
    pub language: Language,
    /// Candidate text.
    pub text: String,
    /// Whether this entry is the word as typed.
    pub is_original: bool,
    /// Validation state shown with the candidate.
    pub validation: CandidateValidation,
}

/// Builds the manual candidate list: the original first, then each other
/// enabled-language candidate. Candidates that are layout-convertible but
/// invalid stay selectable.
#[must_use]
pub fn manual_candidates(
    word: &str,
    enabled: LanguageSet,
    validators: &Validators<'_>,
) -> Vec<ManualCandidate> {
    let Some(original_language) = detect_language(word) else {
        return Vec::new();
    };
    if keys_from_text(word, original_language).is_none() {
        return Vec::new();
    }
    let mut reconstructions = vec![Reconstruction {
        language: original_language,
        text: word.to_owned(),
    }];
    reconstructions.extend(
        reconstruct(word, enabled)
            .into_iter()
            .filter(|candidate| candidate.language != original_language),
    );
    validate_all(reconstructions, validators)
        .into_iter()
        .enumerate()
        .map(|(index, candidate)| ManualCandidate {
            is_original: index == 0,
            language: candidate.language,
            text: candidate.text,
            validation: match candidate.validation {
                Validation::Accepted => CandidateValidation::Recognized,
                Validation::Rejected => CandidateValidation::NotRecognized,
                Validation::Unavailable => CandidateValidation::ValidatorUnavailable,
            },
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{
        english::{EnglishValidator, EntryClass, Lexicon},
        korean::{AnalyzerError, KoreanAnalyzer, KoreanValidator, Morpheme, Pos},
        layout::text_from_keys,
    };

    struct Abbreviations;

    impl Lexicon for Abbreviations {
        fn lookup(&self, word: &str) -> Option<EntryClass> {
            (word == "nasa").then_some(EntryClass::Abbreviation)
        }
    }

    struct Fixed(&'static [&'static str]);

    impl LanguageValidator for Fixed {
        fn validate(&self, candidate: &str) -> Validation {
            if self.0.contains(&candidate) {
                Validation::Accepted
            } else {
                Validation::Rejected
            }
        }
    }

    fn all() -> LanguageSet {
        LanguageSet::default()
    }

    struct NominalizationAnalyzer;

    impl KoreanAnalyzer for NominalizationAnalyzer {
        fn analyze(&self, word: &str) -> Result<Vec<Morpheme>, AnalyzerError> {
            if word != "먹기" {
                return Ok(vec![]);
            }
            Ok(vec![
                Morpheme {
                    surface: "먹".into(),
                    pos: vec![Pos::Vv],
                    in_dictionary: true,
                },
                Morpheme {
                    surface: "기".into(),
                    pos: vec![Pos::Etn],
                    in_dictionary: true,
                },
            ])
        }
    }

    #[test]
    fn unique_korean_nominalization_replaces_wrong_layout() {
        let korean = KoreanValidator::new(NominalizationAnalyzer);
        let english = Fixed(&[]);
        let decision = decide(
            "ajrrl",
            &keys("ajrrl"),
            all(),
            &Validators {
                korean: &korean,
                english: &english,
            },
        );
        assert_eq!(decision.outcome, CorrectionOutcome::Replaced);
        assert_eq!(decision.replacement.as_deref(), Some("먹기"));
        assert_eq!(decision.language, Some(Language::KoDubeolsik));
    }

    fn keys(typed: &str) -> Vec<char> {
        typed.chars().collect()
    }

    #[test]
    fn english_abbreviation_replacement_preserves_recorded_key_case() {
        let english = EnglishValidator::new(Abbreviations);
        let korean = Fixed(&[]);
        let validators = Validators {
            korean: &korean,
            english: &english,
        };
        assert_eq!(english.validate("NASA"), Validation::Accepted);
        assert_eq!(
            text_from_keys(&keys("NASA"), Language::EnUsQwerty),
            Some("NASA".into())
        );
        for typed in ["NASA", "nasa", "NaSa"] {
            let rendered = text_from_keys(&keys(typed), Language::KoDubeolsik).unwrap();
            assert_eq!(rendered, "ㅜㅁㄴㅁ");
            let decision = decide(&rendered, &keys(typed), all(), &validators);
            assert_eq!(decision.outcome, CorrectionOutcome::Replaced);
            assert_eq!(decision.replacement.as_deref(), Some(typed));
            assert_eq!(decision.language, Some(Language::EnUsQwerty));
        }
        let unchanged = decide("NASA", &keys("NASA"), all(), &validators);
        assert_eq!(unchanged.outcome, CorrectionOutcome::PreservedUnchanged);
    }

    #[test]
    fn korean_syllable_does_not_erase_recorded_shift() {
        let english = Fixed(&["rK", "rk"]);
        for typed in ["rK", "rk"] {
            let decision = decide(
                "가",
                &keys(typed),
                all(),
                &Validators {
                    korean: &Fixed(&[]),
                    english: &english,
                },
            );
            assert_eq!(decision.outcome, CorrectionOutcome::Replaced);
            assert_eq!(decision.replacement.as_deref(), Some(typed));
        }
    }

    #[test]
    fn provenance_and_existing_decision_safety_remain_required() {
        let korean = Fixed(&["ㅜㅁㄴㅁ"]);
        let english = EnglishValidator::new(Abbreviations);
        let validators = Validators {
            korean: &korean,
            english: &english,
        };
        let word = "ㅜㅁㄴㅁ";
        assert_eq!(
            decide(word, &keys("NASA"), all(), &validators).outcome,
            CorrectionOutcome::PreservedAmbiguous
        );
        assert_eq!(
            decide(word, &[], all(), &validators).outcome,
            CorrectionOutcome::PreservedSkipped
        );
        assert_eq!(
            decide(word, &keys("other"), all(), &validators).outcome,
            CorrectionOutcome::PreservedSkipped
        );
        let unavailable = Validators {
            korean: &MissingValidator,
            english: &english,
        };
        assert_eq!(
            decide(word, &keys("NASA"), all(), &unavailable).outcome,
            CorrectionOutcome::PreservedValidatorUnavailable
        );
        let korean_only = all().with(Language::EnUsQwerty, false);
        let rejected = Validators {
            korean: &Fixed(&[]),
            english: &english,
        };
        assert_eq!(
            decide(word, &keys("NASA"), korean_only, &rejected).outcome,
            CorrectionOutcome::PreservedNoMatch
        );
        assert_eq!(
            decide(word, &keys("NASA"), all(), &rejected).outcome,
            CorrectionOutcome::Replaced
        );
    }

    #[test]
    fn exactly_one_accepting_language_replaces() {
        let ko = Fixed(&["계속해", "키보드가"]);
        let en = Fixed(&["keyboard"]);
        let validators = Validators {
            korean: &ko,
            english: &en,
        };
        for (word, expected) in [("rPthrgo", "계속해"), ("zlqhemrk", "키보드가")] {
            let decision = decide(word, &keys(word), all(), &validators);
            assert_eq!(decision.outcome, CorrectionOutcome::Replaced);
            assert_eq!(decision.replacement.as_deref(), Some(expected));
            assert_eq!(decision.language, Some(Language::KoDubeolsik));
        }
        let reverse = decide(
            "키보드가",
            &keys("zlqhemrk"),
            all(),
            &Validators {
                korean: &Fixed(&[]),
                english: &Fixed(&["zlqhemrk"]),
            },
        );
        assert_eq!(reverse.replacement.as_deref(), Some("zlqhemrk"));
        assert_eq!(reverse.language, Some(Language::EnUsQwerty));
    }

    #[test]
    fn zero_accepting_languages_preserve() {
        let none = Fixed(&[]);
        let decision = decide(
            "rPthrgo",
            &keys("rPthrgo"),
            all(),
            &Validators {
                korean: &none,
                english: &none,
            },
        );
        assert_eq!(decision.outcome, CorrectionOutcome::PreservedNoMatch);
        assert_eq!(decision.replacement, None);
    }

    #[test]
    fn multiple_accepting_languages_preserve() {
        let ko = Fixed(&["계속해"]);
        let en = Fixed(&["rPthrgo"]);
        let decision = decide(
            "rPthrgo",
            &keys("rPthrgo"),
            all(),
            &Validators {
                korean: &ko,
                english: &en,
            },
        );
        assert_eq!(decision.outcome, CorrectionOutcome::PreservedAmbiguous);
    }

    #[test]
    fn an_unchanged_winner_is_preserved() {
        let ko = Fixed(&[]);
        let en = Fixed(&["hello"]);
        let decision = decide(
            "hello",
            &keys("hello"),
            all(),
            &Validators {
                korean: &ko,
                english: &en,
            },
        );
        assert_eq!(decision.outcome, CorrectionOutcome::PreservedUnchanged);
    }

    #[test]
    fn disabled_languages_are_ignored() {
        let ko = Fixed(&["계속해"]);
        let en = Fixed(&[]);
        let validators = Validators {
            korean: &ko,
            english: &en,
        };
        let english_only = all().with(Language::KoDubeolsik, false);
        assert_eq!(
            decide("rPthrgo", &keys("rPthrgo"), english_only, &validators).outcome,
            CorrectionOutcome::PreservedNoMatch
        );
    }

    #[test]
    fn an_unavailable_validator_preserves_even_if_another_language_accepts() {
        let en = Fixed(&[]);
        let decision = decide(
            "rPthrgo",
            &keys("rPthrgo"),
            all(),
            &Validators {
                korean: &MissingValidator,
                english: &en,
            },
        );
        assert_eq!(
            decision.outcome,
            CorrectionOutcome::PreservedValidatorUnavailable
        );
        // A disabled language's unavailable validator is not required.
        let ko_off = all().with(Language::KoDubeolsik, false);
        assert_eq!(
            decide(
                "rPthrgo",
                &keys("rPthrgo"),
                ko_off,
                &Validators {
                    korean: &MissingValidator,
                    english: &en,
                }
            )
            .outcome,
            CorrectionOutcome::PreservedNoMatch
        );
    }

    #[test]
    fn unsupported_words_are_skipped() {
        let none = Fixed(&[]);
        let decision = decide(
            "a가",
            &keys("a가"),
            all(),
            &Validators {
                korean: &none,
                english: &none,
            },
        );
        assert_eq!(decision.outcome, CorrectionOutcome::PreservedSkipped);
    }

    #[test]
    fn manual_candidates_list_the_original_first_with_validation_labels() {
        let ko = Fixed(&[]);
        let en = Fixed(&["rPthrgo"]);
        let list = manual_candidates(
            "rPthrgo",
            all(),
            &Validators {
                korean: &ko,
                english: &en,
            },
        );
        assert_eq!(list.len(), 2);
        assert!(list[0].is_original);
        assert_eq!(list[0].validation, CandidateValidation::Recognized);
        assert_eq!(list[1].text, "계속해");
        assert_eq!(list[1].validation, CandidateValidation::NotRecognized);

        let unavailable = manual_candidates(
            "rPthrgo",
            all(),
            &Validators {
                korean: &MissingValidator,
                english: &en,
            },
        );
        assert_eq!(
            unavailable[1].validation,
            CandidateValidation::ValidatorUnavailable
        );
    }

    #[test]
    fn manual_candidates_keep_disabled_layout_original_once() {
        let unavailable = MissingValidator;
        let validators = Validators {
            korean: &unavailable,
            english: &unavailable,
        };
        for (word, disabled, original, alternate) in [
            ("키보드가", Language::KoDubeolsik, "키보드가", "zlqhemrk"),
            ("rPthrgo", Language::EnUsQwerty, "rPthrgo", "계속해"),
        ] {
            for enabled in [all(), all().with(disabled, false)] {
                let list = manual_candidates(word, enabled, &validators);
                assert_eq!(list.len(), 2, "word: {word}, enabled: {enabled:?}");
                assert_eq!(list[0].language, disabled);
                assert_eq!(list[0].text, original);
                assert!(list[0].is_original);
                assert_eq!(list[1].text, alternate);
                assert!(!list[1].is_original);
                assert_eq!(
                    list.iter()
                        .filter(|candidate| candidate.is_original)
                        .count(),
                    1
                );
                assert!(list.iter().all(|candidate| {
                    candidate.validation == CandidateValidation::ValidatorUnavailable
                }));
            }
        }
    }

    #[test]
    fn manual_candidates_preserve_validation_and_filter_disabled_alternates() {
        let accepted_korean = Fixed(&["키보드가"]);
        let accepted_english = Fixed(&["zlqhemrk"]);
        let rejected = Fixed(&[]);
        let unavailable = MissingValidator;
        let korean_disabled = all().with(Language::KoDubeolsik, false);

        for (korean, english, original_validation, alternate_validation) in [
            (
                &accepted_korean as &dyn LanguageValidator,
                &rejected as &dyn LanguageValidator,
                CandidateValidation::Recognized,
                CandidateValidation::NotRecognized,
            ),
            (
                &rejected,
                &accepted_english,
                CandidateValidation::NotRecognized,
                CandidateValidation::Recognized,
            ),
            (
                &unavailable,
                &unavailable,
                CandidateValidation::ValidatorUnavailable,
                CandidateValidation::ValidatorUnavailable,
            ),
        ] {
            let validators = Validators { korean, english };
            let list = manual_candidates("키보드가", korean_disabled, &validators);
            assert_eq!(list.len(), 2);
            assert_eq!(list[0].language, Language::KoDubeolsik);
            assert_eq!(list[0].text, "키보드가");
            assert_eq!(list[0].validation, original_validation);
            assert_eq!(list[1].language, Language::EnUsQwerty);
            assert_eq!(list[1].text, "zlqhemrk");
            assert_eq!(list[1].validation, alternate_validation);
        }

        let english_disabled = all().with(Language::EnUsQwerty, false);
        let list = manual_candidates(
            "키보드가",
            english_disabled,
            &Validators {
                korean: &accepted_korean,
                english: &unavailable,
            },
        );
        assert_eq!(list.len(), 1);
        assert_eq!(list[0].text, "키보드가");
        assert!(list[0].is_original);
    }
}
