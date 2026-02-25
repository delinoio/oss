use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    path::PathBuf,
};

use semver::{Prerelease, Version};
use toml_edit::{value, DocumentMut, Item, Value};

use crate::{
    errors::{CargoMonoError, Result},
    types::BumpLevel,
    workspace::Workspace,
};

const DEPENDENCY_SECTION_KEYS: [&str; 3] =
    ["dependencies", "dev-dependencies", "build-dependencies"];

#[derive(Debug, Clone)]
pub struct ManifestUpdateResult {
    pub updated_manifests: BTreeSet<PathBuf>,
    pub dependency_updates: usize,
}

pub fn bump_version(current: &Version, level: BumpLevel, preid: Option<&str>) -> Result<Version> {
    let mut next = current.clone();

    match level {
        BumpLevel::Major => {
            next.major += 1;
            next.minor = 0;
            next.patch = 0;
            next.pre = Prerelease::EMPTY;
            next.build = semver::BuildMetadata::EMPTY;
        }
        BumpLevel::Minor => {
            next.minor += 1;
            next.patch = 0;
            next.pre = Prerelease::EMPTY;
            next.build = semver::BuildMetadata::EMPTY;
        }
        BumpLevel::Patch => {
            next.patch += 1;
            next.pre = Prerelease::EMPTY;
            next.build = semver::BuildMetadata::EMPTY;
        }
        BumpLevel::Prerelease => {
            let preid = preid.ok_or_else(|| {
                CargoMonoError::invalid_input("--preid is required when --level prerelease")
            })?;

            let next_pre = next_prerelease(current, preid)?;
            if current.pre.is_empty() || !current.pre.as_str().starts_with(preid) {
                next.patch += 1;
                next.pre = next_pre;
            } else {
                next.pre = next_pre;
            }
            next.build = semver::BuildMetadata::EMPTY;
        }
    }

    Ok(next)
}

pub fn apply_workspace_bump(
    workspace: &Workspace,
    bumped_versions: &BTreeMap<String, Version>,
) -> Result<ManifestUpdateResult> {
    let mut updated_manifests = BTreeSet::new();
    let mut dependency_updates = 0usize;

    for package in workspace.packages() {
        let content = fs::read_to_string(&package.manifest_path)?;
        let mut document = content.parse::<DocumentMut>()?;

        let mut changed = false;
        if let Some(new_version) = bumped_versions.get(&package.name) {
            changed |= update_package_version(&mut document, new_version);
        }

        let updates_in_manifest = update_dependency_versions(&mut document, bumped_versions);
        if updates_in_manifest > 0 {
            changed = true;
            dependency_updates += updates_in_manifest;
        }

        if changed {
            fs::write(&package.manifest_path, document.to_string())?;
            updated_manifests.insert(package.manifest_relative_path.clone());
        }
    }

    Ok(ManifestUpdateResult {
        updated_manifests,
        dependency_updates,
    })
}

fn update_package_version(document: &mut DocumentMut, new_version: &Version) -> bool {
    let Some(package_item) = document.get_mut("package") else {
        return false;
    };
    let Some(package_table) = package_item.as_table_mut() else {
        return false;
    };

    let new_value = new_version.to_string();
    let current_value = package_table
        .get("version")
        .and_then(Item::as_value)
        .and_then(Value::as_str);

    if current_value == Some(new_value.as_str()) {
        return false;
    }

    package_table["version"] = value(new_value);
    true
}

fn update_dependency_versions(
    document: &mut DocumentMut,
    bumped_versions: &BTreeMap<String, Version>,
) -> usize {
    let mut updates = 0usize;

    for section in DEPENDENCY_SECTION_KEYS {
        if let Some(section_item) = document.get_mut(section) {
            updates += update_dependency_section(section_item, bumped_versions);
        }
    }

    if let Some(workspace_item) = document.get_mut("workspace") {
        if let Some(workspace_table) = workspace_item.as_table_mut() {
            if let Some(workspace_deps) = workspace_table.get_mut("dependencies") {
                updates += update_dependency_section(workspace_deps, bumped_versions);
            }
        }
    }

    if let Some(target_item) = document.get_mut("target") {
        if let Some(targets) = target_item.as_table_mut() {
            for (_, target_config_item) in targets.iter_mut() {
                let Some(target_table) = target_config_item.as_table_mut() else {
                    continue;
                };

                for section in DEPENDENCY_SECTION_KEYS {
                    if let Some(section_item) = target_table.get_mut(section) {
                        updates += update_dependency_section(section_item, bumped_versions);
                    }
                }
            }
        }
    }

    updates
}

