use serde::Serialize;

use crate::{
    cli::{OutputFormat, ShowCommand},
    commands::print_output,
    errors::Result,
    NodeupApp,
};

#[derive(Debug, Serialize)]
struct ActiveRuntimeResponse {
    runtime: String,
    source: String,
    selector: String,
}

#[derive(Debug, Serialize)]
struct HomeResponse {
    data_root: String,
    cache_root: String,
    config_root: String,
}

pub fn execute(command: ShowCommand, output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    match command {
        ShowCommand::ActiveRuntime => show_active_runtime(output, app),
        ShowCommand::Home => show_home(output, app),
    }
}

fn show_active_runtime(output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    let cwd = std::env::current_dir()?;
    let resolved = app.resolver.resolve_with_precedence(None, &cwd)?;
    let response = ActiveRuntimeResponse {
        runtime: resolved.runtime_id(),
        source: format!("{:?}", resolved.source).to_lowercase(),
        selector: resolved.selector.stable_id(),
    };
    let human = format!("Active runtime: {}", response.runtime);

    print_output(output, &human, &response)?;
    Ok(0)
}

fn show_home(output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    let response = HomeResponse {
        data_root: app.paths.data_root.to_string_lossy().to_string(),
        cache_root: app.paths.cache_root.to_string_lossy().to_string(),
        config_root: app.paths.config_root.to_string_lossy().to_string(),
    };
    let human = format!("nodeup home: {}", response.data_root);

    print_output(output, &human, &response)?;
    Ok(0)
}
