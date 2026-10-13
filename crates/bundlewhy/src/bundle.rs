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
    sources::{safe_project_relative, sha256_hex},
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
    // A small summary lets run listings avoid reading full bundles.
    let summary = serde_json::json!({
        "schemaVersion": bundle.schema_version,
        "runId": bundle.analysis.run_id,
        "status": bundle.analysis.status,
        "projectName": bundle.analysis.project_name,
        "bundler": bundle.analysis.engine.as_ref().map(|engine| engine.bundler).unwrap_or_default(),
        "startedAt": bundle.analysis.started_at,
    });
    let _ = std::fs::write(run_dir.join(SUMMARY_FILE), summary.to_string());
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
        })
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
    for (id, path) in &bundle.project_relative {
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

/// Marks sources whose current project file differs from the saved hash.
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
    let mut stale = 0usize;
    for source in &mut bundle.sources {
        let Some(relative) = bundle
            .project_relative
            .get(&source.id)
            .and_then(|path| safe_project_relative(path))
        else {
            continue;
        };
        // A file that cannot be verified (too large, special, outside the
        // root) is not reported stale; only a removed file or a hash mismatch
        // is.
        let removed = std::fs::symlink_metadata(canonical_root.join(&relative)).is_err();
        let is_stale = read_project_file(&canonical_root, &relative)
            .map_or(removed, |bytes| sha256_hex(&bytes) != source.sha256);
        source.stale = is_stale;
        stale += usize::from(is_stale);
    }
    let stale_ids: HashSet<String> = bundle
        .sources
        .iter()
        .filter(|source| source.stale)
        .map(|source| source.id.clone())
        .collect();
    for summary in &mut bundle.analysis.sources {
        summary.stale = stale_ids.contains(&summary.id);
    }
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
}
