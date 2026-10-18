//! Measures bundled dictionary initialization and fixture decision latency.
//!
//! No native input, accessibility target or installation package is opened.

use std::{
    io::{self, Write},
    path::Path,
    time::Instant,
};

use fixkey_core::{
    LanguageSet,
    decision::{Validators, decide},
    english::HunspellValidator,
    korean::KoreanValidator,
    mecab::MecabAnalyzer,
};

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let initialization = Instant::now();
    let english = HunspellValidator::bundled()
        .map_err(|_| io::Error::other("english-dictionary-unavailable"))?;
    let korean = KoreanValidator::new(MecabAnalyzer::load(
        &Path::new(env!("CARGO_MANIFEST_DIR")).join("assets/ko"),
    )?);
    let startup_ns = initialization.elapsed().as_nanos();
    let validators = Validators {
        korean: &korean,
        english: &english,
    };
    let fixtures = [("rPthrgo", "계속해"), ("zlqhemrk", "키보드가")];
    let mut durations = Vec::with_capacity(2000);
    for _ in 0..1000 {
        for (word, expected) in fixtures {
            let keys: Vec<_> = word.chars().collect();
            let started = Instant::now();
            let decision = decide(word, &keys, LanguageSet::default(), &validators);
            durations.push(started.elapsed().as_nanos());
            if decision.replacement.as_deref() != Some(expected) {
                return Err(io::Error::other("dictionary-profile-fixture-failed").into());
            }
        }
    }
    durations.sort_unstable();
    let record = serde_json::json!({
        "scope": "bundled-dictionary-fixtures",
        "version": env!("CARGO_PKG_VERSION"),
        "os": std::env::consts::OS,
        "architecture": std::env::consts::ARCH,
        "dictionary_initialization_ns": startup_ns,
        "decisions": durations.len(),
        "decision_p50_ns": durations[durations.len()/2],
        "decision_p95_ns": durations[durations.len()*95/100],
        "decision_max_ns": durations.last(),
        "native_application_acceptance": false,
        "desktop_startup_measured": false,
    });
    writeln!(io::stdout().lock(), "{record}")?;
    Ok(())
}
