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
const MAX_BUNDLE_BYTES: u64 = 512 * 1024 * 1024;

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

pub fn save(run_dir: &Path, bundle: &ResultBundle) -> BwResult<()> {
    let json = serde_json::to_vec(bundle).map_err(|error| {
        BwError::new(
            ErrorCode::ResultInvalid,
            format!("Could not serialize results: {error}"),
            "Report this as a bug.",
        )
    })?;
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
pub fn read_bundle_bytes(path: &Path) -> std::io::Result<Vec<u8>> {
    let metadata = std::fs::metadata(path)?;
    if !metadata.is_file() || metadata.len() > MAX_BUNDLE_BYTES {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidData,
            "not a regular result bundle file within the size limit",
        ));
    }
    let mut bytes = Vec::new();
    std::io::Read::read_to_end(
        &mut std::io::Read::take(std::fs::File::open(path)?, MAX_BUNDLE_BYTES + 1),
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
    let metadata = std::fs::metadata(&path).map_err(|error| {
        BwError::new(
            ErrorCode::ResultInvalid,
            format!("No result bundle at {}: {error}", path.display()),
            "Pass a run directory created by `bundlewhy analyze` (it contains analysis.json).",
        )
    })?;
    // The run directory is untrusted: refuse special files (a symlink to
    // /dev/zero reports a small length but never reaches EOF) and bound the
    // read.
    if !metadata.is_file() {
        return Err(invalid("analysis.json is not a regular file"));
    }
    if metadata.len() > MAX_BUNDLE_BYTES {
        return Err(invalid(format!(
            "analysis.json is larger than {MAX_BUNDLE_BYTES} bytes"
        )));
    }
    let mut bytes = Vec::new();
    std::fs::File::open(&path)
        .and_then(|file| {
            std::io::Read::read_to_end(
                &mut std::io::Read::take(file, MAX_BUNDLE_BYTES + 1),
                &mut bytes,
            )
        })
        .map_err(|error| invalid(format!("Could not read {}: {error}", path.display())))?;
    if bytes.len() as u64 > MAX_BUNDLE_BYTES {
        return Err(invalid(format!(
            "analysis.json is larger than {MAX_BUNDLE_BYTES} bytes"
        )));
    }
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
        let is_stale = read_project_file(&canonical_root, &relative)
            .is_none_or(|bytes| sha256_hex(&bytes) != source.sha256);
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

        let mut broken = bundle();
        broken.analysis.diagnostics.push(Diagnostic {
            id: "d-1".to_owned(),
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
