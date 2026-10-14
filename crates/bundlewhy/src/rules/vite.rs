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
                .unwrap_or("not declared"),
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
        });
    }
}

/// The collected values that Rolldown consults, in precedence order, to decide
/// whether a module has side effects.
struct DeclaredValues<'a> {
    /// `moduleSideEffects` from `getModuleInfo()`: the value a resolve, load or
    /// transform hook set for the module (`null` when no hook set one).
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
}

/// Where the effective side-effect declaration comes from.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum DeclarationSource {
    /// A `moduleSideEffects` value that a plugin hook set for the module.
    Hook,
    /// The configured `build.rolldownOptions.treeshake.moduleSideEffects`.
    TreeshakeOption,
    /// The `sideEffects` field of the nearest `package.json`.
    Package,
}

impl DeclarationSource {
    /// The declaration as the subject of a sentence.
    fn subject(self) -> &'static str {
        match self {
            Self::Hook => "A plugin's moduleSideEffects value",
            Self::TreeshakeOption => "The configured treeshake.moduleSideEffects",
            Self::Package => "The package sideEffects field",
        }
    }

    /// The declaration with the precedence that makes it the effective one.
    fn label(self) -> &'static str {
        match self {
            Self::Hook => {
                "a plugin's moduleSideEffects value, which takes precedence over \
                 treeshake.moduleSideEffects and package sideEffects"
            }
            Self::TreeshakeOption => {
                "treeshake.moduleSideEffects, which takes precedence over package sideEffects"
            }
            Self::Package => "package sideEffects",
        }
    }
}

/// What the collected values say about a module that Rolldown rendered without
/// any used export, following Rolldown's precedence: a `moduleSideEffects`
/// value that a hook set, then the configured `treeshake.moduleSideEffects`,
/// then the package `sideEffects` field, then Rolldown's own code analysis.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum SideEffectDeclaration {
    /// The effective declaration is side-effect free (`false`).
    Free(DeclarationSource),
    /// The effective declaration keeps the module (`true` from the configured
    /// option or the package, or `"no-treeshake"` from a hook).
    Effectful(DeclarationSource),
    /// A hook returned `true`, which asks Rolldown for its default analysis of
    /// the code and skips the configured option and the package field.
    Analyzed,
    /// Nothing is declared; Rolldown infers the effects from the code.
    Undeclared,
    /// The collected value cannot be matched to this file, so the effective
    /// declaration is unknown.
    Unresolved(DeclarationSource),
}

