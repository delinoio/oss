use std::path::{Path, PathBuf};

use semver::Version;
use tracing::info;

use crate::{
    errors::{NodeupError, Result},
    overrides::OverrideStore,
    release_index::{normalize_version, ReleaseIndexClient},
    selectors::RuntimeSelector,
    store::Store,
    types::{OverrideLookupFallbackReason, RuntimeSelectorSource},
};

#[derive(Debug, Clone)]
pub enum ResolvedRuntimeTarget {
    Version { version: String },
    LinkedPath { name: String, path: PathBuf },
}

#[derive(Debug, Clone)]
pub struct ResolvedRuntime {
    pub source: RuntimeSelectorSource,
    pub selector: RuntimeSelector,
    pub target: ResolvedRuntimeTarget,
}

impl ResolvedRuntime {
    pub fn runtime_id(&self) -> String {
        match &self.target {
            ResolvedRuntimeTarget::Version { version } => version.clone(),
            ResolvedRuntimeTarget::LinkedPath { name, .. } => name.clone(),
        }
    }

    pub fn executable_path(&self, store: &Store, command: &str) -> PathBuf {
        match &self.target {
            ResolvedRuntimeTarget::Version { version } => {
                store.runtime_executable(version, command)
            }
            ResolvedRuntimeTarget::LinkedPath { path, .. } => path.join("bin").join(command),
        }
    }

    pub fn version_if_any(&self) -> Option<&str> {
        match &self.target {
            ResolvedRuntimeTarget::Version { version } => Some(version.as_str()),
            ResolvedRuntimeTarget::LinkedPath { .. } => None,
        }
    }

    pub fn is_installed(&self, store: &Store) -> bool {
        match &self.target {
            ResolvedRuntimeTarget::Version { version } => store.is_installed(version),
            ResolvedRuntimeTarget::LinkedPath { path, .. } => path.exists(),
        }
    }
}

#[derive(Debug, Clone)]
pub struct RuntimeResolver {
    store: Store,
    overrides: OverrideStore,
    releases: ReleaseIndexClient,
}

impl RuntimeResolver {
    pub fn new(store: Store, overrides: OverrideStore, releases: ReleaseIndexClient) -> Self {
        Self {
            store,
            overrides,
            releases,
        }
    }

    pub fn resolve_with_precedence(
        &self,
        explicit_selector: Option<&str>,
        path: &Path,
    ) -> Result<ResolvedRuntime> {
        if let Some(selector) = explicit_selector {
            return self.resolve_selector_with_source(selector, RuntimeSelectorSource::Explicit);
        }

        if let Some(override_entry) = self.overrides.resolve_for_path(path)? {
            info!(
                command_path = "nodeup.resolve.override",
                path = %path.display(),
                matched = true,
                matched_path = %override_entry.path,
                fallback_reason = OverrideLookupFallbackReason::OverrideMatched.as_str(),
                selector = %override_entry.selector,
                "Resolved runtime selector from override"
            );
            return self.resolve_selector_with_source(
                &override_entry.selector,
                RuntimeSelectorSource::Override,
            );
        }

        let settings = self.store.load_settings()?;
        if let Some(selector) = settings.default_selector {
            info!(
                command_path = "nodeup.resolve.override",
                path = %path.display(),
                matched = false,
                fallback_reason = OverrideLookupFallbackReason::FallbackToDefault.as_str(),
                "No directory override matched; falling back to default selector"
            );
            return self.resolve_selector_with_source(&selector, RuntimeSelectorSource::Default);
        }

        info!(
            command_path = "nodeup.resolve.override",
            path = %path.display(),
            matched = false,
            fallback_reason = OverrideLookupFallbackReason::NoDefaultSelector.as_str(),
            "No directory override or default selector found"
        );

        Err(NodeupError::not_found(
            "No runtime selector resolved. Set a default runtime or directory override",
        ))
    }

