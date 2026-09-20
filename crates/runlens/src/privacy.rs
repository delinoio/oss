use std::{
    path::Path,
    sync::atomic::{AtomicBool, Ordering},
};

use regex::Regex;

use crate::{
    config::Redaction,
    error::{Error, Result},
};

pub struct Redactor {
    root: String,
    home: Option<String>,
    temporary: Vec<String>,
    rules: Vec<Regex>,
    secret_values: Vec<String>,
    sensitive_flag: Regex,
    token: Regex,
    indices: Vec<usize>,
    pub path_redacted: AtomicBool,
}
impl Redactor {
    pub fn new(root: &Path, temporary: &[&Path], config: &Redaction) -> Result<Self> {
        let sensitive_flag = Regex::new(r"(?i)(token|password|passwd|secret|credential|authorization|api[-_]?key|private[-_]?key)").expect("static expression");
        let secret_values = std::env::vars_os()
            // OS environments may contain non-Unicode entries. Only valid strings
            // can match the textual evidence that this redactor serializes.
            .filter_map(|(key, value)| Some((key.into_string().ok()?, value.into_string().ok()?)))
            .filter(|(key, _)| {
                sensitive_flag.is_match(key)
                    || config.environment_names.iter().any(|selected| {
                        if cfg!(windows) {
                            selected.eq_ignore_ascii_case(key)
                        } else {
                            selected == key
                        }
                    })
            })
            .map(|(_, value)| value)
            .filter(|v| !v.is_empty())
            .collect();
        Ok(Self { root: normalized(root), home: std::env::var_os(if cfg!(windows) {"USERPROFILE"} else {"HOME"}).map(|p| normalized(Path::new(&p))), temporary: temporary.iter().map(|p| normalized(p)).collect(), rules: config.patterns.iter().map(|p| Regex::new(p).map_err(|_| Error::input("invalid redaction pattern"))).collect::<Result<_>>()?, secret_values, sensitive_flag, token: Regex::new(r"(?i)(?:gh[pousr]_[A-Za-z0-9_]{8,}|github_pat_[A-Za-z0-9_]{8,}|AKIA[A-Z0-9]{16}|Bearer\s+[^\s]+|[a-z][a-z0-9+.-]*://[^\s/@]+:[^\s/@]+@)").expect("static expression"), indices: config.argument_indices.clone(), path_redacted: AtomicBool::new(false) })
    }

    pub fn text(&self, value: &str) -> String {
        let mut value = value.to_owned();
        for secret in &self.secret_values {
            value = value.replace(secret, "[redacted]");
        }
        value = self.token.replace_all(&value, "[redacted]").into_owned();
        for rule in &self.rules {
            value = rule.replace_all(&value, "[redacted]").into_owned();
        }
        for root in &self.temporary {
            value = replace_root(value, root, "${temporary}");
        }
        value = replace_root(value, &self.root, "${workspace}");
        if let Some(home) = &self.home
            && home != "/"
            && !home.is_empty()
        {
            value = replace_root(value, home, "${home}");
        }
        // Control characters must never control the user's terminal.
        value
            .chars()
            .flat_map(|c| {
                if c.is_control() {
                    c.escape_default().collect::<Vec<_>>()
                } else {
                    vec![c]
                }
            })
            .collect()
    }

    pub fn path(&self, path: &Path) -> String {
        let value = normalized(path);
        let redacted = self.text(&value);
        if redacted.contains("[redacted]") || path.to_str().is_none() {
            self.path_redacted.store(true, Ordering::Relaxed);
        }
        redacted
    }

