//! Diagnostic rules and the shared analysis context.
//!
//! Every rule has a fixture blocker and a counterexample (see the tests in
//! `crate::analysis`). Rules report `Confirmed` only for facts the engine
//! reported or that its output shows; everything derived by bundlewhy is
//! `Inferred` and says which evidence supports it.

mod await_context;
pub mod common;
pub mod export_usage;
pub mod rspack;
pub mod vite;

use std::{
    collections::{BTreeMap, HashMap, HashSet, VecDeque},
    path::{Path, PathBuf},
};

use crate::{
    chunks::{Chunk, ResourceIdentity, generated_excerpt, snippet_lines},
    collect::{CollectedModule, Collection},
    model::{
        CodeSnippet, Condition, CorrectiveAction, DependencyPath, Diagnostic, Evidence,
        EvidenceStatus, ExportUsage, LocationKind, OptimizationCategory, SourceLocation,
    },
    sources::{SourceStore, classify, sha256_hex},
};

/// Stable rule identifiers.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum RuleId {
    RspackSideEffectRetained,
    ViteSideEffectRetained,
    RspackModuleConcatenationBailout,
    ViteCommonjsWrapped,
    RspackModuleInMultipleChunks,
    ViteIneffectiveDynamicImport,
    DuplicatePackage,
    UnresolvedEnvCondition,
    MinificationDisabled,
    ViteTreeShakingDisabled,
    RspackUsedExportsDisabled,
    RspackSideEffectsDisabled,
    RspackProvidedExportsDisabled,
    RspackConcatenationDisabled,
    DirectEvalBlocksMangling,
}

impl RuleId {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::RspackSideEffectRetained => "rspack/side-effect-retained",
            Self::ViteSideEffectRetained => "vite/side-effect-retained",
            Self::RspackModuleConcatenationBailout => "rspack/module-concatenation-bailout",
            Self::ViteCommonjsWrapped => "vite/commonjs-wrapped",
            Self::RspackModuleInMultipleChunks => "rspack/module-in-multiple-chunks",
            Self::ViteIneffectiveDynamicImport => "vite/ineffective-dynamic-import",
            Self::DuplicatePackage => "bundle/duplicate-package",
            Self::UnresolvedEnvCondition => "minifier/unresolved-env-condition",
            Self::MinificationDisabled => "minifier/minification-disabled",
            Self::ViteTreeShakingDisabled => "vite/tree-shaking-disabled",
            Self::RspackUsedExportsDisabled => "rspack/used-exports-disabled",
            Self::RspackSideEffectsDisabled => "rspack/side-effects-disabled",
            Self::RspackProvidedExportsDisabled => "rspack/provided-exports-disabled",
            Self::RspackConcatenationDisabled => "rspack/concatenation-disabled",
            Self::DirectEvalBlocksMangling => "minifier/direct-eval-blocks-mangling",
        }
    }

    pub const fn category(self) -> OptimizationCategory {
        match self {
            Self::RspackSideEffectRetained
            | Self::ViteSideEffectRetained
            | Self::ViteTreeShakingDisabled
            | Self::RspackUsedExportsDisabled
            | Self::RspackSideEffectsDisabled
            | Self::RspackProvidedExportsDisabled => OptimizationCategory::TreeShaking,
            Self::RspackModuleConcatenationBailout
            | Self::RspackConcatenationDisabled
            | Self::ViteCommonjsWrapped => OptimizationCategory::ModuleConcatenation,
            Self::RspackModuleInMultipleChunks | Self::ViteIneffectiveDynamicImport => {
                OptimizationCategory::ChunkSplitting
            }
            Self::DuplicatePackage => OptimizationCategory::DuplicateModules,
            Self::UnresolvedEnvCondition | Self::MinificationDisabled => {
                OptimizationCategory::DeadCodeElimination
            }
            Self::DirectEvalBlocksMangling => OptimizationCategory::NameMangling,
        }
    }
}

