//! Source path classification, display paths and the bundle source store.

use std::{
    collections::{BTreeMap, HashSet},
    path::{Component, Path, PathBuf},
};

use sha2::{Digest, Sha256};

use crate::{
    error::{ErrorCode, problem},
    model::{Problem, ProblemSeverity, SourceFile, SourceOrigin, SourceRevisionEvidence},
};

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

/// Which bundler-specific rule decides that a text file without a build
/// snapshot is a non-executable asset module instead of executable source.
///
/// Vite imports `.svg` files and `?raw`, `?url`, `?inline` and `?no-inline`
/// queries as URL or string modules. Other engines load the same paths through
/// user-configured loaders (for example an SVG-to-component loader) that
/// produce executable code, so the Vite rule must not apply to them.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum AssetExemption {
    /// No path-based exemption: every readable text module without a build
    /// snapshot is a coverage gap unless the bundler reported it as an asset
    /// module (see `SourceStore::asset_modules`).
    #[default]
    None,
    /// Vite URL and string asset semantics for known asset paths.
    ViteUrlModules,
}

/// Collects source files referenced by the analysis, keyed by display path.
#[derive(Debug, Default)]
pub struct SourceStore {
    pub include_content: bool,
    /// Path-based asset rule for modules without a build snapshot. Defaults to
    /// the stricter [`AssetExemption::None`].
    pub asset_exemption: AssetExemption,
    /// Resolved paths of modules the bundler itself classified as asset
    /// modules (for example Rspack `asset/resource`). Their output is a URL or
    /// string, never executable source text.
    pub asset_modules: HashSet<PathBuf>,
    files: BTreeMap<String, SourceFile>,
    pub project_relative: BTreeMap<String, String>,
    /// Source ID to project-relative path for sources whose content cannot be
    /// compared with the live file (generated asset modules, binary files and
    /// modules derived through an import query). A saved run only checks that
    /// the file still exists.
    pub existence_relative: BTreeMap<String, String>,
    pub unreadable: Vec<String>,
    /// Readable text with no build-time snapshot. Live bytes cannot prove
    /// which revision the bundler analyzed.
    pub unverified: Vec<String>,
    /// Cached source paths that were confirmed readable text without a
    /// build-time snapshot. Other unavailable sources, such as binary assets
    /// and generated asset stubs, must not become coverage gaps on a later
    /// detector visit.
    readable_without_snapshot: HashSet<String>,
    /// Source text the bundler used, from source-map `sourcesContent`, keyed by
    /// resolved path. Preferred over the live file, which may have changed
    /// after the build read it.
    pub snapshots: std::collections::HashMap<PathBuf, String>,
    /// Display paths whose live file differs from the build snapshot.
    pub changed_since_build: Vec<String>,
    /// Display paths that have a build snapshot but whose live file could not
    /// be read for a reason other than removal. The snapshot stays usable and
    /// the source is not stale: only its freshness is unverified.
    pub freshness_unverified: Vec<String>,
    /// Display paths without a build snapshot (an imported asset, a generated
    /// asset module) whose backing file could not be inspected for a reason
    /// other than removal. Nothing was compared, so no build text is claimed:
    /// only the existence of the file is unverified.
    pub existence_unverified: Vec<String>,
    /// Test seam: live reads of these resolved paths fail with the given
    /// error kind instead of touching the filesystem.
    #[cfg(test)]
    live_read_failures: std::collections::HashMap<PathBuf, std::io::ErrorKind>,
    /// Module paths the bundler reported. Together with the project root
    /// they are the only live files read; any other path (for example one
    /// named only by a source map) relies on `sourcesContent` snapshots.
    pub allowed: std::collections::HashSet<PathBuf>,
}

/// Largest live source file read.
pub const MAX_SOURCE_BYTES: u64 = 16 * 1024 * 1024;

// The stub of an imported asset in builds before the Rolldown-based Vite
// releases is `export default "__VITE_ASSET__<id>__"`. It is a single string
// literal, so the string-literal rule of `is_generated_asset_module` covers it.

/// Prefix of the file URL placeholder Rolldown-based Vite releases write into
/// the stub of an emitted asset: `export default
/// import.meta.ROLLDOWN_FILE_URL_<id>`.
const ROLLDOWN_FILE_URL_PREFIX: &str = "import.meta.ROLLDOWN_FILE_URL_";

/// Extensions of files that hold JavaScript-family source. A string-only
/// default export in such a file is ordinary code, not a generated module.
const SCRIPT_EXTENSIONS: &[&str] = &[
    "js", "jsx", "mjs", "cjs", "ts", "tsx", "mts", "cts", "vue", "svelte", "astro",
];

/// Extensions of binary files a bundler emits as assets. Such a file is never
/// the text a bundler read as source.
const BINARY_ASSET_EXTENSIONS: &[&str] = &[
    "png", "jpg", "jpeg", "gif", "webp", "avif", "ico", "bmp", "tiff", "woff", "woff2", "ttf",
    "otf", "eot", "mp3", "mp4", "webm", "ogg", "wav", "flac", "aac", "pdf",
];

/// Text assets that Vite imports as URL modules by default. Their file text
/// is not the JavaScript module executed by the browser.
const TEXT_ASSET_EXTENSIONS: &[&str] = &["svg"];

/// Import query keys after which Vite turns a file into a string module: the
/// file URL (`?url`, `?no-inline`), the inlined data URL (`?inline`) or the
/// file text (`?raw`).
const ASSET_QUERY_KEYS: &[&str] = &["raw", "url", "inline", "no-inline"];

/// The parts of one bundler resource string. This is the single place that
/// decides which suffix of a module identifier or source-map source is
/// identity and which part names the backing file; every identity comparison
/// (collected modules, source-map sources, shared-file counting) goes through
/// [`split_resource`].
///
/// | input                  | file              | query         | fragment |
/// |------------------------|-------------------|---------------|----------|
/// | `/app/a.js`            | `/app/a.js`       | none          | none     |
/// | `/app/a.js?raw`        | `/app/a.js`       | `raw`         | none     |
/// | `/app/a.js?raw#one`    | `/app/a.js`       | `raw`         | `one`    |
/// | `/app/a.js#one`        | `/app/a.js`       | none          | `one`    |
/// | `/app/a.js#one?raw`    | `/app/a.js`       | none          | `one?raw`|
/// | `/app/a.js?`           | `/app/a.js`       | empty         | none     |
/// | `/app/a.js#`           | `/app/a.js`       | none          | empty    |
/// | `\0virtual:a?x#y`      | `virtual:a`       | `x`           | `y`      |
/// | `C:\app\a.vue?x#y`    | `C:\app\a.vue`    | `x`           | `y`      |
///
/// - The query starts at the first `?` and ends at the first `#` after it. The
///   fragment starts at the first `#` and runs to the end, so a `?` after a `#`
///   belongs to the fragment (URL syntax).
/// - Query and fragment are both part of the resource identity: bundlers derive
///   different modules from one file by either (`Comp.vue?vue&type=style`,
///   `a.js?raw#one` and `a.js?raw#two` are three resources). Only the file part
///   names the backing file.
/// - Leading NUL bytes are the Rollup/Vite marker of a virtual module. They are
///   not part of the name, and `virtual_module` records that the resource has
///   no backing file.
/// - A Windows path (drive letter or UNC prefix) uses `\` as separator, and
///   [`ResourceParts::file_path`] treats it as `/` on every platform so a
///   source map written on Windows compares equal to the collected module path.
///   A POSIX path keeps `\` as an ordinary file-name character.
/// - Loader prefixes (`css-loader!/app/a.vue`) are not part of this split; see
///   [`strip_loader_prefix`].
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) struct ResourceParts<'a> {
    pub file: &'a str,
    pub query: Option<&'a str>,
    pub fragment: Option<&'a str>,
    pub virtual_module: bool,
}

impl ResourceParts<'_> {
    /// The lexically normalized backing file path, with the Windows separator
    /// rule of the table above applied.
    pub fn file_path(&self) -> PathBuf {
        if is_windows_path(self.file) {
            normalize(Path::new(&self.file.replace('\\', "/")))
        } else {
            normalize(Path::new(self.file))
        }
    }
}

/// True for a Windows drive-letter (`C:\`, `C:/`) or UNC (`\\host`) path.
fn is_windows_path(text: &str) -> bool {
    let bytes = text.as_bytes();
    let drive = bytes.len() >= 3
        && bytes[0].is_ascii_alphabetic()
        && bytes[1] == b':'
        && matches!(bytes[2], b'\\' | b'/');
    drive || text.starts_with("\\\\")
}

/// Splits a resource string into file, query and fragment (see
/// [`ResourceParts`]).
pub(crate) fn split_resource(text: &str) -> ResourceParts<'_> {
    let stripped = strip_nul_prefix(text);
    let virtual_module = stripped.len() != text.len();
    let (before_fragment, fragment) = match stripped.split_once('#') {
        Some((head, fragment)) => (head, Some(fragment)),
        None => (stripped, None),
    };
    let (file, query) = match before_fragment.split_once('?') {
        Some((file, query)) => (file, Some(query)),
        None => (before_fragment, None),
    };
    ResourceParts {
        file,
        query,
        fragment,
        virtual_module,
    }
}

/// Removes the leading NUL bytes that mark a virtual module identifier.
pub(crate) fn strip_nul_prefix(text: &str) -> &str {
    text.trim_start_matches('\0')
}

/// Returns the resource after the last loader separator (`css-loader!/a.vue`).
pub(crate) fn strip_loader_prefix(id: &str) -> &str {
    id.rsplit_once('!').map_or(id, |(_, resource)| resource)
}

/// Splits a module path at its first `?` into the file part and the import
/// query. A `#fragment` ends the query and is not part of the file part.
pub(crate) fn split_query(path: &str) -> (&str, Option<&str>) {
    let parts = split_resource(path);
    (parts.file, parts.query)
}

/// True when `path` carries an import query, which makes it a module derived
/// from the file instead of the file itself (for example `a.js?raw`).
pub fn has_import_query(path: &str) -> bool {
    split_query(path).1.is_some()
}

/// True when the import query of `path` selects a string module (`?raw`,
/// `?url`, `?inline` or `?no-inline`, alone or combined with other keys).
fn has_asset_query(path: &str) -> bool {
    split_query(path).1.is_some_and(|query| {
        query.split('&').any(|part| {
            let key = part.split_once('=').map_or(part, |(key, _)| key);
            ASSET_QUERY_KEYS.contains(&key)
        })
    })
}

/// Returns the lowercase extension of the file name of `path`, ignoring any
/// `?query` and `#fragment` suffix. This reads a file name on disk, not a
/// resource identity: a `#` inside the stem (`C#.png`) stays part of the name,
/// and a fragment after the extension (`logo.svg#icon`) is cut off.
fn extension_of(path: &str) -> Option<String> {
    let file = path.split_once('?').map_or(path, |(file, _)| file);
    let name = file.rsplit(['/', '\\']).next().unwrap_or(file);
    name.rsplit_once('.')
        .filter(|(stem, _)| !stem.is_empty())
        .map(|(_, extension)| {
            extension
                .split_once('#')
                .map_or(extension, |(extension, _)| extension)
                .to_ascii_lowercase()
        })
        .filter(|extension| !extension.is_empty())
}

