use forge_tree_doc::{ErrorCode, Result, error};

use crate::{Operation, OperationKind};

pub fn process(op: &Operation) -> Result<(Vec<u8>, String, String)> {
    if !matches!(op.kind, OperationKind::Generate) {
        return error(
            ErrorCode::UnsupportedPackage,
            "sprite",
            "Sprite archive import is unsupported; update the React source",
        );
    }
    let project: forge_sprite::Project = forge_tree_doc::parse(op.model.as_bytes())?;
    let (bytes, geometry) = forge_sprite::generate(&project, &op.assets)?;
    Ok((bytes, op.model.clone(), geometry.to_string()))
}
