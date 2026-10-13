//! Diagnostic rules and the shared analysis context.
//!
//! Every rule has a fixture blocker and a counterexample (see the tests in
//! `crate::analysis`). Rules report `Confirmed` only for facts the engine
//! reported or that its output shows; everything derived by bundlewhy is
//! `Inferred` and says which evidence supports it.

pub mod common;
pub mod rspack;
pub mod vite;

use std::{
    collections::{BTreeMap, HashMap, HashSet, VecDeque},
    path::{Path, PathBuf},
};

use crate::{
    chunks::{Chunk, generated_excerpt, snippet_lines},
    collect::{CollectedModule, Collection},
    model::{
        CodeSnippet, Condition, CorrectiveAction, DependencyPath, Diagnostic, Evidence,
        EvidenceStatus, LocationKind, OptimizationCategory, SourceLocation,
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
            Self::DirectEvalBlocksMangling => "minifier/direct-eval-blocks-mangling",
        }
    }

    pub const fn category(self) -> OptimizationCategory {
        match self {
            Self::RspackSideEffectRetained | Self::ViteSideEffectRetained => {
                OptimizationCategory::TreeShaking
            }
            Self::RspackModuleConcatenationBailout | Self::ViteCommonjsWrapped => {
                OptimizationCategory::ModuleConcatenation
            }
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
        let modules = collection
            .modules
            .iter()
            .map(|module| (module.id.as_str(), module))
            .collect();
        let context_dir = collection
            .context
            .as_ref()
            .map_or_else(|| project_root.clone(), PathBuf::from);
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
        }
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
                    .unwrap_or_else(|| module.id.trim_start_matches('\0').to_owned())
            },
            |path| self.display_path(Path::new(path)),
        )
    }

    pub fn module_display_by_id(&self, id: &str) -> String {
        self.modules.get(id).map_or_else(
            || id.trim_start_matches('\0').to_owned(),
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
        let mut seen: HashSet<&str> = HashSet::from([id]);
        while let Some(path) = queue.pop_front() {
            if paths.len() >= 3 || path.len() > 32 {
                break;
            }
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
                if seen.insert(importer) {
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
        let display = self.display_path(path);
        let root = self.project_root.clone();
        let (source_id, snippet) = match self.sources.add_file(&root, path) {
            Some((id, content)) => {
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
            located.generated = Some(generated);
            if located.snippet.is_none() {
                located.snippet = Some(CodeSnippet {
                    kind: LocationKind::Generated,
                    path: file,
                    start_line: line + 1,
                    highlight_line: line + 1,
                    text: excerpt,
                });
            }
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
            .map(|(_, content)| content)
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
}

impl DiagnosticDraft {
    pub fn build(self, engine: &str) -> Diagnostic {
        let key = format!(
            "{}|{}|{}",
            self.rule.as_str(),
            self.module,
            self.located
                .location
                .as_ref()
                .map(|location| format!("{}:{}", location.path, location.line))
                .unwrap_or_default()
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
    common::run(context, &mut drafts);
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
            RuleId::RspackModuleConcatenationBailout,
            RuleId::RspackModuleInMultipleChunks,
        ],
        "vite" => &[
            RuleId::ViteSideEffectRetained,
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
