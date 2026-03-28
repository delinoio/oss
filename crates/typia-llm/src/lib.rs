#![forbid(unsafe_code)]

//! typia-powered adapter utilities for aisdk tool calling.

use std::fmt::{Display, Formatter};

use aisdk::core::tools::{Tool, ToolExecute};
use schemars::{JsonSchema, schema_for};
use serde::Serialize;
use serde_json::Value;
use tracing::{error, info};
use typia::{LLMData, LlmJsonParseError, LlmJsonParseResult};

/// Input contract for typia-llm tools.
pub trait LlmToolInput: LLMData + JsonSchema + Send + Sync + 'static {}

impl<T> LlmToolInput for T where T: LLMData + JsonSchema + Send + Sync + 'static {}

/// Output contract for typia-llm tools.
pub trait LlmToolOutput: Serialize + Send + Sync + 'static {}

impl<T> LlmToolOutput for T where T: Serialize + Send + Sync + 'static {}

/// Tool metadata used by [`tool`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LlmToolSpec {
    pub name: String,
    pub description: String,
}

impl LlmToolSpec {
    /// Creates a new tool specification.
    pub fn new(name: impl Into<String>, description: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            description: description.into(),
        }
    }
}

/// Build-time error for tool creation.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum LlmToolBuildError {
    InvalidSpec { reason: String },
    ToolBuild { reason: String },
}

impl Display for LlmToolBuildError {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidSpec { reason } => write!(f, "invalid tool spec: {reason}"),
            Self::ToolBuild { reason } => write!(f, "failed to build aisdk tool: {reason}"),
        }
    }
}

impl std::error::Error for LlmToolBuildError {}

/// Input parse/validation error emitted by typia harness.
#[derive(Debug, Clone, PartialEq)]
pub struct LlmToolInputError {
    pub input_json: Value,
    pub recovered_json: Option<Value>,
    pub errors: Vec<LlmJsonParseError>,
}

impl LlmToolInputError {
    /// Formats a deterministic feedback message suitable for returning to an
    /// LLM.
    pub fn to_feedback_string(&self) -> String {
        let mut output = String::new();
        output.push_str("typia-llm input validation failed.\n");
        output.push_str("input_json:\n");
        output.push_str(&to_pretty_json(&self.input_json));
        output.push('\n');

        if let Some(value) = &self.recovered_json {
            output.push_str("recovered_json:\n");
            output.push_str(&to_pretty_json(value));
            output.push('\n');
        }

        output.push_str("errors:\n");
        for (index, error) in self.errors.iter().enumerate() {
            output.push_str(&format!(
                "{}. path={} expected={} description={}\n",
                index + 1,
                error.path,
                error.expected,
                error.description,
            ));
        }

        output
    }
}

impl Display for LlmToolInputError {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "typia-llm input validation failed with {} error(s)",
            self.errors.len()
        )
    }
}

impl std::error::Error for LlmToolInputError {}

/// Execution-time error for typed tool handling.
#[derive(Debug)]
pub enum LlmToolExecutionError {
    Input(LlmToolInputError),
    Handler { message: String },
    SerializeOutput { source: serde_json::Error },
}

impl LlmToolExecutionError {
    fn to_tool_error_message(&self) -> String {
        match self {
            Self::Input(error) => error.to_feedback_string(),
            _ => self.to_string(),
        }
    }
}

impl Display for LlmToolExecutionError {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Input(error) => write!(f, "{error}"),
            Self::Handler { message } => write!(f, "tool handler failed: {message}"),
            Self::SerializeOutput { source } => {
                write!(f, "failed to serialize tool output: {source}")
            }
        }
    }
}

impl std::error::Error for LlmToolExecutionError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::SerializeOutput { source } => Some(source),
            Self::Input(error) => Some(error),
            Self::Handler { .. } => None,
        }
    }
}

/// Builds an aisdk [`Tool`] using typia parsing/coercion/validation for tool
/// input.
pub fn tool<I, O, F, E>(spec: LlmToolSpec, handler: F) -> Result<Tool, LlmToolBuildError>
where
    I: LlmToolInput,
    O: LlmToolOutput,
    F: Fn(I) -> Result<O, E> + Send + Sync + 'static,
    E: Display,
{
    let name = spec.name.trim().to_owned();
    let description = spec.description.trim().to_owned();

    if name.is_empty() {
        return Err(LlmToolBuildError::InvalidSpec {
            reason: "name must not be empty".to_owned(),
        });
    }
    if description.is_empty() {
        return Err(LlmToolBuildError::InvalidSpec {
            reason: "description must not be empty".to_owned(),
        });
    }

    let tool_name = name.clone();
    let input_schema = schema_for!(I);
    let execute = ToolExecute::new(Box::new(move |input| {
        execute_tool::<I, O, F, E>(&tool_name, &handler, input)
            .map_err(|error| error.to_tool_error_message())
    }));

    Tool::builder()
        .name(name)
        .description(description)
        .input_schema(input_schema)
        .execute(execute)
        .build()
        .map_err(|error| LlmToolBuildError::ToolBuild {
            reason: error.to_string(),
        })
}

fn execute_tool<I, O, F, E>(
    tool_name: &str,
    handler: &F,
    input_json: Value,
) -> Result<String, LlmToolExecutionError>
where
    I: LlmToolInput,
    O: LlmToolOutput,
    F: Fn(I) -> Result<O, E> + Send + Sync + 'static,
    E: Display,
{
    let input = parse_input::<I>(input_json).map_err(|error| {
        error!(
            tool_name,
            parse_success = false,
            parse_failure = true,
            error_count = error.errors.len(),
            "failed to parse tool input"
        );
        LlmToolExecutionError::Input(error)
    })?;

    info!(
        tool_name,
        parse_success = true,
        parse_failure = false,
        error_count = 0,
        "parsed tool input"
    );

    let output = handler(input).map_err(|error| {
        let message = error.to_string();
        error!(tool_name, %message, "tool handler execution failed");
        LlmToolExecutionError::Handler { message }
    })?;

    let payload = serde_json::to_string(&output).map_err(|source| {
        error!(tool_name, %source, "tool output serialization failed");
        LlmToolExecutionError::SerializeOutput { source }
    })?;

    info!(tool_name, "tool execution succeeded");
    Ok(payload)
}

fn parse_input<I>(input_json: Value) -> Result<I, LlmToolInputError>
where
    I: LlmToolInput,
{
    let raw_input = input_json.to_string();
    match I::parse(&raw_input) {
        LlmJsonParseResult::Success { data } => Ok(data),
        LlmJsonParseResult::Failure { data, errors, .. } => Err(LlmToolInputError {
            input_json,
            recovered_json: data,
            errors,
        }),
    }
}

fn to_pretty_json(value: &Value) -> String {
    serde_json::to_string_pretty(value).unwrap_or_else(|_| value.to_string())
}
