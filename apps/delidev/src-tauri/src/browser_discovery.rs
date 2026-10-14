// SPDX-License-Identifier: Apache-2.0
//! Native discovery of installed HTTP(S) handlers. Each platform scanner reads
//! only operating-system registration data, never renderer-supplied paths. The
//! renderer sees `BrowserEntry` (stable identity plus display name); launch
//! specifications and local executable paths stay private to native code.
use std::{
    fs,
    io::Read,
    path::{Path, PathBuf},
    process::{Command, Stdio},
    time::{Duration, Instant},
};

use serde::Serialize;

use crate::{
    NativeFailure,
    browser_preferences::{BrowserChoice, BrowserChoices},
};

const MAX_BROWSERS: usize = 64;
const FILE_LIMIT: u64 = 1 << 20;
const IDENTITY_LIMIT: usize = 128;
const NAME_LIMIT: usize = 80;
#[allow(dead_code)]
const COMMAND_OUTPUT_LIMIT: u64 = 256 << 10;
#[allow(dead_code)]
const COMMAND_TIMEOUT: Duration = Duration::from_secs(5);

/// Renderer-visible browser registration. The identity is the stable native
/// registration key (macOS bundle identifier, Linux desktop-file ID or Windows
/// StartMenuInternet key), not a display name or path.
#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct BrowserEntry {
    pub identity: String,
    pub name: String,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum LaunchArg {
    Text(String),
    Url,
    /// A native launcher argument that embeds the URL, such as `--app=%u`.
    Embedded {
        prefix: String,
        suffix: String,
    },
}

/// Argv-only launch recipe. The URL is always one separate or embedded value;
/// no shell string is ever built.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct LaunchSpec {
    pub program: String,
    pub args: Vec<LaunchArg>,
}

