//! Correction session: safety checks, replacement, immediate undo and
//! transient state handling for one active correction context.
//!
//! The session never persists or logs input text. It talks to the focused
//! control only through [`Accessibility`], so platform adapters and tests plug
//! in behind the same trait. There is no clipboard or synthetic typing
//! fallback and failed corrections are never retried.

use crate::{
    decision::{Validators, decide},
    types::{CorrectionOutcome, LanguageSet, SkipReason},
};

/// Opaque identity of the focused editable control.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct TargetId(pub u64);

/// Sensitivity classification of the focused field.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FieldSensitivity {
    /// An ordinary editable field.
    Normal,
    /// A password or other protected field.
    Protected,
    /// The field could not be classified.
    Unknown,
}

/// Read-only view of the focused control. Offsets are in characters.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FieldSnapshot {
    /// Control identity.
    pub target: TargetId,
    /// Sensitivity classification.
    pub sensitivity: FieldSensitivity,
    /// Whether the control supports safe replacement of a text range.
    pub supports_write: bool,
    /// Text of the control.
    pub text: String,
    /// Caret position.
    pub caret: usize,
    /// Length of the selection; zero when nothing is selected.
    pub selection_len: usize,
}

/// Failure of a replacement write.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum WriteError {
    /// Nothing was changed.
    Failed,
    /// The native API failed without a verifiable resulting range. Do not
    /// attempt recovery or further writes against that unknown state.
    Uncertain,
    /// The range was only partly replaced. `written` is the text now in place
    /// of the requested range.
    Partial {
        /// Text currently occupying the replaced range.
        written: String,
    },
}

/// Accessibility access to the focused control.
pub trait Accessibility {
    /// Reads the focused editable control.
    ///
    /// # Errors
    ///
    /// Returns the [`SkipReason`] explaining why the control cannot be read.
    fn snapshot(&mut self) -> Result<FieldSnapshot, SkipReason>;

    /// Replaces the characters in `start..end` of `target` with `text`.
    ///
    /// # Errors
    ///
    /// Returns [`WriteError`] when the write fails or only partly applies.
    fn replace(
        &mut self,
        target: TargetId,
        start: usize,
        end: usize,
        text: &str,
    ) -> Result<(), WriteError>;
}

/// Context captured by the input adapter when the boundary was typed.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct CapturedContext {
    /// Control that received the typed word.
    pub target: TargetId,
    /// Caret position directly after the boundary.
    pub caret: usize,
}

/// Runtime switches for the session.
#[derive(Debug, Clone, Copy)]
pub struct SessionConfig {
    /// Enabled languages.
    pub languages: LanguageSet,
    /// Whether the user paused correction.
    pub paused: bool,
    /// Whether accessibility and input permissions are granted.
    pub permission_granted: bool,
}

impl Default for SessionConfig {
    fn default() -> Self {
        Self {
            languages: LanguageSet::default(),
            paused: false,
            permission_granted: true,
        }
    }
}

/// Result of evaluating one completed word.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Evaluation {
    /// Outcome category.
    pub outcome: CorrectionOutcome,
    /// Reason when the word was skipped or a write failed.
    pub skip_reason: Option<SkipReason>,
}

impl Evaluation {
    const fn outcome(outcome: CorrectionOutcome) -> Self {
        Self {
            outcome,
            skip_reason: None,
        }
    }

    const fn skipped(reason: SkipReason) -> Self {
        Self {
            outcome: CorrectionOutcome::PreservedSkipped,
            skip_reason: Some(reason),
        }
    }
}

/// Result of a Backspace press after an automatic correction.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BackspaceAction {
    /// Backspace passes through. A pre-event observation can leave undo
    /// pending until the host's deletion is observed.
    PassThrough,
    /// The original word was restored with its boundary preserved.
    Restored,
    /// Restoration failed. A partial write may have caused one guarded
    /// recovery attempt; no later automatic retry is made.
    Failed,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum UndoSnapshot {
    Unchanged,
    BoundaryDeleted,
}

#[derive(Debug, Clone)]
struct UndoState {
    target: TargetId,
    original: String,
    replacement: String,
    boundary: String,
    /// Character offset where the replacement starts.
    start: usize,
    /// Field text after the replacement.
    text: String,
    caret: usize,
    backspace_pending: bool,
}

impl UndoState {
    fn boundary_len(&self) -> usize {
        self.boundary.chars().count()
    }

    fn replacement_end(&self) -> usize {
        self.start + self.replacement.chars().count()
    }

    /// Recognizes the unchanged correction or the host's exact one-character
    /// Backspace transition. The input observer cannot suppress that key.
    fn snapshot_phase(&self, snapshot: &FieldSnapshot) -> Option<UndoSnapshot> {
        if snapshot.target != self.target
            || snapshot.selection_len != 0
            || !matches!(snapshot.sensitivity, FieldSensitivity::Normal)
            || !snapshot.supports_write
        {
            return None;
        }
        if snapshot.text == self.text && snapshot.caret == self.caret {
            return Some(UndoSnapshot::Unchanged);
        }
        let boundary_len = self.boundary_len();
        if boundary_len > 0 && snapshot.caret.checked_add(1) == Some(self.caret) {
            let mut expected: Vec<char> = self.text.chars().collect();
            let removed = expected.remove(self.caret - 1);
            if removed == self.boundary.chars().last()?
                && snapshot.text == expected.into_iter().collect::<String>()
            {
                return Some(UndoSnapshot::BoundaryDeleted);
            }
        }
        None
    }

    fn restore_write(&self) -> (usize, String) {
        (
            self.replacement_end() + self.boundary_len() - 1,
            format!("{}{}", self.original, self.boundary),
        )
    }
}

#[derive(Debug, Clone)]
struct RestoredState {
    word: String,
    target: TargetId,
    text: String,
    caret: usize,
}

impl RestoredState {
    fn matches(&self, snapshot: &FieldSnapshot) -> bool {
        snapshot.target == self.target
            && snapshot.text == self.text
            && snapshot.caret == self.caret
            && snapshot.selection_len == 0
            && snapshot.supports_write
            && matches!(snapshot.sensitivity, FieldSensitivity::Normal)
    }
}

/// A replacement that is being written, kept for partial-write recovery.
pub(crate) struct PendingWrite<'a> {
    pub(crate) before: &'a FieldSnapshot,
    pub(crate) start: usize,
    pub(crate) end: usize,
    pub(crate) original: &'a str,
    pub(crate) boundary: &'a str,
}

