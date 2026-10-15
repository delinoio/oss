//! `analyze`, `open` and `export` command implementations.

use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
    sync::Arc,
    time::Instant,
};

use serde_json::json;

use crate::{
    adapter::{self, AdapterOptions, CancelSignal, DetectedEngine},
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
/// Lockfile candidates in every ancestor directory up to the repository root
/// (`.git`), whether or not they exist yet. Missing candidates are hashed as
/// absent, so a workspace lockfile created during the build is detected.
fn ancestor_lockfiles(project_root: &Path) -> Vec<PathBuf> {
    if project_root.join(".git").exists() {
        return Vec::new();
    }

    let mut candidates = Vec::new();
    let mut directory = project_root.parent();
    while let Some(current) = directory {
        candidates.extend(LOCKFILES.iter().map(|name| current.join(name)));
        if current.join(".git").exists() {
            break;
        }
        directory = current.parent();
    }
    candidates
}

fn protected_files_with_root(
    selection: &Selection,
    resolved_root: Option<&Path>,
) -> BTreeMap<PathBuf, Option<String>> {
    let mut files: Vec<PathBuf> = LOCKFILES
        .iter()
        .map(|name| selection.project_root.join(name))
        .collect();
    files.extend(ancestor_lockfiles(&selection.project_root));
    files.push(selection.project_root.join("package.json"));
    if let Some(root) = resolved_root.filter(|root| *root != selection.project_root) {
        files.extend(LOCKFILES.iter().map(|name| root.join(name)));
        files.extend(ancestor_lockfiles(root));
        files.push(root.join("package.json"));
        if let Some((manifest, _)) = nearest_package(root) {
            files.push(manifest);
        }
    }
    if let Some(config) = &selection.config_file {
        files.push(config.clone());
    }
    files
        .into_iter()
        .map(|path| {
            let digest = file_digest(&path);
            (path, digest)
        })
        .collect()
}

fn file_digest(path: &Path) -> Option<String> {
    std::fs::read(path).ok().map(|bytes| sha256_hex(&bytes))
}

fn refresh_protected_files(
    protected_before: &BTreeMap<PathBuf, Option<String>>,
) -> BTreeMap<PathBuf, Option<String>> {
    protected_before
        .keys()
        .map(|path| (path.clone(), file_digest(path)))
        .collect()
}

fn protected_files(selection: &Selection) -> BTreeMap<PathBuf, Option<String>> {
    protected_files_with_root(selection, None)
}

fn nearest_package(root: &Path) -> Option<(PathBuf, String)> {
    let mut directory = Some(root);
    while let Some(current) = directory {
        let manifest = current.join("package.json");
        let name = std::fs::read(&manifest)
            .ok()
            .and_then(|bytes| serde_json::from_slice::<serde_json::Value>(&bytes).ok())
            .and_then(|manifest| {
                manifest
                    .get("name")
                    .and_then(serde_json::Value::as_str)
                    .filter(|name| !name.is_empty())
                    .map(str::to_owned)
            });
        if let Some(name) = name {
            return Some((manifest, name));
        }
        let parent = current.parent();
        directory = parent.filter(|parent| *parent != current);
    }
    None
}

fn project_name_at(root: &Path, fallback: &str) -> String {
    if let Some((_, name)) = nearest_package(root) {
        return name;
    }
    fallback.to_owned()
}

fn project_name_for_collection(
    collection: &crate::collect::Collection,
    effective_root: &Path,
    selected_root: &Path,
    fallback: &str,
) -> String {
    let identity_root = if collection.bundler == "vite" {
        analysis::context_root(collection, selected_root)
    } else {
        effective_root.to_owned()
    };
    project_name_at(&identity_root, fallback)
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

#[derive(Debug)]
struct CompletionTiming {
    total_ms: u32,
    saving_ms: u64,
}

fn elapsed_ms(started: Instant) -> u32 {
    u32::try_from(started.elapsed().as_millis()).unwrap_or(u32::MAX)
}

fn persist_final_result(
    run_dir: &Path,
    result_bundle: &mut ResultBundle,
    started: Instant,
    saving_started: Instant,
    save_bundle: impl FnOnce(&Path, &ResultBundle) -> BwResult<()>,
) -> BwResult<CompletionTiming> {
    bundle::fit_within_limit(result_bundle)?;
    // Keep these fields as the compatibility fallback for older readers. The
    // completion record below replaces them when this run is reopened.
    result_bundle.analysis.finished_at = now();
    result_bundle.analysis.duration_ms = elapsed_ms(started);
    save_bundle(run_dir, result_bundle)?;

    // The measured boundary follows the complete main bundle and summary
    // writes. The small completion record cannot include its own write time.
    let saved_at = Instant::now();
    let timing = CompletionTiming {
        total_ms: u32::try_from(saved_at.duration_since(started).as_millis()).unwrap_or(u32::MAX),
        saving_ms: saved_at.duration_since(saving_started).as_millis() as u64,
    };
    let finished_at = now();
    bundle::save_completion(
        run_dir,
        &result_bundle.analysis.run_id,
        timing.total_ms,
        &finished_at,
    )?;
    if result_bundle.analysis.status == RunStatus::Complete {
        std::fs::remove_file(run_dir.join(INCOMPLETE_MARKER)).map_err(|error| {
            BwError::new(
                ErrorCode::OutputDirInvalid,
                format!("Could not finalize {}: {error}", run_dir.display()),
                "Check that the output directory is writable.",
            )
        })?;
    }
    result_bundle.analysis.finished_at = finished_at;
    result_bundle.analysis.duration_ms = timing.total_ms;
    Ok(timing)
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
    std::fs::write(
        run_dir.join(INCOMPLETE_MARKER),
        "This run has not finished. Results are incomplete.\n",
    )
    .map_err(|error| {
        BwError::new(
            ErrorCode::OutputDirInvalid,
            format!("Could not mark {} incomplete: {error}", run_dir.display()),
            "Check that the output directory is writable.",
        )
    })?;
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

    let mut protected_before = protected_files(&selection);
    let adapter_options = AdapterOptions {
        config_name: options.config_name.clone(),
        allow_unsupported_engine: options.allow_unsupported_engine,
        node: node_executable(),
    };
    let mut stage_started = Instant::now();
    let mut current_stage = RunStage::Detecting;
    let progress_state = state.clone();
    let progress_log = log.clone();
    // The adapter reports the engine before its version check and build, so
    // keep it for the failed-run result.
    let mut detected_engine: Option<DetectedEngine> = None;
    let result = adapter::run(
        &run_dir,
        &selection,
        &adapter_options,
        cancel.clone(),
        |stage, message| {
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
        },
        |root| {
            // Keep the snapshot captured before config evaluation. The adapter
            // can discover additional roots only after evaluating user config,
            // so add new paths without replacing hashes for paths already
            // protected before the adapter started.
            for (path, digest) in protected_files_with_root(&selection, Some(&root)) {
                protected_before.entry(path).or_insert(digest);
            }
        },
        |engine| detected_engine = Some(engine),
    )
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
    let failed_engine = || {
        failed_run_engine_identity(
            selection.bundler,
            detected_engine.as_ref(),
            options.allow_unsupported_engine,
        )
    };

    let (mut result_bundle, mut error) = match result {
        // Detectors are verified for client builds only; reject other builds
        // before any rule runs so no unverified finding is saved.
        Ok(collection) if collection.library_build || collection.ssr_build => incomplete_bundle(
            &selection,
            &run_id,
            &started_at,
            &log,
            options.exclude_sources,
            failed_engine(),
            BwError::new(
                ErrorCode::UnsupportedBuild,
                "The selected configuration is a library, SSR or Node-target build",
                "bundlewhy analyzes client production builds only. Select a client build \
                 configuration.",
            ),
        ),
        Ok(collection) => {
            let effective_root = analysis::effective_root(&collection, &selection.project_root);
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
                project_root: effective_root.clone(),
                project_name: project_name_for_collection(
                    &collection,
                    &effective_root,
                    &selection.project_root,
                    &selection.project_name,
                ),
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
            let project_relative = bundle::retained_relative(
                &effective_root,
                &output.sources,
                &output.project_relative,
            );
            let existence_relative = bundle::retained_relative(
                &effective_root,
                &output.sources,
                &output.existence_relative,
            );
            (
                ResultBundle {
                    schema_version: SCHEMA_VERSION,
                    project_root: effective_root.to_string_lossy().into_owned(),
                    analysis,
                    sources: output.sources,
                    project_relative,
                    existence_relative,
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
            failed_engine(),
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
            failed_engine(),
            adapter::cancelled_error(),
        );
    }

    let protected_after = refresh_protected_files(&protected_before);
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
    let timing = persist_final_result(
        &run_dir,
        &mut result_bundle,
        started,
        saving_started,
        bundle::save,
    )?;
    log.event(
        "stage",
        json!({ "stage": "saving", "durationMs": timing.saving_ms }),
    );
    let total_ms = timing.total_ms;
    let status = result_bundle.analysis.status;
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

/// Engine identity for a run that did not finish. Without a collection, the
/// only evidence is the adapter's `engine` frame. With it, the detected
/// package and version are recorded; support is `Supported` for the frozen
/// matrix version and `UnsupportedSelected` only when the user explicitly
/// allowed a different version. A different version that was not allowed was
/// rejected before explicit selection, so support stays `Unspecified`. With no
/// frame the engine version is unknown and support is `Unspecified`.
fn failed_run_engine_identity(
    bundler: BundlerChoice,
    detected_engine: Option<&DetectedEngine>,
    allow_unsupported_engine: bool,
) -> crate::model::EngineIdentity {
    use crate::model::EngineSupport;
    let spec = bundler.spec();
    let mut identity = engine_identity(bundler, &crate::collect::Collection::default());
    match detected_engine {
        Some(engine) => {
            identity.bundler_package = engine.package.clone();
            identity.bundler_version = engine.version.clone();
            identity.support = if spec.is_matrix_version(&engine.version) {
                EngineSupport::Supported
            } else if allow_unsupported_engine {
                EngineSupport::UnsupportedSelected
            } else {
                EngineSupport::Unspecified
            };
        }
        None => {
            identity.bundler_package = spec.package.to_owned();
            identity.bundler_version = "unknown".to_owned();
            identity.support = EngineSupport::Unspecified;
        }
    }
    identity.diagnostic_notice =
        "No diagnostic build finished; no findings were collected.".to_owned();
    identity
}

/// Result bundle for a run that failed or was cancelled before analysis
/// finished. Coverage is unavailable everywhere and the error is a problem.
fn incomplete_bundle(
    selection: &Selection,
    run_id: &str,
    started_at: &str,
    log: &RunLog,
    exclude_sources: bool,
    engine: crate::model::EngineIdentity,
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
    analysis.engine = Some(engine);
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

/// Creates `output` exclusively and runs `write`. The file was created by this
/// call, so removing it on a failed write cannot touch anything else and
/// leaves no truncated report blocking a retry.
fn write_new_file(
    output: &Path,
    write: impl FnOnce(&mut std::fs::File) -> std::io::Result<()>,
) -> std::io::Result<()> {
    let mut file = std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(output)?;
    let written = write(&mut file);
    if written.is_err() {
        drop(file);
        let _ = std::fs::remove_file(output);
    }
    written
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
        return write_new_file(output, |file| {
            file.write_all(bytes).and_then(|()| file.sync_all())
        });
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

    fn result_for_timing(status: RunStatus) -> ResultBundle {
        ResultBundle {
            schema_version: SCHEMA_VERSION,
            analysis: Analysis {
                schema_version: SCHEMA_VERSION,
                run_id: uuid::Uuid::now_v7().to_string(),
                status,
                ..Analysis::default()
            },
            ..ResultBundle::default()
        }
    }

    #[test]
    fn completed_at_includes_delayed_bundle_write_in_live_saved_and_exported_results() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join(INCOMPLETE_MARKER), "incomplete").unwrap();
        let mut result = result_for_timing(RunStatus::Complete);
        let started = Instant::now();
        let saving_started = Instant::now();
        let timing = persist_final_result(
            dir.path(),
            &mut result,
            started,
            saving_started,
            |path, bundle| {
                std::thread::sleep(std::time::Duration::from_millis(40));
                bundle::save(path, bundle)
            },
        )
        .unwrap();
        assert!(timing.saving_ms >= 40);
        assert!(timing.total_ms >= 40);
        assert_eq!(result.analysis.duration_ms, timing.total_ms);
        assert!(!dir.path().join(INCOMPLETE_MARKER).exists());

        let loaded = bundle::load(dir.path()).unwrap();
        assert_eq!(loaded.analysis.status, RunStatus::Complete);
        assert_eq!(loaded.analysis.duration_ms, timing.total_ms);
        assert_eq!(loaded.analysis.finished_at, result.analysis.finished_at);
        let state = ServerState::new(
            Served {
                progress: RunProgress::default(),
                analysis: None,
                sources: Vec::new(),
                run_dir: dir.path().to_path_buf(),
                out_dir: dir.path().to_path_buf(),
                started,
            },
            Arc::new(CancelSignal::default()),
        );
        let progress = server::serve_bundle(&state, loaded.clone(), dir.path().to_path_buf());
        assert_eq!(progress.elapsed_ms, timing.total_ms);
        let html = export::render(
            "<html><head><!--bundlewhy:report--></head><body></body></html>",
            &loaded.analysis,
            &loaded.sources,
            true,
        );
        let marker = "id=\"bundlewhy-report\">";
        let start = html.find(marker).unwrap() + marker.len();
        let end = start + html[start..].find("</script>").unwrap();
        let exported: serde_json::Value = serde_json::from_str(&html[start..end]).unwrap();
        assert_eq!(exported["analysis"]["durationMs"], timing.total_ms);
    }

    #[test]
    fn completed_at_failed_timing_write_keeps_a_saved_run_incomplete() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join(INCOMPLETE_MARKER), "incomplete").unwrap();
        let mut initial = result_for_timing(RunStatus::Running);
        bundle::save(dir.path(), &initial).unwrap();
        std::fs::create_dir(dir.path().join(format!("{}.tmp", bundle::COMPLETION_FILE))).unwrap();
        initial.analysis.status = RunStatus::Complete;
        let error = persist_final_result(
            dir.path(),
            &mut initial,
            Instant::now(),
            Instant::now(),
            bundle::save,
        )
        .unwrap_err();
        assert_eq!(error.code, ErrorCode::OutputDirInvalid);
        assert!(dir.path().join(INCOMPLETE_MARKER).exists());
        assert!(!dir.path().join(bundle::COMPLETION_FILE).exists());
        assert_eq!(
            bundle::load(dir.path()).unwrap().analysis.status,
            RunStatus::Partial
        );
    }

    #[test]
    fn completed_at_interrupted_after_bundle_write_is_not_complete() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join(INCOMPLETE_MARKER), "incomplete").unwrap();
        let result = result_for_timing(RunStatus::Complete);
        bundle::save(dir.path(), &result).unwrap();
        // Simulate termination after the main write and before the timing
        // record and incomplete-marker removal.
        let loaded = bundle::load(dir.path()).unwrap();
        assert_eq!(loaded.analysis.status, RunStatus::Partial);
        assert!(!dir.path().join(bundle::COMPLETION_FILE).exists());
    }

    #[test]
    fn completed_at_failed_bundle_write_preserves_running_result() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join(INCOMPLETE_MARKER), "incomplete").unwrap();
        let mut result = result_for_timing(RunStatus::Running);
        bundle::save(dir.path(), &result).unwrap();
        std::fs::create_dir(dir.path().join(format!("{}.tmp", bundle::BUNDLE_FILE))).unwrap();
        result.analysis.status = RunStatus::Complete;
        assert!(
            persist_final_result(
                dir.path(),
                &mut result,
                Instant::now(),
                Instant::now(),
                bundle::save,
            )
            .is_err()
        );
        assert!(dir.path().join(INCOMPLETE_MARKER).exists());
        assert!(!dir.path().join(bundle::COMPLETION_FILE).exists());
        assert_eq!(
            bundle::load(dir.path()).unwrap().analysis.status,
            RunStatus::Failed
        );
    }

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

    #[test]
    fn failed_new_exports_do_not_leave_truncated_files() {
        let dir = tempfile::tempdir().unwrap();
        let output = dir.path().join("report.html");
        let error = write_new_file(&output, |file| {
            std::io::Write::write_all(file, b"<html partial")?;
            Err(std::io::Error::other("disk full"))
        })
        .unwrap_err();
        assert_eq!(error.to_string(), "disk full");
        assert!(
            !output.exists(),
            "a failed export must not leave a truncated file"
        );
        // A retry without --force then succeeds.
        write_export(&output, b"ok", false).unwrap();
        assert_eq!(std::fs::read(&output).unwrap(), b"ok");
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
    fn repository_root_lockfiles_do_not_protect_parent_lockfiles() {
        let dir = tempfile::tempdir().unwrap();
        let parent = dir.path().join("parent");
        let project = parent.join("repo");
        std::fs::create_dir_all(project.join(".git")).unwrap();
        let selection = Selection {
            project_root: project.clone(),
            project_name: "repo".to_owned(),
            bundler: BundlerChoice::Vite,
            config_file: None,
        };

        assert!(ancestor_lockfiles(&project).is_empty());
        let before = protected_files(&selection);
        std::fs::write(parent.join("pnpm-lock.yaml"), "lockfileVersion: 9").unwrap();
        assert_eq!(
            before,
            protected_files(&selection),
            "creating a parent lockfile must not mark the project as modified"
        );
        std::fs::write(parent.join("pnpm-lock.yaml"), "lockfileVersion: 8").unwrap();
        assert_eq!(
            before,
            protected_files(&selection),
            "changing a parent lockfile must not mark the project as modified"
        );

        std::fs::write(project.join("pnpm-lock.yaml"), "lockfileVersion: 9").unwrap();
        assert_ne!(
            before,
            protected_files(&selection),
            "a project lockfile created during the run must be detected"
        );
        let with_lockfile = protected_files(&selection);
        std::fs::write(project.join("pnpm-lock.yaml"), "lockfileVersion: 8").unwrap();
        assert_ne!(
            with_lockfile,
            protected_files(&selection),
            "a project lockfile changed during the run must be detected"
        );
    }

    #[test]
    fn workspace_lockfiles_above_the_project_are_protected() {
        let dir = tempfile::tempdir().unwrap();
        let parent = dir.path().join("parent");
        let repository = parent.join("repo");
        std::fs::create_dir_all(repository.join(".git")).unwrap();
        let package = repository.join("packages/app");
        std::fs::create_dir_all(&package).unwrap();
        let candidates = ancestor_lockfiles(&package);
        // Candidates exist for every ancestor up to and including the
        // repository root, even before any lockfile exists.
        assert!(candidates.contains(&repository.join("pnpm-lock.yaml")));
        assert!(candidates.contains(&repository.join("packages/package-lock.json")));
        assert!(!candidates.contains(&parent.join("pnpm-lock.yaml")));
        let selection = Selection {
            project_root: package.clone(),
            project_name: "app".to_owned(),
            bundler: BundlerChoice::Vite,
            config_file: None,
        };
        let before = protected_files(&selection);
        std::fs::write(parent.join("pnpm-lock.yaml"), "lockfileVersion: 9").unwrap();
        assert_eq!(before, protected_files(&selection));
        std::fs::write(repository.join("packages/package-lock.json"), "{}").unwrap();
        let with_workspace_lockfile = protected_files(&selection);
        assert_ne!(
            before, with_workspace_lockfile,
            "a workspace lockfile created during the run must be detected"
        );
        std::fs::write(repository.join("pnpm-lock.yaml"), "lockfileVersion: 9").unwrap();
        let with_repository_lockfile = protected_files(&selection);
        assert_ne!(
            with_workspace_lockfile, with_repository_lockfile,
            "a repository lockfile created during the run must be detected"
        );
        std::fs::write(package.join("yarn.lock"), "lockfileVersion: 9").unwrap();
        assert_ne!(
            with_repository_lockfile,
            protected_files(&selection),
            "a project lockfile created during the run must be detected"
        );
    }

    #[test]
    fn resolved_external_root_files_are_protected() {
        let dir = tempfile::tempdir().unwrap();
        let repository = dir.path().join("repo");
        let project = repository.join("packages/app");
        let external = dir.path().join("web");
        std::fs::create_dir_all(project.join(".git")).unwrap();
        std::fs::create_dir_all(&external).unwrap();
        let selection = Selection {
            project_root: project,
            project_name: "app".to_owned(),
            bundler: BundlerChoice::Vite,
            config_file: None,
        };

        std::fs::write(
            external.join("package.json"),
            r#"{"name":"web-app","private":true}"#,
        )
        .unwrap();
        let before = protected_files_with_root(&selection, Some(&external));
        std::fs::write(external.join("pnpm-lock.yaml"), "lockfileVersion: 9").unwrap();
        let after = protected_files_with_root(&selection, Some(&external));

        assert_ne!(
            before, after,
            "external root lockfile changes must be detected"
        );
        assert_eq!(
            project_name_at(&external, &selection.project_name),
            "web-app"
        );
    }

    #[test]
    fn project_name_uses_nearest_ancestor_manifest() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().join("repo/apps/web/src");
        std::fs::create_dir_all(&root).unwrap();
        std::fs::write(
            dir.path().join("repo/apps/web/package.json"),
            r#"{"name":"web-app"}"#,
        )
        .unwrap();

        assert_eq!(project_name_at(&root, "fallback"), "web-app");
    }

    #[test]
    fn nearest_external_package_manifest_is_protected() {
        let dir = tempfile::tempdir().unwrap();
        let repository = dir.path().join("repo");
        let project = repository.join("packages/app");
        let external = repository.join("apps/web/src");
        std::fs::create_dir_all(project.join(".git")).unwrap();
        std::fs::create_dir_all(&external).unwrap();
        let manifest = repository.join("apps/web/package.json");
        std::fs::write(&manifest, r#"{"name":"web-app","sideEffects":false}"#).unwrap();
        let selection = Selection {
            project_root: project,
            project_name: "app".to_owned(),
            bundler: BundlerChoice::Vite,
            config_file: None,
        };

        let before = protected_files_with_root(&selection, Some(&external));
        std::fs::write(&manifest, r#"{"name":"web-app","sideEffects":true}"#).unwrap();
        let after = protected_files_with_root(&selection, Some(&external));

        assert_ne!(before, after);
    }

    #[test]
    fn discovered_root_does_not_replace_pre_adapter_protected_digests() {
        let dir = tempfile::tempdir().unwrap();
        let repository = dir.path().join("repo");
        let project = repository.join("packages/app");
        let external = repository.join("apps/web");
        std::fs::create_dir_all(project.join(".git")).unwrap();
        std::fs::create_dir_all(&external).unwrap();
        let project_manifest = project.join("package.json");
        std::fs::write(&project_manifest, r#"{"name":"app"}"#).unwrap();
        let external_manifest = external.join("package.json");
        std::fs::write(&external_manifest, r#"{"name":"web"}"#).unwrap();
        let selection = Selection {
            project_root: project,
            project_name: "app".to_owned(),
            bundler: BundlerChoice::Vite,
            config_file: None,
        };

        let mut before = protected_files(&selection);
        let original_project_digest = before.get(&project_manifest).cloned().unwrap();
        std::fs::write(&project_manifest, r#"{"name":"changed"}"#).unwrap();
        for (path, digest) in protected_files_with_root(&selection, Some(&external)) {
            before.entry(path).or_insert(digest);
        }

        assert_eq!(
            before.get(&project_manifest),
            Some(&original_project_digest)
        );
        assert_ne!(
            refresh_protected_files(&before).get(&project_manifest),
            Some(&original_project_digest)
        );
        assert!(before.contains_key(&external_manifest));
    }

    #[test]
    fn vite_project_name_uses_collection_context_for_nested_root() {
        let dir = tempfile::tempdir().unwrap();
        let selected = dir.path().join("project");
        let nested = selected.join("web");
        std::fs::create_dir_all(&nested).unwrap();
        std::fs::write(selected.join("package.json"), r#"{"name":"workspace"}"#).unwrap();
        std::fs::write(nested.join("package.json"), r#"{"name":"web-app"}"#).unwrap();
        let collection = crate::collect::Collection {
            bundler: "vite".to_owned(),
            context: Some(nested.to_string_lossy().into_owned()),
            ..Default::default()
        };

        assert_eq!(
            project_name_for_collection(&collection, &selected, &selected, "fallback"),
            "web-app"
        );
    }

    fn selection_for(bundler: BundlerChoice, root: &Path) -> Selection {
        Selection {
            project_root: root.to_path_buf(),
            project_name: "p".to_owned(),
            bundler,
            config_file: None,
        }
    }

    fn detected(package: &str, version: &str) -> DetectedEngine {
        DetectedEngine {
            package: package.to_owned(),
            version: version.to_owned(),
        }
    }

    fn failed_bundle(
        bundler: BundlerChoice,
        engine: Option<&DetectedEngine>,
        allow_unsupported_engine: bool,
        error: BwError,
    ) -> (ResultBundle, Option<BwError>) {
        let dir = tempfile::tempdir().unwrap();
        let log = RunLog::create(dir.path(), "run");
        incomplete_bundle(
            &selection_for(bundler, dir.path()),
            "run",
            "2026-01-01T00:00:00Z",
            &log,
            false,
            failed_run_engine_identity(bundler, engine, allow_unsupported_engine),
            error,
        )
    }

    #[test]
    fn failed_runs_record_the_detected_engine_matrix_version_as_supported() {
        for bundler in [BundlerChoice::Vite, BundlerChoice::Rspack] {
            let spec = bundler.spec();
            let engine = detected(spec.package, spec.version);
            for allow in [false, true] {
                let (bundle, error) = failed_bundle(
                    bundler,
                    Some(&engine),
                    allow,
                    BwError::new(ErrorCode::BuildFailed, "boom", "Fix it."),
                );
                let identity = bundle.analysis.engine.unwrap();
                assert_eq!(identity.bundler_package, spec.package);
                assert_eq!(identity.bundler_version, spec.version);
                assert_eq!(identity.support, crate::model::EngineSupport::Supported);
                assert_eq!(bundle.analysis.status, RunStatus::Failed);
                assert_eq!(error.unwrap().code, ErrorCode::BuildFailed);
            }
        }
    }

    #[test]
    fn failed_runs_select_an_unsupported_engine_only_when_explicitly_allowed() {
        let engine = detected("vite", "8.0.0");
        let (allowed, _) = failed_bundle(
            BundlerChoice::Vite,
            Some(&engine),
            true,
            BwError::new(ErrorCode::BuildFailed, "boom", "Fix it."),
        );
        let identity = allowed.analysis.engine.unwrap();
        assert_eq!(identity.bundler_version, "8.0.0");
        assert_eq!(
            identity.support,
            crate::model::EngineSupport::UnsupportedSelected
        );

        // The adapter reports the engine and then rejects the version when the
        // flag is absent; the version is known but nothing was selected.
        let (rejected, _) = failed_bundle(
            BundlerChoice::Vite,
            Some(&engine),
            false,
            BwError::new(ErrorCode::EngineVersionUnsupported, "unsupported", "Retry."),
        );
        let identity = rejected.analysis.engine.unwrap();
        assert_eq!(identity.bundler_package, "vite");
        assert_eq!(identity.bundler_version, "8.0.0");
        assert_eq!(identity.support, crate::model::EngineSupport::Unspecified);
    }

    #[test]
    fn failed_runs_never_treat_a_padded_version_as_the_matrix_version() {
        use crate::model::EngineSupport;
        for bundler in [BundlerChoice::Vite, BundlerChoice::Rspack] {
            let spec = bundler.spec();
            for padded in [
                format!(" {}", spec.version),
                format!("{} ", spec.version),
                format!(" {} ", spec.version),
            ] {
                let engine = detected(spec.package, &padded);
                for (allow, expected) in [
                    (false, EngineSupport::Unspecified),
                    (true, EngineSupport::UnsupportedSelected),
                ] {
                    let identity = failed_run_engine_identity(bundler, Some(&engine), allow);
                    // The reported value is preserved exactly.
                    assert_eq!(identity.bundler_version, padded);
                    assert_eq!(identity.support, expected, "{padded:?} allow={allow}");
                }
            }
        }
    }

    #[test]
    fn failed_runs_without_an_engine_event_keep_an_unknown_version() {
        let (bundle, _) = failed_bundle(
            BundlerChoice::Rspack,
            None,
            true,
            BwError::new(ErrorCode::BundlerNotInstalled, "missing", "Install it."),
        );
        let identity = bundle.analysis.engine.unwrap();
        assert_eq!(identity.bundler_package, "@rspack/core");
        assert_eq!(identity.bundler_version, "unknown");
        assert_eq!(identity.support, crate::model::EngineSupport::Unspecified);
    }

    #[test]
    fn cancelled_runs_keep_the_detected_engine_and_invent_nothing() {
        let engine = detected("@rspack/core", "2.2.9");
        let (bundle, error) = failed_bundle(
            BundlerChoice::Rspack,
            Some(&engine),
            false,
            adapter::cancelled_error(),
        );
        assert_eq!(bundle.analysis.status, RunStatus::Cancelled);
        assert_eq!(error.unwrap().code, ErrorCode::RunCancelled);
        let identity = bundle.analysis.engine.unwrap();
        assert_eq!(identity.bundler_version, "2.2.9");
        assert_eq!(identity.support, crate::model::EngineSupport::Supported);
        assert!(identity.minifier.is_empty());
        assert!(identity.adapter_id.is_empty());
        assert!(bundle.analysis.diagnostics.is_empty());
        assert_eq!(bundle.analysis.problems.len(), 1);
        assert!(!bundle.analysis.coverage.is_empty());
        assert!(
            bundle
                .analysis
                .coverage
                .iter()
                .all(|coverage| coverage.state == crate::model::CoverageState::Unavailable)
        );
    }
}
