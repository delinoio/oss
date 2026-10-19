//! Proof that a bounded sequence of physical keys explains an accessibility
//! edit. Existing text and unrelated edits never become input provenance.

use crate::{
    Language,
    layout::text_from_keys,
    session::{FieldSensitivity, FieldSnapshot},
};

/// Waits for the host to publish an exact completed insertion before any write.
/// Only matching committed prefixes may progress during this bounded wait.
pub fn await_completed_word<E>(
    before: &FieldSnapshot,
    keys: &[char],
    boundary: &str,
    timeout: std::time::Duration,
    mut observe: impl FnMut() -> Result<FieldSnapshot, E>,
) -> Option<FieldSnapshot> {
    if !can_start(before) {
        return None;
    }
    let deadline = std::time::Instant::now() + timeout;
    let observed: Vec<_> = keys.iter().copied().chain(boundary.chars()).collect();
    let mut caret = before.caret;
    loop {
        let current = observe().ok()?;
        if current.caret < caret {
            return None;
        }
        if completed_word(before, &current, keys, boundary).is_some() {
            return Some(current);
        }
        if !pending_matches(before, &current, &observed) || std::time::Instant::now() >= deadline {
            return None;
        }
        caret = current.caret;
        std::thread::sleep(std::time::Duration::from_millis(1));
    }
}

/// Establishes a new word boundary without adopting existing document text.
#[must_use]
pub fn can_start(before: &FieldSnapshot) -> bool {
    let chars: Vec<_> = before.text.chars().collect();
    let attached = |ch: char| {
        ch.is_alphanumeric()
            || matches!(
                ch,
                '\'' | '\u{2019}' | '-' | '_' | '.' | '/' | ':' | '@' | '?' | '#'
            )
    };
    before.sensitivity == FieldSensitivity::Normal
        && before.supports_write
        && before.selection_len == 0
        && before.caret <= chars.len()
        && (before.caret == 0 || !attached(chars[before.caret - 1]))
        && chars.get(before.caret).is_none_or(|ch| !attached(*ch))
}

/// Returns the current layout representation only when the complete field,
/// target, caret and selection equal the expected insertion.
///
/// This proof does
/// not authorize writing; the session and adapter recheck immediately before
/// it.
#[must_use]
pub fn completed_word(
    before: &FieldSnapshot,
    current: &FieldSnapshot,
    keys: &[char],
    boundary: &str,
) -> Option<String> {
    if !can_start(before)
        || current.target != before.target
        || current.selection_len != 0
        || current.sensitivity != FieldSensitivity::Normal
        || !current.supports_write
    {
        return None;
    }
    for language in Language::ALL {
        let Some(word) = text_from_keys(keys, language) else {
            continue;
        };
        let mut expected: Vec<char> = before.text.chars().collect();
        expected.splice(
            before.caret..before.caret,
            format!("{word}{boundary}").chars(),
        );
        if current.text == expected.into_iter().collect::<String>()
            && current.caret == before.caret + word.chars().count() + boundary.chars().count()
        {
            return Some(word);
        }
    }
    None
}

