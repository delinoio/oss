#![forbid(unsafe_code)]

//! Serde-based LLM JSON utilities and data trait for `typia`.

use serde::de::DeserializeOwned;

mod lenient_json;
mod validate;

pub use lenient_json::parse_lenient_json_value;
pub use serde;
pub use serde_json;
#[cfg(feature = "derive")]
pub use typia_macros::LLMData;
pub use validate::{
    IValidation, IValidationError, TagRuntime, Validate, apply_tags, join_index_path,
    join_object_path, merge_prefixed_errors, prepend_path,
};

/// Detailed parsing error emitted by the lenient parser or serde validation.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LlmJsonParseError {
    pub path: String,
    pub expected: String,
    pub description: String,
}

/// Result of LLM JSON parsing.
#[derive(Debug, Clone, PartialEq)]
pub enum LlmJsonParseResult<T> {
    Success {
        data: T,
    },
    Failure {
        data: Option<serde_json::Value>,
        input: String,
        errors: Vec<LlmJsonParseError>,
    },
}

/// Trait for Serde-powered LLM data parsing/validation/stringification.
pub trait LLMData: Validate + serde::Serialize + DeserializeOwned + Sized {
    /// Parse raw LLM output using typia's lenient JSON parser and then validate
    /// the shape through serde deserialization.
    fn parse(input: &str) -> LlmJsonParseResult<Self> {
        match parse_lenient_json_value(input) {
            LlmJsonParseResult::Success { data } => match Self::validate(data.clone()) {
                IValidation::Success { data: decoded } => {
                    LlmJsonParseResult::Success { data: decoded }
                }
                IValidation::Failure { errors, .. } => LlmJsonParseResult::Failure {
                    data: Some(data),
                    input: input.to_owned(),
                    errors: errors
                        .into_iter()
                        .map(|error| LlmJsonParseError {
                            path: error.path,
                            expected: error.expected,
                            description: error
                                .description
                                .unwrap_or_else(|| "validation failed".to_owned()),
                        })
                        .collect(),
                },
            },
            LlmJsonParseResult::Failure {
                data,
                input,
                mut errors,
            } => {
                if let Some(value) = data.as_ref()
                    && let IValidation::Failure {
                        errors: validation_errors,
                        ..
                    } = Self::validate(value.clone())
                {
                    errors.extend(validation_errors.into_iter().map(|error| {
                        LlmJsonParseError {
                            path: error.path,
                            expected: error.expected,
                            description: error
                                .description
                                .unwrap_or_else(|| "validation failed".to_owned()),
                        }
                    }));
                }

                LlmJsonParseResult::Failure {
                    data,
                    input,
                    errors,
                }
            }
        }
    }

    /// Serialize into compact JSON text.
    fn stringify(&self) -> Result<String, serde_json::Error> {
        serde_json::to_string(self)
    }
}

#[doc(hidden)]
pub mod __private {
    pub use crate::validate::__private::*;
}
