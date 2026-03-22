use std::{
    ffi::OsString,
    fs,
    path::{Path, PathBuf},
};

use semver::Version;
use serde::Deserialize;
use serde_json::Value;
use tracing::info;

use crate::{
    errors::{NodeupError, Result},
    resolver::ResolvedRuntime,
    store::Store,
};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DelegatedCommandMode {
    Direct,
    NpmExec,
}

impl DelegatedCommandMode {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Direct => "direct",
            Self::NpmExec => "npm-exec",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DelegatedCommandReason {
    NonPackageManagerCommand,
    PackageManagerPinned,
    PackageJsonMissingFieldDirect,
    PackageJsonMissingFieldFallbackNpmExec,
    PackageJsonNotFoundDirect,
    PackageJsonNotFoundFallbackNpmExec,
}

impl DelegatedCommandReason {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::NonPackageManagerCommand => "non-package-manager-command",
            Self::PackageManagerPinned => "package-manager-pinned",
            Self::PackageJsonMissingFieldDirect => "package-json-missing-field-direct",
            Self::PackageJsonMissingFieldFallbackNpmExec => {
                "package-json-missing-field-fallback-npm-exec"
            }
            Self::PackageJsonNotFoundDirect => "package-json-not-found-direct",
            Self::PackageJsonNotFoundFallbackNpmExec => "package-json-not-found-fallback-npm-exec",
        }
    }
}

#[derive(Debug, Clone)]
pub struct DelegatedCommandPlan {
    pub executable: PathBuf,
    pub args: Vec<OsString>,
    pub mode: DelegatedCommandMode,
    pub package_spec: Option<String>,
    pub package_json_path: Option<PathBuf>,
    pub reason: DelegatedCommandReason,
}

pub fn plan_delegated_command(
    resolved: &ResolvedRuntime,
    store: &Store,
    delegated_command: &str,
    delegated_args: &[OsString],
    cwd: &Path,
) -> Result<DelegatedCommandPlan> {
    let maybe_manager = SupportedPackageManager::from_command(delegated_command);

    let plan = if let Some(requested_manager) = maybe_manager {
        let discovery = discover_package_manager(cwd)?;

        if let Some(configured_manager) = discovery.configured {
            if configured_manager.manager != requested_manager {
                let package_json_path = discovery
                    .package_json_path
                    .as_ref()
                    .map(|path| path.display().to_string())
                    .unwrap_or_else(|| "<unknown>".to_string());

                return Err(NodeupError::conflict_with_hint(
                    format!(
                        "Requested command '{delegated_command}' does not match packageManager \
                         '{}' in {}",
                        configured_manager.raw, package_json_path
                    ),
                    format!(
                        "Use `{}` in this project, or update packageManager to match \
                         `{delegated_command}`.",
                        configured_manager.manager.as_str()
                    ),
                ));
            }

            let package_spec = configured_manager.to_package_spec();
            build_npm_exec_plan(
                resolved,
                store,
                requested_manager,
                delegated_args,
                package_spec,
                discovery.package_json_path,
                DelegatedCommandReason::PackageManagerPinned,
            )?
        } else {
            let direct_executable = resolved.executable_path(store, requested_manager.as_str());
            if direct_executable.exists() {
                let reason = if discovery.package_json_path.is_some() {
                    DelegatedCommandReason::PackageJsonMissingFieldDirect
                } else {
                    DelegatedCommandReason::PackageJsonNotFoundDirect
                };
                DelegatedCommandPlan {
                    executable: direct_executable,
                    args: delegated_args.to_vec(),
                    mode: DelegatedCommandMode::Direct,
                    package_spec: None,
                    package_json_path: discovery.package_json_path,
                    reason,
                }
            } else {
                let package_spec = requested_manager.default_package_spec().to_string();
                let reason = if discovery.package_json_path.is_some() {
                    DelegatedCommandReason::PackageJsonMissingFieldFallbackNpmExec
                } else {
                    DelegatedCommandReason::PackageJsonNotFoundFallbackNpmExec
                };

                build_npm_exec_plan(
                    resolved,
                    store,
                    requested_manager,
                    delegated_args,
                    package_spec,
                    discovery.package_json_path,
                    reason,
                )?
            }
        }
    } else {
        DelegatedCommandPlan {
            executable: resolved.executable_path(store, delegated_command),
            args: delegated_args.to_vec(),
            mode: DelegatedCommandMode::Direct,
            package_spec: None,
            package_json_path: None,
            reason: DelegatedCommandReason::NonPackageManagerCommand,
        }
    };

    log_command_plan(resolved, delegated_command, &plan);
    Ok(plan)
}