pub struct Context<'a> {
    pub collection: &'a Collection,
    pub project_root: PathBuf,
    pub context_dir: PathBuf,
    pub project_name: String,
    pub engine: String,
    pub modules: HashMap<&'a str, &'a CollectedModule>,
    pub chunks: Vec<Chunk>,
    pub sources: SourceStore,
    /// Original files whose missing snapshots prevented Vite environment
    /// candidates from being confirmed.
    pub vite_env_source_gaps: HashSet<String>,
    /// Guarded Vite environment candidates, keyed by chunk file and generated
    /// byte offset, that sit in a segment of a usable source map with no
    /// source. They cannot be confirmed against any original text.
    pub vite_env_unmapped_candidates: HashSet<(String, usize)>,
    /// Polled cooperatively by long-running detectors; true stops them.
    pub cancelled: Option<&'a (dyn Fn() -> bool + Sync)>,
    /// Export usage edges grouped for traversal; built on first use.
    pub export_usage_index: std::cell::OnceCell<export_usage::ExportUsageIndex<'a>>,
    /// How many collected modules a source-map source of a given resource
    /// identity could stand for, built on first use; a source map cannot tell
    /// derived modules of one resource apart.
    resource_sharing: std::cell::OnceCell<ResourceSharing>,
}

/// Counts of collected module resources at each level of specificity.
///
/// A source-map source may omit the query or the fragment of its module, so the
/// less a module's identity says, the more siblings the same map source could
/// belong to:
/// - no query, no fragment: every module of the same file;
/// - a query, no fragment: every module of the same file and query (any
///   fragment);
/// - a fragment: only modules with the identical identity.
struct ResourceSharing {
    by_file: HashMap<PathBuf, usize>,
    by_file_query: HashMap<(PathBuf, Option<String>), usize>,
    by_identity: HashMap<ResourceIdentity, usize>,
}

impl ResourceSharing {
    fn new(identities: impl Iterator<Item = ResourceIdentity>) -> Self {
        let mut sharing = Self {
            by_file: HashMap::new(),
            by_file_query: HashMap::new(),
            by_identity: HashMap::new(),
        };
        for identity in identities {
            *sharing.by_file.entry(identity.file.clone()).or_default() += 1;
            *sharing
                .by_file_query
                .entry((identity.file.clone(), identity.query.clone()))
                .or_default() += 1;
            *sharing.by_identity.entry(identity).or_default() += 1;
        }
        sharing
    }

    /// Number of collected modules that a map source with exactly this
    /// identity could belong to (at least one: the module itself).
    fn count(&self, identity: &ResourceIdentity) -> usize {
        let count = if identity.fragment.is_some() {
            self.by_identity.get(identity)
        } else if identity.query.is_some() {
            self.by_file_query
                .get(&(identity.file.clone(), identity.query.clone()))
        } else {
            self.by_file.get(&identity.file)
        };
        count.copied().unwrap_or(1)
    }
}

/// What the emitted chunks show about one module's code in the final output.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FinalEmission {
    /// The final chunk source maps attribute generated bytes to the module.
    Emitted { bytes: u32 },
    /// Every chunk that lists the module has a fully mapped, usable source
    /// map, and none attributes a generated byte to the module's file:
    /// minification removed its code.
    Absent,
    /// Final emission cannot be verified; `reason` names the evidence limit.
    Unverified { reason: String },
}

/// Original or generated location plus a snippet for one diagnostic.
pub struct Located {
    pub location: Option<SourceLocation>,
    pub generated: Option<SourceLocation>,
    pub snippet: Option<CodeSnippet>,
}

impl<'a> Context<'a> {
    pub fn new(
        collection: &'a Collection,
        project_root: PathBuf,
        project_name: String,
        chunks: Vec<Chunk>,
        sources: SourceStore,
    ) -> Self {
        let context_dir = collection
            .context
            .as_ref()
            .map_or_else(|| project_root.clone(), PathBuf::from);
        Self::with_context(
            collection,
            project_root,
            context_dir,
            project_name,
            chunks,
            sources,
        )
    }

    pub fn with_context(
        collection: &'a Collection,
        project_root: PathBuf,
        context_dir: PathBuf,
        project_name: String,
        chunks: Vec<Chunk>,
        sources: SourceStore,
    ) -> Self {
        let modules = collection
            .modules
            .iter()
            .map(|module| (module.id.as_str(), module))
            .collect();
        let engine = format!(
            "{} {} + {} {}",
            collection.bundler_package,
            collection.bundler_version,
            collection.minifier,
            collection.minifier_version
        );
        Self {
            collection,
            project_root,
            context_dir,
            project_name,
            engine,
            modules,
            chunks,
            sources,
            vite_env_source_gaps: HashSet::new(),
            vite_env_unmapped_candidates: HashSet::new(),
            cancelled: None,
            export_usage_index: std::cell::OnceCell::new(),
            resource_sharing: std::cell::OnceCell::new(),
        }
    }