/// True when `path` names a binary asset file (for example `logo.png`).
pub fn is_binary_asset_path(path: &str) -> bool {
    extension_of(path)
        .is_some_and(|extension| BINARY_ASSET_EXTENSIONS.contains(&extension.as_str()))
}

/// True when the module at `path` can be a bundler-generated asset module: it
/// is not a JavaScript-family file, or its import query selects a string
/// module. A script without such a query is ordinary code.
fn is_asset_module_path(path: &str) -> bool {
    has_asset_query(path)
        || extension_of(path)
            .is_none_or(|extension| !SCRIPT_EXTENSIONS.contains(&extension.as_str()))
}

fn is_known_asset_path(path: &str) -> bool {
    has_asset_query(path)
        || is_binary_asset_path(path)
        || extension_of(path)
            .is_some_and(|extension| TEXT_ASSET_EXTENSIONS.contains(&extension.as_str()))
}

/// True when `snapshot` is the module code a bundler generated for an imported
/// asset instead of the text of the file at `path`.
///
/// Recognized shapes, all of the form `export default <expression>`, and only
/// when the whole expression is the shape (anything after it, such as
/// `+ suffix`, makes the module ordinary code):
/// - a single string literal: the `"__VITE_ASSET__<id>__"` marker, an inlined
///   `data:` URL, a `?url` path, or the text of a `?raw` or `?inline` import;
/// - the `import.meta.ROLLDOWN_FILE_URL_<id>` placeholder of an emitted asset.
///
/// Every shape additionally requires an asset path (see
/// [`is_asset_module_path`]): a string-only default export in an ordinary
/// script, including one that starts with the `__VITE_ASSET__` marker text,
/// stays a source snapshot.
pub fn is_generated_asset_module(path: &str, snapshot: &str) -> bool {
    let code = snapshot.trim();
    let code = code.strip_suffix(';').unwrap_or(code).trim_end();
    let Some(expression) = code.strip_prefix("export default") else {
        return false;
    };
    if !expression.starts_with(char::is_whitespace) {
        return false;
    }
    let expression = expression.trim_start();
    let shape = if let Some(placeholder) = expression.strip_prefix(ROLLDOWN_FILE_URL_PREFIX) {
        !placeholder.is_empty()
            && placeholder
                .chars()
                .all(|ch| ch.is_ascii_alphanumeric() || ch == '_' || ch == '$')
    } else {
        serde_json::from_str::<String>(expression).is_ok()
    };
    shape && is_asset_module_path(path)
}

/// Names a path in a log line without exposing it: `module-` plus the first 16
/// hex digits of the SHA-256 of the text. Use it where only an absolute or
/// query-bearing path is available and no display path exists.
pub fn log_label(text: &str) -> String {
    format!("module-{}", &sha256_hex(text.as_bytes())[..16])
}

/// Resolves the file that backs a module path for filesystem access. Bundlers
/// append an import query (`a.js?raw`) or a fragment to the file path to name a
/// derived module; the query is not part of the file name. The literal path
/// wins when it exists (a file name can contain `?` or `#`); otherwise the path
/// without its query, then without its fragment, is used.
///
/// Every candidate is classified through [`probe_entry`]. Only a candidate that
/// is positively found is selected. A candidate whose existence cannot be
/// established (a transient permission or I/O failure) is never selected over
/// a later one, and when no candidate is found the fragment-free path is
/// returned, so a restored file is looked up by its real name. A file whose
/// literal name contains `?` or `#` and whose existence cannot be established
/// is therefore looked up by the query-free name (see the known limits of the
/// bundlewhy foundation contract). The module identity keeps the literal path.
pub fn filesystem_path(path: &Path) -> PathBuf {
    let text = path.to_string_lossy();
    if !text.contains(['?', '#']) {
        return path.to_path_buf();
    }
    let without_query = text.split_once('?').map_or(&*text, |(file, _)| file);
    let without_fragment = without_query
        .split_once('#')
        .map_or(without_query, |(file, _)| file);
    let candidates = [&*text, without_query, without_fragment];
    for candidate in candidates {
        match probe_entry(Path::new(candidate)) {
            Ok(()) => return PathBuf::from(candidate),
            Err(LiveFileFailure::Missing) => {}
            Err(failure) => {
                tracing::debug!(
                    candidate = %log_label(candidate),
                    ?failure,
                    "backing file candidate cannot be inspected; trying the next candidate"
                );
            }
        }
    }
    PathBuf::from(without_fragment)
}

/// Builds the single `BW_SOURCE_FRESHNESS_UNVERIFIED` warning for a run, on the
/// first run and when a saved run is reopened.
///
/// `snapshot_backed` counts sources that keep build snapshot text and whose
/// live file could not be read (the analysis uses that text). `existence_only`
/// counts sources without build snapshot text (binary assets, generated asset
/// modules) whose backing file could not be inspected. The wording claims a
/// build snapshot only for the first group. Returns `None` when both counts are
/// zero.
pub fn freshness_unverified_problem(
    snapshot_backed: usize,
    existence_only: usize,
) -> Option<Problem> {
    let (message, recovery) = match (snapshot_backed, existence_only) {
        (0, 0) => return None,
        (snapshot_backed, 0) => (
            format!(
                "freshness of {snapshot_backed} source file(s) could not be checked; analysis \
                 uses the build snapshot"
            ),
            "The live files could not be read, so bundlewhy cannot tell whether they changed \
             after the build. Results use the source text from the build's source maps."
                .to_owned(),
        ),
        (0, existence_only) => (
            format!(
                "existence of {existence_only} source file(s) without a build snapshot could not \
                 be checked"
            ),
            "The live files could not be inspected, so bundlewhy cannot tell whether they were \
             removed after the build. These sources have no build snapshot text; only their path \
             is kept."
                .to_owned(),
        ),
        (snapshot_backed, existence_only) => (
            format!(
                "freshness of {snapshot_backed} source file(s) could not be checked; analysis \
                 uses the build snapshot. Existence of {existence_only} source file(s) without a \
                 build snapshot could not be checked either"
            ),
            "The live files could not be read, so bundlewhy cannot tell whether they changed or \
             were removed after the build. Results use the source text from the build's source \
             maps where it exists; sources without a build snapshot keep only their path."
                .to_owned(),
        ),
    };
    Some(problem(
        ErrorCode::SourceFreshnessUnverified,
        ProblemSeverity::Warning,
        message,
        recovery,
    ))
}

/// Why the live file behind a source could not be read. Every filesystem
/// failure that decides whether a source is removed, changed or merely
/// unverifiable is classified through this type, on the first run and when a
/// saved run is reopened.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LiveFileFailure {
    /// The file does not exist (`NotFound`). This is the only failure that
    /// proves the file was removed.
    Missing,
    /// The file was not vetted for reading (it leaves the allowed area, is not
    /// a regular file, or is too large). It is never a change.
    NotPermitted,
    /// Any other I/O failure (permission denied, not a directory, interrupted,
    /// ...). The file may still exist and match the analysis, so no change can
    /// be claimed.
    Unreadable(std::io::ErrorKind),
}

impl LiveFileFailure {
    /// Classifies an I/O error kind.
    pub fn from_kind(kind: std::io::ErrorKind) -> Self {
        match kind {
            std::io::ErrorKind::NotFound => Self::Missing,
            other => Self::Unreadable(other),
        }
    }

    /// Classifies an I/O error.
    pub fn from_io(error: &std::io::Error) -> Self {
        Self::from_kind(error.kind())
    }

    /// True only when the failure proves that the file was removed.
    pub fn is_removal(self) -> bool {
        self == Self::Missing
    }
}

/// Checks that the directory entry itself exists, without following a final
/// symlink. A dangling symlink is present (`Ok`); only an entry that is
/// absent is [`LiveFileFailure::Missing`].
pub fn probe_entry_nofollow(path: &Path) -> Result<(), LiveFileFailure> {
    std::fs::symlink_metadata(path)
        .map(|_| ())
        .map_err(|error| LiveFileFailure::from_io(&error))
}

/// Checks that the file behind a path entry exists. A final symlink is
/// followed, so a symlink whose target was removed is
/// [`LiveFileFailure::Missing`] exactly as it is when the file is read.
pub fn probe_entry(path: &Path) -> Result<(), LiveFileFailure> {
    std::fs::metadata(path)
        .map(|_| ())
        .map_err(|error| LiveFileFailure::from_io(&error))
}

/// Resolves a path (after symlinks) and checks that it is accepted by `vet`
/// and is a regular file of at most `max_bytes`. Every failure is classified
/// as a `LiveFileFailure`. Reads and existence probes of vetted files share
/// this check so both classify the same cases as `NotPermitted`.
fn resolve_vetted(
    path: &Path,
    vet: impl FnOnce(&Path) -> bool,
    max_bytes: u64,
) -> Result<PathBuf, LiveFileFailure> {
    let resolved = std::fs::canonicalize(path).map_err(|error| LiveFileFailure::from_io(&error))?;
    if !vet(&resolved) {
        return Err(LiveFileFailure::NotPermitted);
    }
    let metadata =
        std::fs::metadata(&resolved).map_err(|error| LiveFileFailure::from_io(&error))?;
    if !metadata.is_file() || metadata.len() > max_bytes {
        return Err(LiveFileFailure::NotPermitted);
    }
    Ok(resolved)
}

/// Checks that the file behind a path exists and is vetted like a read: it
/// resolves (after symlinks) to a path accepted by `vet` and is a regular file
/// of at most `max_bytes`. The content is not read.
pub fn probe_vetted_file(
    path: &Path,
    vet: impl FnOnce(&Path) -> bool,
    max_bytes: u64,
) -> Result<(), LiveFileFailure> {
    resolve_vetted(path, vet, max_bytes).map(|_| ())
}

/// Reads a file only when it resolves (after symlinks) to a path accepted by
/// `vet` and is a regular file of at most `max_bytes`. Every failure is
/// classified as a `LiveFileFailure`, including a file removed or replaced
/// between the metadata check and the read.
pub fn read_vetted_file(
    path: &Path,
    vet: impl FnOnce(&Path) -> bool,
    max_bytes: u64,
) -> Result<Vec<u8>, LiveFileFailure> {
    let resolved = resolve_vetted(path, vet, max_bytes)?;
    let mut bytes = Vec::new();
    std::fs::File::open(&resolved)
        .and_then(|file| {
            std::io::Read::read_to_end(&mut std::io::Read::take(file, max_bytes + 1), &mut bytes)
        })
        .map_err(|error| LiveFileFailure::from_io(&error))?;
    if bytes.len() as u64 > max_bytes {
        return Err(LiveFileFailure::NotPermitted);
    }
    Ok(bytes)
}

