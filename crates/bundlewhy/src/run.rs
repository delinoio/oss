//! `analyze`, `open` and `export` command implementations.

use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
    sync::Arc,
    time::Instant,
};

use serde_json::json;

use crate::{
    adapter::{self, AdapterOptions, CancelSignal},
    analysis::{self, AnalysisInput, engine_identity},
    bundle::{self, INCOMPLETE_MARKER, ResultBundle},
    detect::{self, Selection, SelectionRequest},
    engine::BundlerChoice,
    error::{BwError, BwResult, ErrorCode, problem},
    export,
    model::{
        Analysis, ProblemSeverity, ReportMode, RunProgress, RunStage, RunStatus, SCHEMA_VERSION,
    },
    runlog::RunLog,
    server::{self, Served, ServerState},
    sources::sha256_hex,
};

#[derive(Debug, Clone)]
pub struct AnalyzeOptions {
    pub out_dir: PathBuf,
    pub project: Option<PathBuf>,
    pub config: Option<PathBuf>,
    pub config_name: Option<String>,
    pub bundler: Option<BundlerChoice>,
    pub allow_unsupported_engine: bool,
    pub no_open: bool,
    pub no_serve: bool,
    pub exclude_sources: bool,
}

const LOCKFILES: [&str; 5] = [
    "package-lock.json",
    "pnpm-lock.yaml",
    "yarn.lock",
    "bun.lock",
    "bun.lockb",
];

/// Hashes the files bundlewhy must never modify.
/// Lockfiles of the nearest ancestor directory that has one, stopping at a
/// repository root (`.git`). In a monorepo the workspace lockfile controls the
/// dependencies a package build uses.
fn ancestor_lockfiles(project_root: &Path) -> Vec<PathBuf> {
    let mut directory = project_root.parent();
    while let Some(current) = directory {
        let found: Vec<PathBuf> = LOCKFILES
            .iter()
            .map(|name| current.join(name))
            .filter(|path| path.is_file())
            .collect();
        if !found.is_empty() {
            return found;
        }
        if current.join(".git").exists() {
            break;
        }
        directory = current.parent();
    }
    Vec::new()
}

fn protected_files(selection: &Selection) -> BTreeMap<PathBuf, Option<String>> {
    let mut files: Vec<PathBuf> = LOCKFILES
        .iter()
        .map(|name| selection.project_root.join(name))
        .collect();
    files.extend(ancestor_lockfiles(&selection.project_root));
    files.push(selection.project_root.join("package.json"));
    if let Some(config) = &selection.config_file {
        files.push(config.clone());
    }
    files
        .into_iter()
        .map(|path| {
            let digest = std::fs::read(&path).ok().map(|bytes| sha256_hex(&bytes));
            (path, digest)
        })
        .collect()
}

fn prepare_out_dir(out_dir: &Path) -> BwResult<PathBuf> {
    std::fs::create_dir_all(out_dir)
        .and_then(|()| std::fs::canonicalize(out_dir))
        .map_err(|error| {
            BwError::new(
                ErrorCode::OutputDirInvalid,
                format!(
                    "Output directory {} is not usable: {error}",
                    out_dir.display()
                ),
                "Pass a writable directory with --out-dir <dir>.",
            )
        })
}

fn now() -> String {
    chrono::Utc::now().to_rfc3339()
}

fn node_executable() -> PathBuf {
    std::env::var_os("BUNDLEWHY_NODE").map_or_else(|| PathBuf::from("node"), PathBuf::from)
}

fn print_line(text: &str) {
    use std::io::Write;
    let _ = writeln!(std::io::stderr(), "{text}");
}

/// Outcome of `analyze`, used for the process exit code.
pub struct AnalyzeOutcome {
    pub status: RunStatus,
    pub error: Option<BwError>,
}

