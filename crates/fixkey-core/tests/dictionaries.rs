//! Tests against the full bundled dictionaries, without native application
//! access.

use std::{fmt::Write, path::Path, sync::OnceLock};

use fixkey_core::{
    CorrectionOutcome, LanguageSet,
    decision::{LanguageValidator, Validation, Validators, decide},
    english::HunspellValidator,
    korean::{KoreanAnalyzer, KoreanValidator, accepts_analysis},
    mecab::MecabAnalyzer,
};
use sha2::{Digest, Sha256};

#[test]
fn bundled_resources_match_pinned_checksums() {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("assets");
    let manifest: serde_json::Value =
        serde_json::from_str(include_str!("../assets/manifest.json")).unwrap();
    for (relative, expected) in manifest["files"].as_object().unwrap() {
        let bytes = std::fs::read(root.join(relative)).unwrap();
        let mut actual = String::new();
        for byte in Sha256::digest(bytes) {
            write!(actual, "{byte:02x}").unwrap();
        }
        assert_eq!(actual, expected.as_str().unwrap(), "{relative}");
    }
}

fn korean() -> &'static MecabAnalyzer {
    static ANALYZER: OnceLock<MecabAnalyzer> = OnceLock::new();
    ANALYZER.get_or_init(|| {
        MecabAnalyzer::load(
            Path::new(env!("CARGO_MANIFEST_DIR"))
                .join("assets/ko")
                .as_path(),
        )
        .expect("hydrate the bundled Korean dictionary with git lfs pull")
    })
}

struct BorrowedAnalyzer;

impl KoreanAnalyzer for BorrowedAnalyzer {
    fn analyze(
        &self,
        word: &str,
    ) -> Result<Vec<fixkey_core::korean::Morpheme>, fixkey_core::korean::AnalyzerError> {
        korean().analyze(word)
    }
}

#[test]
fn bundled_us_dictionary_uses_entry_affix_flags() {
    let english = HunspellValidator::bundled().unwrap();
    for word in [
        "keyboard",
        "keyboards",
        "keyboard's",
        "making",
        "stopped",
        "stopping",
        "tries",
        "tried",
        "happier",
        "happiest",
        "London",
        "london",
        "NASA",
        "nasa",
        "eBay",
        "EBAY",
    ] {
        assert_eq!(english.validate(word), Validation::Accepted, "{word}");
    }
    // Inflections must be licensed by flags, not just by an existing stem.
    for word in ["londoned", "nasas", "nasaly", "rPthrgo", "zlqhemrk", ""] {
        assert_eq!(english.validate(word), Validation::Rejected, "{word}");
    }
}

#[test]
fn case_folding_preserves_affix_flags_and_listed_mixed_case_names() {
    let english = HunspellValidator::from_hunspell(
        "SET UTF-8\nSFX S Y 1\nSFX S 0 s .\n",
        "3\neBay\nkeyboard/S\nNASA\n",
    )
    .unwrap();
    for word in ["ebay", "EBay", "nasa", "NaSa", "keyboards"] {
        assert_eq!(english.validate(word), Validation::Accepted, "{word}");
    }
    for word in ["ebays", "nasas", "keyboarded", "unlisted"] {
        assert_eq!(english.validate(word), Validation::Rejected, "{word}");
    }
    assert!(HunspellValidator::from_hunspell("SFX S Y 1\nSFX S", "1\nword").is_err());
}

#[test]
fn korean_full_dictionary_covers_required_morphology() {
    for word in ["계속해", "키보드가", "먹기", "먹기가", "먹다", "안녕하세요"] {
        let analysis = korean().analyze(word).unwrap();
        assert!(
            analysis.iter().all(|m| m.in_dictionary),
            "{word}: {analysis:?}"
        );
        assert!(accepts_analysis(word, &analysis), "{word}: {analysis:?}");
    }
}

#[test]
fn unknown_fallback_never_proves_dictionary_membership() {
    for word in ["뙇뛣쀍", "쀍쀍쀍가"] {
        let analysis = korean().analyze(word).unwrap();
        assert!(analysis.iter().any(|m| !m.in_dictionary), "{analysis:?}");
        assert!(!accepts_analysis(word, &analysis));
    }
    // Ambiguous surfaces can also have nominal analyses; the POS fixtures
    // separately reject bare predicates and connective-only analyses.
    let word = "겠";
    let analysis = korean().analyze(word).unwrap();
    assert!(!accepts_analysis(word, &analysis), "{analysis:?}");
    assert!(MecabAnalyzer::load(Path::new("/nonexistent-fixkey-dictionary")).is_err());
}

#[test]
fn required_corrections_use_both_real_dictionaries() {
    let english = HunspellValidator::bundled().unwrap();
    let korean = KoreanValidator::new(BorrowedAnalyzer);
    let validators = Validators {
        korean: &korean,
        english: &english,
    };
    for (keys, replacement) in [("rPthrgo", "계속해"), ("zlqhemrk", "키보드가")] {
        let decision = decide(
            keys,
            &keys.chars().collect::<Vec<_>>(),
            LanguageSet::default(),
            &validators,
        );
        assert_eq!(decision.outcome, CorrectionOutcome::Replaced);
        assert_eq!(decision.replacement.as_deref(), Some(replacement));
    }
    let keys: Vec<_> = "keyboard".chars().collect();
    let rendered = fixkey_core::layout::reconstruct("keyboard", LanguageSet::default());
    let korean_text = rendered
        .iter()
        .find(|c| c.language == fixkey_core::Language::KoDubeolsik)
        .unwrap();
    let decision = decide(
        &korean_text.text,
        &keys,
        LanguageSet::default(),
        &validators,
    );
    assert_eq!(decision.replacement.as_deref(), Some("keyboard"));
}
