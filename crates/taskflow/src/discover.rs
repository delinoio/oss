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
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub active_platforms: Option<BTreeSet<String>>,
    pub name: String,
    pub resolved: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Coverage {
    pub adapter: String,
    pub projects: BTreeSet<String>,
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

pub async fn locate_root(start: &Path) -> Result<PathBuf> {
    let start = start.canonicalize()?;
    let mut nearest = None;
    let mut cargo_root = None;
    let mut cargo_probed = false;
    let mut cargo_workspace = false;
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
        if !cargo_probed && directory.join("Cargo.toml").is_file() {
            cargo_probed = true;
            // Cargo owns membership (including exclusions and nested standalone
            // packages). Locate the workspace without resolving dependencies.
            let manifest = output_tool(
                directory,
                &[
                    "cargo",
                    "locate-project",
                    "--workspace",
                    "--message-format=plain",
                    "--offline",
                ],
                &[],
            )
            .await?;
            let manifest = Path::new(std::str::from_utf8(&manifest)?.trim()).canonicalize()?;
            cargo_root = manifest.parent().map(Path::to_path_buf);
            cargo_workspace = cargo_root.as_deref() != Some(directory);
        }
        if cargo_workspace && cargo_root.as_deref() == Some(directory) {
            return Ok(directory.to_path_buf());
        }
        if directory.join(".git").exists() {
            return Ok(nearest
                .or(cargo_root)
                .unwrap_or_else(|| directory.to_path_buf()));
        }
    }
    Ok(nearest.or(cargo_root).unwrap_or(start))
}

async fn cargo_platforms(
    directory: &Path,
    explicit: Option<&str>,
) -> Result<BTreeMap<String, (String, Vec<cargo_platform::Cfg>)>> {
    async fn cfg(directory: &Path, target: &str) -> Result<Vec<cargo_platform::Cfg>> {
        let bytes = output_tool(
            directory,
            &["rustc", "--print", "cfg", "--target", target],
            &[],
        )
        .await?;
        std::str::from_utf8(&bytes)?
            .lines()
            .map(|line| Ok(line.parse()?))
            .collect()
    }
    let host = output_tool(directory, &["rustc", "-vV"], &[]).await?;
    let host = std::str::from_utf8(&host)?
        .lines()
        .find_map(|line| line.strip_prefix("host: "))
        .context("rustc did not report its host target")?
        .to_owned();
    let host_cfg = cfg(directory, &host).await?;
    let mut targets = BTreeMap::from([(host.clone(), host_cfg.clone())]);
    let mut platforms = BTreeMap::new();
    for (os, suffix) in [
        (config::Os::Linux, "unknown-linux-gnu"),
        (config::Os::Macos, "apple-darwin"),
        (config::Os::Windows, "pc-windows-msvc"),
    ] {
        for (arch, rust_arch) in [
            (config::Arch::X64, "x86_64"),
            (config::Arch::Arm64, "aarch64"),
        ] {
            let native = host_cfg
                .contains(&format!("target_os=\"{}\"", config::enum_name(&os)).parse()?)
                && host_cfg.contains(&format!("target_arch=\"{rust_arch}\"").parse()?);
            let target = explicit.map(str::to_owned).unwrap_or_else(|| {
                if native {
                    host.clone()
                } else {
                    format!("{rust_arch}-{suffix}")
                }
            });
            if !targets.contains_key(&target) {
                targets.insert(target.clone(), cfg(directory, &target).await?);
            }
            platforms.insert(
                format!("{}-{}", config::enum_name(&os), config::enum_name(&arch)),
                (target.clone(), targets[&target].clone()),
            );
        }
    }
    Ok(platforms)
}

