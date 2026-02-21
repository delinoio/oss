use semver::Version;
use serde::Serialize;

use crate::{
    cli::OutputFormat,
    commands::print_output,
    errors::{NodeupError, Result},
    release_index::normalize_version,
    resolver::ResolvedRuntimeTarget,
    selectors::RuntimeSelector,
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
    previous_runtime: Option<String>,
    updated_runtime: Option<String>,
    status: String,
}

pub fn check(output: OutputFormat, app: &NodeupApp) -> Result<i32> {
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

    print_output(output, &human, &results)?;
    Ok(0)
}

pub fn update(runtimes: Vec<String>, output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    let selectors = if runtimes.is_empty() {
        selectors_for_update(app)?
    } else {
        runtimes
    };

    if selectors.is_empty() {
        return Err(NodeupError::not_found(
            "No runtimes to update. Install runtimes or configure tracked selectors first",
        ));
    }

    let mut updates = Vec::new();
    for selector in selectors {
        let parsed = RuntimeSelector::parse(&selector)?;
        match parsed {
            RuntimeSelector::LinkedName(_) => {
                updates.push(UpdateEntry {
                    selector,
                    previous_runtime: None,
                    updated_runtime: None,
                    status: "skipped-linked-runtime".to_string(),
                });
            }
            RuntimeSelector::Channel(_) => {
                let resolved = app
                    .resolver
                    .resolve_selector_with_source(&selector, RuntimeSelectorSource::Explicit)?;
                let version = match resolved.target {
                    ResolvedRuntimeTarget::Version { version } => version,
                    ResolvedRuntimeTarget::LinkedPath { .. } => unreachable!(),
                };
                let report = app.installer.ensure_installed(&version, &app.releases)?;
                updates.push(UpdateEntry {
                    selector,
                    previous_runtime: None,
                    updated_runtime: Some(report.version),
                    status: if report.state == crate::installer::InstallState::AlreadyInstalled {
                        "already-up-to-date".to_string()
                    } else {
                        "updated".to_string()
                    },
                });
            }
            RuntimeSelector::Version(version) => {
                let current = format!("v{version}");
                let next = latest_newer_version(app, &current)?;
                if let Some(next_version) = next {
                    app.installer
                        .ensure_installed(&next_version, &app.releases)?;
                    updates.push(UpdateEntry {
                        selector,
                        previous_runtime: Some(current),
                        updated_runtime: Some(next_version),
                        status: "updated".to_string(),
                    });
                } else {
                    updates.push(UpdateEntry {
                        selector,
                        previous_runtime: Some(current.clone()),
                        updated_runtime: Some(current),
                        status: "already-up-to-date".to_string(),
                    });
                }
            }
        }
    }

    let human = format!("Processed updates for {} selector(s)", updates.len());
    print_output(output, &human, &updates)?;
    Ok(0)
}

fn selectors_for_update(app: &NodeupApp) -> Result<Vec<String>> {
    let settings = app.store.load_settings()?;
    if !settings.tracked_selectors.is_empty() {
        return Ok(settings.tracked_selectors);
    }

    let installed = app.store.list_installed_versions()?;
    Ok(installed)
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
