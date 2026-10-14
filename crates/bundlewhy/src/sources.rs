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

/// Live read outcome for one source path.
enum LiveRead {
    Content(String),
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

    /// Adds a file from disk and returns its source ID and content.
    pub fn add_file(&mut self, project_root: &Path, path: &Path) -> Option<(String, String)> {
        let classified = classify(project_root, path);
        // Key by the resolved path rather than the display path.
        let key = normalize(path).to_string_lossy().into_owned();
        if let Some(existing) = self.files.get(&key) {
            return Some((existing.id.clone(), existing.content.clone()));
        }
        let live = self.read_live(project_root, path);
        let (content, stale) = match (self.snapshots.get(&normalize(path)), live) {
            (Some(snapshot), LiveRead::Content(live)) => (snapshot.clone(), *snapshot != live),
            (Some(snapshot), LiveRead::Unreadable(_)) => (snapshot.clone(), true),
            (Some(snapshot), LiveRead::NotPermitted) => (snapshot.clone(), false),
            (None, LiveRead::Content(live)) => (live, false),
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
        let file = SourceFile {
            id: id.clone(),
            path: classified.display.clone(),
            origin: classified.origin,
            sha256: sha256_hex(content.as_bytes()),
            content: content.clone(),
            content_included: true,
            stale,
        };
        if let Some(relative) = classified.project_relative {
            self.project_relative.insert(id.clone(), relative);
        }
        self.files.insert(key, file);
        Some((id, content))
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
            Ok(_) if bytes.len() as u64 <= MAX_SOURCE_BYTES => {
                LiveRead::Content(String::from_utf8_lossy(&bytes).into_owned())
            }
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
        assert_eq!(second.1, "export const v = 2;");
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
        assert_eq!(content, "what the build read");
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
            store.add_file(project.path(), &module).unwrap().1,
            "export {}"
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
}
