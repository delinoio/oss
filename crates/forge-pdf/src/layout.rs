use std::{collections::BTreeMap, num::NonZeroU16, sync::Arc};

use forge_document::{
    Run,
    fonts::{Fonts, ShapedText},
};
use forge_tree_doc::{ErrorCode, Result, error};
use krilla::tagging::{ListNumbering, TableHeaderScope, Tag, TagKind};
use serde::Serialize;
use uuid::Uuid;

use crate::*;

#[derive(Debug, Clone, Serialize)]
pub struct Geometry {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
    pub page: usize,
}
#[derive(Debug, Clone, Serialize)]
pub struct Placed {
    pub frame: Geometry,
    pub fragments: Vec<Geometry>,
}
pub struct Layout {
    pub nodes: BTreeMap<Uuid, Placed>,
    pub(crate) pages: Vec<PlannedPage>,
    pub(crate) semantics: Vec<Semantic>,
    pub(crate) roots: Vec<usize>,
}
pub(crate) struct Semantic {
    pub tag: TagKind,
    pub children: Vec<usize>,
}
pub(crate) struct PlannedPage {
    pub width: f64,
    pub height: f64,
    pub commands: Vec<Command>,
}
pub(crate) enum Command {
    Line {
        text: Arc<ShapedText>,
        line: usize,
        x: f64,
        y: f64,
        tags: Vec<usize>,
        artifact: bool,
    },
    Image {
        asset: String,
        frame: Geometry,
        tag: usize,
    },
    Shape {
        kind: ShapeKind,
        frame: Geometry,
        fill: Option<String>,
        stroke: Option<String>,
        tag: Option<usize>,
    },
}
struct Flow<'a> {
    result: Layout,
    page: &'a Page,
    y: f64,
    fonts: &'a mut Fonts,
}
fn overflow<T>() -> Result<T> {
    error(
        ErrorCode::TextOverflow,
        "layout",
        "Indivisible content cannot fit the page content area. Increase page size, reduce margins \
         or content size",
    )
}