pub async fn analyze(options: AnalyzeOptions) -> BwResult<AnalyzeOutcome> {
    let selection = detect::select(&SelectionRequest {
        project: options.project.clone(),
        config: options.config.clone(),
        bundler: options.bundler,
    })?;
    // Validate paths before creating anything, so an unsupported path never
    // leaves a half-created run behind.
    adapter::utf8_path(&selection.project_root)?;
    if let Some(config) = &selection.config_file {
        adapter::utf8_path(config)?;
    }
    let out_dir = prepare_out_dir(&options.out_dir)?;
    adapter::utf8_path(&out_dir)?;
    let run_id = uuid::Uuid::now_v7().to_string();
    let run_dir = out_dir.join(&run_id);
    std::fs::create_dir(&run_dir).map_err(|error| {
        BwError::new(
            ErrorCode::OutputDirInvalid,
            format!("Could not create {}: {error}", run_dir.display()),
            "Check that the output directory is writable.",
        )
    })?;
    let started_at = now();
    let started = Instant::now();
    let log = Arc::new(RunLog::create(&run_dir, &run_id));
    let _ = std::fs::write(
        run_dir.join(INCOMPLETE_MARKER),
        "This run has not finished. Results are incomplete.\n",
    );
    tracing::info!(run_id = %run_id, project = %selection.project_root.display(), bundler = selection.bundler.as_str(), "run started");
    log.event(
        "run-started",
        json!({ "bundler": selection.bundler.as_str(), "config": selection.config_file.as_ref().and_then(|path| path.file_name()).map(|name| name.to_string_lossy()) }),
    );

    let initial = Analysis {
        schema_version: SCHEMA_VERSION,
        run_id: run_id.clone(),
        project_name: selection.project_name.clone(),
        status: RunStatus::Running,
        started_at: started_at.clone(),
        mode: ReportMode::Live,
        tool_version: env!("CARGO_PKG_VERSION").to_owned(),
        ..Analysis::default()
    };
    bundle::save(
        &run_dir,
        &ResultBundle {
            schema_version: SCHEMA_VERSION,
            project_root: selection.project_root.to_string_lossy().into_owned(),
            analysis: initial,
            ..ResultBundle::default()
        },
    )?;

    let cancel = Arc::new(CancelSignal::default());
    let state = ServerState::new(
        Served {
            progress: RunProgress {
                run_id: run_id.clone(),
                status: RunStatus::Running,
                stage: RunStage::Detecting,
                message: format!(
                    "Preparing the {} diagnostic build",
                    selection.bundler.as_str()
                ),
                elapsed_ms: 0,
                mode: ReportMode::Live,
                cancellable: true,
            },
            analysis: None,
            sources: Vec::new(),
            run_dir: run_dir.clone(),
            out_dir: out_dir.clone(),
            started,
        },
        cancel.clone(),
    );

    let mut url = None;
    if !options.no_serve {
        let running = server::start(state.clone()).await?;
        print_line(&format!(
            "bundlewhy run {run_id}\n  results: {}\n  open:    {}",
            run_dir.display(),
            running.url
        ));
        if !options.no_open
            && let Err(error) = crate::browser::open(&running.url)
        {
            print_line(&format!(
                "{}: {} {}",
                error.code, error.message, error.recovery
            ));
        }
        url = Some(running.url);
    } else {
        print_line(&format!(
            "bundlewhy run {run_id}\n  results: {}",
            run_dir.display()
        ));
    }

    let ctrl_c_cancel = cancel.clone();
    let interrupted = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let interrupted_flag = interrupted.clone();
    let ctrl_c = tokio::spawn(async move {
        if tokio::signal::ctrl_c().await.is_ok() {
            interrupted_flag.store(true, std::sync::atomic::Ordering::SeqCst);
            ctrl_c_cancel.cancel();
        }
    });

    let protected_before = protected_files(&selection);
    let adapter_options = AdapterOptions {
        config_name: options.config_name.clone(),
        allow_unsupported_engine: options.allow_unsupported_engine,
        node: node_executable(),
    };
    let mut stage_started = Instant::now();
    let mut current_stage = RunStage::Detecting;
    let progress_state = state.clone();
    let progress_log = log.clone();
    let result = adapter::run(&run_dir, &selection, &adapter_options, cancel.clone(), |stage, message| {
        if stage != current_stage && stage != RunStage::Unspecified {
            progress_log.event(
                "stage",
                json!({ "stage": current_stage.as_str(), "durationMs": stage_started.elapsed().as_millis() as u64 }),
            );
            current_stage = stage;
            stage_started = Instant::now();
        }
        print_line(&format!("  [{}] {message}", stage.as_str()));
        progress_state.update(|served| {
            if stage != RunStage::Unspecified {
                served.progress.stage = stage;
            }
            served.progress.message = message.clone();
        });
    })
    .await;
    log.event("stage", json!({ "stage": current_stage.as_str(), "durationMs": stage_started.elapsed().as_millis() as u64 }));

    // Analysis and measurement can take substantial CPU time; cancellation
    // stays available until saving starts.
    let set_stage = |stage: RunStage, message: &str| {
        state.update(|served| {
            served.progress.stage = stage;
            served.progress.message = message.to_owned();
            served.progress.cancellable =
                matches!(stage, RunStage::Analyzing | RunStage::Measuring);
        });
    };
    let is_cancelled = || cancel.is_cancelled();

    let (mut result_bundle, mut error) = match result {
        // Detectors are verified for client builds only; reject other builds
        // before any rule runs so no unverified finding is saved.
        Ok(collection) if collection.library_build || collection.ssr_build => incomplete_bundle(
            &selection,
            &run_id,
            &started_at,
            &log,
            options.exclude_sources,
            BwError::new(
                ErrorCode::UnsupportedBuild,
                "The selected configuration is a library, SSR or Node-target build",
                "bundlewhy analyzes client production builds only. Select a client build \
                 configuration.",
            ),
        ),
        Ok(collection) => {
            log.event(
                "engine",
                json!({
                    "bundlerPackage": collection.bundler_package,
                    "bundlerVersion": collection.bundler_version,
                    "minifier": collection.minifier,
                    "minifierVersion": collection.minifier_version,
                    "adapter": format!("{}@{}", collection.adapter_id, collection.adapter_version),
                }),
            );
            set_stage(RunStage::Analyzing, "Explaining optimization decisions");
            let analyzing = Instant::now();
            let measuring_started = std::sync::Mutex::new(None::<Instant>);
            let on_measuring = || {
                log.event(
                    "stage",
                    json!({ "stage": "analyzing", "durationMs": analyzing.elapsed().as_millis() as u64 }),
                );
                set_stage(RunStage::Measuring, "Measuring chunk sizes");
                if let Ok(mut started) = measuring_started.lock() {
                    *started = Some(Instant::now());
                }
            };
            let output = analysis::analyze(&AnalysisInput {
                run_id: run_id.clone(),
                project_root: selection.project_root.clone(),
                project_name: selection.project_name.clone(),
                bundler: selection.bundler,
                collection: &collection,
                build_dir: run_dir.join("build"),
                include_sources: !options.exclude_sources,
                started_at: started_at.clone(),
                on_measuring: Some(&on_measuring),
                cancelled: Some(&is_cancelled),
            });
            let measuring_ms = measuring_started
                .lock()
                .ok()
                .and_then(|started| *started)
                .map_or(0, |started| started.elapsed().as_millis() as u64);
            log.event(
                "stage",
                json!({ "stage": "measuring", "durationMs": measuring_ms, "diagnostics": output.analysis.diagnostics.len() }),
            );
            let analysis = output.analysis;
            let project_relative = output
                .sources
                .iter()
                .filter_map(|source| {
                    let path = selection.project_root.join(&source.path);
                    (source.origin == crate::model::SourceOrigin::Project
                        && path.starts_with(&selection.project_root))
                    .then(|| {
                        crate::sources::safe_project_relative(&source.path)
                            .map(|_| (source.id.clone(), source.path.clone()))
                    })
                    .flatten()
                })
                .collect();
            (
                ResultBundle {
                    schema_version: SCHEMA_VERSION,
                    project_root: selection.project_root.to_string_lossy().into_owned(),
                    analysis,
                    sources: output.sources,
                    project_relative,
                },
                None,
            )
        }
        Err(error) => incomplete_bundle(
            &selection,
            &run_id,
            &started_at,
            &log,
            options.exclude_sources,
            error,
        ),
    };
    // Ctrl+C can arrive after the adapter finished, while analysis or size
    // measurement ran. Never save such a run as complete.
    if cancel.is_cancelled()
        && error
            .as_ref()
            .is_none_or(|error| error.code != ErrorCode::RunCancelled)
    {
        tracing::warn!("cancelled after collection; discarding partial analysis");
        (result_bundle, error) = incomplete_bundle(
            &selection,
            &run_id,
            &started_at,
            &log,
            options.exclude_sources,
            adapter::cancelled_error(),
        );
    }

    let protected_after = protected_files(&selection);
    let modified: Vec<String> = protected_before
        .iter()
        .filter(|(path, digest)| protected_after.get(*path) != Some(digest))
        .filter_map(|(path, _)| {
            path.file_name()
                .map(|name| name.to_string_lossy().into_owned())
        })
        .collect();
    if !modified.is_empty() {
        tracing::error!(files = ?modified, "protected project files changed during the run");
        result_bundle.analysis.problems.push(problem(
            ErrorCode::ProjectModified,
            ProblemSeverity::Error,
            format!(
                "These project files changed during the run: {}",
                modified.join(", ")
            ),
            "bundlewhy does not write project files; a build plugin or another process changed \
             them. Review the changes with your version control.",
        ));
        // Results derived while protected inputs changed are not complete and
        // must not look successful to automation.
        if result_bundle.analysis.status == RunStatus::Complete {
            result_bundle.analysis.status = RunStatus::Partial;
        }
        if error.is_none() {
            error = Some(BwError::new(
                ErrorCode::ProjectModified,
                format!(
                    "Protected project files changed during the run: {}",
                    modified.join(", ")
                ),
                "Review the changes with your version control, then start a new run.",
            ));
        }
    }

    set_stage(RunStage::Saving, "Saving results");
    let saving_started = Instant::now();
    // Size fitting serializes the result and is part of saving; the stored
    // duration covers it. The final write cannot be inside the file it
    // writes, so the run log and progress report the total after it.
    bundle::fit_within_limit(&mut result_bundle)?;
    result_bundle.analysis.finished_at = now();
    result_bundle.analysis.duration_ms =
        u32::try_from(started.elapsed().as_millis()).unwrap_or(u32::MAX);
    bundle::save(&run_dir, &result_bundle)?;
    log.event(
        "stage",
        json!({ "stage": "saving", "durationMs": saving_started.elapsed().as_millis() as u64 }),
    );
    let total_ms = u32::try_from(started.elapsed().as_millis()).unwrap_or(u32::MAX);
    let status = result_bundle.analysis.status;
    if matches!(status, RunStatus::Complete) {
        let _ = std::fs::remove_file(run_dir.join(INCOMPLETE_MARKER));
    }
    log.event(
        "run-finished",
        json!({
            "status": status,
            "durationMs": total_ms,
            "diagnostics": result_bundle.analysis.diagnostics.len(),
            "problems": result_bundle.analysis.problems.iter().map(|entry| entry.code.clone()).collect::<Vec<_>>(),
        }),
    );
    tracing::info!(run_id = %run_id, ?status, diagnostics = result_bundle.analysis.diagnostics.len(), "run finished");

    let mut served_analysis = result_bundle.analysis.clone();
    served_analysis.mode = ReportMode::Live;
    state.update(|served| {
        served.progress.status = status;
        served.progress.stage = RunStage::Finished;
        served.progress.message = String::new();
        served.progress.cancellable = false;
        served.progress.elapsed_ms = total_ms;
        served.analysis = Some(served_analysis);
        served.sources = result_bundle.sources.clone();
    });

    print_line(&format!(
        "  status: {} · {} diagnostic(s) · {} problem(s)",
        status_label(status),
        result_bundle.analysis.diagnostics.len(),
        result_bundle.analysis.problems.len()
    ));
    for entry in &result_bundle.analysis.problems {
        print_line(&format!(
            "  {}: {} {}",
            entry.code, entry.message, entry.recovery
        ));
    }

    if let Some(url) = url
        && !interrupted.load(std::sync::atomic::Ordering::SeqCst)
    {
        print_line(&format!(
            "Results are served at {url}\nPress Ctrl+C to stop the server."
        ));
        let _ = ctrl_c.await;
    }
    Ok(AnalyzeOutcome { status, error })
}

