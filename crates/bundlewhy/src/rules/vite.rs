//! Vite rules backed by Rolldown module info, rendered modules and engine logs.

use std::collections::BTreeSet;

use super::{Context, DiagnosticDraft, RuleId};
use crate::model::{Condition, ConditionOutcome, Evidence, EvidenceStatus};

const MODULE_INFO: &str = "rolldown module info (getModuleInfo)";
const RENDERED: &str = "rolldown rendered chunk modules";
const INFERENCE: &str = "bundlewhy inference from rendered modules";

/// True when the resolved configuration disables Rolldown tree shaking.
pub fn tree_shaking_disabled(collection: &crate::collect::Collection) -> bool {
    collection
        .config_summary
        .get("build.rolldownOptions.treeshake")
        .is_some_and(|value| value == "false")
}

pub fn run(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    if tree_shaking_disabled(context.collection) {
        tree_shaking_disabled_finding(context, drafts);
        // Rendered exports reflect configuration, not tree-shaking decisions.
        commonjs_wrapped(context, drafts);
        ineffective_dynamic_import(context, drafts);
        return;
    }
    side_effect_retained(context, drafts);
    commonjs_wrapped(context, drafts);
    ineffective_dynamic_import(context, drafts);
}

fn side_effect_retained(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let collection = context.collection;
    // A module requested by a dynamic `import()` is the facade of a dynamic
    // entry chunk. Rolldown keeps that module because the import asked for it,
    // so an export-less one is a requested module, not an unrequested one.
    let dynamic_facades: BTreeSet<&str> = collection
        .chunks
        .iter()
        .filter(|chunk| chunk.is_dynamic_entry)
        .filter_map(|chunk| chunk.facade_module_id.as_deref())
        .collect();
    for module in &collection.modules {
        // Export-less modules (for example `import "./polyfill"`) qualify:
        // rendered code with no rendered export is the evidence.
        let no_rendered_exports = module.used_exports.as_ref().is_some_and(Vec::is_empty);
        let esm = module.input_format.as_deref() == Some("es");
        // Scripts that an HTML entry loads are entry points too: their
        // top-level code is meant to run.
        let html_entry = !module.importers.is_empty()
            && module
                .importers
                .iter()
                .all(|importer| importer.ends_with(".html"));
        if !module.included
            || module.is_entry
            || html_entry
            || dynamic_facades.contains(module.id.as_str())
            || !esm
            || !no_rendered_exports
            || module.rendered_length == 0
        {
            continue;
        }
        let display = context.module_display(module);
        let located = context.module_location(module, 0, 0);
        let values = DeclaredValues {
            module: module.module_side_effects.as_deref().unwrap_or("null"),
            option: module.treeshake_side_effects.as_deref().unwrap_or("null"),
            package: module
                .package_side_effects
                .as_deref()
                .unwrap_or(NOT_DECLARED),
        };
        let declaration = SideEffectDeclaration::from_collected(&values);
        // The title feeds the diagnostic ID, so it stays the same however the
        // declaration changes and one retained module keeps one ID.
        let title = format!("{display} is retained although none of its exports is used");
        let (status, summary) = match declaration {
            // An effect declaration is itself the engine-visible reason the
            // module is kept.
            SideEffectDeclaration::Effectful(source) => (
                EvidenceStatus::Confirmed,
                format!(
                    "Rolldown rendered {} bytes of this module although none of its exports is \
                     used. {} declares side effects for it, so Rolldown keeps it.",
                    module.rendered_length,
                    source.subject()
                ),
            ),
            // A side-effect-free module is discarded when nothing uses it, so
            // the retention points to another use of the module.
            SideEffectDeclaration::Free(source) => (
                EvidenceStatus::Inferred,
                format!(
                    "Rolldown rendered {} bytes of this module although none of its exports is \
                     used, and {} declares it side-effect free. Rolldown may discard such a \
                     module even when its code has side effects, so another use of the module \
                     probably keeps it, but Rolldown did not report why.",
                    module.rendered_length,
                    source.subject()
                ),
            ),
            // Rolldown reports no retention reason for rendered modules, so
            // the cause stays an inference from the observed facts.
            SideEffectDeclaration::Analyzed
            | SideEffectDeclaration::Undeclared
            | SideEffectDeclaration::Unresolved(_) => (
                EvidenceStatus::Inferred,
                format!(
                    "Rolldown rendered {} bytes of this module although none of its exports is \
                     used, and did not report why. The module may contain top-level code with \
                     side effects that Rolldown keeps.",
                    module.rendered_length
                ),
            ),
        };
        let (outcome, declared_detail) = declaration.condition(&values);
        let mut evidence = vec![
            Evidence::new(
                MODULE_INFO,
                EvidenceStatus::Confirmed,
                format!(
                    "Exports: {}",
                    module.exports.as_deref().unwrap_or_default().join(", ")
                ),
            ),
            Evidence::new(
                RENDERED,
                EvidenceStatus::Confirmed,
                format!(
                    "Rendered {} bytes with no rendered exports in {}",
                    module.rendered_length,
                    module.chunks.join(", ")
                ),
            ),
        ];
        match declaration {
            SideEffectDeclaration::Effectful(_) => {}
            SideEffectDeclaration::Free(_) => evidence.push(Evidence::new(
                INFERENCE,
                EvidenceStatus::Inferred,
                "Rolldown reports no retention reason; a remaining use of the module is inferred \
                 from its retained bytes, because a side-effect-free module is discarded when \
                 nothing uses it",
            )),
            _ => evidence.push(Evidence::new(
                INFERENCE,
                EvidenceStatus::Inferred,
                "Rolldown reports no retention reason; top-level side effects are inferred from \
                 the retained bytes",
            )),
        }
        drafts.push(DiagnosticDraft {
            rule: RuleId::ViteSideEffectRetained,
            status,
            title,
            summary,
            conditions: vec![
                Condition::new(
                    "No export of the module is used",
                    ConditionOutcome::Met,
                    "renderedExports is empty",
                ),
                Condition::new(
                    "The module is declared side-effect free",
                    outcome,
                    declared_detail,
                ),
                Condition::new(
                    "No rendered code remains",
                    ConditionOutcome::Unmet,
                    format!("renderedLength: {} bytes", module.rendered_length),
                ),
            ],
            located,
            module: display,
            dependency_paths: context.dependency_paths(&module.id),
            evidence,
            direction: declaration.direction().to_owned(),
            safety: declaration.safety().to_owned(),
            dependency: context.is_dependency(module),
            export_usage: None,
        });
    }
}

