use forge_document::geometry::{CoordinateSpace, Frame, Geometry};

use crate::*;

/// Worksheet authoring boxes in points, using Excel's conventional default
/// 7-pixel maximum digit width. They are not print-pagination or pixel-perfect
/// measurements of a particular spreadsheet application's substituted fonts.
pub fn measure(workbook: &Workbook) -> forge_tree_doc::Result<Geometry> {
    validate(workbook)?;
    let mut geometry = Geometry::default();
    for sheet in &workbook.sheets {
        let columns: std::collections::HashMap<_, _> =
            sheet.columns.iter().map(|c| (c.column, c.width)).collect();
        let column_width = |column: u16| {
            let width = columns.get(&column).copied().unwrap_or(8.43);
            ((width * 7.0 + 5.0).floor()) * 0.75
        };
        let mut positions = vec![0.0; 16_385];
        for column in 0..16_384 {
            positions[column + 1] = positions[column] + column_width(column as u16);
        }
        let x = |column: u16| positions[usize::from(column)];
        let mut rows: Vec<_> = sheet
            .rows
            .iter()
            .map(|r| (r.row, r.height - 15.0))
            .collect();
        rows.sort_by_key(|r| r.0);
        let mut prefix = vec![0.0];
        for (_, delta) in &rows {
            prefix.push(prefix.last().copied().unwrap() + delta);
        }
        let y = |row: u32| f64::from(row) * 15.0 + prefix[rows.partition_point(|r| r.0 < row)];
        let mut total_width = 0.0_f64;
        let mut total_height = 0.0_f64;
        for cell in &sheet.cells {
            let range = sheet
                .merges
                .iter()
                .find(|r| r.first == cell.address)
                .copied()
                .unwrap_or(Range {
                    first: cell.address,
                    last: cell.address,
                });
            let height = y(range.last.row + 1) - y(range.first.row);
            let left = x(range.first.column);
            let top = y(range.first.row);
            let width = positions[usize::from(range.last.column) + 1] - left;
            total_width = total_width.max(left + width);
            total_height = total_height.max(top + height);
            geometry.insert(
                cell.id,
                Frame {
                    x: left,
                    y: top,
                    width,
                    height,
                    coordinate_space: CoordinateSpace::Worksheet,
                },
            );
        }
        for chart in &sheet.charts {
            let left = x(chart.at.column);
            let top = y(chart.at.row);
            let width = f64::from(chart.width) * 0.75;
            let height = f64::from(chart.height) * 0.75;
            total_width = total_width.max(left + width);
            total_height = total_height.max(top + height);
            geometry.insert(
                chart.id,
                Frame {
                    x: left,
                    y: top,
                    width,
                    height,
                    coordinate_space: CoordinateSpace::Worksheet,
                },
            );
        }
        geometry.insert(
            sheet.id,
            Frame {
                x: 0.0,
                y: 0.0,
                width: total_width,
                height: total_height,
                coordinate_space: CoordinateSpace::Worksheet,
            },
        );
    }
    Ok(geometry)
}
