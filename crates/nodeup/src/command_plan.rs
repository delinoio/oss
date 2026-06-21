use std::{
    ffi::OsString,
    fs,
    path::{Path, PathBuf},
};

use semver::Version;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use tracing::info;

use crate::{
    command_diagnostics::RuntimeCommandAvailability,
    errors::{ErrorDiagnostics, NodeupError, Result},
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
    pub requested_command: String,
    pub executable: PathBuf,
    pub args: Vec<OsString>,
    pub mode: DelegatedCommandMode,
    pub package_spec: Option<String>,
    pub package_json_path: Option<PathBuf>,
    pub reason: DelegatedCommandReason,
}

#[derive(Debug, Clone, Serialize)]
pub struct DelegatedCommandPlanDiagnostics {
    pub requested_command: String,
    pub mode: String,
    pub reason: String,
    pub executable_path: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub package_spec: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub package_spec_pinned: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub package_json_path: Option<String>,
}

impl DelegatedCommandPlan {
    pub fn diagnostics(&self) -> DelegatedCommandPlanDiagnostics {
        DelegatedCommandPlanDiagnostics {
            requested_command: self.requested_command.clone(),
            mode: self.mode.as_str().to_string(),
            reason: self.reason.as_str().to_string(),
            executable_path: self.executable.to_string_lossy().to_string(),
            package_spec: self.package_spec.clone(),
            package_spec_pinned: self.package_spec_pinned(),
            package_json_path: self
                .package_json_path
                .as_ref()
                .map(|path| path.display().to_string()),
        }
    }

    pub fn package_spec_pinned(&self) -> Option<bool> {
        match self.reason {
            DelegatedCommandReason::PackageManagerPinned => Some(true),
            DelegatedCommandReason::PackageJsonMissingFieldFallbackNpmExec
            | DelegatedCommandReason::PackageJsonNotFoundFallbackNpmExec => Some(false),
            DelegatedCommandReason::NonPackageManagerCommand
            | DelegatedCommandReason::PackageJsonMissingFieldDirect
            | DelegatedCommandReason::PackageJsonNotFoundDirect => None,
        }
    }

