use delino_forge::{preview::preview, store::Store};
use forge_tree_doc::*;
use tokio_util::sync::CancellationToken;
#[tokio::test]
#[ignore = "requires installed LibreOffice and Poppler; mandatory in forge-render CI"]
async fn libreoffice_poppler_render_created_and_edited_presentations() {
    let temp = tempfile::tempdir().unwrap();
    let store = Store::new(Some(temp.path().join("state")), CancellationToken::new()).unwrap();
    let root =
        std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("../forge-pptx/tests/fixtures");
    let asset = store.register_asset(&root.join("sample.png")).unwrap();
    let mut document: Presentation = parse(include_bytes!(
        "../../forge-tree-doc/examples/all-nodes.json"
    ))
    .unwrap();
    document.assets.get_mut("sample").unwrap().handle = asset["handle"].as_str().unwrap().into();
    let created = store.create(document).unwrap();
    let output = temp.path().join("generated");
    let result = preview(store.clone(), created.document_id, output.clone())
        .await
        .unwrap();
    assert_eq!(result["files"].as_array().unwrap().len(), 3);
    let loaded = store.open(&root.join("external.pptx")).unwrap();
    let inspection = store.inspect(loaded.document_id, None, 2).unwrap();
    let children = inspection["content"]["slides"][0]["content"]["children"]
        .as_array()
        .unwrap();
    let node = children.iter().find(|n| n["type"] == "text").unwrap();
    store
        .apply(Patch {
            dsl_version: 1,
            kind: PatchKind::Patch,
            document_id: loaded.document_id,
            base_revision: loaded.revision,
            operations: vec![Operation::SetText {
                target: Target {
                    node_id: Some(node["id"].as_str().unwrap().parse().unwrap()),
                    key: None,
                },
                text: "Rendered edited presentation".into(),
                cell: None,
            }],
        })
        .unwrap();
    let edited = temp.path().join("edited");
    let result = preview(store.clone(), loaded.document_id, edited.clone())
        .await
        .unwrap();
    assert_eq!(result["files"].as_array().unwrap().len(), 2);
    for directory in [&output, &edited] {
        for entry in std::fs::read_dir(directory).unwrap() {
            let path = entry.unwrap().path();
            if path.extension().is_some_and(|e| e == "png") {
                let data = std::fs::read(path).unwrap();
                assert!(data.starts_with(b"\x89PNG\r\n\x1a\n"));
                assert!(data.len() > 10_000);
            }
        }
    }
    if let Some(destination) = std::env::var_os("FORGE_RENDER_ARTIFACTS") {
        let destination = std::path::PathBuf::from(destination);
        std::fs::create_dir_all(&destination).unwrap();
        store
            .export(
                created.document_id,
                &destination.join("generated.pptx"),
                true,
            )
            .unwrap();
        store
            .export(loaded.document_id, &destination.join("edited.pptx"), true)
            .unwrap();
        for (name, path) in [("generated", output), ("edited", edited)] {
            let out = destination.join(name);
            std::fs::create_dir_all(&out).unwrap();
            for file in std::fs::read_dir(path).unwrap() {
                let file = file.unwrap();
                std::fs::copy(file.path(), out.join(file.file_name())).unwrap();
            }
        }
    }
}
