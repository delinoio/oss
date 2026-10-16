//! Turns an adapter collection into an `Analysis` plus stored source files.

use std::{
    collections::HashSet,
    path::{Path, PathBuf},
};

use crate::{
    chunks::{Chunk, measure},
    collect::{CollectedModule, Collection},
    engine::BundlerChoice,
    error::{ErrorCode, problem},
    model::{
        Analysis, CategoryCoverage, ConfigEntry, CoverageState, EngineIdentity, EngineSupport,
        OptimizationCategory, Problem, ProblemSeverity, ReportMode, RunStatus, SCHEMA_VERSION,
        SourceFile,
    },
    rules::{self, Context},
    sources::{AssetExemption, SourceStore, classify, freshness_unverified_problem},
};

pub struct AnalysisInput<'a> {
    pub run_id: String,
    pub project_root: PathBuf,
    pub project_name: String,
    pub bundler: BundlerChoice,
    pub collection: &'a Collection,
    pub build_dir: PathBuf,
    pub include_sources: bool,
    pub started_at: String,
    /// Called once before chunk size measurement starts, so callers can
    /// publish the `MEASURING` stage and time it separately.
    pub on_measuring: Option<&'a (dyn Fn() + Sync)>,
    /// Polled between chunks during measurement; true stops early. The caller
    /// then discards the partial output and saves a cancelled result.
    pub cancelled: Option<&'a (dyn Fn() -> bool + Sync)>,
}

pub struct AnalysisOutput {
    pub analysis: Analysis,
    pub sources: Vec<SourceFile>,
    /// Source ID to project-relative path for the sources whose live file can
    /// be re-hashed when a saved bundle is reopened (text sources only).
    pub project_relative: std::collections::BTreeMap<String, String>,
    /// Source ID to project-relative path for the sources whose live file is
    /// only checked for existence when a saved bundle is reopened (generated
    /// asset modules, binary files and derived modules).
    pub existence_relative: std::collections::BTreeMap<String, String>,
}

/// Returns the root that owns collected sources and their saved freshness
/// paths. Vite's resolved root identifies source ownership; Rspack's context
/// only resolves relative config and source-map paths, so it must not move the
/// ownership boundary for files imported from outside that context.
pub fn effective_root(collection: &Collection, selected_root: &Path) -> PathBuf {
    if collection.bundler != "vite" {
        return selected_root.to_path_buf();
    }
    let configured_root = context_root(collection, selected_root);
    if configured_root.starts_with(selected_root) {
        selected_root.to_path_buf()
    } else {
        nearest_package_root(&configured_root).unwrap_or(configured_root)
    }
}

/// Expands an external Vite subroot to the nearest package boundary so files
/// imported from the rest of that package remain project sources and keep
/// their freshness paths when a saved run is reopened.
fn nearest_package_root(root: &Path) -> Option<PathBuf> {
    let mut directory = Some(root);
    while let Some(current) = directory {
        let manifest = current.join("package.json");
        let named = std::fs::read(&manifest)
            .ok()
            .and_then(|bytes| serde_json::from_slice::<serde_json::Value>(&bytes).ok())
            .and_then(|manifest| {
                manifest
                    .get("name")
                    .and_then(serde_json::Value::as_str)
                    .map(|name| !name.is_empty())
            })
            .unwrap_or(false);
        if named {
            return Some(current.to_path_buf());
        }
        let parent = current.parent();
        directory = parent.filter(|parent| *parent != current);
    }
    None
}

/// Returns the bundler context used to resolve source-map paths. This remains
/// distinct from `effective_root` because Rspack permits imports outside its
/// context while still resolving relative source-map entries from it.
pub fn context_root(collection: &Collection, selected_root: &Path) -> PathBuf {
    collection.context.as_deref().map_or_else(
        || selected_root.to_path_buf(),
        |context| {
            let context = PathBuf::from(context);
            if context.is_absolute() {
                context
            } else {
                selected_root.join(context)
            }
        },
    )
}

pub fn engine_identity(bundler: BundlerChoice, collection: &Collection) -> EngineIdentity {
    let spec = bundler.spec();
    let support = if spec.is_matrix_version(&collection.bundler_version) {
        EngineSupport::Supported
    } else {
        EngineSupport::UnsupportedSelected
    };
    EngineIdentity {
        bundler: bundler.bundler(),
        bundler_package: collection.bundler_package.clone(),
        bundler_version: collection.bundler_version.clone(),
        minifier: collection.minifier.clone(),
        minifier_version: collection.minifier_version.clone(),
        default_minifier: collection.default_minifier,
        adapter_id: collection.adapter_id.clone(),
        adapter_version: collection.adapter_version.clone(),
        support,
        supported_versions: format!("{} {}", spec.package, spec.version),
        diagnostic_engine: true,
        diagnostic_notice: format!(
            "Findings describe a separate diagnostic build of this project with {} {} and \
             bundlewhy collection overrides. Output and behavior are not guaranteed to match your \
             original build.",
            collection.bundler_package, collection.bundler_version
        ),
        overrides: collection.overrides.clone(),
    }
}

#[allow(clippy::too_many_arguments)]
fn coverage(
    collection: &Collection,
    missing_maps: usize,
    failed_chunks: usize,
    unreadable_sources: usize,
    unverified_sources: usize,
    vite_env_source_gaps: usize,
    vite_env_unmapped_candidates: usize,
    unsupported_engine: Option<&str>,
) -> Vec<CategoryCoverage> {
    let rules = rules::rules_by_category(&collection.bundler);
    let source_gaps = unreadable_sources.saturating_add(unverified_sources);
    OptimizationCategory::ALL
        .into_iter()
        .map(|category| {
            let rule_ids: Vec<String> = rules
                .get(&category)
                .map(|ids| ids.iter().map(|id| id.as_str().to_owned()).collect())
                .unwrap_or_default();
            let (state, note) = match category {
                OptimizationCategory::TreeShaking
                    if collection.bundler == "vite"
                        && rules::vite::tree_shaking_disabled(collection) =>
                {
                    (
                        CoverageState::NotApplicable,
                        "Tree shaking is disabled by build.rolldownOptions.treeshake: false."
                            .to_owned(),
                    )
                }
                OptimizationCategory::TreeShaking
                    if collection.bundler == "rspack"
                        && rules::rspack::used_exports_disabled(collection) =>
                {
                    (
                        CoverageState::NotApplicable,
                        format!(
                            "Tree-shaking analysis is disabled by {}.",
                            rules::rspack::disabled_tree_shaking_options(collection)
                                .iter()
                                .map(|option| format!("{option}: false"))
                                .collect::<Vec<_>>()
                                .join(", ")
                        ),
                    )
                }
                OptimizationCategory::ModuleConcatenation
                    if collection.bundler == "rspack"
                        && rules::rspack::concatenation_disabled(collection) =>
                {
                    (
                        CoverageState::NotApplicable,
                        "Module concatenation is disabled by optimization.concatenateModules: \
                         false."
                            .to_owned(),
                    )
                }
                OptimizationCategory::Inlining | OptimizationCategory::ConstantFolding => (
                    CoverageState::Unavailable,
                    "The engine does not expose these decisions and bundlewhy has no verified \
                     detector. Absence of findings is not evidence."
                        .to_owned(),
                ),
                _ if collection.modules.is_empty() => (
                    CoverageState::Unavailable,
                    "No module data was collected.".to_owned(),
                ),
                OptimizationCategory::DeadCodeElimination | OptimizationCategory::NameMangling
                    if !collection.minify_enabled =>
                {
                    (
                        CoverageState::NotApplicable,
                        "Minification is disabled, so the minifier's dead-code elimination and \
                         name mangling do not run."
                            .to_owned(),
                    )
                }
                OptimizationCategory::DeadCodeElimination | OptimizationCategory::NameMangling
                    if !collection.default_minifier =>
                {
                    (
                        CoverageState::Unavailable,
                        format!(
                            "Detectors are verified only for the default minifier; this build \
                             uses {}.",
                            collection.minifier
                        ),
                    )
                }
                // Vite's generated env expression needs confirmation from the
                // build's original source; Rspack's detector does not.
                OptimizationCategory::DeadCodeElimination
                    if collection.bundler == "vite"
                        && (vite_env_source_gaps > 0 || vite_env_unmapped_candidates > 0) =>
                {
                    let mut notes: Vec<String> = Vec::new();
                    if vite_env_source_gaps > 0 {
                        notes.push(format!(
                            "{vite_env_source_gaps} source file(s) lack verifiable build text; \
                             Vite environment-variable findings in them may be missing."
                        ));
                    }
                    if vite_env_unmapped_candidates > 0 {
                        notes.push(format!(
                            "{vite_env_unmapped_candidates} guarded environment candidate(s) have \
                             no source mapping; Vite environment-variable findings for them may \
                             be missing."
                        ));
                    }
                    (CoverageState::Partial, notes.join(" "))
                }
                // The direct-eval detector also reads original source text.
                OptimizationCategory::NameMangling if source_gaps > 0 => (
                    CoverageState::Partial,
                    format!(
                        "{source_gaps} source file(s) lack verifiable build text; direct-eval \
                         findings in them may be missing."
                    ),
                ),
                // Both categories scan generated chunk text; a chunk that
                // could not be read leaves part of the output unscanned.
                OptimizationCategory::DeadCodeElimination | OptimizationCategory::NameMangling
                    if failed_chunks > 0 =>
                {
                    (
                        CoverageState::Partial,
                        format!(
                            "{failed_chunks} emitted chunk(s) could not be read and were not \
                             scanned; findings in them are missing."
                        ),
                    )
                }
                OptimizationCategory::DeadCodeElimination | OptimizationCategory::NameMangling
                    if missing_maps > 0 =>
                {
                    (
                        CoverageState::Partial,
                        format!(
                            "{missing_maps} chunk(s) have no usable source map; their findings \
                             use generated locations and some may be skipped."
                        ),
                    )
                }
                // Detectors and evidence interpretation are verified only for
                // the frozen matrix; an explicitly selected other version can
                // change APIs or stats silently.
                _ if unsupported_engine.is_some() => (
                    CoverageState::Partial,
                    format!(
                        "Detectors are verified for {}; results from this unsupported engine may \
                         be incomplete.",
                        unsupported_engine.unwrap_or_default()
                    ),
                ),
                _ => (CoverageState::Covered, String::new()),
            };
            CategoryCoverage {
                category,
                state,
                note,
                rules: rule_ids,
            }
        })
        .collect()
}

/// Backing paths whose every collected module the bundler classified as an
/// asset module (for example Rspack `asset/resource`).
///
/// Adapters strip loader prefixes and queries from module identifiers, so
/// distinct modules (an `asset/*` rule and an executable loader or query
/// variant of the same resource) can share one backing path. Source evidence is
/// recorded per path, so a path is exempt only when no collected module with
/// that path can emit executable code; one non-asset module keeps the gap.
fn bundler_asset_paths(modules: &[CollectedModule]) -> HashSet<PathBuf> {
    let mut asset_paths = HashSet::new();
    let mut executable_paths = HashSet::new();
    for module in modules {
        let Some(path) = module.path.as_deref() else {
            continue;
        };
        let path = crate::sources::normalize(Path::new(path));
        let is_asset = module
            .module_type
            .as_deref()
            .is_some_and(|module_type| module_type == "asset" || module_type.starts_with("asset/"));
        if is_asset {
            asset_paths.insert(path);
        } else {
            executable_paths.insert(path);
        }
    }
    asset_paths.retain(|path| !executable_paths.contains(path));
    if !asset_paths.is_empty() {
        tracing::debug!(
            asset_paths = asset_paths.len(),
            shared_with_executable = executable_paths.len(),
            "classified bundler asset module paths"
        );
    }
    asset_paths
}

pub fn analyze(input: &AnalysisInput<'_>) -> AnalysisOutput {
    let collection = input.collection;
    let ownership_root = effective_root(collection, &input.project_root);
    let context_dir = context_root(collection, &input.project_root);
    let mut problems: Vec<Problem> = Vec::new();
    let mut chunks = Vec::new();
    let mut failed_chunks = 0usize;
    for chunk in &collection.chunks {
        match Chunk::load(
            &input.build_dir,
            &chunk.file,
            chunk.map.as_deref(),
            chunk.is_entry,
            &context_dir,
        ) {
            Ok(loaded) => chunks.push(loaded),
            Err(error) => {
                failed_chunks += 1;
                problems.push(problem(
                    ErrorCode::CollectionFailed,
                    ProblemSeverity::Warning,
                    format!("Chunk {} could not be read: {error}", chunk.file),
                    "Start a new run; do not modify the run directory while it is being analyzed.",
                ));
            }
        }
    }
    // Chunks made only of bundler-owned virtual helpers have no source-backed
    // candidate a map could attribute; they are neither a problem nor a
    // coverage penalty. Their generated-location findings stay available.
    let mut missing_maps: Vec<String> = Vec::new();
    for chunk in chunks.iter().filter(|chunk| chunk.map.is_none()) {
        if collection.is_virtual_only_chunk(&chunk.file) {
            tracing::debug!(
                chunk = chunk.file.as_str(),
                "virtual-only helper chunk has no source map; not counted as missing"
            );
        } else {
            missing_maps.push(chunk.file.clone());
        }
    }
    if !missing_maps.is_empty() {
        problems.push(problem(
            ErrorCode::SourceMapMissing,
            ProblemSeverity::Warning,
            format!("No usable source map for: {}", missing_maps.join(", ")),
            "Findings in these chunks use generated locations. Check plugins that disable or \
             rewrite source maps.",
        ));
    }
    // With minification off no minifier ran; only the disabled condition
    // applies.
    if collection.minify_enabled && !collection.default_minifier {
        problems.push(problem(
            ErrorCode::MinifierNonDefault,
            ProblemSeverity::Warning,
            format!(
                "The build uses {} instead of the default minifier",
                collection.minifier
            ),
            "Minifier detectors are verified only for the default minifier; their categories are \
             marked unavailable.",
        ));
    }
    if !collection.minify_enabled {
        problems.push(problem(
            ErrorCode::MinificationDisabled,
            ProblemSeverity::Warning,
            "JavaScript minification is disabled in the selected configuration",
            "Enable minification for production builds to analyze minifier optimizations.",
        ));
    }

    // The Vite URL-module rule for `.svg` and query imports is engine
    // specific: other engines run such files through loaders that can emit
    // executable code, so they only get positive evidence from the engine.
    let mut store = SourceStore::new(input.include_sources).with_asset_exemption(
        if collection.bundler == "vite" {
            AssetExemption::ViteUrlModules
        } else {
            AssetExemption::None
        },
    );
    store.asset_modules = bundler_asset_paths(&collection.modules);
    store.allowed = collection
        .modules
        .iter()
        .filter_map(|module| module.path.as_deref())
        .map(|path| crate::sources::normalize(Path::new(path)))
        .collect();
    for chunk in &chunks {
        let Some(map) = chunk.map.as_ref() else {
            continue;
        };
        for (index, path) in chunk.map_sources.iter().enumerate() {
            let content = u32::try_from(index)
                .ok()
                .and_then(|index| map.get_source_contents(index));
            if let (Some(path), Some(content)) = (path, content) {
                store
                    .snapshots
                    .entry(path.clone())
                    .or_insert_with(|| content.to_owned());
            }
        }
    }
    let mut context = Context::with_context(
        collection,
        ownership_root.clone(),
        context_dir.clone(),
        input.project_name.clone(),
        chunks,
        store,
    );
    context.cancelled = input.cancelled;
    let mut diagnostics = rules::run(&mut context);
    if !input.include_sources {
        // Snippets are source excerpts; excluding sources excludes them too.
        for diagnostic in &mut diagnostics {
            diagnostic.snippet = None;
        }
    }
    let root = ownership_root.clone();
    let display = |path: &Path| classify(&root, path).display;
    if let Some(on_measuring) = input.on_measuring {
        on_measuring();
    }
    // Poll cancellation before each chunk: Brotli quality 11 is the most
    // expensive step, and a cancelled run discards this output anyway.
    let chunk_sizes = context
        .chunks
        .iter()
        .take_while(|_| !input.cancelled.is_some_and(|cancelled| cancelled()))
        .map(|chunk| measure(chunk, display))
        .collect();
    let unreadable_sources = context.sources.unreadable.len();
    let unverified_sources = context.sources.unverified.len();
    let vite_env_source_gaps = context.vite_env_source_gaps.len();
    let vite_env_unmapped_candidates = context.vite_env_unmapped_candidates.len();
    if !context.sources.changed_since_build.is_empty() {
        problems.push(problem(
            ErrorCode::SourceStale,
            ProblemSeverity::Warning,
            format!(
                "{} source file(s) changed after the build read them",
                context.sources.changed_since_build.len()
            ),
            "Results use the source text from the build's source maps. Start a new run to analyze \
             the current code.",
        ));
    }
    // Freshness-unverified sources keep a usable build snapshot, so this
    // warning is informational and never makes the run partial.
    problems.extend(freshness_unverified_problem(
        context.sources.freshness_unverified.len(),
        context.sources.existence_unverified.len(),
    ));
    if !context.sources.unreadable.is_empty() {
        problems.push(problem(
            ErrorCode::SourceUnreadable,
            ProblemSeverity::Warning,
            format!(
                "{} source file(s) could not be read",
                context.sources.unreadable.len()
            ),
            "Locations for these files are shown without source content.",
        ));
    }
    if unverified_sources > 0 {
        problems.push(problem(
            ErrorCode::SourceUnverified,
            ProblemSeverity::Warning,
            format!("{unverified_sources} source file(s) have no build-time text snapshot"),
            "Their live text cannot verify the revision analyzed by the build. Use source maps \
             with sourcesContent or start a new run after checking build configuration.",
        ));
    }
    let project_relative = context.sources.project_relative.clone();
    let existence_relative = context.sources.existence_relative.clone();
    let sources = context.sources.into_files();
    let partial = problems.iter().any(|entry| {
        entry.code == ErrorCode::SourceMapMissing.as_str()
            || entry.code == ErrorCode::CollectionFailed.as_str()
            || entry.code == ErrorCode::SourceUnreadable.as_str()
            || entry.code == ErrorCode::SourceUnverified.as_str()
    });
    let mut config_summary: Vec<ConfigEntry> = collection
        .config_summary
        .iter()
        .map(|(key, value)| ConfigEntry {
            key: key.clone(),
            value: value.clone(),
        })
        .collect();
    config_summary.sort_by(|left, right| left.key.cmp(&right.key));
    let analysis = Analysis {
        schema_version: SCHEMA_VERSION,
        run_id: input.run_id.clone(),
        project_name: input.project_name.clone(),
        config_file: collection
            .config_file
            .as_deref()
            .map(|file| classify(&ownership_root, Path::new(file)).display)
            .unwrap_or_default(),
        status: if partial {
            RunStatus::Partial
        } else {
            RunStatus::Complete
        },
        engine: Some(engine_identity(input.bundler, collection)),
        diagnostics,
        coverage: coverage(
            collection,
            missing_maps.len(),
            failed_chunks,
            unreadable_sources,
            unverified_sources,
            vite_env_source_gaps,
            vite_env_unmapped_candidates,
            (!input
                .bundler
                .spec()
                .is_matrix_version(&collection.bundler_version))
            .then(|| {
                format!(
                    "{} {}",
                    input.bundler.spec().package,
                    input.bundler.spec().version
                )
            })
            .as_deref(),
        ),
        chunks: chunk_sizes,
        problems,
        config_summary,
        sources: sources.iter().map(SourceFile::summary).collect(),
        started_at: input.started_at.clone(),
        finished_at: String::new(),
        duration_ms: 0,
        mode: ReportMode::Live,
        tool_version: env!("CARGO_PKG_VERSION").to_owned(),
    };
    AnalysisOutput {
        analysis,
        sources,
        project_relative,
        existence_relative,
    }
}

#[cfg(test)]
mod tests {
    use sourcemap::SourceMap;

    use super::*;
    use crate::model::{ConditionOutcome, Diagnostic, EvidenceStatus, SourceRevisionEvidence};

    const PROJECT_PLACEHOLDER: &str = "/__bundlewhy_project__";
    const BUILD_PLACEHOLDER: &str = "/__bundlewhy_build__";

    struct Fixture {
        _dir: tempfile::TempDir,
        project: PathBuf,
        collection: Collection,
        output: AnalysisOutput,
    }

    fn manifest_dir() -> PathBuf {
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
    }

    /// Relocates a committed fixture collection and build into a temp dir.
    fn run_fixture(bundler: BundlerChoice, edit: impl Fn(&mut serde_json::Value)) -> Fixture {
        run_fixture_with(bundler, true, edit)
    }

    fn run_fixture_with(
        bundler: BundlerChoice,
        include_sources: bool,
        edit: impl Fn(&mut serde_json::Value),
    ) -> Fixture {
        run_fixture_patched(bundler, include_sources, edit, |_, _| {})
    }

