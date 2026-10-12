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

/// One HTML start tag: lowercase name and attributes as lowercase name and
/// raw value (without quotes; empty for boolean attributes).
pub(crate) struct HtmlTag {
    pub name: String,
    pub attributes: Vec<(String, String)>,
}

impl HtmlTag {
    pub fn attribute(&self, name: &str) -> Option<&str> {
        self.attributes
            .iter()
            .find(|(n, _)| n == name)
            .map(|(_, v)| v.as_str())
    }
}

/// Start tags of an HTML document. Comments and the contents of raw-text
/// elements (`script`, `style`, `textarea`, `title`) are skipped; attribute
/// names are case-insensitive and may have whitespace around `=`; values may be
/// double-quoted, single-quoted or unquoted.
pub(crate) fn html_tags(html: &str) -> Vec<HtmlTag> {
    let bytes = html.as_bytes();
    let mut tags = Vec::new();
    let mut i = 0;
    while let Some(offset) = html[i..].find('<') {
        i += offset + 1;
        if html[i..].starts_with("!--") {
            i = html[i..].find("-->").map_or(html.len(), |end| i + end + 3);
            continue;
        }
        let start = i;
        while i < bytes.len() && bytes[i].is_ascii_alphanumeric() {
            i += 1;
        }
        if i == start {
            continue;
        }
        let name = html[start..i].to_ascii_lowercase();
        let mut attributes = Vec::new();
        loop {
            while i < bytes.len() && (bytes[i].is_ascii_whitespace() || bytes[i] == b'/') {
                i += 1;
            }
            if i >= bytes.len() || bytes[i] == b'>' {
                i = (i + 1).min(bytes.len());
                break;
            }
            let name_start = i;
            while i < bytes.len()
                && !bytes[i].is_ascii_whitespace()
                && !matches!(bytes[i], b'=' | b'>' | b'/')
            {
                i += 1;
            }
            let attribute = html[name_start..i].to_ascii_lowercase();
            let mut j = i;
            while j < bytes.len() && bytes[j].is_ascii_whitespace() {
                j += 1;
            }
            let mut value = String::new();
            if j < bytes.len() && bytes[j] == b'=' {
                j += 1;
                while j < bytes.len() && bytes[j].is_ascii_whitespace() {
                    j += 1;
                }
                if j < bytes.len() && matches!(bytes[j], b'"' | b'\'') {
                    let quote = bytes[j] as char;
                    let end = html[j + 1..].find(quote).map_or(html.len(), |e| j + 1 + e);
                    value = html[j + 1..end].to_owned();
                    j = (end + 1).min(html.len());
                } else {
                    let end = html[j..]
                        .find(|c: char| c.is_ascii_whitespace() || c == '>')
                        .map_or(html.len(), |e| j + e);
                    value = html[j..end].to_owned();
                    j = end;
                }
                i = j;
            }
            if attribute.is_empty() {
                i = (i + 1).min(bytes.len());
                continue;
            }
            attributes.push((attribute, value));
        }
        // Raw-text elements contain no markup: skip to their end tag so
        // script or style text that looks like a tag is not reported.
        if matches!(name.as_str(), "script" | "style" | "textarea" | "title") {
            // The end tag name must match exactly: `</scripture>` does not
            // end a script.
            let close = format!("</{name}");
            let rest = html
                .get(i.min(html.len())..)
                .unwrap_or_default()
                .to_ascii_lowercase();
            let mut from = 0;
            let mut end = None;
            while let Some(at) = rest[from..].find(&close) {
                let after = from + at + close.len();
                if rest[after..]
                    .chars()
                    .next()
                    .is_none_or(|c| c.is_ascii_whitespace() || c == '/' || c == '>')
                {
                    end = Some(from + at);
                    break;
                }
                from = after;
            }
            i = end.map_or(html.len(), |end| i + end);
        }
        tags.push(HtmlTag { name, attributes });
    }
    tags
}

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
/// token moves through each round's edits and tokens inside replaced code
/// are dropped. Inserted or replacing code gets a token at its start that
/// maps where the original token at that position mapped, so inserted code
/// maps to the original range it replaced.
fn compose_bundle_map(
    source_map: &sourcemap::SourceMap,
    original: &str,
    changed: &str,
    chain: &[Vec<Edit>],
) -> Result<sourcemap::SourceMap> {
    struct Mapped<'a> {
        line: u32,
        col: u32,
        token: sourcemap::Token<'a>,
    }
    let original_starts = line_starts(original);
    let changed_starts = line_starts(changed);
    let forward = |mut offset: usize, rounds: &[Vec<Edit>]| -> Option<usize> {
        for edits in rounds {
            offset = forward_offset(offset, edits)?;
        }
        (offset <= changed.len()).then_some(offset)
    };
    let mut mapped = Vec::new();
    for token in source_map.tokens() {
        let offset = byte_at_utf16(
            original,
            &original_starts,
            token.get_dst_line(),
            token.get_dst_col(),
        );
        if let Some(offset) = forward(offset, chain) {
            let (line, col) = coordinate(changed, &changed_starts, offset);
            mapped.push(Mapped { line, col, token });
        }
    }
    for (round, edits) in chain.iter().enumerate() {
        let mut delta: isize = 0;
        for edit in edits {
            let growth = edit.replacement.len() as isize - (edit.end - edit.start) as isize;
            let start = (edit.start as isize + delta) as usize;
            delta += growth;
            if edit.replacement.is_empty() {
                continue;
            }
            let Some(offset) = forward(start, &chain[round + 1..]) else {
                continue;
            };
            let source = crate::pipeline::source_offset(edit.start, &chain[..round]);
            let (line, col) = coordinate(original, &original_starts, source.min(original.len()));
            if let Some(token) = source_map.lookup_token(line, col) {
                let (line, col) = coordinate(changed, &changed_starts, offset);
                mapped.push(Mapped { line, col, token });
            }
        }
    }
    // Encoders and consumers expect generated positions in order.
    mapped.sort_by_key(|m| (m.line, m.col));
    mapped.dedup_by_key(|m| (m.line, m.col));
    let mut builder = sourcemap::SourceMapBuilder::new(source_map.get_file());
    for (index, source) in source_map.sources().enumerate() {
        let id = builder.add_source(source);
        builder.set_source_contents(id, source_map.get_source_contents(index as u32));
    }
    for Mapped { line, col, token } in mapped {
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
    Ok(builder.into_sourcemap())
}

