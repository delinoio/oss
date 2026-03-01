pub mod cli;
pub mod commands;
pub mod dispatch;
pub mod errors;
pub mod installer;
pub mod logging;
pub mod overrides;
pub mod paths;
pub mod process;
pub mod release_index;
pub mod resolver;
pub mod selectors;
pub mod store;
pub mod types;

use errors::Result;
use installer::RuntimeInstaller;
use overrides::OverrideStore;
use paths::NodeupPaths;
use release_index::ReleaseIndexClient;
use resolver::RuntimeResolver;
use store::Store;

#[derive(Debug, Clone)]
pub struct NodeupApp {
    pub paths: NodeupPaths,
    pub store: Store,
    pub overrides: OverrideStore,
    pub releases: ReleaseIndexClient,
    pub installer: RuntimeInstaller,
    pub resolver: RuntimeResolver,
}

impl NodeupApp {
    pub fn new() -> Result<Self> {
        let paths = NodeupPaths::detect()?;
        paths.ensure_layout()?;

        let store = Store::new(paths.clone());
        let overrides = OverrideStore::new(paths.clone());
        let release_index_ttl = ReleaseIndexClient::cache_ttl_from_env();
        let releases =
            ReleaseIndexClient::new(paths.release_index_cache_file.clone(), release_index_ttl)?;
        let installer = RuntimeInstaller::new(paths.clone());
        let resolver = RuntimeResolver::new(store.clone(), overrides.clone(), releases.clone());

        Ok(Self {
            paths,
            store,
            overrides,
            releases,
            installer,
            resolver,
        })
    }
}
