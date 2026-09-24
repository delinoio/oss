use forge_document::{
    Style,
    fonts::{Fonts, overlay},
    geometry::{CoordinateSpace, Frame, Geometry},
};
use forge_tree_doc::Result;

use crate::{Block, Document};

/// Return authoring-flow boxes in points. Word owns actual pagination,
/// including widow control, font substitution and table-row splitting. These
/// boxes never claim to be Word page coordinates and do not render preserved
/// source XML.
pub fn measure(document: &Document, fonts: &mut Fonts) -> Result<Geometry> {
    crate::validate(document)?;
    let mut geometry = Geometry::default();
    let mut y = 0.0;
    let mut width = 0.0_f64;
    for section in &document.sections {
        let base = Style {
            language: document.language.clone(),
            ..Default::default()
        };
        let inner = section.width - 2.0 * section.margin;
        let body = measure_blocks(
            &section.blocks,
            fonts,
            &base,
            section.margin,
            y + section.margin,
            inner,
            CoordinateSpace::WordFlow,
            &mut geometry,
        )?;
        // Header/footer are their own authored regions; Office chooses the
        // repeated instances on each final page.
        measure_blocks(
            &section.header,
            fonts,
            &base,
            section.margin,
            0.0,
            inner,
            CoordinateSpace::MountedRegion,
            &mut geometry,
        )?;
        measure_blocks(
            &section.footer,
            fonts,
            &base,
            section.margin,
            0.0,
            inner,
            CoordinateSpace::MountedRegion,
            &mut geometry,
        )?;
        y += body + 2.0 * section.margin;
        width = width.max(section.width);
    }
    geometry.insert(
        document.id,
        Frame {
            x: 0.0,
            y: 0.0,
            width,
            height: y,
            coordinate_space: CoordinateSpace::WordFlow,
        },
    );
    Ok(geometry)
}
#[allow(clippy::too_many_arguments)]
pub fn measure_blocks(
    blocks: &[Block],
    fonts: &mut Fonts,
    base: &Style,
    x: f64,
    y: f64,
    width: f64,
    space: CoordinateSpace,
    result: &mut Geometry,
) -> Result<f64> {
    crate::validate_blocks(blocks, 0, &mut 0, &mut std::collections::HashSet::new())?;
    measure_validated(blocks, fonts, base, x, y, width, space, result)
}
#[allow(clippy::too_many_arguments)]
fn measure_validated(
    blocks: &[Block],
    fonts: &mut Fonts,
    base: &Style,
    x: f64,
    y: f64,
    width: f64,
    space: CoordinateSpace,
    result: &mut Geometry,
) -> Result<f64> {
    let mut cursor = y;
    for block in blocks {
        forge_tree_doc::cancellation::checkpoint()?;
        let (id, w, h) = match block {
            Block::Paragraph {
                id, style, runs, ..
            } => {
                let style = overlay(base, style);
                let text = fonts.shape(runs, &style, width.max(1.0), false)?;
                (*id, width, f64::from(text.layout.height()))
            }
            Block::Image {
                id, width, height, ..
            }
            | Block::Chart {
                id, width, height, ..
            } => (*id, *width, *height),
            Block::PageBreak { id } => (*id, 0.0, 0.0),
            Block::Table { id, columns, rows } => {
                let mut heights = vec![8.0_f64; rows.len()];
                let mut positioned = Vec::new();
                let mut occupied = std::collections::BTreeSet::new();
                for (row_index, row) in rows.iter().enumerate() {
                    let mut column = 0;
                    for cell in &row.cells {
                        while occupied.contains(&(row_index, column)) {
                            column += 1;
                        }
                        let cell_width =
                            columns[column..column + cell.col_span].iter().sum::<f64>();
                        let mut scratch = Geometry::default();
                        let style = overlay(base, &cell.style);
                        let height = measure_validated(
                            &cell.blocks,
                            fonts,
                            &style,
                            0.0,
                            0.0,
                            cell_width - 8.0,
                            space,
                            &mut scratch,
                        )? + 8.0;
                        let existing: f64 =
                            heights[row_index..row_index + cell.row_span].iter().sum();
                        if height > existing {
                            heights[row_index + cell.row_span - 1] += height - existing;
                        }
                        positioned.push((row_index, column, scratch));
                        for r in row_index..row_index + cell.row_span {
                            for c in column..column + cell.col_span {
                                occupied.insert((r, c));
                            }
                        }
                        column += cell.col_span;
                    }
                }
                for (row, column, mut scratch) in positioned {
                    let cell_x = x + columns[..column].iter().sum::<f64>() + 4.0;
                    let cell_y = cursor + heights[..row].iter().sum::<f64>() + 4.0;
                    for (id, placed) in &mut scratch.nodes {
                        placed.frame.x += cell_x;
                        placed.frame.y += cell_y;
                        result.insert(*id, placed.frame.clone());
                    }
                }
                (*id, columns.iter().sum(), heights.iter().sum())
            }
        };
        result.insert(
            id,
            Frame {
                x,
                y: cursor,
                width: w,
                height: h,
                coordinate_space: space,
            },
        );
        cursor += h + 6.0;
    }
    Ok(cursor - y)
}
