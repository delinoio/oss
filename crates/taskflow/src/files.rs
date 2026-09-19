use std::{
    collections::BTreeMap,
    path::{Component, Path, PathBuf},
};

use anyhow::{ensure, Context, Result};
use sha2::{Digest, Sha256};

use crate::{
    config::Input,
    discover::{Project, Workspace},
};

pub fn digest(bytes: &[u8]) -> String {
    format!("{:x}", Sha256::digest(bytes))
}
pub fn slash(path: &Path) -> String {
    path.to_string_lossy().replace('\\', "/")
}
pub fn normalize(path: &Path) -> PathBuf {
    let mut result = PathBuf::new();
    for part in path.components() {
        match part {
            Component::CurDir => {}
            Component::ParentDir => {
                result.pop();
            }
            _ => result.push(part.as_os_str()),
        }
    }
    result
}
/// Validate existing ancestors as well as lexical paths, including a missing
/// leaf.
pub fn within(root: &Path, path: &Path) -> Result<PathBuf> {
    let path = canonical_path(&normalize(path))?;
    ensure!(path.starts_with(root), "path escapes workspace");
    for ancestor in path.ancestors() {
        if ancestor.exists() || ancestor.is_symlink() {
            ensure!(
                ancestor.canonicalize()?.starts_with(root),
                "symlink escapes workspace"
            );
            break;
        }
    }
    Ok(path)
}
pub fn canonical_path(path: &Path) -> Result<PathBuf> {
    if path.is_symlink() {
        return Ok(path
            .parent()
            .context("symlink has no parent")?
            .canonicalize()?
            .join(path.file_name().unwrap()));
    }
    for ancestor in path.ancestors() {
        if ancestor.exists() || ancestor.is_symlink() {
            let canonical = ancestor.canonicalize()?;
            let suffix = path.strip_prefix(ancestor)?;
            return Ok(if suffix.as_os_str().is_empty() {
                canonical
            } else {
                canonical.join(suffix)
            });
        }
    }
    Ok(path.to_path_buf())
}
pub fn ignored_directory(path: &Path) -> bool {
    path.file_name().and_then(|n| n.to_str()).is_some_and(|n| {
        n.starts_with(".taskflow-restore-")
            || matches!(
                n,
                ".git"
                    | ".taskflow"
                    | "node_modules"
                    | "target"
                    | "dist"
                    | ".turbo"
                    | ".pnpm-store"
            )
    })
}
pub fn matches_patterns(patterns: &[String], path: &str) -> Result<bool> {
    let mut matched = false;
    for pattern in patterns {
        let (negative, pattern) = pattern
            .strip_prefix('!')
            .map_or((false, pattern.as_str()), |p| (true, p));
        if globset::Glob::new(pattern)?
            .compile_matcher()
            .is_match(path)
        {
            matched = !negative;
        }
    }
    Ok(matched)
}
pub fn relative_to(project: &Path, path: &Path) -> String {
    let common = project.ancestors().find(|p| path.starts_with(p)).unwrap();
    let mut relative = PathBuf::new();
    for _ in project.strip_prefix(common).unwrap().components() {
        relative.push("..");
    }
    relative.push(path.strip_prefix(common).unwrap());
    slash(&relative)
}
pub fn input_matches(project: &Project, task: &crate::config::Task, path: &Path) -> Result<bool> {
    if path
        .components()
        .any(|c| c.as_os_str() == ".taskflow" || c.as_os_str() == ".git")
    {
        return Ok(false);
    }
    let relative = relative_to(&project.directory, path);
    let mut matched = task.input.is_none() && path.starts_with(&project.directory);
    for input in task.input.iter().flatten() {
        match input {
            Input::Auto(auto) if auto.auto && path.starts_with(&project.directory) => {
                matched = !path
                    .strip_prefix(&project.directory)?
                    .components()
                    .any(|c| ignored_directory(Path::new(c.as_os_str())));
            }
            Input::Pattern(pattern) => {
                let (negative, pattern) = pattern
                    .strip_prefix('!')
                    .map_or((false, pattern.as_str()), |p| (true, p));
                if globset::Glob::new(pattern)?
                    .compile_matcher()
                    .is_match(&relative)
                {
                    matched = !negative;
                }
            }
            _ => {}
        }
    }
    if matches_patterns(task.output.as_deref().unwrap_or(&[]), &relative)? {
        return Ok(false);
    }
    // Exact directory outputs own descendants too. Without this check an
    // `output: [generated]` task would observe generated/file as its own input.
    if task
        .output
        .iter()
        .flatten()
        .any(|output| path.starts_with(normalize(&project.directory.join(output_anchor(output)))))
    {
        return Ok(false);
    }
    Ok(matched)
}
pub fn file_state(path: &Path) -> Result<String> {
    if path.is_symlink() {
        let target = std::fs::read_link(path)?;
        ensure!(
            !path.is_dir(),
            "directory symlinks require explicit underlying input paths"
        );
        return Ok(format!(
            "link:{}:{}",
            slash(&target),
            digest(&std::fs::read(path)?)
        ));
    }
    if path.is_file() {
        Ok(digest(&std::fs::read(path)?))
    } else {
        Ok("missing".into())
    }
}
pub fn input_state(
    ws: &Workspace,
    project: &Project,
    task: &crate::config::Task,
) -> Result<BTreeMap<String, String>> {
    let mut result = BTreeMap::new();
    for entry in walkdir::WalkDir::new(&ws.root).follow_links(false).into_iter().filter_entry(|e| {
        if matches!(e.file_name().to_str(), Some(".git" | ".taskflow")) { return false; }
        !ignored_directory(e.path()) || task.input.iter().flatten().any(|input| matches!(input, Input::Pattern(p) if !p.starts_with('!') && e.file_name().to_str().is_some_and(|name| p.contains(name))))
    }) {
        let entry = entry?;
        if !entry.file_type().is_file() && !entry.file_type().is_symlink() { continue; }
        if input_matches(project, task, entry.path())? {
            within(&ws.root, entry.path())?;
            result.insert(slash(entry.path().strip_prefix(&ws.root)?), input_file_state(entry.path())?);
        }
    }
    for path in &ws.metadata_files {
        result.insert(slash(path.strip_prefix(&ws.root)?), input_file_state(path)?);
    }
    Ok(result)
}

