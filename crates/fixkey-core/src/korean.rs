//! Korean validation: dictionary-backed morphological analysis plus an
//! explicit part-of-speech connection rule table.
//!
//! Tokenizer output alone is never validity. A candidate is accepted only when
//! the analyzer covers the whole word, every morpheme is reported as found in
//! the dictionary (not an unknown-word fallback), and every adjacent
//! part-of-speech pair appears in [`CONNECTION_RULES`].

use crate::decision::{LanguageValidator, Validation};

/// Part-of-speech tags, following the mecab-ko-dic tag set.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Pos {
    /// Common noun (NNG).
    Nng,
    /// Proper noun (NNP).
    Nnp,
    /// Dependent noun (NNB).
    Nnb,
    /// Pronoun (NP).
    Np,
    /// Numeral (NR).
    Nr,
    /// Verb (VV).
    Vv,
    /// Adjective (VA).
    Va,
    /// Auxiliary predicate (VX).
    Vx,
    /// Copula (VCP).
    Vcp,
    /// Determiner (MM).
    Mm,
    /// General adverb (MAG).
    Mag,
    /// Conjunctive adverb (MAJ).
    Maj,
    /// Interjection (IC).
    Ic,
    /// Case particle (JKS, JKC, JKG, JKO, JKB, JKV, JKQ).
    Jk,
    /// Auxiliary particle (JX).
    Jx,
    /// Conjunctive particle (JC).
    Jc,
    /// Pre-final ending (EP).
    Ep,
    /// Sentence-final ending (EF).
    Ef,
    /// Connective ending (EC).
    Ec,
    /// Nominalizing ending (ETN).
    Etn,
    /// Adnominal ending (ETM).
    Etm,
    /// Noun prefix (XPN).
    Xpn,
    /// Noun suffix (XSN).
    Xsn,
    /// Verb-deriving suffix (XSV).
    Xsv,
    /// Adjective-deriving suffix (XSA).
    Xsa,
    /// Root (XR).
    Xr,
}

impl Pos {
    /// Parses one mecab-ko-dic tag such as `NNG` or `JKS`.
    #[must_use]
    pub fn from_tag(tag: &str) -> Option<Self> {
        Some(match tag {
            "NNG" => Self::Nng,
            "NNP" => Self::Nnp,
            "NNB" => Self::Nnb,
            "NP" => Self::Np,
            "NR" => Self::Nr,
            "VV" => Self::Vv,
            "VA" => Self::Va,
            "VX" => Self::Vx,
            "VCP" => Self::Vcp,
            "MM" => Self::Mm,
            "MAG" => Self::Mag,
            "MAJ" => Self::Maj,
            "IC" => Self::Ic,
            "JKS" | "JKC" | "JKG" | "JKO" | "JKB" | "JKV" | "JKQ" => Self::Jk,
            "JX" => Self::Jx,
            "JC" => Self::Jc,
            "EP" => Self::Ep,
            "EF" => Self::Ef,
            "EC" => Self::Ec,
            "ETN" => Self::Etn,
            "ETM" => Self::Etm,
            "XPN" => Self::Xpn,
            "XSN" => Self::Xsn,
            "XSV" => Self::Xsv,
            "XSA" => Self::Xsa,
            "XR" => Self::Xr,
            _ => return None,
        })
    }

    /// Parses a possibly compound tag such as `XSV+EF`.
    #[must_use]
    pub fn parse_compound(tag: &str) -> Option<Vec<Self>> {
        tag.split('+').map(Self::from_tag).collect()
    }
}

/// One analyzed morpheme.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Morpheme {
    /// Surface text of the morpheme.
    pub surface: String,
    /// Part-of-speech components; more than one for inflected compounds.
    pub pos: Vec<Pos>,
    /// Whether the dictionary lists this morpheme. `false` marks an
    /// unknown-word fallback.
    pub in_dictionary: bool,
}

