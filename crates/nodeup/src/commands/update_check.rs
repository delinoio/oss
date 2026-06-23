use semver::Version;
use serde::Serialize;
use tracing::info;

use crate::{
    cli::{OutputColorMode, OutputFormat},
    commands::print_output,
    errors::{ErrorDiagnostics, NodeupError, Result},
    installer::InstallState,
    release_index::{normalize_version, ReleaseIndexResolutionDiagnostic},
    resolver::ResolvedRuntimeTarget,
    selectors::{is_case_variant_of_reserved_channel_selector, RuntimeSelector},
    types::RuntimeSelectorSource,
    NodeupApp,
};

#[derive(Debug, Serialize)]
struct CheckEntry {
    runtime: String,
    latest_available: Option<String>,
    has_update: bool,
}

#[derive(Debug, Serialize)]
struct UpdateEntry {
    selector: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    selector_source: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    implicit_target: Option<bool>,
    selector_kind: String,
    canonical_selector: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    selector_alias_of: Option<String>,
    previous_runtime: Option<String>,
    updated_runtime: Option<String>,
    status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    release_index: Option<ReleaseIndexResolutionDiagnostic>,
}

#[derive(Debug, Clone, Copy)]
struct UpdateSelectorContext {
    source: &'static str,
    tracked_selectors: usize,
    installed_runtimes: usize,
    allow_legacy_stored_linked_names: bool,
    implicit_targets: bool,
}

pub fn check(output: OutputFormat, color: Option<OutputColorMode>, app: &NodeupApp) -> Result<i32> {
    let installed = app.store.list_installed_versions()?;
    let mut results = Vec::new();

    for runtime in installed {
        let latest = latest_newer_version(app, &runtime)?;
        results.push(CheckEntry {
            runtime,
            has_update: latest.is_some(),
            latest_available: latest,
        });
    }

    let human = if results.is_empty() {
        "No installed runtimes found".to_string()
    } else {
        format!("Checked {} installed runtime(s)", results.len())
    };

    print_output(output, color, &human, &results)?;
    Ok(0)
}

pub fn update(
    runtimes: Vec<String>,
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    let (selectors, selector_context) = if runtimes.is_empty() {
        selectors_for_update(app)?
    } else {
        (
            runtimes,
            UpdateSelectorContext {
                source: "explicit-args",
                tracked_selectors: 0,
                installed_runtimes: 0,
                allow_legacy_stored_linked_names: false,
                implicit_targets: false,
            },
        )
    };

    if selectors.is_empty() {
        let mut diagnostics = ErrorDiagnostics::new();
        diagnostics.insert(
            "selector_source".to_string(),
            serde_json::json!(selector_context.source),
        );
        diagnostics.insert(
            "tracked_selectors".to_string(),
            serde_json::json!(selector_context.tracked_selectors),
        );
        diagnostics.insert(
            "installed_runtimes".to_string(),
            serde_json::json!(selector_context.installed_runtimes),
        );
        diagnostics.insert("resolved_selectors".to_string(), serde_json::json!(0));
        diagnostics.insert("selector_preview".to_string(), serde_json::json!(selectors));
        return Err(NodeupError::not_found_with_diagnostics(
            format!(
                "No runtimes are eligible for update (selector_source={}, tracked_selectors={}, \
                 installed_runtimes={}, resolved_selectors={})",
                selector_context.source,
                selector_context.tracked_selectors,
                selector_context.installed_runtimes,
                selectors.len()
            ),
            "Install a runtime with `nodeup toolchain install <runtime>` or configure tracked \
             selectors first.",
            diagnostics,
        ));
    }

    let requested_selectors =
        preflight_update_selectors(selectors, selector_context.allow_legacy_stored_linked_names)?;
    let mut updates = Vec::new();
    let selector_source = selector_context
        .implicit_targets
        .then(|| selector_context.source.to_string());
    let implicit_target = selector_context.implicit_targets.then_some(true);
    for (selector, parsed) in requested_selectors {
        let selector_kind = parsed.kind().as_str().to_string();
        let canonical_selector = parsed.canonical_id();
        let selector_alias_of = parsed.alias_of();
        match parsed {
            RuntimeSelector::LinkedName(_) => {
                updates.push(UpdateEntry {
                    selector,
                    selector_source: selector_source.clone(),
                    implicit_target,
                    selector_kind,
                    canonical_selector,
                    selector_alias_of,
                    previous_runtime: None,
                    updated_runtime: None,
                    status: "skipped-linked-runtime".to_string(),
                    release_index: None,
                });
            }
            RuntimeSelector::Channel(_) => {
                let resolved = app
                    .resolver
                    .resolve_selector_with_source(&selector, RuntimeSelectorSource::Explicit)?;
                let release_index = app.resolver.release_index_diagnostic();
                let version = match resolved.target {
                    ResolvedRuntimeTarget::Version { version } => version,
                    ResolvedRuntimeTarget::LinkedPath { .. } => unreachable!(),
                };
                let report = app.installer.ensure_installed(&version, &app.releases)?;
                updates.push(UpdateEntry {
                    selector,
                    selector_source: selector_source.clone(),
                    implicit_target,
                    selector_kind,
                    canonical_selector,
                    selector_alias_of,
                    previous_runtime: None,
                    updated_runtime: Some(report.version),
                    status: if report.state == InstallState::AlreadyInstalled {
                        "already-up-to-date".to_string()
                    } else {
                        "updated".to_string()
                    },
                    release_index,
                });
                if let Some(entry) = updates.last() {
                    info!(
                        command_path = "nodeup.update.channel",
                        selector = %entry.selector,
                        updated_runtime = ?entry.updated_runtime,
                        status = %entry.status,
                        "Processed channel update selector"
                    );
                }
            }
            RuntimeSelector::Version(version) => {
                let current = format!("v{version}");
                updates.push(UpdateEntry {
                    selector,
                    selector_source: selector_source.clone(),
                    implicit_target,
                    selector_kind,
                    canonical_selector,
                    selector_alias_of,
                    previous_runtime: Some(current.clone()),
                    updated_runtime: Some(current),
                    status: "skipped-exact-version".to_string(),
                    release_index: None,
                });
                if let Some(entry) = updates.last() {
                    info!(
                        command_path = "nodeup.update.version",
                        selector = %entry.selector,
                        previous_runtime = ?entry.previous_runtime,
                        updated_runtime = ?entry.updated_runtime,
                        status = %entry.status,
                        "Skipped immutable exact-version update selector"
                    );
                }
            }
        }
    }

    let human = append_release_index_human_notes(
        format!("Processed updates for {} selector(s)", updates.len()),
        updates
            .iter()
            .filter_map(|entry| entry.release_index.as_ref()),
    );
    print_output(output, color, &human, &updates)?;
    Ok(0)
}

