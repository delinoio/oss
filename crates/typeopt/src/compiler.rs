//! All access to the pinned compiler lives in this adapter. The allocator only
//! receives closed-region facts and byte spans, never compiler-owned
//! identities.
use std::{
    collections::{BTreeMap, HashMap},
    rc::Rc,
    sync::{Arc, Mutex},
};

use anyhow::{Result, bail, ensure};
use ts_goport::{
    frontend::{
        bundled,
        compiler::{ProgramOptions, new_compiler_host, new_program},
        tsoptions::{ParseConfigHost, ParsedCommandLine, get_parsed_command_line_of_config_file},
        vfs::{Fs, Replacements, osvfs_fs, wrapvfs_wrap},
    },
    prelude::*,
};

use crate::optimizer::{Object, Occurrence, Preservation, Property};

mod flow;

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

pub struct Project {
    config: Rc<ParsedCommandLine>,
    fs: Rc<dyn Fs>,
    cwd: String,
    pub version: &'static GoProgram,
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
        let cwd = std::env::current_dir()?.to_string_lossy().into_owned();
        let fs = wrapvfs_wrap(
            bundled::wrap_fs(osvfs_fs()),
            Replacements {
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
        // Include ambient declarations discovered by tsconfig, but root runtime
        // traversal at the explicit entries rather than every include match.
        let mut roots = entries;
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
            fs,
            cwd,
            version,
        })
    }

    pub fn analyze(&self) -> Result<(Vec<Object>, BTreeMap<String, String>)> {
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
            check_diagnostics(&get_semantic_diagnostics(file))?;
        }
        let mut objects = Vec::new();
        let mut sources = BTreeMap::new();
        for file in files {
            crate::app::check_cancelled()?;
            let name = &source_file_info(file).file_name;
            ensure!(
                !name.ends_with(".cts")
                    || name.ends_with(".d.cts")
                    || is_source_file_from_external_library(file),
                "CommonJS TypeScript (.cts) is unsupported; use an ESM source module"
            );
            if !is_application(file) {
                continue;
            }
            let name = source_file_info(file).file_name.clone();
            sources.insert(name, source_file_text(file).to_string());
            // Check forbidden constructs even in untransformed application JS.
            let mut nodes = Vec::new();
            collect(file, &mut nodes);
            for node in &nodes {
                ensure!(
                    node.kind() != SyntaxKind::WithStatement,
                    "with is unsupported"
                );
                ensure!(
                    !(node.kind() == SyntaxKind::Identifier && node.text() == "eval"),
                    "eval is unsupported (including aliases); remove it before optimizing"
                );
            }
            if is_js(file) {
                continue;
            }
            let facts =
                with_type_checker_for_file(file, move |checker| analyze_file(file, checker));
            objects.extend(facts?);
        }
        ensure!(
            !sources.is_empty(),
            "program contains no supported runtime source; select a TypeScript or JavaScript entry"
        );
        Ok((objects, sources))
    }

    pub fn emit(
        &self,
        changed: BTreeMap<String, String>,
    ) -> Result<BTreeMap<String, (String, String)>> {
        crate::app::check_cancelled()?;
        let base = self.fs.clone();
        let fs = wrapvfs_wrap(
            self.fs.clone(),
            Replacements {
                read_file: Some(Box::new(move |path| {
                    changed
                        .get(path)
                        .map_or_else(|| base.read_file(path), |s| (s.clone(), true))
                })),
                ..Default::default()
            },
        );
        tracing::debug!("loading transformed program");
        let version = build(self.config.clone(), fs, &self.cwd);
        let result = (|| {
            let _scope = enter_program(Some(version));
            // Recheck transformed files: this catches accidental structural
            // constraints missed by the closed-region proof before publishing.
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
fn is_js(file: Node) -> bool {
    let n = &source_file_info(file).file_name;
    n.ends_with(".js") || n.ends_with(".jsx") || n.ends_with(".mjs") || n.ends_with(".cjs")
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

fn span(node: Node, source: &str, shorthand: bool, quoted: bool) -> Occurrence {
    // Compiler positions are UTF-8 byte offsets including leading trivia.
    let end = node.end() as usize;
    let start =
        ts_goport::scanner_util::get_token_pos_of_node(node, get_source_file_of_node(node), false)
            as usize;
    assert!(source.is_char_boundary(start) && source.is_char_boundary(end));
    Occurrence {
        start,
        end,
        shorthand,
        quoted,
    }
}

fn analyze_file(file: Node, checker: &mut Checker) -> Result<Vec<Object>> {
    let source = source_file_text(file).to_string();
    let name = source_file_info(file).file_name.clone();
    let mut nodes = Vec::new();
    collect(file, &mut nodes);
    let mut objects = Vec::<Object>::new();
    let mut allocations = HashMap::new();
    for &node in &nodes {
        if node.kind() != SyntaxKind::ObjectLiteralExpression {
            continue;
        }
        let declaration = node.parent();
        let local = declaration.kind() == SyntaxKind::VariableDeclaration
            && declaration.initializer() == node
            && declaration.name().kind() == SyntaxKind::Identifier;
        let mut object = Object {
            file: name.clone(),
            start: node.pos() as usize,
            properties: Vec::new(),
            preservation: None,
        };
        if !is_external_module(file) {
            object.preservation = Some(Preservation::Exported);
        } else if local && declaration.type_().is_some() {
            object.preservation = Some(Preservation::TypeConstraint);
        }
        for property in node.properties().iter() {
            let property_name = property.name();
            if !matches!(
                property.kind(),
                SyntaxKind::PropertyAssignment | SyntaxKind::ShorthandPropertyAssignment
            ) || !matches!(
                property_name.kind(),
                SyntaxKind::Identifier | SyntaxKind::StringLiteral
            ) {
                object.preservation = Some(Preservation::ExternalOrUnsupportedShape);
                continue;
            }
            let mut value_nodes = Vec::new();
            if property.kind() == SyntaxKind::PropertyAssignment {
                collect(property.initializer(), &mut value_nodes);
            }
            if value_nodes.iter().any(|n| {
                matches!(
                    n.kind(),
                    SyntaxKind::FunctionExpression
                        | SyntaxKind::ArrowFunction
                        | SyntaxKind::ClassExpression
                )
            }) {
                // Renaming an inferred function/class name changes .name.
                object.preservation = Some(Preservation::ObservableOperation);
            }
            let key = property_name.text().to_string();
            if ["__proto__", "constructor", "prototype", "then", "toJSON"].contains(&key.as_str()) {
                object.preservation = Some(Preservation::SpecialProperty);
            }
            let occurrence = span(
                property_name,
                &source,
                property.kind() == SyntaxKind::ShorthandPropertyAssignment,
                property_name.kind() == SyntaxKind::StringLiteral,
            );
            if let Some(existing) = object.properties.iter_mut().find(|p| p.name == key) {
                existing.occurrences.push(occurrence);
            } else {
                object.properties.push(Property {
                    name: key,
                    occurrences: vec![occurrence],
                });
            }
        }
        if local {
            // Type evidence is necessary, but never sufficient: actual
            // references below must also stay within this fresh
            // object's closed region.
            let ty = checker.get_type_at_location(declaration.name());
            let typed_properties = checker.get_properties_of_type(ty);
            if typed_properties.len() != object.properties.len() {
                object.preservation = Some(Preservation::TypeConstraint);
            }
        }
        allocations.insert(node, objects.len());
        objects.push(object);
    }
    flow::analyze(&nodes, checker, &source, objects, allocations)
}
