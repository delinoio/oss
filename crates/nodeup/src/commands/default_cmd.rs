use serde::Serialize;
use tracing::{info, warn};

use crate::{
    cli::OutputFormat,
    commands::print_output,
    errors::{ErrorKind, NodeupError, Result},
    resolver::ResolvedRuntimeTarget,
    types::RuntimeSelectorSource,
    NodeupApp,
};

#[derive(Debug, Serialize)]
struct DefaultResolutionError {
    kind: ErrorKind,
    message: String,
}

#[derive(Debug, Serialize)]
struct DefaultResponse {
    default_selector: Option<String>,
    resolved_runtime: Option<String>,
    resolution_error: Option<DefaultResolutionError>,
}

impl From<NodeupError> for DefaultResolutionError {
    fn from(value: NodeupError) -> Self {
        Self {
            kind: value.kind,
            message: value.message,
        }
    }
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
            resolution_error: None,
        };
        let human = format!(
            "Default runtime set to {}",
            response.default_selector.as_deref().unwrap_or("")
        );
        print_output(output, &human, &response)?;
        return Ok(0);
    }

    let settings = app.store.load_settings()?;
    let (resolved_runtime, resolution_error) =
        if let Some(selector) = settings.default_selector.as_ref() {
            match app
                .resolver
                .resolve_selector_with_source(selector, RuntimeSelectorSource::Default)
            {
                Ok(resolved) => (Some(resolved.runtime_id()), None),
                Err(error) => {
                    warn!(
                        command_path = "nodeup.default",
                        selector = %selector,
                        error_kind = error_kind_key(error.kind),
                        error = %error.message,
                        outcome = "unresolved",
                        "Default selector resolution failed during introspection"
                    );
                    (None, Some(DefaultResolutionError::from(error)))
                }
            }
        } else {
            (None, None)
        };

    let default_selector = settings.default_selector;
    let response = DefaultResponse {
        default_selector: default_selector.clone(),
        resolved_runtime,
        resolution_error,
    };
    let human = if let Some(selector) = default_selector.as_ref() {
        if response.resolution_error.is_some() {
            format!("Default runtime: {selector} (resolution unavailable)")
        } else {
            format!("Default runtime: {selector}")
        }
    } else {
        "Default runtime is not set".to_string()
    };

    print_output(output, &human, &response)?;

    Ok(0)
}

fn error_kind_key(kind: ErrorKind) -> &'static str {
    match kind {
        ErrorKind::Internal => "internal",
        ErrorKind::InvalidInput => "invalid-input",
        ErrorKind::UnsupportedPlatform => "unsupported-platform",
        ErrorKind::Network => "network",
        ErrorKind::NotFound => "not-found",
        ErrorKind::Conflict => "conflict",
        ErrorKind::NotImplemented => "not-implemented",
    }
}
