use std::collections::HashSet;

use crate::*;

fn positive(v: f64) -> bool {
    v.is_finite() && v > 0.0 && v <= 100_000.0
}
fn valid_color(v: &str) -> bool {
    v.len() == 7 && v.starts_with('#') && v[1..].bytes().all(|b| b.is_ascii_hexdigit())
}
fn color(c: &Color, doc: &Presentation, path: &str) -> Result<()> {
    if c.color.is_some() && c.color_ref.is_some() {
        return error(
            ErrorCode::InvalidField,
            path,
            "Color value and reference are mutually exclusive",
        );
    }
    if c.color.as_ref().is_some_and(|v| !valid_color(v))
        || c.color_ref
            .as_ref()
            .is_some_and(|v| !doc.theme.colors.contains_key(v))
    {
        return error(
            ErrorCode::InvalidReference,
            path,
            "Invalid color or unresolved theme color",
        );
    }
    Ok(())
}
fn style(s: &TextStyle, doc: &Presentation, path: &str) -> Result<()> {
    color(
        &Color {
            color: s.color.clone(),
            color_ref: s.color_ref.clone(),
        },
        doc,
        path,
    )?;
    if s.font_size.is_some_and(|v| !positive(v) || v > 1000.0)
        || s.font_weight
            .is_some_and(|v| !(100..=900).contains(&v) || v % 100 != 0)
        || s.font_family
            .as_ref()
            .is_some_and(|s| s.is_empty() || s.len() > 256)
    {
        return error(ErrorCode::InvalidField, path, "Invalid text style");
    }
    Ok(())
}
fn rich(
    text: &Option<String>,
    paragraphs: &[Paragraph],
    doc: &Presentation,
    path: &str,
) -> Result<()> {
    if text.is_some() && !paragraphs.is_empty() {
        return error(
            ErrorCode::InvalidField,
            path,
            "Text and paragraphs are mutually exclusive",
        );
    }
    let valid_text = |s: &str| {
        s.chars().all(|c| {
            c == '\t'
                || c == '\n'
                || c == '\r'
                || (c >= '\u{20}' && c != '\u{fffe}' && c != '\u{ffff}')
        }) && s.len() <= 1024 * 1024
    };
    if text.as_ref().is_some_and(|t| !valid_text(t)) || paragraphs.len() > 10_000 {
        return error(ErrorCode::ResourceLimit, path, "Invalid or excessive text");
    }
    for p in paragraphs {
        for r in &p.runs {
            if !valid_text(&r.text) {
                return error(
                    ErrorCode::InvalidField,
                    path,
                    "Text contains invalid XML characters",
                );
            }
            style(&r.style, doc, path)?;
        }
    }
    Ok(())
}
pub fn validate_target(t: &Target) -> Result<()> {
    if t.key.is_some() == t.node_id.is_some()
        || t.key.as_ref().is_some_and(|s| s.is_empty())
        || t.node_id.is_some_and(|u| u.get_version_num() != 7)
    {
        return error(
            ErrorCode::InvalidReference,
            "/target",
            "Target requires exactly one key or UUID-v7 node_id",
        );
    }
    Ok(())
}
/// Returns the origin cell for every occupied grid slot, including merged
/// slots.
pub fn table_grid(n: &Node) -> Result<Vec<Vec<(usize, usize)>>> {
    let (h, w) = (n.rows.len(), n.columns.len());
    if h == 0 || w == 0 || h > 1000 || w > 128 {
        return error(
            ErrorCode::ResourceLimit,
            "/rows",
            "Table dimensions must be 1..1000 rows and 1..128 columns",
        );
    }
    let mut grid = vec![vec![None; w]; h];
    for (r, row) in n.rows.iter().enumerate() {
        let mut col = 0;
        for (c, cell) in row.cells.iter().enumerate() {
            while col < w && grid[r][col].is_some() {
                col += 1;
            }
            if cell.row_span == 0
                || cell.col_span == 0
                || cell.row_span > h - r
                || cell.col_span > w.saturating_sub(col)
            {
                return error(
                    ErrorCode::InvalidGeometry,
                    "/rows",
                    "Cell span extends outside the table",
                );
            }
            for row in grid.iter_mut().skip(r).take(cell.row_span) {
                for slot in row.iter_mut().skip(col).take(cell.col_span) {
                    if slot.is_some() {
                        return error(ErrorCode::InvalidGeometry, "/rows", "Table merges overlap");
                    }
                    *slot = Some((r, c));
                }
            }
            col += cell.col_span;
        }
    }
    if grid.iter().flatten().any(Option::is_none) {
        return error(
            ErrorCode::InvalidGeometry,
            "/rows",
            "Table cells do not cover the grid",
        );
    }
    Ok(grid
        .into_iter()
        .map(|r| r.into_iter().map(Option::unwrap).collect())
        .collect())
}
pub fn validate(doc: &Presentation, allow_opaque: bool) -> Result<()> {
    if doc.dsl_version != 1 {
        return error(
            ErrorCode::InvalidVersion,
            "/dsl_version",
            "Only DSL version 1 is supported",
        );
    }
    if !positive(doc.page.width) || !positive(doc.page.height) {
        return error(
            ErrorCode::InvalidGeometry,
            "/page",
            "Invalid slide dimensions",
        );
    }
    if doc.slides.is_empty() || doc.slides.len() > 1000 {
        return error(
            ErrorCode::ResourceLimit,
            "/slides",
            "Presentations require 1..1000 slides",
        );
    }
    if doc.theme.font_family.is_empty() {
        return error(
            ErrorCode::InvalidField,
            "/theme/font_family",
            "Font family must not be empty",
        );
    }
    for v in doc.theme.colors.values() {
        if !valid_color(v) {
            return error(
                ErrorCode::InvalidField,
                "/theme/colors",
                "Colors must be #RRGGBB",
            );
        }
    }
    for v in doc.theme.text_styles.values() {
        style(v, doc, "/theme/text_styles")?;
    }
    let mut keys = HashSet::new();
    let mut ids = HashSet::new();
    let mut count = 0;
    for (i, s) in doc.slides.iter().enumerate() {
        let path = format!("/slides/{i}");
        identity(&s.key, s.id, &mut keys, &mut ids, &path)?;
        color(&s.background, doc, &path)?;
        if !s.content.is_container() {
            return error(
                ErrorCode::InvalidField,
                &path,
                "Slide content must be a container",
            );
        }
        if s.content.frame.is_some() || s.content.width.is_some() || s.content.height.is_some() {
            return error(
                ErrorCode::InvalidGeometry,
                &format!("{path}/content"),
                "Slide root geometry is defined by page; frame, width and height are not allowed",
            );
        }
        node(
            &s.content,
            None,
            doc,
            &mut keys,
            &mut ids,
            &mut count,
            0,
            allow_opaque,
            &path,
        )?;
        let mut endpoints = Vec::new();
        s.content.visit(&mut |n| {
            if n.kind == NodeKind::Connector {
                endpoints.extend(n.from.iter().chain(n.to.iter()));
            }
        });
        for e in endpoints {
            validate_target(&e.target)?;
            let target = s.content.find(&e.target).ok_or_else(|| {
                Diagnostic::new(
                    ErrorCode::InvalidReference,
                    &path,
                    "Connector target must exist on the same slide",
                )
            })?;
            if target.is_container()
                || matches!(target.kind, NodeKind::Connector | NodeKind::Opaque)
            {
                return error(
                    ErrorCode::InvalidReference,
                    &path,
                    "Connector target must be a supported leaf shape",
                );
            }
        }
    }
    Ok(())
}
fn identity(
    key: &Option<String>,
    id: Option<uuid::Uuid>,
    keys: &mut HashSet<String>,
    ids: &mut HashSet<uuid::Uuid>,
    path: &str,
) -> Result<()> {
    if let Some(k) = key
        && (k.is_empty() || k.len() > 256 || !keys.insert(k.clone()))
    {
        return error(
            ErrorCode::DuplicateIdentity,
            path,
            "Keys must be nonempty and document-unique",
        );
    }
    if let Some(id) = id
        && (id.get_version_num() != 7 || !ids.insert(id))
    {
        return error(
            ErrorCode::DuplicateIdentity,
            path,
            "IDs must be unique UUID v7 values",
        );
    }
    Ok(())
}
#[allow(clippy::too_many_arguments)]
fn node(
    n: &Node,
    parent: Option<NodeKind>,
    doc: &Presentation,
    keys: &mut HashSet<String>,
    ids: &mut HashSet<uuid::Uuid>,
    count: &mut usize,
    depth: usize,
    allow_opaque: bool,
    path: &str,
) -> Result<()> {
    *count += 1;
    if depth > 48 || *count > 20_000 {
        return error(
            ErrorCode::ResourceLimit,
            path,
            "Tree depth or node count exceeded",
        );
    }
    identity(&n.key, n.id, keys, ids, path)?;
    if n.is_container() && n.placeholder_ref.is_some() {
        return error(
            ErrorCode::InvalidField,
            path,
            "Container nodes cannot reference native placeholders",
        );
    }
    if n.kind == NodeKind::Opaque && (!allow_opaque || n.opaque_ref.is_none()) {
        return error(
            ErrorCode::UnsupportedEdit,
            path,
            "Opaque nodes may only originate from an imported package",
        );
    }
    for s in [n.width, n.height].into_iter().flatten() {
        if s.fixed().is_some_and(|v| !positive(v)) {
            return error(
                ErrorCode::InvalidGeometry,
                path,
                "Size must be positive finite points",
            );
        }
    }
    if n.gap < 0.0 || n.padding < 0.0 || !n.gap.is_finite() || !n.padding.is_finite() {
        return error(
            ErrorCode::InvalidGeometry,
            path,
            "Gap and padding must be finite and nonnegative",
        );
    }
    if let Some(f) = n.frame
        && (!f.x.is_finite()
            || !f.y.is_finite()
            || !positive(f.width)
            || !positive(f.height)
            || n.width.is_some()
            || n.height.is_some())
    {
        return error(
            ErrorCode::InvalidGeometry,
            path,
            "Frame requires finite position and positive size without width/height",
        );
    }
    if n.kind == NodeKind::Canvas && (n.padding != 0.0 || n.gap != 0.0) {
        return error(
            ErrorCode::InvalidField,
            path,
            "Canvas uses explicit child coordinates; spacing belongs to rows and columns",
        );
    }
    if n.kind == NodeKind::Connector
        && (parent != Some(NodeKind::Canvas)
            || n.frame.is_some()
            || n.width.is_some()
            || n.height.is_some())
    {
        return error(
            ErrorCode::InvalidGeometry,
            path,
            "Connectors are canvas children with endpoint-derived geometry",
        );
    }
    if parent == Some(NodeKind::Canvas) && n.frame.is_none() && n.kind != NodeKind::Connector {
        return error(
            ErrorCode::InvalidGeometry,
            path,
            "Canvas children require frames",
        );
    }
    if parent.is_some_and(|p| p != NodeKind::Canvas) && n.frame.is_some() {
        return error(
            ErrorCode::InvalidGeometry,
            path,
            "Frames are only allowed inside a canvas",
        );
    }
    if !n.is_container() && (!n.children.is_empty() || n.gap != 0.0 || n.padding != 0.0) {
        return error(
            ErrorCode::InvalidField,
            path,
            "Only containers accept children, gap, and padding",
        );
    }
    if n.style_ref
        .as_ref()
        .is_some_and(|r| !doc.theme.text_styles.contains_key(r))
    {
        return error(ErrorCode::InvalidReference, path, "Unknown text style");
    }
    style(&n.style, doc, path)?;
    color(&n.fill, doc, path)?;
    rich(&n.text, &n.paragraphs, doc, path)?;
    if n.kind != NodeKind::Text && (n.text.is_some() || !n.paragraphs.is_empty()) {
        return error(
            ErrorCode::InvalidField,
            path,
            "Text content is only allowed on text nodes",
        );
    }
    if n.kind != NodeKind::List && (!n.items.is_empty() || n.marker != Marker::default())
        || n.kind != NodeKind::Image && (n.fit != ImageFit::default() || !n.alt.is_empty())
        || n.kind != NodeKind::Shape && (n.shape != Shape::default() || n.fill != Color::default())
        || n.kind != NodeKind::Chart
            && (n.orientation != Orientation::default()
                || n.legend != Legend::default()
                || n.data_labels != DataLabels::default())
        || n.kind != NodeKind::Connector && n.connector_type != ConnectorType::default()
        || !matches!(n.kind, NodeKind::Text | NodeKind::List | NodeKind::Table)
            && (n.style != TextStyle::default()
                || n.style_ref.is_some()
                || n.min_font_size.is_some()
                || n.overflow != Overflow::default())
        || n.kind != NodeKind::Table && (!n.columns.is_empty() || !n.rows.is_empty())
        || n.kind != NodeKind::Chart && n.data.is_some()
        || n.kind != NodeKind::Image && n.asset_ref.is_some()
        || n.kind != NodeKind::Connector && (n.from.is_some() || n.to.is_some())
        || n.kind != NodeKind::Opaque && n.opaque_ref.is_some()
    {
        return error(
            ErrorCode::InvalidField,
            path,
            "Field is not supported by this node type",
        );
    }
    if n.overflow == Overflow::Shrink && n.min_font_size.is_none()
        || n.min_font_size
            .is_some_and(|v| !positive(v) || v > doc.style(n).font_size.unwrap_or(20.0))
    {
        return error(
            ErrorCode::InvalidField,
            path,
            "Shrink requires a valid minimum font size",
        );
    }
    match n.kind {
        NodeKind::Text if n.text.is_none() && n.paragraphs.is_empty() => {
            return error(
                ErrorCode::InvalidField,
                path,
                "Text node requires text or paragraphs",
            );
        }
        NodeKind::List => {
            for s in &n.items {
                rich(&Some(s.clone()), &[], doc, path)?;
            }
        }
        NodeKind::Image
            if n.asset_ref
                .as_ref()
                .is_none_or(|a| !doc.assets.contains_key(a)) =>
        {
            return error(
                ErrorCode::InvalidReference,
                path,
                "Image requires a registered asset reference",
            );
        }
        NodeKind::Table => {
            table_grid(n)?;
            for c in &n.columns {
                if c.width.fixed().is_some_and(|v| !positive(v))
                    || c.width == Size::Mode(SizeMode::Hug)
                {
                    return error(
                        ErrorCode::InvalidGeometry,
                        path,
                        "Table columns require fixed or fill widths",
                    );
                }
            }
            for c in n.rows.iter().flat_map(|r| &r.cells) {
                rich(&c.text, &c.paragraphs, doc, path)?;
                style(&c.style, doc, path)?;
                color(&c.fill, doc, path)?;
            }
        }
        NodeKind::Chart => {
            let Some(d) = &n.data else {
                return error(ErrorCode::InvalidField, path, "Chart requires data");
            };
            for text in d.categories.iter().chain(d.series.iter().map(|s| &s.name)) {
                rich(&Some(text.clone()), &[], doc, path)?;
            }
            let mut seen = HashSet::new();
            if d.categories.is_empty()
                || d.categories.len() > 10_000
                || d.series.is_empty()
                || d.series.len() > 100
                || d.series.iter().any(|s| {
                    s.values.len() != d.categories.len()
                        || s.values.iter().any(|v| !v.is_finite())
                        || !seen.insert(&s.key)
                })
            {
                return error(
                    ErrorCode::InvalidField,
                    path,
                    "Invalid chart series or category lengths",
                );
            }
        }
        NodeKind::Connector if n.from.is_none() || n.to.is_none() => {
            return error(
                ErrorCode::InvalidReference,
                path,
                "Connector requires two endpoints",
            );
        }
        _ => {}
    }
    for (i, c) in n.children.iter().enumerate() {
        node(
            c,
            Some(n.kind),
            doc,
            keys,
            ids,
            count,
            depth + 1,
            allow_opaque,
            &format!("{path}/children/{i}"),
        )?;
    }
    Ok(())
}
