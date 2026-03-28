use std::fmt::{Display, Formatter};

use schemars::JsonSchema;
use serde::{Deserialize, Serialize, Serializer, ser::Error as _};
use serde_json::json;
use typia::LLMData;
use typia_llm::{LlmToolBuildError, LlmToolSpec, tool};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema, LLMData)]
struct SumInput {
    left: u32,
    right: u32,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
struct SumOutput {
    total: u32,
}

#[test]
fn tool_executes_with_typed_input_and_output() {
    let sum_tool = tool::<SumInput, SumOutput, _, HandlerError>(
        LlmToolSpec::new("sum", "Adds two unsigned integers"),
        |input| {
            Ok(SumOutput {
                total: input.left + input.right,
            })
        },
    )
    .expect("tool must build");

    let raw = sum_tool
        .execute
        .call(json!({ "left": 2, "right": 3 }))
        .expect("tool execution should succeed");
    let output: SumOutput = serde_json::from_str(&raw).expect("output json must be valid");

    assert_eq!(output, SumOutput { total: 5 });
}

#[test]
fn tool_coerces_stringified_numbers_with_typia_harness() {
    let sum_tool = tool::<SumInput, SumOutput, _, HandlerError>(
        LlmToolSpec::new("sum", "Adds two unsigned integers"),
        |input| {
            Ok(SumOutput {
                total: input.left + input.right,
            })
        },
    )
    .expect("tool must build");

    let raw = sum_tool
        .execute
        .call(json!({ "left": "40", "right": "2" }))
        .expect("typia coercion should recover stringified numbers");
    let output: SumOutput = serde_json::from_str(&raw).expect("output json must be valid");

    assert_eq!(output, SumOutput { total: 42 });
}

#[test]
fn tool_returns_structured_feedback_on_validation_failure() {
    let sum_tool = tool::<SumInput, SumOutput, _, HandlerError>(
        LlmToolSpec::new("sum", "Adds two unsigned integers"),
        |input| {
            Ok(SumOutput {
                total: input.left + input.right,
            })
        },
    )
    .expect("tool must build");

    let error = sum_tool
        .execute
        .call(json!({ "left": "not-a-number", "right": 2 }))
        .expect_err("validation should fail");
    let message = error.to_string();

    assert!(message.contains("typia-llm input validation failed."));
    assert!(message.contains("$input.left"));
    assert!(message.contains("expected="));
}

#[test]
fn tool_propagates_handler_error() {
    let sum_tool = tool::<SumInput, SumOutput, _, HandlerError>(
        LlmToolSpec::new("sum", "Adds two unsigned integers"),
        |_input| Err(HandlerError("sum is disabled".to_owned())),
    )
    .expect("tool must build");

    let error = sum_tool
        .execute
        .call(json!({ "left": 1, "right": 2 }))
        .expect_err("handler should fail");

    assert!(
        error
            .to_string()
            .contains("tool handler failed: sum is disabled")
    );
}

#[test]
fn tool_reports_output_serialization_failures() {
    let sum_tool = tool::<SumInput, FailingOutput, _, HandlerError>(
        LlmToolSpec::new("sum", "Adds two unsigned integers"),
        |_input| Ok(FailingOutput),
    )
    .expect("tool must build");

    let error = sum_tool
        .execute
        .call(json!({ "left": 1, "right": 2 }))
        .expect_err("serialization should fail");

    assert!(
        error
            .to_string()
            .contains("failed to serialize tool output: forced serialization failure")
    );
}

#[test]
fn tool_schema_exposes_typed_input_properties() {
    let sum_tool = tool::<SumInput, SumOutput, _, HandlerError>(
        LlmToolSpec::new("sum", "Adds two unsigned integers"),
        |input| {
            Ok(SumOutput {
                total: input.left + input.right,
            })
        },
    )
    .expect("tool must build");

    let schema = serde_json::to_value(&sum_tool.input_schema).expect("schema must be serializable");
    let properties = schema
        .get("properties")
        .and_then(serde_json::Value::as_object)
        .expect("object schema must expose properties");

    assert!(properties.contains_key("left"));
    assert!(properties.contains_key("right"));

    let required = schema
        .get("required")
        .and_then(serde_json::Value::as_array)
        .expect("object schema must expose required fields");

    assert!(required.iter().any(|value| value == "left"));
    assert!(required.iter().any(|value| value == "right"));
}

#[test]
fn tool_validates_empty_spec_fields() {
    let error = tool::<SumInput, SumOutput, _, HandlerError>(
        LlmToolSpec::new("", "Adds two unsigned integers"),
        |_input| Ok(SumOutput { total: 0 }),
    )
    .expect_err("empty names must be rejected");

    assert_eq!(
        error,
        LlmToolBuildError::InvalidSpec {
            reason: "name must not be empty".to_owned(),
        }
    );
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct HandlerError(String);

impl Display for HandlerError {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.0)
    }
}

impl std::error::Error for HandlerError {}

#[derive(Debug, Clone, PartialEq, Eq)]
struct FailingOutput;

impl Serialize for FailingOutput {
    fn serialize<S>(&self, _serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        Err(S::Error::custom("forced serialization failure"))
    }
}
