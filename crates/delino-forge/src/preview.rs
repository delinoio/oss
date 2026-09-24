use std::{
    path::{Path, PathBuf},
    process::Stdio,
    time::Duration,
};

use forge_tree_doc::*;
use process_wrap::tokio::{CommandWrap, KillOnDrop};
use tokio::process::Command;
use uuid::Uuid;

use crate::store::Store;

async fn run(cmd: Command, store: &Store) -> Result<()> {
    run_with_deadline(cmd, store, Duration::from_secs(120)).await
}
async fn run_with_deadline(mut cmd: Command, store: &Store, deadline: Duration) -> Result<()> {
    store.check_cancelled()?;
    cmd.stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .kill_on_drop(true);
    let mut wrapped = CommandWrap::from(cmd);
    wrapped.wrap(KillOnDrop);
    #[cfg(unix)]
    wrapped.wrap(process_wrap::tokio::ProcessGroup::leader());
    #[cfg(windows)]
    wrapped.wrap(process_wrap::tokio::JobObject);
    let mut child = wrapped.spawn().map_err(|_| {
        Diagnostic::new(
            ErrorCode::RendererUnavailable,
            "",
            "Required renderer executable is unavailable",
        )
    })?;
    let result = tokio::select! {
        status=child.wait()=>status.map_err(io_error).and_then(|status|if status.success(){Ok(())}else{error(ErrorCode::RendererFailed,"","Renderer returned a failure status")}),
        _=store.cancel.cancelled()=>error(ErrorCode::Cancelled,"","Preview cancelled"),
        _=tokio::time::sleep(deadline)=>error(ErrorCode::Timeout,"","Renderer exceeded its 120-second deadline"),
    };
    if result.is_err() {
        let _ = child.start_kill();
        child.wait().await.map_err(io_error)?;
    }
    result
}
fn file_uri(path: &Path) -> String {
    let path = path.to_string_lossy().replace('\\', "/");
    let mut out = String::new();
    for b in path.bytes() {
        if b.is_ascii_alphanumeric() || b"/-._~:".contains(&b) {
            out.push(b as char);
        } else {
            out.push_str(&format!("%{b:02X}"));
        }
    }
    if out.starts_with('/') {
        format!("file://{out}")
    } else {
        format!("file:///{out}")
    }
}
pub async fn preview(store: Store, id: Uuid, output: PathBuf) -> Result<serde_json::Value> {
    if output.exists() {
        return error(
            ErrorCode::OutputExists,
            "",
            "Preview output directory must not exist",
        );
    }
    let source = store.snapshot_bytes(id)?;
    let imported = forge_pptx::import(&source)?;
    let pixels =
        imported.document.page.width * imported.document.page.height * (96.0 / 72.0_f64).powi(2);
    if imported.document.slides.len() > 100
        || pixels > 40_000_000.0
        || pixels * imported.document.slides.len() as f64 > 250_000_000.0
    {
        return error(
            ErrorCode::ResourceLimit,
            "/page",
            "Preview exceeds the raster size or page-count limit",
        );
    }
    let revision = imported.revision;
    let expected_pages = imported.document.slides.len();
    let bytes = forge_pptx::preview_bytes(&source)?;
    let temp = tempfile::tempdir().map_err(io_error)?;
    let input = temp.path().join("document.pptx");
    std::fs::write(&input, bytes).map_err(io_error)?;
    let profile = temp.path().join("profile");
    std::fs::create_dir(&profile).map_err(io_error)?;
    let mut libre =
        Command::new(std::env::var_os("FORGE_SOFFICE").unwrap_or_else(|| "soffice".into()));
    libre
        .arg(format!("-env:UserInstallation={}", file_uri(&profile)))
        .args([
            "--headless",
            "--nologo",
            "--nodefault",
            "--norestore",
            "--convert-to",
            "pdf:impress_pdf_Export",
            "--outdir",
        ])
        .arg(temp.path())
        .arg(&input);
    run(libre, &store).await?;
    let pdf = temp.path().join("document.pdf");
    if !pdf.is_file() {
        return error(ErrorCode::RendererFailed, "", "Renderer produced no PDF");
    }
    let mut poppler =
        Command::new(std::env::var_os("FORGE_PDFTOPPM").unwrap_or_else(|| "pdftoppm".into()));
    poppler
        .args(["-png", "-r", "96"])
        .arg(&pdf)
        .arg(temp.path().join("slide"));
    run(poppler, &store).await?;
    let mut artifacts = Vec::new();
    for e in std::fs::read_dir(temp.path()).map_err(io_error)? {
        let e = e.map_err(io_error)?;
        let name = e.file_name().to_string_lossy().into_owned();
        if name == "document.pdf" || name.starts_with("slide-") && name.ends_with(".png") {
            artifacts.push((name, e.path()));
        }
    }
    artifacts.sort();
    if artifacts.len() != expected_pages + 1 {
        return error(
            ErrorCode::RendererFailed,
            "",
            "Renderer page count does not match the document",
        );
    }
    let parent = output
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    let stage = tempfile::tempdir_in(parent).map_err(io_error)?;
    for (name, path) in &artifacts {
        std::fs::copy(path, stage.path().join(name)).map_err(io_error)?;
    }
    store.check_cancelled()?;
    // Create the destination exclusively before moving files; no existing directory
    // is ever replaced. Remove only this request's new directory on publication
    // error.
    std::fs::create_dir(&output).map_err(|e| {
        if e.kind() == std::io::ErrorKind::AlreadyExists {
            Diagnostic::new(ErrorCode::OutputExists, "", "Preview output already exists")
        } else {
            io_error(e)
        }
    })?;
    for (name, _) in &artifacts {
        if let Err(e) = std::fs::rename(stage.path().join(name), output.join(name)) {
            let _ = std::fs::remove_dir_all(&output);
            return Err(io_error(e));
        }
    }
    Ok(
        serde_json::json!({"document_id":id,"revision":revision,"renderer":"libreoffice+poppler","files":artifacts.iter().map(|(name,_)|output.join(name)).collect::<Vec<_>>()}),
    )
}

#[cfg(all(test, unix))]
mod tests {
    use tokio_util::sync::CancellationToken;

    use super::*;
    #[tokio::test]
    async fn renderer_timeout_and_cancellation_reap_children() {
        let temp = tempfile::tempdir().unwrap();
        let store = Store::new(Some(temp.path().join("state")), CancellationToken::new()).unwrap();
        for cancel in [false, true] {
            let mut command = Command::new("sh");
            command.args(["-c", "sleep 30 & wait"]);
            let mut state = store.clone();
            state.cancel = CancellationToken::new();
            if cancel {
                let token = state.cancel.clone();
                tokio::spawn(async move {
                    tokio::time::sleep(Duration::from_millis(20)).await;
                    token.cancel();
                });
            }
            let error = run_with_deadline(command, &state, Duration::from_millis(60))
                .await
                .unwrap_err();
            assert_eq!(
                error.code,
                if cancel {
                    ErrorCode::Cancelled
                } else {
                    ErrorCode::Timeout
                }
            );
        }
    }
}
