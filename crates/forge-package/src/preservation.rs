//! Byte-range edits against immutable package snapshots. Engines prove that a
//! region is supported before constructing a replacement; this layer proves
//! snapshot identity, non-overlap and package integrity before returning it.
use std::{collections::BTreeMap, ops::Range};

use forge_tree_doc::{ErrorCode, Result, error};

use crate::{Package, R, main_part, sha, validate_package, xml};

pub const W: &str = "http://schemas.openxmlformats.org/wordprocessingml/2006/main";
pub const S: &str = "http://schemas.openxmlformats.org/spreadsheetml/2006/main";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OfficeKind {
    Presentation,
    Document,
    Workbook,
}

/// Require the exact Transitional root and reject protected or Strict types.
pub fn validate_office(parts: &Package, kind: OfficeKind) -> Result<String> {
    validate_package(parts)?;
    let part = main_part(parts)?;
    let bytes = parts.get(&part).ok_or_else(|| crate::failure("root"))?;
    let doc = xml(bytes)?;
    let (namespace, local) = match kind {
        OfficeKind::Presentation => (crate::P, "presentation"),
        OfficeKind::Document => (W, "document"),
        OfficeKind::Workbook => (S, "workbook"),
    };
    if !doc.root_element().has_tag_name((namespace, local)) {
        return error(
            ErrorCode::UnsupportedPackage,
            "",
            "Unsupported Office format or Strict OOXML root",
        );
    }
    let types = xml(parts
        .get("[Content_Types].xml")
        .ok_or_else(|| crate::failure("types"))?)?;
    for node in types.descendants() {
        if let Some(content_type) = node.attribute("ContentType") {
            let lower = content_type.to_ascii_lowercase();
            if lower.contains("macroenabled")
                || lower.contains("vbaproject")
                || lower.contains("digital-signature")
            {
                return error(
                    ErrorCode::UnsupportedPackage,
                    "",
                    "Protected and macro-enabled packages are unsupported",
                );
            }
        }
    }
    // Referenced XML identities must be unambiguous even when the relationship
    // graph itself is valid. Engines validate their own non-relationship IDs.
    for (path, bytes) in parts.iter().filter(|(p, _)| p.ends_with(".xml")) {
        let doc = xml(bytes)?;
        let rels = crate::relationships(parts, path)?;
        for node in doc.descendants().filter(|n| n.is_element()) {
            for attr in node.attributes().filter(|a| a.namespace() == Some(R)) {
                if matches!(attr.name(), "id" | "embed" | "link")
                    && !rels.iter().any(|r| r.id == attr.value())
                {
                    return error(
                        ErrorCode::InvalidReference,
                        "",
                        "XML references an unresolved relationship",
                    );
                }
            }
        }
    }
    Ok(part)
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Region {
    pub part: String,
    pub range: Range<usize>,
    pub fingerprint: String,
}

impl Region {
    pub fn new(part: &str, range: Range<usize>, parts: &Package) -> Result<Self> {
        let bytes = parts
            .get(part)
            .ok_or_else(|| crate::failure("region part"))?;
        if range.start > range.end || range.end > bytes.len() {
            return error(ErrorCode::InvalidReference, "", "Invalid editable region");
        }
        Ok(Self {
            part: part.into(),
            range,
            fingerprint: sha(bytes),
        })
    }

    pub fn overlaps(&self, other: &Self) -> bool {
        self.part == other.part
            && self.range.start < other.range.end
            && other.range.start < self.range.end
    }
}

pub struct Replacement<'a> {
    pub region: &'a Region,
    pub xml: &'a str,
}

/// All replacements use coordinates in the same immutable input snapshot.
/// Replacements are applied backwards so changing lengths cannot shift peers.
pub fn replace_regions(parts: &Package, replacements: &[Replacement<'_>]) -> Result<Package> {
    let mut groups: BTreeMap<&str, Vec<&Replacement<'_>>> = BTreeMap::new();
    for replacement in replacements {
        let r = replacement.region;
        let bytes = parts
            .get(&r.part)
            .ok_or_else(|| crate::failure("region part"))?;
        if sha(bytes) != r.fingerprint || r.range.start >= r.range.end || r.range.end > bytes.len()
        {
            return error(ErrorCode::RevisionConflict, "", "Editable region is stale");
        }
        groups.entry(&r.part).or_default().push(replacement);
    }
    let mut candidate = parts.clone();
    for (part, mut edits) in groups {
        edits.sort_by_key(|e| e.region.range.start);
        if edits.windows(2).any(|w| w[0].region.overlaps(w[1].region)) {
            return error(ErrorCode::RevisionConflict, "", "Mounted regions overlap");
        }
        let bytes = candidate
            .get_mut(part)
            .ok_or_else(|| crate::failure("region part"))?;
        for edit in edits.iter().rev() {
            let next_len = bytes.len() - edit.region.range.len() + edit.xml.len();
            if next_len > crate::MAX_PART_BYTES {
                return error(
                    ErrorCode::ResourceLimit,
                    "",
                    "Edited XML part exceeds 64 MiB",
                );
            }
            bytes.splice(edit.region.range.clone(), edit.xml.bytes());
        }
        xml(bytes)?;
    }
    validate_package(&candidate)?;
    Ok(candidate)
}
