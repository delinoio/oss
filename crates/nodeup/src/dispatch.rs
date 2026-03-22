use std::ffi::OsString;

use tracing::info;

use crate::{
    command_plan::{plan_delegated_command, DelegatedCommandMode},
    errors::{NodeupError, Result},
    process::{run_command, DelegatedStdioPolicy},
    resolver::ResolvedRuntimeTarget,
    NodeupApp,
};

pub fn dispatch_managed_alias_if_needed(app: &NodeupApp) -> Result<Option<i32>> {
    let mut args = std::env::args_os();
    let Some(argv0) = args.next() else {
        return Ok(None);
    };

    let Some(alias) = crate::types::ManagedAlias::from_argv0(&argv0) else {
        return Ok(None);
    };

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
        return Err(NodeupError::not_found_with_hint(
            format!(
                "Managed alias '{}' is not available in runtime {}",
                alias.as_str(),
                resolved.runtime_id()
            ),
            "Install or relink the active runtime so it provides the delegated executable.",
        ));
    }

    let package_spec = plan.package_spec.as_deref().unwrap_or("none");
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
        package_json_path = %package_json_path,
        reason = plan.reason.as_str(),
        executable = %plan.executable.display(),
        "Dispatching managed alias"
    );

    let exit_code = run_command(
        &plan.executable,
        &plan.args,
        DelegatedStdioPolicy::Inherit,
        "nodeup.dispatch.process",
    )?;
    Ok(Some(exit_code))
}
