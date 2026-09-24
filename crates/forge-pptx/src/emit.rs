use std::{collections::BTreeMap, io::Cursor};

use forge_tree_doc::*;
use pptx::{
    ShapeTree,
    chart::{data::CategoryChartData, xmlwriter::ChartXmlWriter},
    enums::chart::XlChartType,
    units::{Emu, ShapeId},
};
use uuid::Uuid;

use crate::{Assets, Binding, add_metadata, package::*};
pub(crate) fn emu(v: f64) -> Emu {
    Emu((v * 12700.0).round() as i64)
}
fn ns(mut fragment: String) -> String {
    for (prefix, uri) in [("p", P), ("a", A), ("r", R), ("c", C)] {
        let end = fragment.find('>').unwrap();
        if !fragment[..end].contains(&format!("xmlns:{prefix}=")) {
            let at = fragment.find(' ').or_else(|| fragment.find('>')).unwrap();
            fragment.insert_str(at, &format!(" xmlns:{prefix}=\"{uri}\""));
        }
    }
    fragment
}
pub(crate) fn rpr(doc: &Presentation, s: &TextStyle, scale: f64) -> String {
    let color = doc.resolve_color(
        &Color {
            color: s.color.clone(),
            color_ref: s.color_ref.clone(),
        },
        "#172033",
    );
    let font = escape(s.font_family.as_deref().unwrap_or("Noto Sans KR"));
    let sz = (s.font_size.unwrap_or(20.0) * scale * 100.0).round() as u32;
    format!(
        "<a:rPr sz=\"{sz}\" b=\"{}\" i=\"{}\" u=\"{}\"><a:solidFill><a:srgbClr \
         val=\"{}\"/></a:solidFill><a:latin typeface=\"{font}\"/><a:ea typeface=\"{font}\"/><a:cs \
         typeface=\"{font}\"/></a:rPr>",
        u8::from(s.font_weight.unwrap_or(400) >= 600),
        u8::from(s.italic.unwrap_or(false)),
        if s.underline.unwrap_or(false) {
            "sng"
        } else {
            "none"
        },
        &color[1..]
    )
}
pub(crate) fn paragraphs(doc: &Presentation, n: &Node, scale: f64) -> String {
    let base = doc.style(n);
    let mut out = String::new();
    for p in n.paragraphs() {
        let align = match p.align {
            Align::Left => "l",
            Align::Center => "ctr",
            Align::Right => "r",
            Align::Justify => "just",
        };
        let bullet = if n.kind == NodeKind::List {
            match n.marker {
                Marker::Bullet => "<a:buChar char=\"•\"/>",
                Marker::Number => "<a:buAutoNum type=\"arabicPeriod\"/>",
            }
        } else {
            "<a:buNone/>"
        };
        let indent = if n.kind == NodeKind::List {
            " marL=\"304800\" indent=\"-152400\""
        } else {
            ""
        };
        out.push_str(&format!(
            "<a:p><a:pPr algn=\"{align}\"{indent}><a:lnSpc><a:spcPct \
             val=\"125000\"/></a:lnSpc>{bullet}</a:pPr>"
        ));
        for r in p.runs {
            let mut s = base.clone();
            s.overlay(&r.style);
            out.push_str(&format!(
                "<a:r>{}<a:t xml:space=\"preserve\">{}</a:t></a:r>",
                rpr(doc, &s, scale),
                escape(&r.text)
            ));
        }
        out.push_str(&format!(
            "<a:endParaRPr sz=\"{}\"/></a:p>",
            (base.font_size.unwrap_or(20.0) * scale * 100.0).round() as u32
        ));
    }
    if out.is_empty() {
        out.push_str("<a:p/>");
    }
    out
}
pub(crate) fn text_body(doc: &Presentation, n: &Node, scale: f64, prefix: &str) -> String {
    format!(
        "<{prefix}:txBody xmlns:a=\"{A}\" xmlns:p=\"{P}\"><a:bodyPr wrap=\"square\" lIns=\"0\" \
         rIns=\"0\" tIns=\"0\" bIns=\"0\" \
         anchor=\"t\"><a:noAutofit/></a:bodyPr><a:lstStyle/>{}</{prefix}:txBody>",
        paragraphs(doc, n, scale)
    )
}
fn swap_descendant(
    fragment: &str,
    namespace: &str,
    tag: &str,
    replacement: &str,
) -> Result<String> {
    let doc = xml(fragment.as_bytes())?;
    let n = doc
        .descendants()
        .find(|n| n.has_tag_name((namespace, tag)))
        .ok_or_else(|| failure("element"))?;
    String::from_utf8(replace_range(fragment.as_bytes(), n.range(), replacement)).map_err(failure)
}
fn filled_shape(fragment: &str, color: &str) -> Result<String> {
    let doc = xml(fragment.as_bytes())?;
    let sp = doc
        .descendants()
        .find(|n| n.has_tag_name((P, "spPr")))
        .ok_or_else(|| failure("shape"))?;
    let raw = &fragment[sp.range()];
    let raw = raw.replacen(
        "<p:spPr",
        &format!("<p:spPr xmlns:p=\"{P}\" xmlns:a=\"{A}\""),
        1,
    );
    let filled = insert_before_close(
        raw.as_bytes(),
        &format!(
            "<a:solidFill xmlns:a=\"{A}\"><a:srgbClr val=\"{}\"/></a:solidFill><a:ln \
             xmlns:a=\"{A}\"><a:noFill/></a:ln>",
            &color[1..]
        ),
    )?;
    String::from_utf8(replace_range(
        fragment.as_bytes(),
        sp.range(),
        std::str::from_utf8(&filled).map_err(failure)?,
    ))
    .map_err(failure)
}
pub(crate) fn image_info(data: &[u8]) -> Result<(u32, u32, &'static str)> {
    if data.len() > 64 * 1024 * 1024 {
        return error(ErrorCode::ResourceLimit, "/asset", "Image exceeds 64 MiB");
    }
    let reader = image::ImageReader::new(Cursor::new(data))
        .with_guessed_format()
        .map_err(failure)?;
    let ext = match reader.format() {
        Some(image::ImageFormat::Png) => "png",
        Some(image::ImageFormat::Jpeg) => "jpg",
        _ => {
            return error(
                ErrorCode::InvalidField,
                "/asset",
                "Only PNG and JPEG images are supported",
            );
        }
    };
    let (w, h) = reader.into_dimensions().map_err(failure)?;
    if w == 0 || h == 0 || u64::from(w) * u64::from(h) > 64_000_000 {
        return error(
            ErrorCode::ResourceLimit,
            "/asset",
            "Image dimensions exceed 64 million pixels",
        );
    }
    let mut reader = image::ImageReader::new(Cursor::new(data))
        .with_guessed_format()
        .map_err(failure)?;
    let mut limits = image::Limits::default();
    limits.max_alloc = Some(256 * 1024 * 1024);
    reader.limits(limits);
    reader.decode().map_err(failure)?;
    Ok((w, h, ext))
}
pub(crate) fn chart_parts(n: &Node) -> Result<(Vec<u8>, Vec<u8>)> {
    let data = n.data.as_ref().ok_or_else(|| failure("chart"))?;
    let mut source = CategoryChartData::new();
    for c in &data.categories {
        source.add_category(c);
    }
    for s in &data.series {
        source.add_series(&s.name, &s.values);
    }
    let mut chart = ChartXmlWriter::write_category(
        &source,
        if n.orientation == Orientation::Horizontal {
            XlChartType::BarClustered
        } else {
            XlChartType::ColumnClustered
        },
    )
    .map_err(failure)?;
    // pptx 0.1.0's category writer repeats B-column formulas for every series.
    // Repair the references to match generate_category_xlsx until upstream fixes
    // that writer; caches alone cannot prove an editable chart is correct.
    let native = xml(chart.as_bytes())?;
    let mut edits = Vec::new();
    for (index, series) in native
        .descendants()
        .filter(|n| n.has_tag_name((C, "ser")))
        .enumerate()
    {
        let mut column = index + 2;
        let mut letters = Vec::new();
        while column != 0 {
            column -= 1;
            letters.push((b'A' + (column % 26) as u8) as char);
            column /= 26;
        }
        let column: String = letters.into_iter().rev().collect();
        for (tag, formula) in [
            ("tx", format!("Sheet1!${column}$1")),
            (
                "cat",
                format!("Sheet1!$A$2:$A${}", data.categories.len() + 1),
            ),
            (
                "val",
                format!("Sheet1!${column}$2:${column}${}", data.categories.len() + 1),
            ),
        ] {
            if let Some(f) = series
                .children()
                .find(|n| n.has_tag_name((C, tag)))
                .and_then(|n| n.descendants().find(|n| n.has_tag_name((C, "f"))))
            {
                edits.push((f.range(), format!("<c:f>{formula}</c:f>")));
            }
        }
    }
    for (range, value) in edits.into_iter().rev() {
        chart =
            String::from_utf8(replace_range(chart.as_bytes(), range, &value)).map_err(failure)?;
    }
    // The upstream bar writer omits legends and uses signed axis identifiers.
    // Normalize those two details until the writer emits conforming bar-chart
    // defaults.
    chart = chart
        .replace("-2068027336", "100001")
        .replace("-2113994440", "100002");
    let native = xml(chart.as_bytes())?;
    let replacement = match n.legend {
        Legend::Hidden => String::new(),
        Legend::Bottom | Legend::Right => format!(
            "<c:legend><c:legendPos val=\"{}\"/><c:overlay val=\"0\"/></c:legend>",
            if n.legend == Legend::Bottom { "b" } else { "r" }
        ),
    };
    if let Some(legend) = native.descendants().find(|e| e.has_tag_name((C, "legend"))) {
        chart = String::from_utf8(replace_range(
            chart.as_bytes(),
            legend.range(),
            &replacement,
        ))
        .map_err(failure)?;
    } else if !replacement.is_empty() {
        let plot = native
            .descendants()
            .find(|e| e.has_tag_name((C, "plotArea")))
            .ok_or_else(|| failure("plot area"))?;
        chart.insert_str(plot.range().end, &replacement);
    }
    let doc = xml(chart.as_bytes())?;
    if n.data_labels == DataLabels::Value {
        let bar = doc
            .descendants()
            .find(|e| e.has_tag_name((C, "barChart")))
            .ok_or_else(|| failure("bar"))?;
        let end = bar
            .children()
            .find(|e| e.has_tag_name((C, "gapWidth")) || e.has_tag_name((C, "axId")))
            .map(|e| e.range().start)
            .unwrap_or(bar.range().end - 13);
        chart.insert_str(
            end,
            "<c:dLbls><c:showLegendKey val=\"0\"/><c:showVal val=\"1\"/><c:showCatName \
             val=\"0\"/><c:showSerName val=\"0\"/></c:dLbls>",
        );
    }
    let external = format!(
        "<c:externalData xmlns:c=\"{C}\" xmlns:r=\"{R}\" r:id=\"rIdWorkbook\"><c:autoUpdate \
         val=\"0\"/></c:externalData>"
    );
    let bytes = insert_before_close(chart.as_bytes(), &external)?;
    let workbook = pptx::chart::xlsx::generate_category_xlsx(&source).map_err(failure)?;
    Ok((bytes, workbook))
}
pub(crate) fn chart_frame(n: &Node, native_id: u32, frame: Frame) -> Result<String> {
    let id = n.id.ok_or_else(|| failure("id"))?;
    Ok(ns(ShapeTree::new_chart_graphic_frame_xml(
        ShapeId(native_id),
        &n.key.clone().unwrap_or_else(|| id.to_string()),
        &format!("rIdForge{}", id.simple()),
        emu(frame.x),
        emu(frame.y),
        emu(frame.width),
        emu(frame.height),
    )))
}

