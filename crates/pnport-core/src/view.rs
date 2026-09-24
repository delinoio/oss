use std::{
    collections::HashMap,
    fs,
    path::{Path, PathBuf},
};

use pnp::fs::{VPath, VPathInfo};

use crate::{
    cache::{Cache, Lease},
    diagnostic::{cache_error, Code, Error, Result},
    graph::{check_conflict, digest, normalize, Graph},
};

pub struct View {
    pub graph: Graph,
    pub cache: Cache,
    pub session: PathBuf,
    leases: HashMap<PathBuf, Lease>,
}
#[derive(Clone, Debug)]
pub struct Translation {
    pub logical: PathBuf,
    pub physical: PathBuf,
    pub readonly: bool,
    pub virtual_link: bool,
}

impl View {
    pub fn new(graph: Graph, cache: Cache, session: PathBuf) -> Self {
        Self {
            graph,
            cache,
            session,
            leases: HashMap::new(),
        }
    }

    pub fn translate(&mut self, path: &Path) -> Result<Translation> {
        self.translate_with_wait(path, &mut || Ok(()))
    }

    pub fn translate_with_wait(
        &mut self,
        path: &Path,
        wait: &mut dyn FnMut() -> Result<()>,
    ) -> Result<Translation> {
        if !path.is_absolute() {
            return Err(Error::new(
                Code::PnportUnsupportedOperation,
                "Filesystem translation requires an absolute path.",
            ));
        }
        let path = normalize(path);
        // Physical cache and session views can contain their own node_modules
        // trees. They are backing storage, never a second virtual dependency
        // lookup, even when the cache lives below the project directory.
        if path.starts_with(&self.cache.root) || path.starts_with(self.session.join("views")) {
            return self.backing(path, false, false, wait);
        }
        // Yarn's unplugged containers include a real node_modules before the
        // package locator. Those ancestors are installation structure, not an
        // issuer's virtual dependency directory.
        if self.graph.is_location_ancestor(&path) {
            return self.backing(path, false, false, wait);
        }

        // Locations already in the graph (including ZIP-internal node_modules)
        // must not be interpreted as a second dependency lookup.
        if let Some(package) = self.graph.package(&path) {
            let location = normalize(&package.package_location);
            if path.starts_with(&location) {
                let relative = path.strip_prefix(&location).unwrap_or(Path::new(""));
                if !relative
                    .components()
                    .any(|p| p.as_os_str() == "node_modules")
                {
                    return self.backing(path, false, false, wait);
                }
            }
        }
        let components: Vec<_> = path.components().collect();
        let mut prefix = PathBuf::new();
        for (i, part) in components.iter().enumerate() {
            if part.as_os_str() != "node_modules" || self.graph.package(&prefix).is_none() {
                prefix.push(part);
                continue;
            }
            // A ZIP already contains its own package prefix; only interpret a
            // node_modules below the containing locator as a dependency view.
            if let Some(package) = self.graph.package(&path) {
                if normalize(&package.package_location).starts_with(prefix.join("node_modules")) {
                    prefix.push(part);
                    continue;
                }
            }
            if matches!(VPath::from(&prefix), Ok(VPath::Native(_))) {
                check_conflict(&prefix.join("node_modules"))?;
            }
            let remaining = &components[i + 1..];
            if remaining.is_empty() {
                return self.directory(&prefix, None);
            }
            let first = remaining[0]
                .as_os_str()
                .to_str()
                .ok_or_else(|| Error::new(Code::PnportResolutionFailed, "Invalid package name."))?;
            let (name, consumed) = if first.starts_with('@') {
                if remaining.len() == 1 {
                    return self.directory(&prefix, Some(first));
                }
                (
                    format!(
                        "{first}/{}",
                        remaining[1].as_os_str().to_str().ok_or_else(cache_error)?
                    ),
                    2,
                )
            } else {
                (first.to_string(), 1)
            };
            let target = self.graph.resolve(&name, &prefix)?;
            let mut target = normalize(&target);
            for part in &remaining[consumed..] {
                target.push(part);
            }
            let mut translated = self.translate_with_wait(&target, wait)?;
            translated.readonly = true;
            translated.virtual_link = remaining.len() == consumed;
            return Ok(translated);
        }
        self.backing(path, false, false, wait)
    }

    fn backing(
        &mut self,
        logical: PathBuf,
        readonly: bool,
        virtual_link: bool,
        wait: &mut dyn FnMut() -> Result<()>,
    ) -> Result<Translation> {
        let (physical, managed) = match VPath::from(&logical).map_err(|_| cache_error())? {
            VPath::Native(path) => {
                // Materialized bytes remain read-only even if a child reaches
                // their private backing path through /proc/self/fd or a saved
                // absolute pathname instead of the logical ZIP location.
                let managed = self.graph.managed(&path)
                    || path.starts_with(&self.cache.root)
                    || path.starts_with(self.session.join("views"));
                (path, managed)
            }
            VPath::Virtual(info) => (normalize(&info.physical_base_path()), true),
            VPath::Zip(info) => {
                let archive = normalize(&info.physical_base_path());
                if !self.leases.contains_key(&archive) {
                    let lease = self.cache.materialize_with_wait(&archive, wait)?;
                    let active = self.session.join("active");
                    fs::create_dir_all(&active).map_err(|_| cache_error())?;
                    let bytes = serde_json::to_vec(&crate::graph::Input {
                        path: archive.clone(),
                        sha256: lease.sha256.clone(),
                    })
                    .map_err(|_| cache_error())?;
                    let marker = active.join(digest(&bytes));
                    let temp =
                        tempfile::NamedTempFile::new_in(&active).map_err(|_| cache_error())?;
                    fs::write(temp.path(), bytes).map_err(|_| cache_error())?;
                    temp.persist(&marker).map_err(|_| cache_error())?;
                    self.leases.insert(archive.clone(), lease);
                }
                let lease = &self.leases[&archive];
                (lease.content.join(info.zip_path), true)
            }
        };
        Ok(Translation {
            logical,
            physical,
            readonly: readonly || managed,
            virtual_link,
        })
    }

    fn directory(&mut self, issuer: &Path, scope: Option<&str>) -> Result<Translation> {
        let names = self.graph.dependency_names(issuer);
        let base = self
            .session
            .join("views")
            .join(digest(issuer.to_string_lossy().as_bytes()));
        let destination = base.join("node_modules");
        if !destination.exists() {
            fs::create_dir_all(&base).map_err(|_| cache_error())?;
            let stage = tempfile::Builder::new()
                .prefix("view-")
                .tempdir_in(&base)
                .map_err(|_| cache_error())?;
            for name in names {
                let link = stage.path().join(&name);
                fs::create_dir_all(link.parent().ok_or_else(cache_error)?)
                    .map_err(|_| cache_error())?;
                let target = self.graph.resolve(&name, issuer)?;
                #[cfg(unix)]
                std::os::unix::fs::symlink(target, link).map_err(|_| cache_error())?;
                #[cfg(windows)]
                {
                    let _ = target;
                    fs::create_dir(link).map_err(|_| cache_error())?;
                }
            }
            match fs::rename(stage.path(), &destination) {
                Ok(()) => {}
                Err(_) if destination.is_dir() => {}
                Err(_) => return Err(cache_error()),
            }
        }
        let mut logical = issuer.join("node_modules");
        let mut physical = destination;
        if let Some(scope) = scope {
            logical.push(scope);
            physical.push(scope);
        }
        Ok(Translation {
            logical,
            physical,
            readonly: true,
            virtual_link: false,
        })
    }
}
