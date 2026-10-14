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
        let side_effects = module.module_side_effects.as_deref().unwrap_or("null");
        let package_side_effects = module
            .package_side_effects
            .as_deref()
            .unwrap_or("not declared");
        let declaration = SideEffectDeclaration::from_collected(side_effects, package_side_effects);
        // The title feeds the diagnostic ID, so it stays the same however the
        // declaration changes and one retained module keeps one ID.
        let title = format!("{display} is retained although none of its exports is used");
        let (status, summary) = match declaration {
            // An explicit effect declaration is itself the engine-visible
            // reason the module is kept.
            SideEffectDeclaration::Effectful => (
                EvidenceStatus::Confirmed,
                format!(
                    "Rolldown rendered {} bytes of this module although none of its exports is \
                     used. The module or its package declares side effects, so Rolldown keeps it.",
                    module.rendered_length
                ),
            ),
            // Rolldown reports no retention reason for rendered modules, so
            // the cause stays an inference from the observed facts.
            _ => (
                EvidenceStatus::Inferred,
                format!(
                    "Rolldown rendered {} bytes of this module although none of its exports is \
                     used, and did not report why. The module may contain top-level code with \
                     side effects that Rolldown keeps.",
                    module.rendered_length
                ),
            ),
        };
        let (outcome, declared_detail) = declaration.condition(side_effects, package_side_effects);
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
        if declaration != SideEffectDeclaration::Effectful {
            evidence.push(Evidence::new(
                INFERENCE,
                EvidenceStatus::Inferred,
                "Rolldown reports no retention reason; top-level side effects are inferred from \
                 the retained bytes",
            ));
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

/// What the collected `moduleSideEffects` and package `sideEffects` values
/// say about a module that Rolldown rendered without any used export.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum SideEffectDeclaration {
    /// The effective declaration is side-effect free (`false`): the module
    /// value, or the package value when the module value is unspecified.
    Free,
    /// The effective declaration is effectful (`true` or `no-treeshake`),
    /// chosen with the same module-over-package precedence.
    Effectful,
    /// An array summary or unrecognized value cannot be matched to this file.
    Unresolved,
    /// Nothing is declared; Rolldown infers the effects from the code.
    Undeclared,
}

impl SideEffectDeclaration {
    /// Rolldown gives a `moduleSideEffects` value set by a resolve, load or
    /// transform hook precedence over the package `sideEffects` field, so the
    /// collected module value is the effective decision. The package value
    /// applies only when the module value is unspecified (`null`).
    fn from_collected(module: &str, package: &str) -> Self {
        match module {
            "false" => Self::Free,
            "true" | "no-treeshake" => Self::Effectful,
            "null" | "undefined" => match package {
                "false" => Self::Free,
                "true" => Self::Effectful,
                "not declared" => Self::Undeclared,
                // Array summaries and unrecognized values prove nothing either way.
                _ => Self::Unresolved,
            },
            // An unrecognized module value cannot be matched to the package value.
            _ => Self::Unresolved,
        }
    }

    fn condition(self, module: &str, package: &str) -> (ConditionOutcome, String) {
        let values = format!("moduleSideEffects: {module}; package sideEffects: {package}");
        match self {
            Self::Free => {
                // The module value wins when it is set, so only it is named
                // then.
                let source = if module == "false" {
                    "moduleSideEffects"
                } else {
                    "package sideEffects"
                };
                (
                    ConditionOutcome::Met,
                    format!("Declared side-effect free by {source}; {values}"),
                )
            }
            Self::Effectful => {
                let source = if matches!(module, "true" | "no-treeshake") {
                    if package == "false" {
                        "moduleSideEffects, which takes precedence over package sideEffects: false"
                    } else {
                        "moduleSideEffects"
                    }
                } else {
                    "package sideEffects"
                };
                (
                    ConditionOutcome::Unmet,
                    format!("Side effects are declared by {source}; {values}"),
                )
            }
            Self::Unresolved => (
                ConditionOutcome::Unknown,
                format!(
                    "The collected summary cannot show whether this file is declared side-effect \
                     free; {values}"
                ),
            ),
            Self::Undeclared => (
                ConditionOutcome::Unmet,
                format!("No side-effect declaration (null means inferred from code); {values}"),
            ),
        }
    }

    fn direction(self) -> &'static str {
        match self {
            Self::Free => {
                "The module is already declared side-effect free, so adding a declaration will not \
                 help. Check which importers still use it, such as side-effect imports, namespace \
                 or re-export use, or an entry, and remove that use if it is unneeded. If no \
                 importer uses it, report the retained module to the bundler."
            }
            Self::Effectful => {
                "Move top-level side effects into an exported function that callers invoke \
                 explicitly, or narrow the side-effect declaration (a plugin's moduleSideEffects \
                 value or the \"sideEffects\" list in package.json) to the files that do have side \
                 effects."
            }
            Self::Unresolved => {
                "Check whether this file is covered by the \"sideEffects\" list in package.json. \
                 If it is not listed, add the declaration only when the module has no needed side \
                 effect; otherwise move top-level side effects into an exported function that \
                 callers invoke explicitly."
            }
            Self::Undeclared => {
                "Move top-level side effects into an exported function that callers invoke \
                 explicitly, or declare the package side-effect free with \"sideEffects\": false \
                 (or a list of files that do have side effects) in package.json."
            }
        }
    }

    fn safety(self) -> &'static str {
        match self {
            Self::Free => {
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
    use super::*;

    #[test]
    fn side_effect_declaration_precedence_table() {
        use SideEffectDeclaration::{Effectful, Free, Undeclared, Unresolved};
        // (moduleSideEffects, package sideEffects, effective declaration)
        let table = [
            // A set module value wins over every package value.
            ("false", "not declared", Free),
            ("false", "false", Free),
            ("false", "true", Free),
            ("false", "[array of 2]", Free),
            ("true", "not declared", Effectful),
            ("true", "false", Effectful),
            ("true", "true", Effectful),
            ("true", "[array of 2]", Effectful),
            ("no-treeshake", "not declared", Effectful),
            ("no-treeshake", "false", Effectful),
            ("no-treeshake", "true", Effectful),
            ("no-treeshake", "[array of 2]", Effectful),
            // An unspecified module value falls back to the package value.
            ("null", "false", Free),
            ("null", "true", Effectful),
            ("null", "not declared", Undeclared),
            ("null", "[array of 2]", Unresolved),
            ("undefined", "false", Free),
            ("undefined", "not declared", Undeclared),
            // Unrecognized module summaries prove nothing either way.
            ("[array of 2]", "false", Unresolved),
            ("[function]", "true", Unresolved),
            ("[object]", "not declared", Unresolved),
        ];
        for (module, package, expected) in table {
            let declaration = SideEffectDeclaration::from_collected(module, package);
            assert_eq!(declaration, expected, "{module} / {package}");
            let (outcome, detail) = declaration.condition(module, package);
            let expected_outcome = match expected {
                Free => ConditionOutcome::Met,
                Effectful | Undeclared => ConditionOutcome::Unmet,
                Unresolved => ConditionOutcome::Unknown,
            };
            assert_eq!(outcome, expected_outcome, "{module} / {package}");
            assert!(
                detail.contains(module) && detail.contains(package),
                "{detail}"
            );
        }
    }

    #[test]
    fn module_override_is_named_in_the_condition_detail() {
        let declaration = SideEffectDeclaration::from_collected("true", "false");
        let (_, detail) = declaration.condition("true", "false");
        assert!(detail.contains("takes precedence over package sideEffects"));
    }
}
