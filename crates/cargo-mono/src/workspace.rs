use std::{
    collections::{BTreeMap, BTreeSet, HashMap},
    path::{Path, PathBuf},
};

use cargo_metadata::{MetadataCommand, PackageId};
use semver::Version;
use serde::Serialize;

use crate::errors::{CargoMonoError, Result};

pub const GLOBAL_IMPACT_FILES: [&str; 3] = ["Cargo.toml", "Cargo.lock", "rust-toolchain"];

#[derive(Debug, Clone, Serialize)]
pub struct WorkspacePackage {
    pub name: String,
    pub version: Version,
    pub manifest_path: PathBuf,
    pub manifest_relative_path: PathBuf,
    pub directory: PathBuf,
    pub directory_relative_path: PathBuf,
    pub publishable: bool,
}

#[derive(Debug, Clone)]
pub struct Workspace {
    pub root: PathBuf,
    packages: BTreeMap<String, WorkspacePackage>,
    dependencies: BTreeMap<String, BTreeSet<String>>,
    dependents: BTreeMap<String, BTreeSet<String>>,
}

impl Workspace {
    pub fn load() -> Result<Self> {
        let metadata = MetadataCommand::new().exec()?;
        let root = metadata.workspace_root.as_std_path().to_path_buf();

        let workspace_members = metadata
            .workspace_members
            .iter()
            .cloned()
            .collect::<BTreeSet<_>>();

        let mut id_to_name = HashMap::<PackageId, String>::new();
        let mut packages = BTreeMap::<String, WorkspacePackage>::new();

        for package in metadata
            .packages
            .iter()
            .filter(|pkg| workspace_members.contains(&pkg.id))
        {
            let manifest_path = package.manifest_path.as_std_path().to_path_buf();
            let manifest_relative_path = manifest_path
                .strip_prefix(&root)
                .map(Path::to_path_buf)
                .map_err(|error| {
                CargoMonoError::internal(format!(
                    "Workspace manifest is outside workspace root: {} ({error})",
                    manifest_path.display()
                ))
            })?;
            let directory = manifest_path
                .parent()
                .ok_or_else(|| {
                    CargoMonoError::internal(format!(
                        "Failed to resolve package directory from manifest path: {}",
                        manifest_path.display()
                    ))
                })?
                .to_path_buf();
            let directory_relative_path = directory
                .strip_prefix(&root)
                .map(Path::to_path_buf)
                .map_err(|error| {
                    CargoMonoError::internal(format!(
                        "Workspace package directory is outside workspace root: {} ({error})",
                        directory.display()
                    ))
                })?;

            let publishable = package
                .publish
                .as_ref()
                .map_or(true, |registries| !registries.is_empty());

            let entry = WorkspacePackage {
                name: package.name.clone(),
                version: package.version.clone(),
                manifest_path,
                manifest_relative_path,
                directory,
                directory_relative_path,
                publishable,
            };

            id_to_name.insert(package.id.clone(), package.name.clone());
            packages.insert(package.name.clone(), entry);
        }

        let mut dependencies = packages
            .keys()
            .map(|name| (name.clone(), BTreeSet::new()))
            .collect::<BTreeMap<_, _>>();
        let mut dependents = packages
            .keys()
            .map(|name| (name.clone(), BTreeSet::new()))
            .collect::<BTreeMap<_, _>>();

        if let Some(resolve) = metadata.resolve {
            for node in resolve.nodes {
                let Some(node_name) = id_to_name.get(&node.id) else {
                    continue;
                };

                for dependency in node.deps {
                    let Some(dependency_name) = id_to_name.get(&dependency.pkg) else {
                        continue;
                    };

                    dependencies
                        .entry(node_name.clone())
                        .or_default()
                        .insert(dependency_name.clone());
                    dependents
                        .entry(dependency_name.clone())
                        .or_default()
                        .insert(node_name.clone());
                }
            }
        }

        Ok(Self {
            root,
            packages,
            dependencies,
            dependents,
        })
    }

    pub fn all_package_names(&self) -> BTreeSet<String> {
        self.packages.keys().cloned().collect()
    }

    pub fn package(&self, name: &str) -> Option<&WorkspacePackage> {
        self.packages.get(name)
    }

