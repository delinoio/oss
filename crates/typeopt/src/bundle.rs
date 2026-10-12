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
    /// Attributes whose value has a character reference this parser cannot
    /// decode; their value is kept raw.
    pub undecodable: Vec<String>,
    /// Text content of a raw-text element (`script`, `style`, `textarea`,
    /// `title`).
    pub text: Option<String>,
}

/// Decodes the character references of an attribute value: numeric
/// (`&#46;`, `&#x2e;`, with or without `;`), common named ones and the
/// legacy names browsers decode without `;`. Returns `None` for a named
/// reference outside the decoded set.
fn decode_references(value: &str) -> Option<String> {
    decode_references_in(value, true)
}

/// Like [`decode_references`], but keeps a reference it cannot decode as
/// literal text and decodes the rest: each reference is independent.
fn decode_references_lossy(value: &str) -> String {
    decode_references_in(value, false).expect("lossy decoding never fails")
}

fn decode_references_in(value: &str, strict: bool) -> Option<String> {
    const NAMED: [(&str, char); 22] = [
        ("amp", '&'),
        ("lt", '<'),
        ("gt", '>'),
        ("quot", '"'),
        ("apos", '\''),
        ("sol", '/'),
        ("period", '.'),
        ("colon", ':'),
        ("num", '#'),
        ("quest", '?'),
        ("equals", '='),
        ("lowbar", '_'),
        ("comma", ','),
        ("semi", ';'),
        ("excl", '!'),
        ("percnt", '%'),
        ("plus", '+'),
        ("lpar", '('),
        ("rpar", ')'),
        ("dollar", '$'),
        ("ast", '*'),
        ("nbsp", '\u{a0}'),
    ];
    // Named references that browsers also decode without a semicolon.
    const LEGACY: [&str; 106] = [
        "AElig", "AMP", "Aacute", "Acirc", "Agrave", "Aring", "Atilde", "Auml", "COPY", "Ccedil",
        "ETH", "Eacute", "Ecirc", "Egrave", "Euml", "GT", "Iacute", "Icirc", "Igrave", "Iuml",
        "LT", "Ntilde", "Oacute", "Ocirc", "Ograve", "Oslash", "Otilde", "Ouml", "QUOT", "REG",
        "THORN", "Uacute", "Ucirc", "Ugrave", "Uuml", "Yacute", "aacute", "acirc", "acute",
        "aelig", "agrave", "amp", "aring", "atilde", "auml", "brvbar", "ccedil", "cedil", "cent",
        "copy", "curren", "deg", "divide", "eacute", "ecirc", "egrave", "eth", "euml", "frac12",
        "frac14", "frac34", "gt", "iacute", "icirc", "iexcl", "igrave", "iquest", "iuml", "laquo",
        "lt", "macr", "micro", "middot", "nbsp", "not", "ntilde", "oacute", "ocirc", "ograve",
        "ordf", "ordm", "oslash", "otilde", "ouml", "para", "plusmn", "pound", "quot", "raquo",
        "reg", "sect", "shy", "sup1", "sup2", "sup3", "szlig", "thorn", "times", "uacute", "ucirc",
        "ugrave", "uml", "uuml", "yacute", "yen", "yuml",
    ];
    if !value.contains('&') {
        return Some(value.to_owned());
    }
    let mut out = String::with_capacity(value.len());
    let mut rest = value;
    while let Some(at) = rest.find('&') {
        out.push_str(&rest[..at]);
        rest = &rest[at + 1..];
        if let Some(numeric) = rest.strip_prefix('#') {
            let (radix, digits) = match numeric.strip_prefix(['x', 'X']) {
                Some(hex) => (16, hex),
                None => (10, numeric),
            };
            let len = digits
                .find(|c: char| !c.is_digit(radix))
                .unwrap_or(digits.len());
            match u32::from_str_radix(&digits[..len], radix)
                .ok()
                .and_then(char::from_u32)
            {
                Some(decoded) => {
                    out.push(decoded);
                    let after = &digits[len..];
                    rest = after.strip_prefix(';').unwrap_or(after);
                }
                None if strict => return None,
                None => out.push('&'),
            }
            continue;
        }
        let len = rest
            .find(|c: char| !c.is_ascii_alphanumeric())
            .unwrap_or(rest.len());
        if len > 0 && rest[len..].starts_with(';') {
            match NAMED.iter().find(|(name, _)| *name == &rest[..len]) {
                Some((_, decoded)) => {
                    out.push(*decoded);
                    rest = &rest[len + 1..];
                }
                None if strict => return None,
                None => out.push('&'),
            }
            continue;
        }
        // Without `;`, a legacy name decodes unless an alphanumeric or `=`
        // follows it (attribute-value rules).
        let legacy = LEGACY
            .iter()
            .filter(|name| rest.starts_with(**name))
            .max_by_key(|name| name.len());
        match legacy {
            Some(name)
                if !rest[name.len()..]
                    .starts_with(|c: char| c.is_ascii_alphanumeric() || c == '=') =>
            {
                match NAMED
                    .iter()
                    .find(|(known, _)| known.eq_ignore_ascii_case(name))
                {
                    Some((_, decoded)) => {
                        out.push(*decoded);
                        rest = &rest[name.len()..];
                    }
                    None if strict => return None,
                    None => out.push('&'),
                }
            }
            _ => out.push('&'),
        }
    }
    out.push_str(rest);
    Some(out)
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
        let mut undecodable = Vec::new();
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
            match decode_references(&value) {
                Some(decoded) => attributes.push((attribute, decoded)),
                None => {
                    undecodable.push(attribute.clone());
                    attributes.push((attribute, value));
                }
            }
        }
        // Raw-text elements contain no markup: skip to their end tag so
        // script or style text that looks like a tag is not reported.
        let mut text = None;
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
            let content_start = i.min(html.len());
            i = end.map_or(html.len(), |end| i + end);
            text = Some(html[content_start..i].to_owned());
        }
        tags.push(HtmlTag {
            name,
            attributes,
            undecodable,
            text,
        });
    }
    tags
}

