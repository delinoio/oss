use forge_pptx::*;
use forge_tree_doc::*;
use uuid::Uuid;
const EXTERNAL: &[u8] = include_bytes!("fixtures/external.pptx");
const PNG: &[u8] = include_bytes!("fixtures/sample.png");
const REPLACEMENT: &[u8] = include_bytes!("fixtures/replacement.png");
fn key(value: &str) -> Target {
    Target {
        key: Some(value.into()),
        node_id: None,
    }
}
fn target(p: &Presentation, kind: NodeKind) -> Target {
    let mut id = None;
    for s in &p.slides {
        s.content.visit(&mut |n| {
            if n.kind == kind {
                id = id.or(n.id);
            }
        });
    }
    Target {
        key: None,
        node_id: Some(id.expect("native node")),
    }
}
fn patch(i: &Imported, ops: Vec<Operation>) -> Presentation {
    apply_patch(
        &i.document,
        &Patch {
            dsl_version: 1,
            kind: PatchKind::Patch,
            document_id: i.document_id,
            base_revision: i.revision,
            operations: ops,
        },
        i.document_id,
        i.revision,
    )
    .unwrap()
}
fn xml_part(parts: &std::collections::BTreeMap<String, Vec<u8>>, path: &str) -> String {
    String::from_utf8(parts[path].clone()).unwrap()
}
#[test]
fn lexical_true_flips_remain_opaque_and_preserved() {
    let path = "ppt/slides/slide1.xml";
    let original = read_package(EXTERNAL).unwrap();
    let slide = xml_part(&original, path);
    let doc = roxmltree::Document::parse(&slide).unwrap();
    let picture = doc
        .descendants()
        .find(|n| n.tag_name().name() == "pic")
        .unwrap();
    let transform = picture
        .descendants()
        .find(|n| n.tag_name().name() == "xfrm")
        .unwrap();
    for axis in ["flipH", "flipV"] {
        for value in ["1", "true", "0", "false"] {
            let mut parts = original.clone();
            let mut modified = slide.clone();
            modified.insert_str(
                transform.range().start + "<a:xfrm".len(),
                &format!(" {axis}=\"{value}\""),
            );
            parts.insert(path.into(), modified.clone().into_bytes());
            let source = write_package(&parts).unwrap();
            let imported = import(&source).unwrap();
            let flipped = matches!(value, "1" | "true");
            assert_eq!(
                imported.document.slides[0]
                    .content
                    .children
                    .iter()
                    .any(|n| n.kind == NodeKind::Image),
                !flipped
            );
            let next = patch(
                &imported,
                vec![Operation::SetText {
                    target: target(&imported.document, NodeKind::Text),
                    text: "Unrelated edit".into(),
                    cell: None,
                }],
            );
            let output = update(
                &source,
                &imported.document,
                &imported.bindings,
                &next,
                &imported.assets,
                imported.document_id,
                1,
            )
            .unwrap();
            let result = xml_part(&read_package(&output).unwrap(), path);
            let before = roxmltree::Document::parse(&modified).unwrap();
            let after = roxmltree::Document::parse(&result).unwrap();
            let before = before
                .descendants()
                .find(|n| n.tag_name().name() == "pic")
                .unwrap();
            let after = after
                .descendants()
                .find(|n| n.tag_name().name() == "pic")
                .unwrap();
            assert_eq!(&modified[before.range()], &result[after.range()]);
        }
    }
}
#[test]
fn preview_font_uses_free_part_and_relationship_names() {
    let mut parts = read_package(EXTERNAL).unwrap();
    let font_path = "ppt/fonts/forge-noto-sans-kr.fntdata";
    let original_font = b"unrelated embedded content";
    parts.insert(font_path.into(), original_font.to_vec());
    let rel_path = "ppt/_rels/presentation.xml.rels";
    let original_rel = format!("<Relationship Id=\"rIdForgeFont\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/font\" Target=\"/{font_path}\"/>");
    let relationships = xml_part(&parts, rel_path).replace(
        "</Relationships>",
        &format!("{original_rel}</Relationships>"),
    );
    parts.insert(rel_path.into(), relationships.into_bytes());
    let source = write_package(&parts).unwrap();
    let rendered = preview_bytes(&source).unwrap();
    let output = read_package(&rendered).unwrap();
    validate_package(&output).unwrap();
    assert_eq!(output[font_path], original_font);
    assert!(xml_part(&output, rel_path).contains(&original_rel));
    let main = xml_part(&output, "ppt/presentation.xml");
    let doc = roxmltree::Document::parse(&main).unwrap();
    let regular = doc
        .descendants()
        .find(|n| n.tag_name().name() == "regular")
        .unwrap();
    let rid = regular
        .attribute((
            "http://schemas.openxmlformats.org/officeDocument/2006/relationships",
            "id",
        ))
        .unwrap();
    assert_ne!(rid, "rIdForgeFont");
    let rels = xml_part(&output, rel_path);
    let rels = roxmltree::Document::parse(&rels).unwrap();
    let target = rels
        .descendants()
        .find(|n| n.attribute("Id") == Some(rid))
        .unwrap()
        .attribute("Target")
        .unwrap()
        .trim_start_matches('/');
    assert_ne!(target, font_path);
    assert!(output[target].ends_with(FONT_BYTES));
    assert_eq!(preview_bytes(&rendered).unwrap(), rendered);
}
#[test]
fn metadata_cannot_bind_multiple_logical_nodes_to_one_native_shape() {
    let mut document: Presentation = parse(include_bytes!(
        "../../forge-tree-doc/examples/overview.json"
    ))
    .unwrap();
    document.assign_ids();
    let source = generate(&document, &Assets::new(), Uuid::now_v7(), 0).unwrap();
    let imported = import(&source).unwrap();
    assert!(imported.bindings.len() >= 2);
    let mut parts = read_package(&source).unwrap();
    let raw = xml_part(&parts, "customXml/forge.xml");
    let xml = roxmltree::Document::parse(&raw).unwrap();
    let mut metadata: Metadata = serde_json::from_str(xml.root_element().text().unwrap()).unwrap();
    let ids: Vec<_> = metadata.bindings.keys().copied().collect();
    let first = metadata.bindings[&ids[0]].clone();
    metadata.bindings.insert(ids[1], first);
    let json = serde_json::to_string(&metadata)
        .unwrap()
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;");
    parts.insert(
        "customXml/forge.xml".into(),
        format!("<forge xmlns=\"urn:delino:forge:v1\">{json}</forge>").into_bytes(),
    );
    assert_eq!(
        import(&write_package(&parts).unwrap()).unwrap_err().code,
        ErrorCode::InvalidPackage
    );
    assert_eq!(import(&source).unwrap().bindings, imported.bindings);
}
#[test]
fn empty_native_bar_charts_remain_opaque() {
    let mut parts = read_package(EXTERNAL).unwrap();
    let path = "ppt/charts/chart1.xml";
    let chart = xml_part(&parts, path);
    let xml = roxmltree::Document::parse(&chart).unwrap();
    let mut empty = chart.clone();
    for series in xml
        .descendants()
        .filter(|n| n.tag_name().name() == "ser")
        .collect::<Vec<_>>()
        .into_iter()
        .rev()
    {
        empty.replace_range(series.range(), "");
    }
    parts.insert(path.into(), empty.clone().into_bytes());
    let source = write_package(&parts).unwrap();
    let imported = import(&source).unwrap();
    assert!(
        !imported.document.slides[0]
            .content
            .children
            .iter()
            .any(|n| n.kind == NodeKind::Chart)
    );
    assert_eq!(
        update(
            &source,
            &imported.document,
            &imported.bindings,
            &imported.document,
            &imported.assets,
            imported.document_id,
            0
        )
        .unwrap(),
        source
    );
    let next = patch(
        &imported,
        vec![Operation::SetText {
            target: target(&imported.document, NodeKind::Text),
            text: "Unrelated edit".into(),
            cell: None,
        }],
    );
    let output = update(
        &source,
        &imported.document,
        &imported.bindings,
        &next,
        &imported.assets,
        imported.document_id,
        1,
    )
    .unwrap();
    assert_eq!(read_package(&output).unwrap()[path], empty.as_bytes());
}
#[test]
fn imported_chart_caches_follow_indices_and_preserve_unrepresentable_data() {
    let original = read_package(EXTERNAL).unwrap();
    let path = "ppt/charts/chart1.xml";
    let chart = xml_part(&original, path);
    let ordered = "<c:pt idx=\"0\"><c:v>10</c:v></c:pt><c:pt idx=\"1\"><c:v>20</c:v></c:pt><c:pt \
                   idx=\"2\"><c:v>30</c:v></c:pt>";
    let reversed = "<c:pt idx=\"2\"><c:v>30</c:v></c:pt><c:pt idx=\"0\"><c:v>10</c:v></c:pt><c:pt \
                    idx=\"1\"><c:v>20</c:v></c:pt>";
    assert!(chart.contains(ordered));
    let mut parts = original.clone();
    parts.insert(path.into(), chart.replace(ordered, reversed).into_bytes());
    let imported = import(&write_package(&parts).unwrap()).unwrap();
    let node = imported.document.slides[0]
        .content
        .children
        .iter()
        .find(|n| n.kind == NodeKind::Chart)
        .unwrap();
    assert_eq!(
        node.data.as_ref().unwrap().series[0].values,
        vec![10., 20., 30.]
    );

    for replacement in [
        ordered.replace("<c:pt idx=\"1\"><c:v>20</c:v></c:pt>", ""),
        ordered.replace("idx=\"1\"", "idx=\"0\""),
        ordered.replace("idx=\"2\"", "idx=\"3\""),
    ] {
        parts.insert(
            path.into(),
            chart.replace(ordered, &replacement).into_bytes(),
        );
        let source = write_package(&parts).unwrap();
        let imported = import(&source).unwrap();
        assert!(
            !imported.document.slides[0]
                .content
                .children
                .iter()
                .any(|n| n.kind == NodeKind::Chart)
        );
        let next = patch(
            &imported,
            vec![Operation::SetText {
                target: target(&imported.document, NodeKind::Text),
                text: "Unrelated edit".into(),
                cell: None,
            }],
        );
        let output = update(
            &source,
            &imported.document,
            &imported.bindings,
            &next,
            &imported.assets,
            imported.document_id,
            1,
        )
        .unwrap();
        assert_eq!(read_package(&output).unwrap()[path], parts[path]);
    }
}
#[test]
fn all_nodes_are_native_and_chart_workbook_matches() {
    let mut document: Presentation = parse(include_bytes!(
        "../../forge-tree-doc/examples/all-nodes.json"
    ))
    .unwrap();
    document.assets.get_mut("sample").unwrap().handle = format!("asset_{}", sha(PNG));
    document.assign_ids();
    let assets = Assets::from([(format!("asset_{}", sha(PNG)), PNG.to_vec())]);
    let bytes = generate(&document, &assets, Uuid::now_v7(), 0).unwrap();
    let imported = import(&bytes).unwrap();
    assert_eq!(imported.document, document);
    let package = read_package(&bytes).unwrap();
    let slide = xml_part(&package, "ppt/slides/slide2.xml");
    for native in [
        "<p:pic",
        "<a:tbl",
        "gridSpan=\"3\"",
        "rowSpan=\"2\"",
        "<c:chart",
        "<p:cxnSp",
        "<a:srcRect",
    ] {
        assert!(slide.contains(native), "{native}");
    }
    let next = patch(
        &imported,
        vec![
            Operation::SetChartData {
                target: key("details.chart"),
                data: ChartData {
                    categories: vec!["New A".into(), "New B".into()],
                    series: vec![
                        Series {
                            key: "actual".into(),
                            name: "Actual revised".into(),
                            values: vec![99., 88.],
                        },
                        Series {
                            key: "target".into(),
                            name: "Target".into(),
                            values: vec![101., 102.],
                        },
                    ],
                },
            },
            Operation::SetFrame {
                target: key("details.cover"),
                frame: Frame {
                    x: 40.,
                    y: 230.,
                    width: 160.,
                    height: 130.,
                },
            },
            Operation::SetImageAsset {
                target: key("details.image"),
                asset_ref: format!("asset_{}", sha(REPLACEMENT)),
            },
        ],
    );
    let mut assets = assets;
    assets.insert(format!("asset_{}", sha(REPLACEMENT)), REPLACEMENT.to_vec());
    let result = update(
        &bytes,
        &document,
        &imported.bindings,
        &next,
        &assets,
        imported.document_id,
        1,
    )
    .unwrap();
    let reopened = import(&result).unwrap();
    assert_eq!(
        reopened.assets[&format!("asset_{}", sha(REPLACEMENT))],
        REPLACEMENT
    );
    let parts = read_package(&result).unwrap();
    let chart = parts
        .iter()
        .find(|(p, _)| p.starts_with("ppt/charts/") && p.ends_with(".xml"))
        .unwrap();
    let chart_text = String::from_utf8(chart.1.clone()).unwrap();
    assert!(chart_text.contains("New A"));
    assert!(chart_text.contains(">99<"));
    assert!(chart_text.contains("Sheet1!$C$2:$C$3"));
    let wb = parts.iter().find(|(p, _)| p.ends_with(".xlsx")).unwrap();
    let wb = read_package(wb.1).unwrap();
    let sheet = xml_part(&wb, "xl/worksheets/sheet1.xml");
    assert!(sheet.contains("New A"));
    assert!(sheet.contains(">99<"));
}
#[test]
fn unrelated_custom_xml_does_not_claim_forge_identity_or_get_overwritten() {
    let mut parts = read_package(EXTERNAL).unwrap();
    let unrelated = b"<forge xmlns=\"urn:another-application\">original payload</forge>";
    parts.insert("customXml/forge.xml".into(), unrelated.to_vec());
    let relationship = "<Relationship Id=\"rIdForgeMetadata\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/customXml\" Target=\"/customXml/forge.xml\"/>";
    let rels = xml_part(&parts, "_rels/.rels").replace(
        "</Relationships>",
        &format!("{relationship}</Relationships>"),
    );
    parts.insert("_rels/.rels".into(), rels.into_bytes());
    let source = write_package(&parts).unwrap();
    let imported = import(&source).unwrap();
    assert_eq!(
        update(
            &source,
            &imported.document,
            &imported.bindings,
            &imported.document,
            &imported.assets,
            imported.document_id,
            0
        )
        .unwrap(),
        source
    );
    let next = patch(
        &imported,
        vec![Operation::SetText {
            target: target(&imported.document, NodeKind::Text),
            text: "Modified".into(),
            cell: None,
        }],
    );
    let output = update(
        &source,
        &imported.document,
        &imported.bindings,
        &next,
        &imported.assets,
        imported.document_id,
        1,
    )
    .unwrap();
    let reopened = import(&output).unwrap();
    assert_eq!(reopened.document_id, imported.document_id);
    assert_eq!(reopened.revision, 1);
    assert_eq!(
        reopened
            .document
            .find(&target(&next, NodeKind::Text))
            .unwrap()
            .text
            .as_deref(),
        Some("Modified")
    );
    let output_parts = read_package(&output).unwrap();
    assert_eq!(output_parts["customXml/forge.xml"], unrelated);
    assert!(xml_part(&output_parts, "_rels/.rels").contains(relationship));
    let mut externally_changed = output_parts;
    externally_changed.insert("customXml/forge.xml".into(), b"<external/>".to_vec());
    assert_eq!(
        import(&write_package(&externally_changed).unwrap())
            .unwrap_err()
            .code,
        ErrorCode::StaleMetadata
    );
}
#[test]
fn nonuniform_native_table_heights_remain_opaque_during_unrelated_edits() {
    let path = "ppt/slides/slide1.xml";
    let mut parts = read_package(EXTERNAL).unwrap();
    let slide = xml_part(&parts, path);
    let doc = roxmltree::Document::parse(&slide).unwrap();
    let rows: Vec<_> = doc
        .descendants()
        .filter(|n| {
            n.has_tag_name((
                "http://schemas.openxmlformats.org/drawingml/2006/main",
                "tr",
            ))
        })
        .collect();
    let original_table = doc
        .descendants()
        .find(|n| n.tag_name().name() == "tbl")
        .unwrap();
    for heights in [[10, 70, 100], [40, 40, 40]] {
        let mut changed = slide.clone();
        for (row, height) in rows.iter().zip(heights).rev() {
            let raw = &slide[row.range()];
            changed.replace_range(
                row.range(),
                &raw.replacen(
                    &format!("h=\"{}\"", row.attribute("h").unwrap()),
                    &format!("h=\"{}\"", height * 12700),
                    1,
                ),
            );
        }
        assert_ne!(
            &changed[original_table.range().start..],
            &slide[original_table.range().start..]
        );
        parts.insert(path.into(), changed.clone().into_bytes());
        let source = write_package(&parts).unwrap();
        let imported = import(&source).unwrap();
        assert!(
            !imported.document.slides[0]
                .content
                .children
                .iter()
                .any(|n| n.kind == NodeKind::Table)
        );
        let next = patch(
            &imported,
            vec![Operation::SetText {
                target: target(&imported.document, NodeKind::Text),
                text: "Unrelated edit".into(),
                cell: None,
            }],
        );
        let output = update(
            &source,
            &imported.document,
            &imported.bindings,
            &next,
            &imported.assets,
            imported.document_id,
            1,
        )
        .unwrap();
        let result = xml_part(&read_package(&output).unwrap(), path);
        let before = roxmltree::Document::parse(&changed).unwrap();
        let after = roxmltree::Document::parse(&result).unwrap();
        let before_table = before
            .descendants()
            .find(|n| n.tag_name().name() == "tbl")
            .unwrap();
        let after_table = after
            .descendants()
            .find(|n| n.tag_name().name() == "tbl")
            .unwrap();
        assert_eq!(&changed[before_table.range()], &result[after_table.range()]);
    }
}
#[test]
fn external_noop_is_byte_exact_and_partial_edits_preserve_extensions() {
    let i = import(EXTERNAL).unwrap();
    assert_eq!(
        update(
            EXTERNAL,
            &i.document,
            &i.bindings,
            &i.document,
            &i.assets,
            i.document_id,
            0
        )
        .unwrap(),
        EXTERNAL
    );
    assert!(
        i.document.slides[0]
            .content
            .children
            .iter()
            .any(|n| n.kind == NodeKind::Opaque)
    );
    assert!(
        i.document.slides[0]
            .content
            .children
            .iter()
            .any(|n| n.kind == NodeKind::Text && n.placeholder_ref.is_some())
    );
    let table = target(&i.document, NodeKind::Table);
    let image = target(&i.document, NodeKind::Image);
    let chart = target(&i.document, NodeKind::Chart);
    let title = target(&i.document, NodeKind::Text);
    let next = patch(
        &i,
        vec![
            Operation::SetText {
                target: title,
                text: "Edited external title".into(),
                cell: None,
            },
            Operation::SetText {
                target: table,
                text: "Updated cell".into(),
                cell: Some(CellAddress { row: 1, column: 1 }),
            },
            Operation::SetImageAsset {
                target: image,
                asset_ref: format!("asset_{}", sha(REPLACEMENT)),
            },
            Operation::SetChartData {
                target: chart,
                data: ChartData {
                    categories: vec!["A".into(), "B".into(), "C".into()],
                    series: vec![
                        Series {
                            key: "series-0".into(),
                            name: "First".into(),
                            values: vec![33., 44., 55.],
                        },
                        Series {
                            key: "series-1".into(),
                            name: "Second".into(),
                            values: vec![11., 22., 33.],
                        },
                    ],
                },
            },
        ],
    );
    let mut assets = i.assets.clone();
    assets.insert(format!("asset_{}", sha(REPLACEMENT)), REPLACEMENT.to_vec());
    let edited = update(
        EXTERNAL,
        &i.document,
        &i.bindings,
        &next,
        &assets,
        i.document_id,
        1,
    )
    .unwrap();
    let parts = read_package(&edited).unwrap();
    let original = read_package(EXTERNAL).unwrap();
    let slide = xml_part(&parts, "ppt/slides/slide1.xml");
    assert!(slide.contains("Updated cell"));
    assert!(slide.contains("urn:forge-test"));
    assert!(slide.contains("Edited external title"));
    for (p, b) in &original {
        if ![
            "ppt/slides/slide1.xml",
            "ppt/slides/_rels/slide1.xml.rels",
            "ppt/charts/chart1.xml",
            "ppt/embeddings/Microsoft_Excel_Sheet1.xlsx",
            "_rels/.rels",
            "[Content_Types].xml",
        ]
        .contains(&p.as_str())
        {
            assert_eq!(parts[p], *b, "preserved {p}");
        }
    }
    let old_xml = roxmltree::Document::parse(
        std::str::from_utf8(&original["ppt/slides/slide1.xml"]).unwrap(),
    )
    .unwrap();
    let new_xml = roxmltree::Document::parse(&slide).unwrap();
    let ns = "http://schemas.openxmlformats.org/presentationml/2006/main";
    let group = old_xml
        .descendants()
        .find(|n| n.has_tag_name((ns, "grpSp")))
        .unwrap();
    let new_group = new_xml
        .descendants()
        .find(|n| n.has_tag_name((ns, "grpSp")))
        .unwrap();
    assert_eq!(
        &old_xml.input_text()[group.range()],
        &slide[new_group.range()]
    );
}
#[test]
fn stale_metadata_and_unsafe_packages_fail_closed() {
    let mut p: Presentation = parse(include_bytes!(
        "../../forge-tree-doc/examples/overview.json"
    ))
    .unwrap();
    p.assign_ids();
    let bytes = generate(&p, &Assets::new(), Uuid::now_v7(), 0).unwrap();
    let mut parts = read_package(&bytes).unwrap();
    parts.get_mut("ppt/slides/slide1.xml").unwrap().push(b' ');
    assert_eq!(
        import(&write_package(&parts).unwrap()).unwrap_err().code,
        ErrorCode::StaleMetadata
    );
    parts.insert("_xmlsignatures/sig1.xml".into(), b"<signature/>".to_vec());
    assert!(read_package(&write_package(&parts).unwrap()).is_err());
    assert!(read_package(b"PK broken archive").is_err());
    assert!(validate_image(&PNG[..30]).is_err());
}