/// The problem persisted for a failed run. Build and adapter errors can quote
/// source code (code frames, stderr tails); with `--exclude-sources` only the
/// code and a generic message are saved, and the details stay on the terminal.
fn saved_problem(error: &BwError, exclude_sources: bool) -> crate::model::Problem {
    let mut saved = error.problem(ProblemSeverity::Error);
    if exclude_sources {
        crate::error::sanitize_problem(&mut saved);
    }
    saved
}

/// Result bundle for a run that failed or was cancelled before analysis
/// finished. Coverage is unavailable everywhere and the error is a problem.
fn incomplete_bundle(
    selection: &Selection,
    run_id: &str,
    started_at: &str,
    log: &RunLog,
    exclude_sources: bool,
    error: BwError,
) -> (ResultBundle, Option<BwError>) {
    let status = if error.code == ErrorCode::RunCancelled {
        RunStatus::Cancelled
    } else {
        RunStatus::Failed
    };
    log.event("error", json!({ "code": error.code.as_str() }));
    tracing::error!(code = %error.code, "run did not complete");
    let mut analysis = Analysis {
        schema_version: SCHEMA_VERSION,
        run_id: run_id.to_owned(),
        project_name: selection.project_name.clone(),
        config_file: selection
            .config_file
            .as_deref()
            .map(|path| crate::sources::classify(&selection.project_root, path).display)
            .unwrap_or_default(),
        status,
        problems: vec![saved_problem(&error, exclude_sources)],
        started_at: started_at.to_owned(),
        mode: ReportMode::Live,
        tool_version: env!("CARGO_PKG_VERSION").to_owned(),
        ..Analysis::default()
    };
    // Engine identity is known only from a collection; record the
    // expected engine so the UI still shows what was attempted.
    let spec = selection.bundler.spec();
    let mut identity = engine_identity(selection.bundler, &crate::collect::Collection::default());
    identity.bundler_package = spec.package.to_owned();
    identity.bundler_version = "unknown".to_owned();
    // No engine version was discovered, so support is unknown rather than
    // "unsupported selected".
    identity.support = crate::model::EngineSupport::Unspecified;
    identity.diagnostic_notice =
        "No diagnostic build finished; no findings were collected.".to_owned();
    analysis.engine = Some(identity);
    analysis.coverage = crate::model::OptimizationCategory::ALL
        .into_iter()
        .map(|category| crate::model::CategoryCoverage {
            category,
            state: crate::model::CoverageState::Unavailable,
            note: "The run did not finish.".to_owned(),
            rules: Vec::new(),
        })
        .collect();
    (
        ResultBundle {
            schema_version: SCHEMA_VERSION,
            project_root: selection.project_root.to_string_lossy().into_owned(),
            analysis,
            ..ResultBundle::default()
        },
        Some(error),
    )
}

