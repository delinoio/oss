// SPDX-License-Identifier: Apache-2.0
//! Shell activation code and confirmed, atomic profile updates (Go
//! `internal/shell`). Never writes diagnostics to stdout or prompts without
//! the caller's participation.

use std::{
    fs,
    io::{self, Write},
};

use anyhow::{Result, anyhow};
use serde::{Deserialize, Serialize};

use crate::paths::{self, FileId};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    Bash,
    Zsh,
    #[serde(rename = "powershell")]
    PowerShell,
}

impl Kind {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Bash => "bash",
            Self::Zsh => "zsh",
            Self::PowerShell => "powershell",
        }
    }

    /// Parses a shell name; the error matches `unsupported shell "x"; use bash,
    /// zsh, or powershell`.
    pub fn parse(value: &str) -> Result<Self> {
        match value {
            "bash" => Ok(Self::Bash),
            "zsh" => Ok(Self::Zsh),
            "powershell" => Ok(Self::PowerShell),
            other => Err(anyhow!(
                "unsupported shell {other:?}; use bash, zsh, or powershell"
            )),
        }
    }
}

impl std::fmt::Display for Kind {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Action {
    Setup,
    Remove,
}

impl Action {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "setup" => Some(Self::Setup),
            "remove" => Some(Self::Remove),
            _ => None,
        }
    }

    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Setup => "setup",
            Self::Remove => "remove",
        }
    }
}

fn quote(s: &str) -> String {
    format!("'{}'", s.replace('\'', "'\"'\"'"))
}

/// Quotes `s` as a PowerShell single-quoted literal. PowerShell treats the
/// ASCII apostrophe and the typographic quotes U+2018, U+2019, U+201A and
/// U+201B as single-quote delimiters, so each of them must be doubled.
fn psquote(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    out.push('\'');
    for c in s.chars() {
        out.push(c);
        if matches!(c, '\'' | '\u{2018}' | '\u{2019}' | '\u{201A}' | '\u{201B}') {
            out.push(c);
        }
    }
    out.push('\'');
    out
}

/// Returns activation code only. The caller sends notices separately to stderr.
pub fn init(kind: Kind, shim_dir: &str) -> Result<String> {
    if !paths::is_abs(shim_dir) || shim_dir.contains(['\0', '\r', '\n']) {
        return Err(anyhow!(
            "shim directory must be an absolute path without NUL or line breaks"
        ));
    }
    if kind == Kind::PowerShell {
        if shim_dir.contains(paths::LIST_SEPARATOR) {
            return Err(anyhow!("shim directory cannot contain the PATH separator"));
        }
        let q = psquote(shim_dir);
        return Ok(format!(
            "$env:PATH = {q} + [IO.Path]::PathSeparator + (($env:PATH -split \
             [regex]::Escape([string][IO.Path]::PathSeparator) | Where-Object {{ $_ -ne {q} }}) \
             -join [IO.Path]::PathSeparator)\n"
        ));
    }
    if shim_dir.contains(':') {
        return Err(anyhow!(
            "shim directory cannot contain the PATH separator ':'"
        ));
    }
    // Remove duplicate entries without glob matching or shell evaluation. Empty
    // PATH entries retain their meaning. Bash and Zsh both support this syntax.
    Ok(format!(
        "_ccow_dir={}\n_ccow_rest=${{PATH-}}\n_ccow_tail=\n_ccow_sep=\nwhile :; do\n  case \
         $_ccow_rest in\n    *:*) _ccow_part=${{_ccow_rest%%:*}}; _ccow_rest=${{_ccow_rest#*:}}; \
         _ccow_more=1 ;;\n    *) _ccow_part=$_ccow_rest; _ccow_more=0 ;;\n  esac\n\x20 if [ \
         \"$_ccow_part\" != \"$_ccow_dir\" ]; then\n    \
         _ccow_tail=\"$_ccow_tail$_ccow_sep$_ccow_part\"\n    _ccow_sep=:\n  fi\n  [ \
         \"$_ccow_more\" = 1 ] || \
         break\ndone\nPATH=\"$_ccow_dir${{_ccow_sep:+:}}$_ccow_tail\"\nexport PATH\nunset \
         _ccow_dir _ccow_rest _ccow_tail _ccow_sep _ccow_part _ccow_more\n",
        quote(shim_dir)
    ))
}

#[derive(Debug, Clone, Default)]
pub struct Selection {
    /// Explicit `--file` takes precedence.
    pub file: String,
    pub home: String,
    pub zdotdir: String,
    /// `$PROFILE.CurrentUserCurrentHost` from PowerShell 7.
    pub powershell_profile: String,
}