/// The collected `package` value when the package has no `sideEffects` field.
const NOT_DECLARED: &str = "not declared";

/// The collected values that Rolldown consults, in precedence order, to decide
/// whether a module has side effects.
struct DeclaredValues<'a> {
    /// `moduleSideEffects` from `getModuleInfo()`: the value that a JavaScript
    /// resolve, load or transform hook set for the module (`null` when none
    /// did). Native plugins, including Vite's own resolver, do not show up
    /// here, and the resolver turns a declared package `sideEffects` field into
    /// a value of this kind that outranks the configured option.
    module: &'a str,
    /// What the configured `treeshake.moduleSideEffects` decides for the module
    /// (`null` when it gives no verdict).
    option: &'a str,
    /// The `sideEffects` field of the nearest `package.json`.
    package: &'a str,
}

impl DeclaredValues<'_> {
    fn describe(&self) -> String {
        format!(
            "moduleSideEffects: {}; treeshake.moduleSideEffects: {}; package sideEffects: {}",
            self.module, self.option, self.package
        )
    }

    fn package_declared(&self) -> bool {
        self.package != NOT_DECLARED
    }
}

/// Where an effective side-effect declaration comes from.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum DeclaredBy {
    /// A `moduleSideEffects` value that a JavaScript resolve, load or transform
    /// hook set for the module.
    Hook,
    /// The configured `build.rolldownOptions.treeshake.moduleSideEffects`.
    TreeshakeOption,
}