#[test]
fn namespace_prefixes_and_unbound_extensions_survive_text_edits() {
    let mut parts = read_package(EXTERNAL).unwrap();
    let path = "ppt/slides/slide1.xml";
    let slide = xml_part(&parts, path)
        .replace("xmlns:p=", "xmlns:slide=")
        .replace("<p:", "<slide:")
        .replace("</p:", "</slide:")
        .replace("xmlns:a=", "xmlns:draw=")
        .replace("<a:", "<draw:")
        .replace("</a:", "</draw:")
        .replace(
            "</slide:spTree>",
            "<extra:opaque xmlns:extra=\"urn:forge-test\" keep=\"yes\"/></slide:spTree>",
        );
    parts.insert(path.into(), slide.into_bytes());
    let source = write_package(&parts).unwrap();
    let i = import(&source).unwrap();
    let next = patch(
        &i,
        vec![Operation::SetText {
            target: target(&i.document, NodeKind::Text),
            text: "Prefixes preserved".into(),
            cell: None,
        }],
    );
    let edited = update(
        &source,
        &i.document,
        &i.bindings,
        &next,
        &i.assets,
        i.document_id,
        1,
    )
    .unwrap();
    let text = xml_part(&read_package(&edited).unwrap(), path);
    assert!(text.contains("<extra:opaque"));
    assert!(text.contains("Prefixes preserved"));
    assert!(text.contains("xmlns:slide="));
    let next = patch(
        &i,
        vec![Operation::InsertNode {
            parent: key("slide-1.canvas"),
            index: 0,
            node: Box::new(Node {
                kind: NodeKind::Shape,
                frame: Some(Frame {
                    x: 500.,
                    y: 450.,
                    width: 20.,
                    height: 20.,
                }),
                ..Default::default()
            }),
        }],
    );
    assert_eq!(
        update(
            &source,
            &i.document,
            &i.bindings,
            &next,
            &i.assets,
            i.document_id,
            1
        )
        .unwrap_err()
        .code,
        ErrorCode::UnsupportedEdit
    );
}
#[test]
fn structure_and_placeholder_binding_preserve_groups_and_native_ids() {
    let i = import(EXTERNAL).unwrap();
    let next = patch(
        &i,
        vec![Operation::InsertNode {
            parent: key("slide-1.canvas"),
            index: 0,
            node: Box::new(Node {
                kind: NodeKind::Text,
                key: Some("new-placeholder".into()),
                placeholder_ref: Some("0".into()),
                text: Some("New placeholder".into()),
                frame: Some(Frame {
                    x: 20.,
                    y: 20.,
                    width: 300.,
                    height: 40.,
                }),
                ..Default::default()
            }),
        }],
    );
    let result = update(
        EXTERNAL,
        &i.document,
        &i.bindings,
        &next,
        &i.assets,
        i.document_id,
        1,
    )
    .unwrap();
    let imported = import(&result).unwrap();
    let mut native_ids = std::collections::HashSet::new();
    let package = read_package(&result).unwrap();
    let text = xml_part(&package, "ppt/slides/slide1.xml");
    let native = roxmltree::Document::parse(&text).unwrap();
    for element in native
        .descendants()
        .filter(|n| n.tag_name().name() == "cNvPr")
    {
        assert!(native_ids.insert(element.attribute("id").unwrap()));
    }
    let next = patch(
        &imported,
        vec![
            Operation::MoveNode {
                target: key("new-placeholder"),
                parent: key("slide-1.canvas"),
                index: 2,
            },
            Operation::RemoveNode {
                target: key("new-placeholder"),
            },
        ],
    );
    let result = update(
        &result,
        &imported.document,
        &imported.bindings,
        &next,
        &i.assets,
        i.document_id,
        2,
    )
    .unwrap();
    assert!(
        import(&result)
            .unwrap()
            .document
            .find(&key("new-placeholder"))
            .is_none()
    );
}
#[test]
fn malformed_xml_and_zip_expansion_limits_are_rejected() {
    use std::io::{Cursor, Write};

    use zip::write::SimpleFileOptions;
    for filename in ["../outside.xml", "/absolute.xml", "safe.xml"] {
        let mut writer = zip::ZipWriter::new(Cursor::new(Vec::new()));
        writer
            .start_file(filename, SimpleFileOptions::default())
            .unwrap();
        writer
            .write_all(b"<!DOCTYPE x [<!ENTITY content 'x'>]><x>&content;</x>")
            .unwrap();
        let bytes = writer.finish().unwrap().into_inner();
        assert!(read_package(&bytes).is_err());
    }
    let mut writer = zip::ZipWriter::new(Cursor::new(Vec::new()));
    writer
        .start_file(
            "bomb.bin",
            SimpleFileOptions::default().compression_method(zip::CompressionMethod::Deflated),
        )
        .unwrap();
    let chunk = vec![0u8; 1024 * 1024];
    for _ in 0..65 {
        writer.write_all(&chunk).unwrap();
    }
    let bytes = writer.finish().unwrap().into_inner();
    assert_eq!(
        read_package(&bytes).unwrap_err().code,
        ErrorCode::ResourceLimit
    );
}