/// Never guesses between existing Bash startup files or PowerShell hosts.
pub fn select_file(kind: Kind, selection: &Selection) -> Result<String> {
    if !selection.file.is_empty() {
        return paths::abs(&selection.file);
    }
    if kind == Kind::PowerShell {
        if selection.powershell_profile.is_empty() {
            return Err(anyhow!(
                "PowerShell profile selection is ambiguous; pass --file with \
                 $PROFILE.CurrentUserCurrentHost from PowerShell 7"
            ));
        }
        return paths::abs(&selection.powershell_profile);
    }
    if selection.home.is_empty() {
        return Err(anyhow!(
            "home directory is unknown; select a configuration file with --file"
        ));
    }
    if kind == Kind::Zsh {
        let dir = if selection.zdotdir.is_empty() {
            &selection.home
        } else {
            &selection.zdotdir
        };
        return paths::abs(&paths::join2(dir, ".zshrc"));
    }
    let mut existing = Vec::new();
    for name in [".bashrc", ".bash_profile", ".bash_login", ".profile"] {
        let p = paths::join2(&selection.home, name);
        match fs::symlink_metadata(&p) {
            Ok(_) => existing.push(p),
            Err(e) if e.kind() == io::ErrorKind::NotFound => {}
            Err(e) => return Err(e.into()),
        }
    }
    match existing.len() {
        0 => paths::abs(&paths::join2(&selection.home, ".bashrc")),
        1 => paths::abs(&existing[0]),
        _ => Err(anyhow!(
            "multiple Bash configuration files exist; select the intended file with --file"
        )),
    }
}

const BEGIN: &str = "# >>> CacheCow owned activation >>>";
const END: &str = "# <<< CacheCow owned activation <<<";
/// Records a separator inserted for a profile without a final newline, so
/// removal can restore the original bytes exactly.
const JOINED_BEGIN: &str = "# >>> CacheCow owned activation (joined) >>>";

#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum ShellError {
    #[error(
        "shell update requires confirmation; review the proposed target/change and use --yes for \
         noninteractive operation"
    )]
    ConfirmationRequired,
    #[error("shell update cancelled")]
    Cancelled,
}

/// A reviewed profile change.
pub struct Plan {
    pub path: String,
    pub kind: Kind,
    pub action: Action,
    pub before: Vec<u8>,
    pub after: Vec<u8>,
    mode: u32,
    existed: bool,
    identity: Option<FileId>,
    security: Option<security::FileSecurity>,
    removed_block: Vec<u8>,
    added_block: Vec<u8>,
    separator: bool,
}

/// Confirmation callback. Returns false on cancellation.
pub type Confirm<'a> = &'a mut dyn FnMut(&Plan) -> Result<bool>;

pub struct Approval<'a> {
    pub yes: bool,
    pub interactive: bool,
    /// Must show the target and owned-block change before asking. Never
    /// called in noninteractive mode.
    pub confirm: Option<Confirm<'a>>,
}

fn permission_bits(meta: &fs::Metadata) -> u32 {
    paths::go_file_mode(meta) & 0o777
}

impl Plan {
    pub fn changed(&self) -> bool {
        self.before != self.after
    }

    /// Owned block bytes removed and added, and whether a separator line break
    /// outside the block is added or removed. Never returns other content.
    pub fn owned_change(&self) -> (Vec<u8>, Vec<u8>, bool) {
        if !self.changed() {
            return (Vec::new(), Vec::new(), false);
        }
        (
            self.removed_block.clone(),
            self.added_block.clone(),
            self.separator,
        )
    }

    /// A notice, not activation code.
    pub fn current_session_instruction(&self) -> String {
        if self.action == Action::Remove {
            return "This change applies to new shells. In this shell, remove the CacheCow shim \
                    directory from PATH or restart the shell."
                .to_string();
        }
        format!(
            "This change applies to new shells. Apply `ccow shell init {}` output in this shell \
             to activate now.",
            self.kind
        )
    }

    pub fn apply(&self, approval: Approval<'_>) -> Result<()> {
        // A plan that changes nothing needs no approval. `apply_with` still
        // verifies that the profile did not change after review.
        if self.changed() && !approval.yes {
            let Some(confirm) = approval.confirm.filter(|_| approval.interactive) else {
                return Err(ShellError::ConfirmationRequired.into());
            };
            if !confirm(self)? {
                return Err(ShellError::Cancelled.into());
            }
        }
        self.apply_with(&|from, to| fs::rename(from, to))
    }

    fn unchanged(&self) -> Result<()> {
        let st = fs::symlink_metadata(&self.path);
        if !self.existed {
            return match st {
                Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(()),
                Err(e) => Err(e.into()),
                Ok(_) => Err(anyhow!(
                    "configuration appeared after review; prepare and confirm again"
                )),
            };
        }
        let st = st?;
        let same = paths::file_id(&self.path, false).ok() == self.identity;
        if !st.file_type().is_file() || !same || permission_bits(&st) != self.mode {
            return Err(anyhow!(
                "configuration changed after review; prepare and confirm again"
            ));
        }
        if let Some(security) = &self.security
            && !security.matches(&self.path)?
        {
            return Err(anyhow!(
                "configuration permissions changed after review; prepare and confirm again"
            ));
        }
        if fs::read(&self.path)? != self.before {
            return Err(anyhow!(
                "configuration changed after review; prepare and confirm again"
            ));
        }
        Ok(())
    }

