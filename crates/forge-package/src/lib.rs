//! Bounded OOXML package primitives shared by the Forge format engines.
#![forbid(unsafe_code)]

mod preservation;
use std::{
    collections::BTreeMap,
    io::{Cursor, Read, Write},
};

use forge_tree_doc::{Diagnostic, ErrorCode, Result, error};
pub use preservation::*;
use sha2::{Digest, Sha256};
use zip::{ZipArchive, ZipWriter, write::SimpleFileOptions};

pub const P: &str = "http://schemas.openxmlformats.org/presentationml/2006/main";
pub const A: &str = "http://schemas.openxmlformats.org/drawingml/2006/main";
pub const R: &str = "http://schemas.openxmlformats.org/officeDocument/2006/relationships";
pub const C: &str = "http://schemas.openxmlformats.org/drawingml/2006/chart";
pub const REL: &str = "http://schemas.openxmlformats.org/package/2006/relationships";
pub const META: &str = "customXml/forge.xml";
pub const MAX_PACKAGE_BYTES: usize = 256 * 1024 * 1024;
pub const MAX_EXPANDED_BYTES: usize = 512 * 1024 * 1024;
pub const MAX_PART_BYTES: usize = 64 * 1024 * 1024;
pub const MAX_ENTRIES: usize = 10_000;
pub type Package = BTreeMap<String, Vec<u8>>;
pub fn failure(_: impl std::fmt::Display) -> Diagnostic {
    Diagnostic::new(
        ErrorCode::InvalidPackage,
        "",
        "Invalid or unsupported Office package",
    )
}
pub fn sha(bytes: &[u8]) -> String {
    format!("{:x}", Sha256::digest(bytes))
}
pub fn xml(bytes: &[u8]) -> Result<roxmltree::Document<'_>> {
    let text = std::str::from_utf8(bytes).map_err(failure)?;
    if bytes.len() > MAX_PART_BYTES {
        return error(ErrorCode::ResourceLimit, "", "XML part exceeds 64 MiB");
    }
    if text.contains("<!DOCTYPE") || text.contains("<!ENTITY") {
        return error(
            ErrorCode::InvalidPackage,
            "",
            "XML DTDs and entities are forbidden",
        );
    }
    // roxmltree constructs its tree recursively. Enforce the depth budget with
    // an iterative tokenizer first, before hostile nesting can exhaust a native
    // worker's stack. Keep this preflight while the tree parser is recursive.
    let mut reader = quick_xml::Reader::from_reader(bytes);
    let mut depth = 0_usize;
    let mut nodes = 1_usize;
    loop {
        use quick_xml::events::Event;
        match reader.read_event().map_err(failure)? {
            Event::Start(_) => {
                depth += 1;
                nodes += 1;
            }
            Event::Empty(_) => {
                nodes += 1;
                if depth >= 127 {
                    return error(ErrorCode::ResourceLimit, "", "XML depth exceeded");
                }
            }
            Event::End(_) => {
                depth = depth.saturating_sub(1);
            }
            Event::Eof => break,
            Event::Text(_) | Event::CData(_) | Event::Comment(_) | Event::PI(_) => {
                nodes += 1;
            }
            _ => {}
        }
        if depth >= 128 || nodes > 1_000_000 {
            return error(
                ErrorCode::ResourceLimit,
                "",
                "XML depth or node limit exceeded",
            );
        }
    }
    let doc = roxmltree::Document::parse_with_options(
        text,
        roxmltree::ParsingOptions {
            allow_dtd: false,
            nodes_limit: 1_000_000,
            ..Default::default()
        },
    )
    .map_err(failure)?;
    if doc
        .descendants()
        .any(|n| n.ancestors().take(129).count() > 128)
    {
        return error(ErrorCode::ResourceLimit, "", "XML depth exceeded");
    }
    Ok(doc)
}
pub fn read(bytes: &[u8]) -> Result<Package> {
    if bytes.len() > MAX_PACKAGE_BYTES {
        return error(ErrorCode::ResourceLimit, "", "Package exceeds 256 MiB");
    }
    if bytes.starts_with(&[0xd0, 0xcf, 0x11, 0xe0]) {
        return error(
            ErrorCode::UnsupportedPackage,
            "",
            "Encrypted and legacy Office packages are unsupported",
        );
    }
    let mut zip = ZipArchive::new(Cursor::new(bytes)).map_err(failure)?;
    if zip.len() > MAX_ENTRIES {
        return error(ErrorCode::ResourceLimit, "", "ZIP entry count exceeded");
    }
    // Inspect declared expansion before allocating payloads. The streaming
    // checks below independently enforce the same budget on actual bytes.
    let mut declared = 0_u64;
    for index in 0..zip.len() {
        let file = zip.by_index(index).map_err(failure)?;
        if !file.is_dir() {
            declared = declared.checked_add(file.size()).ok_or_else(|| {
                Diagnostic::new(ErrorCode::ResourceLimit, "", "ZIP expansion limit exceeded")
            })?;
            if file.size() > MAX_PART_BYTES as u64 || declared > MAX_EXPANDED_BYTES as u64 {
                return error(
                    ErrorCode::ResourceLimit,
                    "",
                    "ZIP declared expansion exceeds its limit",
                );
            }
        }
    }
    let mut parts = Package::new();
    let mut total = 0_usize;
    let mut names = std::collections::HashSet::new();
    for i in 0..zip.len() {
        let mut file = zip.by_index(i).map_err(failure)?;
        let name = file.name().to_string();
        if file.is_dir() {
            continue;
        }
        if name.starts_with('/')
            || name.contains('\\')
            || name
                .split('/')
                .any(|p| p == ".." || p == "." || p.is_empty())
            || name.contains('\0')
            || name.contains('%')
            || name.contains(':')
            || name.contains('?')
            || name.contains('#')
            || !names.insert(name.to_ascii_lowercase())
        {
            return error(
                ErrorCode::InvalidPackage,
                "",
                "Invalid or duplicate ZIP part path",
            );
        }
        if name.to_ascii_lowercase().starts_with("_xmlsignatures/")
            || name.to_ascii_lowercase().ends_with("vbaproject.bin")
        {
            return error(
                ErrorCode::UnsupportedPackage,
                "",
                "Signed and macro-enabled packages cannot be edited",
            );
        }
        if file.size() > 64 * 1024 * 1024 {
            return error(ErrorCode::ResourceLimit, "", "ZIP part exceeds 64 MiB");
        }
        let mut data = Vec::new();
        file.by_ref()
            .take(64 * 1024 * 1024 + 1)
            .read_to_end(&mut data)
            .map_err(failure)?;
        total += data.len();
        if data.len() > 64 * 1024 * 1024 || total > 512 * 1024 * 1024 {
            return error(ErrorCode::ResourceLimit, "", "ZIP expansion limit exceeded");
        }
        if name.to_ascii_lowercase().ends_with(".xml")
            || name.to_ascii_lowercase().ends_with(".rels")
        {
            xml(&data)?;
        }
        parts.insert(name, data);
    }
    if !parts.contains_key("[Content_Types].xml") || !parts.contains_key("_rels/.rels") {
        return error(
            ErrorCode::InvalidPackage,
            "",
            "Missing package declarations",
        );
    }
    Ok(parts)
}
pub fn write(parts: &Package) -> Result<Vec<u8>> {
    if parts.len() > MAX_ENTRIES
        || parts.values().any(|b| b.len() > MAX_PART_BYTES)
        || parts.values().map(Vec::len).sum::<usize>() > MAX_EXPANDED_BYTES
    {
        return error(
            ErrorCode::ResourceLimit,
            "",
            "Output package limits exceeded",
        );
    }
    let mut out = ZipWriter::new(Cursor::new(Vec::new()));
    let options = SimpleFileOptions::default()
        .compression_method(zip::CompressionMethod::Deflated)
        .last_modified_time(zip::DateTime::default());
    for (name, bytes) in parts {
        out.start_file(name, options).map_err(failure)?;
        out.write_all(bytes).map_err(failure)?;
    }
    let bytes = out.finish().map_err(failure)?.into_inner();
    if bytes.len() > MAX_PACKAGE_BYTES {
        return error(
            ErrorCode::ResourceLimit,
            "",
            "Output package exceeds 256 MiB",
        );
    }
    Ok(bytes)
}
pub fn escape(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}
pub fn relation_path(part: &str) -> String {
    let (dir, name) = part.rsplit_once('/').unwrap_or(("", part));
    if dir.is_empty() {
        format!("_rels/{name}.rels")
    } else {
        format!("{dir}/_rels/{name}.rels")
    }
}
pub fn resolve(part: &str, target: &str) -> Result<String> {
    if target.contains('\\')
        || target.contains(':')
        || target.contains('#')
        || target.contains('?')
        || target.contains('%')
    {
        return error(
            ErrorCode::InvalidPackage,
            "",
            "Unsafe internal relationship target",
        );
    }
    let mut bits: Vec<&str> = if target.starts_with('/') {
        Vec::new()
    } else {
        part.rsplit_once('/')
            .map(|(p, _)| p.split('/').collect())
            .unwrap_or_default()
    };
    for b in target.trim_start_matches('/').split('/') {
        match b {
            "" | "." => {}
            ".." => {
                if bits.pop().is_none() {
                    return error(
                        ErrorCode::InvalidPackage,
                        "",
                        "Relationship escapes package",
                    );
                }
            }
            _ => bits.push(b),
        }
    }
    Ok(bits.join("/"))
}
#[derive(Debug, Clone)]
pub struct Relationship {
    pub id: String,
    pub kind: String,
    pub target: String,
    pub external: bool,
}
pub fn relationships(parts: &Package, part: &str) -> Result<Vec<Relationship>> {
    let path = if part.is_empty() {
        "_rels/.rels".into()
    } else {
        relation_path(part)
    };
    let Some(bytes) = parts.get(&path) else {
        return Ok(Vec::new());
    };
    let doc = xml(bytes)?;
    doc.root_element()
        .children()
        .filter(|n| n.is_element())
        .map(|n| {
            Ok(Relationship {
                id: n.attribute("Id").ok_or_else(|| failure("id"))?.into(),
                kind: n.attribute("Type").ok_or_else(|| failure("type"))?.into(),
                target: n
                    .attribute("Target")
                    .ok_or_else(|| failure("target"))?
                    .into(),
                external: n.attribute("TargetMode") == Some("External"),
            })
        })
        .collect()
}
pub fn related(parts: &Package, part: &str, id: &str) -> Result<String> {
    let r = relationships(parts, part)?
        .into_iter()
        .find(|r| r.id == id)
        .ok_or_else(|| failure("relationship"))?;
    if r.external {
        return error(
            ErrorCode::UnsupportedEdit,
            "",
            "External content is preserved but cannot be read or edited",
        );
    }
    resolve(part, &r.target)
}
pub fn main_part(parts: &Package) -> Result<String> {
    let roots: Vec<_> = relationships(parts, "")?
        .into_iter()
        .filter(|r| r.kind.ends_with("/officeDocument"))
        .collect();
    if roots.len() != 1 || roots[0].external || roots[0].kind != format!("{R}/officeDocument") {
        return Err(failure("ambiguous or unsupported main relationship"));
    }
    let r = &roots[0];
    resolve("", &r.target)
}
pub fn insert_before_close(bytes: &[u8], child: &str) -> Result<Vec<u8>> {
    let doc = xml(bytes)?;
    let range = doc.root_element().range();
    let text = std::str::from_utf8(bytes).map_err(failure)?;
    let root = &text[range.clone()];
    let next = if root.trim_end().ends_with("/>") {
        let at = text[..range.end]
            .rfind("/>")
            .ok_or_else(|| failure("root"))?;
        let name_start = range.start + 1;
        let name_end = text[name_start..]
            .find(|c: char| c.is_whitespace() || c == '/' || c == '>')
            .unwrap()
            + name_start;
        format!(
            "{}>{}</{}>{}",
            &text[..at],
            child,
            &text[name_start..name_end],
            &text[range.end..]
        )
    } else {
        let at = text[..range.end]
            .rfind("</")
            .ok_or_else(|| failure("root"))?;
        format!("{}{}{}", &text[..at], child, &text[at..])
    };
    Ok(next.into_bytes())
}
pub fn add_relationship(
    parts: &mut Package,
    part: &str,
    id: &str,
    kind: &str,
    target: &str,
) -> Result<()> {
    let path = if part.is_empty() {
        "_rels/.rels".into()
    } else {
        relation_path(part)
    };
    if relationships(parts, part)?.iter().any(|r| r.id == id) {
        return error(
            ErrorCode::InvalidPackage,
            "",
            "Duplicate relationship identifier",
        );
    }
    let base = parts
        .get(&path)
        .cloned()
        .unwrap_or_else(|| format!("<Relationships xmlns=\"{REL}\"></Relationships>").into_bytes());
    let child = format!(
        "<Relationship xmlns=\"{REL}\" Id=\"{}\" Type=\"{}\" Target=\"{}\"/>",
        escape(id),
        escape(kind),
        escape(target)
    );
    parts.insert(path, insert_before_close(&base, &child)?);
    Ok(())
}
pub fn ensure_relationship(
    parts: &mut Package,
    part: &str,
    id: &str,
    kind: &str,
    target: &str,
) -> Result<()> {
    if let Some(existing) = relationships(parts, part)?.into_iter().find(|r| r.id == id) {
        if existing.external
            || existing.kind != kind
            || resolve(part, &existing.target)? != resolve(part, target)?
        {
            return error(
                ErrorCode::UnsupportedEdit,
                "",
                "A reserved relationship identifier belongs to different content",
            );
        }
        Ok(())
    } else {
        add_relationship(parts, part, id, kind, target)
    }
}
pub fn add_content_type(parts: &mut Package, path: &str, kind: &str) -> Result<()> {
    let bytes = parts
        .get("[Content_Types].xml")
        .ok_or_else(|| failure("types"))?;
    let doc = xml(bytes)?;
    let name = format!("/{path}");
    if doc
        .descendants()
        .any(|n| n.attribute("PartName") == Some(name.as_str()))
    {
        return Ok(());
    }
    let child = format!(
        "<Override xmlns=\"http://schemas.openxmlformats.org/package/2006/content-types\" \
         PartName=\"{}\" ContentType=\"{}\"/>",
        escape(&name),
        escape(kind)
    );
    let out = insert_before_close(bytes, &child)?;
    parts.insert("[Content_Types].xml".into(), out);
    Ok(())
}
pub fn replace_range(bytes: &[u8], range: std::ops::Range<usize>, new: &str) -> Vec<u8> {
    let mut out = bytes[..range.start].to_vec();
    out.extend_from_slice(new.as_bytes());
    out.extend_from_slice(&bytes[range.end..]);
    out
}

