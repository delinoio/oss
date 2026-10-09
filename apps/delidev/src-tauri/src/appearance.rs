// SPDX-License-Identifier: Apache-2.0
use std::{
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    path::{Path, PathBuf},
    sync::Mutex,
};

use serde::{Deserialize, Serialize};

const DOCUMENT_LIMIT: u64 = 256 * 1024;

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum Theme {
    #[default]
    System,
    Light,
    Dark,
}

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum Layout {
    #[default]
    Regular,
    Compact,
}
#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum Status {
    #[default]
    Default,
    Minimal,
}
#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum ImageSize {
    #[default]
    Fit,
    Original,
}
#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum Disclosure {
    #[default]
    Original,
    Collapsed,
    Expanded,
}
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct CustomTheme {
    pub version: u32,
    pub id: String,
    pub name: String,
    pub light: std::collections::BTreeMap<String, String>,
    pub dark: std::collections::BTreeMap<String, String>,
}
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Preferences {
    pub light_palette: String,
    pub dark_palette: String,
    pub color_assistance: bool,
    pub custom_themes: Vec<CustomTheme>,
    pub composer_layout: Layout,
    pub composer_size: u8,
    pub status: Status,
    pub session_accent: bool,
    pub show_tokens: bool,
    pub show_time: bool,
    pub density: Layout,
    pub conversation_size: u8,
    pub animation: bool,
    pub tool_disclosure: Disclosure,
    pub reasoning_disclosure: Disclosure,
    pub compaction_disclosure: Disclosure,
    pub markdown: bool,
    pub mermaid: bool,
    pub svg: bool,
    pub table_charts: bool,
    pub inline_images: bool,
    pub image_size: ImageSize,
}
impl Default for Preferences {
    fn default() -> Self {
        Self {
            light_palette: "default".into(),
            dark_palette: "default".into(),
            color_assistance: false,
            custom_themes: vec![],
            composer_layout: Layout::Regular,
            composer_size: 0,
            status: Status::Default,
            session_accent: true,
            show_tokens: true,
            show_time: true,
            density: Layout::Regular,
            conversation_size: 0,
            animation: true,
            tool_disclosure: Disclosure::Original,
            reasoning_disclosure: Disclosure::Original,
            compaction_disclosure: Disclosure::Original,
            markdown: true,
            mermaid: true,
            svg: true,
            table_charts: true,
            inline_images: true,
            image_size: ImageSize::Fit,
        }
    }
}
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct PaletteColors {
    pub light: std::collections::BTreeMap<String, String>,
    pub dark: std::collections::BTreeMap<String, String>,
}
impl Preferences {
    pub fn palette_colors(&self) -> Result<PaletteColors, AppearanceProblem> {
        let bundled: std::collections::BTreeMap<String, PaletteColors> =
            serde_json::from_str(include_str!("../../src/appearance-palettes.json"))
                .map_err(|_| AppearanceProblem::InvalidDocument)?;
        let select =
            |id: &str,
             dark: bool|
             -> Result<std::collections::BTreeMap<String, String>, AppearanceProblem> {
                if let Some(theme) = self.custom_themes.iter().find(|theme| theme.id == id) {
                    return Ok(if dark {
                        theme.dark.clone()
                    } else {
                        theme.light.clone()
                    });
                }
                let value = bundled.get(id).ok_or(AppearanceProblem::InvalidDocument)?;
                Ok(if dark {
                    value.dark.clone()
                } else {
                    value.light.clone()
                })
            };
        Ok(PaletteColors {
            light: select(&self.light_palette, false)?,
            dark: select(&self.dark_palette, true)?,
        })
    }