/// Where an edited chunk's source map lives.
enum ChunkMap {
    /// A `data:` URL in the chunk's `sourceMappingURL` comment.
    Inline(String),
    /// A map file inside the output directory.
    File(PathBuf),
}

/// A chunk's source map reference.
enum MapRef {
    /// A map that bundle mode can compose.
    Local(ChunkMap),
    /// An absolute or protocol-relative URL that bundle mode cannot update.
    Remote(String),
}

/// Finds the source map of a chunk: its `sourceMappingURL` (inline, relative
/// to the chunk or root-relative within the output), or the adjacent
/// `<chunk>.map` of hidden source maps.
fn chunk_map(root: &Path, chunk: &Path, text: &str) -> Option<MapRef> {
    let path = match map_reference(text).map(|comment| comment.url) {
        Some(url) if url.starts_with("data:") => {
            return Some(MapRef::Local(ChunkMap::Inline(url.to_owned())));
        }
        Some(reference) => {
            let url = reference.split(['?', '#']).next().unwrap_or_default();
            if url.contains("://") || url.starts_with("//") {
                return Some(MapRef::Remote(reference.to_owned()));
            }
            match url.strip_prefix('/') {
                // Root-relative: resolve under the output root, dropping
                // leading segments of a deployment base path when needed.
                Some(rooted) => {
                    let segments: Vec<&str> = rooted.split('/').collect();
                    (0..segments.len())
                        .map(|skip| root.join(segments[skip..].join("/")))
                        .find(|candidate| candidate.is_file())?
                }
                None => chunk.parent()?.join(url),
            }
        }
        None => chunk.with_file_name(format!("{}.map", chunk.file_name()?.to_string_lossy())),
    };
    let path = path.canonicalize().ok()?;
    (path.starts_with(root.canonicalize().ok()?) && path.is_file())
        .then_some(MapRef::Local(ChunkMap::File(path)))
}