    fn apply_with(&self, rename: &dyn Fn(&str, &str) -> io::Result<()>) -> Result<()> {
        self.unchanged()?;
        if !self.changed() {
            return Ok(());
        }
        // The profile directory must already exist. Do not create arbitrary
        // parent directories selected by an ambiguous shell configuration.
        let (mut f, name) = create_temp(&paths::dir(&self.path), ".cachecow-shell-")?;
        let _guard = TempGuard(name.clone());
        set_mode(&f, self.mode)?;
        f.write_all(&self.after)?;
        f.sync_all()?;
        drop(f);
        self.unchanged()?;
        if let Some(security) = &self.security {
            security.preserve(&name)?;
        }
        rename(&name, &self.path)?;
        tracing::debug!(
            path = %self.path,
            shell = %self.kind,
            action = self.action.as_str(),
            "CacheCow shell configuration updated"
        );
        Ok(())
    }
}

struct TempGuard(String);

impl Drop for TempGuard {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.0);
    }
}

fn create_temp(dir: &str, prefix: &str) -> io::Result<(fs::File, String)> {
    loop {
        let name = paths::join2(dir, &format!("{prefix}{}", uuid::Uuid::now_v7().simple()));
        let mut options = fs::OpenOptions::new();
        options.read(true).write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        match options.open(&name) {
            Ok(f) => return Ok((f, name)),
            Err(e) if e.kind() == io::ErrorKind::AlreadyExists => {}
            Err(e) => return Err(e),
        }
    }
}

fn set_mode(file: &fs::File, mode: u32) -> io::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        file.set_permissions(fs::Permissions::from_mode(mode))
    }
    #[cfg(windows)]
    {
        let mut permissions = file.metadata()?.permissions();
        permissions.set_readonly(mode & 0o200 == 0);
        file.set_permissions(permissions)
    }
}

/// Byte offsets of the owned block: `(start, stop, joined)`.
fn block(b: &[u8]) -> Result<Option<(usize, usize, bool)>> {
    let mut start: Option<(usize, bool)> = None;
    let mut stop: Option<usize> = None;
    let mut offset = 0;
    for line in b.split_inclusive(|c| *c == b'\n') {
        let text = line.strip_suffix(b"\n").unwrap_or(line);
        let text = text.strip_suffix(b"\r").unwrap_or(text);
        if text == BEGIN.as_bytes() || text == JOINED_BEGIN.as_bytes() {
            if start.is_some() {
                return Err(anyhow!(
                    "multiple or nested CacheCow blocks; repair the configuration manually"
                ));
            }
            start = Some((offset, text == JOINED_BEGIN.as_bytes()));
        } else if text == END.as_bytes() {
            if start.is_none() || stop.is_some() {
                return Err(anyhow!(
                    "unmatched CacheCow block marker; repair the configuration manually"
                ));
            }
            stop = Some(offset + line.len());
        }
        offset += line.len();
    }
    match (start, stop) {
        (None, _) => Ok(None),
        (Some(_), None) => Err(anyhow!(
            "unfinished CacheCow block; repair the configuration manually"
        )),
        (Some((s, joined)), Some(e)) => Ok(Some((s, e, joined))),
    }
}

pub fn prepare(kind: Kind, action: Action, path: &str, shim_dir: &str) -> Result<Plan> {
    let abs = paths::abs(path)?;
    let mut p = Plan {
        path: abs.clone(),
        kind,
        action,
        before: Vec::new(),
        after: Vec::new(),
        mode: 0o600,
        existed: false,
        identity: None,
        security: None,
        removed_block: Vec::new(),
        added_block: Vec::new(),
        separator: false,
    };
    match fs::symlink_metadata(&abs) {
        Ok(st) => {
            if !st.file_type().is_file() {
                return Err(anyhow!(
                    "configuration {abs} is not a regular file; use --file to select its real path"
                ));
            }
            p.identity = Some(paths::file_id(&abs, false)?);
            p.existed = true;
            p.mode = permission_bits(&st);
            p.security = Some(security::FileSecurity::read(&abs)?);
            p.before = fs::read(&abs)?;
        }
        Err(e) if e.kind() == io::ErrorKind::NotFound => {}
        Err(e) => return Err(e.into()),
    }
    let found = block(&p.before)?;
    if action == Action::Remove {
        p.after.clone_from(&p.before);
        if let Some((mut start, stop, joined)) = found {
            p.removed_block = p.before[start..stop].to_vec();
            // The separator added by setup can be removed only when the block
            // ends the file. Otherwise the content after the block
            // would run into the last line before it.
            if joined && stop == p.before.len() {
                if start >= 2 && &p.before[start - 2..start] == b"\r\n" {
                    start -= 2;
                    p.separator = true;
                } else if start > 0 && p.before[start - 1] == b'\n' {
                    start -= 1;
                    p.separator = true;
                }
            }
            p.after = [&p.before[..start], &p.before[stop..]].concat();
        }
        return Ok(p);
    }
    let code = init(kind, shim_dir)?;
    let nl = if p.before.windows(2).any(|w| w == b"\r\n") {
        "\r\n"
    } else {
        "\n"
    };
    let mut marker = BEGIN;
    if found.is_some_and(|(_, _, joined)| joined) {
        marker = JOINED_BEGIN;
    }
    if found.is_none() && p.before.last().is_some_and(|c| *c != b'\n') {
        marker = JOINED_BEGIN;
    }
    let owned = format!("{marker}{nl}{}{END}{nl}", code.replace('\n', nl)).into_bytes();
    p.added_block.clone_from(&owned);
    if let Some((start, stop, _)) = found {
        p.removed_block = p.before[start..stop].to_vec();
        p.after = [&p.before[..start], &owned[..], &p.before[stop..]].concat();
    } else {
        p.after.clone_from(&p.before);
        if marker == JOINED_BEGIN {
            p.after.extend_from_slice(nl.as_bytes());
            p.separator = true;
        }
        p.after.extend_from_slice(&owned);
    }
    Ok(p)
}