impl DeclaredBy {
    /// The declaration as the subject of a sentence.
    fn subject(self) -> &'static str {
        match self {
            Self::Hook => "A hook's moduleSideEffects value",
            Self::TreeshakeOption => "The configured treeshake.moduleSideEffects",
        }
    }

    /// The declaration with the precedence that makes it the effective one.
    fn label(self) -> &'static str {
        match self {
            Self::Hook => {
                "a hook's moduleSideEffects value, which takes precedence over \
                 treeshake.moduleSideEffects and package sideEffects"
            }
            Self::TreeshakeOption => {
                "treeshake.moduleSideEffects, which takes precedence over package sideEffects"
            }
        }
    }
}

/// The source whose value bundlewhy cannot evaluate.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum UnresolvedBy {
    /// A `moduleSideEffects` value from a JavaScript hook with an unrecognized
    /// shape.
    Hook,
    /// A configured `treeshake.moduleSideEffects` that is a function or has an
    /// unrecognized shape.
    TreeshakeOption,
    /// The `sideEffects` field of the nearest `package.json`. Vite's resolver
    /// can turn a declared field into a hook value that outranks the configured
    /// option, and bundlewhy cannot read that value, so a declared field leaves
    /// the effective declaration unknown.
    Package,
}

/// What the collected values say about a module that Rolldown rendered without
/// any used export, following Rolldown's precedence: a `moduleSideEffects`
/// value that a JavaScript hook set, then the configured
/// `treeshake.moduleSideEffects`, then the package `sideEffects` field, then
/// Rolldown's own code analysis. Values that native plugins set are not
/// visible, so a declared package field is never treated as the effective
/// declaration.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum SideEffectDeclaration {
    /// The effective declaration is side-effect free (`false`).
    Free(DeclaredBy),
    /// The effective declaration keeps the module (`true` from the configured
    /// option, or `"no-treeshake"` from a hook).
    Effectful(DeclaredBy),
    /// A JavaScript hook returned `true`, which asks Rolldown for its default
    /// analysis of the code and skips the configured option and the package
    /// field.
    Analyzed,
    /// Nothing is declared; Rolldown infers the effects from the code.
    Undeclared,
    /// The collected values cannot be matched to this file, so the effective
    /// declaration is unknown.
    Unresolved(UnresolvedBy),
}

impl SideEffectDeclaration {
    fn from_collected(values: &DeclaredValues<'_>) -> Self {
        match values.module {
            "false" => Self::Free(DeclaredBy::Hook),
            "no-treeshake" => Self::Effectful(DeclaredBy::Hook),
            "true" => Self::Analyzed,
            // A declared package may have given the module a value through a
            // native plugin that outranks the configured option and the
            // package field itself. That value is not collected, so the
            // declaration stays unknown.
            "null" | "undefined" if values.package_declared() => {
                Self::Unresolved(UnresolvedBy::Package)
            }
            "null" | "undefined" => match values.option {
                "false" => Self::Free(DeclaredBy::TreeshakeOption),
                "true" => Self::Effectful(DeclaredBy::TreeshakeOption),
                // No verdict from the option and no package field: Rolldown
                // analyzes the code.
                "null" => Self::Undeclared,
                // A function or an unrecognized option shape is not evaluated.
                _ => Self::Unresolved(UnresolvedBy::TreeshakeOption),
            },
            // An unrecognized hook value cannot be matched to the other values.
            _ => Self::Unresolved(UnresolvedBy::Hook),
        }
    }

    fn condition(self, values: &DeclaredValues<'_>) -> (ConditionOutcome, String) {
        let values = values.describe();
        match self {
            Self::Free(source) => (
                ConditionOutcome::Met,
                format!("Declared side-effect free by {}; {values}", source.label()),
            ),
            Self::Effectful(source) => (
                ConditionOutcome::Unmet,
                format!("Side effects are declared by {}; {values}", source.label()),
            ),
            Self::Analyzed => (
                ConditionOutcome::Unmet,
                format!(
                    "A hook's moduleSideEffects: true asks Rolldown to analyze the code and skips \
                     treeshake.moduleSideEffects and package sideEffects; {values}"
                ),
            ),
            Self::Undeclared => (
                ConditionOutcome::Unmet,
                format!("No side-effect declaration (null means inferred from code); {values}"),
            ),
            Self::Unresolved(source) => {
                let reason = match source {
                    UnresolvedBy::Hook => "a hook's moduleSideEffects value is not recognized",
                    UnresolvedBy::TreeshakeOption => {
                        "treeshake.moduleSideEffects is a value bundlewhy does not evaluate"
                    }
                    UnresolvedBy::Package => {
                        "the package declares sideEffects, which can give this file a value that \
                         bundlewhy cannot read or match to it"
                    }
                };
                (
                    ConditionOutcome::Unknown,
                    format!(
                        "The collected summary cannot show whether this file is declared \
                         side-effect free: {reason}; {values}"
                    ),
                )
            }
        }
    }

