use std::collections::HashSet;

use forge_document::Assets;
use forge_package::*;
use forge_tree_doc::{ErrorCode, Result, error};
use uuid::Uuid;

use crate::{Block, emit::Writer, validate_blocks};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TargetKind {
    Paragraph,
    Table,
    Cell,
    Image,
    Chart,
    Opaque,
}

#[derive(Debug, Clone)]
pub struct Target {
    pub id: Uuid,
    pub kind: TargetKind,
    pub region: Region,
    pub text: String,
}

#[derive(Debug, Clone)]
pub struct Imported {
    pub parts: Package,
    pub targets: Vec<Target>,
    pub main: String,
}

fn supported_attributes(node: roxmltree::Node<'_, '_>) -> bool {
    node.attributes().all(|attribute| {
        matches!(
            attribute.namespace(),
            None | Some(W) | Some(R) | Some("http://www.w3.org/XML/1998/namespace")
        )
    })
}

fn supported_word_element(n: roxmltree::Node<'_, '_>) -> bool {
    // Only ordinary line breaks fit the editable paragraph model. Page/column
    // breaks and text-wrapping clearance must survive as opaque source XML.
    if n.has_tag_name((W, "br")) {
        return n.attributes().all(|attribute| {
            attribute.namespace() == Some(W)
                && matches!(
                    (attribute.name(), attribute.value()),
                    ("type", "textWrapping") | ("clear", "none")
                )
        });
    }
    n.tag_name().namespace() == Some(W)
        && matches!(
            n.tag_name().name(),
            "p" | "pPr"
                | "pStyle"
                | "r"
                | "rPr"
                | "t"
                | "tab"
                | "br"
                | "b"
                | "bCs"
                | "i"
                | "iCs"
                | "u"
                | "color"
                | "sz"
                | "szCs"
                | "rFonts"
                | "lang"
                | "rtl"
                | "bidi"
                | "jc"
                | "spacing"
                | "ind"
                | "keepNext"
                | "keepLines"
                | "pageBreakBefore"
                | "widowControl"
                | "outlineLvl"
                | "numPr"
                | "numId"
                | "ilvl"
                | "hyperlink"
                | "shd"
                | "highlight"
                | "noProof"
                | "caps"
                | "smallCaps"
                | "strike"
                | "vertAlign"
                | "tbl"
                | "tblPr"
                | "tblStyle"
                | "tblW"
                | "tblLayout"
                | "tblLook"
                | "tblGrid"
                | "gridCol"
                | "tblBorders"
                | "top"
                | "left"
                | "bottom"
                | "right"
                | "insideH"
                | "insideV"
                | "tblCellMar"
                | "tr"
                | "trPr"
                | "tblHeader"
                | "trHeight"
                | "cantSplit"
                | "tc"
                | "tcPr"
                | "tcW"
                | "gridSpan"
                | "vMerge"
                | "vAlign"
                | "tcBorders"
                | "tcMar"
        )
}

fn supported_word_node(node: roxmltree::Node<'_, '_>) -> bool {
    node.descendants().filter(|n| n.is_element()).all(|n| {
        supported_word_element(n)
            // Cell replacement retains its original opening/closing wrapper.
            && ((n == node && node.has_tag_name((W, "tc"))) || supported_attributes(n))
    })
}