/// The last `//# sourceMappingURL=<url>` or `/*# sourceMappingURL=<url> */`
/// comment (with `#` or the legacy `@`): its URL and the byte span of the
/// whole comment.
struct MapComment<'a> {
    url: &'a str,
    start: usize,
    end: usize,
}

fn map_reference(text: &str) -> Option<MapComment<'_>> {
    const MARKER: &str = "sourceMappingURL=";
    let mut limit = text.len();
    while let Some(at) = text[..limit].rfind(MARKER) {
        limit = at;
        let url_start = at + MARKER.len();
        let url_end = text[url_start..]
            .find(|c: char| c.is_whitespace() || c == '*')
            .map_or(text.len(), |e| url_start + e);
        let pragma = text[..at].trim_end_matches([' ', '\t']);
        let Some(opener) = pragma.strip_suffix(['#', '@']) else {
            continue;
        };
        let Some(start) = opener.len().checked_sub(2) else {
            continue;
        };
        let end = match text.get(start..opener.len()) {
            Some("//") => url_end,
            Some("/*") => match text[url_end..].find("*/") {
                Some(close) => url_end + close + 2,
                None => continue,
            },
            _ => continue,
        };
        if url_end > url_start {
            return Some(MapComment {
                url: &text[url_start..url_end],
                start,
                end,
            });
        }
    }
    None
}

/// Composes the source map of one edited chunk in place. Returns whether a
/// map was composed (`Some(true)`), dropped because it could not be read
/// (`Some(false)`) or absent (`None`), and how many bytes the chunk text
/// grew because an inline map changed.
fn update_chunk_map(
    root: &Path,
    target: &Path,
    original: &str,
    text: &str,
    chain: &[Vec<Edit>],
) -> Result<(Option<bool>, isize)> {
    let location = match chunk_map(root, target, text) {
        None => return Ok((None, 0)),
        Some(MapRef::Local(location)) => location,
        Some(MapRef::Remote(url)) => {
            // A remote map describes the pre-optimization chunk and cannot be
            // composed here; a missing map is better than a wrong one.
            tracing::warn!("dropping a remote source map reference of an edited chunk");
            let comment = map_reference(text)
                .filter(|comment| comment.url == url)
                .context("cannot locate the remote source map comment of an edited chunk")?;
            let (start, end) = (comment.start, comment.end);
            let updated = format!("{}{}", &text[..start], &text[end..]);
            std::fs::write(target, &updated).context("cannot write an edited chunk")?;
            return Ok((Some(false), updated.len() as isize - text.len() as isize));
        }
    };
    let decoded = match &location {
        ChunkMap::Inline(url) => sourcemap::decode_data_url(url).map_err(anyhow::Error::from),
        ChunkMap::File(path) => std::fs::read(path)
            .map_err(anyhow::Error::from)
            .and_then(|bytes| sourcemap::decode_slice(&bytes).map_err(anyhow::Error::from)),
    };
    let composed = match decoded {
        Ok(sourcemap::DecodedMap::Regular(map)) => compose_bundle_map(&map, original, text, chain),
        Ok(_) => Err(anyhow::anyhow!(
            "indexed or Hermes chunk maps are unsupported"
        )),
        Err(error) => Err(error),
    };
    match (location, composed) {
        (ChunkMap::File(path), Ok(map)) => {
            let mut bytes = Vec::new();
            map.to_writer(&mut bytes)?;
            std::fs::write(path, bytes).context("cannot write a chunk source map")?;
            Ok((Some(true), 0))
        }
        (ChunkMap::Inline(url), Ok(map)) => {
            let at = text.rfind(&url).context("inline source map disappeared")?;
            let updated = format!(
                "{}{}{}",
                &text[..at],
                map.to_data_url()?,
                &text[at + url.len()..]
            );
            std::fs::write(target, &updated).context("cannot write an edited chunk")?;
            Ok((Some(true), updated.len() as isize - text.len() as isize))
        }
        // A map that no longer describes the chunk is worse than none.
        (ChunkMap::File(path), Err(error)) => {
            tracing::warn!(%error, "dropping an unreadable chunk source map");
            std::fs::remove_file(path)?;
            Ok((Some(false), 0))
        }
        (ChunkMap::Inline(url), Err(error)) => {
            tracing::warn!(%error, "dropping an unreadable inline chunk source map");
            let comment = map_reference(text)
                .filter(|comment| comment.url == url)
                .context("cannot locate the unreadable inline source map comment")?;
            let (start, end) = (comment.start, comment.end);
            let updated = format!("{}{}", &text[..start], &text[end..]);
            std::fs::write(target, &updated).context("cannot write an edited chunk")?;
            Ok((Some(false), updated.len() as isize - text.len() as isize))
        }
    }
}

