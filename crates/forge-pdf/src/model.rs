use std::collections::HashSet;

use forge_document::{Run, Style};
use forge_tree_doc::{ErrorCode, Result, error};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Document {
    pub id: Uuid,
    pub title: Option<String>,
    pub language: String,
    pub pages: Vec<Page>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Page {
    pub id: Uuid,
    pub width: f64,
    pub height: f64,
    pub margin: f64,
    pub blocks: Vec<Block>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case", deny_unknown_fields)]
pub enum Block {
    Paragraph {
        id: Uuid,
        #[serde(default)]
        style: Style,
        heading: Option<u8>,
        runs: Vec<Run>,
    },
    List {
        id: Uuid,
        #[serde(default)]
        ordered: bool,
        items: Vec<ListItem>,
    },
    Table {
        id: Uuid,
        columns: Vec<f64>,
        rows: Vec<Row>,
    },
    Image {
        id: Uuid,
        asset: String,
        width: f64,
        height: f64,
        alt: String,
    },
    Shape {
        id: Uuid,
        kind: ShapeKind,
        width: f64,
        height: f64,
        fill: Option<String>,
        stroke: Option<String>,
        alt: Option<String>,
    },
    PageBreak {
        id: Uuid,
    },
}
impl Block {
    pub fn id(&self) -> Uuid {
        match self {
            Self::Paragraph { id, .. }
            | Self::List { id, .. }
            | Self::Table { id, .. }
            | Self::Image { id, .. }
            | Self::Shape { id, .. }
            | Self::PageBreak { id } => *id,
        }
    }
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ShapeKind {
    Rectangle,
    Ellipse,
    Line,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ListItem {
    pub id: Uuid,
    #[serde(default)]
    pub style: Style,
    pub runs: Vec<Run>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Row {
    pub id: Uuid,
    #[serde(default)]
    pub header: bool,
    pub cells: Vec<Cell>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Cell {
    pub id: Uuid,
    #[serde(default)]
    pub style: Style,
    pub runs: Vec<Run>,
}

pub fn validate(document: &Document) -> Result<()> {
    let mut ids = HashSet::new();
    let mut identity = |id: Uuid| -> Result<()> {
        if ids.len() >= 20_000 {
            return error(
                ErrorCode::ResourceLimit,
                "document",
                "PDF exceeds 20000 nodes",
            );
        }
        if id.get_version_num() != 7 || !ids.insert(id) {
            return error(
                ErrorCode::DuplicateIdentity,
                "id",
                "PDF identities must be unique UUID-v7 values",
            );
        }
        Ok(())
    };
    identity(document.id)?;
    forge_document::text(&document.language)?;
    if document.language.is_empty() {
        return error(
            ErrorCode::InvalidField,
            "language",
            "PDF language is required",
        );
    }
    if let Some(title) = &document.title {
        forge_document::text(title)?;
    }
    if document.pages.is_empty() {
        return error(
            ErrorCode::InvalidField,
            "pages",
            "PDF requires at least one page",
        );
    }
    for page in &document.pages {
        identity(page.id)?;
        size(page.width, page.height)?;
        if !page.margin.is_finite()
            || page.margin < 0.0
            || page.margin * 2.0 >= page.width.min(page.height)
        {
            return error(
                ErrorCode::InvalidGeometry,
                "page/margin",
                "Page margins leave no content area",
            );
        }
        for block in &page.blocks {
            identity(block.id())?;
            match block {
                Block::Paragraph {
                    style,
                    heading,
                    runs,
                    ..
                } => {
                    style.validate()?;
                    for run in runs {
                        run.validate()?;
                    }
                    if heading.is_some_and(|level| !(1..=6).contains(&level)) {
                        return error(
                            ErrorCode::InvalidField,
                            "heading",
                            "PDF heading levels must be between 1 and 6",
                        );
                    }
                }
                Block::List { items, .. } => {
                    for item in items {
                        identity(item.id)?;
                        item.style.validate()?;
                        for run in &item.runs {
                            run.validate()?;
                        }
                    }
                }
                Block::Table { columns, rows, .. } => {
                    if columns.is_empty()
                        || columns
                            .iter()
                            .any(|w| !w.is_finite() || *w <= 8.0 || *w > 100_000.0)
                    {
                        return error(
                            ErrorCode::InvalidGeometry,
                            "table/columns",
                            "Table columns must leave room for cell padding",
                        );
                    }
                    let mut body = false;
                    for row in rows {
                        identity(row.id)?;
                        if row.header && body {
                            return error(
                                ErrorCode::InvalidField,
                                "table/header",
                                "Repeated table headers must be consecutive initial rows",
                            );
                        }
                        body |= !row.header;
                        if row.cells.len() != columns.len() {
                            return error(
                                ErrorCode::InvalidField,
                                "table/row",
                                "Cell count must match table columns",
                            );
                        }
                        for cell in &row.cells {
                            identity(cell.id)?;
                            cell.style.validate()?;
                            for run in &cell.runs {
                                run.validate()?;
                            }
                        }
                    }
                }
                Block::Image {
                    width, height, alt, ..
                } => {
                    size(*width, *height)?;
                    forge_document::text(alt)?;
                    if alt.is_empty() {
                        return error(
                            ErrorCode::InvalidField,
                            "image/alt",
                            "Image alternate text is required",
                        );
                    }
                }
                Block::Shape {
                    width,
                    height,
                    fill,
                    stroke,
                    alt,
                    ..
                } => {
                    size(*width, *height)?;
                    for color in [fill, stroke].into_iter().flatten() {
                        forge_document::color(color)?;
                    }
                    if let Some(alt) = alt {
                        forge_document::text(alt)?;
                    }
                }
                Block::PageBreak { .. } => {}
            }
        }
    }
    Ok(())
}
fn size(width: f64, height: f64) -> Result<()> {
    if [width, height]
        .iter()
        .any(|v| !v.is_finite() || *v <= 0.0 || *v > 100_000.0)
    {
        return error(
            ErrorCode::InvalidGeometry,
            "size",
            "Dimensions must be positive and at most 100000 points",
        );
    }
    Ok(())
}