impl LaunchSpec {
    pub fn argv(&self, url: &str) -> Vec<String> {
        self.args
            .iter()
            .map(|argument| match argument {
                LaunchArg::Text(text) => text.clone(),
                LaunchArg::Url => url.to_owned(),
                LaunchArg::Embedded { prefix, suffix } => format!("{prefix}{url}{suffix}"),
            })
            .collect()
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct InstalledBrowser {
    pub entry: BrowserEntry,
    pub launch: LaunchSpec,
}

/// Syntax bound for a persisted or renderer-supplied browser identity.
/// Passing this check grants nothing: dispatch resolves it only through a
/// fresh native inventory.
pub fn valid_identity(identity: &str) -> bool {
    !identity.is_empty()
        && identity.len() <= IDENTITY_LIMIT
        && !identity.starts_with(' ')
        && !identity.ends_with(' ')
        && identity
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"._-+ ()".contains(&byte))
}

fn display_name(raw: &str, fallback: &str) -> String {
    let cleaned: String = raw
        .chars()
        .filter(|character| !character.is_control())
        .collect();
    let cleaned = cleaned.trim();
    let chosen = if cleaned.is_empty() {
        fallback
    } else {
        cleaned
    };
    chosen.chars().take(NAME_LIMIT).collect()
}

fn finish(mut browsers: Vec<InstalledBrowser>) -> Vec<InstalledBrowser> {
    let mut seen = std::collections::BTreeSet::new();
    browsers.retain(|browser| seen.insert(browser.entry.identity.clone()));
    browsers.sort_by(|left, right| {
        left.entry
            .name
            .to_lowercase()
            .cmp(&right.entry.name.to_lowercase())
            .then_with(|| left.entry.identity.cmp(&right.entry.identity))
    });
    browsers.truncate(MAX_BROWSERS);
    browsers
}

fn read_bounded_file(path: &Path) -> Option<Vec<u8>> {
    let metadata = fs::metadata(path).ok()?;
    if !metadata.is_file() || metadata.len() > FILE_LIMIT {
        return None;
    }
    let mut bytes = Vec::new();
    fs::File::open(path)
        .ok()?
        .take(FILE_LIMIT + 1)
        .read_to_end(&mut bytes)
        .ok()?;
    (bytes.len() as u64 <= FILE_LIMIT).then_some(bytes)
}

/// Enumerates the HTTP(S) handlers registered with the operating system.
pub fn discover() -> Result<Vec<InstalledBrowser>, NativeFailure> {
    #[cfg(target_os = "macos")]
    return scan_macos(&macos_roots());
    #[cfg(target_os = "linux")]
    return scan_linux(&linux_roots());
    #[cfg(windows)]
    return discover_windows(&RegExe);
    #[cfg(not(any(target_os = "macos", target_os = "linux", windows)))]
    Err(NativeFailure::BrowserDiscoveryFailed)
}

/// Renderer-visible inventory.
pub fn entries() -> Result<Vec<BrowserEntry>, NativeFailure> {
    Ok(discover()?
        .into_iter()
        .map(|browser| browser.entry)
        .collect())
}

/// Every identity that differs from the currently saved choice for its class
/// must be registered now. Unchanged saved identities may be unavailable and
/// stay visible and selectable for explicit replacement only.
pub fn require_installed_changes(
    current: &BrowserChoices,
    next: &BrowserChoices,
) -> Result<(), NativeFailure> {
    require_installed_changes_with(current, next, &discover)
}

fn require_installed_changes_with(
    current: &BrowserChoices,
    next: &BrowserChoices,
    inventory: &dyn Fn() -> Result<Vec<InstalledBrowser>, NativeFailure>,
) -> Result<(), NativeFailure> {
    let mut installed = None;
    for (previous, wanted) in [
        (&current.general, &next.general),
        (&current.local, &next.local),
    ] {
        let BrowserChoice::Installed { identity } = wanted else {
            continue;
        };
        if !valid_identity(identity) {
            return Err(NativeFailure::InvalidInput);
        }
        if previous == wanted {
            continue;
        }
        if installed.is_none() {
            installed = Some(inventory()?);
        }
        if !installed
            .iter()
            .flatten()
            .any(|browser| &browser.entry.identity == identity)
        {
            return Err(NativeFailure::BrowserUnavailable);
        }
    }
    Ok(())
}

/// Resolves a saved identity through a fresh inventory.
pub fn resolve(identity: &str) -> Result<InstalledBrowser, NativeFailure> {
    if !valid_identity(identity) {
        return Err(NativeFailure::InvalidInput);
    }
    discover()?
        .into_iter()
        .find(|browser| browser.entry.identity == identity)
        .ok_or(NativeFailure::BrowserUnavailable)
}

// ---- macOS -----------------------------------------------------------------

#[allow(dead_code)]
fn macos_roots() -> Vec<PathBuf> {
    let mut roots = vec![
        PathBuf::from("/Applications"),
        PathBuf::from("/Applications/Utilities"),
        PathBuf::from("/System/Applications"),
        PathBuf::from("/System/Cryptexes/App/System/Applications"),
    ];
    if let Some(home) = std::env::var_os("HOME").map(PathBuf::from)
        && home.is_absolute()
    {
        roots.push(home.join("Applications"));
    }
    roots
}

#[allow(dead_code)]
pub(crate) fn scan_macos(roots: &[PathBuf]) -> Result<Vec<InstalledBrowser>, NativeFailure> {
    let mut readable = false;
    let mut found = Vec::new();
    for root in roots {
        let Ok(entries) = fs::read_dir(root) else {
            continue;
        };
        readable = true;
        for entry in entries.flatten().take(2048) {
            let path = entry.path();
            if path.extension().and_then(|value| value.to_str()) != Some("app") {
                continue;
            }
            let Some(bytes) = read_bounded_file(&path.join("Contents/Info.plist")) else {
                continue;
            };
            let fallback = path
                .file_stem()
                .and_then(|value| value.to_str())
                .unwrap_or("");
            if let Some(browser) = parse_info_plist(&bytes, fallback) {
                found.push(browser);
            }
        }
    }
    if readable {
        Ok(finish(found))
    } else {
        Err(NativeFailure::BrowserDiscoveryFailed)
    }
}

#[allow(dead_code)]
pub(crate) fn parse_info_plist(bytes: &[u8], fallback_name: &str) -> Option<InstalledBrowser> {
    let value = plist::Value::from_reader(std::io::Cursor::new(bytes)).ok()?;
    let dictionary = value.as_dictionary()?;
    let identifier = dictionary.get("CFBundleIdentifier")?.as_string()?;
    if !valid_identity(identifier) {
        return None;
    }
    let mut http = false;
    let mut https = false;
    for kind in dictionary.get("CFBundleURLTypes")?.as_array()? {
        let Some(schemes) = kind
            .as_dictionary()
            .and_then(|value| value.get("CFBundleURLSchemes"))
            .and_then(|value| value.as_array())
        else {
            continue;
        };
        for scheme in schemes.iter().filter_map(|value| value.as_string()) {
            http |= scheme.eq_ignore_ascii_case("http");
            https |= scheme.eq_ignore_ascii_case("https");
        }
    }
    if !(http && https) {
        return None;
    }
    let name = ["CFBundleDisplayName", "CFBundleName"]
        .iter()
        .find_map(|key| dictionary.get(key).and_then(|value| value.as_string()))
        .unwrap_or(fallback_name);
    Some(InstalledBrowser {
        entry: BrowserEntry {
            identity: identifier.to_owned(),
            name: display_name(name, fallback_name),
        },
        launch: LaunchSpec {
            program: "/usr/bin/open".into(),
            args: vec![
                LaunchArg::Text("-b".into()),
                LaunchArg::Text(identifier.to_owned()),
                LaunchArg::Url,
            ],
        },
    })
}

// ---- Linux -----------------------------------------------------------------

#[allow(dead_code)]
fn linux_roots() -> Vec<PathBuf> {
    let home = std::env::var_os("HOME")
        .map(PathBuf::from)
        .filter(|path| path.is_absolute());
    let mut roots = Vec::new();
    match std::env::var_os("XDG_DATA_HOME")
        .map(PathBuf::from)
        .filter(|path| path.is_absolute())
    {
        Some(path) => roots.push(path.join("applications")),
        None => roots.extend(
            home.iter()
                .map(|home| home.join(".local/share/applications")),
        ),
    }
    let directories = std::env::var_os("XDG_DATA_DIRS")
        .filter(|value| value.as_encoded_bytes().len() <= 4096)
        .map(|value| std::env::split_paths(&value).take(16).collect::<Vec<_>>())
        .filter(|value| !value.is_empty())
        .unwrap_or_else(|| vec!["/usr/local/share".into(), "/usr/share".into()]);
    roots.extend(
        directories
            .into_iter()
            .filter(|path| path.is_absolute())
            .map(|path| path.join("applications")),
    );
    roots.push("/var/lib/flatpak/exports/share/applications".into());
    roots.extend(
        home.iter()
            .map(|home| home.join(".local/share/flatpak/exports/share/applications")),
    );
    roots.push("/var/lib/snapd/desktop/applications".into());
    roots
}

#[allow(dead_code)]
pub(crate) fn scan_linux(roots: &[PathBuf]) -> Result<Vec<InstalledBrowser>, NativeFailure> {
    let mut readable = false;
    let mut found = Vec::new();
    let mut shadowed = std::collections::BTreeSet::new();
    for root in roots {
        let Ok(entries) = fs::read_dir(root) else {
            continue;
        };
        readable = true;
        let mut names: Vec<_> = entries
            .flatten()
            .filter_map(|entry| entry.file_name().into_string().ok())
            .filter(|name| name.ends_with(".desktop"))
            .take(4096)
            .collect();
        names.sort();
        for name in names {
            // The first directory in precedence order owns a desktop-file ID,
            // even when that entry is hidden or not an HTTP(S) handler.
            if !shadowed.insert(name.clone()) {
                continue;
            }
            let Some(bytes) = read_bounded_file(&root.join(&name)) else {
                continue;
            };
            if let Some(browser) = parse_desktop_entry(&name, &String::from_utf8_lossy(&bytes)) {
                found.push(browser);
            }
        }
    }
    if readable {
        Ok(finish(found))
    } else {
        Err(NativeFailure::BrowserDiscoveryFailed)
    }
}

#[allow(dead_code)]
pub(crate) fn parse_desktop_entry(file_name: &str, text: &str) -> Option<InstalledBrowser> {
    if !valid_identity(file_name) {
        return None;
    }
    let mut in_entry = false;
    let (mut kind, mut name, mut exec, mut mime) = (None, None, None, None);
    let (mut hidden, mut no_display) = (false, false);
    for line in text.lines() {
        let line = line.trim_end_matches('\r');
        if line.starts_with('[') {
            in_entry = line == "[Desktop Entry]";
            continue;
        }
        if !in_entry || line.starts_with('#') {
            continue;
        }
        let Some((key, value)) = line.split_once('=') else {
            continue;
        };
        match key.trim() {
            "Type" => kind = Some(value.trim().to_owned()),
            "Name" => name = Some(value.trim().to_owned()),
            "Exec" => exec = Some(value.trim().to_owned()),
            "MimeType" => mime = Some(value.to_owned()),
            "Hidden" => hidden = value.trim() == "true",
            "NoDisplay" => no_display = value.trim() == "true",
            _ => {}
        }
    }
    if hidden || no_display || kind.as_deref() != Some("Application") {
        return None;
    }
    let mime = mime?;
    let handles = |scheme: &str| mime.split(';').any(|value| value.trim() == scheme);
    if !(handles("x-scheme-handler/http") && handles("x-scheme-handler/https")) {
        return None;
    }
    let launch = parse_exec(&exec?)?;
    let fallback = file_name.trim_end_matches(".desktop");
    Some(InstalledBrowser {
        entry: BrowserEntry {
            identity: file_name.to_owned(),
            name: display_name(&name.unwrap_or_default(), fallback),
        },
        launch,
    })
}

/// Desktop Entry `Exec` parsing with quoting and field codes. Unknown field
/// codes make the entry unusable rather than guessing.
#[allow(dead_code)]
pub(crate) fn parse_exec(exec: &str) -> Option<LaunchSpec> {
    let mut tokens: Vec<String> = Vec::new();
    let mut current = String::new();
    let mut started = false;
    let mut quoted = false;
    let mut chars = exec.chars().peekable();
    while let Some(character) = chars.next() {
        match character {
            '"' => {
                quoted = !quoted;
                started = true;
            }
            '\\' if quoted => match chars.peek() {
                Some(&next) if matches!(next, '"' | '`' | '$' | '\\') => {
                    current.push(next);
                    chars.next();
                }
                _ => current.push('\\'),
            },
            ' ' | '\t' if !quoted => {
                if started {
                    tokens.push(std::mem::take(&mut current));
                    started = false;
                }
            }
            _ => {
                current.push(character);
                started = true;
            }
        }
    }
    if quoted {
        return None;
    }
    if started {
        tokens.push(current);
    }
    let mut iterator = tokens.into_iter();
    let program = iterator.next()?;
    if program.is_empty() || program.contains('%') || program.chars().any(char::is_control) {
        return None;
    }
    let mut args = Vec::new();
    let mut has_url = false;
    for token in iterator {
        let mut prefix = String::new();
        let mut suffix = String::new();
        let mut seen_url = false;
        let mut chars = token.chars();
        while let Some(character) = chars.next() {
            if character != '%' {
                if seen_url { &mut suffix } else { &mut prefix }.push(character);
                continue;
            }
            match chars.next()? {
                '%' => {
                    if seen_url { &mut suffix } else { &mut prefix }.push('%');
                }
                'u' | 'U' | 'f' | 'F' => {
                    if seen_url {
                        return None;
                    }
                    seen_url = true;
                }
                'i' | 'c' | 'k' | 'd' | 'D' | 'n' | 'N' | 'v' | 'm' => {}
                _ => return None,
            }
        }
        if seen_url {
            has_url = true;
            args.push(if prefix.is_empty() && suffix.is_empty() {
                LaunchArg::Url
            } else {
                LaunchArg::Embedded { prefix, suffix }
            });
        } else if !prefix.is_empty() {
            args.push(LaunchArg::Text(prefix));
        }
    }
    if !has_url {
        args.push(LaunchArg::Url);
    }
    Some(LaunchSpec { program, args })
}

// ---- Windows ---------------------------------------------------------------

#[allow(dead_code)]
pub(crate) trait RegistryReader {
    /// Returns the text output of `reg query <arguments>` or `None` on failure.
    fn query(&self, arguments: &[&str]) -> Option<String>;
}

#[allow(dead_code)]
pub(crate) struct RegExe;

impl RegistryReader for RegExe {
    fn query(&self, arguments: &[&str]) -> Option<String> {
        let root = std::env::var_os("SystemRoot")?;
        let mut command = Command::new(PathBuf::from(root).join("System32/reg.exe"));
        command
            .arg("query")
            .args(arguments)
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .env_clear();
        if let Some(root) = std::env::var_os("SystemRoot") {
            command.env("SystemRoot", root);
        }
        let mut child = command.spawn().ok()?;
        let mut stdout = child.stdout.take()?;
        let reader = std::thread::spawn(move || {
            let mut bytes = Vec::new();
            let _ = (&mut stdout)
                .take(COMMAND_OUTPUT_LIMIT)
                .read_to_end(&mut bytes);
            bytes
        });
        let until = Instant::now() + COMMAND_TIMEOUT;
        let status = loop {
            match child.try_wait() {
                Ok(Some(status)) => break status,
                Ok(None) if Instant::now() < until => std::thread::sleep(Duration::from_millis(10)),
                _ => {
                    let _ = child.kill();
                    let _ = child.wait();
                    return None;
                }
            }
        };
        let bytes = reader.join().ok()?;
        status
            .success()
            .then(|| String::from_utf8_lossy(&bytes).into_owned())
    }
}

#[derive(Debug, PartialEq, Eq)]
#[allow(dead_code)]
pub(crate) struct RegistrySection {
    pub path: String,
    pub values: Vec<(String, String)>,
}

/// Parses `reg query` text: `HKEY_...` headers followed by indented
/// `name    TYPE    data` rows.
#[allow(dead_code)]
pub(crate) fn parse_registry(output: &str) -> Vec<RegistrySection> {
    let mut sections: Vec<RegistrySection> = Vec::new();
    for line in output.lines() {
        let line = line.trim_end_matches('\r');
        if line.trim().is_empty() {
            continue;
        }
        if !line.starts_with(' ') {
            sections.push(RegistrySection {
                path: line.trim().to_owned(),
                values: Vec::new(),
            });
        } else if let Some(section) = sections.last_mut() {
            let mut parts = line.trim_start().splitn(3, "    ");
            if let (Some(name), Some(kind), Some(data)) = (parts.next(), parts.next(), parts.next())
                && kind.starts_with("REG_")
            {
                section
                    .values
                    .push((name.to_owned(), data.trim().to_owned()));
            }
        }
    }
    sections
}

#[allow(dead_code)]
fn registry_value<'a>(section: &'a RegistrySection, name: &str) -> Option<&'a str> {
    section
        .values
        .iter()
        .find(|(candidate, _)| candidate.eq_ignore_ascii_case(name))
        .map(|(_, data)| data.as_str())
        .filter(|data| *data != "(value not set)")
}

