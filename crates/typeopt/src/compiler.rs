//! All access to the pinned compiler lives in this adapter. The pipeline only
//! receives plain candidates and byte spans, never compiler-owned identities.
use std::{
    collections::{BTreeMap, BTreeSet, HashMap},
    hash::{Hash, Hasher},
    rc::Rc,
    sync::{Arc, Mutex},
};

use anyhow::{Context as _, Result, bail, ensure};
use api::*;
use tsrs_compiler::{ProgramOptions, new_compiler_host, new_program};
use tsrs_core::P;
use tsrs_tsoptions::{ParseConfigHost, ParsedCommandLine, get_parsed_command_line_of_config_file};
use tsrs_vfs::{FS as Fs, bundled, wrapvfs::Replacements};
mod api;

use crate::{
    passes::PassSet,
    plan::{Phase, Plan},
};

mod analysis;
mod flow;
mod passes;

struct Host {
    fs: Arc<dyn Fs>,
    cwd: String,
}
impl ParseConfigHost for Host {
    fn fs(&self) -> &dyn Fs {
        &*self.fs
    }

    fn get_current_directory(&self) -> &str {
        &self.cwd
    }
}

/// Kind of a non-module analysis input recorded in the snapshot.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, serde::Serialize)]
#[serde(rename_all = "snake_case")]
pub enum InputKind {
    /// tsconfig and extended configuration files.
    Configuration,
    /// Declarations, JSON and other program files outside the application
    /// sources, including dependency declarations.
    Dependency,
    /// A path the compiler probed for existence and found missing. A file or
    /// directory that later appears at this path can change module
    /// resolution, so consumers must reject output once the path exists. The
    /// entry has no `sha256`.
    Absent,
    /// A path that exists but has the wrong kind for a failed probe, such as
    /// a directory named `dep.ts` probed with `file_exists`. Resolution is
    /// unaffected until the path becomes the kind in `Input::becomes`, so
    /// consumers must reject output only once that happens. The entry has no
    /// `sha256`.
    Mismatch,
}

/// The kind of filesystem entry a path must become to satisfy a failed probe.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, serde::Serialize)]
#[serde(rename_all = "snake_case")]
pub enum BecomesKind {
    /// A regular file (after following symlinks).
    File,
    /// A directory (after following symlinks).
    Directory,
}

/// The compiler filesystem call that reported a path as missing.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
enum ProbeKind {
    /// `file_exists`: only a regular file satisfies the probe.
    File,
    /// `directory_exists`: only a directory satisfies the probe.
    Directory,
    /// `stat`: anything that resolves satisfies the probe.
    Stat,
}

/// Missing non-`bundled:` paths and the probes that reported each as missing.
type MissLog = Arc<Mutex<BTreeMap<String, BTreeSet<ProbeKind>>>>;

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, serde::Serialize)]
pub struct Input {
    pub path: String,
    /// SHA-256 of the file content; `None` exactly for `InputKind::Absent`.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sha256: Option<String>,
    pub kind: InputKind,
    /// Set exactly for `InputKind::Mismatch`: the entry kind that would
    /// satisfy the failed probe and change resolution.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub becomes: Option<BecomesKind>,
}

/// Everything the analysis read: application sources by absolute path and
/// the other inputs whose change must invalidate the output.
pub struct Snapshot {
    pub sources: BTreeMap<String, String>,
    pub inputs: Vec<Input>,
}

pub struct Project {
    // Configurations and their parse hosts outlive every program built from them.
    config: P<ParsedCommandLine>,
    /// SHA-256 of every file the compiler read, by path.
    reads: Arc<Mutex<BTreeMap<String, String>>>,
    /// Non-`bundled:` paths whose existence the compiler probed and found
    /// missing (`file_exists`, `directory_exists` or `stat`).
    misses: MissLog,
    fs: Arc<dyn Fs>,
    cwd: String,
    version: Rc<CompilerProgram>,
    entries: Vec<String>,
    config_files: Vec<String>,
    size_options: CompilerOptions,
    /// Owns the synthetic configuration of a bundle-mode program.
    _bundle_config: Option<tempfile::TempDir>,
    /// Bundle mode: semantic diagnostic identities of each original chunk. A
    /// chunk can opt into checking with `// @ts-check`, and its unchecked
    /// types may be wrong, so only diagnostics that an edit adds count as
    /// failures.
    semantic_baseline: std::cell::RefCell<BTreeMap<String, Vec<String>>>,
    // Drop after every program, which can retain configuration pointers.
    region: tsrs_core::arena::Region,
}

fn deny_write() -> String {
    "typeopt compiler filesystem is read-only".into()
}

impl Project {
    pub fn load(path: &str, entries: Vec<String>) -> Result<Self> {
        Self::load_rooted(path, entries, Vec::new(), Some("/"))
    }

    /// Loads the program rooted at `entries`, `roots` and declaration files.
    fn load_rooted(
        path: &str,
        entries: Vec<String>,
        extra_roots: Vec<String>,
        root_dir_override: Option<&str>,
    ) -> Result<Self> {
        let region = tsrs_core::arena::Region::new(1 << 20);
        let region_scope = region.enter();
        let cwd = std::env::current_dir()?.to_string_lossy().into_owned();
        // Every file the compiler reads, including module-resolution
        // metadata such as package manifests, is part of the analysis
        // snapshot that consumers validate.
        let base: Arc<dyn Fs> = Arc::new(bundled::wrap_fs(tsrs_vfs::osvfs::fs()));
        let reads = Arc::new(Mutex::new(BTreeMap::<String, String>::new()));
        let misses = MissLog::default();
        let (reader, recorded) = (base.clone(), reads.clone());
        let fs = wrapvfs_wrap(
            base,
            Some(misses.clone()),
            Replacements {
                read_file: Some(Box::new(move |path| {
                    let text = reader.read_file(path);
                    if let Some(text) = &text
                        && !path.starts_with("bundled:")
                    {
                        recorded
                            .lock()
                            .expect("compiler read log")
                            .entry(path.to_owned())
                            .or_insert_with(|| sha256_hex(text.as_bytes()));
                    }
                    text
                })),
                write_file: Some(Box::new(|_, _| Err(deny_write()))),
                append_file: Some(Box::new(|_, _| Err(deny_write()))),
                remove: Some(Box::new(|_| Err(deny_write()))),
                chtimes: Some(Box::new(|_, _, _| Err(deny_write()))),
                ..Default::default()
            },
        );
        let host = P::new(Host {
            fs: fs.clone(),
            cwd: cwd.clone(),
        });
        let (config, errors) =
            get_parsed_command_line_of_config_file(path, None, None, host.get(), None);
        check_diagnostics(&errors)?;
        let config = config.ok_or_else(|| anyhow::anyhow!("cannot parse tsconfig"))?;
        check_diagnostics(&config.errors)?;
        ensure!(
            config.parsed_config.project_references.is_empty(),
            "project references are unsupported; supply a single application tsconfig"
        );
        ensure!(
            config.parsed_config.content_mappers.is_empty(),
            "content mappers are unsupported; typeopt does not execute code"
        );
        let mut config_files = vec![path.to_owned()];
        config_files.extend(config.extended_source_files().iter().cloned());
        config_files.sort();
        config_files.dedup();
        let mut options = config.compiler_options().unwrap().get().clone();
        ensure!(
            !options.no_check.is_true() && !options.no_resolve.is_true(),
            "noCheck and noResolve are unsupported; full checking and resolution are required"
        );
        ensure!(
            options.plugins.as_ref().is_none_or(Vec::is_empty)
                && !options.run_external_code.is_true(),
            "compiler plugins and runExternalCode are unsupported"
        );
        ensure!(
            options.out_file.is_empty() && !options.emit_declaration_only.is_true(),
            "outFile and emitDeclarationOnly are unsupported; typeopt emits separate ESM modules"
        );
        ensure!(
            matches!(
                options.module,
                ModuleKind::ESNext
                    | ModuleKind::ES2022
                    | ModuleKind::ES2020
                    | ModuleKind::ES2015
                    | ModuleKind::Preserve
            ),
            "set compilerOptions.module to an ESM mode (ESNext, ES2022, ES2020, ES2015 or \
             Preserve)"
        );
        ensure!(
            !matches!(options.jsx, JsxEmit::Preserve | JsxEmit::ReactNative),
            "JSX preservation is unsupported; select react-jsx or react"
        );
        // These are output controls owned by the CLI, not resolution or runtime
        // semantics. The original project never receives emitted files.
        options.no_emit = Tristate::False;
        options.declaration = Tristate::False;
        options.declaration_map = Tristate::False;
        options.incremental = Tristate::False;
        options.composite = Tristate::False;
        options.source_map = Tristate::True;
        options.inline_source_map = Tristate::False;
        options.inline_sources = Tristate::False;
        options.source_root.clear();
        options.map_root.clear();
        options.out_dir = "/__typeopt_output__".into();
        if let Some(root_dir) = root_dir_override {
            // Stage 1 publishes numbered modules instead of the compiler's
            // directory layout. Forced emit otherwise raises TS5011 for a
            // nested entry. Keep this until forced emit accepts such entries
            // without a rootDir override.
            options.root_dir = root_dir.into();
            tracing::debug!(root_dir = %options.root_dir, "overriding stage 1 output root");
        }
        // Size evaluation uses the same emitter settings without maps, so the
        // metric never includes source-map comments.
        let mut size_options = options.clone();
        size_options.source_map = Tristate::False;
        size_options.out_dir.clear();
        // Include ambient declarations discovered by tsconfig, but root runtime
        // traversal at the explicit entries rather than every include match.
        let mut roots = entries.clone();
        roots.extend(extra_roots);
        roots.extend(
            config
                .file_names()
                .iter()
                .filter(|p| p.ends_with(".d.ts") || p.ends_with(".d.mts") || p.ends_with(".d.cts"))
                .cloned(),
        );
        roots.sort();
        roots.dedup();
        let mut config = config.with_file_names(roots);
        config.set_compiler_options(P::new(options));
        let config = P::new(config);
        let version = build(config, fs.clone(), &cwd);
        drop(region_scope);
        Ok(Self {
            region,
            config,
            reads,
            misses,
            fs,
            cwd,
            version,
            entries,
            config_files,
            size_options,
            _bundle_config: None,
            semantic_baseline: Default::default(),
        })
    }

