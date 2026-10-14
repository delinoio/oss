//! Versioned result bundle (`analysis.json`) storage and validation.
//!
//! Saved bundles are untrusted input: the schema version, enum values,
//! identifiers and stored paths are validated before anything is served, and
//! stale checks only read safe project-relative paths.

use std::{
    collections::{BTreeMap, HashSet},
    path::{Path, PathBuf},
};

use serde::{Deserialize, Serialize};

use crate::{
    error::{BwError, BwResult, ErrorCode, problem},
    model::{Analysis, ProblemSeverity, RunStatus, SCHEMA_VERSION, SourceFile},
    sources::{
        as_text, is_binary_asset_path, is_generated_asset_module, safe_project_relative, sha256_hex,
    },
};

pub const BUNDLE_FILE: &str = "analysis.json";
pub const INCOMPLETE_MARKER: &str = "INCOMPLETE";
pub const SUMMARY_FILE: &str = "run-summary.json";
const MAX_SUMMARY_BYTES: u64 = 64 * 1024;
#[cfg(not(test))]
const MAX_BUNDLE_BYTES: u64 = 512 * 1024 * 1024;
// A small limit keeps oversized-result tests fast; behavior is identical.
#[cfg(test)]
const MAX_BUNDLE_BYTES: u64 = 1024 * 1024;

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ResultBundle {
    pub schema_version: u32,
    /// Absolute project root used only for local stale checks.
    #[serde(default)]
    pub project_root: String,
    pub analysis: Analysis,
    #[serde(default)]
    pub sources: Vec<SourceFile>,
    /// Source ID to safe project-relative path.
    #[serde(default)]
    pub project_relative: BTreeMap<String, String>,
    /// Source ID to safe project-relative path for sources whose saved hash
    /// cannot be compared with the live file (generated asset modules, binary
    /// files and modules derived through an import query). A reopened run only
    /// checks that the file still exists.
    #[serde(default)]
    pub existence_relative: BTreeMap<String, String>,
}

/// Keeps a result within the loader's size limit. When source content makes
/// it too large, source content and snippets are dropped and a problem says
/// so, instead of saving a run that `open` and `export` would reject.
pub fn fit_within_limit(bundle: &mut ResultBundle) -> BwResult<()> {
    let size = serde_json::to_vec(bundle).map_or(u64::MAX, |json| json.len() as u64);
    if size <= MAX_BUNDLE_BYTES {
        return Ok(());
    }
    tracing::warn!(
        size,
        limit = MAX_BUNDLE_BYTES,
        "result too large; dropping source content"
    );
    for source in &mut bundle.sources {
        source.content.clear();
        source.content_included = false;
    }
    for summary in &mut bundle.analysis.sources {
        summary.content_included = false;
    }
    for diagnostic in &mut bundle.analysis.diagnostics {
        diagnostic.snippet = None;
    }
    bundle.analysis.problems.push(problem(
        ErrorCode::ResultTooLarge,
        ProblemSeverity::Warning,
        format!(
            "The result exceeded {MAX_BUNDLE_BYTES} bytes, so source content and snippets were \
             not saved"
        ),
        "Use --exclude-sources for this project, or analyze a smaller build.",
    ));
    Ok(())
}

pub fn save(run_dir: &Path, bundle: &ResultBundle) -> BwResult<()> {
    let json = serde_json::to_vec(bundle).map_err(|error| {
        BwError::new(
            ErrorCode::ResultInvalid,
            format!("Could not serialize results: {error}"),
            "Report this as a bug.",
        )
    })?;
    if json.len() as u64 > MAX_BUNDLE_BYTES {
        return Err(BwError::new(
            ErrorCode::ResultTooLarge,
            format!(
                "The result is {} bytes, above the {MAX_BUNDLE_BYTES}-byte limit",
                json.len()
            ),
            "Use --exclude-sources, or analyze a smaller build.",
        ));
    }
    // The summary must never describe a bundle that is not committed: drop
    // the old one first, commit the bundle, then publish the new summary.
    let summary_path = run_dir.join(SUMMARY_FILE);
    let _ = std::fs::remove_file(&summary_path);
    let temporary = run_dir.join(format!("{BUNDLE_FILE}.tmp"));
    // std::fs::rename replaces an existing destination on every supported
    // platform, including Windows (MoveFileExW with replacement).
    std::fs::write(&temporary, json)
        .and_then(|()| std::fs::rename(&temporary, run_dir.join(BUNDLE_FILE)))
        .map_err(|error| {
            BwError::new(
                ErrorCode::OutputDirInvalid,
                format!(
                    "Could not write {}: {error}",
                    run_dir.join(BUNDLE_FILE).display()
                ),
                "Check that the output directory is writable and has free space.",
            )
        })?;
    let summary = serde_json::json!({
        "schemaVersion": bundle.schema_version,
        "runId": bundle.analysis.run_id,
        "status": bundle.analysis.status,
        "projectName": bundle.analysis.project_name,
        "bundler": bundle.analysis.engine.as_ref().map(|engine| engine.bundler).unwrap_or_default(),
        "startedAt": bundle.analysis.started_at,
    });
    let summary_temporary = run_dir.join(format!("{SUMMARY_FILE}.tmp"));
    let written = std::fs::write(&summary_temporary, summary.to_string())
        .and_then(|()| std::fs::rename(&summary_temporary, &summary_path));
    if let Err(error) = written {
        // Listings then read the committed bundle instead; no stale summary.
        tracing::warn!(%error, "run summary could not be written; listings will read the bundle");
        let _ = std::fs::remove_file(&summary_temporary);
        let _ = std::fs::remove_file(&summary_path);
    }
    Ok(())
}

fn invalid(message: impl Into<String>) -> BwError {
    BwError::new(
        ErrorCode::ResultInvalid,
        message,
        "The result bundle is damaged or was not written by bundlewhy. Start a new run with \
         `bundlewhy analyze`.",
    )
}

fn is_diagnostic_id(id: &str) -> bool {
    id.strip_prefix("d-")
        .is_some_and(|hex| hex.len() == 12 && hex.bytes().all(|byte| byte.is_ascii_hexdigit()))
}

fn is_source_id(id: &str) -> bool {
    id.strip_prefix("src-")
        .is_some_and(|hex| hex.len() == 16 && hex.bytes().all(|byte| byte.is_ascii_hexdigit()))
}

/// Parses and validates bundle bytes without touching the filesystem.
pub fn parse(bytes: &[u8]) -> BwResult<ResultBundle> {
    let value: serde_json::Value = serde_json::from_slice(bytes)
        .map_err(|error| invalid(format!("analysis.json is not valid JSON: {error}")))?;
    match value
        .get("schemaVersion")
        .and_then(serde_json::Value::as_u64)
    {
        Some(version) if version == u64::from(SCHEMA_VERSION) => {}
        Some(version) => {
            return Err(BwError::new(
                ErrorCode::SchemaUnsupported,
                format!(
                    "Result schema version {version} is not supported (this bundlewhy reads \
                     version {SCHEMA_VERSION})"
                ),
                "Open the result with the bundlewhy version that created it, or start a new run. \
                 Results are not migrated automatically.",
            ));
        }
        None => return Err(invalid("analysis.json has no numeric schemaVersion")),
    }
    let bundle: ResultBundle = serde_json::from_value(value)
        .map_err(|error| invalid(format!("analysis.json has an invalid structure: {error}")))?;
    validate(&bundle)?;
    Ok(bundle)
}

