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
            if op.operator == "BDC"
                && let Some(dict) = op.operands.last().and_then(|o| o.as_dict().ok())
                && let Ok(value) = dict.get(b"ActualText")
            {
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
    text
}

#[test]
fn initial_table_headers_stay_with_the_first_body_line() {
    let header = id();
    let body = id();
    let doc = document(vec![
        Block::Shape {
            id: id(),
            kind: ShapeKind::Rectangle,
            width: 100.0,
            height: 160.0,
            fill: None,
            stroke: None,
            alt: None,
        },
        Block::Table {
            id: id(),
            columns: vec![260.0],
            rows: vec![
                Row {
                    id: header,
                    header: true,
                    cells: vec![Cell {
                        id: id(),
                        style: Style::default(),
                        runs: runs("Header"),
                    }],
                },
                Row {
                    id: body,
                    header: false,
                    cells: vec![Cell {
                        id: id(),
                        style: Style::default(),
                        runs: runs("Body"),
                    }],
                },
            ],
        },
    ]);
    let (_, layout) = generate(&doc, &Assets::new(), &mut fonts()).unwrap();
    assert_eq!(layout.nodes[&header].frame.page, 1);
    assert_eq!(
        layout.nodes[&header].frame.page,
        layout.nodes[&body].frame.page
    );
}

#[test]
#[cfg(target_os = "macos")]
fn tagged_reading_order_retains_logical_bidi_runs_and_link_annotations() {
    let doc = document(vec![
        Block::Paragraph {
            id: id(),
            heading: Some(2),
            style: Style::default(),
            runs: runs("First heading"),
        },
        Block::Paragraph {
            id: id(),
            heading: None,
            style: Style::default(),
            runs: vec![
                Run {
                    text: "English ".into(),
                    style: Style::default(),
                    hyperlink: None,
                },
                Run {
                    text: "مرحبا 123 שלום".into(),
                    style: Style {
                        direction: forge_document::Direction::Rtl,
                        language: Some("ar".into()),
                        ..Default::default()
                    },
                    hyperlink: Some("https://example.com".into()),
                },
            ],
        },
        paragraph("Last paragraph"),
    ]);
    let (bytes, _) = generate(&doc, &Assets::new(), &mut Fonts::new(true, &[]).unwrap()).unwrap();
    let pdf = lopdf::Document::load_mem(&bytes).unwrap();
    let root = pdf
        .catalog()
        .unwrap()
        .get(b"StructTreeRoot")
        .unwrap()
        .as_reference()
        .unwrap();
    let mut order = Vec::new();
    fn visit(pdf: &lopdf::Document, object: &lopdf::Object, order: &mut Vec<String>) {
        let object = if let Ok(id) = object.as_reference() {
            pdf.get_object(id).unwrap()
        } else {
            object
        };
        match object {
            lopdf::Object::Array(children) => {
                for child in children {
                    visit(pdf, child, order);
                }
            }
            lopdf::Object::Dictionary(dict) => {
                if let Ok(tag) = dict.get(b"S").and_then(lopdf::Object::as_name) {
                    order.push(String::from_utf8_lossy(tag).into_owned());
                }
                if let Ok(children) = dict.get(b"K") {
                    visit(pdf, children, order);
                }
            }
            _ => {}
        }
    }
    visit(&pdf, pdf.get_object(root).unwrap(), &mut order);
    assert!(
        order.iter().position(|tag| tag == "H2").unwrap()
            < order.iter().position(|tag| tag == "Link").unwrap()
    );
    assert_eq!(order.last().unwrap(), "Span");
    let text = structure_text(&pdf);
    assert!(text.find("First heading").unwrap() < text.find("English").unwrap());
    assert!(text.contains("مرحبا 123 שלום"));
    assert!(text.find("English").unwrap() < text.find("Last paragraph").unwrap());
    let objects = format!("{:?}", pdf.objects);
    assert!(objects.contains("OBJR"));
    assert!(objects.contains("https://example.com"));
}

// Follow the PDF's semantic order, resolving page/MCID pairs. The content
// stream may paint bidi runs in visual order; assistive readers follow /K.
#[cfg(target_os = "macos")]
fn structure_text(pdf: &lopdf::Document) -> String {
    let mut marked = std::collections::BTreeMap::new();
    for page in pdf.get_pages().values() {
        let content =
            lopdf::content::Content::decode(&pdf.get_page_content(*page).unwrap()).unwrap();
        for op in content.operations {
            if op.operator != "BDC" {
                continue;
            }
            let Some(dict) = op.operands.last().and_then(|o| o.as_dict().ok()) else {
                continue;
            };
            if let (Ok(id), Ok(bytes)) = (
                dict.get(b"MCID").and_then(lopdf::Object::as_i64),
                dict.get(b"ActualText").and_then(lopdf::Object::as_str),
            ) {
                let value = if bytes.starts_with(&[0xfe, 0xff]) {
                    String::from_utf16(
                        &bytes[2..]
                            .chunks_exact(2)
                            .map(|b| u16::from_be_bytes([b[0], b[1]]))
                            .collect::<Vec<_>>(),
                    )
                    .unwrap()
                } else {
                    String::from_utf8(bytes.to_vec()).unwrap()
                };
                marked.insert((*page, id), value);
            }
        }
    }
    fn visit(
        pdf: &lopdf::Document,
        value: &lopdf::Object,
        page: Option<lopdf::ObjectId>,
        marked: &std::collections::BTreeMap<(lopdf::ObjectId, i64), String>,
        text: &mut String,
    ) {
        let value = if let Ok(id) = value.as_reference() {
            pdf.get_object(id).unwrap()
        } else {
            value
        };
        match value {
            lopdf::Object::Array(items) => {
                for item in items {
                    visit(pdf, item, page, marked, text);
                }
            }
            lopdf::Object::Dictionary(dict) => {
                let page = dict
                    .get(b"Pg")
                    .ok()
                    .and_then(|v| v.as_reference().ok())
                    .or(page);
                if let (Some(page), Ok(id)) =
                    (page, dict.get(b"MCID").and_then(lopdf::Object::as_i64))
                    && let Some(value) = marked.get(&(page, id))
                {
                    text.push_str(value);
                }
                if let Ok(child) = dict.get(b"K") {
                    visit(pdf, child, page, marked, text);
                }
            }
            lopdf::Object::Integer(id) => {
                if let Some(page) = page
                    && let Some(value) = marked.get(&(page, *id))
                {
                    text.push_str(value);
                }
            }
            _ => {}
        }
    }
    let mut text = String::new();
    visit(
        pdf,
        pdf.catalog().unwrap().get(b"StructTreeRoot").unwrap(),
        None,
        &marked,
        &mut text,
    );
    text
}