    /// Loads the JavaScript chunks of a bundler output directory as one closed
    /// ES module program. Chunks that no other chunk imports (HTML entries,
    /// workers) are public entries; every other chunk is internal.
    pub fn load_bundle(dir: &std::path::Path) -> Result<Self> {
        let chunks = bundle_chunks(dir)?;
        ensure!(
            !chunks.is_empty(),
            "bundle directory contains no JavaScript chunk"
        );
        let config_dir = tempfile::Builder::new()
            .prefix("typeopt-bundle-")
            .tempdir()
            .context("cannot create bundle configuration")?;
        let config = serde_json::json!({
            "compilerOptions": {
                "allowJs": true,
                "checkJs": false,
                "module": "esnext",
                "moduleResolution": "bundler",
                "moduleDetection": "force",
                "target": "esnext",
                "lib": ["esnext", "dom", "dom.iterable"],
                "types": [],
                "skipLibCheck": true,
                "rootDir": dir.to_string_lossy(),
            },
            "files": chunks,
        });
        let path = config_dir.path().join("tsconfig.json");
        std::fs::write(&path, serde_json::to_vec_pretty(&config)?)
            .context("cannot write bundle configuration")?;
        // Every chunk is a root: chunks that only HTML, workers, classic
        // scripts or non-literal loaders reach still belong to the program.
        let mut project = Self::load_rooted(
            &path.canonicalize()?.to_string_lossy(),
            Vec::new(),
            chunks.clone(),
            None,
        )?;
        let BundleImports {
            imported,
            unlinked,
            edges,
            computed: computed_import,
            bare,
            dynamic_packages,
            mut dynamic_files,
            worker_scripts,
        } = project.bundle_imported(dir);
        // Chunks known to run as ES modules: imported by another chunk or
        // loaded by an HTML module script. Others count as modules only with
        // ESM statements; classic scripts stay unchanged boundaries.
        let HtmlScripts {
            modules: scripts,
            classic,
            imported: from_html,
            all_public,
            import_map,
            source_reflection,
        } = html_scripts(dir)?;
        crate::app::set_bundle_html_reflection(source_reflection);
        // Without an import map, a package specifier would load a module
        // outside the bundle: the program is not closed.
        if let Some(specifier) = bare.filter(|_| !import_map) {
            bail!(
                "a bundle chunk imports a module outside the bundle directory (specifier \
                 {specifier:?}); bundle mode needs a self-contained bundle"
            );
        }
        if let Some(outside) = project.application_file_outside(dir, &dynamic_files, &edges) {
            bail!(
                "a bundle chunk imports a module outside the bundle directory, which cannot be \
                 published: {outside}"
            );
        }
        for (chunk, specifiers) in &dynamic_packages {
            for specifier in specifiers {
                tracing::warn!(
                    chunk,
                    specifier,
                    "a dynamic package import keeps the bundle chunk unchanged"
                );
            }
        }
        // Every bundle chunk is an explicit program root. A package
        // declaration can resolve back to one, but its diagnostics must still
        // protect that root from publishing malformed code.
        dynamic_files.retain(|file| !chunks.contains(file));
        crate::app::set_bundle_dynamic_packages(dynamic_packages.keys().cloned().collect());
        crate::app::set_bundle_dynamic_files(dynamic_files.iter().cloned().collect());
        let all_public = all_public || computed_import;
        if all_public {
            tracing::info!(
                "an HTML page or a computed import can reach any chunk; every chunk is a public \
                 entry"
            );
        }
        // Chunks that inline HTML scripts import are entries: their exports
        // are observed outside the program.
        let loaded_by_html = |chunk: &String| {
            all_public
                || scripts.contains(chunk)
                || from_html.contains(chunk)
                || unlinked.contains(chunk)
        };
        let mut modules = imported.clone();
        modules.extend(chunks.iter().filter(|c| loaded_by_html(c)).cloned());
        // A chunk that no chunk imports (an HTML entry, a worker or a file
        // referenced by URL) is an entry; HTML entries stay entries even when
        // lazy chunks import shared code back from them.
        let mut entries: Vec<String> = chunks
            .iter()
            .filter(|c| !imported.contains(*c) || loaded_by_html(c))
            .cloned()
            .collect();
        // Chunks that no entry reaches (a cycle of chunks that only import
        // each other, loaded from outside) are entries too: something
        // outside the program loads them.
        let mut reached: Paths = entries.iter().cloned().collect();
        let mut pending = entries.clone();
        while let Some(chunk) = pending.pop() {
            for target in edges.get(&chunk).into_iter().flatten() {
                if reached.insert(target.clone()) {
                    pending.push(target.clone());
                }
            }
        }
        entries.extend(chunks.iter().filter(|c| !reached.contains(*c)).cloned());
        entries.sort();
        entries.dedup();
        project.entries = entries;
        let html_classic = classic.clone();
        let mut classic = classic;
        classic.extend(worker_scripts);
        crate::app::set_bundle_modules(modules, classic);
        // Excluded chunks stay byte-for-byte unchanged and keep importing the
        // original names of what they reach. Whatever an excluded chunk can
        // import must therefore keep its name and content too: a renamed copy
        // would be evaluated next to the original, with a second module state.
        // Other boundary chunks (classic scripts and chunks with `eval`, `with`
        // or `importScripts`) are not edited by passes, but publication
        // rewrites their references to renamed chunks, so they import the
        // renamed copies and freeze nothing.
        let boundaries: Paths = chunks
            .iter()
            .filter(|chunk| {
                crate::app::bundle_excluded(chunk) || crate::app::bundle_dynamic_package(chunk)
            })
            .cloned()
            .collect();
        let reach = boundary_reach(&chunks, &boundaries, &edges);
        log_frozen_chunks(&reach, &scripts, &html_classic, &from_html);
        crate::app::set_bundle_frozen(first_boundary_credit(&reach));
        tracing::info!(
            chunks = chunks.len(),
            entries = project.entries.len(),
            "bundle program loaded"
        );
        project._bundle_config = Some(config_dir);
        Ok(project)
    }

    /// Bundle chunks that stay unchanged unchecked boundaries: excluded,
    /// classic scripts and chunks that use the global `eval`, `with` or
    /// `importScripts` (see `syntax_boundary_rule`).
    /// Publication leaves them byte-for-byte unchanged.
    pub fn bundle_boundaries(&self) -> Paths {
        let _scope = enter_program(Some(&self.version));
        source_files()
            .into_iter()
            .filter(|&file| is_application(file))
            .filter_map(|file| {
                let name = source_file_info(file).file_name.clone();
                let mut nodes = Vec::new();
                collect(file, &mut nodes);
                let esm = has_esm_statements(file) || uses_import_meta(&nodes);
                if crate::app::bundle_dynamic_package(&name) {
                    tracing::debug!(
                        chunk = %name,
                        rule = "dynamic-package-import",
                        "bundle chunk is a boundary"
                    );
                    return Some(name);
                }
                if crate::app::bundle_excluded_or_classic(&name, esm) {
                    tracing::debug!(
                        chunk = %name,
                        rule = "excluded-or-classic-script",
                        "bundle chunk is a boundary"
                    );
                    return Some(name);
                }
                if let Some(rule) = syntax_boundary_rule(&nodes) {
                    tracing::debug!(
                        chunk = %name,
                        rule = rule.as_str(),
                        "bundle chunk is a boundary"
                    );
                    return Some(name);
                }
                if crate::app::bundle_frozen_chunk(&name) {
                    tracing::debug!(
                        chunk = %name,
                        rule = "boundary-reachable",
                        "bundle chunk is a boundary"
                    );
                    return Some(name);
                }
                None
            })
            .collect()
    }

    /// A program file outside `dir` other than the bundled standard
    /// libraries: a chunk import resolved to a sibling file, a package or a
    /// declaration elsewhere. Declarations reached only through a dynamic
    /// package import are intentionally outside the closed runtime bundle;
    /// they are skipped here because that import remains an unresolved
    /// boundary. The set is the resolved dynamic dependency closure, rather
    /// than a lexical package root, so relative imports from an application
    /// chunk cannot hide under a dynamic package directory.
    fn application_file_outside(
        &self,
        dir: &std::path::Path,
        dynamic_files: &Paths,
        edges: &BTreeMap<String, Paths>,
    ) -> Option<String> {
        let _scope = enter_program(Some(&self.version));
        if let Some(outside) = edges
            .values()
            .flatten()
            .filter(|name| !name.starts_with("bundled:"))
            .find(|name| !std::path::Path::new(name).starts_with(dir))
        {
            return Some(outside.clone());
        }
        source_files()
            .into_iter()
            .map(|file| source_file_info(file).file_name.clone())
            .filter(|name| !name.starts_with("bundled:"))
            .filter(|name| !dynamic_files.contains(name))
            .find(|name| !std::path::Path::new(name).starts_with(dir))
    }

    /// Bundle chunks that another chunk imports with an import or export
    /// declaration or a literal dynamic `import()`, and the subset that is
    /// imported through a specifier with a query, fragment or `%` escape.
    /// The compiler cannot link such a specifier to the chunk, so those
    /// chunks are public: what the importer reads stays unseen.
    fn bundle_imported(&self, dir: &std::path::Path) -> BundleImports {
        let _scope = enter_program(Some(&self.version));
        let (mut imported, mut public) = (Paths::new(), Paths::new());
        let mut edges = BTreeMap::<String, Paths>::new();
        let mut computed = false;
        let mut bare = None;
        let mut dynamic_packages = BTreeMap::<String, Paths>::new();
        let mut dynamic_entries = Paths::new();
        let (mut worker_scripts, mut worker_computed) = (Paths::new(), false);
        let mut non_esm = Paths::new();
        let files = source_files();
        for file in files.iter().copied() {
            if !is_application(file) {
                continue;
            }
            let name = source_file_info(file).file_name.clone();
            let base = std::path::Path::new(&name)
                .parent()
                .map(std::path::Path::to_path_buf)
                .unwrap_or_default();
            let mut nodes = Vec::new();
            collect(file, &mut nodes);
            if !has_esm_statements(file) && !uses_import_meta(&nodes) {
                non_esm.insert(name.clone());
            }
            for node in nodes {
                // `importScripts(…)` loads its targets as classic scripts.
                if node.kind() == SyntaxKind::CallExpression
                    && is_import_scripts_callee(node.expression())
                {
                    for argument in node.arguments().iter() {
                        let literal = matches!(
                            argument.kind(),
                            SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
                        );
                        if !literal {
                            worker_computed = true;
                            continue;
                        }
                        let path = percent_decode(
                            argument.text().split(['?', '#']).next().unwrap_or_default(),
                        );
                        let resolved = if is_root_relative(argument.text()) {
                            resolve_root_relative(dir, argument.text())
                        } else {
                            base.join(&path).canonicalize().ok()
                        };
                        if let Some(resolved) = resolved {
                            worker_scripts.insert(resolved.to_string_lossy().into_owned());
                        }
                    }
                    continue;
                }
                let commonjs_require = node.kind() == SyntaxKind::CallExpression
                    && with_type_checker_for_file(file, |checker| {
                        is_require_callee(checker, node.expression())
                    });
                let dynamic = node.kind() == SyntaxKind::CallExpression
                    && node.expression().kind() == SyntaxKind::ImportKeyword;
                let specifier = match node.kind() {
                    SyntaxKind::ImportDeclaration | SyntaxKind::ExportDeclaration => {
                        node.module_specifier()
                    }
                    SyntaxKind::CallExpression
                        if node.expression().kind() == SyntaxKind::ImportKeyword =>
                    {
                        node.arguments().iter().next().unwrap_or(Node::NIL)
                    }
                    SyntaxKind::CallExpression if commonjs_require => {
                        node.arguments().iter().next().unwrap_or(Node::NIL)
                    }
                    _ => continue,
                };
                if specifier.is_nil()
                    || !matches!(
                        specifier.kind(),
                        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
                    )
                {
                    // A computed `import(…)` can load any chunk.
                    if node.kind() == SyntaxKind::CallExpression {
                        computed = true;
                    }
                    continue;
                }
                let text = specifier.text();
                // Root-relative: resolve under the bundle root, dropping
                // leading segments of a deployment base path when needed. The
                // compiler cannot link it, so the target is public.
                if is_root_relative(text) {
                    match resolve_root_relative(dir, text) {
                        Some(resolved) => {
                            let resolved = resolved.to_string_lossy().into_owned();
                            public.insert(resolved.clone());
                            edges
                                .entry(name.clone())
                                .or_default()
                                .insert(resolved.clone());
                            imported.insert(resolved);
                        }
                        None if bare.is_none() => bare = Some(text.to_owned()),
                        None => {}
                    }
                    continue;
                }
                if !(text.starts_with("./") || text.starts_with("../")) {
                    if text.starts_with("//") {
                        // Protocol-relative URLs are external URL references,
                        // not package specifiers eligible for dynamic-package
                        // boundary treatment.
                        if bare.is_none() {
                            bare = Some(text.to_owned());
                        }
                        continue;
                    }
                    // A package specifier names a module outside the bundle.
                    if !text.contains(':') {
                        if dynamic {
                            dynamic_packages
                                .entry(name.clone())
                                .or_default()
                                .insert(text.to_owned());
                            let target = with_type_checker_for_file(file, |checker| {
                                resolved_module_file(checker, specifier)
                            })
                            .or_else(|| resolve_dynamic_package_entry(&name, text));
                            if let Some(target) = target {
                                dynamic_entries.insert(target);
                            }
                        } else if bare.is_none() {
                            bare = Some(text.to_owned());
                        }
                    }
                    continue;
                }
                // URL semantics: queries and fragments do not name files.
                let path = percent_decode(text.split(['?', '#']).next().unwrap_or_default());
                let candidate = base.join(&path);
                let resolved = if candidate.extension().is_some() {
                    candidate
                        .canonicalize()
                        .ok()
                        .filter(|path| path.is_file())
                        .map(|path| path.to_string_lossy().into_owned())
                        .or_else(|| {
                            with_type_checker_for_file(file, |checker| {
                                resolved_module_file(checker, specifier)
                            })
                        })
                        .or_else(|| resolve_module_path(&candidate))
                } else {
                    // Bundle edges represent runtime chunks. Prefer a runtime
                    // candidate before TypeScript's declaration-first result,
                    // which can otherwise link `dep.d.ts` instead of `dep.js`.
                    resolve_runtime_module_path(&candidate)
                        .or_else(|| {
                            with_type_checker_for_file(file, |checker| {
                                resolved_module_file(checker, specifier)
                            })
                        })
                        .or_else(|| resolve_module_path(&candidate))
                };
                if let Some(resolved) = resolved {
                    if path != text {
                        public.insert(resolved.clone());
                    }
                    edges
                        .entry(name.clone())
                        .or_default()
                        .insert(resolved.clone());
                    imported.insert(resolved);
                }
            }
        }
        // A computed `importScripts` target can be any chunk that a classic
        // script can parse: every chunk without ESM syntax.
        if worker_computed {
            worker_scripts.extend(non_esm.iter().cloned());
        }
        let dynamic_files = dynamic_package_files(&files, &dynamic_entries, &edges);
        BundleImports {
            imported,
            unlinked: public,
            edges,
            computed,
            bare,
            dynamic_packages,
            dynamic_files,
            worker_scripts,
        }
    }