fn validate(bundle: &ResultBundle) -> BwResult<()> {
    let analysis = &bundle.analysis;
    if analysis.schema_version != SCHEMA_VERSION {
        return Err(invalid("analysis schemaVersion does not match the bundle"));
    }
    if uuid::Uuid::parse_str(&analysis.run_id).is_err() {
        return Err(invalid("runId is not a UUID"));
    }
    let mut ids = HashSet::new();
    for source in &bundle.sources {
        if !is_source_id(&source.id) || !ids.insert(source.id.as_str()) {
            return Err(invalid(format!(
                "invalid or duplicate source id {:?}",
                source.id
            )));
        }
        if source.path.contains('\0') {
            return Err(invalid("source path contains a NUL byte"));
        }
    }
    // The UI selects diagnostics by ID; duplicates would show the wrong one.
    let mut diagnostic_ids = HashSet::new();
    for diagnostic in &analysis.diagnostics {
        if !is_diagnostic_id(&diagnostic.id) || !diagnostic_ids.insert(diagnostic.id.as_str()) {
            return Err(invalid(format!(
                "invalid or duplicate diagnostic id {:?}",
                diagnostic.id
            )));
        }
    }
    for diagnostic in &analysis.diagnostics {
        for location in [&diagnostic.location, &diagnostic.generated_location]
            .into_iter()
            .flatten()
        {
            if !location.source_id.is_empty() && !ids.contains(location.source_id.as_str()) {
                return Err(invalid(format!(
                    "diagnostic {} references unknown source",
                    diagnostic.id
                )));
            }
        }
    }
    for (id, path) in bundle
        .project_relative
        .iter()
        .chain(&bundle.existence_relative)
    {
        if !ids.contains(id.as_str()) || safe_project_relative(path).is_none() {
            return Err(invalid(format!("unsafe project-relative path for {id}")));
        }
    }
    Ok(())
}

/// Reads `analysis.json` only when it is a regular file no larger than the
/// bundle limit. Shared by `load` and run listing.
/// Opens a file without following a symlink at the final component, closing
/// the race between the metadata check and the open on Unix.
fn open_no_follow(path: &Path) -> std::io::Result<std::fs::File> {
    let mut options = std::fs::OpenOptions::new();
    options.read(true);
    #[cfg(unix)]
    std::os::unix::fs::OpenOptionsExt::custom_flags(&mut options, libc::O_NOFOLLOW);
    let file = options.open(path)?;
    if !file.metadata()?.is_file() {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidData,
            "not a regular file",
        ));
    }
    Ok(file)
}

/// Reads a run summary through the same no-follow, bounded rules with a much
/// smaller limit.
pub fn read_summary_bytes(path: &Path) -> std::io::Result<Vec<u8>> {
    let metadata = std::fs::symlink_metadata(path)?;
    if !metadata.is_file() || metadata.len() > MAX_SUMMARY_BYTES {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidData,
            "not a bounded summary file",
        ));
    }
    let mut bytes = Vec::new();
    std::io::Read::read_to_end(
        &mut std::io::Read::take(open_no_follow(path)?, MAX_SUMMARY_BYTES + 1),
        &mut bytes,
    )?;
    Ok(bytes)
}

pub fn read_bundle_bytes(path: &Path) -> std::io::Result<Vec<u8>> {
    // Inspect the entry itself: a symlink could point at a bundle outside the
    // run directory.
    let metadata = std::fs::symlink_metadata(path)?;
    if !metadata.is_file() || metadata.len() > MAX_BUNDLE_BYTES {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidData,
            "not a regular result bundle file within the size limit",
        ));
    }
    let mut bytes = Vec::new();
    std::io::Read::read_to_end(
        &mut std::io::Read::take(open_no_follow(path)?, MAX_BUNDLE_BYTES + 1),
        &mut bytes,
    )?;
    if bytes.len() as u64 > MAX_BUNDLE_BYTES {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidData,
            "result bundle is too large",
        ));
    }
    Ok(bytes)
}

/// Status a saved run is presented with: a run left `RUNNING` by a process
/// that is gone is failed, and an unfinished run is never presented as
/// complete.
pub fn normalized_status(status: RunStatus, run_dir: &Path) -> RunStatus {
    match status {
        RunStatus::Running => RunStatus::Failed,
        RunStatus::Cancelled | RunStatus::Failed => status,
        _ if run_dir.join(INCOMPLETE_MARKER).exists() => RunStatus::Partial,
        _ => status,
    }
}

pub fn load(run_dir: &Path) -> BwResult<ResultBundle> {
    let path = run_dir.join(BUNDLE_FILE);
    if !path.exists() && std::fs::symlink_metadata(&path).is_err() {
        return Err(BwError::new(
            ErrorCode::ResultInvalid,
            format!("No result bundle at {}", path.display()),
            "Pass a run directory created by `bundlewhy analyze` (it contains analysis.json).",
        ));
    }
    // The run directory is untrusted: only a bounded regular file (never a
    // symlink or special file such as /dev/zero) is read.
    let bytes = read_bundle_bytes(&path).map_err(|error| {
        invalid(format!(
            "analysis.json must be a regular file of at most {MAX_BUNDLE_BYTES} bytes: {error}"
        ))
    })?;
    let mut bundle = parse(&bytes)?;
    let status = normalized_status(bundle.analysis.status, run_dir);
    if bundle.analysis.status == RunStatus::Running {
        bundle.analysis.problems.push(problem(
            ErrorCode::RunInterrupted,
            ProblemSeverity::Error,
            "The run was interrupted before it finished; results are incomplete",
            "Start a new run with `bundlewhy analyze`.",
        ));
    }
    bundle.analysis.status = status;
    mark_stale(&mut bundle);
    Ok(bundle)
}

/// Selects the entries of an analysis project-relative map that a saved bundle
/// keeps: project sources whose stored path is safe and stays inside the
/// project root.
///
/// The stored value is the path of the file that backs the module. It differs
/// from the display path of a module with an import query (`src/a.js?raw` is
/// backed by `src/a.js`), so the value is copied from the map and never derived
/// from the display path.
pub fn retained_relative(
    project_root: &Path,
    sources: &[SourceFile],
    map: &BTreeMap<String, String>,
) -> BTreeMap<String, String> {
    sources
        .iter()
        .filter(|source| source.origin == crate::model::SourceOrigin::Project)
        .filter_map(|source| {
            let relative = map.get(&source.id)?;
            let safe = safe_project_relative(relative)?;
            project_root
                .join(safe)
                .starts_with(project_root)
                .then(|| (source.id.clone(), relative.clone()))
        })
        .collect()
}

