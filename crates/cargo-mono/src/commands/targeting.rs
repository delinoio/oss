use std::collections::BTreeSet;

use crate::{
    cli::{ChangedArgs, TargetArgs},
    errors::{CargoMonoError, Result},
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
        let names = workspace.changed_packages(&changed_files.paths, !changed.direct_only);

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
            return Err(CargoMonoError::invalid_input(format!(
                "Unknown package(s): {}",
                missing.join(", ")
            )));
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
