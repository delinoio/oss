use std::{path::Path, process::Command};

use delino_forge::store::Store;
use forge_tree_doc::*;
use rmcp::{ServiceExt, model::CallToolRequestParams, transport::TokioChildProcess};
use serde_json::{Value, json};
use tokio_util::sync::CancellationToken;
fn document() -> Presentation {
    parse(include_bytes!(
        "../../forge-tree-doc/examples/overview.json"
    ))
    .unwrap()
}
fn patch(id: uuid::Uuid, revision: u64, text: &str) -> Patch {
    Patch {
        dsl_version: 1,
        kind: PatchKind::Patch,
        document_id: id,
        base_revision: revision,
        operations: vec![Operation::SetText {
            target: Target {
                key: Some("overview.title".into()),
                node_id: None,
            },
            text: text.into(),
            cell: None,
        }],
    }
}
fn cli(state: &Path, args: &[&str]) -> std::process::Output {
    Command::new(env!("CARGO_BIN_EXE_delino-forge"))
        .arg("--state-dir")
        .arg(state)
        .args(args)
        .output()
        .unwrap()
}
#[test]
fn state_atomicity_source_conflicts_and_restart() {
    let tmp = tempfile::tempdir().unwrap();
    let root = tmp.path().join("state");
    let s = Store::new(Some(root.clone()), CancellationToken::new()).unwrap();
    let created = s.create(document()).unwrap();
    let id = created.document_id;
    let snapshot = s.snapshot_bytes(id).unwrap();
    let mut invalid = patch(id, 0, "Valid first operation");
    invalid.operations.push(Operation::RemoveNode {
        target: Target {
            key: Some("missing".into()),
            node_id: None,
        },
    });
    assert!(s.apply(invalid).is_err());
    assert_eq!(s.snapshot_bytes(id).unwrap(), snapshot);
    assert_eq!(
        s.apply(patch(id, 10, "Conflict")).unwrap_err().code,
        ErrorCode::RevisionConflict
    );
    let cancelled = Store::new(Some(root.clone()), CancellationToken::new()).unwrap();
    cancelled.cancel.cancel();
    assert_eq!(
        cancelled.apply(patch(id, 0, "Cancelled")).unwrap_err().code,
        ErrorCode::Cancelled
    );
    assert_eq!(s.snapshot_bytes(id).unwrap(), snapshot);
    let lock = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open(root.join("locks").join(format!("{id}.lock")))
        .unwrap();
    fs2::FileExt::lock_exclusive(&lock).unwrap();
    assert_eq!(
        s.apply(patch(id, 0, "Concurrent")).unwrap_err().code,
        ErrorCode::Busy
    );
    fs2::FileExt::unlock(&lock).unwrap();
    drop(lock);
    s.apply(patch(id, 0, "Committed title")).unwrap();
    let reopened = Store::new(Some(root), CancellationToken::new()).unwrap();
    assert_eq!(reopened.inspect(id, None, 0).unwrap()["revision"], 1);
    let out = tmp.path().join("output.pptx");
    reopened.export(id, &out, false).unwrap();
    assert_eq!(
        reopened.export(id, &out, false).unwrap_err().code,
        ErrorCode::OutputExists
    );
    let other = Store::new(Some(tmp.path().join("other")), CancellationToken::new()).unwrap();
    let opened = other.open(&out).unwrap();
    assert_eq!(opened.revision, 1);
    std::fs::write(&out, b"external replacement").unwrap();
    assert_eq!(
        other.apply(patch(id, 1, "Blocked")).unwrap_err().code,
        ErrorCode::SourceChanged
    );
    assert_eq!(std::fs::read(&out).unwrap(), b"external replacement");
}
#[test]
fn cli_end_to_end_stdout_and_missing_renderer() {
    let tmp = tempfile::tempdir().unwrap();
    let root = tmp.path().join("state");
    let input = tmp.path().join("input.json");
    std::fs::write(&input, serde_json::to_vec(&document()).unwrap()).unwrap();
    let result = cli(&root, &["create", input.to_str().unwrap()]);
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    let receipt: Value = serde_json::from_slice(&result.stdout).unwrap();
    let id = receipt["document_id"].as_str().unwrap();
    assert!(!String::from_utf8_lossy(&result.stderr).contains("Delino Forge MCP"));
    let patch_file = tmp.path().join("patch.json");
    std::fs::write(
        &patch_file,
        serde_json::to_vec(&patch(id.parse().unwrap(), 0, "CLI edited title")).unwrap(),
    )
    .unwrap();
    let applied = cli(&root, &["apply", patch_file.to_str().unwrap()]);
    assert!(applied.status.success());
    assert_eq!(
        serde_json::from_slice::<Value>(&applied.stdout).unwrap()["revision"],
        1
    );
    let output = tmp.path().join("result.pptx");
    assert!(
        cli(&root, &["export", id, "--output", output.to_str().unwrap()])
            .status
            .success()
    );
    assert!(
        cli(&root, &["inspect", id, "--depth", "1"])
            .status
            .success()
    );
    let preview = Command::new(env!("CARGO_BIN_EXE_delino-forge"))
        .arg("--state-dir")
        .arg(&root)
        .args(["preview", id, "--output"])
        .arg(tmp.path().join("preview"))
        .env("FORGE_SOFFICE", tmp.path().join("missing-renderer"))
        .output()
        .unwrap();
    assert!(!preview.status.success());
    assert_eq!(
        serde_json::from_slice::<Value>(&preview.stdout).unwrap()["error"]["code"],
        "renderer_unavailable"
    );
    assert!(cli(&root, &["close", id]).status.success());
    assert!(
        cli(&root, &["open", output.to_str().unwrap()])
            .status
            .success()
    );
}
#[tokio::test]
async fn official_mcp_client_exercises_actual_stdio_binary() {
    let tmp = tempfile::tempdir().unwrap();
    let mut command = tokio::process::Command::new(env!("CARGO_BIN_EXE_delino-forge"));
    command
        .arg("--state-dir")
        .arg(tmp.path().join("state"))
        .arg("mcp");
    let client = ().serve(TokioChildProcess::new(command).unwrap()).await.unwrap();
    let tools = client.list_all_tools().await.unwrap();
    assert_eq!(tools.len(), 10);
    assert!(tools.iter().all(|t| t.name.starts_with("forge.")));
    let schema = client
        .call_tool(CallToolRequestParams::new("forge.schema"))
        .await
        .unwrap();
    assert!(schema.structured_content.is_some());
    let asset =
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../forge-pptx/tests/fixtures/sample.png");
    let registered = client
        .call_tool(
            CallToolRequestParams::new("forge.asset.add")
                .with_arguments(json!({"path":asset}).as_object().unwrap().clone()),
        )
        .await
        .unwrap();
    assert!(
        registered.structured_content.unwrap()["handle"]
            .as_str()
            .unwrap()
            .starts_with("asset_")
    );
    let create = client
        .call_tool(
            CallToolRequestParams::new("forge.create")
                .with_arguments(json!({"document":document()}).as_object().unwrap().clone()),
        )
        .await
        .unwrap();
    assert_ne!(create.is_error, Some(true));
    let id = create.structured_content.unwrap()["document_id"]
        .as_str()
        .unwrap()
        .to_string();
    let inspect = client
        .call_tool(
            CallToolRequestParams::new("forge.inspect").with_arguments(
                json!({"document_id":id,"depth":1})
                    .as_object()
                    .unwrap()
                    .clone(),
            ),
        )
        .await
        .unwrap();
    assert_eq!(inspect.structured_content.unwrap()["revision"], 0);
    let apply = client
        .call_tool(
            CallToolRequestParams::new("forge.apply").with_arguments(
                json!({"patch":patch(id.parse().unwrap(),0,"MCP edited title")})
                    .as_object()
                    .unwrap()
                    .clone(),
            ),
        )
        .await
        .unwrap();
    assert_eq!(apply.structured_content.unwrap()["revision"], 1);
    let out = tmp.path().join("export.pptx");
    let export = client
        .call_tool(
            CallToolRequestParams::new("forge.export").with_arguments(
                json!({"document_id":id,"output":out})
                    .as_object()
                    .unwrap()
                    .clone(),
            ),
        )
        .await
        .unwrap();
    assert_ne!(export.is_error, Some(true));
    assert!(out.exists());
    client
        .call_tool(
            CallToolRequestParams::new("forge.close")
                .with_arguments(json!({"document_id":id}).as_object().unwrap().clone()),
        )
        .await
        .unwrap();
    let open = client
        .call_tool(
            CallToolRequestParams::new("forge.open")
                .with_arguments(json!({"path":out}).as_object().unwrap().clone()),
        )
        .await
        .unwrap();
    assert_eq!(open.structured_content.unwrap()["revision"], 1);
    let error = client
        .call_tool(
            CallToolRequestParams::new("forge.apply").with_arguments(
                json!({"patch":patch(id.parse().unwrap(),0,"Stale")})
                    .as_object()
                    .unwrap()
                    .clone(),
            ),
        )
        .await
        .unwrap();
    assert_eq!(error.is_error, Some(true));
    assert_eq!(
        error.structured_content.unwrap()["error"]["code"],
        "revision_conflict"
    );
    client.cancel().await.unwrap();
}