/// Largest project file read for a stale check.
const MAX_STALE_CHECK_BYTES: u64 = 16 * 1024 * 1024;

/// Reads a project file for a stale check only when it resolves to a regular
/// file inside the project root and is not larger than the limit. Special
/// files (for example `/dev/zero`) and symlinks leaving the root are refused.
fn read_project_file(canonical_root: &Path, relative: &Path) -> Option<Vec<u8>> {
    let resolved = std::fs::canonicalize(canonical_root.join(relative)).ok()?;
    if !resolved.starts_with(canonical_root) {
        return None;
    }
    let metadata = std::fs::metadata(&resolved).ok()?;
    if !metadata.is_file() || metadata.len() > MAX_STALE_CHECK_BYTES {
        return None;
    }
    let mut bytes = Vec::new();
    std::io::Read::read_to_end(
        &mut std::io::Read::take(
            std::fs::File::open(&resolved).ok()?,
            MAX_STALE_CHECK_BYTES + 1,
        ),
        &mut bytes,
    )
    .ok()?;
    (bytes.len() as u64 <= MAX_STALE_CHECK_BYTES).then_some(bytes)
}

/// True when the saved source is a bundler-generated asset module whose hash
/// cannot match the live file. Bundles saved before asset modules were left out
/// of the project-relative map still list such sources (for example a PNG
/// whose saved hash is the hash of Vite's generated stub), so the check also
/// runs when a saved run is reopened.
///
/// The stored text identifies a generated module. A bundle saved with
/// `--exclude-sources` has no stored text, so only a binary asset path with
/// live bytes that are not text is recognized. A text snapshot that is not a
/// generated module (ordinary code) is never skipped.
fn is_unverifiable_asset(source: &SourceFile, live: &[u8]) -> bool {
    if source.content_included {
        return is_generated_asset_module(&source.path, &source.content);
    }
    as_text(live).is_none() && is_binary_asset_path(&source.path)
}

