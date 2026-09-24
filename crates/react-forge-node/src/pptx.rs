use std::sync::atomic::Ordering;

use forge_tree_doc::{ErrorCode, Presentation};

use crate::{Operation, OperationKind};

pub fn process(op: &Operation) -> forge_tree_doc::Result<(Vec<u8>, String, String)> {
    let (bytes, doc) = match op.kind {
        OperationKind::Inspect => {
            let imported = forge_pptx::import(&op.source)?;
            (op.source.clone(), imported.document)
        }
        OperationKind::Generate => {
            let mut doc: Presentation = forge_tree_doc::parse(op.model.as_bytes())?;
            doc.assign_ids();
            let bytes = forge_pptx::generate(&doc, &op.assets, op.document_id, op.revision)?;
            (bytes, doc)
        }
        OperationKind::Update => {
            let imported = forge_pptx::import(&op.source)?;
            let mut doc: Presentation = forge_tree_doc::parse(op.model.as_bytes())?;
            doc.assign_ids();
            let mut assets = imported.assets;
            assets.extend(op.assets.clone());
            let bytes = forge_pptx::update(
                &op.source,
                &imported.document,
                &imported.bindings,
                &doc,
                &assets,
                imported.document_id,
                op.revision,
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
    let geometry = forge_tree_doc::layout_for_edit(&doc, Some(&doc))?;
    let model = serde_json::to_string(&doc).map_err(|_| forge_package::failure("model"))?;
    let geometry =
        serde_json::to_string(&geometry).map_err(|_| forge_package::failure("layout"))?;
    Ok((bytes, model, geometry))
}