    pub fn resolve_selector_with_source(
        &self,
        selector_value: &str,
        source: RuntimeSelectorSource,
    ) -> Result<ResolvedRuntime> {
        let selector = RuntimeSelector::parse(selector_value)?;
        let target = match &selector {
            RuntimeSelector::Version(version) => ResolvedRuntimeTarget::Version {
                version: normalize_version(&version.to_string()),
            },
            RuntimeSelector::Channel(channel) => ResolvedRuntimeTarget::Version {
                version: self.releases.resolve_channel(*channel)?,
            },
            RuntimeSelector::LinkedName(name) => {
                let settings = self.store.load_settings()?;
                let path = settings.linked_runtimes.get(name).ok_or_else(|| {
                    NodeupError::not_found(format!("Linked runtime '{name}' does not exist"))
                })?;

                ResolvedRuntimeTarget::LinkedPath {
                    name: name.clone(),
                    path: PathBuf::from(path),
                }
            }
        };

        info!(
            command_path = "nodeup.resolve.selector",
            selector_source = source.as_str(),
            selector = %selector,
            resolved_runtime = %runtime_id_for_target(&target),
            "Resolved runtime selector"
        );

        Ok(ResolvedRuntime {
            source,
            selector,
            target,
        })
    }

    pub fn newer_versions_than(&self, current_version: &str) -> Result<Vec<String>> {
        let normalized = normalize_version(current_version);
        let current = Version::parse(normalized.trim_start_matches('v'))?;

        let mut newer = Vec::new();
        for entry in self.releases.fetch_index()? {
            let parsed = match Version::parse(entry.version.trim_start_matches('v')) {
                Ok(version) => version,
                Err(_) => continue,
            };
            if parsed > current {
                newer.push(entry.version);
            }
        }

        Ok(newer)
    }
}

fn runtime_id_for_target(target: &ResolvedRuntimeTarget) -> String {
    match target {
        ResolvedRuntimeTarget::Version { version } => version.clone(),
        ResolvedRuntimeTarget::LinkedPath { name, .. } => name.clone(),
    }
}

#[cfg(test)]
mod tests {
    use std::{
        fs,
        time::{SystemTime, UNIX_EPOCH},
    };

    use super::*;
    use crate::paths::NodeupPaths;

    fn temp_paths(label: &str) -> NodeupPaths {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let root = std::env::temp_dir().join(format!("nodeup-resolver-{label}-{nonce}"));

        NodeupPaths {
            data_root: root.join("data"),
            cache_root: root.join("cache"),
            config_root: root.join("config"),
            toolchains_dir: root.join("data").join("toolchains"),
            downloads_dir: root.join("cache").join("downloads"),
            settings_file: root.join("config").join("settings.toml"),
            overrides_file: root.join("config").join("overrides.toml"),
        }
    }

    #[test]
    fn resolution_prefers_explicit_selector() {
        let paths = temp_paths("explicit");
        paths.ensure_layout().unwrap();

        let store = Store::new(paths.clone());
        let mut settings = store.load_settings().unwrap();
        settings.default_selector = Some("lts".to_string());
        store.save_settings(&settings).unwrap();

        let overrides = OverrideStore::new(paths.clone());
        let test_path = paths.data_root.join("workspace");
        fs::create_dir_all(&test_path).unwrap();
        overrides.set(&test_path, "v20.0.0").unwrap();

        std::env::set_var(
            "NODEUP_INDEX_URL",
            "https://nodejs.org/download/release/index.json",
        );
        let release_client = ReleaseIndexClient::new().unwrap();
        let resolver = RuntimeResolver::new(store, overrides, release_client);

        let resolved = resolver
            .resolve_with_precedence(Some("v22.0.0"), &test_path)
            .unwrap();

        assert_eq!(resolved.runtime_id(), "v22.0.0");

        let _ = fs::remove_dir_all(paths.data_root.parent().unwrap().parent().unwrap());
    }
}
