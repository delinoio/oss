use forge_document::{Assets, Run, Style, fonts::Fonts};
use forge_pdf::*;
use forge_tree_doc::ErrorCode;
use uuid::Uuid;
fn id() -> Uuid {
    Uuid::now_v7()
}
fn runs(text: &str) -> Vec<Run> {
    vec![Run {
        text: text.into(),
        ..Default::default()
    }]
}
fn paragraph(text: &str) -> Block {
    Block::Paragraph {
        id: id(),
        style: Style::default(),
        heading: None,
        runs: runs(text),
    }
}
fn document(blocks: Vec<Block>) -> Document {
    Document {
        id: id(),
        title: Some("Semantic report".into()),
        language: "en-US".into(),
        pages: vec![Page {
            id: id(),
            width: 300.0,
            height: 250.0,
            margin: 20.0,
            blocks,
        }],
    }
}
fn fonts() -> Fonts {
    Fonts::new(false, &[forge_tree_doc::FONT_BYTES.to_vec()]).unwrap()
}

#[test]
fn tagged_flow_splits_paragraphs_tables_and_repeats_headers() {
    let mut blocks = vec![Block::Paragraph {
        id: id(),
        style: Style::default(),
        heading: Some(1),
        runs: runs("Heading"),
    }];
    blocks.push(paragraph(&"Paragraph flows across pages. ".repeat(35)));
    blocks.push(Block::List {
        id: id(),
        ordered: true,
        items: vec![
            ListItem {
                id: id(),
                style: Style::default(),
                runs: runs("First list item"),
            },
            ListItem {
                id: id(),
                style: Style::default(),
                runs: runs("Second list item"),
            },
        ],
    });
    let mut rows = vec![Row {
        id: id(),
        header: true,
        cells: vec![
            Cell {
                id: id(),
                style: Style::default(),
                runs: runs("Repeated header"),
            },
            Cell {
                id: id(),
                style: Style::default(),
                runs: runs("Value"),
            },
        ],
    }];
    for i in 0..8 {
        rows.push(Row {
            id: id(),
            header: false,
            cells: vec![
                Cell {
                    id: id(),
                    style: Style::default(),
                    runs: runs(&format!("Row {i} {}", "long cell ".repeat(10))),
                },
                Cell {
                    id: id(),
                    style: Style::default(),
                    runs: runs(&format!("{i}")),
                },
            ],
        });
    }
    blocks.push(Block::Table {
        id: id(),
        columns: vec![150.0, 110.0],
        rows,
    });
    blocks.push(Block::Paragraph {
        id: id(),
        style: Style::default(),
        heading: None,
        runs: vec![Run {
            text: "Accessible link".into(),
            hyperlink: Some("https://example.com".into()),
            style: Style::default(),
        }],
    });
    blocks.push(Block::Shape {
        id: id(),
        kind: ShapeKind::Ellipse,
        width: 80.0,
        height: 40.0,
        fill: Some("#1199AA".into()),
        stroke: None,
        alt: Some("An ellipse".into()),
    });
    let doc = document(blocks);
    let (bytes, layout) = generate(&doc, &Assets::new(), &mut fonts()).unwrap();
    if let Ok(path) = std::env::var("FORGE_PDF_TEST_OUTPUT") {
        std::fs::write(path, &bytes).unwrap();
    }
    let pdf = lopdf::Document::load_mem(&bytes).unwrap();
    assert!(pdf.get_pages().len() > 5);
    let text = actual_text(&pdf);
    assert!(text.contains("Accessible link"));
    assert!(text.contains("Repeated header"));
    assert!(
        pdf.get_pages()
            .values()
            .filter(
                |id| String::from_utf8_lossy(&pdf.get_page_content(**id).unwrap())
                    .contains("/Subtype /Header")
            )
            .count()
            > 1
    );
    let debug = format!("{:?}", pdf.objects);
    for tag in [
        "StructTreeRoot",
        "H1",
        "Table",
        "TH",
        "TD",
        "LBody",
        "Lbl",
        "Link",
        "Figure",
        "Lang",
    ] {
        assert!(debug.contains(tag), "missing {tag}");
    }
    assert!(
        layout
            .nodes
            .values()
            .any(|node| node.fragments.iter().map(|f| f.page).max()
                != node.fragments.iter().map(|f| f.page).min())
    );
    if let Ok(path) = std::env::var("FORGE_PDF_TEST_OUTPUT") {
        std::fs::write(path, bytes).unwrap();
    }
}
#[test]
fn oversized_indivisible_objects_and_lines_fail_before_publication() {
    let doc = document(vec![Block::Shape {
        id: id(),
        kind: ShapeKind::Rectangle,
        width: 100.0,
        height: 500.0,
        fill: None,
        stroke: None,
        alt: None,
    }]);
    assert_eq!(
        generate(&doc, &Assets::new(), &mut fonts())
            .err()
            .unwrap()
            .code,
        ErrorCode::TextOverflow
    );
    let doc = document(vec![Block::Paragraph {
        id: id(),
        style: Style {
            font_size: Some(300.0),
            ..Default::default()
        },
        heading: None,
        runs: runs("A"),
    }]);
    assert_eq!(
        generate(&doc, &Assets::new(), &mut fonts())
            .err()
            .unwrap()
            .code,
        ErrorCode::TextOverflow
    );
}
#[test]
#[cfg(target_os = "macos")]
fn native_pdf_embeds_system_cjk_rtl_and_color_emoji() {
    let doc = document(vec![
        paragraph("Latin 한국어 日本語 中文"),
        paragraph("Hello مرحبا שלום 123"),
        paragraph("😀 👨‍👩‍👧‍👦"),
    ]);
    let (bytes, _) = generate(&doc, &Assets::new(), &mut Fonts::new(true, &[]).unwrap()).unwrap();
    if let Ok(path) = std::env::var("FORGE_PDF_FONT_TEST_OUTPUT") {
        std::fs::write(path, &bytes).unwrap();
    }
    let pdf = lopdf::Document::load_mem(&bytes).unwrap();
    let text = actual_text(&pdf);
    assert!(text.contains("한국어"));
    assert!(text.contains("😀"));
    assert!(format!("{:?}", pdf.objects).contains("Type3"));
}

// lopdf 0.38 cannot parse the Type-0 Unicode CMap emitted by pdf-writer 0.14.
// Read the explicit ActualText entries here; the independent rendering suite
// also verifies extraction with Poppler and pypdf against the final bytes.
fn actual_text(pdf: &lopdf::Document) -> String {
    let mut text = String::new();
    for id in pdf.get_pages().values() {
        let data = pdf.get_page_content(*id).unwrap();
        let content = lopdf::content::Content::decode(&data).unwrap();
        for op in content.operations {
            if op.operator == "BDC" {
                if let Some(dict) = op.operands.last().and_then(|o| o.as_dict().ok()) {
                    if let Ok(value) = dict.get(b"ActualText") {
                        let bytes = value.as_str().unwrap();
                        if bytes.starts_with(&[0xfe, 0xff]) {
                            text.push_str(
                                &String::from_utf16(
                                    &bytes[2..]
                                        .chunks_exact(2)
                                        .map(|b| u16::from_be_bytes([b[0], b[1]]))
                                        .collect::<Vec<_>>(),
                                )
                                .unwrap(),
                            );
                        } else {
                            text.push_str(std::str::from_utf8(bytes).unwrap());
                        }
                    }
                }
            }
        }
    }
    text
}