    pub fn argv(&self, argv: &[String]) -> Vec<String> {
        let mut hide_next = false;
        argv.iter()
            .enumerate()
            .map(|(index, arg)| {
                let hide = std::mem::take(&mut hide_next) || self.indices.contains(&index);
                let sensitive = arg.starts_with('-') && self.sensitive_flag.is_match(arg);
                // A hidden argument can itself be a flag. Preserve its effect
                // on the following value before replacing the argument text.
                hide_next = sensitive && !arg.contains('=');
                if hide {
                    return "[redacted]".into();
                }
                if sensitive && let Some((key, _)) = arg.split_once('=') {
                    return format!("{}=[redacted]", self.text(key));
                }
                self.text(arg)
            })
            .collect()
    }
}
pub fn normalized(path: &Path) -> String {
    #[cfg(not(windows))]
    {
        path.to_string_lossy().into_owned()
    }
    #[cfg(windows)]
    {
        windows_path(&path.to_string_lossy())
    }
}
pub(crate) fn windows_path(path: &str) -> String {
    let text = path.replace('\\', "/");
    if let Some(unc) = text.strip_prefix("//?/UNC/") {
        format!("//{unc}")
    } else {
        text.strip_prefix("//?/").unwrap_or(&text).to_owned()
    }
}
fn replace_root(value: String, root: &str, placeholder: &str) -> String {
    #[cfg(windows)]
    let value = {
        // Canonical Windows argv may use extended drive or UNC notation. Mask
        // the complete prefix before handling ordinary path spellings.
        let extended = if let Some(unc) = root.strip_prefix("//") {
            format!("//?/UNC/{unc}")
        } else {
            format!("//?/{root}")
        };
        let value = replace_path_root(&value, &extended, placeholder);
        replace_path_root(&value, &extended.replace('/', "\\"), placeholder)
    };
    let value = replace_path_root(&value, root, placeholder);
    #[cfg(windows)]
    let value = replace_path_root(&value, &root.replace('/', "\\"), placeholder);
    value
}
fn replace_path_root(value: &str, root: &str, placeholder: &str) -> String {
    if root.is_empty() {
        return value.to_owned();
    }
    let separator = |ch| ch == '/' || cfg!(windows) && ch == '\\';
    let root_ends_in_separator = root.chars().next_back().is_some_and(separator);
    let mut result = String::with_capacity(value.len());
    let mut copied = 0;
    for (start, end) in root_matches(value, root) {
        let starts_path = start == 0
            || attached_option_prefix(&value[..start])
            || value[..start].chars().next_back().is_some_and(|ch| {
                ch.is_whitespace() || matches!(ch, '=' | '\'' | '"' | '(' | '[' | '{' | ',')
            });
        let ends_component = end == value.len()
            || root_ends_in_separator
            || value[end..].chars().next().is_some_and(separator);
        if starts_path && ends_component {
            result.push_str(&value[copied..start]);
            result.push_str(placeholder);
            if root_ends_in_separator && end < value.len() {
                result.push('/');
            }
            copied = end;
        }
    }
    result.push_str(&value[copied..]);
    result
}

fn attached_option_prefix(prefix: &str) -> bool {
    // Compilers commonly accept -I/path and -L/path without a delimiter.
    // Restrict this exception to an option token so embedded external paths
    // such as /elsewhere/workspace retain their original identity.
    let token = prefix
        .rsplit(|ch: char| ch.is_whitespace() || matches!(ch, '\'' | '"' | '(' | '[' | '{' | ','))
        .next()
        .unwrap_or_default();
    token.strip_prefix('-').is_some_and(|option| {
        !option.is_empty()
            && option
                .bytes()
                .all(|ch| ch.is_ascii_alphabetic() || ch == b'-')
    })
}

#[cfg(not(windows))]
fn root_matches(value: &str, root: &str) -> Vec<(usize, usize)> {
    value
        .match_indices(root)
        .map(|(start, _)| (start, start + root.len()))
        .collect()
}

