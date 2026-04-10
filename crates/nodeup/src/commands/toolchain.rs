use std::{collections::HashSet, fs, path::PathBuf};

use serde::Serialize;
use tracing::info;

use crate::{
    cli::{OutputColorMode, OutputFormat, ToolchainCommand, ToolchainListDetail},
    commands::print_output,
    errors::{NodeupError, Result},
    resolver::ResolvedRuntimeTarget,
    selectors::{is_reserved_channel_selector_token, is_valid_linked_name, RuntimeSelector},
    store::runtime_executable_path,
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

pub fn execute(
    command: ToolchainCommand,
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    match command {
        ToolchainCommand::List { quiet, verbose } => list(
            ToolchainListDetail::from_flags(quiet, verbose),
            output,
            color,
            app,
        ),
        ToolchainCommand::Install { runtimes } => install(&runtimes, output, color, app),
        ToolchainCommand::Uninstall { runtimes } => uninstall(&runtimes, output, color, app),
        ToolchainCommand::Link { name, path } => link(&name, &path, output, color, app),
    }
}

fn list(
    list_detail: ToolchainListDetail,
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    let settings = app.store.load_settings()?;
    let installed = app.store.list_installed_versions()?;
    let response = ToolchainListResponse {
        installed,
        linked: settings.linked_runtimes,
    };

    info!(
        command_path = "nodeup.toolchain.list",
        list_format = list_detail.as_str(),
        installed_count = response.installed.len(),
        linked_count = response.linked.len(),
        "Listed runtimes"
    );

    let human = render_human_toolchain_list(list_detail, &response, app);
    if output == OutputFormat::Human
        && list_detail == ToolchainListDetail::Quiet
        && human.is_empty()
    {
        return Ok(0);
    }
    print_output(output, color, &human, &response)?;

    Ok(0)
}

fn render_human_toolchain_list(
    list_detail: ToolchainListDetail,
    response: &ToolchainListResponse,
    app: &NodeupApp,
) -> String {
    match list_detail {
        ToolchainListDetail::Standard => format!(
            "Installed runtimes: {} | Linked runtimes: {}",
            response.installed.len(),
            response.linked.len()
        ),
        ToolchainListDetail::Quiet => {
            let mut identifiers = response.installed.clone();
            identifiers.extend(response.linked.keys().cloned());

            if identifiers.is_empty() {
                String::new()
            } else {
                identifiers.join("\n")
            }
        }
        ToolchainListDetail::Verbose => {
            let mut lines = vec![format!(
                "Installed runtimes ({}):",
                response.installed.len()
            )];

            if response.installed.is_empty() {
                lines.push("- (none)".to_string());
            } else {
                for runtime in &response.installed {
                    lines.push(format!(
                        "- {} -> {}",
                        runtime,
                        app.store.runtime_dir(runtime).display()
                    ));
                }
            }

            lines.push(format!("Linked runtimes ({}):", response.linked.len()));

            if response.linked.is_empty() {
                lines.push("- (none)".to_string());
            } else {
                for (name, path) in &response.linked {
                    lines.push(format!("- {} -> {}", name, path));
                }
            }

            lines.join("\n")
        }
    }
}

fn install(
    runtimes: &[String],
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    if runtimes.is_empty() {
        return Err(NodeupError::invalid_input_with_hint(
            format!(
                "Missing runtime selector for `nodeup toolchain install` (requested_count={})",
                runtimes.len()
            ),
            "Run `nodeup toolchain install <runtime>...`.",
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
                return Err(NodeupError::invalid_input_with_hint(
                    format!(
                        "`toolchain install` only supports semantic version or channel selectors \
                         (selector={runtime})"
                    ),
                    "Use selectors like `22.1.0`, `v22.1.0`, `lts`, `current`, or `latest`. \
                     Linked runtimes are added with `nodeup toolchain link <name> <path>`.",
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
    print_output(output, color, &human, &results)?;

    Ok(0)
}

fn uninstall(
    runtimes: &[String],
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    if runtimes.is_empty() {
        return Err(NodeupError::invalid_input_with_hint(
            format!(
                "Missing runtime selector for `nodeup toolchain uninstall` (requested_count={})",
                runtimes.len()
            ),
            "Run `nodeup toolchain uninstall <runtime>...`.",
        ));
    }

    let mut settings = app.store.load_settings()?;
    let overrides = app.overrides.load()?;

    info!(
        command_path = "nodeup.toolchain.uninstall",
        requested_count = runtimes.len(),
        "Starting uninstall preflight"
    );

    let mut unique_versions = Vec::new();
    let mut seen_versions = HashSet::new();
    for runtime in runtimes {
        let selector = RuntimeSelector::parse(runtime)?;
        let version = match selector {
            RuntimeSelector::Version(version) => format!("v{version}"),
            _ => {
                return Err(NodeupError::invalid_input_with_hint(
                    format!(
                        "`toolchain uninstall` only supports exact version selectors \
                         (selector={runtime})"
                    ),
                    "Use selectors like `22.1.0` or `v22.1.0`. Channels and linked runtime names \
                     are not supported here.",
                ));
            }
        };

        if seen_versions.insert(version.clone()) {
            unique_versions.push(version);
        }
    }

    info!(
        command_path = "nodeup.toolchain.uninstall",
        requested_count = runtimes.len(),
        unique_count = unique_versions.len(),
        "Completed uninstall preflight target parsing"
    );

    for version in &unique_versions {
        if settings
            .default_selector
            .as_ref()
            .is_some_and(|default| selector_references_version(default, version))
        {
            return Err(NodeupError::conflict_with_hint(
                format!("Cannot uninstall {version}; it is used as the default runtime"),
                "Set a different default first with `nodeup default <runtime>`, then retry \
                 uninstall.",
            ));
        }

        if overrides
            .entries
            .iter()
            .any(|entry| selector_references_version(&entry.selector, version))
        {
            return Err(NodeupError::conflict_with_hint(
                format!("Cannot uninstall {version}; it is referenced by a directory override"),
                "Update or remove the blocking override with `nodeup override set <runtime> \
                 --path <path>` or `nodeup override unset --path <path>`.",
            ));
        }

        if !app.store.is_installed(version) {
            return Err(NodeupError::not_found_with_hint(
                format!("Runtime {version} is not installed"),
                "List installed runtimes with `nodeup toolchain list` and retry with an installed \
                 version.",
            ));
        }
    }

    for version in &unique_versions {
        app.store.remove_runtime(version)?;
    }

    let removed_versions = unique_versions.into_iter().collect::<HashSet<_>>();
    settings.tracked_selectors.retain(|selector| {
        if let Some(canonical_selector_version) = canonical_version_selector(selector) {
            !removed_versions.contains(&canonical_selector_version)
        } else {
            !removed_versions.contains(selector)
        }
    });
    app.store.save_settings(&settings)?;

    let mut removed_versions = removed_versions.into_iter().collect::<Vec<_>>();
    removed_versions.sort();
    info!(
        command_path = "nodeup.toolchain.uninstall",
        removed_count = removed_versions.len(),
        removed_versions = ?removed_versions,
        "Completed runtime uninstall"
    );
    let human = format!("Removed {} runtime(s)", removed_versions.len());
    print_output(output, color, &human, &removed_versions)?;

    Ok(0)
}

fn selector_references_version(selector: &str, target_version: &str) -> bool {
    canonical_version_selector(selector)
        .is_some_and(|canonical_selector_version| canonical_selector_version == target_version)
}

fn canonical_version_selector(selector: &str) -> Option<String> {
    match RuntimeSelector::parse(selector).ok()? {
        RuntimeSelector::Version(version) => Some(format!("v{version}")),
        _ => None,
    }
}

fn link(
    name: &str,
    path: &str,
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    if !is_valid_linked_name(name) {
        info!(
            command_path = "nodeup.toolchain.link",
            linked_name = %name,
            requested_path = %path,
            validation = false,
            reason = "invalid-linked-name",
            "Rejected linked runtime"
        );
        return Err(NodeupError::invalid_input_with_hint(
            format!("Invalid linked runtime name: {name}"),
            "Use a linked runtime name that matches `[A-Za-z0-9][A-Za-z0-9_-]*`.",
        ));
    }

    if is_reserved_channel_selector_token(name) {
        info!(
            command_path = "nodeup.toolchain.link",
            linked_name = %name,
            requested_path = %path,
            validation = false,
            reason = "reserved-linked-name",
            "Rejected linked runtime"
        );
        return Err(NodeupError::invalid_input_with_hint(
            format!("Invalid linked runtime name: {name}"),
            "Reserved channel selectors (`lts`, `current`, `latest`) cannot be used as linked \
             runtime names.",
        ));
    }

    let runtime_path = PathBuf::from(path);
    if !runtime_path.exists() {
        info!(
            command_path = "nodeup.toolchain.link",
            linked_name = %name,
            requested_path = %runtime_path.display(),
            validation = false,
            reason = "linked-path-missing",
            "Rejected linked runtime"
        );
        return Err(NodeupError::not_found_with_hint(
            format!(
                "Linked runtime path does not exist: {}",
                runtime_path.display()
            ),
            "Provide an existing runtime directory path to `nodeup toolchain link`.",
        ));
    }

    if !runtime_path.is_dir() {
        info!(
            command_path = "nodeup.toolchain.link",
            linked_name = %name,
            requested_path = %runtime_path.display(),
            validation = false,
            reason = "linked-path-not-directory",
            "Rejected linked runtime"
        );
        return Err(NodeupError::invalid_input_with_hint(
            format!(
                "Linked runtime path is not a directory: {}",
                runtime_path.display()
            ),
            "Provide a runtime directory that contains a `bin/node` or `bin/node.exe` executable.",
        ));
    }

    let absolute = fs::canonicalize(&runtime_path)?;
    let node_executable = runtime_executable_path(&absolute, "node");
    if !node_executable.exists() {
        info!(
            command_path = "nodeup.toolchain.link",
            linked_name = %name,
            requested_path = %runtime_path.display(),
            resolved_path = %absolute.display(),
            expected_node_path = %node_executable.display(),
            validation = false,
            reason = "node-executable-missing",
            "Rejected linked runtime"
        );
        return Err(NodeupError::invalid_input_with_hint(
            format!(
                "Linked runtime path must contain a node executable under `bin/`: {}",
                absolute.display()
            ),
            "Verify the runtime root path and ensure `<path>/bin/node` or `<path>/bin/node.exe` \
             exists before linking.",
        ));
    }

    let mut settings = app.store.load_settings()?;
    settings
        .linked_runtimes
        .insert(name.to_string(), absolute.to_string_lossy().to_string());
    app.store.save_settings(&settings)?;
    app.store.track_selector(name)?;

    info!(
        command_path = "nodeup.toolchain.link",
        linked_name = %name,
        linked_path = %absolute.display(),
        validation = true,
        status = "linked",
        "Linked runtime"
    );

    let message = format!("Linked runtime '{name}' -> {}", absolute.display());
    let response = serde_json::json!({
        "name": name,
        "path": absolute,
        "status": "linked"
    });
    print_output(output, color, &message, &response)?;

    Ok(0)
}