    /// What the final emitted chunks show about the module, from the chunks'
    /// source maps. Rolldown's `renderedLength` is measured before
    /// minification, so it cannot show what remains in the shipped code.
    ///
    /// Every outcome is decided here, so the evidence rules stay in one place:
    /// - A source-map source counts for the module only when file, import query
    ///   and fragment all match (`Comp.vue?vue&type=style` is not `Comp.vue`,
    ///   and `a.js?raw#one` is not `a.js?raw#two`), and a match that omits the
    ///   query or the fragment counts only when no other collected module
    ///   shares that less specific resource, because the map cannot tell
    ///   derived modules apart.
    /// - A chunk without a usable map, a differing query on the same file, and
    ///   generated bytes that no mapping attributes to a source are all
    ///   unverified, never proof of absence.
    pub fn final_emission(&self, module: &CollectedModule) -> FinalEmission {
        let Some(path) = module.path.as_deref() else {
            return FinalEmission::Unverified {
                reason: "the module has no file path to match against source map sources"
                    .to_owned(),
            };
        };
        if module.chunks.is_empty() {
            return FinalEmission::Unverified {
                reason: "Rolldown lists no output chunk for the module".to_owned(),
            };
        }
        let identity = ResourceIdentity::from_module(&module.id, path);
        let sharing_modules = self.resource_sharing().count(&identity);
        let mut exact: u32 = 0;
        let mut other_resource: u32 = 0;
        let mut unverified: Vec<String> = Vec::new();
        for file in &module.chunks {
            let Some(index) = self.chunk_index(file) else {
                unverified.push(format!("{file} could not be read"));
                continue;
            };
            let Some((per_source, unmapped)) = self.chunks[index].attribution() else {
                unverified.push(format!("{file} has no usable source map"));
                continue;
            };
            let (mut chunk_exact, mut chunk_other) = (0u32, 0u32);
            for (source, bytes) in per_source {
                let source = ResourceIdentity::from_source_path(source);
                if source.file != identity.file {
                    continue;
                }
                if source.query == identity.query && source.fragment == identity.fragment {
                    chunk_exact = chunk_exact.saturating_add(*bytes);
                } else {
                    chunk_other = chunk_other.saturating_add(*bytes);
                }
            }
            if chunk_exact == 0 && chunk_other == 0 && *unmapped > 0 {
                unverified.push(format!(
                    "{file} has {unmapped} generated bytes that no source mapping attributes"
                ));
            }
            exact = exact.saturating_add(chunk_exact);
            other_resource = other_resource.saturating_add(chunk_other);
        }
        let identifies_module = sharing_modules <= 1;
        tracing::debug!(
            module = %self.module_log_label(&module.id),
            exact_bytes = exact,
            other_resource_bytes = other_resource,
            sharing_modules,
            unverified_chunks = unverified.len(),
            "checked final emission of module"
        );
        if exact > 0 && identifies_module {
            FinalEmission::Emitted { bytes: exact }
        } else if exact > 0 {
            FinalEmission::Unverified {
                reason: format!(
                    "{sharing_modules} collected modules share the resource and the source map \
                     names it without a query or fragment that tells them apart, so it cannot say \
                     which module owns the {exact} generated bytes"
                ),
            }
        } else if other_resource > 0 {
            FinalEmission::Unverified {
                reason: format!(
                    "the source map attributes {other_resource} generated bytes to the same file \
                     under a different resource query or fragment, so it cannot say whether they \
                     belong to this module"
                ),
            }
        } else if unverified.is_empty() {
            FinalEmission::Absent
        } else {
            FinalEmission::Unverified {
                reason: unverified.join("; "),
            }
        }
    }

    fn resource_sharing(&self) -> &ResourceSharing {
        self.resource_sharing.get_or_init(|| {
            ResourceSharing::new(self.modules.values().filter_map(|module| {
                let path = module.path.as_deref()?;
                Some(ResourceIdentity::from_module(&module.id, path))
            }))
        })
    }

    /// True when the run was cancelled; detectors stop early and the caller
    /// discards the partial output.
    pub fn is_cancelled(&self) -> bool {
        self.cancelled.is_some_and(|cancelled| cancelled())
    }

    pub fn display_path(&self, path: &Path) -> String {
        classify(&self.project_root, path).display
    }