/// Analyzer failure; never treated as an invalid word.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum AnalyzerError {
    /// The dictionary is missing or could not be loaded.
    #[error("korean dictionary unavailable")]
    DictionaryUnavailable,
    /// The analyzer failed while processing the input.
    #[error("korean analysis failed")]
    Failed,
}

/// Morphological analyzer abstraction (backed by the pinned mecab-ko project).
///
/// Implementations must report dictionary provenance per morpheme through the
/// analyzer's dictionary interfaces and must not infer it from a plausible
/// part-of-speech tag.
pub trait KoreanAnalyzer {
    /// Analyzes one word.
    ///
    /// # Errors
    ///
    /// Returns [`AnalyzerError`] when analysis cannot be performed.
    fn analyze(&self, word: &str) -> Result<Vec<Morpheme>, AnalyzerError>;
}

use Pos::{
    Ec, Ef, Ep, Etm, Etn, Jc, Jk, Jx, Mag, Maj, Mm, Nnb, Nng, Nnp, Np, Nr, Va, Vcp, Vv, Vx, Xpn,
    Xr, Xsa, Xsn, Xsv,
};

/// Part-of-speech pairs that may be adjacent inside one word, as
/// `(previous, next)`.
pub const CONNECTION_RULES: &[(Pos, Pos)] = &[
    // Nouns take particles, suffixes, the copula and compound nouns.
    (Nng, Jk),
    (Nng, Jx),
    (Nng, Jc),
    (Nng, Xsn),
    (Nng, Xsv),
    (Nng, Xsa),
    (Nng, Vcp),
    (Nng, Nng),
    (Nnp, Jk),
    (Nnp, Jx),
    (Nnp, Jc),
    (Nnp, Xsn),
    (Nnp, Vcp),
    (Nnp, Nng),
    (Nnb, Jk),
    (Nnb, Jx),
    (Nnb, Jc),
    (Nnb, Vcp),
    (Nnb, Xsv),
    (Nnb, Xsa),
    (Np, Jk),
    (Np, Jx),
    (Np, Jc),
    (Np, Vcp),
    (Nr, Jk),
    (Nr, Jx),
    (Nr, Jc),
    (Nr, Vcp),
    (Nr, Xsn),
    // Prefixes, determiners and roots attach to what follows.
    (Xpn, Nng),
    (Xpn, Nnp),
    (Mm, Nng),
    (Mm, Nnb),
    (Xr, Xsn),
    (Xr, Xsv),
    (Xr, Xsa),
    // Suffixes continue like their word class.
    (Xsn, Jk),
    (Xsn, Jx),
    (Xsn, Jc),
    (Xsn, Xsv),
    (Xsn, Xsa),
    (Xsn, Vcp),
    // Predicate stems take endings, never particles directly.
    (Vv, Ep),
    (Vv, Ef),
    (Vv, Ec),
    (Vv, Etn),
    (Vv, Etm),
    (Va, Ep),
    (Va, Ef),
    (Va, Ec),
    (Va, Etn),
    (Va, Etm),
    (Vx, Ep),
    (Vx, Ef),
    (Vx, Ec),
    (Vx, Etn),
    (Vx, Etm),
    (Vcp, Ep),
    (Vcp, Ef),
    (Vcp, Ec),
    (Vcp, Etn),
    (Vcp, Etm),
    (Xsv, Ep),
    (Xsv, Ef),
    (Xsv, Ec),
    (Xsv, Etn),
    (Xsv, Etm),
    (Xsa, Ep),
    (Xsa, Ef),
    (Xsa, Ec),
    (Xsa, Etn),
    (Xsa, Etm),
    // Endings.
    (Ep, Ep),
    (Ep, Ef),
    (Ep, Ec),
    (Ep, Etn),
    (Ep, Etm),
    (Ec, Vx),
    (Ec, Vv),
    (Ec, Va),
    (Etn, Jk),
    (Etn, Jx),
    (Etn, Jc),
    (Etm, Nng),
    (Etm, Nnb),
    // Particles stack.
    (Jk, Jx),
    (Jk, Jc),
    (Jx, Jx),
    (Jc, Jx),
    // Adverbs.
    (Mag, Jx),
    (Mag, Mag),
    (Mag, Xsv),
    (Mag, Xsa),
];