/// Returns the text of `bytes` when they are valid UTF-8 without NUL bytes.
pub fn as_text(bytes: &[u8]) -> Option<&str> {
    if bytes.contains(&0) {
        return None;
    }
    std::str::from_utf8(bytes).ok()
}

/// Live read outcome for one source path.
enum LiveRead {
    /// Raw bytes; they may not be text (for example an imported image).
    Bytes(Vec<u8>),
    /// The read failed; `LiveFileFailure::NotPermitted` (outside the project
    /// and not a module path, special file, or too large) is never treated as
    /// a change.
    Failed(LiveFileFailure),
}

impl SourceStore {
    pub fn new(include_content: bool) -> Self {
        Self {
            include_content,
            ..Self::default()
        }
    }

    /// Sets the path-based asset rule applied to modules without a snapshot.
    pub fn with_asset_exemption(mut self, asset_exemption: AssetExemption) -> Self {
        self.asset_exemption = asset_exemption;
        self
    }

    /// True when a readable text module without a build snapshot is known to
    /// be a non-executable asset module for the selected engine.
    fn is_exempt_asset(&self, display: &str, path: &Path) -> bool {
        self.asset_modules.contains(&normalize(path))
            || (self.asset_exemption == AssetExemption::ViteUrlModules
                && is_known_asset_path(display))
    }

