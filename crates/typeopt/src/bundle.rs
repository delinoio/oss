// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Stage 2 publication: writes edited bundler chunks next to an unchanged
//! copy of the bundle directory, composes the bundler's source maps with the
//! accepted edits and renames edited content-hashed chunks.
use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
};

use anyhow::{Context, Result, ensure};

use crate::{
    compiler::Snapshot,
    output::{byte_at_utf16, coordinate, line_starts},
    pipeline::Outcome,
    plan::Edit,
};

/// Maps an offset of a round's input text to the round's output text.
/// Offsets inside a replaced range have no counterpart, except the range
/// start, which maps to the start of its replacement.
fn forward_offset(offset: usize, edits: &[Edit]) -> Option<usize> {
    let mut delta: isize = 0;
    for edit in edits {
        let growth = edit.replacement.len() as isize - (edit.end - edit.start) as isize;
        if edit.start == edit.end {
            // Insertions before (or at) the offset shift it.
            if edit.start > offset {
                break;
            }
            delta += growth;
        } else if edit.end <= offset {
            delta += growth;
        } else if edit.start == offset {
            break;
        } else if edit.start < offset {
            return None;
        } else {
            break;
        }
    }
    Some((offset as isize + delta) as usize)
}

/// Rewrites the bundler's map of a chunk for the edited chunk text: every
/// token moves through each round's edits; tokens inside replaced code are
/// dropped, so inserted code maps to the original range it replaced.
fn compose_bundle_map(
    map: &[u8],
    original: &str,
    changed: &str,
    chain: &[Vec<Edit>],
) -> Result<Vec<u8>> {
    let source_map = sourcemap::SourceMap::from_slice(map).context("invalid chunk source map")?;
    let original_starts = line_starts(original);
    let changed_starts = line_starts(changed);
    let mut builder = sourcemap::SourceMapBuilder::new(source_map.get_file());
    for (index, source) in source_map.sources().enumerate() {
        let id = builder.add_source(source);
        builder.set_source_contents(id, source_map.get_source_contents(index as u32));
    }
    'tokens: for token in source_map.tokens() {
        let mut offset = byte_at_utf16(
            original,
            &original_starts,
            token.get_dst_line(),
            token.get_dst_col(),
        );
        for edits in chain {
            match forward_offset(offset, edits) {
                Some(next) => offset = next,
                None => continue 'tokens,
            }
        }
        if offset > changed.len() {
            continue;
        }
        let (line, col) = coordinate(changed, &changed_starts, offset);
        builder.add(
            line,
            col,
            token.get_src_line(),
            token.get_src_col(),
            token.get_source(),
            token.get_name(),
            false,
        );
    }
    let mut bytes = Vec::new();
    builder.into_sourcemap().to_writer(&mut bytes)?;
    Ok(bytes)
}

/// `<name>-<hash>.<ext>` or `<name>.<hash>.<ext>` with an 8-character
/// base64url content hash, as Vite and Rollup name chunks. A hash must
/// contain a digit, an uppercase letter, `-` or `_`, so ordinary words such
/// as `polyfill` are not mistaken for one.
fn hashed_name(file: &str) -> Option<(&str, &str, char)> {
    let (stem, ext) = file.rsplit_once('.')?;
    if !matches!(ext, "js" | "mjs") {
        return None;
    }
    // The hash alphabet includes `-`, so split at a fixed width.
    let at = stem.len().checked_sub(9)?;
    if !stem.is_char_boundary(at) {
        return None;
    }
    let separator = stem[at..].chars().next()?;
    if !matches!(separator, '-' | '.') {
        return None;
    }
    let (name, hash) = (&stem[..at], &stem[at + 1..]);
    let alphabet = hash
        .bytes()
        .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-');
    let random = hash
        .bytes()
        .any(|b| b.is_ascii_digit() || b.is_ascii_uppercase() || b == b'_' || b == b'-');
    (!name.is_empty() && hash.len() == 8 && alphabet && random).then_some((name, ext, separator))
}

