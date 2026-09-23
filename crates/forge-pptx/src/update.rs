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
        for tag in ["off", "ext"] {
            let a = desc(target, A, tag).ok_or_else(|| failure("transform"))?;
            let b = desc(source, A, tag).ok_or_else(|| failure("transform"))?;
            let raw = &new[b.range()];
            let at = raw.find(' ').unwrap_or(raw.len() - 2);
            let replacement = format!("{} xmlns:a=\"{A}\"{}", &raw[..at], &raw[at..]);
            changes.push((a.range(), replacement));
        }
    } else {
        return error(
            ErrorCode::UnsupportedEdit,
            "",
            "Geometry is inherited or not safely editable",
        );
    }
    apply_edits(bytes, changes)
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
    if let Some(a) = n.attributes().find(|a| a.name() == name) {
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
    n: &Node,
    scale: f64,
) -> Result<Vec<u8>> {
    let old = xml(bytes)?;
    let shape = shape_element(&old, native_id).ok_or_else(|| failure("shape"))?;
    let body = desc(shape, P, "txBody").ok_or_else(|| failure("text"))?;
    let base = doc.style(n);
    let mut edits = Vec::new();
    for run in body.descendants().filter(|r| r.has_tag_name((A, "r"))) {
        if let Some(props) = run.children().find(|p| p.has_tag_name((A, "rPr"))) {
            let mut style = base.clone();
            if let Some(old) = desc(props, A, "hlinkClick") {
                let _ = old;
            }
            // The node style explicitly overrides run properties for this operation.
            style.overlay(&n.style);
            for (name, value) in [
                (
                    "sz",
                    ((style.font_size.unwrap_or(20.0) * scale * 100.0).round() as u32).to_string(),
                ),
                (
                    "b",
                    u8::from(style.font_weight.unwrap_or(400) >= 600).to_string(),
                ),
                ("i", u8::from(style.italic.unwrap_or(false)).to_string()),
                (
                    "u",
                    if style.underline.unwrap_or(false) {
                        "sng"
                    } else {
                        "none"
                    }
                    .into(),
                ),
            ] {
                edits.push(attribute_edit(props, name, &value));
            }
        } else {
            let pos = run
                .children()
                .find(|n| n.is_element())
                .map(|n| n.range().start)
                .unwrap_or(run.range().end - 6);
            edits.push((
                pos..pos,
                emit::rpr(doc, &base, scale)
                    .replace("<a:rPr ", &format!("<a:rPr xmlns:a=\"{A}\" ")),
            ));
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
    apply_edits(
        bytes,
        vec![(attr.range(), format!("{prefix}:embed=\"{}\"", escape(rid)))],
    )
}
fn update_chart(parts: &mut Package, slide: &str, native_id: u32, n: &Node) -> Result<()> {
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
    if old_series.len() != new_series.len() {
        return error(
            ErrorCode::UnsupportedEdit,
            "/data/series",
            "Changing series count in an imported chart requires a new chart node",
        );
    }
    let mut edits = Vec::new();
    for (old, new) in old_series.iter().zip(new_series) {
        for tag in ["tx", "cat", "val"] {
            let a = desc(*old, C, tag).ok_or_else(|| failure("series"))?;
            let b = desc(new, C, tag).ok_or_else(|| failure("series"))?;
            let raw = std::str::from_utf8(&fresh[b.range()]).map_err(failure)?;
            let at = raw.find('>').ok_or_else(|| failure("xml"))?;
            let out = format!("{} xmlns:c=\"{C}\"{}", &raw[..at], &raw[at..]);
            edits.push((a.range(), out));
        }
    }
    let workbook_rel = relationships(parts, &chart_path)?
        .into_iter()
        .find(|r| r.kind.ends_with("/package") && !r.external)
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
    )?;
    parts.insert(workbook_path, updated_workbook);
    parts.insert(chart_path, apply_edits(&old, edits)?);
    Ok(())
}
fn update_workbook(old: &[u8], fresh: &[u8]) -> Result<Vec<u8>> {
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
    let bytes = replace_range(&original[path], old_data.range(), &replacement);
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
    if before == after {
        return Ok(bytes.to_vec());
    }
    validate(after, true)?;
    let placement = layout_for_edit(after, Some(before))?;
    let old_placement = layout_for_edit(before, Some(before))?;
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
        let mut next = bindings
            .values()
            .filter(|b| &b.part == path)
            .map(|b| b.shape_id)
            .max()
            .unwrap_or(1)
            + 1;
        slide.content.visit(&mut |n| {
            if !n.is_container() && !bindings.contains_key(&n.id.unwrap()) {
                bindings.insert(
                    n.id.unwrap(),
                    Binding {
                        part: path.clone(),
                        shape_id: next,
                        parent_shape_id: None,
                    },
                );
                next += 1;
            }
        });
    }
    for (i, (slide, path)) in after.slides.iter().zip(&paths).enumerate() {
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
            let fragment = emit::emit_node(
                &mut parts, path, n, after, &placement, assets, b.shape_id, &bindings,
            )?;
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
                } else if old.style != n.style || old.style_ref != n.style_ref {
                    current = change_style(
                        &current,
                        b.shape_id,
                        after,
                        n,
                        placement.nodes[&id].font_scale,
                    )?;
                }
                if n.kind == NodeKind::Image && old.asset_ref != n.asset_ref {
                    current = replace_image(&current, b.shape_id, &fragment)?;
                }
                if moved {
                    current = change_geometry(&current, b.shape_id, &fragment)?;
                }
                if n.kind == NodeKind::Table && old != *n {
                    let x = xml(&current)?;
                    let shape = shape_element(&x, b.shape_id).ok_or_else(|| failure("table"))?;
                    let table = desc(shape, A, "tbl").ok_or_else(|| failure("table"))?;
                    current = replace_range(
                        &current,
                        table.range(),
                        &emit::table_xml(after, n, placement.nodes[&id].frame)?,
                    );
                }
                parts.insert(path.clone(), current);
                if n.kind == NodeKind::Chart && old.data != n.data {
                    update_chart(&mut parts, path, b.shape_id, n)?;
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
