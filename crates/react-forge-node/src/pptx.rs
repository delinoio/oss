use std::sync::atomic::Ordering;

use forge_tree_doc::{ErrorCode, Presentation};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::{Operation, OperationKind};

pub fn process(op: &Operation) -> forge_tree_doc::Result<(Vec<u8>, String, String)> {
    let mut fonts = op.fonts()?;
    let mut identity = None;
    let (bytes, doc) = match op.kind {
        OperationKind::Inspect => {
            let imported = forge_pptx::import(&op.source)?;
            identity = Some(SourceIdentity::capture(&imported, &op.source));
            (op.source.clone(), imported.document)
        }
        OperationKind::Generate => {
            let mut doc: Presentation = forge_tree_doc::parse(op.model.as_bytes())?;
            doc.assign_ids();
            let input: serde_json::Value = forge_tree_doc::parse(op.model.as_bytes())?;
            if input.pointer("/theme/font_family").is_none() {
                doc.theme.font_family = fonts.default_family()?;
            }
            check_chart_fonts(&doc, None, &mut fonts)?;
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
            let mut imported = forge_pptx::import(&op.source)?;
            let update: Update = forge_tree_doc::parse(op.model.as_bytes())?;
            update.source_identity.restore(&mut imported, &op.source)?;
            let mut doc = update.document;
            doc.assign_ids();
            let mut assets = imported.assets;
            assets.extend(op.assets.clone());
            check_chart_fonts(&doc, Some(&imported.document), &mut fonts)?;
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
    let mut geometry = serde_json::to_value(&geometry).map_err(forge_package::failure)?;
    if let Some(identity) = identity {
        geometry["source_identity"] =
            serde_json::to_value(identity).map_err(forge_package::failure)?;
    }
    let geometry = geometry.to_string();
    Ok((bytes, model, geometry))
}

fn check_chart_fonts(
    doc: &Presentation,
    previous: Option<&Presentation>,
    fonts: &mut forge_document::fonts::Fonts,
) -> forge_tree_doc::Result<()> {
    forge_tree_doc::validate(doc, previous.is_some())?;
    fn visit(
        node: &forge_tree_doc::Node,
        previous: Option<&Presentation>,
        fonts: &mut forge_document::fonts::Fonts,
    ) -> forge_tree_doc::Result<()> {
        let old = previous.and_then(|doc| {
            doc.find(&forge_tree_doc::Target {
                key: None,
                node_id: node.id,
            })
        });
        if node.kind == forge_tree_doc::NodeKind::Chart
            && old.is_none_or(|old| old.data != node.data)
            && let Some(data) = &node.data
        {
            for text in data
                .categories
                .iter()
                .chain(data.series.iter().map(|series| &series.name))
            {
                fonts.check_text(text, &forge_document::Style::default())?;
            }
        }
        for child in &node.children {
            visit(child, previous, fonts)?;
        }
        Ok(())
    }
    for slide in &doc.slides {
        visit(&slide.content, previous, fonts)?;
    }
    Ok(())
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct SourceIdentity {
    source_hash: String,
    document_id: Uuid,
    ids: Vec<Uuid>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Update {
    document: Presentation,
    source_identity: SourceIdentity,
}
impl SourceIdentity {
    fn capture(imported: &forge_pptx::Imported, source: &[u8]) -> Self {
        let mut ids = Vec::new();
        for slide in &imported.document.slides {
            ids.push(slide.id.unwrap());
            slide.content.visit(&mut |node| ids.push(node.id.unwrap()));
        }
        Self {
            source_hash: forge_package::sha(source),
            document_id: imported.document_id,
            ids,
        }
    }

    fn restore(
        &self,
        imported: &mut forge_pptx::Imported,
        source: &[u8],
    ) -> forge_tree_doc::Result<()> {
        let original = Self::capture(imported, source);
        let mut unique = std::collections::HashSet::from([self.document_id]);
        if self.source_hash != original.source_hash
            || self.ids.len() != original.ids.len()
            || self.document_id.get_version_num() != 7
            || self
                .ids
                .iter()
                .any(|id| id.get_version_num() != 7 || !unique.insert(*id))
        {
            return forge_tree_doc::error(
                ErrorCode::InvalidReference,
                "target",
                "Imported identity snapshot does not match its source",
            );
        }
        // External packages have no Forge metadata, so each independent import
        // initially assigns fresh UUID-v7s. Rebind the immutable source traversal
        // to the session's captured identities without writing metadata on import.
        let mapping: std::collections::BTreeMap<_, _> = original
            .ids
            .into_iter()
            .zip(self.ids.iter().copied())
            .collect();
        // The foundation importer also derives image asset keys from node UUIDs.
        // Rebind both ends of those references so untouched images stay equal.
        let asset_keys: std::collections::BTreeMap<_, _> = mapping
            .iter()
            .map(|(old, new)| (format!("image-{old}"), format!("image-{new}")))
            .filter(|(old, _)| imported.document.assets.contains_key(old))
            .collect();
        imported.document.assets = std::mem::take(&mut imported.document.assets)
            .into_iter()
            .map(|(key, value)| (asset_keys.get(&key).cloned().unwrap_or(key), value))
            .collect();
        for slide in &mut imported.document.slides {
            slide.id = Some(mapping[&slide.id.unwrap()]);
            slide.content.visit_mut(&mut |node| {
                node.id = Some(mapping[&node.id.unwrap()]);
                if let Some(reference) = &mut node.asset_ref
                    && let Some(rebound) = asset_keys.get(reference)
                {
                    *reference = rebound.clone();
                }
                for endpoint in [&mut node.from, &mut node.to].into_iter().flatten() {
                    if let Some(id) = &mut endpoint.target.node_id
                        && let Some(new) = mapping.get(id)
                    {
                        *id = *new;
                    }
                }
            });
        }
        imported.bindings = std::mem::take(&mut imported.bindings)
            .into_iter()
            .map(|(id, binding)| (mapping[&id], binding))
            .collect();
        imported.document_id = self.document_id;
        Ok(())
    }
}
