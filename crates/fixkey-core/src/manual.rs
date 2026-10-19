//! Transient manual-candidate selection with guarded accessibility writes.
//!
//! The desktop owns popup placement and expected popup focus transitions;
//! this module retains only the source control and explicit word range.

use crate::{
    LanguageSet, SkipReason,
    decision::{ManualCandidate, Validators, manual_candidates},
    session::{Accessibility, CorrectionSession, FieldSensitivity, FieldSnapshot, PendingWrite},
};

/// Keyboard navigation direction.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Direction {
    /// Previous candidate.
    Previous,
    /// Next candidate.
    Next,
}

/// Stable manual-picker errors, without input text.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum ManualError {
    /// The source cannot be read or safely changed.
    #[error("manual-target-unavailable")]
    Unavailable(SkipReason),
    /// The captured context changed.
    #[error("manual-target-stale")]
    Stale,
    /// The requested range is not a layout-convertible word at the caret.
    #[error("manual-range-invalid")]
    InvalidRange,
    /// There is no open picker.
    #[error("manual-picker-closed")]
    Closed,
    /// A replacement failed; there is no automatic retry.
    #[error("manual-write-failed")]
    WriteFailed,
}

struct Picker {
    before: FieldSnapshot,
    start: usize,
    end: usize,
    candidates: Vec<ManualCandidate>,
    selected: usize,
}

/// A single open candidate list. Dropping or closing it drops all source
/// text. The original candidate remains selectable even with its layout off.
#[derive(Default)]
pub struct ManualSession {
    picker: Option<Picker>,
}

impl ManualSession {
    /// Opens candidates for `start..end`, immediately before the source caret.
    /// The caller must first establish the input location and permissions.
    ///
    /// # Errors
    ///
    /// Refuses unsafe, stale, selected or non-convertible source text.
    pub fn open(
        &mut self,
        start: usize,
        end: usize,
        enabled: LanguageSet,
        accessibility: &mut dyn Accessibility,
        validators: &Validators<'_>,
    ) -> Result<(), ManualError> {
        self.close();
        let before = safe_snapshot(accessibility)?;
        if start >= end || before.caret != end || end > before.text.chars().count() {
            return Err(ManualError::InvalidRange);
        }
        let word: String = before.text.chars().skip(start).take(end - start).collect();
        let candidates = manual_candidates(&word, enabled, validators);
        if candidates.is_empty() {
            return Err(ManualError::InvalidRange);
        }
        if safe_snapshot(accessibility)? != before {
            return Err(ManualError::Stale);
        }
        self.picker = Some(Picker {
            before,
            start,
            end,
            candidates,
            selected: 0,
        });
        tracing::info!(
            reason = "manual-picker-opened",
            "fixkey manual picker changed"
        );
        Ok(())
    }

    /// Returns the ephemeral list for the candidate popup only.
    #[must_use]
    pub fn candidates(&self) -> &[ManualCandidate] {
        self.picker
            .as_ref()
            .map_or(&[], |picker| &picker.candidates)
    }

    /// Returns the selected candidate index while open.
    #[must_use]
    pub fn selected(&self) -> Option<usize> {
        self.picker.as_ref().map(|picker| picker.selected)
    }

    /// Moves selection with wraparound; all displayed candidates, including
    /// unrecognized or unavailable-validator entries, remain selectable.
    pub const fn navigate(&mut self, direction: Direction) {
        if let Some(picker) = &mut self.picker {
            let count = picker.candidates.len();
            picker.selected = match direction {
                Direction::Next => (picker.selected + 1) % count,
                Direction::Previous => (picker.selected + count - 1) % count,
            };
        }
    }

    /// Invalidates the picker after an external focus, caret, text or
    /// selection change. The adapter must map expected popup focus back to
    /// the source control and report all other focus transitions normally.
    pub fn observe(&mut self, snapshot: &FieldSnapshot) {
        if self
            .picker
            .as_ref()
            .is_some_and(|picker| &picker.before != snapshot)
        {
            self.close();
        }
    }

    /// Closes on Esc, permission loss, pause, settings changes or exit.
    pub fn close(&mut self) {
        self.picker = None;
    }

    /// Applies one explicit Enter action after revalidating the source.
    /// Choosing the original closes without writing.
    ///
    /// # Errors
    ///
    /// Refuses stale or unsafe targets and reports a failed write. Partial
    /// failures use the same guarded, single recovery attempt as correction.
    pub fn apply(&mut self, accessibility: &mut dyn Accessibility) -> Result<(), ManualError> {
        let picker = self.picker.take().ok_or(ManualError::Closed)?;
        let current = safe_snapshot(accessibility)?;
        if current != picker.before {
            return Err(ManualError::Stale);
        }
        let candidate = &picker.candidates[picker.selected];
        if candidate.is_original {
            return Ok(());
        }
        let original: String = current
            .text
            .chars()
            .skip(picker.start)
            .take(picker.end - picker.start)
            .collect();
        if let Err(error) =
            accessibility.replace(current.target, picker.start, picker.end, &candidate.text)
        {
            CorrectionSession::recover_partial_write(
                &error,
                &PendingWrite {
                    before: &current,
                    start: picker.start,
                    end: picker.end,
                    original: &original,
                    boundary: "",
                },
                accessibility,
            );
            tracing::warn!(
                reason = "manual-write-failed",
                "fixkey manual selection failed"
            );
            return Err(ManualError::WriteFailed);
        }
        tracing::info!(
            reason = "manual-choice-applied",
            "fixkey manual selection applied"
        );
        Ok(())
    }
}

