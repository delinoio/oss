//! Rspack rules backed by stats reasons, used exports and optimization
//! bailouts.

use regex::Regex;

use super::{Context, DiagnosticDraft, RuleId};
use crate::model::{Condition, ConditionOutcome, Evidence, EvidenceStatus};

const SIDE_EFFECT_PREFIX: &str = "Statement with side_effects in source code at ";
const CONCAT_PREFIX: &str = "ModuleConcatenation bailout: ";
const STATS_PROVENANCE: &str = "rspack stats (optimizationBailout)";

/// CSS and asset modules are out of scope: bundlewhy diagnoses JavaScript only.
fn is_javascript(module: &crate::collect::CollectedModule) -> bool {
    module
        .module_type
        .as_deref()
        .is_none_or(|kind| kind.starts_with("javascript/"))
}

fn option_disabled(collection: &crate::collect::Collection, option: &str) -> bool {
    collection
        .config_summary
        .get(option)
        .is_some_and(|value| value == "false")
}

/// Rspack options whose `false` value turns off an analysis the side-effect
/// detector depends on: used exports, side-effects analysis and provided
/// exports (side-effects analysis builds on provided exports).
pub const TREE_SHAKING_OPTIONS: [&str; 3] = [
    "optimization.usedExports",
    "optimization.sideEffects",
    "optimization.providedExports",
];

/// The disabled tree-shaking inputs, in a stable order.
pub fn disabled_tree_shaking_options(collection: &crate::collect::Collection) -> Vec<&'static str> {
    TREE_SHAKING_OPTIONS
        .into_iter()
        .filter(|option| option_disabled(collection, option))
        .collect()
}

/// True when any analysis the side-effect detector needs is disabled.
pub fn used_exports_disabled(collection: &crate::collect::Collection) -> bool {
    !disabled_tree_shaking_options(collection).is_empty()
}

/// `optimization.concatenateModules: false` turns scope hoisting off.
pub fn concatenation_disabled(collection: &crate::collect::Collection) -> bool {
    option_disabled(collection, "optimization.concatenateModules")
}

pub fn run(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let disabled = disabled_tree_shaking_options(context.collection);
    if disabled.is_empty() {
        side_effect_retained(context, drafts);
    }
    for option in disabled {
        let finding = match option {
            "optimization.usedExports" => super::common::DisabledOptimization {
                rule: RuleId::RspackUsedExportsDisabled,
                title: "Used-exports analysis is disabled",
                option: "optimization.usedExports",
                summary: "optimization.usedExports is false, so Rspack does not determine which \
                          exports are used and cannot drop unused ones. Tree-shaking findings do \
                          not apply to this build.",
                direction: "Remove `usedExports: false` (the production default is true).",
                safety: "Code that relies on unused exports being kept (for example through \
                         dynamic property access on a namespace) must import them explicitly.",
            },
            "optimization.sideEffects" => super::common::DisabledOptimization {
                rule: RuleId::RspackSideEffectsDisabled,
                title: "Side-effects analysis is disabled",
                option: "optimization.sideEffects",
                summary: "optimization.sideEffects is false, so Rspack never skips unused modules \
                          based on side-effect analysis or package \"sideEffects\" flags. \
                          Tree-shaking findings do not apply to this build.",
                direction: "Remove `sideEffects: false` from optimization (the production default \
                            is true).",
                safety: "Modules imported only for their effects must stay imported or be \
                         declared in package \"sideEffects\" lists.",
            },
            _ => super::common::DisabledOptimization {
                rule: RuleId::RspackProvidedExportsDisabled,
                title: "Provided-exports analysis is disabled",
                option: "optimization.providedExports",
                summary: "optimization.providedExports is false, so Rspack does not know which \
                          exports modules provide; used-exports and side-effects analysis depend \
                          on it. Tree-shaking findings do not apply to this build.",
                direction: "Remove `providedExports: false` (the default is true).",
                safety: "Re-enabling export analysis can change which exports are kept; run the \
                         application's tests.",
            },
        };
        super::common::configuration_disabled(context, drafts, &finding);
    }

    if concatenation_disabled(context.collection) {
        super::common::configuration_disabled(
            context,
            drafts,
            &super::common::DisabledOptimization {
                rule: RuleId::RspackConcatenationDisabled,
                title: "Module concatenation is disabled",
                option: "optimization.concatenateModules",
                summary: "optimization.concatenateModules is false, so Rspack does not \
                          scope-hoist modules. Concatenation bailouts do not apply to this build.",
                direction: "Remove `concatenateModules: false` (the production default is true).",
                safety: "Concatenation changes module evaluation wrappers only; verify code that \
                         inspects module objects at runtime.",
            },
        );
    } else {
        concatenation_bailouts(context, drafts);
    }
    multiple_chunks(context, drafts);
}

