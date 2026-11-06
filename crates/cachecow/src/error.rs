// SPDX-License-Identifier: Apache-2.0
//! Error helpers shared by every CacheCow module.
//!
//! Modules return [`anyhow::Error`] and classify failures with typed sentinel
//! errors. [`find`] searches the full cause chain, including every member of a
//! [`Joined`] error, the way Go's `errors.Is`/`errors.As` searched wrapped and
//! joined errors.

use std::fmt;

pub type Result<T, E = anyhow::Error> = std::result::Result<T, E>;

/// Several independent failures, such as an operation error followed by a
/// cleanup error. Display joins the members with line breaks.
#[derive(Debug)]
pub struct Joined(pub Vec<anyhow::Error>);

impl fmt::Display for Joined {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        for (i, e) in self.0.iter().enumerate() {
            if i > 0 {
                f.write_str("\n")?;
            }
            f.write_str(&display_text(e))?;
        }
        Ok(())
    }
}

/// Text of an I/O error. CacheCow diagnostics are English, but the standard
/// library describes a Windows error code in the user's UI language, for
/// example Korean on a ko-KR system. On Windows an operating-system error is
/// therefore described by the en-US system message, in the same
/// `message (os error N)` shape the standard library uses. A message that the
/// system cannot provide in English, and every error without an
/// operating-system code, keeps the standard text. Other platforms always use
/// the standard text.
pub fn io_text(error: &std::io::Error) -> String {
    #[cfg(windows)]
    if let Some(code) = error.raw_os_error()
        && let Some(message) = english_system_message(code)
    {
        return format!("{message} (os error {code})");
    }
    error.to_string()
}

