use serde::Serialize;
use tracing::info;

use crate::{
    cli::{OutputColorMode, OutputFormat, ShowCommand},
    commands::print_output,
    errors::{NodeupError, Result},
    release_index::ReleaseIndexResolutionDiagnostic,
    resolver::ResolvedRuntimeTarget,
    NodeupApp,
};

#[derive(Debug, Serialize)]
struct ActiveRuntimeResponse {
    runtime: String,
    source: String,
    selector: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    release_index: Option<ReleaseIndexResolutionDiagnostic>,
}

#[derive(Debug, Serialize)]
struct HomeResponse {
    data_root: String,
    cache_root: String,
    config_root: String,
}

pub fn execute(
    command: ShowCommand,
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    match command {
        ShowCommand::ActiveRuntime => show_active_runtime(output, color, app),
        ShowCommand::Home => show_home(output, color, app),
    }
}

fn show_active_runtime(
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    let cwd = std::env::current_dir()?;
    let resolved = app.resolver.resolve_with_precedence(None, &cwd)?;

    if let ResolvedRuntimeTarget::Version { version } = &resolved.target {
        if !app.store.is_installed(version) {
            info!(
                command_path = "nodeup.show.active-runtime",
                runtime = %resolved.runtime_id(),
                selector = %resolved.selector,
                selector_source = resolved.source.as_str(),
                availability = false,
                reason = "runtime-not-installed",
                "Active runtime is unavailable"
            );
            return Err(NodeupError::not_found_with_hint(
                format!("Runtime {version} is not installed"),
                "Install it with `nodeup toolchain install <runtime>` and retry `nodeup show \
                 active-runtime`.",
            ));
        }
    }

    let executable = resolved.executable_path(&app.store, "node");
    if !executable.exists() {
        info!(
            command_path = "nodeup.show.active-runtime",
            runtime = %resolved.runtime_id(),
            selector = %resolved.selector,
            selector_source = resolved.source.as_str(),
            availability = false,
            reason = "node-executable-missing",
            executable = %executable.display(),
            "Active runtime is unavailable"
        );
        return Err(NodeupError::not_found_with_hint(
            format!(
                "Command 'node' does not exist for runtime {}",
                resolved.runtime_id()
            ),
            "Reinstall the runtime with `nodeup toolchain install <runtime>` or relink it with \
             `nodeup toolchain link <name> <path>`.",
        ));
    }

    let response = ActiveRuntimeResponse {
        runtime: resolved.runtime_id(),
        source: format!("{:?}", resolved.source).to_lowercase(),
        selector: resolved.selector.stable_id(),
        release_index: app.resolver.release_index_diagnostic(),
    };
    let human = append_release_index_human_note(
        format!("Active runtime: {}", response.runtime),
        response.release_index.as_ref(),
    );

    print_output(output, color, &human, &response)?;
    Ok(0)
}

fn append_release_index_human_note(
    human: String,
    diagnostic: Option<&ReleaseIndexResolutionDiagnostic>,
) -> String {
    match diagnostic {
        Some(diagnostic) => format!(
            "{human} (release index: stale cache fallback, age={}s, selected={})",
            diagnostic.cache_age_seconds, diagnostic.selected_version
        ),
        None => human,
    }
}

fn show_home(output: OutputFormat, color: Option<OutputColorMode>, app: &NodeupApp) -> Result<i32> {
    let response = HomeResponse {
        data_root: app.paths.data_root.to_string_lossy().to_string(),
        cache_root: app.paths.cache_root.to_string_lossy().to_string(),
        config_root: app.paths.config_root.to_string_lossy().to_string(),
    };
    let human = format!(
        "nodeup home:\ndata_root: {}\ncache_root: {}\nconfig_root: {}",
        response.data_root, response.cache_root, response.config_root
    );

    print_output(output, color, &human, &response)?;
    Ok(0)
}
