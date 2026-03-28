#![forbid(unsafe_code)]

//! MicroAgentica-style Rust agent loop powered by AISDK and rustia tooling.

use std::collections::HashMap;

use aisdk::core::{
    Message, Messages,
    capabilities::{TextInputSupport, ToolCallSupport},
    language_model::{LanguageModel, request::LanguageModelRequest},
    tools::Tool,
    utils::step_count_is,
};
use tracing::info;

mod config;
mod controller;
mod error;
mod mcp;
mod outcome;

pub use config::{DEFAULT_MAX_STEPS, MicroAgenticaConfig};
pub use controller::{ClassController, McpController, MicroAgenticaController};
pub use error::{MicroAgenticaBuildError, MicroAgenticaConversationError, ToolOrigin};
pub use outcome::{
    ConversationOutcome, ConversationStopReason, StepRecord, ToolCallRecord, ToolResultRecord,
    UsageSummary,
};

/// Minimal function-calling agent loop that maps class/MCP controllers into
/// AISDK tools.
#[derive(Debug)]
pub struct MicroAgentica<M>
where
    M: LanguageModel + ToolCallSupport + TextInputSupport + Clone,
{
    model: M,
    config: MicroAgenticaConfig,
    tools: Vec<Tool>,
    messages: Messages,
    total_usage: UsageSummary,
}

impl<M> MicroAgentica<M>
where
    M: LanguageModel + ToolCallSupport + TextInputSupport + Clone,
{
    /// Creates a new instance with default configuration.
    pub async fn new(
        model: M,
        controllers: Vec<MicroAgenticaController>,
    ) -> Result<Self, MicroAgenticaBuildError> {
        Self::with_config(model, controllers, MicroAgenticaConfig::default()).await
    }

    /// Creates a new instance with explicit configuration.
    pub async fn with_config(
        model: M,
        controllers: Vec<MicroAgenticaController>,
        config: MicroAgenticaConfig,
    ) -> Result<Self, MicroAgenticaBuildError> {
        let tools = flatten_tools(controllers).await?;
        info!(
            tool_count = tools.len(),
            max_steps = config.max_steps,
            "initialized MicroAgentica"
        );

        Ok(Self {
            model,
            config,
            tools,
            messages: Vec::new(),
            total_usage: UsageSummary::default(),
        })
    }

    /// Executes one user turn with AISDK `generate_text` loop.
    pub async fn conversate(
        &mut self,
        user_text: impl Into<String>,
    ) -> Result<ConversationOutcome, MicroAgenticaConversationError> {
        let user_text = user_text.into();
        info!(
            user_text_len = user_text.chars().count(),
            existing_history_len = self.messages.len(),
            tool_count = self.tools.len(),
            "starting conversation turn"
        );

        let mut request_messages = self.messages.clone();
        request_messages.push(Message::User(user_text.into()));

        let mut builder = LanguageModelRequest::builder()
            .model(self.model.clone())
            .system(self.config.render_system_prompt())
            .messages(request_messages);
        for tool in &self.tools {
            builder = builder.with_tool(tool.clone());
        }

        let mut request = builder
            .stop_when(step_count_is(self.config.max_steps))
            .build();

        let response = request.generate_text().await.map_err(|source| {
            MicroAgenticaConversationError::ModelRequest {
                reason: source.to_string(),
            }
        })?;

        let response_total_usage = UsageSummary::from_usage(&response.usage());
        let usage = response_total_usage.saturating_sub(&self.total_usage);
        self.total_usage = response_total_usage.clone();

        let messages = response.messages();
        self.messages = messages.clone();

        let outcome =
            ConversationOutcome::from_response(&response, messages, usage, response_total_usage);
        info!(
            step_count = outcome.steps.len(),
            stop_reason = ?outcome.stop_reason,
            "conversation turn completed"
        );
        Ok(outcome)
    }

    /// Returns registered tools.
    pub fn tools(&self) -> &[Tool] {
        &self.tools
    }

    /// Returns current conversation history.
    pub fn messages(&self) -> &[Message] {
        &self.messages
    }

    /// Returns cumulative usage across conversation turns.
    pub fn total_usage(&self) -> &UsageSummary {
        &self.total_usage
    }

    /// Returns active runtime config.
    pub fn config(&self) -> &MicroAgenticaConfig {
        &self.config
    }
}

async fn flatten_tools(
    controllers: Vec<MicroAgenticaController>,
) -> Result<Vec<Tool>, MicroAgenticaBuildError> {
    let mut names = HashMap::<String, ToolOrigin>::new();
    let mut tools = Vec::new();

    for controller in controllers {
        match controller {
            MicroAgenticaController::Class(controller) => {
                let origin = ToolOrigin::Class {
                    controller: controller.name.clone(),
                };
                for tool in controller.tools {
                    register_tool(&mut tools, &mut names, tool, origin.clone())?;
                }
            }
            MicroAgenticaController::Mcp(controller) => {
                let origin = ToolOrigin::Mcp {
                    controller: controller.name.clone(),
                };
                for tool in mcp::build_mcp_tools(&controller.name, controller.peer).await? {
                    register_tool(&mut tools, &mut names, tool, origin.clone())?;
                }
            }
        }
    }

    Ok(tools)
}

fn register_tool(
    sink: &mut Vec<Tool>,
    names: &mut HashMap<String, ToolOrigin>,
    tool: Tool,
    origin: ToolOrigin,
) -> Result<(), MicroAgenticaBuildError> {
    let name = tool.name.trim().to_owned();
    if name.is_empty() {
        return Err(MicroAgenticaBuildError::EmptyToolName { origin });
    }

    if let Some(previous_origin) = names.get(&name) {
        return Err(MicroAgenticaBuildError::DuplicateToolName {
            name,
            first_origin: previous_origin.clone(),
            second_origin: origin,
        });
    }

    names.insert(name, origin);
    sink.push(tool);
    Ok(())
}