/// Part-of-speech tags that may begin a word.
pub(crate) const WORD_START: &[Pos] = &[
    Nng,
    Nnp,
    Nnb,
    Np,
    Nr,
    Vv,
    Va,
    Vx,
    Vcp,
    Mm,
    Mag,
    Maj,
    Xpn,
    Xr,
    Pos::Ic,
];

/// Part-of-speech tags that may end a complete word.
pub(crate) const WORD_END: &[Pos] = &[
    Nng,
    Nnp,
    Nnb,
    Np,
    Nr,
    Mag,
    Maj,
    Pos::Ic,
    Jk,
    Jx,
    Jc,
    Ef,
    Etn,
    Xsn,
];

/// Returns whether `previous` may be directly followed by `next`.
#[must_use]
pub fn connects(previous: Pos, next: Pos) -> bool {
    CONNECTION_RULES.contains(&(previous, next))
}

/// Checks a complete analysis against the dictionary and rule table.
#[must_use]
pub fn accepts_analysis(word: &str, morphemes: &[Morpheme]) -> bool {
    if morphemes.is_empty() {
        return false;
    }
    let covered: String = morphemes.iter().map(|m| m.surface.as_str()).collect();
    if covered != word {
        return false;
    }
    if morphemes
        .iter()
        .any(|m| !m.in_dictionary || m.pos.is_empty())
    {
        return false;
    }
    let tags: Vec<Pos> = morphemes
        .iter()
        .flat_map(|m| m.pos.iter().copied())
        .collect();
    let (Some(first), Some(last)) = (tags.first(), tags.last()) else {
        return false;
    };
    WORD_START.contains(first)
        && WORD_END.contains(last)
        && tags.windows(2).all(|pair| connects(pair[0], pair[1]))
}

/// Korean validator built on an analyzer.
pub struct KoreanValidator<A> {
    analyzer: A,
}

impl<A: KoreanAnalyzer> KoreanValidator<A> {
    /// Creates a validator.
    pub const fn new(analyzer: A) -> Self {
        Self { analyzer }
    }
}