/// `/ve` selects the unnamed value in reg.exe itself. Its printed label is
/// localized, so only the requested key and the single returned type/data row
/// identify the result; the label must never be used as a value name here.
#[allow(dead_code)]
fn queried_unnamed_value(output: &str, key_path: &str) -> Option<String> {
    let sections = parse_registry(output);
    let section = sections
        .iter()
        .find(|section| section.path.eq_ignore_ascii_case(key_path))?;
    let [(_, value)] = section.values.as_slice() else {
        return None;
    };
    (!value.is_empty() && value != "(value not set)").then(|| value.clone())
}

#[allow(dead_code)]
fn expand_variables(value: &str, lookup: &dyn Fn(&str) -> Option<String>) -> Option<String> {
    let mut output = String::new();
    let mut rest = value;
    while let Some(start) = rest.find('%') {
        output.push_str(&rest[..start]);
        let tail = &rest[start + 1..];
        let end = tail.find('%')?;
        output.push_str(&lookup(&tail[..end])?);
        rest = &tail[end + 1..];
    }
    output.push_str(rest);
    Some(output)
}

/// Extracts the executable from a registry open command and ignores every
/// registered argument: DeliDev supplies only the URL.
#[allow(dead_code)]
pub(crate) fn command_executable(
    command: &str,
    lookup: &dyn Fn(&str) -> Option<String>,
) -> Option<String> {
    let command = expand_variables(command.trim(), lookup)?;
    let executable = if let Some(rest) = command.strip_prefix('"') {
        rest.split_once('"')?.0.to_owned()
    } else {
        let end = command.to_ascii_lowercase().find(".exe")? + 4;
        command[..end].to_owned()
    };
    let bytes = executable.as_bytes();
    let absolute = bytes.len() > 7
        && bytes[0].is_ascii_alphabetic()
        && bytes[1] == b':'
        && matches!(bytes[2], b'\\' | b'/');
    (absolute
        && executable.to_ascii_lowercase().ends_with(".exe")
        && !executable.chars().any(char::is_control)
        && !executable.contains(".."))
    .then_some(executable)
}

