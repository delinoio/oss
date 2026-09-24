use std::sync::atomic::Ordering;

use forge_tree_doc::{ErrorCode, Presentation};

use crate::{Operation, OperationKind};

pub fn process(op: &Operation) -> forge_tree_doc::Result<(Vec<u8>, String, String)> {
    let mut fonts = op.fonts()?;
    let (bytes, doc) = match op.kind {
        OperationKind::Inspect => {
            let imported = forge_pptx::import(&op.source)?;
            (op.source.clone(), imported.document)
        }
        OperationKind::Generate => {
            let mut doc: Presentation = forge_tree_doc::parse(op.model.as_bytes())?;
            doc.assign_ids();
            let input: serde_json::Value = forge_tree_doc::parse(op.model.as_bytes())?;
            if input.pointer("/theme/font_family").is_none() {
                doc.theme.font_family = fonts.default_family()?;
            }
            let bytes = forge_pptx::generate_with_measurer(
                &doc,
                &op.assets,
                op.document_id,
                op.revision,
                &mut fonts,
                forge_pptx::FontEmbedding::ReferenceOnly,
            )?;
            (bytes, doc)
        }
        OperationKind::Update => {
            let imported = forge_pptx::import(&op.source)?;
            let mut doc: Presentation = forge_tree_doc::parse(op.model.as_bytes())?;
            doc.assign_ids();
            let mut assets = imported.assets;
            assets.extend(op.assets.clone());
            let bytes = forge_pptx::update_with_measurer(
                &op.source,
                &imported.document,
                &imported.bindings,
                &doc,
                &assets,
                imported.document_id,
                op.revision,
                &mut fonts,
            )?;
            (bytes, doc)
        }
    };
    if op.cancelled.load(Ordering::Acquire) {
        return forge_tree_doc::error(
            ErrorCode::Cancelled,
            "",
            "The native operation was cancelled",
        );
    }
    let geometry = forge_tree_doc::layout_with_measurer(&doc, Some(&doc), &mut fonts)?;
    let model = serde_json::to_string(&doc).map_err(|_| forge_package::failure("model"))?;
    let geometry =
        serde_json::to_string(&geometry).map_err(|_| forge_package::failure("layout"))?;
    Ok((bytes, model, geometry))
}