    pub fn module_display(&self, module: &CollectedModule) -> String {
        module.path.as_deref().map_or_else(
            || {
                module
                    .name
                    .clone()
                    .unwrap_or_else(|| crate::sources::strip_nul_prefix(&module.id).to_owned())
            },
            |path| self.display_path(Path::new(path)),
        )
    }

    /// Label of a module for logs. A bundler module identifier can carry
    /// inline loader options or resource queries, which can hold credentials
    /// or values of the local environment, so the raw identifier is never
    /// logged. A collected module is named by its display path (the resource
    /// path without query or loader options); any other identifier is named by
    /// a prefix of its SHA-256 digest, which identifies it within one log
    /// without revealing it.
    pub fn module_log_label(&self, module_id: &str) -> String {
        match self
            .modules
            .get(module_id)
            .and_then(|module| module.path.as_deref())
        {
            Some(path) => self.display_path(Path::new(path)),
            None => format!("module-{}", &sha256_hex(module_id.as_bytes())[..16]),
        }
    }

    pub fn module_display_by_id(&self, id: &str) -> String {
        self.modules.get(id).map_or_else(
            || crate::sources::strip_nul_prefix(id).to_owned(),
            |module| self.module_display(module),
        )
    }

    /// Dependency status: under node_modules or owned by another package.
    pub fn is_dependency(&self, module: &CollectedModule) -> bool {
        let in_node_modules = module
            .path
            .as_deref()
            .is_some_and(|path| path.replace('\\', "/").contains("/node_modules/"));
        let other_package = module
            .package_name
            .as_deref()
            .is_some_and(|name| name != self.project_name);
        in_node_modules || other_package
    }

    /// Shortest static importer chains from entries to `id` (at most three).
    pub fn dependency_paths(&self, id: &str) -> Vec<DependencyPath> {
        let mut paths = Vec::new();
        let mut queue: VecDeque<Vec<&str>> = VecDeque::from([vec![id]]);
        // Cycles are checked per candidate path so that distinct importer
        // chains reaching the same entry are all kept. The expansion budget
        // bounds work on large, highly connected graphs.
        let mut expansions = 0usize;
        while let Some(path) = queue.pop_front() {
            if paths.len() >= 3 || path.len() > 32 || expansions >= 20_000 {
                break;
            }
            expansions += 1;
            let last = *path.last().unwrap_or(&id);
            let Some(module) = self.modules.get(last) else {
                continue;
            };
            let importers: Vec<&str> = module
                .importers
                .iter()
                .map(String::as_str)
                .filter(|importer| self.modules.contains_key(importer))
                .collect();
            if module.is_entry || importers.is_empty() {
                if path.len() > 1 || module.is_entry {
                    let modules = path
                        .iter()
                        .rev()
                        .map(|entry| self.module_display_by_id(entry))
                        .collect();
                    paths.push(DependencyPath { modules });
                }
                continue;
            }
            for importer in importers {
                if !path.contains(&importer) {
                    let mut next = path.clone();
                    next.push(importer);
                    queue.push_back(next);
                }
            }
        }
        paths
    }

    /// Location inside an original module file (zero-based line).
    pub fn module_location(&mut self, module: &CollectedModule, line: u32, column: u32) -> Located {
        let display = self.module_display(module);
        let Some(path) = module.path.as_deref() else {
            return Located {
                location: Some(SourceLocation {
                    kind: LocationKind::Generated,
                    path: display,
                    ..SourceLocation::default()
                }),
                generated: None,
                snippet: None,
            };
        };
        self.original_location(Path::new(path), line, column)
    }

    pub fn original_location(&mut self, path: &Path, line: u32, column: u32) -> Located {
        self.original_location_with_coverage_gap(path, line, column, true)
    }

    /// Locates a configuration-only file without reducing detector coverage.
    pub fn original_location_without_coverage_gap(
        &mut self,
        path: &Path,
        line: u32,
        column: u32,
    ) -> Located {
        self.original_location_with_coverage_gap(path, line, column, false)
    }

