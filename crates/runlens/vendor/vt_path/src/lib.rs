#![expect(clippy::disallowed_types, reason = "vt_path needs to use std path types internally")]

pub mod absolute;
pub mod relative;

use std::{
    borrow::Cow,
    ffi::OsStr,
    io,
    path::{Path, StripPrefixError},
};

#[cfg(feature = "absolute-redaction")]
pub use absolute::redaction;
pub use absolute::{AbsolutePath, AbsolutePathBuf};
pub use relative::{RelativePath, RelativePathBuf};

/// Returns the current working directory as an absolute path.
///
/// # Errors
///
/// Returns an error if the current directory cannot be determined, which can occur if:
/// - The current directory has been removed
/// - The current directory is not accessible
///
/// # Panics
///
/// Panics if `std::env::current_dir()` returns a non-absolute path, which should never happen in practice.
pub fn current_dir() -> io::Result<AbsolutePathBuf> {
    #[expect(
        clippy::disallowed_methods,
        reason = "std current_dir needed to get the current working directory as an absolute path"
    )]
    let cwd = std::env::current_dir()?;
    // `std::env::current_dir` should always return a absolute path but its documentation doesn't guarantee that.
    // Do a runtime check just in case.
    Ok(AbsolutePathBuf::new(cwd).unwrap())
}

/// Strips `base` from `path`, after normalizing Windows path namespace prefixes.
///
/// On Windows, the `\\?\`, `\\.\`, and `\??\` prefixes are ignored before
/// matching. On other platforms this is equivalent to [`Path::strip_prefix`].
///
/// This is purely lexical and does not access the filesystem.
///
/// # Errors
///
/// Returns an error if `base` is not a path prefix of `path` after applying the
/// platform-specific prefix normalization above.
pub fn strip_path_prefix<'a>(path: &'a OsStr, base: &OsStr) -> Result<Cow<'a, Path>, StripPrefixError> {
    let path = strip_windows_path_prefix(path);
    let base = strip_windows_path_prefix(base);
    match path {
        Cow::Borrowed(path) => Path::new(path).strip_prefix(&*base).map(Cow::Borrowed),
        Cow::Owned(path) => Path::new(&path).strip_prefix(&*base).map(|p| Cow::Owned(p.to_path_buf())),
    }
}

/// Normalize namespace prefixes without turning UNC shares into relative paths.
/// This is lexical only and does not query the filesystem.
fn strip_windows_path_prefix(p: &OsStr) -> Cow<'_, OsStr> {
    #[cfg(windows)]
    {
        use os_str_bytes::OsStrBytesExt as _;
        // UNC needs an owned replacement: removing just the namespace would
        // leave "UNC\\server\\share", which is incorrectly workspace-relative.
        for prefix in [r"\\?\UNC\", r"\\.\UNC\", r"\??\UNC\"] {
            if let Some(stripped) = p.strip_prefix(prefix) {
                let mut unc = std::ffi::OsString::from(r"\\");
                unc.push(stripped);
                return Cow::Owned(unc);
            }
        }
        for prefix in [r"\\?\", r"\\.\", r"\??\"] {
            if let Some(stripped) = p.strip_prefix(prefix) {
                return Cow::Borrowed(stripped);
            }
        }
    }
    Cow::Borrowed(p)
}

#[cfg(test)]
mod tests {
    use std::ffi::OsStr;

    use super::*;

    #[test]
    fn strip_path_prefix_strips_base() {
        let path =
            OsStr::new(if cfg!(windows) { r"C:\repo\pkg\file.txt" } else { "/repo/pkg/file.txt" });
        let base = OsStr::new(if cfg!(windows) { r"C:\repo" } else { "/repo" });

        let stripped = strip_path_prefix(path, base).unwrap();

        assert_eq!(
            stripped,
            Path::new(if cfg!(windows) { r"pkg\file.txt" } else { "pkg/file.txt" })
        );
    }

    #[test]
    fn strip_path_prefix_reports_mismatch() {
        let path =
            OsStr::new(if cfg!(windows) { r"C:\repo\pkg\file.txt" } else { "/repo/pkg/file.txt" });
        let base = OsStr::new(if cfg!(windows) { r"C:\other" } else { "/other" });

        assert!(strip_path_prefix(path, base).is_err());
    }

    #[cfg(windows)]
    #[test]
    fn strip_path_prefix_ignores_windows_namespace_prefixes() {
        let path = OsStr::new(r"\??\C:\repo\pkg\file.txt");
        let base = OsStr::new(r"\\?\C:\repo");

        let stripped = strip_path_prefix(path, base).unwrap();

        assert_eq!(stripped, Path::new(r"pkg\file.txt"));
    }
}
