use std::{collections::VecDeque, pin::Pin, sync::Arc};

use aisdk::core::{
    Message,
    capabilities::{TextInputSupport, ToolCallSupport},
    language_model::{
        LanguageModel, LanguageModelOptions, LanguageModelResponse,
        LanguageModelResponseContentType, LanguageModelStreamChunk, Usage,
    },
    tools::{Tool, ToolCallInfo, ToolExecute},
};
use anyhow::Result;
use async_trait::async_trait;
use futures::Stream;
use rmcp::{
    ClientHandler, ServerHandler, ServiceExt,
    handler::server::{router::tool::ToolRouter, wrapper::Parameters},
    model::{ClientInfo, ServerCapabilities, ServerInfo},
    tool, tool_handler, tool_router,
};
use rustia::LLMData;
use rustia_agentica::{
    ClassController, ConversationStopReason, McpController, MicroAgentica, MicroAgenticaBuildError,
};
use rustia_llm::{LlmToolSpec, tool as rustia_tool};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use tokio::sync::Mutex;

#[derive(Debug, Clone)]
struct ScriptedModel {
    state: Arc<Mutex<ScriptedState>>,
}

#[derive(Debug, Default)]
struct ScriptedState {
    responses: VecDeque<LanguageModelResponse>,
    requests: Vec<LanguageModelOptions>,
}

impl ScriptedModel {
    fn new(responses: Vec<LanguageModelResponse>) -> Self {
        Self {
            state: Arc::new(Mutex::new(ScriptedState {
                responses: responses.into(),
                requests: Vec::new(),
            })),
        }
    }

    async fn requests(&self) -> Vec<LanguageModelOptions> {
        self.state.lock().await.requests.clone()
    }
}

impl ToolCallSupport for ScriptedModel {}
impl TextInputSupport for ScriptedModel {}

#[async_trait]
impl LanguageModel for ScriptedModel {
    fn name(&self) -> String {
        "scripted-model".to_owned()
    }

    async fn generate_text(
        &mut self,
        options: LanguageModelOptions,
    ) -> aisdk::Result<LanguageModelResponse> {
        let mut state = self.state.lock().await;
        state.requests.push(options);
        state
            .responses
            .pop_front()
            .ok_or_else(|| aisdk::Error::Other("missing scripted response".to_owned()))
    }

