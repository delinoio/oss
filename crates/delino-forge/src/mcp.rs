use std::path::PathBuf;

use forge_tree_doc::{
    Diagnostic, ErrorCode, Patch, Presentation, Result as ForgeResult, Target, error,
};
use rmcp::{
    RoleServer, ServerHandler, ServiceExt, handler::server::wrapper::Parameters,
    model::CallToolResult, service::RequestContext, tool, tool_handler, tool_router,
};
use schemars::JsonSchema;
use serde::Deserialize;
use uuid::Uuid;

use crate::{preview::preview, store::Store};

#[derive(Debug, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct PathInput {
    pub path: PathBuf,
}
#[derive(Debug, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct DocumentInput {
    pub document: Presentation,
}
#[derive(Debug, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct PatchInput {
    pub patch: Patch,
}
#[derive(Debug, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct IdInput {
    pub document_id: Uuid,
}
#[derive(Debug, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct InspectInput {
    pub document_id: Uuid,
    #[serde(default)]
    pub target: Option<Target>,
    #[serde(default = "depth")]
    pub depth: usize,
}
fn depth() -> usize {
    2
}
#[derive(Debug, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct ExportInput {
    pub document_id: Uuid,
    pub output: PathBuf,
    #[serde(default)]
    pub overwrite: bool,
}
#[derive(Debug, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct PreviewInput {
    pub document_id: Uuid,
    pub output: PathBuf,
}
#[derive(Clone)]
pub struct Server {
    store: Store,
}
fn result(value: ForgeResult<serde_json::Value>) -> CallToolResult {
    match value {
        Ok(value) => CallToolResult::structured(value),
        Err(e) => {
            tracing::warn!(code=?e.code,stage="tool_result","Forge tool failed");
            CallToolResult::structured_error(serde_json::json!({"error":e}))
        }
    }
}
impl Server {
    pub fn new(store: Store) -> Self {
        Self { store }
    }