#[cfg(not(windows))]
mod security {
    use anyhow::Result;

    /// POSIX permission bits are recorded and checked by the plan itself.
    pub struct FileSecurity;

    impl FileSecurity {
        #[allow(clippy::unnecessary_wraps, reason = "matches the Windows signature")]
        pub const fn read(_path: &str) -> Result<Self> {
            Ok(Self)
        }

        #[allow(clippy::unnecessary_wraps, reason = "matches the Windows signature")]
        pub const fn matches(&self, _path: &str) -> Result<bool> {
            Ok(true)
        }

        #[allow(clippy::unnecessary_wraps, reason = "matches the Windows signature")]
        pub const fn preserve(&self, _path: &str) -> Result<()> {
            Ok(())
        }
    }
}

#[cfg(windows)]
mod security {
    use anyhow::Result;

    use crate::winsec::{self, Parts, SecurityInfo};

    const PARTS: Parts = Parts {
        owner: true,
        group: true,
        dacl: true,
    };

    /// Preserves the profile's owner, group and DACL across atomic replacement.
    /// A failure to apply them stops before the original file is replaced.
    pub struct FileSecurity(SecurityInfo);

    impl FileSecurity {
        pub fn read(path: &str) -> Result<Self> {
            Ok(Self(winsec::read_security(path, PARTS)?))
        }

        pub fn matches(&self, path: &str) -> Result<bool> {
            Ok(self.0.sddl == winsec::read_security(path, PARTS)?.sddl)
        }

        pub fn preserve(&self, path: &str) -> Result<()> {
            winsec::apply_security(path, &self.0)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn temp() -> (tempfile::TempDir, String) {
        let t = tempfile::tempdir().unwrap();
        let p = paths::path_string(t.path()).unwrap();
        (t, p)
    }

    /// Bash and Zsh refuse ':' in the shim directory, and Windows drive paths
    /// contain one. The directory is never accessed, so a UNC form suffices.
    fn posix_shims(dir: &str) -> String {
        #[cfg(windows)]
        return format!(r"\\ccow-test\share{}", &dir[2..]);
        #[cfg(unix)]
        dir.to_string()
    }

    fn write_mode(path: &str, bytes: &[u8], mode: u32) {
        fs::write(path, bytes).unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(path, fs::Permissions::from_mode(mode)).unwrap();
        }
        #[cfg(windows)]
        let _ = mode;
    }

    fn is_shell_error(e: &anyhow::Error, expected: ShellError) -> bool {
        crate::error::is(e, &expected)
    }

    #[test]
    fn noop_apply_needs_no_approval() {
        for kind in [Kind::Bash, Kind::Zsh, Kind::PowerShell] {
            let (_t, base) = temp();
            let path = paths::join2(&base, "profile");
            write_mode(&path, b"# keep\n", 0o640);
            let (_d, dir_base) = temp();
            let mut dir = paths::join2(&dir_base, "shims");
            if kind != Kind::PowerShell {
                dir = posix_shims(&dir);
            }
            prepare(kind, Action::Setup, &path, &dir)
                .unwrap()
                .apply(Approval {
                    yes: true,
                    interactive: false,
                    confirm: None,
                })
                .unwrap();
            let installed = fs::read(&path).unwrap();
            let mut noops = vec![(
                prepare(kind, Action::Setup, &path, &dir).unwrap(),
                path.clone(),
            )];
            let (_u, unmanaged_base) = temp();
            let unmanaged = paths::join2(&unmanaged_base, "unmanaged");
            write_mode(&unmanaged, b"# no block\n", 0o640);
            noops.push((
                prepare(kind, Action::Remove, &unmanaged, "").unwrap(),
                unmanaged.clone(),
            ));
            let absent = paths::join2(&unmanaged_base, "absent");
            noops.push((
                prepare(kind, Action::Remove, &absent, "").unwrap(),
                absent.clone(),
            ));
            for (plan, target) in &noops {
                assert!(!plan.changed(), "{target}");
                plan.apply(Approval {
                    yes: false,
                    interactive: false,
                    confirm: None,
                })
                .unwrap();
                let mut called = false;
                let mut confirm = |_: &Plan| -> Result<bool> {
                    called = true;
                    Ok(false)
                };
                plan.apply(Approval {
                    yes: false,
                    interactive: true,
                    confirm: Some(&mut confirm),
                })
                .unwrap();
                assert!(!called, "no-op plan asked for confirmation: {target}");
            }
            assert_eq!(fs::read(&path).unwrap(), installed);
            assert_eq!(fs::read(&unmanaged).unwrap(), b"# no block\n");
            assert!(
                fs::symlink_metadata(&absent).is_err(),
                "absent file created"
            );
        }
    }