fn update_dependency_section(
    section_item: &mut Item,
    bumped_versions: &BTreeMap<String, Version>,
) -> usize {
    let Some(section_table) = section_item.as_table_mut() else {
        return 0;
    };

    let mut updates = 0usize;

    for (dependency_name, dependency_item) in section_table.iter_mut() {
        let Some(new_version) = bumped_versions.get(dependency_name.get()) else {
            continue;
        };

        if update_dependency_item(dependency_item, new_version) {
            updates += 1;
        }
    }

    updates
}

fn update_dependency_item(dependency_item: &mut Item, new_version: &Version) -> bool {
    let new_version = new_version.to_string();

    if let Some(value_item) = dependency_item.as_value_mut() {
        match value_item {
            Value::String(existing) => {
                if existing.value() == new_version.as_str() {
                    return false;
                }

                *value_item = Value::from(new_version);
                return true;
            }
            Value::InlineTable(inline_table) => {
                if inline_table.get("workspace").and_then(Value::as_bool) == Some(true) {
                    return false;
                }

                let current = inline_table.get("version").and_then(Value::as_str);
                if current == Some(new_version.as_str()) {
                    return false;
                }

                inline_table.insert("version", Value::from(new_version));
                return true;
            }
            _ => return false,
        }
    }

    let Some(table_item) = dependency_item.as_table_mut() else {
        return false;
    };

    if table_item
        .get("workspace")
        .and_then(Item::as_value)
        .and_then(Value::as_bool)
        == Some(true)
    {
        return false;
    }

    let current = table_item
        .get("version")
        .and_then(Item::as_value)
        .and_then(Value::as_str);
    if current == Some(new_version.as_str()) {
        return false;
    }

    table_item["version"] = value(new_version);
    true
}

fn next_prerelease(current: &Version, preid: &str) -> Result<Prerelease> {
    if current.pre.is_empty() {
        return Prerelease::new(&format!("{preid}.1")).map_err(Into::into);
    }

    let raw = current.pre.as_str();
    if !raw.starts_with(preid) {
        return Prerelease::new(&format!("{preid}.1")).map_err(Into::into);
    }

    let suffix = raw.strip_prefix(preid).unwrap_or_default();
    if let Some(number_part) = suffix.strip_prefix('.') {
        if let Ok(number) = number_part.parse::<u64>() {
            return Prerelease::new(&format!("{preid}.{}", number + 1)).map_err(Into::into);
        }
    }

    Prerelease::new(&format!("{preid}.1")).map_err(Into::into)
}

#[cfg(test)]
mod tests {
    use std::{
        collections::{BTreeMap, BTreeSet},
        fs,
        path::Path,
    };

    use tempfile::tempdir;

    use super::*;
    use crate::workspace::WorkspacePackage;

    #[test]
    fn bump_major_resets_minor_and_patch() {
        let current = Version::parse("1.2.3").unwrap();
        let next = bump_version(&current, BumpLevel::Major, None).unwrap();

        assert_eq!(next, Version::parse("2.0.0").unwrap());
    }

    #[test]
    fn bump_minor_resets_patch() {
        let current = Version::parse("1.2.3").unwrap();
        let next = bump_version(&current, BumpLevel::Minor, None).unwrap();

        assert_eq!(next, Version::parse("1.3.0").unwrap());
    }

    #[test]
    fn bump_patch_increments_patch() {
        let current = Version::parse("1.2.3").unwrap();
        let next = bump_version(&current, BumpLevel::Patch, None).unwrap();

        assert_eq!(next, Version::parse("1.2.4").unwrap());
    }

    #[test]
    fn bump_prerelease_requires_preid() {
        let current = Version::parse("1.2.3").unwrap();
        let error = bump_version(&current, BumpLevel::Prerelease, None).unwrap_err();

        assert_eq!(error.kind, crate::errors::ErrorKind::InvalidInput);
    }

    #[test]
    fn bump_prerelease_from_release_increments_patch() {
        let current = Version::parse("1.2.3").unwrap();
        let next = bump_version(&current, BumpLevel::Prerelease, Some("rc")).unwrap();

        assert_eq!(next, Version::parse("1.2.4-rc.1").unwrap());
    }