    fn original_location_with_coverage_gap(
        &mut self,
        path: &Path,
        line: u32,
        column: u32,
        track_coverage_gap: bool,
    ) -> Located {
        let display = self.display_path(path);
        let root = self.project_root.clone();
        let added = if track_coverage_gap {
            self.sources.add_file(&root, path)
        } else {
            self.sources.add_file_without_coverage_gap(&root, path)
        };
        let (source_id, snippet) = match added {
            Some((id, Some(content))) => {
                let (start_line, text) = snippet_lines(&content, line);
                (
                    id,
                    Some(CodeSnippet {
                        kind: LocationKind::Original,
                        path: display.clone(),
                        start_line,
                        highlight_line: line + 1,
                        text,
                    }),
                )
            }
            // A non-text source keeps its identity but has no snippet.
            Some((id, None)) => (id, None),
            None => (String::new(), None),
        };
        Located {
            location: Some(SourceLocation {
                kind: LocationKind::Original,
                source_id,
                path: display,
                line: line + 1,
                column: column + 1,
            }),
            generated: None,
            snippet,
        }
    }

    /// Location of a byte offset in an emitted chunk, mapped to the original
    /// source when the chunk has a usable source map.
    pub fn chunk_location(&mut self, chunk_index: usize, offset: usize) -> Located {
        let chunk = &self.chunks[chunk_index];
        let (line, column) = chunk.position(offset);
        let generated = SourceLocation {
            kind: LocationKind::Generated,
            source_id: String::new(),
            path: chunk.file.clone(),
            line: line + 1,
            column: column + 1,
        };
        let excerpt = generated_excerpt(&chunk.code, offset);
        let file = chunk.file.clone();
        if let Some((path, src_line, src_column)) = chunk.original(line, column) {
            let mut located = self.original_location(&path, src_line, src_column);
            if located.snippet.is_none() {
                return Located {
                    location: Some(generated),
                    generated: None,
                    snippet: Some(CodeSnippet {
                        kind: LocationKind::Generated,
                        path: file,
                        start_line: line + 1,
                        highlight_line: line + 1,
                        text: excerpt,
                    }),
                };
            }
            located.generated = Some(generated);
            return located;
        }
        Located {
            location: Some(generated),
            generated: None,
            snippet: Some(CodeSnippet {
                kind: LocationKind::Generated,
                path: file,
                start_line: line + 1,
                highlight_line: line + 1,
                text: excerpt,
            }),
        }
    }

    pub fn chunk_index(&self, file: &str) -> Option<usize> {
        self.chunks.iter().position(|chunk| chunk.file == file)
    }

    /// Reads a module's original source text when available.
    pub fn module_source(&mut self, module: &CollectedModule) -> Option<String> {
        let path = module.path.as_deref()?;
        let root = self.project_root.clone();
        self.sources
            .add_file(&root, Path::new(path))
            .and_then(|(_, content)| content)
    }
}

pub struct DiagnosticDraft {
    pub rule: RuleId,
    pub status: EvidenceStatus,
    pub title: String,
    pub summary: String,
    pub conditions: Vec<Condition>,
    pub located: Located,
    pub module: String,
    pub dependency_paths: Vec<DependencyPath>,
    pub evidence: Vec<Evidence>,
    pub direction: String,
    pub safety: String,
    pub dependency: bool,
    /// Export-level usage evidence; set only by rules that support it.
    pub export_usage: Option<ExportUsage>,
}

impl DiagnosticDraft {
    pub fn build(self, engine: &str) -> Diagnostic {
        // The title carries the finding-specific subject (for example the
        // environment variable name) and the column separates findings that
        // share a line, so distinct findings never collapse into one ID.
        let key = format!(
            "{}|{}|{}|{}",
            self.rule.as_str(),
            self.module,
            self.located
                .location
                .as_ref()
                .map(|location| format!("{}:{}:{}", location.path, location.line, location.column))
                .unwrap_or_default(),
            self.title
        );
        Diagnostic {
            id: format!("d-{}", &sha256_hex(key.as_bytes())[..12]),
            category: self.rule.category(),
            rule: self.rule.as_str().to_owned(),
            status: self.status,
            title: self.title,
            summary: self.summary,
            conditions: self.conditions,
            location: self.located.location,
            generated_location: self.located.generated,
            snippet: self.located.snippet,
            module: self.module,
            dependency_paths: self.dependency_paths,
            evidence: self.evidence,
            corrective_action: Some(CorrectiveAction {
                direction: self.direction,
                safety: self.safety,
            }),
            engine: engine.to_owned(),
            dependency: self.dependency,
            export_usage: self.export_usage,
        }
    }
}