    #[test]
    fn noop_apply_still_detects_profile_change() {
        let (_t, base) = temp();
        let path = paths::join2(&base, "profile");
        write_mode(&path, b"# keep\n", 0o640);
        let plan = prepare(Kind::Bash, Action::Remove, &path, "").unwrap();
        assert!(!plan.changed());
        write_mode(&path, b"# edited after review\n", 0o640);
        let e = plan
            .apply(Approval {
                yes: false,
                interactive: false,
                confirm: None,
            })
            .unwrap_err();
        assert!(e.to_string().contains("changed after review"), "{e}");
    }

    #[test]
    fn round_trip_and_confirmation() {
        for kind in [Kind::Bash, Kind::Zsh, Kind::PowerShell] {
            for original in [
                "",
                "# keep\nexport A='한 글'\n",
                "# keep without newline",
                "# keep\r\n# CRLF\r\n",
            ] {
                let (_t, base) = temp();
                let path = paths::join2(&base, "profile with spaces");
                write_mode(&path, original.as_bytes(), 0o640);
                let (_d, dir_base) = temp();
                let mut dir = paths::join2(&dir_base, "shims '한 글' $x `cmd` [*]");
                if kind != Kind::PowerShell {
                    dir = posix_shims(&dir);
                }
                let p = prepare(kind, Action::Setup, &path, &dir).unwrap();
                let e = p
                    .apply(Approval {
                        yes: false,
                        interactive: false,
                        confirm: None,
                    })
                    .unwrap_err();
                assert!(is_shell_error(&e, ShellError::ConfirmationRequired), "{e}");
                let mut confirm = |shown: &Plan| -> Result<bool> {
                    assert_eq!(shown.path, path);
                    assert_eq!(shown.before, original.as_bytes());
                    Ok(false)
                };
                let e = p
                    .apply(Approval {
                        yes: false,
                        interactive: true,
                        confirm: Some(&mut confirm),
                    })
                    .unwrap_err();
                assert!(is_shell_error(&e, ShellError::Cancelled), "{e}");
                assert_eq!(
                    fs::read(&path).unwrap(),
                    original.as_bytes(),
                    "changed before approval"
                );
                p.apply(Approval {
                    yes: true,
                    interactive: false,
                    confirm: None,
                })
                .unwrap();
                let again = prepare(kind, Action::Setup, &path, &dir).unwrap();
                assert!(!again.changed(), "not idempotent");
                #[cfg(unix)]
                {
                    use std::os::unix::fs::PermissionsExt;
                    assert_eq!(
                        fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                        0o640
                    );
                }
                let remove = prepare(kind, Action::Remove, &path, "").unwrap();
                remove
                    .apply(Approval {
                        yes: true,
                        interactive: false,
                        confirm: None,
                    })
                    .unwrap();
                assert_eq!(
                    fs::read(&path).unwrap(),
                    original.as_bytes(),
                    "round trip changed bytes"
                );
                let remove = prepare(kind, Action::Remove, &path, "").unwrap();
                assert!(!remove.changed(), "remove not idempotent");
            }
        }
    }

    #[test]
    fn failure_preserves_original() {
        let (_t, base) = temp();
        let path = paths::join2(&base, "profile");
        let original = b"# unrelated\n";
        write_mode(&path, original, 0o600);
        let (_d, shims) = temp();
        let p = prepare(Kind::Bash, Action::Setup, &path, &posix_shims(&shims)).unwrap();
        let e = p
            .apply_with(&|_, _| Err(io::Error::other("simulated disk or rename failure")))
            .unwrap_err();
        assert!(
            e.to_string().contains("simulated disk or rename failure"),
            "{e}"
        );
        assert_eq!(fs::read(&path).unwrap(), original);
        assert_eq!(fs::read_dir(&base).unwrap().count(), 1, "leaked staging");
        write_mode(&path, b"concurrent user edit", 0o600);
        assert!(
            p.apply(Approval {
                yes: true,
                interactive: false,
                confirm: None
            })
            .is_err(),
            "overwrote concurrent edit"
        );
    }

    #[test]
    fn appeared_after_review() {
        let (_t, base) = temp();
        let path = paths::join2(&base, "profile");
        let p = prepare(Kind::Zsh, Action::Setup, &path, &posix_shims(&base)).unwrap();
        write_mode(&path, b"new", 0o600);
        let e = p
            .apply(Approval {
                yes: true,
                interactive: false,
                confirm: None,
            })
            .unwrap_err();
        assert_eq!(
            e.to_string(),
            "configuration appeared after review; prepare and confirm again"
        );
    }

