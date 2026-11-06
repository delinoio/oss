// SPDX-License-Identifier: Apache-2.0
//! Lexical path operations with Go `path/filepath` semantics, plus small
//! filesystem helpers.
//!
//! CacheCow stores and exchanges paths as UTF-8 strings. Persisted records,
//! digests and lease comparisons depend on exact lexical behavior, so these
//! helpers reproduce `filepath.Clean`, `Rel`, `Dir`, `Base`, `Join` and `Abs`
//! rather than relying on `std::path` normalization.

use std::{fs, io, path::Path};

use anyhow::{Result, anyhow};

#[cfg(windows)]
pub const SEPARATOR: char = '\\';
#[cfg(not(windows))]
pub const SEPARATOR: char = '/';

#[cfg(windows)]
pub const LIST_SEPARATOR: char = ';';
#[cfg(not(windows))]
pub const LIST_SEPARATOR: char = ':';

pub fn is_separator(c: char) -> bool {
    #[cfg(windows)]
    {
        c == '\\' || c == '/'
    }
    #[cfg(not(windows))]
    {
        c == '/'
    }
}

fn same_word(a: &str, b: &str) -> bool {
    #[cfg(windows)]
    {
        a.eq_ignore_ascii_case(b) || a.to_lowercase() == b.to_lowercase()
    }
    #[cfg(not(windows))]
    {
        a == b
    }
}

/// Length of the leading volume name (`C:` or `\\host\share`) on Windows.
pub fn volume_name_len(path: &str) -> usize {
    #[cfg(windows)]
    {
        let b = path.as_bytes();
        if b.len() >= 2 && b[1] == b':' && b[0].is_ascii_alphabetic() {
            return 2;
        }
        let sep = |c: u8| c == b'\\' || c == b'/';
        if b.len() >= 5 && sep(b[0]) && sep(b[1]) && !sep(b[2]) && b[2] != b'.' {
            // UNC: \\host\share
            let mut n = 3;
            while n < b.len() && !sep(b[n]) {
                n += 1;
            }
            if n < b.len() {
                n += 1;
                let share = n;
                while n < b.len() && !sep(b[n]) {
                    n += 1;
                }
                if n > share {
                    return n;
                }
            }
        }
        0
    }
    #[cfg(not(windows))]
    {
        let _ = path;
        0
    }
}

pub fn volume_name(path: &str) -> &str {
    &path[..volume_name_len(path)]
}

/// Go `filepath.Clean`.
pub fn clean(path: &str) -> String {
    let original = path;
    let vol_len = volume_name_len(path);
    let vol = &path[..vol_len];
    let path = &path[vol_len..];
    if path.is_empty() {
        if vol_len > 1 && !original.ends_with(':') {
            // UNC root without a trailing separator.
            return from_slash(original);
        }
        return format!("{original}.");
    }
    let rooted = path.starts_with(is_separator);
    let mut parts: Vec<&str> = Vec::new();
    for part in path.split(is_separator) {
        match part {
            "" | "." => {}
            ".." => {
                if parts.last().is_some_and(|p| *p != "..") {
                    parts.pop();
                } else if !rooted {
                    parts.push("..");
                }
            }
            p => parts.push(p),
        }
    }
    let sep = SEPARATOR.to_string();
    let mut out = String::from(vol);
    #[cfg(windows)]
    {
        out = out.replace('/', "\\");
    }
    if rooted {
        out.push(SEPARATOR);
    }
    out.push_str(&parts.join(&sep));
    if out.is_empty() {
        return ".".to_string();
    }
    if out == vol && !rooted {
        out.push('.');
    }
    out
}

/// Converts `/` to the platform separator.
pub fn from_slash(path: &str) -> String {
    if SEPARATOR == '/' {
        path.to_string()
    } else {
        path.replace('/', "\\")
    }
}

/// Converts the platform separator to `/`.
pub fn to_slash(path: &str) -> String {
    if SEPARATOR == '/' {
        path.to_string()
    } else {
        path.replace('\\', "/")
    }
}

/// Go `filepath.IsAbs`.
pub fn is_abs(path: &str) -> bool {
    #[cfg(windows)]
    {
        let l = volume_name_len(path);
        if l == 0 {
            return false;
        }
        if l > 2 {
            return true; // UNC
        }
        path[l..].starts_with(is_separator)
    }
    #[cfg(not(windows))]
    {
        path.starts_with('/')
    }
}

