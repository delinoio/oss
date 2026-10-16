// SPDX-License-Identifier: Apache-2.0
//! Platform-neutral resolution of a Windows `lpApplicationName` against the
//! caller's directory state.
//!
//! `CreateProcess` resolves a partial application path against the *caller's*
//! current drive and directory, never against the requested child
//! `lpCurrentDirectory`. This module works on raw UTF-16 units so the rules
//! stay unit-testable on every host and tolerate unpaired surrogates.

const BACKSLASH: u16 = b'\\' as u16;
const SLASH: u16 = b'/' as u16;
const COLON: u16 = b':' as u16;

fn separator(unit: u16) -> bool {
    unit == BACKSLASH || unit == SLASH
}

fn letter(unit: u16) -> bool {
    u8::try_from(unit).is_ok_and(|byte| byte.is_ascii_alphabetic())
}

fn drive_of(path: &[u16]) -> Option<u16> {
    match path {
        [drive, COLON, ..] if letter(*drive) => Some(*drive),
        _ => None,
    }
}

fn same_drive(left: u16, right: u16) -> bool {
    u8::try_from(left)
        .ok()
        .zip(u8::try_from(right).ok())
        .is_some_and(|(left, right)| left.eq_ignore_ascii_case(&right))
}

fn join(base: &[u16], rest: &[u16]) -> Vec<u16> {
    let mut joined = base.to_vec();
    if !joined.last().is_some_and(|unit| separator(*unit)) {
        joined.push(BACKSLASH);
    }
    joined.extend_from_slice(rest);
    joined
}

/// Length of the `\\server\share` prefix of a UNC path, when present.
fn unc_root(path: &[u16]) -> Option<usize> {
    if path.len() < 2 || !separator(path[0]) || !separator(path[1]) {
        return None;
    }
    let mut index = 2;
    let server = index;
    while index < path.len() && !separator(path[index]) {
        index += 1;
    }
    if index == server || index == path.len() {
        return None;
    }
    index += 1;
    let share = index;
    while index < path.len() && !separator(path[index]) {
        index += 1;
    }
    if index == share {
        return None;
    }
    Some(index)
}

/// Resolve `application` the way native process creation would for a caller
/// whose current directory is `caller`.
///
/// `drive_directory` supplies the caller's remembered directory on a drive
/// other than the current one (the `=X:` environment record). Absolute,
/// UNC, device and `\\?\` inputs are returned unchanged. Returns `None` when
/// the input depends on caller state that is unavailable (for example a
/// root-relative path when the caller directory has no drive or share root).
pub fn resolve_application(
    application: &[u16],
    caller: &[u16],
    drive_directory: impl Fn(u16) -> Option<Vec<u16>>,
) -> Option<Vec<u16>> {
    if application.is_empty() {
        return None;
    }
    if application.len() >= 2 && separator(application[0]) && separator(application[1]) {
        return Some(application.to_vec());
    }
    if let Some(drive) = drive_of(application) {
        let rest = &application[2..];
        if rest.first().is_some_and(|unit| separator(*unit)) {
            return Some(application.to_vec());
        }
        // Drive-relative `C:name`: use the caller directory on the same
        // drive, otherwise the remembered per-drive directory or that
        // drive's root.
        if drive_of(caller).is_some_and(|current| same_drive(current, drive)) {
            return Some(join(caller, rest));
        }
        return Some(match drive_directory(drive) {
            Some(directory) if drive_of(&directory).is_some() => join(&directory, rest),
            _ => join(&[drive, COLON, BACKSLASH], rest),
        });
    }
    if separator(application[0]) {
        // Root-relative `\name`: current drive or UNC share root.
        let root = if let Some(drive) = drive_of(caller) {
            vec![drive, COLON]
        } else {
            caller[..unc_root(caller)?].to_vec()
        };
        let mut resolved = root;
        resolved.extend_from_slice(application);
        return Some(resolved);
    }
    if caller.is_empty() {
        return None;
    }
    Some(join(caller, application))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn wide(value: &str) -> Vec<u16> {
        value.encode_utf16().collect()
    }
    fn resolve(application: &str, caller: &str) -> Option<String> {
        let remembered = |drive: u16| match char::from_u32(u32::from(drive))?.to_ascii_uppercase() {
            'D' => Some(wide(r"D:\work\sub")),
            _ => None,
        };
        resolve_application(&wide(application), &wide(caller), remembered)
            .map(|value| String::from_utf16(&value).unwrap())
    }

    #[test]
    fn relative_paths_use_the_caller_directory() {
        assert_eq!(
            resolve(r"bin\worker.exe", r"C:\caller").unwrap(),
            r"C:\caller\bin\worker.exe"
        );
        assert_eq!(
            resolve("bin/worker.exe", r"C:\caller\").unwrap(),
            r"C:\caller\bin/worker.exe"
        );
        assert_eq!(
            resolve(r"..\worker.exe", r"C:\caller").unwrap(),
            r"C:\caller\..\worker.exe"
        );
        assert_eq!(
            resolve(r"bin\worker.exe", r"\\server\share\caller").unwrap(),
            r"\\server\share\caller\bin\worker.exe"
        );
    }

    #[test]
    fn absolute_unc_and_device_paths_are_unchanged() {
        for value in [
            r"C:\tools\worker.exe",
            "c:/tools/worker.exe",
            r"\\server\share\worker.exe",
            r"\\?\C:\tools\worker.exe",
            r"\\.\pipe\worker",
        ] {
            assert_eq!(resolve(value, r"C:\caller").unwrap(), value);
        }
    }

    #[test]
    fn root_relative_paths_use_the_caller_drive_or_share() {
        assert_eq!(
            resolve(r"\tools\worker.exe", r"E:\caller\deep").unwrap(),
            r"E:\tools\worker.exe"
        );
        assert_eq!(
            resolve(r"\tools\worker.exe", r"\\server\share\caller").unwrap(),
            r"\\server\share\tools\worker.exe"
        );
        assert_eq!(
            resolve(r"\tools\worker.exe", r"\\server\share").unwrap(),
            r"\\server\share\tools\worker.exe"
        );
        assert_eq!(resolve(r"\tools\worker.exe", r"\\server"), None);
        assert_eq!(resolve(r"\tools\worker.exe", ""), None);
    }

    #[test]
    fn drive_relative_paths_follow_per_drive_directories() {
        assert_eq!(
            resolve(r"C:bin\worker.exe", r"C:\caller").unwrap(),
            r"C:\caller\bin\worker.exe"
        );
        assert_eq!(
            resolve(r"c:bin\worker.exe", r"C:\caller").unwrap(),
            r"C:\caller\bin\worker.exe"
        );
        assert_eq!(
            resolve(r"D:bin\worker.exe", r"C:\caller").unwrap(),
            r"D:\work\sub\bin\worker.exe"
        );
        assert_eq!(
            resolve(r"E:bin\worker.exe", r"C:\caller").unwrap(),
            r"E:\bin\worker.exe"
        );
        assert_eq!(
            resolve(r"E:bin\worker.exe", r"\\server\share\caller").unwrap(),
            r"E:\bin\worker.exe"
        );
    }

    #[test]
    fn empty_inputs_are_unresolvable() {
        assert_eq!(resolve("", r"C:\caller"), None);
        assert_eq!(resolve("worker.exe", ""), None);
    }
}