    /// Like `run_fixture_with`, with a hook that may rewrite the copied build
    /// directory. It receives the build directory and the project path.
    fn run_fixture_patched(
        bundler: BundlerChoice,
        include_sources: bool,
        edit: impl Fn(&mut serde_json::Value),
        patch_build: impl Fn(&Path, &str),
    ) -> Fixture {
        let fixture_root = manifest_dir().join("tests/fixtures").join(bundler.as_str());
        let project = manifest_dir()
            .join("../../packages/bundlewhy/fixtures")
            .join(format!("{}-app", bundler.as_str()));
        let project = std::fs::canonicalize(project).unwrap();
        let project_text = project.to_string_lossy().replace('\\', "/");
        let dir = tempfile::tempdir().unwrap();
        let build = dir.path().join("build");
        let build_text = build.to_string_lossy().replace('\\', "/");
        let relocate = |text: &str| {
            text.replace(PROJECT_PLACEHOLDER, &project_text)
                .replace(BUILD_PLACEHOLDER, &build_text)
        };
        for entry in walk(&fixture_root.join("build")) {
            let relative = entry.strip_prefix(fixture_root.join("build")).unwrap();
            let target = build.join(relative);
            std::fs::create_dir_all(target.parent().unwrap()).unwrap();
            std::fs::write(&target, relocate(&std::fs::read_to_string(&entry).unwrap())).unwrap();
        }
        patch_build(&build, &project_text);
        let mut value: serde_json::Value = serde_json::from_str(&relocate(
            &std::fs::read_to_string(fixture_root.join("collect.json")).unwrap(),
        ))
        .unwrap();
        edit(&mut value);
        let collection: Collection = serde_json::from_value(value).unwrap();
        let output = analyze(&AnalysisInput {
            run_id: "test-run".to_owned(),
            project_root: project.clone(),
            project_name: format!("bundlewhy-fixture-{}-app", bundler.as_str()),
            bundler,
            collection: &collection,
            build_dir: build,
            include_sources,
            started_at: String::new(),
            on_measuring: None,
            cancelled: None,
        });
        Fixture {
            _dir: dir,
            project,
            collection,
            output,
        }
    }

    fn vite_without_main_snapshot(
        unreadable: bool,
        edit: impl Fn(&mut serde_json::Value),
    ) -> Fixture {
        run_fixture_patched(BundlerChoice::Vite, true, edit, |build, project| {
            let map_path = build.join("assets/index-BHjrzQLH.js.map");
            let mut map: serde_json::Value =
                serde_json::from_slice(&std::fs::read(&map_path).unwrap()).unwrap();
            let source_index = map["sources"]
                .as_array()
                .unwrap()
                .iter()
                .position(|source| {
                    source
                        .as_str()
                        .is_some_and(|path| path.ends_with("/src/main.js"))
                })
                .unwrap();
            map["sourcesContent"][source_index] = serde_json::Value::Null;
            if unreadable {
                map["sources"][source_index] =
                    format!("{project}/src/__bundlewhy_missing__.js").into();
            }
            std::fs::write(&map_path, serde_json::to_vec(&map).unwrap()).unwrap();
        })
    }

    fn dead_code_coverage(analysis: &Analysis) -> &CategoryCoverage {
        analysis
            .coverage
            .iter()
            .find(|entry| entry.category == OptimizationCategory::DeadCodeElimination)
            .unwrap()
    }

    #[test]
    fn source_provenance_vite_unreadable_mapping_keeps_coverage_partial() {
        let fixture = vite_without_main_snapshot(true, |_| {});
        let analysis = &fixture.output.analysis;
        assert_eq!(analysis.status, RunStatus::Partial);
        assert_eq!(
            analysis
                .problems
                .iter()
                .filter(|entry| entry.code == "BW_SOURCE_UNREADABLE")
                .count(),
            1
        );
        assert!(
            !analysis
                .diagnostics
                .iter()
                .any(|entry| entry.rule == "minifier/unresolved-env-condition")
        );
        assert_eq!(dead_code_coverage(analysis).state, CoverageState::Partial);
        assert!(
            dead_code_coverage(analysis)
                .note
                .contains("environment-variable")
        );
    }

    #[test]
    fn source_provenance_vite_live_text_is_not_a_build_snapshot() {
        let verified = run_fixture(BundlerChoice::Vite, |_| {});
        assert!(
            verified
                .output
                .analysis
                .diagnostics
                .iter()
                .any(|entry| entry.rule == "minifier/unresolved-env-condition")
        );
        assert_eq!(
            dead_code_coverage(&verified.output.analysis).state,
            CoverageState::Covered
        );

        let unverified = vite_without_main_snapshot(false, |_| {});
        let analysis = &unverified.output.analysis;
        assert_eq!(analysis.status, RunStatus::Partial);
        assert!(
            analysis
                .problems
                .iter()
                .any(|entry| entry.code == "BW_SOURCE_UNVERIFIED")
        );
        assert_eq!(dead_code_coverage(analysis).state, CoverageState::Partial);
        assert!(
            !analysis
                .diagnostics
                .iter()
                .any(|entry| entry.rule == "minifier/unresolved-env-condition")
        );
        let main = unverified
            .output
            .sources
            .iter()
            .find(|source| source.path == "src/main.js")
            .unwrap();
        assert_eq!(main.revision_evidence, SourceRevisionEvidence::Unavailable);
        assert!(main.content.is_empty());
        assert!(main.sha256.is_empty());
    }

    #[test]
    fn source_provenance_minifier_precedence_and_rspack_scope() {
        for (edit, expected) in [
            (false, CoverageState::NotApplicable),
            (true, CoverageState::Unavailable),
        ] {
            let fixture = vite_without_main_snapshot(true, |value| {
                if edit {
                    value["defaultMinifier"] = false.into();
                } else {
                    value["minifyEnabled"] = false.into();
                }
            });
            assert_eq!(dead_code_coverage(&fixture.output.analysis).state, expected);
        }

        let value: serde_json::Value = serde_json::from_slice(
            &std::fs::read(manifest_dir().join("tests/fixtures/rspack/collect.json")).unwrap(),
        )
        .unwrap();
        let collection: Collection = serde_json::from_value(value).unwrap();
        let rspack = coverage(&collection, 0, 0, 1, 0, 0, 0, None);
        assert_eq!(
            rspack
                .iter()
                .find(|entry| entry.category == OptimizationCategory::DeadCodeElimination)
                .unwrap()
                .state,
            CoverageState::Covered
        );
    }

    #[test]
    fn vite_dead_code_coverage_ignores_unrelated_source_gaps() {
        let value: serde_json::Value = serde_json::from_slice(
            &std::fs::read(manifest_dir().join("tests/fixtures/vite/collect.json")).unwrap(),
        )
        .unwrap();
        let collection: Collection = serde_json::from_value(value).unwrap();
        let unrelated = coverage(&collection, 0, 0, 0, 1, 0, 0, None);
        assert_eq!(
            unrelated
                .iter()
                .find(|entry| entry.category == OptimizationCategory::DeadCodeElimination)
                .unwrap()
                .state,
            CoverageState::Covered
        );
        let relevant = coverage(&collection, 0, 0, 0, 1, 1, 0, None);
        assert_eq!(
            relevant
                .iter()
                .find(|entry| entry.category == OptimizationCategory::DeadCodeElimination)
                .unwrap()
                .state,
            CoverageState::Partial
        );
    }

    const VITE_INDEX_CHUNK: &str = "assets/index-BHjrzQLH.js";
    const GUARDED_FLAG_READ: &str =
        "({}).BUNDLEWHY_FIXTURE_FLAG===`on`&&console.log(`flag branch`)";
    const VALUE_ONLY_FLAG_READ: &str = "console.log(({}).BUNDLEWHY_FIXTURE_FLAG)";

    /// Where a generated-only segment replaces the source-bearing mapping.
    #[derive(Clone, Copy)]
    enum UnmappedAt {
        /// The `{}` replacement of the flag read.
        FlagRead,
        /// Fifty columns before the flag read, leaving its own segment intact.
        BeforeFlagRead,
        /// No segment starts on the flag's generated line at or before the
        /// flag read, while an earlier generated line ends in a source-bearing
        /// segment.
        AfterEarlierLineMapping,
    }

    /// Rewrites the Vite index chunk map so one position lies in a segment
    /// with no source while every other segment keeps its mapping. The
    /// optional text edit replaces the guarded flag read in the chunk first.
    fn unmap_flag_segment(build: &Path, replacement: Option<&str>, at: UnmappedAt) {
        let chunk_path = build.join(VITE_INDEX_CHUNK);
        let mut code = std::fs::read_to_string(&chunk_path).unwrap();
        if let Some(replacement) = replacement {
            assert!(code.contains(GUARDED_FLAG_READ));
            code = code.replace(GUARDED_FLAG_READ, replacement);
            std::fs::write(&chunk_path, &code).unwrap();
        }
        let flag_offset = code.find("({}).BUNDLEWHY_FIXTURE_FLAG").unwrap();
        // The opening parenthesis belongs to the previous segment.
        let replacement_offset = flag_offset + 1;
        let line = u32::try_from(code[..replacement_offset].matches('\n').count()).unwrap();
        let line_start = code[..replacement_offset].rfind('\n').map_or(0, |i| i + 1);
        let column = u32::try_from(replacement_offset - line_start).unwrap();
        let (gap_column, drop_existing) = match at {
            UnmappedAt::FlagRead => (column, true),
            UnmappedAt::BeforeFlagRead => (column - 50, false),
            UnmappedAt::AfterEarlierLineMapping => (column, true),
        };
        let earlier_line = matches!(at, UnmappedAt::AfterEarlierLineMapping);
        assert!(
            !earlier_line || line > 0,
            "an earlier generated line must exist"
        );

        let map_path = build.join(format!("{VITE_INDEX_CHUNK}.map"));
        let map = SourceMap::from_slice(&std::fs::read(&map_path).unwrap()).unwrap();
        let mut builder = sourcemap::SourceMapBuilder::new(map.get_file());
        for id in 0..map.get_source_count() {
            let source = builder.add_source(map.get_source(id).unwrap());
            builder.set_source_contents(source, map.get_source_contents(id));
        }
        for id in 0..map.get_name_count() {
            builder.add_name(map.get_name(id).unwrap());
        }
        let mut inserted = false;
        if earlier_line {
            builder.add_raw(0, 0, 0, 0, Some(0), None, false);
            inserted = true;
        }
        for token in map.tokens() {
            let position = (token.get_dst_line(), token.get_dst_col());
            if earlier_line && position.0 == line && position.1 <= gap_column {
                continue;
            }
            if !inserted && position >= (line, gap_column) {
                builder.add_raw(line, gap_column, 0, 0, None, None, false);
                inserted = true;
            }
            if drop_existing && position == (line, gap_column) {
                continue;
            }
            builder.add_raw(
                position.0,
                position.1,
                token.get_src_line(),
                token.get_src_col(),
                token.has_source().then(|| token.get_src_id()),
                token.get_name().map(|_| token.get_raw_token().name_id),
                token.is_range(),
            );
        }
        assert!(inserted, "the generated-only segment must be placed");
        let mut bytes = Vec::new();
        builder.into_sourcemap().to_writer(&mut bytes).unwrap();
        std::fs::write(&map_path, bytes).unwrap();
    }

    fn vite_with_unmapped_flag(
        replacement: Option<&'static str>,
        at: UnmappedAt,
        edit: impl Fn(&mut serde_json::Value),
    ) -> Fixture {
        run_fixture_patched(BundlerChoice::Vite, true, edit, move |build, _| {
            unmap_flag_segment(build, replacement, at);
        })
    }

    fn has_unresolved_env_diagnostic(analysis: &Analysis) -> bool {
        analysis
            .diagnostics
            .iter()
            .any(|entry| entry.rule == "minifier/unresolved-env-condition")
    }

    #[test]
    fn vite_unmapped_guarded_candidate_keeps_coverage_partial() {
        let control = run_fixture(BundlerChoice::Vite, |_| {});
        assert!(has_unresolved_env_diagnostic(&control.output.analysis));
        assert_eq!(
            dead_code_coverage(&control.output.analysis).state,
            CoverageState::Covered
        );

        let fixture = vite_with_unmapped_flag(None, UnmappedAt::FlagRead, |_| {});
        let analysis = &fixture.output.analysis;
        assert!(!has_unresolved_env_diagnostic(analysis));
        let coverage = dead_code_coverage(analysis);
        assert_eq!(coverage.state, CoverageState::Partial);
        assert!(coverage.note.contains("1 guarded environment candidate(s)"));
        assert!(coverage.note.contains("no source mapping"));
        assert!(!coverage.note.contains("lack verifiable build text"));
        // The map stays usable and every readable source keeps its snapshot,
        // so the gap is not reported as a source or map problem.
        assert!(
            !analysis
                .problems
                .iter()
                .any(|entry| entry.code == "BW_SOURCE_UNREADABLE"
                    || entry.code == "BW_SOURCE_UNVERIFIED"
                    || entry.code == "BW_SOURCE_MAP_MISSING")
        );
    }

    #[test]
    fn vite_candidate_is_not_covered_by_a_segment_from_an_earlier_line() {
        let fixture = vite_with_unmapped_flag(None, UnmappedAt::AfterEarlierLineMapping, |_| {});
        let analysis = &fixture.output.analysis;
        // The earlier line's source-bearing segment must not attribute the
        // candidate to that line's source.
        assert!(!has_unresolved_env_diagnostic(analysis));
        let coverage = dead_code_coverage(analysis);
        assert_eq!(coverage.state, CoverageState::Partial);
        assert!(coverage.note.contains("1 guarded environment candidate(s)"));
        assert!(coverage.note.contains("no source mapping"));
    }

    #[test]
    fn vite_unrelated_unmapped_text_is_not_a_coverage_gap() {
        let fixture = vite_with_unmapped_flag(None, UnmappedAt::BeforeFlagRead, |_| {});
        let analysis = &fixture.output.analysis;
        assert!(has_unresolved_env_diagnostic(analysis));
        let coverage = dead_code_coverage(analysis);
        assert_eq!(coverage.state, CoverageState::Covered);
        assert!(coverage.note.is_empty());
    }

    #[test]
    fn vite_unmapped_value_only_read_is_not_a_coverage_gap() {
        let fixture =
            vite_with_unmapped_flag(Some(VALUE_ONLY_FLAG_READ), UnmappedAt::FlagRead, |_| {});
        let analysis = &fixture.output.analysis;
        assert!(!has_unresolved_env_diagnostic(analysis));
        let coverage = dead_code_coverage(analysis);
        assert_eq!(coverage.state, CoverageState::Covered);
        assert!(coverage.note.is_empty());
    }

    #[test]
    fn vite_unmapped_candidate_keeps_minifier_precedence() {
        for (edit, expected) in [
            (false, CoverageState::NotApplicable),
            (true, CoverageState::Unavailable),
        ] {
            let fixture = vite_with_unmapped_flag(None, UnmappedAt::FlagRead, |value| {
                if edit {
                    value["defaultMinifier"] = false.into();
                } else {
                    value["minifyEnabled"] = false.into();
                }
            });
            assert_eq!(dead_code_coverage(&fixture.output.analysis).state, expected);
        }
    }

    #[test]
    fn vite_missing_map_uses_the_missing_map_note_not_the_mapping_gap_note() {
        let fixture = run_fixture_patched(
            BundlerChoice::Vite,
            true,
            |_| {},
            |build, _| {
                std::fs::remove_file(build.join(format!("{VITE_INDEX_CHUNK}.map"))).unwrap();
            },
        );
        let coverage = dead_code_coverage(&fixture.output.analysis);
        assert_eq!(coverage.state, CoverageState::Partial);
        assert!(coverage.note.contains("no usable source map"));
        assert!(!coverage.note.contains("no source mapping"));
    }

    #[test]
    fn vite_coverage_reports_unmapped_candidates_and_source_gaps_together() {
        let value: serde_json::Value = serde_json::from_slice(
            &std::fs::read(manifest_dir().join("tests/fixtures/vite/collect.json")).unwrap(),
        )
        .unwrap();
        let collection: Collection = serde_json::from_value(value).unwrap();
        let find_dead_code = |coverage: Vec<CategoryCoverage>| {
            coverage
                .into_iter()
                .find(|entry| entry.category == OptimizationCategory::DeadCodeElimination)
                .unwrap()
        };
        let both = find_dead_code(coverage(&collection, 0, 0, 0, 1, 1, 2, None));
        assert_eq!(both.state, CoverageState::Partial);
        assert!(
            both.note
                .contains("1 source file(s) lack verifiable build text")
        );
        assert!(both.note.contains("2 guarded environment candidate(s)"));
        let only_source = find_dead_code(coverage(&collection, 0, 0, 0, 1, 1, 0, None));
        assert!(!only_source.note.contains("no source mapping"));
        // Name mangling does not depend on the Vite environment detector.
        let mangling = coverage(&collection, 0, 0, 0, 0, 0, 2, None)
            .into_iter()
            .find(|entry| entry.category == OptimizationCategory::NameMangling)
            .unwrap();
        assert_eq!(mangling.state, CoverageState::Covered);
    }

    #[test]
    fn rspack_dead_code_coverage_ignores_unmapped_candidates() {
        let value: serde_json::Value = serde_json::from_slice(
            &std::fs::read(manifest_dir().join("tests/fixtures/rspack/collect.json")).unwrap(),
        )
        .unwrap();
        let collection: Collection = serde_json::from_value(value).unwrap();
        let rspack = coverage(&collection, 0, 0, 0, 0, 0, 3, None);
        assert_eq!(
            rspack
                .iter()
                .find(|entry| entry.category == OptimizationCategory::DeadCodeElimination)
                .unwrap()
                .state,
            CoverageState::Covered
        );
    }

    fn walk(dir: &Path) -> Vec<PathBuf> {
        let mut files = Vec::new();
        for entry in std::fs::read_dir(dir).unwrap() {
            let path = entry.unwrap().path();
            if path.is_dir() {
                files.extend(walk(&path));
            } else {
                files.push(path);
            }
        }
        files
    }

