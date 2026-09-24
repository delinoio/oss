use forge_document::{Assets, Chart, ChartKind, Run, Series, Style};
use forge_docx::*;
use forge_package::{C, R, W, read, xml};
use uuid::Uuid;

fn paragraph(text: &str) -> Block {
    Block::Paragraph {
        id: Uuid::now_v7(),
        style: Style::default(),
        heading: None,
        list: None,
        runs: vec![Run {
            text: text.into(),
            ..Default::default()
        }],
    }
}

fn document(blocks: Vec<Block>) -> Document {
    Document {
        id: Uuid::now_v7(),
        language: Some("en-US".into()),
        sections: vec![Section {
            width: 612.0,
            height: 792.0,
            margin: 72.0,
            header: vec![paragraph("Header")],
            footer: vec![paragraph("Footer")],
            blocks,
        }],
    }
}

#[test]
fn native_document_contains_sections_rich_text_lists_images_merges_and_editable_charts() {
    let mut blocks = vec![
        Block::Paragraph {
            id: Uuid::now_v7(),
            heading: Some(1),
            list: None,
            style: Style {
                bold: true,
                ..Default::default()
            },
            runs: vec![Run {
                text: "Heading and link".into(),
                hyperlink: Some("https://example.com/document".into()),
                style: Style {
                    italic: true,
                    ..Default::default()
                },
            }],
        },
        Block::Paragraph {
            id: Uuid::now_v7(),
            heading: None,
            style: Style::default(),
            list: Some(List {
                kind: ListKind::Number,
                level: 1,
            }),
            runs: vec![Run {
                text: "Numbered".into(),
                ..Default::default()
            }],
        },
        Block::Table {
            id: Uuid::now_v7(),
            columns: vec![100.0, 100.0],
            rows: vec![
                Row {
                    header: true,
                    cells: vec![Cell {
                        row_span: 1,
                        col_span: 2,
                        style: Style::default(),
                        blocks: vec![paragraph("Merged header")],
                    }],
                },
                Row {
                    header: false,
                    cells: vec![
                        Cell {
                            row_span: 1,
                            col_span: 1,
                            style: Style::default(),
                            blocks: vec![paragraph("A")],
                        },
                        Cell {
                            row_span: 1,
                            col_span: 1,
                            style: Style::default(),
                            blocks: vec![paragraph("B")],
                        },
                    ],
                },
            ],
        },
        Block::PageBreak { id: Uuid::now_v7() },
    ];
    let mut assets = Assets::new();
    assets.insert(
        "image".into(),
        include_bytes!("../../forge-pptx/tests/fixtures/sample.png").to_vec(),
    );
    blocks.push(Block::Image {
        id: Uuid::now_v7(),
        asset: "image".into(),
        width: 40.0,
        height: 30.0,
        alt: "Example image".into(),
    });
    for kind in [ChartKind::Bar, ChartKind::Line, ChartKind::Pie] {
        blocks.push(Block::Chart {
            id: Uuid::now_v7(),
            width: 300.0,
            height: 180.0,
            alt: "Editable chart".into(),
            chart: Chart {
                kind,
                title: Some("Data".into()),
                categories: vec!["A".into(), "B".into()],
                series: vec![Series {
                    name: "Values".into(),
                    values: vec![2.0, 5.0],
                }],
                legend: true,
                labels: true,
            },
        });
    }
    let mut model = document(blocks);
    model.sections.push(Section {
        width: 792.0,
        height: 612.0,
        margin: 60.0,
        header: vec![],
        footer: vec![],
        blocks: vec![paragraph("Second section")],
    });
    let bytes = generate(&model, &assets).unwrap();
    let parts = read(&bytes).unwrap();
    let main = xml(&parts["word/document.xml"]).unwrap();
    assert_eq!(
        main.descendants()
            .filter(|n| n.has_tag_name((W, "sectPr")))
            .count(),
        2
    );
    assert!(
        main.descendants()
            .any(|n| n.has_tag_name((W, "gridSpan")) && n.attribute((W, "val")) == Some("2"))
    );
    assert!(main.descendants().any(|n| n.has_tag_name((W, "tblHeader"))));
    assert_eq!(
        parts
            .keys()
            .filter(|p| p.starts_with("word/embeddings/"))
            .count(),
        3
    );
    for (name, bytes) in parts
        .iter()
        .filter(|(p, _)| p.starts_with("word/charts/") && p.ends_with(".xml"))
    {
        let chart = xml(bytes).unwrap();
        let external = chart
            .descendants()
            .find(|n| n.has_tag_name((C, "externalData")))
            .unwrap();
        let workbook =
            forge_package::related(&parts, name, external.attribute((R, "id")).unwrap()).unwrap();
        let data = read(&parts[&workbook]).unwrap();
        assert!(
            std::str::from_utf8(&data["xl/worksheets/sheet1.xml"])
                .unwrap()
                .contains("<v>5</v>")
        );
    }
    let imported = import(&bytes).unwrap();
    assert_eq!(
        imported
            .targets
            .iter()
            .filter(|t| t.kind == TargetKind::Chart)
            .count(),
        3
    );
}

