//! FixKey core: layout reconstruction, language validation and correction
//! state, independent from UI rendering and platform accessibility adapters.
//!
//! The contract for this crate lives in
//! `docs/crates-fixkey-core-foundation.md`.

pub mod types;

pub use types::{CandidateValidation, CorrectionOutcome, Language, LanguageSet, SkipReason};