/// Checks an unfinished key sequence so caret, selection or external text edits
/// discard its transient context before another word boundary arrives.
#[must_use]
pub fn pending_matches(before: &FieldSnapshot, current: &FieldSnapshot, keys: &[char]) -> bool {
    if !can_start(before)
        || before.target != current.target
        || current.selection_len != 0
        || current.sensitivity != FieldSensitivity::Normal
        || !current.supports_write
    {
        return false;
    }
    Language::ALL.into_iter().any(|language| {
        let mut inserted = String::new();
        let mut letters = Vec::new();
        for ch in keys.iter().copied().chain(std::iter::once('\0')) {
            if ch.is_ascii_alphabetic() {
                letters.push(ch);
                continue;
            }
            if !letters.is_empty() {
                let Some(word) = text_from_keys(&letters, language) else {
                    return false;
                };
                inserted.push_str(&word);
                letters.clear();
            }
            if ch != '\0' {
                inserted.push(ch);
            }
        }
        let Some(committed) = current.caret.checked_sub(before.caret) else {
            return false;
        };
        if committed > inserted.chars().count() {
            return false;
        }
        // AT-SPI and some UIA providers expose only committed IME text. Keep
        // a matching committed prefix in memory, but never evaluate it. The
        // completed-word proof still requires every observed key and boundary.
        let prefix: String = inserted.chars().take(committed).collect();
        let mut expected: Vec<char> = before.text.chars().collect();
        expected.splice(before.caret..before.caret, prefix.chars());
        current.text == expected.into_iter().collect::<String>()
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::session::TargetId;
    fn field(text: &str, caret: usize) -> FieldSnapshot {
        FieldSnapshot {
            target: TargetId(1),
            text: text.into(),
            caret,
            selection_len: 0,
            sensitivity: FieldSensitivity::Normal,
            supports_write: true,
        }
    }
    #[test]
    fn waits_for_the_host_to_commit_the_observed_word_and_boundary() {
        let before = field("", 0);
        let keys: Vec<_> = "zlqhemrk".chars().collect();
        let mut reads = 0;
        let result = await_completed_word(
            &before,
            &keys,
            " ",
            std::time::Duration::from_millis(100),
            || {
                reads += 1;
                Ok::<_, ()>(match reads {
                    1 => field("zl", 2),
                    2 => field("zlqhemrk", 8),
                    _ => field("zlqhemrk ", 9),
                })
            },
        );
        assert_eq!(result, Some(field("zlqhemrk ", 9)));
        assert_eq!(reads, 3);
    }
    #[test]
    fn commit_wait_rejects_external_edits_and_caret_retreats() {
        let before = field("", 0);
        let keys: Vec<_> = "zlqhemrk".chars().collect();
        for second in [
            field("external", 8),
            field("z", 1),
            FieldSnapshot {
                target: TargetId(2),
                ..field("zlqhemrk ", 9)
            },
            FieldSnapshot {
                selection_len: 1,
                ..field("zlqhemrk ", 9)
            },
        ] {
            let mut reads = 0;
            assert!(
                await_completed_word(
                    &before,
                    &keys,
                    " ",
                    std::time::Duration::from_millis(100),
                    || {
                        reads += 1;
                        Ok::<_, ()>(if reads == 1 {
                            field("zl", 2)
                        } else {
                            second.clone()
                        })
                    }
                )
                .is_none()
            );
            assert_eq!(reads, 2);
        }
        assert!(
            await_completed_word(
                &before,
                &keys,
                " ",
                std::time::Duration::ZERO,
                || Ok::<_, ()>(before.clone())
            )
            .is_none()
        );
        assert!(
            await_completed_word(
                &before,
                &keys,
                " ",
                std::time::Duration::from_millis(100),
                || Err::<FieldSnapshot, _>(())
            )
            .is_none()
        );
    }
    #[test]
    fn pending_context_refuses_cursor_moves_edits_and_selections() {
        let before = field("", 0);
        let keys = "rPth".chars().collect::<Vec<_>>();
        assert!(pending_matches(&before, &field("rPth", 4), &keys));
        assert!(pending_matches(&before, &field("계소", 2), &keys));
        assert!(pending_matches(&before, &field("계", 1), &keys));
        assert!(pending_matches(&before, &field("", 0), &keys));
        assert!(!pending_matches(&before, &field("rPth", 2), &keys));
        assert!(!pending_matches(&before, &field("rPthx", 5), &keys));
        let mut selected = field("rPth", 4);
        selected.selection_len = 1;
        assert!(!pending_matches(&before, &selected, &keys));
    }
    #[test]
    fn proves_both_layouts_and_preserves_surrounding_content() {
        let before = field("prefix  suffix", 7);
        for word in ["rPthrgo", "계속해"] {
            let current = field(&format!("prefix {word}! suffix"), 8 + word.chars().count());
            assert_eq!(
                completed_word(
                    &before,
                    &current,
                    &"rPthrgo".chars().collect::<Vec<_>>(),
                    "!"
                ),
                Some(word.into())
            );
        }
    }
    #[test]
    fn rejects_selection_focus_caret_protection_and_unobserved_edits() {
        let before = field("", 0);
        let keys = "zlqhemrk".chars().collect::<Vec<_>>();
        let current = field("zlqhemrk ", 9);
        assert!(completed_word(&before, &current, &keys, " ").is_some());
        let mut changed = current.clone();
        changed.target = TargetId(2);
        assert!(completed_word(&before, &changed, &keys, " ").is_none());
        changed = current.clone();
        changed.caret = 8;
        assert!(completed_word(&before, &changed, &keys, " ").is_none());
        changed = current.clone();
        changed.selection_len = 1;
        assert!(completed_word(&before, &changed, &keys, " ").is_none());
        changed = current.clone();
        changed.sensitivity = FieldSensitivity::Protected;
        assert!(completed_word(&before, &changed, &keys, " ").is_none());
        changed = current;
        changed.text.push('x');
        assert!(completed_word(&before, &changed, &keys, " ").is_none());
    }
    #[test]
    fn existing_words_urls_and_uncommitted_composition_do_not_become_typed_words() {
        for text in ["prefix", "https://", "name@", "foo.", "word-"] {
            assert!(!can_start(&field(text, text.chars().count())));
        }
        assert!(!can_start(&field("suffix", 0)));
        assert!(
            completed_word(
                &field("", 0),
                &field("키보드ㄱ", 4),
                &"zlqhemrk".chars().collect::<Vec<_>>(),
                " "
            )
            .is_none()
        );
    }
}