    fn valid(&self) -> bool {
        if ![0, 12, 14, 16, 18].contains(&self.composer_size)
            || ![0, 12, 14, 16, 18].contains(&self.conversation_size)
            || self.custom_themes.len() > 32
        {
            return false;
        }
        let mut identities = std::collections::BTreeSet::new();
        for t in &self.custom_themes {
            if t.version != 1
                || t.name.trim().is_empty()
                || t.name.chars().count() > 80
                || t.name.chars().any(|c| c.is_control())
            {
                return false;
            }
            let Ok(id) = uuid::Uuid::parse_str(&t.id) else {
                return false;
            };
            if id.get_variant() != uuid::Variant::RFC4122
                || id.get_version_num() != 7
                || id.to_string() != t.id
                || !identities.insert(t.id.as_str())
                || !valid_colors(&t.light)
                || !valid_colors(&t.dark)
            {
                return false;
            }
        }
        [&self.light_palette, &self.dark_palette]
            .into_iter()
            .all(|id| {
                ["default", "titanium", "nord", "dracula", "solarized"].contains(&id.as_str())
                    || identities.contains(id.as_str())
            })
    }
}
fn valid_colors(value: &std::collections::BTreeMap<String, String>) -> bool {
    const TOKENS: &[&str] = &[
        "background",
        "surface",
        "surface-subtle",
        "surface-muted",
        "surface-inset",
        "surface-hover",
        "surface-selected",
        "text",
        "text-secondary",
        "muted",
        "text-subtle",
        "border",
        "control-border",
        "border-subtle",
        "accent",
        "accent-hover",
        "link",
        "focus",
        "on-accent",
        "inverse-surface",
        "inverse-hover",
        "inverse-border",
        "on-inverse",
        "on-inverse-muted",
        "selected-background",
        "selected-text",
        "selected-border",
        "warning-background",
        "warning-text",
        "warning-border",
        "danger-background",
        "danger-text",
        "danger-border",
        "success-text",
        "execution-running",
        "conversation-background",
        "conversation-text",
        "conversation-border",
    ];
    if value.len() != TOKENS.len()
        || !TOKENS.iter().all(|k| {
            value.get(*k).is_some_and(|v| {
                v.len() == 7
                    && v.starts_with('#')
                    && v.as_bytes()[1..].iter().all(u8::is_ascii_hexdigit)
            })
        })
    {
        return false;
    }
    let contrast = |a: &str, b: &str| {
        let x = luminance(&value[a]);
        let y = luminance(&value[b]);
        (x.max(y) + 0.05) / (x.min(y) + 0.05)
    };
    [
        ("text", "surface"),
        ("text-secondary", "surface"),
        ("muted", "surface"),
        ("text-subtle", "surface"),
        ("on-accent", "accent"),
        ("on-accent", "accent-hover"),
        ("selected-text", "selected-background"),
        ("warning-text", "warning-background"),
        ("danger-text", "danger-background"),
        ("on-inverse", "inverse-surface"),
    ]
    .into_iter()
    .all(|(a, b)| contrast(a, b) >= 4.5)
        && contrast("control-border", "surface") >= 3.0
}
fn luminance(color: &str) -> f64 {
    let channel = |start: usize| {
        let value = u8::from_str_radix(&color[start..start + 2], 16).unwrap_or(0) as f64 / 255.0;
        if value <= 0.04045 {
            value / 12.92
        } else {
            ((value + 0.055) / 1.055).powf(2.4)
        }
    };
    0.2126 * channel(1) + 0.7152 * channel(3) + 0.0722 * channel(5)
}