pub const fn status_label(status: RunStatus) -> &'static str {
    match status {
        RunStatus::Complete => "complete",
        RunStatus::Partial => "partial (some results are missing)",
        RunStatus::Cancelled => "cancelled (incomplete)",
        RunStatus::Failed => "failed",
        RunStatus::Running => "running",
        RunStatus::Unspecified => "unknown",
    }
}

pub async fn open(run_dir: &Path, no_open: bool) -> BwResult<()> {
    let run_dir = std::fs::canonicalize(run_dir).map_err(|error| {
        BwError::new(
            ErrorCode::ResultInvalid,
            format!(
                "Run directory {} is not accessible: {error}",
                run_dir.display()
            ),
            "Pass a run directory created by `bundlewhy analyze`.",
        )
    })?;
    let loaded = bundle::load(&run_dir)?;
    let out_dir = run_dir
        .parent()
        .map_or_else(|| run_dir.clone(), Path::to_path_buf);
    let cancel = Arc::new(CancelSignal::default());
    let state = ServerState::new(
        Served {
            progress: RunProgress::default(),
            analysis: None,
            sources: Vec::new(),
            run_dir: run_dir.clone(),
            out_dir,
            started: Instant::now(),
        },
        cancel,
    );
    let status = loaded.analysis.status;
    server::serve_bundle(&state, loaded, run_dir.clone());
    let running = server::start(state).await?;
    print_line(&format!(
        "bundlewhy saved run ({})\n  results: {}\n  open:    {}",
        status_label(status),
        run_dir.display(),
        running.url
    ));
    if !no_open && let Err(error) = crate::browser::open(&running.url) {
        print_line(&format!(
            "{}: {} {}",
            error.code, error.message, error.recovery
        ));
    }
    print_line("Press Ctrl+C to stop the server.");
    let _ = tokio::signal::ctrl_c().await;
    Ok(())
}