/// `<name>-<hash>.<ext>` or `<name>.<hash>.<ext>` with an 8-character
/// base64url content hash, as Vite and Rollup name chunks and assets, for
/// the text assets whose references bundle mode rewrites. A hash must
/// contain a digit, an uppercase letter, `-` or `_`, so ordinary words such
/// as `polyfill` are not mistaken for one.
fn hashed_name(file: &str) -> Option<(&str, &str, char)> {
    let (stem, ext) = file.rsplit_once('.')?;
    // Every text asset that reference rewriting can change.
    if !matches!(ext, "js" | "mjs" | "cjs" | "css" | "json" | "webmanifest") {
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
            Some("js" | "mjs" | "cjs" | "html" | "css" | "json" | "map" | "webmanifest")
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

/// Byte offsets where `name` occurs as a file reference: after a path
/// separator, quote, `(` or `=`, and before a quote, `)`, `?`, `#`,
/// whitespace, `>`, `,`, the end of the text or a `.map` suffix. Other
/// occurrences (prose or data that merely contains the name) stay unchanged.
fn references(text: &str, name: &str) -> Vec<usize> {
    let mut found = Vec::new();
    let mut from = 0;
    while let Some(offset) = text[from..].find(name) {
        let at = from + offset;
        let end = at + name.len();
        let before = text[..at].chars().next_back();
        let after = &text[end..];
        let follows = after.is_empty()
            || after.starts_with(".map")
            || after.starts_with(|c: char| {
                c.is_whitespace() || matches!(c, '"' | '\'' | '`' | ')' | '?' | '#' | '>' | ',')
            });
        if matches!(before, Some('/' | '"' | '\'' | '`' | '(' | '=')) && follows {
            found.push(at);
        }
        from = end;
    }
    found
}

/// Replaces the references to renamed files in `text`.
fn rewrite(text: &str, renamed: &BTreeMap<String, String>) -> String {
    let mut edits: Vec<(usize, usize, &str)> = Vec::new();
    for (old, new) in renamed {
        for at in references(text, old) {
            edits.push((at, at + old.len(), new));
        }
    }
    edits.sort();
    let mut out = String::with_capacity(text.len());
    let mut previous = 0;
    for (start, end, replacement) in edits {
        if start < previous {
            continue;
        }
        out.push_str(&text[previous..start]);
        out.push_str(replacement);
        previous = end;
    }
    out.push_str(&text[previous..]);
    out
}

/// Gives every edited content-hashed chunk, and every hashed file that
/// references a renamed one, a new hash, and rewrites the references in all
/// text assets. Browsers cache hashed chunks immutably; an edited chunk that
/// kept its name could otherwise meet a stale copy of a chunk it depends on.
/// A new hash covers the final content of the file and of every renamed file
/// it reaches, so a change in a dependency always renames its importers.
fn rehash(root: &Path, edited: &BTreeSet<PathBuf>) -> Result<usize> {
    use sha2::{Digest, Sha256};
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
    // Every hashed text file and the hashed files it references.
    // Hashed text files grouped by file name. A reference names only the
    // file name, so files that share one (in different directories) are
    // renamed together, from the content of all of them.
    let mut hashed = BTreeMap::<String, Vec<PathBuf>>::new();
    for path in texts.keys() {
        if hashed_name(&base(path)).is_some() {
            hashed.entry(base(path)).or_default().push(path.clone());
        }
    }
    let mut referenced = BTreeMap::<String, BTreeSet<String>>::new();
    for (name, paths) in &hashed {
        let targets = hashed
            .keys()
            .filter(|other| {
                *other != name
                    && paths
                        .iter()
                        .any(|path| !references(&texts[path], other).is_empty())
            })
            .cloned()
            .collect();
        referenced.insert(name.clone(), targets);
    }
    // The rename set: edited hashed chunks and, transitively, hashed files
    // that reference a renamed file.
    let mut renaming: BTreeSet<String> = edited
        .iter()
        .map(|p| base(p))
        .filter(|n| hashed.contains_key(n))
        .collect();
    loop {
        let more: Vec<String> = referenced
            .iter()
            .filter(|(name, targets)| {
                !renaming.contains(*name) && targets.iter().any(|t| renaming.contains(t))
            })
            .map(|(name, _)| name.clone())
            .collect();
        if more.is_empty() {
            break;
        }
        renaming.extend(more);
    }
    if renaming.is_empty() {
        return Ok(0);
    }
    // Content digests with references to renamed files reduced to their
    // logical names, so the digest does not depend on the new hashes.
    let logical: BTreeMap<String, String> = renaming
        .iter()
        .map(|n| {
            let (name, ext, separator) = hashed_name(n).expect("hashed");
            (n.clone(), format!("{name}{separator}.{ext}"))
        })
        .collect();
    let digests: BTreeMap<String, [u8; 32]> = renaming
        .iter()
        .map(|n| {
            let mut hasher = Sha256::new();
            for path in &hashed[n] {
                let relative = path.strip_prefix(root).unwrap_or(path);
                hasher.update(relative.to_string_lossy().as_bytes());
                hasher.update([0]);
                hasher.update(rewrite(&texts[path], &logical).as_bytes());
                hasher.update([0]);
            }
            (n.clone(), hasher.finalize().into())
        })
        .collect();
    let mut renamed = BTreeMap::<String, String>::new();
    for old in &renaming {
        // Renamed files this file reaches, including itself.
        let mut reach = BTreeSet::from([old.clone()]);
        let mut stack = vec![old.clone()];
        while let Some(current) = stack.pop() {
            for target in &referenced[&current] {
                if renaming.contains(target) && reach.insert(target.clone()) {
                    stack.push(target.clone());
                }
            }
        }
        let (name, ext, separator) = hashed_name(old).expect("hashed");
        // Uniform base64url characters from the digest bytes, like a real
        // content hash (a hex-derived name would compress unusually well),
        // and recognizable as a hash by a later run.
        let mut new_name = String::new();
        for salt in 0u32.. {
            let mut hasher = Sha256::new();
            hasher.update(format!("{old}\0{salt}\0").as_bytes());
            for file in &reach {
                hasher.update(file.as_bytes());
                hasher.update(digests[file]);
            }
            let digest = hasher.finalize();
            let alphabet = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
            let hash: String = digest[..8]
                .iter()
                .map(|&b| alphabet[(b % 64) as usize] as char)
                .collect();
            new_name = format!("{name}{separator}{hash}.{ext}");
            if hashed_name(&new_name).is_some() && new_name != *old {
                break;
            }
        }
        renamed.insert(old.clone(), new_name);
    }
    for (file, text) in &texts {
        crate::app::check_cancelled()?;
        let updated = rewrite(text, &renamed);
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

/// Rejects bundles whose HTML pages use Subresource Integrity: the digests
/// would no longer match edited chunks. Check and output modes both run it
/// before analysis.
pub fn check_integrity(dir: &Path) -> Result<()> {
    let mut files = Vec::new();
    files_under(dir, &mut files)?;
    for file in &files {
        if file.extension().is_some_and(|e| e == "html")
            && std::fs::read_to_string(file).is_ok_and(|html| {
                html_tags(&html)
                    .iter()
                    .any(|tag| tag.attribute("integrity").is_some())
            })
        {
            anyhow::bail!(
                "HTML pages use Subresource Integrity, which bundle mode cannot update; compute \
                 integrity attributes after typeopt"
            );
        }
    }
    Ok(())
}

/// A complete bundle output in an operation-owned staging directory. It is
/// published only by [`Staged::commit`]; dropping it removes the staging
/// directory, so a failure after staging never leaves a published result.
pub struct Staged {
    _staging: tempfile::TempDir,
    root: PathBuf,
    destination: PathBuf,
    /// Bytes that recomposed inline source maps added to edited chunks.
    pub inline_map_growth: isize,
}

impl Staged {
    /// Moves the staged output to its destination.
    pub fn commit(self) -> Result<()> {
        std::fs::rename(&self.root, &self.destination).context("cannot publish bundle output")
    }
}

/// Stage 2 output: copies the bundler output directory into staging,
/// replaces the edited chunks, composes their source maps with the edits and
/// gives edited content-hashed chunks (and their importers) new hashes.
pub fn stage(
    source: &Path,
    destination: &Path,
    snapshot: &Snapshot,
    outcome: &Outcome,
) -> Result<Staged> {
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
    let mut edited = BTreeSet::new();
    let mut inline_map_growth: isize = 0;
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
        let chain = outcome.chains.get(name).map_or(&[][..], Vec::as_slice);
        let (map, growth) = update_chunk_map(&root, &target, original, text, chain)?;
        inline_map_growth += growth;
        match map {
            Some(true) => maps += 1,
            Some(false) => dropped += 1,
            None => {}
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
    Ok(Staged {
        _staging: staging,
        root,
        destination: destination.to_path_buf(),
        inline_map_growth,
    })
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;

    use super::{hashed_name, html_tags, references, rewrite};

    #[test]
    fn maps_inserted_code_to_the_position_it_was_inserted_at() {
        let original = "a();b();";
        let mut builder = sourcemap::SourceMapBuilder::new(None);
        builder.add(0, 0, 10, 0, Some("src.ts"), None, false);
        builder.add(0, 4, 20, 0, Some("src.ts"), None, false);
        let map = builder.into_sourcemap();
        let insertion = "import\"x\";";
        let chain = vec![vec![crate::plan::Edit {
            start: 4,
            end: 4,
            replacement: insertion.to_owned(),
        }]];
        let changed = format!("a();{insertion}b();");
        let composed = super::compose_bundle_map(&map, original, &changed, &chain).unwrap();
        let tokens: Vec<_> = composed
            .tokens()
            .map(|t| (t.get_dst_col(), t.get_src_line()))
            .collect();
        // The insertion maps where `b()` did; `b()` keeps its own mapping.
        assert_eq!(tokens, [(0, 10), (4, 20), (4 + insertion.len() as u32, 20)]);
    }

    #[test]
    fn locates_source_map_comments_exactly() {
        let url = "data:application/json;base64,e30=";
        let line = format!("const u=\"http://x\";f();\n//# sourceMappingURL={url}");
        let comment = super::map_reference(&line).unwrap();
        assert_eq!(comment.url, url);
        assert_eq!(&line[..comment.start], "const u=\"http://x\";f();\n");
        assert_eq!(comment.end, line.len());
        let block = format!("const u=\"http://x\";f();/*# sourceMappingURL={url} */g();");
        let comment = super::map_reference(&block).unwrap();
        assert_eq!(comment.url, url);
        assert_eq!(
            format!("{}{}", &block[..comment.start], &block[comment.end..]),
            "const u=\"http://x\";f();g();"
        );
        let legacy = "f();\n//@ sourceMappingURL=a.js.map";
        assert_eq!(super::map_reference(legacy).unwrap().url, "a.js.map");
        assert!(super::map_reference(&format!("x=\"sourceMappingURL={url}\"")).is_none());
    }

    #[test]
    fn rewrites_only_reference_positions() {
        let name = "index-DnKDBunT.js";
        let text = r#"import"./index-DnKDBunT.js";url(/a/index-DnKDBunT.js)<script src=index-DnKDBunT.js>//# sourceMappingURL=index-DnKDBunT.js.map
see index-DnKDBunT.js later;"xindex-DnKDBunT.jsx""#;
        assert_eq!(references(text, name).len(), 4);
        let renamed = BTreeMap::from([(name.to_owned(), "index-NEW12345.js".to_owned())]);
        let out = rewrite(text, &renamed);
        assert!(out.contains(r#"import"./index-NEW12345.js""#));
        assert!(out.contains("sourceMappingURL=index-NEW12345.js.map"));
        assert!(out.contains("see index-DnKDBunT.js later"));
        assert!(out.contains("xindex-DnKDBunT.jsx"));
    }

    #[test]
    fn parses_html_attributes_like_browsers() {
        let tags = html_tags(
            "<!-- <script src=a.js> --><SCRIPT Type = \"module\" SRC='/x/a.js' INTEGRITY = \
             sha384-x defer></script><link rel=modulepreload href=b.js>",
        );
        assert_eq!(tags.len(), 2);
        assert_eq!(tags[0].name, "script");
        assert_eq!(tags[0].attribute("type"), Some("module"));
        assert_eq!(tags[0].attribute("src"), Some("/x/a.js"));
        assert_eq!(tags[0].attribute("integrity"), Some("sha384-x"));
        assert_eq!(tags[0].attribute("defer"), Some(""));
        assert_eq!(tags[1].attribute("href"), Some("b.js"));
        // Markup inside script text is not a tag.
        let tags = html_tags(
            r#"<script>const s='<script type="module" src="./legacy.js" integrity=x>';</SCRIPT><p>"#,
        );
        let names: Vec<_> = tags.iter().map(|t| t.name.as_str()).collect();
        assert_eq!(names, ["script", "p"]);
        assert_eq!(tags[0].attribute("src"), None);
        // Only an exact end tag name ends the raw text.
        let tags = html_tags(
            r#"<script>s='</scripture><script type="module" src="./legacy.js">';</script ><p>"#,
        );
        let names: Vec<_> = tags.iter().map(|t| t.name.as_str()).collect();
        assert_eq!(names, ["script", "p"]);
        // Unterminated markup at the end of a page.
        for html in ["<script src=a.js", "<p a=", "<p =", "<script>x", "<a b='c"] {
            let _ = html_tags(html);
        }
    }

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
        assert_eq!(
            hashed_name("style-DnKDBunT.css"),
            Some(("style", "css", '-'))
        );
        assert_eq!(
            hashed_name("site-AB12cd34.webmanifest"),
            Some(("site", "webmanifest", '-'))
        );
        assert_eq!(hashed_name("logo-DnKDBunT.png"), None);
        assert_eq!(hashed_name("-DnKDBunT.js"), None);
    }
}
