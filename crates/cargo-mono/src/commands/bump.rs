use std::collections::{BTreeMap, BTreeSet};

use semver::Version;
use serde::Serialize;
use tracing::info;

use crate::{
    cli::BumpArgs,
    commands::{print_output, targeting},
    errors::Result,
    git,
    types::{BumpLevel, OutputFormat},
    versioning, CargoMonoApp,
};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
enum BumpSource {
    Selected,
    Dependent,
}

impl BumpSource {
    fn as_str(self) -> &'static str {
        match self {
            Self::Selected => "selected",
            Self::Dependent => "dependent",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
enum BumpSkipReason {
    NonPublishable,
}

impl BumpSkipReason {
    fn as_str(self) -> &'static str {
        match self {
            Self::NonPublishable => "non-publishable",
        }
    }
}

#[derive(Debug, Serialize)]
struct BumpedPackage {
    name: String,
    previous_version: String,
    new_version: String,
    source: BumpSource,
}

#[derive(Debug, Serialize)]
struct SkippedPackage {
    name: String,
    reason: BumpSkipReason,
}

#[derive(Debug, Serialize)]
struct BumpResult {
    workspace_root: String,
    selector: String,
    base_ref: Option<String>,
    merge_base: Option<String>,
    level: String,
    preid: Option<String>,
    bumped_packages: Vec<BumpedPackage>,
    skipped_packages: Vec<SkippedPackage>,
    dependency_updates: usize,
    updated_manifests: Vec<String>,
    commit: Option<String>,
    tags: Vec<String>,
}

pub fn execute(args: &BumpArgs, output: OutputFormat, app: &CargoMonoApp) -> Result<i32> {
    if args.level != BumpLevel::Prerelease && args.preid.is_some() {
        info!(
            command_path = "cargo-mono.bump",
            workspace_root = %app.workspace.root.display(),
            action = "validate-bump-args",
            outcome = "ignored-preid",
            "Ignoring --preid because bump level is not prerelease"
        );
    }

    let resolved = targeting::resolve_targets(&args.target, &args.changed, &app.workspace)?;
    let mut skipped_packages = BTreeMap::<String, BumpSkipReason>::new();

    let mut selected = BTreeSet::<String>::new();
    for package_name in &resolved.names {
        let Some(package) = app.workspace.package(package_name) else {
            continue;
        };

        if package.publishable {
            selected.insert(package_name.clone());
        } else {
            skipped_packages.insert(package_name.clone(), BumpSkipReason::NonPublishable);
        }
    }

    if selected.is_empty() {
        let result = BumpResult {
            workspace_root: app.workspace.root.display().to_string(),
            selector: resolved.selector.as_str().to_string(),
            base_ref: resolved.base_ref,
            merge_base: resolved.merge_base,
            level: args.level.as_str().to_string(),
            preid: args.preid.clone(),
            bumped_packages: Vec::new(),
            skipped_packages: skipped_packages
                .into_iter()
                .map(|(name, reason)| SkippedPackage { name, reason })
                .collect(),
            dependency_updates: 0,
            updated_manifests: Vec::new(),
            commit: None,
            tags: Vec::new(),
        };

        let human = "No publishable packages were selected for bump.".to_string();
        print_output(output, &human, &result)?;
        return Ok(0);
    }

    let mut previous_versions = BTreeMap::<String, Version>::new();
    let mut next_versions = BTreeMap::<String, Version>::new();
    let mut bump_sources = BTreeMap::<String, BumpSource>::new();

    for package_name in &selected {
        let package = app
            .workspace
            .package(package_name)
            .expect("validated package");
        let next = versioning::bump_version(&package.version, args.level, args.preid.as_deref())?;

        previous_versions.insert(package_name.clone(), package.version.clone());
        next_versions.insert(package_name.clone(), next);
        bump_sources.insert(package_name.clone(), BumpSource::Selected);
    }

    if args.bump_dependents {
        let dependents = app.workspace.expand_dependents(&selected);
        for dependent_name in dependents {
            if selected.contains(&dependent_name) {
                continue;
            }

            let Some(package) = app.workspace.package(&dependent_name) else {
                continue;
            };

            if !package.publishable {
                skipped_packages
                    .entry(dependent_name)
                    .or_insert(BumpSkipReason::NonPublishable);
                continue;
            }

            let next = versioning::bump_version(&package.version, BumpLevel::Patch, None)?;

            previous_versions.insert(dependent_name.clone(), package.version.clone());
            next_versions.insert(dependent_name.clone(), next);
            bump_sources.insert(dependent_name, BumpSource::Dependent);
        }
    }

    let manifest_result = versioning::apply_workspace_bump(&app.workspace, &next_versions)?;
    if manifest_result.updated_manifests.is_empty() {
        let result = BumpResult {
            workspace_root: app.workspace.root.display().to_string(),
            selector: resolved.selector.as_str().to_string(),
            base_ref: resolved.base_ref,
            merge_base: resolved.merge_base,
            level: args.level.as_str().to_string(),
            preid: args.preid.clone(),
            bumped_packages: Vec::new(),
            skipped_packages: skipped_packages
                .into_iter()
                .map(|(name, reason)| SkippedPackage { name, reason })
                .collect(),
            dependency_updates: manifest_result.dependency_updates,
            updated_manifests: Vec::new(),
            commit: None,
            tags: Vec::new(),
        };

        print_output(
            output,
            "No manifest changes were produced by the bump operation.",
            &result,
        )?;
        return Ok(0);
    }

    git::add_paths(&manifest_result.updated_manifests)?;
    let commit_message = format!("chore(release): bump {} crate(s)", next_versions.len());
    let commit = git::commit_paths(&commit_message, &manifest_result.updated_manifests)?;

    let mut tags = Vec::with_capacity(next_versions.len());
    for (package_name, new_version) in &next_versions {
        let tag = format!("{package_name}-v{new_version}");
        git::create_tag(&tag)?;
        tags.push(tag);
    }

    let bumped_packages = next_versions
        .iter()
        .map(|(name, next_version)| BumpedPackage {
            name: name.clone(),
            previous_version: previous_versions
                .get(name)
                .expect("tracked previous version")
                .to_string(),
            new_version: next_version.to_string(),
            source: *bump_sources.get(name).expect("tracked bump source"),
        })
        .collect::<Vec<_>>();

    for package in &bumped_packages {
        info!(
            command_path = "cargo-mono.bump",
            workspace_root = %app.workspace.root.display(),
            package = %package.name,
            action = "bump-package",
            outcome = "updated",
            source = package.source.as_str(),
            "Applied package bump"
        );
    }

    let result = BumpResult {
        workspace_root: app.workspace.root.display().to_string(),
        selector: resolved.selector.as_str().to_string(),
        base_ref: resolved.base_ref,
        merge_base: resolved.merge_base,
        level: args.level.as_str().to_string(),
        preid: args.preid.clone(),
        bumped_packages,
        skipped_packages: skipped_packages
            .into_iter()
            .map(|(name, reason)| SkippedPackage { name, reason })
            .collect(),
        dependency_updates: manifest_result.dependency_updates,
        updated_manifests: manifest_result
            .updated_manifests
            .iter()
            .map(|path| path.display().to_string())
            .collect(),
        commit: Some(commit.clone()),
        tags,
    };

    info!(
        command_path = "cargo-mono.bump",
        workspace_root = %result.workspace_root,
        git_ref = %commit,
        action = "bump-release",
        outcome = "success",
        package_count = result.bumped_packages.len(),
        "Completed bump release operation"
    );

    let mut human_lines = vec![format!(
        "Bumped {} package(s); commit {}.",
        result.bumped_packages.len(),
        commit
    )];

    for package in &result.bumped_packages {
        human_lines.push(format!(
            "- {}: {} -> {} ({})",
            package.name,
            package.previous_version,
            package.new_version,
            package.source.as_str()
        ));
    }

    for skipped in &result.skipped_packages {
        human_lines.push(format!(
            "- skipped {} ({})",
            skipped.name,
            skipped.reason.as_str()
        ));
    }

    print_output(output, &human_lines.join("\n"), &result)?;
    Ok(0)
}
