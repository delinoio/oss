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
fn paragraph_backgrounds_inherit_into_runs_and_local_backgrounds_override_them() {
    let shaded = Block::Paragraph {
        id: Uuid::now_v7(),
        style: Style {
            background: Some("#CCEEFF".into()),
            ..Default::default()
        },
        heading: None,
        list: None,
        runs: vec![
            Run {
                text: "Inherited background".into(),
                ..Default::default()
            },
            Run {
                text: "Local background".into(),
                style: Style {
                    background: Some("#FFCCAA".into()),
                    ..Default::default()
                },
                ..Default::default()
            },
        ],
    };
    let generated = generate(
        &document(vec![shaded.clone(), paragraph("Plain")]),
        &Assets::new(),
    )
    .unwrap();
    let imported = import(include_bytes!("fixtures/external.docx")).unwrap();
    let target = imported
        .targets
        .iter()
        .find(|t| t.text == "External paragraph")
        .unwrap();
    let edited = replace(
        &imported,
        &[(target.id, vec![shaded, paragraph("Plain")])],
        &Assets::new(),
    )
    .unwrap();
    for bytes in [generated, edited] {
        let parts = read(&bytes).unwrap();
        let main = xml(&parts["word/document.xml"]).unwrap();
        for (text, color) in [
            ("Inherited background", Some("CCEEFF")),
            ("Local background", Some("FFCCAA")),
            ("Plain", None),
        ] {
            let run = main
                .descendants()
                .find(|n| n.has_tag_name((W, "t")) && n.text() == Some(text))
                .unwrap()
                .parent()
                .unwrap();
            let shading = run.descendants().find(|n| n.has_tag_name((W, "shd")));
            assert_eq!(shading.and_then(|n| n.attribute((W, "fill"))), color);
            if let Some(shading) = shading {
                assert_eq!(shading.attribute((W, "val")), Some("clear"));
                assert_eq!(shading.attribute((W, "color")), Some("auto"));
            }
        }
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
                bold: Some(true),
                ..Default::default()
            },
            runs: vec![Run {
                text: "Heading and link".into(),
                hyperlink: Some("https://example.com/document".into()),
                style: Style {
                    italic: Some(true),
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
        assert!(
            chart
                .descendants()
                .any(|node| node.has_tag_name((forge_package::A, "srgbClr"))),
            "standalone Word chart series need explicit colors"
        );
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

#[test]
fn external_chart_replacements_allocate_drawing_and_list_ids_without_restyling_originals() {
    let original = import(include_bytes!("fixtures/charts.docx")).unwrap();
    let targets: Vec<_> = original
        .targets
        .iter()
        .filter(|t| t.kind == TargetKind::Chart)
        .collect();
    assert_eq!(targets.len(), 3);
    for (target, kind) in targets
        .iter()
        .zip([ChartKind::Bar, ChartKind::Line, ChartKind::Pie])
    {
        let list = Block::Paragraph {
            id: Uuid::now_v7(),
            style: Style::default(),
            heading: None,
            list: Some(List {
                kind: ListKind::Number,
                level: 0,
            }),
            runs: vec![Run {
                text: "New numbered item".into(),
                ..Default::default()
            }],
        };
        let chart = Block::Chart {
            id: Uuid::now_v7(),
            width: 300.0,
            height: 180.0,
            alt: "Updated data".into(),
            chart: Chart {
                kind,
                title: Some("Updated external chart".into()),
                categories: vec!["Replacement".into()],
                series: vec![Series {
                    name: "New values".into(),
                    values: vec![23.0],
                }],
                legend: true,
                labels: true,
            },
        };
        let output = replace(&original, &[(target.id, vec![list, chart])], &Assets::new()).unwrap();
        let parts = read(&output).unwrap();
        let main = &parts[&original.main];
        assert!(main.starts_with(&original.parts[&original.main][..target.region.range.start]));
        assert!(main.ends_with(&original.parts[&original.main][target.region.range.end..]));
        for (name, bytes) in &original.parts {
            if name != &original.main
                && name != &forge_package::relation_path(&original.main)
                && name != "[Content_Types].xml"
                && name != "word/numbering.xml"
            {
                assert_eq!(parts[name], *bytes, "{name}");
            }
        }
        let main = xml(main).unwrap();
        let mut ids = std::collections::HashSet::new();
        for node in main.descendants().filter(|n| {
            n.has_tag_name((
                "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing",
                "docPr",
            ))
        }) {
            assert!(
                ids.insert(node.attribute("id").unwrap()),
                "drawing IDs must remain unique"
            );
        }
        let old = xml(&original.parts["word/numbering.xml"]).unwrap();
        let new = xml(&parts["word/numbering.xml"]).unwrap();
        let source = std::str::from_utf8(&original.parts["word/numbering.xml"]).unwrap();
        let updated = std::str::from_utf8(&parts["word/numbering.xml"]).unwrap();
        for child in old.root_element().children().filter(|n| n.is_element()) {
            assert!(updated.contains(&source[child.range()]));
        }
        let num_id = main
            .descendants()
            .find(|n| n.has_tag_name((W, "numId")))
            .unwrap()
            .attribute((W, "val"))
            .unwrap();
        assert!(
            !old.descendants()
                .any(|n| n.has_tag_name((W, "num")) && n.attribute((W, "numId")) == Some(num_id))
        );
        assert!(
            new.descendants()
                .any(|n| n.has_tag_name((W, "num")) && n.attribute((W, "numId")) == Some(num_id))
        );
        assert_eq!(
            import(&output)
                .unwrap()
                .targets
                .iter()
                .filter(|t| t.kind == TargetKind::Chart)
                .count(),
            3
        );
    }
}

#[test]
fn cell_wrapper_and_empty_section_header_boundaries_are_preserved() {
    let mut parts = read(include_bytes!("fixtures/external.docx")).unwrap();
    let original = String::from_utf8(parts["word/document.xml"].clone())
        .unwrap()
        .replacen(
            "<w:tc>",
            "<w:tc w:rsidR=\"01020304\" xmlns:retained=\"urn:retained\">",
            1,
        );
    parts.insert("word/document.xml".into(), original.into_bytes());
    let input = forge_package::write(&parts).unwrap();
    let imported = import(&input).unwrap();
    let target = imported
        .targets
        .iter()
        .find(|t| t.kind == TargetKind::Cell)
        .unwrap();
    let updated = read(
        &replace(
            &imported,
            &[(target.id, vec![paragraph("Replacement")])],
            &Assets::new(),
        )
        .unwrap(),
    )
    .unwrap();
    assert!(
        std::str::from_utf8(&updated["word/document.xml"])
            .unwrap()
            .contains("<w:tc w:rsidR=\"01020304\" xmlns:retained=\"urn:retained\">")
    );
    let mut model = document(vec![paragraph("First section")]);
    model.sections.push(Section {
        width: 612.0,
        height: 792.0,
        margin: 72.0,
        header: vec![],
        footer: vec![],
        blocks: vec![paragraph("Second section")],
    });
    let parts = read(&generate(&model, &Assets::new()).unwrap()).unwrap();
    for path in ["word/header2.xml", "word/footer2.xml"] {
        let parsed = xml(&parts[path]).unwrap();
        assert!(parsed.descendants().any(|n| n.has_tag_name((W, "p"))));
        assert!(!parsed.descendants().any(|n| n.has_tag_name((W, "t"))));
    }
}

#[test]
fn foreign_paragraph_attributes_and_drawing_bookmarks_remain_opaque() {
    let mut parts = read(include_bytes!("fixtures/charts.docx")).unwrap();
    let original = std::str::from_utf8(&parts["word/document.xml"]).unwrap();
    let parsed = xml(original.as_bytes()).unwrap();
    let text_paragraph = parsed
        .descendants()
        .find(|n| {
            n.has_tag_name((W, "p")) && n.descendants().any(|child| child.has_tag_name((W, "t")))
        })
        .unwrap();
    let picture = parsed
        .descendants()
        .find(|n| {
            n.has_tag_name((W, "p"))
                && n.descendants()
                    .any(|child| child.has_tag_name((forge_package::A, "blip")))
        })
        .unwrap();
    let mut modified = original.to_string();
    let mut replacements = vec![
        (
            text_paragraph.range(),
            original[text_paragraph.range()].replacen(
                "<w:p",
                "<w:p xmlns:custom=\"urn:custom\" custom:meaning=\"protected\"",
                1,
            ),
        ),
        (
            picture.range(),
            original[picture.range()].replacen(
                "</w:p>",
                "<w:bookmarkStart w:id=\"919\" w:name=\"protected\"/><w:bookmarkEnd \
                 w:id=\"919\"/></w:p>",
                1,
            ),
        ),
    ];
    replacements.sort_by_key(|(range, _)| std::cmp::Reverse(range.start));
    for (range, replacement) in replacements {
        modified.replace_range(range, &replacement);
    }
    parts.insert("word/document.xml".into(), modified.into_bytes());
    let input = forge_package::write(&parts).unwrap();
    let imported = import(&input).unwrap();
    let main = std::str::from_utf8(&imported.parts["word/document.xml"]).unwrap();
    let protected: Vec<_> = imported
        .targets
        .iter()
        .filter(|target| target.region.part == "word/document.xml")
        .filter(|target| {
            let content = &main[target.region.range.clone()];
            content.contains("custom:meaning") || content.contains("w:bookmarkStart")
        })
        .collect();
    assert_eq!(protected.len(), 2);
    for target in protected {
        assert_eq!(target.kind, TargetKind::Opaque);
        assert!(
            replace(
                &imported,
                &[(target.id, vec![paragraph("Rejected")])],
                &Assets::new()
            )
            .is_err()
        );
    }
    assert_eq!(replace(&imported, &[], &Assets::new()).unwrap(), input);
}

#[test]
fn typed_breaks_stay_opaque_while_line_breaks_remain_editable() {
    for (attributes, editable) in [
        ("", true),
        (" w:type=\"textWrapping\"", true),
        (" w:type=\"page\"", false),
        (" w:type=\"column\"", false),
        (" w:clear=\"all\"", false),
    ] {
        let bytes = generate(
            &document(vec![paragraph("Before"), paragraph("Other")]),
            &Assets::new(),
        )
        .unwrap();
        let mut parts = read(&bytes).unwrap();
        let original = String::from_utf8(parts["word/document.xml"].clone()).unwrap();
        let changed = original.replacen(
            "Before</w:t>",
            &format!("Before</w:t><w:br{attributes}/><w:t>After</w:t>"),
            1,
        );
        parts.insert("word/document.xml".into(), changed.into_bytes());
        let imported = import(&forge_package::write(&parts).unwrap()).unwrap();
        let target = imported
            .targets
            .iter()
            .find(|target| target.text.contains("Before"))
            .unwrap();
        assert_eq!(
            target.kind,
            if editable {
                TargetKind::Paragraph
            } else {
                TargetKind::Opaque
            }
        );
        if !editable {
            assert!(
                replace(
                    &imported,
                    &[(target.id, vec![paragraph("Lost")])],
                    &Assets::new()
                )
                .is_err()
            );
            let other = imported
                .targets
                .iter()
                .find(|target| target.text == "Other")
                .unwrap();
            let output = replace(
                &imported,
                &[(other.id, vec![paragraph("Edited")])],
                &Assets::new(),
            )
            .unwrap();
            let next = read(&output).unwrap();
            let protected = &parts["word/document.xml"][target.region.range.clone()];
            assert!(
                next["word/document.xml"]
                    .windows(protected.len())
                    .any(|window| window == protected)
            );
        }
    }
}

#[test]
fn cell_text_styles_cascade_through_nested_blocks_with_explicit_overrides() {
    let doc: Document = serde_json::from_value(serde_json::json!({"sections":[{"blocks":[
        {"type":"table","columns":[240],"rows":[{"cells":[{
            "style":{"font_family":"Arial","font_size":18,"bold":true,"color":"#112233"},
            "blocks":[
                {"type":"paragraph","runs":[{"text":"Inherited"},{"text":"Run override","style":{"bold":false,"color":"#445566"}}]},
                {"type":"paragraph","style":{"font_size":12},"runs":[{"text":"Paragraph override"}]},
                {"type":"table","columns":[200],"rows":[{"cells":[{"style":{"italic":true},"blocks":[{"type":"paragraph","runs":[{"text":"Nested"}]}]}]}]}
            ]
        }]}]}
    ]}]})).unwrap();
    let parts = read(&generate(&doc, &Assets::new()).unwrap()).unwrap();
    let parsed = xml(&parts["word/document.xml"]).unwrap();
    for (text, size, bold, color) in [
        ("Inherited", "36", "1", "112233"),
        ("Run override", "36", "0", "445566"),
        ("Paragraph override", "24", "1", "112233"),
        ("Nested", "36", "1", "112233"),
    ] {
        let run = parsed
            .descendants()
            .find(|n| n.has_tag_name((W, "t")) && n.text() == Some(text))
            .unwrap()
            .parent()
            .unwrap();
        let property = |name| {
            run.descendants()
                .find(|n| n.has_tag_name((W, name)))
                .unwrap()
        };
        assert_eq!(property("rFonts").attribute((W, "ascii")), Some("Arial"));
        assert_eq!(property("sz").attribute((W, "val")), Some(size));
        assert_eq!(property("b").attribute((W, "val")), Some(bold));
        assert_eq!(property("color").attribute((W, "val")), Some(color));
        if text == "Nested" {
            assert_eq!(property("i").attribute((W, "val")), Some("1"));
        }
    }
}
