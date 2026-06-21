use serde::Serialize;
use tracing::{info, warn};

use crate::{
    cli::{OutputColorMode, OutputFormat},
    commands::print_output,
    errors::{ErrorKind, NodeupError, Result},
    release_index::ReleaseIndexResolutionDiagnostic,
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
    #[serde(skip_serializing_if = "Option::is_none")]
    release_index: Option<ReleaseIndexResolutionDiagnostic>,
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

pub fn execute(
    runtime: Option<&str>,
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
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
            release_index: app.resolver.release_index_diagnostic(),
            resolution_error: None,
        };
        let human = append_release_index_human_note(
            format!(
                "Default runtime set to {}",
                response.default_selector.as_deref().unwrap_or("")
            ),
            response.release_index.as_ref(),
        );
        print_output(output, color, &human, &response)?;
        return Ok(0);
    }

    let settings = app.store.load_settings()?;
    let (resolved_runtime, release_index, resolution_error) =
        if let Some(selector) = settings.default_selector.as_ref() {
            match app
                .resolver
                .resolve_selector_with_source(selector, RuntimeSelectorSource::Default)
            {
                Ok(resolved) => (
                    Some(resolved.runtime_id()),
                    app.resolver.release_index_diagnostic(),
                    None,
                ),
                Err(error) => {
                    warn!(
                        command_path = "nodeup.default",
                        selector = %selector,
                        error_kind = error_kind_key(error.kind),
                        error = %error.message,
                        outcome = "unresolved",
                        "Default selector resolution failed during introspection"
                    );
                    (None, None, Some(DefaultResolutionError::from(error)))
                }
            }
        } else {
            (None, None, None)
        };

    let default_selector = settings.default_selector;
    let response = DefaultResponse {
        default_selector: default_selector.clone(),
        resolved_runtime,
        release_index,
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
    let human = append_release_index_human_note(human, response.release_index.as_ref());

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