    fn find<'a>(diagnostics: &'a [Diagnostic], rule: &str, module: &str) -> Option<&'a Diagnostic> {
        diagnostics
            .iter()
            .find(|diagnostic| diagnostic.rule == rule && diagnostic.module.contains(module))
    }

    fn assert_absent(diagnostics: &[Diagnostic], rule: &str, module: &str) {
        assert!(
            find(diagnostics, rule, module).is_none(),
            "unexpected {rule} for {module}"
        );
    }

    #[test]
    fn rspack_fixture_blockers_and_counterexamples() {
        let fixture = run_fixture(BundlerChoice::Rspack, |_| {});
        let analysis = &fixture.output.analysis;
        let diagnostics = &analysis.diagnostics;
        assert_eq!(
            analysis.status,
            RunStatus::Complete,
            "{:?}",
            analysis.problems
        );

        // Tree shaking: effectful module kept, side-effect-free module removed.
        let retained = find(
            diagnostics,
            "rspack/side-effect-retained",
            "src/effects/register.js",
        )
        .unwrap();
        assert_eq!(retained.status, EvidenceStatus::Confirmed);
        assert_eq!(retained.location.as_ref().unwrap().line, 2);
        assert!(
            retained
                .conditions
                .iter()
                .any(|condition| condition.outcome == ConditionOutcome::Unmet)
        );
        assert_eq!(
            retained.dependency_paths[0].modules,
            vec!["src/main.js", "src/effects/register.js"]
        );
        assert_absent(
            diagnostics,
            "rspack/side-effect-retained",
            "src/pure/index.js",
        );

        // Module concatenation: CommonJS and eval bail out, plain ESM is
        // concatenated.
        let cjs = find(
            diagnostics,
            "rspack/module-concatenation-bailout",
            "src/legacy/cjs.cjs",
        )
        .unwrap();
        assert!(cjs.summary.contains("not an ECMAScript module"));
        assert!(
            find(
                diagnostics,
                "rspack/module-concatenation-bailout",
                "src/dynamic/evaluate.js"
            )
            .is_some()
        );
        assert_absent(
            diagnostics,
            "rspack/module-concatenation-bailout",
            "src/dynamic/plain.js",
        );
        assert_absent(
            diagnostics,
            "rspack/module-concatenation-bailout",
            "src/main.js",
        );
        assert_absent(
            diagnostics,
            "rspack/module-concatenation-bailout",
            "src/esm/value.js",
        );

        // Chunk splitting: shared lazy module is duplicated; single-chunk
        // modules are not.
        let shared = find(
            diagnostics,
            "rspack/module-in-multiple-chunks",
            "src/lazy/common.js",
        )
        .unwrap();
        assert_eq!(shared.status, EvidenceStatus::Confirmed);
        assert_absent(
            diagnostics,
            "rspack/module-in-multiple-chunks",
            "src/lazy/a.js",
        );

        common_assertions(analysis);
    }

    fn path_modules(path: &crate::model::ExportUsagePath) -> Vec<String> {
        let mut names: Vec<String> = path
            .edges
            .iter()
            .map(|edge| edge.consumer.module.clone())
            .collect();
        names.push(path.edges.last().unwrap().provider.module.clone());
        names
    }

    #[test]
    fn rspack_export_usage_follows_the_engine_edges_for_every_shape() {
        use crate::model::{
            ExportScope, ExportUsagePathEnd, ExportUsageState, ExportUsageTruncation,
        };

        let fixture = run_fixture(BundlerChoice::Rspack, |_| {});
        let diagnostics = &fixture.output.analysis.diagnostics;
        let leaf = find(
            diagnostics,
            "rspack/module-concatenation-bailout",
            "src/exports/leaf.js",
        )
        .unwrap();
        let usage = leaf.export_usage.as_ref().unwrap();
        assert_eq!(usage.state, ExportUsageState::Available);
        assert_eq!(usage.truncation, ExportUsageTruncation::None);
        let chains: Vec<Vec<String>> = usage.paths.iter().map(path_modules).collect();
        let expect = |names: &[&str]| -> Vec<String> {
            names.iter().map(|name| (*name).to_owned()).collect()
        };

        // Re-export: Rspack reports the entry's use of `leaf` directly; the
        // re-exporting module has no edge and is not invented.
        assert!(chains.contains(&expect(&["src/main.js", "src/exports/leaf.js"])));
        assert!(
            chains
                .iter()
                .flatten()
                .all(|module| module != "src/exports/reexport.js")
        );
        // Branching: two consumers of `leaf` with the same entry.
        let namespace = usage
            .paths
            .iter()
            .find(|path| path_modules(path).contains(&"src/exports/namespace.js".to_owned()))
            .unwrap();
        assert_eq!(
            path_modules(namespace),
            expect(&[
                "src/main.js",
                "src/exports/namespace.js",
                "src/exports/leaf.js"
            ])
        );
        assert_eq!(namespace.end, ExportUsagePathEnd::Root);
        // Namespace: the provider side has no named export.
        let last = namespace.edges.last().unwrap();
        assert_eq!(last.provider.scope, ExportScope::Module);
        assert!(last.provider.export_path.is_empty());
        // The entry consumes from module scope, not a named export.
        assert_eq!(namespace.edges[0].consumer.scope, ExportScope::Module);
        assert_eq!(namespace.edges[1].consumer.scope, ExportScope::Named);
        // Cycle: leaf and cycle-back use each other.
        let cycle = usage
            .paths
            .iter()
            .find(|path| path.end == ExportUsagePathEnd::Cycle)
            .unwrap();
        assert_eq!(
            path_modules(cycle),
            expect(&[
                "src/exports/leaf.js",
                "src/exports/cycle-back.js",
                "src/exports/leaf.js"
            ])
        );
        // Dependency ID and location come from the engine edge.
        for edge in usage.paths.iter().flat_map(|path| &path.edges) {
            assert!(!edge.dependency_id.is_empty());
            assert!(edge.location.contains(':'));
        }

        // The chain through chain-a / chain-b is reached from cycle-back too.
        let cycle_back = find(
            diagnostics,
            "rspack/module-concatenation-bailout",
            "src/exports/cycle-back.js",
        )
        .unwrap();
        assert!(cycle_back.export_usage.as_ref().unwrap().paths.len() >= 2);

        // Module importer paths are unchanged by the new evidence.
        assert_eq!(
            leaf.dependency_paths[0].modules.first().unwrap(),
            "src/main.js"
        );
    }

    #[test]
    fn rspack_diagnostic_without_edges_reports_unavailable_export_usage() {
        use crate::model::ExportUsageState;

        let fixture = run_fixture(BundlerChoice::Rspack, |_| {});
        let diagnostics = &fixture.output.analysis.diagnostics;
        // Loaded only through import(): the engine reports no edge for it.
        let lazy = find(
            diagnostics,
            "rspack/module-concatenation-bailout",
            "src/lazy/a.js",
        )
        .unwrap();
        let usage = lazy.export_usage.as_ref().unwrap();
        assert_eq!(usage.state, ExportUsageState::Unavailable);
        assert_eq!(usage.note, super::rules::export_usage::NOTE_NO_EDGE);
        assert!(usage.paths.is_empty());
        // Other evidence of the diagnostics is intact: a retained module with
        // no edge keeps its importer path.
        assert!(!lazy.evidence.is_empty());
        let register = find(
            diagnostics,
            "rspack/side-effect-retained",
            "src/effects/register.js",
        )
        .unwrap();
        assert_eq!(
            register.export_usage.as_ref().unwrap().state,
            ExportUsageState::Unavailable
        );
        assert_eq!(register.status, EvidenceStatus::Confirmed);
        assert!(!register.dependency_paths.is_empty());
    }

    #[test]
    fn collections_without_export_usage_keep_their_existing_evidence() {
        use crate::model::ExportUsageState;

        let with_usage = run_fixture(BundlerChoice::Rspack, |_| {});
        let without = run_fixture(BundlerChoice::Rspack, |value| {
            value.as_object_mut().unwrap().remove("exportUsage");
        });
        let strip = |diagnostics: &[Diagnostic]| -> Vec<Diagnostic> {
            diagnostics
                .iter()
                .cloned()
                .map(|mut diagnostic| {
                    diagnostic.export_usage = None;
                    diagnostic
                })
                .collect()
        };
        assert_eq!(
            strip(&with_usage.output.analysis.diagnostics),
            strip(&without.output.analysis.diagnostics)
        );
        for diagnostic in &without.output.analysis.diagnostics {
            if let Some(usage) = &diagnostic.export_usage {
                assert_eq!(usage.state, ExportUsageState::Unavailable);
                assert_eq!(usage.note, super::rules::export_usage::NOTE_NOT_COLLECTED);
            }
        }
    }

    #[test]
    fn unavailable_engine_graph_is_reported_per_diagnostic_and_nothing_else_changes() {
        use crate::model::ExportUsageState;

        let fixture = run_fixture(BundlerChoice::Rspack, |value| {
            value["exportUsage"] = serde_json::json!({
                "available": false,
                "reason": "plugin-api-missing",
                "skippedEdges": 0,
                "edges": []
            });
        });
        let analysis = &fixture.output.analysis;
        assert_eq!(
            analysis.status,
            RunStatus::Complete,
            "{:?}",
            analysis.problems
        );
        let usages: Vec<_> = analysis
            .diagnostics
            .iter()
            .filter_map(|diagnostic| diagnostic.export_usage.as_ref())
            .collect();
        assert!(!usages.is_empty());
        assert!(usages.iter().all(|usage| {
            usage.state == ExportUsageState::Unavailable
                && usage.note
                    == super::rules::export_usage::reason_note(Some(
                        crate::collect::ExportUsageReason::PluginApiMissing,
                    ))
                && usage.paths.is_empty()
        }));
        assert!(
            find(
                &analysis.diagnostics,
                "rspack/side-effect-retained",
                "src/effects/register.js"
            )
            .is_some()
        );
    }

    #[test]
    fn edges_with_empty_segments_never_make_a_saved_result_unreadable() {
        use crate::model::ExportUsageState;

        let fixture = run_fixture(BundlerChoice::Rspack, |value| {
            let usage = value["exportUsage"].as_object_mut().unwrap();
            let edges = usage["edges"].as_array_mut().unwrap();
            let leaf = edges
                .iter()
                .find(|edge| {
                    edge["provider"]["module"]
                        .as_str()
                        .is_some_and(|module| module.ends_with("exports/leaf.js"))
                })
                .unwrap()
                .clone();
            let mut empty_segment = leaf.clone();
            empty_segment["provider"]["export"] = serde_json::json!([""]);
            let mut empty_module = leaf;
            empty_module["consumer"]["module"] = serde_json::json!("");
            edges.push(empty_segment);
            edges.push(empty_module);
        });
        let analysis = &fixture.output.analysis;
        let leaf = find(
            &analysis.diagnostics,
            "rspack/module-concatenation-bailout",
            "src/exports/leaf.js",
        )
        .unwrap();
        let usage = leaf.export_usage.as_ref().unwrap();
        assert_eq!(usage.state, ExportUsageState::Available);
        assert!(!usage.paths.is_empty());
        assert!(
            usage.note.starts_with("2 engine edge(s)"),
            "the dropped edges are disclosed: {}",
            usage.note
        );
        // The saved result reopens: every retained reference is valid.
        let mut saved = analysis.clone();
        saved.run_id = uuid::Uuid::now_v7().to_string();
        let bundle = crate::bundle::ResultBundle {
            schema_version: crate::model::SCHEMA_VERSION,
            analysis: saved,
            sources: fixture.output.sources.clone(),
            project_relative: fixture.output.project_relative.clone(),
            existence_relative: fixture.output.existence_relative.clone(),
            ..crate::bundle::ResultBundle::default()
        };
        crate::bundle::parse(&serde_json::to_vec(&bundle).unwrap()).unwrap();
    }

    #[test]
    fn vite_diagnostics_never_carry_export_usage() {
        let fixture = run_fixture(BundlerChoice::Vite, |_| {});
        assert!(!fixture.output.analysis.diagnostics.is_empty());
        assert!(
            fixture
                .output
                .analysis
                .diagnostics
                .iter()
                .all(|diagnostic| diagnostic.export_usage.is_none())
        );
    }

    /// A snapshot-backed source whose live read fails with an error other than
    /// "not found" must not be reported as changed: the run keeps the snapshot
    /// text, stays complete and carries the freshness-unverified warning.
    #[cfg(unix)]
    #[test]
    fn vite_snapshot_source_with_a_failed_live_read_is_unverified_not_stale() {
        // A path below a regular file fails with a not-a-directory error.
        const BLOCKED_SOURCE: &str = "package.json/blocked.ts";
        let snapshot = "export const blocked = 1;";
        let fixture = run_fixture_patched(
            BundlerChoice::Vite,
            true,
            |value| {
                let modules = value["modules"].as_array_mut().unwrap();
                let mut blocked = modules
                    .iter()
                    .find(|module| {
                        module["path"]
                            .as_str()
                            .is_some_and(|path| path.contains("/src/effects/"))
                    })
                    .unwrap()
                    .clone();
                let project = blocked["packageDir"].as_str().unwrap().to_owned();
                let path = format!("{project}/{BLOCKED_SOURCE}");
                blocked["id"] = path.clone().into();
                blocked["path"] = path.into();
                blocked["isEntry"] = false.into();
                modules.push(blocked);
            },
            |build, project| {
                let map_path = build.join("assets/index-BHjrzQLH.js.map");
                let mut map: serde_json::Value =
                    serde_json::from_str(&std::fs::read_to_string(&map_path).unwrap()).unwrap();
                map["sources"]
                    .as_array_mut()
                    .unwrap()
                    .push(format!("{project}/{BLOCKED_SOURCE}").into());
                map["sourcesContent"]
                    .as_array_mut()
                    .unwrap()
                    .push(snapshot.into());
                std::fs::write(&map_path, serde_json::to_string(&map).unwrap()).unwrap();
            },
        );
        let analysis = &fixture.output.analysis;
        let codes: Vec<&str> = analysis
            .problems
            .iter()
            .map(|entry| entry.code.as_str())
            .collect();
        assert!(!codes.contains(&"BW_SOURCE_STALE"), "{codes:?}");
        assert!(!codes.contains(&"BW_SOURCE_UNREADABLE"), "{codes:?}");
        assert_eq!(
            codes
                .iter()
                .filter(|code| **code == "BW_SOURCE_FRESHNESS_UNVERIFIED")
                .count(),
            1,
            "{codes:?}"
        );
        // The warning is informational: it never makes the run partial.
        assert_eq!(analysis.status, RunStatus::Complete, "{codes:?}");
        let blocked = fixture
            .output
            .sources
            .iter()
            .find(|source| source.path.ends_with(BLOCKED_SOURCE))
            .unwrap();
        assert!(!blocked.stale);
        assert!(blocked.content_included);
        assert_eq!(blocked.content, snapshot);
        assert_eq!(
            blocked.revision_evidence,
            SourceRevisionEvidence::BuildSnapshot
        );
    }

    #[test]
    fn vite_png_asset_stub_is_neither_stale_nor_stored_as_content() {
        const PNG_SOURCE: &str = "src/assets/logo.png";
        let stub = "export default \"__VITE_ASSET__Vx9kQ1__\"";
        let fixture = run_fixture_patched(
            BundlerChoice::Vite,
            true,
            |value| {
                let modules = value["modules"].as_array_mut().unwrap();
                let mut png = modules
                    .iter()
                    .find(|module| {
                        module["path"]
                            .as_str()
                            .is_some_and(|path| path.contains("/src/effects/"))
                    })
                    .unwrap()
                    .clone();
                let project = png["packageDir"].as_str().unwrap().to_owned();
                let path = format!("{project}/{PNG_SOURCE}");
                png["id"] = path.clone().into();
                png["path"] = path.into();
                png["isEntry"] = false.into();
                modules.push(png);
            },
            |build, project| {
                let map_path = build.join("assets/index-BHjrzQLH.js.map");
                let mut map: serde_json::Value =
                    serde_json::from_str(&std::fs::read_to_string(&map_path).unwrap()).unwrap();
                map["sources"]
                    .as_array_mut()
                    .unwrap()
                    .push(format!("{project}/{PNG_SOURCE}").into());
                map["sourcesContent"]
                    .as_array_mut()
                    .unwrap()
                    .push(stub.into());
                std::fs::write(&map_path, serde_json::to_string(&map).unwrap()).unwrap();
            },
        );
        let analysis = &fixture.output.analysis;
        assert!(
            !analysis
                .problems
                .iter()
                .any(|entry| entry.code == "BW_SOURCE_STALE"),
            "{:?}",
            analysis.problems
        );
        let png = fixture
            .output
            .sources
            .iter()
            .find(|source| source.path == PNG_SOURCE)
            .expect("the PNG keeps its source entry");
        assert!(!png.stale);
        assert!(png.content.is_empty());
        assert!(!png.content_included);
        assert!(!png.id.is_empty());
        assert!(
            fixture
                .output
                .sources
                .iter()
                .all(|source| !source.content.contains("__VITE_ASSET__"))
        );
        // Saved-run reloads only re-hash text sources.
        assert!(!fixture.output.project_relative.contains_key(&png.id));
        // Its removal is still detected when the saved run is reopened.
        assert_eq!(
            fixture
                .output
                .existence_relative
                .get(&png.id)
                .map(String::as_str),
            Some(PNG_SOURCE)
        );
        assert!(
            fixture
                .output
                .sources
                .iter()
                .filter(|source| source.origin == crate::model::SourceOrigin::Project)
                .filter(|source| source.id != png.id)
                .all(|source| fixture.output.project_relative.contains_key(&source.id))
        );
    }

    #[test]
    fn configured_context_root_owns_source_freshness_paths() {
        let fixture = run_fixture(BundlerChoice::Vite, |_| {});
        let selected_root = tempfile::tempdir().unwrap();
        let output = analyze(&AnalysisInput {
            run_id: "test-run".to_owned(),
            project_root: selected_root.path().to_path_buf(),
            project_name: "bundlewhy-fixture-vite-app".to_owned(),
            bundler: BundlerChoice::Vite,
            collection: &fixture.collection,
            build_dir: fixture._dir.path().join("build"),
            include_sources: true,
            started_at: String::new(),
            on_measuring: None,
            cancelled: None,
        });

        assert!(!output.project_relative.is_empty());
        assert!(
            output
                .project_relative
                .values()
                .all(|path| !Path::new(path).is_absolute())
        );
    }

    #[test]
    fn rspack_context_resolves_maps_without_changing_source_ownership() {
        let selected_root = PathBuf::from("/workspace/project");
        let collection = Collection {
            bundler: "rspack".to_owned(),
            context: Some("/workspace/config".to_owned()),
            ..Collection::default()
        };

        assert_eq!(effective_root(&collection, &selected_root), selected_root);
        assert_eq!(
            context_root(&collection, &selected_root),
            PathBuf::from("/workspace/config")
        );
    }

    #[test]
    fn nested_vite_root_keeps_selected_project_ownership() {
        let selected_root = PathBuf::from("/workspace/project");
        let collection = Collection {
            bundler: "vite".to_owned(),
            context: Some("web".to_owned()),
            ..Collection::default()
        };

        assert_eq!(effective_root(&collection, &selected_root), selected_root);
        assert_eq!(
            context_root(&collection, &selected_root),
            PathBuf::from("/workspace/project/web")
        );
    }

    #[test]
    fn external_vite_root_owns_sources() {
        let selected_root = PathBuf::from("/workspace/project");
        let collection = Collection {
            bundler: "vite".to_owned(),
            context: Some("/workspace/web".to_owned()),
            ..Collection::default()
        };

        assert_eq!(
            effective_root(&collection, &selected_root),
            PathBuf::from("/workspace/web")
        );
    }

    #[test]
    fn external_vite_subroot_expands_to_nearest_package_boundary() {
        let fixture = tempfile::tempdir().unwrap();
        let selected_root = fixture.path().join("wrapper");
        let package_root = fixture.path().join("app");
        let subroot = package_root.join("src");
        std::fs::create_dir_all(&selected_root).unwrap();
        std::fs::create_dir_all(&subroot).unwrap();
        std::fs::write(
            package_root.join("package.json"),
            r#"{"name":"external-app"}"#,
        )
        .unwrap();
        let collection = Collection {
            bundler: "vite".to_owned(),
            context: Some(subroot.to_string_lossy().into_owned()),
            ..Collection::default()
        };

        assert_eq!(effective_root(&collection, &selected_root), package_root);
    }

    #[test]
    fn vite_fixture_blockers_and_counterexamples() {
        let fixture = run_fixture(BundlerChoice::Vite, |_| {});
        let analysis = &fixture.output.analysis;
        let diagnostics = &analysis.diagnostics;
        assert_eq!(
            analysis.status,
            RunStatus::Complete,
            "{:?}",
            analysis.problems
        );

        assert!(
            find(
                diagnostics,
                "vite/side-effect-retained",
                "src/effects/register.js"
            )
            .is_some()
        );
        let register = find(
            diagnostics,
            "vite/side-effect-retained",
            "src/effects/register.js",
        )
        .unwrap();
        // Rolldown reports no retention reason, so the cause is inferred.
        assert_eq!(register.status, EvidenceStatus::Inferred);
        assert!(!register.summary.contains("must run"));
        // The fixture module declares an export and the collection holds no
        // importer binding data, so the direction asks to check importers and
        // does not advise changing side-effect declarations.
        let register_direction = &register.corrective_action.as_ref().unwrap().direction;
        assert!(register_direction.contains("Check which importers"));
        assert!(!register_direction.contains("\"sideEffects\""));
        assert!(
            !register
                .corrective_action
                .as_ref()
                .unwrap()
                .safety
                .is_empty()
        );
        assert_eq!(
            declared_free_outcome(register),
            ConditionOutcome::Unmet,
            "an undeclared module is not declared side-effect free"
        );
        assert_absent(
            diagnostics,
            "vite/side-effect-retained",
            "src/pure/index.js",
        );
        assert_absent(
            diagnostics,
            "vite/side-effect-retained",
            "src/legacy/cjs.cjs",
        );
        // The entry script loaded by index.html is meant to run.
        assert_absent(diagnostics, "vite/side-effect-retained", "src/main.js");

        let cjs = find(diagnostics, "vite/commonjs-wrapped", "src/legacy/cjs.cjs").unwrap();
        assert_eq!(cjs.status, EvidenceStatus::Confirmed);
        assert_absent(diagnostics, "vite/commonjs-wrapped", "src/esm/value.js");

        let dynamic = find(
            diagnostics,
            "vite/ineffective-dynamic-import",
            "src/shared/static-and-dynamic.js",
        )
        .unwrap();
        assert!(
            dynamic
                .evidence
                .iter()
                .any(|evidence| evidence.provenance.contains("INEFFECTIVE_DYNAMIC_IMPORT"))
        );
        // The finding is tied to the importer and the emitted chunk.
        assert!(dynamic.evidence.iter().any(|evidence| {
            evidence.provenance == "emitted chunk graph"
                && evidence.detail.contains("src/main.js")
                && evidence.detail.contains("assets/index-BHjrzQLH.js")
        }));
        assert_absent(
            diagnostics,
            "vite/ineffective-dynamic-import",
            "src/lazy/a.js",
        );

        common_assertions(analysis);
    }

    fn common_assertions(analysis: &Analysis) {
        let diagnostics = &analysis.diagnostics;
        let duplicate = find(diagnostics, "bundle/duplicate-package", "vendor/dup-v").unwrap();
        assert!(duplicate.title.contains("bundlewhy-dup-lib"));
        assert_eq!(duplicate.dependency_paths.len(), 2);
        assert!(
            diagnostics
                .iter()
                .all(|diagnostic| !diagnostic.title.contains("bundlewhy-single-lib"))
        );

        let env = diagnostics
            .iter()
            .find(|diagnostic| diagnostic.rule == "minifier/unresolved-env-condition")
            .expect("unresolved env diagnostic");
        assert_eq!(env.status, EvidenceStatus::Inferred);
        assert!(env.title.contains("BUNDLEWHY_FIXTURE_FLAG"));
        assert_eq!(env.location.as_ref().unwrap().path, "src/main.js");
        assert!(env.generated_location.is_some());
        assert!(env.summary.contains("retains a branch guarded by"));
        assert!(env.conditions.iter().any(|condition| {
            condition.outcome == ConditionOutcome::Met
                && condition.detail.contains("&& short-circuit branch")
        }));
        assert!(
            !diagnostics
                .iter()
                .any(|diagnostic| diagnostic.title.contains("NODE_ENV"))
        );

        let eval = find(
            diagnostics,
            "minifier/direct-eval-blocks-mangling",
            "src/dynamic/evaluate.js",
        )
        .unwrap();
        assert_eq!(eval.status, EvidenceStatus::Confirmed);
        assert_eq!(eval.location.as_ref().unwrap().line, 4);
        assert_absent(
            diagnostics,
            "minifier/direct-eval-blocks-mangling",
            "src/dynamic/plain.js",
        );
        assert_absent(diagnostics, "minifier/minification-disabled", "");

        let engine = analysis.engine.as_ref().unwrap();
        assert_eq!(engine.support, EngineSupport::Supported);
        assert!(engine.diagnostic_engine);
        assert!(!engine.overrides.is_empty());

        for category in [
            OptimizationCategory::Inlining,
            OptimizationCategory::ConstantFolding,
        ] {
            let entry = analysis
                .coverage
                .iter()
                .find(|entry| entry.category == category)
                .unwrap();
            assert_eq!(entry.state, CoverageState::Unavailable);
        }
        let covered = analysis
            .coverage
            .iter()
            .filter(|entry| entry.state == CoverageState::Covered)
            .count();
        assert_eq!(covered, 6);

        for chunk in &analysis.chunks {
            assert!(chunk.gzip_bytes > 0 && chunk.brotli_bytes > 0);
            let mapped: u32 = chunk
                .sources
                .iter()
                .map(|source| source.generated_bytes)
                .sum();
            assert_eq!(
                mapped + chunk.unmapped_bytes,
                chunk.raw_bytes,
                "{}",
                chunk.file
            );
        }
        for diagnostic in diagnostics {
            assert!(
                diagnostic
                    .corrective_action
                    .as_ref()
                    .is_some_and(|action| !action.safety.is_empty())
            );
            let location = diagnostic.location.as_ref().unwrap();
            if !location.source_id.is_empty() {
                assert!(
                    analysis
                        .sources
                        .iter()
                        .any(|source| source.id == location.source_id)
                );
            }
        }
    }

    #[test]
    fn missing_source_maps_fall_back_to_generated_locations() {
        let fixture = run_fixture(BundlerChoice::Rspack, |value| {
            for chunk in value["chunks"].as_array_mut().unwrap() {
                chunk["map"] = serde_json::Value::String("missing.map".to_owned());
            }
        });
        let analysis = &fixture.output.analysis;
        assert_eq!(analysis.status, RunStatus::Partial);
        assert!(
            analysis
                .problems
                .iter()
                .any(|entry| entry.code == "BW_SOURCE_MAP_MISSING")
        );
        let env = analysis
            .diagnostics
            .iter()
            .find(|diagnostic| diagnostic.rule == "minifier/unresolved-env-condition")
            .unwrap();
        assert_eq!(
            env.location.as_ref().unwrap().kind,
            crate::model::LocationKind::Generated
        );
        assert!(
            analysis
                .chunks
                .iter()
                .all(|chunk| chunk.attribution == crate::model::AttributionState::Unavailable)
        );
        let dce = analysis
            .coverage
            .iter()
            .find(|entry| entry.category == OptimizationCategory::DeadCodeElimination)
            .unwrap();
        assert_eq!(dce.state, CoverageState::Partial);
    }

    #[test]
    fn excluded_sources_remove_snippets() {
        let fixture = run_fixture_with(BundlerChoice::Vite, false, |_| {});
        let analysis = &fixture.output.analysis;
        assert!(!analysis.diagnostics.is_empty());
        assert!(
            analysis
                .diagnostics
                .iter()
                .all(|diagnostic| diagnostic.snippet.is_none())
        );
        assert!(
            fixture
                .output
                .sources
                .iter()
                .all(|source| source.content.is_empty())
        );
    }

    #[test]
    fn unreadable_chunks_mark_generated_code_categories_partial() {
        let fixture = run_fixture(BundlerChoice::Rspack, |value| {
            value["chunks"][0]["file"] = serde_json::json!("missing-chunk.js");
        });
        let analysis = &fixture.output.analysis;
        assert_eq!(analysis.status, RunStatus::Partial);
        for category in [
            OptimizationCategory::DeadCodeElimination,
            OptimizationCategory::NameMangling,
        ] {
            let entry = analysis
                .coverage
                .iter()
                .find(|entry| entry.category == category)
                .unwrap();
            assert_eq!(entry.state, CoverageState::Partial, "{category:?}");
        }
    }

    #[test]
    fn rspack_side_effect_rule_covers_export_less_modules() {
        let fixture = run_fixture(BundlerChoice::Rspack, |value| {
            for module in value["modules"].as_array_mut().unwrap() {
                if module["name"] == "./src/effects/register.js" {
                    module["exports"] = serde_json::json!([]);
                }
            }
        });
        assert!(
            find(
                &fixture.output.analysis.diagnostics,
                "rspack/side-effect-retained",
                "src/effects/register.js"
            )
            .is_some()
        );
    }

    #[test]
    fn vite_commonjs_rule_requires_the_rendered_wrapper_helper() {
        let fixture = run_fixture(BundlerChoice::Vite, |value| {
            for module in value["modules"].as_array_mut().unwrap() {
                if module["id"]
                    .as_str()
                    .is_some_and(|id| id.contains("rolldown/runtime"))
                {
                    module["usedExports"] = serde_json::json!(["__toESM"]);
                }
            }
        });
        assert_absent(
            &fixture.output.analysis.diagnostics,
            "vite/commonjs-wrapped",
            "src/legacy/cjs.cjs",
        );
    }

    /// Rewrites the Vite fixture so that `target_id` names a wrapped CommonJS
    /// module with the given virtual/path facts, importing from the entry.
    fn vite_cjs_variant(id: &'static str, path: Option<&'static str>, is_virtual: bool) -> Fixture {
        run_fixture(BundlerChoice::Vite, move |value| {
            let mut template = value["modules"]
                .as_array()
                .unwrap()
                .iter()
                .find(|module| {
                    module["id"]
                        .as_str()
                        .is_some_and(|id| id.ends_with("src/legacy/cjs.cjs"))
                })
                .unwrap()
                .clone();
            template["id"] = serde_json::json!(id);
            template["path"] = serde_json::json!(path);
            template["virtual"] = serde_json::json!(is_virtual);
            value["modules"].as_array_mut().unwrap().push(template);
        })
    }

    fn action(diagnostic: &Diagnostic) -> &crate::model::CorrectiveAction {
        diagnostic.corrective_action.as_ref().unwrap()
    }

    fn generated_placeholder_diagnostic<'a>(fixture: &'a Fixture, module: &str) -> &'a Diagnostic {
        find(
            &fixture.output.analysis.diagnostics,
            "vite/commonjs-wrapped",
            module,
        )
        .unwrap_or_else(|| panic!("missing vite/commonjs-wrapped for {module}"))
    }

    /// Words that name a specific Node built-in; guidance for a placeholder
    /// whose original specifier is unknown must not use them.
    fn names_specific_builtin(text: &str) -> bool {
        text.split(|c: char| !c.is_alphanumeric() && c != '_')
            .any(|word| matches!(word, "fs" | "os" | "crypto" | "net" | "child_process"))
    }

    fn assert_generated_placeholder_common(diagnostic: &Diagnostic, id: &str) {
        assert_eq!(diagnostic.status, EvidenceStatus::Confirmed, "{id}");
        assert!(
            diagnostic
                .evidence
                .iter()
                .all(|evidence| evidence.status == EvidenceStatus::Confirmed),
            "{id}"
        );
        assert!(diagnostic.summary.contains("generates"), "{id}");
        assert!(diagnostic.summary.contains("must not be edited"), "{id}");
        assert!(
            action(diagnostic).direction.contains("dependency path"),
            "{id}"
        );
        assert!(action(diagnostic).direction.contains("server-only"), "{id}");
        assert!(
            !action(diagnostic)
                .direction
                .contains("ECMAScript module build"),
            "{id}"
        );
        assert!(!action(diagnostic).direction.contains("ESM"), "{id}");
        assert!(action(diagnostic).safety.contains("Do not edit"), "{id}");
        assert!(!action(diagnostic).safety.contains("ESM build"), "{id}");
        assert!(
            !names_specific_builtin(&action(diagnostic).direction),
            "{id}"
        );
    }

    #[test]
    fn vite_browser_external_with_builtin_suffix_gets_node_builtin_guidance() {
        for id in [
            "\u{0}__vite-browser-external:fs",
            "__vite-browser-external:path",
        ] {
            let fixture = vite_cjs_variant(id, None, true);
            let diagnostic =
                generated_placeholder_diagnostic(&fixture, id.trim_start_matches('\0'));
            assert_generated_placeholder_common(diagnostic, id);
            assert!(diagnostic.summary.contains("Node built-in"), "{id}");
            assert!(
                action(diagnostic).direction.contains("Node built-in"),
                "{id}"
            );
            assert!(action(diagnostic).direction.contains("polyfill"), "{id}");
            assert!(
                diagnostic
                    .evidence
                    .iter()
                    .any(|evidence| evidence.detail.contains("built-in suffix")),
                "{id}"
            );
        }
    }

    #[test]
    fn vite_bare_browser_external_keeps_guidance_generic() {
        // The bare id is also produced for a dependency file that its
        // `browser` field maps to `false`, so it must not be called a built-in.
        for id in ["__vite-browser-external", "\u{0}__vite-browser-external"] {
            let fixture = vite_cjs_variant(id, None, true);
            let diagnostic =
                generated_placeholder_diagnostic(&fixture, id.trim_start_matches('\0'));
            assert_generated_placeholder_common(diagnostic, id);
            let direction = &action(diagnostic).direction;
            assert!(direction.contains("browser field"), "{id}");
            assert!(!direction.contains("polyfill"), "{id}");
            assert!(!direction.contains("alias"), "{id}");
            assert!(
                !diagnostic.summary.contains("generates for a Node built-in"),
                "{id}"
            );
            assert!(
                !diagnostic.summary.contains("for a Node built-in imported"),
                "{id}"
            );
            assert!(
                !action(diagnostic).safety.contains("built-in"),
                "{id}: the safety text must not assume a Node built-in"
            );
            assert!(
                diagnostic
                    .evidence
                    .iter()
                    .all(|evidence| { !evidence.detail.contains("generates for a Node built-in") }),
                "{id}"
            );
            assert!(
                diagnostic
                    .evidence
                    .iter()
                    .any(|evidence| evidence.detail.contains("does not show whether")),
                "{id}"
            );
        }
    }

    #[test]
    fn vite_commonjs_guidance_stays_for_other_modules() {
        // An ordinary file-backed module keeps the ESM-conversion advice.
        let fixture = run_fixture(BundlerChoice::Vite, |_| {});
        let ordinary = generated_placeholder_diagnostic(&fixture, "src/legacy/cjs.cjs");
        assert!(
            action(ordinary)
                .direction
                .contains("ECMAScript module build")
        );
        assert!(action(ordinary).safety.contains("ESM build"));

        // A virtual CommonJS module with another id is not the placeholder.
        let other = vite_cjs_variant("\u{0}virtual:other-cjs", None, true);
        let diagnostic = generated_placeholder_diagnostic(&other, "virtual:other-cjs");
        assert!(
            action(diagnostic)
                .direction
                .contains("ECMAScript module build")
        );
        assert!(action(diagnostic).safety.contains("ESM build"));

        // Ids that only share the placeholder prefix are not placeholders.
        for id in ["__vite-browser-external:", "__vite-browser-externalish"] {
            let lookalike = vite_cjs_variant(id, None, true);
            let diagnostic = generated_placeholder_diagnostic(&lookalike, id);
            assert!(
                action(diagnostic)
                    .direction
                    .contains("ECMAScript module build"),
                "{id}"
            );
            assert!(action(diagnostic).safety.contains("ESM build"), "{id}");
        }

        // A file-backed module that merely resembles the id keeps the advice.
        let backed = vite_cjs_variant(
            "__vite-browser-external",
            Some("/__bundlewhy_project__/src/legacy/cjs.cjs"),
            false,
        );
        assert!(
            backed
                .output
                .analysis
                .diagnostics
                .iter()
                .filter(|d| d.rule == "vite/commonjs-wrapped")
                .all(|d| action(d).direction.contains("ECMAScript module build"))
        );
    }

    #[test]
    fn vite_disabled_tree_shaking_is_not_reported_as_covered() {
        let fixture = run_fixture(BundlerChoice::Vite, |value| {
            value["configSummary"]["build.rolldownOptions.treeshake"] = serde_json::json!("false");
        });
        let analysis = &fixture.output.analysis;
        assert!(
            find(
                &analysis.diagnostics,
                "vite/tree-shaking-disabled",
                "vite.config.js"
            )
            .is_some()
        );
        assert_absent(&analysis.diagnostics, "vite/side-effect-retained", "");
        let entry = analysis
            .coverage
            .iter()
            .find(|entry| entry.category == OptimizationCategory::TreeShaking)
            .unwrap();
        assert_eq!(entry.state, CoverageState::NotApplicable);
        assert!(
            !analysis
                .problems
                .iter()
                .any(|problem| problem.code == "BW_SOURCE_UNVERIFIED")
        );
        for category in [
            OptimizationCategory::DeadCodeElimination,
            OptimizationCategory::NameMangling,
        ] {
            let entry = analysis
                .coverage
                .iter()
                .find(|entry| entry.category == category)
                .unwrap();
            assert_eq!(entry.state, CoverageState::Covered, "{category:?}");
        }
    }

    #[test]
    fn rspack_disabled_optimizations_are_not_reported_as_covered() {
        let fixture = run_fixture(BundlerChoice::Rspack, |value| {
            value["configSummary"]["optimization.usedExports"] = serde_json::json!("false");
            value["configSummary"]["optimization.concatenateModules"] = serde_json::json!("false");
        });
        let analysis = &fixture.output.analysis;
        assert!(
            find(
                &analysis.diagnostics,
                "rspack/used-exports-disabled",
                "rspack.config.mjs"
            )
            .is_some()
        );
        assert!(
            find(
                &analysis.diagnostics,
                "rspack/concatenation-disabled",
                "rspack.config.mjs"
            )
            .is_some()
        );
        assert_absent(&analysis.diagnostics, "rspack/side-effect-retained", "");
        assert_absent(
            &analysis.diagnostics,
            "rspack/module-concatenation-bailout",
            "",
        );
        for category in [
            OptimizationCategory::TreeShaking,
            OptimizationCategory::ModuleConcatenation,
        ] {
            let entry = analysis
                .coverage
                .iter()
                .find(|entry| entry.category == category)
                .unwrap();
            assert_eq!(entry.state, CoverageState::NotApplicable, "{category:?}");
        }
    }

    #[test]
    fn rspack_disabled_side_effects_or_provided_exports_are_not_covered() {
        for option in ["optimization.sideEffects", "optimization.providedExports"] {
            let fixture = run_fixture(BundlerChoice::Rspack, |value| {
                value["configSummary"][option] = serde_json::json!("false");
            });
            let analysis = &fixture.output.analysis;
            assert_absent(&analysis.diagnostics, "rspack/side-effect-retained", "");
            assert!(
                analysis
                    .diagnostics
                    .iter()
                    .any(|diagnostic| diagnostic.rule.ends_with("-disabled")
                        && diagnostic.title.contains("analysis is disabled")),
                "{option}"
            );
            let entry = analysis
                .coverage
                .iter()
                .find(|entry| entry.category == OptimizationCategory::TreeShaking)
                .unwrap();
            assert_eq!(entry.state, CoverageState::NotApplicable, "{option}");
        }
    }

    fn declared_free_outcome(diagnostic: &Diagnostic) -> ConditionOutcome {
        diagnostic
            .conditions
            .iter()
            .find(|condition| condition.description == "The module is declared side-effect free")
            .expect("declared side-effect free condition")
            .outcome
    }

    fn retained_register(
        module_side_effects: &str,
        treeshake_side_effects: &str,
        package_side_effects: Option<&str>,
    ) -> Diagnostic {
        let module_side_effects = module_side_effects.to_owned();
        let treeshake_side_effects = treeshake_side_effects.to_owned();
        let package_side_effects = package_side_effects.map(str::to_owned);
        let fixture = run_fixture(BundlerChoice::Vite, |value| {
            for module in value["modules"].as_array_mut().unwrap() {
                if module["id"]
                    .as_str()
                    .is_some_and(|id| id.ends_with("src/effects/register.js"))
                {
                    // An effect-only module declares no export, so the
                    // finding can state that nothing uses one.
                    module["exports"] = serde_json::json!([]);
                    module["moduleSideEffects"] = serde_json::json!(module_side_effects);
                    module["treeshakeSideEffects"] = serde_json::json!(treeshake_side_effects);
                    module["packageSideEffects"] = serde_json::json!(package_side_effects);
                }
            }
        });
        find(
            &fixture.output.analysis.diagnostics,
            "vite/side-effect-retained",
            "src/effects/register.js",
        )
        .expect("register.js stays reported")
        .clone()
    }

    fn direction(diagnostic: &Diagnostic) -> &str {
        &diagnostic.corrective_action.as_ref().unwrap().direction
    }

    fn has_inferred_effect_evidence(diagnostic: &Diagnostic) -> bool {
        diagnostic.evidence.iter().any(|evidence| {
            evidence.status == EvidenceStatus::Inferred
                && evidence.detail.contains("top-level side effects")
        })
    }

    #[test]
    fn vite_side_effect_retained_honors_free_declarations() {
        for (module, option, package) in [
            ("false", "null", None),
            // A hook value takes precedence over the option and the package.
            ("false", "true", Some("true")),
            ("false", "null", Some("false")),
            ("false", "null", Some("[array of 2]")),
            // Without a hook value or a package declaration the option decides.
            ("null", "false", None),
            ("undefined", "false", None),
        ] {
            let finding = retained_register(module, option, package);
            assert_eq!(
                declared_free_outcome(&finding),
                ConditionOutcome::Met,
                "{module} / {option} / {package:?}"
            );
            assert_eq!(finding.status, EvidenceStatus::Inferred);
            assert!(
                !direction(&finding).contains("declare the package side-effect free"),
                "{}",
                direction(&finding)
            );
            assert!(
                direction(&finding).contains("already declared side-effect free"),
                "{}",
                direction(&finding)
            );
            // An effective `false` lets Rolldown discard a module that has
            // effects, so retained effects are not inferred for it.
            assert!(
                !finding.summary.contains("may contain top-level code"),
                "{}",
                finding.summary
            );
            assert!(
                finding.summary.contains("another use of the module"),
                "{}",
                finding.summary
            );
            assert!(
                !has_inferred_effect_evidence(&finding),
                "{:?}",
                finding.evidence
            );
            assert!(
                finding
                    .evidence
                    .iter()
                    .any(|evidence| evidence.status == EvidenceStatus::Inferred
                        && evidence.detail.contains("remaining use")),
                "{:?}",
                finding.evidence
            );
            assert!(
                !finding
                    .corrective_action
                    .as_ref()
                    .unwrap()
                    .safety
                    .is_empty()
            );
        }
    }

    #[test]
    fn vite_side_effect_retained_reports_explicit_effect_declarations() {
        for (module, option, package) in [
            ("no-treeshake", "null", None),
            // A hook value takes precedence over the option and the package.
            ("no-treeshake", "false", Some("false")),
            ("no-treeshake", "null", Some("false")),
            // The option decides when no hook value is set and the package
            // declares nothing.
            ("null", "true", None),
        ] {
            let finding = retained_register(module, option, package);
            assert_eq!(
                declared_free_outcome(&finding),
                ConditionOutcome::Unmet,
                "{module} / {option} / {package:?}"
            );
            assert_eq!(finding.status, EvidenceStatus::Confirmed);
            assert!(finding.summary.contains("declares side effects"));
            assert!(!direction(&finding).contains("declare the package side-effect free"));
            assert!(!has_inferred_effect_evidence(&finding));
            assert!(
                !finding
                    .corrective_action
                    .as_ref()
                    .unwrap()
                    .safety
                    .is_empty()
            );
        }
    }

    #[test]
    fn vite_side_effect_retained_analyzes_hook_true_and_names_the_plugin() {
        // `true` from a JavaScript hook asks for Rolldown's analysis and skips
        // both the option and the package, whatever they say. The package
        // declaration is not the source of this value, so the guidance never
        // asks to edit it.
        for (module, option, package) in [
            ("true", "false", Some("false")),
            ("true", "true", Some("true")),
            ("true", "false", Some("[array of 2]")),
            ("true", "true", None),
            ("true", "null", None),
        ] {
            let finding = retained_register(module, option, package);
            let case = format!("{module} / {option} / {package:?}");
            assert_eq!(
                declared_free_outcome(&finding),
                ConditionOutcome::Unmet,
                "{case}"
            );
            assert_eq!(finding.status, EvidenceStatus::Inferred, "{case}");
            assert!(has_inferred_effect_evidence(&finding), "{case}");
            assert!(!direction(&finding).contains("will not help"), "{case}");
            assert!(
                direction(&finding).contains("stop the plugin"),
                "{}",
                direction(&finding)
            );
            assert!(!direction(&finding).contains("narrowing"), "{case}");
        }
    }

    #[test]
    fn vite_side_effect_retained_is_unknown_for_unmatched_values() {
        let list = retained_register("null", "null", Some("[array of 2]"));
        assert_eq!(declared_free_outcome(&list), ConditionOutcome::Unknown);
        assert_eq!(list.status, EvidenceStatus::Inferred);
        // A retained file is most likely covered by the list; the guidance
        // never asks to add a declaration for it.
        assert!(direction(&list).contains("most likely covered"));
        assert!(direction(&list).contains("narrow or remove that entry"));
        assert!(!direction(&list).contains("add the declaration"));
        assert!(has_inferred_effect_evidence(&list));

        // A configured function cannot be evaluated.
        let option = retained_register("null", "[function]", None);
        assert_eq!(declared_free_outcome(&option), ConditionOutcome::Unknown);
        assert!(
            direction(&option).contains("build.rolldownOptions.treeshake.moduleSideEffects"),
            "{}",
            direction(&option)
        );
        assert!(!direction(&option).contains("covered"));

        // A declared package may have given the module a value that outranks
        // the option and is not collected, whatever the declaration is and
        // whatever the option says.
        for (option, package) in [
            ("null", "false"),
            ("null", "true"),
            ("false", "true"),
            ("true", "false"),
            ("[function]", "[array of 2]"),
            ("[array of 2]", "true"),
        ] {
            let finding = retained_register("null", option, Some(package));
            assert_eq!(
                declared_free_outcome(&finding),
                ConditionOutcome::Unknown,
                "{option} / {package}"
            );
            assert_eq!(finding.status, EvidenceStatus::Inferred);
            assert!(direction(&finding).contains("most likely covered"));
            assert!(!direction(&finding).contains("add the declaration"));
        }

        let hook = retained_register("[function]", "null", Some("true"));
        assert_eq!(declared_free_outcome(&hook), ConditionOutcome::Unknown);
        assert!(direction(&hook).contains("plugin"));
        assert!(!direction(&hook).contains("covered"));
        for finding in [&list, &option, &hook] {
            assert!(
                !finding
                    .corrective_action
                    .as_ref()
                    .unwrap()
                    .safety
                    .is_empty()
            );
        }
    }

    /// Runs the Vite fixture with `src/effects/register.js` declaring
    /// `exports` and the given hook value, and returns its finding.
    fn retained_with_exports(exports: &[&str], module_side_effects: &str) -> Diagnostic {
        retained_with_declaration(exports, module_side_effects, None)
    }

    /// Like `retained_with_exports`, also setting the collected
    /// `treeshake.moduleSideEffects` verdict when one is given.
    fn retained_with_declaration(
        exports: &[&str],
        module_side_effects: &str,
        treeshake_side_effects: Option<&str>,
    ) -> Diagnostic {
        let exports: Vec<String> = exports.iter().map(|name| (*name).to_owned()).collect();
        let module_side_effects = module_side_effects.to_owned();
        let treeshake_side_effects = treeshake_side_effects.map(str::to_owned);
        let fixture = run_fixture(BundlerChoice::Vite, |value| {
            for module in value["modules"].as_array_mut().unwrap() {
                if module["id"]
                    .as_str()
                    .is_some_and(|id| id.ends_with("src/effects/register.js"))
                {
                    module["exports"] = serde_json::json!(exports);
                    module["usedExports"] = serde_json::json!([]);
                    module["moduleSideEffects"] = serde_json::json!(module_side_effects);
                    if let Some(option) = &treeshake_side_effects {
                        module["treeshakeSideEffects"] = serde_json::json!(option);
                    }
                }
            }
        });
        find(
            &fixture.output.analysis.diagnostics,
            "vite/side-effect-retained",
            "src/effects/register.js",
        )
        .expect("register.js stays reported")
        .clone()
    }

    fn export_use_outcome(diagnostic: &Diagnostic) -> ConditionOutcome {
        diagnostic
            .conditions
            .iter()
            .find(|condition| condition.description == "No export of the module is used")
            .expect("export use condition")
            .outcome
    }

    #[test]
    fn vite_side_effect_retained_does_not_assert_unused_exports_without_importer_data() {
        // Re-export facades, default re-exports and directly exported
        // components that a same-chunk importer reads have an empty
        // `renderedExports` list. That list does not show that no importer
        // uses the export.
        for (exports, hook) in [
            (
                vec!["EmbeddedActionsParser", "Lexer", "createToken"],
                "null",
            ),
            (vec!["default"], "null"),
            (vec!["unstable_batchedUpdates"], "null"),
            (vec!["FontAwesomeIcon"], "no-treeshake"),
            (vec!["FontAwesomeIcon"], "false"),
            (vec!["FontAwesomeIcon"], "true"),
        ] {
            let case = format!("{exports:?} / {hook}");
            let finding = retained_with_exports(&exports, hook);
            assert_eq!(
                export_use_outcome(&finding),
                ConditionOutcome::Unknown,
                "{case}"
            );
            // Even an effect declaration does not make the finding
            // Confirmed: an importer, not the declaration, can be the reason.
            assert_eq!(finding.status, EvidenceStatus::Inferred, "{case}");
            for text in [&finding.title, &finding.summary] {
                assert!(
                    !text.contains("none of its exports is used"),
                    "{case}: {text}"
                );
            }
            assert!(finding.summary.contains("unknown"), "{}", finding.summary);
            assert!(
                finding.summary.contains("chunk-level bindings"),
                "{}",
                finding.summary
            );
            let direction = direction(&finding);
            assert!(direction.contains("Check which importers"), "{direction}");
            for declaration_advice in [
                "sideEffects",
                "moduleSideEffects",
                "side-effect free with",
                "narrow",
            ] {
                assert!(
                    !direction.contains(declaration_advice),
                    "{case}: {direction}"
                );
            }
            let safety = &finding.corrective_action.as_ref().unwrap().safety;
            assert!(safety.contains("Do not remove"), "{safety}");
            assert!(
                finding
                    .evidence
                    .iter()
                    .any(|evidence| evidence.status == EvidenceStatus::Inferred
                        && evidence.detail.contains("not established")),
                "{:?}",
                finding.evidence
            );
        }
    }

    #[test]
    fn vite_side_effect_retained_keeps_effect_only_modules_confirmed() {
        // A module that declares no export has nothing for an importer to
        // read, so the rendered code is the only reason it is kept.
        let finding = retained_with_exports(&[], "no-treeshake");
        assert_eq!(export_use_outcome(&finding), ConditionOutcome::Met);
        assert_eq!(finding.status, EvidenceStatus::Confirmed);
        assert!(!finding.summary.contains("unknown"), "{}", finding.summary);
        assert!(direction(&finding).contains("Move top-level side effects"));
        let analyzed = retained_with_exports(&[], "null");
        assert_eq!(export_use_outcome(&analyzed), ConditionOutcome::Met);
        assert_eq!(analyzed.status, EvidenceStatus::Inferred);
        assert!(has_inferred_effect_evidence(&analyzed));
    }

    #[test]
    fn vite_side_effect_retained_confirms_only_modules_without_exports() {
        // (hook value, configured option verdict, whether the effective
        // declaration keeps the module). Only a module that declares no export
        // can be Confirmed; a module that declares exports stays Inferred with
        // an Unknown export-use condition under every declaration.
        for (hook, option, keeps) in [
            ("no-treeshake", None, true),
            ("null", Some("true"), true),
            ("false", None, false),
            ("true", None, false),
            ("null", Some("false"), false),
            ("null", Some("null"), false),
        ] {
            let case = format!("{hook} / {option:?}");
            let without_exports = retained_with_declaration(&[], hook, option);
            assert_eq!(
                export_use_outcome(&without_exports),
                ConditionOutcome::Met,
                "{case}"
            );
            let expected = if keeps {
                EvidenceStatus::Confirmed
            } else {
                EvidenceStatus::Inferred
            };
            assert_eq!(without_exports.status, expected, "{case}");
            let with_exports = retained_with_declaration(&["default"], hook, option);
            assert_eq!(
                export_use_outcome(&with_exports),
                ConditionOutcome::Unknown,
                "{case}"
            );
            assert_eq!(with_exports.status, EvidenceStatus::Inferred, "{case}");
        }
    }

    #[test]
    fn vite_side_effect_retained_fixture_effect_only_import_is_still_reported() {
        // The unmodified fixture module is imported without bindings and its
        // code remains rendered. It is still reported, as unknown export use.
        let baseline = run_fixture(BundlerChoice::Vite, |_| {});
        let finding = find(
            &baseline.output.analysis.diagnostics,
            "vite/side-effect-retained",
            "src/effects/register.js",
        )
        .expect("register.js stays reported");
        assert_eq!(export_use_outcome(finding), ConditionOutcome::Unknown);
        assert!(!finding.title.contains("none of its exports is used"));
    }

    #[test]
    fn vite_side_effect_retained_id_ignores_the_export_use_state() {
        let with_exports = retained_with_exports(&["FontAwesomeIcon"], "null");
        let without_exports = retained_with_exports(&[], "null");
        assert_eq!(with_exports.id, without_exports.id);
        assert_eq!(with_exports.title, without_exports.title);
    }

    /// Runs the Vite fixture after adding one emitted chunk whose facade is
    /// `src/effects/register.js`, an export-less module.
    fn register_with_chunk(is_dynamic_entry: bool, is_entry: bool) -> Fixture {
        run_fixture(BundlerChoice::Vite, |value| {
            let register_id = value["modules"]
                .as_array()
                .unwrap()
                .iter()
                .filter_map(|module| module["id"].as_str())
                .find(|id| id.ends_with("src/effects/register.js"))
                .expect("fixture has register.js")
                .to_owned();
            let chunks = value["chunks"].as_array_mut().unwrap();
            let mut chunk = chunks
                .iter()
                .find(|chunk| chunk["isDynamicEntry"] == true)
                .expect("fixture has a dynamic entry chunk")
                .clone();
            chunk["facadeModuleId"] = serde_json::json!(register_id);
            chunk["isDynamicEntry"] = serde_json::json!(is_dynamic_entry);
            chunk["isEntry"] = serde_json::json!(is_entry);
            chunks.push(chunk);
        })
    }

    #[test]
    fn vite_side_effect_retained_skips_dynamic_entry_facades() {
        let fixture = register_with_chunk(true, false);
        let analysis = &fixture.output.analysis;
        assert_eq!(
            analysis.status,
            RunStatus::Complete,
            "{:?}",
            analysis.problems
        );
        assert_absent(
            &analysis.diagnostics,
            "vite/side-effect-retained",
            "src/effects/register.js",
        );
    }

    #[test]
    fn vite_side_effect_retained_keeps_static_import_modules_with_non_dynamic_facade_chunks() {
        // A chunk facade that is not a dynamic entry does not exempt the
        // module.
        let fixture = register_with_chunk(false, false);
        assert!(
            find(
                &fixture.output.analysis.diagnostics,
                "vite/side-effect-retained",
                "src/effects/register.js"
            )
            .is_some()
        );
        // The unmodified fixture keeps the finding for the static import.
        let baseline = run_fixture(BundlerChoice::Vite, |_| {});
        assert!(
            find(
                &baseline.output.analysis.diagnostics,
                "vite/side-effect-retained",
                "src/effects/register.js"
            )
            .is_some()
        );
        // Other dynamic entry facades stay unaffected and unreported.
        assert_absent(
            &baseline.output.analysis.diagnostics,
            "vite/side-effect-retained",
            "src/lazy/a.js",
        );
    }

    #[test]
    fn vite_side_effect_retained_id_ignores_the_declaration() {
        let ids: Vec<String> = [
            ("null", "null", None),
            ("false", "null", None),
            ("true", "null", Some("false")),
            ("null", "false", Some("true")),
            ("null", "false", None),
            ("null", "null", Some("[array of 2]")),
        ]
        .into_iter()
        .map(|(module, option, package)| retained_register(module, option, package).id)
        .collect();
        assert!(ids.iter().all(|id| id == &ids[0]), "{ids:?}");
    }

    #[test]
    fn vite_side_effect_retained_keeps_provenance_and_paths() {
        let finding = retained_register("null", "null", None);
        assert_eq!(
            finding.evidence[0].provenance,
            "rolldown module info (getModuleInfo)"
        );
        assert_eq!(
            finding.evidence[1].provenance,
            "rolldown rendered chunk modules"
        );
        assert_eq!(finding.evidence[0].status, EvidenceStatus::Confirmed);
        assert_eq!(finding.evidence[1].status, EvidenceStatus::Confirmed);
        assert!(
            finding
                .evidence
                .iter()
                .any(|evidence| evidence.status == EvidenceStatus::Inferred)
        );
        assert_eq!(
            finding.dependency_paths[0].modules,
            vec!["index.html", "src/main.js", "src/effects/register.js"]
        );
    }

    #[test]
    fn vite_export_less_side_effect_modules_are_reported() {
        let fixture = run_fixture(BundlerChoice::Vite, |value| {
            for module in value["modules"].as_array_mut().unwrap() {
                if module["id"]
                    .as_str()
                    .is_some_and(|id| id.ends_with("src/effects/register.js"))
                {
                    module["exports"] = serde_json::json!([]);
                }
            }
        });
        let finding = find(
            &fixture.output.analysis.diagnostics,
            "vite/side-effect-retained",
            "src/effects/register.js",
        )
        .unwrap();
        assert!(
            !finding
                .corrective_action
                .as_ref()
                .unwrap()
                .direction
                .is_empty()
        );
        assert!(
            !finding
                .corrective_action
                .as_ref()
                .unwrap()
                .safety
                .is_empty()
        );
    }

    /// Runs the Vite fixture with the entry chunk's source map replaced by a
    /// synthetic one (`None` deletes the map) and returns the analysis.
    fn vite_with_entry_map(
        sources: &'static [&'static str],
        mappings: &'static str,
        edit: impl Fn(&mut serde_json::Value),
    ) -> Fixture {
        run_fixture_patched(BundlerChoice::Vite, true, edit, |build, project| {
            let map_path = build.join("assets/index-BHjrzQLH.js.map");
            if sources.is_empty() {
                std::fs::remove_file(&map_path).unwrap();
                return;
            }
            let map = serde_json::json!({
                "version": 3,
                "sources": sources
                    .iter()
                    .map(|source| format!("{project}/{source}"))
                    .collect::<Vec<_>>(),
                "names": [],
                "mappings": mappings,
            });
            std::fs::write(&map_path, serde_json::to_vec(&map).unwrap()).unwrap();
        })
    }

    const REGISTER_SOURCE: &str = "src/effects/register.js";

    /// Mappings that attribute all three generated lines of the fixture entry
    /// chunk (preload helper, bundled code, `sourceMappingURL` comment) to the
    /// first source. A segment ends with its generated line, so a map that
    /// covers the whole chunk needs one segment on each line.
    const FULLY_MAPPED_TO_FIRST_SOURCE: &str = "AAAA;AAAA;AAAA";
    /// Same coverage, attributed to the second source of the map.
    const FULLY_MAPPED_TO_SECOND_SOURCE: &str = "ACAA;AAAA;AAAA";

    #[test]
    fn vite_side_effect_retained_requires_final_generated_bytes() {
        // Listed in the final map's `sources` but without a generated segment.
        let listed = vite_with_entry_map(
            &[REGISTER_SOURCE, "src/main.js"],
            FULLY_MAPPED_TO_SECOND_SOURCE,
            |_| {},
        );
        assert_absent(
            &listed.output.analysis.diagnostics,
            "vite/side-effect-retained",
            REGISTER_SOURCE,
        );
        // Absent from the final map's `sources` altogether.
        let unlisted = vite_with_entry_map(&["src/main.js"], FULLY_MAPPED_TO_FIRST_SOURCE, |_| {});
        assert_absent(
            &unlisted.output.analysis.diagnostics,
            "vite/side-effect-retained",
            REGISTER_SOURCE,
        );
        // The collector still reports intermediate bytes for the module.
        let register = unlisted
            .collection
            .modules
            .iter()
            .find(|module| module.id.ends_with(REGISTER_SOURCE))
            .unwrap();
        assert!(register.rendered_length > 0 && register.included);
    }

    #[test]
    fn vite_side_effect_retained_keeps_modules_in_final_code() {
        let fixture = vite_with_entry_map(&[REGISTER_SOURCE], "AAAA", |value| {
            for module in value["modules"].as_array_mut().unwrap() {
                if module["id"]
                    .as_str()
                    .is_some_and(|id| id.ends_with(REGISTER_SOURCE))
                {
                    // No declared export: only then can the declaration confirm
                    // the finding, so the final-map check is what decides here.
                    module["exports"] = serde_json::json!([]);
                    module["usedExports"] = serde_json::json!([]);
                    module["moduleSideEffects"] = serde_json::json!("no-treeshake");
                }
            }
        });
        let finding = find(
            &fixture.output.analysis.diagnostics,
            "vite/side-effect-retained",
            REGISTER_SOURCE,
        )
        .expect("a module with final generated bytes stays reported");
        // Verified final emission keeps the declaration-specific status.
        assert_eq!(finding.status, EvidenceStatus::Confirmed);
        assert!(finding.summary.contains("declares side effects"));
        assert!(finding.summary.contains("before minification"));
        assert!(
            finding
                .evidence
                .iter()
                .any(|evidence| evidence.provenance == "final chunk source maps"
                    && evidence.status == EvidenceStatus::Confirmed
                    && evidence.detail.contains("generated bytes")),
            "{:?}",
            finding.evidence
        );
        assert!(
            finding
                .evidence
                .iter()
                .any(|evidence| evidence.detail.contains("intermediate Rolldown output")),
            "{:?}",
            finding.evidence
        );
    }

    #[test]
    fn vite_side_effect_retained_keeps_unknown_export_use_with_final_bytes() {
        // The fixture module declares exports, so export use stays Unknown and
        // the finding Inferred; the final-map check only adds its evidence.
        let fixture = vite_with_entry_map(&[REGISTER_SOURCE], "AAAA", |_| {});
        let finding = find(
            &fixture.output.analysis.diagnostics,
            "vite/side-effect-retained",
            REGISTER_SOURCE,
        )
        .expect("a module with final generated bytes stays reported");
        assert_eq!(finding.status, EvidenceStatus::Inferred);
        assert!(
            finding
                .evidence
                .iter()
                .any(|evidence| evidence.provenance == "final chunk source maps"
                    && evidence.status == EvidenceStatus::Confirmed),
            "{:?}",
            finding.evidence
        );
        assert!(
            finding
                .conditions
                .iter()
                .any(
                    |condition| condition.description == "No export of the module is used"
                        && condition.outcome == ConditionOutcome::Unknown
                ),
            "{:?}",
            finding.conditions
        );
    }

    #[test]
    fn vite_side_effect_retained_is_not_confirmed_without_a_final_map() {
        let fixture = vite_with_entry_map(&[], "", |value| {
            for module in value["modules"].as_array_mut().unwrap() {
                if module["id"]
                    .as_str()
                    .is_some_and(|id| id.ends_with(REGISTER_SOURCE))
                {
                    // No declared export: only then can the declaration confirm
                    // the finding, so the final-map check is what decides here.
                    module["exports"] = serde_json::json!([]);
                    module["usedExports"] = serde_json::json!([]);
                    module["moduleSideEffects"] = serde_json::json!("no-treeshake");
                }
            }
        });
        let finding = find(
            &fixture.output.analysis.diagnostics,
            "vite/side-effect-retained",
            REGISTER_SOURCE,
        )
        .expect("an unverifiable module is reported with an evidence limit");
        assert_eq!(finding.status, EvidenceStatus::Inferred);
        assert!(finding.summary.contains("could not be checked"));
        assert!(
            finding
                .evidence
                .iter()
                .any(|evidence| evidence.provenance == "final chunk source maps"
                    && evidence.status == EvidenceStatus::Inferred
                    && evidence.detail.contains("no usable source map")),
            "{:?}",
            finding.evidence
        );
        assert!(
            finding
                .conditions
                .iter()
                .any(|condition| condition.detail.contains("not verified")),
            "{:?}",
            finding.conditions
        );
    }

    /// Replaces every string equal to `old` with `new`, except `path` values:
    /// a collected module path never carries the import query.
    fn rename_module_id(value: &mut serde_json::Value, old: &str, new: &str) {
        match value {
            serde_json::Value::String(text) if text == old => *text = new.to_owned(),
            serde_json::Value::Array(items) => {
                items
                    .iter_mut()
                    .for_each(|item| rename_module_id(item, old, new));
            }
            serde_json::Value::Object(fields) => {
                for (key, item) in fields.iter_mut() {
                    if key != "path" {
                        rename_module_id(item, old, new);
                    }
                }
            }
            _ => {}
        }
    }

    #[derive(Debug, Clone, Copy, PartialEq, Eq)]
    enum Expected {
        /// Reported with verified final bytes.
        Emitted,
        /// Reported as unverified: Inferred evidence and an Unknown condition.
        Unverified,
        /// Not reported: fully mapped final output holds no code of the file.
        Absent,
    }

    struct EmissionCell {
        name: &'static str,
        /// Import query of the candidate module's identifier.
        module_query: Option<&'static str>,
        /// Fragment of the candidate module's identifier.
        module_fragment: Option<&'static str>,
        /// A second, not reported module that shares the candidate's file.
        sibling: bool,
        /// Identifier suffix of that sibling; `None` keeps the default (the
        /// plain identifier for a derived candidate, `?sibling` otherwise).
        sibling_suffix: Option<&'static str>,
        /// Source-map `sources` entries (project-relative, may carry a query).
        sources: &'static [&'static str],
        mappings: &'static str,
        expected: Expected,
    }

    #[test]
    fn vite_side_effect_retained_final_emission_cells() {
        const QUERIED: &str = "src/effects/register.js?vue&type=style";
        const OTHER_QUERY: &str = "src/effects/register.js?other";
        const FRAGMENT_NONE: &str = "src/effects/register.js?raw";
        const FRAGMENT_ONE: &str = "src/effects/register.js?raw#one";
        const FRAGMENT_TWO: &str = "src/effects/register.js?raw#two";
        const REGISTER_FRAGMENT_ONE: &str = "src/effects/register.js#one";
        const MAIN: &str = "src/main.js";
        let cells = [
            EmissionCell {
                name: "plain module, plain map source",
                module_query: None,
                module_fragment: None,
                sibling: false,
                sibling_suffix: None,
                sources: &[REGISTER_SOURCE],
                mappings: "AAAA",
                expected: Expected::Emitted,
            },
            EmissionCell {
                name: "queried module, map source repeats the query",
                module_query: Some("vue&type=style"),
                module_fragment: None,
                sibling: false,
                sibling_suffix: None,
                sources: &[QUERIED],
                mappings: "AAAA",
                expected: Expected::Emitted,
            },
            EmissionCell {
                name: "queried module, map omits the query",
                module_query: Some("vue&type=style"),
                module_fragment: None,
                sibling: false,
                sibling_suffix: None,
                sources: &[REGISTER_SOURCE],
                mappings: "AAAA",
                expected: Expected::Unverified,
            },
            EmissionCell {
                name: "plain module, map source carries a query",
                module_query: None,
                module_fragment: None,
                sibling: false,
                sibling_suffix: None,
                sources: &[QUERIED],
                mappings: "AAAA",
                expected: Expected::Unverified,
            },
            EmissionCell {
                name: "queried module, map source carries another query",
                module_query: Some("vue&type=style"),
                module_fragment: None,
                sibling: false,
                sibling_suffix: None,
                sources: &[OTHER_QUERY],
                mappings: "AAAA",
                expected: Expected::Unverified,
            },
            EmissionCell {
                name: "plain module shares its file, map omits the query",
                module_query: None,
                module_fragment: None,
                sibling: true,
                sibling_suffix: None,
                sources: &[REGISTER_SOURCE],
                mappings: "AAAA",
                expected: Expected::Unverified,
            },
            EmissionCell {
                name: "queried module shares its file, map repeats the query",
                module_query: Some("vue&type=style"),
                module_fragment: None,
                sibling: true,
                sibling_suffix: None,
                sources: &[QUERIED],
                mappings: "AAAA",
                expected: Expected::Emitted,
            },
            EmissionCell {
                name: "fragment module, map source repeats the fragment",
                module_query: Some("raw"),
                module_fragment: Some("one"),
                sibling: false,
                sibling_suffix: None,
                sources: &[FRAGMENT_ONE],
                mappings: "AAAA",
                expected: Expected::Emitted,
            },
            EmissionCell {
                name: "fragment module shares file and query, map repeats the fragment",
                module_query: Some("raw"),
                module_fragment: Some("one"),
                sibling: true,
                sibling_suffix: Some("?raw#two"),
                sources: &[FRAGMENT_ONE],
                mappings: "AAAA",
                expected: Expected::Emitted,
            },
            EmissionCell {
                name: "fragment module shares file and query, map names the sibling fragment",
                module_query: Some("raw"),
                module_fragment: Some("one"),
                sibling: true,
                sibling_suffix: Some("?raw#two"),
                sources: &[FRAGMENT_TWO],
                mappings: "AAAA",
                expected: Expected::Unverified,
            },
            EmissionCell {
                name: "fragment module, map omits the fragment",
                module_query: Some("raw"),
                module_fragment: Some("one"),
                sibling: false,
                sibling_suffix: None,
                sources: &[FRAGMENT_NONE],
                mappings: "AAAA",
                expected: Expected::Unverified,
            },
            EmissionCell {
                name: "queried module shares its query with a fragment module, map omits the \
                       fragment",
                module_query: Some("raw"),
                module_fragment: None,
                sibling: true,
                sibling_suffix: Some("?raw#two"),
                sources: &[FRAGMENT_NONE],
                mappings: "AAAA",
                expected: Expected::Unverified,
            },
            EmissionCell {
                name: "fragment-only module, map repeats the fragment",
                module_query: None,
                module_fragment: Some("one"),
                sibling: false,
                sibling_suffix: None,
                sources: &[REGISTER_FRAGMENT_ONE],
                mappings: "AAAA",
                expected: Expected::Emitted,
            },
            EmissionCell {
                name: "plain module, map source carries a fragment",
                module_query: None,
                module_fragment: None,
                sibling: false,
                sibling_suffix: None,
                sources: &[REGISTER_FRAGMENT_ONE],
                mappings: "AAAA",
                expected: Expected::Unverified,
            },
            EmissionCell {
                name: "file listed without a generated segment",
                module_query: None,
                module_fragment: None,
                sibling: false,
                sibling_suffix: None,
                sources: &[REGISTER_SOURCE, MAIN],
                mappings: FULLY_MAPPED_TO_SECOND_SOURCE,
                expected: Expected::Absent,
            },
            EmissionCell {
                name: "file missing from a fully mapped map",
                module_query: None,
                module_fragment: None,
                sibling: false,
                sibling_suffix: None,
                sources: &[MAIN],
                mappings: FULLY_MAPPED_TO_FIRST_SOURCE,
                expected: Expected::Absent,
            },
            EmissionCell {
                name: "file missing and generated bytes without a source mapping",
                module_query: None,
                module_fragment: None,
                sibling: false,
                sibling_suffix: None,
                sources: &[MAIN],
                mappings: "AAAA,E",
                expected: Expected::Unverified,
            },
            EmissionCell {
                name: "file listed without a segment and unmapped generated bytes",
                module_query: None,
                module_fragment: None,
                sibling: false,
                sibling_suffix: None,
                sources: &[REGISTER_SOURCE, MAIN],
                mappings: "ACAA,E",
                expected: Expected::Unverified,
            },
        ];
        for cell in cells {
            let fixture = vite_with_entry_map(cell.sources, cell.mappings, |value| {
                let base = value["modules"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .filter_map(|module| module["id"].as_str())
                    .find(|id| id.ends_with(REGISTER_SOURCE))
                    .unwrap()
                    .to_owned();
                if cell.sibling {
                    let mut sibling = value["modules"]
                        .as_array()
                        .unwrap()
                        .iter()
                        .find(|module| module["id"] == base.as_str())
                        .unwrap()
                        .clone();
                    let sibling_id = match cell.sibling_suffix {
                        Some(suffix) => format!("{base}{suffix}"),
                        None if cell.module_query.is_some() => base.clone(),
                        None => format!("{base}?sibling"),
                    };
                    sibling["id"] = sibling_id.into();
                    sibling["included"] = false.into();
                    value["modules"].as_array_mut().unwrap().push(sibling);
                }
                if cell.module_query.is_some() || cell.module_fragment.is_some() {
                    // Rename only the candidate, not a sibling that keeps the
                    // plain identifier.
                    let renamed = format!(
                        "{base}{}{}",
                        cell.module_query
                            .map_or_else(String::new, |query| format!("?{query}")),
                        cell.module_fragment
                            .map_or_else(String::new, |fragment| format!("#{fragment}")),
                    );
                    let modules = value["modules"].as_array_mut().unwrap();
                    let position = modules
                        .iter()
                        .position(|module| module["id"] == base.as_str())
                        .unwrap();
                    let mut candidate = modules[position].clone();
                    rename_module_id(&mut candidate, &base, &renamed);
                    modules[position] = candidate;
                }
            });
            let finding = find(
                &fixture.output.analysis.diagnostics,
                "vite/side-effect-retained",
                REGISTER_SOURCE,
            );
            match (cell.expected, finding) {
                (Expected::Absent, None) => {}
                (Expected::Absent, Some(finding)) => {
                    panic!("{}: unexpected finding {finding:?}", cell.name)
                }
                (_, None) => panic!("{}: finding suppressed", cell.name),
                (expected, Some(finding)) => {
                    let final_maps = finding
                        .evidence
                        .iter()
                        .find(|evidence| evidence.provenance == "final chunk source maps")
                        .unwrap_or_else(|| panic!("{}: no final map evidence", cell.name));
                    let remains = finding
                        .conditions
                        .iter()
                        .find(|condition| condition.description == "No rendered code remains")
                        .unwrap();
                    if expected == Expected::Emitted {
                        assert_eq!(
                            final_maps.status,
                            EvidenceStatus::Confirmed,
                            "{}",
                            cell.name
                        );
                        assert_eq!(remains.outcome, ConditionOutcome::Unmet, "{}", cell.name);
                    } else {
                        assert_eq!(final_maps.status, EvidenceStatus::Inferred, "{}", cell.name);
                        assert_eq!(remains.outcome, ConditionOutcome::Unknown, "{}", cell.name);
                        assert_eq!(finding.status, EvidenceStatus::Inferred, "{}", cell.name);
                        assert!(
                            finding.summary.contains("could not be checked"),
                            "{}",
                            cell.name
                        );
                    }
                }
            }
        }
    }

    #[test]
    fn cancellation_stops_chunk_measurement() {
        let fixture_dir = manifest_dir().join("tests/fixtures/rspack");
        let collection: Collection = serde_json::from_str(
            &std::fs::read_to_string(fixture_dir.join("collect.json")).unwrap(),
        )
        .unwrap();
        let always = || true;
        let output = analyze(&AnalysisInput {
            run_id: "test-run".to_owned(),
            project_root: PathBuf::from("/__bundlewhy_project__"),
            project_name: "p".to_owned(),
            bundler: BundlerChoice::Rspack,
            collection: &collection,
            build_dir: fixture_dir.join("build"),
            include_sources: false,
            started_at: String::new(),
            on_measuring: None,
            cancelled: Some(&always),
        });
        assert!(output.analysis.chunks.is_empty());
        // Rule analysis also stops: the shared detectors never run.
        assert!(
            !output
                .analysis
                .diagnostics
                .iter()
                .any(|diagnostic| diagnostic.rule.starts_with("minifier/")
                    || diagnostic.rule == "bundle/duplicate-package")
        );
    }

    #[test]
    fn computed_process_env_reads_in_rspack_output_are_reported() {
        let fixture = run_fixture(BundlerChoice::Rspack, |_| {});
        let build = fixture._dir.path().join("build/main.js");
        let code = std::fs::read_to_string(&build).unwrap();
        // Rewrite the dotted read in the emitted chunk to the computed form.
        let rewritten = code.replace(
            "process.env.BUNDLEWHY_FIXTURE_FLAG",
            "process.env[\"BUNDLEWHY_FIXTURE_FLAG\"]",
        );
        assert_ne!(code, rewritten);
        std::fs::write(&build, rewritten).unwrap();
        let collection: Collection = serde_json::from_str(
            &std::fs::read_to_string(manifest_dir().join("tests/fixtures/rspack/collect.json"))
                .unwrap()
                .replace("/__bundlewhy_project__", &fixture.project.to_string_lossy()),
        )
        .unwrap();
        let output = analyze(&AnalysisInput {
            run_id: "test-run".to_owned(),
            project_root: fixture.project.clone(),
            project_name: "bundlewhy-fixture-rspack-app".to_owned(),
            bundler: BundlerChoice::Rspack,
            collection: &collection,
            build_dir: fixture._dir.path().join("build"),
            include_sources: true,
            started_at: String::new(),
            on_measuring: None,
            cancelled: None,
        });
        assert!(
            output
                .analysis
                .diagnostics
                .iter()
                .any(
                    |diagnostic| diagnostic.rule == "minifier/unresolved-env-condition"
                        && diagnostic.title.contains("BUNDLEWHY_FIXTURE_FLAG")
                )
        );
    }

    #[test]
    fn data_only_env_reads_do_not_produce_a_branch_diagnostic() {
        for bundler in [BundlerChoice::Vite, BundlerChoice::Rspack] {
            let fixture = run_fixture_patched(
                bundler,
                true,
                |_| {},
                |build, _| {
                    let (file, guard, read) = match bundler {
                        BundlerChoice::Vite => (
                            "assets/index-BHjrzQLH.js",
                            "({}).BUNDLEWHY_FIXTURE_FLAG===`on`&&console.log(`flag branch`)",
                            "({}).BUNDLEWHY_FIXTURE_FLAG",
                        ),
                        BundlerChoice::Rspack => (
                            "main.js",
                            "\"on\"===process.env.BUNDLEWHY_FIXTURE_FLAG&&console.log(\"flag \
                             branch\")",
                            "process.env.BUNDLEWHY_FIXTURE_FLAG",
                        ),
                    };
                    let path = build.join(file);
                    let code = std::fs::read_to_string(&path).unwrap();
                    assert!(code.contains(guard));
                    let replacement = match bundler {
                        // Keep the Vite read at the same generated offset so
                        // its source mapping still points at the edited read.
                        BundlerChoice::Vite => format!("{read};console.log(`flag branch`)"),
                        BundlerChoice::Rspack => format!("console.log({read})"),
                    };
                    std::fs::write(&path, code.replace(guard, &replacement)).unwrap();
                    if bundler == BundlerChoice::Vite {
                        let map_path = build.join(format!("{file}.map"));
                        let mut map: serde_json::Value =
                            serde_json::from_str(&std::fs::read_to_string(&map_path).unwrap())
                                .unwrap();
                        let source_index = map["sources"]
                            .as_array()
                            .unwrap()
                            .iter()
                            .position(|source| source.as_str().unwrap().ends_with("/src/main.js"))
                            .unwrap();
                        let source = map["sourcesContent"][source_index].as_str().unwrap();
                        let branch = "if (process.env.BUNDLEWHY_FIXTURE_FLAG === \"on\") {\n  \
                                      console.log(\"flag branch\");\n}";
                        assert!(source.contains(branch));
                        map["sourcesContent"][source_index] = serde_json::json!(source.replace(
                            branch,
                            "console.log(process.env.BUNDLEWHY_FIXTURE_FLAG);\nconsole.log(\"flag \
                             branch\");\n// data-only read",
                        ));
                        std::fs::write(map_path, serde_json::to_vec(&map).unwrap()).unwrap();
                    }
                },
            );
            if bundler == BundlerChoice::Vite {
                assert!(fixture.output.sources.iter().any(|source| {
                    source.path == "src/main.js"
                        && source
                            .content
                            .contains("console.log(process.env.BUNDLEWHY_FIXTURE_FLAG);")
                }));
            }
            assert!(
                !fixture
                    .output
                    .analysis
                    .diagnostics
                    .iter()
                    .any(|diagnostic| { diagnostic.rule == "minifier/unresolved-env-condition" }),
                "{bundler:?} reported a branch for a data-only read"
            );
        }
    }

    /// Separators between a control keyword and its opening parenthesis. Every
    /// entry must keep the keyword guard of the equivalent plain input.
    const KEYWORD_SEPARATORS: [&str; 8] = [
        " ",
        " /* preserved */ ",
        " // preserved\n",
        " // preserved\r\n",
        " //\n",
        " // preserved \t \n",
        " // one\n// two\n /* three */ // four\n",
        "\n// preserved\n\n  ",
    ];

    /// Control keywords and the phrase their guard evidence must contain.
    const KEYWORD_PHRASES: [(&str, &str); 3] = [
        ("if", "in an if condition"),
        ("while", "in a while condition"),
        ("switch", "in a switch condition"),
    ];

    const RSPACK_FLAG_GUARD: &str =
        "\"on\"===process.env.BUNDLEWHY_FIXTURE_FLAG&&console.log(\"flag branch\")";
    const RSPACK_FLAG_READ: &str = "process.env.BUNDLEWHY_FIXTURE_FLAG";
    const VITE_FLAG_GUARD: &str = "({}).BUNDLEWHY_FIXTURE_FLAG===`on`&&";
    const VITE_FLAG_READ: &str = "({}).BUNDLEWHY_FIXTURE_FLAG";
    const VITE_CHUNK: &str = "assets/index-BHjrzQLH.js";

    fn env_diagnostics(output: &AnalysisOutput) -> Vec<&Diagnostic> {
        output
            .analysis
            .diagnostics
            .iter()
            .filter(|diagnostic| diagnostic.rule == "minifier/unresolved-env-condition")
            .collect()
    }

    /// Blanks the fixture's own guard with spaces, which keeps every generated
    /// offset (and so the source map) intact, and appends `tail` as the last
    /// statement. A tail at the end of the chunk is not followed by a comma,
    /// semicolon or newline, which would otherwise end the read's pending
    /// scope before the condition is classified.
    fn replace_flag_guard_with_tail(
        build: &Path,
        file: &str,
        guard: &str,
        tail: &str,
    ) -> (String, String) {
        let path = build.join(file);
        let code = std::fs::read_to_string(&path).unwrap();
        assert!(code.contains(guard));
        let mut rewritten = code.replace(guard, &" ".repeat(guard.len()));
        if !rewritten.ends_with('\n') {
            rewritten.push('\n');
        }
        let appended_at = rewritten.len();
        rewritten.push_str(tail);
        std::fs::write(&path, &rewritten).unwrap();
        (code, rewritten[..appended_at].to_owned())
    }

    #[test]
    fn rspack_comments_before_a_condition_keep_the_env_diagnostic() {
        for (keyword, phrase) in KEYWORD_PHRASES {
            for separator in KEYWORD_SEPARATORS {
                let fixture = run_fixture_patched(
                    BundlerChoice::Rspack,
                    true,
                    |_| {},
                    |build, _| {
                        let tail =
                            format!("{keyword}{separator}({RSPACK_FLAG_READ})run(\"flag branch\")");
                        replace_flag_guard_with_tail(build, "main.js", RSPACK_FLAG_GUARD, &tail);
                    },
                );
                let diagnostics = env_diagnostics(&fixture.output);
                assert_eq!(diagnostics.len(), 1, "{keyword} {separator:?}");
                let env = diagnostics[0];
                assert_eq!(env.status, EvidenceStatus::Inferred);
                assert!(env.title.contains("BUNDLEWHY_FIXTURE_FLAG"));
                assert!(
                    env.conditions.iter().any(|condition| {
                        condition.outcome == ConditionOutcome::Met
                            && condition.detail.contains(phrase)
                    }),
                    "{keyword} {separator:?}"
                );
            }
        }
    }

    #[test]
    fn rspack_parenthesized_keyword_operands_keep_the_env_diagnostic() {
        for tail in [
            "if (typeof(process.env.BUNDLEWHY_FIXTURE_FLAG) === \"undefined\") { use(); }",
            "if (typeof (process.env.BUNDLEWHY_FIXTURE_FLAG) === \"undefined\") { use(); }",
            "async function gate() { if (await(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); } }",
            "const gate = async () => { if (await(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); } \
             }",
            "export {}; if (await(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); }",
            "export /* c */ {}; if (await(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); }",
            "const o = { async \"load\"() { if (await(process.env.BUNDLEWHY_FIXTURE_FLAG)) { \
             use(); } } }",
            "class C { async #load() { if (await(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); } \
             } }",
        ] {
            let fixture = run_fixture_patched(
                BundlerChoice::Rspack,
                true,
                |_| {},
                |build, _| {
                    replace_flag_guard_with_tail(build, "main.js", RSPACK_FLAG_GUARD, tail);
                },
            );
            let diagnostics = env_diagnostics(&fixture.output);
            assert_eq!(diagnostics.len(), 1, "{tail}");
            let env = diagnostics[0];
            assert_eq!(env.status, EvidenceStatus::Inferred);
            assert!(env.title.contains("BUNDLEWHY_FIXTURE_FLAG"), "{tail}");
            assert!(
                env.conditions
                    .iter()
                    .any(|condition| condition.outcome == ConditionOutcome::Met),
                "{tail}"
            );
        }
    }

    #[test]
    fn rspack_keyword_lookalike_calls_are_not_branch_guards() {
        for tail in [
            "const value = typeof(process.env.BUNDLEWHY_FIXTURE_FLAG);",
            "while (typeof(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); }",
            "switch (typeof(process.env.BUNDLEWHY_FIXTURE_FLAG)) { default: use(); }",
            "const value = typeof (process.env.BUNDLEWHY_FIXTURE_FLAG);",
            "check(process.env.BUNDLEWHY_FIXTURE_FLAG);",
            "obj.typeof(process.env.BUNDLEWHY_FIXTURE_FLAG);",
            "mytypeof(process.env.BUNDLEWHY_FIXTURE_FLAG);",
            "if (check(typeof(process.env.BUNDLEWHY_FIXTURE_FLAG))) { use(); }",
            "if (obj.typeof(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); }",
            "if (mytypeof(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); }",
            "if (await(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); }",
            "const m = obj?.import.meta; if (await(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); }",
            "var async; async\nfunction gate(await) { if \
             (await(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); } }",
            "const g = async () => 1\nif (await(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); }",
            "function gate(await) { if (await(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); } }",
            "async function outer() { function gate() { if \
             (await(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); } } }",
            "if (obj.\u{3bb}typeof(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); }",
            "if (\u{3bb}typeof(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); }",
            "if (obj.\u{301}typeof(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); }",
            "if (\u{203f}typeof(process.env.BUNDLEWHY_FIXTURE_FLAG)) { use(); }",
        ] {
            let fixture = run_fixture_patched(
                BundlerChoice::Rspack,
                true,
                |_| {},
                |build, _| {
                    replace_flag_guard_with_tail(build, "main.js", RSPACK_FLAG_GUARD, tail);
                },
            );
            assert!(
                env_diagnostics(&fixture.output).is_empty(),
                "reported a branch for {tail:?}"
            );
        }
    }

    #[test]
    fn rspack_comment_separated_calls_are_not_branch_guards() {
        for tail in [
            "run // preserved\n(process.env.BUNDLEWHY_FIXTURE_FLAG)",
            "run // preserved\r\n(process.env.BUNDLEWHY_FIXTURE_FLAG)",
            "obj.if // preserved\n(process.env.BUNDLEWHY_FIXTURE_FLAG)",
            "obj.while // preserved\n(process.env.BUNDLEWHY_FIXTURE_FLAG)",
            "// if (process.env.BUNDLEWHY_FIXTURE_FLAG) run()\nrun()",
            "for // preserved\n(process.env.BUNDLEWHY_FIXTURE_FLAG;;) run()",
            "for // preserved\n(;;process.env.BUNDLEWHY_FIXTURE_FLAG) run()",
        ] {
            let fixture = run_fixture_patched(
                BundlerChoice::Rspack,
                true,
                |_| {},
                |build, _| {
                    replace_flag_guard_with_tail(build, "main.js", RSPACK_FLAG_GUARD, tail);
                },
            );
            assert!(
                env_diagnostics(&fixture.output).is_empty(),
                "reported a branch for {tail:?}"
            );
        }
        let fixture = run_fixture_patched(
            BundlerChoice::Rspack,
            true,
            |_| {},
            |build, _| {
                let tail = "for // preserved\n(;process.env.BUNDLEWHY_FIXTURE_FLAG;) run()";
                replace_flag_guard_with_tail(build, "main.js", RSPACK_FLAG_GUARD, tail);
            },
        );
        let diagnostics = env_diagnostics(&fixture.output);
        assert_eq!(diagnostics.len(), 1);
        assert!(diagnostics[0].conditions.iter().any(|condition| {
            condition.outcome == ConditionOutcome::Met
                && condition.detail.contains("in a for condition")
        }));
    }

    /// Replaces the Vite fixture's guard with `tail` appended at the end of the
    /// chunk. With `remap`, a source-map token is added so the appended
    /// `({}).FLAG` read maps to the original `process.env.FLAG` read; without
    /// it the appended read has no mapping to the original source.
    fn patch_vite_tail(build: &Path, tail: &str, remap: bool) {
        let (old_code, before) =
            replace_flag_guard_with_tail(build, VITE_CHUNK, VITE_FLAG_GUARD, tail);
        if !remap {
            return;
        }
        // The original guard position. The detector maps Vite's `{}` (one
        // column after the opening parenthesis) to the original read.
        let guard_offset = old_code.find(VITE_FLAG_GUARD).unwrap();
        let line_start = old_code[..guard_offset]
            .rfind('\n')
            .map_or(0, |index| index + 1);
        let guard_line = u32::try_from(old_code[..guard_offset].matches('\n').count()).unwrap();
        let guard_col =
            u32::try_from(old_code[line_start..guard_offset].encode_utf16().count()).unwrap();
        // The appended read's position: after the last newline of `before`
        // plus the text of the tail up to the read.
        let tail_offset = tail.find(VITE_FLAG_READ).unwrap();
        let read_line = u32::try_from(before.matches('\n').count()).unwrap()
            + u32::try_from(tail[..tail_offset].matches('\n').count()).unwrap();
        let read_col = u32::try_from(
            tail[..tail_offset]
                .rsplit('\n')
                .next()
                .unwrap()
                .encode_utf16()
                .count(),
        )
        .unwrap();

        let map_path = build.join(format!("{VITE_CHUNK}.map"));
        let old = sourcemap::SourceMap::from_slice(&std::fs::read(&map_path).unwrap()).unwrap();
        let read_token = old.lookup_token(guard_line, guard_col + 1).unwrap();
        let mut builder = sourcemap::SourceMapBuilder::new(old.get_file());
        builder.set_source_root(old.get_source_root());
        for (index, source) in old.sources().enumerate() {
            let id = builder.add_source(source);
            builder.set_source_contents(id, old.get_source_contents(index as u32));
        }
        for token in old.tokens() {
            builder.add_raw(
                token.get_dst_line(),
                token.get_dst_col(),
                token.get_src_line(),
                token.get_src_col(),
                token.has_source().then(|| token.get_src_id()),
                None,
                false,
            );
        }
        builder.add_raw(
            read_line,
            read_col + 1,
            read_token.get_src_line(),
            read_token.get_src_col(),
            Some(read_token.get_src_id()),
            None,
            false,
        );
        let mut out = Vec::new();
        builder.into_sourcemap().to_writer(&mut out).unwrap();
        std::fs::write(map_path, out).unwrap();
    }

    #[test]
    fn vite_comments_before_a_condition_keep_the_mapped_env_diagnostic() {
        for (keyword, phrase) in KEYWORD_PHRASES {
            for separator in KEYWORD_SEPARATORS {
                let fixture = run_fixture_patched(
                    BundlerChoice::Vite,
                    true,
                    |_| {},
                    |build, _| {
                        let tail =
                            format!("{keyword}{separator}({VITE_FLAG_READ})run(`flag branch`)");
                        patch_vite_tail(build, &tail, true);
                    },
                );
                let diagnostics = env_diagnostics(&fixture.output);
                assert_eq!(diagnostics.len(), 1, "{keyword} {separator:?}");
                assert_eq!(diagnostics[0].status, EvidenceStatus::Inferred);
                assert!(diagnostics[0].conditions.iter().any(|condition| {
                    condition.outcome == ConditionOutcome::Met && condition.detail.contains(phrase)
                }));
            }
        }
    }

    #[test]
    fn vite_unmapped_comment_separated_reads_still_need_the_original_read() {
        // Without a mapping to the original `process.env` read, the existing
        // confirmation requirement still suppresses the diagnostic.
        for separator in ["", " // preserved\n"] {
            let fixture = run_fixture_patched(
                BundlerChoice::Vite,
                true,
                |_| {},
                |build, _| {
                    let tail = format!("if{separator}({VITE_FLAG_READ})run(`flag branch`)");
                    patch_vite_tail(build, &tail, false);
                },
            );
            assert!(env_diagnostics(&fixture.output).is_empty(), "{separator:?}");
        }
    }

    #[test]
    fn vite_comment_separated_calls_are_not_branch_guards() {
        let fixture = run_fixture_patched(
            BundlerChoice::Vite,
            true,
            |_| {},
            |build, _| {
                let tail = format!("run // preserved\n({VITE_FLAG_READ})");
                patch_vite_tail(build, &tail, true);
            },
        );
        assert!(env_diagnostics(&fixture.output).is_empty());
    }

    /// Rewrites the fixture's guarded env branch into `replacement(read)` in
    /// the generated chunk, keeping the read at the same generated offset.
    fn patch_env_branch(build: &Path, bundler: BundlerChoice, replacement: &str) {
        let (file, guard, read) = match bundler {
            BundlerChoice::Vite => (
                "assets/index-BHjrzQLH.js",
                "({}).BUNDLEWHY_FIXTURE_FLAG===`on`&&console.log(`flag branch`)",
                "({}).BUNDLEWHY_FIXTURE_FLAG",
            ),
            BundlerChoice::Rspack => (
                "main.js",
                "\"on\"===process.env.BUNDLEWHY_FIXTURE_FLAG&&console.log(\"flag branch\")",
                "process.env.BUNDLEWHY_FIXTURE_FLAG",
            ),
        };
        let path = build.join(file);
        let code = std::fs::read_to_string(&path).unwrap();
        assert!(code.contains(guard));
        let replacement = replacement.replace("READ", read);
        std::fs::write(&path, code.replace(guard, &replacement)).unwrap();
    }

    #[test]
    fn optional_chain_env_reads_produce_a_nullish_short_circuit_finding() {
        for bundler in [BundlerChoice::Vite, BundlerChoice::Rspack] {
            let fixture = run_fixture_patched(
                bundler,
                true,
                |_| {},
                |build, _| {
                    patch_env_branch(build, bundler, "READ?.trim();console.log(`flag branch`)")
                },
            );
            let env = fixture
                .output
                .analysis
                .diagnostics
                .iter()
                .find(|diagnostic| diagnostic.rule == "minifier/unresolved-env-condition")
                .unwrap_or_else(|| panic!("{bundler:?} missed the optional-chain read"));
            assert_eq!(env.status, EvidenceStatus::Inferred);
            assert!(env.title.contains("BUNDLEWHY_FIXTURE_FLAG"));
            assert!(env.conditions.iter().any(|condition| {
                condition.outcome == ConditionOutcome::Met
                    && condition.detail.contains("short-circuits the chain")
            }));
        }
    }

    #[test]
    fn optional_chain_env_reads_stay_absent_without_minification_or_data_use() {
        let disabled = run_fixture_patched(
            BundlerChoice::Rspack,
            true,
            |value| value["minifyEnabled"] = serde_json::json!(false),
            |build, _| {
                patch_env_branch(
                    build,
                    BundlerChoice::Rspack,
                    "READ?.trim();console.log(`flag branch`)",
                )
            },
        );
        assert!(
            !disabled
                .output
                .analysis
                .diagnostics
                .iter()
                .any(|diagnostic| diagnostic.rule == "minifier/unresolved-env-condition")
        );
        for bundler in [BundlerChoice::Vite, BundlerChoice::Rspack] {
            let fixture = run_fixture_patched(
                bundler,
                true,
                |_| {},
                |build, _| {
                    patch_env_branch(
                        build,
                        bundler,
                        "obj?.method(READ);console.log(`flag branch`)",
                    )
                },
            );
            assert!(
                !fixture
                    .output
                    .analysis
                    .diagnostics
                    .iter()
                    .any(|diagnostic| diagnostic.rule == "minifier/unresolved-env-condition"),
                "{bundler:?} reported an optional-chain argument read"
            );
        }
    }

    #[test]
    fn vite_optional_chain_env_reads_need_a_mapped_process_env_source() {
        let fixture = run_fixture_patched(
            BundlerChoice::Vite,
            true,
            |_| {},
            |build, _| {
                patch_env_branch(
                    build,
                    BundlerChoice::Vite,
                    "READ?.trim();console.log(`flag branch`)",
                );
                let map_path = build.join("assets/index-BHjrzQLH.js.map");
                let mut map: serde_json::Value =
                    serde_json::from_str(&std::fs::read_to_string(&map_path).unwrap()).unwrap();
                let source_index = map["sources"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .position(|source| source.as_str().unwrap().ends_with("/src/main.js"))
                    .unwrap();
                let source = map["sourcesContent"][source_index].as_str().unwrap();
                // The mapped original line no longer reads process.env.
                let edited = source.replace("process.env.BUNDLEWHY_FIXTURE_FLAG", "unrelated.FLAG");
                assert_ne!(source, edited);
                map["sourcesContent"][source_index] = serde_json::json!(edited);
                std::fs::write(map_path, serde_json::to_vec(&map).unwrap()).unwrap();
            },
        );
        assert!(
            !fixture
                .output
                .analysis
                .diagnostics
                .iter()
                .any(|diagnostic| diagnostic.rule == "minifier/unresolved-env-condition")
        );
    }

    #[test]
    fn env_reads_inside_invalid_containers_do_not_produce_a_branch_diagnostic() {
        let guard = "\"on\"===process.env.BUNDLEWHY_FIXTURE_FLAG&&console.log(\"flag branch\")";
        let read = "process.env.BUNDLEWHY_FIXTURE_FLAG";
        let diagnostic_count = |replacement: String| {
            let fixture = run_fixture_patched(
                BundlerChoice::Rspack,
                true,
                |_| {},
                |build, _| {
                    let path = build.join("main.js");
                    let code = std::fs::read_to_string(&path).unwrap();
                    assert!(code.contains(guard));
                    std::fs::write(&path, code.replace(guard, &replacement)).unwrap();
                },
            );
            fixture
                .output
                .analysis
                .diagnostics
                .iter()
                .filter(|diagnostic| diagnostic.rule == "minifier/unresolved-env-condition")
                .count()
        };
        // The generated read only supplies a value inside an array literal,
        // nested array or function object; the following operator does not
        // depend on it.
        for container in [
            format!("[{read}]"),
            format!("[[{read}]]"),
            format!("(function(){{ return {read} }})"),
            format!("(function(){{ return {read}; }})"),
            format!("(()=>{read})"),
        ] {
            for suffix in [
                "&&console.log(\"flag branch\")",
                "||console.log(\"flag branch\")",
                "??console.log(\"flag branch\")",
                "?console.log(\"flag branch\"):0",
            ] {
                assert_eq!(
                    diagnostic_count(format!("{container}{suffix}")),
                    0,
                    "{container}{suffix}"
                );
            }
        }
        // A real branch inside the container remains detected.
        assert_eq!(
            diagnostic_count(format!("[{read}&&console.log(\"flag branch\")]")),
            1
        );
        assert_eq!(
            diagnostic_count(format!(
                "function run(){{{read}&&console.log(\"flag branch\")}}run()"
            )),
            1
        );
    }

    #[test]
    fn env_reads_keep_their_container_classification_after_keywords_regexes_and_members() {
        let guard = "\"on\"===process.env.BUNDLEWHY_FIXTURE_FLAG&&console.log(\"flag branch\")";
        let read = "process.env.BUNDLEWHY_FIXTURE_FLAG";
        let diagnostic_count = |replacement: String| {
            let fixture = run_fixture_patched(
                BundlerChoice::Rspack,
                true,
                |_| {},
                |build, _| {
                    let path = build.join("main.js");
                    let code = std::fs::read_to_string(&path).unwrap();
                    assert!(code.contains(guard));
                    std::fs::write(&path, code.replace(guard, &replacement)).unwrap();
                },
            );
            fixture
                .output
                .analysis
                .diagnostics
                .iter()
                .filter(|diagnostic| diagnostic.rule == "minifier/unresolved-env-condition")
                .count()
        };
        let suffix = "&&console.log(\"flag branch\")";
        // An array literal after `return` is a value, not a computed member.
        assert_eq!(
            diagnostic_count(format!("function run(){{return[{read}]{suffix}}}run()")),
            0
        );
        // Parentheses after `return` and `await` only group their operand.
        assert_eq!(
            diagnostic_count(format!("function run(){{return({read}){suffix}}}run()")),
            1
        );
        assert_eq!(
            diagnostic_count(format!(
                "async function run(){{await({read}){suffix}}}run()"
            )),
            1
        );
        // A bracket after a regular expression literal is a computed member.
        assert_eq!(diagnostic_count(format!("/x/[{read}]{suffix}")), 1);
        // An array literal that is indexed yields the environment value.
        assert_eq!(diagnostic_count(format!("[{read}][0]{suffix}")), 1);
        assert_eq!(diagnostic_count(format!("[{read}]{suffix}")), 0);
    }

    #[test]
    fn disabled_minification_suppresses_env_condition_findings() {
        let fixture = run_fixture(BundlerChoice::Rspack, |value| {
            value["minifyEnabled"] = serde_json::json!(false);
        });
        let diagnostics = &fixture.output.analysis.diagnostics;
        assert!(
            !diagnostics
                .iter()
                .any(|diagnostic| diagnostic.rule == "minifier/unresolved-env-condition")
        );
        assert!(
            diagnostics
                .iter()
                .any(|diagnostic| diagnostic.rule == "minifier/minification-disabled")
        );
    }

    #[test]
    fn disabled_minification_does_not_claim_a_non_default_minifier() {
        let fixture = run_fixture(BundlerChoice::Vite, |value| {
            value["minifyEnabled"] = serde_json::json!(false);
            value["defaultMinifier"] = serde_json::json!(false);
        });
        let problems = &fixture.output.analysis.problems;
        assert!(
            problems
                .iter()
                .any(|entry| entry.code == "BW_MINIFICATION_DISABLED")
        );
        assert!(
            !problems
                .iter()
                .any(|entry| entry.code == "BW_MINIFIER_NON_DEFAULT")
        );
    }

    /// Rspack fixture whose `evaluate.js` module is replaced by an included
    /// `icon.svg` module: a readable backing file that holds the eval source,
    /// no source-map `sourcesContent` and the engine's eval bailout. The
    /// closure can further edit the collection.
    fn rspack_svg_eval_fixture(
        edit: impl Fn(&mut serde_json::Value, &mut serde_json::Value),
    ) -> Fixture {
        let svg_dir = tempfile::tempdir().unwrap();
        let svg = svg_dir.path().join("icon.svg");
        let evaluate = manifest_dir()
            .join("../../packages/bundlewhy/fixtures/rspack-app/src/dynamic/evaluate.js");
        std::fs::write(&svg, std::fs::read(evaluate).unwrap()).unwrap();
        let svg_text = svg.to_string_lossy().replace('\\', "/");
        let fixture = run_fixture(BundlerChoice::Rspack, |value| {
            let mut module = serde_json::Value::Null;
            for entry in value["modules"].as_array_mut().unwrap() {
                if entry["name"] == "./src/dynamic/evaluate.js" {
                    entry["path"] = svg_text.clone().into();
                    module = entry.clone();
                }
            }
            edit(value, &mut module);
            for entry in value["modules"].as_array_mut().unwrap() {
                if entry["name"] == "./src/dynamic/evaluate.js" {
                    *entry = module.clone();
                }
            }
        });
        drop(svg_dir);
        fixture
    }

    #[test]
    fn rspack_svg_module_without_snapshot_is_an_unverified_coverage_gap() {
        let fixture = rspack_svg_eval_fixture(|_, _| {});
        let analysis = &fixture.output.analysis;
        assert_eq!(analysis.status, RunStatus::Partial);
        assert_eq!(
            analysis
                .problems
                .iter()
                .filter(|entry| entry.code == "BW_SOURCE_UNVERIFIED")
                .count(),
            1
        );
        assert_absent(
            &analysis.diagnostics,
            "minifier/direct-eval-blocks-mangling",
            "icon.svg",
        );
        let mangling = analysis
            .coverage
            .iter()
            .find(|entry| entry.category == OptimizationCategory::NameMangling)
            .unwrap();
        assert_eq!(mangling.state, CoverageState::Partial);
        assert!(mangling.note.contains("direct-eval"));
        // The gap is recorded without storing unverified text.
        let icon = fixture
            .output
            .sources
            .iter()
            .find(|source| source.path.ends_with("icon.svg"))
            .unwrap();
        assert_eq!(icon.revision_evidence, SourceRevisionEvidence::Unavailable);
        assert!(icon.content.is_empty());
    }

    #[test]
    fn vite_svg_module_without_snapshot_keeps_existence_only_evidence() {
        let svg_dir = tempfile::tempdir().unwrap();
        let svg = svg_dir.path().join("icon.svg");
        std::fs::write(&svg, "eval('not executable as imported')").unwrap();
        let svg_text = svg.to_string_lossy().replace('\\', "/");
        let fixture = run_fixture(BundlerChoice::Vite, |value| {
            for entry in value["modules"].as_array_mut().unwrap() {
                if entry["path"]
                    .as_str()
                    .is_some_and(|path| path.ends_with("/src/dynamic/evaluate.js"))
                {
                    entry["path"] = svg_text.clone().into();
                }
            }
        });
        let analysis = &fixture.output.analysis;
        assert!(
            !analysis
                .problems
                .iter()
                .any(|entry| entry.code == "BW_SOURCE_UNVERIFIED")
        );
        assert_ne!(
            analysis
                .coverage
                .iter()
                .find(|entry| entry.category == OptimizationCategory::NameMangling)
                .unwrap()
                .state,
            CoverageState::Partial
        );
        assert_absent(
            &analysis.diagnostics,
            "minifier/direct-eval-blocks-mangling",
            "icon.svg",
        );
        let icon = fixture
            .output
            .sources
            .iter()
            .find(|source| source.path.ends_with("icon.svg"));
        assert!(icon.is_none_or(|source| source.content.is_empty()));
    }

    #[test]
    fn rspack_asset_module_type_is_positive_asset_evidence() {
        for module_type in ["asset/resource", "asset/inline", "asset/source", "asset"] {
            let fixture = rspack_svg_eval_fixture(|_, module| {
                module["moduleType"] = module_type.into();
            });
            let analysis = &fixture.output.analysis;
            assert!(
                !analysis
                    .problems
                    .iter()
                    .any(|entry| entry.code == "BW_SOURCE_UNVERIFIED"),
                "{module_type}"
            );
            let mangling = analysis
                .coverage
                .iter()
                .find(|entry| entry.category == OptimizationCategory::NameMangling)
                .unwrap();
            assert_ne!(mangling.state, CoverageState::Partial, "{module_type}");
        }
    }

    /// Adds a second collected module with the same backing path as the
    /// `icon.svg` fixture module, as an Rspack executable loader or query
    /// variant of one resource does after the adapter strips its identifier.
    fn add_same_path_variant(
        value: &mut serde_json::Value,
        module: &serde_json::Value,
        module_type: &str,
    ) {
        let mut variant = module.clone();
        variant["id"] = "svg-loader!/project/src/icon.svg?component".into();
        variant["name"] = "./src/icon.svg?component".into();
        variant["moduleType"] = module_type.into();
        value["modules"].as_array_mut().unwrap().push(variant);
    }

    #[test]
    fn rspack_asset_evidence_does_not_exempt_an_executable_variant_of_the_path() {
        for variant_type in ["javascript/auto", "javascript/esm", "css/auto"] {
            let fixture = rspack_svg_eval_fixture(|value, module| {
                module["moduleType"] = "asset/resource".into();
                add_same_path_variant(value, module, variant_type);
            });
            let analysis = &fixture.output.analysis;
            assert_eq!(analysis.status, RunStatus::Partial, "{variant_type}");
            assert_eq!(
                analysis
                    .problems
                    .iter()
                    .filter(|entry| entry.code == "BW_SOURCE_UNVERIFIED")
                    .count(),
                1,
                "{variant_type}"
            );
            let mangling = analysis
                .coverage
                .iter()
                .find(|entry| entry.category == OptimizationCategory::NameMangling)
                .unwrap();
            assert_eq!(mangling.state, CoverageState::Partial, "{variant_type}");
        }
    }

    #[test]
    fn rspack_asset_variants_of_the_same_path_stay_exempt() {
        let fixture = rspack_svg_eval_fixture(|value, module| {
            module["moduleType"] = "asset/resource".into();
            add_same_path_variant(value, module, "asset/inline");
        });
        let analysis = &fixture.output.analysis;
        assert!(
            !analysis
                .problems
                .iter()
                .any(|entry| entry.code == "BW_SOURCE_UNVERIFIED")
        );
    }

    #[test]
    fn bundler_asset_paths_ignore_module_order_and_virtual_modules() {
        let module = |path: Option<&str>, module_type: Option<&str>| CollectedModule {
            path: path.map(str::to_owned),
            module_type: module_type.map(str::to_owned),
            ..CollectedModule::default()
        };
        let asset = module(Some("/p/a/../icon.svg"), Some("asset/resource"));
        let executable = module(Some("/p/icon.svg"), Some("javascript/auto"));
        let other = module(Some("/p/logo.svg"), Some("asset"));
        let virtual_module = module(None, Some("javascript/auto"));
        let untyped = module(Some("/p/untyped.svg"), None);
        let forward = bundler_asset_paths(&[
            asset.clone(),
            executable.clone(),
            other.clone(),
            virtual_module.clone(),
            untyped.clone(),
        ]);
        let reverse = bundler_asset_paths(&[untyped, virtual_module, other, executable, asset]);
        assert_eq!(forward, reverse);
        assert_eq!(forward, HashSet::from([PathBuf::from("/p/logo.svg")]));
    }

    #[test]
    fn rspack_svg_gap_keeps_minifier_precedence() {
        for (disabled, expected) in [
            (true, CoverageState::NotApplicable),
            (false, CoverageState::Unavailable),
        ] {
            let fixture = rspack_svg_eval_fixture(|value, _| {
                if disabled {
                    value["minifyEnabled"] = false.into();
                } else {
                    value["defaultMinifier"] = false.into();
                }
            });
            let mangling = fixture
                .output
                .analysis
                .coverage
                .iter()
                .find(|entry| entry.category == OptimizationCategory::NameMangling)
                .unwrap();
            assert_eq!(mangling.state, expected, "disabled={disabled}");
        }
    }

    #[test]
    fn rspack_css_modules_are_out_of_scope() {
        // A CSS module with an engine-reported bailout must not produce a
        // JavaScript diagnostic, while the same bailout on JavaScript does.
        let fixture = run_fixture(BundlerChoice::Rspack, |value| {
            for module in value["modules"].as_array_mut().unwrap() {
                if module["name"] == "./src/legacy/cjs.cjs" {
                    module["moduleType"] = serde_json::json!("css/auto");
                }
            }
        });
        let diagnostics = &fixture.output.analysis.diagnostics;
        assert_absent(
            diagnostics,
            "rspack/module-concatenation-bailout",
            "src/legacy/cjs.cjs",
        );
        assert!(
            find(
                diagnostics,
                "rspack/module-concatenation-bailout",
                "src/dynamic/evaluate.js"
            )
            .is_some()
        );
    }

    #[test]
    fn disabled_minification_is_a_confirmed_configuration_finding() {
        let fixture = run_fixture(BundlerChoice::Vite, |value| {
            value["minifyEnabled"] = serde_json::Value::Bool(false);
            value["configSummary"]["build.minify"] = serde_json::Value::String("false".to_owned());
        });
        let analysis = &fixture.output.analysis;
        let finding = find(
            &analysis.diagnostics,
            "minifier/minification-disabled",
            "vite.config.js",
        )
        .unwrap();
        assert_eq!(finding.status, EvidenceStatus::Confirmed);
        assert_absent(
            &analysis.diagnostics,
            "minifier/direct-eval-blocks-mangling",
            "",
        );
        for category in [
            OptimizationCategory::NameMangling,
            OptimizationCategory::DeadCodeElimination,
        ] {
            let entry = analysis
                .coverage
                .iter()
                .find(|entry| entry.category == category)
                .unwrap();
            assert_eq!(entry.state, CoverageState::NotApplicable, "{category:?}");
        }
    }

    #[test]
    fn unsupported_engine_version_is_recorded() {
        let fixture = run_fixture(BundlerChoice::Vite, |value| {
            value["bundlerVersion"] = serde_json::Value::String("8.0.0".to_owned());
        });
        let covered = fixture
            .output
            .analysis
            .coverage
            .iter()
            .filter(|entry| entry.state == CoverageState::Covered)
            .count();
        assert_eq!(
            covered, 0,
            "unsupported engines must not report covered categories"
        );
        let engine = fixture.output.analysis.engine.unwrap();
        assert_eq!(engine.support, EngineSupport::UnsupportedSelected);
        assert_eq!(engine.supported_versions, "vite 8.3.4");
    }

    /// One extra chunk appended to the Vite fixture: its emitted text, the
    /// collected module ids it lists and whether a map file is written.
    struct ExtraChunk {
        file: &'static str,
        code: &'static str,
        modules: Vec<&'static str>,
        map: ExtraMap,
    }

    #[derive(Clone, Copy)]
    enum ExtraMap {
        Absent,
        EmptyMappings,
    }

    const PRELOAD_HELPER: &str = "\u{0}vite/preload-helper.js";
    const ROLLDOWN_RUNTIME: &str = "\u{0}rolldown/runtime.js";
    const HELPER_CODE: &str = "var e=Object.create;export{e as t};\n";
    const APP_MODULE: &str = "/__bundlewhy_project__/src/lazy/common.js";

    fn helper_chunks() -> Vec<ExtraChunk> {
        vec![
            ExtraChunk {
                file: "assets/preload-helper-HclGiUj8.js",
                code: HELPER_CODE,
                modules: vec![PRELOAD_HELPER],
                map: ExtraMap::Absent,
            },
            ExtraChunk {
                file: "assets/rolldown-runtime-QTnfLwEv.js",
                code: HELPER_CODE,
                modules: vec![ROLLDOWN_RUNTIME],
                map: ExtraMap::Absent,
            },
        ]
    }

    fn vite_with_extra_chunks(
        extra: &[ExtraChunk],
        edit: impl Fn(&mut serde_json::Value),
    ) -> Fixture {
        fixture_with_extra_chunks(BundlerChoice::Vite, extra, edit)
    }

    /// Appends `extra` to the fixture of `bundler`. Every listed bundler-owned
    /// virtual helper is also registered as a pathless virtual module so the
    /// chunk shape is identical for both bundlers and only the bundler differs.
    fn fixture_with_extra_chunks(
        bundler: BundlerChoice,
        extra: &[ExtraChunk],
        edit: impl Fn(&mut serde_json::Value),
    ) -> Fixture {
        run_fixture_patched(
            bundler,
            true,
            |value| {
                for chunk in extra {
                    for id in &chunk.modules {
                        if !id.starts_with('\u{0}') {
                            continue;
                        }
                        let modules = value["modules"].as_array_mut().unwrap();
                        if modules.iter().any(|module| module["id"] == *id) {
                            continue;
                        }
                        modules.push(serde_json::json!({
                            "id": id,
                            "path": null,
                            "virtual": true,
                            "included": true,
                        }));
                    }
                }
                let chunks = value["chunks"].as_array_mut().unwrap();
                for chunk in extra {
                    chunks.push(serde_json::json!({
                        "file": chunk.file,
                        "isEntry": false,
                        "isDynamicEntry": false,
                        "facadeModuleId": null,
                        "modules": chunk.modules,
                    }));
                }
                edit(value);
            },
            |build, _| {
                for chunk in extra {
                    let path = build.join(chunk.file);
                    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
                    std::fs::write(&path, chunk.code).unwrap();
                    if let ExtraMap::EmptyMappings = chunk.map {
                        let map = serde_json::json!({
                            "version": 3,
                            "sources": [],
                            "names": [],
                            "mappings": "",
                        });
                        std::fs::write(
                            build.join(format!("{}.map", chunk.file)),
                            serde_json::to_vec(&map).unwrap(),
                        )
                        .unwrap();
                    }
                }
            },
        )
    }

    fn missing_map_problems(analysis: &Analysis) -> Vec<&Problem> {
        analysis
            .problems
            .iter()
            .filter(|entry| entry.code == "BW_SOURCE_MAP_MISSING")
            .collect()
    }

    #[test]
    fn virtual_only_chunks_without_maps_are_not_missing_maps() {
        let baseline = run_fixture(BundlerChoice::Vite, |_| {});
        let fixture = vite_with_extra_chunks(&helper_chunks(), |_| {});
        let analysis = &fixture.output.analysis;
        assert!(missing_map_problems(analysis).is_empty());
        assert_eq!(analysis.status, baseline.output.analysis.status);
        for (left, right) in analysis
            .coverage
            .iter()
            .zip(baseline.output.analysis.coverage.iter())
        {
            assert_eq!(left.state, right.state, "{:?}", left.category);
            assert_eq!(left.note, right.note, "{:?}", left.category);
        }
        assert!(
            !dead_code_coverage(analysis)
                .note
                .contains("no usable source map")
        );
    }

    #[test]
    fn source_backed_chunk_without_map_is_still_missing() {
        let fixture = vite_with_extra_chunks(&helper_chunks(), |value| {
            for chunk in value["chunks"].as_array_mut().unwrap() {
                if chunk["file"] == "assets/common-DMiq4WEy.js" {
                    chunk["map"] = "missing.map".into();
                }
            }
        });
        let analysis = &fixture.output.analysis;
        assert_eq!(analysis.status, RunStatus::Partial);
        let problems = missing_map_problems(analysis);
        assert_eq!(problems.len(), 1);
        assert!(problems[0].message.contains("assets/common-DMiq4WEy.js"));
        assert!(!problems[0].message.contains("preload-helper"));
        assert!(!problems[0].message.contains("rolldown-runtime"));
        let dce = dead_code_coverage(analysis);
        assert_eq!(dce.state, CoverageState::Partial);
        assert!(dce.note.contains("1 chunk(s) have no usable source map"));
    }

    #[test]
    fn look_alike_virtual_chunk_keeps_the_map_requirement() {
        let custom = vec![ExtraChunk {
            file: "assets/custom-AAAA.js",
            code: HELPER_CODE,
            modules: vec!["\u{0}vite/custom-runtime.js"],
            map: ExtraMap::Absent,
        }];
        let fixture = vite_with_extra_chunks(&custom, |_| {});
        let analysis = &fixture.output.analysis;
        assert_eq!(analysis.status, RunStatus::Partial);
        let problems = missing_map_problems(analysis);
        assert_eq!(problems.len(), 1);
        assert!(problems[0].message.contains("assets/custom-AAAA.js"));
    }

    #[test]
    fn mixed_virtual_and_source_chunk_keeps_the_map_requirement() {
        let mixed = vec![ExtraChunk {
            file: "assets/mixed-AAAA.js",
            code: HELPER_CODE,
            modules: vec![ROLLDOWN_RUNTIME, APP_MODULE],
            map: ExtraMap::Absent,
        }];
        let fixture = vite_with_extra_chunks(&mixed, |_| {});
        let analysis = &fixture.output.analysis;
        assert_eq!(analysis.status, RunStatus::Partial);
        let problems = missing_map_problems(analysis);
        assert_eq!(problems.len(), 1);
        assert!(problems[0].message.contains("assets/mixed-AAAA.js"));
    }

    #[test]
    fn virtual_only_helpers_keep_the_unsupported_engine_note() {
        let fixture = vite_with_extra_chunks(&helper_chunks(), |value| {
            value["bundlerVersion"] = serde_json::Value::String("8.0.0".to_owned());
        });
        let analysis = &fixture.output.analysis;
        assert!(missing_map_problems(analysis).is_empty());
        let dce = dead_code_coverage(analysis);
        assert_eq!(dce.state, CoverageState::Partial);
        assert!(dce.note.contains("unsupported engine"), "{}", dce.note);
        assert!(!dce.note.contains("no usable source map"));
    }

    #[test]
    fn mixed_helper_and_source_chunks_warn_only_for_source_backed_names() {
        let mut chunks = helper_chunks();
        chunks.extend([
            ExtraChunk {
                file: "assets/components-CVP2oapY.js",
                code: HELPER_CODE,
                modules: vec![APP_MODULE],
                map: ExtraMap::Absent,
            },
            ExtraChunk {
                file: "assets/shared-DGKhew9c.js",
                code: HELPER_CODE,
                modules: vec![APP_MODULE],
                map: ExtraMap::Absent,
            },
            ExtraChunk {
                file: "assets/components-DPIrGkNp.js",
                code: HELPER_CODE,
                modules: vec![APP_MODULE],
                map: ExtraMap::EmptyMappings,
            },
        ]);
        let fixture = vite_with_extra_chunks(&chunks, |value| {
            value["bundlerVersion"] = serde_json::Value::String("8.2.2".to_owned());
        });
        let analysis = &fixture.output.analysis;
        assert_eq!(analysis.status, RunStatus::Partial);
        let problems = missing_map_problems(analysis);
        assert_eq!(problems.len(), 1);
        assert_eq!(
            problems[0].message,
            "No usable source map for: assets/components-CVP2oapY.js, assets/shared-DGKhew9c.js, \
             assets/components-DPIrGkNp.js"
        );
        assert!(
            dead_code_coverage(analysis)
                .note
                .contains("3 chunk(s) have no usable source map")
        );
    }

    #[test]
    fn virtual_only_helper_chunks_stay_analyzed_with_generated_attribution() {
        let fixture = vite_with_extra_chunks(&helper_chunks(), |_| {});
        let analysis = &fixture.output.analysis;
        assert!(missing_map_problems(analysis).is_empty());
        // Suppressing the warning must not drop the chunk from the analysis:
        // detectors still scan its generated text and sizes are still
        // reported, with attribution honestly marked unavailable.
        for file in [
            "assets/preload-helper-HclGiUj8.js",
            "assets/rolldown-runtime-QTnfLwEv.js",
        ] {
            let chunk = analysis
                .chunks
                .iter()
                .find(|chunk| chunk.file == file)
                .unwrap();
            assert!(chunk.raw_bytes > 0);
            assert_eq!(
                chunk.attribution,
                crate::model::AttributionState::Unavailable
            );
        }
    }

    #[test]
    fn rspack_virtual_only_chunks_keep_the_map_requirement() {
        let baseline = run_fixture(BundlerChoice::Rspack, |_| {});
        let fixture = fixture_with_extra_chunks(BundlerChoice::Rspack, &helper_chunks(), |_| {});
        let analysis = &fixture.output.analysis;
        assert_eq!(analysis.status, RunStatus::Partial);
        let problems = missing_map_problems(analysis);
        assert_eq!(problems.len(), 1);
        assert_eq!(
            problems[0].message,
            "No usable source map for: assets/preload-helper-HclGiUj8.js, \
             assets/rolldown-runtime-QTnfLwEv.js"
        );
        let dce = dead_code_coverage(analysis);
        assert_eq!(dce.state, CoverageState::Partial);
        assert!(
            dce.note.contains("2 chunk(s) have no usable source map"),
            "{}",
            dce.note
        );
        assert_ne!(
            dce.state,
            dead_code_coverage(&baseline.output.analysis).state
        );
    }

    #[test]
    fn unsupported_bundler_collections_keep_the_map_requirement() {
        for bundler in ["", "webpack", "Vite", "rollup"] {
            let fixture = vite_with_extra_chunks(&helper_chunks(), |value| {
                value["bundler"] = serde_json::Value::String(bundler.to_owned());
            });
            let problems = missing_map_problems(&fixture.output.analysis);
            let reported = problems
                .iter()
                .any(|problem| problem.message.contains("preload-helper"));
            assert!(
                reported,
                "bundler {bundler:?} must keep the map requirement"
            );
        }
    }

    #[test]
    fn virtual_only_chunk_predicate_requires_a_vite_collection_with_pathless_helpers() {
        let collection =
            |bundler: &str, chunk_modules: Vec<&str>, modules: Vec<(&str, bool, Option<&str>)>| {
                Collection {
                    bundler: bundler.to_owned(),
                    chunks: vec![crate::collect::CollectedChunk {
                        file: "a.js".to_owned(),
                        modules: chunk_modules.into_iter().map(str::to_owned).collect(),
                        ..Default::default()
                    }],
                    modules: modules
                        .into_iter()
                        .map(|(id, is_virtual, path)| crate::collect::CollectedModule {
                            id: id.to_owned(),
                            is_virtual,
                            path: path.map(str::to_owned),
                            ..Default::default()
                        })
                        .collect(),
                    ..Default::default()
                }
            };
        let helper = (PRELOAD_HELPER, true, None);
        let runtime = (ROLLDOWN_RUNTIME, true, None);
        let plugin = ("\u{0}virtual:routes", true, None);
        let pathed = (PRELOAD_HELPER, true, Some("/x/helper.js"));
        let real = (PRELOAD_HELPER, false, None);
        let app = (APP_MODULE, false, Some("/x/common.js"));

        // Chunk shape x bundler: only a Vite collection may suppress.
        type Shape = (
            &'static str,
            Vec<&'static str>,
            Vec<(&'static str, bool, Option<&'static str>)>,
        );
        let shapes: Vec<(Shape, bool)> = vec![
            (
                ("vite helper only", vec![PRELOAD_HELPER], vec![helper]),
                true,
            ),
            (
                (
                    "rolldown runtime only",
                    vec![ROLLDOWN_RUNTIME],
                    vec![runtime],
                ),
                true,
            ),
            (
                (
                    "both helpers",
                    vec![PRELOAD_HELPER, ROLLDOWN_RUNTIME],
                    vec![helper, runtime],
                ),
                true,
            ),
            (
                (
                    "look-alike vite namespace id",
                    vec!["\u{0}vite/custom-runtime.js"],
                    vec![("\u{0}vite/custom-runtime.js", true, None)],
                ),
                false,
            ),
            (
                (
                    "look-alike rolldown namespace id",
                    vec!["\u{0}rolldown/plugin-helper.js"],
                    vec![("\u{0}rolldown/plugin-helper.js", true, None)],
                ),
                false,
            ),
            (
                (
                    "built-in helper plus look-alike",
                    vec![PRELOAD_HELPER, "\u{0}vite/custom-runtime.js"],
                    vec![helper, ("\u{0}vite/custom-runtime.js", true, None)],
                ),
                false,
            ),
            (("no listed modules", vec![], vec![helper]), false),
            (("id not collected", vec![PRELOAD_HELPER], vec![]), false),
            (
                ("plugin virtual", vec!["\u{0}virtual:routes"], vec![plugin]),
                false,
            ),
            (
                ("helper with file path", vec![PRELOAD_HELPER], vec![pathed]),
                false,
            ),
            (
                ("non-virtual module", vec![PRELOAD_HELPER], vec![real]),
                false,
            ),
            (
                (
                    "helper plus source",
                    vec![PRELOAD_HELPER, APP_MODULE],
                    vec![helper, app],
                ),
                false,
            ),
        ];
        for ((name, chunk_modules, modules), vite_expected) in shapes {
            for (bundler, choice) in [
                ("vite", Some(BundlerChoice::Vite)),
                ("rspack", Some(BundlerChoice::Rspack)),
                ("webpack", None),
                ("", None),
            ] {
                let built = collection(bundler, chunk_modules.clone(), modules.clone());
                assert_eq!(built.bundler_choice(), choice, "{bundler:?}");
                let expected = vite_expected && choice == Some(BundlerChoice::Vite);
                assert_eq!(
                    built.is_virtual_only_chunk("a.js"),
                    expected,
                    "{name} with bundler {bundler:?}"
                );
                assert!(!built.is_virtual_only_chunk("b.js"), "{name}: unknown file");
            }
        }
    }

    #[test]
    fn collected_engine_support_uses_the_exact_matrix_version() {
        for bundler in [BundlerChoice::Vite, BundlerChoice::Rspack] {
            let spec = bundler.spec();
            for (version, expected) in [
                (spec.version.to_owned(), EngineSupport::Supported),
                (
                    format!(" {}", spec.version),
                    EngineSupport::UnsupportedSelected,
                ),
                (
                    format!("{} ", spec.version),
                    EngineSupport::UnsupportedSelected,
                ),
                ("0.0.0".to_owned(), EngineSupport::UnsupportedSelected),
            ] {
                let collection = Collection {
                    bundler_package: spec.package.to_owned(),
                    bundler_version: version.clone(),
                    ..Collection::default()
                };
                let identity = engine_identity(bundler, &collection);
                assert_eq!(identity.support, expected, "{version:?}");
                assert_eq!(identity.bundler_version, version);
            }
        }
    }
}
