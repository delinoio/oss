//! All access to the pinned compiler lives in this adapter. The pipeline only
//! receives plain candidates and byte spans, never compiler-owned identities.
use std::{
    collections::{BTreeMap, HashMap},
    hash::{Hash, Hasher},
    rc::Rc,
    sync::{Arc, Mutex},
};

use anyhow::{Context as _, Result, bail, ensure};
use ts_goport::{
    frontend::{
        bundled,
        compiler::{ProgramOptions, new_compiler_host, new_program},
        tsoptions::{ParseConfigHost, ParsedCommandLine, get_parsed_command_line_of_config_file},
        vfs::{Fs, Replacements, osvfs_fs, wrapvfs_wrap},
    },
    prelude::*,
};

use crate::{
    passes::PassSet,
    plan::{Phase, Plan},
};

mod analysis;
mod flow;
mod passes;

struct Host {
    fs: Rc<dyn Fs>,
    cwd: String,
}
impl ParseConfigHost for Host {
    fn fs(&self) -> Rc<dyn Fs> {
        self.fs.clone()
    }

    fn get_current_directory(&self) -> String {
        self.cwd.clone()
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
}

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, serde::Serialize)]
pub struct Input {
    pub path: String,
    pub sha256: String,
    pub kind: InputKind,
}

/// Everything the analysis read: application sources by absolute path and
/// the other inputs whose change must invalidate the output.
pub struct Snapshot {
    pub sources: BTreeMap<String, String>,
    pub inputs: Vec<Input>,
}

pub struct Project {
    config: Rc<ParsedCommandLine>,
    /// SHA-256 of every file the compiler read, by path.
    reads: Rc<std::cell::RefCell<BTreeMap<String, String>>>,
    fs: Rc<dyn Fs>,
    cwd: String,
    version: &'static GoProgram,
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
}

fn deny_write() -> ts_goport::frontend::vfs::FsError {
    ts_goport::frontend::vfs::FsError::path(
        "write",
        "",
        std::io::Error::new(
            std::io::ErrorKind::PermissionDenied,
            "typeopt compiler filesystem is read-only",
        ),
    )
}

impl Project {
    pub fn load(path: &str, entries: Vec<String>) -> Result<Self> {
        Self::load_rooted(path, entries, Vec::new())
    }