/// Parses `./src/a.js:2:1-76` into zero-based (line, column).
fn parse_statement_location(text: &str) -> Option<(u32, u32)> {
    let pattern = Regex::new(r":(\d+):(\d+)(?:-\d+(?::\d+)?)?\s*$").ok()?;
    let captures = pattern.captures(text)?;
    let line: u32 = captures[1].parse().ok()?;
    let column: u32 = captures[2].parse().ok()?;
    Some((line.saturating_sub(1), column.saturating_sub(1)))
}

fn side_effect_retained(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let side_effects_option = context
        .collection
        .config_summary
        .get("optimization.sideEffects")
        .cloned()
        .unwrap_or_else(|| "undefined".to_owned());
    let modules: Vec<_> = context.collection.modules.iter().collect();
    for module in modules.into_iter().filter(|module| is_javascript(module)) {
        if context.is_cancelled() {
            return;
        }
        let no_used_exports = module.used_exports.as_ref().is_some_and(Vec::is_empty);
        // Export-less modules (for example `import "./polyfill"`) qualify too:
        // Rspack's side-effect bailout is the evidence, not the export list.
        if !module.included || module.is_entry || !no_used_exports {
            continue;
        }
        let statements: Vec<&str> = module
            .bailouts
            .iter()
            .filter_map(|entry| entry.strip_prefix(SIDE_EFFECT_PREFIX))
            .collect();
        // Inclusion with no used exports alone does not show that a side
        // effect blocked removal; require Rspack's own side-effect bailout.
        if statements.is_empty() {
            continue;
        }
        let (line, column) = statements
            .first()
            .and_then(|text| parse_statement_location(text))
            .unwrap_or((0, 0));
        let located = context.module_location(module, line, column);
        let display = context.module_display(module);
        let mut evidence = vec![Evidence::new(
            "rspack stats (usedExports, providedExports)",
            EvidenceStatus::Confirmed,
            format!(
                "Provided exports: {}. Used exports: none.",
                module.exports.as_deref().unwrap_or_default().join(", ")
            ),
        )];
        for statement in &statements {
            evidence.push(Evidence::new(
                STATS_PROVENANCE,
                EvidenceStatus::Confirmed,
                format!("Statement with side effects at {statement}"),
            ));
        }
        let side_effect_detail = format!(
            "Rspack found {} statement(s) with side effects; package sideEffects: {}",
            statements.len(),
            module
                .package_side_effects
                .as_deref()
                .unwrap_or("not declared")
        );
        drafts.push(DiagnosticDraft {
            rule: RuleId::RspackSideEffectRetained,
            status: EvidenceStatus::Confirmed,
            title: format!("{display} is kept only for its side effects"),
            summary: "No export of this module is used, but Rspack keeps the module because \
                      evaluating it has side effects. Unused-module removal does not apply."
                .to_owned(),
            conditions: vec![
                Condition::new(
                    "No export of the module is used",
                    ConditionOutcome::Met,
                    "usedExports is empty",
                ),
                Condition::new(
                    "The module has no side effects or is declared side-effect free",
                    ConditionOutcome::Unmet,
                    side_effect_detail,
                ),
                Condition::new(
                    "optimization.sideEffects is enabled",
                    if side_effects_option == "false" {
                        ConditionOutcome::Unmet
                    } else {
                        ConditionOutcome::Met
                    },
                    format!(
                        "optimization.sideEffects: {side_effects_option} (production default: \
                         true)"
                    ),
                ),
            ],
            located,
            module: display,
            dependency_paths: context.dependency_paths(&module.id),
            evidence,
            direction: "Move top-level side effects into an exported function that callers invoke \
                        explicitly, or declare the package side-effect free with \"sideEffects\": \
                        false (or a list of files that do have side effects) in package.json."
                .to_owned(),
            safety: "Only declare modules side-effect free when no importer depends on evaluating \
                     them for their effect (polyfills, registrations, CSS imports). Otherwise the \
                     effect is silently removed."
                .to_owned(),
            dependency: context.is_dependency(module),
            export_usage: Some(super::export_usage::build(context, &module.id)),
        });
    }
}