/// Runs every rule for the collection's engine.
pub fn run(context: &mut Context<'_>) -> Vec<Diagnostic> {
    let mut drafts = Vec::new();
    match context.collection.bundler.as_str() {
        "rspack" => rspack::run(context, &mut drafts),
        "vite" => vite::run(context, &mut drafts),
        other => tracing::warn!(bundler = other, "no rules for bundler"),
    }
    if !context.is_cancelled() {
        common::run(context, &mut drafts);
    }
    let engine = context.engine.clone();
    let mut diagnostics: Vec<Diagnostic> = drafts
        .into_iter()
        .map(|draft| draft.build(&engine))
        .collect();
    let mut seen = HashSet::new();
    diagnostics.retain(|diagnostic| seen.insert(diagnostic.id.clone()));
    diagnostics.sort_by(|left, right| {
        (left.category, left.dependency, &left.module, &left.rule).cmp(&(
            right.category,
            right.dependency,
            &right.module,
            &right.rule,
        ))
    });
    diagnostics
}

/// Rules registered for each category, used for coverage records.
pub fn rules_by_category(bundler: &str) -> BTreeMap<OptimizationCategory, Vec<RuleId>> {
    let mut map: BTreeMap<OptimizationCategory, Vec<RuleId>> = BTreeMap::new();
    let engine_rules: &[RuleId] = match bundler {
        "rspack" => &[
            RuleId::RspackSideEffectRetained,
            RuleId::RspackUsedExportsDisabled,
            RuleId::RspackSideEffectsDisabled,
            RuleId::RspackProvidedExportsDisabled,
            RuleId::RspackModuleConcatenationBailout,
            RuleId::RspackConcatenationDisabled,
            RuleId::RspackModuleInMultipleChunks,
        ],
        "vite" => &[
            RuleId::ViteSideEffectRetained,
            RuleId::ViteTreeShakingDisabled,
            RuleId::ViteCommonjsWrapped,
            RuleId::ViteIneffectiveDynamicImport,
        ],
        _ => &[],
    };
    for rule in engine_rules.iter().chain(&[
        RuleId::DuplicatePackage,
        RuleId::UnresolvedEnvCondition,
        RuleId::MinificationDisabled,
        RuleId::DirectEvalBlocksMangling,
    ]) {
        map.entry(rule.category()).or_default().push(*rule);
    }
    map
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::collect::{CollectedModule, Collection};

    fn module(id: &str, importers: &[&str], entry: bool) -> CollectedModule {
        CollectedModule {
            id: id.to_owned(),
            name: Some(id.to_owned()),
            is_entry: entry,
            importers: importers.iter().map(|value| (*value).to_owned()).collect(),
            included: true,
            ..CollectedModule::default()
        }
    }

    #[test]
    fn findings_on_one_line_get_distinct_ids() {
        let draft = |name: &str, column: u32| DiagnosticDraft {
            rule: RuleId::UnresolvedEnvCondition,
            status: EvidenceStatus::Inferred,
            title: format!("process.env.{name} is not a compile-time constant"),
            summary: String::new(),
            conditions: Vec::new(),
            located: Located {
                location: Some(SourceLocation {
                    path: "src/a.js".to_owned(),
                    line: 3,
                    column,
                    ..SourceLocation::default()
                }),
                generated: None,
                snippet: None,
            },
            module: "src/a.js".to_owned(),
            dependency_paths: Vec::new(),
            evidence: Vec::new(),
            direction: String::new(),
            safety: String::new(),
            dependency: false,
            export_usage: None,
        };
        let first = draft("A", 5).build("engine");
        let second = draft("B", 5).build("engine");
        let third = draft("A", 30).build("engine");
        assert_ne!(first.id, second.id);
        assert_ne!(first.id, third.id);
    }

    #[test]
    fn dependency_paths_keep_every_shortest_branch_and_skip_dynamic_edges() {
        let mut target = module("m", &["a", "b"], false);
        target.dynamic_importers = vec!["lazy".to_owned()];
        let collection = Collection {
            modules: vec![
                module("entry", &[], true),
                module("a", &["entry"], false),
                module("b", &["entry"], false),
                module("lazy", &["entry"], false),
                target,
            ],
            ..Collection::default()
        };
        let context = Context::new(
            &collection,
            PathBuf::from("/project"),
            "project".to_owned(),
            Vec::new(),
            SourceStore::new(false),
        );
        let paths: Vec<Vec<String>> = context
            .dependency_paths("m")
            .into_iter()
            .map(|path| path.modules)
            .collect();
        assert_eq!(
            paths,
            vec![vec!["entry", "a", "m"], vec!["entry", "b", "m"]]
        );
    }
}