#[test]
fn external_document_edits_preserve_unselected_parts_and_xml_and_opaque_regions() {
    let bytes = include_bytes!("fixtures/external.docx");
    let imported = import(bytes).unwrap();
    let target = imported
        .targets
        .iter()
        .find(|t| t.kind == TargetKind::Paragraph && t.text == "External paragraph")
        .unwrap();
    let old = imported.parts[&target.region.part].clone();
    let output = replace(
        &imported,
        &[(target.id, vec![paragraph("Changed paragraph")])],
        &Assets::new(),
    )
    .unwrap();
    let parts = read(&output).unwrap();
    let new = &parts[&target.region.part];
    assert_eq!(
        &new[..target.region.range.start],
        &old[..target.region.range.start]
    );
    assert!(new.ends_with(&old[target.region.range.end..]));
    for (name, bytes) in &imported.parts {
        if name != &target.region.part {
            assert_eq!(&parts[name], bytes, "{name}");
        }
    }
    let edited = import(&output).unwrap();
    assert!(edited.targets.iter().any(|t| t.text == "Changed paragraph"));
    let opaque = imported
        .targets
        .iter()
        .find(|t| t.kind == TargetKind::Opaque && t.text.contains("Protected equation"))
        .unwrap();
    assert!(
        replace(
            &imported,
            &[(opaque.id, vec![paragraph("lost")])],
            &Assets::new()
        )
        .is_err()
    );
    let table = imported
        .targets
        .iter()
        .find(|t| t.kind == TargetKind::Table)
        .unwrap();
    let cell = imported
        .targets
        .iter()
        .find(|t| t.kind == TargetKind::Cell && t.region.overlaps(&table.region))
        .unwrap();
    assert!(
        replace(
            &imported,
            &[(table.id, vec![]), (cell.id, vec![])],
            &Assets::new()
        )
        .is_err()
    );
    let changed = replace(
        &imported,
        &[(cell.id, vec![paragraph("Cell changed")])],
        &Assets::new(),
    )
    .unwrap();
    assert!(
        import(&changed)
            .unwrap()
            .targets
            .iter()
            .any(|t| t.text == "Cell changed")
    );
}

#[test]
fn invalid_merge_and_duplicate_identity_fail_without_mutating_input() {
    let node = paragraph("same");
    let model = document(vec![node.clone(), node]);
    assert!(generate(&model, &Assets::new()).is_err());
    let grid = table_grid(
        &[100.0],
        &[Row {
            header: false,
            cells: vec![Cell {
                row_span: 2,
                col_span: 1,
                style: Style::default(),
                blocks: vec![],
            }],
        }],
    );
    assert!(grid.is_err());
}