    /// Checks the original program and records the analysis snapshot.
    pub fn snapshot(&self) -> Result<Snapshot> {
        let _scope = enter_program(Some(&self.version));
        check_diagnostics(&get_config_file_parsing_diagnostics())?;
        check_diagnostics(&get_program_diagnostics())?;
        check_diagnostics(&get_global_diagnostics())?;
        let files = source_files();
        for (index, &file) in files.iter().enumerate() {
            if index % 32 == 0 {
                tracing::debug!(
                    checked = index,
                    total = files.len(),
                    "checking program files"
                );
            }
            crate::app::check_cancelled()?;
            let file_name = source_file_info(file).file_name.clone();
            if !(crate::app::bundle_mode() && crate::app::bundle_dynamic_file(&file_name)) {
                check_diagnostics(&get_syntactic_diagnostics(file))?;
            }
            if crate::app::bundle_mode() {
                self.semantic_baseline.borrow_mut().insert(
                    source_file_info(file).file_name.clone(),
                    diagnostic_identities(&get_semantic_diagnostics(file)),
                );
            } else {
                check_diagnostics(&get_semantic_diagnostics(file))?;
            }
        }
        let mut sources = BTreeMap::new();
        let mut inputs = Vec::new();
        for path in &self.config_files {
            let text = std::fs::read(path).context("cannot read project configuration")?;
            inputs.push(Input {
                path: path.clone(),
                sha256: Some(sha256_hex(&text)),
                kind: InputKind::Configuration,
                becomes: None,
            });
        }
        for file in files {
            crate::app::check_cancelled()?;
            let name = source_file_info(file).file_name.clone();
            ensure!(
                !name.ends_with(".cts")
                    || name.ends_with(".d.cts")
                    || is_source_file_from_external_library(file),
                "CommonJS TypeScript (.cts) is unsupported; use an ESM source module"
            );
            if !is_application(file) {
                if !name.starts_with("bundled:") {
                    inputs.push(Input {
                        sha256: Some(sha256_hex(source_file_text(file).as_bytes())),
                        path: name,
                        kind: InputKind::Dependency,
                        becomes: None,
                    });
                }
                continue;
            }
            sources.insert(name, source_file_text(file).to_string());
            // Check forbidden constructs even in untransformed application JS.
            let mut nodes = Vec::new();
            collect(file, &mut nodes);
            for node in &nodes {
                // A bundle chunk with eval or with stays an unchecked
                // boundary instead (see `uses_eval_or_with`).
                ensure!(
                    crate::app::bundle_mode() || node.kind() != SyntaxKind::WithStatement,
                    "with is unsupported"
                );
                ensure!(
                    crate::app::bundle_mode()
                        || !(node.kind() == SyntaxKind::Identifier && node.text() == "eval"),
                    "eval is unsupported (including aliases); remove it before optimizing"
                );
                // module: Preserve emits these forms as require/module.exports,
                // which breaks the ESM output contract.
                ensure!(
                    !(node.kind() == SyntaxKind::ImportEqualsDeclaration
                        && node.module_reference().kind() == SyntaxKind::ExternalModuleReference),
                    "import = require() is unsupported; use an ESM import"
                );
                ensure!(
                    !(node.kind() == SyntaxKind::ExportAssignment && node.is_export_equals()),
                    "export = is unsupported; use an ESM export"
                );
            }
        }
        ensure!(
            !sources.is_empty(),
            "program contains no supported runtime source; select a TypeScript or JavaScript entry"
        );
        // Resolution reads (package manifests and other metadata) that are
        // neither sources nor already recorded program files.
        let known: std::collections::BTreeSet<String> = sources
            .keys()
            .chain(inputs.iter().map(|i| &i.path))
            .cloned()
            .collect();
        for (path, sha256) in self.reads.lock().expect("compiler read log").iter() {
            if !known.contains(path) {
                inputs.push(Input {
                    path: path.clone(),
                    sha256: Some(sha256.clone()),
                    kind: InputKind::Dependency,
                    becomes: None,
                });
            }
        }
        inputs.sort();
        inputs.dedup();
        // Negative resolution dependencies come after the positive inputs, in
        // path order. A path is kept when nothing resolves there (a dangling
        // symlink counts) or when it appeared after the failed probe: the
        // already-built program ignores such a file, so the entry makes
        // consumers reject the output instead of trusting a stale module
        // graph. A path that exists with the wrong kind for its failed probe
        // (a directory probed as a file, or the reverse) is recorded as a
        // `mismatch` that names the kind which would satisfy the probe, so it
        // never becomes a false absence but still invalidates the output once
        // it changes kind.
        let mut negative = Vec::new();
        for (path, probes) in self.misses.lock().expect("compiler probe log").iter() {
            if known.contains(path) {
                continue;
            }
            let (kind, becomes) = match classify_miss(path, probes) {
                MissState::Absent => (InputKind::Absent, None),
                MissState::Appeared => {
                    tracing::warn!(
                        path = path.as_str(),
                        "resolution candidate appeared after the compiler found it missing; \
                         recording it so the output is rejected"
                    );
                    (InputKind::Absent, None)
                }
                MissState::Mismatch(becomes) => {
                    tracing::debug!(
                        path = path.as_str(),
                        becomes = ?becomes,
                        "recording wrong-kind resolution candidate"
                    );
                    (InputKind::Mismatch, Some(becomes))
                }
                MissState::Unrelated => continue,
            };
            negative.push(Input {
                path: path.clone(),
                sha256: None,
                kind,
                becomes,
            });
        }
        negative.sort();
        tracing::debug!(
            probes = negative.len(),
            "recording absent resolution candidates"
        );
        inputs.extend(negative);
        Ok(Snapshot { sources, inputs })
    }

    fn build_with(&self, texts: &BTreeMap<String, String>) -> Rc<CompilerProgram> {
        self.build_with_config(self.config, texts)
    }

    fn build_with_config(
        &self,
        config: P<ParsedCommandLine>,
        texts: &BTreeMap<String, String>,
    ) -> Rc<CompilerProgram> {
        let base = self.fs.clone();
        let texts = texts.clone();
        let fs = wrapvfs_wrap(
            self.fs.clone(),
            None,
            Replacements {
                read_file: Some(Box::new(move |path| {
                    texts.get(path).cloned().or_else(|| base.read_file(path))
                })),
                ..Default::default()
            },
        );
        build(config, fs, &self.cwd)
    }

    pub fn emit(
        &self,
        changed: &BTreeMap<String, String>,
    ) -> Result<BTreeMap<String, (String, String)>> {
        crate::app::check_cancelled()?;
        tracing::debug!("loading transformed program");
        // Root every original application module: a transformation may
        // remove the last import of a module whose initialization was
        // proven unobservable, and that module must still be emitted.
        let _config_scope = self.region.enter();
        let mut roots = self.config.file_names().to_vec();
        {
            let _scope = enter_program(Some(&self.version));
            roots.extend(
                source_files()
                    .into_iter()
                    .filter(|&f| is_application(f))
                    .map(|f| source_file_info(f).file_name.clone()),
            );
        }
        roots.sort();
        roots.dedup();
        let version = self.build_with_config(P::new(self.config.with_file_names(roots)), changed);
        let result = (|| {
            let _scope = enter_program(Some(&version));
            // Recheck transformed files before publishing anything.
            check_diagnostics(&get_program_diagnostics())?;
            check_diagnostics(&get_global_diagnostics())?;
            for file in source_files() {
                crate::app::check_cancelled()?;
                check_diagnostics(&get_syntactic_diagnostics(file))?;
                check_diagnostics(&get_semantic_diagnostics(file))?;
            }
            tracing::debug!("emitting checked transformed program");
            let outputs = Arc::new(Mutex::new(BTreeMap::<String, (String, String)>::new()));
            let writes = outputs.clone();
            let result = version.program.emit(
                &tsrs_core::context::Context::background(),
                &tsrs_compiler::EmitOptions {
                    target_source_files: Some(
                        source_files()
                            .into_iter()
                            .filter(|f| is_application(*f) && !is_js(*f))
                            .map(|f| f.raw().as_source_file_p())
                            .collect(),
                    ),
                    write_file: Some(
                        &move |name: &str, text: &str, data: &mut tsrs_compiler::WriteFileData| {
                            crate::app::check_cancelled().map_err(|e| e.to_string())?;
                            if data.source_file.is_none() {
                                return Err("emitter omitted source identity".into());
                            }
                            let source = data.source_file.unwrap().file_name().to_owned();
                            let mut guard = writes.lock().map_err(|_| "output lock poisoned")?;
                            let entry = guard.entry(source).or_default();
                            if name.ends_with(".map") {
                                entry.1 = text.to_owned();
                            } else {
                                entry.0 = text.to_owned();
                            }
                            Ok(())
                        },
                    ),
                    ..Default::default()
                },
            );
            check_diagnostics(&result.diagnostics)?;
            ensure!(!result.emit_skipped, "compiler skipped emission");
            let outputs = outputs
                .lock()
                .map_err(|_| anyhow::anyhow!("output lock poisoned"))?
                .clone();
            Ok(outputs)
        })();
        drop(version);
        result
    }
}

fn texts_key(texts: &BTreeMap<String, String>) -> u64 {
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    texts.hash(&mut hasher);
    hasher.finish()
}

/// Pipeline backend over the pinned compiler. Programs are rebuilt from the
/// current in-memory texts; the last checked program is reused for the next
/// planning round when the texts are unchanged.
pub struct Backend<'a> {
    project: &'a Project,
    original: u64,
    checked: Option<(u64, Rc<CompilerProgram>)>,
    sizes: HashMap<(String, u64), crate::pipeline::Emitted>,
}

