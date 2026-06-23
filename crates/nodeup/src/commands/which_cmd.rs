use std::path::Path;

use serde::Serialize;
use tracing::info;

use crate::{
    cli::{OutputColorMode, OutputFormat},
    command_diagnostics::RuntimeCommandAvailability,
    command_plan::{plan_delegated_command, DelegatedCommandMode, DelegatedCommandPlanDiagnostics},
    commands::print_output,
    errors::{NodeupError, Result},
    release_index::ReleaseIndexResolutionDiagnostic,
    resolver::ResolvedRuntimeTarget,
    store::runtime_executable_is_runnable,
    NodeupApp,
};

#[derive(Debug, Serialize)]
struct WhichResponse {
    runtime: String,
    command: String,
    requested_command: String,
    executable_path: String,
    mode: String,
    reason: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    package_manager_strategy: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    corepack_supported: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    package_spec: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    package_spec_pinned: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    package_json_path: Option<String>,
    planning: DelegatedCommandPlanDiagnostics,
    #[serde(skip_serializing_if = "Option::is_none")]
    release_index: Option<ReleaseIndexResolutionDiagnostic>,
}

pub fn execute(
    runtime: Option<&str>,
    command: &str,
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    let cwd = std::env::current_dir()?;
    let resolved = app.resolver.resolve_with_precedence(runtime, &cwd)?;

    if let ResolvedRuntimeTarget::Version { version } = &resolved.target {
        if !app.store.is_installed(version) {
            return Err(NodeupError::not_found_with_hint(
                format!("Runtime {version} is not installed"),
                "Install it with `nodeup toolchain install <runtime>` and retry `nodeup which \
                 ...`.",
            ));
        }
    }

    let plan = plan_delegated_command(&resolved, &app.store, command, &[], &cwd)?;

    if plan.mode == DelegatedCommandMode::Direct && !Path::new(&plan.executable).exists() {
        let diagnostics = RuntimeCommandAvailability::for_resolved_runtime(
            &resolved,
            &app.store,
            command,
            false,
            "nodeup-which-command-resolution",
        )
        .into_error_diagnostics();
        return Err(NodeupError::not_found_with_diagnostics(
            format!(
                "Command '{command}' does not exist for runtime {} (checked_path={})",
                resolved.runtime_id(),
                plan.executable.display()
            ),
            "Use `nodeup show active-runtime` to confirm the runtime, then install or relink a \
             runtime that provides the command. On Windows, verify PATH/PATHEXT precedence with \
             `where <command>` or PowerShell `Get-Command <command> -All`.",
            diagnostics,
        ));
    }

    if plan.mode == DelegatedCommandMode::Direct
        && !runtime_executable_is_runnable(&plan.executable)
    {
        let diagnostics = RuntimeCommandAvailability::for_resolved_runtime(
            &resolved,
            &app.store,
            command,
            false,
            "nodeup-which-command-resolution",
        )
        .into_error_diagnostics();
        return Err(NodeupError::not_found_with_diagnostics(
            format!(
                "Command '{command}' exists but is not runnable for runtime {} (path={})",
                resolved.runtime_id(),
                plan.executable.display()
            ),
            "On Unix, ensure the executable bit is set. On Windows, relink a runtime that \
             provides the expected executable name.",
            diagnostics,
        ));
    }

    let package_spec = plan.package_spec.as_deref().unwrap_or("none");
    let package_manager_strategy = plan.package_manager_strategy().unwrap_or("none");
    let corepack_supported = plan
        .corepack_supported()
        .map(|value| value.to_string())
        .unwrap_or_else(|| "none".to_string());
    let package_spec_pinned = plan
        .package_spec_pinned()
        .map(|value| value.to_string())
        .unwrap_or_else(|| "none".to_string());
    let package_json_path = plan
        .package_json_path
        .as_ref()
        .map(|path| path.display().to_string())
        .unwrap_or_else(|| "none".to_string());

    info!(
        command_path = "nodeup.which",
        selector_source = resolved.source.as_str(),
        selector = %resolved.selector,
        runtime = %resolved.runtime_id(),
        delegated_command = command,
        mode = plan.mode.as_str(),
        package_spec,
        package_manager_strategy,
        corepack_supported,
        package_spec_pinned,
        package_json_path = %package_json_path,
        reason = plan.reason.as_str(),
        executable = %plan.executable.display(),
        "Resolved delegated executable"
    );

    let planning = plan.diagnostics();
    let response = WhichResponse {
        runtime: resolved.runtime_id(),
        command: command.to_string(),
        requested_command: planning.requested_command.clone(),
        executable_path: plan.executable.to_string_lossy().to_string(),
        mode: planning.mode.clone(),
        reason: planning.reason.clone(),
        package_manager_strategy: planning.package_manager_strategy.clone(),
        corepack_supported: planning.corepack_supported,
        package_spec: planning.package_spec.clone(),
        package_spec_pinned: planning.package_spec_pinned,
        package_json_path: planning.package_json_path.clone(),
        planning,
        release_index: app.resolver.release_index_diagnostic(),
    };

    let human = if let Some(notice) = plan.npm_exec_human_notice() {
        format!("{}\n{notice}", response.executable_path)
    } else if let Some(notice) = plan.direct_package_manager_human_notice() {
        format!("{}\n{notice}", response.executable_path)
    } else {
        response.executable_path.clone()
    };

    print_output(output, color, &human, &response)?;

    Ok(0)
}