fn char_slice(text: &str, start: usize, end: usize) -> String {
    text.chars()
        .skip(start)
        .take(end.saturating_sub(start))
        .collect()
}

/// One active correction context for the current user session.
#[derive(Debug)]
pub struct CorrectionSession {
    config: SessionConfig,
    undo: Option<UndoState>,
    restored: Option<RestoredState>,
}

impl CorrectionSession {
    /// Creates a session.
    #[must_use]
    pub const fn new(config: SessionConfig) -> Self {
        Self {
            config,
            undo: None,
            restored: None,
        }
    }

    /// Returns whether an immediate undo is available.
    #[must_use]
    pub const fn has_undo(&self) -> bool {
        self.undo.is_some()
    }

    /// Clears all transient state.
    pub fn clear_transient(&mut self) {
        self.undo = None;
        self.restored = None;
    }

    /// Pauses or resumes correction. Pausing clears transient state.
    pub fn set_paused(&mut self, paused: bool) {
        self.config.paused = paused;
        if paused {
            self.clear_transient();
        }
    }

    /// Records a permission change. Losing permission clears transient state.
    pub fn set_permission_granted(&mut self, granted: bool) {
        self.config.permission_granted = granted;
        if !granted {
            self.clear_transient();
        }
    }

    /// Updates the enabled languages.
    pub const fn set_languages(&mut self, languages: LanguageSet) {
        self.config.languages = languages;
    }

    /// Signals that the next word started; the previous word's undo and
    /// recorrection suppression no longer apply.
    pub fn next_word_started(&mut self) {
        self.clear_transient();
    }

    /// Observes a focus, caret, selection or edit change. Transient state is
    /// kept only for the engine's expected correction or restoration, or the
    /// host's expected Backspace deletion. This read-only observation does
    /// not complete a pending undo; use [`Self::observe_after_backspace`].
    pub fn observe(&mut self, snapshot: &FieldSnapshot) {
        if let Some(undo) = &self.undo
            && undo.snapshot_phase(snapshot).is_none()
        {
            self.undo = None;
        }
        if self
            .restored
            .as_ref()
            .is_some_and(|state| !state.matches(snapshot))
        {
            self.restored = None;
        }
    }

    /// Whether an observed Backspace still awaits the host's deletion.
    #[must_use]
    pub fn backspace_pending(&self) -> bool {
        self.undo
            .as_ref()
            .is_some_and(|undo| undo.backspace_pending)
    }

    /// Handles the accessibility observation after a previously observed
    /// Backspace key has reached the host. A matching host deletion completes
    /// the pending undo; an unchanged snapshot leaves it pending.
    pub fn observe_after_backspace(
        &mut self,
        accessibility: &mut dyn Accessibility,
    ) -> BackspaceAction {
        if self
            .undo
            .as_ref()
            .is_some_and(|undo| undo.backspace_pending)
        {
            self.on_backspace(accessibility)
        } else {
            BackspaceAction::PassThrough
        }
    }

    /// Evaluates a completed word and replaces it when safe.
    ///
    /// `recorded_keys` must be the directly observed physical keys for `word`
    /// in `captured`'s input context. If their provenance is unavailable, pass
    /// an empty slice so correction fails closed.
    pub fn on_word_complete(
        &mut self,
        word: &str,
        recorded_keys: &[char],
        boundary: &str,
        captured: CapturedContext,
        accessibility: &mut dyn Accessibility,
        validators: &Validators<'_>,
    ) -> Evaluation {
        let started = std::time::Instant::now();
        let evaluation = self.evaluate(
            word,
            recorded_keys,
            boundary,
            captured,
            accessibility,
            validators,
        );
        tracing::info!(
            outcome = evaluation.outcome.as_str(),
            skip_reason = evaluation.skip_reason.map(SkipReason::as_str),
            duration_us = u64::try_from(started.elapsed().as_micros()).unwrap_or(u64::MAX),
            "fixkey correction evaluated"
        );
        evaluation
    }

    fn evaluate(
        &mut self,
        word: &str,
        recorded_keys: &[char],
        boundary: &str,
        captured: CapturedContext,
        accessibility: &mut dyn Accessibility,
        validators: &Validators<'_>,
    ) -> Evaluation {
        if self.config.paused {
            return Evaluation::skipped(SkipReason::Paused);
        }
        if !self.config.permission_granted {
            return Evaluation::skipped(SkipReason::PermissionMissing);
        }
        if self
            .restored
            .as_ref()
            .is_some_and(|state| state.word == word)
        {
            return Evaluation::skipped(SkipReason::RestoredWord);
        }
        // A new evaluation starts a new word.
        self.restored = None;
        self.undo = None;
        let snapshot = match accessibility.snapshot() {
            Ok(snapshot) => snapshot,
            Err(reason) => return Evaluation::skipped(reason),
        };
        match snapshot.sensitivity {
            FieldSensitivity::Protected => return Evaluation::skipped(SkipReason::ProtectedField),
            FieldSensitivity::Unknown => return Evaluation::skipped(SkipReason::UnknownField),
            FieldSensitivity::Normal => {}
        }
        if !snapshot.supports_write {
            return Evaluation::skipped(SkipReason::UnsupportedWrite);
        }
        let typed = format!("{word}{boundary}");
        let typed_len = typed.chars().count();
        let word_len = word.chars().count();
        if snapshot.target != captured.target
            || snapshot.caret != captured.caret
            || snapshot.selection_len != 0
            || snapshot.caret < typed_len
            || char_slice(&snapshot.text, snapshot.caret - typed_len, snapshot.caret) != typed
        {
            return Evaluation::outcome(CorrectionOutcome::PreservedStaleContext);
        }
        let decision = decide(word, recorded_keys, self.config.languages, validators);
        if decision.outcome == CorrectionOutcome::PreservedSkipped {
            return Evaluation::skipped(SkipReason::Programmatic);
        }
        let Some(replacement) = decision.replacement else {
            return Evaluation::outcome(decision.outcome);
        };
        // Validators can run after the user edits or changes the focused
        // control. Use a fresh snapshot for the write and its undo state.
        let current = match accessibility.snapshot() {
            Ok(snapshot) => snapshot,
            Err(reason) => return Evaluation::skipped(reason),
        };
        match current.sensitivity {
            FieldSensitivity::Protected => return Evaluation::skipped(SkipReason::ProtectedField),
            FieldSensitivity::Unknown => return Evaluation::skipped(SkipReason::UnknownField),
            FieldSensitivity::Normal => {}
        }
        if !current.supports_write {
            return Evaluation::skipped(SkipReason::UnsupportedWrite);
        }
        if current.target != snapshot.target
            || current.caret != snapshot.caret
            || current.selection_len != snapshot.selection_len
            || current.text != snapshot.text
        {
            return Evaluation::outcome(CorrectionOutcome::PreservedStaleContext);
        }
        let start = current.caret - typed_len;
        let end = start + word_len;
        match accessibility.replace(current.target, start, end, &replacement) {
            Ok(()) => {
                let mut chars: Vec<char> = current.text.chars().collect();
                chars.splice(start..end, replacement.chars());
                let caret = start + replacement.chars().count() + boundary.chars().count();
                self.undo = Some(UndoState {
                    target: current.target,
                    original: word.to_owned(),
                    replacement,
                    boundary: boundary.to_owned(),
                    start,
                    text: chars.into_iter().collect(),
                    caret,
                    backspace_pending: false,
                });
                Evaluation::outcome(CorrectionOutcome::Replaced)
            }
            Err(error) => {
                Self::recover_partial_write(
                    &error,
                    &PendingWrite {
                        before: &current,
                        start,
                        end,
                        original: word,
                        boundary,
                    },
                    accessibility,
                );
                Evaluation::skipped(SkipReason::UnsupportedWrite)
            }
        }
    }