/// Go `filepath.Join`: joins non-empty elements and cleans the result.
pub fn join<S: AsRef<str>>(elements: &[S]) -> String {
    let parts: Vec<&str> = elements
        .iter()
        .map(AsRef::as_ref)
        .filter(|s| !s.is_empty())
        .collect();
    if parts.is_empty() {
        return String::new();
    }
    clean(&parts.join(&SEPARATOR.to_string()))
}

/// Two-element [`join`].
pub fn join2(a: &str, b: &str) -> String {
    join(&[a, b])
}

/// Go `filepath.Dir`.
pub fn dir(path: &str) -> String {
    let vol_len = volume_name_len(path);
    let vol = &path[..vol_len];
    let rest = &path[vol_len..];
    let i = rest.rfind(is_separator).map_or(0, |i| i + 1);
    let d = clean(&rest[..i]);
    if d == "." && vol_len > 2 {
        return vol.to_string();
    }
    format!("{vol}{d}")
}

/// Go `filepath.Base`.
pub fn base(path: &str) -> String {
    if path.is_empty() {
        return ".".to_string();
    }
    let mut p = path.trim_end_matches(is_separator);
    if p.is_empty() {
        return SEPARATOR.to_string();
    }
    p = &p[volume_name_len(p)..];
    match p.rfind(is_separator) {
        Some(i) => p[i + 1..].to_string(),
        None => p.to_string(),
    }
}

/// Go `filepath.Ext`.
pub fn ext(path: &str) -> &str {
    for (i, c) in path.char_indices().rev() {
        if is_separator(c) {
            break;
        }
        if c == '.' {
            return &path[i..];
        }
    }
    ""
}

/// Go `filepath.Rel`. Returns `None` where Go returns an error.
pub fn rel(basepath: &str, targpath: &str) -> Option<String> {
    let base_vol = volume_name(basepath).to_string();
    let targ_vol = volume_name(targpath).to_string();
    let base_clean = clean(basepath);
    let targ_clean = clean(targpath);
    if same_word(&targ_clean, &base_clean) {
        return Some(".".to_string());
    }
    let mut base = &base_clean[base_vol.len().min(base_clean.len())..];
    let targ = &targ_clean[targ_vol.len().min(targ_clean.len())..];
    let sep_string = SEPARATOR.to_string();
    if base == "." {
        base = "";
    } else if base.is_empty() && base_vol.len() > 2 {
        base = &sep_string;
    }
    let base_slashed = base.starts_with(SEPARATOR);
    let targ_slashed = targ.starts_with(SEPARATOR);
    if base_slashed != targ_slashed || !same_word(&base_vol, &targ_vol) {
        return None;
    }
    let b = base.as_bytes();
    let t = targ.as_bytes();
    let sep = SEPARATOR as u8;
    let (bl, tl) = (b.len(), t.len());
    let (mut b0, mut bi, mut t0, mut ti) = (0usize, 0usize, 0usize, 0usize);
    loop {
        while bi < bl && b[bi] != sep {
            bi += 1;
        }
        while ti < tl && t[ti] != sep {
            ti += 1;
        }
        if !same_word(&targ[t0..ti], &base[b0..bi]) {
            break;
        }
        if bi < bl {
            bi += 1;
        }
        if ti < tl {
            ti += 1;
        }
        b0 = bi;
        t0 = ti;
        if b0 >= bl && t0 >= tl {
            break;
        }
    }
    if &base[b0..bi] == ".." {
        return None;
    }
    if b0 != bl {
        let seps = base[b0..bl].matches(SEPARATOR).count();
        let mut out = String::from("..");
        for _ in 0..seps {
            out.push(SEPARATOR);
            out.push_str("..");
        }
        if t0 != tl {
            out.push(SEPARATOR);
            out.push_str(&targ[t0..]);
        }
        return Some(out);
    }
    Some(targ[t0..].to_string())
}

/// Lexical containment used throughout CacheCow: `child` is `parent` or a
/// descendant of it. Mirrors `filepath.Rel` plus the `..` prefix check.
pub fn lexically_within(parent: &str, child: &str) -> bool {
    match rel(parent, child) {
        Some(r) => r != ".." && !r.starts_with(&format!("..{SEPARATOR}")) && !is_abs(&r),
        None => false,
    }
}

/// Go `filepath.SplitList`.
pub fn split_list(list: &str) -> Vec<String> {
    split_list_raw(list)
        .into_iter()
        .map(|(_, dir)| dir)
        .collect()
}