/// The value of JavaScript string literal source (without its quotes):
/// escape sequences decoded. `None` for a malformed escape.
pub(crate) fn decode_js_string(raw: &str) -> Option<String> {
    let mut out = String::with_capacity(raw.len());
    let mut chars = raw.chars().peekable();
    while let Some(c) = chars.next() {
        if c != '\\' {
            out.push(c);
            continue;
        }
        let escaped = chars.next()?;
        let hex = |chars: &mut std::iter::Peekable<std::str::Chars>, n: usize| -> Option<u32> {
            let digits: String = (0..n).map(|_| chars.next()).collect::<Option<_>>()?;
            u32::from_str_radix(&digits, 16).ok()
        };
        match escaped {
            'n' => out.push('\n'),
            'r' => out.push('\r'),
            't' => out.push('\t'),
            'b' => out.push('\u{8}'),
            'f' => out.push('\u{c}'),
            'v' => out.push('\u{b}'),
            '0' if !chars.peek().is_some_and(char::is_ascii_digit) => out.push('\0'),
            'x' => out.push(char::from_u32(hex(&mut chars, 2)?)?),
            'u' if chars.peek() == Some(&'{') => {
                chars.next();
                let digits: String = chars.by_ref().take_while(|c| *c != '}').collect();
                out.push(char::from_u32(u32::from_str_radix(&digits, 16).ok()?)?);
            }
            'u' => {
                let unit = hex(&mut chars, 4)?;
                // A surrogate pair spans two escapes; an unpaired surrogate
                // has no Unicode scalar value and counts as unknown.
                if (0xd800..0xdc00).contains(&unit) {
                    if chars.next()? != '\\' || chars.next()? != 'u' {
                        return None;
                    }
                    let low = hex(&mut chars, 4)?;
                    if !(0xdc00..0xe000).contains(&low) {
                        return None;
                    }
                    out.push(char::from_u32(
                        0x10000 + ((unit - 0xd800) << 10) + (low - 0xdc00),
                    )?);
                } else {
                    out.push(char::from_u32(unit)?);
                }
            }
            // Line continuations produce nothing.
            '\n' | '\u{2028}' | '\u{2029}' => {}
            '\r' => {
                if chars.peek() == Some(&'\n') {
                    chars.next();
                }
            }
            '1'..='9' => return None,
            other => out.push(other),
        }
    }
    Some(out)
}

