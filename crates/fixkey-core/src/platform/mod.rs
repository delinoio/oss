//! Native accessibility and listen-only input observation.

use std::sync::Arc;

use crate::session::TargetId;

#[cfg(any(target_os = "macos", test))]
mod write_ack;

/// Stable application identity, independent from its display label.
#[derive(Clone, PartialEq, Eq)]
pub enum Identity {
    Macos(String),
    WindowsApp(String),
    WindowsExecutable(String),
    Linux {
        class: String,
        desktop_entry_id: Option<String>,
    },
}
/// Non-content application metadata.
#[derive(Clone)]
pub struct Application {
    pub identity: Identity,
    pub name: String,
}
/// Content-free caret/selection identity. Native markers are opaque and must
/// only be compared within one adapter lifetime, never used as text offsets.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Cursor {
    pub target: TargetId,
    pub marker: [u64; 3],
}
/// A directly observed key or a provenance/context invalidation signal.
#[derive(Clone, Copy)]
pub enum Key {
    Character(char),
    Backspace,
    Modifier,
    Enter,
    Tab,
    Reset,
    Paste,
    Untrusted,
}
/// Safe target identity observed before dispatching a key.
#[derive(Clone, Copy)]
pub struct KeyEvent {
    pub key: Key,
    pub target: TargetId,
}
/// Callback invoked before any key is retained. It must establish permission,
/// field safety and exclusion policy without reading text from unsafe fields.
pub type Gate = Arc<dyn Fn(&Application) -> bool + Send + Sync>;
pub type Handler = Arc<dyn Fn(KeyEvent) + Send + Sync>;

#[cfg(target_os = "macos")]
mod macos;
#[cfg(target_os = "macos")]
pub use macos::Adapter;

#[cfg(target_os = "windows")]
mod windows;
#[cfg(target_os = "windows")]
pub use windows::{Adapter, available_applications};

#[cfg(any(target_os = "linux", test))]
mod linux_identity;

#[cfg(target_os = "linux")]
mod linux;
#[cfg(target_os = "linux")]
pub use linux::{Adapter, available_applications};

/// Converts character offsets into native UTF-16 offsets without splitting
/// surrogate pairs. Unknown or out-of-range offsets are rejected.
#[must_use]
pub fn utf16_offset(text: &str, characters: usize) -> Option<usize> {
    (characters <= text.chars().count())
        .then(|| text.chars().take(characters).map(char::len_utf16).sum())
}
#[must_use]
pub fn character_offset(text: &str, units: usize) -> Option<usize> {
    let mut offset = 0;
    for (index, ch) in text.chars().enumerate() {
        if offset == units {
            return Some(index);
        }
        offset += ch.len_utf16();
        if offset > units {
            return None;
        }
    }
    (offset == units).then(|| text.chars().count())
}

#[cfg(any(target_os = "windows", target_os = "linux", test))]
pub(super) fn pc_key(scan: u32, shift: bool, caps: bool) -> Key {
    if matches!(scan, 42 | 54 | 58) {
        return Key::Modifier;
    }
    if scan == 14 {
        return Key::Backspace;
    }
    if scan == 28 {
        return Key::Enter;
    }
    if scan == 15 {
        return Key::Tab;
    }
    let rows = [(16, "qwertyuiop"), (30, "asdfghjkl"), (44, "zxcvbnm")];
    for (start, row) in rows {
        if let Some(offset) = scan.checked_sub(start)
            && let Some(ch) = row.chars().nth(offset as usize)
        {
            return Key::Character(if shift ^ caps {
                ch.to_ascii_uppercase()
            } else {
                ch
            });
        }
    }
    let symbols = [
        (2, '1', '!'),
        (3, '2', '@'),
        (4, '3', '#'),
        (5, '4', '$'),
        (6, '5', '%'),
        (7, '6', '^'),
        (8, '7', '&'),
        (9, '8', '*'),
        (10, '9', '('),
        (11, '0', ')'),
        (12, '-', '_'),
        (13, '=', '+'),
        (26, '[', '{'),
        (27, ']', '}'),
        (39, ';', ':'),
        (40, '\'', '"'),
        (41, '`', '~'),
        (43, '\\', '|'),
        (51, ',', '<'),
        (52, '.', '>'),
        (53, '/', '?'),
        (57, ' ', ' '),
    ];
    symbols
        .iter()
        .find(|(code, _, _)| *code == scan)
        .map_or(Key::Reset, |(_, plain, upper)| {
            Key::Character(if shift { *upper } else { *plain })
        })
}

/// Assigns an equality-checked identity token; a hash alone is not target
/// identity.
#[cfg(any(target_os = "windows", target_os = "linux"))]
fn identity_token(key: String) -> TargetId {
    static CURRENT: std::sync::Mutex<Option<(String, TargetId)>> = std::sync::Mutex::new(None);
    let mut current = CURRENT
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    if let Some((previous, id)) = &*current
        && previous == &key
    {
        return *id;
    }
    let id = next_target();
    *current = Some((key, id));
    id
}
fn next_target() -> TargetId {
    static NEXT: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(1);
    TargetId(NEXT.fetch_add(1, std::sync::atomic::Ordering::Relaxed))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn physical_keys_keep_shift_caps_and_navigation_distinct() {
        assert!(matches!(pc_key(19, true, false), Key::Character('R')));
        assert!(matches!(pc_key(19, true, true), Key::Character('r')));
        assert!(matches!(pc_key(40, false, false), Key::Character('\'')));
        assert!(matches!(pc_key(14, false, false), Key::Backspace));
        assert!(matches!(pc_key(28, false, false), Key::Enter));
        assert!(matches!(pc_key(255, false, false), Key::Reset));
    }
    #[test]
    fn utf16_ranges_preserve_korean_and_surrogate_boundaries() {
        let text = "a😀키보드";
        assert_eq!(utf16_offset(text, 2), Some(3));
        assert_eq!(character_offset(text, 3), Some(2));
        assert_eq!(character_offset(text, 2), None);
        assert_eq!(utf16_offset(text, 99), None);
    }
}
