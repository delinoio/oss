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
            let document: forge_docx::Document = forge_tree_doc::parse(op.model.as_bytes())?;
            let bytes = forge_docx::generate(&document, &op.assets)?;
            Ok((bytes, op.model.clone(), "{\"nodes\":{}}".into()))
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
            let mut changes = Vec::new();
            for edit in edits.edits {
                let Some(target) = document.targets.get(edit.target_index) else {
                    return error(
                        ErrorCode::InvalidReference,
                        "target",
                        "Invalid imported target",
                    );
                };
                changes.push((target.id, edit.blocks));
            }
            let bytes = forge_docx::replace(&document, &changes, &op.assets)?;
            Ok((bytes, op.model.clone(), "{\"nodes\":{}}".into()))
        }
    }
}
