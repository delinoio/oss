use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
};

use anyhow::{bail, ensure, Context, Result};
use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::config::{self, Config, DependencyKind, WorkspaceConfig};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Project {
    pub id: String,
    pub directory: PathBuf,
    pub native: BTreeSet<String>,
    pub config: Option<Config>,
}
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
pub struct ProjectEdge {
    pub from: String,
    pub to: String,
    pub kind: DependencyKind,
    pub condition: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Coverage {
    pub adapter: String,
    pub complete: bool,
    pub message: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Workspace {
    pub root: PathBuf,
    pub config: Config,
    pub projects: BTreeMap<String, Project>,
    pub edges: BTreeSet<ProjectEdge>,
    pub coverage: Vec<Coverage>,
    pub metadata_files: BTreeSet<PathBuf>,
    pub generation: String,
}

pub fn locate_root(start: &Path) -> Result<PathBuf> {
    let start = start.canonicalize()?;
    let mut nearest = None;
    for directory in start.ancestors() {
        let configuration = directory.join("taskflow.yml");
        if configuration.is_file() {
            nearest.get_or_insert_with(|| directory.to_path_buf());
            if !config::load(&configuration)?.workspace.manifests.is_empty() {
                return Ok(directory.to_path_buf());
            }
        }
        if directory.join("pnpm-workspace.yaml").exists() || directory.join("go.work").exists() {
            return Ok(directory.to_path_buf());
        }
        if directory.join(".git").exists() {
            return Ok(nearest.unwrap_or_else(|| directory.to_path_buf()));
        }
    }
    Ok(nearest.unwrap_or(start))
}

impl Workspace {
    pub async fn discover(root: &Path) -> Result<Self> {
        let root = root
            .canonicalize()
            .context("workspace root does not exist")?;
        let config_path = root.join("taskflow.yml");
        let root_config = if config_path.exists() {
            config::load(&config_path)?
        } else {
            Config {
                version: 1,
                project: "repo".into(),
                workspace: WorkspaceConfig::default(),
                tasks: BTreeMap::new(),
                start: BTreeMap::new(),
                dotenv: true,
                remote: None,
                ci: None,
            }
        };
        let mut ws = Self {
            root: root.clone(),
            config: root_config,
            projects: BTreeMap::new(),
            edges: BTreeSet::new(),
            coverage: vec![],
            metadata_files: BTreeSet::new(),
            generation: String::new(),
        };
        ws.add_project(&root, "root")?;
        let mut manifests = ws.config.workspace.manifests.clone();
        if manifests.is_empty() {
            for file in ["pnpm-workspace.yaml", "Cargo.toml", "go.work"] {
                if root.join(file).is_file() {
                    manifests.push(file.into());
                }
            }
            if !root.join("pnpm-workspace.yaml").exists() && root.join("package.json").is_file() {
                manifests.push("package.json".into());
            }
            if !root.join("go.work").exists() && root.join("go.mod").is_file() {
                manifests.push("go.mod".into());
            }
        }
        for manifest in manifests {
            let path = crate::files::within(&root, &root.join(&manifest))?;
            ensure!(
                path.is_file(),
                "workspace manifest does not exist: {manifest}"
            );
            let directory = path.parent().unwrap();
            match path.file_name().and_then(|s| s.to_str()).unwrap_or("") {
                "pnpm-workspace.yaml" | "package.json" => ws.pnpm(directory).await?,
                "Cargo.toml" => ws.cargo(&path).await?,
                "go.mod" | "go.work" => ws.go(&path).await?,
                _ => bail!("unsupported native manifest: {manifest}"),
            }
        }
        // The directory map is authoritative; equal native names are never edges.
        for project in ws.projects.values() {
            for name in [
                "taskflow.yml",
                "package.json",
                "pnpm-workspace.yaml",
                "pnpm-lock.yaml",
                "Cargo.toml",
                "Cargo.lock",
                "go.mod",
                "go.sum",
                "go.work",
                "go.work.sum",
                ".npmrc",
                "rust-toolchain",
                "rust-toolchain.toml",
            ] {
                ws.metadata_files.insert(project.directory.join(name));
            }
        }
        let graph_bytes = serde_json::to_vec(&(&ws.projects, &ws.edges, &ws.coverage, &ws.config))?;
        ws.generation = crate::files::digest(&graph_bytes);
        Ok(ws)
    }

    fn add_project(&mut self, directory: &Path, native: &str) -> Result<String> {
        let directory = directory
            .canonicalize()
            .context("native project directory is missing")?;
        ensure!(
            directory.starts_with(&self.root),
            "native project is outside workspace: {}",
            directory.display()
        );
        if let Some(project) = self
            .projects
            .values_mut()
            .find(|p| p.directory == directory)
        {
            project.native.insert(native.into());
            return Ok(project.id.clone());
        }
        let path = directory.join("taskflow.yml");
        let config = path.is_file().then(|| config::load(&path)).transpose()?;
        let id = config
            .as_ref()
            .map(|c| c.project.clone())
            .unwrap_or_else(|| {
                format!(
                    "path:{}",
                    crate::files::slash(directory.strip_prefix(&self.root).unwrap())
                        .trim_end_matches('/')
                )
            });
        ensure!(
            !self.projects.contains_key(&id),
            "duplicate explicit project ID: {id}"
        );
        self.projects.insert(
            id.clone(),
            Project {
                id: id.clone(),
                directory,
                native: BTreeSet::from([native.into()]),
                config,
            },
        );
        Ok(id)
    }

    async fn pnpm(&mut self, directory: &Path) -> Result<()> {
        let args = [
            "pnpm",
            "-r",
            "list",
            "--json",
            "--depth",
            "0",
            "--lockfile-only",
        ];
        let data = metadata(directory, &args, &[]).await;
        let (items, complete) = match data {
            Ok(value) if value.is_array() => (value.as_array().unwrap().clone(), true),
            _ => {
                // Membership comes from pnpm itself even before a lockfile exists.
                // No names or declared version ranges are treated as resolved edges.
                let membership = metadata(
                    directory,
                    &["pnpm", "-r", "list", "--json", "--depth", "-1"],
                    &[],
                )
                .await;
                let mut members = match membership {
                    Ok(v) => v.as_array().cloned().unwrap_or_default(),
                    Err(_) => vec![],
                };
                if members.is_empty() {
                    let native = directory.join("pnpm-workspace.yaml");
                    if native.exists() {
                        let yaml: serde_yaml::Value =
                            serde_yaml::from_slice(&std::fs::read(&native)?)?;
                        let patterns: Vec<String> = yaml
                            .get("packages")
                            .and_then(|v| v.as_sequence())
                            .into_iter()
                            .flatten()
                            .filter_map(|v| v.as_str().map(str::to_owned))
                            .collect();
                        for entry in walkdir::WalkDir::new(directory)
                            .into_iter()
                            .filter_entry(|e| !crate::files::ignored_directory(e.path()))
                        {
                            let entry = entry?;
                            if entry.file_name() != "package.json" {
                                continue;
                            }
                            let parent = entry.path().parent().unwrap();
                            let relative = crate::files::slash(parent.strip_prefix(directory)?);
                            if crate::files::matches_patterns(&patterns, &relative)? {
                                members.push(serde_json::json!({"path": parent}));
                            }
                        }
                    }
                    members.push(serde_json::json!({"path": directory}));
                }
                (members, false)
            }
        };
        self.coverage.push(Coverage {
            adapter: "pnpm".into(),
            complete,
            message: if complete {
                "Resolved lockfile project identities"
            } else {
                "Membership only: run an explicit pnpm installation task to resolve dependencies"
            }
            .into(),
        });
        for item in &items {
            if let Some(path) = item.get("path").and_then(Value::as_str) {
                self.add_project(Path::new(path), "pnpm")?;
            }
        }
        if !complete {
            return Ok(());
        }
        for item in &items {
            let Some(path) = item.get("path").and_then(Value::as_str) else {
                continue;
            };
            let source = self.add_project(Path::new(path), "pnpm")?;
            let package: Value =
                serde_json::from_slice(&std::fs::read(Path::new(path).join("package.json"))?)?;
            for (field, kind) in [
                ("dependencies", DependencyKind::Dependencies),
                ("devDependencies", DependencyKind::DevDependencies),
                ("optionalDependencies", DependencyKind::OptionalDependencies),
                ("peerDependencies", DependencyKind::PeerDependencies),
            ] {
                for name in package
                    .get(field)
                    .and_then(Value::as_object)
                    .into_iter()
                    .flat_map(|m| m.keys())
                {
                    let resolved = [
                        field,
                        "dependencies",
                        "devDependencies",
                        "optionalDependencies",
                    ]
                    .iter()
                    .find_map(|key| item.get(key).and_then(|v| v.get(name)));
                    let Some(target) = resolved.and_then(|v| v.get("path")).and_then(Value::as_str)
                    else {
                        continue;
                    };
                    let target = Path::new(target);
                    if target.starts_with(&self.root)
                        && !target.components().any(|c| c.as_os_str() == "node_modules")
                        && target.join("package.json").is_file()
                    {
                        let to = self.add_project(target, "pnpm")?;
                        self.edges.insert(ProjectEdge {
                            from: source.clone(),
                            to,
                            kind,
                            condition: None,
                        });
                    }
                }
            }
        }
        Ok(())
    }

    async fn cargo(&mut self, manifest: &Path) -> Result<()> {
        let directory = manifest.parent().unwrap();
        let mut args = vec![
            "cargo".to_owned(),
            "metadata".into(),
            "--format-version".into(),
            "1".into(),
            "--locked".into(),
            "--offline".into(),
            "--manifest-path".into(),
            manifest.to_string_lossy().into_owned(),
        ];
        if let Some(target) = &self.config.workspace.cargo_target {
            args.extend(["--filter-platform".into(), target.clone()]);
        }
        if self.config.workspace.cargo_no_default_features {
            args.push("--no-default-features".into());
        }
        if !self.config.workspace.cargo_features.is_empty() {
            args.extend([
                "--features".into(),
                self.config.workspace.cargo_features.join(","),
            ]);
        }
        let full = metadata_owned(directory, &args, &[]).await;
        let (data, complete) = match full {
            Ok(data) => (data, true),
            Err(_) => {
                args.push("--no-deps".into());
                (
                    metadata_owned(directory, &args, &[]).await.context(
                        "Cargo membership unavailable; prepare native metadata explicitly",
                    )?,
                    false,
                )
            }
        };
        let mut native_ids = BTreeMap::new();
        for package in data
            .get("packages")
            .and_then(Value::as_array)
            .into_iter()
            .flatten()
        {
            if package.get("source").is_some_and(|v| !v.is_null()) {
                continue;
            }
            let path = Path::new(
                package["manifest_path"]
                    .as_str()
                    .context("Cargo package lacks manifest path")?,
            );
            if !path.starts_with(&self.root) {
                continue;
            }
            let id = self.add_project(path.parent().unwrap(), "cargo")?;
            native_ids.insert(
                package["id"]
                    .as_str()
                    .context("Cargo package lacks ID")?
                    .to_owned(),
                id,
            );
        }
        for node in data
            .pointer("/resolve/nodes")
            .and_then(Value::as_array)
            .into_iter()
            .flatten()
        {
            let Some(from) = node
                .get("id")
                .and_then(Value::as_str)
                .and_then(|id| native_ids.get(id))
            else {
                continue;
            };
            for dep in node
                .get("deps")
                .and_then(Value::as_array)
                .into_iter()
                .flatten()
            {
                let Some(to) = dep
                    .get("pkg")
                    .and_then(Value::as_str)
                    .and_then(|id| native_ids.get(id))
                else {
                    continue;
                };
                for kind in dep
                    .get("dep_kinds")
                    .and_then(Value::as_array)
                    .into_iter()
                    .flatten()
                {
                    let kind_id = match kind["kind"].as_str() {
                        Some("dev") => DependencyKind::DevDependencies,
                        Some("build") => DependencyKind::BuildDependencies,
                        None => DependencyKind::Dependencies,
                        Some(_) => bail!("unsupported Cargo dependency kind"),
                    };
                    self.edges.insert(ProjectEdge {
                        from: from.clone(),
                        to: to.clone(),
                        kind: kind_id,
                        condition: kind["target"].as_str().map(str::to_owned),
                    });
                }
            }
        }
        self.coverage.push(Coverage {
            adapter: "cargo".into(),
            complete,
            message: if complete {
                "Cargo format-v1 resolved features and target conditions"
            } else {
                "Membership only: Cargo dependencies must be prepared by an explicit prerequisite"
            }
            .into(),
        });
        Ok(())
    }

    async fn go(&mut self, manifest: &Path) -> Result<()> {
        let directory = manifest.parent().unwrap();
        let environment = [("GOTOOLCHAIN", "local")];
        let mut paths = vec![];
        if manifest.file_name().unwrap() == "go.work" {
            let data = metadata(directory, &["go", "work", "edit", "-json"], &environment).await?;
            for member in data["Use"].as_array().into_iter().flatten() {
                paths.push(
                    directory.join(
                        member["DiskPath"]
                            .as_str()
                            .context("Go workspace member lacks path")?,
                    ),
                );
            }
        } else {
            paths.push(directory.to_path_buf());
        }
        let mut modules = BTreeMap::new();
        let mut declarations = vec![];
        for path in paths {
            let data = metadata(&path, &["go", "mod", "edit", "-json"], &environment).await?;
            let name = data
                .pointer("/Module/Path")
                .and_then(Value::as_str)
                .context("Go module lacks path")?;
            modules.insert(name.to_owned(), self.add_project(&path, "go")?);
            declarations.push((path, data));
        }
        let mut complete = true;
        for (path, declaration) in declarations {
            let source = modules[declaration["Module"]["Path"].as_str().unwrap()].clone();
            let args = ["go", "list", "-mod=readonly", "-m", "-json", "all"];
            let output = output_tool(&path, &args, &environment).await;
            let Ok(output) = output else {
                complete = false;
                continue;
            };
            let resolved: Vec<Value> = serde_json::Deserializer::from_slice(&output)
                .into_iter()
                .collect::<std::result::Result<_, _>>()?;
            let mut targets = BTreeMap::new();
            for module in resolved {
                let name = module["Path"]
                    .as_str()
                    .context("Go resolution lacks identity")?
                    .to_owned();
                let effective = module.get("Replace").unwrap_or(&module);
                if let Some(dir) = effective.get("Dir").and_then(Value::as_str) {
                    let dir = Path::new(dir);
                    if dir.starts_with(&self.root) && dir.join("go.mod").is_file() {
                        targets.insert(name, self.add_project(dir, "go")?);
                    }
                }
            }
            for require in declaration["Require"].as_array().into_iter().flatten() {
                let name = require["Path"]
                    .as_str()
                    .context("Go requirement lacks module identity")?;
                if let Some(to) = targets.get(name) {
                    self.edges.insert(ProjectEdge {
                        from: source.clone(),
                        to: to.clone(),
                        kind: DependencyKind::Dependencies,
                        condition: Some(
                            if require["Indirect"].as_bool().unwrap_or(false) {
                                "indirect"
                            } else {
                                "direct"
                            }
                            .into(),
                        ),
                    });
                }
            }
        }
        self.coverage.push(Coverage {
            adapter: "go".into(),
            complete,
            message: if complete {
                "Go module identities and resolved local replacements"
            } else {
                "Membership only: explicit dependency preparation is required"
            }
            .into(),
        });
        Ok(())
    }

    pub fn complete(&self) -> bool {
        self.coverage.iter().all(|c| c.complete)
    }
}

async fn metadata(directory: &Path, args: &[&str], environment: &[(&str, &str)]) -> Result<Value> {
    serde_json::from_slice(&output_tool(directory, args, environment).await?)
        .context("native metadata is not valid JSON")
}
async fn metadata_owned(
    directory: &Path,
    args: &[String],
    environment: &[(&str, &str)],
) -> Result<Value> {
    metadata(
        directory,
        &args.iter().map(String::as_str).collect::<Vec<_>>(),
        environment,
    )
    .await
}
pub async fn output_tool(
    directory: &Path,
    args: &[&str],
    environment: &[(&str, &str)],
) -> Result<Vec<u8>> {
    crate::process::capture(
        directory,
        &config::Command::Argv(args.iter().map(|v| (*v).to_owned()).collect()),
        environment,
    )
    .await
}
