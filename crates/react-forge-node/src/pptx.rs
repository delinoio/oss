use std::sync::atomic::Ordering;

use forge_tree_doc::{ErrorCode, Presentation};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::{Operation, OperationKind};

pub fn process(op: &Operation) -> forge_tree_doc::Result<(Vec<u8>, String, String)> {
    let (bytes, doc, mut fonts) = match op.kind {
        OperationKind::Inspect => {
            let imported = forge_pptx::import(&op.source)?;
            let identity = SourceIdentity::capture(&imported, &op.source);
            // Import exposes structure and source identity before callers can
            // register fonts. Geometry is computed by export/measure instead.
            return Ok((
                op.source.clone(),
                serde_json::to_string(&imported.document).map_err(forge_package::failure)?,
                serde_json::json!({"source_identity": identity}).to_string(),
            ));
        }
        OperationKind::Generate => {
            let mut fonts = op.fonts()?;
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
            (bytes, doc, fonts)
        }
        OperationKind::Update => {
            let mut fonts = op.fonts()?;
            let mut imported = forge_pptx::import(&op.source)?;
            let update: Update = forge_tree_doc::parse(op.model.as_bytes())?;
            update.source_identity.restore(&mut imported, &op.source)?;
            let mut doc = imported.document.clone();
            for edit in update.edits {
                let target = doc
                    .find_mut(&forge_tree_doc::Target {
                        node_id: Some(edit.target),
                        key: None,
                    })
                    .ok_or_else(|| forge_package::failure("replacement target"))?;
                let frame = edit.replacement.frame.or(target.frame);
                *target = edit.replacement;
                target.id = Some(edit.target);
                target.frame = frame;
            }
            doc.assets.extend(update.assets);
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
            (bytes, doc, fonts)
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
    let geometry = serde_json::to_string(&geometry).map_err(forge_package::failure)?;
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
    edits: Vec<Edit>,
    assets: std::collections::BTreeMap<String, forge_tree_doc::AssetRef>,
    source_identity: SourceIdentity,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Edit {
    target: Uuid,
    replacement: forge_tree_doc::Node,
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn large_imported_models_export_through_bounded_replacement_envelopes() {
        let mut parts = forge_pptx::read_package(include_bytes!(
            "../../forge-pptx/tests/fixtures/external.pptx"
        ))
        .unwrap();
        let path = "ppt/slides/slide1.xml";
        let original = String::from_utf8(parts[path].clone()).unwrap();
        let xml = forge_package::xml(original.as_bytes()).unwrap();
        let title = xml
            .descendants()
            .find(|n| n.has_tag_name((forge_package::P, "cNvPr")) && n.attribute("id") == Some("2"))
            .unwrap()
            .parent()
            .unwrap()
            .parent()
            .unwrap();
        let raw = &original[title.range()];
        let text = "A".repeat(1_000_000);
        let shapes: String = (1000..1018)
            .map(|id| {
                raw.replace("id=\"2\"", &format!("id=\"{id}\""))
                    .replace("External presentation", &text)
            })
            .collect();
        parts.insert(
            path.into(),
            original
                .replace("</p:spTree>", &format!("{shapes}</p:spTree>"))
                .into_bytes(),
        );
        let source = forge_pptx::write_package(&parts).unwrap();
        let imported = forge_pptx::import(&source).unwrap();
        assert!(
            serde_json::to_vec(&imported.document).unwrap().len() > forge_tree_doc::MAX_JSON_BYTES
        );
        let identity = SourceIdentity::capture(&imported, &source);
        let target = imported.document.slides[0]
            .content
            .children
            .iter()
            .find(|n| n.kind == forge_tree_doc::NodeKind::Text)
            .unwrap();
        for edits in [
            serde_json::json!([]),
            serde_json::json!([{"target": target.id, "replacement":{"type":"text","text":"Edited"}}]),
        ] {
            let op = Operation {
                format: crate::Format::Pptx,
                kind: OperationKind::Update,
                model: serde_json::json!({"source_identity":identity,"edits":edits,"assets":{}})
                    .to_string(),
                source: source.clone(),
                assets: Default::default(),
                document_id: imported.document_id,
                revision: 1,
                cancelled: Default::default(),
                font_options: crate::FontOptions {
                    system: false,
                    ids: vec![],
                },
            };
            // Caller-only text work needs a registered font; no-op import itself
            // remains independent of system discovery.
            let mut op = op;
            op.assets
                .insert("font".into(), forge_tree_doc::FONT_BYTES.to_vec());
            op.font_options.ids.push("font".into());
            let (output, _, _) = process(&op).unwrap();
            if edits.as_array().unwrap().is_empty() {
                assert_eq!(output, source);
            } else {
                assert!(
                    String::from_utf8(forge_pptx::read_package(&output).unwrap()[path].clone())
                        .unwrap()
                        .contains("Edited")
                );
            }
        }
    }
}