fn is_text_asset(path: &Path) -> bool {
    path.extension().is_some_and(|e| {
        matches!(
            e.to_str(),
            Some("js" | "mjs" | "cjs" | "html" | "css" | "json" | "map" | "webmanifest" | "txt")
        )
    })
}

fn files_under(dir: &Path, out: &mut Vec<PathBuf>) -> Result<()> {
    for entry in std::fs::read_dir(dir)? {
        let entry = entry?;
        if entry.file_type()?.is_dir() {
            files_under(&entry.path(), out)?;
        } else {
            out.push(entry.path());
        }
    }
    Ok(())
}

/// Gives every edited content-hashed chunk, and every hashed chunk that
/// references a renamed one, a new hash, and rewrites the references in all
/// text assets. Browsers cache hashed chunks immutably; an edited chunk that
/// kept its name could otherwise meet a stale copy of a chunk it depends on.
fn rehash(root: &Path, edited: &BTreeSet<PathBuf>) -> Result<usize> {
    let mut files = Vec::new();
    files_under(root, &mut files)?;
    files.sort();
    let mut texts = BTreeMap::new();
    for file in &files {
        if is_text_asset(file)
            && let Ok(text) = std::fs::read_to_string(file)
        {
            texts.insert(file.clone(), text);
        }
    }
    let base = |p: &Path| p.file_name().unwrap().to_string_lossy().into_owned();
    let mut renamed = BTreeMap::<String, String>::new();
    let mut pending: Vec<PathBuf> = edited
        .iter()
        .filter(|p| hashed_name(&base(p)).is_some())
        .cloned()
        .collect();
    let mut seen = BTreeSet::new();
    while let Some(file) = pending.pop() {
        if !seen.insert(file.clone()) {
            continue;
        }
        let old = base(&file);
        let (name, ext, separator) = hashed_name(&old).expect("filtered to hashed names");
        // Uniform base64url characters from the digest bytes, like a real
        // content hash (a hex-derived name would compress unusually well),
        // and recognizable as a hash by a later run.
        let mut new_name = String::new();
        for salt in 0u32.. {
            use sha2::{Digest, Sha256};
            let digest = Sha256::digest(format!("{old}\0{salt}\0{}", texts[&file]).as_bytes());
            let alphabet = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
            let hash: String = digest[..8]
                .iter()
                .map(|&b| alphabet[(b % 64) as usize] as char)
                .collect();
            new_name = format!("{name}{separator}{hash}.{ext}");
            if hashed_name(&new_name).is_some() && new_name != old {
                break;
            }
        }
        renamed.insert(old.clone(), new_name);
        for (other, text) in &texts {
            if hashed_name(&base(other)).is_some() && text.contains(&old) {
                pending.push(other.clone());
            }
        }
    }
    if renamed.is_empty() {
        return Ok(0);
    }
    for (file, text) in &texts {
        crate::app::check_cancelled()?;
        let mut updated = text.clone();
        for (old, new) in &renamed {
            if updated.contains(old.as_str()) {
                updated = updated.replace(old.as_str(), new);
            }
        }
        if &updated != text {
            std::fs::write(file, updated).context("cannot rewrite a chunk reference")?;
        }
    }
    for file in &files {
        let name = base(file);
        let (stem, suffix) = match name.strip_suffix(".map") {
            Some(stem) => (stem.to_owned(), ".map"),
            None => (name.clone(), ""),
        };
        if let Some(new) = renamed.get(&stem) {
            std::fs::rename(file, file.with_file_name(format!("{new}{suffix}")))
                .context("cannot rename a rehashed chunk")?;
        }
    }
    Ok(renamed.len())
}

