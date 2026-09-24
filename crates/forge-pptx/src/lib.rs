//! Native editable PPTX generation with package-preserving updates.
#![forbid(unsafe_code)]
mod emit;
mod font;
mod import;
mod package;
mod update;
use std::collections::BTreeMap;

pub use emit::{generate, generate_with_measurer};
use forge_tree_doc::{Presentation, Result};
pub use import::{Binding, Imported, TemplateLayout, import};
pub use package::{
    MAX_PACKAGE_BYTES, read as read_package, sha, validate_package, write as write_package,
};
use serde::{Deserialize, Serialize};
pub use update::{update, update_with_measurer};
use uuid::Uuid;
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FontEmbedding {
    PinnedDefault,
    ReferenceOnly,
}
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
const METADATA_NS: &str = "urn:delino:forge:v1";

pub(crate) fn metadata_part(parts: &package::Package) -> Result<Option<String>> {
    use package::*;
    let mut found = None;
    for relationship in relationships(parts, "")? {
        if relationship.external || relationship.kind != format!("{R}/customXml") {
            continue;
        }
        let path = resolve("", &relationship.target)?;
        let doc = xml(parts.get(&path).ok_or_else(|| failure("custom XML part"))?)?;
        if doc.root_element().has_tag_name((METADATA_NS, "forge")) {
            if found.is_some() {
                return Err(failure("ambiguous Forge metadata"));
            }
            found = Some(path);
        }
    }
    Ok(found)
}

pub(crate) fn add_metadata(
    parts: &mut package::Package,
    document_id: Uuid,
    revision: u64,
    document: &Presentation,
    bindings: &BTreeMap<Uuid, Binding>,
) -> Result<()> {
    use package::*;
    let path = if let Some(path) = metadata_part(parts)? {
        path
    } else {
        // A filename or relationship ID alone does not establish ownership.
        // Keep unrelated custom XML intact and allocate a fresh metadata part.
        let mut path = META.to_owned();
        while parts.keys().any(|p| p.eq_ignore_ascii_case(&path)) {
            path = format!("customXml/forge-{}.xml", Uuid::now_v7());
        }
        let relationships = relationships(parts, "")?;
        let mut id = "rIdForgeMetadata".to_owned();
        while relationships.iter().any(|r| r.id == id) {
            id = format!("rIdForgeMetadata{}", Uuid::now_v7().simple());
        }
        add_content_type(parts, &path, "application/xml")?;
        add_relationship(
            parts,
            "",
            &id,
            &format!("{R}/customXml"),
            &format!("/{path}"),
        )?;
        path
    };
    let hashes = parts
        .iter()
        .filter(|(p, _)| **p != path)
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
        path,
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
