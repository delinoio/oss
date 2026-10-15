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
    model::{
        Analysis, Diagnostic, ExportReference, ExportScope, ExportUsage, ExportUsageEdge,
        ExportUsagePathEnd, ExportUsageState, ExportUsageTruncation, ProblemSeverity, RunStatus,
        SCHEMA_VERSION, SourceFile, SourceRevisionEvidence,
    },
    rules::export_usage::{MAX_EDGES_PER_PATH, MAX_PATHS},
    sources::{
        LiveFileFailure, as_text, filesystem_path, freshness_unverified_problem,
        is_binary_asset_path, is_generated_asset_module, probe_entry_nofollow, probe_vetted_file,
        read_vetted_file, safe_project_relative, sha256_hex,
    },
};

pub const BUNDLE_FILE: &str = "analysis.json";
pub const INCOMPLETE_MARKER: &str = "INCOMPLETE";
pub const SUMMARY_FILE: &str = "run-summary.json";
pub const COMPLETION_FILE: &str = "completion.json";
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

/// A small record written after the result bundle and summary are committed.
/// Its write is outside the measured interval to avoid a self-referential
/// duration, while the main result persistence is inside that interval.
#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Completion {
    run_id: String,
    duration_ms: u32,
    finished_at: String,
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
    // A replacement bundle must not retain timing for the previous contents.
    let _ = std::fs::remove_file(run_dir.join(COMPLETION_FILE));
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

