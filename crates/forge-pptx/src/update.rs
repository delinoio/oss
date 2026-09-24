use std::collections::{BTreeMap, BTreeSet};

use forge_tree_doc::*;
use uuid::Uuid;

use crate::{
    Assets, Binding, add_metadata, emit,
    import::{desc, shape_element, slide_paths},
    package::*,
};

fn apply_edits(bytes: &[u8], mut edits: Vec<(std::ops::Range<usize>, String)>) -> Result<Vec<u8>> {
    edits.sort_by_key(|e| e.0.start);
    if edits.windows(2).any(|w| w[0].0.end > w[1].0.start) {
        return error(
            ErrorCode::UnsupportedEdit,
            "",
            "Overlapping XML edits cannot be preserved",
        );
    }
    let mut out = bytes.to_vec();
    for (range, value) in edits.into_iter().rev() {
        out = replace_range(&out, range, &value);
    }
    xml(&out)?;
    Ok(out)
}
fn new_fragment_root(fragment: &str) -> Result<roxmltree::Document<'_>> {
    xml(fragment.as_bytes())
}
fn change_geometry(bytes: &[u8], native_id: u32, new: &str) -> Result<Vec<u8>> {
    let old = xml(bytes)?;
    let shape = shape_element(&old, native_id).ok_or_else(|| failure("shape"))?;
    let fresh = new_fragment_root(new)?;
    let target = desc(shape, A, "xfrm").or_else(|| desc(shape, P, "xfrm"));
    let source =
        desc(fresh.root_element(), A, "xfrm").or_else(|| desc(fresh.root_element(), P, "xfrm"));
    let mut changes = Vec::new();
    if let (Some(target), Some(source)) = (target, source) {
        for attr in ["flipH", "flipV"] {
            if target.attribute(attr) != source.attribute(attr) {
                changes.push(attribute_edit(
                    target,
                    attr,
                    source.attribute(attr).unwrap_or("0"),
                ));
            }
        }
        for tag in ["off", "ext"] {
            let a = desc(target, A, tag).ok_or_else(|| failure("transform"))?;
            let b = desc(source, A, tag).ok_or_else(|| failure("transform"))?;
            let raw = &new[b.range()];
            let at = raw.find(' ').unwrap_or(raw.len() - 2);
            let replacement = format!("{} xmlns:a=\"{A}\"{}", &raw[..at], &raw[at..]);
            changes.push((a.range(), replacement));
        }
    } else if let (None, Some(source)) = (target, source) {
        let props = desc(shape, P, "spPr").ok_or_else(|| failure("shape geometry"))?;
        let raw = &old.input_text()[props.range()];
        let replacement = format!(
            "<a:xfrm xmlns:a=\"{A}\">{}{}</a:xfrm>",
            &new[desc(source, A, "off")
                .ok_or_else(|| failure("offset"))?
                .range()],
            &new[desc(source, A, "ext")
                .ok_or_else(|| failure("extent"))?
                .range()]
        );
        if raw.ends_with("/>") {
            let prefix = props.lookup_prefix(P).unwrap_or("p");
            changes.push((
                props.range().end - 2..props.range().end,
                format!(">{replacement}</{prefix}:spPr>"),
            ));
        } else {
            let at = props.range().start + raw.find('>').ok_or_else(|| failure("properties"))? + 1;
            changes.push((at..at, replacement));
        }
    } else {
        return error(
            ErrorCode::UnsupportedEdit,
            "",
            "Geometry cannot be represented safely",
        );
    }
    apply_edits(bytes, changes)
}
fn change_shape_properties(
    bytes: &[u8],
    native_id: u32,
    doc: &Presentation,
    old: &Node,
    new: &Node,
) -> Result<Vec<u8>> {
    let parsed = xml(bytes)?;
    let shape = shape_element(&parsed, native_id).ok_or_else(|| failure("shape"))?;
    let properties = shape
        .children()
        .find(|n| n.has_tag_name((P, "spPr")))
        .ok_or_else(|| failure("shape properties"))?;
    let geometries: Vec<_> = properties
        .children()
        .filter(|n| n.has_tag_name((A, "prstGeom")))
        .collect();
    if geometries.len() != 1 {
        return error(
            ErrorCode::UnsupportedEdit,
            "/shape",
            "Shape geometry cannot be replaced safely",
        );
    }
    let geometry = geometries[0];
    let mut edits = Vec::new();
    if old.shape != new.shape {
        let preset = match new.shape {
            Shape::Rect => "rect",
            Shape::RoundedRect => "roundRect",
            Shape::Ellipse => "ellipse",
        };
        edits.push((
            geometry.range(),
            format!("<a:prstGeom xmlns:a=\"{A}\" prst=\"{preset}\"><a:avLst/></a:prstGeom>"),
        ));
    }
    if old.fill != new.fill {
        let fills: Vec<_> = properties
            .children()
            .filter(|n| {
                n.tag_name().namespace() == Some(A)
                    && matches!(
                        n.tag_name().name(),
                        "noFill" | "solidFill" | "gradFill" | "blipFill" | "pattFill" | "grpFill"
                    )
            })
            .collect();
        if fills.len() > 1 {
            return error(
                ErrorCode::UnsupportedEdit,
                "/fill",
                "Ambiguous native shape fills cannot be replaced safely",
            );
        }
        let color = doc.resolve_color(&new.fill, "#2563EB");
        let fill = format!(
            "<a:solidFill xmlns:a=\"{A}\"><a:srgbClr val=\"{}\"/></a:solidFill>",
            &color[1..]
        );
        // Fill follows geometry and precedes line/effect/extension children.
        // Only explicitly changed properties are owned by this edit.
        let range = fills
            .first()
            .map(|n| n.range())
            .unwrap_or(geometry.range().end..geometry.range().end);
        edits.push((range, fill));
    }
    apply_edits(bytes, edits)
}