/// `Display` text of one error in a cause chain, with [`io_text`] for I/O
/// errors.
pub fn cause_text(cause: &(dyn std::error::Error + 'static)) -> String {
    match cause.downcast_ref::<std::io::Error>() {
        Some(io) => io_text(io),
        None => cause.to_string(),
    }
}

/// `Display` text of the outermost layer of `error`: [`io_text`] when that
/// layer is an I/O error, and the ordinary text when a context message wraps
/// it.
pub fn display_text(error: &anyhow::Error) -> String {
    let text = error.to_string();
    match error.downcast_ref::<std::io::Error>() {
        Some(io) if io.to_string() == text => io_text(io),
        _ => text,
    }
}

/// Every layer of the cause chain, joined by `: ` with repeated text omitted.
/// Operating-system error text is English (see [`io_text`]).
pub fn chain_text(error: &anyhow::Error) -> String {
    let mut text = String::new();
    for cause in error.chain() {
        let part = cause_text(cause);
        if text.is_empty() {
            text = part;
        } else if !text.contains(&part) {
            text.push_str(": ");
            text.push_str(&part);
        }
    }
    text
}

/// The en-US description of a Win32 error code, without the trailing line
/// break that `FormatMessageW` appends. `None` when the system has no English
/// message for the code.
#[cfg(windows)]
fn english_system_message(code: i32) -> Option<String> {
    use windows_sys::Win32::System::Diagnostics::Debug::{
        FORMAT_MESSAGE_FROM_SYSTEM, FORMAT_MESSAGE_IGNORE_INSERTS, FormatMessageW,
    };
    /// `MAKELANGID(LANG_ENGLISH, SUBLANG_ENGLISH_US)`.
    const LANGUAGE_ENGLISH_US: u32 = 0x0409;
    let mut buffer = [0u16; 512];
    // SAFETY: `buffer` is writable for the `buffer.len()` UTF-16 units passed
    // as the size, no insert arguments are used
    // (`FORMAT_MESSAGE_IGNORE_INSERTS`), and the system message table is
    // the only source.
    let length = unsafe {
        FormatMessageW(
            FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS,
            std::ptr::null(),
            code as u32,
            LANGUAGE_ENGLISH_US,
            buffer.as_mut_ptr(),
            buffer.len() as u32,
            std::ptr::null(),
        )
    } as usize;
    if length == 0 || length > buffer.len() {
        return None;
    }
    let message = String::from_utf16(&buffer[..length]).ok()?;
    let message = message.trim_end();
    (!message.is_empty()).then(|| message.to_string())
}

impl std::error::Error for Joined {}

/// Joins the present errors. Returns `None` when every input is `None`, and the
/// only error unchanged when exactly one is present.
pub fn join(errors: impl IntoIterator<Item = Option<anyhow::Error>>) -> Option<anyhow::Error> {
    let mut all: Vec<anyhow::Error> = errors.into_iter().flatten().collect();
    match all.len() {
        0 => None,
        1 => all.pop(),
        _ => Some(anyhow::Error::new(Joined(all))),
    }
}

/// Joins an operation result with an optional cleanup error.
pub fn join_result<T>(result: Result<T>, cleanup: Option<anyhow::Error>) -> Result<T> {
    match (result, cleanup) {
        (Ok(v), None) => Ok(v),
        (Ok(_), Some(e)) => Err(e),
        (Err(e), None) => Err(e),
        (Err(e), Some(c)) => Err(anyhow::Error::new(Joined(vec![e, c]))),
    }
}

/// Discovery evidence that no longer matches the checkout or the cache
/// configuration, for example because a source file appeared in a worktree
/// while the command waited for a lease. The evidence is only a validation
/// token, so a fresh discovery supersedes it and the managed flow repeats
/// discovery instead of reporting the change to the user.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
#[error("{0}")]
pub struct StaleDiscovery(pub String);

/// Builds a [`StaleDiscovery`] error with the given user-facing message.
pub fn stale(message: impl Into<String>) -> anyhow::Error {
    anyhow::Error::new(StaleDiscovery(message.into()))
}

/// Finds the first error of type `T` in the cause chain of `error`, including
/// the members of [`Joined`] errors at any depth.
pub fn find<T>(error: &anyhow::Error) -> Option<&T>
where
    T: std::error::Error + Send + Sync + 'static,
{
    for cause in error.chain() {
        if let Some(v) = cause.downcast_ref::<T>() {
            return Some(v);
        }
        if let Some(joined) = cause.downcast_ref::<Joined>() {
            for member in &joined.0 {
                if let Some(v) = find::<T>(member) {
                    return Some(v);
                }
            }
        }
    }
    None
}

/// Reports whether `error` carries the sentinel value `expected`.
pub fn is<T>(error: &anyhow::Error, expected: &T) -> bool
where
    T: std::error::Error + PartialEq + Send + Sync + 'static,
{
    if error
        .chain()
        .any(|c| c.downcast_ref::<T>() == Some(expected))
    {
        return true;
    }
    error.chain().any(|c| {
        c.downcast_ref::<Joined>()
            .is_some_and(|j| j.0.iter().any(|m| is(m, expected)))
    })
}

/// Reports whether `error` contains an I/O error of the given kind.
pub fn is_io_kind(error: &anyhow::Error, kind: std::io::ErrorKind) -> bool {
    if error.chain().any(|c| {
        c.downcast_ref::<std::io::Error>()
            .is_some_and(|e| e.kind() == kind)
    }) {
        return true;
    }
    error.chain().any(|c| {
        c.downcast_ref::<Joined>()
            .is_some_and(|j| j.0.iter().any(|m| is_io_kind(m, kind)))
    })
}

/// Go `os.IsNotExist` equivalent for errors that kept their I/O cause.
pub fn is_not_found(error: &anyhow::Error) -> bool {
    is_io_kind(error, std::io::ErrorKind::NotFound)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Debug, thiserror::Error, PartialEq, Eq)]
    enum Sentinel {
        #[error("first")]
        First,
        #[error("second")]
        Second,
    }

    #[test]
    fn finds_sentinels_through_context_and_joins() {
        let e = anyhow::Error::new(Sentinel::Second).context("outer");
        assert!(is(&e, &Sentinel::Second));
        assert!(!is(&e, &Sentinel::First));
        let joined = join([Some(anyhow::anyhow!("plain")), Some(e)]).unwrap();
        assert!(is(&joined, &Sentinel::Second));
        assert_eq!(find::<Sentinel>(&joined), Some(&Sentinel::Second));
        assert_eq!(joined.to_string(), "plain\nouter");
    }

    #[test]
    fn io_errors_without_an_os_code_keep_their_text() {
        let e = std::io::Error::other("custom failure");
        assert_eq!(io_text(&e), "custom failure");
        let e = anyhow::Error::new(e).context("outer");
        assert_eq!(chain_text(&e), "outer: custom failure");
        assert_eq!(display_text(&e), "outer");
    }

    #[cfg(not(windows))]
    #[test]
    fn other_platforms_keep_the_standard_os_text() {
        let e = std::io::Error::from_raw_os_error(2);
        assert_eq!(io_text(&e), e.to_string());
    }

    /// The expected text must not depend on the UI language of the machine that
    /// runs the test: the standard library would describe these codes in
    /// Korean on a ko-KR system.
    #[cfg(windows)]
    #[test]
    fn windows_os_errors_use_english_system_text() {
        let os = std::io::Error::from_raw_os_error;
        assert_eq!(io_text(&os(5)), "Access is denied. (os error 5)");
        assert_eq!(
            io_text(&os(112)),
            "There is not enough space on the disk. (os error 112)"
        );
        // A code without a system message keeps the standard text.
        assert_eq!(io_text(&os(0x7fff_fff0)), os(0x7fff_fff0).to_string());

        let sharing = "The process cannot access the file because it is being used by another \
                       process. (os error 32)";
        let chained = anyhow::Error::new(os(32)).context("open cache entry");
        assert_eq!(chain_text(&chained), format!("open cache entry: {sharing}"));
        assert_eq!(
            crate::session::error_text(&chained),
            format!("open cache entry: {sharing}")
        );
        assert_eq!(display_text(&anyhow::Error::new(os(32))), sharing);
    }

    #[cfg(windows)]
    #[test]
    fn windows_wrapped_path_and_joined_errors_are_english() {
        let os = std::io::Error::from_raw_os_error;
        let wrapped = crate::clone::wrap(os(112), "copy cache entry \"a\"");
        let space = "There is not enough space on the disk. (os error 112)";
        assert_eq!(
            wrapped.to_string(),
            format!("copy cache entry \"a\": {space}")
        );
        assert_eq!(chain_text(&wrapped), wrapped.to_string());

        let path = crate::repository::path_error("lstat", "C:\\absent", os(3));
        assert_eq!(
            path.to_string(),
            "lstat C:\\absent: The system cannot find the path specified. (os error 3)"
        );
        assert!(is_not_found(&path), "typed I/O cause must stay searchable");

        let joined = join([Some(anyhow::Error::new(os(5))), Some(wrapped)]).unwrap();
        assert_eq!(
            joined.to_string(),
            format!("Access is denied. (os error 5)\ncopy cache entry \"a\": {space}")
        );
    }

    #[test]
    fn detects_not_found() {
        let e = anyhow::Error::new(std::io::Error::from(std::io::ErrorKind::NotFound)).context("x");
        assert!(is_not_found(&e));
        assert!(join([None, None]).is_none());
    }
}
