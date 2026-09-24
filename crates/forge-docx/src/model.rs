use std::collections::HashSet;

use forge_document::{Chart, Run, Style};
use forge_tree_doc::{ErrorCode, Result, error};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Document {
    #[serde(default = "Uuid::now_v7")]
    pub id: Uuid,
    pub language: Option<String>,
    pub sections: Vec<Section>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Section {
    #[serde(default = "page_width")]
    pub width: f64,
    #[serde(default = "page_height")]
    pub height: f64,
    #[serde(default = "margin")]
    pub margin: f64,
    #[serde(default)]
    pub header: Vec<Block>,
    #[serde(default)]
    pub footer: Vec<Block>,
    pub blocks: Vec<Block>,
}
fn page_width() -> f64 {
    612.0
}
fn page_height() -> f64 {
    792.0
}
fn margin() -> f64 {
    72.0
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ListKind {
    Bullet,
    Number,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct List {
    pub kind: ListKind,
    #[serde(default)]
    pub level: u8,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields, tag = "type", rename_all = "snake_case")]
pub enum Block {
    Paragraph {
        #[serde(default = "Uuid::now_v7")]
        id: Uuid,
        #[serde(default)]
        style: Style,
        heading: Option<u8>,
        list: Option<List>,
        runs: Vec<Run>,
    },
    Table {
        #[serde(default = "Uuid::now_v7")]
        id: Uuid,
        columns: Vec<f64>,
        rows: Vec<Row>,
    },
    Image {
        #[serde(default = "Uuid::now_v7")]
        id: Uuid,
        asset: String,
        width: f64,
        height: f64,
        alt: String,
    },
    Chart {
        #[serde(default = "Uuid::now_v7")]
        id: Uuid,
        chart: Chart,
        width: f64,
        height: f64,
        alt: String,
    },
    PageBreak {
        #[serde(default = "Uuid::now_v7")]
        id: Uuid,
    },
}

impl Block {
    pub fn id(&self) -> Uuid {
        match self {
            Self::Paragraph { id, .. }
            | Self::Table { id, .. }
            | Self::Image { id, .. }
            | Self::Chart { id, .. }
            | Self::PageBreak { id } => *id,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Row {
    #[serde(default)]
    pub header: bool,
    pub cells: Vec<Cell>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Cell {
    #[serde(default = "one")]
    pub row_span: usize,
    #[serde(default = "one")]
    pub col_span: usize,
    #[serde(default)]
    pub style: Style,
    pub blocks: Vec<Block>,
}
fn one() -> usize {
    1
}

/// Every slot identifies an anchor's (row, cell index), including
/// continuations.
pub fn table_grid(columns: &[f64], rows: &[Row]) -> Result<Vec<Vec<(usize, usize)>>> {
    if columns.is_empty()
        || columns.len() > 256
        || rows.is_empty()
        || rows.len() > 20_000
        || columns.len() * rows.len() > 20_000
    {
        return error(
            ErrorCode::ResourceLimit,
            "table",
            "Table requires at most 20000 grid slots",
        );
    }
    for width in columns {
        dimension(*width)?;
    }
    let mut grid = vec![vec![None; columns.len()]; rows.len()];
    for (r, row) in rows.iter().enumerate() {
        let mut next = 0;
        for (c, cell) in row.cells.iter().enumerate() {
            while next < columns.len() && grid[r][next].is_some() {
                next += 1;
            }
            if cell.row_span == 0
                || cell.col_span == 0
                || cell.row_span > rows.len() - r
                || cell.col_span > columns.len().saturating_sub(next)
            {
                return error(
                    ErrorCode::InvalidField,
                    "table/merge",
                    "Cell merge lies outside the table",
                );
            }
            for line in grid.iter_mut().skip(r).take(cell.row_span) {
                for slot in line.iter_mut().skip(next).take(cell.col_span) {
                    if slot.replace((r, c)).is_some() {
                        return error(
                            ErrorCode::InvalidField,
                            "table/merge",
                            "Table merges overlap",
                        );
                    }
                }
            }
            next += cell.col_span;
        }
    }
    grid.into_iter()
        .map(|row| {
            row.into_iter()
                .map(|slot| {
                    slot.ok_or_else(|| {
                        forge_tree_doc::Diagnostic::new(
                            ErrorCode::InvalidField,
                            "table",
                            "Table grid has missing cells",
                        )
                    })
                })
                .collect()
        })
        .collect()
}

pub fn dimension(value: f64) -> Result<()> {
    if !value.is_finite() || value <= 0.0 || value > 100_000.0 {
        return error(
            ErrorCode::InvalidGeometry,
            "size",
            "Dimension must be positive, finite and bounded",
        );
    }
    Ok(())
}

pub fn validate(document: &Document) -> Result<()> {
    let mut ids = HashSet::new();
    let mut count = 0;
    if document.id.get_version_num() != 7 || document.sections.is_empty() {
        return error(
            ErrorCode::InvalidField,
            "document",
            "Document requires a UUID-v7 identity and sections",
        );
    }
    if let Some(language) = &document.language {
        forge_document::text(language)?;
    }
    for section in &document.sections {
        dimension(section.width)?;
        dimension(section.height)?;
        if !section.margin.is_finite()
            || section.margin < 0.0
            || 2.0 * section.margin >= section.width.min(section.height)
        {
            return error(
                ErrorCode::InvalidGeometry,
                "section/margin",
                "Section margins leave no content area",
            );
        }
        for blocks in [&section.header, &section.blocks, &section.footer] {
            validate_blocks(blocks, 1, &mut count, &mut ids)?;
        }
    }
    Ok(())
}

pub fn validate_blocks(
    blocks: &[Block],
    depth: usize,
    count: &mut usize,
    ids: &mut HashSet<Uuid>,
) -> Result<()> {
    if depth > 48 {
        return error(
            ErrorCode::ResourceLimit,
            "blocks",
            "Document depth exceeds 48",
        );
    }
    for block in blocks {
        *count += 1;
        if *count > 20_000 {
            return error(
                ErrorCode::ResourceLimit,
                "blocks",
                "Document node count exceeds 20000",
            );
        }
        if block.id().get_version_num() != 7 || !ids.insert(block.id()) {
            return error(
                ErrorCode::DuplicateIdentity,
                "blocks/id",
                "Node requires a unique UUID-v7 identity",
            );
        }
        match block {
            Block::Paragraph {
                style,
                heading,
                list,
                runs,
                ..
            } => {
                style.validate()?;
                if heading.is_some_and(|level| !(1..=9).contains(&level))
                    || list.as_ref().is_some_and(|list| list.level > 8)
                {
                    return error(
                        ErrorCode::InvalidField,
                        "paragraph",
                        "Heading or list level is invalid",
                    );
                }
                for run in runs {
                    run.validate()?;
                }
            }
            Block::Table { columns, rows, .. } => {
                table_grid(columns, rows)?;
                for row in rows {
                    for cell in &row.cells {
                        cell.style.validate()?;
                        validate_blocks(&cell.blocks, depth + 1, count, ids)?;
                    }
                }
            }
            Block::Image {
                width, height, alt, ..
            } => {
                dimension(*width)?;
                dimension(*height)?;
                forge_document::text(alt)?;
            }
            Block::Chart {
                chart,
                width,
                height,
                alt,
                ..
            } => {
                chart.validate()?;
                dimension(*width)?;
                dimension(*height)?;
                forge_document::text(alt)?;
            }
            Block::PageBreak { .. } => {}
        }
    }
    Ok(())
}