#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum AppearanceProblem {
    Unavailable,
    ReadFailed,
    InvalidDocument,
    UnsupportedVersion,
    WriteFailed,
    OutcomeUnknown,
    Changed,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct AppearanceSnapshot {
    pub revision: u32,
    pub theme: Theme,
    pub problem: Option<AppearanceProblem>,
    pub preferences: Preferences,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Document {
    version: u32,
    theme: Theme,
    #[serde(skip_serializing_if = "Option::is_none")]
    preferences: Option<Preferences>,
}

/// One process-wide device preference, independent of every server connection.
/// The lock covers inspection and atomic publication; revisions order IPC
/// replies and events even when their delivery order differs from their commit
/// order.
pub struct AppearanceStore {
    path: Option<PathBuf>,
    state: Mutex<AppearanceSnapshot>,
}

impl AppearanceStore {
    pub fn new(config_dir: Option<PathBuf>) -> Self {
        Self {
            path: config_dir.map(|directory| directory.join("appearance.json")),
            state: Mutex::new(AppearanceSnapshot {
                revision: 0,
                theme: Theme::System,
                problem: Some(AppearanceProblem::Unavailable),
                preferences: Preferences::default(),
            }),
        }
    }

    /// Retained presentation only; auxiliary views cannot inspect preference
    /// files.
    pub fn current(&self) -> AppearanceSnapshot {
        self.state
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .clone()
    }

    pub fn read(&self) -> AppearanceSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        let previous = state.clone();
        match self.path.as_deref().map(inspect) {
            Some(Ok((theme, preferences))) => {
                state.theme = theme;
                state.preferences = preferences;
                state.problem = None;
            }
            Some(Err(problem)) => {
                if matches!(
                    problem,
                    AppearanceProblem::InvalidDocument | AppearanceProblem::UnsupportedVersion
                ) {
                    state.theme = Theme::System;
                    state.preferences = Preferences::default();
                }
                state.problem = Some(problem);
                tracing::warn!(operation = "appearance_read", ?problem);
            }
            None => state.problem = Some(AppearanceProblem::Unavailable),
        }
        if state.revision == 0
            || state.theme != previous.theme
            || state.preferences != previous.preferences
            || state.problem != previous.problem
        {
            advance(&mut state);
        }
        state.clone()
    }

    pub fn update(&self, theme: Theme, expected_revision: u32) -> AppearanceSnapshot {
        let preferences = self.current().preferences;
        self.update_preferences(theme, preferences, expected_revision)
    }

    pub fn update_preferences(
        &self,
        theme: Theme,
        preferences: Preferences,
        expected_revision: u32,
    ) -> AppearanceSnapshot {
        self.commit(theme, preferences, expected_revision, persist)
    }

    #[cfg(test)]
    fn update_with(
        &self,
        theme: Theme,
        expected_revision: u32,
        write: impl FnOnce(&Path, Theme) -> Result<(), AppearanceProblem>,
    ) -> AppearanceSnapshot {
        let preferences = self.current().preferences;
        self.commit(theme, preferences, expected_revision, |path, theme, _| {
            write(path, theme)
        })
    }

    fn commit(
        &self,
        theme: Theme,
        preferences: Preferences,
        expected_revision: u32,
        write: impl FnOnce(&Path, Theme, &Preferences) -> Result<(), AppearanceProblem>,
    ) -> AppearanceSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        if state.revision != expected_revision {
            // Do not mutate the shared committed state for a stale caller. It
            // must inspect again before choosing against the current selection.
            let mut snapshot = state.clone();
            snapshot.problem = Some(AppearanceProblem::Changed);
            return snapshot;
        }
        if state.problem.is_some() || state.revision == u32::MAX {
            return state.clone();
        }
        if !preferences.valid() {
            let mut rejected = state.clone();
            rejected.problem = Some(AppearanceProblem::InvalidDocument);
            return rejected;
        }
        let result = self
            .path
            .as_deref()
            .ok_or(AppearanceProblem::Unavailable)
            .and_then(|path| {
                // Reinspect before replacement, preserving external
                // invalid/newer documents instead of silently
                // upgrading or overwriting them.
                let current = inspect(path)?;
                if current != (state.theme, state.preferences.clone()) {
                    return Err(AppearanceProblem::Changed);
                }
                write(path, theme, &preferences)
            });
        match result {
            Ok(()) => {
                state.theme = theme;
                state.preferences = preferences;
                tracing::info!(operation = "appearance_update", state = "committed");
            }
            Err(problem) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "appearance_update", ?problem);
            }
        }
        advance(&mut state);
        state.clone()
    }
}

