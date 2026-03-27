#![forbid(unsafe_code)]

//! Serde-based LLM JSON utilities and data trait for `typia`.

use serde::de::DeserializeOwned;

mod lenient_json;

pub use lenient_json::parse_lenient_json_value;
pub use serde;
pub use serde_json;
#[cfg(feature = "derive")]
pub use typia_macros::LLMData;

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
pub trait LLMData: serde::Serialize + DeserializeOwned + Sized {
    /// Parse raw LLM output using typia's lenient JSON parser and then validate
    /// the shape through serde deserialization.
    fn parse(input: &str) -> LlmJsonParseResult<Self> {
        match parse_lenient_json_value(input) {
            LlmJsonParseResult::Success { data } => match deserialize_with_path::<Self>(&data) {
                Ok(decoded) => LlmJsonParseResult::Success { data: decoded },
                Err(error) => LlmJsonParseResult::Failure {
                    data: Some(data),
                    input: input.to_owned(),
                    errors: vec![error],
                },
            },
            LlmJsonParseResult::Failure {
                data,
                input,
                mut errors,
            } => {
                if let Some(value) = data.as_ref()
                    && let Err(error) = deserialize_with_path::<Self>(value)
                {
                    errors.push(error);
                }

                LlmJsonParseResult::Failure {
                    data,
                    input,
                    errors,
                }
            }
        }
    }

    /// Validate a parsed JSON value using serde deserialization.
    fn validate(value: serde_json::Value) -> Result<Self, serde_json::Error> {
        serde_json::from_value(value)
    }

    /// Serialize into compact JSON text.
    fn stringify(&self) -> Result<String, serde_json::Error> {
        serde_json::to_string(self)
    }
}

fn deserialize_with_path<T>(value: &serde_json::Value) -> Result<T, LlmJsonParseError>
where
    T: DeserializeOwned,
{
    let encoded = serde_json::to_vec(value).map_err(|error| LlmJsonParseError {
        path: "$input".to_owned(),
        expected: "JSON value".to_owned(),
        description: error.to_string(),
    })?;

    let mut deserializer = serde_json::Deserializer::from_slice(&encoded);
    serde_path_to_error::deserialize::<_, T>(&mut deserializer).map_err(|error| {
        let raw_path = error.path().to_string();
        let path = normalize_path(&raw_path);
        let description = error.into_inner().to_string();

        LlmJsonParseError {
            path,
            expected: "serde-compatible schema".to_owned(),
            description,
        }
    })
}

fn normalize_path(path: &str) -> String {
    if path.is_empty() {
        "$input".to_owned()
    } else if path.starts_with('[') {
        format!("$input{path}")
    } else {
        format!("$input.{path}")
    }
}
