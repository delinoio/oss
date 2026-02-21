use std::ffi::OsString;

use tracing::info;

use crate::{
    errors::{NodeupError, Result},
    process::run_command,
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

    let executable = resolved.executable_path(&app.store, alias.as_str());
    if !executable.exists() {
        return Err(NodeupError::not_found(format!(
            "Managed alias '{}' is not available in runtime {}",
            alias.as_str(),
            resolved.runtime_id()
        )));
    }

    info!(
        command_path = "nodeup.dispatch.alias",
        argv0 = %alias.as_str(),
        runtime = %resolved.runtime_id(),
        executable = %executable.display(),
        "Dispatching managed alias"
    );

    let exit_code = run_command(&executable, &delegated_args, "nodeup.dispatch.process")?;
    Ok(Some(exit_code))
}
