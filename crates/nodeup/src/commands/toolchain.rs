use std::{collections::HashSet, fs, path::PathBuf};

use serde::Serialize;
use serde_json::json;
use tracing::info;

use crate::{
    cli::{OutputColorMode, OutputFormat, ToolchainCommand, ToolchainListDetail},
    command_diagnostics::{
        managed_alias_availability_for_linked_runtime, render_availability_matrix,
        RuntimeCommandAvailability, PATH_PRECEDENCE_GUIDANCE,
    },
    commands::print_output,
    errors::{ErrorDiagnostics, ErrorKind, NodeupError, Result},
    release_index::ReleaseIndexResolutionDiagnostic,
    resolver::ResolvedRuntimeTarget,
    selectors::{
        is_case_variant_of_reserved_channel_selector, is_reserved_channel_selector_token,
        is_valid_linked_name, RuntimeSelector,
    },
    store::{runtime_executable_is_runnable, runtime_primary_executable_path},
    types::PlatformTarget,
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
    selector_kind: String,
    canonical_selector: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    selector_alias_of: Option<String>,
    runtime: String,
    status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    release_index: Option<ReleaseIndexResolutionDiagnostic>,
}

#[derive(Debug, Serialize)]
struct ToolchainLinkResponse {
    name: String,
    selector_kind: &'static str,
    canonical_selector: String,
    path: String,
    status: String,
    managed_shim_commands: Vec<RuntimeCommandAvailability>,
    install_on_demand_eligible: bool,
    path_precedence_guidance: &'static str,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize)]
#[serde(rename_all = "kebab-case")]
enum RuntimeReferenceBlockerKind {
    GlobalDefault,
    DirectoryOverride,
}

impl RuntimeReferenceBlockerKind {
    fn as_str(self) -> &'static str {
        match self {
            Self::GlobalDefault => "global-default",
            Self::DirectoryOverride => "directory-override",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
struct RuntimeReferenceBlocker {
    reference_type: RuntimeReferenceBlockerKind,
    runtime: String,
    selector: String,
    path: String,
    clear_command: String,
    change_command: String,
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
        ToolchainCommand::Unlink { names } => unlink(&names, output, color, app),
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

    PlatformTarget::ensure_supported_host("runtime installation")?;

    let mut results = Vec::new();
    for runtime in runtimes {
        let selector = RuntimeSelector::parse(runtime)?;
        if matches!(selector, RuntimeSelector::LinkedName(_)) {
            return Err(NodeupError::invalid_input_with_hint(
                format!(
                    "`toolchain install` only supports semantic version or channel selectors \
                     (selector={runtime})"
                ),
                "Use selectors like `22.1.0`, `v22.1.0`, `lts`, `current`, or `latest`. Linked \
                 runtimes are added with `nodeup toolchain link <name> <path>`.",
            ));
        }

        let resolved = app.resolver.resolve_selector_with_source(
            &selector.stable_id(),
            crate::types::RuntimeSelectorSource::Explicit,
        )?;
        let release_index = app.resolver.release_index_diagnostic();

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
            selector_kind: selector.kind().as_str().to_string(),
            canonical_selector: selector.canonical_id(),
            selector_alias_of: selector.alias_of(),
            runtime: report.version,
            status: status.to_string(),
            release_index,
        });
    }

    let human = append_release_index_human_notes(
        format!("Installed/verified {} runtime(s)", results.len()),
        results
            .iter()
            .filter_map(|result| result.release_index.as_ref()),
    );
    print_output(output, color, &human, &results)?;

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
                    "Use selectors like `22.1.0` or `v22.1.0`. Remove linked runtime records with \
                     `nodeup toolchain unlink <name>`.",
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

    let mut blockers = Vec::new();
    let mut missing_version = None;
    for version in &unique_versions {
        blockers.extend(runtime_reference_blockers(
            version,
            settings.default_selector.as_deref(),
            &overrides.entries,
            app,
        ));
        if missing_version.is_none() && !app.store.is_installed(version) {
            missing_version = Some(version.clone());
        }
    }

    if !blockers.is_empty() {
        blockers.sort_by(|left, right| {
            left.runtime
                .cmp(&right.runtime)
                .then_with(|| left.reference_type.cmp(&right.reference_type))
                .then_with(|| left.path.cmp(&right.path))
                .then_with(|| left.selector.cmp(&right.selector))
        });
        return Err(runtime_reference_blockers_error(blockers));
    }