    async fn stream_text(
        &mut self,
        _options: LanguageModelOptions,
    ) -> aisdk::Result<
        Pin<Box<dyn Stream<Item = aisdk::Result<Vec<LanguageModelStreamChunk>>> + Send>>,
    > {
        Err(aisdk::Error::Other(
            "streaming is not used in these tests".to_owned(),
        ))
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema, LLMData)]
struct SumInput {
    left: u32,
    right: u32,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
struct SumOutput {
    total: u32,
}

fn build_sum_tool() -> Tool {
    rustia_tool::<SumInput, SumOutput, _, String>(
        LlmToolSpec::new("sum", "Adds two integers"),
        |input| {
            Ok(SumOutput {
                total: input.left + input.right,
            })
        },
    )
    .expect("sum tool must build")
}

fn build_simple_tool(name: &str) -> Tool {
    Tool::builder()
        .name(name)
        .description("simple test tool")
        .input_schema(
            schemars::Schema::try_from(json!({
                "type": "object",
                "properties": {},
                "required": []
            }))
            .expect("schema must be valid"),
        )
        .execute(ToolExecute::new(Box::new(|_input| Ok("ok".to_owned()))))
        .build()
        .expect("tool must build")
}

fn usage(input: usize, output: usize) -> Option<Usage> {
    Some(Usage {
        input_tokens: Some(input),
        output_tokens: Some(output),
        reasoning_tokens: None,
        cached_tokens: None,
    })
}

fn tool_call_response(
    tool_name: &str,
    call_id: &str,
    input: Value,
    usage: Option<Usage>,
) -> LanguageModelResponse {
    let mut call = ToolCallInfo::new(tool_name);
    call.id(call_id);
    call.input(input);

    LanguageModelResponse {
        contents: vec![LanguageModelResponseContentType::ToolCall(call)],
        usage,
    }
}

fn text_response(text: &str, usage: Option<Usage>) -> LanguageModelResponse {
    LanguageModelResponse {
        contents: vec![LanguageModelResponseContentType::Text(text.to_owned())],
        usage,
    }
}

fn last_tool_output(messages: &[Message]) -> Option<String> {
    messages.iter().rev().find_map(|message| match message {
        Message::Tool(info) => match &info.output {
            Ok(Value::String(value)) => Some(value.clone()),
            Ok(value) => Some(value.to_string()),
            Err(error) => Some(error.to_string()),
        },
        _ => None,
    })
}

#[tokio::test(flavor = "multi_thread")]
async fn class_tool_applies_rustia_coercion_before_execution() -> Result<()> {
    let model = ScriptedModel::new(vec![
        tool_call_response(
            "sum",
            "call-1",
            json!({ "left": "40", "right": "2" }),
            usage(10, 3),
        ),
        text_response("done", usage(4, 2)),
    ]);

    let mut agent = MicroAgentica::new(
        model.clone(),
        vec![ClassController::named("math", vec![build_sum_tool()]).into()],
    )
    .await?;

    let outcome = agent.conversate("add the numbers").await?;

    assert_eq!(outcome.assistant_text.as_deref(), Some("done"));
    assert_eq!(outcome.stop_reason, ConversationStopReason::Finish);

    let requests = model.requests().await;
    assert_eq!(requests.len(), 2);

    let second_messages = requests[1].messages();
    let output = last_tool_output(&second_messages).expect("tool output must exist");
    assert!(output.contains("\"total\":42"));

    Ok(())
}

#[tokio::test(flavor = "multi_thread")]
async fn class_tool_validation_feedback_is_forwarded_to_the_loop() -> Result<()> {
    let model = ScriptedModel::new(vec![
        tool_call_response(
            "sum",
            "call-1",
            json!({ "left": "not-a-number", "right": 2 }),
            usage(7, 2),
        ),
        text_response("please retry with valid input", usage(2, 1)),
    ]);

    let mut agent = MicroAgentica::new(
        model.clone(),
        vec![ClassController::named("math", vec![build_sum_tool()]).into()],
    )
    .await?;

    let outcome = agent.conversate("run sum").await?;
    assert_eq!(
        outcome.assistant_text.as_deref(),
        Some("please retry with valid input")
    );

    let requests = model.requests().await;
    assert_eq!(requests.len(), 2);

    let second_messages = requests[1].messages();
    let output = last_tool_output(&second_messages).expect("tool output must exist");
    assert!(output.contains("rustia-llm input validation failed."));
    assert!(output.contains("$input.left"));

    let failing_step = outcome
        .steps
        .iter()
        .find(|step| !step.tool_results.is_empty())
        .expect("tool step must exist");
    assert!(failing_step.tool_results[0].is_error);

    Ok(())
}

#[tokio::test(flavor = "multi_thread")]
async fn rejects_duplicate_tool_names_at_build_time() {
    let model = ScriptedModel::new(vec![]);

    let result = MicroAgentica::new(
        model,
        vec![
            ClassController::named("a", vec![build_simple_tool("duplicate")]).into(),
            ClassController::named("b", vec![build_simple_tool("duplicate")]).into(),
        ],
    )
    .await;

    assert!(matches!(
        result,
        Err(MicroAgenticaBuildError::DuplicateToolName { .. })
    ));
}

#[derive(Debug, Clone, Deserialize, JsonSchema)]
struct EchoInput {
    message: String,
}

#[derive(Debug, Clone)]
struct EchoMcpServer {
    tool_router: ToolRouter<Self>,
}

impl EchoMcpServer {
    fn new() -> Self {
        Self {
            tool_router: Self::tool_router(),
        }
    }
}

#[tool_router]
impl EchoMcpServer {
    #[tool(name = "echo", description = "Echo the input message")]
    fn echo(&self, Parameters(EchoInput { message }): Parameters<EchoInput>) -> String {
        format!("echo:{message}")
    }
}

#[tool_handler(router = self.tool_router)]
impl ServerHandler for EchoMcpServer {
    fn get_info(&self) -> ServerInfo {
        ServerInfo {
            capabilities: ServerCapabilities::builder().enable_tools().build(),
            ..Default::default()
        }
    }
}

#[derive(Debug, Clone, Default)]
struct DummyClient;

impl ClientHandler for DummyClient {
    fn get_info(&self) -> ClientInfo {
        ClientInfo::default()
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn mcp_bridge_converts_and_executes_remote_tools() -> Result<()> {
    let (server_transport, client_transport) = tokio::io::duplex(4096);

    let server_task = tokio::spawn(async move {
        let server = EchoMcpServer::new().serve(server_transport).await?;
        server.waiting().await?;
        anyhow::Ok(())
    });

    let client = DummyClient.serve(client_transport).await?;
    let peer = client.peer().clone();

    let model = ScriptedModel::new(vec![
        tool_call_response(
            "echo",
            "call-mcp",
            json!({ "message": "hello" }),
            usage(4, 2),
        ),
        text_response("mcp complete", usage(2, 1)),
    ]);

    let mut agent = MicroAgentica::new(
        model.clone(),
        vec![McpController::new("local-mcp", peer).into()],
    )
    .await?;

    let outcome = agent.conversate("call echo").await?;
    assert_eq!(outcome.assistant_text.as_deref(), Some("mcp complete"));

    let requests = model.requests().await;
    assert_eq!(requests.len(), 2);

    let second_messages = requests[1].messages();
    let output = last_tool_output(&second_messages).expect("MCP output must exist");
    assert_eq!(output, "echo:hello");

    let _ = client.cancel().await;
    server_task.abort();

    Ok(())
}

#[tokio::test(flavor = "multi_thread")]
async fn accumulates_history_and_usage_across_turns() -> Result<()> {
    let model = ScriptedModel::new(vec![
        tool_call_response(
            "sum",
            "call-1",
            json!({ "left": "1", "right": "2" }),
            usage(6, 2),
        ),
        text_response("first turn done", usage(3, 1)),
        text_response("second turn done", usage(5, 2)),
    ]);

    let mut agent = MicroAgentica::new(
        model,
        vec![ClassController::named("math", vec![build_sum_tool()]).into()],
    )
    .await?;

    let first = agent.conversate("first").await?;
    let second = agent.conversate("second").await?;

    assert_eq!(first.usage.input_tokens, Some(9));
    assert_eq!(first.usage.output_tokens, Some(3));
    assert_eq!(first.total_usage.input_tokens, Some(9));
    assert_eq!(first.total_usage.output_tokens, Some(3));

    assert_eq!(second.usage.input_tokens, Some(5));
    assert_eq!(second.usage.output_tokens, Some(2));
    assert_eq!(second.total_usage.input_tokens, Some(14));
    assert_eq!(second.total_usage.output_tokens, Some(5));

    assert!(second.messages.len() > first.messages.len());
    assert_eq!(second.stop_reason, ConversationStopReason::Finish);

    Ok(())
}
