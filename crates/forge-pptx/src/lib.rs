//! Native editable PPTX generation with package-preserving updates.
#![forbid(unsafe_code)]
mod emit;
mod font;
mod import;
mod package;
mod update;
use std::collections::BTreeMap;

pub use emit::generate;
use forge_tree_doc::{Presentation, Result};
pub use import::{Binding, Imported, TemplateLayout, import};
pub use package::{
    MAX_PACKAGE_BYTES, read as read_package, sha, validate_package, write as write_package,
};
use serde::{Deserialize, Serialize};
pub use update::update;
use uuid::Uuid;
pub type Assets = BTreeMap<String, Vec<u8>>;
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Metadata {
    pub version: u32,
    pub document_id: Uuid,
    pub revision: u64,
    pub document: Presentation,
    pub bindings: BTreeMap<Uuid, Binding>,
    pub hashes: BTreeMap<String, String>,
}
pub(crate) fn add_metadata(
    parts: &mut package::Package,
    document_id: Uuid,
    revision: u64,
    document: &Presentation,
    bindings: &BTreeMap<Uuid, Binding>,
) -> Result<()> {
    use package::*;
    if !parts.contains_key(META) {
        add_content_type(parts, META, "application/xml")?;
        add_relationship(
            parts,
            "",
            "rIdForgeMetadata",
            &format!("{R}/customXml"),
            "/customXml/forge.xml",
        )?;
    }
    let hashes = parts
        .iter()
        .filter(|(p, _)| p.as_str() != META)
        .map(|(p, b)| (p.clone(), sha(b)))
        .collect();
    let metadata = Metadata {
        version: 1,
        document_id,
        revision,
        document: document.clone(),
        bindings: bindings.clone(),
        hashes,
    };
    let json = serde_json::to_string(&metadata).map_err(failure)?;
    parts.insert(
        META.into(),
        format!(
            "<forge xmlns=\"urn:delino:forge:v1\">{}</forge>",
            escape(&json)
        )
        .into_bytes(),
    );
    Ok(())
}
/// Validate image format and dimensions before registering an asset.
pub fn validate_image(bytes: &[u8]) -> Result<()> {
    emit::image_info(bytes).map(|_| ())
}

/// Prepare a disposable renderer input with the pinned default font. Managed
/// and exported source parts remain untouched by this preview-only operation.
pub fn preview_bytes(bytes: &[u8]) -> forge_tree_doc::Result<Vec<u8>> {
    let mut parts = package::read(bytes)?;
    font::embed(&mut parts)?;
    package::write(&parts)
}
