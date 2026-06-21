use std::path::{Path, PathBuf};

use serde::Serialize;
use serde_json::json;

use crate::{
    errors::ErrorDiagnostics,
    resolver::{ResolvedRuntime, ResolvedRuntimeTarget},
    store::{runtime_executable_candidate_paths, runtime_executable_is_runnable, Store},
    types::ManagedAlias,
};

pub const PATH_PRECEDENCE_GUIDANCE: &str =
    "PATH is searched from left to right. On Windows, PATHEXT controls extension probing order; \
     place the Nodeup shim directory before other Node.js or package-manager directories, then \
     verify with `where npm`, `where node`, or PowerShell `Get-Command npm -All`.";

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "snake_case")]
pub struct RuntimeCommandAvailability {
    pub command: String,
    pub runtime: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub linked_runtime_name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub linked_runtime_path: Option<String>,
    pub checked_paths: Vec<String>,
    pub selected_path: String,
    pub direct_executable_exists: bool,
    pub direct_executable_runnable: bool,
    pub managed_shim_available: bool,
    pub availability_mode: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub delegated_executable_path: Option<String>,
    pub install_on_demand_eligible: bool,
    pub install_on_demand_scope: String,
    pub path_precedence_guidance: &'static str,
}

impl RuntimeCommandAvailability {
    pub fn for_resolved_runtime(
        resolved: &ResolvedRuntime,
        store: &Store,
        command: &str,
        install_on_demand_eligible: bool,
        install_on_demand_scope: impl Into<String>,
    ) -> Self {
        let runtime_root = runtime_root_for_resolved_runtime(resolved, store);
        let checked_path_values = runtime_root
            .as_deref()
            .map(|root| runtime_executable_candidate_paths(root, command))
            .unwrap_or_else(|| vec![PathBuf::from("<runtime-not-installed>").join(command)]);
        let selected_path = selected_existing_or_primary(&checked_path_values);
        let direct_executable_exists = selected_path.exists();
        let direct_executable_runnable = runtime_executable_is_runnable(&selected_path);
        let npm_exec_fallback = npm_exec_fallback_for_command(
            runtime_root.as_deref(),
            command,
            direct_executable_exists,
        );
        let (linked_runtime_name, linked_runtime_path) = linked_runtime_details(resolved);

        Self {
            command: command.to_string(),
            runtime: resolved.runtime_id(),
            linked_runtime_name,
            linked_runtime_path,
            checked_paths: checked_path_values
                .iter()
                .map(|path| path.display().to_string())
                .collect(),
            selected_path: selected_path.display().to_string(),
            direct_executable_exists,
            direct_executable_runnable,
            managed_shim_available: direct_executable_runnable || npm_exec_fallback.is_some(),
            availability_mode: availability_mode(direct_executable_runnable, &npm_exec_fallback)
                .to_string(),
            delegated_executable_path: npm_exec_fallback
                .as_ref()
                .map(|path| path.display().to_string()),
            install_on_demand_eligible,
            install_on_demand_scope: install_on_demand_scope.into(),
            path_precedence_guidance: PATH_PRECEDENCE_GUIDANCE,
        }
    }

    pub fn for_linked_runtime(
        name: &str,
        runtime_root: &Path,
        command: &str,
        install_on_demand_eligible: bool,
    ) -> Self {
        let checked_path_values = runtime_executable_candidate_paths(runtime_root, command);
        let selected_path = selected_existing_or_primary(&checked_path_values);
        let direct_executable_exists = selected_path.exists();
        let direct_executable_runnable = runtime_executable_is_runnable(&selected_path);
        let npm_exec_fallback =
            npm_exec_fallback_for_command(Some(runtime_root), command, direct_executable_exists);

        Self {
            command: command.to_string(),
            runtime: name.to_string(),
            linked_runtime_name: Some(name.to_string()),
            linked_runtime_path: Some(runtime_root.display().to_string()),
            checked_paths: checked_path_values
                .iter()
                .map(|path| path.display().to_string())
                .collect(),
            selected_path: selected_path.display().to_string(),
            direct_executable_exists,
            direct_executable_runnable,
            managed_shim_available: direct_executable_runnable || npm_exec_fallback.is_some(),
            availability_mode: availability_mode(direct_executable_runnable, &npm_exec_fallback)
                .to_string(),
            delegated_executable_path: npm_exec_fallback
                .as_ref()
                .map(|path| path.display().to_string()),
            install_on_demand_eligible,
            install_on_demand_scope: "linked-runtime".to_string(),
            path_precedence_guidance: PATH_PRECEDENCE_GUIDANCE,
        }
    }