fn safe_snapshot(accessibility: &mut dyn Accessibility) -> Result<FieldSnapshot, ManualError> {
    let snapshot = accessibility.snapshot().map_err(ManualError::Unavailable)?;
    let reason = match snapshot.sensitivity {
        FieldSensitivity::Protected => Some(SkipReason::ProtectedField),
        FieldSensitivity::Unknown => Some(SkipReason::UnknownField),
        FieldSensitivity::Normal if !snapshot.supports_write => Some(SkipReason::UnsupportedWrite),
        FieldSensitivity::Normal => None,
    };
    if let Some(reason) = reason {
        return Err(ManualError::Unavailable(reason));
    }
    if snapshot.selection_len != 0 {
        return Err(ManualError::Stale);
    }
    Ok(snapshot)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{
        CandidateValidation, Language,
        decision::{LanguageValidator, MissingValidator, Validation},
        session::{TargetId, WriteError},
    };

    struct Reject;
    impl LanguageValidator for Reject {
        fn validate(&self, _: &str) -> Validation {
            Validation::Rejected
        }
    }
    struct Field {
        snapshot: FieldSnapshot,
        writes: usize,
        fail: bool,
    }
    impl Field {
        fn new() -> Self {
            Self {
                snapshot: FieldSnapshot {
                    target: TargetId(1),
                    sensitivity: FieldSensitivity::Normal,
                    supports_write: true,
                    text: "prefix zlqhemrk".into(),
                    caret: 15,
                    selection_len: 0,
                },
                writes: 0,
                fail: false,
            }
        }
    }
    impl Accessibility for Field {
        fn snapshot(&mut self) -> Result<FieldSnapshot, SkipReason> {
            Ok(self.snapshot.clone())
        }

        fn replace(
            &mut self,
            _: TargetId,
            start: usize,
            end: usize,
            text: &str,
        ) -> Result<(), WriteError> {
            self.writes += 1;
            if self.fail {
                return Err(WriteError::Failed);
            }
            let mut chars: Vec<_> = self.snapshot.text.chars().collect();
            chars.splice(start..end, text.chars());
            self.snapshot.text = chars.into_iter().collect();
            self.snapshot.caret = start + text.chars().count();
            Ok(())
        }
    }
    fn open(session: &mut ManualSession, field: &mut Field) {
        session
            .open(
                7,
                15,
                LanguageSet::default(),
                field,
                &Validators {
                    korean: &Reject,
                    english: &Reject,
                },
            )
            .unwrap();
    }

    #[test]
    fn explicit_unrecognized_choice_changes_only_the_word_and_closes() {
        let mut field = Field::new();
        let mut picker = ManualSession::default();
        open(&mut picker, &mut field);
        assert_eq!(
            picker.candidates()[1].validation,
            CandidateValidation::NotRecognized
        );
        picker.navigate(Direction::Next);
        picker.apply(&mut field).unwrap();
        assert_eq!(field.snapshot.text, "prefix 키보드가");
        assert_eq!(field.writes, 1);
        assert_eq!(picker.selected(), None);
        assert_eq!(picker.apply(&mut field), Err(ManualError::Closed));
    }

    #[test]
    fn original_and_escape_never_write_and_navigation_wraps() {
        let mut field = Field::new();
        let mut picker = ManualSession::default();
        open(&mut picker, &mut field);
        picker.navigate(Direction::Previous);
        assert_eq!(picker.selected(), Some(1));
        picker.navigate(Direction::Next);
        assert_eq!(picker.selected(), Some(0));
        picker.apply(&mut field).unwrap();
        assert_eq!(field.writes, 0);
        open(&mut picker, &mut field);
        picker.close();
        assert_eq!(picker.apply(&mut field), Err(ManualError::Closed));
    }

    #[test]
    fn stale_source_or_lost_safety_refuses_enter_and_discards_state() {
        for mutation in 0..6 {
            let mut field = Field::new();
            let mut picker = ManualSession::default();
            open(&mut picker, &mut field);
            picker.navigate(Direction::Next);
            match mutation {
                0 => field.snapshot.target = TargetId(2),
                1 => field.snapshot.caret -= 1,
                2 => field.snapshot.text.push('x'),
                3 => field.snapshot.selection_len = 1,
                4 => field.snapshot.sensitivity = FieldSensitivity::Protected,
                _ => field.snapshot.supports_write = false,
            }
            assert!(picker.apply(&mut field).is_err());
            assert_eq!(field.writes, 0);
            assert_eq!(picker.selected(), None);
        }
    }

    #[test]
    fn unavailable_validator_is_distinct_and_disabled_original_remains() {
        let mut field = Field::new();
        let mut picker = ManualSession::default();
        picker
            .open(
                7,
                15,
                LanguageSet::default().with(Language::EnUsQwerty, false),
                &mut field,
                &Validators {
                    korean: &MissingValidator,
                    english: &Reject,
                },
            )
            .unwrap();
        assert!(picker.candidates()[0].is_original);
        assert_eq!(
            picker.candidates()[1].validation,
            CandidateValidation::ValidatorUnavailable
        );
        picker.navigate(Direction::Next);
        picker.apply(&mut field).unwrap();
        assert_eq!(field.snapshot.text, "prefix 키보드가");
    }

    #[test]
    fn failed_writes_do_not_retry_and_external_observation_invalidates() {
        let mut field = Field::new();
        let mut picker = ManualSession::default();
        open(&mut picker, &mut field);
        picker.navigate(Direction::Next);
        field.fail = true;
        assert_eq!(picker.apply(&mut field), Err(ManualError::WriteFailed));
        assert_eq!(picker.apply(&mut field), Err(ManualError::Closed));
        assert_eq!(field.writes, 1);
        open(&mut picker, &mut field);
        field.snapshot.target = TargetId(3);
        picker.observe(&field.snapshot);
        assert_eq!(picker.selected(), None);
    }
}
