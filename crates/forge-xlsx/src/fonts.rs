use forge_document::{Style, fonts::Fonts};
use forge_tree_doc::Result;

use crate::{CachedValue, Cell, CellFormat, Value};

pub fn prepare_cell_fonts(cell: &mut Cell, fonts: &mut Fonts) -> Result<()> {
    let format = cell.format.get_or_insert_with(CellFormat::default);
    if format.style.font_family.is_none() {
        format.style.font_family = Some(fonts.default_family()?);
    }
    match &cell.value {
        Value::Text(text) => fonts.check_text(text, &format.style)?,
        Value::Formula(formula) => match &formula.cached {
            Some(CachedValue::Text(text)) => fonts.check_text(text, &format.style)?,
            _ => fonts.check_text("0123456789", &format.style)?,
        },
        Value::Blank => {}
        _ => fonts.check_text("0123456789", &format.style)?,
    }
    Ok(())
}
pub fn check_validation_fonts(rule: &crate::Validation, fonts: &mut Fonts) -> Result<()> {
    for text in [&rule.prompt, &rule.error].into_iter().flatten() {
        fonts.check_text(text, &Style::default())?;
    }
    Ok(())
}
