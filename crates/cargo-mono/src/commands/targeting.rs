use std::collections::BTreeSet;

use crate::{
    cli::{ChangedArgs, TargetArgs},
    errors::{CargoMonoError, ErrorKind, Result},
    git,
    types::TargetSelector,
    workspace::Workspace,
};

#[derive(Debug, Clone)]
pub struct ResolvedTargets {
    pub selector: TargetSelector,
    pub names: BTreeSet<String>,
    pub base_ref: Option<String>,
    pub merge_base: Option<String>,
}

pub fn resolve_targets(
    target: &TargetArgs,
    changed: &ChangedArgs,
    workspace: &Workspace,
) -> Result<ResolvedTargets> {
    if target.changed {
        let changed_files = git::changed_files(&changed.base, changed.include_uncommitted)?;
        let names = workspace.changed_packages_with_filters(
            &changed_files.paths,
            !changed.direct_only,
            &changed.include_path,
            &changed.exclude_path,
        )?;

        return Ok(ResolvedTargets {
            selector: TargetSelector::Changed,
            names,
            base_ref: Some(changed.base.clone()),
            merge_base: Some(changed_files.merge_base),
        });
    }

    if !target.package.is_empty() {
        let names = target.package.iter().cloned().collect::<BTreeSet<String>>();

        let missing = names
            .iter()
            .filter(|name| workspace.package(name).is_none())
            .cloned()
            .collect::<Vec<_>>();

        if !missing.is_empty() {
            let requested = names.iter().cloned().collect::<Vec<_>>();
            return Err(CargoMonoError::with_details(
                ErrorKind::InvalidInput,
                "Unknown package selector(s).",
                vec![
                    ("requested_packages", requested.join(",")),
                    ("missing_packages", missing.join(",")),
                    ("selected_count", requested.len().to_string()),
                    (
                        "workspace_package_count",
                        workspace.all_package_names().len().to_string(),
                    ),
                ],
                "Run `cargo mono list` to view valid workspace package names.",
            ));
        }

        return Ok(ResolvedTargets {
            selector: TargetSelector::Package,
            names,
            base_ref: None,
            merge_base: None,
        });
    }

    let names = workspace.all_package_names();
    let selector = if target.all {
        TargetSelector::All
    } else {
        // `--all` is the default behavior when no selector is provided.
        TargetSelector::All
    };

    Ok(ResolvedTargets {
        selector,
        names,
        base_ref: None,
        merge_base: None,
    })
}