/// Go `filepath.SplitList` that also returns each entry's original spelling
/// as `(raw, dir)`. They differ only on Windows, where `dir` has the quotes
/// removed and `raw` keeps them.
pub fn split_list_raw(list: &str) -> Vec<(String, String)> {
    if list.is_empty() {
        return Vec::new();
    }
    #[cfg(windows)]
    {
        let mut out = Vec::new();
        let (mut raw, mut dir) = (String::new(), String::new());
        let mut quoted = false;
        for c in list.chars() {
            match c {
                ';' if !quoted => out.push((std::mem::take(&mut raw), std::mem::take(&mut dir))),
                c => {
                    if c == '"' {
                        quoted = !quoted;
                    } else {
                        dir.push(c);
                    }
                    raw.push(c);
                }
            }
        }
        out.push((raw, dir));
        out
    }
    #[cfg(not(windows))]
    {
        list.split(':')
            .map(|dir| (dir.to_string(), dir.to_string()))
            .collect()
    }
}

/// Current working directory as a UTF-8 string.
pub fn current_dir() -> Result<String> {
    path_string(&std::env::current_dir()?)
}

/// Converts a filesystem path to a UTF-8 string.
pub fn path_string(path: &Path) -> Result<String> {
    path.to_str().map(str::to_string).ok_or_else(|| {
        anyhow!(
            "path {} is not valid UTF-8; use a UTF-8 path",
            path.display()
        )
    })
}

/// Go `filepath.Abs`.
pub fn abs(path: &str) -> Result<String> {
    if is_abs(path) {
        return Ok(clean(path));
    }
    #[cfg(windows)]
    {
        let full = std::path::absolute(path)?;
        Ok(clean(&path_string(&full)?))
    }
    #[cfg(not(windows))]
    {
        Ok(join2(&current_dir()?, path))
    }
}

/// Go `filepath.EvalSymlinks` for an absolute or relative path. The result is
/// absolute when the input is absolute.
pub fn eval_symlinks(path: &str) -> io::Result<String> {
    #[cfg(windows)]
    let resolved = dunce::canonicalize(path)?;
    #[cfg(not(windows))]
    let resolved = fs::canonicalize(path)?;
    resolved.to_str().map(str::to_string).ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            "resolved path is not valid UTF-8",
        )
    })
}

/// Stable identity of a filesystem object, like Go `os.SameFile`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct FileId {
    pub device: u64,
    pub index: u64,
}

#[cfg(unix)]
fn id_from_metadata(m: &fs::Metadata) -> FileId {
    use std::os::unix::fs::MetadataExt;
    FileId {
        device: m.dev(),
        index: m.ino(),
    }
}

/// Identity of the object at `path`. `follow` selects stat (true) or lstat.
pub fn file_id(path: &str, follow: bool) -> io::Result<FileId> {
    #[cfg(unix)]
    {
        let m = if follow {
            fs::metadata(path)?
        } else {
            fs::symlink_metadata(path)?
        };
        Ok(id_from_metadata(&m))
    }
    #[cfg(windows)]
    {
        windows_id::by_path(path, follow)
    }
}

/// Identity of an open file.
pub fn file_id_of(file: &fs::File) -> io::Result<FileId> {
    #[cfg(unix)]
    {
        Ok(id_from_metadata(&file.metadata()?))
    }
    #[cfg(windows)]
    {
        use std::os::windows::io::AsRawHandle;
        windows_id::by_handle(file.as_raw_handle())
    }
}

/// Go `os.SameFile` on two paths (following symlinks). Errors compare unequal.
pub fn same_file(a: &str, b: &str) -> bool {
    matches!((file_id(a, true), file_id(b, true)), (Ok(x), Ok(y)) if x == y)
}

#[cfg(windows)]
mod windows_id {
    use std::{io, os::windows::ffi::OsStrExt};

    use windows_sys::Win32::{
        Foundation::{CloseHandle, HANDLE, INVALID_HANDLE_VALUE},
        Storage::FileSystem::{
            BY_HANDLE_FILE_INFORMATION, CreateFileW, FILE_FLAG_BACKUP_SEMANTICS,
            FILE_FLAG_OPEN_REPARSE_POINT, FILE_SHARE_DELETE, FILE_SHARE_READ, FILE_SHARE_WRITE,
            GetFileInformationByHandle, OPEN_EXISTING,
        },
    };

    use super::FileId;