impl<'a> Backend<'a> {
    pub fn new(project: &'a Project, snapshot: &Snapshot) -> Self {
        Self {
            project,
            original: texts_key(&snapshot.sources),
            checked: None,
            sizes: HashMap::new(),
        }
    }

    fn program_for(&mut self, texts: &BTreeMap<String, String>) -> (Rc<CompilerProgram>, bool) {
        let key = texts_key(texts);
        if key == self.original {
            return (self.project.version.clone(), false);
        }
        if let Some((cached, version)) = &self.checked
            && *cached == key
        {
            return (version.clone(), false);
        }
        (self.project.build_with(texts), true)
    }

    fn retain(&mut self, key: u64, version: Rc<CompilerProgram>) {
        if let Some((_, old)) = self.checked.replace((key, version)) {
            drop(old);
        }
    }

    /// Releases the retained program unless it checked `texts`. A check
    /// runs on the texts of a new round, and later plans and checks read
    /// only the texts of the latest check, so the retained program is not
    /// read again; releasing it before the rebuild keeps one fewer program
    /// alive at the peak.
    fn release_stale(&mut self, texts: &BTreeMap<String, String>) {
        let key = texts_key(texts);
        if let Some((cached, old)) = self.checked.take() {
            if cached == key {
                self.checked = Some((cached, old));
            } else {
                tracing::debug!("released the program of the previous check before the rebuild");
                drop(old);
            }
        }
    }
}

impl crate::pipeline::Backend for Backend<'_> {
    fn plan(
        &mut self,
        texts: &BTreeMap<String, String>,
        phase: Phase,
        enabled: &PassSet,
    ) -> Result<Plan> {
        let (version, fresh) = self.program_for(texts);
        let result = (|| {
            let _scope = enter_program(Some(&version));
            let (files, externals): (Vec<Node>, Vec<Node>) =
                source_files().into_iter().partition(|&f| is_application(f));
            let Some(&first) = files.first() else {
                bail!("program contains no application module");
            };
            let entries = self.project.entries.clone();
            let enabled = enabled.clone();
            with_type_checker_for_file(first, move |checker| {
                let mut ctx = analysis::Ctx::new(checker, files, externals, &entries)?;
                let mut plan = passes::plan(&mut ctx, phase, &enabled)?;
                plan.imports = ctx.import_sites();
                Ok(plan)
            })
        })();
        if fresh {
            drop(version);
        }
        result
    }

    fn check(&mut self, texts: &BTreeMap<String, String>) -> Result<Vec<String>> {
        self.release_stale(texts);
        let (version, fresh) = self.program_for(texts);
        let result = (|| {
            let _scope = enter_program(Some(&version));
            check_diagnostics(&get_program_diagnostics())?;
            let mut failing = Vec::new();
            for file in source_files() {
                crate::app::check_cancelled()?;
                if crate::app::bundle_mode() {
                    // Bundle edits are not parsed per candidate: a syntax
                    // error, including TypeScript-only syntax in a chunk,
                    // rolls the responsible bundle back.
                    let name = source_file_info(file).file_name.clone();
                    if crate::app::bundle_dynamic_file(&name) {
                        continue;
                    }
                    let baseline = self.project.semantic_baseline.borrow();
                    let before = baseline.get(&name).map_or(&[][..], Vec::as_slice);
                    if !get_syntactic_diagnostics(file).is_empty()
                        || adds_diagnostics(
                            before,
                            &diagnostic_identities(&get_semantic_diagnostics(file)),
                        )
                    {
                        failing.push(name);
                    }
                    continue;
                }
                check_diagnostics(&get_syntactic_diagnostics(file))?;
                if !get_semantic_diagnostics(file).is_empty() {
                    failing.push(source_file_info(file).file_name.clone());
                }
            }
            if failing.is_empty() && !get_global_diagnostics().is_empty() {
                failing.push(String::new());
            }
            Ok(failing)
        })();
        match (&result, fresh) {
            (Ok(failing), true) if failing.is_empty() => self.retain(texts_key(texts), version),
            (_, true) => drop(version),
            _ => {}
        }
        result
    }

    fn emit_module(&mut self, file: &str, text: &str) -> Result<crate::pipeline::Emitted> {
        if crate::app::bundle_mode() {
            // Bundle chunks are published as edited text, so the text itself
            // is the measured output. Parsing every candidate chunk would
            // retain compiler memory per candidate; syntax errors are caught
            // by the recheck after each round and rolled back there.
            return Ok(crate::pipeline::Emitted {
                size: crate::bundle::chunk_code_len(text),
                imports: crate::pipeline::bundle_static_imports(text),
            });
        }
        if is_js_name(file) {
            // JavaScript passes through unchanged and has no emitted form.
            return Ok(crate::pipeline::Emitted {
                size: text.len(),
                imports: crate::pipeline::static_imports(text),
            });
        }
        // Only transpiled sizes are cached; the bundle and JavaScript paths
        // above measure the text directly, so hashing it there is wasted.
        let mut hasher = std::collections::hash_map::DefaultHasher::new();
        text.hash(&mut hasher);
        let key = (file.to_owned(), hasher.finish());
        if let Some(emitted) = self.sizes.get(&key) {
            return Ok(emitted.clone());
        }
        let output = tsrs_api::transpile::transpile(
            text,
            Some(&self.project.size_options),
            file,
            true,
            false,
        )
        .map_err(|_| anyhow::anyhow!("transpilation failed"))?;
        ensure!(
            output.diagnostics.is_empty(),
            "transformed module has syntax errors"
        );
        let emitted = crate::pipeline::Emitted {
            size: output.output_text.len(),
            imports: crate::pipeline::static_imports(&output.output_text),
        };
        self.sizes.insert(key, emitted.clone());
        Ok(emitted)
    }
}

fn build(config: P<ParsedCommandLine>, fs: Arc<dyn Fs>, cwd: &str) -> Rc<CompilerProgram> {
    let region = tsrs_core::arena::Region::new(1 << 20);
    let scope = region.enter();
    let mut options = ProgramOptions::new(
        config,
        new_compiler_host(cwd, fs, &bundled::lib_path(), None, None, None),
    );
    options.single_threaded = Tristate::True;
    options.leaf_files = tsrs_compiler::LeafMode::Off;
    options.checker_recycling = false;
    let program = new_program(options);
    drop(scope);
    Rc::new(CompilerProgram { program, region })
}

// Own overlays rather than borrowing an arena-allocated Arc: failed checks and
// candidate rebuilds must release their source buffers with their program.
struct OverlayFs {
    base: Arc<dyn Fs>,
    replacements: Replacements,
    /// Receives every missing non-`bundled:` path the compiler probes. Rebuilds
    /// over this overlay pass `None` because the wrapped base already records.
    misses: Option<MissLog>,
}

impl OverlayFs {
    fn record_miss(&self, path: &str, probe: ProbeKind) {
        if let Some(misses) = &self.misses
            && !path.starts_with("bundled:")
        {
            misses
                .lock()
                .expect("compiler probe log")
                .entry(path.to_owned())
                .or_default()
                .insert(probe);
        }
    }
}
impl Fs for OverlayFs {
    fn use_case_sensitive_file_names(&self) -> bool {
        self.base.use_case_sensitive_file_names()
    }

    fn file_exists(&self, path: &str) -> bool {
        let exists = self.base.file_exists(path);
        if !exists {
            self.record_miss(path, ProbeKind::File);
        }
        exists
    }

    fn read_file(&self, path: &str) -> Option<String> {
        self.replacements
            .read_file
            .as_ref()
            .map_or_else(|| self.base.read_file(path), |f| f(path))
    }

    fn directory_exists(&self, path: &str) -> bool {
        let exists = self.base.directory_exists(path);
        if !exists {
            self.record_miss(path, ProbeKind::Directory);
        }
        exists
    }

    fn get_accessible_entries(&self, path: &str) -> tsrs_vfs::Entries {
        self.base.get_accessible_entries(path)
    }

    fn stat(&self, path: &str) -> Option<tsrs_vfs::FileInfo> {
        let info = self.base.stat(path);
        if info.is_none() {
            self.record_miss(path, ProbeKind::Stat);
        }
        info
    }

    fn realpath(&self, path: &str) -> String {
        self.base.realpath(path)
    }

    fn write_file(&self, _path: &str, _text: &str) -> Result<(), String> {
        Err(deny_write())
    }

    fn append_file(&self, _path: &str, _text: &str) -> Result<(), String> {
        Err(deny_write())
    }

    fn remove(&self, _path: &str) -> Result<(), String> {
        Err(deny_write())
    }

    fn chtimes(
        &self,
        _path: &str,
        _a: std::time::SystemTime,
        _m: std::time::SystemTime,
    ) -> Result<(), String> {
        Err(deny_write())
    }
}
fn wrapvfs_wrap(
    base: Arc<dyn Fs>,
    misses: Option<MissLog>,
    replacements: Replacements,
) -> Arc<dyn Fs> {
    Arc::new(OverlayFs {
        base,
        replacements,
        misses,
    })
}

/// What the filesystem holds now at a path the compiler probed as missing.
#[derive(Debug, PartialEq, Eq)]
enum MissState {
    /// Nothing resolves there. A dangling symlink counts, because the compiler
    /// probes follow symlinks and creating its target changes resolution.
    Absent,
    /// Something now satisfies a probe that reported a miss, so the path
    /// appeared after the compiler resolved modules and the program was built
    /// from a different filesystem state than the one on disk.
    Appeared,
    /// The path exists but does not satisfy the failed probes (for example, a
    /// directory probed with `file_exists`). Resolution changes once it
    /// becomes the carried kind.
    Mismatch(BecomesKind),
    /// The path exists as neither a file nor a directory, or its state is
    /// unknown, such as after a permission error. It is not a resolution
    /// dependency.
    Unrelated,
}

fn classify_miss(path: &str, probes: &BTreeSet<ProbeKind>) -> MissState {
    match std::fs::metadata(path) {
        Ok(metadata) => {
            let satisfies = |probe: &ProbeKind| match probe {
                ProbeKind::File => metadata.is_file(),
                ProbeKind::Directory => metadata.is_dir(),
                ProbeKind::Stat => true,
            };
            if probes.iter().any(satisfies) {
                MissState::Appeared
            } else if metadata.is_dir() && probes.contains(&ProbeKind::File) {
                MissState::Mismatch(BecomesKind::File)
            } else if metadata.is_file() && probes.contains(&ProbeKind::Directory) {
                MissState::Mismatch(BecomesKind::Directory)
            } else {
                MissState::Unrelated
            }
        }
        Err(error)
            if matches!(
                error.kind(),
                std::io::ErrorKind::NotFound | std::io::ErrorKind::NotADirectory
            ) =>
        {
            MissState::Absent
        }
        Err(_) => MissState::Unrelated,
    }
}