impl<A: KoreanAnalyzer> LanguageValidator for KoreanValidator<A> {
    fn validate(&self, candidate: &str) -> Validation {
        match self.analyzer.analyze(candidate) {
            Ok(morphemes) if accepts_analysis(candidate, &morphemes) => Validation::Accepted,
            Ok(_) => Validation::Rejected,
            Err(error) => {
                tracing::warn!(?error, "korean validator unavailable");
                Validation::Unavailable
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn m(surface: &str, tag: &str, in_dictionary: bool) -> Morpheme {
        Morpheme {
            surface: surface.into(),
            pos: Pos::parse_compound(tag).unwrap(),
            in_dictionary,
        }
    }

    struct Scripted(Result<Vec<Morpheme>, AnalyzerError>);

    impl KoreanAnalyzer for Scripted {
        fn analyze(&self, _word: &str) -> Result<Vec<Morpheme>, AnalyzerError> {
            self.0.clone()
        }
    }

    fn validate(word: &str, result: Result<Vec<Morpheme>, AnalyzerError>) -> Validation {
        KoreanValidator::new(Scripted(result)).validate(word)
    }

    #[test]
    fn noun_plus_subject_particle_is_accepted() {
        let analysis = vec![m("키보드", "NNG", true), m("가", "JKS", true)];
        assert_eq!(validate("키보드가", Ok(analysis)), Validation::Accepted);
    }

    #[test]
    fn noun_plus_inflected_verb_suffix_is_accepted() {
        let analysis = vec![m("계속", "NNG", true), m("해", "XSV+EF", true)];
        assert_eq!(validate("계속해", Ok(analysis)), Validation::Accepted);
    }

    #[test]
    fn complete_nominalization_is_accepted() {
        let nominalization = vec![m("먹", "VV", true), m("기", "ETN", true)];
        assert!(accepts_analysis("먹기", &nominalization));
        assert_eq!(validate("먹기", Ok(nominalization)), Validation::Accepted);

        let with_particle = vec![
            m("먹", "VV", true),
            m("기", "ETN", true),
            m("가", "JKS", true),
        ];
        assert!(accepts_analysis("먹기가", &with_particle));
        assert_eq!(validate("먹기가", Ok(with_particle)), Validation::Accepted);

        let sentence_final = vec![m("먹", "VV", true), m("다", "EF", true)];
        assert!(accepts_analysis("먹다", &sentence_final));
        assert_eq!(validate("먹다", Ok(sentence_final)), Validation::Accepted);
    }

    #[test]
    fn incomplete_or_unknown_nominalizations_are_rejected() {
        let unknown_stem = vec![m("먹", "VV", false), m("기", "ETN", true)];
        let unknown_ending = vec![m("먹", "VV", true), m("기", "ETN", false)];
        let partial = vec![m("먹", "VV", true), m("기", "ETN", true)];
        let invalid_connection = vec![m("먹", "NNG", true), m("기", "ETN", true)];

        for (word, analysis) in [
            ("먹기", unknown_stem),
            ("먹기", unknown_ending),
            ("먹기가", partial),
            ("먹기", invalid_connection),
        ] {
            assert!(!accepts_analysis(word, &analysis));
            assert_eq!(validate(word, Ok(analysis)), Validation::Rejected);
        }

        let bare_stem = vec![m("먹", "VV", true)];
        let connective = vec![m("먹", "VV", true), m("고", "EC", true)];
        assert!(!accepts_analysis("먹", &bare_stem));
        assert!(!accepts_analysis("먹고", &connective));
        assert_eq!(validate("먹", Ok(bare_stem)), Validation::Rejected);
        assert_eq!(validate("먹고", Ok(connective)), Validation::Rejected);
    }

    #[test]
    fn unknown_morphemes_are_rejected_even_with_a_plausible_tag() {
        let analysis = vec![m("키보드", "NNG", false), m("가", "JKS", true)];
        assert_eq!(validate("키보드가", Ok(analysis)), Validation::Rejected);
    }

    #[test]
    fn invalid_connections_are_rejected() {
        // A particle cannot directly follow a verb stem.
        let analysis = vec![m("가", "VV", true), m("가", "JKS", true)];
        assert_eq!(validate("가가", Ok(analysis)), Validation::Rejected);
        // A particle cannot start a word.
        assert_eq!(
            validate("가", Ok(vec![m("가", "JKS", true)])),
            Validation::Rejected
        );
        // A bare stem or connective ending is not a complete word.
        assert_eq!(
            validate("가", Ok(vec![m("가", "VV", true)])),
            Validation::Rejected
        );
        let connective = vec![m("먹", "VV", true), m("고", "EC", true)];
        assert_eq!(validate("먹고", Ok(connective)), Validation::Rejected);
    }

    #[test]
    fn incomplete_analysis_is_rejected() {
        let partial = vec![m("키보드", "NNG", true)];
        assert_eq!(validate("키보드가", Ok(partial)), Validation::Rejected);
        assert_eq!(validate("키보드가", Ok(vec![])), Validation::Rejected);
    }

    #[test]
    fn analyzer_failures_are_unavailable_not_rejected() {
        assert_eq!(
            validate("키보드가", Err(AnalyzerError::DictionaryUnavailable)),
            Validation::Unavailable
        );
        assert_eq!(
            validate("키보드가", Err(AnalyzerError::Failed)),
            Validation::Unavailable
        );
    }

    #[test]
    fn tags_parse_including_compounds() {
        assert_eq!(Pos::from_tag("JKB"), Some(Pos::Jk));
        assert_eq!(Pos::parse_compound("XSV+EF"), Some(vec![Pos::Xsv, Pos::Ef]));
        assert_eq!(Pos::parse_compound("SF"), None);
    }
}
