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
            let imported = forge_pptx::import(&op.source)?;
            let mut doc: Presentation = forge_tree_doc::parse(op.model.as_bytes())?;
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
    let geometry =
        serde_json::to_string(&geometry).map_err(|_| forge_package::failure("layout"))?;
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