fn concatenation_direction(reason: &str) -> (&'static str, &'static str) {
    if reason.contains("not an ECMAScript module") {
        (
            "Import an ECMAScript module build of this code (check the package's \"exports\" or \
             \"module\" field) or convert the module to ESM syntax.",
            "Confirm that the ESM build exposes the same API and evaluation behavior as the \
             CommonJS build.",
        )
    } else if reason.contains("eval()") {
        (
            "Replace direct eval with indirect eval ((0, eval)(code)) or new Function so the \
             module scope is no longer observable.",
            "Indirect eval runs in global scope and cannot read local variables; update code that \
             depends on local scope access.",
        )
    } else if reason.contains("referenced from different chunks") {
        (
            "Keep the modules that must be concatenated in the same chunk, for example by \
             adjusting optimization.splitChunks or importing the shared module from a common \
             parent chunk.",
            "Changing chunk boundaries changes loading order and request counts; measure load \
             performance after the change.",
        )
    } else {
        (
            "Review the bailout reason reported by Rspack and remove the construct that prevents \
             scope hoisting.",
            "Keep runtime behavior unchanged; concatenation is a size and performance \
             optimization only.",
        )
    }
}

fn concatenation_bailouts(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let concatenate_option = context
        .collection
        .config_summary
        .get("optimization.concatenateModules")
        .cloned()
        .unwrap_or_else(|| "undefined".to_owned());
    let modules: Vec<_> = context.collection.modules.iter().collect();
    for module in modules.into_iter().filter(|module| is_javascript(module)) {
        if context.is_cancelled() {
            return;
        }
        if !module.included || module.concatenated_into.is_some() || module.chunks.is_empty() {
            continue;
        }
        let reasons: Vec<&str> = module
            .bailouts
            .iter()
            .filter_map(|entry| entry.strip_prefix(CONCAT_PREFIX))
            // Entry modules are concatenation roots; a module outside any chunk
            // was removed or inlined. Neither blocks an optimization.
            .filter(|reason| {
                !reason.contains("Module is an entry point") && !reason.contains("not in any chunk")
            })
            .collect();
        if reasons.is_empty() {
            continue;
        }
        let display = context.module_display(module);
        let located = context.module_location(module, 0, 0);
        let (direction, safety) = concatenation_direction(reasons[0]);
        let mut conditions = vec![Condition::new(
            "optimization.concatenateModules is enabled",
            if concatenate_option == "false" {
                ConditionOutcome::Unmet
            } else {
                ConditionOutcome::Met
            },
            format!(
                "optimization.concatenateModules: {concatenate_option} (production default: true)"
            ),
        )];
        conditions.extend(reasons.iter().map(|reason| {
            let description = if reason.contains("not an ECMAScript module") {
                "The module is an ECMAScript module"
            } else if reason.contains("eval()") {
                "The module does not use eval()"
            } else if reason.contains("referenced from different chunks") {
                "All importers of an inner module are in the same chunk"
            } else {
                "Rspack concatenation requirement"
            };
            Condition::new(description, ConditionOutcome::Unmet, (*reason).to_owned())
        }));
        let evidence = reasons
            .iter()
            .map(|reason| {
                Evidence::new(
                    STATS_PROVENANCE,
                    EvidenceStatus::Confirmed,
                    format!("{CONCAT_PREFIX}{reason}"),
                )
            })
            .collect();
        drafts.push(DiagnosticDraft {
            rule: RuleId::RspackModuleConcatenationBailout,
            status: EvidenceStatus::Confirmed,
            title: format!("{display} is not concatenated into its importer"),
            summary: format!("Rspack could not scope-hoist this module: {}", reasons[0]),
            conditions,
            located,
            module: display,
            dependency_paths: context.dependency_paths(&module.id),
            evidence,
            direction: direction.to_owned(),
            safety: safety.to_owned(),
            dependency: context.is_dependency(module),
            export_usage: Some(super::export_usage::build(context, &module.id)),
        });
    }
}

