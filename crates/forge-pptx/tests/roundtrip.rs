use forge_pptx::*;
use forge_tree_doc::*;
use uuid::Uuid;
#[test]
fn generates_and_reopens() {
    let mut p: Presentation = parse(include_bytes!(
        "../../forge-tree-doc/examples/overview.json"
    ))
    .unwrap();
    p.assign_ids();
    let id = Uuid::now_v7();
    let bytes = generate(&p, &Assets::new(), id, 0).unwrap();
    let imported = import(&bytes).unwrap();
    assert_eq!(p, imported.document);
    assert_eq!(id, imported.document_id);
    let target = Target {
        key: Some("overview.title".into()),
        node_id: None,
    };
    let patch = Patch {
        dsl_version: 1,
        kind: PatchKind::Patch,
        document_id: id,
        base_revision: 0,
        operations: vec![Operation::SetText {
            cell: None,
            target,
            text: "Forge document tools".into(),
        }],
    };
    let next = apply_patch(&p, &patch, id, 0).unwrap();
    let out = update(&bytes, &p, &imported.bindings, &next, &Assets::new(), id, 1).unwrap();
    assert_eq!(import(&out).unwrap().revision, 1);
    let a = read_package(&bytes).unwrap();
    let b = read_package(&out).unwrap();
    for (name, old) in a {
        if !name.starts_with("ppt/slides/slide") && name != "customXml/forge.xml" {
            assert_eq!(b[&name], old, "{name}");
        }
    }
}
