//! Core types and engine logic for `FixKey`.
//!
//! [`Language`], [`CorrectionOutcome`], [`SkipReason`], and
//! [`CandidateValidation`] provide stable identifiers. [`LanguageSet`] selects
//! the enabled layouts. The [`layout`] and [`tokenizer`] modules reconstruct
//! typed words and classify token boundaries. The [`decision`] and [`session`]
//! modules implement replacement decisions, safety checks, and transient undo
//! state.
//!
//! [`english::HunspellValidator`] loads the bundled SCOWL/ESDB US English
//! dictionary and its affix rules. [`mecab::MecabAnalyzer`] loads the compiled
//! Korean system dictionary from application resources using MeCab-Ko.
//! [`english::Lexicon`] and [`korean::KoreanAnalyzer`] also allow injected
//! data for tests. The session uses the [`session::Accessibility`] trait;
//! platform adapters and desktop integration are not included in this crate.
//!
//! The contract for this crate lives in
//! `docs/crates-fixkey-core-foundation.md`.

pub mod decision;
pub mod english;
pub mod korean;
pub mod layout;
pub mod manual;
pub mod mecab;
pub mod observation;
#[cfg(feature = "native")]
pub mod platform;
pub mod session;
pub mod tokenizer;
pub mod types;

pub use types::{CandidateValidation, CorrectionOutcome, Language, LanguageSet, SkipReason};
