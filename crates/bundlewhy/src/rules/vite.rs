//! Vite rules backed by Rolldown module info, rendered modules and engine logs.

use std::collections::BTreeSet;

use super::{Context, DiagnosticDraft, RuleId};
use crate::model::{Condition, ConditionOutcome, Evidence, EvidenceStatus};

const MODULE_INFO: &str = "rolldown module info (getModuleInfo)";
const RENDERED: &str = "rolldown rendered chunk modules";

pub fn run(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    side_effect_retained(context, drafts);
    commonjs_wrapped(context, drafts);
    ineffective_dynamic_import(context, drafts);
}

fn side_effect_retained(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let collection = context.collection;
    for module in &collection.modules {
        let has_exports = module
            .exports
            .as_ref()
            .is_some_and(|exports| !exports.is_empty());
        let no_rendered_exports = module.used_exports.as_ref().is_some_and(Vec::is_empty);
        let esm = module.input_format.as_deref() == Some("es");
        if !module.included
            || module.is_entry
            || !esm
            || !has_exports
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
        drafts.push(DiagnosticDraft {
            rule: RuleId::ViteSideEffectRetained,
            status: EvidenceStatus::Confirmed,
            title: format!("{display} is kept only for its side effects"),
            summary: format!(
                "Rolldown rendered {} bytes of this module although none of its exports is used. \
                 Code with side effects at the top level must run, so the module is not removed.",
                module.rendered_length
            ),
            conditions: vec![
                Condition::new(
                    "No export of the module is used",
                    ConditionOutcome::Met,
                    "renderedExports is empty",
                ),
                Condition::new(
                    "The module is declared side-effect free",
                    ConditionOutcome::Unmet,
                    format!(
                        "moduleSideEffects: {side_effects} (null means inferred from code); \
                         package sideEffects: {package_side_effects}"
                    ),
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
            evidence: vec![
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
            ],
            direction: "Move top-level side effects into an exported function that callers invoke \
                        explicitly, or declare the package side-effect free with \"sideEffects\": \
                        false (or a list of files that do have side effects) in package.json."
                .to_owned(),
            safety: "Only declare modules side-effect free when no importer depends on evaluating \
                     them for their effect (polyfills, registrations, CSS imports). Otherwise the \
                     effect is silently removed."
                .to_owned(),
            dependency: context.is_dependency(module),
        });
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
        let display = context.module_display(module);
        let located = context.module_location(module, 0, 0);
        let mut evidence = vec![Evidence::new(
            MODULE_INFO,
            EvidenceStatus::Confirmed,
            "Rolldown detected the input format as cjs.",
        )];
        let status = if let Some(helper) = &helper {
            evidence.push(Evidence::new(
                "rolldown runtime rendered exports",
                EvidenceStatus::Confirmed,
                format!("The runtime helper {helper} is rendered in the bundle."),
            ));
            EvidenceStatus::Confirmed
        } else {
            evidence.push(Evidence::new(
                "bundlewhy analysis",
                EvidenceStatus::Inferred,
                "The CommonJS runtime helper was not observed; wrapping is inferred from the \
                 input format.",
            ));
            EvidenceStatus::Inferred
        };
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
                    if helper.is_some() {
                        ConditionOutcome::Unmet
                    } else {
                        ConditionOutcome::Unknown
                    },
                    helper.as_ref().map_or_else(
                        || "Runtime helper not observed".to_owned(),
                        |name| format!("{name} rendered"),
                    ),
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