/// Dynamic `import(…)` calls in script text.
pub(crate) struct DynamicImports {
    /// Specifiers of `import("…")` calls with a string literal argument.
    pub literals: Vec<String>,
    /// Some `import(…)` call has another argument, which can name any chunk.
    pub non_literal: bool,
}

pub(crate) fn literal_dynamic_imports(code: &str) -> DynamicImports {
    let bytes = code.as_bytes();
    let mut found = Vec::new();
    let mut non_literal = false;
    let mut from = 0;
    while let Some(offset) = code[from..].find("import") {
        let at = from + offset;
        from = at + "import".len();
        if at > 0
            && (bytes[at - 1].is_ascii_alphanumeric()
                || matches!(bytes[at - 1], b'_' | b'$' | b'.'))
        {
            continue;
        }
        // Whitespace and comments may surround the parenthesis.
        let open = crate::pipeline::skip_js_trivia(code, from);
        if bytes.get(open) != Some(&b'(') {
            continue;
        }
        let rest = &code[crate::pipeline::skip_js_trivia(code, open + 1)..];
        let Some(quote) = rest
            .chars()
            .next()
            .filter(|c| matches!(c, '"' | '\'' | '`'))
        else {
            non_literal = true;
            continue;
        };
        if let Some(end) = rest[1..].find(quote) {
            found.push(rest[1..1 + end].to_owned());
        }
    }
    DynamicImports {
        literals: found,
        non_literal,
    }
}

/// Maps an offset of a round's input text to the round's output text.
/// Offsets inside a replaced range have no counterpart, except the start of
/// a non-empty replacement, which maps to the start of its replacement.
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
            // The start of a deletion is removed code; a replacement start
            // maps to the start of its replacement.
            if edit.replacement.is_empty() {
                return None;
            }
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
    // Sources stay relative to the original `sourceRoot`.
    builder.set_source_root(source_map.get_source_root());
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
    /// An explicit reference that bundle mode cannot compose: an absolute or
    /// protocol-relative URL, or a local path that is missing or outside the
    /// output.
    Uncomposable(String),
}

/// Finds the source map of a chunk: its `sourceMappingURL` (inline, relative
/// to the chunk or root-relative within the output), or the adjacent
/// `<chunk>.map` of hidden source maps.
fn chunk_map(root: &Path, chunk: &Path, text: &str) -> Option<MapRef> {
    let root = root.canonicalize().ok()?;
    let local = |path: PathBuf| {
        path.canonicalize()
            .ok()
            .filter(|path| path.starts_with(&root) && path.is_file())
    };
    match map_reference(text).map(|comment| comment.url) {
        Some(url) if url.starts_with("data:") => {
            Some(MapRef::Local(ChunkMap::Inline(url.to_owned())))
        }
        // An explicit reference that cannot be resolved inside the output
        // (remote, missing or outside) cannot be composed.
        Some(reference) => {
            let url = reference.split(['?', '#']).next().unwrap_or_default();
            let path = if url.contains("://") || url.starts_with("//") {
                None
            } else if let Some(rooted) = url.strip_prefix('/') {
                // Root-relative: resolve under the output root, dropping
                // leading segments of a deployment base path when needed.
                let segments: Vec<&str> = rooted.split('/').collect();
                (0..segments.len()).find_map(|skip| local(root.join(segments[skip..].join("/"))))
            } else {
                chunk.parent().and_then(|dir| local(dir.join(url)))
            };
            Some(path.map_or_else(
                || MapRef::Uncomposable(reference.to_owned()),
                |path| MapRef::Local(ChunkMap::File(path)),
            ))
        }
        // Hidden source maps: an adjacent `<chunk>.map`, if present.
        None => {
            local(chunk.with_file_name(format!("{}.map", chunk.file_name()?.to_string_lossy())))
                .map(|path| MapRef::Local(ChunkMap::File(path)))
        }
    }
}