fn input_file_state(path: &Path) -> Result<String> {
    let state = file_state(path)?;
    #[cfg(unix)]
    if path.is_file() {
        use std::os::unix::fs::PermissionsExt;
        // Follow file links: the target's mode controls command execution too.
        // Keep portable CI structure fingerprints content-only; task input keys
        // already include the execution platform and must include its modes.
        let mode = std::fs::metadata(path)?.permissions().mode() & 0o7777;
        return Ok(format!("{mode:o}:{state}"));
    }
    Ok(state)
}
pub fn output_anchor(pattern: &str) -> PathBuf {
    let literal = pattern.split(['*', '?', '[', '{']).next().unwrap_or("");
    if literal.len() == pattern.len() {
        PathBuf::from(literal)
    } else if literal.ends_with('/') {
        PathBuf::from(literal.trim_end_matches('/'))
    } else {
        Path::new(literal)
            .parent()
            .unwrap_or(Path::new(""))
            .to_path_buf()
    }
}
pub fn atomic_write(path: &Path, bytes: &[u8]) -> Result<()> {
    let parent = path.parent().context("file has no parent")?;
    std::fs::create_dir_all(parent)?;
    let mut file = tempfile::NamedTempFile::new_in(parent)?;
    use std::io::Write;
    file.write_all(bytes)?;
    file.as_file().sync_all()?;
    file.persist(path).map_err(|e| e.error)?;
    Ok(())
}