#[allow(dead_code)]
pub(crate) fn windows_browser(
    key_name: &str,
    sections: &[RegistrySection],
    name: Option<&str>,
    command: &str,
    lookup: &dyn Fn(&str) -> Option<String>,
) -> Option<InstalledBrowser> {
    if !valid_identity(key_name) {
        return None;
    }
    let find = |suffix: &str| {
        sections.iter().find(|section| {
            section
                .path
                .to_ascii_lowercase()
                .ends_with(&format!("\\{}", suffix.to_ascii_lowercase()))
        })
    };
    let associations = find("capabilities\\urlassociations")?;
    registry_value(associations, "http")?;
    registry_value(associations, "https")?;
    let executable = command_executable(command, lookup)?;
    let name = name.unwrap_or(key_name);
    Some(InstalledBrowser {
        entry: BrowserEntry {
            identity: key_name.to_owned(),
            name: display_name(name, key_name),
        },
        launch: LaunchSpec {
            program: executable,
            args: vec![LaunchArg::Url],
        },
    })
}

#[allow(dead_code)]
pub(crate) fn discover_windows(
    reader: &dyn RegistryReader,
) -> Result<Vec<InstalledBrowser>, NativeFailure> {
    let lookup = |name: &str| {
        matches!(
            name.to_ascii_lowercase().as_str(),
            "programfiles"
                | "programfiles(x86)"
                | "programw6432"
                | "localappdata"
                | "systemroot"
                | "windir"
        )
        .then(|| std::env::var(name).ok())
        .flatten()
    };
    let mut found = Vec::new();
    let mut readable = false;
    for root in [
        r"HKLM\SOFTWARE\Clients\StartMenuInternet",
        r"HKLM\SOFTWARE\WOW6432Node\Clients\StartMenuInternet",
        r"HKCU\SOFTWARE\Clients\StartMenuInternet",
    ] {
        let Some(listing) = reader.query(&[root]) else {
            continue;
        };
        readable = true;
        let prefix = root
            .replace("HKLM", "HKEY_LOCAL_MACHINE")
            .replace("HKCU", "HKEY_CURRENT_USER");
        for section in parse_registry(&listing).into_iter().take(129) {
            let Some(key_name) = section
                .path
                .strip_prefix(&format!("{prefix}\\"))
                .filter(|name| !name.contains('\\'))
            else {
                continue;
            };
            let Some(detail) = reader.query(&[&format!(r"{root}\{key_name}"), "/s"]) else {
                continue;
            };
            let key_path = format!(r"{root}\{key_name}");
            let full_path = format!(r"{prefix}\{key_name}");
            let name = reader
                .query(&[&key_path, "/ve"])
                .and_then(|output| queried_unnamed_value(&output, &full_path));
            let command_path = format!(r"{key_path}\shell\open\command");
            let full_command_path = format!(r"{full_path}\shell\open\command");
            let Some(command) = reader
                .query(&[&command_path, "/ve"])
                .and_then(|output| queried_unnamed_value(&output, &full_command_path))
            else {
                continue;
            };
            if let Some(browser) = windows_browser(
                key_name,
                &parse_registry(&detail),
                name.as_deref(),
                &command,
                &lookup,
            ) {
                found.push(browser);
            }
        }
    }
    if readable {
        Ok(finish(found))
    } else {
        Err(NativeFailure::BrowserDiscoveryFailed)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn plist_xml(identifier: &str, schemes: &[&str]) -> Vec<u8> {
        let schemes: String = schemes
            .iter()
            .map(|scheme| format!("<string>{scheme}</string>"))
            .collect();
        format!(
            "<?xml version=\"1.0\" encoding=\"UTF-8\"?><plist \
             version=\"1.0\"><dict><key>CFBundleIdentifier</key><string>{identifier}</\
             string><key>CFBundleName</key><string>Fixture \
             Browser</string><key>CFBundleURLTypes</key><array><dict><key>CFBundleURLSchemes</\
             key><array>{schemes}</array></dict></array></dict></plist>"
        )
        .into_bytes()
    }

    #[test]
    fn identity_syntax_is_bounded_and_path_free() {
        for good in [
            "com.google.Chrome",
            "org.mozilla.firefox.desktop",
            "Google Chrome",
            "IEXPLORE.EXE",
        ] {
            assert!(valid_identity(good), "{good}");
        }
        for bad in [
            "",
            " lead",
            "trail ",
            "../x",
            "a/b",
            "a\\b",
            "C:\\x.exe",
            "a;b",
            "a\nb",
            &"x".repeat(129),
        ] {
            assert!(!valid_identity(bad), "{bad:?}");
        }
    }

    #[test]
    fn macos_requires_both_web_schemes_and_launches_by_bundle_identifier() {
        let browser =
            parse_info_plist(&plist_xml("com.example.Fixture", &["http", "HTTPS"]), "x").unwrap();
        assert_eq!(browser.entry.identity, "com.example.Fixture");
        assert_eq!(browser.entry.name, "Fixture Browser");
        assert_eq!(browser.launch.program, "/usr/bin/open");
        assert_eq!(
            browser.launch.argv("https://example.com/?a=b&c=$(x)"),
            [
                "-b",
                "com.example.Fixture",
                "https://example.com/?a=b&c=$(x)"
            ]
        );
        assert!(parse_info_plist(&plist_xml("com.example.Mail", &["mailto"]), "x").is_none());
        assert!(parse_info_plist(&plist_xml("com.example.Http", &["http"]), "x").is_none());
        assert!(parse_info_plist(&plist_xml("bad/id", &["http", "https"]), "x").is_none());
        assert!(parse_info_plist(b"not a plist", "x").is_none());
    }

    #[test]
    fn macos_scan_reads_application_bundles_and_reports_unreadable_roots() {
        let root = tempfile::tempdir().unwrap();
        for (app, identifier, schemes) in [
            ("Zed Browser.app", "com.example.Zed", vec!["http", "https"]),
            ("Alpha.app", "com.example.Alpha", vec!["http", "https"]),
            ("Notes.app", "com.example.Notes", vec!["notes"]),
        ] {
            let contents = root.path().join(app).join("Contents");
            fs::create_dir_all(&contents).unwrap();
            fs::write(contents.join("Info.plist"), plist_xml(identifier, &schemes)).unwrap();
        }
        let found = scan_macos(&[root.path().to_owned(), root.path().join("missing")]).unwrap();
        let identities: Vec<_> = found
            .iter()
            .map(|browser| browser.entry.identity.as_str())
            .collect();
        assert_eq!(identities, ["com.example.Alpha", "com.example.Zed"]);
        assert_eq!(
            scan_macos(&[root.path().join("missing")]),
            Err(NativeFailure::BrowserDiscoveryFailed)
        );
        let empty = tempfile::tempdir().unwrap();
        assert_eq!(scan_macos(&[empty.path().to_owned()]).unwrap(), []);
    }

    // Fixtures are assembled from short lines so rustfmt cannot reflow an
    // escape sequence inside a long literal.
    fn firefox() -> String {
        [
            "[Desktop Entry]",
            "Type=Application",
            "Name=Firefox Web Browser",
            "Exec=/usr/lib/firefox/firefox --new-window %u",
            "MimeType=text/html;x-scheme-handler/http;x-scheme-handler/https;",
            "[Desktop Action new-window]",
            "Name=Other",
            "Exec=ignored",
            "",
        ]
        .join("\n")
    }

    #[test]
    fn linux_desktop_entries_require_both_scheme_handlers_and_visible_applications() {
        let browser = parse_desktop_entry("firefox.desktop", &firefox()).unwrap();
        assert_eq!(browser.entry.identity, "firefox.desktop");
        assert_eq!(browser.entry.name, "Firefox Web Browser");
        assert_eq!(browser.launch.program, "/usr/lib/firefox/firefox");
        assert_eq!(
            browser.launch.argv("https://example.com/"),
            ["--new-window", "https://example.com/"]
        );
        let without_https = firefox().replace(";x-scheme-handler/https", "");
        assert!(parse_desktop_entry("a.desktop", &without_https).is_none());
        assert!(
            parse_desktop_entry(
                "a.desktop",
                &firefox().replace("Type=Application", "Type=Link")
            )
            .is_none()
        );
        assert!(
            parse_desktop_entry(
                "a.desktop",
                &firefox().replace("Name=", "NoDisplay=true\nName=")
            )
            .is_none()
        );
        assert!(
            parse_desktop_entry(
                "a.desktop",
                &firefox().replace("Name=", "Hidden=true\nName=")
            )
            .is_none()
        );
        assert!(parse_desktop_entry("../a.desktop", &firefox()).is_none());
    }

    #[test]
    fn exec_parsing_is_argv_only_with_field_codes_and_quoting() {
        let spec =
            parse_exec(r#"env FOO=bar "/opt/My Browser/browser" --profile "a b" %U %i"#).unwrap();
        assert_eq!(spec.program, "env");
        assert_eq!(
            spec.argv("https://x.test/"),
            [
                "FOO=bar",
                "/opt/My Browser/browser",
                "--profile",
                "a b",
                "https://x.test/"
            ]
        );
        assert_eq!(parse_exec("browser").unwrap().argv("u"), ["u"]);
        assert_eq!(
            parse_exec("browser --app=%u").unwrap().argv("u"),
            ["--app=u"]
        );
        assert_eq!(
            parse_exec("browser 100%% %f").unwrap().argv("u"),
            ["100%", "u"]
        );
        // The URL is one argv value, never shell text.
        assert_eq!(
            parse_exec("sh -c browser %u")
                .unwrap()
                .argv("https://x.test/;rm -rf ~&`id`"),
            ["-c", "browser", "https://x.test/;rm -rf ~&`id`"]
        );
        assert!(parse_exec("browser %z").is_none());
        assert!(parse_exec("%u").is_none());
        assert!(parse_exec("browser \"unterminated").is_none());
        assert!(parse_exec("").is_none());
    }

    #[test]
    fn linux_scan_keeps_first_directory_precedence() {
        let first = tempfile::tempdir().unwrap();
        let second = tempfile::tempdir().unwrap();
        fs::write(
            first.path().join("firefox.desktop"),
            firefox().replace("Type=Application", "NoDisplay=true\nType=Application"),
        )
        .unwrap();
        fs::write(second.path().join("firefox.desktop"), firefox()).unwrap();
        fs::write(
            second.path().join("other.desktop"),
            firefox().replace("Firefox Web Browser", "Other"),
        )
        .unwrap();
        fs::write(second.path().join("readme.txt"), "x").unwrap();
        let found = scan_linux(&[first.path().to_owned(), second.path().to_owned()]).unwrap();
        let identities: Vec<_> = found
            .iter()
            .map(|browser| browser.entry.identity.as_str())
            .collect();
        assert_eq!(identities, ["other.desktop"]);
        assert_eq!(
            scan_linux(&[first.path().join("missing")]),
            Err(NativeFailure::BrowserDiscoveryFailed)
        );
    }

    const HIVE: &str = "HKEY_LOCAL_MACHINE\\SOFTWARE\\Clients\\StartMenuInternet";
    fn listing() -> String {
        [
            "",
            HIVE,
            &format!("{HIVE}\\Google Chrome"),
            &format!("{HIVE}\\Plain"),
            "",
        ]
        .join("\r\n")
    }
    fn chrome() -> String {
        let root = format!("{HIVE}\\Google Chrome");
        [
            "",
            &root,
            "    (Default)    REG_SZ    Google Chrome",
            "",
            &format!("{root}\\Capabilities\\URLAssociations"),
            "    http    REG_SZ    ChromeHTML",
            "    https    REG_SZ    ChromeHTML",
            "",
            &format!("{root}\\shell\\open\\command"),
            "    (Default)    REG_SZ    \"C:\\Program \
             Files\\Google\\Chrome\\Application\\chrome.exe\" --flag",
            "",
        ]
        .join("\r\n")
    }
    fn plain() -> String {
        [
            "",
            &format!("{HIVE}\\Plain"),
            "    (Default)    REG_SZ    Plain",
            "",
        ]
        .join("\r\n")
    }

    fn unnamed(path: &str, label: &str, value: &str) -> String {
        ["", path, &format!("    {label}    REG_SZ    {value}"), ""].join("\r\n")
    }

    struct Fixture {
        label: &'static str,
        detail: String,
        command: Option<&'static str>,
    }
    impl Fixture {
        fn chrome(label: &'static str) -> Self {
            Self {
                label,
                detail: chrome().replace("(Default)", label),
                command: Some(r#""C:\Program Files\Google\Chrome\Application\chrome.exe" --flag"#),
            }
        }
    }
    impl RegistryReader for Fixture {
        fn query(&self, arguments: &[&str]) -> Option<String> {
            match arguments {
                [r"HKLM\SOFTWARE\Clients\StartMenuInternet"] => Some(listing()),
                [
                    r"HKLM\SOFTWARE\Clients\StartMenuInternet\Google Chrome",
                    "/s",
                ] => Some(self.detail.clone()),
                [
                    r"HKLM\SOFTWARE\Clients\StartMenuInternet\Google Chrome",
                    "/ve",
                ] => Some(unnamed(
                    &format!("{HIVE}\\Google Chrome"),
                    self.label,
                    "Google Chrome",
                )),
                [
                    r"HKLM\SOFTWARE\Clients\StartMenuInternet\Google Chrome\shell\open\command",
                    "/ve",
                ] => self.command.map(|command| {
                    unnamed(
                        &format!("{HIVE}\\Google Chrome\\shell\\open\\command"),
                        self.label,
                        command,
                    )
                }),
                [r"HKLM\SOFTWARE\Clients\StartMenuInternet\Plain", "/s"] => Some(plain()),
                _ => None,
            }
        }
    }

    #[test]
    fn windows_registry_registration_yields_executable_without_registered_arguments() {
        let found = discover_windows(&Fixture::chrome("(Default)")).unwrap();
        assert_eq!(found.len(), 1);
        assert_eq!(found[0].entry.identity, "Google Chrome");
        assert_eq!(found[0].entry.name, "Google Chrome");
        assert_eq!(
            found[0].launch.program,
            r"C:\Program Files\Google\Chrome\Application\chrome.exe"
        );
        assert_eq!(
            found[0].launch.argv("https://example.com/?a&b"),
            ["https://example.com/?a&b"]
        );
    }

    #[test]
    fn windows_registry_unnamed_values_ignore_localized_display_labels() {
        let expected = discover_windows(&Fixture::chrome("(Default)")).unwrap();
        for label in ["(par défaut)", "(Standard)", "(기본값)", "arbitrary label"] {
            assert_eq!(discover_windows(&Fixture::chrome(label)).unwrap(), expected);
        }
        assert_eq!(
            queried_unnamed_value(&unnamed(HIVE, "(par défaut)", "Browser Name"), HIVE,).as_deref(),
            Some("Browser Name")
        );
        assert_eq!(
            queried_unnamed_value(&unnamed(HIVE, "(par défaut)", "Browser Name"), "other"),
            None
        );
    }

    #[test]
    fn windows_registry_rejects_missing_associations_and_commands() {
        for scheme in ["http", "https"] {
            let mut fixture = Fixture::chrome("(par défaut)");
            fixture.detail = fixture
                .detail
                .replace(&format!("    {scheme}    REG_SZ    ChromeHTML\r\n"), "");
            assert!(discover_windows(&fixture).unwrap().is_empty());
        }

        let mut fixture = Fixture::chrome("(par défaut)");
        fixture.command = None;
        fixture.detail = fixture.detail.replace("(par défaut)", "unrelated");
        assert!(fixture.detail.contains("unrelated"));
        assert!(discover_windows(&fixture).unwrap().is_empty());

        let mut fixture = Fixture::chrome("(par défaut)");
        fixture.command = Some("browser.exe --flag");
        assert!(discover_windows(&fixture).unwrap().is_empty());
    }

    #[test]
    fn windows_failure_is_distinct_from_an_empty_inventory() {
        struct Failing;
        impl RegistryReader for Failing {
            fn query(&self, _: &[&str]) -> Option<String> {
                None
            }
        }
        struct Empty;
        impl RegistryReader for Empty {
            fn query(&self, _: &[&str]) -> Option<String> {
                Some(String::new())
            }
        }
        assert_eq!(
            discover_windows(&Failing),
            Err(NativeFailure::BrowserDiscoveryFailed)
        );
        assert_eq!(discover_windows(&Empty).unwrap(), []);
    }

    #[test]
    fn windows_command_executables_are_absolute_and_variable_expanded() {
        let lookup = |name: &str| (name == "ProgramFiles").then(|| r"C:\Program Files".to_owned());
        assert_eq!(
            command_executable(
                r"%ProgramFiles%\Mozilla Firefox\firefox.exe -osint",
                &lookup
            )
            .as_deref(),
            Some(r"C:\Program Files\Mozilla Firefox\firefox.exe")
        );
        for bad in [
            r"firefox.exe",
            r"\\server\share\browser.exe",
            r"C:\x\..\evil.exe",
            r"%Unknown%\browser.exe",
            r#""C:\x\script.cmd""#,
            "cmd /c start",
        ] {
            assert!(command_executable(bad, &lookup).is_none(), "{bad}");
        }
    }

    #[test]
    fn newly_selected_identities_must_be_installed_but_unchanged_ones_may_be_missing() {
        let installed = |identity: &str| BrowserChoice::Installed {
            identity: identity.into(),
        };
        let inventory = || {
            Ok(vec![InstalledBrowser {
                entry: BrowserEntry {
                    identity: "present".into(),
                    name: "Present".into(),
                },
                launch: LaunchSpec {
                    program: "x".into(),
                    args: vec![LaunchArg::Url],
                },
            }])
        };
        let current = BrowserChoices {
            general: installed("gone"),
            local: BrowserChoice::SystemDefault,
        };
        let keep = BrowserChoices {
            general: installed("gone"),
            local: installed("present"),
        };
        assert_eq!(
            require_installed_changes_with(&current, &keep, &inventory),
            Ok(())
        );
        let unknown = BrowserChoices {
            general: installed("gone"),
            local: installed("other"),
        };
        assert_eq!(
            require_installed_changes_with(&current, &unknown, &inventory),
            Err(NativeFailure::BrowserUnavailable)
        );
        let bad = BrowserChoices {
            general: installed("../x"),
            local: BrowserChoice::SystemDefault,
        };
        assert_eq!(
            require_installed_changes_with(&current, &bad, &inventory),
            Err(NativeFailure::InvalidInput)
        );
        let defaults = BrowserChoices::default();
        assert_eq!(
            require_installed_changes_with(&current, &defaults, &|| Err(
                NativeFailure::BrowserDiscoveryFailed
            )),
            Ok(())
        );
        assert_eq!(
            require_installed_changes_with(&defaults, &keep, &|| Err(
                NativeFailure::BrowserDiscoveryFailed
            )),
            Err(NativeFailure::BrowserDiscoveryFailed)
        );
    }

    #[test]
    fn inventory_is_sorted_deduplicated_and_bounded() {
        let make = |identity: &str, name: &str| InstalledBrowser {
            entry: BrowserEntry {
                identity: identity.into(),
                name: name.into(),
            },
            launch: LaunchSpec {
                program: "x".into(),
                args: vec![LaunchArg::Url],
            },
        };
        let mut many: Vec<_> = (0..100)
            .map(|index| make(&format!("id{index:03}"), "Same"))
            .collect();
        many.push(make("id000", "Duplicate"));
        let finished = finish(many);
        assert_eq!(finished.len(), MAX_BROWSERS);
        assert_eq!(finished[0].entry.identity, "id000");
        assert_eq!(finished[0].entry.name, "Same");
    }
}