    pub fn npm_exec_human_notice(&self) -> Option<String> {
        if self.mode != DelegatedCommandMode::NpmExec {
            return None;
        }

        let package_spec = self.package_spec.as_deref().unwrap_or("<unknown>");
        let pinned = match self.package_spec_pinned() {
            Some(true) => "pinned",
            Some(false) => "unpinned fallback; add exact packageManager for reproducible projects",
            None => "unknown pin state",
        };
        let package_json = self
            .package_json_path
            .as_ref()
            .map(|path| path.display().to_string())
            .unwrap_or_else(|| "none".to_string());

        Some(format!(
            "nodeup: {} will run via npm exec using package {} ({pinned}; \
             package_json={package_json}; npm={}; reason={})",
            self.requested_command,
            package_spec,
            self.executable.display(),
            self.reason.as_str()
        ))
    }
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
                    requested_command: delegated_command.to_string(),
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
            requested_command: delegated_command.to_string(),
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
        let availability = RuntimeCommandAvailability::for_resolved_runtime(
            resolved,
            store,
            "npm",
            matches!(
                &resolved.target,
                crate::resolver::ResolvedRuntimeTarget::Version { .. }
            ),
            "package-manager-fallback-requires-runtime-npm",
        );
        let checked_paths = availability.checked_paths.join("|");
        let diagnostics = availability.into_error_diagnostics();
        return Err(NodeupError::not_found_with_diagnostics(
            format!(
                "Command 'npm' does not exist for runtime {} (required_for_package_manager={}, \
                 checked_paths={checked_paths})",
                resolved.runtime_id(),
                manager.as_str(),
            ),
            "Install or relink a runtime that provides npm, then retry the command. On Windows, \
             verify PATH/PATHEXT precedence with `where npm` or PowerShell `Get-Command npm -All`.",
            diagnostics,
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
        requested_command: manager.as_str().to_string(),
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
        package_spec_pinned = plan
            .package_spec_pinned()
            .map(|value| value.to_string())
            .unwrap_or_else(|| "none".to_string()),
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

        let separator_count = raw.matches('@').count();
        if separator_count == 0 && matches!(raw, "yarn" | "pnpm") {
            return Err(package_manager_invalid_input(
                format!(
                    "Missing packageManager version for manager '{raw}' in {}",
                    package_json_path.display()
                ),
                format!("Use an exact value such as `{raw}@<major>.<minor>.<patch>`."),
                package_manager_diagnostics(PackageManagerDiagnosticsInput {
                    package_json_path,
                    raw_value: Some(raw),
                    received_type: None,
                    failed_part: "version",
                    problem: "missing-version",
                    manager: Some(raw),
                    version: None,
                    correction: json!(format!("{raw}@<major>.<minor>.<patch>")),
                }),
            ));
        }

        if separator_count != 1 {
            return Err(package_manager_invalid_input(
                format!(
                    "Invalid packageManager separator in {} (value={raw})",
                    package_json_path.display()
                ),
                "Use exactly one `@` separator in `<manager>@<exact-semver>`, for example \
                 `yarn@4.13.0` or `pnpm@10.32.1`.",
                package_manager_diagnostics(PackageManagerDiagnosticsInput {
                    package_json_path,
                    raw_value: Some(raw),
                    received_type: None,
                    failed_part: "separator",
                    problem: "malformed-separator",
                    manager: None,
                    version: None,
                    correction: json!(["yarn@4.13.0", "pnpm@10.32.1"]),
                }),
            ));
        }

        let (manager_raw, version_raw) = raw
            .split_once('@')
            .expect("validated exactly one packageManager separator");

        if manager_raw.is_empty() {
            return Err(package_manager_invalid_input(
                format!(
                    "Invalid packageManager manager in {} (value={raw})",
                    package_json_path.display()
                ),
                "Set packageManager to `yarn@<exact-semver>` or `pnpm@<exact-semver>`.",
                package_manager_diagnostics(PackageManagerDiagnosticsInput {
                    package_json_path,
                    raw_value: Some(raw),
                    received_type: None,
                    failed_part: "manager",
                    problem: "missing-manager",
                    manager: None,
                    version: Some(version_raw),
                    correction: json!(["yarn@4.13.0", "pnpm@10.32.1"]),
                }),
            ));
        }

        if version_raw.is_empty() {
            return Err(package_manager_invalid_input(
                format!(
                    "Missing packageManager version for manager '{manager_raw}' in {}",
                    package_json_path.display()
                ),
                format!("Use an exact value such as `{manager_raw}@<major>.<minor>.<patch>`."),
                package_manager_diagnostics(PackageManagerDiagnosticsInput {
                    package_json_path,
                    raw_value: Some(raw),
                    received_type: None,
                    failed_part: "version",
                    problem: "missing-version",
                    manager: Some(manager_raw),
                    version: None,
                    correction: json!(format!("{manager_raw}@<major>.<minor>.<patch>")),
                }),
            ));
        }

        let manager = match manager_raw {
            "yarn" => SupportedPackageManager::Yarn,
            "pnpm" => SupportedPackageManager::Pnpm,
            _ => {
                return Err(package_manager_invalid_input(
                    format!(
                        "Unsupported packageManager manager '{manager_raw}' in {}",
                        package_json_path.display()
                    ),
                    "Use `yarn@<exact-semver>` or `pnpm@<exact-semver>`.",
                    package_manager_diagnostics(PackageManagerDiagnosticsInput {
                        package_json_path,
                        raw_value: Some(raw),
                        received_type: None,
                        failed_part: "manager",
                        problem: "unsupported-manager",
                        manager: Some(manager_raw),
                        version: Some(version_raw),
                        correction: json!(["yarn@4.13.0", "pnpm@10.32.1"]),
                    }),
                ));
            }
        };

        let version = Version::parse(version_raw).map_err(|error| {
            package_manager_invalid_input(
                format!(
                    "Invalid packageManager version '{version_raw}' in {}: {error}",
                    package_json_path.display()
                ),
                format!(
                    "Use an exact `{manager_raw}@<major>.<minor>.<patch>` value, for example \
                     `{manager_raw}@10.32.1`."
                ),
                {
                    let mut diagnostics =
                        package_manager_diagnostics(PackageManagerDiagnosticsInput {
                            package_json_path,
                            raw_value: Some(raw),
                            received_type: None,
                            failed_part: "version",
                            problem: "non-exact-semver",
                            manager: Some(manager_raw),
                            version: Some(version_raw),
                            correction: json!(format!("{manager_raw}@<major>.<minor>.<patch>")),
                        });
                    diagnostics.insert("semver_error".to_string(), json!(error.to_string()));
                    diagnostics
                },
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
            package_manager_invalid_input(
                format!(
                    "Invalid packageManager value type in {}: expected string, received {}",
                    package_json_path.display(),
                    json_type_label(&raw_value),
                ),
                "Set packageManager to a string like `yarn@4.13.0` or `pnpm@10.32.1`.",
                package_manager_diagnostics(PackageManagerDiagnosticsInput {
                    package_json_path: &package_json_path,
                    raw_value: None,
                    received_type: Some(json_type_label(&raw_value)),
                    failed_part: "value",
                    problem: "non-string",
                    manager: None,
                    version: None,
                    correction: json!("<manager>@<exact-semver>"),
                }),
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

fn package_manager_invalid_input(
    cause: impl Into<String>,
    hint: impl Into<String>,
    diagnostics: ErrorDiagnostics,
) -> NodeupError {
    NodeupError::with_hint_and_diagnostics(
        crate::errors::ErrorKind::InvalidInput,
        cause,
        hint,
        diagnostics,
    )
}

struct PackageManagerDiagnosticsInput<'a> {
    package_json_path: &'a Path,
    raw_value: Option<&'a str>,
    received_type: Option<&'a str>,
    failed_part: &'a str,
    problem: &'a str,
    manager: Option<&'a str>,
    version: Option<&'a str>,
    correction: Value,
}

fn package_manager_diagnostics(input: PackageManagerDiagnosticsInput<'_>) -> ErrorDiagnostics {
    let mut diagnostics = ErrorDiagnostics::new();
    diagnostics.insert("diagnostic".to_string(), json!("package-manager-invalid"));
    diagnostics.insert(
        "package_json_path".to_string(),
        json!(input.package_json_path.display().to_string()),
    );
    diagnostics.insert("expected".to_string(), json!("<manager>@<exact-semver>"));
    diagnostics.insert("supported_managers".to_string(), json!(["yarn", "pnpm"]));
    diagnostics.insert("failed_part".to_string(), json!(input.failed_part));
    diagnostics.insert("problem".to_string(), json!(input.problem));
    diagnostics.insert("correction".to_string(), input.correction);
    if let Some(raw_value) = input.raw_value {
        diagnostics.insert("package_manager".to_string(), json!(raw_value));
    }
    if let Some(received_type) = input.received_type {
        diagnostics.insert("received_type".to_string(), json!(received_type));
    }
    if let Some(manager) = input.manager {
        diagnostics.insert("manager".to_string(), json!(manager));
    }
    if let Some(version) = input.version {
        diagnostics.insert("version".to_string(), json!(version));
    }
    diagnostics
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
    fn package_manager_rejects_missing_versions_with_version_diagnostics() {
        let (store, runtime_dir) = setup_store("missing-version");
        fs::write(runtime_dir.join("bin").join("npm"), "#!/bin/sh\nexit 0\n").expect("write npm");

        let project_dir = runtime_dir.join("workspace");
        fs::create_dir_all(&project_dir).expect("create project dir");
        fs::write(
            project_dir.join("package.json"),
            r#"{"packageManager":"pnpm"}"#,
        )
        .expect("write package.json");

        let runtime = linked_runtime(&runtime_dir, "linked");
        let error = plan_delegated_command(&runtime, &store, "pnpm", &[], &project_dir)
            .expect_err("missing package manager version");

        assert_eq!(error.kind, crate::errors::ErrorKind::InvalidInput);
        let diagnostics = error.diagnostics.expect("diagnostics");
        assert_eq!(diagnostics["failed_part"], "version");
        assert_eq!(diagnostics["problem"], "missing-version");
        assert_eq!(diagnostics["manager"], "pnpm");
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
