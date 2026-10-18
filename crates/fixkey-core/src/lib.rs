//! Core types and engine logic for `FixKey`.
//!
//! [`Language`], [`CorrectionOutcome`], [`SkipReason`], and
//! [`CandidateValidation`] provide stable identifiers. [`LanguageSet`] selects
//! the enabled layouts. The [`layout`] and [`tokenizer`] modules reconstruct
//! typed words and classify token boundaries. The [`decision`] and [`session`]
//! modules implement replacement decisions, safety checks, and transient undo
//! state.
//!
//! Language validation uses caller-provided data: [`english::Lexicon`] and
//! [`korean::KoreanAnalyzer`] are interfaces for the validators. This crate
//! does not bundle an English lexicon, a Korean dictionary, or a Korean
//! analyzer. The session uses the [`session::Accessibility`] trait; platform
//! adapters and desktop integration are not included in this crate.
//!
//! The contract for this crate lives in
//! `docs/crates-fixkey-core-foundation.md`.

pub mod decision;
pub mod english;
pub mod korean;
pub mod layout;
pub mod session;
pub mod tokenizer;
pub mod types;

pub use types::{CandidateValidation, CorrectionOutcome, Language, LanguageSet, SkipReason};