#[test]
fn horizontal_bars_and_series_resize_keep_cache_and_workbook_in_sync() {
    let mut p: Presentation = parse(include_bytes!(
        "../../forge-tree-doc/examples/all-nodes.json"
    ))
    .unwrap();
    let mut chart = p.find(&key("details.chart")).unwrap().clone();
    chart.orientation = Orientation::Horizontal;
    chart.frame = Some(Frame {
        x: 20.,
        y: 20.,
        width: 500.,
        height: 350.,
    });
    p.assets.clear();
    p.slides.truncate(1);
    p.slides[0].content = Node {
        kind: NodeKind::Canvas,
        children: vec![chart],
        ..Default::default()
    };
    p.assign_ids();
    let bytes = generate(&p, &Assets::new(), Uuid::now_v7(), 0).unwrap();
    let imported = import(&bytes).unwrap();
    let mut data = p.find(&key("details.chart")).unwrap().data.clone().unwrap();
    data.series.push(Series {
        key: "extra".into(),
        name: "Third series".into(),
        values: vec![40., 50., 60.],
    });
    let next = patch(
        &imported,
        vec![Operation::SetChartData {
            target: key("details.chart"),
            data,
        }],
    );
    let updated = update(
        &bytes,
        &p,
        &imported.bindings,
        &next,
        &Assets::new(),
        imported.document_id,
        1,
    )
    .unwrap();
    let parts = read_package(&updated).unwrap();
    let chart = parts
        .iter()
        .find(|(name, _)| name.starts_with("ppt/charts/") && name.ends_with(".xml"))
        .unwrap();
    let text = std::str::from_utf8(chart.1).unwrap();
    assert!(text.contains("<c:barDir val=\"bar\"/>"));
    let native = roxmltree::Document::parse(text).unwrap();
    assert!(text.contains("Sheet1!$D$2:$D$4"));
    assert_eq!(
        native
            .descendants()
            .filter(|n| n.tag_name().name() == "ser")
            .count(),
        3
    );
    let workbook = parts
        .iter()
        .find(|(name, _)| name.ends_with(".xlsx"))
        .unwrap();
    let workbook = read_package(workbook.1).unwrap();
    assert!(xml_part(&workbook, "xl/worksheets/sheet1.xml").contains("Third series"));
}
#[test]
fn moving_existing_chart_preserves_chart_workbook_and_relationship_bytes() {
    let imported = import(EXTERNAL).unwrap();
    let chart = target(&imported.document, NodeKind::Chart);
    let frame = Frame {
        x: 550.,
        y: 140.,
        width: 350.,
        height: 250.,
    };
    let next = patch(
        &imported,
        vec![Operation::SetFrame {
            target: chart.clone(),
            frame,
        }],
    );
    let output = update(
        EXTERNAL,
        &imported.document,
        &imported.bindings,
        &next,
        &imported.assets,
        imported.document_id,
        1,
    )
    .unwrap();
    let original = read_package(EXTERNAL).unwrap();
    let result = read_package(&output).unwrap();
    for (path, bytes) in &original {
        if path.starts_with("ppt/charts/")
            || path.starts_with("ppt/embeddings/")
            || path == "ppt/slides/_rels/slide1.xml.rels"
        {
            assert_eq!(result[path], *bytes, "preserved {path}");
        }
    }
    assert!(!result.keys().any(|p| p.starts_with("ppt/charts/forge-")));
    assert_eq!(
        import(&output)
            .unwrap()
            .document
            .find(&chart)
            .unwrap()
            .frame,
        Some(frame)
    );
    let slide = xml_part(&result, "ppt/slides/slide1.xml");
    let xml = roxmltree::Document::parse(&slide).unwrap();
    let chart_id = imported.bindings[&chart.node_id.unwrap()]
        .shape_id
        .to_string();
    let native = xml
        .descendants()
        .find(|n| n.tag_name().name() == "cNvPr" && n.attribute("id") == Some(chart_id.as_str()))
        .unwrap()
        .parent()
        .unwrap()
        .parent()
        .unwrap();
    let offset = native
        .descendants()
        .find(|n| n.tag_name().name() == "off")
        .unwrap();
    assert_eq!(
        offset.attribute("x").unwrap().parse::<i64>().unwrap(),
        550 * 12700
    );
    assert_eq!(
        offset.attribute("y").unwrap().parse::<i64>().unwrap(),
        140 * 12700
    );
}
#[test]
fn chart_data_edits_reject_workbook_row_child_extensions() {
    let mut parts = read_package(EXTERNAL).unwrap();
    let workbook_path = "ppt/embeddings/Microsoft_Excel_Sheet1.xlsx";
    let original = read_package(&parts[workbook_path]).unwrap();
    let sheet_path = "xl/worksheets/sheet1.xml";
    for extension in [
        "<extLst><ext uri=\"urn:preserved\"/></extLst>",
        "<x:metadata xmlns:x=\"urn:preserved\"/>",
    ] {
        let mut workbook = original.clone();
        let sheet = xml_part(&workbook, sheet_path);
        assert!(sheet.contains("</row>"));
        workbook.insert(
            sheet_path.into(),
            sheet
                .replacen("</row>", &format!("{extension}</row>"), 1)
                .into_bytes(),
        );
        let workbook_bytes = write_package(&workbook).unwrap();
        parts.insert(workbook_path.into(), workbook_bytes.clone());
        let source = write_package(&parts).unwrap();
        let imported = import(&source).unwrap();
        let chart = target(&imported.document, NodeKind::Chart);
        let mut data = imported
            .document
            .find(&chart)
            .unwrap()
            .data
            .clone()
            .unwrap();
        data.series[0].values[0] += 1.;
        let next = patch(
            &imported,
            vec![Operation::SetChartData {
                target: chart,
                data,
            }],
        );
        assert_eq!(
            update(
                &source,
                &imported.document,
                &imported.bindings,
                &next,
                &imported.assets,
                imported.document_id,
                1
            )
            .unwrap_err()
            .code,
            ErrorCode::UnsupportedEdit
        );
        let next = patch(
            &imported,
            vec![Operation::SetText {
                target: target(&imported.document, NodeKind::Text),
                text: "Unrelated edit".into(),
                cell: None,
            }],
        );
        let output = update(
            &source,
            &imported.document,
            &imported.bindings,
            &next,
            &imported.assets,
            imported.document_id,
            1,
        )
        .unwrap();
        assert_eq!(
            read_package(&output).unwrap()[workbook_path],
            workbook_bytes
        );
    }
}
#[test]
fn chart_insertion_never_overwrites_preexisting_package_parts() {
    let id = Uuid::now_v7();
    let stem = format!("ppt/charts/forge-{}", id.simple());
    let original = read_package(EXTERNAL).unwrap();
    let authored: Presentation = parse(include_bytes!(
        "../../forge-tree-doc/examples/all-nodes.json"
    ))
    .unwrap();
    let mut chart = authored.find(&key("details.chart")).unwrap().clone();
    chart.id = Some(id);
    let collisions = [
        (format!("{stem}.xml"), original["ppt/charts/chart1.xml"].clone()),
        (format!("{stem}.xlsx"), original["ppt/embeddings/Microsoft_Excel_Sheet1.xlsx"].clone()),
        (format!("ppt/charts/_rels/forge-{}.xml.rels", id.simple()), b"<Relationships xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\"/>".to_vec()),
        (format!("{}.xml", stem.replace("forge-", "FORGE-")), original["ppt/charts/chart1.xml"].clone()),
    ];
    for collision in std::iter::once(None).chain(collisions.into_iter().map(Some)) {
        let mut parts = original.clone();
        if let Some((path, bytes)) = &collision {
            parts.insert(path.clone(), bytes.clone());
        }
        let source = write_package(&parts).unwrap();
        let imported = import(&source).unwrap();
        let next = patch(
            &imported,
            vec![Operation::InsertNode {
                parent: Target {
                    key: None,
                    node_id: imported.document.slides[0].content.id,
                },
                index: 1,
                node: Box::new(chart.clone()),
            }],
        );
        let result = update(
            &source,
            &imported.document,
            &imported.bindings,
            &next,
            &imported.assets,
            imported.document_id,
            1,
        );
        if collision.is_some() {
            assert_eq!(result.unwrap_err().code, ErrorCode::UnsupportedEdit);
        } else {
            let result = result.unwrap();
            assert!(
                import(&result)
                    .unwrap()
                    .document
                    .find(&Target {
                        key: None,
                        node_id: Some(id)
                    })
                    .is_some()
            );
            assert!(
                read_package(&result)
                    .unwrap()
                    .contains_key(&format!("{stem}.xml"))
            );
        }
    }
}