fn advance(state: &mut AppearanceSnapshot) {
    // Never wrap into an older revision. Exhaustion remains read-only until
    // restart instead of letting a delayed event replace newer state.
    if let Some(revision) = state.revision.checked_add(1) {
        state.revision = revision;
    } else {
        state.problem = Some(AppearanceProblem::Unavailable);
    }
}

fn inspect(path: &Path) -> Result<(Theme, Preferences), AppearanceProblem> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok((Theme::System, Preferences::default()));
        }
        Err(_) => return Err(AppearanceProblem::ReadFailed),
    };
    if !metadata.is_file() || metadata.len() > DOCUMENT_LIMIT {
        return Err(AppearanceProblem::InvalidDocument);
    }
    let file = File::open(path).map_err(|_| AppearanceProblem::ReadFailed)?;
    let mut bytes = Vec::new();
    file.take(DOCUMENT_LIMIT + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| AppearanceProblem::ReadFailed)?;
    if bytes.len() as u64 > DOCUMENT_LIMIT {
        return Err(AppearanceProblem::InvalidDocument);
    }
    let document: Document =
        serde_json::from_slice(&bytes).map_err(|_| AppearanceProblem::InvalidDocument)?;
    let preferences = match (document.version, document.preferences) {
        (1, None) => Preferences::default(),
        (2, Some(value)) if value.valid() => value,
        (1 | 2, _) => return Err(AppearanceProblem::InvalidDocument),
        _ => return Err(AppearanceProblem::UnsupportedVersion),
    };
    Ok((document.theme, preferences))
}

fn persist(path: &Path, theme: Theme, preferences: &Preferences) -> Result<(), AppearanceProblem> {
    let parent = path.parent().ok_or(AppearanceProblem::WriteFailed)?;
    fs::create_dir_all(parent).map_err(|_| AppearanceProblem::WriteFailed)?;
    let temporary = parent.join(format!(".appearance-{}.tmp", uuid::Uuid::now_v7()));
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    // Cleanup owns only the file that this attempt created exclusively. A
    // creation failure cannot authorize deleting a pre-existing scratch file.
    let mut file = options
        .open(&temporary)
        .map_err(|_| AppearanceProblem::WriteFailed)?;
    let scratch = &temporary;
    let result = (move || {
        let bytes = serde_json::to_vec(&Document {
            version: 2,
            theme,
            preferences: Some(preferences.clone()),
        })
        .map_err(|_| AppearanceProblem::WriteFailed)?;
        if bytes.len() as u64 > DOCUMENT_LIMIT {
            return Err(AppearanceProblem::InvalidDocument);
        }
        file.write_all(&bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| AppearanceProblem::WriteFailed)?;
        drop(file);
        replace(scratch, path).map_err(|_| AppearanceProblem::WriteFailed)?;
        // Publication happened already. A failed directory sync is uncertain,
        // so retain the prior selection and require a fresh read before retry.
        #[cfg(unix)]
        File::open(parent)
            .and_then(|directory| directory.sync_all())
            .map_err(|_| AppearanceProblem::OutcomeUnknown)?;
        Ok(())
    })();
    let _ = fs::remove_file(temporary);
    result
}

#[cfg(not(windows))]
pub(crate) fn replace(source: &Path, target: &Path) -> std::io::Result<()> {
    fs::rename(source, target)
}