    fn direction(self) -> &'static str {
        match self {
            Self::Free(_) => {
                "The module is already declared side-effect free, so adding a declaration will not \
                 help. Check which importers still use it, such as side-effect imports, namespace \
                 or re-export use, or an entry, and remove that use if it is unneeded. If no \
                 importer uses it, report the retained module to the bundler."
            }
            Self::Effectful(DeclaredBy::Hook) => {
                "Move top-level side effects into an exported function that callers invoke \
                 explicitly, or change the plugin that returns moduleSideEffects \"no-treeshake\" \
                 for this file so that it covers only the files that do have side effects. Some of \
                 Vite's own plugins return it for HTML, CSS and entry assets."
            }
            Self::Effectful(DeclaredBy::TreeshakeOption) => {
                "Move top-level side effects into an exported function that callers invoke \
                 explicitly, or narrow build.rolldownOptions.treeshake.moduleSideEffects so that \
                 it marks only the files that do have side effects."
            }
            Self::Analyzed => {
                "Move top-level side effects into an exported function that callers invoke \
                 explicitly, or stop the plugin from returning moduleSideEffects: true for this \
                 file. That value makes Rolldown analyze the code and read neither \
                 treeshake.moduleSideEffects nor the package \"sideEffects\" field."
            }
            Self::Undeclared => {
                "Move top-level side effects into an exported function that callers invoke \
                 explicitly, or declare the package side-effect free with \"sideEffects\": false \
                 (or a list of files that do have side effects) in package.json."
            }
            Self::Unresolved(UnresolvedBy::Hook) => {
                "Check the plugin that sets moduleSideEffects for this file; its value was not \
                 recognized. If the module has no needed side effect, make the plugin declare it \
                 side-effect free; otherwise move top-level side effects into an exported function \
                 that callers invoke explicitly."
            }
            Self::Unresolved(UnresolvedBy::TreeshakeOption) => {
                "Check what build.rolldownOptions.treeshake.moduleSideEffects decides for this \
                 file; it takes precedence over the package \"sideEffects\" field. If it marks the \
                 file as having side effects and the module has no needed side effect, narrow it. \
                 Otherwise move top-level side effects into an exported function that callers \
                 invoke explicitly."
            }
            Self::Unresolved(UnresolvedBy::Package) => {
                "The package declares \"sideEffects\", which can decide this file ahead of \
                 treeshake.moduleSideEffects, but bundlewhy cannot tell how the declaration \
                 applies to the file. A blanket true or a list entry that covers the file marks it \
                 as having side effects, so a retained file is most likely covered by one. Find \
                 the entry that matches it and narrow or remove that entry if the module has no \
                 needed side effect; adding the file to a list would declare the opposite. A \
                 declaration of false leaves another use of the module as the cause. Otherwise \
                 move top-level side effects into an exported function that callers invoke \
                 explicitly."
            }
        }
    }

    fn safety(self) -> &'static str {
        match self {
            Self::Free(_) => {
                "Do not remove an importer that evaluates the module for its effect (polyfills, \
                 registrations, CSS imports); the effect would be silently lost."
            }
            _ => {
                "Only declare modules side-effect free when no importer depends on evaluating them \
                 for their effect (polyfills, registrations, CSS imports). Otherwise the effect is \
                 silently removed."
            }
        }
    }
}

/// Id of the module Vite synthesizes as an empty browser stand-in. Vite emits
/// it as `__vite-browser-external` (optionally behind the virtual-module NUL
/// prefix). It is produced for a Node built-in imported by browser-targeted
/// code and also for a dependency file that its `browser` field maps to
/// `false`. Vite appends `:<specifier>` only for built-ins.
const BROWSER_EXTERNAL_ID: &str = "__vite-browser-external";

