use std::collections::BTreeMap;

use cosmic_text::{Attrs, Buffer, Family, FontSystem, Metrics, Shaping, Weight, fontdb};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::*;

pub const FONT_BYTES: &[u8] = include_bytes!("../assets/fonts/noto-sans-kr/NotoSansKR-VF.ttf");
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PlacedNode {
    pub node_id: Uuid,
    pub frame: Frame,
    pub font_scale: f64,
}
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Layout {
    pub nodes: BTreeMap<Uuid, PlacedNode>,
}
pub struct TextMeasurer {
    fonts: FontSystem,
}
impl Default for TextMeasurer {
    fn default() -> Self {
        let mut db = fontdb::Database::new();
        db.load_system_fonts();
        db.load_font_data(FONT_BYTES.to_vec());
        Self {
            fonts: FontSystem::new_with_locale_and_db("en-US".into(), db),
        }
    }
}
impl TextMeasurer {
    fn check_font(&self, s: &TextStyle) -> Result<()> {
        let family = s.font_family.as_deref().unwrap_or("Noto Sans KR");
        if self
            .fonts
            .db()
            .query(&fontdb::Query {
                families: &[fontdb::Family::Name(family)],
                ..Default::default()
            })
            .is_none()
        {
            return error(
                ErrorCode::FontUnavailable,
                "/style/font_family",
                "Requested font is unavailable",
            );
        }
        Ok(())
    }