fn multiple_chunks(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let split_option = context
        .collection
        .config_summary
        .get("optimization.splitChunks")
        .cloned()
        .unwrap_or_else(|| "undefined".to_owned());
    let modules: Vec<_> = context.collection.modules.iter().collect();
    for module in modules.into_iter().filter(|module| is_javascript(module)) {
        if context.is_cancelled() {
            return;
        }
        if !module.included || module.chunks.len() < 2 {
            continue;
        }
        let display = context.module_display(module);
        let located = context.module_location(module, 0, 0);
        drafts.push(DiagnosticDraft {
            rule: RuleId::RspackModuleInMultipleChunks,
            status: EvidenceStatus::Confirmed,
            title: format!("{display} is duplicated in {} chunks", module.chunks.len()),
            summary: "Rspack placed a copy of this module in several chunks instead of one shared \
                      chunk. Each copy is downloaded and evaluated separately."
                .to_owned(),
            conditions: vec![
                Condition::new(
                    "The module is emitted in a single chunk",
                    ConditionOutcome::Unmet,
                    format!("Emitted in: {}", module.chunks.join(", ")),
                ),
                Condition::new(
                    "splitChunks extracts modules shared by several chunks",
                    ConditionOutcome::Unknown,
                    format!(
                        "optimization.splitChunks: {split_option}. Default cache groups only \
                         extract shared modules above minSize."
                    ),
                ),
            ],
            located,
            module: display,
            dependency_paths: context.dependency_paths(&module.id),
            evidence: vec![Evidence::new(
                "rspack stats (module chunks)",
                EvidenceStatus::Confirmed,
                format!("Module chunks: {}", module.chunks.join(", ")),
            )],
            direction: "Configure optimization.splitChunks (for example a cache group or a lower \
                        minSize for this module) so the shared module moves into one common \
                        chunk, or import it from a common parent chunk."
                .to_owned(),
            safety: "Extracting a shared chunk adds a request for every consumer; confirm the \
                     trade-off with real load measurements."
                .to_owned(),
            dependency: context.is_dependency(module),
            export_usage: Some(super::export_usage::build(context, &module.id)),
        });
    }
}

#[cfg(test)]
mod tests {
    use std::{
        path::PathBuf,
        sync::atomic::{AtomicUsize, Ordering},
    };

    use super::{super::Context, *};
    use crate::{collect::Collection, sources::SourceStore};

    fn fixture_collection() -> Collection {
        let path = concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/tests/fixtures/rspack/collect.json"
        );
        serde_json::from_str(&std::fs::read_to_string(path).unwrap()).unwrap()
    }

    /// Rules drafted when the cancellation signal turns true at its
    /// `cancel_at`-th poll (zero-based); `None` never cancels.
    fn drafted_rules(cancel_at: Option<usize>) -> Vec<RuleId> {
        let collection = fixture_collection();
        let polls = AtomicUsize::new(0);
        let cancelled = || cancel_at.is_some_and(|at| polls.fetch_add(1, Ordering::SeqCst) >= at);
        let mut context = Context::new(
            &collection,
            PathBuf::from("/__bundlewhy_project__"),
            "project".to_owned(),
            Vec::new(),
            SourceStore::new(false),
        );
        context.cancelled = Some(&cancelled);
        let mut drafts = Vec::new();
        run(&mut context, &mut drafts);
        drafts.into_iter().map(|draft| draft.rule).collect()
    }

    /// Cancellation of the module loops. Mirrors the cancellation table in
    /// the contract: a detector loop checks the signal before each module
    /// and stops the whole rule set once it is set.
    #[test]
    fn module_loops_stop_when_the_run_is_cancelled() {
        let all = drafted_rules(None);
        for rule in [
            RuleId::RspackSideEffectRetained,
            RuleId::RspackModuleConcatenationBailout,
            RuleId::RspackModuleInMultipleChunks,
        ] {
            assert!(all.contains(&rule), "control run lacks {rule:?}");
        }
        // Cancelled before the first module: no detector drafts anything.
        assert!(drafted_rules(Some(0)).is_empty());
        // Cancelled during the first detector: the later detectors do not
        // start their work.
        let early = drafted_rules(Some(1));
        assert!(!early.contains(&RuleId::RspackModuleConcatenationBailout));
        assert!(!early.contains(&RuleId::RspackModuleInMultipleChunks));
    }

    #[test]
    fn parses_statement_locations() {
        assert_eq!(
            parse_statement_location("./src/effects/register.js:2:1-76"),
            Some((1, 0))
        );
        assert_eq!(
            parse_statement_location("./src/main.js:18:1-20:2"),
            Some((17, 0))
        );
        assert_eq!(parse_statement_location("unknown"), None);
    }
}