/// What the collected id of a Vite browser-external placeholder reveals about
/// the module it stands in for.
#[derive(Debug, Clone, PartialEq, Eq)]
enum BrowserExternalKind {
    /// The id retains a built-in suffix (for example `:fs`), so the original
    /// specifier is a Node built-in.
    NodeBuiltin,
    /// The bare id. It does not reveal the original specifier: it can stand in
    /// for a Node built-in or for a file the dependency maps to `false` in its
    /// `browser` field.
    Unspecified,
}

/// Classifies a Vite-generated browser-external placeholder module: collected
/// as virtual (no file path) with the browser-external id. Returns `None` for
/// every other module.
fn vite_browser_external_kind(
    module: &crate::collect::CollectedModule,
) -> Option<BrowserExternalKind> {
    if !module.is_virtual || module.path.is_some() {
        return None;
    }
    let rest = module
        .id
        .strip_prefix('\0')
        .unwrap_or(&module.id)
        .strip_prefix(BROWSER_EXTERNAL_ID)?;
    if rest.is_empty() {
        Some(BrowserExternalKind::Unspecified)
    } else if rest.strip_prefix(':').is_some_and(|name| !name.is_empty()) {
        Some(BrowserExternalKind::NodeBuiltin)
    } else {
        None
    }
}

fn commonjs_wrapped(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let collection = context.collection;
    let helper = collection
        .modules
        .iter()
        .filter(|module| module.id.contains("rolldown/runtime"))
        .flat_map(|module| module.used_exports.iter().flatten())
        .find(|name| name.starts_with("__commonJS"))
        .cloned();
    for module in &collection.modules {
        if !module.included || module.input_format.as_deref() != Some("cjs") {
            continue;
        }
        // The rule needs both facts: the CommonJS input format and the
        // rendered wrapper helper. Without the helper there is no evidence of
        // wrapping, so no finding is reported.
        let Some(helper) = helper.as_ref() else {
            continue;
        };
        let display = context.module_display(module);
        let located = context.module_location(module, 0, 0);
        let mut evidence = vec![Evidence::new(
            MODULE_INFO,
            EvidenceStatus::Confirmed,
            "Rolldown detected the input format as cjs.",
        )];
        evidence.push(Evidence::new(
            "rolldown runtime rendered exports",
            EvidenceStatus::Confirmed,
            format!("The runtime helper {helper} is rendered in the bundle."),
        ));
        let generated = vite_browser_external_kind(module);
        if let Some(kind) = &generated {
            let detail = match kind {
                BrowserExternalKind::NodeBuiltin => {
                    "The module is virtual (no file path) and its id is Vite's browser-external \
                     placeholder with a built-in suffix, which Vite generates for a Node built-in \
                     imported by browser-targeted code."
                }
                BrowserExternalKind::Unspecified => {
                    "The module is virtual (no file path) and its id is Vite's bare \
                     browser-external placeholder, which Vite generates as an empty browser \
                     stand-in. The id does not show whether the original specifier was a Node \
                     built-in or a file that a dependency maps to false in its browser field."
                }
            };
            evidence.push(Evidence::new(
                MODULE_INFO,
                EvidenceStatus::Confirmed,
                detail,
            ));
        }
        let (summary, direction, safety) = match generated {
            Some(BrowserExternalKind::NodeBuiltin) => (
                "Rolldown wraps CommonJS modules in a runtime closure instead of hoisting them \
                 into the chunk scope. This module is a placeholder that Vite generates for a \
                 Node built-in imported by browser-targeted code; it has no source file, must not \
                 be edited, and has no ECMAScript module build to import.",
                "Follow the dependency path to the importer that pulls the Node built-in into the \
                 browser bundle, then decide for browser compatibility: remove or replace that \
                 usage, alias or polyfill the built-in for the browser, or keep the import \
                 server-only.",
                "Do not edit or convert the generated placeholder. Confirm that the importer does \
                 not need the built-in's behavior in the browser before removing or replacing the \
                 usage; the placeholder exposes no real implementation.",
            ),
            Some(BrowserExternalKind::Unspecified) => (
                "Rolldown wraps CommonJS modules in a runtime closure instead of hoisting them \
                 into the chunk scope. This module is a placeholder that Vite generates as an \
                 empty browser stand-in for a module that is not available in the browser; it has \
                 no source file, must not be edited, and has no ECMAScript module build to import.",
                "Follow the dependency path to the importer that pulls the externalized module \
                 into the browser bundle, then identify the original specifier from that \
                 importer: a Node built-in or a file that the dependency maps to false in its \
                 browser field. Decide for browser compatibility whether to remove or replace \
                 that usage, provide a browser-compatible replacement, or keep the import \
                 server-only.",
                "Do not edit or convert the generated placeholder. Confirm that the importer does \
                 not need the original module's behavior in the browser before removing or \
                 replacing the usage; the placeholder exposes no real implementation.",
            ),
            None => (
                "Rolldown wraps CommonJS modules in a runtime closure instead of hoisting them \
                 into the chunk scope. The wrapper, its interop helpers and exports that cannot \
                 be analyzed statically stay in the bundle.",
                "Import an ECMAScript module build of this code (check the package's \"exports\" \
                 or \"module\" field) or convert the module to ESM syntax.",
                "Confirm that the ESM build exposes the same API and evaluation order; CommonJS \
                 modules evaluate lazily on first require.",
            ),
        };
        let status = EvidenceStatus::Confirmed;
        drafts.push(DiagnosticDraft {
            rule: RuleId::ViteCommonjsWrapped,
            status,
            title: format!("{display} is wrapped as CommonJS"),
            summary: summary.to_owned(),
            conditions: vec![
                Condition::new(
                    "The module is an ECMAScript module",
                    ConditionOutcome::Unmet,
                    "inputFormat: cjs",
                ),
                Condition::new(
                    "The module is hoisted without a runtime wrapper",
                    ConditionOutcome::Unmet,
                    format!("{helper} rendered"),
                ),
            ],
            located,
            module: display,
            dependency_paths: context.dependency_paths(&module.id),
            evidence,
            direction: direction.to_owned(),
            safety: safety.to_owned(),
            dependency: context.is_dependency(module),
            export_usage: None,
        });
    }
}

