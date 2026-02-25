use serde::Serialize;
use tracing::info;

use crate::{commands::print_output, errors::Result, types::OutputFormat, CargoMonoApp};

#[derive(Debug, Serialize)]
struct ListPackage {
    name: String,
    version: String,
    manifest_path: String,
    publishable: bool,
}

#[derive(Debug, Serialize)]
struct ListResult {
    workspace_root: String,
    packages: Vec<ListPackage>,
}

pub fn execute(output: OutputFormat, app: &CargoMonoApp) -> Result<i32> {
    let packages = app
        .workspace
        .packages()
        .map(|package| ListPackage {
            name: package.name.clone(),
            version: package.version.to_string(),
            manifest_path: package.manifest_relative_path.display().to_string(),
            publishable: package.publishable,
        })
        .collect::<Vec<_>>();

    let result = ListResult {
        workspace_root: app.workspace.root.display().to_string(),
        packages,
    };

    info!(
        command_path = "cargo-mono.list",
        workspace_root = %result.workspace_root,
        package_count = result.packages.len(),
        action = "list-packages",
        outcome = "success",
        "Listed workspace packages"
    );

    let human_lines = if result.packages.is_empty() {
        vec!["No workspace packages found.".to_string()]
    } else {
        let mut lines = Vec::with_capacity(result.packages.len() + 1);
        lines.push(format!("Workspace packages: {}", result.packages.len()));

        for package in &result.packages {
            let publishable = if package.publishable {
                "publishable"
            } else {
                "non-publishable"
            };
            lines.push(format!(
                "- {} {} ({publishable}) [{}]",
                package.name, package.version, package.manifest_path
            ));
        }

        lines
    };

    print_output(output, &human_lines.join("\n"), &result)?;
    Ok(0)
}
