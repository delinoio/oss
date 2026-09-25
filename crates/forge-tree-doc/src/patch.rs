use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::*;

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Patch {
    pub dsl_version: u32,
    pub kind: PatchKind,
    pub document_id: Uuid,
    pub base_revision: u64,
    pub operations: Vec<Operation>,
}
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(tag = "op", rename_all = "snake_case", deny_unknown_fields)]
pub enum Operation {
    SetText {
        target: Target,
        text: String,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        cell: Option<CellAddress>,
    },
    SetTextStyle {
        target: Target,
        style: TextStyle,
    },
    UnsetTextStyle {
        target: Target,
        properties: Vec<StyleProperty>,
    },
    SetFrame {
        target: Target,
        frame: Frame,
    },
    InsertNode {
        parent: Target,
        index: usize,
        node: Box<Node>,
    },
    RemoveNode {
        target: Target,
    },
    MoveNode {
        target: Target,
        parent: Target,
        index: usize,
    },
    SetChartData {
        target: Target,
        data: ChartData,
    },
    SetImageAsset {
        target: Target,
        asset_ref: String,
    },
}
fn target_mut<'a>(doc: &'a mut Presentation, target: &Target) -> Result<&'a mut Node> {
    validate_target(target)?;
    let n = doc
        .find_mut(target)
        .ok_or_else(|| Diagnostic::new(ErrorCode::NotFound, "/target", "Node not found"))?;
    if n.kind == NodeKind::Opaque {
        return error(
            ErrorCode::UnsupportedEdit,
            "/target",
            "Opaque nodes cannot be edited",
        );
    }
    Ok(n)
}
fn remove(n: &mut Node, t: &Target) -> Option<Node> {
    if let Some(i) = n.children.iter().position(|n| t.matches(n)) {
        Some(n.children.remove(i))
    } else {
        n.children.iter_mut().find_map(|n| remove(n, t))
    }
}
fn detach(doc: &mut Presentation, t: &Target) -> Result<Node> {
    validate_target(t)?;
    target_mut(doc, t)?;
    doc.slides
        .iter_mut()
        .find_map(|s| remove(&mut s.content, t))
        .ok_or_else(|| {
            Diagnostic::new(
                ErrorCode::UnsupportedEdit,
                "/target",
                "Cannot remove the slide root",
            )
        })
}
fn insert(doc: &mut Presentation, p: &Target, i: usize, n: Node) -> Result<()> {
    let parent = target_mut(doc, p)?;
    if !parent.is_container() || i > parent.children.len() {
        return error(
            ErrorCode::InvalidField,
            "/parent",
            "Invalid insertion parent or index",
        );
    }
    parent.children.insert(i, n);
    Ok(())
}
pub fn apply_patch(
    doc: &Presentation,
    patch: &Patch,
    document_id: Uuid,
    revision: u64,
) -> Result<Presentation> {
    if patch.dsl_version != 1 {
        return error(
            ErrorCode::InvalidVersion,
            "/dsl_version",
            "Only DSL version 1 is supported",
        );
    }
    if patch.document_id != document_id || patch.base_revision != revision {
        return error(
            ErrorCode::RevisionConflict,
            "/base_revision",
            "Document or revision does not match",
        );
    }
    if patch.operations.is_empty() || patch.operations.len() > 10_000 {
        return error(
            ErrorCode::ResourceLimit,
            "/operations",
            "Patch requires 1..10000 operations",
        );
    }
    let mut next = doc.clone();
    for op in &patch.operations {
        match op {
            Operation::SetText { target, text, cell } => {
                let n = target_mut(&mut next, target)?;
                if let Some(address) = cell {
                    if n.kind != NodeKind::Table {
                        return error(
                            ErrorCode::InvalidField,
                            "/cell",
                            "Cell addresses require a table",
                        );
                    }
                    let grid = table_grid(n)?;
                    let &(r, c) = grid
                        .get(address.row)
                        .and_then(|r| r.get(address.column))
                        .ok_or_else(|| {
                            Diagnostic::new(
                                ErrorCode::InvalidReference,
                                "/cell",
                                "Cell lies outside table",
                            )
                        })?;
                    if r != address.row
                        || grid[r].iter().position(|p| *p == (r, c)) != Some(address.column)
                    {
                        return error(
                            ErrorCode::InvalidReference,
                            "/cell",
                            "Address the origin of a merged cell",
                        );
                    }
                    n.rows[r].cells[c].text = Some(text.clone());
                    n.rows[r].cells[c].paragraphs.clear();
                    continue;
                }
                if n.kind != NodeKind::Text {
                    return error(
                        ErrorCode::InvalidField,
                        "/target",
                        "set_text requires a text node",
                    );
                }
                n.text = Some(text.clone());
                n.paragraphs.clear();
            }
            Operation::SetTextStyle { target, style } => {
                target_mut(&mut next, target)?.style.overlay(style)
            }
            Operation::UnsetTextStyle { target, properties } => {
                target_mut(&mut next, target)?.style.unset(properties)
            }
            Operation::SetFrame { target, frame } => {
                let n = target_mut(&mut next, target)?;
                n.frame = Some(*frame);
                n.width = None;
                n.height = None;
            }
            Operation::SetChartData { target, data } => {
                let n = target_mut(&mut next, target)?;
                if n.kind != NodeKind::Chart {
                    return error(
                        ErrorCode::InvalidField,
                        "/target",
                        "set_chart_data requires a chart",
                    );
                }
                n.data = Some(data.clone());
            }
            Operation::SetImageAsset { target, asset_ref } => {
                if !next.assets.contains_key(asset_ref)
                    && asset_ref.starts_with("asset_")
                    && asset_ref.len() == 70
                    && asset_ref[6..].bytes().all(|b| b.is_ascii_hexdigit())
                {
                    next.assets.insert(
                        asset_ref.clone(),
                        AssetRef {
                            handle: asset_ref.clone(),
                        },
                    );
                }
                let n = target_mut(&mut next, target)?;
                if n.kind != NodeKind::Image {
                    return error(
                        ErrorCode::InvalidField,
                        "/target",
                        "set_image_asset requires an image",
                    );
                }
                n.asset_ref = Some(asset_ref.clone());
            }
            Operation::InsertNode {
                parent,
                index,
                node,
            } => {
                let mut n = *node.clone();
                let mut opaque = false;
                n.visit(&mut |n| opaque |= n.kind == NodeKind::Opaque);
                if opaque {
                    return error(
                        ErrorCode::UnsupportedEdit,
                        "/node",
                        "Cannot insert opaque content",
                    );
                }
                n.visit_mut(&mut |n| {
                    n.id.get_or_insert_with(Uuid::now_v7);
                });
                insert(&mut next, parent, *index, n)?;
            }
            Operation::RemoveNode { target } => {
                let n = detach(&mut next, target)?;
                let mut opaque = false;
                n.visit(&mut |n| opaque |= n.kind == NodeKind::Opaque);
                if opaque {
                    return error(
                        ErrorCode::UnsupportedEdit,
                        "/target",
                        "Cannot delete a container with unsupported content",
                    );
                }
            }
            Operation::MoveNode {
                target,
                parent,
                index,
            } => {
                validate_target(parent)?;
                if next.find(target).and_then(|n| n.find(parent)).is_some() {
                    return error(
                        ErrorCode::LayoutCycle,
                        "/parent",
                        "Cannot move a node into itself or its descendants",
                    );
                }
                let n = detach(&mut next, target)?;
                let mut opaque = false;
                n.visit(&mut |n| opaque |= n.kind == NodeKind::Opaque);
                if opaque {
                    return error(
                        ErrorCode::UnsupportedEdit,
                        "/target",
                        "Cannot move a container with unsupported content",
                    );
                }
                insert(&mut next, parent, *index, n)?;
            }
        }
    }
    validate(&next, true)?;
    Ok(next)
}
