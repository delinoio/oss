use std::{fs, path::PathBuf};

use serde::Serialize;
use tracing::info;

use crate::{
    cli::{OutputFormat, ToolchainCommand},
    commands::print_output,
    errors::{NodeupError, Result},
    resolver::ResolvedRuntimeTarget,
    selectors::{is_valid_linked_name, RuntimeSelector},
    NodeupApp,
};

#[derive(Debug, Serialize)]
struct ToolchainListResponse {
    installed: Vec<String>,
    linked: std::collections::BTreeMap<String, String>,
}

#[derive(Debug, Serialize)]
struct ToolchainInstallResult {
    selector: String,
    runtime: String,
    status: String,
}

pub fn execute(command: ToolchainCommand, output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    match command {
        ToolchainCommand::List => list(output, app),
        ToolchainCommand::Install { runtimes } => install(&runtimes, output, app),
        ToolchainCommand::Uninstall { runtimes } => uninstall(&runtimes, output, app),
        ToolchainCommand::Link { name, path } => link(&name, &path, output, app),
    }
}

fn list(output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    let settings = app.store.load_settings()?;
    let installed = app.store.list_installed_versions()?;
    let response = ToolchainListResponse {
        installed,
        linked: settings.linked_runtimes,
    };

    let human = format!(
        "Installed runtimes: {} | Linked runtimes: {}",
        response.installed.len(),
        response.linked.len()
    );
    print_output(output, &human, &response)?;

    Ok(0)
}

fn install(runtimes: &[String], output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    if runtimes.is_empty() {
        return Err(NodeupError::invalid_input(
            "nodeup toolchain install requires at least one runtime selector",
        ));
    }

    let mut results = Vec::new();
    for runtime in runtimes {
        let resolved = app
            .resolver
            .resolve_selector_with_source(runtime, crate::types::RuntimeSelectorSource::Explicit)?;

        let version = match resolved.target {
            ResolvedRuntimeTarget::Version { version } => version,
            ResolvedRuntimeTarget::LinkedPath { .. } => {
                return Err(NodeupError::invalid_input(
                    "toolchain install only supports version/channel selectors",
                ));
            }
        };

        let report = app.installer.ensure_installed(&version, &app.releases)?;
        app.store.track_selector(runtime)?;

        let status = if report.state == crate::installer::InstallState::AlreadyInstalled {
            "already-installed"
        } else {
            "installed"
        };

        info!(
            command_path = "nodeup.toolchain.install",
            selector = %runtime,
            runtime = %report.version,
            status,
            "Installed runtime"
        );

        results.push(ToolchainInstallResult {
            selector: runtime.clone(),
            runtime: report.version,
            status: status.to_string(),
        });
    }

    let human = format!("Installed/verified {} runtime(s)", results.len());
    print_output(output, &human, &results)?;

    Ok(0)
}

fn uninstall(runtimes: &[String], output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    if runtimes.is_empty() {
        return Err(NodeupError::invalid_input(
            "nodeup toolchain uninstall requires at least one runtime selector",
        ));
    }

    let mut settings = app.store.load_settings()?;
    let overrides = app.overrides.load()?;

    let mut removed_versions = Vec::new();
    for runtime in runtimes {
        let selector = RuntimeSelector::parse(runtime)?;
        let version = match selector {
            RuntimeSelector::Version(version) => format!("v{version}"),
            _ => {
                return Err(NodeupError::invalid_input(
                    "toolchain uninstall only supports exact version selectors",
                ));
            }
        };

        if settings
            .default_selector
            .as_ref()
            .is_some_and(|default| default == runtime || default == &version)
        {
            return Err(NodeupError::conflict(format!(
                "Cannot uninstall {version}; it is used as default runtime"
            )));
        }

        if overrides
            .entries
            .iter()
            .any(|entry| entry.selector == runtime.as_str() || entry.selector == version)
        {
            return Err(NodeupError::conflict(format!(
                "Cannot uninstall {version}; it is referenced by an override"
            )));
        }

        app.store.remove_runtime(&version)?;
        removed_versions.push(version);
    }

    settings
        .tracked_selectors
        .retain(|selector| !removed_versions.contains(selector));
    app.store.save_settings(&settings)?;

    let human = format!("Removed {} runtime(s)", removed_versions.len());
    print_output(output, &human, &removed_versions)?;

    Ok(0)
}

fn link(name: &str, path: &str, output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    if !is_valid_linked_name(name) {
        return Err(NodeupError::invalid_input(format!(
            "Invalid linked runtime name: {name}"
        )));
    }

    let runtime_path = PathBuf::from(path);
    if !runtime_path.exists() {
        return Err(NodeupError::not_found(format!(
            "Linked runtime path does not exist: {}",
            runtime_path.display()
        )));
    }

    let absolute = fs::canonicalize(&runtime_path)?;
    let mut settings = app.store.load_settings()?;
    settings
        .linked_runtimes
        .insert(name.to_string(), absolute.to_string_lossy().to_string());
    app.store.save_settings(&settings)?;
    app.store.track_selector(name)?;

    let message = format!("Linked runtime '{name}' -> {}", absolute.display());
    let response = serde_json::json!({
        "name": name,
        "path": absolute,
        "status": "linked"
    });
    print_output(output, &message, &response)?;

    Ok(0)
}
