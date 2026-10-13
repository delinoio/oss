//! Emitted chunk loading, source-map lookup, attribution and size measurement.

use std::{
    collections::BTreeMap,
    io::Write,
    path::{Path, PathBuf},
};

use sourcemap::SourceMap;

use crate::{
    model::{AttributionState, ChunkSize, SourceAttribution},
    sources::normalize,
};

pub struct Chunk {
    pub file: String,
    pub code: String,
    pub entry: bool,
    pub map: Option<SourceMap>,
    /// Absolute source path for each source index of `map`.
    pub map_sources: Vec<Option<PathBuf>>,
    line_starts: Vec<usize>,
}

/// Joins `sourceRoot` and a source with one separator. Absolute and URL
/// sources ignore the root; a trailing `/` on the root and a leading `./` on
/// the source never produce `//` or an absolute-looking remainder.
pub fn join_source_root(root: &str, source: &str) -> String {
    if root.is_empty() || source.contains("://") || source.starts_with('/') {
        return source.to_owned();
    }
    let source = source.strip_prefix("./").unwrap_or(source);
    format!("{}/{source}", root.trim_end_matches('/'))
}

/// Resolves a source-map `sources` entry to an absolute path.
pub fn resolve_map_source(entry: &str, map_dir: &Path, context: &Path) -> Option<PathBuf> {
    if let Some(rest) = entry.strip_prefix("webpack://") {
        // `webpack://<namespace>/<path relative to context>`.
        let relative = rest.split_once('/').map_or(rest, |(_, path)| path);
        let relative = relative.split('?').next().unwrap_or(relative);
        if relative.starts_with("webpack/") || relative.is_empty() {
            return None;
        }
        return Some(normalize(&context.join(relative)));
    }
    if entry.contains("://") || entry.starts_with('\0') {
        return None;
    }
    let path = Path::new(entry);
    if path.is_absolute() {
        Some(normalize(path))
    } else {
        Some(normalize(&map_dir.join(path)))
    }
}

fn line_starts(code: &str) -> Vec<usize> {
    let mut starts = vec![0];
    starts.extend(code.match_indices('\n').map(|(index, _)| index + 1));
    starts
}

impl Chunk {
    pub fn load(
        build_dir: &Path,
        file: &str,
        map_file: Option<&str>,
        entry: bool,
        context: &Path,
    ) -> std::io::Result<Self> {
        let path = build_dir.join(file);
        let code = std::fs::read_to_string(&path)?;
        let map_path = map_file.map_or_else(
            || build_dir.join(format!("{file}.map")),
            |map| build_dir.join(map),
        );
        let map =
            std::fs::read(&map_path)
                .ok()
                .and_then(|bytes| match SourceMap::from_slice(&bytes) {
                    Ok(map) => Some(map),
                    Err(error) => {
                        tracing::warn!(chunk = file, %error, "source map could not be parsed");
                        None
                    }
                });
        let map_dir = map_path
            .parent()
            .map_or_else(|| build_dir.to_path_buf(), Path::to_path_buf);
        let map_sources = map.as_ref().map_or_else(Vec::new, |map| {
            map.sources()
                .map(|source| {
                    let root = map.get_source_root().unwrap_or("");
                    let joined = join_source_root(root, source);
                    resolve_map_source(&joined, &map_dir, context)
                })
                .collect()
        });
        Ok(Self {
            file: file.to_owned(),
            line_starts: line_starts(&code),
            code,
            entry,
            map,
            map_sources,
        })
    }

    /// Converts a byte offset to a zero-based (line, UTF-16 column) position.
    pub fn position(&self, offset: usize) -> (u32, u32) {
        let line = self
            .line_starts
            .partition_point(|start| *start <= offset)
            .saturating_sub(1);
        let start = self.line_starts[line];
        let column: usize = self.code[start..offset].chars().map(char::len_utf16).sum();
        (
            u32::try_from(line).unwrap_or(u32::MAX),
            u32::try_from(column).unwrap_or(u32::MAX),
        )
    }

    /// Maps a generated position to (source path, zero-based line, column).
    pub fn original(&self, line: u32, column: u32) -> Option<(PathBuf, u32, u32)> {
        let map = self.map.as_ref()?;
        let token = map.lookup_token(line, column)?;
        let source = token.get_src_id();
        let path = self.map_sources.get(source as usize)?.clone()?;
        Some((path, token.get_src_line(), token.get_src_col()))
    }

