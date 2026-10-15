// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Stage 2 publication: writes edited bundler chunks next to an unchanged
//! copy of the bundle directory, composes the bundler's source maps with the
//! accepted edits and renames edited content-hashed chunks.
use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
};

use anyhow::{Context, Result, bail, ensure};
use swc_core::{
    common::{FileName, GLOBALS, Globals, Mark, SourceMap, SyntaxContext, sync::Lrc},
    ecma::{
        ast::{
            BindingIdent, CallExpr, Callee, ClassDecl, ClassExpr, EsVersion, Expr, FnDecl, FnExpr,
            Id, ImportDecl, ImportSpecifier, Lit, MemberProp, ModuleExportName, ObjectLit, Prop,
            PropName, PropOrSpread, WithStmt,
        },
        parser::{EsSyntax, Syntax, parse_file_as_program},
        transforms::base::resolver,
        visit::{Visit, VisitMutWith, VisitWith},
    },
};

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
pub(crate) fn decode_references_lossy(value: &str) -> String {
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
                .map(numeric_reference)
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

/// The character an HTML numeric character reference produces: NUL,
/// surrogates and values beyond Unicode become U+FFFD, and C1 controls map
/// through the Windows-1252 table, as browsers parse them.
fn numeric_reference(value: u32) -> char {
    const C1: [(u32, char); 27] = [
        (0x80, '\u{20AC}'),
        (0x82, '\u{201A}'),
        (0x83, '\u{0192}'),
        (0x84, '\u{201E}'),
        (0x85, '\u{2026}'),
        (0x86, '\u{2020}'),
        (0x87, '\u{2021}'),
        (0x88, '\u{02C6}'),
        (0x89, '\u{2030}'),
        (0x8a, '\u{0160}'),
        (0x8b, '\u{2039}'),
        (0x8c, '\u{0152}'),
        (0x8e, '\u{017D}'),
        (0x91, '\u{2018}'),
        (0x92, '\u{2019}'),
        (0x93, '\u{201C}'),
        (0x94, '\u{201D}'),
        (0x95, '\u{2022}'),
        (0x96, '\u{2013}'),
        (0x97, '\u{2014}'),
        (0x98, '\u{02DC}'),
        (0x99, '\u{2122}'),
        (0x9a, '\u{0161}'),
        (0x9b, '\u{203A}'),
        (0x9c, '\u{0153}'),
        (0x9e, '\u{017E}'),
        (0x9f, '\u{0178}'),
    ];
    if value == 0 {
        return '\u{FFFD}';
    }
    if let Some((_, c)) = C1.iter().find(|(code, _)| *code == value) {
        return *c;
    }
    char::from_u32(value).unwrap_or('\u{FFFD}')
}

impl HtmlTag {
    pub fn attribute(&self, name: &str) -> Option<&str> {
        self.attributes
            .iter()
            .find(|(n, _)| n == name)
            .map(|(_, v)| v.as_str())
    }
}

/// Start tags of an HTML document, and end tags as `/name`. Comments and the
/// contents of raw-text
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
        // End tags are reported as `/name`, so callers can track nesting.
        let end_tag = html[i..].starts_with('/');
        if end_tag {
            i += 1;
        }
        let start = i;
        while i < bytes.len() && bytes[i].is_ascii_alphanumeric() {
            i += 1;
        }
        if i == start {
            continue;
        }
        if end_tag {
            let name = format!("/{}", html[start..i].to_ascii_lowercase());
            i = html[i..].find('>').map_or(html.len(), |end| i + end + 1);
            tags.push(HtmlTag {
                name,
                attributes: Vec::new(),
                undecodable: Vec::new(),
                text: None,
            });
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

/// Text with JavaScript (`\u0041`, `\u{41}`, `\x41`) and CSS (`\41 `,
/// `\-`) backslash escapes decoded where they form a character; other text
/// stays as it is. Used only to find file names spelled with escapes.
fn decode_backslash_escapes_lossy(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(at) = rest.find('\\') {
        out.push_str(&rest[..at]);
        rest = &rest[at + 1..];
        let hex_run = |s: &str, max: usize| {
            s.char_indices()
                .take(max)
                .take_while(|(_, c)| c.is_ascii_hexdigit())
                .last()
                .map_or(0, |(i, c)| i + c.len_utf8())
        };
        let decoded = if let Some(braced) = rest.strip_prefix("u{") {
            braced.find('}').and_then(|end| {
                u32::from_str_radix(&braced[..end], 16)
                    .ok()
                    .and_then(char::from_u32)
                    .map(|c| (c, 2 + end + 1))
            })
        } else if let Some(unit) = rest.strip_prefix('u').filter(|u| hex_run(u, 4) == 4) {
            u32::from_str_radix(&unit[..4], 16)
                .ok()
                .and_then(char::from_u32)
                .map(|c| (c, 5))
        } else if let Some(byte) = rest.strip_prefix('x').filter(|x| hex_run(x, 2) == 2) {
            u32::from_str_radix(&byte[..2], 16)
                .ok()
                .and_then(char::from_u32)
                .map(|c| (c, 3))
        } else if hex_run(rest, 6) > 0 {
            // CSS: up to six hex digits and one optional whitespace.
            let len = hex_run(rest, 6);
            let space = usize::from(rest[len..].starts_with([' ', '\t', '\n']));
            u32::from_str_radix(&rest[..len], 16)
                .ok()
                .and_then(char::from_u32)
                .map(|c| (c, len + space))
        } else {
            rest.chars().next().map(|c| (c, c.len_utf8()))
        };
        match decoded {
            Some((c, len)) => {
                out.push(c);
                rest = &rest[len..];
            }
            None => out.push('\\'),
        }
    }
    out.push_str(rest);
    out
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
            // URL path semantics: no query or fragment, `%` escapes decoded.
            let url = crate::compiler::percent_decode(
                reference.split(['?', '#']).next().unwrap_or_default(),
            );
            let url = url.as_str();
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

/// Bytes of a chunk's code: the text without its trailing source map
/// comment (inline or referencing a file). Source maps are debugging
/// metadata and never count as size, so composing, re-encoding or removing a
/// map comment does not change the measured size.
/// Where the trailing source map comment of a chunk starts, if it has one.
pub(crate) fn map_comment_start(text: &str) -> Option<usize> {
    map_reference(text).map(|comment| comment.start)
}

pub(crate) fn chunk_code_len(text: &str) -> usize {
    map_reference(text).map_or(text.len(), |comment| {
        text.len() - (comment.end - comment.start)
    })
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
        let pragma = text[..at].trim_end_matches([' ', '\t']);
        let Some(opener) = pragma.strip_suffix(['#', '@']) else {
            continue;
        };
        let Some(start) = opener.len().checked_sub(2) else {
            continue;
        };
        // A URL ends at whitespace; in a block directive also at the `*/`
        // that closes it (a `*` alone is a valid URL character).
        let block = text.get(start..opener.len()) == Some("/*");
        let rest = &text[url_start..];
        let whitespace = rest.find(char::is_whitespace).unwrap_or(rest.len());
        let url_end = url_start
            + if block {
                rest.find("*/")
                    .map_or(whitespace, |close| close.min(whitespace))
            } else {
                whitespace
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
/// Composes the source map of an edited chunk. `text` is the edited chunk
/// that `chain` produced from `original`; `published` is the text written,
/// which differs from `text` when minification replaced its code, and then
/// `minified` maps `published` back to `text`.
#[allow(clippy::too_many_arguments)]
fn update_chunk_map(
    root: &Path,
    target: &Path,
    original: &str,
    text: &str,
    published: &str,
    minified: Option<&sourcemap::SourceMap>,
    chain: &[Vec<Edit>],
    original_maps: &mut BTreeMap<PathBuf, Vec<u8>>,
) -> Result<Option<bool>> {
    let location = match chunk_map(root, target, published) {
        None => return Ok(None),
        Some(MapRef::Local(location)) => location,
        Some(MapRef::Uncomposable(url)) => {
            // Such a map describes the pre-optimization chunk and cannot be
            // composed here; a missing map is better than a wrong one.
            tracing::warn!("dropping an uncomposable source map reference of an edited chunk");
            let comment = map_reference(published)
                .filter(|comment| comment.url == url)
                .context("cannot locate the source map comment of an edited chunk")?;
            let (start, end) = (comment.start, comment.end);
            let updated = format!("{}{}", &published[..start], &published[end..]);
            std::fs::write(target, &updated).context("cannot write an edited chunk")?;
            return Ok(Some(false));
        }
    };
    if let ChunkMap::File(path) = &location
        && let Ok(bytes) = std::fs::read(path)
    {
        original_maps.entry(path.clone()).or_insert(bytes);
    }
    let decoded = match &location {
        ChunkMap::Inline(url) => sourcemap::decode_data_url(url).map_err(anyhow::Error::from),
        ChunkMap::File(path) => std::fs::read(path)
            .map_err(anyhow::Error::from)
            .and_then(|bytes| sourcemap::decode_slice(&bytes).map_err(anyhow::Error::from)),
    };
    let composed = match decoded {
        Ok(sourcemap::DecodedMap::Regular(map)) => {
            compose_bundle_map(&map, original, text, chain).map(|mut edited| {
                // The composed map leads from the edited chunk to the
                // sources; the minifier's map leads from the published code
                // to the edited chunk. Adjusting the first by the second
                // leads from the published code to the sources.
                if let Some(minified) = minified {
                    edited.adjust_mappings(minified);
                }
                edited
            })
        }
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
            let at = published
                .rfind(&url)
                .context("inline source map disappeared")?;
            let updated = format!(
                "{}{}{}",
                &published[..at],
                map.to_data_url()?,
                &published[at + url.len()..]
            );
            std::fs::write(target, &updated).context("cannot write an edited chunk")?;
            Ok(Some(true))
        }
        // A map that no longer describes the chunk is worse than none.
        (ChunkMap::File(path), Err(error)) => {
            tracing::warn!(%error, "dropping an unreadable chunk source map");
            std::fs::remove_file(path)?;
            // An explicit trailing directive would point at the deleted map;
            // a hidden adjacent map has no directive to remove.
            if let Some(comment) = map_reference(published) {
                let (start, end) = (comment.start, comment.end);
                let updated = format!("{}{}", &published[..start], &published[end..]);
                std::fs::write(target, &updated).context("cannot write an edited chunk")?;
            }
            Ok(Some(false))
        }
        (ChunkMap::Inline(url), Err(error)) => {
            tracing::warn!(%error, "dropping an unreadable inline chunk source map");
            let comment = map_reference(published)
                .filter(|comment| comment.url == url)
                .context("cannot locate the unreadable inline source map comment")?;
            let (start, end) = (comment.start, comment.end);
            let updated = format!("{}{}", &published[..start], &published[end..]);
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
/// separator, quote, `(` (also `url(` followed by whitespace) or `=`, and
/// before a quote, `)`, `?`, `#`, whitespace, `>`, `,`, the end of the text or
/// a `.map` suffix, and not in an absolute or protocol-relative URL. Other
/// occurrences (prose, data or other hosts' files that merely share the name)
/// stay unchanged.
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
        // Escapes (`https:\/\/`, `&#47;`, `%2F`) do not hide another host.
        let token = crate::compiler::percent_decode(&decode_references_lossy(
            &decode_backslash_escapes_lossy(&text[token_start..at]),
        ));
        let external = token.contains("://") || token.starts_with("//");
        // An unquoted HTML attribute value may follow `=` after whitespace,
        // and a CSS `url(` token may be followed by whitespace before its
        // unquoted value (`url( name )`).
        let spaced = text[..at].trim_end_matches([' ', '\t', '\n', '\r', '\u{c}']);
        let after_assignment = spaced.ends_with('=');
        // The `url` must begin a complete CSS identifier token: `myurl(`,
        // `curl(`, `--url(` and `.url(` are not CSS `url(` tokens.
        let after_css_url = spaced.strip_suffix('(').is_some_and(|head| {
            head.len() >= 3
                && head.as_bytes()[head.len() - 3..].eq_ignore_ascii_case(b"url")
                && !head[..head.len() - 3].chars().next_back().is_some_and(|c| {
                    c.is_alphanumeric()
                        || !c.is_ascii()
                        || matches!(c, '-' | '_' | '\\' | '.' | '$')
                })
        });
        let leads = matches!(before, Some('/' | '"' | '\'' | '`' | '(' | '='))
            || (before.is_some_and(|c| c.is_ascii_whitespace())
                && (after_assignment || after_css_url));
        if leads && follows && !external {
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
/// What rehashing did: how many names changed and how many code bytes the
/// retained original chunks add to the output.
struct Rehashed {
    renamed: usize,
    retained_bytes: usize,
}

fn rehash(
    root: &Path,
    edited: &BTreeSet<PathBuf>,
    originals: &BTreeMap<PathBuf, String>,
    original_maps: &BTreeMap<PathBuf, Vec<u8>>,
    frozen: &BTreeSet<PathBuf>,
) -> Result<Rehashed> {
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
    // Pinned chunks (excluded chunks and what they import) stay
    // byte-for-byte unchanged: never renamed or rewritten. They keep the
    // original files they reference (below). Other boundary chunks are
    // rewritten and renamed like every text asset.
    let frozen_names: BTreeSet<String> = frozen.iter().map(|p| base(p)).collect();
    // The rename set: edited hashed chunks and, transitively, hashed files
    // that reference a renamed file.
    // References name only the file name, so a name that a frozen chunk
    // carries anywhere keeps it everywhere.
    let mut renaming: BTreeSet<String> = edited
        .iter()
        .map(|p| base(p))
        .filter(|n| hashed.contains_key(n) && !frozen_names.contains(n))
        .collect();
    loop {
        let more: Vec<String> = referenced
            .iter()
            .filter(|(name, targets)| {
                !renaming.contains(*name)
                    && !frozen_names.contains(*name)
                    && targets.iter().any(|t| renaming.contains(t))
            })
            .map(|(name, _)| name.clone())
            .collect();
        if more.is_empty() {
            break;
        }
        renaming.extend(more);
    }
    if renaming.is_empty() {
        return Ok(Rehashed {
            renamed: 0,
            retained_bytes: 0,
        });
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
    // Names a new file must not take: every file in the bundle (a rename
    // would replace it) and every name already assigned.
    let mut taken: BTreeSet<String> = files.iter().map(|f| base(f)).collect();
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
            if hashed_name(&new_name).is_some()
                && !taken.contains(&new_name)
                && !taken.contains(&format!("{new_name}.map"))
            {
                break;
            }
        }
        taken.insert(new_name.clone());
        taken.insert(format!("{new_name}.map"));
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
    // Everything an excluded chunk can import is pinned with it, so a pinned
    // chunk names a renamed chunk only through something that is not an
    // import (a string, for example): it keeps referencing the original name.
    for path in frozen {
        if let Some(text) = texts.get(path) {
            for name in renamed.keys() {
                if !references(text, name).is_empty() {
                    tracing::warn!(
                        boundary = %path.display(),
                        chunk = name.as_str(),
                        "an excluded chunk names a renamed chunk without importing it; the original \
                         is kept"
                    );
                    retained.insert(name.clone());
                }
            }
        }
    }
    for text in texts.values() {
        if !text.contains('%') && !text.contains('&') && !text.contains('\\') {
            continue;
        }
        // Each reference decodes on its own: an unknown entity elsewhere in
        // the text must not hide an encoded file name. JavaScript and CSS
        // escapes (`\u002d`, `\x2d`, `\2d `, `\-`) count as well.
        let unescaped = decode_backslash_escapes_lossy(text);
        let unescaped = decode_references_lossy(&unescaped);
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
    // Original contents come from the analyzed snapshot: the analyzed text
    // of edited chunks and the staged (verified) copy of every other file,
    // never the live input directory.
    let original_text = |path: &PathBuf| -> String {
        originals
            .get(path)
            .or_else(|| texts.get(path))
            .cloned()
            .unwrap_or_default()
    };
    let mut pending: Vec<String> = retained.iter().cloned().collect();
    while let Some(old) = pending.pop() {
        for path in hashed.get(&old).into_iter().flatten() {
            let text = original_text(path);
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
        if frozen.contains(file) {
            continue;
        }
        let updated = rewrite(text, &renamed);
        if &updated != text {
            std::fs::write(file, updated).context("cannot rewrite a chunk reference")?;
        }
    }
    // A retained original keeps its original map: the adjacent map moves
    // with the renamed chunk, so the original goes back to the old path; a
    // map elsewhere that was composed for the optimized chunk no longer
    // describes the original, which then drops its map comment.
    let original_map = |path: &PathBuf| -> Option<Vec<u8>> {
        let canonical = path.canonicalize().unwrap_or_else(|_| path.clone());
        original_maps
            .get(&canonical)
            .cloned()
            .or_else(|| std::fs::read(path).ok())
    };
    let mut restore = Vec::<(PathBuf, Vec<u8>)>::new();
    let mut retained_bytes = 0usize;
    for old in &retained {
        for path in hashed.get(old).into_iter().flatten() {
            let mut text = original_text(path);
            let adjacent = path.with_file_name(format!("{old}.map"));
            if let Some(map) = original_map(&adjacent).filter(|_| adjacent.exists()) {
                restore.push((adjacent, map));
            } else if let Some(MapRef::Local(ChunkMap::File(map))) = chunk_map(root, path, &text)
                && original_maps.contains_key(&map)
                && let Some(comment) = map_reference(&text)
            {
                let (start, end) = (comment.start, comment.end);
                text = format!("{}{}", &text[..start], &text[end..]);
            }
            if path
                .extension()
                .is_some_and(|e| e == "js" || e == "mjs" || e == "cjs")
            {
                retained_bytes += chunk_code_len(&text);
            }
            restore.push((path.clone(), text.into_bytes()));
        }
    }
    for file in &files {
        let name = base(file);
        let (stem, suffix) = match name.strip_suffix(".map") {
            Some(stem) => (stem.to_owned(), ".map"),
            None => (name.clone(), ""),
        };
        if let Some(new) = renamed.get(&stem)
            && !frozen.contains(file)
        {
            std::fs::rename(file, file.with_file_name(format!("{new}{suffix}")))
                .context("cannot rename a rehashed chunk")?;
        }
    }
    for (path, bytes) in restore {
        std::fs::write(&path, bytes).context("cannot retain an original chunk")?;
    }
    if !retained.is_empty() {
        tracing::warn!(
            retained = retained.len(),
            "kept original chunks that encoded references still name"
        );
    }
    Ok(Rehashed {
        renamed: renamed.len(),
        retained_bytes,
    })
}

/// Precache entries `{url:"…",revision:"…"}` (either order) of a service
/// worker manifest, as Workbox and vite-plugin-pwa emit them inside a
/// `precacheAndRoute`/`precache` array: the decoded URL and the byte span of
/// the revision string. Parsing JavaScript excludes apparent calls in strings,
/// comments, template text and regular expressions.
fn precache_entries(text: &str) -> Vec<(String, usize, usize, bool)> {
    struct WorkboxBindings {
        direct: BTreeSet<Id>,
        namespace: BTreeSet<Id>,
        ambiguous: BTreeSet<String>,
        declared: BTreeSet<String>,
    }

    impl WorkboxBindings {
        fn mark_declared(&mut self, name: &str) {
            self.declared.insert(name.to_owned());
            if matches!(name, "precache" | "precacheAndRoute") {
                self.ambiguous.insert(name.to_owned());
            }
        }
    }

    impl Visit for WorkboxBindings {
        fn visit_import_decl(&mut self, declaration: &ImportDecl) {
            let workbox = declaration.src.value == "workbox-precaching";
            for specifier in &declaration.specifiers {
                if let ImportSpecifier::Namespace(namespace) = specifier {
                    if workbox {
                        self.namespace.insert(namespace.local.to_id());
                    } else {
                        self.mark_declared(namespace.local.sym.as_ref());
                    }
                    continue;
                }
                let ImportSpecifier::Named(named) = specifier else {
                    self.mark_declared(named_local_name(specifier));
                    continue;
                };
                let imported = named
                    .imported
                    .as_ref()
                    .and_then(|name| match name {
                        ModuleExportName::Ident(name) => Some(name.sym.as_ref()),
                        ModuleExportName::Str(name) => name.value.as_str(),
                    })
                    .unwrap_or(named.local.sym.as_ref());
                if workbox && matches!(imported, "precache" | "precacheAndRoute") {
                    let local = named.local.sym.to_string();
                    if self.declared.contains(&local) {
                        self.ambiguous.insert(local.clone());
                    }
                    self.direct.insert(named.local.to_id());
                } else {
                    self.mark_declared(named.local.sym.as_ref());
                }
            }
        }

        fn visit_binding_ident(&mut self, binding: &BindingIdent) {
            self.mark_declared(binding.id.sym.as_ref());
            binding.visit_children_with(self);
        }

        fn visit_fn_decl(&mut self, declaration: &FnDecl) {
            self.mark_declared(declaration.ident.sym.as_ref());
            declaration.visit_children_with(self);
        }

        fn visit_fn_expr(&mut self, expression: &FnExpr) {
            if let Some(ident) = &expression.ident {
                self.mark_declared(ident.sym.as_ref());
            }
            expression.visit_children_with(self);
        }

        fn visit_class_decl(&mut self, declaration: &ClassDecl) {
            self.mark_declared(declaration.ident.sym.as_ref());
            declaration.visit_children_with(self);
        }

        fn visit_class_expr(&mut self, expression: &ClassExpr) {
            if let Some(ident) = &expression.ident {
                self.mark_declared(ident.sym.as_ref());
            }
            expression.visit_children_with(self);
        }
    }

    fn named_local_name(specifier: &ImportSpecifier) -> &str {
        specifier.local().sym.as_ref()
    }

    struct Entries<'a> {
        text: &'a str,
        base: u32,
        unresolved_ctxt: SyntaxContext,
        direct_workbox_bindings: BTreeSet<Id>,
        namespace_workbox_bindings: BTreeSet<Id>,
        ambiguous_bindings: BTreeSet<String>,
        with_depth: usize,
        values: Vec<(String, usize, usize, bool)>,
    }

    impl Entries<'_> {
        fn name(key: &PropName) -> Option<&str> {
            match key {
                PropName::Ident(name) => Some(name.sym.as_ref()),
                PropName::Str(name) => Some(name.value.as_str()?),
                _ => None,
            }
        }

        fn value(&self, value: &swc_core::ecma::ast::Str) -> Option<(String, usize, usize)> {
            let start = value.span.lo.0.checked_sub(self.base)? as usize + 1;
            let end = (value.span.hi.0.checked_sub(self.base)? as usize).checked_sub(1)?;
            let raw = self.text.get(start..end)?;
            Some((decode_js_string(raw)?, start, end))
        }

        fn entry(&self, object: &ObjectLit) -> Option<(String, usize, usize)> {
            let (mut url, mut revision) = (None, None);
            for property in &object.props {
                let PropOrSpread::Prop(property) = property else {
                    return None;
                };
                let Prop::KeyValue(property) = &**property else {
                    return None;
                };
                match Self::name(&property.key) {
                    Some("url") if url.is_none() => {
                        let Expr::Lit(Lit::Str(value)) = &*property.value else {
                            return None;
                        };
                        url = Some(self.value(value)?.0);
                    }
                    Some("revision") if revision.is_none() => {
                        let Expr::Lit(Lit::Str(value)) = &*property.value else {
                            return None;
                        };
                        let (_, start, end) = self.value(value)?;
                        // Keep replacement spans simple so source-map offsets
                        // stay stable and the revision remains a literal.
                        if self.text[start..end].contains('\\') {
                            return None;
                        }
                        revision = Some((start, end));
                    }
                    Some("url" | "revision") => return None,
                    _ => {}
                }
            }
            let (start, end) = revision?;
            Some((url?, start, end))
        }
    }

    impl Visit for Entries<'_> {
        fn visit_with_stmt(&mut self, statement: &WithStmt) {
            statement.obj.visit_with(self);
            self.with_depth += 1;
            statement.body.visit_with(self);
            self.with_depth -= 1;
        }

        fn visit_call_expr(&mut self, call: &CallExpr) {
            let (name, trusted) = match &call.callee {
                Callee::Expr(callee) => match &**callee {
                    Expr::Ident(name) => (
                        Some(name.sym.as_ref()),
                        self.direct_workbox_bindings.contains(&name.to_id())
                            || (matches!(name.sym.as_ref(), "precache" | "precacheAndRoute")
                                && !self.ambiguous_bindings.contains(name.sym.as_ref())),
                    ),
                    Expr::Member(member) => match &member.prop {
                        MemberProp::Ident(name) => (
                            Some(name.sym.as_ref()),
                            is_workbox_receiver(
                                &member.obj,
                                &self.namespace_workbox_bindings,
                                self.unresolved_ctxt,
                            ),
                        ),
                        _ => (None, false),
                    },
                    _ => (None, false),
                },
                _ => (None, false),
            };
            let trusted = trusted && self.with_depth == 0;
            if name.is_some_and(|name| {
                matches!(name, "precache" | "precacheAndRoute")
                    || self
                        .direct_workbox_bindings
                        .iter()
                        .any(|(symbol, _)| symbol.as_ref() == name)
            }) && let Some(first) = call.args.first()
                && let Expr::Array(array) = &*first.expr
            {
                for element in array.elems.iter().flatten() {
                    if let Expr::Object(object) = &*element.expr
                        && let Some(entry) = self.entry(object)
                    {
                        let (url, start, end) = entry;
                        self.values.push((url, start, end, trusted));
                    }
                }
            }
            call.visit_children_with(self);
        }
    }

    fn is_workbox_receiver(
        receiver: &Expr,
        namespace_workbox_bindings: &BTreeSet<Id>,
        unresolved_ctxt: SyntaxContext,
    ) -> bool {
        match receiver {
            Expr::Ident(name) => {
                // The resolver marks unresolved globals with its supplied
                // unresolved context and gives local bindings distinct IDs.
                (name.sym == "workbox" && name.ctxt == unresolved_ctxt)
                    || namespace_workbox_bindings.contains(&name.to_id())
            }
            Expr::Member(member) => {
                let MemberProp::Ident(name) = &member.prop else {
                    return false;
                };
                (name.sym == "workbox"
                    && matches!(&*member.obj, Expr::Ident(root)
                        if matches!(root.sym.as_ref(), "self" | "globalThis" | "window")
                            && root.ctxt == unresolved_ctxt))
                    || (name.sym == "precaching"
                        && is_workbox_receiver(
                            &member.obj,
                            namespace_workbox_bindings,
                            unresolved_ctxt,
                        ))
            }
            _ => false,
        }
    }

    let source_map: Lrc<SourceMap> = Default::default();
    let file = source_map.new_source_file(Lrc::new(FileName::Anon), text.to_owned());
    GLOBALS.set(&Globals::new(), || {
        let mut errors = Vec::new();
        let Ok(mut program) = parse_file_as_program(
            &file,
            Syntax::Es(EsSyntax::default()),
            EsVersion::latest(),
            None,
            &mut errors,
        ) else {
            return Vec::new();
        };
        if !errors.is_empty() {
            return Vec::new();
        }
        let unresolved_mark = Mark::new();
        let top_level_mark = Mark::new();
        program.visit_mut_with(&mut resolver(unresolved_mark, top_level_mark, false));
        let unresolved_ctxt = SyntaxContext::empty().apply_mark(unresolved_mark);
        let mut bindings = WorkboxBindings {
            direct: BTreeSet::new(),
            namespace: BTreeSet::new(),
            ambiguous: BTreeSet::new(),
            declared: BTreeSet::new(),
        };
        program.visit_with(&mut bindings);
        let mut visitor = Entries {
            text,
            base: file.start_pos.0,
            unresolved_ctxt,
            direct_workbox_bindings: bindings.direct,
            namespace_workbox_bindings: bindings.namespace,
            ambiguous_bindings: bindings.ambiguous,
            with_depth: 0,
            values: Vec::new(),
        };
        program.visit_with(&mut visitor);
        visitor.values.sort_by_key(|(_, start, _, _)| *start);
        visitor.values
    })
}

/// Maps a Workbox URL to a regular staged file. A root-relative deployment
/// prefix may be absent from the bundle, but an exact staged path wins.
fn precache_target(root: &Path, directory: &Path, url: &str) -> Option<PathBuf> {
    let path = crate::compiler::percent_decode(url.split(['?', '#']).next()?);
    if path.is_empty()
        || path.contains('\\')
        || path.starts_with("//")
        || url::Url::parse(&path).is_ok()
    {
        return None;
    }
    let rooted = path.strip_prefix('/');
    let joined = rooted.map_or_else(|| directory.join(&path), |path| root.join(path));
    let exact = normalize_within(root, &joined)?;
    match std::fs::symlink_metadata(&exact) {
        Ok(metadata) => return metadata.is_file().then_some(exact),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
        Err(_) => return None,
    }
    let rooted = rooted?;
    let segments: Vec<&str> = rooted.split('/').collect();
    // A fallback must only remove a deployment prefix, never a traversal or
    // an empty path component that could change the URL's meaning.
    if segments.len() < 2
        || segments
            .iter()
            .any(|s| s.is_empty() || *s == "." || *s == "..")
    {
        return None;
    }
    (1..segments.len()).find_map(|skip| {
        let candidate = normalize_within(root, &root.join(segments[skip..].join("/")))?;
        std::fs::symlink_metadata(&candidate)
            .ok()
            .filter(|metadata| metadata.is_file())
            .map(|_| candidate)
    })
}

/// Gives precache entries of files whose published content differs from the
/// input a new revision. A service worker keeps a cached file while its
/// revision string is unchanged, so an unchanged revision would keep serving
/// stale HTML that names chunks the output no longer has. The new revision
/// is derived from the new content and keeps the original length, so source
/// maps of the manifest file stay valid. Content-hashed files are left alone:
/// their names already cover their content. Returns how many revisions
/// changed.
fn refresh_precache_revisions(
    source: &Path,
    root: &Path,
    pinned: &BTreeSet<PathBuf>,
) -> Result<usize> {
    let mut files = Vec::new();
    files_under(root, &mut files)?;
    files.sort();

    let eligible = files
        .iter()
        .filter(|file| {
            let name = file.file_name().unwrap().to_string_lossy();
            let script = file
                .extension()
                .is_some_and(|e| e == "js" || e == "mjs" || e == "cjs");
            script && !pinned.contains(*file) && hashed_name(&name).is_none()
        })
        .cloned()
        .collect::<BTreeSet<_>>();
    let mut dependencies = BTreeMap::new();
    for file in &eligible {
        let Ok(text) = std::fs::read_to_string(file) else {
            continue;
        };
        let directory = file.parent().unwrap_or(root);
        let targets = precache_entries(&text)
            .into_iter()
            .filter(|(_, _, _, trusted)| *trusted)
            .filter_map(|(url, _, _, _)| precache_target(root, directory, &url))
            .filter(|target| eligible.contains(target))
            .collect::<Vec<_>>();
        dependencies.insert(file.clone(), targets);
    }

    fn visit_manifest(
        file: &Path,
        dependencies: &BTreeMap<PathBuf, Vec<PathBuf>>,
        visiting: &mut BTreeSet<PathBuf>,
        visited: &mut BTreeSet<PathBuf>,
        order: &mut Vec<PathBuf>,
    ) -> Result<()> {
        if visited.contains(file) {
            return Ok(());
        }
        if !visiting.insert(file.to_path_buf()) {
            bail!(
                "cyclic precache manifest dependency involving {}",
                file.display()
            );
        }
        if let Some(targets) = dependencies.get(file) {
            for target in targets {
                visit_manifest(target, dependencies, visiting, visited, order)?;
            }
        }
        visiting.remove(file);
        visited.insert(file.to_path_buf());
        order.push(file.to_path_buf());
        Ok(())
    }

    let mut ordered = Vec::with_capacity(eligible.len());
    let mut visiting = BTreeSet::new();
    let mut visited = BTreeSet::new();
    for file in &eligible {
        visit_manifest(
            file,
            &dependencies,
            &mut visiting,
            &mut visited,
            &mut ordered,
        )?;
    }

    let mut refreshed = 0;
    for file in ordered {
        let Ok(mut text) = std::fs::read_to_string(&file) else {
            continue;
        };
        let directory = file.parent().unwrap_or(root);
        let mut changed = false;
        for (url, start, end, trusted) in precache_entries(&text.clone()) {
            if !trusted {
                continue;
            }
            // Only regular files of the staged bundle: `..` must not leave it,
            // and a device or FIFO must not block publication.
            let Some(staged) = precache_target(root, directory, &url) else {
                continue;
            };
            let Ok(published) = std::fs::read(&staged) else {
                continue;
            };
            let relative = staged.strip_prefix(root).unwrap_or(&staged);
            if std::fs::read(source.join(relative)).is_ok_and(|input| input == published) {
                continue;
            }
            ensure!(
                end > start || !trusted,
                "cannot refresh empty precache revision in {}",
                relative_display(root, &file)
            );
            if end == start {
                tracing::debug!(
                    manifest = %relative_display(root, &file),
                    target = %relative_display(root, &staged),
                    "skipped an empty revision from an ambiguous precache call"
                );
                continue;
            }
            let digest = crate::compiler::sha256_hex(&published);
            let mut revision: String = digest.chars().cycle().take(end - start).collect();
            if text[start..end] == revision {
                let last = revision.pop().expect("nonempty revision");
                revision.push(if last == '0' { '1' } else { '0' });
            }
            text.replace_range(start..end, &revision);
            refreshed += 1;
            changed = true;
            tracing::debug!(
                manifest = %relative_display(root, &file),
                target = %relative_display(root, &staged),
                "refreshed a precache revision of a changed file"
            );
        }
        if changed {
            std::fs::write(file, text).context("cannot refresh a precache revision")?;
        }
    }
    Ok(refreshed)
}

/// `path` with `.` and `..` components resolved lexically, if it stays
/// inside `root`.
fn normalize_within(root: &Path, path: &Path) -> Option<PathBuf> {
    use std::path::Component;
    let relative = path.strip_prefix(root).ok()?;
    let mut parts: Vec<&std::ffi::OsStr> = Vec::new();
    for component in relative.components() {
        match component {
            Component::Normal(part) => parts.push(part),
            Component::CurDir => {}
            Component::ParentDir => {
                parts.pop()?;
            }
            Component::RootDir | Component::Prefix(_) => return None,
        }
    }
    Some(
        parts
            .iter()
            .fold(root.to_path_buf(), |path, part| path.join(part)),
    )
}

fn relative_display(root: &Path, path: &Path) -> String {
    path.strip_prefix(root)
        .unwrap_or(path)
        .to_string_lossy()
        .into_owned()
}

/// Rejects bundles that bundle mode cannot publish faithfully, before
/// analysis in check and output modes:
/// - symbolic links: analysis would follow them to their targets while the
///   staged copy would materialize separate files (or external contents);
/// - HTML pages with Subresource Integrity or a Content Security Policy with
///   hash sources: the digests would no longer match rewritten content.
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
        // A Content Security Policy that allows inline code by hash would
        // block inline scripts whose chunk references bundle mode rewrites.
        if is_html(file)
            && std::fs::read_to_string(file).is_ok_and(|html| {
                html_tags(&html).iter().any(|tag| {
                    tag.name == "meta"
                        && tag.attribute("http-equiv").is_some_and(|v| {
                            v.trim().eq_ignore_ascii_case("content-security-policy")
                        })
                        && tag.attribute("content").is_some_and(|policy| {
                            let policy = policy.to_ascii_lowercase();
                            ["'sha256-", "'sha384-", "'sha512-"]
                                .iter()
                                .any(|source| policy.contains(source))
                        })
                })
            })
        {
            anyhow::bail!(
                "HTML pages use a Content Security Policy with hash sources, which bundle mode \
                 cannot update; compute policy hashes after typeopt"
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
    /// Code bytes that `--minify` removed from edited chunks.
    pub minified_bytes: usize,
    _staging: tempfile::TempDir,
    root: PathBuf,
    destination: PathBuf,
    /// Code bytes of original chunks kept next to renamed ones; they are
    /// part of the published output.
    pub retained_bytes: usize,
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
    pinned: &BTreeSet<String>,
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
    // Analyzed texts of edited chunks, for chunks that rehashing retains.
    let mut originals = BTreeMap::new();
    // Original bytes of map files that composition changes or removes.
    let mut original_maps = BTreeMap::new();
    let (mut maps, mut dropped) = (0usize, 0usize);
    let mut minified_bytes = 0usize;
    // A read in any chunk may stringify a callable imported from an edited
    // chunk. Keep the edited text when the bundle has no closed target proof.
    let source_reflection = crate::app::minify()
        && (crate::app::bundle_html_reflection()
            || outcome
                .texts
                .values()
                .any(|text| crate::minify::may_observe_function_source(text)));
    if source_reflection {
        tracing::info!("callable source reflection may be reachable; bundle minification skipped");
    }
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
        let (published, minified) = if crate::app::minify() && !source_reflection {
            minify_chunk(name, text)
        } else {
            (text.clone(), None)
        };
        if minified.is_some() {
            minified_bytes += chunk_code_len(text) - chunk_code_len(&published);
        }
        std::fs::write(&target, &published).context("cannot write an edited chunk")?;
        edited.insert(target.clone());
        originals.insert(target.clone(), original.clone());
        let chain = outcome.chains.get(name).map_or(&[][..], Vec::as_slice);
        match update_chunk_map(
            &root,
            &target,
            original,
            text,
            &published,
            minified.as_ref(),
            chain,
            &mut original_maps,
        )? {
            Some(true) => maps += 1,
            Some(false) => dropped += 1,
            None => {}
        }
    }
    // Pinned chunks (excluded and what they import), as staged paths.
    let frozen: BTreeSet<PathBuf> = pinned
        .iter()
        .filter_map(|path| Path::new(path).strip_prefix(source).ok())
        .map(|relative| root.join(relative))
        .collect();
    let rehashed = if crate::app::keep_chunk_names() {
        Rehashed {
            renamed: 0,
            retained_bytes: 0,
        }
    } else {
        rehash(&root, &edited, &originals, &original_maps, &frozen)?
    };
    let revisions = refresh_precache_revisions(source, &root, &frozen)?;
    tracing::info!(
        edited = edited.len(),
        maps,
        dropped_maps = dropped,
        rehashed = rehashed.renamed,
        precache_revisions = revisions,
        retained_bytes = rehashed.retained_bytes,
        minified_bytes,
        "bundle chunks written"
    );
    Ok(Staged {
        _staging: staging,
        root,
        destination: destination.to_path_buf(),
        retained_bytes: rehashed.retained_bytes,
        minified_bytes,
    })
}

/// The published text of an edited chunk under `--minify`: its code minified
/// with SWC when that compresses smaller, followed by its original source
/// map comment, with the map from the published code to the edited chunk.
/// Otherwise the edited chunk itself.
fn minify_chunk(name: &str, text: &str) -> (String, Option<sourcemap::SourceMap>) {
    let (code, comment) = match map_reference(text) {
        Some(comment) => (
            format!("{}{}", &text[..comment.start], &text[comment.end..]),
            Some(text[comment.start..comment.end].to_owned()),
        ),
        None => (text.to_owned(), None),
    };
    let Some(minified) = crate::minify::minify_module(&code) else {
        tracing::debug!(
            chunk = name,
            "SWC could not minify an edited chunk; kept as is"
        );
        return (text.to_owned(), None);
    };
    let before = crate::minify::compressed(&code);
    let after = minified.compressed;
    if !crate::minify::better(before, after) {
        tracing::debug!(
            chunk = name,
            brotli_before = before.brotli,
            brotli_after = after.brotli,
            gzip_before = before.gzip,
            gzip_after = after.gzip,
            "minified chunk does not compress smaller; kept as is"
        );
        return (text.to_owned(), None);
    }
    tracing::debug!(
        chunk = name,
        bytes_before = code.len(),
        bytes_after = minified.code.len(),
        brotli_before = before.brotli,
        brotli_after = after.brotli,
        "edited chunk minified"
    );
    let published = match comment {
        Some(comment) => format!("{}\n{comment}", minified.code),
        None => minified.code,
    };
    (published, Some(minified.map))
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
        // `*` is a URL character outside the closing `*/` of a block.
        assert_eq!(
            super::map_reference("f();\n//# sourceMappingURL=maps/app*.js.map\n")
                .unwrap()
                .url,
            "maps/app*.js.map"
        );
        assert_eq!(
            super::map_reference("f();/*# sourceMappingURL=a*b.map*/")
                .unwrap()
                .url,
            "a*b.map"
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
        // Escaped slashes still name another host.
        assert!(references(r#""https:\/\/cdn.example\/index-DnKDBunT.js""#, name).is_empty());
        // Another host's file with the same name keeps its URL.
        let external = r#"src="https://cdn.example/index-DnKDBunT.js" u='//cdn.example/x/index-DnKDBunT.js' l="/assets/index-DnKDBunT.js""#;
        assert_eq!(references(external, name).len(), 1);
        // Unquoted HTML attribute values may follow `=` after whitespace.
        assert_eq!(
            references("<script type=module src = index-DnKDBunT.js>", name).len(),
            1
        );
        // A CSS `url(` token may be followed by whitespace; prose and other
        // hosts' files stay unchanged.
        assert_eq!(references("a{b:url( index-DnKDBunT.js )}", name).len(), 1);
        assert_eq!(
            references("a{b:URL(\n\tindex-DnKDBunT.js\n)}", name).len(),
            1
        );
        assert_eq!(references("a{b:url(index-DnKDBunT.js)}", name).len(), 1);
        assert_eq!(references("a{b:url( 'index-DnKDBunT.js' )}", name).len(), 1);
        assert!(references("a{b:url( https://cdn.example/index-DnKDBunT.js )}", name).is_empty());
        assert!(references("foo( index-DnKDBunT.js )", name).is_empty());
        // `url` must start a CSS identifier token, not end a longer identifier.
        for ident in ["myurl", "curl", "--url", "_url", "x.url", "$url", "\\url"] {
            let text = format!("{ident}( index-DnKDBunT.js )");
            assert!(references(&text, name).is_empty(), "{text}");
        }
        assert_eq!(references("a{b:URL( index-DnKDBunT.js )}", name).len(), 1);
        assert_eq!(references("a,url( index-DnKDBunT.js )", name).len(), 1);
        assert_eq!(references("url( index-DnKDBunT.js )", name).len(), 1);
        assert_eq!(
            rewrite(
                "myurl( index-DnKDBunT.js ) url( index-DnKDBunT.js )",
                &BTreeMap::from([(name.to_owned(), "index-NEW12345.js".to_owned())])
            ),
            "myurl( index-DnKDBunT.js ) url( index-NEW12345.js )"
        );
        assert!(references("see ( index-DnKDBunT.js )", name).is_empty());
        let spaced = "a{b:url( index-DnKDBunT.js )}";
        let renamed = BTreeMap::from([(name.to_owned(), "index-NEW12345.js".to_owned())]);
        assert_eq!(rewrite(spaced, &renamed), "a{b:url( index-NEW12345.js )}");
        assert_eq!(rewrite(spaced, &renamed), rewrite(spaced, &renamed));
    }

    #[test]
    fn decodes_backslash_escapes_for_name_detection() {
        assert_eq!(
            super::decode_backslash_escapes_lossy(r#""./settings\u002dBBBBBBBB.js""#),
            r#""./settings-BBBBBBBB.js""#
        );
        assert_eq!(
            super::decode_backslash_escapes_lossy(r"url(lib\-A.js) url(lib\2d A.js) \x2d"),
            "url(lib-A.js) url(lib-A.js) -"
        );
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
        assert_eq!(tags.len(), 3);
        assert_eq!(tags[0].name, "script");
        assert_eq!(tags[1].name, "/script");
        assert_eq!(tags[0].attribute("type"), Some("module"));
        assert_eq!(tags[0].attribute("src"), Some("/x/a.js"));
        assert_eq!(tags[0].attribute("integrity"), Some("sha384-x"));
        assert_eq!(tags[0].attribute("defer"), Some(""));
        assert_eq!(tags[2].attribute("href"), Some("b.js"));
        // Markup inside script text is not a tag.
        let tags = html_tags(
            r#"<script>const s='<script type="module" src="./legacy.js" integrity=x>';</SCRIPT><p>"#,
        );
        let names: Vec<_> = tags.iter().map(|t| t.name.as_str()).collect();
        assert_eq!(names, ["script", "/script", "p"]);
        assert_eq!(tags[0].attribute("src"), None);
        // Only an exact end tag name ends the raw text.
        let tags = html_tags(
            r#"<script>s='</scripture><script type="module" src="./legacy.js">';</script ><p>"#,
        );
        let names: Vec<_> = tags.iter().map(|t| t.name.as_str()).collect();
        assert_eq!(names, ["script", "/script", "p"]);
        // Character references in attribute values are decoded.
        let tags = html_tags(
            "<script src=\"shared&#46;js\" type=\"&#x6d;odule\" data-x=\"a&amp;b&c\"></script><a \
             href=\"&unknownref;\">",
        );
        assert_eq!(tags[0].attribute("src"), Some("shared.js"));
        assert_eq!(tags[0].attribute("type"), Some("module"));
        assert_eq!(tags[0].attribute("data-x"), Some("a&b&c"));
        assert_eq!(tags[2].undecodable, ["href"]);
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
        // Numeric references follow the HTML replacement table.
        assert_eq!(
            super::decode_references("s&#x80;.js").as_deref(),
            Some("s€.js")
        );
        assert_eq!(
            super::decode_references("s&#0;&#xD800;").as_deref(),
            Some("s\u{FFFD}\u{FFFD}")
        );
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
