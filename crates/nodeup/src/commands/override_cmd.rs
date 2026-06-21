use std::path::PathBuf;

use serde::Serialize;
use tracing::info;

use crate::{
    cli::{OutputColorMode, OutputFormat, OverrideCommand},
    commands::print_output,
    errors::Result,
    selectors::RuntimeSelector,
    NodeupApp,
};

#[derive(Debug, Serialize)]
struct OverrideListItem {
    path: String,
    selector: String,
    selector_kind: String,
    canonical_selector: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    selector_alias_of: Option<String>,
}

#[derive(Debug, Serialize)]
struct OverrideSetResponse {
    path: PathBuf,
    selector: String,
    selector_kind: String,
    canonical_selector: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    selector_alias_of: Option<String>,
    status: &'static str,
}

pub fn execute(
    command: OverrideCommand,
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    match command {
        OverrideCommand::List => list(output, color, app),
        OverrideCommand::Set { runtime, path } => {
            set(&runtime, path.as_deref(), output, color, app)
        }
        OverrideCommand::Unset { path, nonexistent } => {
            unset(path.as_deref(), nonexistent, output, color, app)
        }
    }
}

fn list(output: OutputFormat, color: Option<OutputColorMode>, app: &NodeupApp) -> Result<i32> {
    let entries = app
        .overrides
        .list()?
        .into_iter()
        .map(|entry| {
            let selector = RuntimeSelector::parse(&entry.selector)?;
            Ok(OverrideListItem {
                path: entry.path,
                selector: entry.selector,
                selector_kind: selector.kind().as_str().to_string(),
                canonical_selector: selector.canonical_id(),
                selector_alias_of: selector.alias_of(),
            })
        })
        .collect::<Result<Vec<_>>>()?;

    let human = format!("Configured overrides: {}", entries.len());
    print_output(output, color, &human, &entries)?;
    Ok(0)
}

fn set(
    runtime: &str,
    path: Option<&str>,
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    let target_path = match path {
        Some(path) => PathBuf::from(path),
        None => std::env::current_dir()?,
    };

    let selector = RuntimeSelector::parse(runtime)?;
    let canonical_selector = selector.stable_id();

    app.overrides.set(&target_path, &canonical_selector)?;
    app.store.track_selector(&canonical_selector)?;

    info!(
        command_path = "nodeup.override.set",
        path = %target_path.display(),
        selector_input = %runtime,
        selector_canonical = %canonical_selector,
        "Configured runtime override"
    );

    let human = format!(
        "Override set: {} -> {}",
        target_path.display(),
        canonical_selector
    );
    let response = OverrideSetResponse {
        path: target_path,
        selector: canonical_selector,
        selector_kind: selector.kind().as_str().to_string(),
        canonical_selector: selector.canonical_id(),
        selector_alias_of: selector.alias_of(),
        status: "set",
    };

    print_output(output, color, &human, &response)?;
    Ok(0)
}

fn unset(
    path: Option<&str>,
    nonexistent: bool,
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    let path = path.map(PathBuf::from);
    let removed = app.overrides.unset(path.as_deref(), nonexistent)?;
    let human = format!("Removed {} override(s)", removed.len());
    print_output(output, color, &human, &removed)?;
    Ok(0)
}