    /// Restores the original only when the target still matches the expected
    /// intermediate state; otherwise stops without further writes.
    pub(crate) fn recover_partial_write(
        error: &WriteError,
        write: &PendingWrite<'_>,
        accessibility: &mut dyn Accessibility,
    ) {
        let WriteError::Partial { written } = error else {
            return;
        };
        let Ok(now) = accessibility.snapshot() else {
            return;
        };
        let (before, start) = (write.before, write.start);
        let written_end = start + written.chars().count();
        let mut expected: Vec<char> = before.text.chars().collect();
        expected.splice(start..write.end, written.chars());
        let expected_caret = expected.len() - (before.text.chars().count() - before.caret);
        let matches_intermediate = now.target == before.target
            && now.selection_len == 0
            && matches!(now.sensitivity, FieldSensitivity::Normal)
            && now.supports_write
            && now.caret == expected_caret
            && now.text == expected.into_iter().collect::<String>()
            && char_slice(
                &now.text,
                written_end,
                written_end + write.boundary.chars().count(),
            ) == write.boundary;
        if !matches_intermediate {
            tracing::warn!(
                reason = SkipReason::UnsupportedWrite.as_str(),
                "partial write left an unexpected state; no further writes"
            );
            return;
        }
        if accessibility
            .replace(now.target, start, written_end, write.original)
            .is_err()
        {
            tracing::warn!("restoring the original after a partial write failed");
        }
    }

    /// Observes a Backspace press. Before the host deletes the boundary, this
    /// only marks undo pending. Call [`Self::observe_after_backspace`] after
    /// the host handles that key. If the host already deleted the boundary,
    /// this call restores immediately. Later presses pass through.
    pub fn on_backspace(&mut self, accessibility: &mut dyn Accessibility) -> BackspaceAction {
        let Some(undo) = self.undo.as_ref() else {
            return BackspaceAction::PassThrough;
        };
        let Ok(snapshot) = accessibility.snapshot() else {
            self.undo = None;
            return BackspaceAction::PassThrough;
        };
        if !self.config.permission_granted || self.config.paused {
            self.clear_transient();
            return BackspaceAction::PassThrough;
        }
        let Some(phase) = undo.snapshot_phase(&snapshot) else {
            self.undo = None;
            return BackspaceAction::PassThrough;
        };
        if phase == UndoSnapshot::Unchanged {
            if let Some(undo) = self.undo.as_mut() {
                undo.backspace_pending = true;
            }
            return BackspaceAction::PassThrough;
        }
        let Some(undo) = self.undo.take() else {
            return BackspaceAction::PassThrough;
        };
        let (end, restore) = undo.restore_write();
        let result = accessibility.replace(undo.target, undo.start, end, &restore);
        let outcome = match result {
            Ok(()) => BackspaceAction::Restored,
            Err(WriteError::Partial { written }) => {
                let mut expected: Vec<char> = snapshot.text.chars().collect();
                expected.splice(undo.start..end, written.chars());
                let expected: String = expected.into_iter().collect();
                let expected_caret = undo.start + written.chars().count();
                match accessibility.snapshot() {
                    Ok(now)
                        if now.target == undo.target
                            && now.selection_len == 0
                            && matches!(now.sensitivity, FieldSensitivity::Normal)
                            && now.supports_write
                            && now.text == expected
                            && now.caret == expected_caret =>
                    {
                        if accessibility
                            .replace(undo.target, undo.start, expected_caret, &restore)
                            .is_ok()
                        {
                            tracing::info!("partial undo write recovered");
                            BackspaceAction::Restored
                        } else {
                            tracing::warn!("partial undo recovery failed");
                            BackspaceAction::Failed
                        }
                    }
                    _ => {
                        tracing::warn!("partial undo left an unsafe intermediate state");
                        BackspaceAction::Failed
                    }
                }
            }
            Err(WriteError::Failed | WriteError::Uncertain) => BackspaceAction::Failed,
        };
        if matches!(outcome, BackspaceAction::Restored) {
            let mut expected: Vec<char> = snapshot.text.chars().collect();
            expected.splice(undo.start..end, restore.chars());
            self.restored = Some(RestoredState {
                word: undo.original,
                target: undo.target,
                text: expected.into_iter().collect(),
                caret: undo.start + restore.chars().count(),
            });
        }
        tracing::info!(
            restored = matches!(outcome, BackspaceAction::Restored),
            "fixkey immediate undo handled"
        );
        outcome
    }
}

#[cfg(test)]
mod tests {
    use std::{cell::RefCell, rc::Rc};

    use super::*;
    use crate::decision::{LanguageValidator, Validation};