    pub fn by_handle(handle: std::os::windows::io::RawHandle) -> io::Result<FileId> {
        let mut info: BY_HANDLE_FILE_INFORMATION = unsafe { std::mem::zeroed() };
        // SAFETY: `handle` is a live handle owned by the caller and `info` is a
        // properly sized output buffer.
        if unsafe { GetFileInformationByHandle(handle as HANDLE, &mut info) } == 0 {
            return Err(io::Error::last_os_error());
        }
        Ok(FileId {
            device: u64::from(info.dwVolumeSerialNumber),
            index: (u64::from(info.nFileIndexHigh) << 32) | u64::from(info.nFileIndexLow),
        })
    }

    pub fn by_path(path: &str, follow: bool) -> io::Result<FileId> {
        let wide: Vec<u16> = std::ffi::OsStr::new(path)
            .encode_wide()
            .chain(Some(0))
            .collect();
        let mut flags = FILE_FLAG_BACKUP_SEMANTICS;
        if !follow {
            flags |= FILE_FLAG_OPEN_REPARSE_POINT;
        }
        // SAFETY: `wide` is NUL-terminated; zero access only queries metadata.
        let handle = unsafe {
            CreateFileW(
                wide.as_ptr(),
                0,
                FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                std::ptr::null(),
                OPEN_EXISTING,
                flags,
                std::ptr::null_mut(),
            )
        };
        if handle == INVALID_HANDLE_VALUE {
            return Err(io::Error::last_os_error());
        }
        let result = by_handle(handle as std::os::windows::io::RawHandle);
        // SAFETY: `handle` was opened above and is closed exactly once.
        unsafe { CloseHandle(handle) };
        result
    }
}

/// Go `os.FileMode` bits for metadata, used only for change detection.
pub fn go_file_mode(m: &fs::Metadata) -> u32 {
    const MODE_DIR: u32 = 1 << 31;
    const MODE_SYMLINK: u32 = 1 << 27;
    const MODE_IRREGULAR: u32 = 1 << 19;
    let ft = m.file_type();
    let mut mode = if ft.is_dir() {
        MODE_DIR
    } else if ft.is_symlink() {
        MODE_SYMLINK
    } else if ft.is_file() {
        0
    } else {
        MODE_IRREGULAR
    };
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        mode |= m.permissions().mode() & 0o777;
    }
    #[cfg(windows)]
    {
        mode |= if m.permissions().readonly() {
            0o444
        } else {
            0o666
        };
        if ft.is_dir() {
            mode |= 0o111;
        }
    }
    mode
}

/// Go `fs.DirEntry.Type()` bits for a file type.
pub fn go_type_bits(ft: fs::FileType) -> u32 {
    if ft.is_dir() {
        1 << 31
    } else if ft.is_symlink() {
        1 << 27
    } else if ft.is_file() {
        0
    } else {
        1 << 19
    }
}

/// Result of a [`walk_dir`] callback.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Walk {
    Continue,
    /// On a directory, skips its contents. On a file, skips the remaining
    /// entries of the containing directory, as Go's `filepath.SkipDir` does.
    SkipDir,
}

/// One entry visited by [`walk_dir`].
pub struct WalkEntry {
    pub path: String,
    pub name: String,
    pub file_type: fs::FileType,
}

impl WalkEntry {
    pub fn is_dir(&self) -> bool {
        self.file_type.is_dir()
    }
}

/// Go `filepath.WalkDir`: visits `root` (lstat) and its descendants in lexical
/// order without following symlinks. Errors stop the walk.
pub fn walk_dir(root: &str, f: &mut dyn FnMut(&WalkEntry) -> Result<Walk>) -> Result<()> {
    let meta = fs::symlink_metadata(root)?;
    let entry = WalkEntry {
        path: root.to_string(),
        name: base(root),
        file_type: meta.file_type(),
    };
    walk_entry(&entry, f).map(|_| ())
}

fn walk_entry(entry: &WalkEntry, f: &mut dyn FnMut(&WalkEntry) -> Result<Walk>) -> Result<Walk> {
    let decision = f(entry)?;
    if !entry.is_dir() || decision == Walk::SkipDir {
        return Ok(decision);
    }
    let mut children: Vec<(String, fs::FileType)> = Vec::new();
    for child in fs::read_dir(&entry.path)? {
        let child = child?;
        let name = child
            .file_name()
            .into_string()
            .map_err(|n| anyhow!("file name {} is not valid UTF-8", n.to_string_lossy()))?;
        children.push((name, child.file_type()?));
    }
    children.sort_by(|a, b| a.0.cmp(&b.0));
    for (name, file_type) in children {
        let child = WalkEntry {
            path: join2(&entry.path, &name),
            name,
            file_type,
        };
        if walk_entry(&child, f)? == Walk::SkipDir && !child.is_dir() {
            break;
        }
    }
    Ok(Walk::Continue)
}