#[test]
fn simultaneous_writers_publish_only_one_revision() {
    let tmp = tempfile::tempdir().unwrap();
    let store = Store::new(Some(tmp.path().join("state")), CancellationToken::new()).unwrap();
    let id = store.create(document()).unwrap().document_id;
    let barrier = std::sync::Arc::new(std::sync::Barrier::new(2));
    let mut handles = Vec::new();
    for text in ["Writer one", "Writer two"] {
        let store = store.clone();
        let barrier = barrier.clone();
        handles.push(std::thread::spawn(move || {
            barrier.wait();
            store.apply(patch(id, 0, text))
        }));
    }
    let results: Vec<_> = handles.into_iter().map(|h| h.join().unwrap()).collect();
    assert_eq!(results.iter().filter(|r| r.is_ok()).count(), 1);
    assert!(
        results
            .iter()
            .filter_map(|r| r.as_ref().err())
            .all(|e| matches!(e.code, ErrorCode::Busy | ErrorCode::RevisionConflict))
    );
    assert_eq!(store.inspect(id, None, 0).unwrap()["revision"], 1);
    let abandoned = store
        .root
        .join("documents")
        .join(id.to_string())
        .join(".revision-interrupted");
    std::fs::create_dir(&abandoned).unwrap();
    std::fs::write(abandoned.join("document.pptx"), b"incomplete").unwrap();
    let restarted = Store::new(Some(store.root.clone()), CancellationToken::new()).unwrap();
    assert_eq!(restarted.inspect(id, None, 0).unwrap()["revision"], 1);
}
#[test]
fn mcp_startup_failure_never_prints_cli_json_on_stdout() {
    let tmp = tempfile::NamedTempFile::new().unwrap();
    let result = cli(tmp.path(), &["mcp"]);
    assert!(!result.status.success());
    assert!(result.stdout.is_empty());
}
