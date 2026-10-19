//! Bounded read-back of native writes that can be acknowledged asynchronously.

use std::time::{Duration, Instant};

use crate::session::FieldSnapshot;

/// Accepts only the exact before state while waiting, or a verified after
/// state. Any unrelated edit, focus, selection or safety change aborts.
pub(super) fn wait_for_transition<E>(
    before: &FieldSnapshot,
    expected: &[FieldSnapshot],
    timeout: Duration,
    mut observe: impl FnMut() -> Result<FieldSnapshot, E>,
) -> Result<FieldSnapshot, ()> {
    let deadline = Instant::now() + timeout;
    loop {
        let current = observe().map_err(|_| ())?;
        if expected.contains(&current) {
            return Ok(current);
        }
        if &current != before || Instant::now() >= deadline {
            return Err(());
        }
        std::thread::sleep(Duration::from_millis(1));
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::session::{FieldSensitivity, TargetId};

    fn snapshot() -> FieldSnapshot {
        FieldSnapshot {
            target: TargetId(1),
            sensitivity: FieldSensitivity::Normal,
            supports_write: true,
            text: "word ".into(),
            caret: 5,
            selection_len: 0,
        }
    }

    #[test]
    fn waits_for_delayed_selection_without_reissuing_a_write() {
        let before = snapshot();
        let selected = FieldSnapshot {
            caret: 0,
            selection_len: 4,
            ..before.clone()
        };
        let mut observations = 0;
        let result = wait_for_transition(
            &before,
            std::slice::from_ref(&selected),
            Duration::from_millis(100),
            || {
                observations += 1;
                Ok::<_, ()>(if observations < 3 {
                    before.clone()
                } else {
                    selected.clone()
                })
            },
        );
        assert_eq!(result, Ok(selected));
        assert_eq!(observations, 3);
    }

    #[test]
    fn rejects_unrelated_changes_before_a_later_expected_state() {
        let before = snapshot();
        let expected = FieldSnapshot {
            caret: 0,
            selection_len: 4,
            ..before.clone()
        };
        for changed in [
            FieldSnapshot {
                target: TargetId(2),
                ..before.clone()
            },
            FieldSnapshot {
                text: "user edit".into(),
                ..before.clone()
            },
            FieldSnapshot {
                caret: 2,
                ..before.clone()
            },
            FieldSnapshot {
                sensitivity: FieldSensitivity::Protected,
                ..before.clone()
            },
            FieldSnapshot {
                sensitivity: FieldSensitivity::Unknown,
                ..before.clone()
            },
            FieldSnapshot {
                supports_write: false,
                ..before.clone()
            },
        ] {
            let mut reads = 0;
            assert!(
                wait_for_transition(
                    &before,
                    std::slice::from_ref(&expected),
                    Duration::from_millis(100),
                    || {
                        reads += 1;
                        Ok::<_, ()>(if reads == 1 {
                            changed.clone()
                        } else {
                            expected.clone()
                        })
                    }
                )
                .is_err()
            );
            assert_eq!(reads, 1);
        }
    }

    #[test]
    fn bounds_missing_acknowledgements_and_refuses_read_errors() {
        let before = snapshot();
        let expected = FieldSnapshot {
            caret: 0,
            selection_len: 4,
            ..before.clone()
        };
        assert!(
            wait_for_transition(
                &before,
                std::slice::from_ref(&expected),
                Duration::ZERO,
                || Ok::<_, ()>(before.clone())
            )
            .is_err()
        );
        assert!(
            wait_for_transition(&before, &[expected], Duration::from_millis(100), || Err::<
                FieldSnapshot,
                _,
            >(
                ()
            ))
            .is_err()
        );
    }

    #[test]
    fn accepts_a_verified_text_result_with_an_expected_native_selection() {
        let before = snapshot();
        let written = FieldSnapshot {
            text: "replacement ".into(),
            caret: 11,
            ..before
        };
        let collapsed = FieldSnapshot {
            caret: 12,
            ..written.clone()
        };
        assert_eq!(
            wait_for_transition(
                &before,
                &[written.clone(), collapsed],
                Duration::ZERO,
                || Ok::<_, ()>(written.clone())
            ),
            Ok(written)
        );
    }
}