    if let Some(version) = missing_version {
        return Err(NodeupError::not_found_with_hint(
            format!("Runtime {version} is not installed"),
            "List installed runtimes with `nodeup toolchain list` and retry with an installed \
             version.",
        ));
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

fn runtime_reference_blockers(
    version: &str,
    default_selector: Option<&str>,
    overrides: &[crate::overrides::OverrideEntry],
    app: &NodeupApp,
) -> Vec<RuntimeReferenceBlocker> {
    let mut blockers = Vec::new();

    if let Some(default) = default_selector {
        if selector_references_version(default, version) {
            blockers.push(RuntimeReferenceBlocker {
                reference_type: RuntimeReferenceBlockerKind::GlobalDefault,
                runtime: version.to_string(),
                selector: default.to_string(),
                path: app.store.paths().settings_file.display().to_string(),
                clear_command: "nodeup default <runtime>".to_string(),
                change_command: "nodeup default <runtime>".to_string(),
            });
        }
    }

    for entry in overrides {
        if selector_references_version(&entry.selector, version) {
            blockers.push(RuntimeReferenceBlocker {
                reference_type: RuntimeReferenceBlockerKind::DirectoryOverride,
                runtime: version.to_string(),
                selector: entry.selector.clone(),
                path: entry.path.clone(),
                clear_command: format!("nodeup override unset --path {}", shell_quote(&entry.path)),
                change_command: format!(
                    "nodeup override set <runtime> --path {}",
                    shell_quote(&entry.path)
                ),
            });
        }
    }

    blockers
}

fn runtime_reference_blockers_error(blockers: Vec<RuntimeReferenceBlocker>) -> NodeupError {
    let blocked_version_list = blockers
        .iter()
        .map(|blocker| blocker.runtime.as_str())
        .collect::<std::collections::BTreeSet<_>>()
        .into_iter()
        .collect::<Vec<_>>();
    let blocked_versions = blocked_version_list.join(", ");
    let blocker_summary = blockers
        .iter()
        .map(|blocker| {
            format!(
                "{} path={} selector={}",
                blocker.reference_type.as_str(),
                blocker.path,
                blocker.selector
            )
        })
        .collect::<Vec<_>>()
        .join("; ");
    let follow_up_commands = blockers
        .iter()
        .map(|blocker| match blocker.reference_type {
            RuntimeReferenceBlockerKind::GlobalDefault => "`nodeup default <runtime>`".to_string(),
            RuntimeReferenceBlockerKind::DirectoryOverride => {
                format!(
                    "`{}` or `{}`",
                    blocker.clear_command, blocker.change_command
                )
            }
        })
        .collect::<std::collections::BTreeSet<_>>()
        .into_iter()
        .collect::<Vec<_>>();
    let retry_commands = blockers
        .iter()
        .map(|blocker| format!("nodeup toolchain uninstall {}", blocker.runtime))
        .collect::<std::collections::BTreeSet<_>>()
        .into_iter()
        .collect::<Vec<_>>();

    let mut diagnostics = ErrorDiagnostics::new();
    diagnostics.insert("blocked_versions".to_string(), json!(blocked_version_list));
    diagnostics.insert("blockers".to_string(), json!(blockers));

    NodeupError::with_hint_and_diagnostics(
        ErrorKind::Conflict,
        format!(
            "Cannot uninstall {blocked_versions}; referenced by blocking runtime selectors \
             ({blocker_summary})"
        ),
        format!(
            "Clear or change the blocking references first with {}, then retry with {}.",
            follow_up_commands.join(", "),
            retry_commands
                .iter()
                .map(|command| format!("`{command}`"))
                .collect::<Vec<_>>()
                .join(", ")
        ),
        diagnostics,
    )
}

fn shell_quote(value: &str) -> String {
    if !value.is_empty()
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'/' | b'.' | b'_' | b'-'))
    {
        return value.to_string();
    }

