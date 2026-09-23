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
    let wb = parts.iter().find(|(p, _)| p.ends_with(".xlsx")).unwrap();
    let wb = read_package(wb.1).unwrap();
    let sheet = xml_part(&wb, "xl/worksheets/sheet1.xml");
    assert!(sheet.contains("New A"));
    assert!(sheet.contains(">99<"));
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