    #[test]
    fn malformed_and_symlink() {
        for s in [
            format!("{BEGIN}\n"),
            format!("{END}\n"),
            format!("{BEGIN}\n{BEGIN}\n{END}\n"),
            format!("{BEGIN}\n{END}\n{BEGIN}\n{END}\n"),
        ] {
            let (_t, base) = temp();
            let path = paths::join2(&base, "profile");
            write_mode(&path, s.as_bytes(), 0o600);
            assert!(
                prepare(Kind::Bash, Action::Setup, &path, &base).is_err(),
                "accepted malformed markers {s:?}"
            );
        }
        #[cfg(unix)]
        {
            let (_t, dir) = temp();
            let path = paths::join2(&dir, "profile");
            let target = paths::join2(&dir, "target");
            fs::write(&target, "keep").unwrap();
            std::os::unix::fs::symlink(&target, &path).unwrap();
            assert!(
                prepare(Kind::Bash, Action::Setup, &path, &dir).is_err(),
                "followed profile symlink"
            );
        }
    }

    #[test]
    fn selection() {
        let (_t, home) = temp();
        let sel = |s: Selection| select_file(Kind::Bash, &s);
        assert_eq!(
            sel(Selection {
                home: home.clone(),
                ..Selection::default()
            })
            .unwrap(),
            paths::join2(&home, ".bashrc")
        );
        fs::write(paths::join2(&home, ".bashrc"), "").unwrap();
        fs::write(paths::join2(&home, ".bash_profile"), "").unwrap();
        assert!(
            sel(Selection {
                home: home.clone(),
                ..Selection::default()
            })
            .is_err(),
            "guessed ambiguous profile"
        );
        let explicit = paths::join2(&home, "my profile");
        assert_eq!(
            sel(Selection {
                home: home.clone(),
                file: explicit.clone(),
                ..Selection::default()
            })
            .unwrap(),
            explicit
        );
        let zsh = select_file(
            Kind::Zsh,
            &Selection {
                home: home.clone(),
                zdotdir: paths::join2(&home, "zsh"),
                ..Selection::default()
            },
        )
        .unwrap();
        assert_eq!(zsh, paths::join(&[home.as_str(), "zsh", ".zshrc"]));
        assert!(
            select_file(
                Kind::PowerShell,
                &Selection {
                    home: home.clone(),
                    ..Selection::default()
                }
            )
            .is_err()
        );
        let ps = select_file(
            Kind::PowerShell,
            &Selection {
                powershell_profile: explicit.clone(),
                ..Selection::default()
            },
        )
        .unwrap();
        assert_eq!(ps, explicit);
        assert!(select_file(Kind::Bash, &Selection::default()).is_err());
    }

    #[cfg(unix)]
    #[test]
    fn activation_in_native_shells() {
        for kind in [Kind::Bash, Kind::Zsh] {
            let Some(binary) = which(kind.as_str()) else {
                continue;
            };
            let (_t, base) = temp();
            let dir = paths::join2(&base, "한 글 ' $HOME `touch bad` [*]");
            let code = init(kind, &dir).unwrap();
            let initial = format!("/usr/bin::/bin:{dir}:{dir}");
            let mut cmd = std::process::Command::new(binary);
            if kind == Kind::Zsh {
                // User startup files can change PATH before this isolated test.
                cmd.arg("-f");
            }
            let out = cmd
                .arg("-c")
                .arg(format!("{code}{code}printf '%s' \"$PATH\""))
                .env("PATH", &initial)
                .current_dir(&base)
                .output()
                .unwrap();
            assert!(out.status.success(), "{kind}: {out:?}");
            assert_eq!(
                String::from_utf8(out.stdout).unwrap(),
                format!("{dir}:/usr/bin::/bin"),
                "{kind}"
            );
            assert!(
                fs::symlink_metadata(paths::join2(&base, "bad")).is_err(),
                "evaluated shim directory"
            );
        }
    }

    #[test]
    fn activation_in_powershell() {
        let Some(binary) = which("pwsh") else { return };
        let (_t, base) = temp();
        let dir = paths::join2(
            &base,
            "한 글 ' ‘ ’ ‚ ‛ ’; Write-Output pwned; ’ $HOME `cmd` [*]",
        );
        let code = init(Kind::PowerShell, &dir).unwrap();
        let initial = paths::join2(&base, "real");
        let out = std::process::Command::new(binary)
            .args(["-NoLogo", "-NoProfile", "-NonInteractive", "-Command"])
            .arg(format!(
                "$env:PATH = $env:CCOW_TEST_INITIAL_PATH; {code}{code}[Console]::Write($env:PATH)"
            ))
            .env("CCOW_TEST_INITIAL_PATH", &initial)
            .output()
            .unwrap();
        let stdout = String::from_utf8(out.stdout).unwrap();
        assert_eq!(stdout, format!("{dir}{}{initial}", paths::LIST_SEPARATOR));
        assert!(!stdout.contains("pwned\n"), "{stdout}");
    }

    #[test]
    fn powershell_quotes_typographic_single_quotes() {
        for quote in ['\'', '\u{2018}', '\u{2019}', '\u{201A}', '\u{201B}'] {
            let dir = format!("/x/O{quote}Neil");
            let code = init(Kind::PowerShell, &dir).unwrap();
            let doubled = format!("O{quote}{quote}Neil");
            // The directory is embedded in two literals, each with a doubled
            // quote.
            assert_eq!(code.matches(&doubled).count(), 2, "{quote:?}: {code}");
            // The ASCII apostrophe also delimits the literals, so only the
            // typographic quotes have a fixed total count.
            if quote != '\'' {
                assert_eq!(code.matches(quote).count(), 4, "{quote:?}: {code}");
            }
        }
        let injection = "/x/a\u{2019}; Write-Output pwned; \u{2019}b";
        let code = init(Kind::PowerShell, injection).unwrap();
        let literal = psquote(injection);
        assert_eq!(
            literal,
            "'/x/a\u{2019}\u{2019}; Write-Output pwned; \u{2019}\u{2019}b'"
        );
        assert_eq!(code.matches(&literal).count(), 2, "{code}");
        // Other characters, including the double quote and the grave accent,
        // stay as is.
        assert_eq!(psquote("a\"b`c"), "'a\"b`c'");
    }