/// Creates the export exclusively (never following a link). A replacement is
/// written to an exclusively created sibling and renamed over the target.
fn write_export(output: &Path, bytes: &[u8], replace: bool) -> std::io::Result<()> {
    use std::io::Write;
    let create = |path: &Path| {
        std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(path)
    };
    if !replace {
        return create(output)?.write_all(bytes);
    }
    let name = output.file_name().map_or_else(
        || "report".into(),
        |name| name.to_string_lossy().into_owned(),
    );
    let temporary = output.with_file_name(format!(".{name}.{}.tmp", uuid::Uuid::now_v7()));
    let result = create(&temporary)
        .and_then(|mut file| file.write_all(bytes))
        .and_then(|()| std::fs::rename(&temporary, output));
    if result.is_err() {
        let _ = std::fs::remove_file(&temporary);
    }
    result
}

pub fn export_report(
    run_dir: &Path,
    output: &Path,
    exclude_sources: bool,
    force: bool,
) -> BwResult<()> {
    let loaded = bundle::load(run_dir)?;
    // Never write through a symlink: an existing link (dangling or not) is
    // refused, and --force replaces a regular file by renaming a new file
    // over it, which replaces the directory entry rather than a link target.
    let existing = std::fs::symlink_metadata(output).ok();
    if existing
        .as_ref()
        .is_some_and(|metadata| metadata.file_type().is_symlink())
    {
        return Err(BwError::new(
            ErrorCode::ExportFailed,
            format!("{} is a symbolic link", output.display()),
            "Choose an --output path that is not a symbolic link.",
        ));
    }
    if existing.is_some() && !force {
        return Err(BwError::new(
            ErrorCode::ExportFailed,
            format!("{} already exists", output.display()),
            "Choose another --output path or pass --force to replace it.",
        ));
    }
    let include_sources = !exclude_sources;
    let html = export::render(
        export::UI_HTML,
        &loaded.analysis,
        &loaded.sources,
        include_sources,
    );
    write_export(output, html.as_bytes(), existing.is_some()).map_err(|error| {
        BwError::new(
            ErrorCode::ExportFailed,
            format!("Could not write {}: {error}", output.display()),
            "Check that the destination is writable.",
        )
    })?;
    let included = loaded
        .sources
        .iter()
        .filter(|source| source.content_included)
        .count();
    print_line(&format!(
        "Exported {} ({} bytes)",
        output.display(),
        html.len()
    ));
    if include_sources && included > 0 {
        print_line(&format!(
            "Source content included: yes ({included} file(s)). Reports with source content may \
             contain secrets from your code; review before sharing. Use --exclude-sources to omit \
             it."
        ));
    } else {
        print_line("Source content included: no.");
    }
    tracing::info!(output = %output.display(), sources = include_sources && included > 0, "report exported");
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn source_excluded_runs_do_not_save_build_error_excerpts() {
        let error = BwError::new(
            ErrorCode::BuildFailed,
            "src/secret.ts:3:1 const apiKey = \"sk-live\"",
            "Fix it.",
        );
        let saved = saved_problem(&error, true);
        assert!(!saved.message.contains("apiKey"));
        assert_eq!(saved.code, "BW_BUILD_FAILED");
        assert!(saved_problem(&error, false).message.contains("apiKey"));
    }

    #[cfg(unix)]
    #[test]
    fn exports_never_write_through_symlinks() {
        let dir = tempfile::tempdir().unwrap();
        let target = dir.path().join("target.txt");
        std::fs::write(&target, "keep").unwrap();
        let link = dir.path().join("report.html");
        std::os::unix::fs::symlink(&target, &link).unwrap();
        // write_export refuses an existing entry when not replacing.
        assert!(write_export(&link, b"x", false).is_err());
        assert_eq!(std::fs::read_to_string(&target).unwrap(), "keep");
        let dangling = dir.path().join("dangling.html");
        std::os::unix::fs::symlink(dir.path().join("missing.txt"), &dangling).unwrap();
        assert!(write_export(&dangling, b"x", false).is_err());
        assert!(!dir.path().join("missing.txt").exists());
        let regular = dir.path().join("regular.html");
        std::fs::write(&regular, "old").unwrap();
        write_export(&regular, b"new", true).unwrap();
        assert_eq!(std::fs::read_to_string(&regular).unwrap(), "new");
    }

    #[test]
    fn workspace_lockfiles_above_the_project_are_protected() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::create_dir(dir.path().join(".git")).unwrap();
        std::fs::write(dir.path().join("pnpm-lock.yaml"), "lockfileVersion: 9").unwrap();
        let package = dir.path().join("packages/app");
        std::fs::create_dir_all(&package).unwrap();
        assert_eq!(
            ancestor_lockfiles(&package),
            vec![dir.path().join("pnpm-lock.yaml")]
        );
        // A repository root without a lockfile ends the search.
        let other = tempfile::tempdir().unwrap();
        std::fs::create_dir(other.path().join(".git")).unwrap();
        std::fs::create_dir_all(other.path().join("app")).unwrap();
        assert!(ancestor_lockfiles(&other.path().join("app")).is_empty());
    }
}