    pub fn packages<'a>(&'a self) -> impl Iterator<Item = &'a WorkspacePackage> + 'a {
        self.packages.values()
    }

    pub fn changed_packages(
        &self,
        changed_paths: &BTreeSet<PathBuf>,
        include_dependents: bool,
    ) -> BTreeSet<String> {
        if changed_paths
            .iter()
            .any(|path| self.is_global_impact_path(path))
        {
            return self.all_package_names();
        }

        let mut direct_matches = BTreeSet::new();

        for raw_path in changed_paths {
            let Some(relative_path) = self.normalize_relative_path(raw_path) else {
                continue;
            };

            for (name, package) in &self.packages {
                if relative_path.starts_with(&package.directory_relative_path) {
                    direct_matches.insert(name.clone());
                }
            }
        }

        if include_dependents {
            return self.expand_dependents(&direct_matches);
        }

        direct_matches
    }

    pub fn expand_dependents(&self, names: &BTreeSet<String>) -> BTreeSet<String> {
        let mut expanded = names.clone();
        let mut queue = names.iter().cloned().collect::<Vec<_>>();

        while let Some(current) = queue.pop() {
            let Some(next_dependents) = self.dependents.get(&current) else {
                continue;
            };

            for dependent in next_dependents {
                if expanded.insert(dependent.clone()) {
                    queue.push(dependent.clone());
                }
            }
        }

        expanded
    }

    pub fn topological_order(&self, selected: &BTreeSet<String>) -> Result<Vec<String>> {
        let mut indegree = selected
            .iter()
            .map(|name| {
                let count = self
                    .dependencies
                    .get(name)
                    .map_or(0usize, |deps| deps.intersection(selected).count());
                (name.clone(), count)
            })
            .collect::<BTreeMap<_, _>>();

        let mut ready = indegree
            .iter()
            .filter_map(|(name, degree)| {
                if *degree == 0 {
                    Some(name.clone())
                } else {
                    None
                }
            })
            .collect::<BTreeSet<_>>();

        let mut ordered = Vec::with_capacity(selected.len());

        while let Some(name) = ready.first().cloned() {
            ready.remove(&name);
            ordered.push(name.clone());

            if let Some(next) = self.dependents.get(&name) {
                for dependent in next {
                    if !selected.contains(dependent) {
                        continue;
                    }

                    let Some(degree) = indegree.get_mut(dependent) else {
                        continue;
                    };

                    if *degree > 0 {
                        *degree -= 1;
                        if *degree == 0 {
                            ready.insert(dependent.clone());
                        }
                    }
                }
            }
        }

        if ordered.len() != selected.len() {
            return Err(CargoMonoError::conflict(
                "Failed to build package order due to dependency cycle",
            ));
        }

        Ok(ordered)
    }

    fn normalize_relative_path(&self, path: &Path) -> Option<PathBuf> {
        if path.is_absolute() {
            return path.strip_prefix(&self.root).ok().map(Path::to_path_buf);
        }

        if let Ok(without_prefix) = path.strip_prefix("./") {
            return Some(without_prefix.to_path_buf());
        }

        Some(path.to_path_buf())
    }

    fn is_global_impact_path(&self, path: &Path) -> bool {
        let Some(relative) = self.normalize_relative_path(path) else {
            return false;
        };

        GLOBAL_IMPACT_FILES
            .iter()
            .any(|global| relative == Path::new(global))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn package(name: &str, root: &Path) -> WorkspacePackage {
        let directory_relative_path = PathBuf::from(format!("crates/{name}"));
        let manifest_relative_path = directory_relative_path.join("Cargo.toml");

        WorkspacePackage {
            name: name.to_string(),
            version: Version::new(0, 1, 0),
            manifest_path: root.join(&manifest_relative_path),
            manifest_relative_path,
            directory: root.join(&directory_relative_path),
            directory_relative_path,
            publishable: true,
        }
    }

    fn fixture_workspace() -> Workspace {
        let root = PathBuf::from("/repo");
        let packages = ["app", "cli", "core"]
            .into_iter()
            .map(|name| (name.to_string(), package(name, &root)))
            .collect::<BTreeMap<_, _>>();

        let mut dependencies = BTreeMap::<String, BTreeSet<String>>::new();
        dependencies.insert("app".to_string(), BTreeSet::from(["core".to_string()]));
        dependencies.insert("cli".to_string(), BTreeSet::from(["core".to_string()]));
        dependencies.insert("core".to_string(), BTreeSet::new());

        let mut dependents = BTreeMap::<String, BTreeSet<String>>::new();
        dependents.insert("app".to_string(), BTreeSet::new());
        dependents.insert("cli".to_string(), BTreeSet::new());
        dependents.insert(
            "core".to_string(),
            BTreeSet::from(["app".to_string(), "cli".to_string()]),
        );

        Workspace {
            root,
            packages,
            dependencies,
            dependents,
        }
    }

    #[test]
    fn changed_packages_maps_direct_paths() {
        let workspace = fixture_workspace();
        let paths = BTreeSet::from([PathBuf::from("crates/core/src/lib.rs")]);

        let changed = workspace.changed_packages(&paths, false);

        assert_eq!(changed, BTreeSet::from(["core".to_string()]));
    }

    #[test]
    fn changed_packages_expands_dependents_by_default() {
        let workspace = fixture_workspace();
        let paths = BTreeSet::from([PathBuf::from("crates/core/src/lib.rs")]);

        let changed = workspace.changed_packages(&paths, true);

        assert_eq!(
            changed,
            BTreeSet::from(["app".to_string(), "cli".to_string(), "core".to_string()])
        );
    }

    #[test]
    fn global_impact_file_marks_all_packages_changed() {
        let workspace = fixture_workspace();
        let paths = BTreeSet::from([PathBuf::from("Cargo.toml")]);

        let changed = workspace.changed_packages(&paths, false);

        assert_eq!(
            changed,
            BTreeSet::from(["app".to_string(), "cli".to_string(), "core".to_string()])
        );
    }

    #[test]
    fn topological_order_sorts_dependencies_first() {
        let workspace = fixture_workspace();
        let selected = BTreeSet::from(["app".to_string(), "cli".to_string(), "core".to_string()]);

        let ordered = workspace.topological_order(&selected).unwrap();

        let core_index = ordered.iter().position(|name| name == "core").unwrap();
        let app_index = ordered.iter().position(|name| name == "app").unwrap();
        let cli_index = ordered.iter().position(|name| name == "cli").unwrap();

        assert!(core_index < app_index);
        assert!(core_index < cli_index);
    }
}