    /// Splits the generated text at mapping boundaries. Each byte belongs to at
    /// most one segment, so per-source totals never overlap or exceed the size.
    pub fn attribution(&self) -> Option<(BTreeMap<PathBuf, u32>, u32)> {
        let map = self.map.as_ref()?;
        let mut positions: Vec<(u32, u32, Option<u32>)> = map
            .tokens()
            .map(|token| {
                let source = token.has_source().then(|| token.get_src_id());
                (token.get_dst_line(), token.get_dst_col(), source)
            })
            .collect();
        positions.sort_by_key(|(line, column, _)| (*line, *column));
        // Convert positions to byte offsets in one forward pass; minified
        // chunks are often a single very long line, so per-token scans from the
        // line start would be quadratic.
        let mut tokens: Vec<(usize, Option<u32>)> = Vec::with_capacity(positions.len());
        let mut cursor_line = u32::MAX;
        let mut cursor_byte = 0usize;
        let mut cursor_units = 0usize;
        for (line, column, source) in positions {
            if line != cursor_line {
                cursor_line = line;
                cursor_byte = self
                    .line_starts
                    .get(line as usize)
                    .copied()
                    .unwrap_or(self.code.len());
                cursor_units = 0;
            }
            let line_end = self
                .line_starts
                .get(line as usize + 1)
                .copied()
                .unwrap_or(self.code.len());
            while cursor_units < column as usize && cursor_byte < line_end {
                let Some(character) = self.code[cursor_byte..].chars().next() else {
                    break;
                };
                cursor_units += character.len_utf16();
                cursor_byte += character.len_utf8();
            }
            tokens.push((cursor_byte, source));
        }
        let total = self.code.len();
        let mut per_id: BTreeMap<u32, usize> = BTreeMap::new();
        let mut unmapped = tokens.first().map_or(total, |(offset, _)| *offset);
        for (index, (start, source)) in tokens.iter().enumerate() {
            let end = tokens
                .get(index + 1)
                .map_or(total, |(offset, _)| *offset)
                .max(*start);
            match source.filter(|id| matches!(self.map_sources.get(*id as usize), Some(Some(_)))) {
                Some(id) => *per_id.entry(id).or_default() += end - start,
                None => unmapped += end - start,
            }
        }
        let mut per_source: BTreeMap<PathBuf, u32> = BTreeMap::new();
        for (id, bytes) in per_id {
            if let Some(Some(path)) = self.map_sources.get(id as usize) {
                *per_source.entry(path.clone()).or_default() +=
                    u32::try_from(bytes).unwrap_or(u32::MAX);
            }
        }
        Some((per_source, u32::try_from(unmapped).unwrap_or(u32::MAX)))
    }

    /// Returns up to nine lines around `line` (zero-based), each capped at 240
    /// characters.
    pub fn snippet(text: &str, line: u32) -> (u32, String) {
        snippet_lines(text, line)
    }
}

pub fn snippet_lines(text: &str, line: u32) -> (u32, String) {
    let lines: Vec<&str> = text.lines().collect();
    let line = (line as usize).min(lines.len().saturating_sub(1));
    let start = line.saturating_sub(4);
    let end = (line + 5).min(lines.len());
    let body = lines[start..end]
        .iter()
        .map(|entry| {
            if entry.chars().count() > 240 {
                format!("{}…", entry.chars().take(240).collect::<String>())
            } else {
                (*entry).to_owned()
            }
        })
        .collect::<Vec<_>>()
        .join("\n");
    (u32::try_from(start + 1).unwrap_or(1), body)
}

/// Snippet around a generated byte offset for minified single-line chunks.
pub fn generated_excerpt(code: &str, offset: usize) -> String {
    let mut start = offset.saturating_sub(100);
    while start > 0 && !code.is_char_boundary(start) {
        start -= 1;
    }
    let mut end = (offset + 140).min(code.len());
    while end < code.len() && !code.is_char_boundary(end) {
        end += 1;
    }
    let mut excerpt = String::new();
    if start > 0 {
        excerpt.push('…');
    }
    excerpt.push_str(&code[start..end]);
    if end < code.len() {
        excerpt.push('…');
    }
    excerpt
}

pub fn gzip_size(bytes: &[u8]) -> u32 {
    let mut encoder = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::best());
    let _ = encoder.write_all(bytes);
    encoder
        .finish()
        .map_or(0, |out| u32::try_from(out.len()).unwrap_or(u32::MAX))
}

pub fn brotli_size(bytes: &[u8]) -> u32 {
    let mut out = Vec::new();
    let params = brotli::enc::BrotliEncoderParams {
        quality: 11,
        ..Default::default()
    };
    match brotli::BrotliCompress(&mut &bytes[..], &mut out, &params) {
        Ok(_) => u32::try_from(out.len()).unwrap_or(u32::MAX),
        Err(_) => 0,
    }
}