#[cfg(windows)]
fn root_matches(value: &str, root: &str) -> Vec<(usize, usize)> {
    use windows_sys::Win32::Globalization::{FIND_FROMSTART, FindStringOrdinal};
    let source = value.encode_utf16().collect::<Vec<_>>();
    let pattern = root.encode_utf16().collect::<Vec<_>>();
    if pattern.is_empty() || source.len() > i32::MAX as usize || pattern.len() > i32::MAX as usize {
        return Vec::new();
    }
    // Windows ordinal casing preserves UTF-16 lengths. Map native offsets back
    // to UTF-8 boundaries instead of assuming Unicode folds preserve byte sizes.
    let mut boundaries = vec![(0, 0)];
    let mut units = 0;
    for (offset, ch) in value.char_indices() {
        units += ch.len_utf16();
        boundaries.push((units, offset + ch.len_utf8()));
    }
    let byte_offset = |units| {
        boundaries
            .binary_search_by_key(&units, |p| p.0)
            .ok()
            .map(|i| boundaries[i].1)
    };
    let mut matches = Vec::new();
    let mut offset = 0;
    while offset < source.len() {
        // SAFETY: both UTF-16 buffers have the explicit supplied lengths and
        // remain alive for the call. No locale or null terminator is involved.
        let found = unsafe {
            FindStringOrdinal(
                FIND_FROMSTART,
                source[offset..].as_ptr(),
                (source.len() - offset) as i32,
                pattern.as_ptr(),
                pattern.len() as i32,
                1,
            )
        };
        if found < 0 {
            break;
        }
        let start = offset + found as usize;
        offset = start + pattern.len();
        if let (Some(start), Some(end)) = (byte_offset(start), byte_offset(offset)) {
            matches.push((start, end));
        }
    }
    matches
}

pub(crate) fn strip_path_root<'a>(value: &'a str, root: &str) -> Option<&'a str> {
    if root.is_empty() {
        return None;
    }
    root_matches(value, root)
        .into_iter()
        .find_map(|(start, end)| {
            (start == 0
                && (end == value.len() || root.ends_with('/') || value[end..].starts_with('/')))
            .then_some(&value[end..])
        })
}

pub(crate) fn within_root(path: &Path, root: &Path) -> bool {
    strip_path_root(&normalized(path), &normalized(root)).is_some()
}

/// Normalize configured identities without resolving or executing an argv
/// program.
pub fn command_identities(
    config: &crate::config::Config,
    root: &Path,
    temporary: &[&Path],
) -> crate::error::Result<std::collections::BTreeMap<String, crate::model::Identity>> {
    let redactor = Redactor::new(root, temporary, &config.redaction)?;
    Ok(config
        .commands
        .iter()
        .map(|(name, command)| {
            let cwd = root.join(&command.cwd);
            let cwd = cwd
                .canonicalize()
                .unwrap_or_else(|_| cwd.components().collect());
            (
                redactor.text(name),
                crate::model::Identity {
                    name: Some(redactor.text(name)),
                    argv: redactor.argv(&command.argv),
                    cwd: redactor.path(&cwd),
                },
            )
        })
        .collect())
}

/// Query equality plus whether the analyst lacks the source OS uppercase table.
pub(crate) fn windows_query_eq(left: &str, right: &str) -> (bool, bool) {
    if left == right || (left.is_ascii() && right.is_ascii()) {
        return (left.eq_ignore_ascii_case(right), false);
    }
    #[cfg(windows)]
    {
        let left: Vec<u16> = left.encode_utf16().collect();
        let right: Vec<u16> = right.encode_utf16().collect();
        // SAFETY: both buffers are live with their explicit bounded lengths.
        let result = unsafe {
            windows_sys::Win32::Globalization::CompareStringOrdinal(
                left.as_ptr(),
                left.len() as i32,
                right.as_ptr(),
                right.len() as i32,
                1,
            )
        };
        (result == 2, result == 0)
    }
    #[cfg(not(windows))]
    {
        // Windows ordinal comparison uses an OS-owned uppercase table, not full
        // Unicode case folding (which would equate e.g. sharp-s with SS). Offer
        // simple-uppercase candidates offline, but never claim table parity.
        // Remove this uncertainty only when reports bind a portable case table.
        let upper = |c: char| {
            let mut chars = c.to_uppercase();
            let first = chars.next().unwrap();
            if chars.next().is_none() { first } else { c }
        };
        (left.chars().map(upper).eq(right.chars().map(upper)), true)
    }
}
