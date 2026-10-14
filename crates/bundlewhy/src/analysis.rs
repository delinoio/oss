//! Turns an adapter collection into an `Analysis` plus stored source files.

use std::path::{Path, PathBuf};

use crate::{
    chunks::{Chunk, measure},
    collect::Collection,
    engine::BundlerChoice,
    error::{ErrorCode, problem},
    model::{
        Analysis, CategoryCoverage, ConfigEntry, CoverageState, EngineIdentity, EngineSupport,
        OptimizationCategory, Problem, ProblemSeverity, ReportMode, RunStatus, SCHEMA_VERSION,
        SourceFile,
    },
    rules::{self, Context},
    sources::{SourceStore, classify},
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
}

pub fn engine_identity(bundler: BundlerChoice, collection: &Collection) -> EngineIdentity {
    let spec = bundler.spec();
    let support = if collection.bundler_version == spec.version {
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

fn coverage(
    collection: &Collection,
    missing_maps: usize,
    failed_chunks: usize,
    unreadable_sources: usize,
    unsupported_engine: Option<&str>,
) -> Vec<CategoryCoverage> {
    let rules = rules::rules_by_category(&collection.bundler);
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
                // The direct-eval detector reads module sources; an unreadable
                // source may hide a finding.
                OptimizationCategory::NameMangling if unreadable_sources > 0 => (
                    CoverageState::Partial,
                    format!(
                        "{unreadable_sources} source file(s) could not be read; direct-eval \
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

pub fn analyze(input: &AnalysisInput<'_>) -> AnalysisOutput {
    let collection = input.collection;
    let context_dir = collection
        .context
        .as_ref()
        .map_or_else(|| input.project_root.clone(), PathBuf::from);
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
    let missing_maps: Vec<String> = chunks
        .iter()
        .filter(|chunk| chunk.map.is_none())
        .map(|chunk| chunk.file.clone())
        .collect();
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

    let mut store = SourceStore::new(input.include_sources);
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
    let mut context = Context::new(
        collection,
        input.project_root.clone(),
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
    let root = input.project_root.clone();
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
    let sources = context.sources.into_files();
    let partial = problems.iter().any(|entry| {
        entry.code == ErrorCode::SourceMapMissing.as_str()
            || entry.code == ErrorCode::CollectionFailed.as_str()
            || entry.code == ErrorCode::SourceUnreadable.as_str()
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
            .map(|file| classify(&input.project_root, Path::new(file)).display)
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
            (collection.bundler_version != input.bundler.spec().version)
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
    AnalysisOutput { analysis, sources }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::{ConditionOutcome, Diagnostic, EvidenceStatus};

    const PROJECT_PLACEHOLDER: &str = "/__bundlewhy_project__";
    const BUILD_PLACEHOLDER: &str = "/__bundlewhy_build__";

    struct Fixture {
        _dir: tempfile::TempDir,
        project: PathBuf,
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
            output,
        }
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
        assert!(
            find(
                &fixture.output.analysis.diagnostics,
                "vite/side-effect-retained",
                "src/effects/register.js"
            )
            .is_some()
        );
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
}