    async fn execute<F>(
        &self,
        context: RequestContext<RoleServer>,
        operation: &'static str,
        f: F,
    ) -> CallToolResult
    where
        F: FnOnce(Store) -> ForgeResult<serde_json::Value> + Send + 'static,
    {
        let mut store = self.store.clone();
        let cancel = store.cancel.child_token();
        store.cancel = cancel.clone();
        let request_cancel = context.ct;
        let started = std::time::Instant::now();
        let monitor = tokio::spawn(async move {
            request_cancel.cancelled().await;
            cancel.cancel();
        });
        let value = tokio::task::spawn_blocking(move || f(store))
            .await
            .unwrap_or_else(|_| error(ErrorCode::Io, "", "Worker failed"));
        monitor.abort();
        tracing::info!(
            operation,
            elapsed_ms = started.elapsed().as_millis() as u64,
            "Forge operation completed"
        );
        result(value)
    }
}
#[tool_router]
impl Server {
    #[tool(
        name = "forge.schema",
        description = "Return the versioned presentation and patch JSON Schemas."
    )]
    fn schema(&self) -> CallToolResult {
        CallToolResult::structured(forge_tree_doc::schema())
    }

    #[tool(
        name = "forge.capabilities",
        description = "Describe supported document formats, node types, commands, and preview \
                       dependencies."
    )]
    fn capabilities(&self) -> CallToolResult {
        CallToolResult::structured(crate::capabilities())
    }

    #[tool(
        name = "forge.asset.add",
        description = "Register a local PNG or JPEG. Returns a content-addressed asset handle."
    )]
    async fn asset(
        &self,
        Parameters(input): Parameters<PathInput>,
        context: RequestContext<RoleServer>,
    ) -> CallToolResult {
        self.execute(context, "asset.add", move |s| s.register_asset(&input.path))
            .await
    }

    #[tool(
        name = "forge.create",
        description = "Validate a presentation DSL, create an editable PPTX, and return its \
                       managed document ID and revision. No source file is overwritten."
    )]
    async fn create(
        &self,
        Parameters(input): Parameters<DocumentInput>,
        context: RequestContext<RoleServer>,
    ) -> CallToolResult {
        self.execute(context, "create", move |s| {
            s.create(input.document).map(|r| serde_json::json!(r))
        })
        .await
    }

    #[tool(
        name = "forge.open",
        description = "Import an existing local PPTX, preserving unsupported parts. Returns a \
                       persistent document ID and revision; source files are unchanged."
    )]
    async fn open(
        &self,
        Parameters(input): Parameters<PathInput>,
        context: RequestContext<RoleServer>,
    ) -> CallToolResult {
        self.execute(context, "open", move |s| {
            s.open(&input.path).map(|r| serde_json::json!(r))
        })
        .await
    }

    #[tool(
        name = "forge.inspect",
        description = "Read the managed document outline or a node subtree. Depth is limited to 8 \
                       and node output is bounded."
    )]
    async fn inspect(
        &self,
        Parameters(input): Parameters<InspectInput>,
        context: RequestContext<RoleServer>,
    ) -> CallToolResult {
        self.execute(context, "inspect", move |s| {
            s.inspect(input.document_id, input.target, input.depth)
        })
        .await
    }

    #[tool(
        name = "forge.apply",
        description = "Apply a complete patch atomically to a managed document. Requires the \
                       matching document ID and base revision. Export is a separate explicit \
                       operation."
    )]
    async fn apply(
        &self,
        Parameters(input): Parameters<PatchInput>,
        context: RequestContext<RoleServer>,
    ) -> CallToolResult {
        self.execute(context, "apply", move |s| {
            s.apply(input.patch).map(|r| serde_json::json!(r))
        })
        .await
    }

    #[tool(
        name = "forge.export",
        description = "Export a validated PPTX to a local path. Existing outputs require \
                       overwrite=true."
    )]
    async fn export(
        &self,
        Parameters(input): Parameters<ExportInput>,
        context: RequestContext<RoleServer>,
    ) -> CallToolResult {
        self.execute(context, "export", move |s| {
            s.export(input.document_id, &input.output, input.overwrite)
        })
        .await
    }

    #[tool(
        name = "forge.preview",
        description = "Render PDF and slide PNG previews using installed LibreOffice and Poppler. \
                       Output directory must not exist. Each renderer has a 120-second deadline."
    )]
    async fn preview(
        &self,
        Parameters(input): Parameters<PreviewInput>,
        context: RequestContext<RoleServer>,
    ) -> CallToolResult {
        let mut store = self.store.clone();
        let cancel = store.cancel.child_token();
        store.cancel = cancel.clone();
        let monitor = tokio::spawn(async move {
            context.ct.cancelled().await;
            cancel.cancel();
        });
        let value = preview(store, input.document_id, input.output).await;
        monitor.abort();
        result(value)
    }

    #[tool(
        name = "forge.close",
        description = "Delete the selected managed document and its local revisions. Exported \
                       files and registered assets remain."
    )]
    async fn close(
        &self,
        Parameters(input): Parameters<IdInput>,
        context: RequestContext<RoleServer>,
    ) -> CallToolResult {
        self.execute(context, "close", move |s| s.close(input.document_id))
            .await
    }
}
#[tool_handler(
    name = "delino-forge",
    version = "0.0.0",
    instructions = "Use forge.schema and forge.capabilities, register local images, create or \
                    open a document, inspect stable IDs, apply revision-checked patches, then \
                    explicitly export. No internal LLM or network fetches are used."
)]
impl ServerHandler for Server {}
pub async fn serve(store: Store) -> ForgeResult<()> {
    let cancel = store.cancel.clone();
    let server = Server::new(store)
        .serve_with_ct(rmcp::transport::stdio(), cancel)
        .await
        .map_err(|_| Diagnostic::new(ErrorCode::Io, "", "MCP transport failed"))?;
    server
        .waiting()
        .await
        .map_err(|_| Diagnostic::new(ErrorCode::Io, "", "MCP service failed"))?;
    Ok(())
}