fn append_release_index_human_notes<'a>(
    human: String,
    diagnostics: impl Iterator<Item = &'a ReleaseIndexResolutionDiagnostic>,
) -> String {
    let notes = diagnostics
        .map(|diagnostic| {
            format!(
                "{}->{} stale cache age={}s",
                diagnostic.selector, diagnostic.selected_version, diagnostic.cache_age_seconds
            )
        })
        .collect::<Vec<_>>();

    if notes.is_empty() {
        human
    } else {
        format!("{human} (release index: {})", notes.join(", "))
    }
}

fn preflight_update_selectors(
    selectors: Vec<String>,
    allow_legacy_stored_linked_names: bool,
) -> Result<Vec<(String, RuntimeSelector)>> {
    selectors
        .into_iter()
        .map(|selector| {
            parse_update_selector(&selector, allow_legacy_stored_linked_names)
                .map(|parsed| (selector, parsed))
        })
        .collect()
}

fn selectors_for_update(app: &NodeupApp) -> Result<(Vec<String>, UpdateSelectorContext)> {
    let settings = app.store.load_settings()?;
    if !settings.tracked_selectors.is_empty() {
        let tracked_count = settings.tracked_selectors.len();
        return Ok((
            settings.tracked_selectors,
            UpdateSelectorContext {
                source: "tracked-selectors",
                tracked_selectors: tracked_count,
                installed_runtimes: 0,
                allow_legacy_stored_linked_names: true,
                implicit_targets: true,
            },
        ));
    }

    let installed = app.store.list_installed_versions()?;
    let installed_count = installed.len();
    Ok((
        installed,
        UpdateSelectorContext {
            source: "installed-runtimes",
            tracked_selectors: 0,
            installed_runtimes: installed_count,
            allow_legacy_stored_linked_names: false,
            implicit_targets: true,
        },
    ))
}

fn parse_update_selector(
    selector: &str,
    allow_legacy_stored_linked_names: bool,
) -> Result<RuntimeSelector> {
    match RuntimeSelector::parse(selector) {
        Ok(parsed) => Ok(parsed),
        Err(error)
            if allow_legacy_stored_linked_names
                && is_case_variant_of_reserved_channel_selector(selector.trim()) =>
        {
            // Tracked selectors can come from settings written before reserved-case linked
            // names were rejected. Keep no-arg update compatible while explicit
            // CLI args stay strict.
            Ok(RuntimeSelector::LinkedName(selector.trim().to_string()))
        }
        Err(error) => Err(error),
    }
}

fn latest_newer_version(app: &NodeupApp, current: &str) -> Result<Option<String>> {
    let current_semver = Version::parse(normalize_version(current).trim_start_matches('v'))?;
    let mut best: Option<Version> = None;

    for entry in app.releases.fetch_index()? {
        let candidate = match Version::parse(entry.version.trim_start_matches('v')) {
            Ok(version) => version,
            Err(_) => continue,
        };
        if candidate <= current_semver {
            continue;
        }
        if best
            .as_ref()
            .is_none_or(|best_version| candidate > *best_version)
        {
            best = Some(candidate);
        }
    }

    Ok(best.map(|version| format!("v{version}")))
}
