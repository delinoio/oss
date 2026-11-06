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
            write!(f, "{e}")?;
        }
        Ok(())
    }
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
    fn detects_not_found() {
        let e = anyhow::Error::new(std::io::Error::from(std::io::ErrorKind::NotFound)).context("x");
        assert!(is_not_found(&e));
        assert!(join([None, None]).is_none());
    }
}