    /// Loads the program rooted at `entries`, `roots` and declaration files.
    fn load_rooted(path: &str, entries: Vec<String>, extra_roots: Vec<String>) -> Result<Self> {
        let cwd = std::env::current_dir()?.to_string_lossy().into_owned();
        // Every file the compiler reads, including module-resolution
        // metadata such as package manifests, is part of the analysis
        // snapshot that consumers validate.
        let base = bundled::wrap_fs(osvfs_fs());
        let reads = Rc::new(std::cell::RefCell::new(BTreeMap::<String, String>::new()));
        let (reader, recorded) = (base.clone(), reads.clone());
        let fs = wrapvfs_wrap(
            base,
            Replacements {
                read_file: Some(Box::new(move |path| {
                    let (text, ok) = reader.read_file(path);
                    if ok && !path.starts_with("bundled:") {
                        recorded
                            .borrow_mut()
                            .entry(path.to_owned())
                            .or_insert_with(|| sha256_hex(text.as_bytes()));
                    }
                    (text, ok)
                })),
                write_file: Some(Box::new(|_, _| Err(deny_write()))),
                append_file: Some(Box::new(|_, _| Err(deny_write()))),
                remove: Some(Box::new(|_| Err(deny_write()))),
                chtimes: Some(Box::new(|_, _, _| Err(deny_write()))),
                ..Default::default()
            },
        );
        let host = Host {
            fs: fs.clone(),
            cwd: cwd.clone(),
        };
        let (config, errors) =
            get_parsed_command_line_of_config_file(path, None, None, &host, None);
        check_diagnostics(&errors)?;
        let config = config.ok_or_else(|| anyhow::anyhow!("cannot parse tsconfig"))?;
        check_diagnostics(&config.errors)?;
        ensure!(
            config
                .parsed_config
                .project_references
                .as_ref()
                .is_none_or(Vec::is_empty),
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
        let mut options = (**config.compiler_options()).clone();
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
                ModuleKind::ES_NEXT
                    | ModuleKind::ES2022
                    | ModuleKind::ES2020
                    | ModuleKind::ES2015
                    | ModuleKind::PRESERVE
            ),
            "set compilerOptions.module to an ESM mode (ESNext, ES2022, ES2020, ES2015 or \
             Preserve)"
        );
        ensure!(
            !matches!(options.jsx, JsxEmit::PRESERVE | JsxEmit::REACT_NATIVE),
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
        config.set_compiler_options(Rc::new(options));
        let config = Rc::new(config);
        let version = build(config.clone(), fs.clone(), &cwd);
        Ok(Self {
            config,
            reads,
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
        )?;
        if let Some(outside) = project.application_file_outside(dir) {
            bail!(
                "a bundle chunk imports a module outside the bundle directory, which cannot be \
                 published: {outside}"
            );
        }
        let (imported, unlinked, edges) = project.bundle_imported();
        // Chunks known to run as ES modules: imported by another chunk or
        // loaded by an HTML module script. Others count as modules only with
        // ESM statements; classic scripts stay unchanged boundaries.
        let HtmlScripts {
            modules: scripts,
            classic,
            imported: from_html,
            all_public,
        } = html_scripts(dir)?;
        if all_public {
            tracing::info!("an HTML page can reach any chunk; every chunk is a public entry");
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
        crate::app::set_bundle_modules(modules, classic);
        tracing::info!(
            chunks = chunks.len(),
            entries = project.entries.len(),
            "bundle program loaded"
        );
        project._bundle_config = Some(config_dir);
        Ok(project)
    }

    /// An application module of the program that lies outside `dir`.
    fn application_file_outside(&self, dir: &std::path::Path) -> Option<String> {
        let _scope = enter_program(Some(self.version));
        source_files()
            .into_iter()
            .filter(|&file| is_application(file))
            .map(|file| source_file_info(file).file_name.clone())
            .find(|name| !std::path::Path::new(name).starts_with(dir))
    }

    /// Bundle chunks that another chunk imports with an import or export
    /// declaration or a literal dynamic `import()`, and the subset that is
    /// imported through a specifier with a query, fragment or `%` escape.
    /// The compiler cannot link such a specifier to the chunk, so those
    /// chunks are public: what the importer reads stays unseen. Also returns
    /// the import edges between chunks.
    fn bundle_imported(&self) -> (Paths, Paths, BTreeMap<String, Paths>) {
        let _scope = enter_program(Some(self.version));
        let (mut imported, mut public) = (Paths::new(), Paths::new());
        let mut edges = BTreeMap::<String, Paths>::new();
        for file in source_files() {
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
            for node in nodes {
                let specifier = match node.kind() {
                    SyntaxKind::ImportDeclaration | SyntaxKind::ExportDeclaration => {
                        node.module_specifier()
                    }
                    SyntaxKind::CallExpression
                        if node.expression().kind() == SyntaxKind::ImportKeyword =>
                    {
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
                    continue;
                }
                let text = specifier.text();
                if !(text.starts_with("./") || text.starts_with("../")) {
                    continue;
                }
                // URL semantics: queries and fragments do not name files.
                let path = percent_decode(text.split(['?', '#']).next().unwrap_or_default());
                if let Ok(resolved) = base.join(&path).canonicalize() {
                    let resolved = resolved.to_string_lossy().into_owned();
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
        (imported, public, edges)
    }

    /// Checks the original program and records the analysis snapshot.
    pub fn snapshot(&self) -> Result<Snapshot> {
        let _scope = enter_program(Some(self.version));
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
            check_diagnostics(&get_syntactic_diagnostics(file))?;
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
                sha256: sha256_hex(&text),
                kind: InputKind::Configuration,
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
                        sha256: sha256_hex(source_file_text(file).as_bytes()),
                        path: name,
                        kind: InputKind::Dependency,
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
        for (path, sha256) in self.reads.borrow().iter() {
            if !known.contains(path) {
                inputs.push(Input {
                    path: path.clone(),
                    sha256: sha256.clone(),
                    kind: InputKind::Dependency,
                });
            }
        }
        inputs.sort();
        inputs.dedup();
        Ok(Snapshot { sources, inputs })
    }

    fn build_with(&self, texts: &BTreeMap<String, String>) -> &'static GoProgram {
        self.build_with_config(self.config.clone(), texts)
    }

    fn build_with_config(
        &self,
        config: Rc<ParsedCommandLine>,
        texts: &BTreeMap<String, String>,
    ) -> &'static GoProgram {
        let base = self.fs.clone();
        let texts = texts.clone();
        let fs = wrapvfs_wrap(
            self.fs.clone(),
            Replacements {
                read_file: Some(Box::new(move |path| {
                    texts
                        .get(path)
                        .map_or_else(|| base.read_file(path), |s| (s.clone(), true))
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
        let mut roots = self.config.file_names().to_vec();
        {
            let _scope = enter_program(Some(self.version));
            roots.extend(
                source_files()
                    .into_iter()
                    .filter(|&f| is_application(f))
                    .map(|f| source_file_info(f).file_name.clone()),
            );
        }
        roots.sort();
        roots.dedup();
        let version = self.build_with_config(Rc::new(self.config.with_file_names(roots)), changed);
        let result = (|| {
            let _scope = enter_program(Some(version));
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
            let result = ts_goport::emitter::program_emit::emit(
                ts_goport::emitter::program_emit::EmitOptions {
                    target_source_files: Some(
                        source_files()
                            .into_iter()
                            .filter(|f| is_application(*f) && !is_js(*f))
                            .collect(),
                    ),
                    write_file: Some(Arc::new(move |name, text, data| {
                        crate::app::check_cancelled().map_err(|e| e.to_string())?;
                        if data.source_file.is_nil() {
                            return Err("emitter omitted source identity".into());
                        }
                        let source = source_file_info(data.source_file).file_name.clone();
                        let mut guard = writes.lock().map_err(|_| "output lock poisoned")?;
                        let entry = guard.entry(source).or_default();
                        if name.ends_with(".map") {
                            entry.1 = text.to_owned();
                        } else {
                            entry.0 = text.to_owned();
                        }
                        Ok(())
                    })),
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
        release_program(version);
        result
    }
}
impl Drop for Project {
    fn drop(&mut self) {
        release_program(self.version);
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
    checked: Option<(u64, &'static GoProgram)>,
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

    fn program_for(&mut self, texts: &BTreeMap<String, String>) -> (&'static GoProgram, bool) {
        let key = texts_key(texts);
        if key == self.original {
            return (self.project.version, false);
        }
        if let Some((cached, version)) = self.checked
            && cached == key
        {
            return (version, false);
        }
        (self.project.build_with(texts), true)
    }

    fn retain(&mut self, key: u64, version: &'static GoProgram) {
        if let Some((_, old)) = self.checked.replace((key, version)) {
            release_program(old);
        }
    }
}

impl Drop for Backend<'_> {
    fn drop(&mut self) {
        if let Some((_, version)) = self.checked.take() {
            release_program(version);
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
            let _scope = enter_program(Some(version));
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
            release_program(version);
        }
        result
    }

    fn check(&mut self, texts: &BTreeMap<String, String>) -> Result<Vec<String>> {
        let (version, fresh) = self.program_for(texts);
        let result = (|| {
            let _scope = enter_program(Some(version));
            check_diagnostics(&get_program_diagnostics())?;
            let mut failing = Vec::new();
            for file in source_files() {
                crate::app::check_cancelled()?;
                if crate::app::bundle_mode() {
                    // Bundle edits are not parsed per candidate: a syntax
                    // error, including TypeScript-only syntax in a chunk,
                    // rolls the responsible bundle back.
                    let name = source_file_info(file).file_name.clone();
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
            (_, true) => release_program(version),
            _ => {}
        }
        result
    }

    fn emit_module(&mut self, file: &str, text: &str) -> Result<crate::pipeline::Emitted> {
        let mut hasher = std::collections::hash_map::DefaultHasher::new();
        text.hash(&mut hasher);
        let key = (file.to_owned(), hasher.finish());
        if let Some(emitted) = self.sizes.get(&key) {
            return Ok(emitted.clone());
        }
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
        let output = ts_goport::transpile::transpile_module(
            &ts_goport::gostd::context::background(),
            text,
            ts_goport::transpile::Options {
                compiler_options: Some(self.project.size_options.clone()),
                file_name: file.to_owned(),
                report_diagnostics: true,
            },
        )
        .context("size evaluation was cancelled")?;
        let syntax: Vec<_> = output
            .diagnostics
            .iter()
            .filter(|d| d.category == ts_goport::diagnostics::Category::Error)
            .collect();
        ensure!(
            syntax.is_empty(),
            "transformed module has syntax errors (TS{})",
            syntax[0].code
        );
        let emitted = crate::pipeline::Emitted {
            size: output.output_text.len(),
            imports: crate::pipeline::static_imports(&output.output_text),
        };
        self.sizes.insert(key, emitted.clone());
        Ok(emitted)
    }
}

fn build(config: Rc<ParsedCommandLine>, fs: Rc<dyn Fs>, cwd: &str) -> &'static GoProgram {
    let _scope = enter_program(None);
    let np = Rc::new(new_program(ProgramOptions {
        host: new_compiler_host(cwd, fs, &bundled::lib_path(), None, None, None),
        config,
        single_threaded: Tristate::True,
        use_source_of_project_reference: false,
        typings_location: String::new(),
        project_name: String::new(),
        create_module_resolver: None,
        skip_module_resolution: false,
    }));
    new_program_version(&np, None)
}

pub fn sha256_hex(bytes: &[u8]) -> String {
    use sha2::{Digest, Sha256};
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

fn check_diagnostics(diagnostics: &[Diagnostic]) -> Result<()> {
    if diagnostics.is_empty() {
        return Ok(());
    }
    let messages: Vec<_> = diagnostics
        .iter()
        .take(20)
        .map(|d| {
            let location = if d.file.is_some() {
                let (line, column) =
                    get_ecma_line_and_utf16_character_of_position(d.file, d.loc().pos());
                format!(
                    "{}:{}:{}: ",
                    source_file_info(d.file).file_name,
                    line + 1,
                    column + 1
                )
            } else {
                String::new()
            };
            if crate::app::VERBOSE.load(std::sync::atomic::Ordering::Relaxed) {
                format!("{location}TS{}: {}", d.code, redacted_message(d))
            } else {
                format!("{location}TS{} (use --verbose for details)", d.code)
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
fn redacted_message(d: &Diagnostic) -> String {
    if !d.message_text.is_empty() {
        // Pre-rendered external text has no template to keep.
        return REDACTED_ARGUMENT.to_owned();
    }
    let mut template = d.clone();
    template.message_args = vec![REDACTED_ARGUMENT.to_owned(); d.message_args.len()];
    template.localize(&ts_goport::locale::DEFAULT)
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

type Paths = std::collections::BTreeSet<String>;

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
            for tag in &tags {
                if tag.name == "base"
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
fn diagnostic_identities(diagnostics: &[Diagnostic]) -> Vec<String> {
    let mut identities: Vec<String> = diagnostics
        .iter()
        .map(|d| {
            let snippet = if d.file.is_some() {
                let text = source_file_text(d.file);
                let (pos, end) = (d.loc().pos() as usize, d.loc().end() as usize);
                text.get(pos.min(text.len())..end.min(text.len()))
                    .unwrap_or_default()
                    .trim()
                    .to_owned()
            } else {
                String::new()
            };
            format!(
                "{}\0{}\0{snippet}",
                d.code,
                d.localize(&ts_goport::locale::DEFAULT)
            )
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

/// Decodes `%XX` escapes of a URL path.
fn percent_decode(path: &str) -> String {
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

/// Whether a file uses `eval` (direct or aliased) or `with`, which can look
/// up the file's bindings by runtime strings, or `importScripts`, which only
/// a classic worker has: such a file may run as a classic script whose
/// top-level bindings are globals, even if a module also imports it.
pub(crate) fn uses_eval_or_with(nodes: &[Node]) -> bool {
    nodes.iter().any(|node| {
        node.kind() == SyntaxKind::WithStatement
            || (node.kind() == SyntaxKind::Identifier
                && matches!(node.text(), "eval" | "importScripts"))
    })
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
        ) || has_syntactic_modifier(statement, ModifierFlags::EXPORT)
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
    ts_goport::scanner_util::get_token_pos_of_node(node, get_source_file_of_node(node), false)
        as usize
}