/// Marks sources whose current project file differs from the saved hash and
/// reconciles the `BW_SOURCE_STALE` warning with the result.
///
/// The call is idempotent: every earlier `BW_SOURCE_STALE` problem (including
/// the build-time "changed after the build read them" warning) is replaced by
/// at most one warning that carries the current count, and no warning remains
/// when nothing is stale. The count covers every source that is stale after
/// the call: sources rechecked against their live file and sources that cannot
/// be rechecked (no project-relative or existence-only entry, such as a
/// dependency source or an asset saved by an earlier version) but keep their
/// saved stale flag. Other problems keep their order. When the project root cannot be verified, no
/// check is possible, so saved flags and warnings are left untouched.
pub fn mark_stale(bundle: &mut ResultBundle) {
    let root = PathBuf::from(&bundle.project_root);
    if bundle.project_root.is_empty() || !root.is_absolute() {
        return;
    }
    // The root comes from an untrusted bundle; it must resolve to a directory.
    let Some(canonical_root) = std::fs::canonicalize(&root)
        .ok()
        .filter(|path| path.is_dir())
    else {
        return;
    };
    for source in &mut bundle.sources {
        // A source listed for existence only (an asset module, a binary file
        // or a derived module) keeps no comparable hash: only its removal
        // counts.
        let (path, existence_only) = match bundle.project_relative.get(&source.id) {
            Some(path) => (path, false),
            None => match bundle.existence_relative.get(&source.id) {
                Some(path) => (path, true),
                None => continue,
            },
        };
        let Some(relative) = safe_project_relative(path) else {
            continue;
        };
        // A file that cannot be verified (too large, special, outside the
        // root) is not reported stale; only a removed file or a hash mismatch
        // is.
        let removed = std::fs::symlink_metadata(canonical_root.join(&relative)).is_err();
        if existence_only {
            source.stale = removed;
            continue;
        }
        let is_stale = read_project_file(&canonical_root, &relative).map_or(removed, |bytes| {
            if is_unverifiable_asset(source, &bytes) {
                tracing::debug!(path = %source.path, "asset stub source is not freshness-checked");
                false
            } else {
                sha256_hex(&bytes) != source.sha256
            }
        });
        source.stale = is_stale;
    }
    // Sources that were not rechecked keep their saved flag and still count.
    let stale = bundle.sources.iter().filter(|source| source.stale).count();
    let stale_ids: HashSet<String> = bundle
        .sources
        .iter()
        .filter(|source| source.stale)
        .map(|source| source.id.clone())
        .collect();
    for summary in &mut bundle.analysis.sources {
        summary.stale = stale_ids.contains(&summary.id);
    }
    let stale_code = ErrorCode::SourceStale.as_str();
    bundle
        .analysis
        .problems
        .retain(|entry| entry.code != stale_code);
    tracing::debug!(stale, "reconciled stale source warning");
    if stale > 0 {
        bundle.analysis.problems.push(problem(
            ErrorCode::SourceStale,
            ProblemSeverity::Warning,
            format!("{stale} source file(s) changed or were removed since this analysis"),
            "Saved locations and snippets describe the analyzed version. Start a new run to \
             analyze the current code.",
        ));
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::{Diagnostic, SourceLocation};

    fn bundle() -> ResultBundle {
        ResultBundle {
            schema_version: SCHEMA_VERSION,
            project_root: String::new(),
            analysis: Analysis {
                schema_version: SCHEMA_VERSION,
                run_id: uuid::Uuid::now_v7().to_string(),
                status: RunStatus::Complete,
                ..Analysis::default()
            },
            sources: vec![SourceFile {
                id: "src-0123456789abcdef".to_owned(),
                path: "src/a.js".to_owned(),
                ..SourceFile::default()
            }],
            project_relative: BTreeMap::new(),
            existence_relative: BTreeMap::new(),
        }
    }

    #[test]
    fn round_trips_and_rejects_unknown_schema() {
        let bytes = serde_json::to_vec(&bundle()).unwrap();
        assert!(parse(&bytes).is_ok());
        let mut value: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
        value["schemaVersion"] = serde_json::json!(2);
        let error = parse(&serde_json::to_vec(&value).unwrap()).unwrap_err();
        assert_eq!(error.code, ErrorCode::SchemaUnsupported);
        let error = parse(b"{\"analysis\":{}}").unwrap_err();
        assert_eq!(error.code, ErrorCode::ResultInvalid);
        let error = parse(b"not json").unwrap_err();
        assert_eq!(error.code, ErrorCode::ResultInvalid);
    }

    #[test]
    fn rejects_corrupted_structure_and_unknown_enums() {
        let mut value = serde_json::to_value(bundle()).unwrap();
        value["analysis"]["status"] = serde_json::json!("RUN_STATUS_EXPLODED");
        assert_eq!(
            parse(&serde_json::to_vec(&value).unwrap())
                .unwrap_err()
                .code,
            ErrorCode::ResultInvalid
        );

        let mut duplicate = bundle();
        for _ in 0..2 {
            duplicate.analysis.diagnostics.push(Diagnostic {
                id: "d-0123456789ab".to_owned(),
                ..Diagnostic::default()
            });
        }
        assert_eq!(
            parse(&serde_json::to_vec(&duplicate).unwrap())
                .unwrap_err()
                .code,
            ErrorCode::ResultInvalid
        );

        let mut broken = bundle();
        broken.analysis.diagnostics.push(Diagnostic {
            id: "d-0123456789ab".to_owned(),
            location: Some(SourceLocation {
                source_id: "src-ffffffffffffffff".to_owned(),
                ..SourceLocation::default()
            }),
            ..Diagnostic::default()
        });
        assert_eq!(
            parse(&serde_json::to_vec(&broken).unwrap())
                .unwrap_err()
                .code,
            ErrorCode::ResultInvalid
        );
    }

    #[test]
    fn rejects_path_traversal_in_project_relative_paths() {
        for path in ["../outside.js", "/etc/passwd", "src/../../x"] {
            let mut traversal = bundle();
            traversal
                .project_relative
                .insert("src-0123456789abcdef".to_owned(), path.to_owned());
            assert_eq!(
                parse(&serde_json::to_vec(&traversal).unwrap())
                    .unwrap_err()
                    .code,
                ErrorCode::ResultInvalid,
                "{path}"
            );
        }
    }

    #[test]
    fn oversized_unverifiable_sources_are_not_stale_but_removed_ones_are() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        let big = vec![b'a'; (MAX_STALE_CHECK_BYTES + 1) as usize];
        std::fs::write(project.join("src/big.js"), &big).unwrap();
        let mut saved = bundle();
        saved.project_root = project.to_string_lossy().into_owned();
        saved.sources[0].sha256 = sha256_hex(&big);
        saved
            .project_relative
            .insert("src-0123456789abcdef".to_owned(), "src/big.js".to_owned());
        mark_stale(&mut saved);
        assert!(!saved.sources[0].stale);
        std::fs::remove_file(project.join("src/big.js")).unwrap();
        mark_stale(&mut saved);
        assert!(saved.sources[0].stale);
    }

    fn stale_count(bundle: &ResultBundle) -> usize {
        bundle
            .analysis
            .problems
            .iter()
            .filter(|entry| entry.code == "BW_SOURCE_STALE")
            .count()
    }

    fn stale_fixture(dir: &Path, saved_text: &str) -> ResultBundle {
        let project = dir.join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        std::fs::write(project.join("src/a.js"), saved_text).unwrap();
        let mut saved = bundle();
        saved.project_root = project.to_string_lossy().into_owned();
        saved.sources[0].sha256 = sha256_hex(saved_text.as_bytes());
        saved.analysis.sources = vec![crate::model::SourceFileSummary {
            id: "src-0123456789abcdef".to_owned(),
            ..crate::model::SourceFileSummary::default()
        }];
        saved
            .project_relative
            .insert("src-0123456789abcdef".to_owned(), "src/a.js".to_owned());
        saved
    }

    #[test]
    fn mark_stale_is_idempotent_and_replaces_the_build_time_warning() {
        let dir = tempfile::tempdir().unwrap();
        let mut saved = stale_fixture(dir.path(), "original");
        std::fs::write(dir.path().join("project/src/a.js"), "changed").unwrap();
        saved.analysis.problems.push(problem(
            ErrorCode::SourceUnreadable,
            ProblemSeverity::Warning,
            "unreadable",
            "recover",
        ));
        saved.analysis.problems.push(problem(
            ErrorCode::SourceStale,
            ProblemSeverity::Warning,
            "9 source file(s) changed after the build read them",
            "build-time",
        ));
        saved.analysis.problems.push(problem(
            ErrorCode::RunInterrupted,
            ProblemSeverity::Error,
            "interrupted",
            "recover",
        ));
        for _ in 0..3 {
            mark_stale(&mut saved);
            assert_eq!(stale_count(&saved), 1);
        }
        let codes: Vec<_> = saved
            .analysis
            .problems
            .iter()
            .map(|entry| entry.code.as_str())
            .collect();
        assert_eq!(
            codes,
            [
                "BW_SOURCE_UNREADABLE",
                "BW_RUN_INTERRUPTED",
                "BW_SOURCE_STALE"
            ]
        );
        let warning = saved.analysis.problems.last().unwrap();
        assert_eq!(
            warning.message,
            "1 source file(s) changed or were removed since this analysis"
        );
        assert!(saved.sources[0].stale && saved.analysis.sources[0].stale);
    }

    #[test]
    fn restoring_a_changed_file_clears_the_warning_but_keeps_other_problems() {
        let dir = tempfile::tempdir().unwrap();
        let mut saved = stale_fixture(dir.path(), "original");
        saved.analysis.problems.push(problem(
            ErrorCode::RunInterrupted,
            ProblemSeverity::Error,
            "interrupted",
            "recover",
        ));
        let file = dir.path().join("project/src/a.js");
        std::fs::write(&file, "changed").unwrap();
        save(dir.path(), &saved).unwrap();
        let loaded = load(dir.path()).unwrap();
        assert_eq!(stale_count(&loaded), 1);
        assert!(loaded.sources[0].stale && loaded.analysis.sources[0].stale);

        std::fs::remove_file(&file).unwrap();
        let removed = load(dir.path()).unwrap();
        assert_eq!(stale_count(&removed), 1);
        assert!(removed.sources[0].stale);

        std::fs::write(&file, "original").unwrap();
        let restored = load(dir.path()).unwrap();
        assert_eq!(stale_count(&restored), 0);
        assert!(!restored.sources[0].stale && !restored.analysis.sources[0].stale);
        assert_eq!(restored.analysis.problems.len(), 1);
        assert_eq!(restored.analysis.problems[0].code, "BW_RUN_INTERRUPTED");
    }

    #[test]
    fn a_saved_build_time_warning_is_dropped_when_the_checked_source_is_unchanged() {
        let dir = tempfile::tempdir().unwrap();
        let mut saved = stale_fixture(dir.path(), "original");
        saved.analysis.problems.push(problem(
            ErrorCode::SourceStale,
            ProblemSeverity::Warning,
            "1 source file(s) changed after the build read them",
            "build-time",
        ));
        mark_stale(&mut saved);
        assert_eq!(stale_count(&saved), 0);
    }

    #[test]
    fn an_unverifiable_project_root_leaves_saved_flags_and_warnings_untouched() {
        let mut saved = bundle();
        saved.project_root = "relative/root".to_owned();
        saved.sources[0].stale = true;
        saved.analysis.problems.push(problem(
            ErrorCode::SourceStale,
            ProblemSeverity::Warning,
            "build-time",
            "recover",
        ));
        mark_stale(&mut saved);
        assert!(saved.sources[0].stale);
        assert_eq!(stale_count(&saved), 1);
    }

    #[test]
    fn oversized_results_drop_source_content_instead_of_saving_unreadable_runs() {
        let mut large = bundle();
        large.sources[0].content = "x".repeat((MAX_BUNDLE_BYTES + 1) as usize);
        large.sources[0].content_included = true;
        fit_within_limit(&mut large).unwrap();
        assert!(large.sources[0].content.is_empty());
        assert!(
            large
                .analysis
                .problems
                .iter()
                .any(|entry| entry.code == "BW_RESULT_TOO_LARGE")
        );
    }

    #[test]
    fn a_failed_bundle_write_leaves_no_summary_claiming_completion() {
        let dir = tempfile::tempdir().unwrap();
        let mut saved = bundle();
        saved.analysis.status = RunStatus::Running;
        save(dir.path(), &saved).unwrap();
        assert!(
            std::fs::read_to_string(dir.path().join(SUMMARY_FILE))
                .unwrap()
                .contains("RUN_STATUS_RUNNING")
        );
        // Block the bundle commit: a directory where the temporary file goes.
        std::fs::create_dir(dir.path().join(format!("{BUNDLE_FILE}.tmp"))).unwrap();
        saved.analysis.status = RunStatus::Complete;
        assert!(save(dir.path(), &saved).is_err());
        assert!(!dir.path().join(SUMMARY_FILE).exists());
    }

    #[cfg(unix)]
    #[test]
    fn load_refuses_symlinked_bundles() {
        let dir = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        save(outside.path(), &bundle()).unwrap();
        std::os::unix::fs::symlink(
            outside.path().join(BUNDLE_FILE),
            dir.path().join(BUNDLE_FILE),
        )
        .unwrap();
        assert_eq!(load(dir.path()).unwrap_err().code, ErrorCode::ResultInvalid);
    }

    #[cfg(unix)]
    #[test]
    fn load_refuses_special_bundle_files() {
        let dir = tempfile::tempdir().unwrap();
        std::os::unix::fs::symlink("/dev/zero", dir.path().join(BUNDLE_FILE)).unwrap();
        assert_eq!(load(dir.path()).unwrap_err().code, ErrorCode::ResultInvalid);
    }

    #[cfg(unix)]
    #[test]
    fn stale_checks_refuse_special_files_and_escaping_symlinks() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(&project).unwrap();
        std::fs::write(dir.path().join("outside.js"), "original").unwrap();
        std::os::unix::fs::symlink(dir.path().join("outside.js"), project.join("escape.js"))
            .unwrap();
        std::os::unix::fs::symlink("/dev/zero", project.join("zero.js")).unwrap();
        let canonical = std::fs::canonicalize(&project).unwrap();
        assert!(read_project_file(&canonical, Path::new("escape.js")).is_none());
        assert!(read_project_file(&canonical, Path::new("zero.js")).is_none());
        std::fs::write(project.join("inside.js"), "ok").unwrap();
        assert_eq!(
            read_project_file(&canonical, Path::new("inside.js")).unwrap(),
            b"ok"
        );
    }

    #[test]
    fn interrupted_runs_reopen_as_failed_and_stale_sources_are_marked() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        std::fs::write(project.join("src/a.js"), "changed").unwrap();
        let mut saved = bundle();
        saved.analysis.status = RunStatus::Running;
        saved.project_root = project.to_string_lossy().into_owned();
        saved.sources[0].sha256 = sha256_hex(b"original");
        saved
            .project_relative
            .insert("src-0123456789abcdef".to_owned(), "src/a.js".to_owned());
        save(dir.path(), &saved).unwrap();
        let loaded = load(dir.path()).unwrap();
        assert_eq!(loaded.analysis.status, RunStatus::Failed);
        assert!(
            loaded
                .analysis
                .problems
                .iter()
                .any(|entry| entry.code == "BW_RUN_INTERRUPTED")
        );
        assert!(loaded.sources[0].stale);
        assert!(
            loaded
                .analysis
                .problems
                .iter()
                .any(|entry| entry.code == "BW_SOURCE_STALE")
        );
    }

    #[test]
    fn reopened_bundles_do_not_mark_non_text_assets_stale() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        std::fs::write(
            project.join("src/logo.png"),
            [0x89, b'P', b'N', b'G', 0, 0xff],
        )
        .unwrap();
        std::fs::write(project.join("src/a.js"), "export const a = 1;").unwrap();
        let canonical = std::fs::canonicalize(&project).unwrap();
        let mut store = crate::sources::SourceStore::new(true);
        for (name, snapshot) in [
            ("src/logo.png", "export default \"__VITE_ASSET__abc__\""),
            ("src/a.js", "export const a = 1;"),
        ] {
            store.snapshots.insert(
                crate::sources::normalize(&canonical.join(name)),
                snapshot.to_owned(),
            );
            store.add_file(&canonical, &canonical.join(name)).unwrap();
        }
        assert!(store.changed_since_build.is_empty());
        let project_relative = store.project_relative.clone();
        let mut saved = bundle();
        saved.project_root = canonical.to_string_lossy().into_owned();
        saved.sources = store.into_files();
        saved.project_relative = project_relative;
        assert_eq!(saved.sources.len(), 2);
        assert_eq!(saved.project_relative.len(), 1);
        save(dir.path(), &saved).unwrap();
        let loaded = load(dir.path()).unwrap();
        assert!(loaded.sources.iter().all(|source| !source.stale));
        assert!(
            !loaded
                .analysis
                .problems
                .iter()
                .any(|entry| entry.code == "BW_SOURCE_STALE")
        );

        // A text source edited after the analysis is still reported once.
        std::fs::write(project.join("src/a.js"), "export const a = 2;").unwrap();
        let loaded = load(dir.path()).unwrap();
        let stale: Vec<_> = loaded.sources.iter().filter(|s| s.stale).collect();
        assert_eq!(stale.len(), 1);
        assert_eq!(stale[0].path, "src/a.js");
        assert_eq!(
            loaded
                .analysis
                .problems
                .iter()
                .filter(|entry| entry.code == "BW_SOURCE_STALE")
                .count(),
            1
        );
    }

    /// Bundles saved before generated asset modules were left out of the
    /// project-relative map still list them, with the hash of the generated
    /// module. Reopening must not compare that hash with the live asset.
    #[test]
    fn reopened_saved_bundles_skip_stored_asset_modules_and_keep_text_checks() {
        const PNG: &[u8] = &[0x89, b'P', b'N', b'G', 0, 0xff, 0xfe];
        const SVG: &str = "<svg xmlns=\"http://www.w3.org/2000/svg\"/>";
        const MARKER: &str = "export default \"__VITE_ASSET__abc__\"";
        const PLACEHOLDER: &str = "export default import.meta.ROLLDOWN_FILE_URL_abc123";
        const DATA_URL: &str = "export default \"data:image/svg+xml,%3Csvg%2F%3E\"";
        const RAW: &str = "export default \"<svg\\n/>\"";
        const TEXT: &str = "export const a = 1;";
        // (file, stored text, content kept, live bytes or removed, expected
        // stale)
        type Case = (
            &'static str,
            &'static str,
            bool,
            Option<&'static [u8]>,
            bool,
        );
        let cases: &[Case] = &[
            // Stored generated asset modules are never compared.
            ("src/logo.png", MARKER, true, Some(PNG), false),
            ("src/logo.png", DATA_URL, true, Some(PNG), false),
            ("src/icon.svg", MARKER, true, Some(SVG.as_bytes()), false),
            (
                "src/icon.svg",
                PLACEHOLDER,
                true,
                Some(SVG.as_bytes()),
                false,
            ),
            ("src/icon.svg", DATA_URL, true, Some(SVG.as_bytes()), false),
            ("src/icon.svg?raw", RAW, true, Some(SVG.as_bytes()), false),
            // A removed asset is still a change.
            ("src/logo.png", MARKER, true, None, true),
            ("src/icon.svg", PLACEHOLDER, true, None, true),
            // Without stored text only a non-text binary asset is recognized.
            ("src/logo.png", MARKER, false, Some(PNG), false),
            ("src/logo.png", MARKER, false, None, true),
            ("src/main.js", TEXT, false, Some(PNG), true),
            // Ordinary text snapshots keep being compared.
            ("src/main.js", TEXT, true, Some(TEXT.as_bytes()), false),
            (
                "src/main.js",
                TEXT,
                true,
                Some(b"export const a = 2;"),
                true,
            ),
            ("src/main.js", TEXT, true, Some(PNG), true),
            ("src/main.js", TEXT, true, None, true),
            (
                "src/label.js",
                "export default \"hello\";",
                true,
                Some(b"changed"),
                true,
            ),
            (
                "src/label.js",
                "export default \"hello\";",
                true,
                Some(b"export default \"hello\";"),
                false,
            ),
        ];
        for (file, stored, content_kept, live, expected) in cases {
            let dir = tempfile::tempdir().unwrap();
            let project = dir.path().join("project");
            std::fs::create_dir_all(project.join("src")).unwrap();
            let relative = file.split('?').next().unwrap();
            if let Some(bytes) = live {
                std::fs::write(project.join(relative), bytes).unwrap();
            }
            let mut saved = bundle();
            saved.project_root = std::fs::canonicalize(&project)
                .unwrap()
                .to_string_lossy()
                .into_owned();
            saved.sources[0].path = (*file).to_owned();
            saved.sources[0].sha256 = sha256_hex(stored.as_bytes());
            saved.sources[0].content_included = *content_kept;
            if *content_kept {
                saved.sources[0].content = (*stored).to_owned();
            }
            saved.analysis.sources = vec![crate::model::SourceFileSummary {
                id: "src-0123456789abcdef".to_owned(),
                ..crate::model::SourceFileSummary::default()
            }];
            saved
                .project_relative
                .insert("src-0123456789abcdef".to_owned(), relative.to_owned());
            save(dir.path(), &saved).unwrap();
            let loaded = load(dir.path()).unwrap();
            assert_eq!(loaded.sources[0].stale, *expected, "{file} {stored}");
            assert_eq!(loaded.analysis.sources[0].stale, *expected, "{file}");
            let warnings = loaded
                .analysis
                .problems
                .iter()
                .filter(|entry| entry.code == "BW_SOURCE_STALE")
                .count();
            assert_eq!(warnings, usize::from(*expected), "{file} {stored}");
        }
    }

    /// A source listed for an existence check keeps no comparable hash: its
    /// content never makes it stale, only its removal does, and a restored file
    /// clears the flag.
    #[test]
    fn existence_checked_sources_become_stale_only_when_removed() {
        const PNG: &[u8] = &[0x89, b'P', b'N', b'G', 0, 0xff, 0xfe];
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        let file = project.join("src/logo.png");
        std::fs::write(&file, PNG).unwrap();
        let mut saved = bundle();
        saved.project_root = std::fs::canonicalize(&project)
            .unwrap()
            .to_string_lossy()
            .into_owned();
        // The saved hash is the hash of another revision of the file.
        saved.sources[0].path = "src/logo.png".to_owned();
        saved.sources[0].sha256 = sha256_hex(b"revision at analysis time");
        saved.sources[0].content_included = false;
        saved.analysis.sources = vec![crate::model::SourceFileSummary {
            id: "src-0123456789abcdef".to_owned(),
            ..crate::model::SourceFileSummary::default()
        }];
        saved
            .existence_relative
            .insert("src-0123456789abcdef".to_owned(), "src/logo.png".to_owned());
        save(dir.path(), &saved).unwrap();

        let steps: [(Option<&[u8]>, bool); 5] = [
            (Some(PNG), false),
            (Some(b"replaced by other bytes"), false),
            (None, true),
            (Some(PNG), false),
            (None, true),
        ];
        for (live, expected) in steps {
            match live {
                Some(bytes) => std::fs::write(&file, bytes).unwrap(),
                None => {
                    let _ = std::fs::remove_file(&file);
                }
            }
            for _ in 0..2 {
                let loaded = load(dir.path()).unwrap();
                assert_eq!(loaded.sources[0].stale, expected);
                assert_eq!(loaded.analysis.sources[0].stale, expected);
                assert_eq!(
                    loaded
                        .analysis
                        .problems
                        .iter()
                        .filter(|entry| entry.code == "BW_SOURCE_STALE")
                        .count(),
                    usize::from(expected)
                );
            }
        }
    }

    /// Unsafe paths in either relative map are rejected before any check.
    #[test]
    fn rejects_unsafe_existence_relative_paths() {
        for path in ["../outside.png", "/etc/passwd", "src/../../x", "a\\b", ""] {
            let mut traversal = bundle();
            traversal
                .existence_relative
                .insert("src-0123456789abcdef".to_owned(), path.to_owned());
            assert_eq!(
                parse(&serde_json::to_vec(&traversal).unwrap())
                    .unwrap_err()
                    .code,
                ErrorCode::ResultInvalid,
                "{path:?}"
            );
        }
        let mut unknown = bundle();
        unknown
            .existence_relative
            .insert("src-ffffffffffffffff".to_owned(), "src/a.png".to_owned());
        assert!(parse(&serde_json::to_vec(&unknown).unwrap()).is_err());
        // Bundles saved before the map existed still load.
        let mut json = serde_json::to_value(bundle()).unwrap();
        json.as_object_mut().unwrap().remove("existenceRelative");
        assert!(parse(&serde_json::to_vec(&json).unwrap()).is_ok());
    }

    /// Analysis to reopen: assets, binary files and query modules that existed
    /// during the analysis are reported once they are removed, while edits to
    /// their content never are. Query modules are checked through their backing
    /// file.
    #[test]
    fn removed_assets_and_query_modules_are_reported_after_a_saved_run_is_reopened() {
        const PNG: &[u8] = &[0x89, b'P', b'N', b'G', 0, 0xff, 0xfe];
        const SVG: &str = "<svg xmlns=\"http://www.w3.org/2000/svg\"/>";
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        let canonical = std::fs::canonicalize(&project).unwrap();
        // (module path, backing file, bytes, snapshot)
        let entries: &[(&str, &str, &[u8], Option<&str>)] = &[
            // Generated stub of a PNG.
            (
                "src/logo.png",
                "src/logo.png",
                PNG,
                Some("export default \"__VITE_ASSET__abc__\""),
            ),
            // Binary file read without a snapshot.
            ("src/photo.png", "src/photo.png", PNG, None),
            // Text asset inlined as a data URL.
            (
                "src/icon.svg?inline",
                "src/icon.svg",
                SVG.as_bytes(),
                Some("export default \"data:image/svg+xml,%3Csvg%2F%3E\""),
            ),
            // A script imported as text.
            (
                "src/a.js?raw",
                "src/a.js",
                b"export const a = 1;",
                Some("export default \"export const a = 1;\""),
            ),
            // An ordinary text source.
            (
                "src/main.js",
                "src/main.js",
                b"export const m = 1;",
                Some("export const m = 1;"),
            ),
        ];
        let mut store = crate::sources::SourceStore::new(true);
        for (module, backing, bytes, snapshot) in entries {
            std::fs::write(canonical.join(backing), bytes).unwrap();
            let path = canonical.join(module);
            if let Some(snapshot) = snapshot {
                store
                    .snapshots
                    .insert(crate::sources::normalize(&path), (*snapshot).to_owned());
            }
            store.add_file(&canonical, &path).unwrap();
        }
        assert!(store.changed_since_build.is_empty());
        assert_eq!(store.project_relative.len(), 1);
        assert_eq!(store.existence_relative.len(), 4);
        let store_project_relative = store.project_relative.clone();
        let store_existence_relative = store.existence_relative.clone();
        let stale_warning_count = |bundle: &ResultBundle| {
            bundle
                .analysis
                .problems
                .iter()
                .filter(|entry| entry.code == "BW_SOURCE_STALE")
                .count()
        };
        let mut saved = bundle();
        saved.project_root = canonical.to_string_lossy().into_owned();
        saved.sources = store.into_files();
        saved.analysis.sources = saved.sources.iter().map(SourceFile::summary).collect();
        // The analysis maps hold the backing files; the saved maps keep them.
        saved.project_relative =
            retained_relative(&canonical, &saved.sources, &store_project_relative);
        saved.existence_relative =
            retained_relative(&canonical, &saved.sources, &store_existence_relative);
        assert_eq!(saved.project_relative.len(), 1);
        assert_eq!(saved.existence_relative.len(), 4);
        let mut backing: Vec<_> = saved
            .project_relative
            .values()
            .chain(saved.existence_relative.values())
            .map(String::as_str)
            .collect();
        backing.sort_unstable();
        assert_eq!(
            backing,
            [
                "src/a.js",
                "src/icon.svg",
                "src/logo.png",
                "src/main.js",
                "src/photo.png"
            ]
        );
        save(dir.path(), &saved).unwrap();

        let loaded = load(dir.path()).unwrap();
        assert!(loaded.sources.iter().all(|source| !source.stale));
        assert_eq!(stale_warning_count(&loaded), 0);

        // Content edits never make an existence-checked source stale; an edit
        // to the ordinary source does.
        std::fs::write(canonical.join("src/logo.png"), b"other bytes").unwrap();
        std::fs::write(canonical.join("src/icon.svg"), "<svg changed/>").unwrap();
        std::fs::write(canonical.join("src/a.js"), "export const a = 2;").unwrap();
        let loaded = load(dir.path()).unwrap();
        assert_eq!(stale_warning_count(&loaded), 0);

        // Removing each file reports exactly the sources it backs, once.
        let stale_paths = |bundle: &ResultBundle| -> Vec<String> {
            let mut paths: Vec<_> = bundle
                .sources
                .iter()
                .filter(|source| source.stale)
                .map(|source| source.path.clone())
                .collect();
            paths.sort_unstable();
            paths
        };
        std::fs::remove_file(canonical.join("src/logo.png")).unwrap();
        let loaded = load(dir.path()).unwrap();
        assert_eq!(stale_paths(&loaded), ["src/logo.png"]);
        assert_eq!(stale_warning_count(&loaded), 1);
        std::fs::remove_file(canonical.join("src/a.js")).unwrap();
        std::fs::remove_file(canonical.join("src/photo.png")).unwrap();
        std::fs::remove_file(canonical.join("src/main.js")).unwrap();
        let loaded = load(dir.path()).unwrap();
        assert_eq!(
            stale_paths(&loaded),
            [
                "src/a.js?raw",
                "src/logo.png",
                "src/main.js",
                "src/photo.png"
            ]
        );
        assert_eq!(stale_warning_count(&loaded), 1);
        assert!(
            loaded.analysis.problems.iter().any(|entry| entry.message
                == "4 source file(s) changed or were removed since this analysis")
        );
        // The query module follows its backing file; restoring it clears it.
        std::fs::write(canonical.join("src/a.js"), "export const a = 1;").unwrap();
        let loaded = load(dir.path()).unwrap();
        assert!(!stale_paths(&loaded).contains(&"src/a.js?raw".to_owned()));
    }

    #[test]
    fn retained_relative_keeps_the_backing_file_of_safe_project_sources() {
        let root = Path::new("/work/app");
        let source = |id: &str, path: &str, origin| SourceFile {
            id: id.to_owned(),
            path: path.to_owned(),
            origin,
            ..SourceFile::default()
        };
        use crate::model::SourceOrigin::{Dependency, Project};
        let sources = vec![
            // A module with an import query is displayed with it, but the map
            // names the backing file.
            source("a", "src/a.js?raw", Project),
            source("b", "src/b.js", Project),
            source("c", "node_modules/x/index.js", Dependency),
            source("d", "src/d.js", Project),
            source("e", "src/e.js", Project),
            source("f", "src/f.js", Project),
        ];
        let map: BTreeMap<String, String> = [
            ("a", "src/a.js"),
            ("b", "src/b.js"),
            ("c", "node_modules/x/index.js"),
            ("d", "../escape.js"),
            ("e", "/etc/passwd"),
            ("unknown", "src/u.js"),
        ]
        .into_iter()
        .map(|(id, path)| (id.to_owned(), path.to_owned()))
        .collect();
        let kept = retained_relative(root, &sources, &map);
        assert_eq!(
            kept.iter()
                .map(|(id, path)| (id.as_str(), path.as_str()))
                .collect::<Vec<_>>(),
            [("a", "src/a.js"), ("b", "src/b.js")]
        );
    }

    fn sources_with_flags(entries: &[(&str, bool, Option<&str>)]) -> ResultBundle {
        // (id, saved stale flag, project-relative path)
        let mut saved = bundle();
        saved.sources.clear();
        saved.analysis.sources.clear();
        for (id, stale, relative) in entries {
            saved.sources.push(SourceFile {
                id: (*id).to_owned(),
                path: format!("src/{id}.js"),
                stale: *stale,
                sha256: sha256_hex(b"saved"),
                ..SourceFile::default()
            });
            saved
                .analysis
                .sources
                .push(crate::model::SourceFileSummary {
                    id: (*id).to_owned(),
                    stale: *stale,
                    ..crate::model::SourceFileSummary::default()
                });
            if let Some(relative) = relative {
                saved
                    .project_relative
                    .insert((*id).to_owned(), (*relative).to_owned());
            }
        }
        saved
    }

    /// Every stale source counts toward the single warning, whether it was
    /// rechecked against its live file or kept its saved flag.
    #[test]
    fn warning_counts_rechecked_and_retained_stale_sources() {
        // (entries, existing warning, expected warning count message)
        type Entry = (&'static str, bool, Option<&'static str>);
        let cases: &[(&[Entry], bool, Option<usize>)] = &[
            // Not listed for a recheck, saved stale: the flag and the warning stay.
            (&[("a", true, None)], true, Some(1)),
            (&[("a", true, None)], false, Some(1)),
            // An unsafe stored path is never read; the saved flag is retained.
            (&[("a", true, Some("../escape.js"))], true, Some(1)),
            // A retained flag and a rechecked changed source count together.
            (
                &[("a", true, None), ("b", false, Some("src/b.js"))],
                true,
                Some(2),
            ),
            // A rechecked unchanged source clears its own flag only.
            (
                &[("a", true, None), ("c", true, Some("src/c.js"))],
                true,
                Some(1),
            ),
            // Nothing stale: an earlier build-time warning is dropped.
            (&[("a", false, None)], true, None),
            (&[("c", true, Some("src/c.js"))], true, None),
        ];
        for (entries, existing_warning, expected) in cases {
            let dir = tempfile::tempdir().unwrap();
            let project = dir.path().join("project");
            std::fs::create_dir_all(project.join("src")).unwrap();
            // `b` differs from its saved hash; `c` matches it.
            std::fs::write(project.join("src/b.js"), "changed").unwrap();
            std::fs::write(project.join("src/c.js"), "saved").unwrap();
            let mut saved = sources_with_flags(entries);
            saved.project_root = project.to_string_lossy().into_owned();
            if *existing_warning {
                saved.analysis.problems.push(problem(
                    ErrorCode::SourceStale,
                    ProblemSeverity::Warning,
                    "9 source file(s) changed after the build read them",
                    "build-time",
                ));
            }
            for _ in 0..2 {
                mark_stale(&mut saved);
                let warnings: Vec<_> = saved
                    .analysis
                    .problems
                    .iter()
                    .filter(|entry| entry.code == "BW_SOURCE_STALE")
                    .collect();
                match expected {
                    Some(count) => {
                        assert_eq!(warnings.len(), 1, "{entries:?}");
                        assert_eq!(
                            warnings[0].message,
                            format!(
                                "{count} source file(s) changed or were removed since this \
                                 analysis"
                            )
                        );
                    }
                    None => assert!(warnings.is_empty(), "{entries:?}"),
                }
                // Source and summary flags agree after every call.
                for (source, summary) in saved.sources.iter().zip(&saved.analysis.sources) {
                    assert_eq!(source.stale, summary.stale, "{entries:?}");
                }
            }
        }
    }

    /// A run saved by an earlier version holds a Vite asset whose file was
    /// already missing during analysis as stale without any recheck entry. The
    /// warning must survive a reopen and an export.
    #[test]
    fn stale_asset_stub_with_a_missing_file_keeps_its_warning_on_reopen_and_export() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        std::fs::write(project.join("src/a.js"), "export const a = 1;").unwrap();
        let canonical = std::fs::canonicalize(&project).unwrap();
        let mut store = crate::sources::SourceStore::new(true);
        for (name, snapshot) in [
            ("src/gone.png", "export default \"__VITE_ASSET__abc__\""),
            ("src/a.js", "export const a = 1;"),
        ] {
            store.snapshots.insert(
                crate::sources::normalize(&canonical.join(name)),
                snapshot.to_owned(),
            );
            store.add_file(&canonical, &canonical.join(name)).unwrap();
        }
        assert_eq!(store.changed_since_build, vec!["src/gone.png".to_owned()]);
        let project_relative = store.project_relative.clone();
        let mut saved = bundle();
        saved.project_root = canonical.to_string_lossy().into_owned();
        saved.sources = store.into_files();
        saved.analysis.sources = saved
            .sources
            .iter()
            .map(|source| crate::model::SourceFileSummary {
                id: source.id.clone(),
                stale: source.stale,
                ..crate::model::SourceFileSummary::default()
            })
            .collect();
        saved.project_relative = project_relative;
        // The stale asset is saved without a recheck entry.
        let gone = saved
            .sources
            .iter()
            .find(|source| source.path == "src/gone.png")
            .unwrap()
            .id
            .clone();
        assert!(!saved.project_relative.contains_key(&gone));
        saved.analysis.problems.push(problem(
            ErrorCode::SourceStale,
            ProblemSeverity::Warning,
            "1 source file(s) changed after the build read them",
            "build-time",
        ));
        save(dir.path(), &saved).unwrap();
        for _ in 0..2 {
            let loaded = load(dir.path()).unwrap();
            let warnings: Vec<_> = loaded
                .analysis
                .problems
                .iter()
                .filter(|entry| entry.code == "BW_SOURCE_STALE")
                .collect();
            assert_eq!(warnings.len(), 1);
            assert_eq!(
                warnings[0].message,
                "1 source file(s) changed or were removed since this analysis"
            );
            assert!(
                loaded
                    .sources
                    .iter()
                    .any(|source| source.id == gone && source.stale)
            );
            assert!(
                loaded
                    .analysis
                    .sources
                    .iter()
                    .any(|s| s.id == gone && s.stale)
            );
            for include_sources in [true, false] {
                let html = crate::export::render(
                    "<html><head></head><body></body></html>",
                    &loaded.analysis,
                    &loaded.sources,
                    include_sources,
                );
                let marker = "id=\"bundlewhy-report\">";
                let start = html.find(marker).unwrap() + marker.len();
                let end = start + html[start..].find("</script>").unwrap();
                let payload: serde_json::Value = serde_json::from_str(&html[start..end]).unwrap();
                let stale_warnings = payload["analysis"]["problems"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .filter(|entry| entry["code"] == "BW_SOURCE_STALE")
                    .count();
                assert_eq!(stale_warnings, 1);
                assert!(
                    payload["sources"]
                        .as_array()
                        .unwrap()
                        .iter()
                        .any(|source| source["id"] == gone.as_str() && source["stale"] == true)
                );
            }
        }
    }
}