impl SideEffectDeclaration {
    fn from_collected(values: &DeclaredValues<'_>) -> Self {
        use DeclarationSource::{Hook, Package, TreeshakeOption};
        match values.module {
            "false" => Self::Free(Hook),
            "no-treeshake" => Self::Effectful(Hook),
            "true" => Self::Analyzed,
            "null" | "undefined" => match values.option {
                "false" => Self::Free(TreeshakeOption),
                "true" => Self::Effectful(TreeshakeOption),
                // No verdict from the option: Rolldown falls back to the package.
                "null" => match values.package {
                    "false" => Self::Free(Package),
                    "true" => Self::Effectful(Package),
                    "not declared" => Self::Undeclared,
                    // List summaries and unrecognized values prove nothing either way.
                    _ => Self::Unresolved(Package),
                },
                // A function or an unrecognized option shape is not evaluated.
                _ => Self::Unresolved(TreeshakeOption),
            },
            // An unrecognized hook value cannot be matched to the other values.
            _ => Self::Unresolved(Hook),
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
                    "A plugin's moduleSideEffects: true asks Rolldown to analyze the code and \
                     skips treeshake.moduleSideEffects and package sideEffects; {values}"
                ),
            ),
            Self::Undeclared => (
                ConditionOutcome::Unmet,
                format!("No side-effect declaration (null means inferred from code); {values}"),
            ),
            Self::Unresolved(source) => {
                let reason = match source {
                    DeclarationSource::Hook => {
                        "the plugin's moduleSideEffects value is not recognized"
                    }
                    DeclarationSource::TreeshakeOption => {
                        "treeshake.moduleSideEffects is a value bundlewhy does not evaluate"
                    }
                    DeclarationSource::Package => {
                        "a package sideEffects value cannot be matched to this file"
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
            Self::Effectful(DeclarationSource::Hook) => {
                "Move top-level side effects into an exported function that callers invoke \
                 explicitly, or change the plugin that returns moduleSideEffects \"no-treeshake\" \
                 for this file so that it covers only the files that do have side effects."
            }
            Self::Effectful(DeclarationSource::TreeshakeOption) => {
                "Move top-level side effects into an exported function that callers invoke \
                 explicitly, or narrow build.rolldownOptions.treeshake.moduleSideEffects so that \
                 it marks only the files that do have side effects."
            }
            Self::Effectful(DeclarationSource::Package) => {
                "Move top-level side effects into an exported function that callers invoke \
                 explicitly, or narrow the \"sideEffects\" declaration in package.json (a blanket \
                 true) to the files that do have side effects."
            }
            Self::Analyzed => {
                "Move top-level side effects into an exported function that callers invoke \
                 explicitly, or stop the plugin from returning moduleSideEffects: true for this \
                 file. That value makes Rolldown analyze the code and skip both \
                 treeshake.moduleSideEffects and the package \"sideEffects\" field, so declaring \
                 the package side-effect free will not help."
            }
            Self::Undeclared => {
                "Move top-level side effects into an exported function that callers invoke \
                 explicitly, or declare the package side-effect free with \"sideEffects\": false \
                 (or a list of files that do have side effects) in package.json."
            }
            Self::Unresolved(DeclarationSource::Hook) => {
                "Check the plugin that sets moduleSideEffects for this file; its value was not \
                 recognized. If the module has no needed side effect, make the plugin declare it \
                 side-effect free; otherwise move top-level side effects into an exported function \
                 that callers invoke explicitly."
            }
            Self::Unresolved(DeclarationSource::TreeshakeOption) => {
                "Check what build.rolldownOptions.treeshake.moduleSideEffects decides for this \
                 file; it takes precedence over the package \"sideEffects\" field. If it marks the \
                 file as having side effects and the module has no needed side effect, narrow it. \
                 Otherwise move top-level side effects into an exported function that callers \
                 invoke explicitly."
            }
            Self::Unresolved(DeclarationSource::Package) => {
                "Rolldown treats a file that the package \"sideEffects\" list does not cover as \
                 side-effect free, so a retained file is most likely covered by an entry of the \
                 list. Find the entry that matches it and narrow or remove that entry if the \
                 module has no needed side effect; adding the file to the list would declare the \
                 opposite. Otherwise move top-level side effects into an exported function that \
                 callers invoke explicitly."
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
        let status = EvidenceStatus::Confirmed;
        drafts.push(DiagnosticDraft {
            rule: RuleId::ViteCommonjsWrapped,
            status,
            title: format!("{display} is wrapped as CommonJS"),
            summary: "Rolldown wraps CommonJS modules in a runtime closure instead of hoisting \
                      them into the chunk scope. The wrapper, its interop helpers and exports \
                      that cannot be analyzed statically stay in the bundle."
                .to_owned(),
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
            direction: "Import an ECMAScript module build of this code (check the package's \
                        \"exports\" or \"module\" field) or convert the module to ESM syntax."
                .to_owned(),
            safety: "Confirm that the ESM build exposes the same API and evaluation order; \
                     CommonJS modules evaluate lazily on first require."
                .to_owned(),
            dependency: context.is_dependency(module),
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
        });
    }
}

fn tree_shaking_disabled_finding(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let config_file = context.collection.config_file.clone();
    let located = match &config_file {
        Some(file) => context.original_location(std::path::Path::new(file), 0, 0),
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
    });
}

#[cfg(test)]
mod tests {
    use super::{
        DeclarationSource::{Hook, Package, TreeshakeOption},
        SideEffectDeclaration::{Analyzed, Effectful, Free, Undeclared, Unresolved},
        *,
    };

    #[test]
    fn side_effect_declaration_precedence_table() {
        // (hook moduleSideEffects, treeshake.moduleSideEffects, package
        // sideEffects, effective declaration), following Rolldown's order:
        // hook value, configured option, package field, code analysis.
        let table = [
            // A hook value wins over the option and every package value.
            ("false", "true", "true", Free(Hook)),
            ("false", "false", "not declared", Free(Hook)),
            ("false", "null", "[array of 2]", Free(Hook)),
            ("no-treeshake", "false", "false", Effectful(Hook)),
            ("no-treeshake", "null", "not declared", Effectful(Hook)),
            // `true` asks for the default analysis and skips option and package.
            ("true", "false", "false", Analyzed),
            ("true", "null", "false", Analyzed),
            ("true", "true", "true", Analyzed),
            ("true", "[function]", "[array of 2]", Analyzed),
            // Without a hook value the configured option decides next.
            ("null", "false", "true", Free(TreeshakeOption)),
            ("null", "false", "not declared", Free(TreeshakeOption)),
            ("undefined", "false", "[array of 2]", Free(TreeshakeOption)),
            ("null", "true", "false", Effectful(TreeshakeOption)),
            ("null", "true", "not declared", Effectful(TreeshakeOption)),
            ("null", "[function]", "false", Unresolved(TreeshakeOption)),
            ("null", "[array of 2]", "true", Unresolved(TreeshakeOption)),
            // No verdict from the option falls back to the package field.
            ("null", "null", "false", Free(Package)),
            ("undefined", "null", "false", Free(Package)),
            ("null", "null", "true", Effectful(Package)),
            ("null", "null", "not declared", Undeclared),
            ("undefined", "null", "not declared", Undeclared),
            ("null", "null", "[array of 2]", Unresolved(Package)),
            ("null", "null", "src/*.js", Unresolved(Package)),
            // Unrecognized hook values prove nothing either way.
            ("[array of 2]", "null", "false", Unresolved(Hook)),
            ("[function]", "false", "true", Unresolved(Hook)),
            ("[object]", "null", "not declared", Unresolved(Hook)),
        ];
        for (module, option, package, expected) in table {
            let values = DeclaredValues {
                module,
                option,
                package,
            };
            let declaration = SideEffectDeclaration::from_collected(&values);
            assert_eq!(declaration, expected, "{module} / {option} / {package}");
            let (outcome, detail) = declaration.condition(&values);
            let expected_outcome = match expected {
                Free(_) => ConditionOutcome::Met,
                Effectful(_) | Analyzed | Undeclared => ConditionOutcome::Unmet,
                Unresolved(_) => ConditionOutcome::Unknown,
            };
            assert_eq!(outcome, expected_outcome, "{module} / {option} / {package}");
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
            package: "true",
        };
        let (_, detail) = SideEffectDeclaration::from_collected(&values).condition(&values);
        assert!(
            detail.contains("treeshake.moduleSideEffects, which takes precedence over package")
        );
    }

    #[test]
    fn guidance_matches_the_unresolved_source() {
        let package = Unresolved(Package).direction();
        assert!(package.contains("most likely covered"));
        assert!(package.contains("narrow or remove that entry"));
        assert!(!package.contains("add the declaration"));
        let option = Unresolved(TreeshakeOption).direction();
        assert!(option.contains("build.rolldownOptions.treeshake.moduleSideEffects"));
        assert!(!option.contains("covered"));
        let hook = Unresolved(Hook).direction();
        assert!(hook.contains("plugin"));
        assert!(!hook.contains("covered"));
    }
}