/// Insert into a previously parsed element without reparsing a detached
/// fragment. Nested Office elements can use namespaces declared only on their
/// ancestors.
pub fn insert_element_child(
    bytes: &[u8],
    range: std::ops::Range<usize>,
    child: &str,
) -> Result<Vec<u8>> {
    let text = std::str::from_utf8(bytes).map_err(failure)?;
    let element = text
        .get(range.clone())
        .ok_or_else(|| failure("element range"))?;
    if element.ends_with("/>") {
        let name = element
            .strip_prefix('<')
            .ok_or_else(|| failure("element"))?
            .split(|c: char| c.is_whitespace() || c == '/' || c == '>')
            .next()
            .ok_or_else(|| failure("element name"))?;
        Ok(replace_range(
            bytes,
            range.end - 2..range.end,
            &format!(">{child}</{name}>"),
        ))
    } else {
        let at = range.start
            + element
                .rfind("</")
                .ok_or_else(|| failure("element close"))?;
        Ok(replace_range(bytes, at..at, child))
    }
}
pub fn validate_package(parts: &Package) -> Result<()> {
    main_part(parts)?;
    for name in parts.keys().filter(|p| p.ends_with(".rels")) {
        let owner = if name == "_rels/.rels" {
            String::new()
        } else {
            let (dir, file) = name.rsplit_once("/_rels/").ok_or_else(|| failure("rels"))?;
            format!(
                "{dir}/{}",
                file.strip_suffix(".rels").ok_or_else(|| failure("rels"))?
            )
        };
        let mut seen = std::collections::HashSet::new();
        for r in relationships(parts, &owner)? {
            if r.kind.contains("/digital-signature/") || r.kind.ends_with("/vbaProject") {
                return error(
                    ErrorCode::UnsupportedPackage,
                    "",
                    "Signed and macro-enabled packages are unsupported",
                );
            }
            if !seen.insert(r.id) {
                return error(ErrorCode::InvalidPackage, "", "Duplicate relationship ID");
            }
            if !r.external && !parts.contains_key(&resolve(&owner, &r.target)?) {
                return error(
                    ErrorCode::InvalidPackage,
                    "",
                    "Dangling package relationship",
                );
            }
        }
    }
    Ok(())
}