    struct Accepts(&'static [&'static str]);

    impl LanguageValidator for Accepts {
        fn validate(&self, candidate: &str) -> Validation {
            if self.0.contains(&candidate) {
                Validation::Accepted
            } else {
                Validation::Rejected
            }
        }
    }

    #[derive(Clone, Copy, PartialEq, Eq)]
    enum Fault {
        None,
        FailWrite,
        UncertainWrite,
        /// The first write leaves this text in place of the range.
        PartialWrite(&'static str),
        /// Every write fails after a partial one, including the restore.
        PartialThenFailRestore(&'static str),
        UndoPartial(&'static str),
        UndoPartialThenFailRecovery(&'static str),
        UndoFail,
    }

    struct MockField {
        target: TargetId,
        sensitivity: FieldSensitivity,
        supports_write: bool,
        text: Vec<char>,
        caret: usize,
        selection_len: usize,
        fault: Fault,
        writes: usize,
        reads: usize,
        snapshot_error: Option<SkipReason>,
    }

    impl MockField {
        fn new(text: &str) -> Self {
            let text: Vec<char> = text.chars().collect();
            Self {
                target: TargetId(1),
                sensitivity: FieldSensitivity::Normal,
                supports_write: true,
                caret: text.len(),
                text,
                selection_len: 0,
                fault: Fault::None,
                writes: 0,
                reads: 0,
                snapshot_error: None,
            }
        }

        fn text(&self) -> String {
            self.text.iter().collect()
        }

        fn captured(&self) -> CapturedContext {
            CapturedContext {
                target: self.target,
                caret: self.caret,
            }
        }

        /// Replaces a range, keeping the caret's distance from the end.
        fn apply(&mut self, start: usize, end: usize, text: &str) {
            let tail = self.text.len() - self.caret;
            self.text.splice(start..end, text.chars());
            self.caret = self.text.len() - tail;
        }

        /// Simulates the application handling a Backspace key.
        fn backspace(&mut self) {
            self.caret -= 1;
            self.text.remove(self.caret);
        }
    }

    impl Accessibility for MockField {
        fn snapshot(&mut self) -> Result<FieldSnapshot, SkipReason> {
            self.reads += 1;
            if let Some(reason) = self.snapshot_error {
                return Err(reason);
            }
            Ok(FieldSnapshot {
                target: self.target,
                sensitivity: self.sensitivity,
                supports_write: self.supports_write,
                text: self.text(),
                caret: self.caret,
                selection_len: self.selection_len,
            })
        }

        fn replace(
            &mut self,
            _target: TargetId,
            start: usize,
            end: usize,
            text: &str,
        ) -> Result<(), WriteError> {
            self.writes += 1;
            match self.fault {
                Fault::FailWrite => return Err(WriteError::Failed),
                Fault::UncertainWrite => {
                    self.apply(start, end, "unknown");
                    return Err(WriteError::Uncertain);
                }
                Fault::PartialWrite(written) if self.writes == 1 => {
                    self.apply(start, end, written);
                    return Err(WriteError::Partial {
                        written: written.to_owned(),
                    });
                }
                Fault::PartialThenFailRestore(written) => {
                    if self.writes == 1 {
                        self.apply(start, end, written);
                        return Err(WriteError::Partial {
                            written: written.to_owned(),
                        });
                    }
                    return Err(WriteError::Failed);
                }
                Fault::UndoPartial(written) | Fault::UndoPartialThenFailRecovery(written)
                    if self.writes == 2 =>
                {
                    self.apply(start, end, written);
                    return Err(WriteError::Partial {
                        written: written.to_owned(),
                    });
                }
                Fault::UndoPartialThenFailRecovery(_) if self.writes == 3 => {
                    return Err(WriteError::Failed);
                }
                Fault::UndoFail if self.writes == 2 => return Err(WriteError::Failed),
                _ => {}
            }
            self.apply(start, end, text);
            Ok(())
        }
    }

    struct SharedField(Rc<RefCell<MockField>>);

    impl Accessibility for SharedField {
        fn snapshot(&mut self) -> Result<FieldSnapshot, SkipReason> {
            self.0.borrow_mut().snapshot()
        }

        fn replace(
            &mut self,
            target: TargetId,
            start: usize,
            end: usize,
            text: &str,
        ) -> Result<(), WriteError> {
            self.0.borrow_mut().replace(target, start, end, text)
        }
    }

    struct MutatingValidator {
        field: Rc<RefCell<MockField>>,
        mutate: fn(&mut MockField),
        result: Validation,
    }

    impl LanguageValidator for MutatingValidator {
        fn validate(&self, _candidate: &str) -> Validation {
            (self.mutate)(&mut self.field.borrow_mut());
            self.result
        }
    }

    /// Wraps a field and appends an unexpected character after the first write.
    struct Interfering(MockField);
    impl Accessibility for Interfering {
        fn snapshot(&mut self) -> Result<FieldSnapshot, SkipReason> {
            let mut snapshot = self.0.snapshot()?;
            if self.0.writes > 0 {
                snapshot.text.push('?');
            }
            Ok(snapshot)
        }

        fn replace(
            &mut self,
            target: TargetId,
            start: usize,
            end: usize,
            text: &str,
        ) -> Result<(), WriteError> {
            self.0.replace(target, start, end, text)
        }
    }

    struct UndoIntermediate(MockField, Option<fn(&mut FieldSnapshot)>);

    impl Accessibility for UndoIntermediate {
        fn snapshot(&mut self) -> Result<FieldSnapshot, SkipReason> {
            if self.0.writes >= 2 && self.1.is_none() {
                return Err(SkipReason::PermissionMissing);
            }
            let mut snapshot = self.0.snapshot()?;
            if self.0.writes >= 2 {
                self.1.expect("snapshot mutation is present")(&mut snapshot);
            }
            Ok(snapshot)
        }

        fn replace(
            &mut self,
            target: TargetId,
            start: usize,
            end: usize,
            text: &str,
        ) -> Result<(), WriteError> {
            self.0.replace(target, start, end, text)
        }
    }

    fn validators(korean: &'static [&'static str]) -> (Accepts, Accepts) {
        (Accepts(korean), Accepts(&[]))
    }

    fn run(field: &mut MockField, session: &mut CorrectionSession, word: &str) -> Evaluation {
        run_with_boundary(field, session, word, " ")
    }

    fn run_with_boundary(
        field: &mut MockField,
        session: &mut CorrectionSession,
        word: &str,
        boundary: &str,
    ) -> Evaluation {
        let (ko, en) = validators(&["계속해", "키보드가"]);
        let validators = Validators {
            korean: &ko,
            english: &en,
        };
        let captured = field.captured();
        session.on_word_complete(
            word,
            &word.chars().collect::<Vec<_>>(),
            boundary,
            captured,
            field,
            &validators,
        )
    }

    fn new_session() -> CorrectionSession {
        CorrectionSession::new(SessionConfig::default())
    }

    fn run_with_validator_callback(
        mutate: fn(&mut MockField),
        validation: Validation,
    ) -> (Evaluation, Rc<RefCell<MockField>>, CorrectionSession) {
        let field = Rc::new(RefCell::new(MockField::new("rPthrgo ")));
        let captured = field.borrow().captured();
        let korean = MutatingValidator {
            field: Rc::clone(&field),
            mutate,
            result: validation,
        };
        let english = Accepts(&[]);
        let mut shared = SharedField(Rc::clone(&field));
        let mut session = new_session();
        let result = session.on_word_complete(
            "rPthrgo",
            &"rPthrgo".chars().collect::<Vec<_>>(),
            " ",
            captured,
            &mut shared,
            &Validators {
                korean: &korean,
                english: &english,
            },
        );
        (result, field, session)
    }

    #[test]
    fn proven_deletion_before_failed_range_insertion_restores_original_once() {
        let mut field = MockField::new("rPthrgo ");
        field.fault = Fault::PartialWrite("");
        let mut session = new_session();
        assert_eq!(
            run(&mut field, &mut session, "rPthrgo").skip_reason,
            Some(SkipReason::UnsupportedWrite)
        );
        assert_eq!(field.text(), "rPthrgo ");
        assert_eq!(field.writes, 2);
        assert!(!session.has_undo());
    }

    #[test]
    fn uncertain_native_writes_never_trigger_recovery_or_retry() {
        let mut field = MockField::new("rPthrgo ");
        field.fault = Fault::UncertainWrite;
        let mut session = new_session();
        assert_eq!(
            run(&mut field, &mut session, "rPthrgo").skip_reason,
            Some(SkipReason::UnsupportedWrite)
        );
        assert_eq!(field.writes, 1);
        assert_eq!(field.text(), "unknown ");
        assert!(!session.has_undo());
        assert_eq!(
            session.on_backspace(&mut field),
            BackspaceAction::PassThrough
        );
        assert_eq!(field.writes, 1);
    }

    #[test]
    fn edits_and_context_changes_during_validation_prevent_the_write() {
        let (edited, edited_field, edited_session) = run_with_validator_callback(
            |field| field.text = "changed ".chars().collect(),
            Validation::Accepted,
        );
        assert_eq!(edited.outcome, CorrectionOutcome::PreservedStaleContext);
        assert_eq!(edited_field.borrow().text(), "changed ");
        assert_eq!(edited_field.borrow().writes, 0);
        assert!(!edited_session.has_undo());

        let mutations: [fn(&mut MockField); 5] = [
            |field| field.text[0] = 'x',
            |field| {
                let _ = field.text.pop();
            },
            |field| field.target = TargetId(2),
            |field| field.caret -= 1,
            |field| field.selection_len = 1,
        ];
        for mutate in mutations {
            let (result, field, session) =
                run_with_validator_callback(mutate, Validation::Accepted);
            let field = field.borrow();
            assert_eq!(result.outcome, CorrectionOutcome::PreservedStaleContext);
            assert_eq!(field.writes, 0);
            assert_eq!(field.reads, 2);
            assert!(!session.has_undo());
            assert_ne!(field.text(), "계속해 ");
        }
    }

    #[test]
    fn post_validation_read_and_field_safety_failures_preserve_skip_reasons() {
        type Mutation = fn(&mut MockField);
        let cases: [(Mutation, SkipReason); 4] = [
            (
                |field| field.snapshot_error = Some(SkipReason::PermissionMissing),
                SkipReason::PermissionMissing,
            ),
            (
                |field| field.sensitivity = FieldSensitivity::Protected,
                SkipReason::ProtectedField,
            ),
            (
                |field| field.sensitivity = FieldSensitivity::Unknown,
                SkipReason::UnknownField,
            ),
            (
                |field| field.supports_write = false,
                SkipReason::UnsupportedWrite,
            ),
        ];
        for (mutate, reason) in cases {
            let (result, field, session) =
                run_with_validator_callback(mutate, Validation::Accepted);
            let field = field.borrow();
            assert_eq!(result.outcome, CorrectionOutcome::PreservedSkipped);
            assert_eq!(result.skip_reason, Some(reason));
            assert_eq!(field.writes, 0);
            assert_eq!(field.reads, 2);
            assert!(!session.has_undo());
        }
    }

    #[test]
    fn unchanged_post_validation_context_still_corrects_and_undoes_once() {
        let (result, field, mut session) =
            run_with_validator_callback(|_| {}, Validation::Accepted);
        assert_eq!(result.outcome, CorrectionOutcome::Replaced);
        assert_eq!(field.borrow().text(), "계속해 ");
        assert_eq!(field.borrow().writes, 1);
        assert_eq!(field.borrow().reads, 2);
        assert!(session.has_undo());
        field.borrow_mut().backspace();
        assert_eq!(
            session.on_backspace(&mut SharedField(Rc::clone(&field))),
            BackspaceAction::Restored
        );
        assert_eq!(field.borrow().text(), "rPthrgo ");
    }

    #[test]
    fn unavailable_validator_does_not_create_a_stale_write() {
        let (result, field, session) = run_with_validator_callback(
            |field| field.text = "changed ".chars().collect(),
            Validation::Unavailable,
        );
        assert_eq!(
            result.outcome,
            CorrectionOutcome::PreservedValidatorUnavailable
        );
        assert_eq!(field.borrow().writes, 0);
        assert_eq!(field.borrow().text(), "changed ");
        assert!(!session.has_undo());
    }

    #[test]
    fn no_match_and_ambiguous_results_still_preserve_without_a_second_read() {
        let (no_match, field, session) = run_with_validator_callback(|_| {}, Validation::Rejected);
        assert_eq!(no_match.outcome, CorrectionOutcome::PreservedNoMatch);
        assert_eq!(field.borrow().reads, 1);
        assert_eq!(field.borrow().writes, 0);
        assert!(!session.has_undo());

        let mut field = MockField::new("rPthrgo ");
        let captured = field.captured();
        let mut session = new_session();
        let ambiguous = session.on_word_complete(
            "rPthrgo",
            &"rPthrgo".chars().collect::<Vec<_>>(),
            " ",
            captured,
            &mut field,
            &Validators {
                korean: &Accepts(&["계속해"]),
                english: &Accepts(&["rPthrgo"]),
            },
        );
        assert_eq!(ambiguous.outcome, CorrectionOutcome::PreservedAmbiguous);
        assert_eq!(field.reads, 1);
        assert_eq!(field.writes, 0);
        assert!(!session.has_undo());
    }

    #[test]
    fn recorded_keys_replace_korean_with_exact_case_and_undo_restores_rendered_word() {
        let mut field = MockField::new("ㅜㅁㄴㅁ ");
        let mut session = new_session();
        let korean = Accepts(&[]);
        let english = Accepts(&["NASA"]);
        let captured = field.captured();
        let result = session.on_word_complete(
            "ㅜㅁㄴㅁ",
            &"NASA".chars().collect::<Vec<_>>(),
            " ",
            captured,
            &mut field,
            &Validators {
                korean: &korean,
                english: &english,
            },
        );
        assert_eq!(result.outcome, CorrectionOutcome::Replaced);
        assert_eq!(field.text(), "NASA ");
        field.backspace();
        assert_eq!(session.on_backspace(&mut field), BackspaceAction::Restored);
        assert_eq!(field.text(), "ㅜㅁㄴㅁ ");
    }

    #[test]
    fn missing_or_mismatched_keys_never_write_or_create_undo() {
        let korean = Accepts(&[]);
        let english = Accepts(&["NASA"]);
        let validators = Validators {
            korean: &korean,
            english: &english,
        };
        for recorded in ["", "other", "nasa-"] {
            let mut field = MockField::new("ㅜㅁㄴㅁ ");
            let captured = field.captured();
            let mut session = new_session();
            let result = session.on_word_complete(
                "ㅜㅁㄴㅁ",
                &recorded.chars().collect::<Vec<_>>(),
                " ",
                captured,
                &mut field,
                &validators,
            );
            assert_eq!(result.outcome, CorrectionOutcome::PreservedSkipped);
            assert_eq!(result.skip_reason, Some(SkipReason::Programmatic));
            assert_eq!(field.writes, 0);
            assert!(!session.has_undo());
        }
    }

    #[test]
    fn a_typed_word_is_replaced_and_the_boundary_preserved() {
        let mut field = MockField::new("hi rPthrgo ");
        let mut session = new_session();
        let result = run(&mut field, &mut session, "rPthrgo");
        assert_eq!(result.outcome, CorrectionOutcome::Replaced);
        assert_eq!(field.text(), "hi 계속해 ");
        assert!(session.has_undo());
    }

    #[test]
    fn backspace_restores_the_original_and_keeps_the_boundary() {
        let mut field = MockField::new("rPthrgo ");
        let mut session = new_session();
        run(&mut field, &mut session, "rPthrgo");
        field.backspace(); // the application deletes the boundary
        assert_eq!(session.on_backspace(&mut field), BackspaceAction::Restored);
        assert_eq!(field.text(), "rPthrgo ");
        assert!(!session.has_undo());
    }

    #[test]
    fn pre_event_backspace_waits_for_host_deletion() {
        for boundary in [" ", ". "] {
            let mut field = MockField::new(&format!("rPthrgo{boundary}"));
            let mut session = new_session();
            run_with_boundary(&mut field, &mut session, "rPthrgo", boundary);
            let corrected = field.text();
            assert_eq!(
                session.on_backspace(&mut field),
                BackspaceAction::PassThrough
            );
            session.observe(&field.snapshot().unwrap());
            assert_eq!(field.text(), corrected);
            assert_eq!(field.writes, 1);
            assert!(session.has_undo());
            assert_eq!(
                session.observe_after_backspace(&mut field),
                BackspaceAction::PassThrough
            );
            assert!(session.has_undo());
            field.backspace();
            assert_eq!(
                session.observe_after_backspace(&mut field),
                BackspaceAction::Restored
            );
            assert_eq!(field.text(), format!("rPthrgo{boundary}"));
            assert_eq!(field.writes, 2);
            assert!(!session.has_undo());
            assert_eq!(
                session.observe_after_backspace(&mut field),
                BackspaceAction::PassThrough
            );
            session.observe(&field.snapshot().unwrap());
            assert_eq!(
                run_with_boundary(&mut field, &mut session, "rPthrgo", boundary).skip_reason,
                Some(SkipReason::RestoredWord)
            );
        }
    }

    #[test]
    fn post_event_undo_replaces_the_surviving_boundary_prefix() {
        for boundary in [" ", ". ", "... "] {
            let mut field = MockField::new(&format!("hi rPthrgo{boundary}tail"));
            field.caret = "hi rPthrgo".chars().count() + boundary.chars().count();
            let mut session = new_session();
            run_with_boundary(&mut field, &mut session, "rPthrgo", boundary);
            field.backspace();
            assert_eq!(session.on_backspace(&mut field), BackspaceAction::Restored);
            assert_eq!(field.text(), format!("hi rPthrgo{boundary}tail"));
            assert_eq!(field.writes, 2);
            session.observe(&field.snapshot().unwrap());
            assert_eq!(
                run_with_boundary(&mut field, &mut session, "rPthrgo", boundary).skip_reason,
                Some(SkipReason::RestoredWord)
            );
        }
    }

    #[test]
    fn undo_refuses_lost_write_capability_in_both_event_orders() {
        for deleted in [false, true] {
            for observed in [false, true] {
                let mut field = MockField::new("rPthrgo ");
                let mut session = new_session();
                run(&mut field, &mut session, "rPthrgo");
                if deleted {
                    field.backspace();
                }
                field.supports_write = false;
                if observed {
                    session.observe(&field.snapshot().unwrap());
                    assert!(!session.has_undo());
                }
                assert_eq!(
                    session.on_backspace(&mut field),
                    BackspaceAction::PassThrough
                );
                assert_eq!(field.writes, 1);
                assert!(!session.has_undo());
            }
        }
    }

    #[test]
    fn stale_or_unsafe_post_event_state_never_restores() {
        type Mutation = fn(&mut MockField);
        let mutations: [Mutation; 6] = [
            |f| f.target = TargetId(2),
            |f| f.caret -= 1,
            |f| f.selection_len = 1,
            |f| f.text[0] = 'x',
            |f| f.sensitivity = FieldSensitivity::Protected,
            |f| f.supports_write = false,
        ];
        for mutate in mutations {
            let mut field = MockField::new("rPthrgo ");
            let mut session = new_session();
            run(&mut field, &mut session, "rPthrgo");
            assert_eq!(
                session.on_backspace(&mut field),
                BackspaceAction::PassThrough
            );
            field.backspace();
            mutate(&mut field);
            assert_eq!(
                session.observe_after_backspace(&mut field),
                BackspaceAction::PassThrough
            );
            assert_eq!(field.writes, 1);
            assert!(!session.has_undo());
        }
    }

    #[test]
    fn matching_partial_undo_is_recovered_once() {
        for pre_event in [false, true] {
            for boundary in [" ", ". "] {
                let mut field = MockField::new(&format!("hi rPthrgo{boundary}tail"));
                field.caret = "hi rPthrgo".chars().count() + boundary.chars().count();
                let mut session = new_session();
                run_with_boundary(&mut field, &mut session, "rPthrgo", boundary);
                field.fault = Fault::UndoPartial("r");
                if pre_event {
                    assert_eq!(
                        session.on_backspace(&mut field),
                        BackspaceAction::PassThrough
                    );
                }
                field.backspace();
                let action = if pre_event {
                    session.observe_after_backspace(&mut field)
                } else {
                    session.on_backspace(&mut field)
                };
                assert_eq!(action, BackspaceAction::Restored);
                assert_eq!(field.text(), format!("hi rPthrgo{boundary}tail"));
                assert_eq!(field.writes, 3);
                assert!(!session.has_undo());
                session.observe(&field.snapshot().unwrap());
                assert_eq!(
                    run_with_boundary(&mut field, &mut session, "rPthrgo", boundary).skip_reason,
                    Some(SkipReason::RestoredWord)
                );
            }
        }
    }

    #[test]
    fn failed_undo_and_failed_recovery_do_not_retry() {
        for (fault, expected_writes) in [
            (Fault::UndoFail, 2),
            (Fault::UndoPartialThenFailRecovery("r"), 3),
        ] {
            let mut field = MockField::new("rPthrgo ");
            let mut session = new_session();
            run(&mut field, &mut session, "rPthrgo");
            field.fault = fault;
            field.backspace();
            assert_eq!(session.on_backspace(&mut field), BackspaceAction::Failed);
            assert_eq!(field.writes, expected_writes);
            assert!(!session.has_undo());
            assert_eq!(
                session.on_backspace(&mut field),
                BackspaceAction::PassThrough
            );
            assert_eq!(field.writes, expected_writes);
        }
    }

    #[test]
    fn unsafe_partial_undo_intermediate_causes_no_recovery_write() {
        type Mutation = Option<fn(&mut FieldSnapshot)>;
        let mutations: [Mutation; 7] = [
            None,
            Some(|s| s.target = TargetId(3)),
            Some(|s| s.text.push('x')),
            Some(|s| s.caret += 1),
            Some(|s| s.selection_len = 1),
            Some(|s| s.sensitivity = FieldSensitivity::Unknown),
            Some(|s| s.supports_write = false),
        ];
        for mutation in mutations {
            let mut field = UndoIntermediate(MockField::new("rPthrgo "), mutation);
            let mut session = new_session();
            run(&mut field.0, &mut session, "rPthrgo");
            field.0.fault = Fault::UndoPartial("r");
            field.0.backspace();
            assert_eq!(session.on_backspace(&mut field), BackspaceAction::Failed);
            assert_eq!(field.0.writes, 2);
            assert!(!session.has_undo());
        }
    }

    #[test]
    fn expected_restoration_observation_preserves_only_matching_suppression() {
        let mut field = MockField::new("rPthrgo ");
        let mut session = new_session();
        run(&mut field, &mut session, "rPthrgo");
        field.backspace();
        assert_eq!(session.on_backspace(&mut field), BackspaceAction::Restored);
        session.observe(&field.snapshot().unwrap());
        assert_eq!(
            run(&mut field, &mut session, "rPthrgo").skip_reason,
            Some(SkipReason::RestoredWord)
        );
        field.caret -= 1;
        session.observe(&field.snapshot().unwrap());
        field.caret += 1;
        assert_eq!(
            run(&mut field, &mut session, "rPthrgo").outcome,
            CorrectionOutcome::Replaced
        );
        session.set_paused(true);
        session.set_paused(false);
        assert!(!session.has_undo());
    }

    #[test]
    fn external_observations_and_reset_boundaries_clear_suppression() {
        type Mutation = fn(&mut MockField);
        let mutations: [Mutation; 4] = [
            |f| f.target = TargetId(8),
            |f| f.text[0] = 'x',
            |f| f.caret -= 1,
            |f| f.selection_len = 1,
        ];
        for mutate in mutations {
            let mut field = MockField::new("rPthrgo ");
            let mut session = new_session();
            run(&mut field, &mut session, "rPthrgo");
            field.backspace();
            assert_eq!(session.on_backspace(&mut field), BackspaceAction::Restored);
            mutate(&mut field);
            session.observe(&field.snapshot().unwrap());
            assert!(session.restored.is_none());
        }
        for reset in [
            CorrectionSession::next_word_started as fn(&mut CorrectionSession),
            |session| session.set_paused(true),
            |session| session.set_permission_granted(false),
        ] {
            let mut field = MockField::new("rPthrgo ");
            let mut session = new_session();
            run(&mut field, &mut session, "rPthrgo");
            field.backspace();
            assert_eq!(session.on_backspace(&mut field), BackspaceAction::Restored);
            reset(&mut session);
            assert!(session.restored.is_none());
        }
    }

    #[test]
    fn only_the_first_backspace_restores() {
        let mut field = MockField::new("rPthrgo ");
        let mut session = new_session();
        run(&mut field, &mut session, "rPthrgo");
        field.backspace();
        session.on_backspace(&mut field);
        field.backspace();
        assert_eq!(
            session.on_backspace(&mut field),
            BackspaceAction::PassThrough
        );
        assert_eq!(field.text(), "rPthrgo");
    }

    #[test]
    fn a_restored_word_is_not_recorrected() {
        let mut field = MockField::new("rPthrgo ");
        let mut session = new_session();
        run(&mut field, &mut session, "rPthrgo");
        field.backspace();
        session.on_backspace(&mut field);
        let again = run(&mut field, &mut session, "rPthrgo");
        assert_eq!(again.skip_reason, Some(SkipReason::RestoredWord));
        assert_eq!(field.text(), "rPthrgo ");
        // The next word clears the suppression.
        session.next_word_started();
        assert_eq!(
            run(&mut field, &mut session, "rPthrgo").outcome,
            CorrectionOutcome::Replaced
        );
    }

    #[test]
    fn external_changes_clear_the_undo_state() {
        let mut field = MockField::new("rPthrgo ");
        let mut session = new_session();
        run(&mut field, &mut session, "rPthrgo");
        field.caret -= 2;
        session.observe(&field.snapshot().unwrap());
        assert!(!session.has_undo());
        assert_eq!(
            session.on_backspace(&mut field),
            BackspaceAction::PassThrough
        );
    }

    #[test]
    fn the_engines_own_replacement_does_not_clear_the_undo_state() {
        let mut field = MockField::new("rPthrgo ");
        let mut session = new_session();
        run(&mut field, &mut session, "rPthrgo");
        session.observe(&field.snapshot().unwrap());
        assert!(session.has_undo());
        // A target change also clears it.
        field.target = TargetId(2);
        session.observe(&field.snapshot().unwrap());
        assert!(!session.has_undo());
    }

    #[test]
    fn pause_and_permission_loss_clear_state_and_block_correction() {
        let mut field = MockField::new("rPthrgo ");
        let mut session = new_session();
        run(&mut field, &mut session, "rPthrgo");
        session.set_paused(true);
        assert!(!session.has_undo());
        assert_eq!(
            run(&mut field, &mut session, "rPthrgo").skip_reason,
            Some(SkipReason::Paused)
        );
        session.set_paused(false);
        session.set_permission_granted(false);
        assert_eq!(
            run(&mut field, &mut session, "rPthrgo").skip_reason,
            Some(SkipReason::PermissionMissing)
        );
        assert_eq!(field.writes, 1);
    }

    #[test]
    fn protected_unknown_unsupported_and_unreadable_fields_are_refused() {
        for (setup, reason) in [
            (
                (|f: &mut MockField| f.sensitivity = FieldSensitivity::Protected)
                    as fn(&mut MockField),
                SkipReason::ProtectedField,
            ),
            (
                |f| f.sensitivity = FieldSensitivity::Unknown,
                SkipReason::UnknownField,
            ),
            (|f| f.supports_write = false, SkipReason::UnsupportedWrite),
            (
                |f| f.snapshot_error = Some(SkipReason::PermissionMissing),
                SkipReason::PermissionMissing,
            ),
        ] {
            let mut field = MockField::new("rPthrgo ");
            setup(&mut field);
            let mut session = new_session();
            let result = run(&mut field, &mut session, "rPthrgo");
            assert_eq!(result.outcome, CorrectionOutcome::PreservedSkipped);
            assert_eq!(result.skip_reason, Some(reason));
            assert_eq!(field.writes, 0);
            assert_eq!(field.text(), "rPthrgo ");
        }
    }

    #[test]
    fn stale_contexts_are_discarded_without_writes() {
        type Mutation = fn(&mut MockField);
        let mutations: [Mutation; 5] = [
            |f| f.caret -= 1,
            |f| f.target = TargetId(9),
            |f| f.selection_len = 2,
            |f| f.text[2] = 'x',
            |f| {
                f.text.pop();
            },
        ];
        for mutate in mutations {
            let mut field = MockField::new("rPthrgo ");
            let captured = field.captured();
            mutate(&mut field);
            let (ko, en) = validators(&["계속해"]);
            let mut session = new_session();
            let result = session.on_word_complete(
                "rPthrgo",
                &"rPthrgo".chars().collect::<Vec<_>>(),
                " ",
                captured,
                &mut field,
                &Validators {
                    korean: &ko,
                    english: &en,
                },
            );
            assert_eq!(result.outcome, CorrectionOutcome::PreservedStaleContext);
            assert_eq!(field.writes, 0);
        }
    }

    #[test]
    fn failed_writes_are_not_retried() {
        let mut field = MockField::new("rPthrgo ");
        field.fault = Fault::FailWrite;
        let mut session = new_session();
        let result = run(&mut field, &mut session, "rPthrgo");
        assert_eq!(result.skip_reason, Some(SkipReason::UnsupportedWrite));
        assert_eq!(field.writes, 1);
        assert_eq!(field.text(), "rPthrgo ");
        assert!(!session.has_undo());
    }

    #[test]
    fn a_partial_write_is_restored_when_the_intermediate_state_matches() {
        let mut field = MockField::new("rPthrgo ");
        field.fault = Fault::PartialWrite("계");
        let mut session = new_session();
        let result = run(&mut field, &mut session, "rPthrgo");
        assert_eq!(result.skip_reason, Some(SkipReason::UnsupportedWrite));
        assert_eq!(field.writes, 2);
        assert_eq!(field.text(), "rPthrgo ");
        assert!(!session.has_undo());
    }

    #[test]
    fn a_partial_write_stops_when_the_intermediate_state_changed() {
        let mut field = MockField::new("rPthrgo ");
        field.fault = Fault::PartialThenFailRestore("계");
        let mut session = new_session();
        run(&mut field, &mut session, "rPthrgo");
        // The restore attempt failed once and was not retried.
        assert_eq!(field.writes, 2);

        let mut changed = MockField::new("rPthrgo ");
        changed.fault = Fault::PartialWrite("계");
        changed.supports_write = true;
        // An unexpected extra edit makes the intermediate state differ.
        let mut interfering = Interfering(changed);
        let (ko, en) = validators(&["계속해"]);
        let captured = interfering.0.captured();
        let mut session = new_session();
        session.on_word_complete(
            "rPthrgo",
            &"rPthrgo".chars().collect::<Vec<_>>(),
            " ",
            captured,
            &mut interfering,
            &Validators {
                korean: &ko,
                english: &en,
            },
        );
        assert_eq!(interfering.0.writes, 1, "no further writes");
    }

    #[test]
    fn zero_or_multiple_matches_leave_the_text_untouched() {
        let mut field = MockField::new("zzzz ");
        let mut session = new_session();
        let result = run(&mut field, &mut session, "zzzz");
        assert_eq!(result.outcome, CorrectionOutcome::PreservedNoMatch);
        assert_eq!(field.writes, 0);
        assert!(!session.has_undo());
    }
}