pub fn measure(chunk: &Chunk, display: impl Fn(&Path) -> String) -> ChunkSize {
    let bytes = chunk.code.as_bytes();
    let mut size = ChunkSize {
        file: chunk.file.clone(),
        raw_bytes: u32::try_from(bytes.len()).unwrap_or(u32::MAX),
        gzip_bytes: gzip_size(bytes),
        brotli_bytes: brotli_size(bytes),
        attribution: AttributionState::Unavailable,
        sources: Vec::new(),
        unmapped_bytes: 0,
        entry: chunk.entry,
    };
    if let Some((per_source, unmapped)) = chunk.attribution() {
        let mut merged: BTreeMap<String, u32> = BTreeMap::new();
        for (path, bytes) in per_source {
            *merged.entry(display(&path)).or_default() += bytes;
        }
        let mut sources: Vec<SourceAttribution> = merged
            .into_iter()
            .map(|(path, generated_bytes)| SourceAttribution {
                path,
                generated_bytes,
            })
            .collect();
        sources.sort_by(|left, right| {
            right
                .generated_bytes
                .cmp(&left.generated_bytes)
                .then(left.path.cmp(&right.path))
        });
        size.attribution = AttributionState::Supported;
        size.sources = sources;
        size.unmapped_bytes = unmapped;
    }
    size
}

#[cfg(test)]
mod tests {
    use super::*;

    fn chunk_with_map(code: &str, map: &str) -> (tempfile::TempDir, Chunk) {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("out.js"), code).unwrap();
        std::fs::write(dir.path().join("out.js.map"), map).unwrap();
        let chunk = Chunk::load(dir.path(), "out.js", None, true, dir.path()).unwrap();
        (dir, chunk)
    }

    #[test]
    fn attribution_never_double_counts() {
        // Two tokens on line 0: columns 0 (a.js) and 6 (b.js), plus a duplicate
        // token at column 6 that maps to a.js again.
        let map =
            r#"{"version":3,"sources":["a.js","b.js"],"names":[],"mappings":"AAAA,MCAA,ADAA"}"#;
        let (_dir, chunk) = chunk_with_map("const a=1;const b=2;", map);
        let (per_source, unmapped) = chunk.attribution().unwrap();
        let total: u32 = per_source.values().sum::<u32>() + unmapped;
        assert_eq!(total, 20);
        assert_eq!(unmapped, 0);
    }

    #[test]
    fn missing_map_marks_attribution_unavailable() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("out.js"), "console.log(1)").unwrap();
        let chunk = Chunk::load(dir.path(), "out.js", None, false, dir.path()).unwrap();
        let size = measure(&chunk, |path| path.display().to_string());
        assert_eq!(size.attribution, AttributionState::Unavailable);
        assert_eq!(size.raw_bytes, 14);
        assert!(size.gzip_bytes > 0 && size.brotli_bytes > 0);
    }

    #[test]
    fn resolves_webpack_and_relative_sources() {
        let context = Path::new("/app");
        assert_eq!(
            resolve_map_source("webpack://name/./src/a.js", Path::new("/out"), context),
            Some(PathBuf::from("/app/src/a.js"))
        );
        assert_eq!(
            resolve_map_source(
                "webpack://name/webpack/runtime/x",
                Path::new("/out"),
                context
            ),
            None
        );
        assert_eq!(
            resolve_map_source("../src/b.js", Path::new("/app/dist/assets"), context),
            Some(PathBuf::from("/app/dist/src/b.js"))
        );
    }

    #[test]
    fn source_roots_join_without_creating_absolute_paths() {
        assert_eq!(
            join_source_root("webpack://app/", "./src/a.js"),
            "webpack://app/src/a.js"
        );
        assert_eq!(
            join_source_root("webpack://app", "src/a.js"),
            "webpack://app/src/a.js"
        );
        assert_eq!(join_source_root("", "./src/a.js"), "./src/a.js");
        assert_eq!(join_source_root("/root/", "/abs/a.js"), "/abs/a.js");
        assert_eq!(
            resolve_map_source(
                &join_source_root("webpack://app/", "./src/a.js"),
                Path::new("/out"),
                Path::new("/app")
            ),
            Some(PathBuf::from("/app/src/a.js"))
        );
    }

    #[test]
    fn position_uses_utf16_columns() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("out.js"), "a\n😀b").unwrap();
        let chunk = Chunk::load(dir.path(), "out.js", None, false, dir.path()).unwrap();
        let offset = chunk.code.find('b').unwrap();
        assert_eq!(chunk.position(offset), (1, 2));
    }
}