    pub fn into_error_diagnostics(self) -> ErrorDiagnostics {
        let mut diagnostics = ErrorDiagnostics::new();
        diagnostics.insert("command".to_string(), json!(self.command));
        diagnostics.insert("runtime".to_string(), json!(self.runtime));
        if let Some(linked_runtime_name) = self.linked_runtime_name {
            diagnostics.insert(
                "linked_runtime_name".to_string(),
                json!(linked_runtime_name),
            );
        }
        if let Some(linked_runtime_path) = self.linked_runtime_path {
            diagnostics.insert(
                "linked_runtime_path".to_string(),
                json!(linked_runtime_path),
            );
        }
        diagnostics.insert("checked_paths".to_string(), json!(self.checked_paths));
        diagnostics.insert("selected_path".to_string(), json!(self.selected_path));
        diagnostics.insert(
            "direct_executable_exists".to_string(),
            json!(self.direct_executable_exists),
        );
        diagnostics.insert(
            "direct_executable_runnable".to_string(),
            json!(self.direct_executable_runnable),
        );
        diagnostics.insert(
            "managed_shim_available".to_string(),
            json!(self.managed_shim_available),
        );
        diagnostics.insert(
            "availability_mode".to_string(),
            json!(self.availability_mode),
        );
        if let Some(delegated_executable_path) = self.delegated_executable_path {
            diagnostics.insert(
                "delegated_executable_path".to_string(),
                json!(delegated_executable_path),
            );
        }
        diagnostics.insert(
            "install_on_demand_eligible".to_string(),
            json!(self.install_on_demand_eligible),
        );
        diagnostics.insert(
            "install_on_demand_scope".to_string(),
            json!(self.install_on_demand_scope),
        );
        diagnostics.insert(
            "path_precedence_guidance".to_string(),
            json!(self.path_precedence_guidance),
        );
        diagnostics
    }
}

pub fn managed_alias_availability_for_linked_runtime(
    name: &str,
    runtime_root: &Path,
) -> Vec<RuntimeCommandAvailability> {
    [
        ManagedAlias::Node,
        ManagedAlias::Npm,
        ManagedAlias::Npx,
        ManagedAlias::Yarn,
        ManagedAlias::Pnpm,
    ]
    .into_iter()
    .map(|alias| {
        RuntimeCommandAvailability::for_linked_runtime(name, runtime_root, alias.as_str(), false)
    })
    .collect()
}

pub fn render_availability_matrix(availability: &[RuntimeCommandAvailability]) -> String {
    availability
        .iter()
        .map(|entry| {
            let status = if entry.direct_executable_runnable {
                "available (direct)"
            } else if entry.availability_mode == "npm-exec" {
                "available (via npm exec)"
            } else if entry.direct_executable_exists {
                "not runnable"
            } else {
                "missing"
            };
            format!(
                "- {}: {} (checked: {})",
                entry.command,
                status,
                entry.checked_paths.join(", ")
            )
        })
        .collect::<Vec<_>>()
        .join("\n")
}

fn runtime_root_for_resolved_runtime(resolved: &ResolvedRuntime, store: &Store) -> Option<PathBuf> {
    match &resolved.target {
        ResolvedRuntimeTarget::Version { version } => Some(store.runtime_dir(version)),
        ResolvedRuntimeTarget::LinkedPath { path, .. } => Some(path.clone()),
    }
}

fn linked_runtime_details(resolved: &ResolvedRuntime) -> (Option<String>, Option<String>) {
    match &resolved.target {
        ResolvedRuntimeTarget::LinkedPath { name, path } => {
            (Some(name.clone()), Some(path.display().to_string()))
        }
        ResolvedRuntimeTarget::Version { .. } => (None, None),
    }
}

fn selected_existing_or_primary(candidates: &[PathBuf]) -> PathBuf {
    candidates
        .iter()
        .find(|path| path.exists())
        .cloned()
        .or_else(|| candidates.first().cloned())
        .unwrap_or_else(|| PathBuf::from("<none>"))
}

fn npm_exec_fallback_for_command(
    runtime_root: Option<&Path>,
    command: &str,
    direct_executable_exists: bool,
) -> Option<PathBuf> {
    if direct_executable_exists || !matches!(command, "yarn" | "pnpm") {
        return None;
    }

    let npm_path =
        selected_existing_or_primary(&runtime_executable_candidate_paths(runtime_root?, "npm"));

    runtime_executable_is_runnable(&npm_path).then_some(npm_path)
}

fn availability_mode(
    direct_executable_runnable: bool,
    npm_exec_fallback: &Option<PathBuf>,
) -> &'static str {
    if direct_executable_runnable {
        "direct"
    } else if npm_exec_fallback.is_some() {
        "npm-exec"
    } else {
        "unavailable"
    }
}