impl Workspace {
    pub fn select_platform(mut self, os: Option<config::Os>, arch: Option<config::Arch>) -> Self {
        if os.is_none() && arch.is_none() {
            return self;
        }
        for config in self
            .projects
            .values_mut()
            .filter_map(|p| p.config.as_mut())
            .chain(std::iter::once(&mut self.config))
        {
            for task in config.tasks.values_mut() {
                task.platform.os = task.platform.os.or(os);
                task.platform.arch = task.platform.arch.or(arch);
            }
        }
        self.generation = crate::files::digest(
            &serde_json::to_vec(&(&self.generation, os, arch)).expect("platform serialization"),
        );
        self
    }

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
        let metadata_state = ws
            .metadata_files
            .iter()
            .map(|path| {
                Ok((
                    crate::files::relative_to(&ws.root, path),
                    crate::files::file_state(path)?,
                ))
            })
            .collect::<Result<BTreeMap<_, _>>>()?;
        let graph_bytes = serde_json::to_vec(&(
            &ws.projects,
            &ws.edges,
            &ws.coverage,
            &ws.config,
            metadata_state,
        ))?;
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
            Err(error) if crate::process::aborts_discovery(&error) => return Err(error),
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
                    Err(error) if crate::process::aborts_discovery(&error) => return Err(error),
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
        let mut projects = BTreeSet::new();
        for item in &items {
            if let Some(path) = item.get("path").and_then(Value::as_str) {
                projects.insert(self.add_project(Path::new(path), "pnpm")?);
            }
        }
        self.coverage.push(Coverage {
            adapter: "pnpm".into(),
            projects,
            complete,
            message: if complete {
                "Resolved lockfile project identities"
            } else {
                "Membership only: run an explicit pnpm installation task to resolve dependencies"
            }
            .into(),
        });
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
                    let target = crate::files::canonical_path(Path::new(target))?;
                    if target.starts_with(&self.root)
                        && !target.components().any(|c| c.as_os_str() == "node_modules")
                        && target.join("package.json").is_file()
                    {
                        let to = self.add_project(&target, "pnpm")?;
                        self.edges.insert(ProjectEdge {
                            from: source.clone(),
                            to,
                            kind,
                            condition: None,
                            active_platforms: None,
                            name: name.clone(),
                            resolved: resolved
                                .and_then(|v| v.get("version"))
                                .and_then(Value::as_str)
                                .unwrap_or("local")
                                .into(),
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
        let (data, mut complete) = match full {
            Ok(data) => (data, true),
            Err(error) if crate::process::aborts_discovery(&error) => return Err(error),
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
        let has_conditions = data
            .pointer("/resolve/nodes")
            .and_then(Value::as_array)
            .into_iter()
            .flatten()
            .flat_map(|node| node["deps"].as_array().into_iter().flatten())
            .flat_map(|dep| dep["dep_kinds"].as_array().into_iter().flatten())
            .any(|kind| kind["target"].is_string());
        let platforms = if has_conditions {
            match cargo_platforms(directory, self.config.workspace.cargo_target.as_deref()).await {
                Ok(platforms) => platforms,
                Err(error) if crate::process::aborts_discovery(&error) => return Err(error),
                Err(_) => {
                    complete = false;
                    tracing::warn!(
                        code = "cargo-target-cfg-unavailable",
                        "Cargo target cfg metadata is unavailable; native selectors cannot execute"
                    );
                    BTreeMap::new()
                }
            }
        } else {
            BTreeMap::new()
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
            let path = crate::files::canonical_path(Path::new(
                package["manifest_path"]
                    .as_str()
                    .context("Cargo package lacks manifest path")?,
            ))?;
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
                        active_platforms: kind["target"]
                            .as_str()
                            .map(|condition| -> Result<_> {
                                let condition: cargo_platform::Platform = condition.parse()?;
                                Ok(platforms
                                    .iter()
                                    .filter(|(_, (target, cfg))| condition.matches(target, cfg))
                                    .map(|(platform, _)| platform.clone())
                                    .collect())
                            })
                            .transpose()?,
                        name: dep["name"]
                            .as_str()
                            .context("Cargo dependency alias missing")?
                            .into(),
                        // Cargo's local package ID embeds the checkout URL. The
                        // resolved directory has already mapped to a stable project.
                        resolved: format!("project:{to}"),
                    });
                }
            }
        }
        self.coverage.push(Coverage {
            adapter: "cargo".into(),
            projects: native_ids.values().cloned().collect(),
            complete,
            message: if complete {
                "Cargo format-v1 resolved features and target conditions"
            } else {
                "Cargo resolution or target cfg unavailable: prepare native metadata before \
                 executing selectors"
            }
            .into(),
        });
        Ok(())
    }

    async fn go(&mut self, manifest: &Path) -> Result<()> {
        let directory = manifest.parent().unwrap();
        // Never let a parent checkout's workspace or an inherited GOWORK change
        // which native manifest this adapter resolves.
        let work = if manifest.file_name().unwrap() == "go.work" {
            manifest.to_string_lossy().into_owned()
        } else {
            "off".into()
        };
        let environment = [
            ("GOTOOLCHAIN", "local"),
            ("GOWORK", work.as_str()),
            ("GOPROXY", "off"),
            // Override private-module proxy bypass as well as public downloads.
            ("GONOPROXY", "none"),
            ("GOSUMDB", "off"),
            ("GOVCS", "*:off"),
        ];
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
        let mut projects: BTreeSet<_> = modules.values().cloned().collect();
        for (path, declaration) in declarations {
            let source = modules[declaration["Module"]["Path"].as_str().unwrap()].clone();
            let args = ["go", "list", "-mod=readonly", "-m", "-json", "all"];
            let output = output_tool(&path, &args, &environment).await;
            let output = match output {
                Ok(output) => output,
                Err(error) if crate::process::aborts_discovery(&error) => return Err(error),
                Err(_) => {
                    complete = false;
                    continue;
                }
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
                    let dir = crate::files::canonical_path(Path::new(dir))?;
                    if dir.starts_with(&self.root) && dir.join("go.mod").is_file() {
                        let id = self.add_project(&dir, "go")?;
                        projects.insert(id.clone());
                        targets.insert(name, id);
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
                        active_platforms: None,
                        name: name.into(),
                        resolved: require["Version"].as_str().unwrap_or("workspace").into(),
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
            projects,
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

    pub fn project_complete(&self, project: &str) -> bool {
        self.coverage
            .iter()
            .filter(|coverage| coverage.projects.contains(project))
            .all(|coverage| coverage.complete)
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