pub fn save_completion(
    run_dir: &Path,
    run_id: &str,
    duration_ms: u32,
    finished_at: &str,
) -> BwResult<()> {
    use std::io::Write;

    let completion = Completion {
        run_id: run_id.to_owned(),
        duration_ms,
        finished_at: finished_at.to_owned(),
    };
    let bytes = serde_json::to_vec(&completion).map_err(|error| {
        BwError::new(
            ErrorCode::ResultInvalid,
            format!("Could not serialize completion timing: {error}"),
            "Report this as a bug.",
        )
    })?;
    let temporary = run_dir.join(format!("{COMPLETION_FILE}.tmp"));
    let written = std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&temporary)
        .and_then(|mut file| file.write_all(&bytes))
        .and_then(|()| std::fs::rename(&temporary, run_dir.join(COMPLETION_FILE)));
    if let Err(error) = written {
        let _ = std::fs::remove_file(&temporary);
        return Err(BwError::new(
            ErrorCode::OutputDirInvalid,
            format!(
                "Could not write {}: {error}",
                run_dir.join(COMPLETION_FILE).display()
            ),
            "Check that the output directory is writable and has free space.",
        ));
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
    let sources_by_id: BTreeMap<_, _> = bundle
        .sources
        .iter()
        .map(|source| (source.id.as_str(), source))
        .collect();
    for summary in &analysis.sources {
        if summary.revision_evidence == SourceRevisionEvidence::BuildSnapshot
            && !sources_by_id
                .get(summary.id.as_str())
                .is_some_and(|source| {
                    source.path == summary.path
                        && source.revision_evidence == SourceRevisionEvidence::BuildSnapshot
                })
        {
            return Err(invalid(format!(
                "source summary {:?} claims a build snapshot without a matching source record",
                summary.id
            )));
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
    for diagnostic in &analysis.diagnostics {
        if let Some(usage) = &diagnostic.export_usage {
            validate_export_usage(diagnostic, usage)?;
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

fn valid_export_reference(reference: &ExportReference) -> bool {
    !reference.module.is_empty()
        && match reference.scope {
            ExportScope::Named => {
                !reference.export_path.is_empty()
                    && reference.export_path.iter().all(|part| !part.is_empty())
            }
            ExportScope::Module => reference.export_path.is_empty(),
            ExportScope::Unspecified => false,
        }
}

/// Export usage evidence is untrusted input: its shape must match what the
/// analysis produces, so a damaged or hand-edited bundle cannot show a chain
/// whose edges do not connect or whose limits contradict each other.
fn validate_export_usage(diagnostic: &Diagnostic, usage: &ExportUsage) -> BwResult<()> {
    let id = &diagnostic.id;
    let bad = |detail: &str| {
        invalid(format!(
            "diagnostic {id} has invalid export usage: {detail}"
        ))
    };
    match usage.state {
        ExportUsageState::Unspecified => return Err(bad("state is unspecified")),
        ExportUsageState::Unavailable => {
            if !usage.paths.is_empty() {
                return Err(bad("unavailable evidence lists paths"));
            }
            return Ok(());
        }
        ExportUsageState::Available => {}
    }
    if usage.paths.is_empty() {
        return Err(bad("available evidence has no paths"));
    }
    if usage.paths.len() > MAX_PATHS {
        return Err(bad("too many paths"));
    }
    if usage.truncation == ExportUsageTruncation::Unspecified {
        return Err(bad("truncation is unspecified"));
    }
    for path in &usage.paths {
        if path.edges.is_empty() || path.edges.len() > MAX_EDGES_PER_PATH {
            return Err(bad("a path has no edges or too many edges"));
        }
        match path.end {
            ExportUsagePathEnd::Unspecified => return Err(bad("a path end is unspecified")),
            ExportUsagePathEnd::Truncated if usage.truncation == ExportUsageTruncation::None => {
                return Err(bad("a truncated path without a truncation marker"));
            }
            // An unconfirmed start exists only because an edge was dropped,
            // and the analysis always discloses dropped edges in the note.
            ExportUsagePathEnd::Incomplete if usage.note.is_empty() => {
                return Err(bad("an incomplete path without a disclosure note"));
            }
            _ => {}
        }
        for edge in &path.edges {
            if !valid_export_reference(&edge.consumer) || !valid_export_reference(&edge.provider) {
                return Err(bad("an edge has an invalid export reference"));
            }
        }
        // Each edge's provider is the next edge's consumer: the chain runs
        // from the outermost consumer to the diagnostic module.
        if path
            .edges
            .windows(2)
            .any(|pair| pair[0].provider != pair[1].consumer)
        {
            return Err(bad("path edges are not connected"));
        }
        // The last edge provides the diagnostic's own module; otherwise the
        // chain is evidence about some other module.
        if path
            .edges
            .last()
            .is_some_and(|edge| edge.provider.module != diagnostic.module)
        {
            return Err(bad("a path does not end at the diagnostic module"));
        }
        if repeats_inside_chain(&path.edges) {
            return Err(bad("a path repeats an export before its first edge"));
        }
        if closes_cycle(&path.edges) != (path.end == ExportUsagePathEnd::Cycle) {
            return Err(bad("a path end disagrees with its first edge about a cycle"));
        }
    }
    Ok(())
}

/// True when the first edge (the outermost consumer) revisits an export of the
/// chain: its consumer is the consumer or provider of another edge, or the
/// consumer of the only edge is its own provider. The analysis ends exactly
/// these chains with `CYCLE`.
fn closes_cycle(edges: &[ExportUsageEdge]) -> bool {
    let Some((first, rest)) = edges.split_first() else {
        return false;
    };
    first.consumer == first.provider
        || rest
            .iter()
            .any(|edge| edge.consumer == first.consumer || edge.provider == first.consumer)
}

/// True when an edge other than the first revisits an export of the edges
/// closer to the diagnostic module, or consumes the export it provides. The
/// analysis ends a chain at the first such edge, so it never extends past one.
fn repeats_inside_chain(edges: &[ExportUsageEdge]) -> bool {
    edges.iter().enumerate().skip(1).any(|(index, edge)| {
        edge.consumer == edge.provider
            || edges[index + 1..]
                .iter()
                .any(|inner| inner.consumer == edge.consumer || inner.provider == edge.consumer)
    })
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
    if bytes.len() as u64 > MAX_SUMMARY_BYTES {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidData,
            "metadata file is too large",
        ));
    }
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
        // The marker entry itself is inspected: a dangling symlink named
        // `INCOMPLETE` is present, so it is never read as absence.
        _ => match probe_entry_nofollow(&run_dir.join(INCOMPLETE_MARKER)) {
            Ok(()) => RunStatus::Partial,
            Err(LiveFileFailure::Missing) => status,
            Err(failure) => {
                // The marker cannot be inspected, so completeness cannot be
                // proven; an unfinished run is never presented as complete.
                tracing::warn!(
                    run_dir = %run_dir.display(),
                    ?failure,
                    "incomplete marker could not be inspected; presenting the run as partial"
                );
                RunStatus::Partial
            }
        },
    }
}

pub fn load(run_dir: &Path) -> BwResult<ResultBundle> {
    let path = run_dir.join(BUNDLE_FILE);
    if let Err(error) = std::fs::symlink_metadata(&path) {
        return Err(match LiveFileFailure::from_io(&error) {
            LiveFileFailure::Missing => BwError::new(
                ErrorCode::ResultInvalid,
                format!("No result bundle at {}", path.display()),
                "Pass a run directory created by `bundlewhy analyze` (it contains analysis.json).",
            ),
            failure => {
                tracing::warn!(path = %path.display(), ?failure, %error, "result bundle is inaccessible");
                invalid(format!("analysis.json is inaccessible: {error}"))
            }
        });
    }
    // The run directory is untrusted: only a bounded regular file (never a
    // symlink or special file such as /dev/zero) is read.
    let bytes = read_bundle_bytes(&path).map_err(|error| {
        invalid(format!(
            "analysis.json must be a regular file of at most {MAX_BUNDLE_BYTES} bytes: {error}"
        ))
    })?;
    let mut bundle = parse(&bytes)?;
    let completion_path = run_dir.join(COMPLETION_FILE);
    match std::fs::symlink_metadata(&completion_path) {
        Ok(_) => {
            let bytes = read_summary_bytes(&completion_path)
                .map_err(|error| invalid(format!("completion.json is invalid: {error}")))?;
            let completion: Completion = serde_json::from_slice(&bytes)
                .map_err(|error| invalid(format!("completion.json is invalid: {error}")))?;
            if completion.run_id != bundle.analysis.run_id
                || completion.duration_ms < bundle.analysis.duration_ms
                || chrono::DateTime::parse_from_rfc3339(&completion.finished_at).is_err()
            {
                return Err(invalid("completion.json does not match the result bundle"));
            }
            bundle.analysis.duration_ms = completion.duration_ms;
            bundle.analysis.finished_at = completion.finished_at;
        }
        Err(error) => match LiveFileFailure::from_io(&error) {
            // Bundles saved before completion.json use analysis.json timing.
            LiveFileFailure::Missing => {}
            _ => return Err(invalid(format!("completion.json is inaccessible: {error}"))),
        },
    }
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
/// files (for example `/dev/zero`) and symlinks leaving the root are refused
/// with `LiveFileFailure::NotPermitted`; a removed file is
/// `LiveFileFailure::Missing`; any other failure keeps its I/O error kind.
fn read_project_file(canonical_root: &Path, relative: &Path) -> Result<Vec<u8>, LiveFileFailure> {
    read_vetted_file(
        &canonical_root.join(relative),
        |resolved| resolved.starts_with(canonical_root),
        MAX_STALE_CHECK_BYTES,
    )
}

/// Checks an existence-only project file with the same vetting as
/// [`read_project_file`]: a directory, an oversized or special file and a
/// symlink leaving the root are `LiveFileFailure::NotPermitted`, never a
/// verified existence.
fn probe_project_file(canonical_root: &Path, relative: &Path) -> Result<(), LiveFileFailure> {
    probe_vetted_file(
        &canonical_root.join(relative),
        |resolved| resolved.starts_with(canonical_root),
        MAX_STALE_CHECK_BYTES,
    )
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

/// True when the saved source keeps build snapshot text that the analysis
/// uses, so the freshness warning may say so. Only a source with explicit build
/// snapshot evidence qualifies, plus a text source saved before revision
/// evidence was recorded (it is rechecked by hash, so it has stored text). An
/// existence-only source without that evidence (an asset, a source whose
/// `sourcesContent` was missing) has no build text to claim.
fn has_snapshot_text(source: &SourceFile, existence_only: bool) -> bool {
    match source.revision_evidence {
        SourceRevisionEvidence::BuildSnapshot => true,
        SourceRevisionEvidence::Unspecified => !existence_only,
        SourceRevisionEvidence::Unavailable => false,
    }
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
/// saved stale flag. Other problems keep their order. When the project root
/// cannot be verified, no check is possible, so saved flags and warnings are
/// left untouched.
///
/// The `BW_SOURCE_FRESHNESS_UNVERIFIED` warning is recomputed the same way:
/// earlier ones are dropped and one warning is added only for sources that
/// cannot be inspected or read during this call. Every filesystem failure is
/// classified through `LiveFileFailure`: only `NotFound` (a removed file) is
/// stale; permission, not-a-directory, other I/O failures and files that are
/// not vetted for reading are unverified, never changed.
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
    // Unverified sources are counted by what the warning may claim: sources
    // with build snapshot text versus sources that only have an existence
    // check.
    let mut snapshot_unverified = 0usize;
    let mut existence_unverified = 0usize;
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
        // Older schema-1 bundles stored the module path rather than the
        // backing file in projectRelative. Resolve it with the same literal
        // filename precedence as a new analysis, then validate the selected
        // path again before reading it.
        let backing = if existence_only {
            canonical_root.join(&relative)
        } else {
            filesystem_path(&canonical_root.join(&relative))
        };
        let Some(relative) = backing
            .strip_prefix(&canonical_root)
            .ok()
            .and_then(|path| path.to_str())
            .and_then(safe_project_relative)
        else {
            continue;
        };
        // Only a removed file (`NotFound`) or a hash mismatch is a change. A
        // file that cannot be verified (too large, special, outside the root,
        // or an I/O failure such as permission denied) is never reported stale;
        // it only leaves its freshness unverified.
        if existence_only {
            source.stale = match probe_project_file(&canonical_root, &relative) {
                Ok(()) => false,
                Err(failure) if failure.is_removal() => true,
                Err(failure) => {
                    tracing::warn!(path = %source.path, ?failure, "existence check failed on reopen; freshness is unverified");
                    if has_snapshot_text(source, true) {
                        snapshot_unverified += 1;
                    } else {
                        existence_unverified += 1;
                    }
                    false
                }
            };
            continue;
        }
        source.stale = match read_project_file(&canonical_root, &relative) {
            Ok(bytes) => {
                if is_unverifiable_asset(source, &bytes) {
                    tracing::debug!(path = %source.path, "asset stub source is not freshness-checked");
                    false
                } else {
                    sha256_hex(&bytes) != source.sha256
                }
            }
            Err(failure) if failure.is_removal() => true,
            Err(failure) => {
                // Present or not provably removed, but not readable: no change
                // can be claimed, only that freshness cannot be checked.
                tracing::warn!(path = %source.path, ?failure, "live read failed on reopen; freshness is unverified");
                if has_snapshot_text(source, false) {
                    snapshot_unverified += 1;
                } else {
                    existence_unverified += 1;
                }
                false
            }
        };
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
    // The freshness warning is recomputed from this reopen: it clears when the
    // live files are readable again and stays while a file cannot be read.
    let unverified_code = ErrorCode::SourceFreshnessUnverified.as_str();
    bundle
        .analysis
        .problems
        .retain(|entry| entry.code != unverified_code);
    bundle
        .analysis
        .problems
        .extend(freshness_unverified_problem(
            snapshot_unverified,
            existence_unverified,
        ));
    tracing::debug!(
        stale,
        snapshot_unverified,
        existence_unverified,
        "reconciled stale source warning"
    );
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
    use crate::model::{
        Diagnostic, ExportUsageEdge, ExportUsagePath, SourceLocation, SourceRevisionEvidence,
    };

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
    fn rejects_build_snapshot_summary_without_matching_source_record() {
        let mut saved = bundle();
        let mut summary = saved.sources[0].summary();
        summary.revision_evidence = SourceRevisionEvidence::BuildSnapshot;
        saved.analysis.sources.push(summary);
        assert_eq!(
            parse(&serde_json::to_vec(&saved).unwrap())
                .unwrap_err()
                .code,
            ErrorCode::ResultInvalid
        );
    }

    #[test]
    fn completed_at_legacy_bundle_uses_embedded_duration() {
        let dir = tempfile::tempdir().unwrap();
        let mut legacy = bundle();
        legacy.analysis.duration_ms = 123;
        legacy.analysis.finished_at = "2026-10-14T09:00:00Z".to_owned();
        save(dir.path(), &legacy).unwrap();
        let loaded = load(dir.path()).unwrap();
        assert_eq!(loaded.analysis.duration_ms, 123);
        assert_eq!(loaded.analysis.finished_at, legacy.analysis.finished_at);
    }

    #[test]
    fn completed_at_rejects_timing_for_another_run() {
        let dir = tempfile::tempdir().unwrap();
        let saved = bundle();
        save(dir.path(), &saved).unwrap();
        save_completion(
            dir.path(),
            &uuid::Uuid::now_v7().to_string(),
            123,
            "2026-10-14T09:00:00Z",
        )
        .unwrap();
        assert_eq!(load(dir.path()).unwrap_err().code, ErrorCode::ResultInvalid);
    }

    #[cfg(unix)]
    #[test]
    fn completed_at_rejects_symlinked_timing_metadata() {
        let dir = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        save(dir.path(), &bundle()).unwrap();
        std::fs::write(outside.path().join(COMPLETION_FILE), "{}").unwrap();
        std::os::unix::fs::symlink(
            outside.path().join(COMPLETION_FILE),
            dir.path().join(COMPLETION_FILE),
        )
        .unwrap();
        assert_eq!(load(dir.path()).unwrap_err().code, ErrorCode::ResultInvalid);
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
    fn source_provenance_survives_saved_reload_and_export() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::create_dir_all(dir.path().join("src")).unwrap();
        std::fs::write(dir.path().join("src/a.js"), "live text").unwrap();
        let mut saved = bundle();
        saved.project_root = dir.path().to_string_lossy().into_owned();
        saved.sources[0].revision_evidence = SourceRevisionEvidence::Unavailable;
        saved.analysis.sources = vec![saved.sources[0].summary()];
        saved
            .existence_relative
            .insert(saved.sources[0].id.clone(), "src/a.js".to_owned());
        let mut reopened = parse(&serde_json::to_vec(&saved).unwrap()).unwrap();
        mark_stale(&mut reopened);
        assert_eq!(
            reopened.sources[0].revision_evidence,
            SourceRevisionEvidence::Unavailable
        );
        assert_eq!(
            reopened.analysis.sources[0].revision_evidence,
            SourceRevisionEvidence::Unavailable
        );
        assert!(!reopened.sources[0].stale);
        assert!(reopened.sources[0].content.is_empty());
        assert!(reopened.sources[0].sha256.is_empty());
        let html = crate::export::render(
            crate::export::UI_HTML,
            &reopened.analysis,
            &reopened.sources,
            true,
        );
        assert!(html.contains("SOURCE_REVISION_EVIDENCE_UNAVAILABLE"));
        assert!(!html.contains("live text"));
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

    fn named(module: &str, name: &str) -> ExportReference {
        ExportReference {
            module: module.to_owned(),
            export_path: vec![name.to_owned()],
            scope: ExportScope::Named,
        }
    }

    fn module_scope(module: &str) -> ExportReference {
        ExportReference {
            module: module.to_owned(),
            export_path: Vec::new(),
            scope: ExportScope::Module,
        }
    }

    fn usage_edge(consumer: ExportReference, provider: ExportReference) -> ExportUsageEdge {
        ExportUsageEdge {
            consumer,
            provider,
            dependency_id: "7".to_owned(),
            location: "3:1-4".to_owned(),
        }
    }

    fn valid_usage() -> ExportUsage {
        ExportUsage {
            state: ExportUsageState::Available,
            note: String::new(),
            paths: vec![ExportUsagePath {
                edges: vec![
                    usage_edge(module_scope("src/main.js"), named("src/lib.js", "foo")),
                    usage_edge(named("src/lib.js", "foo"), named("src/shared.js", "bar")),
                ],
                end: ExportUsagePathEnd::Root,
            }],
            truncation: ExportUsageTruncation::None,
        }
    }

    fn bundle_with_usage(usage: ExportUsage) -> ResultBundle {
        let mut bundle = bundle();
        bundle.analysis.diagnostics.push(Diagnostic {
            id: "d-0123456789ab".to_owned(),
            module: "src/shared.js".to_owned(),
            export_usage: Some(usage),
            ..Diagnostic::default()
        });
        bundle
    }

    fn parse_usage(usage: ExportUsage) -> BwResult<ResultBundle> {
        parse(&serde_json::to_vec(&bundle_with_usage(usage)).unwrap())
    }

    #[test]
    fn export_usage_round_trips_and_older_bundles_still_open() {
        let parsed = parse_usage(valid_usage()).unwrap();
        assert_eq!(
            parsed.analysis.diagnostics[0].export_usage,
            Some(valid_usage())
        );
        // A bundle written before the field existed has no `exportUsage` key.
        let mut value = serde_json::to_value(bundle_with_usage(valid_usage())).unwrap();
        value["analysis"]["diagnostics"][0]
            .as_object_mut()
            .unwrap()
            .remove("exportUsage");
        let older = parse(&serde_json::to_vec(&value).unwrap()).unwrap();
        assert!(older.analysis.diagnostics[0].export_usage.is_none());
    }

    #[test]
    fn an_incomplete_path_end_with_a_note_is_valid_without_a_truncation_marker() {
        let mut usage = valid_usage();
        usage.paths[0].end = ExportUsagePathEnd::Incomplete;
        usage.note = "1 engine edge(s) were not shown.".to_owned();
        assert_eq!(usage.truncation, ExportUsageTruncation::None);
        let parsed = parse_usage(usage.clone()).unwrap();
        assert_eq!(parsed.analysis.diagnostics[0].export_usage, Some(usage));
    }

    #[test]
    fn unavailable_export_usage_is_valid_without_paths() {
        let usage = ExportUsage {
            state: ExportUsageState::Unavailable,
            note: "hook missing".to_owned(),
            ..ExportUsage::default()
        };
        assert!(parse_usage(usage).is_ok());
    }

    #[test]
    fn rejects_every_invalid_export_usage_shape() {
        type Edit = fn(&mut ExportUsage);
        let cases: [(&str, Edit); 17] = [
            ("unspecified state", |usage| {
                usage.state = ExportUsageState::Unspecified;
            }),
            ("unavailable with paths", |usage| {
                usage.state = ExportUsageState::Unavailable;
            }),
            ("available without paths", |usage| usage.paths.clear()),
            ("too many paths", |usage| {
                let path = usage.paths[0].clone();
                usage.paths = vec![path; MAX_PATHS + 1];
            }),
            ("unspecified truncation", |usage| {
                usage.truncation = ExportUsageTruncation::Unspecified;
            }),
            ("path without edges", |usage| usage.paths[0].edges.clear()),
            ("path with too many edges", |usage| {
                let edge = usage.paths[0].edges[1].clone();
                usage.paths[0].edges = vec![edge; MAX_EDGES_PER_PATH + 1];
            }),
            ("unspecified path end", |usage| {
                usage.paths[0].end = ExportUsagePathEnd::Unspecified;
            }),
            ("truncated end without marker", |usage| {
                usage.paths[0].end = ExportUsagePathEnd::Truncated;
            }),
            ("incomplete end without a note", |usage| {
                usage.paths[0].end = ExportUsagePathEnd::Incomplete;
            }),
            ("unspecified scope", |usage| {
                usage.paths[0].edges[0].consumer.scope = ExportScope::Unspecified;
            }),
            ("named scope without export path", |usage| {
                usage.paths[0].edges[1].consumer.export_path.clear();
            }),
            ("module scope with export path", |usage| {
                usage.paths[0].edges[0].consumer.export_path = vec!["x".to_owned()];
            }),
            ("empty module", |usage| {
                usage.paths[0].edges[0].consumer.module.clear();
            }),
            ("disconnected edges", |usage| {
                usage.paths[0].edges[1].consumer = named("src/other.js", "foo");
            }),
            ("path ends at another module", |usage| {
                usage.paths[0].edges[1].provider = named("src/other.js", "bar");
            }),
            ("cycle end without a closing edge", |usage| {
                usage.paths[0].end = ExportUsagePathEnd::Cycle;
            }),
        ];
        for (name, edit) in cases {
            let mut usage = valid_usage();
            edit(&mut usage);
            let error = parse_usage(usage).expect_err(name);
            assert_eq!(error.code, ErrorCode::ResultInvalid, "{name}");
            assert!(error.message.contains("export usage"), "{name}");
        }
    }

    /// Chain shape against the diagnostic module and the cycle rules. Every
    /// row names a chain, the end it carries and whether a saved bundle with
    /// it opens. Mirrors the "Saved export usage" table in the contract.
    #[test]
    fn export_usage_chain_shape_table() {
        use ExportUsagePathEnd::{Cycle, Root, Truncated};
        let foo = || named("src/lib.js", "foo");
        let bar = || named("src/shared.js", "bar");
        let main = || module_scope("src/main.js");
        let cases: Vec<(&str, Vec<ExportUsageEdge>, ExportUsagePathEnd, bool)> = vec![
            (
                "one edge into the diagnostic module",
                vec![usage_edge(main(), bar())],
                Root,
                true,
            ),
            (
                "one edge into another module",
                vec![usage_edge(main(), foo())],
                Root,
                false,
            ),
            (
                "the last edge provides another module although earlier edges name the diagnostic \
                 module",
                vec![usage_edge(main(), bar()), usage_edge(bar(), foo())],
                Root,
                false,
            ),
            (
                "a self-referencing edge ends the chain with a cycle",
                vec![usage_edge(bar(), bar())],
                Cycle,
                true,
            ),
            (
                "a self-referencing edge cannot be a root",
                vec![usage_edge(bar(), bar())],
                Root,
                false,
            ),
            (
                "a self-referencing edge cannot be truncated",
                vec![usage_edge(bar(), bar())],
                Truncated,
                false,
            ),
            (
                "a self-referencing start edge cannot be extended",
                vec![usage_edge(main(), bar()), usage_edge(bar(), bar())],
                Root,
                false,
            ),
            (
                "the first edge returns to the provider of a later edge",
                vec![usage_edge(bar(), foo()), usage_edge(foo(), bar())],
                Cycle,
                true,
            ),
            (
                "a closing first edge cannot be a root",
                vec![usage_edge(bar(), foo()), usage_edge(foo(), bar())],
                Root,
                false,
            ),
            (
                "the first edge consumes the export it provides above another edge",
                vec![usage_edge(foo(), foo()), usage_edge(foo(), bar())],
                Cycle,
                true,
            ),
            (
                "a chain without a repeated export cannot end with a cycle",
                vec![usage_edge(main(), foo()), usage_edge(foo(), bar())],
                Cycle,
                false,
            ),
            (
                "an inner edge revisits an export of the later edges",
                vec![
                    usage_edge(main(), bar()),
                    usage_edge(bar(), foo()),
                    usage_edge(foo(), bar()),
                ],
                Root,
                false,
            ),
            (
                "an inner revisit cannot be hidden by a cycle end",
                vec![
                    usage_edge(main(), bar()),
                    usage_edge(bar(), foo()),
                    usage_edge(foo(), bar()),
                ],
                Cycle,
                false,
            ),
        ];
        for (name, edges, end, opens) in cases {
            let usage = ExportUsage {
                state: ExportUsageState::Available,
                note: String::new(),
                paths: vec![ExportUsagePath { edges, end }],
                truncation: if end == Truncated {
                    ExportUsageTruncation::Depth
                } else {
                    ExportUsageTruncation::None
                },
            };
            let result = parse_usage(usage);
            assert_eq!(result.is_ok(), opens, "{name}: {:?}", result.err());
            if !opens {
                assert_eq!(result.unwrap_err().code, ErrorCode::ResultInvalid, "{name}");
            }
        }
    }

    /// Everything the traversal can produce opens: the validation accepts
    /// exactly the chains the analysis can derive. Random small graphs over
    /// few modules and exports make cycles, self-references and shared
    /// exports likely.
    #[test]
    fn every_chain_the_traversal_derives_passes_validation() {
        use std::path::PathBuf;

        use crate::{
            collect::{CollectedExportEdge, CollectedExportRef, CollectedExportUsage, Collection},
            rules::{
                Context,
                export_usage::{ExportUsageIndex, Limits, MAX_EDGES_PER_PATH, MAX_PATHS, traverse},
            },
            sources::SourceStore,
        };

        let mut seed = 0x2545_f491_4f6c_dd1d_u64;
        let mut next = move |bound: usize| {
            seed ^= seed << 13;
            seed ^= seed >> 7;
            seed ^= seed << 17;
            (seed % bound as u64) as usize
        };
        let reference = |module: usize, export: usize| CollectedExportRef {
            module: format!("m{module}"),
            export: match export {
                0 => None,
                1 => Some(vec!["a".to_owned()]),
                _ => Some(vec!["b".to_owned()]),
            },
        };
        let mut checked_paths = 0usize;
        let mut cycles = 0usize;
        let mut incomplete = 0usize;
        for case in 0..2_000 {
            let modules = 2 + next(3);
            let edge_count = 1 + next(12);
            let edges: Vec<CollectedExportEdge> = (0..edge_count)
                .map(|index| CollectedExportEdge {
                    consumer: reference(next(modules), next(3)),
                    provider: reference(next(modules), next(3)),
                    dependency_id: Some(index.to_string()),
                    location: None,
                })
                .collect();
            let collection = Collection {
                export_usage: Some(CollectedExportUsage {
                    available: true,
                    edges,
                    skipped_edges: u32::from(next(4) == 0),
                    unattributed_edges: u32::from(next(4) == 0),
                    ..CollectedExportUsage::default()
                }),
                ..Collection::default()
            };
            let context = Context::new(
                &collection,
                PathBuf::from("/project"),
                "project".to_owned(),
                Vec::new(),
                SourceStore::new(false),
            );
            let limits = match case % 4 {
                0 => Limits::DEFAULT,
                1 => Limits {
                    max_edges_per_path: 1 + next(3),
                    ..Limits::DEFAULT
                },
                2 => Limits {
                    max_paths: 1 + next(MAX_PATHS),
                    max_expansions: next(6),
                    ..Limits::DEFAULT
                },
                _ => Limits {
                    max_queued: 1 + next(4),
                    max_edges_per_path: MAX_EDGES_PER_PATH,
                    ..Limits::DEFAULT
                },
            };
            let usage_data = collection.export_usage.as_ref().unwrap();
            let index = ExportUsageIndex::new(usage_data);
            for module in 0..modules {
                let name = format!("m{module}");
                let usage = traverse(&context, &index, &name, limits);
                for path in &usage.paths {
                    checked_paths += 1;
                    cycles += usize::from(path.end == ExportUsagePathEnd::Cycle);
                    incomplete += usize::from(path.end == ExportUsagePathEnd::Incomplete);
                }
                let diagnostic = Diagnostic {
                    id: "d-0123456789ab".to_owned(),
                    module: name,
                    export_usage: Some(usage.clone()),
                    ..Diagnostic::default()
                };
                validate_export_usage(&diagnostic, &usage)
                    .unwrap_or_else(|error| panic!("case {case}: {} for {usage:?}", error.message));
            }
        }
        // The sample must exercise the interesting ends, or it proves little.
        assert!(checked_paths > 1_000, "{checked_paths}");
        assert!(cycles > 50, "{cycles}");
        assert!(incomplete > 20, "{incomplete}");
    }

    #[test]
    fn rejects_unknown_export_usage_enum_values() {
        let mut value = serde_json::to_value(bundle_with_usage(valid_usage())).unwrap();
        value["analysis"]["diagnostics"][0]["exportUsage"]["state"] =
            serde_json::json!("EXPORT_USAGE_STATE_EXPLODED");
        assert_eq!(
            parse(&serde_json::to_vec(&value).unwrap())
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
    fn legacy_source_query_asset_reopens_without_false_stale_state() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        let asset = project.join("src/icon.svg");
        std::fs::write(&asset, "<svg/>").unwrap();

        let mut saved = stale_fixture(dir.path(), "source");
        saved.sources[0].path = "src/icon.svg?raw".to_owned();
        saved.sources[0].content = "export default \"<svg/>\"".to_owned();
        saved.sources[0].content_included = true;
        saved.sources[0].sha256 = sha256_hex(saved.sources[0].content.as_bytes());
        saved.sources[0].stale = true;
        saved.analysis.sources[0] = saved.sources[0].summary();
        saved
            .project_relative
            .insert(saved.sources[0].id.clone(), "src/icon.svg?raw".to_owned());
        save(dir.path(), &saved).unwrap();

        for _ in 0..2 {
            let loaded = load(dir.path()).unwrap();
            assert_eq!(loaded.project_relative, saved.project_relative);
            assert!(!loaded.sources[0].stale);
            assert!(!loaded.analysis.sources[0].stale);
            assert_eq!(stale_count(&loaded), 0);
        }

        std::fs::remove_file(&asset).unwrap();
        let removed = load(dir.path()).unwrap();
        assert!(removed.sources[0].stale && removed.analysis.sources[0].stale);
        assert_eq!(stale_count(&removed), 1);

        std::fs::write(&asset, "<svg changed/>").unwrap();
        let restored = load(dir.path()).unwrap();
        assert!(!restored.sources[0].stale && !restored.analysis.sources[0].stale);
        assert_eq!(stale_count(&restored), 0);
    }

    #[test]
    fn legacy_source_literal_paths_win_and_text_still_uses_its_hash() {
        // Windows cannot create a literal filename containing `?`.
        let suffixes: &[&str] = if cfg!(windows) {
            &["#component"]
        } else {
            &["?raw", "#component"]
        };
        for suffix in suffixes {
            let dir = tempfile::tempdir().unwrap();
            let mut saved = stale_fixture(dir.path(), "original");
            let relative = format!("src/a.js{suffix}");
            saved.sources[0].path = relative.clone();
            saved.sources[0].content = "original".to_owned();
            saved.sources[0].content_included = true;
            saved.analysis.sources[0] = saved.sources[0].summary();
            saved
                .project_relative
                .insert(saved.sources[0].id.clone(), relative.clone());
            save(dir.path(), &saved).unwrap();

            let literal = dir.path().join("project").join(&relative);
            std::fs::write(&literal, "different").unwrap();
            let loaded = load(dir.path()).unwrap();
            assert!(loaded.sources[0].stale, "{relative}");
            assert_eq!(stale_count(&loaded), 1);

            std::fs::remove_file(&literal).unwrap();
            let loaded = load(dir.path()).unwrap();
            assert!(!loaded.sources[0].stale, "{relative}");
            assert_eq!(stale_count(&loaded), 0);

            std::fs::write(dir.path().join("project/src/a.js"), "changed").unwrap();
            let loaded = load(dir.path()).unwrap();
            assert!(loaded.sources[0].stale, "{relative}");
            assert_eq!(stale_count(&loaded), 1);
        }
    }

    #[test]
    fn legacy_source_query_keeps_saved_path_safety_limits() {
        let dir = tempfile::tempdir().unwrap();
        let mut saved = stale_fixture(dir.path(), "original");
        let project = dir.path().join("project");
        let backing = project.join("src/a.js");
        saved.sources[0].path = "src/a.js?raw".to_owned();
        saved
            .project_relative
            .insert(saved.sources[0].id.clone(), "src/a.js?raw".to_owned());

        // A directory and an oversized regular file both exist, but neither
        // can be read or treated as a source change.
        std::fs::remove_file(&backing).unwrap();
        std::fs::create_dir(&backing).unwrap();
        mark_stale(&mut saved);
        assert!(!saved.sources[0].stale);
        std::fs::remove_dir(&backing).unwrap();
        std::fs::write(&backing, vec![b'x'; (MAX_STALE_CHECK_BYTES + 1) as usize]).unwrap();
        mark_stale(&mut saved);
        assert!(!saved.sources[0].stale);

        #[cfg(unix)]
        {
            std::fs::remove_file(&backing).unwrap();
            let outside = dir.path().join("outside.js");
            std::fs::write(&outside, "different").unwrap();
            std::os::unix::fs::symlink(&outside, &backing).unwrap();
            mark_stale(&mut saved);
            assert!(!saved.sources[0].stale);
        }

        saved.project_relative.insert(
            saved.sources[0].id.clone(),
            "src/../../outside.js?raw".to_owned(),
        );
        assert_eq!(
            parse(&serde_json::to_vec(&saved).unwrap())
                .unwrap_err()
                .code,
            ErrorCode::ResultInvalid
        );
    }

    #[test]
    fn legacy_source_missing_fragment_clears_after_restoration() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        let module = project.join("src/a.js#component");
        let mut store = crate::sources::SourceStore::new(true);
        store.snapshots.insert(
            crate::sources::normalize(&module),
            "export const a = 1;".to_owned(),
        );
        store.add_file(&project, &module).unwrap();
        let mut saved = bundle();
        saved.project_root = project.to_string_lossy().into_owned();
        saved.project_relative = store.project_relative.clone();
        saved.sources = store.into_files();
        saved.analysis.sources = saved.sources.iter().map(SourceFile::summary).collect();
        assert_eq!(
            saved.project_relative.get(&saved.sources[0].id),
            Some(&"src/a.js".to_owned())
        );
        assert_eq!(saved.sources[0].path, "src/a.js#component");
        save(dir.path(), &saved).unwrap();

        let missing = load(dir.path()).unwrap();
        assert!(missing.sources[0].stale);
        assert_eq!(stale_count(&missing), 1);

        std::fs::write(project.join("src/a.js"), "export const a = 1;").unwrap();
        let restored = load(dir.path()).unwrap();
        assert!(!restored.sources[0].stale && !restored.analysis.sources[0].stale);
        assert_eq!(stale_count(&restored), 0);
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
        assert_eq!(
            read_project_file(&canonical, Path::new("escape.js")),
            Err(LiveFileFailure::NotPermitted)
        );
        assert_eq!(
            read_project_file(&canonical, Path::new("zero.js")),
            Err(LiveFileFailure::NotPermitted)
        );
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

    fn unverified_bundle(project: &Path, text: &str) -> ResultBundle {
        let mut saved = bundle();
        saved.project_root = project.to_string_lossy().into_owned();
        saved.sources[0].content = text.to_owned();
        saved.sources[0].content_included = true;
        saved.sources[0].sha256 = sha256_hex(text.as_bytes());
        saved
            .project_relative
            .insert("src-0123456789abcdef".to_owned(), "src/a.js".to_owned());
        saved.analysis.problems.push(problem(
            ErrorCode::SourceFreshnessUnverified,
            ProblemSeverity::Warning,
            "freshness of 1 source file(s) could not be checked; analysis uses the build snapshot",
            "The live files could not be read.",
        ));
        saved
    }

    fn problem_count(bundle: &ResultBundle, code: &str) -> usize {
        bundle
            .analysis
            .problems
            .iter()
            .filter(|entry| entry.code == code)
            .count()
    }

    #[test]
    fn reopen_clears_the_freshness_warning_when_live_bytes_are_readable_and_equal() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        std::fs::write(project.join("src/a.js"), "export const a = 1;").unwrap();
        let mut saved = unverified_bundle(&project, "export const a = 1;");
        mark_stale(&mut saved);
        assert!(!saved.sources[0].stale);
        assert_eq!(problem_count(&saved, "BW_SOURCE_FRESHNESS_UNVERIFIED"), 0);
        assert_eq!(problem_count(&saved, "BW_SOURCE_STALE"), 0);
    }

    #[test]
    fn reopen_keeps_an_unreadable_file_unverified_without_a_changed_claim() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        // A directory in place of the file is present but cannot be read.
        std::fs::create_dir_all(project.join("src/a.js")).unwrap();
        let mut saved = unverified_bundle(&project, "export const a = 1;");
        for _ in 0..2 {
            mark_stale(&mut saved);
            assert!(!saved.sources[0].stale);
            assert_eq!(problem_count(&saved, "BW_SOURCE_FRESHNESS_UNVERIFIED"), 1);
            assert_eq!(problem_count(&saved, "BW_SOURCE_STALE"), 0);
        }
    }

    #[test]
    fn reopen_reports_changed_and_removed_files_as_stale_not_unverified() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        std::fs::write(project.join("src/a.js"), "edited").unwrap();
        let mut saved = unverified_bundle(&project, "export const a = 1;");
        mark_stale(&mut saved);
        assert!(saved.sources[0].stale);
        assert_eq!(problem_count(&saved, "BW_SOURCE_FRESHNESS_UNVERIFIED"), 0);
        assert_eq!(problem_count(&saved, "BW_SOURCE_STALE"), 1);

        std::fs::remove_file(project.join("src/a.js")).unwrap();
        let mut saved = unverified_bundle(&project, "export const a = 1;");
        mark_stale(&mut saved);
        assert!(saved.sources[0].stale);
        assert_eq!(problem_count(&saved, "BW_SOURCE_FRESHNESS_UNVERIFIED"), 0);
        assert_eq!(problem_count(&saved, "BW_SOURCE_STALE"), 1);
    }

    /// How a source's backing path is set up for a reopen test.
    #[derive(Clone, Copy, Debug)]
    enum Backing {
        Unchanged,
        Edited,
        Removed,
        /// A directory in place of the file.
        Directory,
        /// A regular file over the stale-check size limit.
        Oversized,
        /// The parent of the path is a regular file (not-a-directory error).
        ParentIsFile,
        /// A symlink whose target was removed.
        #[cfg(unix)]
        DanglingSymlink,
        /// A symlink leaving the project root.
        #[cfg(unix)]
        EscapingSymlink,
        /// The parent directory cannot be searched (permission denied).
        #[cfg(unix)]
        PermissionDenied,
    }

    /// Builds a project whose `src/a.js` is set up as `backing` and returns the
    /// reopened bundle. Returns `None` when the setup cannot be created here.
    fn reopen_with(dir: &Path, backing: Backing, existence_only: bool) -> Option<ResultBundle> {
        let project = dir.join("project");
        let src = project.join("src");
        std::fs::create_dir_all(&src).unwrap();
        let file = src.join("a.js");
        let text = "export const a = 1;";
        let mut locked = None;
        match backing {
            Backing::Unchanged => std::fs::write(&file, text).unwrap(),
            Backing::Edited => std::fs::write(&file, "edited").unwrap(),
            Backing::Removed => {}
            Backing::Directory => std::fs::create_dir(&file).unwrap(),
            Backing::Oversized => {
                let oversized = std::fs::File::create(&file).unwrap();
                oversized.set_len(MAX_STALE_CHECK_BYTES + 1).unwrap();
            }
            Backing::ParentIsFile => {
                std::fs::remove_dir(&src).unwrap();
                std::fs::write(&src, "file").unwrap();
            }
            #[cfg(unix)]
            Backing::DanglingSymlink => {
                std::os::unix::fs::symlink(project.join("gone.js"), &file).unwrap();
            }
            #[cfg(unix)]
            Backing::EscapingSymlink => {
                std::fs::write(dir.join("outside.js"), text).unwrap();
                std::os::unix::fs::symlink(dir.join("outside.js"), &file).unwrap();
            }
            #[cfg(unix)]
            Backing::PermissionDenied => {
                std::fs::write(&file, text).unwrap();
                if !crate::sources::test_support::deny_access(&src) {
                    return None;
                }
                locked = Some(src.clone());
            }
        }
        let mut saved = unverified_bundle(&project, text);
        saved.analysis.problems.clear();
        if existence_only {
            saved.project_relative.clear();
            saved
                .existence_relative
                .insert("src-0123456789abcdef".to_owned(), "src/a.js".to_owned());
        }
        mark_stale(&mut saved);
        #[cfg(unix)]
        if let Some(src) = locked {
            crate::sources::test_support::restore_access(&src);
        }
        #[cfg(not(unix))]
        let _ = locked;
        Some(saved)
    }

    /// Every cell of the reopen classification: a removed file is stale, a
    /// changed file is stale, and any other failure (or an unvetted file) is
    /// unverified without a changed claim. Text and existence-only sources
    /// follow the same rule.
    #[test]
    fn reopen_classifies_every_filesystem_failure_for_text_and_existence_only_sources() {
        // (backing, text: (stale, unverified), existence-only: (stale,
        // unverified))
        let table = [
            (Backing::Unchanged, (false, 0), (false, 0)),
            (Backing::Edited, (true, 0), (false, 0)),
            (Backing::Removed, (true, 0), (true, 0)),
            // A directory exists but cannot be read as a file.
            (Backing::Directory, (false, 1), (false, 1)),
            // Over the size limit: vetted like a read, never a verified file.
            (Backing::Oversized, (false, 1), (false, 1)),
            (Backing::ParentIsFile, (false, 1), (false, 1)),
            #[cfg(unix)]
            (Backing::DanglingSymlink, (true, 0), (true, 0)),
            #[cfg(unix)]
            (Backing::EscapingSymlink, (false, 1), (false, 1)),
            #[cfg(unix)]
            (Backing::PermissionDenied, (false, 1), (false, 1)),
        ];
        for (backing, text, existence) in table {
            for (existence_only, (stale, unverified)) in [(false, text), (true, existence)] {
                let dir = tempfile::tempdir().unwrap();
                let Some(saved) = reopen_with(dir.path(), backing, existence_only) else {
                    eprintln!("skipped {backing:?}: permissions cannot block this account");
                    continue;
                };
                let label = format!("{backing:?} existence_only={existence_only}");
                assert_eq!(saved.sources[0].stale, stale, "{label}");
                assert_eq!(
                    problem_count(&saved, "BW_SOURCE_STALE"),
                    usize::from(stale),
                    "{label}"
                );
                assert_eq!(
                    problem_count(&saved, "BW_SOURCE_FRESHNESS_UNVERIFIED"),
                    usize::from(unverified > 0),
                    "{label}"
                );
            }
        }
    }

    /// The reopen warning claims a build snapshot only for sources that keep
    /// snapshot text; an existence-only source without it gets its own wording.
    #[test]
    fn reopen_warning_wording_follows_the_revision_evidence_of_each_source() {
        // (evidence, existence-only, claims a build snapshot)
        let table = [
            (SourceRevisionEvidence::BuildSnapshot, false, true),
            // A derived module keeps snapshot text but is checked for existence.
            (SourceRevisionEvidence::BuildSnapshot, true, true),
            (SourceRevisionEvidence::Unavailable, true, false),
            (SourceRevisionEvidence::Unavailable, false, false),
            // Saved before evidence was recorded.
            (SourceRevisionEvidence::Unspecified, false, true),
            (SourceRevisionEvidence::Unspecified, true, false),
        ];
        for (evidence, existence_only, claims_snapshot) in table {
            let dir = tempfile::tempdir().unwrap();
            let project = dir.path().join("project");
            // The parent of the path is a regular file (not-a-directory error).
            std::fs::create_dir_all(&project).unwrap();
            std::fs::write(project.join("src"), "file").unwrap();
            let mut saved = unverified_bundle(&project, "export const a = 1;");
            saved.analysis.problems.clear();
            saved.sources[0].revision_evidence = evidence;
            if existence_only {
                saved.project_relative.clear();
                saved
                    .existence_relative
                    .insert("src-0123456789abcdef".to_owned(), "src/a.js".to_owned());
            }
            mark_stale(&mut saved);
            let label = format!("{evidence:?} existence_only={existence_only}");
            assert!(!saved.sources[0].stale, "{label}");
            assert_eq!(
                problem_count(&saved, "BW_SOURCE_FRESHNESS_UNVERIFIED"),
                1,
                "{label}"
            );
            let message = &saved
                .analysis
                .problems
                .iter()
                .find(|entry| entry.code == "BW_SOURCE_FRESHNESS_UNVERIFIED")
                .unwrap()
                .message;
            assert_eq!(
                message.contains("analysis uses the build snapshot"),
                claims_snapshot,
                "{label}: {message}"
            );
        }
    }

    #[test]
    fn project_file_reads_are_classified_not_collapsed() {
        let dir = tempfile::tempdir().unwrap();
        let project = std::fs::canonicalize(dir.path()).unwrap();
        std::fs::write(project.join("blocker"), "file").unwrap();
        std::fs::create_dir(project.join("dir.js")).unwrap();
        std::fs::write(project.join("ok.js"), "ok").unwrap();
        assert_eq!(
            read_project_file(&project, Path::new("ok.js")).unwrap(),
            b"ok"
        );
        // (relative path, expected failure)
        let table = [
            ("missing.js", LiveFileFailure::Missing),
            ("dir.js", LiveFileFailure::NotPermitted),
            (
                "blocker/a.js",
                LiveFileFailure::Unreadable(std::io::ErrorKind::NotADirectory),
            ),
        ];
        for (relative, expected) in table {
            assert_eq!(
                read_project_file(&project, Path::new(relative)).unwrap_err(),
                expected,
                "{relative}"
            );
        }
    }

    #[test]
    fn an_unreadable_incomplete_marker_never_presents_a_run_as_complete() {
        let dir = tempfile::tempdir().unwrap();
        let run_dir = dir.path().join("run");
        std::fs::create_dir(&run_dir).unwrap();
        assert_eq!(
            normalized_status(RunStatus::Complete, &run_dir),
            RunStatus::Complete
        );
        std::fs::write(run_dir.join(INCOMPLETE_MARKER), "").unwrap();
        assert_eq!(
            normalized_status(RunStatus::Complete, &run_dir),
            RunStatus::Partial
        );
        std::fs::remove_file(run_dir.join(INCOMPLETE_MARKER)).unwrap();
        // A run directory that is a regular file cannot hold a marker.
        let file_dir = dir.path().join("file");
        std::fs::write(&file_dir, "file").unwrap();
        assert_eq!(
            normalized_status(RunStatus::Complete, &file_dir),
            RunStatus::Partial
        );
        #[cfg(unix)]
        {
            // A dangling symlink is a present marker entry, never absence.
            let dangling = dir.path().join("dangling");
            std::fs::create_dir(&dangling).unwrap();
            std::os::unix::fs::symlink(dir.path().join("gone"), dangling.join(INCOMPLETE_MARKER))
                .unwrap();
            assert_eq!(
                normalized_status(RunStatus::Complete, &dangling),
                RunStatus::Partial
            );
            if crate::sources::test_support::deny_access(&run_dir) {
                let status = normalized_status(RunStatus::Complete, &run_dir);
                crate::sources::test_support::restore_access(&run_dir);
                assert_eq!(status, RunStatus::Partial);
            } else {
                eprintln!("skipped: permissions cannot block this account");
            }
        }
    }

    #[test]
    fn load_separates_a_missing_bundle_from_an_inaccessible_one() {
        let dir = tempfile::tempdir().unwrap();
        let run_dir = dir.path().join("run");
        std::fs::create_dir(&run_dir).unwrap();
        let error = load(&run_dir).unwrap_err();
        assert!(error.message.contains("No result bundle"), "{error:?}");

        // The run directory is a regular file: the bundle path is not
        // "missing".
        let file_dir = dir.path().join("file");
        std::fs::write(&file_dir, "file").unwrap();
        let error = load(&file_dir).unwrap_err();
        assert!(error.message.contains("inaccessible"), "{error:?}");

        #[cfg(unix)]
        {
            let locked = dir.path().join("locked");
            std::fs::create_dir(&locked).unwrap();
            if crate::sources::test_support::deny_access(&locked) {
                let result = load(&locked);
                crate::sources::test_support::restore_access(&locked);
                let error = result.unwrap_err();
                assert!(error.message.contains("inaccessible"), "{error:?}");
            } else {
                eprintln!("skipped: permissions cannot block this account");
            }
        }
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

    /// Rechecked text sources, existence-checked sources and sources that keep
    /// their saved flag all count toward the single warning, and the count
    /// stays the same across repeated calls.
    #[test]
    fn warning_counts_existence_checked_sources_with_rechecked_and_retained_ones() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        // `b` differs from its saved hash; `y` exists; `x` was removed.
        std::fs::write(project.join("src/b.js"), "changed").unwrap();
        std::fs::write(project.join("src/y.png"), [0x89, b'P', b'N', b'G', 0, 0xff]).unwrap();
        let mut saved = sources_with_flags(&[
            ("b", false, Some("src/b.js")),
            // No entry in either map: the saved flag is retained.
            ("d", true, None),
            ("x", false, None),
            // Present again: the saved flag is cleared by the recheck.
            ("y", true, None),
        ]);
        saved.project_root = project.to_string_lossy().into_owned();
        saved
            .existence_relative
            .insert("x".to_owned(), "src/x.png".to_owned());
        saved
            .existence_relative
            .insert("y".to_owned(), "src/y.png".to_owned());
        for _ in 0..2 {
            mark_stale(&mut saved);
            let stale_ids: Vec<_> = saved
                .sources
                .iter()
                .filter(|source| source.stale)
                .map(|source| source.id.as_str())
                .collect();
            assert_eq!(stale_ids, ["b", "d", "x"]);
            let warnings: Vec<_> = saved
                .analysis
                .problems
                .iter()
                .filter(|entry| entry.code == "BW_SOURCE_STALE")
                .collect();
            assert_eq!(warnings.len(), 1);
            assert_eq!(
                warnings[0].message,
                "3 source file(s) changed or were removed since this analysis"
            );
            for (source, summary) in saved.sources.iter().zip(&saved.analysis.sources) {
                assert_eq!(source.stale, summary.stale);
            }
        }
    }

    /// An asset that existed during the analysis and is removed afterwards is
    /// reported once on every reopen and in every export, and cleared when the
    /// file is restored.
    #[test]
    fn asset_removed_after_the_analysis_is_reported_on_reopen_and_export() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(project.join("src")).unwrap();
        std::fs::write(project.join("src/a.js"), "export const a = 1;").unwrap();
        let logo = project.join("src/logo.png");
        std::fs::write(&logo, [0x89, b'P', b'N', b'G', 0, 0xff]).unwrap();
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
        let mut saved = bundle();
        saved.project_root = canonical.to_string_lossy().into_owned();
        saved.project_relative = store.project_relative.clone();
        saved.existence_relative = store.existence_relative.clone();
        saved.sources = store.into_files();
        saved.analysis.sources = saved.sources.iter().map(SourceFile::summary).collect();
        assert_eq!(saved.existence_relative.len(), 1);
        save(dir.path(), &saved).unwrap();
        let reopen = |expected: usize| {
            for _ in 0..2 {
                let loaded = load(dir.path()).unwrap();
                assert_eq!(stale_count(&loaded), expected);
                assert_eq!(loaded.sources.iter().filter(|s| s.stale).count(), expected);
                let html = crate::export::render(
                    "<html><head></head><body></body></html>",
                    &loaded.analysis,
                    &loaded.sources,
                    true,
                );
                let marker = "id=\"bundlewhy-report\">";
                let start = html.find(marker).unwrap() + marker.len();
                let end = start + html[start..].find("</script>").unwrap();
                let payload: serde_json::Value = serde_json::from_str(&html[start..end]).unwrap();
                let exported = payload["analysis"]["problems"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .filter(|entry| entry["code"] == "BW_SOURCE_STALE")
                    .count();
                assert_eq!(exported, expected);
            }
        };
        reopen(0);
        std::fs::remove_file(&logo).unwrap();
        reopen(1);
        std::fs::write(&logo, b"restored").unwrap();
        reopen(0);
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