fn ineffective_dynamic_import(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let collection = context.collection;
    let logged: BTreeSet<&str> = collection
        .logs
        .iter()
        .filter(|log| log.code.as_deref() == Some("INEFFECTIVE_DYNAMIC_IMPORT"))
        .filter_map(|log| log.id.as_deref())
        .collect();
    let dynamic_entry_chunks: BTreeSet<&str> = collection
        .chunks
        .iter()
        .filter(|chunk| chunk.is_dynamic_entry)
        .map(|chunk| chunk.file.as_str())
        .collect();
    for module in &collection.modules {
        let graph_blocked = !module.importers.is_empty()
            && !module.dynamic_importers.is_empty()
            && !module
                .chunks
                .iter()
                .any(|chunk| dynamic_entry_chunks.contains(chunk.as_str()));
        let engine_reported = logged.contains(module.id.as_str());
        if !module.included || !(graph_blocked || engine_reported) {
            continue;
        }
        let display = context.module_display(module);
        let located = context.module_location(module, 0, 0);
        let static_importers: Vec<String> = module
            .importers
            .iter()
            .map(|id| context.module_display_by_id(id))
            .collect();
        let dynamic_importers: Vec<String> = module
            .dynamic_importers
            .iter()
            .map(|id| context.module_display_by_id(id))
            .collect();
        let mut evidence = vec![Evidence::new(
            MODULE_INFO,
            EvidenceStatus::Confirmed,
            format!(
                "Static importers: {}. Dynamic importers: {}.",
                static_importers.join(", "),
                dynamic_importers.join(", ")
            ),
        )];
        if engine_reported {
            evidence.push(Evidence::new(
                "rolldown log INEFFECTIVE_DYNAMIC_IMPORT",
                EvidenceStatus::Confirmed,
                "Rolldown reported that the dynamic import will not move the module into another \
                 chunk.",
            ));
        }
        drafts.push(DiagnosticDraft {
            rule: RuleId::ViteIneffectiveDynamicImport,
            status: EvidenceStatus::Confirmed,
            title: format!("Dynamic import of {display} does not create a separate chunk"),
            summary: "The module is also imported statically, so it is bundled with its static \
                      importer and the dynamic import only defers evaluation, not loading."
                .to_owned(),
            conditions: vec![
                Condition::new(
                    "The module is imported only dynamically",
                    ConditionOutcome::Unmet,
                    format!("Static importers: {}", static_importers.join(", ")),
                ),
                Condition::new(
                    "The module is emitted in its own dynamic chunk",
                    ConditionOutcome::Unmet,
                    format!("Emitted in: {}", module.chunks.join(", ")),
                ),
            ],
            located,
            module: display,
            dependency_paths: context.dependency_paths(&module.id),
            evidence,
            direction: "Remove the static imports so every importer loads the module with \
                        import(), or remove the dynamic import if the module is needed at startup."
                .to_owned(),
            safety: "Replacing a static import with import() makes the module load \
                     asynchronously; callers must await it and handle loading failures."
                .to_owned(),
            dependency: context.is_dependency(module),
            export_usage: None,
        });
    }
}

