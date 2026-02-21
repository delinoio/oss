use serde::Serialize;
use tracing::info;

use crate::{
    cli::OutputFormat, commands::print_output, errors::Result, resolver::ResolvedRuntimeTarget,
    types::RuntimeSelectorSource, NodeupApp,
};

#[derive(Debug, Serialize)]
struct DefaultResponse {
    default_selector: Option<String>,
    resolved_runtime: Option<String>,
}

pub fn execute(runtime: Option<&str>, output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    if let Some(runtime_selector) = runtime {
        let resolved = app
            .resolver
            .resolve_selector_with_source(runtime_selector, RuntimeSelectorSource::Explicit)?;

        if let ResolvedRuntimeTarget::Version { version } = &resolved.target {
            app.installer.ensure_installed(version, &app.releases)?;
        }

        let mut settings = app.store.load_settings()?;
        settings.default_selector = Some(runtime_selector.to_string());
        app.store.save_settings(&settings)?;
        app.store.track_selector(runtime_selector)?;

        info!(
            command_path = "nodeup.default",
            selector = %runtime_selector,
            resolved_runtime = %resolved.runtime_id(),
            "Updated default runtime"
        );

        let response = DefaultResponse {
            default_selector: Some(runtime_selector.to_string()),
            resolved_runtime: Some(resolved.runtime_id()),
        };
        let human = format!(
            "Default runtime set to {}",
            response.default_selector.as_deref().unwrap_or("")
        );
        print_output(output, &human, &response)?;
        return Ok(0);
    }

    let settings = app.store.load_settings()?;
    let resolved_runtime = if let Some(selector) = settings.default_selector.as_ref() {
        Some(
            app.resolver
                .resolve_selector_with_source(selector, RuntimeSelectorSource::Default)?
                .runtime_id(),
        )
    } else {
        None
    };

    let response = DefaultResponse {
        default_selector: settings.default_selector,
        resolved_runtime,
    };
    let human = if let Some(default_selector) = response.default_selector.as_ref() {
        format!("Default runtime: {default_selector}")
    } else {
        "Default runtime is not set".to_string()
    };

    print_output(output, &human, &response)?;

    Ok(0)
}
