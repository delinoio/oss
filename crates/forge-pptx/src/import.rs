use std::collections::BTreeMap;

use forge_tree_doc::*;
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::{Assets, Metadata, package::*};
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Binding {
    pub part: String,
    pub shape_id: u32,
    pub parent_shape_id: Option<u32>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TemplateLayout {
    pub reference: String,
    pub name: String,
    pub placeholders: Vec<String>,
}
#[derive(Debug, Clone)]
pub struct Imported {
    pub document: Presentation,
    pub bindings: BTreeMap<Uuid, Binding>,
    pub assets: Assets,
    pub layouts: Vec<TemplateLayout>,
    pub document_id: Uuid,
    pub revision: u64,
}
pub(crate) fn desc<'a, 'b>(
    n: roxmltree::Node<'a, 'b>,
    ns: &str,
    tag: &str,
) -> Option<roxmltree::Node<'a, 'b>> {
    n.descendants().find(|n| n.has_tag_name((ns, tag)))
}
pub(crate) fn shape_id(n: roxmltree::Node<'_, '_>) -> Option<u32> {
    desc(n, P, "cNvPr")?.attribute("id")?.parse().ok()
}
pub(crate) fn shape_element<'a, 'b>(
    doc: &'a roxmltree::Document<'b>,
    id: u32,
) -> Option<roxmltree::Node<'a, 'b>> {
    doc.descendants()
        .find(|n| {
            n.has_tag_name((P, "cNvPr"))
                && n.attribute("id").and_then(|s| s.parse::<u32>().ok()) == Some(id)
        })
        .and_then(|n| n.parent()?.parent())
}
fn number(n: roxmltree::Node<'_, '_>, a: &str) -> Option<f64> {
    n.attribute(a)?.parse::<f64>().ok().map(|n| n / 12700.0)
}
fn frame(n: roxmltree::Node<'_, '_>) -> Option<Frame> {
    let x = desc(n, A, "xfrm").or_else(|| desc(n, P, "xfrm"))?;
    if x.attribute("rot").is_some_and(|r| r != "0")
        || x.attribute("flipH") == Some("1")
        || x.attribute("flipV") == Some("1")
    {
        return None;
    }
    let off = desc(x, A, "off")?;
    let ext = desc(x, A, "ext")?;
    let frame = Frame {
        x: number(off, "x")?,
        y: number(off, "y")?,
        width: number(ext, "cx")?,
        height: number(ext, "cy")?,
    };
    if !frame.x.is_finite()
        || !frame.y.is_finite()
        || frame.width <= 0.0
        || frame.height <= 0.0
        || !frame.width.is_finite()
        || !frame.height.is_finite()
    {
        return None;
    }
    Some(frame)
}
fn inherited_frame(parts: &Package, path: &str, item: roxmltree::Node<'_, '_>) -> Option<Frame> {
    if let Some(value) = frame(item) {
        return Some(value);
    }
    // A present transform that cannot be represented (rotation/flip) is not
    // inherited.
    if desc(item, A, "xfrm").is_some() || desc(item, P, "xfrm").is_some() {
        return None;
    }
    let ph = desc(item, P, "ph")?;
    let idx = ph.attribute("idx").unwrap_or("0");
    let kind = ph.attribute("type").unwrap_or("obj");
    let layout = relationships(parts, path)
        .ok()?
        .into_iter()
        .find(|r| r.kind.ends_with("/slideLayout") && !r.external)?;
    let layout_path = resolve(path, &layout.target).ok()?;
    let layout_doc = xml(parts.get(&layout_path)?).ok()?;
    let matched = layout_doc
        .descendants()
        .find(|n| n.has_tag_name((P, "ph")) && n.attribute("idx").unwrap_or("0") == idx)?;
    let shape = matched.parent()?.parent()?.parent()?;
    if let Some(value) = frame(shape) {
        return Some(value);
    }
    let kind = matched.attribute("type").unwrap_or(kind);
    let master = relationships(parts, &layout_path)
        .ok()?
        .into_iter()
        .find(|r| r.kind.ends_with("/slideMaster") && !r.external)?;
    let master_path = resolve(&layout_path, &master.target).ok()?;
    let master_doc = xml(parts.get(&master_path)?).ok()?;
    let matched = master_doc
        .descendants()
        .find(|n| n.has_tag_name((P, "ph")) && n.attribute("type").unwrap_or("obj") == kind)?;
    frame(matched.parent()?.parent()?.parent()?)
}
fn text_style(n: roxmltree::Node<'_, '_>) -> TextStyle {
    let mut s = TextStyle::default();
    s.font_size = n
        .attribute("sz")
        .and_then(|v| v.parse::<f64>().ok())
        .map(|v| v / 100.0);
    s.font_weight = n
        .attribute("b")
        .map(|b| if b == "1" || b == "true" { 700 } else { 400 });
    s.italic = n.attribute("i").map(|b| b == "1" || b == "true");
    s.underline = n.attribute("u").map(|v| v != "none");
    s.font_family = desc(n, A, "latin")
        .and_then(|n| n.attribute("typeface"))
        .filter(|s| !s.starts_with('+'))
        .map(str::to_owned);
    s.color = desc(n, A, "srgbClr")
        .and_then(|n| n.attribute("val"))
        .map(|s| format!("#{s}"));
    s
}
fn paragraphs(n: roxmltree::Node<'_, '_>) -> Vec<Paragraph> {
    n.children()
        .filter(|n| n.has_tag_name((A, "p")))
        .map(|p| {
            let align = match p
                .children()
                .find(|n| n.has_tag_name((A, "pPr")))
                .and_then(|n| n.attribute("algn"))
            {
                Some("ctr") => Align::Center,
                Some("r") => Align::Right,
                Some("just") => Align::Justify,
                _ => Align::Left,
            };
            let runs = p
                .children()
                .filter(|n| {
                    n.has_tag_name((A, "r"))
                        || n.has_tag_name((A, "fld"))
                        || n.has_tag_name((A, "br"))
                })
                .map(|r| Run {
                    text: if r.has_tag_name((A, "br")) {
                        "\n".into()
                    } else {
                        r.descendants()
                            .filter(|n| n.has_tag_name((A, "t")))
                            .filter_map(|n| n.text())
                            .collect()
                    },
                    style: desc(r, A, "rPr").map(text_style).unwrap_or_default(),
                })
                .collect();
            Paragraph { align, runs }
        })
        .collect()
}
fn image_projection_matches(
    item: roxmltree::Node<'_, '_>,
    data: &[u8],
    frame: Frame,
    fit: ImageFit,
) -> bool {
    let Ok((width, height, _)) = crate::emit::image_info(data) else {
        return false;
    };
    let ratio = f64::from(width) / f64::from(height);
    let frame_ratio = frame.width / frame.height;
    if fit == ImageFit::Contain {
        return (ratio - frame_ratio).abs() < 0.002 * ratio;
    }
    let Some(crop) = desc(item, A, "srcRect") else {
        return false;
    };
    let mut expected = [0., 0., 0., 0.];
    if ratio > frame_ratio {
        let v = ((1. - frame_ratio / ratio) * 50000.).round();
        expected[0] = v;
        expected[2] = v;
    } else {
        let v = ((1. - ratio / frame_ratio) * 50000.).round();
        expected[1] = v;
        expected[3] = v;
    }
    ["l", "t", "r", "b"]
        .iter()
        .zip(expected)
        .all(|(name, value)| {
            crop.attribute(*name)
                .unwrap_or("0")
                .parse::<f64>()
                .is_ok_and(|actual| (actual - value).abs() <= 1.)
        })
}
fn parse_table(n: roxmltree::Node<'_, '_>) -> Result<(Vec<Column>, Vec<TableRow>)> {
    let tbl = desc(n, A, "tbl").ok_or_else(|| failure("table"))?;
    let columns = desc(tbl, A, "tblGrid")
        .ok_or_else(|| failure("grid"))?
        .children()
        .filter(|n| n.has_tag_name((A, "gridCol")))
        .map(|n| {
            Ok(Column {
                width: Size::Points(number(n, "w").ok_or_else(|| failure("width"))?),
            })
        })
        .collect::<Result<Vec<_>>>()?;
    let rows = tbl
        .children()
        .filter(|n| n.has_tag_name((A, "tr")))
        .map(|r| TableRow {
            cells: r
                .children()
                .filter(|n| n.has_tag_name((A, "tc")))
                .filter(|n| {
                    !matches!(n.attribute("hMerge"), Some("1" | "true"))
                        && !matches!(n.attribute("vMerge"), Some("1" | "true"))
                })
                .map(|c| Cell {
                    text: None,
                    paragraphs: desc(c, A, "txBody").map(paragraphs).unwrap_or_default(),
                    style: TextStyle::default(),
                    fill: Color {
                        color: desc(c, A, "tcPr")
                            .and_then(|n| desc(n, A, "srgbClr"))
                            .and_then(|n| n.attribute("val"))
                            .map(|s| format!("#{s}")),
                        color_ref: None,
                    },
                    row_span: c
                        .attribute("rowSpan")
                        .and_then(|s| s.parse().ok())
                        .unwrap_or(1),
                    col_span: c
                        .attribute("gridSpan")
                        .and_then(|s| s.parse().ok())
                        .unwrap_or(1),
                })
                .collect(),
        })
        .collect();
    Ok((columns, rows))
}
// Sparse caches encode missing cells, which v1's dense chart data cannot
// retain. Reorder complete caches by their declared indices instead of XML
// order.
fn chart_cache(n: roxmltree::Node<'_, '_>) -> Result<Vec<String>> {
    let counts: Vec<_> = n
        .descendants()
        .filter(|n| n.has_tag_name((C, "ptCount")))
        .collect();
    if counts.len() != 1 {
        return Err(failure("chart cache count"));
    }
    let count: usize = counts[0]
        .attribute("val")
        .and_then(|v| v.parse().ok())
        .ok_or_else(|| failure("chart cache count"))?;
    if !(1..=10_000).contains(&count) {
        return Err(failure("chart cache limit"));
    }
    let mut values = vec![None; count];
    for point in counts[0]
        .parent()
        .unwrap()
        .children()
        .filter(|n| n.has_tag_name((C, "pt")))
    {
        let index: usize = point
            .attribute("idx")
            .and_then(|v| v.parse().ok())
            .ok_or_else(|| failure("chart cache index"))?;
        let slot = values
            .get_mut(index)
            .ok_or_else(|| failure("chart cache index"))?;
        if slot.is_some() {
            return Err(failure("duplicate chart cache index"));
        }
        let value = desc(point, C, "v").ok_or_else(|| failure("chart cache value"))?;
        *slot = Some(value.text().unwrap_or("").to_owned());
    }
    values
        .into_iter()
        .map(|v| v.ok_or_else(|| failure("sparse chart cache")))
        .collect()
}
fn parse_chart(
    parts: &Package,
    part: &str,
    n: roxmltree::Node<'_, '_>,
) -> Result<(ChartData, Orientation, Legend, DataLabels)> {
    let rid = desc(n, C, "chart")
        .and_then(|n| n.attribute((R, "id")))
        .ok_or_else(|| failure("chart"))?;
    let path = related(parts, part, rid)?;
    let doc = xml(parts.get(&path).ok_or_else(|| failure("chart"))?)?;
    let bars: Vec<_> = doc
        .descendants()
        .filter(|n| n.has_tag_name((C, "barChart")))
        .collect();
    if bars.len() != 1 {
        return error(
            ErrorCode::UnsupportedEdit,
            "",
            "Only a single native bar chart is editable",
        );
    }
    let bar = bars[0];
    let mut data = ChartData::default();
    for (i, s) in bar
        .children()
        .filter(|n| n.has_tag_name((C, "ser")))
        .enumerate()
    {
        let cat = desc(s, C, "cat").ok_or_else(|| failure("category"))?;
        let labels = chart_cache(cat)?;
        if i == 0 {
            data.categories = labels;
        } else if data.categories != labels {
            return error(
                ErrorCode::UnsupportedEdit,
                "",
                "Series use different category domains",
            );
        }
        let val = desc(s, C, "val").ok_or_else(|| failure("values"))?;
        let values = chart_cache(val)?
            .into_iter()
            .map(|p| {
                p.parse::<f64>()
                    .ok()
                    .filter(|v| v.is_finite())
                    .ok_or_else(|| failure("value"))
            })
            .collect::<Result<Vec<f64>>>()?;
        if values.len() != data.categories.len() {
            return Err(failure("chart cache lengths"));
        }
        let name = desc(s, C, "tx")
            .and_then(|n| desc(n, C, "v"))
            .and_then(|n| n.text())
            .unwrap_or("Series")
            .into();
        data.series.push(Series {
            key: format!("series-{i}"),
            name,
            values,
        });
    }
    let orientation = if desc(bar, C, "barDir").and_then(|n| n.attribute("val")) == Some("bar") {
        Orientation::Horizontal
    } else {
        Orientation::Vertical
    };
    let legend = match desc(doc.root_element(), C, "legendPos").and_then(|n| n.attribute("val")) {
        Some("b") => Legend::Bottom,
        Some(_) => Legend::Right,
        None => Legend::Hidden,
    };
    let labels = if desc(bar, C, "showVal").and_then(|n| n.attribute("val")) == Some("1") {
        DataLabels::Value
    } else {
        DataLabels::Hidden
    };
    Ok((data, orientation, legend, labels))
}
pub(crate) fn slide_paths(parts: &Package) -> Result<Vec<String>> {
    let main = main_part(parts)?;
    let doc = xml(parts.get(&main).ok_or_else(|| failure("main"))?)?;
    if doc.root_element().tag_name().namespace() != Some(P) {
        return error(
            ErrorCode::UnsupportedPackage,
            "",
            "Only Transitional PresentationML is supported",
        );
    }
    doc.descendants()
        .filter(|n| n.has_tag_name((P, "sldId")))
        .map(|n| {
            related(
                parts,
                &main,
                n.attribute((R, "id")).ok_or_else(|| failure("slide id"))?,
            )
        })
        .collect()
}
pub fn import(bytes: &[u8]) -> Result<Imported> {
    let parts = read(bytes)?;
    validate_package(&parts)?;
    let layouts = template_layouts(&parts)?;
    if let Some(metadata_path) = crate::metadata_part(&parts)? {
        let x = xml(&parts[&metadata_path])?;
        let text = x.root_element().text().ok_or_else(|| failure("metadata"))?;
        let m: Metadata = serde_json::from_str(text).map_err(failure)?;
        let hashes: BTreeMap<_, _> = parts
            .iter()
            .filter(|(n, _)| **n != metadata_path)
            .map(|(n, b)| (n.clone(), sha(b)))
            .collect();
        if m.version != 1 || m.hashes != hashes {
            return error(
                ErrorCode::StaleMetadata,
                "",
                "Package changed outside Forge; explicitly reimport without Forge metadata",
            );
        }
        validate(&m.document, true)?;
        if m.document_id.get_version_num() != 7 {
            return Err(failure("document identity"));
        }
        let mut leaves = Vec::new();
        for slide in &m.document.slides {
            slide.content.visit(&mut |n| {
                if !n.is_container() {
                    leaves.push(n);
                }
            });
        }
        if leaves.len() != m.bindings.len()
            || leaves
                .iter()
                .any(|n| n.id.is_none() || !m.bindings.contains_key(&n.id.unwrap()))
        {
            return Err(failure("metadata bindings"));
        }
        for n in leaves {
            let binding = &m.bindings[&n.id.unwrap()];
            let native = xml(parts
                .get(&binding.part)
                .ok_or_else(|| failure("binding part"))?)?;
            if shape_element(&native, binding.shape_id).is_none() {
                return Err(failure("binding shape"));
            }
        }
        let assets = assets_for_document(&parts, &m.document, &m.bindings)?;
        return Ok(Imported {
            document: m.document,
            bindings: m.bindings,
            assets,
            layouts,
            document_id: m.document_id,
            revision: m.revision,
        });
    }
    let main = main_part(&parts)?;
    let x = xml(&parts[&main])?;
    let sz = desc(x.root_element(), P, "sldSz").ok_or_else(|| failure("size"))?;
    let mut document = Presentation {
        dsl_version: 1,
        kind: DocumentKind::Presentation,
        unit: Unit::Pt,
        page: Page {
            width: number(sz, "cx").ok_or_else(|| failure("width"))?,
            height: number(sz, "cy").ok_or_else(|| failure("height"))?,
        },
        theme: Theme::default(),
        assets: BTreeMap::new(),
        slides: Vec::new(),
    };
    let mut bindings = BTreeMap::new();
    let mut assets = Assets::new();
    for (i, path) in slide_paths(&parts)?.into_iter().enumerate() {
        let x = xml(&parts[&path])?;
        let tree = desc(x.root_element(), P, "spTree").ok_or_else(|| failure("tree"))?;
        let mut native_ids = std::collections::HashSet::new();
        for native in tree.descendants().filter(|n| n.has_tag_name((P, "cNvPr"))) {
            let id = native
                .attribute("id")
                .and_then(|v| v.parse::<u32>().ok())
                .ok_or_else(|| failure("shape identity"))?;
            if !native_ids.insert(id) {
                return Err(failure("duplicate native identity"));
            }
        }
        let mut children = Vec::new();
        let mut seen = std::collections::HashSet::new();
        for item in tree.children().filter(|n| {
            n.is_element() && !n.has_tag_name((P, "nvGrpSpPr")) && !n.has_tag_name((P, "grpSpPr"))
        }) {
            let Some(native_id) = shape_id(item) else {
                continue;
            };
            if !seen.insert(native_id) {
                return error(
                    ErrorCode::InvalidPackage,
                    "",
                    "Duplicate native shape identifier",
                );
            }
            let id = Uuid::now_v7();
            let mut n = Node {
                id: Some(id),
                key: Some(format!("slide-{}.shape-{native_id}", i + 1)),
                kind: NodeKind::Opaque,
                frame: Some(inherited_frame(&parts, &path, item).unwrap_or(Frame {
                    x: 0.0,
                    y: 0.0,
                    width: 1.0,
                    height: 1.0,
                })),
                opaque_ref: Some(format!("{path}#{native_id}")),
                ..Default::default()
            };
            let parsed = if inherited_frame(&parts, &path, item).is_none() {
                false
            } else if item.has_tag_name((P, "sp")) {
                let body = desc(item, P, "txBody");
                let has_text = body.is_some_and(|b| {
                    b.descendants().any(|n| {
                        n.has_tag_name((A, "t")) && n.text().is_some_and(|s| !s.is_empty())
                    })
                });
                let textbox = desc(item, P, "cNvSpPr")
                    .is_some_and(|n| matches!(n.attribute("txBox"), Some("1" | "true")));
                if let Some(body) =
                    body.filter(|_| has_text || textbox || desc(item, P, "ph").is_some())
                {
                    n.kind = NodeKind::Text;
                    n.paragraphs = paragraphs(body);
                    if n.paragraphs.is_empty() {
                        n.text = Some(String::new());
                    }
                    true
                } else if let Some(preset) =
                    desc(item, A, "prstGeom").and_then(|n| n.attribute("prst"))
                {
                    n.shape = match preset {
                        "rect" => Shape::Rect,
                        "roundRect" => Shape::RoundedRect,
                        "ellipse" => Shape::Ellipse,
                        _ => Shape::Rect,
                    };
                    if matches!(preset, "rect" | "roundRect" | "ellipse") {
                        n.kind = NodeKind::Shape;
                        true
                    } else {
                        false
                    }
                } else {
                    false
                }
            } else if item.has_tag_name((P, "pic")) {
                if let Some(rid) = desc(item, A, "blip").and_then(|n| n.attribute((R, "embed"))) {
                    if let Ok(media) = related(&parts, &path, rid) {
                        if let Some(data) = parts.get(&media) {
                            if crate::emit::image_info(data).is_ok() {
                                let handle = format!("asset_{}", sha(data));
                                let key = format!("image-{id}");
                                document.assets.insert(
                                    key.clone(),
                                    AssetRef {
                                        handle: handle.clone(),
                                    },
                                );
                                assets.insert(handle, data.clone());
                                n.kind = NodeKind::Image;
                                n.asset_ref = Some(key);
                                n.fit = if desc(item, A, "srcRect").is_some() {
                                    ImageFit::Cover
                                } else {
                                    ImageFit::Contain
                                };
                                image_projection_matches(item, data, n.frame.unwrap(), n.fit)
                            } else {
                                false
                            }
                        } else {
                            false
                        }
                    } else {
                        false
                    }
                } else {
                    false
                }
            } else if desc(item, A, "tbl").is_some() {
                if let Ok((columns, rows)) = parse_table(item) {
                    n.kind = NodeKind::Table;
                    n.columns = columns;
                    n.rows = rows;
                    table_grid(&n).is_ok()
                } else {
                    false
                }
            } else if desc(item, C, "chart").is_some() {
                if let Ok((data, orientation, legend, data_labels)) =
                    parse_chart(&parts, &path, item)
                {
                    n.kind = NodeKind::Chart;
                    n.data = Some(data);
                    n.orientation = orientation;
                    n.legend = legend;
                    n.data_labels = data_labels;
                    true
                } else {
                    false
                }
            } else {
                false
            };
            if parsed {
                n.opaque_ref = None;
            } else {
                n = Node {
                    id: n.id,
                    key: n.key,
                    kind: NodeKind::Opaque,
                    frame: n.frame,
                    opaque_ref: n.opaque_ref,
                    ..Default::default()
                };
            }
            if let Some(ph) = desc(item, P, "ph") {
                n.placeholder_ref = Some(ph.attribute("idx").unwrap_or("0").into());
            }
            bindings.insert(
                id,
                Binding {
                    part: path.clone(),
                    shape_id: native_id,
                    parent_shape_id: None,
                },
            );
            children.push(n);
        }
        let layout_ref = relationships(&parts, &path)?
            .into_iter()
            .find(|r| r.kind.ends_with("/slideLayout") && !r.external)
            .map(|r| resolve(&path, &r.target))
            .transpose()?;
        let background = desc(x.root_element(), P, "bgPr")
            .and_then(|n| desc(n, A, "srgbClr"))
            .and_then(|n| n.attribute("val"))
            .map(|s| format!("#{s}"));
        document.slides.push(Slide {
            id: Some(Uuid::now_v7()),
            key: Some(format!("slide-{}", i + 1)),
            background: Color {
                color: background,
                color_ref: None,
            },
            slide_layout_ref: layout_ref,
            content: Node {
                kind: NodeKind::Canvas,
                id: Some(Uuid::now_v7()),
                key: Some(format!("slide-{}.canvas", i + 1)),
                children,
                ..Default::default()
            },
        });
    }
    validate(&document, true)?;
    Ok(Imported {
        document,
        bindings,
        assets,
        layouts,
        document_id: Uuid::now_v7(),
        revision: 0,
    })
}
fn template_layouts(parts: &Package) -> Result<Vec<TemplateLayout>> {
    let mut layouts = Vec::new();
    for (path, bytes) in parts
        .iter()
        .filter(|(p, _)| p.starts_with("ppt/slideLayouts/") && p.ends_with(".xml"))
    {
        let doc = xml(bytes)?;
        if !doc.root_element().has_tag_name((P, "sldLayout")) {
            continue;
        }
        let name = desc(doc.root_element(), P, "cSld")
            .and_then(|n| n.attribute("name"))
            .unwrap_or("")
            .into();
        let placeholders = doc
            .descendants()
            .filter(|n| n.has_tag_name((P, "ph")))
            .map(|n| n.attribute("idx").unwrap_or("0").into())
            .collect();
        layouts.push(TemplateLayout {
            reference: path.clone(),
            name,
            placeholders,
        });
    }
    Ok(layouts)
}
fn assets_for_document(
    parts: &Package,
    document: &Presentation,
    bindings: &BTreeMap<Uuid, Binding>,
) -> Result<Assets> {
    let mut assets = Assets::new();
    for (bid, b) in bindings {
        let Some(n) = document.find(&Target {
            node_id: Some(*bid),
            key: None,
        }) else {
            return Err(failure("binding"));
        };
        if n.kind != NodeKind::Image {
            continue;
        }
        let doc = xml(parts.get(&b.part).ok_or_else(|| failure("slide"))?)?;
        let shape = shape_element(&doc, b.shape_id).ok_or_else(|| failure("shape"))?;
        let rid = desc(shape, A, "blip")
            .and_then(|n| n.attribute((R, "embed")))
            .ok_or_else(|| failure("image"))?;
        let path = related(parts, &b.part, rid)?;
        let bytes = parts.get(&path).ok_or_else(|| failure("media"))?;
        let handle = n
            .asset_ref
            .as_ref()
            .and_then(|key| document.assets.get(key))
            .ok_or_else(|| failure("asset"))?
            .handle
            .clone();
        if handle != format!("asset_{}", sha(bytes)) {
            return Err(failure("asset checksum"));
        }
        crate::emit::image_info(bytes)?;
        assets.insert(handle, bytes.clone());
    }
    Ok(assets)
}