pub fn sha256_hex(bytes: &[u8]) -> String {
    use sha2::{Digest, Sha256};
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

fn check_diagnostics(diagnostics: &[P<tsrs_ast::Diagnostic>]) -> Result<()> {
    if diagnostics.is_empty() {
        return Ok(());
    }
    let messages: Vec<_> = diagnostics
        .iter()
        .take(20)
        .map(|d| {
            let location = if d.file().is_some() {
                let (line, column) = tsrs_scanner::get_ecma_line_and_utf16_character_of_position(
                    d.file().unwrap().get(),
                    d.pos().unwrap_or(0),
                );
                format!(
                    "{}:{}:{}: ",
                    d.file().unwrap().file_name(),
                    line + 1,
                    column + 1
                )
            } else {
                String::new()
            };
            if crate::app::VERBOSE.load(std::sync::atomic::Ordering::Relaxed) {
                format!("{location}TS{}: {}", d.code(), redacted_message(d))
            } else {
                format!("{location}TS{} (use --verbose for details)", d.code())
            }
        })
        .collect();
    bail!("TypeScript diagnostics: {}", messages.join("; "))
}

/// Placeholder for diagnostic arguments, which can interpolate source literals,
/// identifiers and types.
const REDACTED_ARGUMENT: &str = "<redacted>";

/// Renders the diagnostic template without source-derived arguments, so verbose
/// output never copies literals or other source text into logs.
fn redacted_message(d: &P<tsrs_ast::Diagnostic>) -> String {
    d.message().map_or_else(
        || REDACTED_ARGUMENT.to_owned(),
        |m| {
            let args = vec![REDACTED_ARGUMENT.to_owned(); d.message_args().len()];
            m.format(
                &args
                    .iter()
                    .map(|s| s as &dyn std::fmt::Display)
                    .collect::<Vec<_>>(),
            )
        },
    )
}
/// JavaScript chunks of a bundler output directory, sorted, as canonical
/// paths. Classic scripts, including `importScripts` workers, belong to the
/// program as unchecked boundaries, so the chunks they reach stay observed.
fn bundle_chunks(dir: &std::path::Path) -> Result<Vec<String>> {
    fn walk(dir: &std::path::Path, out: &mut Vec<String>) -> Result<()> {
        for entry in std::fs::read_dir(dir).context("cannot read bundle directory")? {
            let entry = entry?;
            let path = entry.path();
            if entry.file_type()?.is_dir() {
                walk(&path, out)?;
            } else if path.extension().is_some_and(|e| e == "js" || e == "mjs") {
                out.push(path.canonicalize()?.to_string_lossy().into_owned());
            }
        }
        Ok(())
    }
    let mut out = Vec::new();
    walk(dir, &mut out)?;
    out.sort();
    Ok(out)
}

pub(crate) type Paths = std::collections::BTreeSet<String>;

/// Bundle files that HTML pages reach, as canonical paths.
#[derive(Default)]
struct HtmlScripts {
    /// Loaded with `<script type="module" src>`.
    modules: Paths,
    /// Loaded as classic scripts.
    classic: Paths,
    /// Imported by inline scripts (static imports of inline modules and
    /// literal dynamic imports): public entries of the bundle.
    imported: Paths,
    /// A page has an import map or an inline import this scan cannot
    /// resolve: every chunk is a public entry.
    all_public: bool,
    /// A page has an import map, which can resolve package specifiers.
    import_map: bool,
    /// Executable inline HTML code can observe callable source from an
    /// imported chunk. It is outside `snapshot.sources`, so publication must
    /// carry this boundary into the minification gate explicitly.
    source_reflection: bool,
}

/// How the chunks of a bundle import each other.
struct BundleImports {
    /// Chunks that another chunk imports.
    imported: Paths,
    /// Chunks imported through specifiers the compiler cannot link.
    unlinked: Paths,
    /// Import edges between chunks.
    edges: BTreeMap<String, Paths>,
    /// Some chunk has a computed `import(…)`.
    computed: bool,
    /// A package specifier, or a root-relative one that names no bundle
    /// file, that some chunk imports.
    bare: Option<String>,
    /// Bare package specifiers loaded through literal dynamic imports,
    /// grouped by the unchanged chunk that contains each import.
    dynamic_packages: BTreeMap<String, Paths>,
    /// Compiler inputs reached only through those dynamic package imports.
    dynamic_files: Paths,
    /// Chunks that `importScripts` can load as classic scripts.
    worker_scripts: Paths,
}

/// Resolve a module specifier to the source file that declares its module.
/// TypeScript's module symbol includes package entry points and declaration
/// files, which gives bundle mode the same resolution boundary as analysis.
fn resolved_module_file(checker: &mut Checker, specifier: Node) -> Option<String> {
    let symbol = checker.get_symbol_at_location(specifier);
    if symbol.is_nil() {
        return None;
    }
    let declaration = checker.sym(symbol).declarations.first().copied()?;
    Some(source_file_info(get_source_file_of_node(declaration)).file_name)
}

/// Resolve a bare package entry when TypeScript intentionally leaves a
/// dynamic import unresolved. Declaration entry points are preferred because
/// they are the compiler inputs that must be kept in the dynamic closure.
fn resolve_dynamic_package_entry(importer: &str, specifier: &str) -> Option<String> {
    if specifier.contains(':') || specifier.starts_with('.') || specifier.starts_with('/') {
        return None;
    }
    let mut parts = specifier.split('/');
    let package = match parts.next()? {
        scope if scope.starts_with('@') => format!("{scope}/{}", parts.next()?),
        name => name.to_owned(),
    };
    let subpath = parts.collect::<Vec<_>>().join("/");
    let importer = std::path::Path::new(importer);
    let mut ancestor = importer.parent();
    while let Some(root) = ancestor {
        let package_root = root.join("node_modules").join(&package);
        if package_root.is_dir() {
            let target = if subpath.is_empty() {
                let manifest = package_root.join("package.json");
                let fields = std::fs::read_to_string(manifest)
                    .ok()
                    .and_then(|text| serde_json::from_str::<serde_json::Value>(&text).ok());
                let entry = fields.as_ref().and_then(|value| {
                    ["types", "typings", "module", "main"]
                        .into_iter()
                        .find_map(|field| value.get(field)?.as_str())
                });
                entry.map_or_else(
                    || package_root.join("index"),
                    |entry| package_root.join(entry),
                )
            } else {
                package_root.join(&subpath)
            };
            if let Some(file) = resolve_module_path(&target) {
                return Some(file);
            }
        }
        ancestor = root.parent();
    }
    None
}

fn resolve_module_path(path: &std::path::Path) -> Option<String> {
    let candidates = [
        path.to_path_buf(),
        path.with_extension("ts"),
        path.with_extension("tsx"),
        path.with_extension("d.ts"),
        path.with_extension("js"),
        path.with_extension("jsx"),
        path.with_extension("mjs"),
        path.with_extension("cjs"),
        path.join("index.ts"),
        path.join("index.tsx"),
        path.join("index.d.ts"),
        path.join("index.js"),
        path.join("index.jsx"),
        path.join("index.mjs"),
        path.join("index.cjs"),
    ];
    candidates
        .into_iter()
        .find(|candidate| candidate.is_file())
        .and_then(|candidate| candidate.canonicalize().ok())
        .map(|candidate| candidate.to_string_lossy().into_owned())
}

fn resolve_runtime_module_path(path: &std::path::Path) -> Option<String> {
    let candidates = [
        path.to_path_buf(),
        path.with_extension("js"),
        path.with_extension("jsx"),
        path.with_extension("mjs"),
        path.with_extension("cjs"),
        path.join("index.js"),
        path.join("index.jsx"),
        path.join("index.mjs"),
        path.join("index.cjs"),
    ];
    candidates
        .into_iter()
        .find(|candidate| candidate.is_file())
        .and_then(|candidate| candidate.canonicalize().ok())
        .map(|candidate| candidate.to_string_lossy().into_owned())
}

/// Collect the files reached only through a literal dynamic package import.
/// The closure includes package declarations and their transitive module
/// dependencies. Static bundle edges are removed so a relative application
/// import cannot be hidden under the dynamic package's filesystem root.
fn dynamic_package_files(
    files: &[Node],
    entries: &Paths,
    edges: &BTreeMap<String, Paths>,
) -> Paths {
    let by_name: HashMap<String, Node> = files
        .iter()
        .copied()
        .map(|file| (source_file_info(file).file_name.clone(), file))
        .collect();
    let mut dynamic = Paths::new();
    let mut pending: Vec<String> = entries.iter().cloned().collect();
    while let Some(name) = pending.pop() {
        if !dynamic.insert(name.clone()) {
            continue;
        }
        let Some(&file) = by_name.get(&name) else {
            continue;
        };
        let mut nodes = Vec::new();
        collect(file, &mut nodes);
        with_type_checker_for_file(file, |checker| {
            for reference in source_file_reference_targets(file) {
                let target = if std::path::Path::new(&reference).is_absolute() {
                    Some(reference)
                } else {
                    resolve_dynamic_package_entry(&name, &reference)
                };
                if let Some(target) = target
                    && !dynamic.contains(&target)
                {
                    pending.push(target);
                }
            }
            for node in nodes {
                let (specifier, commonjs_require) = match node.kind() {
                    SyntaxKind::ImportDeclaration | SyntaxKind::ExportDeclaration => {
                        (node.module_specifier(), false)
                    }
                    SyntaxKind::ImportEqualsDeclaration => {
                        (node.module_reference().expression(), false)
                    }
                    SyntaxKind::ImportType => {
                        let argument = node.argument();
                        if argument.kind() == SyntaxKind::LiteralType {
                            (argument.literal(), false)
                        } else {
                            (Node::NIL, false)
                        }
                    }
                    SyntaxKind::CallExpression
                        if node.expression().kind() == SyntaxKind::ImportKeyword =>
                    {
                        (node.arguments().iter().next().unwrap_or(Node::NIL), false)
                    }
                    SyntaxKind::CallExpression if is_require_callee(checker, node.expression()) => {
                        (node.arguments().iter().next().unwrap_or(Node::NIL), true)
                    }
                    _ => continue,
                };
                if specifier.is_nil()
                    || !matches!(
                        specifier.kind(),
                        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
                    )
                {
                    continue;
                }
                let target = if commonjs_require && specifier.text().starts_with('.') {
                    std::path::Path::new(&name)
                        .parent()
                        .and_then(|parent| {
                            resolve_runtime_module_path(&parent.join(specifier.text()))
                        })
                        .or_else(|| resolved_module_file(checker, specifier))
                } else {
                    resolved_module_file(checker, specifier)
                        .or_else(|| resolve_dynamic_package_entry(&name, specifier.text()))
                };
                if let Some(target) = target
                    && !dynamic.contains(&target)
                {
                    pending.push(target);
                }
            }
        });
    }
    let static_targets: Paths = edges.values().flatten().cloned().collect();
    dynamic.retain(|file| !static_targets.contains(file));
    dynamic
}

/// Chunks that a boundary chunk can import, statically or through a literal
/// dynamic `import()`, directly or through other chunks, each with the first
/// boundary chunk in path order that reaches it. Boundary chunks themselves
/// and imported files that are not in `chunks` (JSON or CSS assets) are not
/// part of the result. A reference names only the file name, so a
/// chunk that shares its file name with a frozen chunk (in another directory)
/// is frozen with it: rehashing keeps a frozen name for every file that
/// carries it, and an edited copy under the old content hash would let
/// immutable caches serve stale code.
#[cfg(test)]
fn boundary_closure(
    chunks: &[String],
    boundaries: &Paths,
    edges: &BTreeMap<String, Paths>,
) -> BTreeMap<String, String> {
    first_boundary_credit(&boundary_reach(chunks, boundaries, edges))
}

/// Credits every chunk to the first boundary chunk in path order that reaches
/// it.
fn first_boundary_credit(reach: &BTreeMap<String, Paths>) -> BTreeMap<String, String> {
    let mut frozen = BTreeMap::<String, String>::new();
    for (boundary, chunks) in reach {
        for chunk in chunks {
            frozen
                .entry(chunk.clone())
                .or_insert_with(|| boundary.clone());
        }
    }
    frozen
}

/// For every boundary chunk that reaches at least one chunk, the chunks it
/// freezes (see `boundary_closure`), counting chunks that an earlier boundary
/// reaches as well.
fn boundary_reach(
    chunks: &[String],
    boundaries: &Paths,
    edges: &BTreeMap<String, Paths>,
) -> BTreeMap<String, Paths> {
    // Edges also reach existing assets that are not JavaScript chunks (JSON,
    // CSS): those never take part in staging, so they cannot be frozen.
    let known: Paths = chunks.iter().cloned().collect();
    let mut by_name = BTreeMap::<&std::ffi::OsStr, Vec<&String>>::new();
    for chunk in chunks {
        if let Some(name) = std::path::Path::new(chunk).file_name() {
            by_name.entry(name).or_default().push(chunk);
        }
    }
    let mut reach = BTreeMap::<String, Paths>::new();
    for boundary in boundaries {
        // Traversal passes through other boundary chunks: what a nested
        // boundary imports is reachable from this one as well.
        let mut frozen = Paths::new();
        let mut visited = Paths::from([boundary.clone()]);
        let mut pending = vec![boundary.clone()];
        while let Some(chunk) = pending.pop() {
            let mut reached: Vec<String> =
                edges.get(&chunk).into_iter().flatten().cloned().collect();
            while let Some(target) = reached.pop() {
                if !visited.insert(target.clone()) {
                    continue;
                }
                // An excluded importer keeps original names, so pin a
                // dynamically importing target and its imports with it.
                let freeze_dynamic_boundary = crate::app::bundle_excluded(boundary)
                    && crate::app::bundle_dynamic_package(&target);
                if known.contains(&target)
                    && (!boundaries.contains(&target) || freeze_dynamic_boundary)
                {
                    frozen.insert(target.clone());
                    let name = std::path::Path::new(&target).file_name();
                    reached.extend(
                        name.and_then(|name| by_name.get(name))
                            .into_iter()
                            .flatten()
                            .map(|sibling| (*sibling).clone()),
                    );
                }
                pending.push(target);
            }
        }
        if !frozen.is_empty() {
            reach.insert(boundary.clone(), frozen);
        }
    }
    reach
}

/// Logs which boundary chunk freezes how many chunks. A boundary that a page
/// loads as a script keeps everything it imports out of optimization, which
/// is worth a warning, whether the page loads it as a module script, as a
/// classic script or through an inline script. Every boundary is checked on
/// its own reach: a chunk that an earlier boundary reaches as well is
/// credited to that boundary in the report, but the page-loaded boundary
/// still keeps it out of optimization.
fn log_frozen_chunks(
    reach: &BTreeMap<String, Paths>,
    scripts: &Paths,
    classic_scripts: &Paths,
    from_html: &Paths,
) {
    for (boundary, reached) in reach {
        let chunks = reached.len();
        tracing::info!(
            boundary = boundary.as_str(),
            chunks,
            "chunks that a boundary chunk can import stay unchanged"
        );
        if scripts.contains(boundary)
            || classic_scripts.contains(boundary)
            || from_html.contains(boundary)
        {
            tracing::warn!(
                boundary = boundary.as_str(),
                chunks,
                "an unchanged HTML entry chunk imports chunks that therefore cannot be optimized"
            );
        }
    }
}

/// JavaScript MIME type essences, which make a `<script>` a classic script
/// (https://mimesniff.spec.whatwg.org/#javascript-mime-type).
const JAVASCRIPT_MIME_TYPES: [&str; 16] = [
    "application/ecmascript",
    "application/javascript",
    "application/x-ecmascript",
    "application/x-javascript",
    "text/ecmascript",
    "text/javascript",
    "text/javascript1.0",
    "text/javascript1.1",
    "text/javascript1.2",
    "text/javascript1.3",
    "text/javascript1.4",
    "text/javascript1.5",
    "text/jscript",
    "text/livescript",
    "text/x-ecmascript",
    "text/x-javascript",
];

/// What HTML pages in the bundle load. Script URLs resolve with URL
/// semantics against the page's first `<base href>` (or the page) within a
/// virtual origin rooted at the bundle directory; a path that does not exist
/// there is retried without leading segments (a deployment base path).
/// External URLs are ignored.
fn html_scripts(dir: &std::path::Path) -> Result<HtmlScripts> {
    const ORIGIN: &str = "http://bundle.invalid/";
    fn resolve(root: &std::path::Path, base: &url::Url, reference: &str) -> Option<String> {
        let url = base.join(reference.trim()).ok()?;
        if url.scheme() != "http" || url.host_str() != Some("bundle.invalid") {
            return None;
        }
        let path = percent_decode(url.path().trim_start_matches('/'));
        let segments: Vec<&str> = path.split('/').collect();
        (0..segments.len())
            .map(|skip| root.join(segments[skip..].join("/")))
            .filter_map(|c| c.canonicalize().ok())
            .find(|c| c.starts_with(root) && c.is_file())
            .map(|c| c.to_string_lossy().into_owned())
    }
    /// Whether an attribute whose raw value has an undecodable character
    /// reference can hold code: an event handler, or a value that reads as a
    /// `javascript:` URL once undecodable references and the characters the
    /// URL parser removes (leading C0 controls and spaces, TAB, LF and CR
    /// anywhere) are dropped, and that sits in an attribute where a browser
    /// runs such a URL.
    fn may_run_code(tag: &str, attribute: &str, raw: &str) -> bool {
        if attribute.starts_with("on") {
            return true;
        }
        // Navigating or loading these runs a `javascript:` URL; the same
        // text in any other attribute (`title`, `data-*`) is inert.
        let runs_url = match tag {
            "a" | "area" => matches!(attribute, "href" | "xlink:href"),
            "form" => attribute == "action",
            "button" | "input" => attribute == "formaction",
            "iframe" | "frame" | "embed" => attribute == "src",
            "object" => attribute == "data",
            _ => false,
        };
        if !runs_url {
            return false;
        }
        // Decode the references this parser knows first: they can spell the
        // scheme (`java&#115;cript:`) next to one it cannot decode. A
        // reference that stays undecoded is dropped, since it may expand to
        // a character the URL parser strips.
        let decoded = crate::bundle::decode_references_lossy(raw);
        let mut stripped = String::new();
        let mut chars = decoded.chars().peekable();
        while let Some(c) = chars.next() {
            if c == '&' {
                while chars
                    .next_if(|n| n.is_ascii_alphanumeric() || *n == '#')
                    .is_some()
                {}
                chars.next_if_eq(&';');
            } else if !matches!(c, '\t' | '\n' | '\r') {
                stripped.push(c.to_ascii_lowercase());
            }
        }
        // The URL parser trims leading C0 controls and spaces, and removes
        // only TAB, LF and CR from the rest of the value, so an internal
        // space or other control keeps the text from being a scheme.
        stripped
            .trim_start_matches(|c: char| c <= ' ')
            .starts_with("javascript:")
    }
    fn walk(root: &std::path::Path, dir: &std::path::Path, found: &mut HtmlScripts) -> Result<()> {
        for entry in std::fs::read_dir(dir).context("cannot read bundle directory")? {
            let entry = entry?;
            let path = entry.path();
            if entry.file_type()?.is_dir() {
                walk(root, &path, found)?;
                continue;
            }
            if !crate::bundle::is_html(&path) {
                continue;
            }
            let html = std::fs::read_to_string(&path).context("cannot read an HTML page")?;
            let tags = crate::bundle::html_tags(&html);
            // A reference this parser cannot decode could name any file.
            ensure!(
                !tags.iter().any(|t| {
                    (t.name == "script" && t.undecodable.iter().any(|a| a == "src" || a == "type"))
                        || (t.name == "base" && t.undecodable.iter().any(|a| a == "href"))
                }),
                "an HTML script or base URL uses an unsupported character reference"
            );
            let relative = path.strip_prefix(root).unwrap_or(&path).to_string_lossy();
            let origin = url::Url::parse(ORIGIN).expect("valid origin");
            let page = origin
                .join(&relative)
                .context("cannot form a URL for an HTML page")?;
            // A script resolves against the first `<base href>` parsed before
            // it, or against the page.
            let mut base: Option<url::Url> = None;
            // A base inside template or noscript content does not apply.
            // Scripts and handlers there are still counted: template content
            // can be cloned into the document and run later.
            let mut inert = 0usize;
            for tag in &tags {
                match tag.name.as_str() {
                    "template" | "noscript" => {
                        inert += 1;
                        continue;
                    }
                    "/template" | "/noscript" => {
                        inert = inert.saturating_sub(1);
                        continue;
                    }
                    _ => {}
                }
                // Event handlers and `javascript:` URLs are inline code that
                // can import chunks.
                for (attribute, value) in &tag.attributes {
                    // The browser decodes the value before it runs, so a
                    // reference this parser cannot decode may hide an import
                    // from the scan below: assume any chunk can be loaded.
                    if tag.undecodable.contains(attribute)
                        && may_run_code(&tag.name, attribute, value)
                    {
                        tracing::info!(
                            page = %path.display(),
                            attribute = %attribute,
                            "an HTML executable attribute has an undecodable character reference; \
                             every chunk is a public entry and bundle minification is skipped"
                        );
                        // The undecoded code may read callable source, so the
                        // minification gate must treat it as source reflection.
                        found.all_public = true;
                        found.source_reflection = true;
                        continue;
                    }
                    let code = if attribute.starts_with("on") {
                        Some(value.as_str())
                    } else {
                        let trimmed = value.trim_start();
                        trimmed
                            .get(..11)
                            .filter(|scheme| scheme.eq_ignore_ascii_case("javascript:"))
                            .map(|_| &trimmed[11..])
                    };
                    let Some(code) = code else {
                        continue;
                    };
                    found.source_reflection |= crate::minify::may_observe_function_source(code);
                    let dynamic = crate::bundle::literal_dynamic_imports(code);
                    if dynamic.non_literal {
                        found.all_public = true;
                    }
                    for raw in dynamic.literals {
                        match crate::bundle::decode_js_string(&raw).filter(|_| !raw.contains("${"))
                        {
                            Some(specifier) => {
                                if let Some(file) =
                                    resolve(root, base.as_ref().unwrap_or(&page), &specifier)
                                {
                                    found.imported.insert(file);
                                }
                            }
                            None => found.all_public = true,
                        }
                    }
                }
                if tag.name == "base"
                    && inert == 0
                    && base.is_none()
                    && let Some(href) = tag.attribute("href")
                {
                    base = Some(
                        page.join(href.trim())
                            .context("an HTML page has an invalid base URL")?,
                    );
                    continue;
                }
                if tag.name != "script" {
                    continue;
                }
                let base = base.as_ref().unwrap_or(&page);
                // The MIME type essence: parameters and case do not matter.
                let kind = tag.attribute("type").map(|t| {
                    t.split(';')
                        .next()
                        .unwrap_or_default()
                        .trim()
                        .to_ascii_lowercase()
                });
                // An import map can map any specifier to any chunk.
                if kind.as_deref() == Some("importmap") {
                    found.all_public = true;
                    found.import_map = true;
                    continue;
                }
                let module = kind.as_deref() == Some("module");
                let classic = kind
                    .as_deref()
                    .is_none_or(|t| t.is_empty() || JAVASCRIPT_MIME_TYPES.contains(&t));
                if !module && !classic {
                    continue;
                }
                match tag.attribute("src") {
                    Some(src) => {
                        if let Some(file) = resolve(root, base, src) {
                            if module {
                                found.modules.insert(file);
                            } else {
                                found.classic.insert(file);
                            }
                        }
                    }
                    None => {
                        // Inline code can import chunks: they become public.
                        let code = tag.text.as_deref().unwrap_or_default();
                        found.source_reflection |= crate::minify::may_observe_function_source(code);
                        let dynamic = crate::bundle::literal_dynamic_imports(code);
                        // A computed specifier can name any chunk.
                        if dynamic.non_literal {
                            found.all_public = true;
                        }
                        let mut specifiers = dynamic.literals;
                        if module {
                            specifiers.extend(crate::pipeline::bundle_static_imports(code));
                        }
                        for raw in specifiers {
                            // The specifier is JavaScript string source: decode
                            // its escapes; a template with substitutions or a
                            // malformed escape could name any chunk.
                            match crate::bundle::decode_js_string(&raw)
                                .filter(|_| !raw.contains("${"))
                            {
                                Some(specifier) => {
                                    if let Some(file) = resolve(root, base, &specifier) {
                                        found.imported.insert(file);
                                    }
                                }
                                None => found.all_public = true,
                            }
                        }
                    }
                }
            }
        }
        Ok(())
    }
    let mut found = HtmlScripts::default();
    walk(dir, dir, &mut found)?;
    Ok(found)
}

/// Sorted identities of diagnostics: code, message with its arguments and
/// the source text at the diagnostic. Positions move with every edit, so
/// they are not part of the identity; a new diagnostic that reuses an old
/// code at different code still differs.
fn diagnostic_identities(diagnostics: &[P<tsrs_ast::Diagnostic>]) -> Vec<String> {
    let mut identities: Vec<String> = diagnostics
        .iter()
        .map(|d| {
            let snippet = if d.file().is_some() {
                let text = d.file().unwrap().text();
                let (pos, end) = (d.pos().unwrap_or(0) as usize, d.end().unwrap_or(0) as usize);
                text.get(pos.min(text.len())..end.min(text.len()))
                    .unwrap_or_default()
                    .trim()
                    .to_owned()
            } else {
                String::new()
            };
            format!("{}\0{}\0{snippet}", d.code(), d.localize())
        })
        .collect();
    identities.sort();
    identities
}

/// Whether `after` has a diagnostic identity more often than `before`.
fn adds_diagnostics(before: &[String], after: &[String]) -> bool {
    let mut remaining = before.to_vec();
    after
        .iter()
        .any(|code| match remaining.iter().position(|c| c == code) {
            Some(at) => {
                remaining.swap_remove(at);
                false
            }
            None => true,
        })
}

/// Whether a specifier is a root-relative URL path (a single leading slash).
fn is_root_relative(text: &str) -> bool {
    text.starts_with('/') && !text.starts_with("//")
}

/// Resolves a root-relative URL path under the bundle root `dir`, dropping
/// leading segments of a deployment base path until a bundled file matches.
/// The query and fragment are ignored and `%` escapes are decoded.
fn resolve_root_relative(dir: &std::path::Path, text: &str) -> Option<std::path::PathBuf> {
    let path = percent_decode(text.split(['?', '#']).next().unwrap_or_default());
    let segments: Vec<&str> = path.trim_start_matches('/').split('/').collect();
    (0..segments.len())
        .filter_map(|skip| dir.join(segments[skip..].join("/")).canonicalize().ok())
        .find(|c| c.starts_with(dir) && c.is_file())
}

/// Decodes `%XX` escapes of a URL path.
pub(crate) fn percent_decode(path: &str) -> String {
    let bytes = path.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%'
            && let Some(byte) = path
                .get(i + 1..i + 3)
                .and_then(|hex| u8::from_str_radix(hex, 16).ok())
        {
            out.push(byte);
            i += 3;
            continue;
        }
        out.push(bytes[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// The rule that makes a bundle chunk an unchanged boundary because of its
/// syntax. The names are stable and appear in debug logs.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum SyntaxBoundaryRule {
    /// A `with` statement can look up the chunk's bindings by runtime strings.
    WithStatement,
    /// The global `eval` is used as a value: a direct call or an alias.
    EvalReference,
    /// `importScripts(...)` is called, which only a classic worker can do.
    ImportScriptsCall,
    /// `importScripts` is used as a value that can alias it.
    ImportScriptsReference,
}

impl SyntaxBoundaryRule {
    pub(crate) fn as_str(self) -> &'static str {
        match self {
            Self::WithStatement => "with-statement",
            Self::EvalReference => "eval-reference",
            Self::ImportScriptsCall => "import-scripts-call",
            Self::ImportScriptsReference => "import-scripts-reference",
        }
    }
}

/// Whether a node is an `Identifier` or string key that names a global-object
/// receiver such as `globalThis` or `self`.
fn is_global_object_receiver(node: Node) -> bool {
    if node.is_nil() {
        return false;
    }
    let node = skip_parentheses(node);
    node.kind() == SyntaxKind::Identifier
        && matches!(node.text(), "globalThis" | "self" | "window" | "global")
}

/// Whether a call's callee names `importScripts`: `importScripts`,
/// `self.importScripts`, `self["importScripts"]` or a parenthesized form.
fn is_import_scripts_callee(callee: Node) -> bool {
    let callee = skip_parentheses(callee);
    match callee.kind() {
        SyntaxKind::Identifier => callee.text() == "importScripts",
        SyntaxKind::PropertyAccessExpression => callee.name().text() == "importScripts",
        SyntaxKind::ElementAccessExpression => {
            let key = callee.argument_expression();
            matches!(
                key.kind(),
                SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
            ) && key.text() == "importScripts"
        }
        _ => false,
    }
}

/// Whether a call's callee resolves to the global CommonJS `require` function.
///
/// A local binding can reuse the conventional name without loading a module;
/// only unresolved names and ambient declarations represent the host-provided
/// CommonJS function in bundle mode.
fn is_require_callee(checker: &mut Checker, callee: Node) -> bool {
    let callee = skip_parentheses(callee);
    if callee.kind() != SyntaxKind::Identifier || callee.text() != "require" {
        return false;
    }
    let symbol = checker.get_symbol_at_location(callee);
    if symbol.is_nil() {
        return true;
    }
    checker.sym(symbol).declarations.iter().all(|&declaration| {
        let source = source_file_info(get_source_file_of_node(declaration)).file_name;
        source.starts_with("bundled:")
            || source.ends_with(".d.ts")
            || has_syntactic_modifier(declaration, ModifierFlags::Ambient)
            || has_syntactic_modifier(declaration.parent(), ModifierFlags::Ambient)
    })
}

/// The expression inside any number of enclosing parentheses.
fn skip_parentheses(mut node: Node) -> Node {
    while node.kind() == SyntaxKind::ParenthesizedExpression {
        node = node.expression();
    }
    node
}

/// The source object that `key` reads from when it is the property key of a
/// destructuring pattern: `const { key: alias } = source` or the assignment
/// `({ key: alias } = source)`. `NIL` for any other node.
fn destructuring_source(key: Node) -> Node {
    let holder = key.parent();
    match holder.kind() {
        SyntaxKind::BindingElement if holder.property_name() == key => {
            let pattern = holder.parent();
            let owner = pattern.parent();
            if pattern.kind() == SyntaxKind::ObjectBindingPattern
                && owner.kind() == SyntaxKind::VariableDeclaration
            {
                return owner.initializer();
            }
        }
        SyntaxKind::PropertyAssignment if holder.name() == key => {
            let literal = holder.parent();
            let assignment = literal.parent();
            if literal.kind() == SyntaxKind::ObjectLiteralExpression
                && assignment.kind() == SyntaxKind::BinaryExpression
                && assignment.operator_token().kind() == SyntaxKind::EqualsToken
                && assignment.left() == literal
            {
                return assignment.right();
            }
        }
        _ => {}
    }
    Node::NIL
}

/// Classifies one node as a use of the global `eval` or `importScripts`.
/// Property names, property keys, `typeof` operands and type names mention
/// the name without running code, so they are not uses. A member of a global
/// object (`globalThis.eval`, `self["importScripts"]`) is the global itself,
/// also when a destructuring pattern reads it (`const { importScripts: load } =
/// self`), because the renamed binding hides the name.
fn global_use_rule(node: Node) -> Option<SyntaxBoundaryRule> {
    let (name, is_name_node) = match node.kind() {
        SyntaxKind::Identifier => (node.text(), true),
        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral => {
            (node.text(), false)
        }
        _ => return None,
    };
    if !matches!(name, "eval" | "importScripts") {
        return None;
    }
    let parent = node.parent();
    // The expression whose value the name denotes, and whether the name
    // selects a member of a receiver instead of a binding.
    let (expression, via_receiver) = if is_name_node {
        match parent.kind() {
            SyntaxKind::PropertyAccessExpression if parent.name() == node => {
                (parent, Some(parent.expression()))
            }
            // A destructuring key of a global object reads the global.
            SyntaxKind::BindingElement | SyntaxKind::PropertyAssignment
                if is_global_object_receiver(destructuring_source(node)) =>
            {
                (node, Some(destructuring_source(node)))
            }
            // Keys, member names and type names never run code.
            SyntaxKind::PropertyAssignment
            | SyntaxKind::PropertyDeclaration
            | SyntaxKind::MethodDeclaration
            | SyntaxKind::GetAccessor
            | SyntaxKind::SetAccessor
            | SyntaxKind::PropertySignature
            | SyntaxKind::MethodSignature
            | SyntaxKind::EnumMember
            | SyntaxKind::JsxAttribute
                if parent.name() == node =>
            {
                return None;
            }
            // `import { eval as helper }`, `export { helper as eval }` and
            // `export * as eval from` name a module export, not the global.
            SyntaxKind::ImportSpecifier if parent.property_name() == node => return None,
            SyntaxKind::ExportSpecifier
                if parent.name() == node && parent.property_name().is_some() =>
            {
                return None;
            }
            // `export { eval as e } from "./lib"` names the other module's export.
            SyntaxKind::ExportSpecifier
                if parent.property_name() == node
                    && parent.parent().parent().module_specifier().is_some() =>
            {
                return None;
            }
            SyntaxKind::NamespaceExport if parent.name() == node => return None,
            SyntaxKind::BindingElement if parent.property_name() == node => return None,
            SyntaxKind::QualifiedName if parent.right() == node => return None,
            _ => (node, None),
        }
    } else {
        // A string literal selects a member only as `receiver["name"]`.
        if parent.kind() != SyntaxKind::ElementAccessExpression
            || parent.argument_expression() != node
        {
            return None;
        }
        (parent, Some(parent.expression()))
    };
    // The outermost parenthesized form of an expression: `(eval)` and
    // `((self.importScripts))` mean the same as the bare expression.
    let outermost = |expression: Node| {
        let mut outer = expression;
        while outer.parent().kind() == SyntaxKind::ParenthesizedExpression {
            outer = outer.parent();
        }
        outer
    };
    let is_call = |callee: Node| {
        let callee = outermost(callee);
        let holder = callee.parent();
        holder.kind() == SyntaxKind::CallExpression && holder.expression() == callee
    };
    if outermost(expression).parent().kind() == SyntaxKind::TypeOfExpression {
        // `typeof eval` and `typeof self.importScripts` are feature checks.
        return None;
    }
    let global = match via_receiver {
        None => true,
        // `importScripts` is called on any receiver (`self`, `globalThis`, a
        // worker scope held in a variable); `eval` is the global only on a
        // global object, because `obj.eval` is an ordinary property.
        Some(receiver) => {
            is_global_object_receiver(receiver) || (name == "importScripts" && is_call(expression))
        }
    };
    if !global {
        return None;
    }
    Some(match (name, is_call(expression)) {
        ("eval", _) => SyntaxBoundaryRule::EvalReference,
        (_, true) => SyntaxBoundaryRule::ImportScriptsCall,
        (_, false) => SyntaxBoundaryRule::ImportScriptsReference,
    })
}

/// The syntax rule that makes a file a boundary, if any: a `with` statement,
/// the global `eval` used as a value (a call or an alias), which can look up
/// the file's bindings by runtime strings, or `importScripts` called or used
/// as a value, which only a classic worker has: such a file may run as a
/// classic script whose top-level bindings are globals, even if a module also
/// imports it. Mentions that cannot run code (`typeof self.importScripts`,
/// `obj.eval`, `{ eval: 1 }`) are not uses.
pub(crate) fn syntax_boundary_rule(nodes: &[Node]) -> Option<SyntaxBoundaryRule> {
    nodes.iter().find_map(|&node| {
        if node.kind() == SyntaxKind::WithStatement {
            Some(SyntaxBoundaryRule::WithStatement)
        } else {
            global_use_rule(node)
        }
    })
}

/// Whether a file has a syntax boundary rule (see `syntax_boundary_rule`).
pub(crate) fn uses_eval_or_with(nodes: &[Node]) -> bool {
    syntax_boundary_rule(nodes).is_some()
}

/// Whether a file uses `import.meta`, which only ES modules can parse.
pub(crate) fn uses_import_meta(nodes: &[Node]) -> bool {
    nodes.iter().any(|node| {
        node.kind() == SyntaxKind::MetaProperty && node.keyword_token() == SyntaxKind::ImportKeyword
    })
}

/// Whether the file has ES module statements (imports or exports).
pub(crate) fn has_esm_statements(file: Node) -> bool {
    file.statements().iter().any(|statement| {
        matches!(
            statement.kind(),
            SyntaxKind::ImportDeclaration
                | SyntaxKind::ExportDeclaration
                | SyntaxKind::ExportAssignment
        ) || has_syntactic_modifier(statement, ModifierFlags::Export)
    })
}

fn is_js_name(n: &str) -> bool {
    n.ends_with(".js") || n.ends_with(".jsx") || n.ends_with(".mjs") || n.ends_with(".cjs")
}
fn is_js(file: Node) -> bool {
    is_js_name(&source_file_info(file).file_name)
}
fn is_application(file: Node) -> bool {
    let n = &source_file_info(file).file_name;
    (n.ends_with(".ts")
        || n.ends_with(".tsx")
        || n.ends_with(".mts")
        || n.ends_with(".js")
        || n.ends_with(".jsx")
        || n.ends_with(".mjs")
        || n.ends_with(".cjs"))
        && !is_source_file_from_external_library(file)
        && !n.contains("/node_modules/")
        && !n.starts_with("bundled:")
        && !n.ends_with(".d.ts")
        && !n.ends_with(".d.mts")
        && !n.ends_with(".d.cts")
}
fn collect(node: Node, nodes: &mut Vec<Node>) {
    nodes.push(node);
    node.for_each_child(|child| {
        collect(child, nodes);
        false
    });
}

/// Start of the node's first token, excluding leading trivia.
fn token_start(node: Node) -> usize {
    token_start_in(node, get_source_file_of_node(node))
}

/// [`token_start`] for a node of a known source file. Finding the source file
/// walks every parent, which dominated planning on deeply nested bundles.
fn token_start_in(node: Node, source_file: Node) -> usize {
    tsrs_scanner::get_token_pos_of_node(node.raw(), source_file.raw().as_source_file_p(), false)
        as usize
}

#[cfg(test)]
mod boundary_closure_tests {
    use std::collections::BTreeMap;

    use super::{Paths, boundary_closure, boundary_reach};

    fn graph(edges: &[(&str, &[&str])]) -> BTreeMap<String, Paths> {
        edges
            .iter()
            .map(|(from, to)| {
                (
                    (*from).to_owned(),
                    to.iter().map(|t| (*t).to_owned()).collect(),
                )
            })
            .collect()
    }

    /// Every chunk that the graph mentions.
    fn chunks(edges: &BTreeMap<String, Paths>) -> Vec<String> {
        let all: Paths = edges
            .iter()
            .flat_map(|(from, to)| std::iter::once(from).chain(to))
            .cloned()
            .collect();
        all.into_iter().collect()
    }

    #[test]
    fn closure_follows_edges_transitively_and_names_the_first_boundary() {
        let edges = graph(&[
            ("a", &["x", "b2"]),
            ("b", &["y"]),
            ("x", &["z"]),
            ("y", &["z", "w"]),
            ("z", &["x"]),
            ("free", &["w2"]),
        ]);
        let boundaries: Paths = ["a", "b", "b2"].map(String::from).into_iter().collect();
        let frozen = boundary_closure(&chunks(&edges), &boundaries, &edges);
        let frozen: Vec<_> = frozen
            .iter()
            .map(|(k, v)| (k.as_str(), v.as_str()))
            .collect();
        // Boundaries are not part of the closure; `z` is reached first through
        // `a`, and chunks no boundary reaches stay editable.
        assert_eq!(frozen, [("w", "b"), ("x", "a"), ("y", "b"), ("z", "a")]);
    }

    #[test]
    fn closure_continues_through_nested_boundaries() {
        // `a` imports the boundary `b`, which imports `x`: the first boundary
        // in path order, `a`, reaches `x`, and `b` is not a frozen chunk.
        let edges = graph(&[("a", &["b"]), ("b", &["x", "a"]), ("x", &["y"])]);
        let boundaries: Paths = ["a", "b"].map(String::from).into_iter().collect();
        let frozen = boundary_closure(&chunks(&edges), &boundaries, &edges);
        let frozen: Vec<_> = frozen
            .iter()
            .map(|(k, v)| (k.as_str(), v.as_str()))
            .collect();
        assert_eq!(frozen, [("x", "a"), ("y", "a")]);
    }

    #[test]
    fn closure_skips_imported_files_that_are_not_chunks() {
        // `a` imports a JSON asset and a stylesheet next to the chunk `x`: only
        // JavaScript chunks can be frozen, because staging never sees assets.
        let edges = graph(&[("a", &["data-H.json", "style-H.css", "x"])]);
        let boundaries: Paths = ["a"].map(String::from).into_iter().collect();
        let chunks: Vec<String> = ["a", "x"].map(String::from).into();
        let frozen = boundary_closure(&chunks, &boundaries, &edges);
        let frozen: Vec<_> = frozen
            .iter()
            .map(|(k, v)| (k.as_str(), v.as_str()))
            .collect();
        assert_eq!(frozen, [("x", "a")]);
    }

    #[test]
    fn reach_keeps_a_later_boundary_that_shares_chunks_with_an_earlier_one() {
        // `a` (not loaded by a page) and `page` (loaded by a page) both reach
        // `x`: the closure credits `x` to `a` only, but the reach of `page`
        // still names it, so the HTML-entry warning is not skipped.
        let edges = graph(&[("a", &["x"]), ("page", &["x", "y"])]);
        let boundaries: Paths = ["a", "page"].map(String::from).into_iter().collect();
        let chunks = chunks(&edges);
        let closure = boundary_closure(&chunks, &boundaries, &edges);
        let closure: Vec<_> = closure
            .iter()
            .map(|(k, v)| (k.as_str(), v.as_str()))
            .collect();
        assert_eq!(closure, [("x", "a"), ("y", "page")]);
        let reach = boundary_reach(&chunks, &boundaries, &edges);
        let reach: Vec<_> = reach
            .iter()
            .map(|(k, v)| (k.as_str(), v.iter().map(String::as_str).collect::<Vec<_>>()))
            .collect();
        assert_eq!(reach, [("a", vec!["x"]), ("page", vec!["x", "y"])]);
    }

    #[test]
    fn closure_freezes_chunks_that_share_a_file_name_with_a_frozen_chunk() {
        // `a` imports `dir1/x-H.js`; `dir2/x-H.js` shares its file name and is
        // imported only by an editable chunk, and `dir2/y.js` is what that
        // sibling imports. The sibling and its imports are frozen with it,
        // while chunks with other names stay editable.
        let edges = graph(&[
            ("a", &["dir1/x-H.js"]),
            ("free", &["dir2/x-H.js", "dir2/other.js"]),
            ("dir2/x-H.js", &["dir2/y.js"]),
        ]);
        let boundaries: Paths = ["a"].map(String::from).into_iter().collect();
        let frozen = boundary_closure(&chunks(&edges), &boundaries, &edges);
        let frozen: Vec<_> = frozen
            .iter()
            .map(|(k, v)| (k.as_str(), v.as_str()))
            .collect();
        assert_eq!(
            frozen,
            [
                ("dir1/x-H.js", "a"),
                ("dir2/x-H.js", "a"),
                ("dir2/y.js", "a")
            ]
        );
    }
}

#[cfg(all(test, any(target_os = "linux", target_os = "macos")))]
mod tests {
    use super::*;

    fn probes(kinds: &[ProbeKind]) -> BTreeSet<ProbeKind> {
        kinds.iter().copied().collect()
    }

    fn classify(path: &std::path::Path, kinds: &[ProbeKind]) -> MissState {
        classify_miss(&path.to_string_lossy(), &probes(kinds))
    }

    #[test]
    fn missing_paths_and_dangling_symlinks_are_absent() {
        let temp = tempfile::tempdir().unwrap();
        let file = temp.path().join("file.ts");
        std::fs::write(&file, "").unwrap();
        let dangling = temp.path().join("dangling.ts");
        std::os::unix::fs::symlink(temp.path().join("target.ts"), &dangling).unwrap();
        for kind in [ProbeKind::File, ProbeKind::Directory, ProbeKind::Stat] {
            assert_eq!(
                classify(&temp.path().join("none.ts"), &[kind]),
                MissState::Absent
            );
            assert_eq!(classify(&dangling, &[kind]), MissState::Absent);
            // A path below a regular file reports ENOTDIR.
            assert_eq!(
                classify(&file.join("nested.ts"), &[kind]),
                MissState::Absent
            );
        }
        // The symlink target appearing turns the candidate into a hit.
        std::fs::write(temp.path().join("target.ts"), "").unwrap();
        assert_eq!(classify(&dangling, &[ProbeKind::File]), MissState::Appeared);
    }

    #[test]
    fn a_path_that_now_satisfies_a_failed_probe_has_appeared() {
        let temp = tempfile::tempdir().unwrap();
        let file = temp.path().join("file.ts");
        std::fs::write(&file, "").unwrap();
        let directory = temp.path().join("dir");
        std::fs::create_dir(&directory).unwrap();
        assert_eq!(classify(&file, &[ProbeKind::File]), MissState::Appeared);
        assert_eq!(classify(&file, &[ProbeKind::Stat]), MissState::Appeared);
        assert_eq!(
            classify(&file, &[ProbeKind::Directory, ProbeKind::File]),
            MissState::Appeared
        );
        assert_eq!(
            classify(&directory, &[ProbeKind::Directory]),
            MissState::Appeared
        );
        assert_eq!(
            classify(&directory, &[ProbeKind::Stat]),
            MissState::Appeared
        );
    }

    #[test]
    fn a_probe_of_the_wrong_kind_records_the_kind_that_would_satisfy_it() {
        let temp = tempfile::tempdir().unwrap();
        let file = temp.path().join("file.ts");
        std::fs::write(&file, "").unwrap();
        let directory = temp.path().join("dir");
        std::fs::create_dir(&directory).unwrap();
        assert_eq!(
            classify(&file, &[ProbeKind::Directory]),
            MissState::Mismatch(BecomesKind::Directory)
        );
        assert_eq!(
            classify(&directory, &[ProbeKind::File]),
            MissState::Mismatch(BecomesKind::File)
        );
    }

    #[test]
    fn candidate_created_before_snapshot_is_still_recorded() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        std::fs::write(
            root.join("tsconfig.json"),
            r#"{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true,"jsx":"react-jsx"}}"#,
        )
        .unwrap();
        std::fs::write(root.join("main.ts"), "import { f } from './dep'; f();").unwrap();
        std::fs::write(root.join("dep.tsx"), "export function f() {}").unwrap();
        let project = Project::load(
            &root.join("tsconfig.json").to_string_lossy(),
            vec![root.join("main.ts").to_string_lossy().into_owned()],
        )
        .unwrap();
        // The higher-priority candidate appears after `./dep` resolved past
        // it but before the snapshot is taken.
        let late = root.join("dep.ts");
        std::fs::write(&late, "export function f() {}").unwrap();
        let snapshot = project.snapshot().unwrap();
        let late = late.to_string_lossy();
        assert!(
            snapshot
                .inputs
                .iter()
                .any(|i| i.kind == InputKind::Absent && i.path == late.as_ref()),
            "{:#?}",
            snapshot.inputs
        );
    }
}
