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
