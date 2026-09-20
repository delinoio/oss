use std::{
    collections::{BTreeMap, VecDeque},
    io::Read,
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
fn digest_reader(mut reader: impl Read) -> std::io::Result<String> {
    let mut hash = Sha256::new();
    let mut buffer = [0; 64 * 1024];
    loop {
        let count = reader.read(&mut buffer)?;
        if count == 0 {
            break;
        }
        hash.update(&buffer[..count]);
    }
    Ok(format!("{:x}", hash.finalize()))
}
pub fn slash(path: &Path) -> Result<String> {
    let path = path
        .to_str()
        .context("TaskFlow paths must be valid UTF-8")?;
    // Artifact paths use portable separators. A Unix backslash is a literal
    // filename byte, so replacing it would merge two distinct identities.
    #[cfg(unix)]
    ensure!(
        !path.contains('\\'),
        "TaskFlow Unix paths cannot contain literal backslashes"
    );
    Ok(path.replace('\\', "/"))
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
#[cfg(windows)]
mod windows;

pub fn canonical_path(path: &Path) -> Result<PathBuf> {
    slash(path)?;
    let mut ancestors = path.ancestors();
    if path.is_symlink() {
        // Preserve a link leaf for ownership validation against its parent.
        ancestors.next();
    }
    for ancestor in ancestors {
        #[cfg(windows)]
        let canonical = windows::canonicalize(ancestor);
        #[cfg(not(windows))]
        let canonical = ancestor.canonicalize();
        match canonical {
            Ok(canonical) => {
                slash(&canonical)?;
                let suffix = path.strip_prefix(ancestor)?;
                return Ok(if suffix.as_os_str().is_empty() {
                    canonical
                } else {
                    canonical.join(suffix)
                });
            }
            // Notifications can outlive a temporary file or directory. Avoid an
            // existence-check race, but never hide a broken link or access error.
            Err(error)
                if error.kind() == std::io::ErrorKind::NotFound && !ancestor.is_symlink() => {}
            Err(error) => return Err(error.into()),
        }
    }
    Ok(path.to_path_buf())
}
pub(crate) fn reserved_name(name: &str) -> bool {
    // These names cross cache/CI platform boundaries. Reserve their ASCII case
    // aliases even on a case-sensitive producer so they cannot target internal
    // state when consumed on a case-insensitive filesystem.
    let name = name.to_ascii_lowercase();
    matches!(name.as_str(), ".git" | ".taskflow") || name.starts_with(".taskflow-restore-")
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
pub fn relative_to(project: &Path, path: &Path) -> Result<String> {
    slash(project)?;
    slash(path)?;
    let common = project
        .ancestors()
        .find(|p| path.starts_with(p))
        .context("paths have no common ancestor")?;
    let mut relative = PathBuf::new();
    for _ in project.strip_prefix(common).unwrap().components() {
        relative.push("..");
    }
    relative.push(path.strip_prefix(common).unwrap());
    slash(&relative)
}
/// A directory notification may represent mutations to any descendant, and a
/// removed directory cannot be identified with a metadata query. Use positive
/// literal roots only to decide whether to rescan; the snapshot applies exact
/// globs, negative patterns, and output exclusions before enqueueing work.
pub fn input_event_may_match(project: &Project, task: &crate::config::Task, path: &Path) -> bool {
    if path
        .components()
        .any(|part| part.as_os_str().to_str().is_some_and(reserved_name))
    {
        return false;
    }
    let overlaps = |root: &Path| path.starts_with(root) || root.starts_with(path);
    if task.input.is_none() {
        return overlaps(&project.directory);
    }
    task.input.iter().flatten().any(|input| match input {
        Input::Auto(auto) => auto.auto && overlaps(&project.directory),
        Input::Pattern(pattern) if !pattern.starts_with('!') => {
            // Backslashes are glob escapes on some hosts. Preserve coverage
            // without guessing a narrower literal directory interpretation.
            pattern.contains('\\')
                || overlaps(&normalize(&project.directory.join(output_anchor(pattern))))
        }
        _ => false,
    })
}

pub fn input_matches(project: &Project, task: &crate::config::Task, path: &Path) -> Result<bool> {
    if path
        .components()
        .any(|component| component.as_os_str().to_str().is_some_and(reserved_name))
    {
        return Ok(false);
    }
    let relative = relative_to(&project.directory, path)?;
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
    Ok(matched && !output_matches(project, task, path)?)
}

pub(crate) fn output_matches(
    project: &Project,
    task: &crate::config::Task,
    path: &Path,
) -> Result<bool> {
    let relative = relative_to(&project.directory, path)?;
    if matches_patterns(task.output.as_deref().unwrap_or(&[]), &relative)? {
        return Ok(true);
    }
    // Complete trees own their root and descendants even when the directory
    // was deleted before an event is handled. Partial globs own matches only.
    Ok(task
        .output
        .iter()
        .flatten()
        .map(|output| output.strip_suffix("/**").unwrap_or(output))
        .filter(|output| !output.contains(['*', '?', '[', '{']) && !output.starts_with('!'))
        .any(|output| path.starts_with(normalize(&project.directory.join(output)))))
}
pub fn file_state(path: &Path) -> Result<String> {
    if path.is_symlink() {
        let target = std::fs::read_link(path)?;
        ensure!(
            !path.is_dir(),
            "directory symlinks require explicit underlying input paths"
        );
        let state = match std::fs::File::open(path) {
            Ok(file) => digest_reader(file)?,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => "missing".into(),
            Err(error) => return Err(error.into()),
        };
        return Ok(format!("link:{}:{state}", slash(&target)?));
    }
    if path.is_file() {
        Ok(digest_reader(std::fs::File::open(path)?)?)
    } else {
        Ok("missing".into())
    }
}

// Resolve links component by component even when the final target is missing.
// Canonicalization alone cannot distinguish an internal dangling link from an
// escaping one, and normalizing `..` before following links hides escapes.
fn validate_input_link(root: &Path, path: &Path) -> Result<()> {
    fn parts(path: &Path) -> Result<VecDeque<PathBuf>> {
        slash(path)?;
        path.components()
            .map(|part| match part {
                Component::Normal(value) => Ok(PathBuf::from(value)),
                Component::CurDir => Ok(PathBuf::from(".")),
                Component::ParentDir => Ok(PathBuf::from("..")),
                _ => anyhow::bail!("input link target must resolve inside workspace"),
            })
            .collect()
    }
    let mut pending = parts(path.strip_prefix(root)?)?;
    let mut resolved = root.to_path_buf();
    let mut links = 0;
    while let Some(part) = pending.pop_front() {
        if part == Path::new(".") {
            continue;
        }
        if part == Path::new("..") {
            ensure!(
                resolved != root && resolved.pop(),
                "input link escapes workspace"
            );
            continue;
        }
        let candidate = resolved.join(part);
        match std::fs::symlink_metadata(&candidate) {
            Ok(metadata) if metadata.is_symlink() => {
                links += 1;
                ensure!(links <= 40, "input link cycle or excessive link depth");
                let target = std::fs::read_link(&candidate)?;
                let mut expanded = if target.is_absolute() {
                    slash(&target)?;
                    // macOS /var and /private/var can name the same workspace.
                    // Enter at the first canonical workspace prefix, then keep
                    // all remaining components for ordinary link validation.
                    let mut entry = None;
                    for prefix in target.ancestors().collect::<Vec<_>>().into_iter().rev() {
                        match prefix.canonicalize() {
                            Ok(canonical) if canonical.starts_with(root) => {
                                entry = Some((canonical, prefix));
                                break;
                            }
                            Ok(_) => {}
                            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
                            Err(error) => return Err(error.into()),
                        }
                    }
                    let (canonical, prefix) = entry.context("input link escapes workspace")?;
                    resolved = canonical;
                    parts(target.strip_prefix(prefix)?)?
                } else {
                    parts(&target)?
                };
                expanded.append(&mut pending);
                pending = expanded;
            }
            Ok(_) => resolved = candidate,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => resolved = candidate,
            Err(error) => return Err(error.into()),
        }
    }
    Ok(())
}
pub fn input_state(
    ws: &Workspace,
    project: &Project,
    task: &crate::config::Task,
) -> Result<BTreeMap<String, String>> {
    let mut result = BTreeMap::new();
    let explicit_roots: Vec<_> = task
        .input
        .iter()
        .flatten()
        .filter_map(|input| {
            let Input::Pattern(pattern) = input else {
                return None;
            };
            if pattern.starts_with('!') {
                return None;
            }
            // A literal directory prefix safely prunes unrelated trees. Wildcards
            // and escaped literals need conservative traversal until full matching
            // can inspect the file; a directory-name substring is not a glob test.
            if pattern.contains('\\') {
                return Some(ws.root.clone());
            }
            Some(normalize(&project.directory.join(output_anchor(pattern))))
        })
        .collect();
    let automatic = task.input.is_none()
        || task
            .input
            .iter()
            .flatten()
            .any(|input| matches!(input, Input::Auto(value) if value.auto));
    let mut candidates = explicit_roots.clone();
    if automatic {
        candidates.push(project.directory.clone());
    }
    let mut roots = Vec::new();
    for mut root in candidates {
        if ws.root.starts_with(&root) {
            root = ws.root.clone();
        }
        if !root.starts_with(&ws.root) {
            continue;
        }
        // Starting a walker below a directory link must not bypass the existing
        // no-follow policy. Stop at its first link and let normal matching/link
        // validation decide whether that entry itself is an input.
        let mut prefix = ws.root.clone();
        for part in root.strip_prefix(&ws.root)?.components() {
            prefix.push(part);
            match std::fs::symlink_metadata(&prefix) {
                Ok(metadata) if metadata.file_type().is_symlink() => {
                    root = prefix;
                    break;
                }
                Ok(_) => {}
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => break,
                Err(error) => return Err(error.into()),
            }
        }
        if root
            .components()
            .any(|part| part.as_os_str().to_str().is_some_and(reserved_name))
        {
            continue;
        }
        roots.push(canonical_path(&root)?);
    }
    roots.sort();
    let mut scan_roots: Vec<PathBuf> = Vec::new();
    for root in roots {
        if !scan_roots.iter().any(|parent| root.starts_with(parent)) {
            scan_roots.push(root);
        }
    }
    tracing::debug!(
        automatic,
        roots = scan_roots.len(),
        "Scanning task input roots"
    );
    for root in scan_roots {
        for entry in walkdir::WalkDir::new(&root)
            .follow_links(false)
            .follow_root_links(false)
            .into_iter()
            .filter_entry(|entry| {
                if entry.file_name().to_str().is_some_and(reserved_name) {
                    return false;
                }
                !ignored_directory(entry.path())
                    || explicit_roots.iter().any(|prefix| {
                        prefix.starts_with(entry.path()) || entry.path().starts_with(prefix)
                    })
            })
        {
            let entry = match entry {
                Ok(entry) => entry,
                // A literal file or glob prefix may not exist yet. This is an empty
                // match, just as it was when reached from a workspace-wide walk.
                Err(error)
                    if error.depth() == 0
                        && error
                            .io_error()
                            .is_some_and(|e| e.kind() == std::io::ErrorKind::NotFound) =>
                {
                    continue
                }
                Err(error) => return Err(error.into()),
            };
            if !entry.file_type().is_file() && !entry.file_type().is_symlink() {
                continue;
            }
            if input_matches(project, task, entry.path())? {
                if entry.file_type().is_symlink() {
                    validate_input_link(&ws.root, entry.path())?;
                } else {
                    within(&ws.root, entry.path())?;
                }
                result.insert(
                    slash(entry.path().strip_prefix(&ws.root)?)?,
                    input_file_state(entry.path())?,
                );
            }
        }
    }
    for path in &ws.metadata_files {
        result.insert(
            slash(path.strip_prefix(&ws.root)?)?,
            input_file_state(path)?,
        );
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

#[cfg(test)]
mod hashing_tests {
    use super::*;

    #[test]
    fn input_digest_uses_bounded_reads_and_preserves_sha256_identity() {
        struct BoundedReader(std::io::Cursor<Vec<u8>>);
        impl Read for BoundedReader {
            fn read(&mut self, buffer: &mut [u8]) -> std::io::Result<usize> {
                assert!(buffer.len() <= 64 * 1024);
                self.0.read(buffer)
            }
        }
        let bytes = vec![42; 1024 * 1024 + 7];
        let expected = digest(&bytes);
        assert_eq!(
            digest_reader(BoundedReader(std::io::Cursor::new(bytes.clone()))).unwrap(),
            expected
        );
        let root = tempfile::tempdir().unwrap();
        let file = root.path().join("input");
        std::fs::write(&file, &bytes).unwrap();
        assert_eq!(file_state(&file).unwrap(), expected);
        #[cfg(unix)]
        {
            let link = root.path().join("link");
            std::os::unix::fs::symlink("input", &link).unwrap();
            assert_eq!(file_state(&link).unwrap(), format!("link:input:{expected}"));
        }
    }
}