fn change_text(
    bytes: &[u8],
    native_id: u32,
    doc: &Presentation,
    n: &Node,
    scale: f64,
) -> Result<Vec<u8>> {
    let old = xml(bytes)?;
    let shape = shape_element(&old, native_id).ok_or_else(|| failure("shape"))?;
    let body = desc(shape, P, "txBody").ok_or_else(|| failure("text"))?;
    let ps: Vec<_> = body
        .children()
        .filter(|n| n.has_tag_name((A, "p")))
        .collect();
    if ps.is_empty() {
        return error(
            ErrorCode::UnsupportedEdit,
            "",
            "Text body has no editable paragraphs",
        );
    }
    let generated =
        emit::paragraphs(doc, n, scale).replace("<a:p>", &format!("<a:p xmlns:a=\"{A}\">"));
    let mut changes = vec![(ps[0].range(), generated)];
    for p in ps.iter().skip(1) {
        changes.push((p.range(), String::new()));
    }
    apply_edits(bytes, changes)
}
/// Adds/replaces only an attribute's lexical range, preserving unknown
/// siblings.
fn attribute_edit(
    n: roxmltree::Node<'_, '_>,
    name: &str,
    value: &str,
) -> (std::ops::Range<usize>, String) {
    if let Some(a) = n
        .attributes()
        .find(|a| a.namespace().is_none() && a.name() == name)
    {
        (a.range(), format!("{name}=\"{}\"", escape(value)))
    } else {
        let text = n.document().input_text();
        let start = n.range().start;
        let at = text[start..]
            .find(|c: char| c == '>' || c.is_whitespace() || c == '/')
            .unwrap()
            + start;
        (at..at, format!(" {name}=\"{}\"", escape(value)))
    }
}
fn change_style(
    bytes: &[u8],
    native_id: u32,
    doc: &Presentation,
    previous: &Node,
    n: &Node,
    old_scale: f64,
    scale: f64,
) -> Result<Vec<u8>> {
    let old = xml(bytes)?;
    let shape = shape_element(&old, native_id).ok_or_else(|| failure("shape"))?;
    let body = desc(shape, P, "txBody").ok_or_else(|| failure("text"))?;
    let before = doc.style(previous);
    let base = doc.style(n);
    let model = n.paragraphs();
    let mut edits = Vec::new();
    for (pi, paragraph) in body
        .children()
        .filter(|p| p.has_tag_name((A, "p")))
        .enumerate()
    {
        for (ri, run) in paragraph
            .children()
            .filter(|r| {
                r.has_tag_name((A, "r")) || r.has_tag_name((A, "fld")) || r.has_tag_name((A, "br"))
            })
            .enumerate()
        {
            let mut style = base.clone();
            if let Some(r) = model.get(pi).and_then(|p| p.runs.get(ri)) {
                style.overlay(&r.style);
            }
            let props = run.children().find(|p| p.has_tag_name((A, "rPr")));
            let mut attributes = Vec::new();
            if before.font_size != base.font_size || old_scale != scale {
                attributes.push((
                    "sz",
                    ((style.font_size.unwrap_or(20.0) * scale * 100.0).round() as u32).to_string(),
                ));
            }
            if before.font_weight != base.font_weight {
                attributes.push((
                    "b",
                    u8::from(style.font_weight.unwrap_or(400) >= 600).to_string(),
                ));
            }
            if before.italic != base.italic {
                attributes.push(("i", u8::from(style.italic.unwrap_or(false)).to_string()));
            }
            if before.underline != base.underline {
                attributes.push((
                    "u",
                    if style.underline.unwrap_or(false) {
                        "sng"
                    } else {
                        "none"
                    }
                    .into(),
                ));
            }
            let mut children = Vec::new();
            if before.font_family != base.font_family {
                for tag in ["latin", "ea", "cs"] {
                    children.push((
                        tag,
                        format!(
                            "<a:{tag} xmlns:a=\"{A}\" typeface=\"{}\"/>",
                            escape(style.font_family.as_deref().unwrap_or("Noto Sans KR"))
                        ),
                    ));
                }
            }
            if before.color != base.color || before.color_ref != base.color_ref {
                let color = doc.resolve_color(
                    &Color {
                        color: style.color.clone(),
                        color_ref: style.color_ref.clone(),
                    },
                    "#172033",
                );
                children.push((
                    "solidFill",
                    format!(
                        "<a:solidFill xmlns:a=\"{A}\"><a:srgbClr val=\"{}\"/></a:solidFill>",
                        &color[1..]
                    ),
                ));
            }
            if let Some(props) = props {
                for (name, value) in attributes {
                    edits.push(attribute_edit(props, name, &value));
                }
                let mut additions = String::new();
                for (tag, content) in children {
                    if tag == "solidFill"
                        && props.children().any(|c| {
                            matches!(
                                c.tag_name().name(),
                                "gradFill" | "pattFill" | "blipFill" | "grpFill" | "noFill"
                            )
                        })
                    {
                        return error(
                            ErrorCode::UnsupportedEdit,
                            "/style",
                            "Non-solid text fills require explicit text replacement",
                        );
                    }
                    if let Some(child) = props.children().find(|c| c.has_tag_name((A, tag))) {
                        edits.push((child.range(), content));
                    } else {
                        additions.push_str(&content);
                    }
                }
                if !additions.is_empty() {
                    let raw = &old.input_text()[props.range()];
                    if raw.ends_with("/>") {
                        let prefix = props.lookup_prefix(A).unwrap_or("a");
                        let pos = props.range().end - 2;
                        edits.push((
                            pos..props.range().end,
                            format!(">{additions}</{prefix}:rPr>"),
                        ));
                    } else {
                        let pos = props.range().start
                            + raw.rfind("</").ok_or_else(|| failure("properties"))?;
                        edits.push((pos..pos, additions));
                    }
                }
            } else if !attributes.is_empty() || !children.is_empty() {
                let attributes = attributes
                    .into_iter()
                    .map(|(k, v)| format!(" {k}=\"{}\"", escape(&v)))
                    .collect::<String>();
                let children = children.into_iter().map(|(_, v)| v).collect::<String>();
                let start = run.range().start
                    + old.input_text()[run.range()]
                        .find('>')
                        .ok_or_else(|| failure("run"))?
                    + 1;
                edits.push((
                    start..start,
                    format!("<a:rPr xmlns:a=\"{A}\"{attributes}>{children}</a:rPr>"),
                ));
            }
        }
    }
    apply_edits(bytes, edits)
}
fn replace_image(bytes: &[u8], native_id: u32, new: &str) -> Result<Vec<u8>> {
    let old = xml(bytes)?;
    let shape = shape_element(&old, native_id).ok_or_else(|| failure("image"))?;
    let blip = desc(shape, A, "blip").ok_or_else(|| failure("blip"))?;
    let fresh = xml(new.as_bytes())?;
    let rid = desc(fresh.root_element(), A, "blip")
        .and_then(|n| n.attribute((R, "embed")))
        .ok_or_else(|| failure("embed"))?;
    let attr = blip
        .attributes()
        .find(|a| a.namespace() == Some(R) && a.name() == "embed")
        .ok_or_else(|| failure("embed"))?;
    let prefix = blip.lookup_prefix(R).ok_or_else(|| failure("namespace"))?;
    let mut edits = vec![(attr.range(), format!("{prefix}:embed=\"{}\"", escape(rid)))];
    let old_crop = desc(shape, A, "srcRect");
    let new_crop = desc(fresh.root_element(), A, "srcRect");
    let crop = new_crop
        .map(|c| {
            let raw = &new[c.range()];
            raw.replacen("<a:srcRect", &format!("<a:srcRect xmlns:a=\"{A}\""), 1)
        })
        .unwrap_or_default();
    if let Some(old_crop) = old_crop {
        edits.push((old_crop.range(), crop));
    } else if !crop.is_empty() {
        edits.push((blip.range().end..blip.range().end, crop));
    }
    apply_edits(bytes, edits)
}
fn change_table(
    bytes: &[u8],
    native_id: u32,
    doc: &Presentation,
    before: &Node,
    after: &Node,
) -> Result<Vec<u8>> {
    if before.columns != after.columns
        || before.style != after.style
        || before.style_ref != after.style_ref
    {
        return error(
            ErrorCode::UnsupportedEdit,
            "/table",
            "Replace the table node to change its column or base style definitions",
        );
    }
    let old = xml(bytes)?;
    let table = desc(
        shape_element(&old, native_id).ok_or_else(|| failure("table"))?,
        A,
        "tbl",
    )
    .ok_or_else(|| failure("table"))?;
    let native_rows: Vec<_> = table
        .children()
        .filter(|n| n.has_tag_name((A, "tr")))
        .collect();
    let grid = table_grid(before)?;
    let mut edits = Vec::new();
    for (r, row) in before.rows.iter().enumerate() {
        let cells: Vec<_> = native_rows[r]
            .children()
            .filter(|n| n.has_tag_name((A, "tc")))
            .collect();
        for (c, previous) in row.cells.iter().enumerate() {
            let next = &after.rows[r].cells[c];
            if previous == next {
                continue;
            }
            let mut scrubbed = next.clone();
            scrubbed.text = previous.text.clone();
            scrubbed.paragraphs = previous.paragraphs.clone();
            if scrubbed != *previous {
                return error(
                    ErrorCode::UnsupportedEdit,
                    "/cell",
                    "Cell edits must preserve merges and native formatting",
                );
            }
            let col = grid[r]
                .iter()
                .position(|p| *p == (r, c))
                .ok_or_else(|| failure("cell"))?;
            let body = desc(cells[col], A, "txBody").ok_or_else(|| failure("cell text"))?;
            let paragraphs: Vec<_> = body
                .children()
                .filter(|n| n.has_tag_name((A, "p")))
                .collect();
            let mut style = doc.style(after);
            style.overlay(&next.style);
            let node = Node {
                kind: NodeKind::Text,
                text: next.text.clone(),
                paragraphs: next.paragraphs.clone(),
                style,
                ..Default::default()
            };
            let first = paragraphs.first().ok_or_else(|| failure("paragraph"))?;
            edits.push((
                first.range(),
                emit::paragraphs(doc, &node, 1.0)
                    .replace("<a:p>", &format!("<a:p xmlns:a=\"{A}\">")),
            ));
            edits.extend(
                paragraphs
                    .iter()
                    .skip(1)
                    .map(|p| (p.range(), String::new())),
            );
        }
    }
    apply_edits(bytes, edits)
}
fn update_chart(
    parts: &mut Package,
    slide: &str,
    native_id: u32,
    previous: &Node,
    n: &Node,
) -> Result<()> {
    let doc = xml(&parts[slide])?;
    let shape = shape_element(&doc, native_id).ok_or_else(|| failure("chart"))?;
    let rid = desc(shape, C, "chart")
        .and_then(|n| n.attribute((R, "id")))
        .ok_or_else(|| failure("chart"))?;
    let chart_path = related(parts, slide, rid)?;
    let old = parts[&chart_path].clone();
    let old_doc = xml(&old)?;
    let (fresh, workbook) = emit::chart_parts(n)?;
    let new_doc = xml(&fresh)?;
    let old_bar = desc(old_doc.root_element(), C, "barChart").ok_or_else(|| failure("chart"))?;
    let new_bar = desc(new_doc.root_element(), C, "barChart").ok_or_else(|| failure("chart"))?;
    let old_series: Vec<_> = old_bar
        .children()
        .filter(|n| n.has_tag_name((C, "ser")))
        .collect();
    let new_series: Vec<_> = new_bar
        .children()
        .filter(|n| n.has_tag_name((C, "ser")))
        .collect();
    let mut edits = Vec::new();
    for (old, new) in old_series.iter().zip(&new_series) {
        for tag in ["tx", "cat", "val"] {
            let a = desc(*old, C, tag).ok_or_else(|| failure("series"))?;
            let b = desc(*new, C, tag).ok_or_else(|| failure("series"))?;
            let raw = std::str::from_utf8(&fresh[b.range()]).map_err(failure)?;
            let at = raw.find('>').ok_or_else(|| failure("xml"))?;
            let out = format!("{} xmlns:c=\"{C}\"{}", &raw[..at], &raw[at..]);
            edits.push((a.range(), out));
        }
    }
    for old in old_series.iter().skip(new_series.len()) {
        edits.push((old.range(), String::new()));
    }
    if new_series.len() > old_series.len() {
        let at = old_series
            .last()
            .ok_or_else(|| failure("series"))?
            .range()
            .end;
        let mut added = String::new();
        for new in new_series.iter().skip(old_series.len()) {
            let raw = std::str::from_utf8(&fresh[new.range()]).map_err(failure)?;
            added.push_str(&raw.replacen(
                "<c:ser>",
                &format!("<c:ser xmlns:c=\"{C}\" xmlns:a=\"{A}\">"),
                1,
            ));
        }
        edits.push((at..at, added));
    }
    let (expected_previous, _) = emit::chart_parts(previous)?;
    let expected_previous = xml(&expected_previous)?;
    let expected_formulas: Vec<_> = expected_previous
        .descendants()
        .filter(|n| n.has_tag_name((C, "f")))
        .map(|n| n.text().unwrap_or("").replace("'Sheet1'", "Sheet1"))
        .collect();
    let actual_formulas: Vec<_> = old_bar
        .descendants()
        .filter(|n| n.has_tag_name((C, "f")))
        .map(|n| n.text().unwrap_or("").replace("'Sheet1'", "Sheet1"))
        .collect();
    if actual_formulas != expected_formulas {
        return error(
            ErrorCode::UnsupportedEdit,
            "/data",
            "Chart references a noncanonical workbook range",
        );
    }
    let external_data: Vec<_> = old_doc
        .root_element()
        .children()
        .filter(|n| n.has_tag_name((C, "externalData")))
        .collect();
    if external_data.len() != 1 {
        return error(
            ErrorCode::UnsupportedEdit,
            "/data",
            "Chart must identify exactly one embedded workbook",
        );
    }
    let workbook_id = external_data[0].attribute((R, "id")).ok_or_else(|| {
        Diagnostic::new(
            ErrorCode::UnsupportedEdit,
            "/data",
            "Chart workbook relationship is missing",
        )
    })?;
    let workbook_rel = relationships(parts, &chart_path)?
        .into_iter()
        .find(|r| r.id == workbook_id && r.kind == format!("{R}/package") && !r.external)
        .ok_or_else(|| {
            Diagnostic::new(
                ErrorCode::UnsupportedEdit,
                "",
                "Chart has no editable embedded workbook",
            )
        })?;
    let workbook_path = resolve(&chart_path, &workbook_rel.target)?;
    let updated_workbook = update_workbook(
        parts
            .get(&workbook_path)
            .ok_or_else(|| failure("workbook"))?,
        &workbook,
        previous
            .data
            .as_ref()
            .ok_or_else(|| failure("chart data"))?,
    )?;
    parts.insert(workbook_path, updated_workbook);
    parts.insert(chart_path, apply_edits(&old, edits)?);
    Ok(())
}
fn update_workbook(old: &[u8], fresh: &[u8], previous: &ChartData) -> Result<Vec<u8>> {
    let mut original = read(old)?;
    let generated = read(fresh)?;
    let sheets: Vec<_> = original
        .keys()
        .filter(|p| p.starts_with("xl/worksheets/") && p.ends_with(".xml"))
        .cloned()
        .collect();
    if sheets.len() != 1 {
        return error(
            ErrorCode::UnsupportedEdit,
            "",
            "Multi-sheet chart workbooks require explicit replacement",
        );
    }
    let path = &sheets[0];
    let old_xml = xml(&original[path])?;
    let new_path = generated
        .keys()
        .find(|p| p.starts_with("xl/worksheets/") && p.ends_with(".xml"))
        .ok_or_else(|| failure("worksheet"))?;
    let new_xml = xml(&generated[new_path])?;
    let ns = "http://schemas.openxmlformats.org/spreadsheetml/2006/main";
    let old_data =
        desc(old_xml.root_element(), ns, "sheetData").ok_or_else(|| failure("sheetData"))?;
    let new_data =
        desc(new_xml.root_element(), ns, "sheetData").ok_or_else(|| failure("sheetData"))?;
    if old_data.descendants().any(|n| n.has_tag_name((ns, "f"))) {
        return error(
            ErrorCode::UnsupportedEdit,
            "",
            "Formula-bearing chart workbooks cannot be replaced safely",
        );
    }
    let book = xml(original
        .get("xl/workbook.xml")
        .ok_or_else(|| failure("workbook"))?)?;
    if desc(book.root_element(), ns, "sheet").and_then(|n| n.attribute("name")) != Some("Sheet1") {
        return error(
            ErrorCode::UnsupportedEdit,
            "/data",
            "Noncanonical workbook sheet names require a replacement chart",
        );
    }
    for cell in old_data.descendants().filter(|n| n.has_tag_name((ns, "c"))) {
        let reference = cell.attribute("r").ok_or_else(|| failure("cell"))?;
        let letters = reference
            .bytes()
            .take_while(u8::is_ascii_uppercase)
            .collect::<Vec<_>>();
        let column = letters.iter().fold(0usize, |a, c| {
            a.saturating_mul(26)
                .saturating_add(usize::from(*c - b'A' + 1))
        });
        let row = reference
            .get(letters.len()..)
            .and_then(|r| r.parse::<usize>().ok())
            .ok_or_else(|| failure("cell"))?;
        if column == 0
            || column > previous.series.len() + 1
            || row == 0
            || row > previous.categories.len() + 1
            || cell
                .attributes()
                .any(|a| !matches!(a.name(), "r" | "s" | "t"))
            || cell.children().filter(|n| n.is_element()).any(|n| {
                n.tag_name().namespace() != Some(ns) || !matches!(n.tag_name().name(), "v" | "is")
            })
        {
            return error(
                ErrorCode::UnsupportedEdit,
                "/data",
                "Workbook contains data outside the editable chart range",
            );
        }
    }
    if old_data.children().filter(|n| n.is_element()).any(|n| {
        !n.has_tag_name((ns, "row"))
            || n.attributes()
                .any(|a| a.name() != "r" && a.name() != "spans")
            || n.children()
                .filter(|child| child.is_element())
                .any(|child| !child.has_tag_name((ns, "c")))
    }) {
        return error(
            ErrorCode::UnsupportedEdit,
            "/data",
            "Workbook row extensions require a replacement chart",
        );
    }
    if old_data.attributes().len() != 0
        || old_data.descendants().any(|n| n.is_comment() || n.is_pi())
    {
        return error(
            ErrorCode::UnsupportedEdit,
            "/data",
            "Workbook data extensions require explicit replacement",
        );
    }
    let strings = generated
        .get("xl/sharedStrings.xml")
        .map(|s| {
            xml(s).map(|d| {
                d.root_element()
                    .children()
                    .filter(|n| n.has_tag_name((ns, "si")))
                    .map(|n| {
                        n.descendants()
                            .filter(|n| n.has_tag_name((ns, "t")))
                            .filter_map(|n| n.text())
                            .collect::<String>()
                    })
                    .collect::<Vec<_>>()
            })
        })
        .transpose()?
        .unwrap_or_default();
    let mut rows = String::new();
    for row in new_data.children().filter(|n| n.is_element()) {
        let mut cells = String::new();
        for cell in row.children().filter(|n| n.is_element()) {
            let reference = cell.attribute("r").ok_or_else(|| failure("cell"))?;
            let value = desc(cell, ns, "v").and_then(|n| n.text()).unwrap_or("");
            let style = old_data
                .descendants()
                .find(|n| n.has_tag_name((ns, "c")) && n.attribute("r") == Some(reference))
                .and_then(|n| n.attribute("s"))
                .map(|s| format!(" s=\"{}\"", escape(s)))
                .unwrap_or_default();
            if cell.attribute("t") == Some("s") {
                let i: usize = value.parse().map_err(failure)?;
                let text = strings.get(i).ok_or_else(|| failure("string"))?;
                cells.push_str(&format!(
                    "<c r=\"{}\" t=\"inlineStr\"{style}><is><t \
                     xml:space=\"preserve\">{}</t></is></c>",
                    escape(reference),
                    escape(text)
                ));
            } else {
                cells.push_str(&format!(
                    "<c r=\"{}\"{style}><v>{}</v></c>",
                    escape(reference),
                    escape(value)
                ));
            }
        }
        rows.push_str(&format!(
            "<row r=\"{}\">{cells}</row>",
            row.attribute("r").unwrap_or("1")
        ));
    }
    let replacement = format!("<sheetData xmlns=\"{ns}\">{rows}</sheetData>");
    let mut changes = vec![(old_data.range(), replacement)];
    if let (Some(old_dimension), Some(new_dimension)) = (
        desc(old_xml.root_element(), ns, "dimension"),
        desc(new_xml.root_element(), ns, "dimension"),
    ) {
        changes.push(attribute_edit(
            old_dimension,
            "ref",
            new_dimension
                .attribute("ref")
                .ok_or_else(|| failure("dimensions"))?,
        ));
    }
    let bytes = apply_edits(&original[path], changes)?;
    original.insert(path.clone(), bytes);
    write(&original)
}
#[allow(clippy::too_many_arguments)]
pub fn update(
    bytes: &[u8],
    before: &Presentation,
    old_bindings: &BTreeMap<Uuid, Binding>,
    after: &Presentation,
    assets: &Assets,
    document_id: Uuid,
    revision: u64,
) -> Result<Vec<u8>> {
    update_with_measurer(
        bytes,
        before,
        old_bindings,
        after,
        assets,
        document_id,
        revision,
        &mut TextMeasurer::default(),
    )
}