#[allow(clippy::too_many_arguments)]
pub(crate) fn emit_node(
    parts: &mut Package,
    slide_part: &str,
    n: &Node,
    doc: &Presentation,
    placement: &Layout,
    assets: &Assets,
    native_id: u32,
    bindings: &BTreeMap<Uuid, Binding>,
) -> Result<String> {
    let id = n.id.ok_or_else(|| failure("id"))?;
    let placed = placement.nodes.get(&id).ok_or_else(|| failure("layout"))?;
    let mut f = placed.frame;
    let name = n.key.clone().unwrap_or_else(|| id.to_string());
    let sid = ShapeId(native_id);
    let fragment = match n.kind {
        NodeKind::Text | NodeKind::List => {
            let s = ns(ShapeTree::new_textbox_xml(
                sid,
                &name,
                emu(f.x),
                emu(f.y),
                emu(f.width),
                emu(f.height),
            ));
            swap_descendant(&s, P, "txBody", &text_body(doc, n, placed.font_scale, "p"))?
        }
        NodeKind::Shape => {
            let geometry = match n.shape {
                Shape::Rect => "rect",
                Shape::RoundedRect => "roundRect",
                Shape::Ellipse => "ellipse",
            };
            let s = ns(ShapeTree::new_autoshape_xml(
                sid,
                &name,
                emu(f.x),
                emu(f.y),
                emu(f.width),
                emu(f.height),
                geometry,
            ));
            filled_shape(&s, &doc.resolve_color(&n.fill, "#2563EB"))?
        }
        NodeKind::Image => {
            let reference = n
                .asset_ref
                .as_ref()
                .and_then(|key| doc.assets.get(key))
                .ok_or_else(|| failure("asset"))?;
            let bytes = assets.get(&reference.handle).ok_or_else(|| {
                Diagnostic::new(
                    ErrorCode::NotFound,
                    "/asset_ref",
                    "Registered image bytes are unavailable",
                )
            })?;
            let (w, h, ext) = image_info(bytes)?;
            let part = format!("ppt/media/forge-{}.{}", sha(bytes), ext);
            parts.entry(part.clone()).or_insert_with(|| bytes.clone());
            add_content_type(
                parts,
                &part,
                if ext == "png" {
                    "image/png"
                } else {
                    "image/jpeg"
                },
            )?;
            let rid = format!("rIdForge{}{}", id.simple(), sha(bytes));
            ensure_relationship(
                parts,
                slide_part,
                &rid,
                &format!("{R}/image"),
                &format!("/{part}"),
            )?;
            let ratio = f64::from(w) / f64::from(h);
            let mut crop = String::new();
            if n.fit == ImageFit::Contain {
                let scale = (f.width / f64::from(w)).min(f.height / f64::from(h));
                let nw = f64::from(w) * scale;
                let nh = f64::from(h) * scale;
                f.x += (f.width - nw) / 2.0;
                f.y += (f.height - nh) / 2.0;
                f.width = nw;
                f.height = nh;
            } else if ratio > f.width / f.height {
                let c = ((1.0 - f.width / f.height / ratio) * 50000.0).round() as u32;
                crop = format!("<a:srcRect l=\"{c}\" r=\"{c}\"/>");
            } else {
                let c = ((1.0 - ratio / (f.width / f.height)) * 50000.0).round() as u32;
                crop = format!("<a:srcRect t=\"{c}\" b=\"{c}\"/>");
            }
            let s = ns(ShapeTree::new_picture_xml(
                sid,
                &name,
                &n.alt,
                &rid,
                emu(f.x),
                emu(f.y),
                emu(f.width),
                emu(f.height),
            ));
            if crop.is_empty() {
                s
            } else {
                s.replace("<a:stretch>", &format!("{crop}<a:stretch>"))
            }
        }
        NodeKind::Chart => {
            let stem = format!("ppt/charts/forge-{}", id.simple());
            let chart_path = format!("{stem}.xml");
            let workbook_path = format!("{stem}.xlsx");
            let (chart, workbook) = chart_parts(n)?;
            parts.insert(chart_path.clone(), chart);
            parts.insert(workbook_path.clone(), workbook);
            add_content_type(
                parts,
                &chart_path,
                "application/vnd.openxmlformats-officedocument.drawingml.chart+xml",
            )?;
            add_content_type(
                parts,
                &workbook_path,
                "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
            )?;
            ensure_relationship(
                parts,
                &chart_path,
                "rIdWorkbook",
                &format!("{R}/package"),
                &format!("/{workbook_path}"),
            )?;
            let rid = format!("rIdForge{}", id.simple());
            ensure_relationship(
                parts,
                slide_part,
                &rid,
                &format!("{R}/chart"),
                &format!("/{chart_path}"),
            )?;
            chart_frame(n, native_id, f)?
        }
        NodeKind::Table => {
            let s = ns(ShapeTree::new_table_xml(
                sid,
                &name,
                n.rows.len() as u32,
                n.columns.len() as u32,
                emu(f.x),
                emu(f.y),
                emu(f.width),
                emu(f.height),
            ));
            swap_descendant(&s, A, "tbl", &table_xml(doc, n, f)?)?
        }
        NodeKind::Connector => {
            let endpoint = |e: &Endpoint| -> Result<(&Binding, (f64, f64))> {
                let target = doc
                    .find(&e.target)
                    .and_then(|n| n.id)
                    .ok_or_else(|| failure("target"))?;
                let b = bindings.get(&target).ok_or_else(|| failure("binding"))?;
                let f = placement
                    .nodes
                    .get(&target)
                    .ok_or_else(|| failure("placement"))?
                    .frame;
                Ok((b, anchor_point(f, e.anchor)))
            };
            let (a, ap) = endpoint(n.from.as_ref().unwrap())?;
            let (b, bp) = endpoint(n.to.as_ref().unwrap())?;
            let s = ns(ShapeTree::new_connector_xml_with_flip(
                sid,
                &name,
                emu(f.x),
                emu(f.y),
                emu(f.width),
                emu(f.height),
                if n.connector_type == ConnectorType::Elbow {
                    "bentConnector3"
                } else {
                    "line"
                },
                ap.0 > bp.0,
                ap.1 > bp.1,
            ));
            let s = s.replace(
                "</p:spPr>",
                "<a:ln w=\"19050\"><a:solidFill><a:srgbClr \
                 val=\"172033\"/></a:solidFill><a:prstDash val=\"solid\"/></a:ln></p:spPr>",
            );
            let anchor = |e: &Endpoint| {
                let cardinal = match e.anchor {
                    Anchor::Top => 0,
                    Anchor::Left => 1,
                    Anchor::Bottom => 2,
                    Anchor::Right => 3,
                };
                // DrawingML ellipse exposes eight connection sites; the cardinal
                // sites are the even indices, unlike rectangle's four sites.
                if doc
                    .find(&e.target)
                    .is_some_and(|n| n.kind == NodeKind::Shape && n.shape == Shape::Ellipse)
                {
                    cardinal * 2
                } else {
                    cardinal
                }
            };
            swap_descendant(
                &s,
                P,
                "cNvCxnSpPr",
                &format!(
                    "<p:cNvCxnSpPr xmlns:p=\"{P}\" xmlns:a=\"{A}\"><a:stCxn id=\"{}\" \
                     idx=\"{}\"/><a:endCxn id=\"{}\" idx=\"{}\"/></p:cNvCxnSpPr>",
                    a.shape_id,
                    anchor(n.from.as_ref().unwrap()),
                    b.shape_id,
                    anchor(n.to.as_ref().unwrap())
                ),
            )?
        }
        _ => {
            return error(
                ErrorCode::UnsupportedEdit,
                "/type",
                "Node cannot be emitted as a leaf",
            );
        }
    };
    let fragment = if let Some(reference) = &n.placeholder_ref {
        let relation = relationships(parts, slide_part)?
            .into_iter()
            .find(|r| r.kind.ends_with("/slideLayout") && !r.external)
            .ok_or_else(|| failure("layout"))?;
        let layout = resolve(slide_part, &relation.target)?;
        let native = xml(parts.get(&layout).ok_or_else(|| failure("layout"))?)?;
        let ph = native
            .descendants()
            .find(|p| p.has_tag_name((P, "ph")) && p.attribute("idx").unwrap_or("0") == reference)
            .ok_or_else(|| {
                Diagnostic::new(
                    ErrorCode::InvalidReference,
                    "/placeholder_ref",
                    "Placeholder is unavailable in this slide layout",
                )
            })?;
        if !matches!(n.kind, NodeKind::Text | NodeKind::Image | NodeKind::Shape) {
            return error(
                ErrorCode::UnsupportedEdit,
                "/placeholder_ref",
                "Placeholder binding requires text, image or shape",
            );
        }
        let attrs = ph
            .attributes()
            .filter(|a| a.namespace().is_none())
            .map(|a| format!(" {}=\"{}\"", a.name(), escape(a.value())))
            .collect::<String>();
        swap_descendant(
            &fragment,
            P,
            "nvPr",
            &format!("<p:nvPr xmlns:p=\"{P}\"><p:ph{attrs}/></p:nvPr>"),
        )?
    } else {
        fragment
    };
    xml(fragment.as_bytes())?;
    Ok(fragment)
}
pub(crate) fn table_xml(doc: &Presentation, n: &Node, f: Frame) -> Result<String> {
    let widths = table_widths(n, f.width)?;
    let grid = table_grid(n)?;
    let mut out = format!("<a:tbl xmlns:a=\"{A}\"><a:tblPr/><a:tblGrid>");
    for w in widths {
        out.push_str(&format!("<a:gridCol w=\"{}\"/>", emu(w).0));
    }
    out.push_str("</a:tblGrid>");
    for (r, row) in grid.iter().enumerate() {
        out.push_str(&format!(
            "<a:tr h=\"{}\">",
            emu(f.height / n.rows.len() as f64).0
        ));
        for (c, &(or, oc)) in row.iter().enumerate() {
            let cell = &n.rows[or].cells[oc];
            let first = grid[or].iter().position(|p| *p == (or, oc)).unwrap();
            let origin = r == or && c == first;
            let mut attrs = String::new();
            if origin {
                if cell.col_span > 1 {
                    attrs.push_str(&format!(" gridSpan=\"{}\"", cell.col_span));
                }
                if cell.row_span > 1 {
                    attrs.push_str(&format!(" rowSpan=\"{}\"", cell.row_span));
                }
            } else {
                if c > first {
                    attrs.push_str(" hMerge=\"1\"");
                }
                if r > or {
                    attrs.push_str(" vMerge=\"1\"");
                }
            }
            let mut style = doc.style(n);
            style.overlay(&cell.style);
            let cn = Node {
                kind: NodeKind::Text,
                text: if origin {
                    cell.text.clone()
                } else {
                    Some(String::new())
                },
                paragraphs: if origin {
                    cell.paragraphs.clone()
                } else {
                    Vec::new()
                },
                style,
                ..Default::default()
            };
            let fill = doc.resolve_color(&cell.fill, "#FFFFFF");
            out.push_str(&format!(
                "<a:tc{attrs}>{}<a:tcPr marL=\"50800\" marR=\"50800\" marT=\"50800\" \
                 marB=\"50800\"><a:solidFill><a:srgbClr val=\"{}\"/></a:solidFill></a:tcPr></a:tc>",
                text_body(doc, &cn, 1.0, "a"),
                &fill[1..]
            ));
        }
        out.push_str("</a:tr>");
    }
    out.push_str("</a:tbl>");
    Ok(out)
}
fn match_generated_content_type_paths(parts: &mut Package) -> Result<()> {
    // pptx 0.1.0 lowercases override names but retains ZIP member casing. OPC
    // permits this, but case-sensitive readers can lose the specialized MIME
    // types. Match exact member spelling for new packages until upstream does
    // so. Never run this on imported packages: their original XML is preserved.
    let names: BTreeMap<_, _> = parts
        .keys()
        .map(|name| (name.to_ascii_lowercase(), name))
        .collect();
    if names.len() != parts.len() {
        return Err(failure("ambiguous generated part names"));
    }
    let bytes = parts
        .get("[Content_Types].xml")
        .ok_or_else(|| failure("content types"))?;
    let doc = xml(bytes)?;
    let mut edits = Vec::new();
    for node in doc.root_element().children().filter(|n| {
        n.has_tag_name((
            "http://schemas.openxmlformats.org/package/2006/content-types",
            "Override",
        ))
    }) {
        let attribute = node
            .attributes()
            .find(|a| a.name() == "PartName")
            .ok_or_else(|| failure("override path"))?;
        let path = attribute
            .value()
            .strip_prefix('/')
            .ok_or_else(|| failure("override path"))?;
        let actual = names
            .get(&path.to_ascii_lowercase())
            .ok_or_else(|| failure("missing generated part"))?;
        if path != actual.as_str() {
            edits.push((attribute.range_value(), escape(&format!("/{actual}"))));
        }
    }
    let mut out = bytes.clone();
    for (range, value) in edits.into_iter().rev() {
        out = replace_range(&out, range, &value);
    }
    parts.insert("[Content_Types].xml".into(), out);
    Ok(())
}