    fn which(name: &str) -> Option<String> {
        let path = std::env::var("PATH").ok()?;
        paths::split_list(&path).into_iter().find_map(|dir| {
            for candidate in [name.to_string(), format!("{name}.exe")] {
                let p = paths::join2(&dir, &candidate);
                if fs::metadata(&p).is_ok_and(|m| m.is_file()) {
                    return Some(p);
                }
            }
            None
        })
    }

    #[test]
    fn init_matches_go_bytes() {
        let expected = String::new()
            + "_ccow_dir='/x/a'\"'\"'b'\n"
            + "_ccow_rest=${PATH-}\n_ccow_tail=\n_ccow_sep=\n"
            + "while :; do\n  case $_ccow_rest in\n    *:*) _ccow_part=${_ccow_rest%%:*}; \
               _ccow_rest=${_ccow_rest#*:}; _ccow_more=1 ;;\n    *) _ccow_part=$_ccow_rest; \
               _ccow_more=0 ;;\n  esac\n"
            + "  if [ \"$_ccow_part\" != \"$_ccow_dir\" ]; then\n    \
               _ccow_tail=\"$_ccow_tail$_ccow_sep$_ccow_part\"\n    _ccow_sep=:\n  fi\n  [ \
               \"$_ccow_more\" = 1 ] || break\ndone\n"
            + "PATH=\"$_ccow_dir${_ccow_sep:+:}$_ccow_tail\"\nexport PATH\nunset _ccow_dir \
               _ccow_rest _ccow_tail _ccow_sep _ccow_part _ccow_more\n";
        #[cfg(unix)]
        assert_eq!(init(Kind::Bash, "/x/a'b").unwrap(), expected);
        #[cfg(unix)]
        assert_eq!(
            init(Kind::PowerShell, "/x/a'b").unwrap(),
            "$env:PATH = '/x/a''b' + [IO.Path]::PathSeparator + (($env:PATH -split \
             [regex]::Escape([string][IO.Path]::PathSeparator) | Where-Object { $_ -ne '/x/a''b' \
             }) -join [IO.Path]::PathSeparator)\n"
        );
        let _ = expected;
    }

    #[test]
    fn init_validation() {
        let e = Kind::parse("fish").unwrap_err();
        assert_eq!(
            e.to_string(),
            "unsupported shell \"fish\"; use bash, zsh, or powershell"
        );
        for p in ["relative", "/tmp/a\nb", "/tmp/a\0b"] {
            assert!(init(Kind::Bash, p).is_err(), "accepted {p:?}");
        }
        let (_t, base) = temp();
        let code = init(Kind::PowerShell, &paths::join2(&base, "a'b")).unwrap();
        assert!(code.contains("a''b"), "{code}");
        #[cfg(unix)]
        assert_eq!(
            init(Kind::Bash, "/tmp/a:b").unwrap_err().to_string(),
            "shim directory cannot contain the PATH separator ':'"
        );
    }

