use forge_tree_doc::{ErrorCode, Result, error};

use crate::{Operation, OperationKind};
pub fn process(op: &Operation) -> Result<(Vec<u8>, String, String)> {
    if !matches!(op.kind, OperationKind::Generate) {
        return error(
            ErrorCode::UnsupportedPackage,
            "pdf",
            "PDF import and editing are not supported",
        );
    }
    let document: forge_pdf::Document = forge_tree_doc::parse(op.model.as_bytes())?;
    let (bytes, layout) = forge_pdf::generate(&document, &op.assets, &mut op.fonts()?)?;
    let geometry = serde_json::json!({"nodes":layout.nodes}).to_string();
    Ok((bytes, op.model.clone(), geometry))
}