    /// Adds a source and returns its ID and verified build text, if available.
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
        self.add_file_with_coverage_gap(project_root, path, true)
    }

    /// Adds a source for attribution without counting a missing build snapshot
    /// as a coverage gap. Use this for configuration-only locations that no
    /// detector scans as source evidence.
    pub fn add_file_without_coverage_gap(
        &mut self,
        project_root: &Path,
        path: &Path,
    ) -> Option<(String, Option<String>)> {
        self.add_file_with_coverage_gap(project_root, path, false)
    }

    fn add_file_with_coverage_gap(
        &mut self,
        project_root: &Path,
        path: &Path,
        track_coverage_gap: bool,
    ) -> Option<(String, Option<String>)> {
        let classified = classify(project_root, path);
        // Key by the resolved path rather than the display path.
        let key = normalize(path).to_string_lossy().into_owned();
        if let Some(existing) = self.files.get(&key) {
            let id = existing.id.clone();
            let content = existing.content_included.then(|| existing.content.clone());
            let existing_path = existing.path.clone();
            if track_coverage_gap
                && self.readable_without_snapshot.contains(&existing_path)
                && !self.unverified.iter().any(|path| path == &existing_path)
            {
                tracing::debug!(path = %existing_path, "promoting cached unavailable source to coverage gap");
                self.unverified.push(existing_path);
            }
            return Some((id, content));
        }
        // The module path may carry an import query that is not part of the
        // file name; filesystem access uses the backing file.
        let file_path = filesystem_path(path);
        let live = self.read_live(project_root, path, &file_path);
        let snapshot = self.snapshots.get(&normalize(path));
        let stub = snapshot
            .is_some_and(|snapshot| is_generated_asset_module(&classified.display, snapshot));
        let derived = has_import_query(&classified.display);
        // `existence_only`: the stored hash cannot be compared with the live
        // file, so a saved run only checks that the file still exists.
        let mut unverified_freshness = false;
        let (text, sha256, stale, existence_only): (Option<String>, String, bool, bool) = match (
            snapshot, live,
        ) {
            // The snapshot is generated asset module code, or there is no
            // snapshot and the live file is not text: the two cannot be
            // compared, so the source is unverifiable and never stale.
            (Some(_), LiveRead::Bytes(_)) if stub => {
                tracing::debug!(path = %classified.display, "generated asset module is not freshness-checked");
                (None, String::new(), false, true)
            }
            (None, LiveRead::Bytes(bytes)) if as_text(&bytes).is_none() => {
                tracing::debug!(path = %classified.display, "non-text source is not freshness-checked");
                (None, String::new(), false, true)
            }
            // A module derived through an import query (for example
            // `Comp.vue?vue&type=style`) holds transformed code, which is
            // not expected to equal the file.
            (Some(snapshot), LiveRead::Bytes(_)) if derived => {
                tracing::debug!(path = %classified.display, "derived module is not content-checked");
                (
                    Some(snapshot.clone()),
                    sha256_hex(snapshot.as_bytes()),
                    false,
                    true,
                )
            }
            // A text snapshot that is not an asset module is compared with
            // the live bytes. A live file that is no longer text is a
            // change.
            (Some(snapshot), LiveRead::Bytes(bytes)) => (
                Some(snapshot.clone()),
                sha256_hex(snapshot.as_bytes()),
                as_text(&bytes) != Some(snapshot.as_str()),
                false,
            ),
            // A removed file is a change; any other read failure, including a
            // file that is not vetted for reading (`NotPermitted`), only means
            // that freshness could not be checked (`snapshot_failure_is_stale`).
            (Some(_), LiveRead::Failed(failure)) if stub => {
                let stale = Self::snapshot_failure_is_stale(&classified.display, failure);
                unverified_freshness |= !stale;
                (None, sha256_hex(b""), stale, true)
            }
            (Some(snapshot), LiveRead::Failed(failure)) => {
                let stale = Self::snapshot_failure_is_stale(&classified.display, failure);
                unverified_freshness |= !stale;
                (
                    Some(snapshot.clone()),
                    sha256_hex(snapshot.as_bytes()),
                    stale,
                    derived,
                )
            }
            (None, LiveRead::Bytes(bytes)) => match as_text(&bytes) {
                Some(_) if self.is_exempt_asset(&classified.display, path) => {
                    tracing::debug!(path = %classified.display, "asset source has no build snapshot; retaining existence only");
                    (None, String::new(), false, true)
                }
                Some(_) => {
                    tracing::debug!(path = %classified.display, "live source has no build snapshot");
                    self.readable_without_snapshot
                        .insert(classified.display.clone());
                    if track_coverage_gap {
                        self.unverified.push(classified.display.clone());
                    }
                    (None, String::new(), false, true)
                }
                None => (None, sha256_hex(&bytes), false, true),
            },
            (None, LiveRead::Failed(failure)) => {
                tracing::debug!(path = %classified.display, ?failure, "source file unreadable or not vetted for live reading");
                self.unreadable.push(classified.display);
                return None;
            }
        };
        // A source with build snapshot text keeps that text for the analysis;
        // any other source (a generated asset module) was never compared with
        // text, so only its existence is unverified.
        let snapshot_backed = snapshot.is_some() && !stub;
        if unverified_freshness {
            if snapshot_backed {
                self.freshness_unverified.push(classified.display.clone());
            } else {
                self.existence_unverified.push(classified.display.clone());
            }
        }
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
            revision_evidence: if snapshot_backed {
                SourceRevisionEvidence::BuildSnapshot
            } else {
                SourceRevisionEvidence::Unavailable
            },
        };
        // A saved bundle is reopened against the backing file, not the module
        // path with its import query. Text sources are re-hashed; the others
        // only have to still exist.
        if let Some(relative) = classify(project_root, &file_path).project_relative
            && classified.project_relative.is_some()
        {
            if existence_only {
                self.existence_relative.insert(id.clone(), relative);
            } else if verifiable {
                self.project_relative.insert(id.clone(), relative);
            }
        }
        self.files.insert(key, file);
        Some((id, text))
    }

    /// Classifies a failed live read of a source that has a build snapshot and
    /// returns whether the source counts as stale. Only a removed file
    /// (`NotFound`) proves a change since the build; any other failure leaves
    /// the snapshot valid, so the source is only freshness-unverified.
    fn snapshot_failure_is_stale(source_path: &str, failure: LiveFileFailure) -> bool {
        if failure.is_removal() {
            tracing::debug!(path = %source_path, "source with a build snapshot was removed since the build");
            return true;
        }
        tracing::warn!(
            path = %source_path,
            ?failure,
            "live read failed; freshness of source with a build snapshot is unverified"
        );
        false
    }

    /// Reads a live source only when it is a regular file of at most
    /// `MAX_SOURCE_BYTES` that lies inside the project root or is a module
    /// path the bundler reported. `file_path` is the backing file of the
    /// module `path`.
    fn read_live(&self, project_root: &Path, path: &Path, file_path: &Path) -> LiveRead {
        #[cfg(test)]
        if let Some(kind) = self
            .live_read_failures
            .get(&normalize(path))
            .or_else(|| self.live_read_failures.get(&normalize(file_path)))
        {
            return LiveRead::Failed(LiveFileFailure::from_kind(*kind));
        }
        let root =
            std::fs::canonicalize(project_root).unwrap_or_else(|_| project_root.to_path_buf());
        let vet = |resolved: &Path| {
            resolved.starts_with(&root)
                || self.allowed.contains(&normalize(path))
                || self.allowed.contains(&normalize(file_path))
                || self.allowed.contains(resolved)
        };
        match read_vetted_file(file_path, vet, MAX_SOURCE_BYTES) {
            Ok(bytes) => LiveRead::Bytes(bytes),
            Err(failure) => LiveRead::Failed(failure),
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

/// Filesystem helpers shared by unit tests in this crate.
#[cfg(all(test, unix))]
pub(crate) mod test_support {
    use std::path::Path;

    /// Removes all permissions from a directory. Returns false (and leaves the
    /// directory unchanged) on platforms or accounts where this does not
    /// block access, for example when running as root.
    pub(crate) fn deny_access(dir: &Path) -> bool {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(dir, std::fs::Permissions::from_mode(0o000)).unwrap();
        if std::fs::read_dir(dir).is_err() {
            return true;
        }
        std::fs::set_permissions(dir, std::fs::Permissions::from_mode(0o755)).unwrap();
        false
    }

    pub(crate) fn restore_access(dir: &Path) {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(dir, std::fs::Permissions::from_mode(0o755)).unwrap();
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
        assert!(second.1.is_none());
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
        assert_eq!(
            files[0].revision_evidence,
            SourceRevisionEvidence::BuildSnapshot
        );
    }

    #[test]
    fn failed_live_read_with_a_snapshot_is_unverified_not_stale() {
        use std::io::ErrorKind;
        for kind in [
            ErrorKind::PermissionDenied,
            ErrorKind::Interrupted,
            ErrorKind::TimedOut,
            ErrorKind::Other,
        ] {
            let dir = tempfile::tempdir().unwrap();
            let file = dir.path().join("a.js");
            std::fs::write(&file, "export const a = 1;").unwrap();
            let mut store = SourceStore::new(true);
            store
                .snapshots
                .insert(normalize(&file), "export const a = 1;".to_owned());
            store.live_read_failures.insert(normalize(&file), kind);
            let (id, content) = store.add_file(dir.path(), &file).unwrap();
            assert_eq!(content.as_deref(), Some("export const a = 1;"), "{kind:?}");
            assert!(store.changed_since_build.is_empty(), "{kind:?}");
            assert!(store.unreadable.is_empty(), "{kind:?}");
            assert!(store.unverified.is_empty(), "{kind:?}");
            assert_eq!(
                store.freshness_unverified,
                vec!["a.js".to_owned()],
                "{kind:?}"
            );
            // The text source stays in the saved-run recheck map.
            assert_eq!(store.project_relative[&id], "a.js", "{kind:?}");
            let source = store.into_files().pop().unwrap();
            assert!(!source.stale, "{kind:?}");
            assert!(source.content_included, "{kind:?}");
            assert_eq!(source.content, "export const a = 1;", "{kind:?}");
            assert_eq!(
                source.revision_evidence,
                SourceRevisionEvidence::BuildSnapshot
            );
        }
    }

    #[test]
    fn failed_live_read_of_an_asset_stub_snapshot_is_unverified_not_stale() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("logo.png");
        std::fs::write(&file, b"png").unwrap();
        let mut store = SourceStore::new(true);
        store.snapshots.insert(
            normalize(&file),
            "export default \"__VITE_ASSET__abc__\"".to_owned(),
        );
        store
            .live_read_failures
            .insert(normalize(&file), std::io::ErrorKind::PermissionDenied);
        assert!(store.add_file(dir.path(), &file).unwrap().1.is_none());
        assert!(store.changed_since_build.is_empty());
        // The stub is not build text: only its existence is unverified.
        assert!(store.freshness_unverified.is_empty());
        assert_eq!(store.existence_unverified, vec!["logo.png".to_owned()]);
        let file = store.into_files().pop().unwrap();
        assert!(!file.stale);
        assert_eq!(file.revision_evidence, SourceRevisionEvidence::Unavailable);
    }

    #[test]
    fn removed_live_file_with_a_snapshot_is_stale_not_unverified() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("a.js");
        std::fs::write(&file, "export const a = 1;").unwrap();
        let mut store = SourceStore::new(true);
        store
            .snapshots
            .insert(normalize(&file), "export const a = 1;".to_owned());
        store
            .live_read_failures
            .insert(normalize(&file), std::io::ErrorKind::NotFound);
        assert!(store.add_file(dir.path(), &file).unwrap().1.is_some());
        assert_eq!(store.changed_since_build, vec!["a.js".to_owned()]);
        assert!(store.freshness_unverified.is_empty());
        assert!(store.into_files()[0].stale);
    }

    /// A real (not injected) non-`NotFound` failure: a path below a regular
    /// file fails with a not-a-directory error.
    #[test]
    fn real_not_a_directory_failure_with_a_snapshot_is_unverified() {
        let dir = tempfile::tempdir().unwrap();
        let blocker = dir.path().join("blocker");
        std::fs::write(&blocker, "file").unwrap();
        let file = blocker.join("a.js");
        let mut store = SourceStore::new(true);
        store
            .snapshots
            .insert(normalize(&file), "export const a = 1;".to_owned());
        assert!(store.add_file(dir.path(), &file).unwrap().1.is_some());
        assert!(store.changed_since_build.is_empty());
        assert_eq!(store.freshness_unverified.len(), 1);
        assert!(!store.into_files()[0].stale);
    }

    #[test]
    fn unreadable_file_without_a_snapshot_is_not_freshness_unverified() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("a.js");
        std::fs::write(&file, "x").unwrap();
        let mut store = SourceStore::new(true);
        store
            .live_read_failures
            .insert(normalize(&file), std::io::ErrorKind::PermissionDenied);
        assert!(store.add_file(dir.path(), &file).is_none());
        assert_eq!(store.unreadable, vec!["a.js".to_owned()]);
        assert!(store.freshness_unverified.is_empty());
    }

    #[test]
    fn source_provenance_without_snapshot_does_not_claim_live_revision() {
        for edited_after_build in [false, true] {
            let dir = tempfile::tempdir().unwrap();
            let file = dir.path().join("a.js");
            std::fs::write(&file, "export const value = 'built';").unwrap();
            if edited_after_build {
                std::fs::write(&file, "export const value = 'after';").unwrap();
            }
            // Without sourcesContent, neither the matching-looking live file
            // nor a post-build edit can prove the bundler's input revision.
            let mut store = SourceStore::new(true);
            let (id, text) = store.add_file(dir.path(), &file).unwrap();
            assert!(text.is_none());
            assert_eq!(store.unverified, vec!["a.js"]);
            assert!(store.changed_since_build.is_empty());
            assert!(!store.project_relative.contains_key(&id));
            assert_eq!(store.existence_relative[&id], "a.js");
            let source = store.into_files().pop().unwrap();
            assert_eq!(
                source.revision_evidence,
                SourceRevisionEvidence::Unavailable
            );
            assert!(source.content.is_empty());
            assert!(!source.content_included);
            assert!(source.sha256.is_empty());
            assert!(!source.stale);
        }
    }

    #[test]
    fn cached_unavailable_source_is_promoted_when_coverage_is_tracked() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("a.js");
        std::fs::write(&file, "export const value = 1;").unwrap();
        let mut store = SourceStore::new(true);

        let first = store
            .add_file_without_coverage_gap(dir.path(), &file)
            .unwrap();
        assert!(first.1.is_none());
        assert!(store.unverified.is_empty());

        let second = store.add_file(dir.path(), &file).unwrap();
        assert_eq!(second.0, first.0);
        assert_eq!(store.unverified, vec!["a.js".to_owned()]);

        // Repeated detector visits keep the coverage-gap list deduplicated.
        store.add_file(dir.path(), &file).unwrap();
        assert_eq!(store.unverified, vec!["a.js".to_owned()]);
    }

    #[test]
    fn cached_asset_source_is_not_promoted_to_a_coverage_gap() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("asset.png");
        std::fs::write(&file, [0_u8, 159, 146, 150]).unwrap();
        let mut store = SourceStore::new(true);
        store.snapshots.insert(
            normalize(&file),
            r#"export default "__VITE_ASSET__abc__";"#.to_owned(),
        );

        let first = store
            .add_file_without_coverage_gap(dir.path(), &file)
            .unwrap();
        assert!(first.1.is_none());
        assert!(store.unverified.is_empty());

        let second = store.add_file(dir.path(), &file).unwrap();
        assert_eq!(second.0, first.0);
        assert!(store.unverified.is_empty());
    }

    #[test]
    fn text_assets_without_snapshots_do_not_create_detector_coverage_gaps() {
        let dir = tempfile::tempdir().unwrap();
        for name in [
            "logo.svg",
            "source.js?raw",
            "source.js?url",
            "source.js?inline",
            "source.js?no-inline",
        ] {
            let file = dir.path().join(name);
            let backing = filesystem_path(&file);
            std::fs::write(&backing, "eval('not executable as imported')").unwrap();
            let mut store =
                SourceStore::new(true).with_asset_exemption(AssetExemption::ViteUrlModules);
            let (id, content) = store.add_file(dir.path(), &file).unwrap();
            assert!(content.is_none(), "{name}");
            assert!(store.unverified.is_empty(), "{name}");
            store.add_file(dir.path(), &file).unwrap();
            assert!(store.unverified.is_empty(), "{name}");
            assert!(store.readable_without_snapshot.is_empty(), "{name}");
            assert!(store.existence_relative.contains_key(&id), "{name}");
        }
    }

    #[test]
    fn engines_without_the_vite_exemption_keep_text_assets_as_coverage_gaps() {
        // Rspack and other engines load `.svg` and query imports through
        // user-configured loaders that can emit executable code, so the Vite
        // URL-module rule must not hide their coverage gap.
        let dir = tempfile::tempdir().unwrap();
        for name in ["icon.svg", "source.js?raw", "source.js?url", "plain.js"] {
            let file = dir.path().join(name);
            let backing = filesystem_path(&file);
            std::fs::write(&backing, "eval('executable loader output')").unwrap();
            let mut store = SourceStore::new(true);
            assert_eq!(store.asset_exemption, AssetExemption::None);
            let (id, content) = store.add_file(dir.path(), &file).unwrap();
            assert!(content.is_none(), "{name}");
            assert_eq!(store.unverified.len(), 1, "{name}");
            assert!(store.readable_without_snapshot.contains(name), "{name}");
            assert!(store.existence_relative.contains_key(&id), "{name}");
            // A later visit keeps the single gap.
            store.add_file(dir.path(), &file).unwrap();
            assert_eq!(store.unverified.len(), 1, "{name}");
        }
    }

    #[test]
    fn vite_exemption_does_not_cover_plain_scripts() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("plain.js");
        std::fs::write(&file, "eval('code')").unwrap();
        let mut store = SourceStore::new(true).with_asset_exemption(AssetExemption::ViteUrlModules);
        assert!(store.add_file(dir.path(), &file).unwrap().1.is_none());
        assert_eq!(store.unverified, vec!["plain.js".to_owned()]);
    }

    #[test]
    fn build_snapshots_win_over_the_asset_exemption_setting() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("icon.svg");
        std::fs::write(&file, "eval('x')").unwrap();
        for exemption in [AssetExemption::None, AssetExemption::ViteUrlModules] {
            let mut store = SourceStore::new(true).with_asset_exemption(exemption);
            store
                .snapshots
                .insert(normalize(&file), "eval('x')".to_owned());
            let (_, content) = store.add_file(dir.path(), &file).unwrap();
            assert_eq!(content.as_deref(), Some("eval('x')"), "{exemption:?}");
            assert!(store.unverified.is_empty(), "{exemption:?}");
        }
    }

    #[test]
    fn bundler_reported_asset_modules_keep_existence_only_evidence() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("icon.svg");
        std::fs::write(&file, "<svg/>").unwrap();
        let mut store = SourceStore::new(true);
        store.asset_modules.insert(normalize(&file));
        let (id, content) = store.add_file(dir.path(), &file).unwrap();
        assert!(content.is_none());
        assert!(store.unverified.is_empty());
        assert!(store.readable_without_snapshot.is_empty());
        assert!(store.existence_relative.contains_key(&id));
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
        assert!(store.add_file(project.path(), &module).unwrap().1.is_none());
        assert_eq!(store.unverified.len(), 1);
        assert!(store.unverified[0].ends_with("/module.js"));
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
        assert!(files[0].sha256.is_empty());
        assert_eq!(
            files[0].revision_evidence,
            SourceRevisionEvidence::Unavailable
        );
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
        assert!(files[0].sha256.is_empty());
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
        let source = store.into_files().pop().unwrap();
        assert!(!source.stale);
        assert!(source.sha256.is_empty());
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

    #[test]
    fn generated_asset_module_shapes_are_recognized_from_code_and_path() {
        const LONG_DATA_URL: &str = "export default \"data:image/png;base64,iVBORw0KGgo=\"";
        // (path, snapshot, expected)
        let cases: &[(&str, &str, bool)] = &[
            // Marker stub of earlier Vite releases.
            ("src/logo.png", VITE_STUB, true),
            (
                "src/logo.png",
                "  export default \"__VITE_ASSET__abc__$_x__\";\n",
                true,
            ),
            // Placeholder stub of Rolldown-based Vite releases.
            (
                "src/logo.png",
                "export default import.meta.ROLLDOWN_FILE_URL_ref1_url2",
                true,
            ),
            (
                "src/logo.png",
                "export default import.meta.ROLLDOWN_FILE_URL_",
                false,
            ),
            (
                "src/logo.png",
                "export default import.meta.ROLLDOWN_FILE_URL_a.b",
                false,
            ),
            // Inlined data URLs, `?url` paths and `?raw` / `?inline` text.
            ("src/logo.png", LONG_DATA_URL, true),
            (
                "src/icon.svg",
                "export default \"data:image/svg+xml,%3Csvg%3E%3C/svg%3E\"",
                true,
            ),
            ("src/icon.svg?inline", LONG_DATA_URL, true),
            ("public/logo.png", "export default \"/logo.png\";", true),
            (
                "src/notes.txt?raw",
                "export default \"line one\\nline two\\n\"",
                true,
            ),
            (
                "src/a.js?raw",
                "export default \"export const a = 1;\\n\"",
                true,
            ),
            (
                "src/style.css?inline",
                "export default \".a{color:red}\"",
                true,
            ),
            // A string-only default export in a script is ordinary code.
            ("src/label.js", "export default \"hello\";", false),
            ("src/label.ts", "export default \"hello\"", false),
            ("src/Label.TSX", "export default \"hello\"", false),
            ("src/label.mjs", LONG_DATA_URL, false),
            // The marker text is not enough: a script that happens to export
            // the marker string is ordinary code, with or without a plain
            // query, fragment or lookalike query key.
            ("src/a.js", VITE_STUB, false),
            ("src/a.ts", VITE_STUB, false),
            ("src/a.vue", VITE_STUB, false),
            ("src/a.js?v=1", VITE_STUB, false),
            ("src/a.js?foo=raw", VITE_STUB, false),
            ("src/a.js?rawish", VITE_STUB, false),
            ("src/a.js#raw", VITE_STUB, false),
            ("src/C#.js", VITE_STUB, false),
            (
                "src/a.js",
                "export default \"__VITE_ASSET__ and then some text\";",
                false,
            ),
            // The Rolldown placeholder follows the same path rule.
            (
                "src/a.js",
                "export default import.meta.ROLLDOWN_FILE_URL_ref1",
                false,
            ),
            (
                "src/a.js?url",
                "export default import.meta.ROLLDOWN_FILE_URL_ref1",
                true,
            ),
            // An asset import query makes a script path an asset module, alone
            // or combined with other keys, before a fragment, or with a value.
            ("src/a.js?raw", VITE_STUB, true),
            ("src/a.js?url", VITE_STUB, true),
            ("src/a.js?inline", VITE_STUB, true),
            ("src/a.js?no-inline", VITE_STUB, true),
            ("src/a.js?worker&url", VITE_STUB, true),
            ("src/a.js?raw&v=1", VITE_STUB, true),
            ("src/a.js?raw#frag", VITE_STUB, true),
            ("src/a.js?url=1", VITE_STUB, true),
            // A non-script path needs no query; a fragment or other query does
            // not change that.
            ("src/logo.png?v=2", VITE_STUB, true),
            ("src/icon.svg#frag", VITE_STUB, true),
            ("src/Makefile", VITE_STUB, true),
            // Only the complete expression counts, not a prefix of it.
            (
                "src/logo.png",
                "export default \"__VITE_ASSET__abc__\" + suffix",
                false,
            ),
            (
                "src/logo.png",
                "export default \"__VITE_ASSET__abc__\"; run()",
                false,
            ),
            ("src/logo.png", "export default \"__VITE_ASSET__abc", false),
            ("src/logo.png", "export default \"a\", \"b\"", false),
            ("src/logo.png", "export defaulted \"a\"", false),
            // Whitespace between the keyword and the expression is free.
            ("src/logo.png", "export default\n\"/logo.png\"", true),
            // Other module code is never an asset module.
            ("src/icon.svg", "export default {}", false),
            ("src/icon.svg", "export default \"a\" + b", false),
            ("src/icon.svg", "export const a = \"x\"", false),
            ("src/icon.svg", "<svg/>", false),
            ("src/icon.svg", "", false),
        ];
        for (path, snapshot, expected) in cases {
            assert_eq!(
                is_generated_asset_module(path, snapshot),
                *expected,
                "{path}: {snapshot}"
            );
        }
    }

    #[test]
    fn binary_asset_paths_are_recognized_by_extension() {
        for (path, expected) in [
            ("src/logo.png", true),
            ("src/LOGO.PNG", true),
            ("src/font.woff2?v=1", true),
            ("src/logo.png#frag", true),
            ("src/logo.png?raw#frag", true),
            ("src/C#.png", true),
            ("src/main.js?url.png", false),
            ("src/main.js", false),
            ("src/icon.svg", false),
            ("src/png", false),
            ("src/.png", false),
        ] {
            assert_eq!(is_binary_asset_path(path), expected, "{path}");
        }
    }

    #[derive(Clone, Copy, Debug)]
    enum Snapshot {
        None,
        Text,
        MarkerStub,
        FileUrlStub,
        DataUrlStub,
        RawStub,
    }

    #[derive(Clone, Copy, Debug)]
    enum Live {
        MatchingText,
        ChangedText,
        Binary,
        InvalidUtf8,
        Removed,
    }

    const SVG_TEXT: &str = "<svg xmlns=\"http://www.w3.org/2000/svg\"/>";

    /// Observable outcome of `add_file` for one source.
    #[derive(Debug, PartialEq, Eq)]
    struct Outcome {
        stale: bool,
        content_kept: bool,
        /// Listed for a content re-hash when a saved run is reopened.
        rehashed: bool,
        /// Listed for an existence check when a saved run is reopened.
        existence_checked: bool,
    }

    const fn outcome(
        stale: bool,
        content_kept: bool,
        rehashed: bool,
        existence_checked: bool,
    ) -> Outcome {
        Outcome {
            stale,
            content_kept,
            rehashed,
            existence_checked,
        }
    }

    /// Decision matrix: file x snapshot kind x live file kind. A `?query` in
    /// the file name is part of the module path; the live file is the name
    /// without it.
    #[test]
    fn source_store_decision_matrix_covers_text_and_asset_snapshots() {
        use Live::*;
        use Snapshot::*;
        // (file, snapshot, live, expected)
        type Case = (&'static str, Snapshot, Live, Outcome);
        let cases: Vec<Case> = vec![
            // Ordinary text snapshot: any difference, including a live file
            // that is no longer text, is a change.
            (
                "a.js",
                Text,
                MatchingText,
                outcome(false, true, true, false),
            ),
            ("a.js", Text, ChangedText, outcome(true, true, true, false)),
            ("a.js", Text, Binary, outcome(true, true, true, false)),
            ("a.js", Text, InvalidUtf8, outcome(true, true, true, false)),
            ("a.js", Text, Removed, outcome(true, true, true, false)),
            // No snapshot: a live text read cannot verify the built revision.
            // Keep only an existence check for a saved run.
            (
                "a.js",
                None,
                MatchingText,
                outcome(false, false, false, true),
            ),
            ("a.js", None, Binary, outcome(false, false, false, true)),
            (
                "a.js",
                None,
                InvalidUtf8,
                outcome(false, false, false, true),
            ),
            // Generated asset modules are never compared with the live file,
            // whatever it holds; only removal counts as a change, and a saved
            // run keeps checking that the file exists.
            (
                "logo.png",
                MarkerStub,
                Binary,
                outcome(false, false, false, true),
            ),
            (
                "logo.png",
                MarkerStub,
                Removed,
                outcome(true, false, false, true),
            ),
            (
                "icon.svg",
                MarkerStub,
                MatchingText,
                outcome(false, false, false, true),
            ),
            (
                "icon.svg",
                FileUrlStub,
                MatchingText,
                outcome(false, false, false, true),
            ),
            (
                "icon.svg",
                FileUrlStub,
                Removed,
                outcome(true, false, false, true),
            ),
            (
                "icon.svg",
                DataUrlStub,
                MatchingText,
                outcome(false, false, false, true),
            ),
            (
                "icon.svg",
                DataUrlStub,
                ChangedText,
                outcome(false, false, false, true),
            ),
            (
                "logo.png",
                DataUrlStub,
                Binary,
                outcome(false, false, false, true),
            ),
            (
                "note.txt",
                RawStub,
                MatchingText,
                outcome(false, false, false, true),
            ),
            // A script whose snapshot is the asset marker text is ordinary
            // code: compared with the live file, content kept.
            (
                "a.js",
                MarkerStub,
                MatchingText,
                outcome(true, true, true, false),
            ),
            (
                "a.js",
                MarkerStub,
                Removed,
                outcome(true, true, true, false),
            ),
            (
                "a.js",
                FileUrlStub,
                ChangedText,
                outcome(true, true, true, false),
            ),
            (
                "a.js",
                DataUrlStub,
                ChangedText,
                outcome(true, true, true, false),
            ),
            // An import query that selects a string module makes a script an
            // asset module, backed by the file without the query.
            (
                "a.js?raw",
                RawStub,
                MatchingText,
                outcome(false, false, false, true),
            ),
            (
                "a.js?raw",
                RawStub,
                ChangedText,
                outcome(false, false, false, true),
            ),
            (
                "a.js?raw",
                RawStub,
                Removed,
                outcome(true, false, false, true),
            ),
            (
                "a.js?inline",
                DataUrlStub,
                Binary,
                outcome(false, false, false, true),
            ),
            (
                "a.js?url",
                MarkerStub,
                MatchingText,
                outcome(false, false, false, true),
            ),
            (
                "a.js?url#frag",
                FileUrlStub,
                Removed,
                outcome(true, false, false, true),
            ),
            (
                "icon.svg?raw",
                RawStub,
                MatchingText,
                outcome(false, false, false, true),
            ),
            (
                "icon.svg?inline",
                DataUrlStub,
                Removed,
                outcome(true, false, false, true),
            ),
            // Another import query names a module derived from the file: its
            // transformed code is not compared, only the file must exist.
            (
                "a.js?v=1",
                Text,
                MatchingText,
                outcome(false, true, false, true),
            ),
            (
                "a.js?v=1",
                Text,
                ChangedText,
                outcome(false, true, false, true),
            ),
            ("a.js?v=1", Text, Binary, outcome(false, true, false, true)),
            ("a.js?v=1", Text, Removed, outcome(true, true, false, true)),
            (
                "a.js?v=1",
                MarkerStub,
                ChangedText,
                outcome(false, true, false, true),
            ),
            // Without a snapshot neither a plain nor a queried live file
            // proves the analyzed revision.
            (
                "a.js?v=1",
                None,
                MatchingText,
                outcome(false, false, false, true),
            ),
            ("a.js?raw", None, Binary, outcome(false, false, false, true)),
        ];
        for (file, snapshot, live, expected) in cases {
            let dir = tempfile::tempdir().unwrap();
            let disk_name = file.split('?').next().unwrap();
            let is_script = disk_name.ends_with(".js");
            let path = dir.path().join(file);
            let snapshot_text = match snapshot {
                None => Option::None,
                Text => Some("export const a = 1;".to_owned()),
                MarkerStub => Some(VITE_STUB.to_owned()),
                FileUrlStub => {
                    Some("export default import.meta.ROLLDOWN_FILE_URL_abc123".to_owned())
                }
                DataUrlStub => {
                    Some("export default \"data:image/svg+xml,%3Csvg%2F%3E\"".to_owned())
                }
                RawStub => Some(format!("export default {:?}", SVG_TEXT)),
            };
            let live_bytes: Option<Vec<u8>> = match live {
                MatchingText if is_script => Some(b"export const a = 1;".to_vec()),
                MatchingText => Some(SVG_TEXT.as_bytes().to_vec()),
                ChangedText if is_script => Some(b"export const a = 2;".to_vec()),
                ChangedText => Some(b"<svg changed/>".to_vec()),
                Binary => Some(PNG_BYTES.to_vec()),
                InvalidUtf8 => Some(vec![b'a', 0xff, 0xfe, b'b']),
                Removed => Option::None,
            };
            if let Some(bytes) = &live_bytes {
                // Created through the file name without the query.
                std::fs::write(dir.path().join(disk_name), bytes).unwrap();
            }
            let mut store = SourceStore::new(true);
            if let Some(text) = snapshot_text {
                store.snapshots.insert(normalize(&path), text);
            }
            let (id, text) = store
                .add_file(dir.path(), &path)
                .unwrap_or_else(|| panic!("{file} {snapshot:?} {live:?} must keep a source"));
            let actual = Outcome {
                stale: !store.changed_since_build.is_empty(),
                content_kept: text.is_some(),
                rehashed: store.project_relative.contains_key(&id),
                existence_checked: store.existence_relative.contains_key(&id),
            };
            // The saved path is the backing file, never the module path with
            // its query, and a source is listed for at most one check.
            for relative in store
                .project_relative
                .values()
                .chain(store.existence_relative.values())
            {
                assert_eq!(relative, disk_name, "{file} {snapshot:?} {live:?}");
            }
            assert!(!(actual.rehashed && actual.existence_checked), "{file}");
            let files = store.into_files();
            assert_eq!(files.len(), 1);
            assert_eq!(actual, expected, "{file} {snapshot:?} {live:?}");
            assert_eq!(files[0].path, file, "{file} {snapshot:?} {live:?}");
            assert_eq!(files[0].stale, actual.stale, "{file} {snapshot:?} {live:?}");
            assert_eq!(
                files[0].content_included, actual.content_kept,
                "{file} {snapshot:?} {live:?}"
            );
        }
    }

    #[test]
    fn module_paths_with_a_query_read_the_backing_file() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("src/a.js");
        std::fs::create_dir_all(file.parent().unwrap()).unwrap();
        std::fs::write(&file, "export const a = 1;").unwrap();
        for name in ["src/a.js?raw", "src/a.js?v=1#x", "src/a.js#frag"] {
            let path = dir.path().join(name);
            let mut store =
                SourceStore::new(true).with_asset_exemption(AssetExemption::ViteUrlModules);
            // No source map snapshot: a live read verifies the path, but not
            // the revision used by the build.
            let (id, text) = store
                .add_file(dir.path(), &path)
                .unwrap_or_else(|| panic!("{name} must be readable"));
            assert!(text.is_none(), "{name}");
            assert!(store.unreadable.is_empty(), "{name}");
            let expected_gap = if name.ends_with("?raw") {
                Vec::new()
            } else {
                vec![name.to_owned()]
            };
            assert_eq!(store.unverified, expected_gap, "{name}");
            assert!(store.changed_since_build.is_empty(), "{name}");
            assert_eq!(store.existence_relative[&id], "src/a.js", "{name}");
            // The display path and module identity keep the query.
            let files = store.into_files();
            assert_eq!(files[0].path, name);
            assert_eq!(files[0].id, source_id(&normalize(&path).to_string_lossy()));
        }
        // Different queries are different modules of the same file.
        let mut store = SourceStore::new(true);
        let raw = store
            .add_file(dir.path(), &dir.path().join("src/a.js?raw"))
            .unwrap();
        let plain = store.add_file(dir.path(), &file).unwrap();
        assert_ne!(raw.0, plain.0);
        assert_eq!(store.into_files().len(), 2);
    }

    #[test]
    fn query_paths_are_vetted_through_the_backing_file() {
        let project = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        let module = outside.path().join("module.txt");
        std::fs::write(&module, "plain text").unwrap();
        let queried = outside.path().join("module.txt?raw");
        let mut store = SourceStore::new(true);
        // Neither the module path nor its backing file was reported.
        assert!(store.add_file(project.path(), &queried).is_none());
        assert_eq!(store.unreadable.len(), 1);
        // The bundler reported the module path with its query.
        let mut store = SourceStore::new(true);
        store.allowed.insert(normalize(&queried));
        assert!(store.add_file(project.path(), &queried).is_some());
        // The bundler reported the backing file.
        let mut store = SourceStore::new(true);
        store.allowed.insert(normalize(&module));
        assert!(store.add_file(project.path(), &queried).is_some());
        // Outside the project nothing is listed for a saved-run recheck.
        assert!(store.project_relative.is_empty());
        assert!(store.existence_relative.is_empty());
    }

    #[test]
    fn filesystem_path_prefers_an_existing_literal_name() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        std::fs::write(root.join("plain.js"), "").unwrap();
        std::fs::write(root.join("C#.js"), "").unwrap();
        std::fs::write(root.join("hash#frag.js"), "").unwrap();
        // (module path, expected backing file)
        let cases: &[(&str, &str)] = &[
            ("plain.js", "plain.js"),
            ("plain.js?raw", "plain.js"),
            ("plain.js?raw&x=1", "plain.js"),
            ("plain.js#frag", "plain.js"),
            ("plain.js?raw#frag", "plain.js"),
            // A literal file name wins over its query-free prefix.
            ("C#.js", "C#.js"),
            ("C#.js?raw", "C#.js"),
            ("hash#frag.js?raw", "hash#frag.js"),
            // Nothing exists: query and fragment suffixes are dropped so a
            // restored backing file can be found.
            ("gone.js", "gone.js"),
            ("gone.js?raw", "gone.js"),
            ("gone.js?raw#frag", "gone.js"),
            ("gone#name.js", "gone"),
        ];
        for (module, expected) in cases {
            assert_eq!(
                filesystem_path(&root.join(module)),
                root.join(expected),
                "{module}"
            );
        }
    }

    #[test]
    fn legacy_source_missing_fragment_records_the_backing_file() {
        for (module, snapshot, existence_only) in [
            ("src/a.js#component", "export const a = 1;", false),
            (
                "src/a.js?raw#component",
                "export default \"export const a = 1;\"",
                true,
            ),
        ] {
            let dir = tempfile::tempdir().unwrap();
            std::fs::create_dir(dir.path().join("src")).unwrap();
            let path = dir.path().join(module);
            let mut store = SourceStore::new(true);
            store
                .snapshots
                .insert(normalize(&path), snapshot.to_owned());
            let (id, _) = store.add_file(dir.path(), &path).unwrap();
            let backing = if existence_only {
                &store.existence_relative
            } else {
                &store.project_relative
            };
            assert_eq!(backing.get(&id).map(String::as_str), Some("src/a.js"));
            assert_eq!(store.into_files()[0].path, module);
        }
    }

    #[test]
    fn text_snapshot_replaced_by_binary_is_stale_and_keeps_the_snapshot_hash() {
        for replacement in [PNG_BYTES.to_vec(), vec![b'a', 0xff, 0xfe, b'b']] {
            let dir = tempfile::tempdir().unwrap();
            let file = dir.path().join("a.js");
            std::fs::write(&file, &replacement).unwrap();
            let mut store = SourceStore::new(true);
            store
                .snapshots
                .insert(normalize(&file), "export const a = 1;".to_owned());
            let (id, text) = store.add_file(dir.path(), &file).unwrap();
            assert_eq!(text.as_deref(), Some("export const a = 1;"));
            assert_eq!(store.changed_since_build, vec!["a.js".to_owned()]);
            // The saved-run reload keeps comparing the live file.
            assert!(store.project_relative.contains_key(&id));
            let files = store.into_files();
            assert!(files[0].stale);
            assert!(files[0].content_included);
            assert_eq!(files[0].sha256, sha256_hex(b"export const a = 1;"));
        }
    }

    #[test]
    fn derived_module_keeps_the_snapshot_hash() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("a.js");
        let snapshot = "export const value = 1;";
        std::fs::write(&file, snapshot).unwrap();
        let queried = dir.path().join("a.js?v=1");
        let mut store = SourceStore::new(true);
        store
            .snapshots
            .insert(normalize(&queried), snapshot.to_owned());

        store.add_file(dir.path(), &queried).unwrap();
        let source = store.into_files().pop().unwrap();
        assert_eq!(
            source.revision_evidence,
            SourceRevisionEvidence::BuildSnapshot
        );
        assert!(source.content_included);
        assert_eq!(source.sha256, sha256_hex(snapshot.as_bytes()));
    }

    #[test]
    fn split_resource_follows_the_documented_table() {
        // (input, file, query, fragment, virtual module, normalized file path)
        type Case = (
            &'static str,
            &'static str,
            Option<&'static str>,
            Option<&'static str>,
            bool,
            &'static str,
        );
        let cases: &[Case] = &[
            ("/app/a.js", "/app/a.js", None, None, false, "/app/a.js"),
            (
                "/app/a.js?raw",
                "/app/a.js",
                Some("raw"),
                None,
                false,
                "/app/a.js",
            ),
            (
                "/app/a.js?raw#one",
                "/app/a.js",
                Some("raw"),
                Some("one"),
                false,
                "/app/a.js",
            ),
            (
                "/app/a.js#one",
                "/app/a.js",
                None,
                Some("one"),
                false,
                "/app/a.js",
            ),
            (
                "/app/a.js#one?raw",
                "/app/a.js",
                None,
                Some("one?raw"),
                false,
                "/app/a.js",
            ),
            (
                "/app/a.js?",
                "/app/a.js",
                Some(""),
                None,
                false,
                "/app/a.js",
            ),
            (
                "/app/a.js#",
                "/app/a.js",
                None,
                Some(""),
                false,
                "/app/a.js",
            ),
            (
                "/app/a.js?a?b#c#d",
                "/app/a.js",
                Some("a?b"),
                Some("c#d"),
                false,
                "/app/a.js",
            ),
            (
                "/app/./x/../a.js?q",
                "/app/./x/../a.js",
                Some("q"),
                None,
                false,
                "/app/a.js",
            ),
            (
                "\0virtual:a?x#y",
                "virtual:a",
                Some("x"),
                Some("y"),
                true,
                "virtual:a",
            ),
            ("\0\0virtual:a", "virtual:a", None, None, true, "virtual:a"),
            (
                "C:\\app\\a.vue?x#y",
                "C:\\app\\a.vue",
                Some("x"),
                Some("y"),
                false,
                "C:/app/a.vue",
            ),
            (
                "C:/app/a.vue",
                "C:/app/a.vue",
                None,
                None,
                false,
                "C:/app/a.vue",
            ),
            (
                "\\\\host\\share\\a.js#f",
                "\\\\host\\share\\a.js",
                None,
                Some("f"),
                false,
                "/host/share/a.js",
            ),
            // A backslash in a POSIX path is an ordinary file-name character.
            (
                "/app/a\\b.js?q",
                "/app/a\\b.js",
                Some("q"),
                None,
                false,
                "/app/a\\b.js",
            ),
        ];
        for (input, file, query, fragment, virtual_module, path) in cases {
            let parts = split_resource(input);
            assert_eq!(parts.file, *file, "{input}");
            assert_eq!(parts.query, *query, "{input}");
            assert_eq!(parts.fragment, *fragment, "{input}");
            assert_eq!(parts.virtual_module, *virtual_module, "{input}");
            assert_eq!(parts.file_path(), PathBuf::from(path), "{input}");
        }
    }

    #[test]
    fn live_file_failure_classifier_table() {
        use std::io::ErrorKind;
        // (kind, classification, proves removal)
        let table = [
            (ErrorKind::NotFound, LiveFileFailure::Missing, true),
            (
                ErrorKind::PermissionDenied,
                LiveFileFailure::Unreadable(ErrorKind::PermissionDenied),
                false,
            ),
            (
                ErrorKind::NotADirectory,
                LiveFileFailure::Unreadable(ErrorKind::NotADirectory),
                false,
            ),
            (
                ErrorKind::IsADirectory,
                LiveFileFailure::Unreadable(ErrorKind::IsADirectory),
                false,
            ),
            (
                ErrorKind::InvalidData,
                LiveFileFailure::Unreadable(ErrorKind::InvalidData),
                false,
            ),
            (
                ErrorKind::Interrupted,
                LiveFileFailure::Unreadable(ErrorKind::Interrupted),
                false,
            ),
            (
                ErrorKind::TimedOut,
                LiveFileFailure::Unreadable(ErrorKind::TimedOut),
                false,
            ),
            (
                ErrorKind::Other,
                LiveFileFailure::Unreadable(ErrorKind::Other),
                false,
            ),
        ];
        for (kind, expected, removal) in table {
            assert_eq!(LiveFileFailure::from_kind(kind), expected, "{kind:?}");
            assert_eq!(
                LiveFileFailure::from_io(&std::io::Error::from(kind)),
                expected,
                "{kind:?}"
            );
            assert_eq!(expected.is_removal(), removal, "{kind:?}");
        }
        assert!(!LiveFileFailure::NotPermitted.is_removal());
    }

    #[test]
    fn read_vetted_file_classifies_every_failure() {
        let dir = tempfile::tempdir().unwrap();
        let root = std::fs::canonicalize(dir.path()).unwrap();
        let inside = |resolved: &Path| resolved.starts_with(&root);
        std::fs::write(root.join("ok.js"), "ok").unwrap();
        std::fs::write(root.join("big.js"), "0123456789").unwrap();
        std::fs::create_dir(root.join("dir.js")).unwrap();
        std::fs::write(root.join("blocker"), "file").unwrap();

        assert_eq!(
            read_vetted_file(&root.join("ok.js"), inside, 16).unwrap(),
            b"ok"
        );
        // (path, expected failure)
        let table = [
            ("missing.js", LiveFileFailure::Missing),
            ("dir.js", LiveFileFailure::NotPermitted),
            ("big.js", LiveFileFailure::NotPermitted),
        ];
        for (name, expected) in table {
            assert_eq!(
                read_vetted_file(&root.join(name), inside, 5).unwrap_err(),
                expected,
                "{name}"
            );
        }
        // A path below a regular file is not "removed".
        assert_eq!(
            read_vetted_file(&root.join("blocker/a.js"), inside, 16).unwrap_err(),
            LiveFileFailure::Unreadable(std::io::ErrorKind::NotADirectory)
        );
        // A path rejected by the vetting closure is never a change.
        assert_eq!(
            read_vetted_file(&root.join("ok.js"), |_| false, 16).unwrap_err(),
            LiveFileFailure::NotPermitted
        );
    }

    #[cfg(unix)]
    #[test]
    fn read_vetted_file_classifies_symlinks() {
        let dir = tempfile::tempdir().unwrap();
        let root = std::fs::canonicalize(dir.path()).unwrap();
        let project = root.join("project");
        std::fs::create_dir(&project).unwrap();
        let project = std::fs::canonicalize(&project).unwrap();
        let inside = |resolved: &Path| resolved.starts_with(&project);
        std::fs::write(root.join("outside.js"), "outside").unwrap();
        std::fs::write(project.join("real.js"), "real").unwrap();
        std::os::unix::fs::symlink(root.join("outside.js"), project.join("escape.js")).unwrap();
        std::os::unix::fs::symlink(project.join("real.js"), project.join("inner.js")).unwrap();
        std::os::unix::fs::symlink(project.join("gone.js"), project.join("dangling.js")).unwrap();
        std::os::unix::fs::symlink(project.join("loop-b"), project.join("loop-a")).unwrap();
        std::os::unix::fs::symlink(project.join("loop-a"), project.join("loop-b")).unwrap();
        std::os::unix::fs::symlink("/dev/zero", project.join("zero.js")).unwrap();

        assert_eq!(
            read_vetted_file(&project.join("inner.js"), inside, 16).unwrap(),
            b"real"
        );
        // (name, expected failure)
        let table = [
            ("escape.js", LiveFileFailure::NotPermitted),
            ("zero.js", LiveFileFailure::NotPermitted),
            // The target is gone, so the file is effectively removed.
            ("dangling.js", LiveFileFailure::Missing),
        ];
        for (name, expected) in table {
            assert_eq!(
                read_vetted_file(&project.join(name), inside, 16).unwrap_err(),
                expected,
                "{name}"
            );
        }
        // A symlink loop is an I/O failure, not a removal.
        let failure = read_vetted_file(&project.join("loop-a"), inside, 16).unwrap_err();
        assert!(
            matches!(failure, LiveFileFailure::Unreadable(_)),
            "{failure:?}"
        );
    }

    #[test]
    fn read_vetted_file_classifies_a_file_changed_between_check_and_read() {
        let dir = tempfile::tempdir().unwrap();
        let root = std::fs::canonicalize(dir.path()).unwrap();
        let file = root.join("a.js");

        // Removed after canonicalization, before the metadata check.
        std::fs::write(&file, "x").unwrap();
        let failure = read_vetted_file(
            &file,
            |resolved| {
                std::fs::remove_file(resolved).unwrap();
                true
            },
            16,
        )
        .unwrap_err();
        assert_eq!(failure, LiveFileFailure::Missing);

        // Replaced by a directory after canonicalization.
        std::fs::write(&file, "x").unwrap();
        let failure = read_vetted_file(
            &file,
            |resolved| {
                std::fs::remove_file(resolved).unwrap();
                std::fs::create_dir(resolved).unwrap();
                true
            },
            16,
        )
        .unwrap_err();
        assert_eq!(failure, LiveFileFailure::NotPermitted);
    }

    #[cfg(unix)]
    #[test]
    fn read_vetted_file_and_probe_report_permission_denied() {
        let dir = tempfile::tempdir().unwrap();
        let root = std::fs::canonicalize(dir.path()).unwrap();
        let locked = root.join("locked");
        std::fs::create_dir(&locked).unwrap();
        std::fs::write(locked.join("a.js"), "x").unwrap();
        if !test_support::deny_access(&locked) {
            eprintln!("skipped: the filesystem permissions cannot block this account");
            return;
        }
        let read = read_vetted_file(&locked.join("a.js"), |_| true, 16);
        let probe = probe_entry(&locked.join("a.js"));
        // The directory is also not "missing" when it is looked up directly.
        let list = std::fs::read_dir(&locked).map_err(|error| LiveFileFailure::from_io(&error));
        test_support::restore_access(&locked);
        let denied = LiveFileFailure::Unreadable(std::io::ErrorKind::PermissionDenied);
        assert_eq!(read.unwrap_err(), denied);
        assert_eq!(probe.unwrap_err(), denied);
        assert_eq!(list.err(), Some(denied));
    }

    /// First-run counterpart of the reopen table: the same filesystem setups
    /// classify identically for a source with a build snapshot and for a
    /// generated asset stub (which has no build text).
    #[test]
    fn first_run_classifies_real_filesystem_failures_for_snapshot_and_stub_sources() {
        #[derive(Clone, Copy, Debug)]
        enum Setup {
            Removed,
            Directory,
            ParentIsFile,
            /// A regular file larger than the live read limit.
            Oversized,
            #[cfg(unix)]
            DanglingSymlink,
            /// A symlink to a file outside the project root.
            #[cfg(unix)]
            EscapingSymlink,
            #[cfg(unix)]
            PermissionDenied,
        }
        // (setup, stale, unverified)
        let table = [
            (Setup::Removed, true, false),
            (Setup::Directory, false, true),
            (Setup::ParentIsFile, false, true),
            (Setup::Oversized, false, true),
            #[cfg(unix)]
            (Setup::DanglingSymlink, true, false),
            #[cfg(unix)]
            (Setup::EscapingSymlink, false, true),
            #[cfg(unix)]
            (Setup::PermissionDenied, false, true),
        ];
        for stub in [false, true] {
            for (setup, stale, unverified) in table {
                let dir = tempfile::tempdir().unwrap();
                let root = std::fs::canonicalize(dir.path()).unwrap();
                let project = root.join("project");
                let src = project.join("src");
                std::fs::create_dir_all(&src).unwrap();
                // A generated asset module needs an asset path.
                let file = src.join(if stub { "logo.png" } else { "a.js" });
                #[cfg(unix)]
                let mut locked = false;
                match setup {
                    Setup::Removed => {}
                    Setup::Directory => std::fs::create_dir(&file).unwrap(),
                    Setup::ParentIsFile => {
                        std::fs::remove_dir(&src).unwrap();
                        std::fs::write(&src, "file").unwrap();
                    }
                    Setup::Oversized => {
                        let handle = std::fs::File::create(&file).unwrap();
                        handle.set_len(MAX_SOURCE_BYTES + 1).unwrap();
                    }
                    #[cfg(unix)]
                    Setup::DanglingSymlink => {
                        std::os::unix::fs::symlink(project.join("gone.js"), &file).unwrap();
                    }
                    #[cfg(unix)]
                    Setup::EscapingSymlink => {
                        std::fs::write(root.join("outside.js"), "x").unwrap();
                        std::os::unix::fs::symlink(root.join("outside.js"), &file).unwrap();
                    }
                    #[cfg(unix)]
                    Setup::PermissionDenied => {
                        std::fs::write(&file, "x").unwrap();
                        if !test_support::deny_access(&src) {
                            eprintln!("skipped {setup:?}: permissions cannot block this account");
                            continue;
                        }
                        locked = true;
                    }
                }
                let mut store = SourceStore::new(true);
                let snapshot = if stub {
                    "export default \"__VITE_ASSET__abc__\""
                } else {
                    "export const a = 1;"
                };
                store
                    .snapshots
                    .insert(normalize(&file), snapshot.to_owned());
                let added = store.add_file(&project, &file);
                #[cfg(unix)]
                if locked {
                    test_support::restore_access(&src);
                }
                let label = format!("{setup:?} stub={stub}");
                assert!(added.is_some(), "{label}");
                assert_eq!(
                    store.changed_since_build.len(),
                    usize::from(stale),
                    "{label}"
                );
                // Snapshot text and stubs are reported separately, so the
                // warning never claims build text for a stub.
                let (snapshot_backed, existence_only) = (
                    store.freshness_unverified.len(),
                    store.existence_unverified.len(),
                );
                let expected = usize::from(unverified);
                assert_eq!(
                    (snapshot_backed, existence_only),
                    if stub { (0, expected) } else { (expected, 0) },
                    "{label}"
                );
                assert_eq!(store.into_files()[0].stale, stale, "{label}");
            }
        }
    }

    /// An existence probe and a read vet a path identically: every cell gives
    /// the same classification for both.
    #[test]
    fn probe_vetted_file_matches_read_vetted_file_for_every_cell() {
        let dir = tempfile::tempdir().unwrap();
        let root = std::fs::canonicalize(dir.path()).unwrap();
        std::fs::write(root.join("ok.js"), "ok").unwrap();
        std::fs::write(root.join("big.js"), "0123456789").unwrap();
        std::fs::create_dir(root.join("dir.js")).unwrap();
        std::fs::write(root.join("blocker"), "file").unwrap();
        let outside = tempfile::tempdir().unwrap();
        std::fs::write(outside.path().join("out.js"), "ok").unwrap();
        #[cfg(unix)]
        {
            std::os::unix::fs::symlink(outside.path().join("out.js"), root.join("escape.js"))
                .unwrap();
            std::os::unix::fs::symlink(root.join("gone.js"), root.join("dangling.js")).unwrap();
        }
        // (name, max bytes, expected)
        let mut table: Vec<(&str, u64, Result<(), LiveFileFailure>)> = vec![
            ("ok.js", 8, Ok(())),
            ("missing.js", 8, Err(LiveFileFailure::Missing)),
            ("big.js", 8, Err(LiveFileFailure::NotPermitted)),
            ("dir.js", 8, Err(LiveFileFailure::NotPermitted)),
            (
                "blocker/a.js",
                8,
                Err(LiveFileFailure::Unreadable(
                    std::io::ErrorKind::NotADirectory,
                )),
            ),
        ];
        #[cfg(unix)]
        {
            table.push(("escape.js", 8, Err(LiveFileFailure::NotPermitted)));
            table.push(("dangling.js", 8, Err(LiveFileFailure::Missing)));
        }
        for (name, max_bytes, expected) in table {
            let path = root.join(name);
            let vet = |resolved: &Path| resolved.starts_with(&root);
            let probed = probe_vetted_file(&path, vet, max_bytes);
            let read = read_vetted_file(&path, vet, max_bytes).map(|_| ());
            assert_eq!(probed, expected, "probe {name}");
            assert_eq!(read, expected, "read {name}");
        }
    }

    /// A log label never exposes the path or its query.
    #[test]
    fn log_label_hides_the_path_and_is_stable() {
        let path = "/home/user/project/src/a.js?token=secret";
        let label = log_label(path);
        assert_eq!(label.len(), "module-".len() + 16);
        assert!(label.starts_with("module-"));
        assert!(!label.contains("secret") && !label.contains("home"));
        assert_eq!(label, log_label(path));
        assert_ne!(label, log_label("/home/user/project/src/b.js?token=secret"));
    }

    #[test]
    fn probe_entry_distinguishes_missing_from_other_failures() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("blocker"), "file").unwrap();
        std::fs::write(dir.path().join("a.js"), "x").unwrap();
        assert_eq!(probe_entry(&dir.path().join("a.js")), Ok(()));
        assert_eq!(
            probe_entry(&dir.path().join("missing.js")),
            Err(LiveFileFailure::Missing)
        );
        assert_eq!(
            probe_entry(&dir.path().join("blocker/a.js")),
            Err(LiveFileFailure::Unreadable(
                std::io::ErrorKind::NotADirectory
            ))
        );
    }

    #[cfg(unix)]
    #[test]
    fn probe_entry_follows_symlinks_like_a_read() {
        let dir = tempfile::tempdir().unwrap();
        let root = std::fs::canonicalize(dir.path()).unwrap();
        std::fs::write(root.join("real.js"), "x").unwrap();
        std::os::unix::fs::symlink(root.join("real.js"), root.join("inner.js")).unwrap();
        std::os::unix::fs::symlink(root.join("gone.js"), root.join("dangling.js")).unwrap();
        std::os::unix::fs::symlink(root.join("loop-b"), root.join("loop-a")).unwrap();
        std::os::unix::fs::symlink(root.join("loop-a"), root.join("loop-b")).unwrap();
        assert_eq!(probe_entry(&root.join("inner.js")), Ok(()));
        // A symlink whose target was removed is missing, as it is for a read.
        assert_eq!(
            probe_entry(&root.join("dangling.js")),
            Err(LiveFileFailure::Missing)
        );
        assert_eq!(
            probe_entry(&root.join("dangling.js")),
            read_vetted_file(&root.join("dangling.js"), |_| true, 16).map(|_| ())
        );
        // A loop is an I/O failure, not a removal.
        assert!(!probe_entry(&root.join("loop-a")).unwrap_err().is_removal());
    }

    /// Candidate selection of `filesystem_path`, one cell per setup. A
    /// candidate that cannot be inspected is never preferred over the
    /// query-free path.
    #[test]
    fn filesystem_path_selects_the_backing_file_table() {
        #[derive(Clone, Copy, Debug)]
        enum Setup {
            /// Only the literal name exists.
            LiteralExists,
            /// Only the query-free name exists.
            QueryFreeExists,
            /// Both exist: the literal wins.
            BothExist,
            /// Nothing exists.
            NothingExists,
            /// The parent is a regular file, so no candidate can be inspected.
            UninspectableParent,
            /// A dangling literal symlink with an existing query-free file.
            DanglingLiteral,
            /// Only the fragment-free name exists.
            FragmentFreeExists,
            /// No candidate can be inspected because the directory is locked.
            #[cfg(unix)]
            PermissionDenied,
        }
        // (setup, module file name, expected backing file name)
        let table = [
            (Setup::LiteralExists, "a.js?raw", "a.js?raw"),
            (Setup::QueryFreeExists, "a.js?raw", "a.js"),
            (Setup::BothExist, "a.js?raw", "a.js?raw"),
            (Setup::NothingExists, "a.js?raw", "a.js"),
            (Setup::UninspectableParent, "a.js?raw", "a.js"),
            (Setup::DanglingLiteral, "a.js?raw", "a.js"),
            (Setup::FragmentFreeExists, "a.js?raw#frag", "a.js"),
            (Setup::NothingExists, "a.js#frag", "a.js"),
            #[cfg(unix)]
            (Setup::PermissionDenied, "a.js?raw", "a.js"),
        ];
        for (setup, module, expected) in table {
            let dir = tempfile::tempdir().unwrap();
            let root = std::fs::canonicalize(dir.path()).unwrap();
            let parent = root.join("src");
            #[cfg(unix)]
            let mut locked = false;
            match setup {
                Setup::UninspectableParent => std::fs::write(&parent, "file").unwrap(),
                other => {
                    std::fs::create_dir(&parent).unwrap();
                    match other {
                        Setup::LiteralExists => std::fs::write(parent.join(module), "x").unwrap(),
                        Setup::QueryFreeExists | Setup::FragmentFreeExists => {
                            std::fs::write(parent.join("a.js"), "x").unwrap();
                        }
                        Setup::BothExist => {
                            std::fs::write(parent.join(module), "x").unwrap();
                            std::fs::write(parent.join("a.js"), "x").unwrap();
                        }
                        #[cfg(unix)]
                        Setup::DanglingLiteral => {
                            std::fs::write(parent.join("a.js"), "x").unwrap();
                            std::os::unix::fs::symlink(root.join("gone.js"), parent.join(module))
                                .unwrap();
                        }
                        #[cfg(not(unix))]
                        Setup::DanglingLiteral => {
                            std::fs::write(parent.join("a.js"), "x").unwrap();
                        }
                        #[cfg(unix)]
                        Setup::PermissionDenied => {
                            std::fs::write(parent.join("a.js"), "x").unwrap();
                            if !test_support::deny_access(&parent) {
                                eprintln!("skipped: permissions cannot block this account");
                                continue;
                            }
                            locked = true;
                        }
                        Setup::NothingExists | Setup::UninspectableParent => {}
                    }
                }
            }
            let selected = filesystem_path(&parent.join(module));
            #[cfg(unix)]
            if locked {
                test_support::restore_access(&parent);
            }
            assert_eq!(selected, parent.join(expected), "{setup:?} {module}");
        }
    }

    #[test]
    fn loader_and_nul_prefixes_are_stripped_by_the_shared_helpers() {
        let cases = [
            ("css-loader!/app/a.vue?x", "/app/a.vue?x"),
            ("a!b!/app/a.vue", "/app/a.vue"),
            ("/app/a.vue", "/app/a.vue"),
            ("!/app/a.vue", "/app/a.vue"),
        ];
        for (id, resource) in cases {
            assert_eq!(strip_loader_prefix(id), resource, "{id}");
        }
        assert_eq!(strip_nul_prefix("\0virtual:a"), "virtual:a");
        assert_eq!(strip_nul_prefix("virtual:a"), "virtual:a");
    }

    #[test]
    fn freshness_warning_claims_a_build_snapshot_only_for_snapshot_backed_sources() {
        // (snapshot-backed, existence-only, claims a build snapshot, mentions
        // existence)
        let table = [
            (0, 0, None),
            (2, 0, Some((true, false))),
            (0, 3, Some((false, true))),
            (1, 2, Some((true, true))),
        ];
        for (snapshot_backed, existence_only, expected) in table {
            let warning = freshness_unverified_problem(snapshot_backed, existence_only);
            let label = format!("{snapshot_backed}/{existence_only}");
            let Some((claims_snapshot, mentions_existence)) = expected else {
                assert!(warning.is_none(), "{label}");
                continue;
            };
            let warning = warning.unwrap();
            assert_eq!(warning.code, "BW_SOURCE_FRESHNESS_UNVERIFIED", "{label}");
            assert_eq!(
                warning.message.contains("analysis uses the build snapshot"),
                claims_snapshot,
                "{label}: {}",
                warning.message
            );
            assert_eq!(
                warning.message.contains("existence of")
                    || warning.message.contains("Existence of"),
                mentions_existence,
                "{label}: {}",
                warning.message
            );
        }
    }
}
