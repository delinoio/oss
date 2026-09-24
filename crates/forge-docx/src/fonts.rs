use forge_document::{
    Style,
    fonts::{Fonts, overlay},
};
use forge_tree_doc::Result;

use crate::Block;

/// Validate only newly authored regions. Preserved source XML is never rendered
/// or checked against fonts installed on this machine.
pub fn prepare_fonts(blocks: &mut [Block], fonts: &mut Fonts, base: &Style) -> Result<()> {
    for block in blocks {
        match block {
            Block::Paragraph { style, runs, .. } => {
                *style = overlay(base, style);
                if style.font_family.is_none() {
                    style.font_family = Some(fonts.default_family()?);
                }
                fonts.shape(runs, style, 100_000.0, false)?;
                for run in runs {
                    run.style = overlay(style, &run.style);
                }
            }
            Block::Table { rows, .. } => {
                for row in rows {
                    for cell in &mut row.cells {
                        let style = overlay(base, &cell.style);
                        prepare_fonts(&mut cell.blocks, fonts, &style)?;
                    }
                }
            }
            Block::Chart { chart, .. } => fonts.check_chart(chart)?,
            Block::Image { .. } | Block::PageBreak { .. } => {}
        }
    }
    Ok(())
}