pub fn layout(document: &Document, fonts: &mut Fonts) -> Result<Layout> {
    validate(document)?;
    let mut flow = Flow {
        result: Layout {
            nodes: BTreeMap::new(),
            pages: vec![],
            semantics: vec![],
            roots: vec![],
        },
        page: &document.pages[0],
        y: 0.0,
        fonts,
    };
    for page in &document.pages {
        flow.page = page;
        flow.new_page()?;
        flow.place(page.id, flow.frame(0.0, 0.0, page.width, page.height));
        for block in &page.blocks {
            flow.block(block, &document.language)?;
        }
    }
    Ok(flow.result)
}
impl Flow<'_> {
    fn new_page(&mut self) -> Result<()> {
        if self.result.pages.len() >= 20_000 {
            return error(
                ErrorCode::ResourceLimit,
                "pages",
                "Pagination exceeds 20000 pages",
            );
        }
        self.result.pages.push(PlannedPage {
            width: self.page.width,
            height: self.page.height,
            commands: vec![],
        });
        self.y = self.page.margin;
        Ok(())
    }

    fn width(&self) -> f64 {
        self.page.width - 2.0 * self.page.margin
    }

    fn capacity(&self) -> f64 {
        self.page.height - 2.0 * self.page.margin
    }

    fn remaining(&self) -> f64 {
        self.page.height - self.page.margin - self.y
    }

    fn ensure(&mut self, height: f64) -> Result<()> {
        if height > self.capacity() + 0.001 {
            return overflow();
        }
        if height > self.remaining() + 0.001 {
            self.new_page()?;
        }
        Ok(())
    }

    fn frame(&self, x: f64, y: f64, width: f64, height: f64) -> Geometry {
        Geometry {
            x,
            y,
            width,
            height,
            page: self.result.pages.len() - 1,
        }
    }

    fn place(&mut self, id: Uuid, frame: Geometry) {
        self.result
            .nodes
            .entry(id)
            .and_modify(|placed| placed.fragments.push(frame.clone()))
            .or_insert_with(|| Placed {
                frame: frame.clone(),
                fragments: vec![frame],
            });
    }

    fn command(&mut self, command: Command) {
        self.result.pages.last_mut().unwrap().commands.push(command);
    }

    fn tag(&mut self, tag: impl Into<TagKind>, parent: Option<usize>) -> usize {
        let id = self.result.semantics.len();
        self.result.semantics.push(Semantic {
            tag: tag.into(),
            children: vec![],
        });
        if let Some(parent) = parent {
            self.result.semantics[parent].children.push(id);
        } else {
            self.result.roots.push(id);
        }
        id
    }

    fn spans(&mut self, runs: &[Run], parent: usize, language: &str) -> Vec<usize> {
        runs.iter()
            .map(|run| {
                let lang = Some(run.style.language.as_deref().unwrap_or(language).to_owned());
                if run.hyperlink.is_some() {
                    self.tag(Tag::Link.with_lang(lang), Some(parent))
                } else {
                    self.tag(Tag::Span.with_lang(lang), Some(parent))
                }
            })
            .collect()
    }

    fn paragraph(&mut self, id: Uuid, text: Arc<ShapedText>, tags: Vec<usize>) -> Result<()> {
        for (line_index, line) in text.layout.lines().enumerate() {
            let height = f64::from(line.metrics().line_height);
            self.ensure(height)?;
            let width = f64::from(line.metrics().advance);
            if width > self.width() + 0.1 {
                return overflow();
            }
            self.place(
                id,
                self.frame(self.page.margin, self.y, self.width(), height),
            );
            self.command(Command::Line {
                text: text.clone(),
                line: line_index,
                x: self.page.margin,
                y: self.y,
                tags: tags.clone(),
                artifact: false,
            });
            self.y += height;
        }
        self.y += 6.0;
        Ok(())
    }

    fn block(&mut self, block: &Block, language: &str) -> Result<()> {
        match block {
            Block::Paragraph {
                id,
                style,
                heading,
                runs,
            } => {
                let mut style = style.clone();
                if style.language.is_none() {
                    style.language = Some(language.into());
                }
                if heading.is_some() {
                    style.bold = Some(true);
                    if style.font_size.is_none() {
                        style.font_size = Some(24.0 - f64::from(heading.unwrap()) * 2.0);
                    }
                }
                let tag = if let Some(level) = heading {
                    self.tag(
                        Tag::Hn(
                            NonZeroU16::new((*level).into()).unwrap(),
                            Some(runs.iter().map(|r| r.text.as_str()).collect()),
                        ),
                        None,
                    )
                } else {
                    self.tag(Tag::P, None)
                };
                let tags = self.spans(runs, tag, language);
                let text = Arc::new(self.fonts.shape(runs, &style, self.width(), true)?);
                self.paragraph(*id, text, tags)?;
            }
            Block::List { id, ordered, items } => {
                let list = self.tag(
                    Tag::L(if *ordered {
                        ListNumbering::Decimal
                    } else {
                        ListNumbering::Disc
                    }),
                    None,
                );
                for (index, item) in items.iter().enumerate() {
                    let li = self.tag(Tag::LI, Some(list));
                    let label = self.tag(Tag::Lbl, Some(li));
                    let body = self.tag(Tag::LBody, Some(li));
                    let mut runs = vec![Run {
                        text: if *ordered {
                            format!("{}. ", index + 1)
                        } else {
                            "• ".into()
                        },
                        ..Default::default()
                    }];
                    runs.extend(item.runs.clone());
                    let mut tags = vec![label];
                    tags.extend(self.spans(&item.runs, body, language));
                    let text =
                        Arc::new(self.fonts.shape(&runs, &item.style, self.width(), true)?);
                    self.paragraph(item.id, text, tags)?;
                    if let Some(placed) = self.result.nodes.get(&item.id).cloned() {
                        for frame in placed.fragments {
                            self.place(*id, frame);
                        }
                    }
                }
            }
            Block::Image {
                id,
                asset,
                width,
                height,
                alt,
            } => {
                if *width > self.width() {
                    return overflow();
                }
                self.ensure(*height)?;
                let tag = self.tag(Tag::Figure(Some(alt.clone())), None);
                let frame = self.frame(self.page.margin, self.y, *width, *height);
                self.place(*id, frame.clone());
                self.command(Command::Image {
                    asset: asset.clone(),
                    frame,
                    tag,
                });
                self.y += height + 6.0;
            }
            Block::Shape {
                id,
                kind,
                width,
                height,
                fill,
                stroke,
                alt,
            } => {
                if *width > self.width() {
                    return overflow();
                }
                self.ensure(*height)?;
                let tag = alt
                    .as_ref()
                    .map(|alt| self.tag(Tag::Figure(Some(alt.clone())), None));
                let frame = self.frame(self.page.margin, self.y, *width, *height);
                self.place(*id, frame.clone());
                self.command(Command::Shape {
                    kind: *kind,
                    frame,
                    fill: fill.clone(),
                    stroke: stroke.clone(),
                    tag,
                });
                self.y += height + 6.0;
            }
            Block::PageBreak { id } => {
                self.new_page()?;
                self.place(*id, self.frame(self.page.margin, self.y, 0.0, 0.0));
            }
            Block::Table { id, columns, rows } => self.table(*id, columns, rows, language)?,
        }
        Ok(())
    }

    fn table(&mut self, id: Uuid, columns: &[f64], rows: &[Row], language: &str) -> Result<()> {
        if columns.iter().sum::<f64>() > self.width() + 0.001 {
            return overflow();
        }
        let table = self.tag(Tag::Table, None);
        let mut shaped = Vec::new();
        for row in rows {
            let row_tag = self.tag(Tag::TR, Some(table));
            let mut cells = Vec::new();
            for (cell, width) in row.cells.iter().zip(columns) {
                let cell_tag = if row.header {
                    self.tag(Tag::TH(TableHeaderScope::Column), Some(row_tag))
                } else {
                    self.tag(Tag::TD, Some(row_tag))
                };
                let mut style = cell.style.clone();
                if row.header {
                    style.bold = Some(true);
                }
                let text = Arc::new(self.fonts.shape(&cell.runs, &style, width - 8.0, true)?);
                let tags = self.spans(&cell.runs, cell_tag, language);
                cells.push((text, tags));
            }
            shaped.push(cells);
        }
        let header_count = rows.iter().take_while(|row| row.header).count();
        let row_height = |cells: &Vec<(Arc<ShapedText>, Vec<usize>)>| {
            cells
                .iter()
                .map(|(t, _)| f64::from(t.layout.height()))
                .fold(0.0_f64, f64::max)
                + 8.0
        };
        let header_height: f64 = shaped[..header_count].iter().map(row_height).sum();
        if header_height >= self.capacity() && rows.len() > header_count {
            return overflow();
        }
        for (row_index, (row, cells)) in rows.iter().zip(&shaped).enumerate() {
            let mut offsets = vec![0; cells.len()];
            let counts: Vec<_> = cells.iter().map(|(t, _)| t.layout.len()).collect();
            loop {
                let full_height = row_height(cells);
                let mut end = offsets.clone();
                let mut height = 8.0_f64;
                if row.header {
                    if full_height > self.capacity() {
                        return overflow();
                    }
                    // Initial headers stay together with a body line where possible.
                    if row_index == 0 {
                        let body_line = shaped
                            .get(header_count)
                            .map(|cells| {
                                cells
                                    .iter()
                                    .filter_map(|(text, _)| text.layout.lines().next())
                                    .map(|line| f64::from(line.metrics().line_height))
                                    .fold(0.0_f64, f64::max)
                                    + 8.0
                            })
                            .unwrap_or(0.0);
                        self.ensure(header_height + body_line)?;
                    }
                    end.clone_from(&counts);
                    height = full_height;
                } else {
                    for (column, (text, _)) in cells.iter().enumerate() {
                        let mut used = 8.0;
                        for line in text.layout.lines().skip(offsets[column]) {
                            let next = f64::from(line.metrics().line_height);
                            if used + next > self.remaining() + 0.001 {
                                break;
                            }
                            used += next;
                            end[column] += 1;
                        }
                        height = height.max(used);
                    }
                    if end == offsets && (offsets != counts || height > self.remaining() + 0.001) {
                        if (self.y - self.page.margin - header_height).abs() < 0.001
                            || self.y == self.page.margin
                        {
                            return overflow();
                        }
                        self.new_page()?;
                        for (header, header_cells) in
                            rows[..header_count].iter().zip(&shaped[..header_count])
                        {
                            let counts: Vec<_> =
                                header_cells.iter().map(|(t, _)| t.layout.len()).collect();
                            self.table_segment(
                                id,
                                header,
                                columns,
                                header_cells,
                                &vec![0; counts.len()],
                                &counts,
                                row_height(header_cells),
                                true,
                            )?;
                        }
                        continue;
                    }
                }
                self.table_segment(id, row, columns, cells, &offsets, &end, height, false)?;
                offsets = end;
                if offsets == counts {
                    break;
                }
                self.new_page()?;
                for (header, header_cells) in
                    rows[..header_count].iter().zip(&shaped[..header_count])
                {
                    let counts: Vec<_> = header_cells.iter().map(|(t, _)| t.layout.len()).collect();
                    self.table_segment(
                        id,
                        header,
                        columns,
                        header_cells,
                        &vec![0; counts.len()],
                        &counts,
                        row_height(header_cells),
                        true,
                    )?;
                }
            }
        }
        self.y += 6.0;
        Ok(())
    }

    #[allow(clippy::too_many_arguments)]
    fn table_segment(
        &mut self,
        id: Uuid,
        row: &Row,
        columns: &[f64],
        cells: &[(Arc<ShapedText>, Vec<usize>)],
        start: &[usize],
        end: &[usize],
        height: f64,
        artifact: bool,
    ) -> Result<()> {
        if height > self.remaining() + 0.001 {
            return overflow();
        }
        let frame = self.frame(self.page.margin, self.y, columns.iter().sum(), height);
        if !artifact {
            self.place(id, frame.clone());
            self.place(row.id, frame);
        }
        let mut x = self.page.margin;
        for (index, ((text, tags), width)) in cells.iter().zip(columns).enumerate() {
            let frame = self.frame(x, self.y, *width, height);
            if !artifact {
                self.place(row.cells[index].id, frame.clone());
            }
            self.command(Command::Shape {
                kind: ShapeKind::Rectangle,
                frame,
                fill: row.cells[index].style.background.clone(),
                stroke: Some("#888888".into()),
                tag: None,
            });
            let mut y = self.y + 4.0;
            for (line_index, line) in text
                .layout
                .lines()
                .enumerate()
                .take(end[index])
                .skip(start[index])
            {
                self.command(Command::Line {
                    text: text.clone(),
                    line: line_index,
                    x: x + 4.0,
                    y,
                    tags: tags.clone(),
                    artifact,
                });
                y += f64::from(line.metrics().line_height);
            }
            x += width;
        }
        self.y += height;
        Ok(())
    }
}
