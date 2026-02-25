use serde::Serialize;
use tracing::info;

use crate::{
    cli::ChangedArgs, commands::print_output, errors::Result, git, types::OutputFormat,
    CargoMonoApp,
};

#[derive(Debug, Serialize)]
struct ChangedResult {
    workspace_root: String,
    base_ref: String,
    merge_base: String,
    include_uncommitted: bool,
    direct_only: bool,
    files: Vec<String>,
    packages: Vec<String>,
}

pub fn execute(args: &ChangedArgs, output: OutputFormat, app: &CargoMonoApp) -> Result<i32> {
    let changed_files = git::changed_files(&args.base, args.include_uncommitted)?;
    let changed_packages = app
        .workspace
        .changed_packages(&changed_files.paths, !args.direct_only)
        .into_iter()
        .collect::<Vec<_>>();

    let result = ChangedResult {
        workspace_root: app.workspace.root.display().to_string(),
        base_ref: args.base.clone(),
        merge_base: changed_files.merge_base.clone(),
        include_uncommitted: args.include_uncommitted,
        direct_only: args.direct_only,
        files: changed_files
            .paths
            .iter()
            .map(|path| path.display().to_string())
            .collect(),
        packages: changed_packages,
    };

    info!(
        command_path = "cargo-mono.changed",
        workspace_root = %result.workspace_root,
        base_ref = %result.base_ref,
        git_ref = %result.merge_base,
        package_count = result.packages.len(),
        action = "resolve-changed-packages",
        outcome = "success",
        "Resolved changed packages"
    );

    let human = if result.packages.is_empty() {
        format!(
            "No changed packages found (base={}, merge-base={}).",
            result.base_ref, result.merge_base
        )
    } else {
        let mut lines = vec![format!(
            "Changed packages: {} (base={}, merge-base={})",
            result.packages.len(),
            result.base_ref,
            result.merge_base
        )];

        for package in &result.packages {
            lines.push(format!("- {package}"));
        }

        lines.join("\n")
    };

    print_output(output, &human, &result)?;
    Ok(0)
}
