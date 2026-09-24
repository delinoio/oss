use std::{
    collections::BTreeSet,
    fs, io,
    path::{Component, Path, PathBuf},
};

use globset::{Glob, GlobSet, GlobSetBuilder};

#[cfg(target_os = "linux")]
use crate::trace::{EncodedPath, PathEncoding, PathScope};

pub struct Selector {
    root: PathBuf,
    includes: GlobSet,
    excludes: GlobSet,
}

impl Selector {
    pub fn new(
        root: &Path,
        includes: &[String],
        excludes: &[String],
    ) -> Result<Self, &'static str> {
        if includes.is_empty() {
            return Err("At least one --include selector is required.");
        }
        let root = fs::canonicalize(root).map_err(|_| "Cannot resolve --root.")?;
        if !root.is_dir() {
            return Err("--root must name an existing directory.");
        }
        Ok(Self {
            root,
            includes: compile(includes)?,
            excludes: compile(excludes)?,
        })
    }

    #[cfg(target_os = "linux")]
    pub fn root(&self) -> &Path {
        &self.root
    }

    pub fn matches(&self, relative: &Path) -> bool {
        self.includes.is_match(relative) && !self.excludes.is_match(relative)
    }

    #[cfg(target_os = "linux")]
    pub fn resolve_trace_path(&self, path: &EncodedPath) -> Option<PathBuf> {
        if path.scope != PathScope::Project || path.encoding != PathEncoding::UnixBytes {
            return None;
        }
        #[cfg(unix)]
        {
            use std::os::unix::ffi::OsStrExt as _;
            let bytes = path.decode().ok()?;
            let relative = Path::new(std::ffi::OsStr::from_bytes(&bytes));
            if relative.is_absolute()
                || relative
                    .components()
                    .any(|part| matches!(part, Component::ParentDir))
                || !self.matches(relative)
            {
                return None;
            }
            let resolved = fs::canonicalize(self.root.join(relative)).ok()?;
            resolved.starts_with(&self.root).then_some(resolved)
        }
        #[cfg(not(unix))]
        {
            None
        }
    }

    pub fn selected_files(&self) -> io::Result<Vec<SelectedFile>> {
        let mut selected = Vec::new();
        let mut ancestor_directories = BTreeSet::new();
        self.visit(&self.root, &mut ancestor_directories, &mut selected)?;
        selected.sort_by(|left, right| left.logical.cmp(&right.logical));
        Ok(selected)
    }

    fn visit(
        &self,
        directory: &Path,
        ancestor_directories: &mut BTreeSet<PathBuf>,
        selected: &mut Vec<SelectedFile>,
    ) -> io::Result<()> {
        let physical = fs::canonicalize(directory)?;
        if !physical.starts_with(&self.root) || !ancestor_directories.insert(physical.clone()) {
            return Ok(());
        }
        for entry in fs::read_dir(directory)? {
            let entry = entry?;
            let logical = entry.path();
            let metadata = match fs::metadata(&logical) {
                Ok(metadata) => metadata,
                Err(error) if error.kind() == io::ErrorKind::NotFound => continue,
                Err(error) => return Err(error),
            };
            let physical = fs::canonicalize(&logical)?;
            if !physical.starts_with(&self.root) {
                continue;
            }
            if metadata.is_dir() {
                self.visit(&logical, ancestor_directories, selected)?;
            } else if metadata.is_file()
                && let Ok(relative) = logical.strip_prefix(&self.root)
                && self.matches(relative)
            {
                selected.push(SelectedFile {
                    logical: relative.to_path_buf(),
                    physical,
                    initially_empty: metadata.len() == 0,
                });
            }
        }
        ancestor_directories.remove(&physical);
        Ok(())
    }
}

pub struct SelectedFile {
    pub logical: PathBuf,
    pub physical: PathBuf,
    pub initially_empty: bool,
}

fn compile(patterns: &[String]) -> Result<GlobSet, &'static str> {
    let mut builder = GlobSetBuilder::new();
    for pattern in patterns {
        let path = Path::new(pattern);
        if pattern.is_empty()
            || path.is_absolute()
            || path
                .components()
                .any(|part| matches!(part, Component::ParentDir | Component::Prefix(_)))
        {
            return Err("Selectors must be nonempty project-relative globs without '..'.");
        }
        let glob = Glob::new(pattern).map_err(|_| "Invalid glob selector.")?;
        builder.add(glob);
    }
    builder.build().map_err(|_| "Invalid glob selector.")
}

#[cfg(test)]
mod tests {
    use std::fs;

    use super::Selector;

    #[test]
    fn rejects_escaping_and_invalid_globs() {
        let root = tempfile::tempdir().unwrap();
        for pattern in ["../secret", "/absolute/**", "[unclosed", ""] {
            assert!(Selector::new(root.path(), &[pattern.to_owned()], &[]).is_err());
        }
    }

    #[cfg(unix)]
    #[test]
    fn follows_internal_aliases_and_skips_external_links() {
        use std::os::unix::fs::symlink;

        let root = tempfile::tempdir().unwrap();
        let external = tempfile::tempdir().unwrap();
        fs::create_dir(root.path().join("assets")).unwrap();
        fs::write(root.path().join("assets/file.dat"), b"content").unwrap();
        fs::write(external.path().join("secret.dat"), b"secret").unwrap();
        symlink("assets", root.path().join("alias")).unwrap();
        symlink(
            external.path().join("secret.dat"),
            root.path().join("outside"),
        )
        .unwrap();
        let selector = Selector::new(
            root.path(),
            &[
                "assets/**".to_owned(),
                "alias/**".to_owned(),
                "outside".to_owned(),
            ],
            &[],
        )
        .unwrap();
        let files = selector.selected_files().unwrap();
        assert_eq!(files.len(), 2);
        assert_eq!(files[0].physical, files[1].physical);
    }
}
