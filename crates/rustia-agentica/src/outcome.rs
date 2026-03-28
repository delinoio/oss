use aisdk::core::{
    Message, Messages,
    language_model::{
        LanguageModelResponseContentType, StopReason, Usage, generate_text::GenerateTextResponse,
    },
};
use serde_json::Value;

/// Aggregated result of one `MicroAgentica::conversate` turn.
#[derive(Debug, Clone)]
pub struct ConversationOutcome {
    pub assistant_text: Option<String>,
    pub stop_reason: ConversationStopReason,
    pub steps: Vec<StepRecord>,
    pub usage: UsageSummary,
    pub total_usage: UsageSummary,
    pub messages: Messages,
}

impl ConversationOutcome {
    pub(crate) fn from_response(
        response: &GenerateTextResponse,
        messages: Messages,
        usage: UsageSummary,
        total_usage: UsageSummary,
    ) -> Self {
        Self {
            assistant_text: response.text(),
            stop_reason: response
                .stop_reason()
                .map(ConversationStopReason::from)
                .unwrap_or(ConversationStopReason::Unknown),
            steps: response.steps().iter().map(StepRecord::from_step).collect(),
            usage,
            total_usage,
            messages,
        }
    }
}

/// Stop reason normalized from AISDK stop signals.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ConversationStopReason {
    Finish,
    Hook,
    Error(String),
    Unknown,
}

impl From<StopReason> for ConversationStopReason {
    fn from(value: StopReason) -> Self {
        match value {
            StopReason::Finish => Self::Finish,
            StopReason::Hook => Self::Hook,
            StopReason::Error(error) => Self::Error(error.to_string()),
            StopReason::Provider(reason) => Self::Error(reason),
            StopReason::Other(reason) => Self::Error(reason),
        }
    }
}

/// One step record from the AISDK generation loop.
#[derive(Debug, Clone, PartialEq)]
pub struct StepRecord {
    pub step_id: usize,
    pub assistant_texts: Vec<String>,
    pub tool_calls: Vec<ToolCallRecord>,
    pub tool_results: Vec<ToolResultRecord>,
    pub usage: UsageSummary,
}

impl StepRecord {
    fn from_step(step: &aisdk::core::language_model::Step) -> Self {
        let mut assistant_texts = Vec::new();
        let mut tool_calls = Vec::new();
        let mut tool_results = Vec::new();

        for message in step.messages() {
            match message {
                Message::Assistant(assistant) => match &assistant.content {
                    LanguageModelResponseContentType::Text(text) => {
                        assistant_texts.push(text.clone());
                    }
                    LanguageModelResponseContentType::ToolCall(info) => {
                        tool_calls.push(ToolCallRecord {
                            id: info.tool.id.clone(),
                            name: info.tool.name.clone(),
                            input: info.input.clone(),
                        });
                    }
                    _ => {}
                },
                Message::Tool(result) => {
                    let (mut is_error, output) = match &result.output {
                        Ok(value) => (false, value.clone()),
                        Err(error) => (true, Value::String(error.to_string())),
                    };

                    if let Value::String(text) = &output {
                        if text.starts_with("Error:") {
                            is_error = true;
                        }
                    }

                    tool_results.push(ToolResultRecord {
                        id: result.tool.id.clone(),
                        name: result.tool.name.clone(),
                        output,
                        is_error,
                    });
                }
                _ => {}
            }
        }

        Self {
            step_id: step.step_id,
            assistant_texts,
            tool_calls,
            tool_results,
            usage: UsageSummary::from_usage(&step.usage()),
        }
    }
}

/// Tool call trace record.
#[derive(Debug, Clone, PartialEq)]
pub struct ToolCallRecord {
    pub id: String,
    pub name: String,
    pub input: Value,
}

/// Tool result trace record.
#[derive(Debug, Clone, PartialEq)]
pub struct ToolResultRecord {
    pub id: String,
    pub name: String,
    pub output: Value,
    pub is_error: bool,
}

/// Stable usage summary shape used by `ConversationOutcome` and `StepRecord`.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct UsageSummary {
    pub input_tokens: Option<usize>,
    pub output_tokens: Option<usize>,
    pub reasoning_tokens: Option<usize>,
    pub cached_tokens: Option<usize>,
}

impl UsageSummary {
    pub fn from_usage(usage: &Usage) -> Self {
        Self {
            input_tokens: usage.input_tokens,
            output_tokens: usage.output_tokens,
            reasoning_tokens: usage.reasoning_tokens,
            cached_tokens: usage.cached_tokens,
        }
    }

    pub fn saturating_sub(&self, baseline: &UsageSummary) -> UsageSummary {
        UsageSummary {
            input_tokens: subtract_options(self.input_tokens, baseline.input_tokens),
            output_tokens: subtract_options(self.output_tokens, baseline.output_tokens),
            reasoning_tokens: subtract_options(self.reasoning_tokens, baseline.reasoning_tokens),
            cached_tokens: subtract_options(self.cached_tokens, baseline.cached_tokens),
        }
    }
}

fn subtract_options(total: Option<usize>, baseline: Option<usize>) -> Option<usize> {
    match (total, baseline) {
        (Some(left), Some(right)) => Some(left.saturating_sub(right)),
        (Some(left), None) => Some(left),
        (None, Some(_)) => None,
        (None, None) => None,
    }
}