    #[test]
    fn owned_change() {
        let (_t, base) = temp();
        let dir = posix_shims(&paths::join2(&base, "shims"));
        let code = init(Kind::Bash, &dir).unwrap();
        let owned =
            |marker: &str, nl: &str| format!("{marker}{nl}{}{END}{nl}", code.replace('\n', nl));
        let old_block = format!("{BEGIN}\n# stale activation\n{END}\n");
        let stale_joined = format!("{JOINED_BEGIN}\n# stale\n{END}\n");
        let cases: Vec<(&str, String, Action, String, String, bool, bool)> = vec![
            (
                "append to empty",
                String::new(),
                Action::Setup,
                String::new(),
                owned(BEGIN, "\n"),
                false,
                true,
            ),
            (
                "append to newline-terminated",
                "# keep\n".into(),
                Action::Setup,
                String::new(),
                owned(BEGIN, "\n"),
                false,
                true,
            ),
            (
                "append without final newline",
                "# keep".into(),
                Action::Setup,
                String::new(),
                owned(JOINED_BEGIN, "\n"),
                true,
                true,
            ),
            (
                "append to CRLF",
                "# keep\r\n".into(),
                Action::Setup,
                String::new(),
                owned(BEGIN, "\r\n"),
                false,
                true,
            ),
            (
                "append to CRLF without final newline",
                "# keep\r\n# tail".into(),
                Action::Setup,
                String::new(),
                owned(JOINED_BEGIN, "\r\n"),
                true,
                true,
            ),
            (
                "replace different block",
                format!("# keep\n{old_block}# after\n"),
                Action::Setup,
                old_block.clone(),
                owned(BEGIN, "\n"),
                false,
                true,
            ),
            (
                "replace joined block",
                format!("# keep\n{stale_joined}"),
                Action::Setup,
                stale_joined.clone(),
                owned(JOINED_BEGIN, "\n"),
                false,
                true,
            ),
            (
                "identical block",
                format!("# keep\n{}", owned(BEGIN, "\n")),
                Action::Setup,
                String::new(),
                String::new(),
                false,
                false,
            ),
            (
                "remove normal block",
                format!("# keep\n{}# after\n", owned(BEGIN, "\n")),
                Action::Remove,
                owned(BEGIN, "\n"),
                String::new(),
                false,
                true,
            ),
            (
                "remove joined block",
                format!("# keep\n{}", owned(JOINED_BEGIN, "\n")),
                Action::Remove,
                owned(JOINED_BEGIN, "\n"),
                String::new(),
                true,
                true,
            ),
            (
                "remove joined CRLF block",
                format!("# keep\r\n{}", owned(JOINED_BEGIN, "\r\n")),
                Action::Remove,
                owned(JOINED_BEGIN, "\r\n"),
                String::new(),
                true,
                true,
            ),
            (
                "remove joined block followed by content",
                format!("# keep\n{}# after\n", owned(JOINED_BEGIN, "\n")),
                Action::Remove,
                owned(JOINED_BEGIN, "\n"),
                String::new(),
                false,
                true,
            ),
            (
                "remove joined CRLF block followed by content",
                format!("# keep\r\n{}# after\r\n", owned(JOINED_BEGIN, "\r\n")),
                Action::Remove,
                owned(JOINED_BEGIN, "\r\n"),
                String::new(),
                false,
                true,
            ),
            (
                "remove joined block at file start",
                owned(JOINED_BEGIN, "\n"),
                Action::Remove,
                owned(JOINED_BEGIN, "\n"),
                String::new(),
                false,
                true,
            ),
            (
                "remove without block",
                "# keep\n".into(),
                Action::Remove,
                String::new(),
                String::new(),
                false,
                false,
            ),
            (
                "remove from empty",
                String::new(),
                Action::Remove,
                String::new(),
                String::new(),
                false,
                false,
            ),
        ];
        for (name, original, action, removed, added, separator, changed) in cases {
            let (_p, profile_dir) = temp();
            let path = paths::join2(&profile_dir, "profile");
            write_mode(&path, original.as_bytes(), 0o600);
            let p = prepare(Kind::Bash, action, &path, &dir).unwrap();
            assert_eq!(p.changed(), changed, "{name}");
            let (got_removed, mut got_added, got_separator) = p.owned_change();
            assert_eq!(String::from_utf8(got_removed).unwrap(), removed, "{name}");
            assert_eq!(
                String::from_utf8(got_added.clone()).unwrap(),
                added,
                "{name}"
            );
            assert_eq!(got_separator, separator, "{name}");
            if !got_added.is_empty() {
                got_added[0] = b'X';
                assert_ne!(p.owned_change().1[0], b'X', "exposed internal bytes");
            }
        }
    }

    #[test]
    fn remove_joined_block_keeps_following_content_on_its_own_line() {
        let (_d, dir_base) = temp();
        let dir = posix_shims(&dir_base);
        for (nl, original, appended) in [
            ("\n", "# keep", "# after\n"),
            ("\r\n", "# first\r\n# keep", "# after\r\n"),
        ] {
            let (_t, base) = temp();
            let path = paths::join2(&base, "profile");
            write_mode(&path, original.as_bytes(), 0o600);
            let approve = || Approval {
                yes: true,
                interactive: false,
                confirm: None,
            };
            prepare(Kind::Bash, Action::Setup, &path, &dir)
                .unwrap()
                .apply(approve())
                .unwrap();
            // Another tool appends content after the CacheCow block.
            let mut grown = fs::read(&path).unwrap();
            assert!(String::from_utf8_lossy(&grown).contains(JOINED_BEGIN));
            grown.extend_from_slice(appended.as_bytes());
            write_mode(&path, &grown, 0o600);

            let remove = prepare(Kind::Bash, Action::Remove, &path, &dir).unwrap();
            assert!(!remove.owned_change().2, "separator must stay");
            assert_eq!(
                String::from_utf8(remove.after.clone()).unwrap(),
                format!("{original}{nl}{appended}")
            );
            remove.apply(approve()).unwrap();
            assert_eq!(
                fs::read(&path).unwrap(),
                format!("{original}{nl}{appended}").as_bytes()
            );
        }
    }

    #[test]
    fn session_instructions() {
        let (_t, base) = temp();
        let path = paths::join2(&base, "profile");
        let setup = prepare(Kind::Zsh, Action::Setup, &path, &posix_shims(&base)).unwrap();
        assert_eq!(
            setup.current_session_instruction(),
            "This change applies to new shells. Apply `ccow shell init zsh` output in this shell \
             to activate now."
        );
        let remove = prepare(Kind::Zsh, Action::Remove, &path, "").unwrap();
        assert!(
            remove
                .current_session_instruction()
                .contains("remove the CacheCow shim directory from PATH")
        );
    }
}