fn tree_shaking_disabled_finding(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let config_file = context.collection.config_file.clone();
    let located = match &config_file {
        Some(file) => {
            context.original_location_without_coverage_gap(std::path::Path::new(file), 0, 0)
        }
        None => super::Located {
            location: None,
            generated: None,
            snippet: None,
        },
    };
    let module = config_file
        .as_deref()
        .map(|file| context.display_path(std::path::Path::new(file)))
        .unwrap_or_default();
    drafts.push(DiagnosticDraft {
        rule: RuleId::ViteTreeShakingDisabled,
        status: EvidenceStatus::Confirmed,
        title: "Tree shaking is disabled".to_owned(),
        summary: "build.rolldownOptions.treeshake is false, so Rolldown keeps unused exports and \
                  modules. Tree-shaking findings do not apply to this build."
            .to_owned(),
        conditions: vec![Condition::new(
            "Tree shaking is enabled",
            ConditionOutcome::Unmet,
            "build.rolldownOptions.treeshake: false",
        )],
        located,
        module,
        dependency_paths: Vec::new(),
        evidence: vec![Evidence::new(
            "resolved Vite configuration",
            EvidenceStatus::Confirmed,
            "build.rolldownOptions.treeshake: false",
        )],
        direction: "Remove `treeshake: false` from build.rolldownOptions (or the legacy \
                    rollupOptions) for production builds."
            .to_owned(),
        safety: "Code that relies on unused modules running for their side effects must keep \
                 those imports or declare the side effects."
            .to_owned(),
        dependency: false,
        export_usage: None,
    });
}

#[cfg(test)]
mod tests {
    use super::{
        DeclaredBy::{Hook, TreeshakeOption},
        SideEffectDeclaration::{Analyzed, Effectful, Free, Undeclared, Unresolved},
        UnresolvedBy, *,
    };