#[cfg(windows)]
pub(crate) fn replace(source: &Path, target: &Path) -> std::io::Result<()> {
    use std::os::windows::ffi::OsStrExt;
    #[link(name = "kernel32")]
    unsafe extern "system" {
        fn MoveFileExW(source: *const u16, target: *const u16, flags: u32) -> i32;
    }
    let source: Vec<u16> = source.as_os_str().encode_wide().chain(Some(0)).collect();
    let target: Vec<u16> = target.as_os_str().encode_wide().chain(Some(0)).collect();
    // Replace the same-directory target atomically and wait for publication.
    if unsafe { MoveFileExW(source.as_ptr(), target.as_ptr(), 0x1 | 0x8) } == 0 {
        Err(std::io::Error::last_os_error())
    } else {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn version_one_read_preserves_bytes_and_upgrades_full_preferences() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("appearance.json");
        let bytes = br#"{"version":1,"theme":"dark"}"#;
        fs::write(&path, bytes).unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(fs::read(&path).unwrap(), bytes);
        let mut preferences = initial.preferences.clone();
        preferences.density = Layout::Compact;
        preferences.composer_size = 18;
        assert_eq!(
            store
                .update_preferences(Theme::Dark, preferences.clone(), initial.revision)
                .problem,
            None
        );
        assert_eq!(
            AppearanceStore::new(Some(directory.path().into()))
                .read()
                .preferences,
            preferences
        );
        let encoded: serde_json::Value = serde_json::from_slice(&fs::read(path).unwrap()).unwrap();
        assert_eq!(encoded["version"], 2);
    }

    #[test]
    fn invalid_complete_preferences_cannot_replace_committed_storage() {
        let directory = tempfile::tempdir().unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let initial = store.read();
        let mut invalid = initial.preferences.clone();
        invalid.light_palette = "foreign".into();
        let rejected = store.update_preferences(Theme::Dark, invalid, initial.revision);
        assert_eq!(rejected.problem, Some(AppearanceProblem::InvalidDocument));
        assert_eq!(store.read(), initial);
        assert!(!directory.path().join("appearance.json").exists());
    }

    #[test]
    fn custom_themes_require_complete_contrast_maps_and_unique_original_references() {
        let source: serde_json::Value =
            serde_json::from_str(include_str!("../../src/appearance-palettes.json")).unwrap();
        let theme = CustomTheme {
            version: 1,
            id: uuid::Uuid::now_v7().to_string(),
            name: "Fixture".into(),
            light: serde_json::from_value(source["default"]["light"].clone()).unwrap(),
            dark: serde_json::from_value(source["default"]["dark"].clone()).unwrap(),
        };
        let mut preferences = Preferences::default();
        preferences.custom_themes.push(theme.clone());
        preferences.light_palette = theme.id.clone();
        assert!(preferences.valid());
        preferences.custom_themes.push(theme.clone());
        assert!(!preferences.valid());
        preferences.custom_themes.pop();
        preferences.custom_themes[0]
            .light
            .insert("text".into(), "#FFFFFF".into());
        assert!(!preferences.valid());
        preferences.custom_themes[0] = theme.clone();
        preferences.custom_themes[0]
            .dark
            .insert("backdrop".into(), "#000000".into());
        assert!(!preferences.valid());
        preferences.custom_themes = vec![theme.clone(); 33];
        assert!(!preferences.valid());
    }

    #[test]
    fn failed_or_uncertain_publication_requires_a_fresh_read() {
        let directory = tempfile::tempdir().unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let initial = store.read();
        let dark = store.update(Theme::Dark, initial.revision);
        let failure = store.update_with(Theme::Light, dark.revision, |_, _| {
            Err(AppearanceProblem::WriteFailed)
        });
        assert_eq!(failure.theme, Theme::Dark);
        assert_eq!(failure.problem, Some(AppearanceProblem::WriteFailed));
        assert_eq!(
            store.update(Theme::Light, failure.revision).theme,
            Theme::Dark
        );
        let inspected = store.read();
        let uncertain = store.update_with(Theme::Light, inspected.revision, |path, theme| {
            persist(path, theme, &Preferences::default())?;
            Err(AppearanceProblem::OutcomeUnknown)
        });
        assert_eq!(uncertain.theme, Theme::Dark);
        assert_eq!(uncertain.problem, Some(AppearanceProblem::OutcomeUnknown));
        assert_eq!(
            store.update(Theme::Dark, uncertain.revision).theme,
            Theme::Dark
        );
        let inspected = store.read();
        assert_eq!(inspected.theme, Theme::Light);
        assert_eq!(inspected.problem, None);
        assert_eq!(
            store.update(Theme::System, inspected.revision).problem,
            None
        );
    }

    #[test]
    fn unchanged_reads_keep_all_windows_on_the_same_revision() {
        let directory = tempfile::tempdir().unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(store.read(), initial);
        assert_eq!(store.update(Theme::Dark, initial.revision).problem, None);
    }

    #[cfg(unix)]
    #[test]
    fn linked_storage_is_preserved_without_touching_its_target() {
        let directory = tempfile::tempdir().unwrap();
        let target = directory.path().join("external.json");
        fs::write(&target, br#"{"version":1,"theme":"dark"}"#).unwrap();
        let original = fs::read(&target).unwrap();
        std::os::unix::fs::symlink(&target, directory.path().join("appearance.json")).unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(AppearanceProblem::InvalidDocument));
        store.update(Theme::Light, snapshot.revision);
        assert_eq!(fs::read(target).unwrap(), original);
        assert!(
            fs::symlink_metadata(directory.path().join("appearance.json"))
                .unwrap()
                .is_symlink()
        );
    }

    #[test]
    fn default_restart_and_stale_selection() {
        let directory = tempfile::tempdir().unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(initial.theme, Theme::System);
        assert_eq!(initial.problem, None);
        let dark = store.update(Theme::Dark, initial.revision);
        assert_eq!(dark.theme, Theme::Dark);
        assert_eq!(dark.problem, None);
        assert_eq!(
            store.update(Theme::Light, initial.revision).problem,
            Some(AppearanceProblem::Changed)
        );
        assert_eq!(store.read().theme, Theme::Dark);
        let restarted = AppearanceStore::new(Some(directory.path().into()));
        let observed = restarted.read();
        assert_eq!(observed.theme, Theme::Dark);
        assert_eq!(
            restarted.update(Theme::Light, observed.revision).theme,
            Theme::Light
        );
        assert_eq!(restarted.read().theme, Theme::Light);
    }

    #[test]
    fn invalid_and_newer_documents_remain_intact() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("appearance.json");
        for bytes in [
            b"broken".as_slice(),
            br#"{"version":2,"theme":"dark"}"#,
            br#"{"version":1,"theme":"other"}"#,
            br#"{"version":1,"theme":"dark","extra":true}"#,
        ] {
            fs::write(&path, bytes).unwrap();
            let store = AppearanceStore::new(Some(directory.path().into()));
            let observed = store.read();
            assert_eq!(observed.theme, Theme::System);
            assert!(observed.problem.is_some());
            assert_eq!(
                store.update(Theme::Light, observed.revision).theme,
                Theme::System
            );
            assert_eq!(fs::read(&path).unwrap(), bytes);
        }
    }

    #[test]
    fn external_change_requires_inspection_before_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let initial = store.read();
        fs::write(
            directory.path().join("appearance.json"),
            br#"{"version":1,"theme":"dark"}"#,
        )
        .unwrap();
        let rejected = store.update(Theme::Light, initial.revision);
        assert_eq!(rejected.theme, Theme::System);
        assert_eq!(rejected.problem, Some(AppearanceProblem::Changed));
        let observed = store.read();
        assert_eq!(observed.theme, Theme::Dark);
        assert_eq!(store.update(Theme::Light, observed.revision).problem, None);
    }

    #[test]
    fn write_failure_keeps_committed_selection_and_blocks_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let initial = store.read();
        let committed = store.update(Theme::Dark, initial.revision);
        fs::remove_file(directory.path().join("appearance.json")).unwrap();
        fs::remove_dir(directory.path()).unwrap();
        fs::write(directory.path(), b"not a directory").unwrap();
        let failed = store.update(Theme::Light, committed.revision);
        assert_eq!(failed.theme, Theme::Dark);
        assert!(failed.problem.is_some());
        assert_eq!(
            store.update(Theme::System, failed.revision).theme,
            Theme::Dark
        );
        fs::remove_file(directory.path()).unwrap();
    }
}
