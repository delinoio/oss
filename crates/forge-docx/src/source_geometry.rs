//! Source flow constraints, not a replacement for Word pagination or autofit.
use forge_package::W;
use roxmltree::{Document, Node};

fn child<'a, 'i>(node: Node<'a, 'i>, name: &str) -> Option<Node<'a, 'i>> {
    node.children().find(|n| n.has_tag_name((W, name)))
}

fn number(node: Node<'_, '_>, name: &str) -> Option<f64> {
    let value = node.attribute((W, name))?.parse::<f64>().ok()?;
    (value.is_finite() && value >= 0.0).then_some(value)
}

fn positive(value: f64) -> Option<f64> {
    (value.is_finite() && value > 0.0).then_some(value)
}

fn section_width(section: Node<'_, '_>) -> Option<f64> {
    // A source column cannot be identified without paginating its preceding
    // content. Do not invent a page-wide box for a multi-column section.
    if let Some(columns) = child(section, "cols")
        && (columns.attribute((W, "num")).is_some_and(|v| v != "1")
            || columns.children().any(|n| n.has_tag_name((W, "col"))))
    {
        return None;
    }
    let page = child(section, "pgSz")?;
    let margin = child(section, "pgMar")?;
    // Gutter placement may depend on document settings and binding direction.
    if number(margin, "gutter").unwrap_or(0.0) != 0.0 {
        return None;
    }
    positive((number(page, "w")? - number(margin, "left")? - number(margin, "right")?) / 20.0)
}

fn cell_width(cell: Node<'_, '_>, styles: Option<&Document<'_>>) -> Option<f64> {
    let properties = child(cell, "tcPr");
    let row = cell.parent()?;
    let table = row.parent().filter(|n| n.has_tag_name((W, "tbl")))?;
    let table_properties = child(table, "tblPr");
    let width = if let Some(width) = properties.and_then(|p| child(p, "tcW")) {
        if width.attribute((W, "type")).is_some_and(|v| v != "dxa") {
            return None;
        }
        number(width, "w")?
    } else {
        let span = |cell| {
            child(cell, "tcPr")
                .and_then(|p| child(p, "gridSpan"))
                .map(|s| s.attribute((W, "val"))?.parse::<usize>().ok())
                .unwrap_or(Some(1))
        };
        let before = child(row, "trPr")
            .and_then(|p| child(p, "gridBefore"))
            .map(|n| n.attribute((W, "val"))?.parse::<usize>().ok())
            .unwrap_or(Some(0))?;
        let start = row
            .children()
            .take_while(|n| *n != cell)
            .filter(|n| n.has_tag_name((W, "tc")))
            .try_fold(before, |sum, cell| sum.checked_add(span(cell)?))?;
        let grid: Vec<_> = child(table, "tblGrid")?
            .children()
            .filter(|n| n.has_tag_name((W, "gridCol")))
            .collect();
        grid.get(start..start.checked_add(span(cell)?)?)?
            .iter()
            .try_fold(0.0, |sum, n| Some(sum + number(*n, "w")?))?
    };
    let mut margins = vec![
        properties.and_then(|p| child(p, "tcMar")),
        child(row, "tblPrEx").and_then(|p| child(p, "tblCellMar")),
        table_properties.and_then(|p| child(p, "tblCellMar")),
    ];
    if let Some(styles) = styles {
        let selected = table_properties
            .and_then(|p| child(p, "tblStyle"))
            .and_then(|s| s.attribute((W, "val")));
        let mut style = styles.descendants().find(|s| {
            s.has_tag_name((W, "style"))
                && s.attribute((W, "type")) == Some("table")
                && match selected {
                    Some(id) => s.attribute((W, "styleId")) == Some(id),
                    None => matches!(s.attribute((W, "default")), Some("1" | "true" | "on")),
                }
        });
        if selected.is_some() && style.is_none() {
            return None;
        }
        let mut seen = std::collections::HashSet::new();
        while let Some(current) = style {
            if !seen.insert(current.id()) || seen.len() > 128 {
                return None;
            }
            // Conditional table styles require Word's row/banding evaluation.
            if current.descendants().any(|n| {
                n.has_tag_name((W, "tblStylePr"))
                    && n.descendants()
                        .any(|n| matches!(n.tag_name().name(), "tcMar" | "tblCellMar"))
            }) {
                return None;
            }
            margins.push(child(current, "tblPr").and_then(|p| child(p, "tblCellMar")));
            style = if let Some(base) = child(current, "basedOn") {
                let id = base.attribute((W, "val"))?;
                Some(styles.descendants().find(|s| {
                    s.has_tag_name((W, "style")) && s.attribute((W, "styleId")) == Some(id)
                })?)
            } else {
                None
            };
        }
    } else if table_properties
        .and_then(|p| child(p, "tblStyle"))
        .is_some()
    {
        return None;
    }
    let margin = |logical, physical| -> Option<f64> {
        for container in margins.iter().flatten() {
            if let Some(value) = child(*container, logical).or_else(|| child(*container, physical))
            {
                return match value.attribute((W, "type")) {
                    Some("nil") => Some(0.0),
                    None | Some("dxa") => number(value, "w"),
                    _ => None,
                };
            }
        }
        // ECMA-376 start/end table margins default to 115 twips when the
        // complete style hierarchy omits them.
        Some(115.0)
    };
    positive((width - margin("start", "left")? - margin("end", "right")?) / 20.0)
}

pub(crate) struct SourceGeometry<'a, 'i> {
    sections: Vec<(usize, Option<f64>)>,
    styles: Option<&'a Document<'i>>,
    cells: std::collections::HashMap<roxmltree::NodeId, Option<f64>>,
}

impl<'a, 'i> SourceGeometry<'a, 'i> {
    pub(crate) fn new(main: &Document<'_>, styles: Option<&'a Document<'i>>) -> Self {
        let sections = main
            .descendants()
            .filter(|n| {
                n.has_tag_name((W, "sectPr"))
                    && n.parent().is_some_and(|p| {
                        p.has_tag_name((W, "body"))
                            || (p.has_tag_name((W, "pPr"))
                                && p.parent()
                                    .and_then(|p| p.parent())
                                    .is_some_and(|p| p.has_tag_name((W, "body"))))
                    })
            })
            .map(|s| (s.range().start, section_width(s)))
            .collect();
        Self {
            sections,
            styles,
            cells: Default::default(),
        }
    }

    pub(crate) fn available_width(&mut self, node: Node<'_, '_>) -> Option<f64> {
        if let Some(cell) = node.ancestors().find(|n| n.has_tag_name((W, "tc"))) {
            *self
                .cells
                .entry(cell.id())
                .or_insert_with(|| cell_width(cell, self.styles))
        } else if node.ancestors().any(|n| n.has_tag_name((W, "body"))) {
            let top = node
                .ancestors()
                .find(|n| n.parent().is_some_and(|p| p.has_tag_name((W, "body"))))?;
            let index = self
                .sections
                .partition_point(|(start, _)| *start < top.range().start);
            self.sections.get(index)?.1
        } else {
            // Headers/footers may be inherited across sections. A single box
            // is truthful only when all possible section widths agree.
            let width = self.sections.first()?.1?;
            self.sections
                .iter()
                .all(|(_, w)| *w == Some(width))
                .then_some(width)
        }
    }
}