    #[test]
    fn side_effect_declaration_precedence_table() {
        // (JavaScript hook value, treeshake.moduleSideEffects, package
        // sideEffects, effective declaration), following Rolldown's order: hook
        // value, configured option, package field, code analysis. A declared
        // package field can reach the module through a native plugin whose
        // value outranks the option, so it leaves the declaration unknown.
        let table = [
            // A JavaScript hook value wins over every other value.
            ("false", "true", "true", Free(Hook)),
            ("false", "false", "not declared", Free(Hook)),
            ("false", "true", "[array of 2]", Free(Hook)),
            ("no-treeshake", "false", "false", Effectful(Hook)),
            ("no-treeshake", "null", "not declared", Effectful(Hook)),
            // `true` asks for the default analysis and skips option and package.
            ("true", "false", "false", Analyzed),
            ("true", "false", "not declared", Analyzed),
            ("true", "true", "true", Analyzed),
            ("true", "[function]", "[array of 2]", Analyzed),
            // Without a hook value and a package field the option decides.
            ("null", "false", "not declared", Free(TreeshakeOption)),
            ("undefined", "false", "not declared", Free(TreeshakeOption)),
            ("null", "true", "not declared", Effectful(TreeshakeOption)),
            (
                "null",
                "[function]",
                "not declared",
                Unresolved(UnresolvedBy::TreeshakeOption),
            ),
            (
                "null",
                "[array of 2]",
                "not declared",
                Unresolved(UnresolvedBy::TreeshakeOption),
            ),
            // Nothing declared anywhere.
            ("null", "null", "not declared", Undeclared),
            ("undefined", "null", "not declared", Undeclared),
            // A declared package may have given the module a value that is not
            // collected and outranks the option, whatever it declares.
            ("null", "null", "false", Unresolved(UnresolvedBy::Package)),
            (
                "undefined",
                "null",
                "true",
                Unresolved(UnresolvedBy::Package),
            ),
            (
                "null",
                "null",
                "[array of 2]",
                Unresolved(UnresolvedBy::Package),
            ),
            (
                "null",
                "null",
                "\"src/*.js\"",
                Unresolved(UnresolvedBy::Package),
            ),
            // A quoted string is a glob, never the boolean of the same spelling.
            (
                "null",
                "null",
                "\"false\"",
                Unresolved(UnresolvedBy::Package),
            ),
            ("null", "false", "true", Unresolved(UnresolvedBy::Package)),
            ("null", "true", "false", Unresolved(UnresolvedBy::Package)),
            (
                "null",
                "[function]",
                "false",
                Unresolved(UnresolvedBy::Package),
            ),
            // Unrecognized hook values prove nothing either way.
            (
                "[array of 2]",
                "null",
                "false",
                Unresolved(UnresolvedBy::Hook),
            ),
            (
                "[function]",
                "false",
                "true",
                Unresolved(UnresolvedBy::Hook),
            ),
            (
                "[object]",
                "null",
                "not declared",
                Unresolved(UnresolvedBy::Hook),
            ),
        ];
        for (module, option, package, expected) in table {
            let values = DeclaredValues {
                module,
                option,
                package,
            };
            let case = format!("{module} / {option} / {package}");
            let declaration = SideEffectDeclaration::from_collected(&values);
            assert_eq!(declaration, expected, "{case}");
            let (outcome, detail) = declaration.condition(&values);
            let expected_outcome = match expected {
                Free(_) => ConditionOutcome::Met,
                Effectful(_) | Analyzed | Undeclared => ConditionOutcome::Unmet,
                Unresolved(_) => ConditionOutcome::Unknown,
            };
            assert_eq!(outcome, expected_outcome, "{case}");
            for value in [module, option, package] {
                assert!(detail.contains(value), "{detail}");
            }
        }
    }

    #[test]
    fn declaration_source_is_named_with_its_precedence() {
        let values = DeclaredValues {
            module: "no-treeshake",
            option: "null",
            package: "false",
        };
        let (_, detail) = SideEffectDeclaration::from_collected(&values).condition(&values);
        assert!(detail.contains("takes precedence over treeshake.moduleSideEffects and package"));
        let values = DeclaredValues {
            module: "null",
            option: "false",
            package: "not declared",
        };
        let (_, detail) = SideEffectDeclaration::from_collected(&values).condition(&values);
        assert!(
            detail.contains("treeshake.moduleSideEffects, which takes precedence over package")
        );
    }

    #[test]
    fn guidance_matches_the_unresolved_source() {
        let package = Unresolved(UnresolvedBy::Package).direction();
        assert!(package.contains("most likely covered"));
        assert!(package.contains("narrow or remove that entry"));
        assert!(!package.contains("add the declaration"));
        let option = Unresolved(UnresolvedBy::TreeshakeOption).direction();
        assert!(option.contains("build.rolldownOptions.treeshake.moduleSideEffects"));
        assert!(!option.contains("covered"));
        let hook = Unresolved(UnresolvedBy::Hook).direction();
        assert!(hook.contains("plugin"));
        assert!(!hook.contains("covered"));
    }

    #[test]
    fn analyzed_guidance_points_at_the_plugin_that_returns_true() {
        // The value is read from a JavaScript hook, so no package edit changes
        // it.
        let plugin = Analyzed.direction();
        assert!(plugin.contains("stop the plugin"), "{plugin}");
        assert!(!plugin.contains("narrowing"), "{plugin}");
        assert!(!plugin.contains("will not help"), "{plugin}");
    }
}
