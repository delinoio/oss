use std::{borrow::Cow, future::Future};

use aisdk::core::tools::{Tool, ToolExecute};
use rmcp::{
    Peer, RoleClient,
    model::{CallToolRequestParam, CallToolResult, Tool as McpTool},
};
use schemars::Schema;
use serde_json::Value;
use tokio::runtime::{Handle, RuntimeFlavor};
use tracing::{debug, error, info};

use crate::error::{MicroAgenticaBuildError, ToolOrigin};

pub(crate) async fn build_mcp_tools(
    controller_name: &str,
    peer: Peer<RoleClient>,
) -> Result<Vec<Tool>, MicroAgenticaBuildError> {
    let remote_tools =
        peer.list_all_tools()
            .await
            .map_err(|source| MicroAgenticaBuildError::McpListTools {
                controller: controller_name.to_owned(),
                reason: source.to_string(),
            })?;

    info!(
        controller_name,
        tool_count = remote_tools.len(),
        "listed MCP tools"
    );

    remote_tools
        .into_iter()
        .map(|remote| build_mcp_tool(controller_name, peer.clone(), remote))
        .collect()
}

fn build_mcp_tool(
    controller_name: &str,
    peer: Peer<RoleClient>,
    remote: McpTool,
) -> Result<Tool, MicroAgenticaBuildError> {
    let tool_name = remote.name.trim().to_owned();
    if tool_name.is_empty() {
        return Err(MicroAgenticaBuildError::EmptyToolName {
            origin: ToolOrigin::Mcp {
                controller: controller_name.to_owned(),
            },
        });
    }

    let description = remote
        .description
        .as_ref()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
        .unwrap_or_else(|| format!("MCP tool {tool_name}"));

    let input_schema = Schema::try_from(Value::Object(remote.input_schema.as_ref().clone()))
        .map_err(|source| MicroAgenticaBuildError::InvalidMcpSchema {
            controller: controller_name.to_owned(),
            tool_name: tool_name.clone(),
            reason: source.to_string(),
        })?;

    let tool_name_for_execute = tool_name.clone();
    let controller_name_for_execute = controller_name.to_owned();
    let execute = ToolExecute::new(Box::new(move |input| {
        execute_mcp_tool(
            &controller_name_for_execute,
            &tool_name_for_execute,
            peer.clone(),
            input,
        )
    }));

    Tool::builder()
        .name(tool_name.clone())
        .description(description)
        .input_schema(input_schema)
        .execute(execute)
        .build()
        .map_err(|source| MicroAgenticaBuildError::ToolBuild {
            origin: ToolOrigin::Mcp {
                controller: controller_name.to_owned(),
            },
            tool_name,
            reason: source.to_string(),
        })
}

fn execute_mcp_tool(
    controller_name: &str,
    tool_name: &str,
    peer: Peer<RoleClient>,
    input: Value,
) -> Result<String, String> {
    let arguments = match input {
        Value::Object(map) => Some(map),
        Value::Null => None,
        other => {
            return Err(format!(
                "MCP tool '{tool_name}' expects object arguments or null, got: {other}"
            ));
        }
    };

    debug!(controller_name, tool_name, "calling MCP tool");

    // WORKAROUND:
    // aisdk tool callbacks are currently synchronous (`Fn(Value) -> Result<String,
    // String>`). MCP tool calls are async (`peer.call_tool(...).await`), so
    // this bridge blocks in place and drives the async future with
    // `Handle::block_on`. Remove this workaround when aisdk adds async tool
    // callback support.
    let tool_name_owned = tool_name.to_owned();
    let result = block_on_mcp_call(async move {
        peer.call_tool(CallToolRequestParam {
            name: Cow::Owned(tool_name_owned),
            arguments,
        })
        .await
    })?;

    let payload = mcp_result_payload(&result)?;
    if result.is_error.unwrap_or(false) {
        error!(
            controller_name,
            tool_name, "MCP tool returned error payload"
        );
        Err(payload)
    } else {
        info!(controller_name, tool_name, "MCP tool call succeeded");
        Ok(payload)
    }
}

fn mcp_result_payload(result: &CallToolResult) -> Result<String, String> {
    if let Some(value) = &result.structured_content {
        return serde_json::to_string(value).map_err(|source| source.to_string());
    }

    let text_chunks: Vec<String> = result
        .content
        .iter()
        .filter_map(|content| content.raw.as_text().map(|text| text.text.clone()))
        .collect();
    if !text_chunks.is_empty() {
        return Ok(text_chunks.join("\n"));
    }

    serde_json::to_string(&result.content).map_err(|source| source.to_string())
}

fn block_on_mcp_call<F>(future: F) -> Result<CallToolResult, String>
where
    F: Future<Output = Result<CallToolResult, rmcp::ServiceError>> + Send + 'static,
{
    let handle = Handle::try_current().map_err(|source| {
        format!(
            "failed to access tokio runtime for MCP tool bridge: {source}. this bridge requires a \
             running tokio runtime"
        )
    })?;

    if handle.runtime_flavor() != RuntimeFlavor::MultiThread {
        return Err(
            "MCP tool bridge requires tokio multi-thread runtime while aisdk tool callbacks are \
             synchronous"
                .to_owned(),
        );
    }

    tokio::task::block_in_place(|| handle.block_on(future)).map_err(|source| source.to_string())
}