/// Preserving edit with an operation-owned measurement policy. Unchanged framed
/// source nodes keep native geometry and never require font inspection.
#[allow(clippy::too_many_arguments)]
pub fn update_with_measurer(
    bytes: &[u8],
    before: &Presentation,
    old_bindings: &BTreeMap<Uuid, Binding>,
    after: &Presentation,
    assets: &Assets,
    document_id: Uuid,
    revision: u64,
    text: &mut dyn TextLayout,
) -> Result<Vec<u8>> {
    if before == after {
        return Ok(bytes.to_vec());
    }
    validate(after, true)?;
    let placement = layout_with_measurer(after, Some(before), text)?;
    let old_placement = layout_with_measurer(before, Some(before), text)?;
    let mut parts = read(bytes)?;
    let paths = slide_paths(&parts)?;
    if paths.len() != after.slides.len() {
        return error(
            ErrorCode::UnsupportedEdit,
            "/slides",
            "Slide count changes require a new presentation",
        );
    }
    let mut bindings = old_bindings.clone();
    for (slide, path) in after.slides.iter().zip(&paths) {
        let native = xml(&parts[path])?;
        let mut next = native
            .descendants()
            .filter(|n| n.has_tag_name((P, "cNvPr")))
            .filter_map(|n| n.attribute("id").and_then(|v| v.parse::<u32>().ok()))
            .max()
            .unwrap_or(1);
        let mut needed = 0u32;
        slide.content.visit(&mut |n| {
            if !n.is_container() && !bindings.contains_key(&n.id.unwrap()) {
                needed += 1;
            }
        });
        next.checked_add(needed)
            .ok_or_else(|| failure("shape identifiers exhausted"))?;
        slide.content.visit(&mut |n| {
            if !n.is_container() && !bindings.contains_key(&n.id.unwrap()) {
                next += 1;
                bindings.insert(
                    n.id.unwrap(),
                    Binding {
                        part: path.clone(),
                        shape_id: next,
                        parent_shape_id: None,
                    },
                );
            }
        });
    }
    for (i, (slide, path)) in after.slides.iter().zip(&paths).enumerate() {
        forge_tree_doc::cancellation::checkpoint()?;
        let mut leaves = Vec::new();
        slide.content.visit(&mut |n| {
            if !n.is_container() {
                leaves.push(n)
            }
        });
        let mut old_leaves = Vec::new();
        before.slides[i].content.visit(&mut |n| {
            if !n.is_container() {
                old_leaves.push(n)
            }
        });
        let old_ids: Vec<_> = old_leaves.iter().map(|n| n.id.unwrap()).collect();
        let new_ids: Vec<_> = leaves.iter().map(|n| n.id.unwrap()).collect();
        if old_leaves
            .iter()
            .any(|n| n.kind == NodeKind::Opaque && !new_ids.contains(&n.id.unwrap()))
        {
            return error(
                ErrorCode::UnsupportedEdit,
                "/children",
                "Unsupported native content cannot be removed by replacing its container",
            );
        }
        if new_ids
            .iter()
            .any(|id| old_bindings.get(id).is_some_and(|b| &b.part != path))
        {
            return error(
                ErrorCode::UnsupportedEdit,
                "/target",
                "Moving native shapes across slides is unsupported",
            );
        }
        for n in &leaves {
            forge_tree_doc::cancellation::checkpoint()?;
            let id = n.id.unwrap();
            let b = bindings[&id].clone();
            let old = before.find(&Target {
                node_id: Some(id),
                key: None,
            });
            let moved = old_placement.nodes.get(&id).map(|p| p.frame)
                != placement.nodes.get(&id).map(|p| p.frame);
            if old == Some(*n) && !moved {
                continue;
            }
            if n.kind == NodeKind::Opaque {
                return error(
                    ErrorCode::UnsupportedEdit,
                    "/target",
                    "Unsupported native content cannot be edited or moved",
                );
            }
            // Inserted leaves are emitted once in the drawing-order rewrite
            // below, so their new package parts cannot collide with themselves.
            if old.is_none() {
                continue;
            }
            // Existing chart parts belong to the selective editor below. Build
            // only a frame fragment; change_geometry copies its transform, not
            // its synthetic relationship. No package clone or workbook generation.
            let fragment = if old.is_some() && n.kind == NodeKind::Chart {
                emit::chart_frame(n, b.shape_id, placement.nodes[&id].frame)?
            } else {
                emit::emit_node(
                    &mut parts, path, n, after, &placement, assets, b.shape_id, &bindings,
                )?
            };
            if let Some(old) = old {
                let mut current = parts[path].clone();
                if n.kind != old.kind {
                    return error(
                        ErrorCode::UnsupportedEdit,
                        "/type",
                        "Changing native shape type requires replacement",
                    );
                }
                if old.text != n.text || old.paragraphs != n.paragraphs || old.items != n.items {
                    current = change_text(
                        &current,
                        b.shape_id,
                        after,
                        n,
                        placement.nodes[&id].font_scale,
                    )?;
                } else if n.kind != NodeKind::Table
                    && (old.style != n.style
                        || old.style_ref != n.style_ref
                        || old_placement.nodes[&id].font_scale != placement.nodes[&id].font_scale)
                {
                    current = change_style(
                        &current,
                        b.shape_id,
                        after,
                        old,
                        n,
                        old_placement.nodes[&id].font_scale,
                        placement.nodes[&id].font_scale,
                    )?;
                }
                if n.kind == NodeKind::Shape && (old.shape != n.shape || old.fill != n.fill) {
                    current = change_shape_properties(&current, b.shape_id, after, old, n)?;
                }
                if n.kind == NodeKind::Image && (old.asset_ref != n.asset_ref || moved) {
                    current = replace_image(&current, b.shape_id, &fragment)?;
                }
                if moved || n.kind == NodeKind::Image && old.asset_ref != n.asset_ref {
                    current = change_geometry(&current, b.shape_id, &fragment)?;
                }
                if n.kind == NodeKind::Table
                    && (old.rows != n.rows || old.style != n.style || old.style_ref != n.style_ref)
                {
                    current = change_table(&current, b.shape_id, after, old, n)?;
                }
                parts.insert(path.clone(), current);
                if n.kind == NodeKind::Chart && old.data != n.data {
                    update_chart(&mut parts, path, b.shape_id, old, n)?;
                }
            }
        }
        if old_ids != new_ids {
            let bytes = parts[path].clone();
            let x = xml(&bytes)?;
            let tree = desc(x.root_element(), P, "spTree").ok_or_else(|| failure("tree"))?;
            let mut fragments = BTreeMap::new();
            for id in &old_ids {
                let b = &old_bindings[id];
                let shape = shape_element(&x, b.shape_id).ok_or_else(|| failure("shape"))?;
                fragments.insert(
                    *id,
                    std::str::from_utf8(&bytes[shape.range()])
                        .map_err(failure)?
                        .to_string(),
                );
            }
            let mut text = String::new();
            for n in &leaves {
                forge_tree_doc::cancellation::checkpoint()?;
                let id = n.id.unwrap();
                if let Some(raw) = fragments.get(&id) {
                    text.push_str(raw);
                } else {
                    text.push_str(&emit::emit_node(
                        &mut parts,
                        path,
                        n,
                        after,
                        &placement,
                        assets,
                        bindings[&id].shape_id,
                        &bindings,
                    )?);
                }
            }
            let shapes: Vec<_> = tree
                .children()
                .filter(|n| {
                    n.is_element()
                        && !n.has_tag_name((P, "nvGrpSpPr"))
                        && !n.has_tag_name((P, "grpSpPr"))
                })
                .collect();
            if shapes.iter().any(|shape| {
                !old_ids.iter().any(|id| {
                    shape_element(&x, old_bindings[id].shape_id)
                        .is_some_and(|known| known == *shape)
                })
            }) {
                return error(
                    ErrorCode::UnsupportedEdit,
                    "/children",
                    "Unbound native elements prevent safe drawing-order changes",
                );
            }
            let mut edits = Vec::new();
            if let Some(first) = shapes.first() {
                edits.push((first.range(), text));
                for s in shapes.iter().skip(1) {
                    edits.push((s.range(), String::new()));
                }
            } else {
                let end = tree.range().end;
                let raw = std::str::from_utf8(&bytes).map_err(failure)?;
                let pos = raw[..end].rfind("</").ok_or_else(|| failure("tree"))?;
                edits.push((pos..pos, text));
            }
            parts.insert(path.clone(), apply_edits(&bytes, edits)?);
        }
    }
    let mut keep = BTreeSet::new();
    for s in &after.slides {
        s.content.visit(&mut |n| {
            if let Some(id) = n.id {
                keep.insert(id);
            }
        });
    }
    bindings.retain(|id, _| keep.contains(id));
    add_metadata(&mut parts, document_id, revision, after, &bindings)?;
    validate_package(&parts)?;
    write(&parts)
}