/// Stage 2 publication: copies the bundler output directory, replaces the
/// edited chunks, composes their source maps with the edits and gives edited
/// content-hashed chunks (and their importers) new hashes.
pub fn publish(
    source: &Path,
    destination: &Path,
    snapshot: &Snapshot,
    outcome: &Outcome,
) -> Result<()> {
    ensure!(
        std::fs::symlink_metadata(destination)
            .is_err_and(|e| e.kind() == std::io::ErrorKind::NotFound),
        "output destination already exists or cannot be inspected"
    );
    let parent = destination
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    let staging = tempfile::Builder::new()
        .prefix(".typeopt-bundle-")
        .tempdir_in(parent)
        .context("cannot create staging directory")?;
    fn copy(from: &Path, to: &Path) -> Result<()> {
        std::fs::create_dir_all(to)?;
        for entry in std::fs::read_dir(from)? {
            let entry = entry?;
            let target = to.join(entry.file_name());
            if entry.file_type()?.is_dir() {
                copy(&entry.path(), &target)?;
            } else {
                std::fs::copy(entry.path(), &target)?;
            }
        }
        Ok(())
    }
    let root = staging.path().join("out");
    copy(source, &root).context("cannot copy bundle directory")?;
    // Subresource Integrity digests would no longer match edited chunks.
    let mut files = Vec::new();
    files_under(&root, &mut files)?;
    for file in &files {
        if file.extension().is_some_and(|e| e == "html")
            && std::fs::read_to_string(file).is_ok_and(|html| html.contains("integrity="))
        {
            anyhow::bail!(
                "HTML pages use Subresource Integrity, which bundle mode cannot update; compute \
                 integrity attributes after typeopt"
            );
        }
    }
    let mut edited = BTreeSet::new();
    let (mut maps, mut dropped) = (0usize, 0usize);
    for (name, text) in &outcome.texts {
        crate::app::check_cancelled()?;
        let original = &snapshot.sources[name];
        if original == text {
            continue;
        }
        let relative = Path::new(name)
            .strip_prefix(source)
            .context("edited chunk is outside the bundle directory")?;
        let target = root.join(relative);
        std::fs::write(&target, text).context("cannot write an edited chunk")?;
        edited.insert(target.clone());
        let map = target.with_file_name(format!(
            "{}.map",
            target.file_name().unwrap().to_string_lossy()
        ));
        if let Ok(bytes) = std::fs::read(&map) {
            let chain = outcome.chains.get(name).map_or(&[][..], Vec::as_slice);
            match compose_bundle_map(&bytes, original, text, chain) {
                Ok(composed) => {
                    std::fs::write(&map, composed).context("cannot write a chunk source map")?;
                    maps += 1;
                }
                Err(error) => {
                    // A map that no longer describes the chunk is worse than
                    // none.
                    tracing::warn!(%error, "dropping an unreadable chunk source map");
                    std::fs::remove_file(&map)?;
                    dropped += 1;
                }
            }
        }
    }
    let rehashed = if crate::app::keep_chunk_names() {
        0
    } else {
        rehash(&root, &edited)?
    };
    tracing::info!(
        edited = edited.len(),
        maps,
        dropped_maps = dropped,
        rehashed,
        "bundle chunks written"
    );
    std::fs::rename(&root, destination).context("cannot publish bundle output")?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::hashed_name;

    #[test]
    fn recognizes_vite_and_rollup_hash_patterns() {
        assert_eq!(hashed_name("index-DnKDBunT.js"), Some(("index", "js", '-')));
        assert_eq!(hashed_name("index.D-Q9VWEY.js"), Some(("index", "js", '.')));
        assert_eq!(
            hashed_name("vendor-a1b2c3d4.mjs"),
            Some(("vendor", "mjs", '-'))
        );
        assert_eq!(hashed_name("worker.polyfill.js"), None);
        assert_eq!(hashed_name("react-dom-client.js"), None);
        assert_eq!(hashed_name("style-DnKDBunT.css"), None);
        assert_eq!(hashed_name("-DnKDBunT.js"), None);
    }
}
