use std::ffi::OsString;

use tracing::info;

use crate::{
    command_diagnostics::RuntimeCommandAvailability,
    command_plan::{plan_delegated_command, DelegatedCommandMode},
    errors::{NodeupError, Result},
    process::{run_command, DelegatedStdioPolicy},
    resolver::ResolvedRuntimeTarget,
    store::runtime_executable_is_runnable,
    types::PlatformTarget,
    NodeupApp,
};

pub fn dispatch_managed_alias_if_needed(
    app: &NodeupApp,
    json_error_output_requested: bool,
) -> Result<Option<i32>> {
    let mut args = std::env::args_os();
    let Some(argv0) = args.next() else {
        return Ok(None);
    };

    let Some(alias) = crate::types::ManagedAlias::from_argv0(&argv0) else {
        return Ok(None);
    };

    PlatformTarget::ensure_supported_host("shim dispatch")?;

    let delegated_args = args.collect::<Vec<OsString>>();
    let cwd = std::env::current_dir()?;
    let resolved = app.resolver.resolve_with_precedence(None, &cwd)?;

    if let ResolvedRuntimeTarget::Version { version } = &resolved.target {
        if !app.store.is_installed(version) {
            app.installer.ensure_installed(version, &app.releases)?;
        }
    }

    let plan =
        plan_delegated_command(&resolved, &app.store, alias.as_str(), &delegated_args, &cwd)?;

    if plan.mode == DelegatedCommandMode::Direct && !plan.executable.exists() {
        let diagnostics = RuntimeCommandAvailability::for_resolved_runtime(
            &resolved,
            &app.store,
            alias.as_str(),
            false,
            "managed-shim-active-selector",
        )
        .into_error_diagnostics();
        return Err(NodeupError::not_found_with_diagnostics(
            format!(
                "Managed alias '{}' is not available in runtime {} (checked_path={}, \
                 install_on_demand_eligible={})",
                alias.as_str(),
                resolved.runtime_id(),
                plan.executable.display(),
                false
            ),
            "Install or relink the active runtime so it provides the delegated executable. On \
             Windows, verify PATH/PATHEXT precedence with `where <alias>` or PowerShell \
             `Get-Command <alias> -All`.",
            diagnostics,
        ));
    }

    if plan.mode == DelegatedCommandMode::Direct
        && !runtime_executable_is_runnable(&plan.executable)
    {
        let diagnostics = RuntimeCommandAvailability::for_resolved_runtime(
            &resolved,
            &app.store,
            alias.as_str(),
            false,
            "managed-shim-active-selector",
        )
        .into_error_diagnostics();
        return Err(NodeupError::not_found_with_diagnostics(
            format!(
                "Managed alias '{}' exists but is not runnable for runtime {} (path={})",
                alias.as_str(),
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
        command_path = "nodeup.dispatch.alias",
        argv0 = %alias.as_str(),
        runtime = %resolved.runtime_id(),
        mode = plan.mode.as_str(),
        package_spec,
        package_manager_strategy,
        corepack_supported,
        package_spec_pinned,
        package_json_path = %package_json_path,
        reason = plan.reason.as_str(),
        executable = %plan.executable.display(),
        "Dispatching managed alias"
    );

    if !json_error_output_requested {
        if let Some(notice) = plan.direct_package_manager_human_notice() {
            eprintln!("{notice}");
        }
        if let Some(notice) = plan.npm_exec_human_notice() {
            eprintln!("{notice}");
        }
    }

    let exit_code = run_command(
        &plan.executable,
        &plan.args,
        DelegatedStdioPolicy::Inherit,
        "nodeup.dispatch.process",
    )?;
    Ok(Some(exit_code))
}
