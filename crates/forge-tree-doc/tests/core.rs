use forge_tree_doc::*;
use uuid::Uuid;
fn sample() -> Presentation {
    let mut p: Presentation = parse(include_bytes!("../examples/overview.json")).unwrap();
    p.assign_ids();
    p
}
#[test]
fn example_layout_and_font() {
    let p = sample();
    let l = layout(&p).unwrap();
    let title = p
        .find(&Target {
            key: Some("overview.title".into()),
            node_id: None,
        })
        .unwrap();
    assert_eq!(l.nodes[&title.id.unwrap()].frame.x, 40.0);
    let mut m = TextMeasurer::default();
    let n = Node {
        text: Some("한글 문서와 English text".into()),
        ..Default::default()
    };
    assert!(
        m.measure(&n.paragraphs(), &p.style(&n), 120.0, 1.0)
            .unwrap()
            .1
            > 25.0
    );
}
#[test]
fn strict_parser_and_revision_rollback() {
    assert!(
        parse::<Presentation>(br#"{"dsl_version":1,"kind":"presentation","slides":[],"typo":1}"#)
            .is_err()
    );
    let p = sample();
    let id = Uuid::now_v7();
    let patch = Patch {
        dsl_version: 1,
        kind: PatchKind::Patch,
        document_id: id,
        base_revision: 0,
        operations: vec![
            Operation::SetText {
                cell: None,
                target: Target {
                    key: Some("overview.title".into()),
                    node_id: None,
                },
                text: "Changed".into(),
            },
            Operation::RemoveNode {
                target: Target {
                    key: Some("missing".into()),
                    node_id: None,
                },
            },
        ],
    };
    assert!(apply_patch(&p, &patch, id, 0).is_err());
    assert_eq!(
        p.find(&Target {
            key: Some("overview.title".into()),
            node_id: None
        })
        .unwrap()
        .text
        .as_deref(),
        Some("Delino Forge MCP")
    );
    assert_eq!(
        apply_patch(&p, &patch, id, 1).unwrap_err().code,
        ErrorCode::RevisionConflict
    );
}
#[test]
fn detects_duplicate_identity_and_font_failure() {
    let mut p = sample();
    p.slides[0].content.children[0].key = Some("overview".into());
    assert_eq!(
        validate(&p, false).unwrap_err().code,
        ErrorCode::DuplicateIdentity
    );
    let mut p = sample();
    p.theme.font_family = "Forge definitely missing font".into();
    assert_eq!(layout(&p).unwrap_err().code, ErrorCode::FontUnavailable);
}
#[test]
fn shrink_and_overflow() {
    let mut p = sample();
    let title = &mut p.slides[0].content.children[0];
    title.height = Some(Size::Points(20.0));
    assert_eq!(layout(&p).unwrap_err().code, ErrorCode::TextOverflow);
    let title = &mut p.slides[0].content.children[0];
    title.overflow = Overflow::Shrink;
    title.min_font_size = Some(12.0);
    assert!(layout(&p).is_ok());
}
#[test]
fn bundled_font_digest() {
    use sha2::{Digest, Sha256};
    assert_eq!(
        format!("{:x}", Sha256::digest(FONT_BYTES)),
        "9e1d729e7e2b36f9ef439da102f8c134c10aabe46f1c843bf0aca5c043b86f76"
    );
}

fn canvas(children: serde_json::Value) -> Presentation {
    let mut p:Presentation=serde_json::from_value(serde_json::json!({"dsl_version":1,"kind":"presentation","page":{"width":600,"height":400},"slides":[{"content":{"type":"canvas","key":"root","children":children}}]})).unwrap();
    p.assign_ids();
    p
}
#[test]
fn rich_style_inheritance_and_minimum_size_are_enforced() {
    let mut p = canvas(
        serde_json::json!([{"type":"text","key":"text","frame":{"x":0,"y":0,"width":300,"height":100},"style":{"font_size":24,"font_weight":700},"paragraphs":[{"runs":[{"text":"한글 English ","style":{"font_size":12}},{"text":"inherits"}]}]}]),
    );
    p.theme.text_styles.insert(
        "body".into(),
        TextStyle {
            font_size: Some(18.),
            color: Some("#123456".into()),
            ..Default::default()
        },
    );
    p.slides[0].content.children[0].style_ref = Some("body".into());
    let n = &p.slides[0].content.children[0];
    let style = p.style(n);
    assert_eq!(style.font_size, Some(24.));
    assert_eq!(style.color.as_deref(), Some("#123456"));
    assert_eq!(style.font_weight, Some(700));
    layout(&p).unwrap();
    let n = &mut p.slides[0].content.children[0];
    n.frame.as_mut().unwrap().height = 8.;
    n.overflow = Overflow::Shrink;
    n.min_font_size = Some(12.);
    assert_eq!(layout(&p).unwrap_err().code, ErrorCode::TextOverflow);
}
#[test]
fn geometry_cycles_duplicate_ids_merges_and_connectors() {
    let mut p = sample();
    let title = p.slides[0].content.children[0].id;
    p.slides[0].content.children[1].id = title;
    assert_eq!(
        validate(&p, false).unwrap_err().code,
        ErrorCode::DuplicateIdentity
    );
    let p = canvas(
        serde_json::json!([{"type":"connector","from":{"target":{"key":"missing"},"anchor":"left"},"to":{"target":{"key":"root"},"anchor":"right"}}]),
    );
    assert_eq!(
        validate(&p, false).unwrap_err().code,
        ErrorCode::InvalidReference
    );
    let p = canvas(
        serde_json::json!([{"type":"table","frame":{"x":0,"y":0,"width":300,"height":200},"columns":[{"width":"fill"},{"width":"fill"}],"rows":[{"cells":[{"text":"merge","row_span":2},{"text":"ok"}]},{"cells":[{"text":"overlap","col_span":2}]}]}]),
    );
    assert_eq!(
        validate(&p, false).unwrap_err().code,
        ErrorCode::InvalidGeometry
    );
    let mut p = sample();
    p.slides[0].content.children[1].height = Some(Size::Mode(SizeMode::Hug));
    assert_eq!(layout(&p).unwrap_err().code, ErrorCode::LayoutCycle);
}
#[test]
fn flow_fill_and_canvas_coordinates_are_deterministic() {
    let mut p = sample();
    let l = layout(&p).unwrap();
    let f = l.nodes[&p.slides[0].content.children[1].id.unwrap()].frame;
    assert_eq!(f.width, 880.);
    assert_eq!(f.x, 40.);
    assert!(f.height > 300.);
    p = canvas(
        serde_json::json!([{"type":"canvas","frame":{"x":20,"y":30,"width":200,"height":100},"children":[{"type":"shape","key":"shape","frame":{"x":5,"y":10,"width":20,"height":20}}]}]),
    );
    let n = p
        .find(&Target {
            key: Some("shape".into()),
            node_id: None,
        })
        .unwrap();
    let f = layout(&p).unwrap().nodes[&n.id.unwrap()].frame;
    assert_eq!((f.x, f.y), (25., 40.));
}
#[test]
fn operation_move_cycle_and_unset_style() {
    let p = sample();
    let id = Uuid::now_v7();
    let target = Target {
        key: Some("overview.title".into()),
        node_id: None,
    };
    let op = Patch {
        dsl_version: 1,
        kind: PatchKind::Patch,
        document_id: id,
        base_revision: 0,
        operations: vec![
            Operation::SetTextStyle {
                target: target.clone(),
                style: TextStyle {
                    font_size: Some(12.),
                    ..Default::default()
                },
            },
            Operation::UnsetTextStyle {
                target: target.clone(),
                properties: vec![StyleProperty::FontSize],
            },
        ],
    };
    let next = apply_patch(&p, &op, id, 0).unwrap();
    assert_eq!(next.style(next.find(&target).unwrap()).font_size, Some(36.));
    let op = Patch {
        operations: vec![Operation::MoveNode {
            target: Target {
                key: Some("overview.content".into()),
                node_id: None,
            },
            parent: target,
            index: 0,
        }],
        ..op
    };
    assert_eq!(
        apply_patch(&p, &op, id, 0).unwrap_err().code,
        ErrorCode::LayoutCycle
    );
}

#[test]
fn committed_schema_matches_rust_contracts() {
    let committed: serde_json::Value =
        serde_json::from_slice(include_bytes!("../schema.json")).unwrap();
    assert_eq!(schema(), committed);
}
