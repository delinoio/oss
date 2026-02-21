use std::path::PathBuf;

use serde::Serialize;
use tracing::info;

use crate::{
    cli::{OutputFormat, OverrideCommand},
    commands::print_output,
    errors::Result,
    selectors::RuntimeSelector,
    NodeupApp,
};

#[derive(Debug, Serialize)]
struct OverrideListItem {
    path: String,
    selector: String,
}

pub fn execute(command: OverrideCommand, output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    match command {
        OverrideCommand::List => list(output, app),
        OverrideCommand::Set { runtime, path } => set(&runtime, path.as_deref(), output, app),
        OverrideCommand::Unset { path, nonexistent } => {
            unset(path.as_deref(), nonexistent, output, app)
        }
    }
}

fn list(output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    let entries = app
        .overrides
        .list()?
        .into_iter()
        .map(|entry| OverrideListItem {
            path: entry.path,
            selector: entry.selector,
        })
        .collect::<Vec<_>>();

    let human = format!("Configured overrides: {}", entries.len());
    print_output(output, &human, &entries)?;
    Ok(0)
}

fn set(runtime: &str, path: Option<&str>, output: OutputFormat, app: &NodeupApp) -> Result<i32> {
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
    let response = serde_json::json!({
        "path": target_path,
        "selector": canonical_selector,
        "status": "set"
    });

    print_output(output, &human, &response)?;
    Ok(0)
}

fn unset(
    path: Option<&str>,
    nonexistent: bool,
    output: OutputFormat,
    app: &NodeupApp,
) -> Result<i32> {
    let path = path.map(PathBuf::from);
    let removed = app.overrides.unset(path.as_deref(), nonexistent)?;
    let human = format!("Removed {} override(s)", removed.len());
    print_output(output, &human, &removed)?;
    Ok(0)
}