    pub fn measure(
        &mut self,
        paragraphs: &[Paragraph],
        base: &TextStyle,
        width: f64,
        scale: f64,
    ) -> Result<(f64, f64)> {
        self.check_font(base)?;
        if width <= 0.0 {
            return error(
                ErrorCode::InvalidGeometry,
                "/width",
                "No width available for text",
            );
        }
        let base_size = base.font_size.unwrap_or(20.0) * scale;
        let mut max_width = 0.0_f64;
        let mut height = 0.0;
        for p in paragraphs {
            let styles: Vec<_> = p
                .runs
                .iter()
                .map(|r| {
                    let mut s = base.clone();
                    s.overlay(&r.style);
                    s
                })
                .collect();
            for s in &styles {
                self.check_font(s)?;
            }
            let attrs = Attrs::new()
                .family(Family::Name(
                    base.font_family.as_deref().unwrap_or("Noto Sans KR"),
                ))
                .weight(Weight(base.font_weight.unwrap_or(400)));
            let mut buffer =
                Buffer::new_empty(Metrics::new(base_size as f32, (base_size * 1.25) as f32));
            buffer.set_size(Some(width as f32), None);
            let spans = p.runs.iter().zip(&styles).map(|(r, s)| {
                let size = s.font_size.unwrap_or(20.0) * scale;
                let a = Attrs::new()
                    .family(Family::Name(
                        s.font_family.as_deref().unwrap_or("Noto Sans KR"),
                    ))
                    .weight(Weight(s.font_weight.unwrap_or(400)))
                    .style(if s.italic.unwrap_or(false) {
                        cosmic_text::Style::Italic
                    } else {
                        cosmic_text::Style::Normal
                    })
                    .metrics(Metrics::new(size as f32, (size * 1.25) as f32));
                (r.text.as_str(), a)
            });
            buffer.set_rich_text(spans, &attrs, Shaping::Advanced, None);
            buffer.shape_until_scroll(&mut self.fonts, false);
            let mut h = 0.0_f64;
            for line in buffer.layout_runs() {
                max_width = max_width.max(f64::from(line.line_w));
                h = h.max(f64::from(line.line_top + line.line_height));
            }
            height += h.max(base_size * 1.25);
        }
        Ok((max_width, height.max(base_size * 1.25)))
    }
}
fn size_of(n: &Node, horizontal: bool) -> Option<Size> {
    if horizontal { n.width } else { n.height }
}
fn dims(n: &Node, parent: NodeKind, horizontal: bool) -> Size {
    size_of(n, horizontal).unwrap_or(Size::Mode(if (parent == NodeKind::Column) == horizontal {
        SizeMode::Fill
    } else {
        SizeMode::Hug
    }))
}
pub fn table_widths(n: &Node, width: f64) -> Result<Vec<f64>> {
    let fixed: f64 = n.columns.iter().filter_map(|c| c.width.fixed()).sum();
    let fill = n.columns.iter().filter(|c| c.width.is_fill()).count();
    if fixed > width + 0.001 || (fill > 0 && fixed >= width) {
        return error(
            ErrorCode::InvalidGeometry,
            "/columns",
            "Table columns exceed available width",
        );
    }
    let equal = (width - fixed) / fill.max(1) as f64;
    Ok(n.columns
        .iter()
        .map(|c| c.width.fixed().unwrap_or(equal))
        .collect())
}
struct Engine<'a> {
    doc: &'a Presentation,
    text: TextMeasurer,
    result: Layout,
    unchanged: std::collections::HashSet<Uuid>,
}
impl Engine<'_> {
    fn intrinsic(&mut self, n: &Node, width: f64, depth: usize) -> Result<(f64, f64)> {
        if depth > 48 {
            return error(ErrorCode::ResourceLimit, "", "Layout depth exceeded");
        }
        if let Some(f) = n.frame {
            return Ok((f.width, f.height));
        }
        let measured = match n.kind {
            NodeKind::Text | NodeKind::List => self.text.measure(
                &n.paragraphs(),
                &self.doc.style(n),
                (width - if n.kind == NodeKind::List { 24.0 } else { 0.0 }).max(1.0),
                1.0,
            )?,
            NodeKind::Row | NodeKind::Column => {
                let horizontal = n.kind == NodeKind::Row;
                let mut main = 0.0_f64;
                let mut cross = 0.0_f64;
                for c in &n.children {
                    if dims(c, n.kind, horizontal).is_fill()
                        && size_of(n, horizontal).is_none_or(|s| s == Size::Mode(SizeMode::Hug))
                    {
                        return error(
                            ErrorCode::LayoutCycle,
                            "/children",
                            "Fill depends on a hugging parent",
                        );
                    }
                    let (w, h) =
                        self.intrinsic(c, (width - 2.0 * n.padding).max(1.0), depth + 1)?;
                    main += if horizontal { w } else { h };
                    cross = cross.max(if horizontal { h } else { w });
                }
                main += n.gap * n.children.len().saturating_sub(1) as f64 + 2.0 * n.padding;
                cross += 2.0 * n.padding;
                if horizontal {
                    (main, cross)
                } else {
                    (cross, main)
                }
            }
            NodeKind::Canvas => {
                let mut w = 0.0_f64;
                let mut h = 0.0_f64;
                for c in &n.children {
                    if let Some(f) = c.frame {
                        w = w.max(f.x + f.width);
                        h = h.max(f.y + f.height);
                    }
                }
                (w, h)
            }
            NodeKind::Image | NodeKind::Shape | NodeKind::Chart => (240.0, 160.0),
            NodeKind::Table => (width, n.rows.len() as f64 * 40.0),
            NodeKind::Connector | NodeKind::Opaque => (1.0, 1.0),
        };
        Ok((
            n.width.and_then(Size::fixed).unwrap_or(measured.0),
            n.height.and_then(Size::fixed).unwrap_or(measured.1),
        ))
    }

    fn place(&mut self, n: &Node, f: Frame) -> Result<()> {
        let id = n.id.ok_or_else(|| {
            Diagnostic::new(
                ErrorCode::InvalidReference,
                "/id",
                "Assign node IDs before layout",
            )
        })?;
        if f.width <= 0.0 || f.height <= 0.0 {
            return error(
                ErrorCode::InvalidGeometry,
                "/frame",
                "No space remains for node",
            );
        }
        let mut scale = 1.0;
        if !self.unchanged.contains(&id) && matches!(n.kind, NodeKind::Text | NodeKind::List) {
            let style = self.doc.style(n);
            let base = style.font_size.unwrap_or(20.0);
            let width = f.width - if n.kind == NodeKind::List { 24.0 } else { 0.0 };
            loop {
                let (w, h) = self.text.measure(&n.paragraphs(), &style, width, scale)?;
                if h <= f.height + 0.01 && w <= width + 0.01 {
                    break;
                }
                let minimum = n.min_font_size.unwrap_or(base) / base;
                if n.overflow != Overflow::Shrink || scale <= minimum + 0.00001 {
                    let mut e = Diagnostic::new(
                        ErrorCode::TextOverflow,
                        "/frame",
                        "Text exceeds its available frame",
                    );
                    e.node_key = n.key.clone();
                    return Err(e);
                }
                scale = (scale - 0.5 / base).max(minimum);
            }
        }
        if !self.unchanged.contains(&id) && n.kind == NodeKind::Table {
            let grid = table_grid(n)?;
            let widths = table_widths(n, f.width)?;
            let row_height = f.height / n.rows.len() as f64;
            for (r, row) in n.rows.iter().enumerate() {
                for (c, cell) in row.cells.iter().enumerate() {
                    let col = grid[r].iter().position(|p| *p == (r, c)).ok_or_else(|| {
                        Diagnostic::new(ErrorCode::InvalidGeometry, "/rows", "Invalid table merge")
                    })?;
                    let width: f64 = widths[col..col + cell.col_span].iter().sum();
                    let mut style = self.doc.style(n);
                    style.overlay(&cell.style);
                    let paragraphs = Node {
                        text: cell.text.clone(),
                        paragraphs: cell.paragraphs.clone(),
                        ..Default::default()
                    }
                    .paragraphs();
                    let (w, h) = self.text.measure(&paragraphs, &style, width - 8.0, 1.0)?;
                    if h > row_height * cell.row_span as f64 - 8.0 || w > width - 8.0 {
                        return error(
                            ErrorCode::TextOverflow,
                            "/rows",
                            "Table cell text exceeds its merged frame",
                        );
                    }
                }
            }
        }
        self.result.nodes.insert(
            id,
            PlacedNode {
                node_id: id,
                frame: f,
                font_scale: scale,
            },
        );
        if n.kind == NodeKind::Canvas {
            for c in &n.children {
                if c.kind == NodeKind::Connector {
                    continue;
                }
                let cf = c.frame.ok_or_else(|| {
                    Diagnostic::new(
                        ErrorCode::InvalidGeometry,
                        "/frame",
                        "Canvas child requires a frame",
                    )
                })?;
                if !c.id.is_some_and(|id| self.unchanged.contains(&id))
                    && (cf.x < 0.0
                        || cf.y < 0.0
                        || cf.x + cf.width > f.width + 0.01
                        || cf.y + cf.height > f.height + 0.01)
                {
                    return error(
                        ErrorCode::InvalidGeometry,
                        "/frame",
                        "Canvas child exceeds its parent",
                    );
                }
                self.place(
                    c,
                    Frame {
                        x: f.x + cf.x,
                        y: f.y + cf.y,
                        ..cf
                    },
                )?;
            }
        } else if matches!(n.kind, NodeKind::Row | NodeKind::Column) {
            let horizontal = n.kind == NodeKind::Row;
            let main = if horizontal { f.width } else { f.height } - 2.0 * n.padding;
            let cross = if horizontal { f.height } else { f.width } - 2.0 * n.padding;
            let mut used = n.gap * n.children.len().saturating_sub(1) as f64;
            let mut fills = 0;
            let mut sizes = Vec::new();
            for c in &n.children {
                let s = dims(c, n.kind, horizontal);
                let m = if let Some(v) = s.fixed() {
                    v
                } else if s.is_fill() {
                    fills += 1;
                    0.0
                } else {
                    let (w, h) = self.intrinsic(c, if horizontal { main } else { cross }, 0)?;
                    if horizontal { w } else { h }
                };
                sizes.push(m);
                used += m;
            }
            if used > main + 0.01 || cross <= 0.0 || (fills > 0 && used >= main) {
                return error(
                    ErrorCode::InvalidGeometry,
                    "/children",
                    "Children exceed container capacity",
                );
            }
            let fill = (main - used) / fills.max(1) as f64;
            let mut cursor = n.padding;
            for (c, m) in n.children.iter().zip(sizes) {
                let m = if dims(c, n.kind, horizontal).is_fill() {
                    fill
                } else {
                    m
                };
                let cs = dims(c, n.kind, !horizontal);
                let csize = if let Some(v) = cs.fixed() {
                    v
                } else if cs.is_fill() {
                    cross
                } else {
                    let (w, h) = self.intrinsic(c, if horizontal { m } else { cross }, 0)?;
                    if horizontal { h } else { w }
                };
                if csize > cross + 0.01 {
                    return error(
                        ErrorCode::InvalidGeometry,
                        "/children",
                        "Child exceeds cross-axis capacity",
                    );
                }
                let cf = if horizontal {
                    Frame {
                        x: f.x + cursor,
                        y: f.y + n.padding,
                        width: m,
                        height: csize,
                    }
                } else {
                    Frame {
                        x: f.x + n.padding,
                        y: f.y + cursor,
                        width: csize,
                        height: m,
                    }
                };
                self.place(c, cf)?;
                cursor += m + n.gap;
            }
        }
        Ok(())
    }
}
pub fn layout(doc: &Presentation) -> Result<Layout> {
    layout_for_edit(doc, None)
}
pub fn layout_for_edit(doc: &Presentation, previous: Option<&Presentation>) -> Result<Layout> {
    validate(doc, true)?;
    let mut unchanged = std::collections::HashSet::new();
    if let Some(previous) = previous {
        for slide in &doc.slides {
            slide.content.visit(&mut |n| {
                if let Some(id) = n.id {
                    if previous.find(&Target {
                        node_id: Some(id),
                        key: None,
                    }) == Some(n)
                    {
                        unchanged.insert(id);
                    }
                }
            });
        }
    }
    let mut engine = Engine {
        doc,
        text: TextMeasurer::default(),
        result: Layout::default(),
        unchanged,
    };
    for slide in &doc.slides {
        engine.place(
            &slide.content,
            Frame {
                x: 0.0,
                y: 0.0,
                width: doc.page.width,
                height: doc.page.height,
            },
        )?;
    }
    for slide in &doc.slides {
        let mut connectors = Vec::new();
        slide.content.visit(&mut |n| {
            if n.kind == NodeKind::Connector {
                connectors.push(n)
            }
        });
        for n in connectors {
            let get = |e: &Endpoint| -> Result<(f64, f64)> {
                let t = slide
                    .content
                    .find(&e.target)
                    .and_then(|n| n.id)
                    .and_then(|id| engine.result.nodes.get(&id))
                    .ok_or_else(|| {
                        Diagnostic::new(
                            ErrorCode::InvalidReference,
                            "/target",
                            "Connector target has no frame",
                        )
                    })?;
                Ok(anchor_point(t.frame, e.anchor))
            };
            let a = get(n.from.as_ref().unwrap())?;
            let b = get(n.to.as_ref().unwrap())?;
            let id = n.id.unwrap();
            engine.result.nodes.insert(
                id,
                PlacedNode {
                    node_id: id,
                    frame: Frame {
                        x: a.0.min(b.0),
                        y: a.1.min(b.1),
                        width: (a.0 - b.0).abs().max(0.01),
                        height: (a.1 - b.1).abs().max(0.01),
                    },
                    font_scale: 1.0,
                },
            );
        }
    }
    Ok(engine.result)
}
pub fn anchor_point(f: Frame, a: Anchor) -> (f64, f64) {
    match a {
        Anchor::Top => (f.x + f.width / 2.0, f.y),
        Anchor::Right => (f.x + f.width, f.y + f.height / 2.0),
        Anchor::Bottom => (f.x + f.width / 2.0, f.y + f.height),
        Anchor::Left => (f.x, f.y + f.height / 2.0),
    }
}