fn build_npm_exec_plan(
    resolved: &ResolvedRuntime,
    store: &Store,
    manager: SupportedPackageManager,
    delegated_args: &[OsString],
    package_spec: String,
    package_json_path: Option<PathBuf>,
    reason: DelegatedCommandReason,
) -> Result<DelegatedCommandPlan> {
    let npm_executable = resolved.executable_path(store, "npm");
    if !npm_executable.exists() {
        return Err(NodeupError::not_found_with_hint(
            format!(
                "Command 'npm' does not exist for runtime {} (required_for_package_manager={})",
                resolved.runtime_id(),
                manager.as_str(),
            ),
            "Install or relink a runtime that provides npm, then retry the command.",
        ));
    }

    let mut args = vec![
        OsString::from("exec"),
        OsString::from("--yes"),
        OsString::from("--package"),
        OsString::from(package_spec.as_str()),
        OsString::from("--"),
        OsString::from(manager.as_str()),
    ];
    args.extend(delegated_args.iter().cloned());

    Ok(DelegatedCommandPlan {
        executable: npm_executable,
        args,
        mode: DelegatedCommandMode::NpmExec,
        package_spec: Some(package_spec),
        package_json_path,
        reason,
    })
}

fn log_command_plan(
    resolved: &ResolvedRuntime,
    delegated_command: &str,
    plan: &DelegatedCommandPlan,
) {
    let package_spec = plan.package_spec.as_deref().unwrap_or("none");
    let package_json_path = plan
        .package_json_path
        .as_ref()
        .map(|path| path.display().to_string())
        .unwrap_or_else(|| "none".to_string());

    info!(
        command_path = "nodeup.command-plan",
        runtime = %resolved.runtime_id(),
        delegated_command,
        mode = plan.mode.as_str(),
        package_spec,
        package_json_path = %package_json_path,
        reason = plan.reason.as_str(),
        executable = %plan.executable.display(),
        args_len = plan.args.len(),
        "Planned delegated command execution"
    );
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum SupportedPackageManager {
    Yarn,
    Pnpm,
}

impl SupportedPackageManager {
    fn from_command(value: &str) -> Option<Self> {
        match value {
            "yarn" => Some(Self::Yarn),
            "pnpm" => Some(Self::Pnpm),
            _ => None,
        }
    }

    fn as_str(self) -> &'static str {
        match self {
            Self::Yarn => "yarn",
            Self::Pnpm => "pnpm",
        }
    }

    fn default_package_spec(self) -> &'static str {
        match self {
            Self::Yarn => "@yarnpkg/cli-dist",
            Self::Pnpm => "pnpm",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct ConfiguredPackageManager {
    raw: String,
    manager: SupportedPackageManager,
    version: Version,
}

impl ConfiguredPackageManager {
    fn parse(raw: &str, package_json_path: &Path) -> Result<Self> {
        let raw = raw.trim();
        let (manager_raw, version_raw) = raw.split_once('@').ok_or_else(|| {
            NodeupError::invalid_input_with_hint(
                format!(
                    "Invalid packageManager value '{raw}' in {}",
                    package_json_path.display()
                ),
                "Use `<manager>@<exact-semver>` with manager `yarn` or `pnpm` (for example \
                 `yarn@4.13.0` or `pnpm@10.32.1`).",
            )
        })?;

        let manager = match manager_raw {
            "yarn" => SupportedPackageManager::Yarn,
            "pnpm" => SupportedPackageManager::Pnpm,
            _ => {
                return Err(NodeupError::invalid_input_with_hint(
                    format!(
                        "Unsupported packageManager '{manager_raw}' in {}",
                        package_json_path.display()
                    ),
                    "Use `yarn@<exact-semver>` or `pnpm@<exact-semver>`.",
                ));
            }
        };

        let version = Version::parse(version_raw).map_err(|error| {
            NodeupError::invalid_input_with_hint(
                format!(
                    "Invalid packageManager version '{version_raw}' in {}: {error}",
                    package_json_path.display()
                ),
                "Use an exact semantic version (for example `yarn@4.13.0` or `pnpm@10.32.1`).",
            )
        })?;

        Ok(Self {
            raw: raw.to_string(),
            manager,
            version,
        })
    }

    fn to_package_spec(&self) -> String {
        match self.manager {
            SupportedPackageManager::Pnpm => format!("pnpm@{}", self.version),
            SupportedPackageManager::Yarn => {
                if self.version.major >= 2 {
                    format!("@yarnpkg/cli-dist@{}", self.version)
                } else {
                    format!("yarn@{}", self.version)
                }
            }
        }
    }
}

#[derive(Debug, Deserialize)]
struct PackageJsonManifest {
    #[serde(rename = "packageManager")]
    package_manager: Option<Value>,
}

#[derive(Debug, Clone)]
struct PackageManagerDiscovery {
    package_json_path: Option<PathBuf>,
    configured: Option<ConfiguredPackageManager>,
}

fn discover_package_manager(cwd: &Path) -> Result<PackageManagerDiscovery> {
    let Some(package_json_path) = nearest_package_json(cwd) else {
        return Ok(PackageManagerDiscovery {
            package_json_path: None,
            configured: None,
        });
    };

    let content = fs::read_to_string(&package_json_path).map_err(|error| {
        NodeupError::invalid_input_with_hint(
            format!("Failed to read {}: {error}", package_json_path.display()),
            "Ensure package.json is readable, then retry the command.",
        )
    })?;
    let manifest: PackageJsonManifest = serde_json::from_str(&content).map_err(|error| {
        NodeupError::invalid_input_with_hint(
            format!("Failed to parse {}: {error}", package_json_path.display()),
            "Fix package.json JSON syntax, then retry the command.",
        )
    })?;

    let configured = if let Some(raw_value) = manifest.package_manager {
        let raw = raw_value.as_str().ok_or_else(|| {
            NodeupError::invalid_input_with_hint(
                format!(
                    "Invalid packageManager value type in {}: expected string, received {}",
                    package_json_path.display(),
                    json_type_label(&raw_value),
                ),
                "Set packageManager to a string like `yarn@4.13.0` or `pnpm@10.32.1`.",
            )
        })?;
        Some(ConfiguredPackageManager::parse(raw, &package_json_path)?)
    } else {
        None
    };

    Ok(PackageManagerDiscovery {
        package_json_path: Some(package_json_path),
        configured,
    })
}

fn nearest_package_json(cwd: &Path) -> Option<PathBuf> {
    let mut current = Some(cwd);

    while let Some(path) = current {
        let candidate = path.join("package.json");
        if candidate.is_file() {
            return Some(candidate);
        }
        current = path.parent();
    }

    None
}

fn json_type_label(value: &Value) -> &'static str {
    match value {
        Value::Null => "null",
        Value::Bool(_) => "bool",
        Value::Number(_) => "number",
        Value::String(_) => "string",
        Value::Array(_) => "array",
        Value::Object(_) => "object",
    }
}

#[cfg(test)]
mod tests {
    use std::{
        fs,
        path::{Path, PathBuf},
        time::{SystemTime, UNIX_EPOCH},
    };

    use super::*;
    use crate::{
        paths::NodeupPaths,
        resolver::{ResolvedRuntime, ResolvedRuntimeTarget},
        selectors::RuntimeSelector,
        types::RuntimeSelectorSource,
    };

    fn temp_paths(label: &str) -> NodeupPaths {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("system time")
            .as_nanos();
        let root = std::env::temp_dir().join(format!("nodeup-command-plan-{label}-{nonce}"));

        NodeupPaths {
            data_root: root.join("data"),
            cache_root: root.join("cache"),
            config_root: root.join("config"),
            toolchains_dir: root.join("data").join("toolchains"),
            downloads_dir: root.join("cache").join("downloads"),
            release_index_cache_file: root.join("cache").join("release-index.json"),
            settings_file: root.join("config").join("settings.toml"),
            overrides_file: root.join("config").join("overrides.toml"),
        }
    }

    fn linked_runtime(runtime_dir: &Path, name: &str) -> ResolvedRuntime {
        ResolvedRuntime {
            source: RuntimeSelectorSource::Explicit,
            selector: RuntimeSelector::LinkedName(name.to_string()),
            target: ResolvedRuntimeTarget::LinkedPath {
                name: name.to_string(),
                path: runtime_dir.to_path_buf(),
            },
        }
    }

    fn os_args(values: &[&str]) -> Vec<OsString> {
        values.iter().map(OsString::from).collect()
    }

    fn args_to_strings(values: &[OsString]) -> Vec<String> {
        values
            .iter()
            .map(|value| value.to_string_lossy().to_string())
            .collect()
    }

    fn setup_store(label: &str) -> (Store, PathBuf) {
        let paths = temp_paths(label);
        paths.ensure_layout().expect("ensure layout");

        let runtime_dir = paths.data_root.join("linked-runtime");
        let runtime_bin = runtime_dir.join("bin");
        fs::create_dir_all(&runtime_bin).expect("create runtime bin");

        (Store::new(paths), runtime_dir)
    }

    #[test]
    fn yarn_major_one_maps_to_yarn_package_spec() {
        let (store, runtime_dir) = setup_store("yarn-major-one");
        fs::write(runtime_dir.join("bin").join("npm"), "#!/bin/sh\nexit 0\n").expect("write npm");

        let project_dir = runtime_dir.join("workspace");
        fs::create_dir_all(&project_dir).expect("create project dir");
        fs::write(
            project_dir.join("package.json"),
            r#"{"packageManager":"yarn@1.22.22"}"#,
        )
        .expect("write package.json");

        let runtime = linked_runtime(&runtime_dir, "linked");
        let plan = plan_delegated_command(
            &runtime,
            &store,
            "yarn",
            &os_args(&["--version"]),
            &project_dir,
        )
        .expect("plan command");

        assert_eq!(plan.mode, DelegatedCommandMode::NpmExec);
        assert_eq!(plan.package_spec.as_deref(), Some("yarn@1.22.22"));
        assert_eq!(
            args_to_strings(&plan.args),
            vec![
                "exec",
                "--yes",
                "--package",
                "yarn@1.22.22",
                "--",
                "yarn",
                "--version",
            ]
        );
    }

    #[test]
    fn yarn_major_two_or_later_maps_to_cli_dist_package_spec() {
        let (store, runtime_dir) = setup_store("yarn-major-two");
        fs::write(runtime_dir.join("bin").join("npm"), "#!/bin/sh\nexit 0\n").expect("write npm");

        let project_dir = runtime_dir.join("workspace");
        fs::create_dir_all(&project_dir).expect("create project dir");
        fs::write(
            project_dir.join("package.json"),
            r#"{"packageManager":"yarn@4.13.0"}"#,
        )
        .expect("write package.json");

        let runtime = linked_runtime(&runtime_dir, "linked");
        let plan = plan_delegated_command(&runtime, &store, "yarn", &[], &project_dir)
            .expect("plan command");

        assert_eq!(plan.mode, DelegatedCommandMode::NpmExec);
        assert_eq!(
            plan.package_spec.as_deref(),
            Some("@yarnpkg/cli-dist@4.13.0")
        );
        assert_eq!(plan.reason, DelegatedCommandReason::PackageManagerPinned);
    }

    #[test]
    fn package_manager_rejects_non_semver_versions() {
        let (store, runtime_dir) = setup_store("invalid-semver");
        fs::write(runtime_dir.join("bin").join("npm"), "#!/bin/sh\nexit 0\n").expect("write npm");

        let project_dir = runtime_dir.join("workspace");
        fs::create_dir_all(&project_dir).expect("create project dir");
        fs::write(
            project_dir.join("package.json"),
            r#"{"packageManager":"pnpm@10.x"}"#,
        )
        .expect("write package.json");

        let runtime = linked_runtime(&runtime_dir, "linked");
        let error = plan_delegated_command(&runtime, &store, "pnpm", &[], &project_dir)
            .expect_err("invalid package manager version");

        assert_eq!(error.kind, crate::errors::ErrorKind::InvalidInput);
    }

    #[test]
    fn package_manager_rejects_mismatched_requested_command() {
        let (store, runtime_dir) = setup_store("mismatch");
        fs::write(runtime_dir.join("bin").join("npm"), "#!/bin/sh\nexit 0\n").expect("write npm");

        let project_dir = runtime_dir.join("workspace");
        fs::create_dir_all(&project_dir).expect("create project dir");
        fs::write(
            project_dir.join("package.json"),
            r#"{"packageManager":"pnpm@10.32.1"}"#,
        )
        .expect("write package.json");

        let runtime = linked_runtime(&runtime_dir, "linked");
        let error = plan_delegated_command(&runtime, &store, "yarn", &[], &project_dir)
            .expect_err("mismatch should fail");

        assert_eq!(error.kind, crate::errors::ErrorKind::Conflict);
    }

    #[test]
    fn missing_package_manager_field_prefers_direct_binary() {
        let (store, runtime_dir) = setup_store("field-missing-direct");
        fs::write(runtime_dir.join("bin").join("yarn"), "#!/bin/sh\nexit 0\n").expect("write yarn");

        let project_dir = runtime_dir.join("workspace");
        fs::create_dir_all(&project_dir).expect("create project dir");
        fs::write(project_dir.join("package.json"), "{}").expect("write package.json");

        let runtime = linked_runtime(&runtime_dir, "linked");
        let plan = plan_delegated_command(&runtime, &store, "yarn", &[], &project_dir)
            .expect("plan command");

        assert_eq!(plan.mode, DelegatedCommandMode::Direct);
        assert_eq!(
            plan.reason,
            DelegatedCommandReason::PackageJsonMissingFieldDirect
        );
        assert!(plan.executable.ends_with("bin/yarn"));
    }

    #[test]
    fn no_package_json_falls_back_to_npm_exec_when_binary_is_missing() {
        let (store, runtime_dir) = setup_store("no-package-json-fallback");
        fs::write(runtime_dir.join("bin").join("npm"), "#!/bin/sh\nexit 0\n").expect("write npm");

        let cwd = runtime_dir.join("workspace-without-package-json");
        fs::create_dir_all(&cwd).expect("create workspace dir");

        let runtime = linked_runtime(&runtime_dir, "linked");
        let plan = plan_delegated_command(&runtime, &store, "yarn", &os_args(&["install"]), &cwd)
            .expect("plan command");

        assert_eq!(plan.mode, DelegatedCommandMode::NpmExec);
        assert_eq!(plan.package_spec.as_deref(), Some("@yarnpkg/cli-dist"));
        assert_eq!(
            plan.reason,
            DelegatedCommandReason::PackageJsonNotFoundFallbackNpmExec
        );
        assert_eq!(
            args_to_strings(&plan.args),
            vec![
                "exec",
                "--yes",
                "--package",
                "@yarnpkg/cli-dist",
                "--",
                "yarn",
                "install",
            ]
        );
    }
}
