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
    /// No undo applies; Backspace behaves normally.
    PassThrough,
    /// The original word was restored with its boundary preserved.
    Restored,
    /// Restoration was attempted and failed; no further writes were made.
    Failed,
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
}

impl UndoState {
    fn boundary_len(&self) -> usize {
        self.boundary.chars().count()
    }

    fn replacement_end(&self) -> usize {
        self.start + self.replacement.chars().count()
    }

    /// Returns the restore text when `snapshot` is the expected
    /// post-replacement state, or that state after Backspace removed exactly
    /// the boundary's last character.
    fn restore_text(&self, snapshot: &FieldSnapshot) -> Option<String> {
        if snapshot.target != self.target
            || snapshot.selection_len != 0
            || !matches!(snapshot.sensitivity, FieldSensitivity::Normal)
        {
            return None;
        }
        if snapshot.text == self.text && snapshot.caret == self.caret {
            return Some(self.original.clone());
        }
        let boundary_len = self.boundary_len();
        if boundary_len > 0 && snapshot.caret + 1 == self.caret {
            let mut expected: Vec<char> = self.text.chars().collect();
            let removed = expected.remove(self.caret - 1);
            if removed == self.boundary.chars().last()?
                && snapshot.text == expected.into_iter().collect::<String>()
            {
                // Backspace removed the boundary; put the original and the
                // boundary back.
                return Some(format!("{}{}", self.original, self.boundary));
            }
        }
        None
    }
}

/// A replacement that is being written, kept for partial-write recovery.
struct PendingWrite<'a> {
    before: &'a FieldSnapshot,
    start: usize,
    end: usize,
    original: &'a str,
    boundary: &'a str,
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
    restored_word: Option<String>,
}

impl CorrectionSession {
    /// Creates a session.
    #[must_use]
    pub const fn new(config: SessionConfig) -> Self {
        Self {
            config,
            undo: None,
            restored_word: None,
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
        self.restored_word = None;
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
    /// kept only when the snapshot is the state the engine's own replacement
    /// (or an expected Backspace on it) produced.
    pub fn observe(&mut self, snapshot: &FieldSnapshot) {
        if let Some(undo) = &self.undo
            && undo.restore_text(snapshot).is_none()
        {
            self.undo = None;
        }
        if self.undo.is_none() {
            self.restored_word = None;
        }
    }

    /// Evaluates a completed word and replaces it when safe.
    pub fn on_word_complete(
        &mut self,
        word: &str,
        boundary: &str,
        captured: CapturedContext,
        accessibility: &mut dyn Accessibility,
        validators: &Validators<'_>,
    ) -> Evaluation {
        let started = std::time::Instant::now();
        let evaluation = self.evaluate(word, boundary, captured, accessibility, validators);
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
        if self.restored_word.as_deref() == Some(word) {
            return Evaluation::skipped(SkipReason::RestoredWord);
        }
        // A new evaluation starts a new word.
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
        let decision = decide(word, self.config.languages, validators);
        let Some(replacement) = decision.replacement else {
            return Evaluation::outcome(decision.outcome);
        };
        let start = snapshot.caret - typed_len;
        let end = start + word_len;
        match accessibility.replace(snapshot.target, start, end, &replacement) {
            Ok(()) => {
                let mut chars: Vec<char> = snapshot.text.chars().collect();
                chars.splice(start..end, replacement.chars());
                let caret = start + replacement.chars().count() + boundary.chars().count();
                self.undo = Some(UndoState {
                    target: snapshot.target,
                    original: word.to_owned(),
                    replacement,
                    boundary: boundary.to_owned(),
                    start,
                    text: chars.into_iter().collect(),
                    caret,
                });
                Evaluation::outcome(CorrectionOutcome::Replaced)
            }
            Err(error) => {
                Self::recover_partial_write(
                    &error,
                    &PendingWrite {
                        before: &snapshot,
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
    fn recover_partial_write(
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
        let matches_intermediate = now.target == before.target
            && now.selection_len == 0
            && matches!(now.sensitivity, FieldSensitivity::Normal)
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

    /// Handles a Backspace press. The first press right after an automatic
    /// correction restores the original word and keeps the boundary; later
    /// presses pass through.
    pub fn on_backspace(&mut self, accessibility: &mut dyn Accessibility) -> BackspaceAction {
        let Some(undo) = self.undo.take() else {
            return BackspaceAction::PassThrough;
        };
        let Ok(snapshot) = accessibility.snapshot() else {
            return BackspaceAction::PassThrough;
        };
        let Some(restore) = undo.restore_text(&snapshot) else {
            return BackspaceAction::PassThrough;
        };
        // `restore` ends with the boundary only when Backspace already removed
        // it, so the write covers the replacement plus that missing boundary.
        let end = undo.replacement_end();
        let result = accessibility.replace(undo.target, undo.start, end, &restore);
        let outcome = if result.is_ok() {
            self.restored_word = Some(undo.original);
            BackspaceAction::Restored
        } else {
            BackspaceAction::Failed
        };
        tracing::info!(
            restored = matches!(outcome, BackspaceAction::Restored),
            "fixkey immediate undo handled"
        );
        outcome
    }
}

#[cfg(test)]
mod tests {
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
        /// The first write leaves this text in place of the range.
        PartialWrite(&'static str),
        /// Every write fails after a partial one, including the restore.
        PartialThenFailRestore(&'static str),
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
                _ => {}
            }
            self.apply(start, end, text);
            Ok(())
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

    fn validators(korean: &'static [&'static str]) -> (Accepts, Accepts) {
        (Accepts(korean), Accepts(&[]))
    }

    fn run(field: &mut MockField, session: &mut CorrectionSession, word: &str) -> Evaluation {
        let (ko, en) = validators(&["계속해", "키보드가"]);
        let validators = Validators {
            korean: &ko,
            english: &en,
        };
        let captured = field.captured();
        session.on_word_complete(word, " ", captured, field, &validators)
    }

    fn new_session() -> CorrectionSession {
        CorrectionSession::new(SessionConfig::default())
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