    #[test]
    fn bump_prerelease_same_identifier_increments_suffix() {
        let current = Version::parse("1.2.3-rc.7").unwrap();
        let next = bump_version(&current, BumpLevel::Prerelease, Some("rc")).unwrap();

        assert_eq!(next, Version::parse("1.2.3-rc.8").unwrap());
    }

    #[test]
    fn apply_workspace_bump_updates_package_and_internal_dependency_versions() {
        let temp_dir = tempdir().unwrap();
        let root = temp_dir.path();

        let alpha_dir = root.join("crates/alpha");
        let beta_dir = root.join("crates/beta");
        fs::create_dir_all(&alpha_dir).unwrap();
        fs::create_dir_all(&beta_dir).unwrap();

        let alpha_manifest = alpha_dir.join("Cargo.toml");
        let beta_manifest = beta_dir.join("Cargo.toml");

        fs::write(
            &alpha_manifest,
            r#"[package]
name = "alpha"
version = "0.1.0"

[dependencies]
"#,
        )
        .unwrap();

        fs::write(
            &beta_manifest,
            r#"[package]
name = "beta"
version = "0.5.0"

[dependencies]
alpha = { path = "../alpha", version = "0.1.0" }
"#,
        )
        .unwrap();

        let workspace = workspace_fixture(root, vec![("alpha", "0.1.0"), ("beta", "0.5.0")]);

        let bumped_versions =
            BTreeMap::from([("alpha".to_string(), Version::parse("0.2.0").unwrap())]);

        let result = apply_workspace_bump(&workspace, &bumped_versions).unwrap();

        assert_eq!(
            result.updated_manifests,
            BTreeSet::from([
                PathBuf::from("crates/alpha/Cargo.toml"),
                PathBuf::from("crates/beta/Cargo.toml")
            ])
        );
        assert_eq!(result.dependency_updates, 1);

        let alpha_content = fs::read_to_string(alpha_manifest).unwrap();
        assert!(alpha_content.contains("version = \"0.2.0\""));

        let beta_content = fs::read_to_string(beta_manifest).unwrap();
        assert!(beta_content.contains("alpha = { path = \"../alpha\", version = \"0.2.0\" }"));
    }

    #[test]
    fn apply_workspace_bump_skips_workspace_true_dependencies() {
        let temp_dir = tempdir().unwrap();
        let root = temp_dir.path();

        let alpha_dir = root.join("crates/alpha");
        let beta_dir = root.join("crates/beta");
        fs::create_dir_all(&alpha_dir).unwrap();
        fs::create_dir_all(&beta_dir).unwrap();

        let alpha_manifest = alpha_dir.join("Cargo.toml");
        let beta_manifest = beta_dir.join("Cargo.toml");

        fs::write(
            &alpha_manifest,
            r#"[package]
name = "alpha"
version = "0.1.0"
"#,
        )
        .unwrap();

        fs::write(
            &beta_manifest,
            r#"[package]
name = "beta"
version = "0.5.0"

[dependencies]
alpha = { workspace = true }
"#,
        )
        .unwrap();

        let workspace = workspace_fixture(root, vec![("alpha", "0.1.0"), ("beta", "0.5.0")]);

        let bumped_versions =
            BTreeMap::from([("alpha".to_string(), Version::parse("0.2.0").unwrap())]);

        let result = apply_workspace_bump(&workspace, &bumped_versions).unwrap();
        assert_eq!(result.dependency_updates, 0);

        let beta_content = fs::read_to_string(beta_manifest).unwrap();
        assert!(beta_content.contains("alpha = { workspace = true }"));
    }

    fn workspace_fixture(root: &Path, versions: Vec<(&str, &str)>) -> Workspace {
        let packages = versions
            .into_iter()
            .map(|(name, version)| {
                let directory_relative_path = PathBuf::from(format!("crates/{name}"));
                let manifest_relative_path = directory_relative_path.join("Cargo.toml");
                (
                    name.to_string(),
                    WorkspacePackage {
                        name: name.to_string(),
                        version: Version::parse(version).unwrap(),
                        manifest_path: root.join(&manifest_relative_path),
                        manifest_relative_path,
                        directory: root.join(&directory_relative_path),
                        directory_relative_path,
                        publishable: true,
                    },
                )
            })
            .collect::<BTreeMap<_, _>>();

        Workspace::from_parts(
            root.to_path_buf(),
            packages,
            BTreeMap::new(),
            BTreeMap::new(),
        )
    }
}
