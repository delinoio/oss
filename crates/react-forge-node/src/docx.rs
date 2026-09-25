use forge_tree_doc::{ErrorCode, Result, error};
use serde::Deserialize;
use serde_json::json;

use crate::{Operation, OperationKind};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Edits {
    edits: Vec<Edit>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Edit {
    target_index: usize,
    blocks: Vec<forge_docx::Block>,
}

pub fn process(op: &Operation) -> Result<(Vec<u8>, String, String)> {
    match op.kind {
        OperationKind::Generate => {
            let mut document: forge_docx::Document = forge_tree_doc::parse(op.model.as_bytes())?;
            forge_docx::validate(&document)?;
            let mut fonts = op.fonts()?;
            let base = forge_document::Style {
                language: document.language.clone(),
                ..Default::default()
            };
            for section in &mut document.sections {
                for blocks in [
                    &mut section.header,
                    &mut section.blocks,
                    &mut section.footer,
                ] {
                    forge_docx::prepare_fonts(blocks, &mut fonts, &base)?;
                }
            }
            let bytes = forge_docx::generate(&document, &op.assets)?;
            Ok((
                bytes,
                op.model.clone(),
                serde_json::to_string(&forge_docx::measure(&document, &mut fonts)?)
                    .map_err(forge_package::failure)?,
            ))
        }
        OperationKind::Inspect => {
            let document = forge_docx::import(&op.source)?;
            let targets: Vec<_> = document.targets.iter().enumerate().map(|(index,t)| {
                use forge_docx::TargetKind;
                let kind = match t.kind { TargetKind::Paragraph=>"paragraph",TargetKind::Table=>"table",TargetKind::Cell=>"cell",TargetKind::Image=>"image",TargetKind::Chart=>"chart",TargetKind::Opaque=>"opaque" };
                json!({"id":t.id,"kind":kind,"target_index":index,"part":t.region.part,"start":t.region.range.start,"end":t.region.range.end,"text":t.text})
            }).collect();
            Ok((
                op.source.clone(),
                json!({"targets":targets}).to_string(),
                "{\"nodes\":{}}".into(),
            ))
        }
        OperationKind::Update => {
            let document = forge_docx::import(&op.source)?;
            let edits: Edits = forge_tree_doc::parse(op.model.as_bytes())?;
            let mut fonts = op.fonts()?;
            let mut changes = Vec::new();
            let mut ids = std::collections::HashSet::new();
            let mut count = 0;
            for mut edit in edits.edits {
                forge_docx::validate_blocks(&edit.blocks, 0, &mut count, &mut ids)?;
                forge_docx::prepare_fonts(
                    &mut edit.blocks,
                    &mut fonts,
                    &forge_document::Style::default(),
                )?;
                let Some(target) = document.targets.get(edit.target_index) else {
                    return error(
                        ErrorCode::InvalidReference,
                        "target",
                        "Invalid imported target",
                    );
                };
                changes.push((target.id, edit.blocks));
            }
            let mut geometry = forge_document::geometry::Geometry::default();
            for (id, blocks) in &changes {
                let Some(width) = document
                    .targets
                    .iter()
                    .find(|t| t.id == *id)
                    .and_then(|t| t.available_width)
                else {
                    // Export remains safe without a provable source width; omit
                    // geometry so measurement returns InvalidTarget, not a fake box.
                    continue;
                };
                forge_docx::measure_blocks(
                    blocks,
                    &mut fonts,
                    &forge_document::Style::default(),
                    0.0,
                    0.0,
                    width,
                    forge_document::geometry::CoordinateSpace::MountedRegion,
                    &mut geometry,
                )?;
            }
            let bytes = forge_docx::replace(&document, &changes, &op.assets)?;
            Ok((
                bytes,
                op.model.clone(),
                serde_json::to_string(&geometry).map_err(forge_package::failure)?,
            ))
        }
    }
}