fn env_nonempty(key: &str) -> Option<String> {
    std::env::var(key).ok().filter(|v| !v.is_empty())
}

/// Go `os.UserHomeDir`.
pub fn user_home_dir() -> Result<String> {
    #[cfg(windows)]
    let key = "USERPROFILE";
    #[cfg(not(windows))]
    let key = "HOME";
    env_nonempty(key).ok_or_else(|| anyhow!("${key} is not defined"))
}

/// Go `os.UserConfigDir`.
pub fn user_config_dir() -> Result<String> {
    #[cfg(windows)]
    {
        env_nonempty("AppData").ok_or_else(|| anyhow!("%AppData% is not defined"))
    }
    #[cfg(target_os = "macos")]
    {
        Ok(join2(&user_home_dir()?, "Library/Application Support"))
    }
    #[cfg(all(unix, not(target_os = "macos")))]
    {
        if let Some(v) = env_nonempty("XDG_CONFIG_HOME") {
            if !is_abs(&v) {
                return Err(anyhow!("path in $XDG_CONFIG_HOME is relative"));
            }
            return Ok(v);
        }
        Ok(join2(&user_home_dir()?, ".config"))
    }
}

/// Go `os.UserCacheDir`.
pub fn user_cache_dir() -> Result<String> {
    #[cfg(windows)]
    {
        env_nonempty("LocalAppData").ok_or_else(|| anyhow!("%LocalAppData% is not defined"))
    }
    #[cfg(target_os = "macos")]
    {
        Ok(join2(&user_home_dir()?, "Library/Caches"))
    }
    #[cfg(all(unix, not(target_os = "macos")))]
    {
        if let Some(v) = env_nonempty("XDG_CACHE_HOME") {
            if !is_abs(&v) {
                return Err(anyhow!("path in $XDG_CACHE_HOME is relative"));
            }
            return Ok(v);
        }
        Ok(join2(&user_home_dir()?, ".cache"))
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    #[test]
    fn clean_matches_go() {
        for (input, want) in [
            ("", "."),
            ("abc", "abc"),
            ("abc/def/", "abc/def"),
            ("a//b", "a/b"),
            ("/../a", "/a"),
            ("../../a", "../../a"),
            ("a/../..", ".."),
            ("/", "/"),
            ("a/./b/.", "a/b"),
            ("abc/def/../ghi/../jkl", "abc/jkl"),
        ] {
            assert_eq!(clean(input), want, "{input}");
        }
    }

    #[test]
    fn rel_matches_go() {
        for (base, targ, want) in [
            ("a/b", "a/b", Some(".")),
            ("a/b", "a/b/c", Some("c")),
            ("a/b/c", "a/b", Some("..")),
            ("a/b", "a/c", Some("../c")),
            ("/a/b", "/a/b/c/d", Some("c/d")),
            ("/a", "/b/c", Some("../b/c")),
            ("/", "/a", Some("a")),
            ("/a", "b", None),
            ("..", "a", None),
            ("/ab", "/a", Some("../a")),
        ] {
            assert_eq!(rel(base, targ).as_deref(), want, "{base} {targ}");
        }
        assert!(lexically_within("/a", "/a/b"));
        assert!(lexically_within("/a", "/a"));
        assert!(!lexically_within("/a", "/ab"));
        assert!(!lexically_within("/a/b", "/a"));
    }

    #[test]
    fn dir_base_join() {
        assert_eq!(dir("/a/b"), "/a");
        assert_eq!(dir("/a"), "/");
        assert_eq!(dir("a"), ".");
        assert_eq!(dir("/"), "/");
        assert_eq!(base("/a/b/"), "b");
        assert_eq!(base("/"), "/");
        assert_eq!(join(&["/a", "", "b/../c"]), "/a/c");
        assert_eq!(ext("x/a.tar.gz"), ".gz");
        assert_eq!(split_list(""), Vec::<String>::new());
        assert_eq!(split_list("a::b"), vec!["a", "", "b"]);
    }
}