fn drawing_kind(
    parts: &Package,
    owner: &str,
    node: roxmltree::Node<'_, '_>,
) -> Result<Option<TargetKind>> {
    let drawings: Vec<_> = node
        .descendants()
        .filter(|n| n.has_tag_name((W, "drawing")))
        .collect();
    if drawings.len() != 1 || node.descendants().any(|n| n.has_tag_name((W, "t"))) {
        return Ok(None);
    }
    // Bookmarks, field/revision markers and foreign paragraph/run attributes
    // outside the drawing are not owned by a simple image/chart replacement.
    if node.descendants().filter(|n| n.is_element()).any(|n| {
        if n.ancestors()
            .any(|ancestor| ancestor.has_tag_name((W, "drawing")))
        {
            return !supported_attributes(n)
                || !matches!(
                n.tag_name().namespace(),
                Some(W)
                    | Some(A)
                    | Some(C)
                    | Some(
                        "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
                    )
                    | Some("http://schemas.openxmlformats.org/drawingml/2006/picture")
            );
        }
        !supported_word_element(n) || !supported_attributes(n)
    }) {
        return Ok(None);
    }
    // Arbitrary DrawingML groups/effects/anchors cannot be replaced as a simple
    // inline block without dropping their semantics.
    if node.descendants().any(|n| {
        matches!(
            n.tag_name().name(),
            "anchor" | "extLst" | "AlternateContent" | "effectLst" | "effectDag"
        )
    }) {
        return Ok(None);
    }
    if let Some(chart) = node.descendants().find(|n| n.has_tag_name((C, "chart"))) {
        let Some(id) = chart.attribute((R, "id")) else {
            return Ok(None);
        };
        let path = related(parts, owner, id)?;
        let doc = xml(parts.get(&path).ok_or_else(|| failure("chart"))?)?;
        if doc.descendants().any(|n| {
            matches!(n.tag_name().name(), "extLst" | "externalData")
                && n.tag_name().namespace() != Some(C)
        }) {
            return Ok(None);
        }
        if doc.descendants().filter(|n| n.is_element()).any(|n| {
            !matches!(n.tag_name().namespace(), Some(C) | Some(A))
                || n.attributes().any(|attribute| {
                    !matches!(
                        attribute.namespace(),
                        None | Some(R) | Some("http://www.w3.org/XML/1998/namespace")
                    )
                })
        }) {
            return Ok(None);
        }
        if doc.descendants().any(|n| n.tag_name().name() == "extLst") {
            return Ok(None);
        }
        let plots: Vec<_> = doc
            .descendants()
            .filter(|n| n.has_tag_name((C, "plotArea")))
            .collect();
        if plots.len() != 1 {
            return Ok(None);
        }
        let charts: Vec<_> = plots[0]
            .children()
            .filter(|n| n.is_element() && n.tag_name().name().ends_with("Chart"))
            .collect();
        if charts.len() != 1
            || !matches!(
                charts[0].tag_name().name(),
                "barChart" | "lineChart" | "pieChart"
            )
        {
            return Ok(None);
        }
        return Ok(Some(TargetKind::Chart));
    }
    if node
        .descendants()
        .filter(|n| n.has_tag_name((A, "blip")))
        .count()
        == 1
    {
        return Ok(Some(TargetKind::Image));
    }
    Ok(None)
}

pub fn import(bytes: &[u8]) -> Result<Imported> {
    let parts = read(bytes)?;
    let main = validate_office(&parts, OfficeKind::Document)?;
    let main_doc = xml(&parts[&main])?;
    if main_doc
        .root_element()
        .children()
        .filter(|n| n.has_tag_name((W, "body")))
        .count()
        != 1
    {
        return error(
            ErrorCode::InvalidPackage,
            "document/body",
            "Word document requires exactly one body",
        );
    }
    let mut owners = vec![main.clone()];
    for relation in relationships(&parts, &main)? {
        if !relation.external
            && (relation.kind == format!("{R}/header") || relation.kind == format!("{R}/footer"))
        {
            let path = resolve(&main, &relation.target)?;
            if !owners.contains(&path) {
                owners.push(path);
            }
        }
    }
    let mut targets = Vec::new();
    for owner in owners {
        forge_tree_doc::cancellation::checkpoint()?;
        let doc = xml(&parts[&owner])?;
        for node in doc.descendants().filter(|n| {
            n.is_element()
                && n.tag_name().namespace() == Some(W)
                && matches!(n.tag_name().name(), "p" | "tbl" | "tc")
        }) {
            let kind = if supported_word_node(node) {
                match node.tag_name().name() {
                    "p" => TargetKind::Paragraph,
                    "tbl" => TargetKind::Table,
                    _ => TargetKind::Cell,
                }
            } else if node.has_tag_name((W, "p")) {
                drawing_kind(&parts, &owner, node)?.unwrap_or(TargetKind::Opaque)
            } else {
                TargetKind::Opaque
            };
            targets.push(Target {
                id: Uuid::now_v7(),
                kind,
                region: Region::new(&owner, node.range(), &parts)?,
                text: node
                    .descendants()
                    .filter(|n| n.has_tag_name((W, "t")))
                    .filter_map(|n| n.text())
                    .collect(),
            });
        }
    }
    Ok(Imported {
        parts,
        targets,
        main,
    })
}

