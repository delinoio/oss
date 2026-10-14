//! Source path classification, display paths and the bundle source store.

use std::{
    collections::BTreeMap,
    path::{Component, Path, PathBuf},
};

use sha2::{Digest, Sha256};

use crate::model::{SourceFile, SourceOrigin};

pub fn sha256_hex(bytes: &[u8]) -> String {
    let digest = Sha256::digest(bytes);
    digest.iter().map(|byte| format!("{byte:02x}")).collect()
}

/// Lexically normalizes `.` and `..` components without touching the
/// filesystem.
pub fn normalize(path: &Path) -> PathBuf {
    let mut out = PathBuf::new();
    for component in path.components() {
        match component {
            Component::CurDir => {}
            Component::ParentDir => {
                if !out.pop() {
                    out.push("..");
                }
            }
            other => out.push(other.as_os_str()),
        }
    }
    out
}

fn to_slash(path: &Path) -> String {
    path.components()
        .map(|component| component.as_os_str().to_string_lossy().into_owned())
        .filter(|part| part != "/" && !part.is_empty())
        .collect::<Vec<_>>()
        .join("/")
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Classified {
    pub display: String,
    pub origin: SourceOrigin,
    /// Project-relative path without `..`, used only for stale checks.
    pub project_relative: Option<String>,
}

/// Classifies an absolute module path relative to the project root.
///
/// Paths under `node_modules` keep their full root-relative ancestry (for
/// example `node_modules/a/node_modules/b/index.js` or a pnpm store path), so
/// distinct installed copies of the same package get distinct display paths.
/// Paths outside the project root fall back to a `..`-relative display so no
/// absolute prefix is exposed.
pub fn classify(project_root: &Path, path: &Path) -> Classified {
    let normalized = normalize(path);
    let slash = normalized.to_string_lossy().replace('\\', "/");
    if slash.contains("/node_modules/") || slash.starts_with("node_modules/") {
        let display = match normalized.strip_prefix(project_root) {
            Ok(relative) => to_slash(relative),
            Err(_) => relative_display(project_root, &normalized),
        };
        return Classified {
            display,
            origin: SourceOrigin::Dependency,
            project_relative: None,
        };
    }
    if let Ok(relative) = normalized.strip_prefix(project_root) {
        let display = to_slash(relative);
        return Classified {
            display: display.clone(),
            origin: SourceOrigin::Project,
            project_relative: Some(display),
        };
    }
    // Outside the project root (for example a workspace sibling package).
    let display = relative_display(project_root, &normalized);
    Classified {
        display,
        origin: SourceOrigin::Dependency,
        project_relative: None,
    }
}

fn relative_display(base: &Path, target: &Path) -> String {
    let base_parts: Vec<_> = base.components().collect();
    let target_parts: Vec<_> = target.components().collect();
    let common = base_parts
        .iter()
        .zip(&target_parts)
        .take_while(|(left, right)| left == right)
        .count();
    let mut parts: Vec<String> = vec!["..".to_owned(); base_parts.len() - common];
    parts.extend(
        target_parts[common..]
            .iter()
            .map(|part| part.as_os_str().to_string_lossy().into_owned()),
    );
    parts.join("/")
}

/// Validates a stored project-relative path before any filesystem access.
pub fn safe_project_relative(path: &str) -> Option<PathBuf> {
    if path.is_empty() || path.contains('\\') || path.contains('\0') {
        return None;
    }
    let candidate = Path::new(path);
    if candidate.is_absolute() {
        return None;
    }
    let mut out = PathBuf::new();
    for component in candidate.components() {
        match component {
            Component::Normal(part) => out.push(part),
            _ => return None,
        }
    }
    Some(out)
}

pub fn source_id(display: &str) -> String {
    format!("src-{}", &sha256_hex(display.as_bytes())[..16])
}

/// Collects source files referenced by the analysis, keyed by display path.
#[derive(Debug, Default)]
pub struct SourceStore {
    pub include_content: bool,
    files: BTreeMap<String, SourceFile>,
    pub project_relative: BTreeMap<String, String>,
    pub unreadable: Vec<String>,
    /// Source text the bundler used, from source-map `sourcesContent`, keyed by
    /// resolved path. Preferred over the live file, which may have changed
    /// after the build read it.
    pub snapshots: std::collections::HashMap<PathBuf, String>,
    /// Display paths whose live file differs from the build snapshot.
    pub changed_since_build: Vec<String>,
    /// Module paths the bundler reported. Together with the project root
    /// they are the only live files read; any other path (for example one
    /// named only by a source map) relies on `sourcesContent` snapshots.
    pub allowed: std::collections::HashSet<PathBuf>,
}

/// Largest live source file read.
pub const MAX_SOURCE_BYTES: u64 = 16 * 1024 * 1024;

/// Prefix of the JavaScript stub Vite emits for an imported asset (for
/// example a PNG). Its `sourcesContent` is generated code, not the file text.
const VITE_ASSET_STUB_PREFIX: &str = "export default \"__VITE_ASSET__";

fn is_asset_stub(snapshot: &str) -> bool {
    snapshot.trim_start().starts_with(VITE_ASSET_STUB_PREFIX)
}

/// Returns the text of `bytes` when they are valid UTF-8 without NUL bytes.
fn as_text(bytes: &[u8]) -> Option<&str> {
    if bytes.contains(&0) {
        return None;
    }
    std::str::from_utf8(bytes).ok()
}

/// Live read outcome for one source path.
enum LiveRead {
    /// Raw bytes; they may not be text (for example an imported image).
    Bytes(Vec<u8>),
    /// Not vetted for reading (outside the project and not a module path,
    /// special file, or too large); never treated as a change.
    NotPermitted,
    Unreadable(std::io::Error),
}

impl SourceStore {
    pub fn new(include_content: bool) -> Self {
        Self {
            include_content,
            ..Self::default()
        }
    }

    /// Adds a file from disk and returns its source ID and text.
    ///
    /// The text is `None` for a non-text source (an imported image or a
    /// bundler-generated asset stub): it keeps its path and ID for
    /// attribution but has no content, no snippet source and no freshness
    /// check.
    pub fn add_file(
        &mut self,
        project_root: &Path,
        path: &Path,
    ) -> Option<(String, Option<String>)> {
        let classified = classify(project_root, path);
        // Key by the resolved path rather than the display path.
        let key = normalize(path).to_string_lossy().into_owned();
        if let Some(existing) = self.files.get(&key) {
            return Some((
                existing.id.clone(),
                existing.content_included.then(|| existing.content.clone()),
            ));
        }
        let live = self.read_live(project_root, path);
        let snapshot = self.snapshots.get(&normalize(path));
        let stub = snapshot.is_some_and(|snapshot| is_asset_stub(snapshot));
        let (text, sha256, stale): (Option<String>, String, bool) = match (snapshot, live) {
            // The live file is not text, or the snapshot is a generated asset
            // stub: the two cannot be compared, so the source is unverifiable
            // and never stale.
            (_, LiveRead::Bytes(bytes)) if stub || as_text(&bytes).is_none() => {
                tracing::debug!(path = %classified.display, "non-text source is not freshness-checked");
                (None, sha256_hex(&bytes), false)
            }
            (Some(snapshot), LiveRead::Bytes(bytes)) => (
                Some(snapshot.clone()),
                sha256_hex(snapshot.as_bytes()),
                as_text(&bytes) != Some(snapshot.as_str()),
            ),
            (Some(_), LiveRead::Unreadable(_)) if stub => (None, sha256_hex(b""), true),
            (Some(_), LiveRead::NotPermitted) if stub => (None, sha256_hex(b""), false),
            (Some(snapshot), LiveRead::Unreadable(_)) => (
                Some(snapshot.clone()),
                sha256_hex(snapshot.as_bytes()),
                true,
            ),
            (Some(snapshot), LiveRead::NotPermitted) => (
                Some(snapshot.clone()),
                sha256_hex(snapshot.as_bytes()),
                false,
            ),
            (None, LiveRead::Bytes(bytes)) => match as_text(&bytes) {
                Some(live) => (Some(live.to_owned()), sha256_hex(&bytes), false),
                None => (None, sha256_hex(&bytes), false),
            },
            (None, LiveRead::NotPermitted) => {
                tracing::debug!(path = %classified.display, "source not vetted for live reading");
                self.unreadable.push(classified.display);
                return None;
            }
            (None, LiveRead::Unreadable(error)) => {
                tracing::debug!(path = %classified.display, %error, "source file unreadable");
                self.unreadable.push(classified.display);
                return None;
            }
        };
        if stale {
            tracing::debug!(path = %classified.display, "source changed after the build read it");
            self.changed_since_build.push(classified.display.clone());
        }
        let id = source_id(&key);
        let verifiable = text.is_some();
        let file = SourceFile {
            id: id.clone(),
            path: classified.display.clone(),
            origin: classified.origin,
            sha256,
            content: text.clone().unwrap_or_default(),
            content_included: verifiable,
            stale,
        };
        // Only text sources are re-hashed against the live file when a saved
        // bundle is reopened.
        if let (true, Some(relative)) = (verifiable, classified.project_relative) {
            self.project_relative.insert(id.clone(), relative);
        }
        self.files.insert(key, file);
        Some((id, text))
    }

    /// Reads a live source only when it is a regular file of at most
    /// `MAX_SOURCE_BYTES` that lies inside the project root or is a module
    /// path the bundler reported.
    fn read_live(&self, project_root: &Path, path: &Path) -> LiveRead {
        let resolved = match std::fs::canonicalize(path) {
            Ok(resolved) => resolved,
            Err(error) => return LiveRead::Unreadable(error),
        };
        let root =
            std::fs::canonicalize(project_root).unwrap_or_else(|_| project_root.to_path_buf());
        let vetted = resolved.starts_with(&root)
            || self.allowed.contains(&normalize(path))
            || self.allowed.contains(&resolved);
        if !vetted {
            return LiveRead::NotPermitted;
        }
        let metadata = match std::fs::metadata(&resolved) {
            Ok(metadata) => metadata,
            Err(error) => return LiveRead::Unreadable(error),
        };
        if !metadata.is_file() || metadata.len() > MAX_SOURCE_BYTES {
            return LiveRead::NotPermitted;
        }
        let mut bytes = Vec::new();
        let read = std::fs::File::open(&resolved).and_then(|file| {
            std::io::Read::read_to_end(
                &mut std::io::Read::take(file, MAX_SOURCE_BYTES + 1),
                &mut bytes,
            )
        });
        match read {
            Ok(_) if bytes.len() as u64 <= MAX_SOURCE_BYTES => LiveRead::Bytes(bytes),
            Ok(_) => LiveRead::NotPermitted,
            Err(error) => LiveRead::Unreadable(error),
        }
    }

    pub fn into_files(self) -> Vec<SourceFile> {
        let include = self.include_content;
        self.files
            .into_values()
            .map(|mut file| {
                if !include {
                    file.content.clear();
                    file.content_included = false;
                }
                file
            })
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn classifies_project_dependency_and_external_paths() {
        let root = Path::new("/work/app");
        let project = classify(root, Path::new("/work/app/src/./main.ts"));
        assert_eq!(project.display, "src/main.ts");
        assert_eq!(project.origin, SourceOrigin::Project);
        assert_eq!(project.project_relative.as_deref(), Some("src/main.ts"));

        let dependency = classify(
            root,
            Path::new("/work/app/node_modules/.pnpm/lodash@4/node_modules/lodash/index.js"),
        );
        assert_eq!(
            dependency.display,
            "node_modules/.pnpm/lodash@4/node_modules/lodash/index.js"
        );
        assert_eq!(dependency.origin, SourceOrigin::Dependency);
        assert!(dependency.project_relative.is_none());

        let external = classify(root, Path::new("/work/shared/util.js"));
        assert_eq!(external.display, "../shared/util.js");
        assert!(external.project_relative.is_none());
    }

    #[test]
    fn nested_node_modules_copies_get_distinct_safe_displays() {
        let root = Path::new("/work/app");
        let top = classify(
            root,
            Path::new("/work/app/node_modules/@dnd-kit/sortable/dist/sortable.esm.js"),
        );
        let nested = classify(
            root,
            Path::new(
                "/work/app/node_modules/@douyinfe/semi-ui/node_modules/@dnd-kit/sortable/dist/\
                 sortable.esm.js",
            ),
        );
        assert_eq!(
            top.display,
            "node_modules/@dnd-kit/sortable/dist/sortable.esm.js"
        );
        assert_eq!(
            nested.display,
            "node_modules/@douyinfe/semi-ui/node_modules/@dnd-kit/sortable/dist/sortable.esm.js"
        );
        for classified in [&top, &nested] {
            assert_eq!(classified.origin, SourceOrigin::Dependency);
            assert!(classified.project_relative.is_none());
            assert!(!classified.display.starts_with('/'));
            assert!(!classified.display.contains("/work/app"));
        }
    }

    #[test]
    fn node_modules_outside_the_project_root_stay_relative() {
        let root = Path::new("/work/repo/app");
        let hoisted = classify(
            root,
            Path::new("/work/repo/node_modules/.pnpm/pkg@1/node_modules/pkg/index.js"),
        );
        assert_eq!(
            hoisted.display,
            "../node_modules/.pnpm/pkg@1/node_modules/pkg/index.js"
        );
        assert_eq!(hoisted.origin, SourceOrigin::Dependency);
        assert!(hoisted.project_relative.is_none());
        let workspace = classify(
            root,
            Path::new("/work/repo/app/packages/a/node_modules/x/i.js"),
        );
        assert_eq!(workspace.display, "packages/a/node_modules/x/i.js");
        assert_eq!(workspace.origin, SourceOrigin::Dependency);
    }

    #[test]
    fn rejects_unsafe_project_relative_paths() {
        assert!(safe_project_relative("src/main.ts").is_some());
        for unsafe_path in ["", "../secret", "/etc/passwd", "src/../../x", "a\\b", "./x"] {
            assert!(
                safe_project_relative(unsafe_path).is_none(),
                "{unsafe_path}"
            );
        }
    }

    #[test]
    fn distinct_pnpm_copies_have_distinct_displays_and_ids() {
        let dir = tempfile::tempdir().unwrap();
        let mut paths = Vec::new();
        for (version, body) in [
            ("pkg@1", "export const v = 1;"),
            ("pkg@2", "export const v = 2;"),
        ] {
            let package = dir
                .path()
                .join("node_modules/.pnpm")
                .join(version)
                .join("node_modules/pkg");
            std::fs::create_dir_all(&package).unwrap();
            std::fs::write(package.join("index.js"), body).unwrap();
            paths.push(package.join("index.js"));
        }
        let mut store = SourceStore::new(true);
        let first = store.add_file(dir.path(), &paths[0]).unwrap();
        let second = store.add_file(dir.path(), &paths[1]).unwrap();
        assert_ne!(first.0, second.0);
        assert_eq!(second.1.as_deref(), Some("export const v = 2;"));
        let files = store.into_files();
        assert_eq!(files.len(), 2);
        let mut displays: Vec<&str> = files.iter().map(|file| file.path.as_str()).collect();
        displays.sort_unstable();
        assert_eq!(
            displays,
            vec![
                "node_modules/.pnpm/pkg@1/node_modules/pkg/index.js",
                "node_modules/.pnpm/pkg@2/node_modules/pkg/index.js",
            ]
        );
    }

    #[test]
    fn build_snapshots_win_over_changed_live_files() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("a.js");
        std::fs::write(&file, "edited after the build").unwrap();
        let mut store = SourceStore::new(true);
        store
            .snapshots
            .insert(normalize(&file), "what the build read".to_owned());
        let (_, content) = store.add_file(dir.path(), &file).unwrap();
        assert_eq!(content.as_deref(), Some("what the build read"));
        assert_eq!(store.changed_since_build, vec!["a.js".to_owned()]);
        let files = store.into_files();
        assert!(files[0].stale);
        assert_eq!(files[0].sha256, sha256_hex(b"what the build read"));
    }

    #[cfg(unix)]
    #[test]
    fn live_reads_are_limited_to_vetted_regular_files() {
        let project = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        let secret = outside.path().join("secret.txt");
        std::fs::write(&secret, "token").unwrap();
        let mut store = SourceStore::new(true);
        // A path outside the project that the bundler never reported.
        assert!(store.add_file(project.path(), &secret).is_none());
        // Special files are never read.
        store.allowed.insert(PathBuf::from("/dev/zero"));
        assert!(
            store
                .add_file(project.path(), Path::new("/dev/zero"))
                .is_none()
        );
        // A reported module path outside the project is read.
        let module = outside.path().join("module.js");
        std::fs::write(&module, "export {}").unwrap();
        store.allowed.insert(normalize(&module));
        assert_eq!(
            store
                .add_file(project.path(), &module)
                .unwrap()
                .1
                .as_deref(),
            Some("export {}")
        );
    }

    #[test]
    fn excluded_content_keeps_hashes() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("a.js");
        std::fs::write(&file, "export const a = 1;").unwrap();
        let mut store = SourceStore::new(false);
        store.add_file(dir.path(), &file).unwrap();
        let files = store.into_files();
        assert_eq!(files.len(), 1);
        assert!(files[0].content.is_empty());
        assert!(!files[0].content_included);
        assert_eq!(files[0].sha256, sha256_hex(b"export const a = 1;"));
    }

    const PNG_BYTES: &[u8] = &[
        0x89, b'P', b'N', b'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0xff, 0xfe,
    ];
    const VITE_STUB: &str = "export default \"__VITE_ASSET__abc123__\"";

    #[test]
    fn unchanged_png_with_a_vite_stub_snapshot_is_not_stale() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("src/logo.png");
        std::fs::create_dir_all(file.parent().unwrap()).unwrap();
        std::fs::write(&file, PNG_BYTES).unwrap();
        let mut store = SourceStore::new(true);
        store
            .snapshots
            .insert(normalize(&file), VITE_STUB.to_owned());
        let (id, text) = store.add_file(dir.path(), &file).unwrap();
        // No snippet source, but a stable ID and the asset path remain.
        assert_eq!(text, None);
        assert_eq!(id, source_id(&normalize(&file).to_string_lossy()));
        // Looking it up again keeps the same answer.
        assert_eq!(
            store.add_file(dir.path(), &file).unwrap(),
            (id.clone(), None)
        );
        assert!(store.changed_since_build.is_empty());
        // Saved-run reload re-hashes only sources listed here.
        assert!(store.project_relative.is_empty());
        let files = store.into_files();
        assert_eq!(files.len(), 1);
        assert_eq!(files[0].id, id);
        assert_eq!(files[0].path, "src/logo.png");
        assert!(!files[0].stale);
        assert!(!files[0].content_included);
        assert!(files[0].content.is_empty());
        assert_eq!(files[0].sha256, sha256_hex(PNG_BYTES));
    }

    #[test]
    fn binary_files_without_a_snapshot_are_not_text_sources() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("logo.png");
        std::fs::write(&file, PNG_BYTES).unwrap();
        let mut store = SourceStore::new(true);
        let (_, text) = store.add_file(dir.path(), &file).unwrap();
        assert_eq!(text, None);
        assert!(store.changed_since_build.is_empty());
        assert!(store.project_relative.is_empty());
        assert!(!store.into_files()[0].stale);
    }

    #[test]
    fn text_asset_stub_snapshots_are_not_compared_with_live_text() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("icon.svg");
        std::fs::write(&file, "<svg xmlns=\"http://www.w3.org/2000/svg\"/>").unwrap();
        let mut store = SourceStore::new(true);
        store
            .snapshots
            .insert(normalize(&file), VITE_STUB.to_owned());
        assert_eq!(store.add_file(dir.path(), &file).unwrap().1, None);
        assert!(store.changed_since_build.is_empty());
        assert!(store.project_relative.is_empty());
    }

    #[test]
    fn removed_asset_stub_sources_are_still_stale() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("gone.png");
        let mut store = SourceStore::new(true);
        store
            .snapshots
            .insert(normalize(&file), VITE_STUB.to_owned());
        assert_eq!(store.add_file(dir.path(), &file).unwrap().1, None);
        assert_eq!(store.changed_since_build, vec!["gone.png".to_owned()]);
        let files = store.into_files();
        assert!(files[0].stale);
        assert!(files[0].content.is_empty());
    }

    #[test]
    fn changed_removed_and_unchanged_text_sources_keep_their_stale_state() {
        let dir = tempfile::tempdir().unwrap();
        let changed = dir.path().join("changed.js");
        let same = dir.path().join("same.js");
        let removed = dir.path().join("removed.js");
        std::fs::write(&changed, "edited").unwrap();
        std::fs::write(&same, "export const same = 1;").unwrap();
        let mut store = SourceStore::new(true);
        for (path, snapshot) in [
            (&changed, "built"),
            (&same, "export const same = 1;"),
            (&removed, "built"),
        ] {
            store.snapshots.insert(normalize(path), snapshot.to_owned());
        }
        for path in [&changed, &same, &removed] {
            assert!(store.add_file(dir.path(), path).unwrap().1.is_some());
        }
        assert_eq!(
            store.changed_since_build,
            vec!["changed.js".to_owned(), "removed.js".to_owned()]
        );
        // Text sources stay in the saved-run stale check.
        assert_eq!(store.project_relative.len(), 3);
    }
}