/// Bytes of a chunk's code: the text without an inline `data:` source map
/// comment. Source maps are debugging metadata and never count as size,
/// whether inline or in separate files.
pub(crate) fn chunk_code_len(text: &str) -> usize {
    match map_reference(text) {
        Some(comment) if comment.url.starts_with("data:") => {
            text.len() - (comment.end - comment.start)
        }
        _ => text.len(),
    }
}

/// The trailing `//# sourceMappingURL=<url>` or
/// `/*# sourceMappingURL=<url> */` comment (with `#` or the legacy `@`) at
/// the end of the chunk, possibly after code on the same line: its URL and
/// the byte span of the whole comment.
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
        // A directive is the trailing comment of the chunk: only whitespace
        // follows it, and its URL has no quote or backtick. Text inside a
        // string or template literal that looks like one would need a closing
        // quote after the URL, so it is never accepted.
        let url = &text[url_start..url_end];
        if !text[end..].trim().is_empty() || url.contains(['"', '\'', '`']) {
            continue;
        }
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
/// map was composed (`Some(true)`), dropped because it could not be composed
/// (`Some(false)`) or absent (`None`).
fn update_chunk_map(
    root: &Path,
    target: &Path,
    original: &str,
    text: &str,
    chain: &[Vec<Edit>],
) -> Result<Option<bool>> {
    let location = match chunk_map(root, target, text) {
        None => return Ok(None),
        Some(MapRef::Local(location)) => location,
        Some(MapRef::Uncomposable(url)) => {
            // Such a map describes the pre-optimization chunk and cannot be
            // composed here; a missing map is better than a wrong one.
            tracing::warn!("dropping an uncomposable source map reference of an edited chunk");
            let comment = map_reference(text)
                .filter(|comment| comment.url == url)
                .context("cannot locate the source map comment of an edited chunk")?;
            let (start, end) = (comment.start, comment.end);
            let updated = format!("{}{}", &text[..start], &text[end..]);
            std::fs::write(target, &updated).context("cannot write an edited chunk")?;
            return Ok(Some(false));
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
            Ok(Some(true))
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
            Ok(Some(true))
        }
        // A map that no longer describes the chunk is worse than none.
        (ChunkMap::File(path), Err(error)) => {
            tracing::warn!(%error, "dropping an unreadable chunk source map");
            std::fs::remove_file(path)?;
            Ok(Some(false))
        }
        (ChunkMap::Inline(url), Err(error)) => {
            tracing::warn!(%error, "dropping an unreadable inline chunk source map");
            let comment = map_reference(text)
                .filter(|comment| comment.url == url)
                .context("cannot locate the unreadable inline source map comment")?;
            let (start, end) = (comment.start, comment.end);
            let updated = format!("{}{}", &text[..start], &text[end..]);
            std::fs::write(target, &updated).context("cannot write an edited chunk")?;
            Ok(Some(false))
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

/// Whether a file is an HTML page (`.html` or `.htm`).
pub(crate) fn is_html(path: &Path) -> bool {
    path.extension()
        .is_some_and(|e| e.eq_ignore_ascii_case("html") || e.eq_ignore_ascii_case("htm"))
}

fn is_text_asset(path: &Path) -> bool {
    is_html(path)
        || path.extension().is_some_and(|e| {
            matches!(
                e.to_str(),
                Some("js" | "mjs" | "cjs" | "css" | "json" | "map" | "webmanifest")
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
/// whitespace, `>`, `,`, the end of the text or a `.map` suffix, and not in
/// an absolute or protocol-relative URL. Other occurrences (prose, data or
/// other hosts' files that merely share the name) stay unchanged.
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
        // The URL token the name ends: an absolute or protocol-relative URL
        // names another host's file, which keeps its name.
        let token_start = text[..at]
            .rfind(|c: char| {
                c.is_whitespace() || matches!(c, '"' | '\'' | '`' | '(' | '=' | '<' | '>' | ',')
            })
            .map_or(0, |d| d + 1);
        let token = &text[token_start..at];
        let external = token.contains("://") || token.starts_with("//");
        if matches!(before, Some('/' | '"' | '\'' | '`' | '(' | '=')) && follows && !external {
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
fn rehash(source: &Path, root: &Path, edited: &BTreeSet<PathBuf>) -> Result<usize> {
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
    // A reference can spell a name with URL or HTML escapes (`%2D`,
    // `&#45;`), which rewriting cannot follow. Such names keep their original
    // file next to the renamed one, together with every renamed file that the
    // original references, so the encoded reference still loads a complete,
    // consistent original graph.
    // File-name-like tokens of a text and how often each occurs.
    let tokens = |text: &str| {
        let mut counts = BTreeMap::<String, usize>::new();
        for token in
            text.split(|c: char| !(c.is_ascii_alphanumeric() || matches!(c, '-' | '_' | '.')))
        {
            if renamed.contains_key(token) {
                *counts.entry(token.to_owned()).or_default() += 1;
            }
        }
        counts
    };
    let mut retained = BTreeSet::<String>::new();
    for text in texts.values() {
        if !text.contains('%') && !text.contains('&') {
            continue;
        }
        // Each reference decodes on its own: an unknown entity elsewhere in
        // the text must not hide an encoded file name.
        let unescaped = decode_references_lossy(text);
        let unescaped = crate::compiler::percent_decode(&unescaped);
        if unescaped == *text {
            continue;
        }
        let plain = tokens(text);
        for (name, count) in tokens(&unescaped) {
            if count > plain.get(&name).copied().unwrap_or(0) {
                retained.insert(name);
            }
        }
    }
    let original = |old: &str| -> Vec<PathBuf> { hashed.get(old).cloned().unwrap_or_default() };
    let mut pending: Vec<String> = retained.iter().cloned().collect();
    while let Some(old) = pending.pop() {
        for path in original(&old) {
            let relative = path.strip_prefix(root).unwrap_or(&path);
            let text = std::fs::read_to_string(source.join(relative)).unwrap_or_default();
            for other in renamed.keys() {
                if !retained.contains(other) && !references(&text, other).is_empty() {
                    retained.insert(other.clone());
                    pending.push(other.clone());
                }
            }
        }
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
            if retained.contains(&stem) {
                let relative = file.strip_prefix(root).unwrap_or(file);
                std::fs::copy(source.join(relative), file)
                    .context("cannot retain an encoded-referenced chunk")?;
            }
        }
    }
    if !retained.is_empty() {
        tracing::warn!(
            retained = retained.len(),
            "kept original chunks that encoded references still name"
        );
    }
    Ok(renamed.len())
}

/// Rejects bundles that bundle mode cannot publish faithfully, before
/// analysis in check and output modes:
/// - symbolic links: analysis would follow them to their targets while the
///   staged copy would materialize separate files (or external contents);
/// - HTML pages with Subresource Integrity: the digests would no longer match
///   edited chunks.
pub fn preflight(dir: &Path) -> Result<()> {
    fn reject_links(dir: &Path) -> Result<()> {
        for entry in std::fs::read_dir(dir).context("cannot read bundle directory")? {
            let entry = entry?;
            let kind = entry.file_type()?;
            anyhow::ensure!(
                !kind.is_symlink(),
                "bundle directories with symbolic links are unsupported; copy the linked files \
                 into the bundle"
            );
            if kind.is_dir() {
                reject_links(&entry.path())?;
            }
        }
        Ok(())
    }
    reject_links(dir)?;
    let mut files = Vec::new();
    files_under(dir, &mut files)?;
    for file in &files {
        if is_html(file)
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

/// Relative path and SHA-256 of every file of a bundle directory.
pub type Fingerprint = BTreeMap<PathBuf, [u8; 32]>;

/// Fingerprints a bundle directory, so publication can verify that it
/// copies the same files that were analyzed.
pub fn fingerprint(dir: &Path) -> Result<Fingerprint> {
    use sha2::{Digest, Sha256};
    let mut files = Vec::new();
    files_under(dir, &mut files)?;
    files
        .into_iter()
        .map(|file| {
            let bytes = std::fs::read(&file).context("cannot read a bundle file")?;
            let relative = file.strip_prefix(dir).unwrap_or(&file).to_path_buf();
            Ok((relative, Sha256::digest(&bytes).into()))
        })
        .collect()
}

/// A complete bundle output in an operation-owned staging directory. It is
/// published only by [`Staged::commit`]; dropping it removes the staging
/// directory, so a failure after staging never leaves a published result.
pub struct Staged {
    _staging: tempfile::TempDir,
    root: PathBuf,
    destination: PathBuf,
}

impl Staged {
    /// Moves the staged output to its destination. The rename fails instead
    /// of replacing a destination that appeared after the initial check.
    pub fn commit(self) -> Result<()> {
        crate::output::rename_exclusive(&self.root, &self.destination)
            .context("cannot publish bundle output")
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
    analyzed: &Fingerprint,
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
    // The edits describe the directory as analyzed: a bundler or watcher
    // that changed it meanwhile would leave a mixed build.
    anyhow::ensure!(
        fingerprint(&root)? == *analyzed,
        "the bundle directory changed during analysis; rerun typeopt on a stable build"
    );
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
        let chain = outcome.chains.get(name).map_or(&[][..], Vec::as_slice);
        match update_chunk_map(&root, &target, original, text, chain)? {
            Some(true) => maps += 1,
            Some(false) => dropped += 1,
            None => {}
        }
    }
    let rehashed = if crate::app::keep_chunk_names() {
        0
    } else {
        rehash(source, &root, &edited)?
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
        let mut map = builder.into_sourcemap();
        map.set_source_root(Some("../src"));
        let insertion = "import\"x\";";
        let chain = vec![vec![crate::plan::Edit {
            start: 4,
            end: 4,
            replacement: insertion.to_owned(),
        }]];
        let changed = format!("a();{insertion}b();");
        let composed = super::compose_bundle_map(&map, original, &changed, &chain).unwrap();
        assert_eq!(composed.get_source_root(), Some("../src"));
        let tokens: Vec<_> = composed
            .tokens()
            .map(|t| (t.get_dst_col(), t.get_src_line()))
            .collect();
        // The insertion maps where `b()` did; `b()` keeps its own mapping.
        assert_eq!(tokens, [(0, 10), (4, 20), (4 + insertion.len() as u32, 20)]);
    }

    #[test]
    fn fingerprints_detect_changed_and_added_files() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("a.js"), "a").unwrap();
        let before = super::fingerprint(dir.path()).unwrap();
        assert_eq!(super::fingerprint(dir.path()).unwrap(), before);
        std::fs::write(dir.path().join("a.js"), "b").unwrap();
        assert_ne!(super::fingerprint(dir.path()).unwrap(), before);
        std::fs::write(dir.path().join("a.js"), "a").unwrap();
        std::fs::write(dir.path().join("b.js"), "b").unwrap();
        assert_ne!(super::fingerprint(dir.path()).unwrap(), before);
    }

    #[test]
    fn deleted_code_keeps_no_mapping() {
        let original = "a();b();c();";
        let mut builder = sourcemap::SourceMapBuilder::new(None);
        builder.add(0, 0, 10, 0, Some("src.ts"), None, false);
        builder.add(0, 4, 20, 0, Some("src.ts"), None, false);
        builder.add(0, 8, 30, 0, Some("src.ts"), None, false);
        let map = builder.into_sourcemap();
        // Delete `b();`: `c();` moves to column 4 and keeps its own mapping.
        let chain = vec![vec![crate::plan::Edit {
            start: 4,
            end: 8,
            replacement: String::new(),
        }]];
        let composed = super::compose_bundle_map(&map, original, "a();c();", &chain).unwrap();
        let tokens: Vec<_> = composed
            .tokens()
            .map(|t| (t.get_dst_col(), t.get_src_line()))
            .collect();
        assert_eq!(tokens, [(0, 10), (4, 30)]);
    }

    #[test]
    fn locates_source_map_comments_exactly() {
        let url = "data:application/json;base64,e30=";
        let line = format!("const u=\"http://x\";f();\n//# sourceMappingURL={url}");
        let comment = super::map_reference(&line).unwrap();
        assert_eq!(comment.url, url);
        assert_eq!(&line[..comment.start], "const u=\"http://x\";f();\n");
        assert_eq!(comment.end, line.len());
        let block = format!("const u=\"http://x\";f();\n/*# sourceMappingURL={url} */\n");
        let comment = super::map_reference(&block).unwrap();
        assert_eq!(comment.url, url);
        assert_eq!(
            format!("{}{}", &block[..comment.start], &block[comment.end..]),
            "const u=\"http://x\";f();\n\n"
        );
        // A trailing directive after code on the same line.
        let inline = "f();//# sourceMappingURL=maps/f.js.map\n";
        let comment = super::map_reference(inline).unwrap();
        assert_eq!(
            (comment.url, &inline[..comment.start]),
            ("maps/f.js.map", "f();")
        );
        // Directive-like text inside strings or followed by code is not one.
        for text in [
            "const m=\"//# sourceMappingURL=https://maps.example/x.map\";",
            "const m=\"//# sourceMappingURL=a.map\"",
            "f();/*# sourceMappingURL=a.js.map */g();",
            "const t=`\n//# sourceMappingURL=a.js.map\n`;",
            "const t=`//# sourceMappingURL=a.js.map`",
        ] {
            assert!(super::map_reference(text).is_none(), "{text}");
        }
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
        // Another host's file with the same name keeps its URL.
        let external = r#"src="https://cdn.example/index-DnKDBunT.js" u='//cdn.example/x/index-DnKDBunT.js' l="/assets/index-DnKDBunT.js""#;
        assert_eq!(references(external, name).len(), 1);
    }

    #[test]
    fn decodes_javascript_string_escapes() {
        assert_eq!(
            super::decode_js_string(r"./lib\u002ejs").as_deref(),
            Some("./lib.js")
        );
        assert_eq!(
            super::decode_js_string(r"./\x6cib\u{2E}js").as_deref(),
            Some("./lib.js")
        );
        assert_eq!(super::decode_js_string(r"./a\'b").as_deref(), Some("./a'b"));
        assert_eq!(
            super::decode_js_string(r"\uD83D\uDE00").as_deref(),
            Some("😀")
        );
        assert_eq!(super::decode_js_string(r"bad\u12"), None);
        // A high surrogate must be followed by a low surrogate.
        assert_eq!(super::decode_js_string(r"\uD83D\u0041"), None);
        assert_eq!(super::decode_js_string(r"\uD83D\uE000"), None);
    }

    #[test]
    fn finds_literal_dynamic_imports_across_comments() {
        assert_eq!(
            super::literal_dynamic_imports(
                "import/* a */(\"./a.js\");import(/* b */'./b.js');import \
                 (\n`./c.js`);x.import('no');"
            )
            .literals,
            ["./a.js", "./b.js", "./c.js"]
        );
        let found = super::literal_dynamic_imports("const p='./lib.js';import(p);");
        assert!(found.literals.is_empty() && found.non_literal);
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
        // Character references in attribute values are decoded.
        let tags = html_tags(
            "<script src=\"shared&#46;js\" type=\"&#x6d;odule\" data-x=\"a&amp;b&c\"></script><a \
             href=\"&unknownref;\">",
        );
        assert_eq!(tags[0].attribute("src"), Some("shared.js"));
        assert_eq!(tags[0].attribute("type"), Some("module"));
        assert_eq!(tags[0].attribute("data-x"), Some("a&b&c"));
        assert_eq!(tags[1].undecodable, ["href"]);
        // Legacy references without `;` decode unless alphanumerics follow.
        assert_eq!(
            super::decode_references("shared&amp.js").as_deref(),
            Some("shared&.js")
        );
        assert_eq!(
            super::decode_references("a&ampx").as_deref(),
            Some("a&ampx")
        );
        assert_eq!(
            super::decode_references("a&amp=b").as_deref(),
            Some("a&amp=b")
        );
        assert_eq!(super::decode_references("a&copy.js"), None);
        // Lossy decoding keeps unknown references and decodes the others.
        assert_eq!(
            super::decode_references_lossy("&copy; index&#45;DnKDBunT.js &unknown;"),
            "&copy; index-DnKDBunT.js &unknown;"
        );
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