/// Replace a supported paragraph/table/drawing or the contents of a table cell.
/// Unselected parts and XML ranges retain their original bytes. New
/// relationship and media identities are allocated without claiming any
/// original names.
pub fn replace(
    imported: &Imported,
    edits: &[(Uuid, Vec<Block>)],
    assets: &Assets,
) -> Result<Vec<u8>> {
    let mut ids = HashSet::new();
    let mut count = 0;
    let mut selected = Vec::new();
    for (id, blocks) in edits {
        validate_blocks(blocks, 1, &mut count, &mut ids)?;
        let target = imported
            .targets
            .iter()
            .find(|t| t.id == *id)
            .ok_or_else(|| {
                forge_tree_doc::Diagnostic::new(
                    ErrorCode::NotFound,
                    "target",
                    "Target is not part of this document",
                )
            })?;
        if target.kind == TargetKind::Opaque {
            return error(
                ErrorCode::UnsupportedEdit,
                "target",
                "Region contains unsupported content",
            );
        }
        if selected
            .iter()
            .any(|other: &&Target| other.region.overlaps(&target.region))
        {
            return error(
                ErrorCode::RevisionConflict,
                "target",
                "Mounted regions overlap",
            );
        }
        selected.push(target);
    }
    let mut writer = Writer::new(imported.parts.clone(), assets, &imported.main)?;
    let mut fragments = Vec::new();
    for (target, (_, blocks)) in selected.iter().zip(edits) {
        forge_tree_doc::cancellation::checkpoint()?;
        let mut fragment = writer.blocks(blocks, &target.region.part)?;
        if target.kind == TargetKind::Cell {
            let bytes = &imported.parts[&target.region.part];
            let doc = xml(bytes)?;
            let cell = doc
                .descendants()
                .find(|n| n.range() == target.region.range)
                .ok_or_else(|| failure("cell"))?;
            let prefix = std::str::from_utf8(bytes).map_err(failure)?;
            let properties = cell
                .children()
                .find(|n| n.has_tag_name((W, "tcPr")))
                .map(|n| &prefix[n.range()])
                .unwrap_or("");
            if !matches!(
                blocks.last(),
                Some(
                    Block::Paragraph { .. }
                        | Block::PageBreak { .. }
                        | Block::Image { .. }
                        | Block::Chart { .. }
                )
            ) {
                fragment.push_str("<w:p/>");
            }
            // Preserve the original cell opening/closing tags, attributes and
            // namespace bindings along with tcPr. Only the selected cell's block
            // content is owned by the mounted subtree.
            let original = &prefix[cell.range()];
            let opening = original.find('>').ok_or_else(|| failure("cell"))? + 1;
            let closing = original.rfind("</").ok_or_else(|| failure("cell"))?;
            fragment = format!(
                "{}{properties}{fragment}{}",
                &original[..opening],
                &original[closing..]
            );
        }
        fragments.push(fragment);
    }
    // Relationships/media may have changed, but selected XML parts have not.
    let replacements: Vec<_> = selected
        .iter()
        .zip(&fragments)
        .map(|(target, fragment)| Replacement {
            region: &target.region,
            xml: fragment,
        })
        .collect();
    let candidate = replace_regions(&writer.parts, &replacements)?;
    validate_office(&candidate, OfficeKind::Document)?;
    write(&candidate)
}
