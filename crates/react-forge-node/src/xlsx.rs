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
    value: Value,
}
#[derive(Deserialize)]
#[serde(
    tag = "type",
    content = "value",
    rename_all = "snake_case",
    deny_unknown_fields
)]
enum Value {
    Cell(forge_xlsx::Cell),
    ConditionalFormat(forge_xlsx::ConditionalFormat),
    Validation(forge_xlsx::Validation),
    Chart(forge_document::Chart),
}

pub fn process(op: &Operation) -> Result<(Vec<u8>, String, String)> {
    match op.kind {
        OperationKind::Generate => {
            let mut document: forge_xlsx::Workbook = forge_tree_doc::parse(op.model.as_bytes())?;
            let mut fonts = op.fonts()?;
            for sheet in &mut document.sheets {
                for cell in &mut sheet.cells {
                    forge_xlsx::prepare_cell_fonts(cell, &mut fonts)?;
                }
                for chart in &sheet.charts {
                    fonts.check_chart(&chart.chart)?;
                }
                for rule in &sheet.validations {
                    forge_xlsx::check_validation_fonts(rule, &mut fonts)?;
                }
            }
            let bytes = forge_xlsx::generate(&document)?;
            Ok((bytes, op.model.clone(), "{\"nodes\":{}}".into()))
        }
        OperationKind::Inspect => {
            let document = forge_xlsx::import(&op.source)?;
            let targets: Vec<_> = document
                .targets
                .iter()
                .enumerate()
                .map(|(index, t)| {
                    use forge_xlsx::TargetKind;
                    let kind = match t.kind {
                        TargetKind::Cell => "cell",
                        TargetKind::ConditionalFormat => "conditional_format",
                        TargetKind::Validation => "validation",
                        TargetKind::Chart => "chart",
                        TargetKind::Opaque => "opaque",
                    };
                    json!({"id":t.id,"kind":kind,"target_index":index,"part":t.region.part,
                    "start":t.region.range.start,"end":t.region.range.end,"text":t.text,
                    "sheet":t.sheet,"address":t.address,"range":t.range})
                })
                .collect();
            Ok((
                op.source.clone(),
                json!({"targets": targets}).to_string(),
                "{\"nodes\":{}}".into(),
            ))
        }
        OperationKind::Update => {
            let document = forge_xlsx::import(&op.source)?;
            let edits: Edits = forge_tree_doc::parse(op.model.as_bytes())?;
            let mut fonts = op.fonts()?;
            let mut changes = Vec::new();
            for edit in edits.edits {
                let Some(target) = document.targets.get(edit.target_index) else {
                    return error(
                        ErrorCode::InvalidReference,
                        "target",
                        "Invalid imported target",
                    );
                };
                use forge_xlsx::EditValue;
                let value = match edit.value {
                    Value::Cell(mut v) => {
                        // Omitted format on an imported edit retains the source style.
                        let previous = v.format.clone();
                        forge_xlsx::prepare_cell_fonts(&mut v, &mut fonts)?;
                        if previous.is_none() {
                            v.format = None;
                        }
                        EditValue::Cell(v)
                    }
                    Value::ConditionalFormat(v) => EditValue::ConditionalFormat(v),
                    Value::Validation(v) => {
                        forge_xlsx::check_validation_fonts(&v, &mut fonts)?;
                        EditValue::Validation(v)
                    }
                    Value::Chart(v) => {
                        fonts.check_chart(&v)?;
                        EditValue::Chart(v)
                    }
                };
                changes.push((target.id, value));
            }
            Ok((
                forge_xlsx::replace(&document, &changes)?,
                op.model.clone(),
                "{\"nodes\":{}}".into(),
            ))
        }
    }
}