pub fn generate(
    doc: &Presentation,
    assets: &Assets,
    document_id: Uuid,
    revision: u64,
) -> Result<Vec<u8>> {
    validate(doc, false)?;
    let placed = layout(doc)?;
    let mut native = pptx::Presentation::new().map_err(failure)?;
    native
        .set_slide_width(emu(doc.page.width).0)
        .map_err(failure)?;
    native
        .set_slide_height(emu(doc.page.height).0)
        .map_err(failure)?;
    let layouts = native.slide_layouts().map_err(failure)?;
    let mut slide_parts = Vec::new();
    for s in &doc.slides {
        let selected = if let Some(reference) = &s.slide_layout_ref {
            layouts
                .iter()
                .find(|l| {
                    l.partname.as_str().trim_start_matches('/') == reference.trim_start_matches('/')
                })
                .ok_or_else(|| {
                    Diagnostic::new(
                        ErrorCode::InvalidReference,
                        "/slide_layout_ref",
                        "Template layout is unavailable",
                    )
                })?
        } else {
            layouts
                .iter()
                .find(|l| l.name.eq_ignore_ascii_case("blank"))
                .unwrap_or(&layouts[0])
        };
        let slide = native.add_slide(selected).map_err(failure)?;
        slide_parts.push(slide.partname.as_str().trim_start_matches('/').to_string());
    }
    let mut parts = read(&native.to_bytes().map_err(failure)?)?;
    match_generated_content_type_paths(&mut parts)?;
    let mut bindings = BTreeMap::new();
    for (s, part) in doc.slides.iter().zip(&slide_parts) {
        let mut next = 2;
        s.content.visit(&mut |n| {
            if !n.is_container() {
                bindings.insert(
                    n.id.unwrap(),
                    Binding {
                        part: part.clone(),
                        shape_id: next,
                        parent_shape_id: None,
                    },
                );
                next += 1;
            }
        });
    }
    for (s, part) in doc.slides.iter().zip(&slide_parts) {
        let mut nodes = Vec::new();
        s.content.visit(&mut |n| {
            if !n.is_container() {
                nodes.push(n)
            }
        });
        let mut content = String::new();
        for n in nodes {
            content.push_str(&emit_node(
                &mut parts,
                part,
                n,
                doc,
                &placed,
                assets,
                bindings[&n.id.unwrap()].shape_id,
                &bindings,
            )?);
        }
        let bg = doc.resolve_color(&s.background, "#FFFFFF");
        parts.insert(
            part.clone(),
            format!(
                "<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?><p:sld \
                 xmlns:p=\"{P}\" xmlns:a=\"{A}\" \
                 xmlns:r=\"{R}\"><p:cSld><p:bg><p:bgPr><a:solidFill><a:srgbClr \
                 val=\"{}\"/></a:solidFill><a:effectLst/></p:bgPr></p:bg><p:spTree><p:\
                 nvGrpSpPr><p:cNvPr id=\"1\" \
                 name=\"\"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off \
                 x=\"0\" y=\"0\"/><a:ext cx=\"0\" cy=\"0\"/><a:chOff x=\"0\" y=\"0\"/><a:chExt \
                 cx=\"0\" cy=\"0\"/></a:xfrm></p:grpSpPr>{content}</p:spTree></p:cSld><p:\
                 clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>",
                &bg[1..]
            )
            .into_bytes(),
        );
    }
    crate::font::embed(&mut parts)?;
    add_metadata(&mut parts, document_id, revision, doc, &bindings)?;
    validate_package(&parts)?;
    write(&parts)
}