    format!("'{}'", value.replace('\'', "'\\''"))
}

fn selector_references_linked_name(selector: &str, target_name: &str) -> bool {
    match RuntimeSelector::parse(selector).ok() {
        Some(RuntimeSelector::LinkedName(name)) => name == target_name,
        Some(_) => false,
        None => selector == target_name,
    }
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

    if is_case_variant_of_reserved_channel_selector(name) {
        info!(
            command_path = "nodeup.toolchain.link",
            linked_name = %name,
            requested_path = %path,
            validation = false,
            reason = "reserved-linked-name-case-variant",
            "Rejected linked runtime"
        );
        return Err(NodeupError::invalid_input_with_hint(
            format!("Invalid linked runtime name: {name}"),
            "Linked runtime names are case-sensitive, but names that differ from reserved channel \
             selectors (`lts`, `current`, `latest`) only by case are not allowed.",
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
    let node_executable = runtime_primary_executable_path(&absolute, "node");
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

    if !runtime_executable_is_runnable(&node_executable) {
        info!(
            command_path = "nodeup.toolchain.link",
            linked_name = %name,
            requested_path = %runtime_path.display(),
            resolved_path = %absolute.display(),
            expected_node_path = %node_executable.display(),
            validation = false,
            reason = "node-executable-not-runnable",
            "Rejected linked runtime"
        );
        return Err(NodeupError::invalid_input_with_hint(
            format!(
                "Linked runtime node executable exists but is not runnable: {}",
                node_executable.display()
            ),
            "On Unix, ensure the executable bit is set on `<path>/bin/node`. On Windows, ensure \
             the runtime provides `<path>/bin/node.exe`.",
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

    let managed_shim_commands = managed_alias_availability_for_linked_runtime(name, &absolute);
    let availability_matrix = render_availability_matrix(&managed_shim_commands);
    let message = format!(
        "Linked runtime '{name}' -> {}\nManaged shim command availability:\n{}\nInstall on \
         demand: not eligible for linked runtimes; install-on-demand only provisions missing \
         Nodeup-managed version runtimes selected by a shim.\nWindows PATH/PATHEXT guidance: {}",
        absolute.display(),
        availability_matrix,
        PATH_PRECEDENCE_GUIDANCE
    );
    let response = ToolchainLinkResponse {
        name: name.to_string(),
        selector_kind: "linked-runtime",
        canonical_selector: name.to_string(),
        path: absolute.display().to_string(),
        status: "linked".to_string(),
        managed_shim_commands,
        install_on_demand_eligible: false,
        path_precedence_guidance: PATH_PRECEDENCE_GUIDANCE,
    };
    print_output(output, color, &message, &response)?;

    Ok(0)
}

fn unlink(
    names: &[String],
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    if names.is_empty() {
        return Err(NodeupError::invalid_input_with_hint(
            format!(
                "Missing linked runtime name for `nodeup toolchain unlink` (requested_count={})",
                names.len()
            ),
            "Run `nodeup toolchain unlink <name>...`.",
        ));
    }

    let mut settings = app.store.load_settings()?;
    let overrides = app.overrides.load()?;

    info!(
        command_path = "nodeup.toolchain.unlink",
        requested_count = names.len(),
        "Starting linked runtime unlink preflight"
    );

    let mut unique_names = Vec::new();
    let mut seen_names = HashSet::new();
    for name in names {
        if seen_names.insert(name.clone()) {
            unique_names.push(name.clone());
        }
    }

    for name in &unique_names {
        if !settings.linked_runtimes.contains_key(name) {
            return Err(NodeupError::not_found_with_hint(
                format!("Linked runtime '{name}' does not exist"),
                "List linked runtimes with `nodeup toolchain list --verbose` and retry with an \
                 existing linked runtime name.",
            ));
        }

        if settings
            .default_selector
            .as_ref()
            .is_some_and(|default| selector_references_linked_name(default, name))
        {
            return Err(NodeupError::conflict_with_hint(
                format!("Cannot unlink '{name}'; it is used as the default runtime"),
                "Set a different default first with `nodeup default <runtime>`, then retry unlink.",
            ));
        }

        if overrides
            .entries
            .iter()
            .any(|entry| selector_references_linked_name(&entry.selector, name))
        {
            return Err(NodeupError::conflict_with_hint(
                format!("Cannot unlink '{name}'; it is referenced by a directory override"),
                "Update or remove the blocking override with `nodeup override set <runtime> \
                 --path <path>` or `nodeup override unset --path <path>`.",
            ));
        }
    }

    for name in &unique_names {
        settings.linked_runtimes.remove(name);
    }

    let removed_names = unique_names.into_iter().collect::<HashSet<_>>();
    settings
        .tracked_selectors
        .retain(|selector| !removed_names.contains(selector));
    app.store.save_settings(&settings)?;

    let mut removed_names = removed_names.into_iter().collect::<Vec<_>>();
    removed_names.sort();
    info!(
        command_path = "nodeup.toolchain.unlink",
        removed_count = removed_names.len(),
        removed_names = ?removed_names,
        "Completed linked runtime unlink"
    );
    let human = format!("Removed {} linked runtime(s)", removed_names.len());
    print_output(output, color, &human, &removed_names)?;

    Ok(0)
}
