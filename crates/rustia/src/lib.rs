#![forbid(unsafe_code)]

//! Serde-based LLM JSON utilities and data trait for `rustia`.

use std::collections::HashSet;

use serde::de::DeserializeOwned;
use serde_json::Value;

mod lenient_json;
mod validate;

pub use lenient_json::parse_lenient_json_value;
#[cfg(feature = "derive")]
pub use rustia_macros::LLMData;
pub use serde;
pub use serde_json;
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
    /// Parse raw LLM output using rustia's lenient JSON parser and then
    /// validate the shape through serde deserialization.
    fn parse(input: &str) -> LlmJsonParseResult<Self> {
        match parse_lenient_json_value(input) {
            LlmJsonParseResult::Success { data } => {
                match validate_with_parse_coercion::<Self>(data) {
                    CoercionValidation::Success { data, .. } => {
                        LlmJsonParseResult::Success { data }
                    }
                    CoercionValidation::Failure { value, errors } => LlmJsonParseResult::Failure {
                        data: Some(value),
                        input: input.to_owned(),
                        errors: map_validation_errors(errors),
                    },
                }
            }
            LlmJsonParseResult::Failure {
                data,
                input,
                mut errors,
            } => {
                let data = data.map(|value| match validate_with_parse_coercion::<Self>(value) {
                    CoercionValidation::Success { value, .. } => value,
                    CoercionValidation::Failure {
                        value: coerced,
                        errors: validation_errors,
                    } => {
                        errors.extend(map_validation_errors(validation_errors));
                        coerced
                    }
                });

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

const MAX_PARSE_COERCION_ROUNDS: usize = 16;

enum CoercionValidation<T> {
    Success {
        data: T,
        value: Value,
    },
    Failure {
        value: Value,
        errors: Vec<IValidationError>,
    },
}

enum JsonPathSegment {
    Key(String),
    Index(usize),
}

fn validate_with_parse_coercion<T>(mut value: Value) -> CoercionValidation<T>
where
    T: Validate,
{
    for _ in 0..MAX_PARSE_COERCION_ROUNDS {
        match T::validate(value.clone()) {
            IValidation::Success { data } => return CoercionValidation::Success { data, value },
            IValidation::Failure { errors, .. } => {
                if !coerce_value_from_errors(&mut value, &errors) {
                    return CoercionValidation::Failure { value, errors };
                }
            }
        }
    }

    match T::validate(value.clone()) {
        IValidation::Success { data } => CoercionValidation::Success { data, value },
        IValidation::Failure { errors, .. } => CoercionValidation::Failure { value, errors },
    }
}

fn coerce_value_from_errors(value: &mut Value, errors: &[IValidationError]) -> bool {
    let mut changed = false;
    let mut seen = HashSet::new();

    for error in errors {
        if !seen.insert(error.path.clone()) {
            continue;
        }
        let Some(path) = parse_validation_path(&error.path) else {
            continue;
        };
        if coerce_stringified_path(value, &path) {
            changed = true;
        }
    }

    changed
}

fn coerce_stringified_path(root: &mut Value, path: &[JsonPathSegment]) -> bool {
    let Some(target) = value_mut_on_path(root, path) else {
        return false;
    };

    let raw = match target {
        Value::String(raw) => raw.clone(),
        _ => return false,
    };

    let Some(coerced) = parse_stringified_non_string(&raw) else {
        return false;
    };
    *target = coerced;
    true
}

fn parse_stringified_non_string(raw: &str) -> Option<Value> {
    let mut cursor = raw.to_owned();
    for _ in 0..MAX_PARSE_COERCION_ROUNDS {
        let LlmJsonParseResult::Success { data } = parse_lenient_json_value(&cursor) else {
            return None;
        };
        match data {
            Value::String(next) => {
                if next == cursor {
                    return None;
                }
                cursor = next;
            }
            other => return Some(other),
        }
    }
    None
}

fn value_mut_on_path<'a>(root: &'a mut Value, path: &[JsonPathSegment]) -> Option<&'a mut Value> {
    let mut cursor = root;

    for segment in path {
        match segment {
            JsonPathSegment::Key(key) => {
                cursor = cursor.as_object_mut()?.get_mut(key)?;
            }
            JsonPathSegment::Index(index) => {
                cursor = cursor.as_array_mut()?.get_mut(*index)?;
            }
        }
    }

    Some(cursor)
}

fn parse_validation_path(path: &str) -> Option<Vec<JsonPathSegment>> {
    let mut chars = path.chars().peekable();

    for expected in "$input".chars() {
        if chars.next()? != expected {
            return None;
        }
    }

    let mut output = Vec::new();

    while let Some(ch) = chars.peek().copied() {
        match ch {
            '.' => {
                chars.next();
                let mut key = String::new();
                while let Some(next) = chars.peek().copied() {
                    if matches!(next, '.' | '[') {
                        break;
                    }
                    key.push(next);
                    chars.next();
                }
                if key.is_empty() {
                    continue;
                }
                output.push(JsonPathSegment::Key(key));
            }
            '[' => {
                chars.next();
                match chars.peek().copied() {
                    Some('"') => {
                        chars.next();
                        let mut key = String::new();
                        let mut escaped = false;

                        for next in chars.by_ref() {
                            if escaped {
                                key.push(next);
                                escaped = false;
                                continue;
                            }
                            if next == '\\' {
                                escaped = true;
                                continue;
                            }
                            if next == '"' {
                                break;
                            }
                            key.push(next);
                        }

                        if escaped || chars.next() != Some(']') {
                            return None;
                        }
                        output.push(JsonPathSegment::Key(key));
                    }
                    Some(next) if next.is_ascii_digit() => {
                        let mut digits = String::new();
                        while let Some(digit) = chars.peek().copied() {
                            if !digit.is_ascii_digit() {
                                break;
                            }
                            digits.push(digit);
                            chars.next();
                        }
                        if chars.next() != Some(']') {
                            return None;
                        }
                        let index = digits.parse::<usize>().ok()?;
                        output.push(JsonPathSegment::Index(index));
                    }
                    _ => return None,
                }
            }
            _ => return None,
        }
    }

    Some(output)
}

fn map_validation_errors(errors: Vec<IValidationError>) -> Vec<LlmJsonParseError> {
    errors
        .into_iter()
        .map(|error| LlmJsonParseError {
            path: error.path,
            expected: error.expected,
            description: error
                .description
                .unwrap_or_else(|| "validation failed".to_owned()),
        })
        .collect()
}
