use std::collections::{BTreeMap, HashMap};

use base64::Engine as _;
use regex::Regex;
use serde::de::DeserializeOwned;
use serde_json::Value;
use url::Url;
use uuid::Uuid;

/// Validation result with rustia-compatible success/failure discriminator.
#[derive(Debug, Clone, PartialEq)]
pub enum IValidation<T> {
    Success {
        data: T,
    },
    Failure {
        data: Value,
        errors: Vec<IValidationError>,
    },
}

/// Validation error detail compatible with rustia's validate() payload shape.
#[derive(Debug, Clone, PartialEq)]
pub struct IValidationError {
    pub path: String,
    pub expected: String,
    pub value: Value,
    pub description: Option<String>,
}

/// Runtime validator trait.
pub trait Validate: DeserializeOwned + Sized {
    fn validate(value: Value) -> IValidation<Self>;
    fn validate_equals(value: Value) -> IValidation<Self>;
}

/// Runtime representation for derive-time rustia tags.
#[derive(Debug, Clone, PartialEq)]
pub enum TagRuntime {
    MinLength(usize),
    MaxLength(usize),
    MinItems(usize),
    MaxItems(usize),
    UniqueItems(bool),
    Minimum(f64),
    Maximum(f64),
    ExclusiveMinimum(f64),
    ExclusiveMaximum(f64),
    MultipleOf(f64),
    Pattern(String),
    Format(String),
    Type(String),
    Items(Vec<TagRuntime>),
    Keys(Vec<TagRuntime>),
    Values(Vec<TagRuntime>),
    Metadata { kind: String, args: Vec<String> },
}

pub fn validate_with_serde<T>(value: Value) -> IValidation<T>
where
    T: DeserializeOwned,
{
    let encoded = match serde_json::to_vec(&value) {
        Ok(encoded) => encoded,
        Err(error) => {
            return IValidation::Failure {
                data: value.clone(),
                errors: vec![IValidationError {
                    path: "$input".to_owned(),
                    expected: "JSON value".to_owned(),
                    value,
                    description: Some(error.to_string()),
                }],
            };
        }
    };

    let mut deserializer = serde_json::Deserializer::from_slice(&encoded);
    match serde_path_to_error::deserialize::<_, T>(&mut deserializer) {
        Ok(data) => IValidation::Success { data },
        Err(error) => {
            let raw_path = error.path().to_string();
            let path = normalize_path(&raw_path);
            let description = Some(error.into_inner().to_string());
            let error_value = read_value_on_path(&value, &path).unwrap_or(Value::Null);

            IValidation::Failure {
                data: value,
                errors: vec![IValidationError {
                    path,
                    expected: "serde-compatible schema".to_owned(),
                    value: error_value,
                    description,
                }],
            }
        }
    }
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

fn read_value_on_path(root: &Value, path: &str) -> Option<Value> {
    if path == "$input" {
        return Some(root.clone());
    }
    let mut cursor = root;
    let mut chars = path.strip_prefix("$input")?.chars().peekable();

    while let Some(ch) = chars.peek().copied() {
        match ch {
            '.' => {
                chars.next();
                let mut key = String::new();
                while let Some(next) = chars.peek().copied() {
                    if next == '.' || next == '[' {
                        break;
                    }
                    key.push(next);
                    chars.next();
                }
                cursor = cursor.get(&key)?;
            }
            '[' => {
                chars.next();
                if chars.peek().copied() == Some('"') {
                    chars.next();
                    let mut key = String::new();
                    while let Some(next) = chars.next() {
                        if next == '"' {
                            break;
                        }
                        if next == '\\' {
                            if let Some(escaped) = chars.next() {
                                key.push(escaped);
                            } else {
                                return None;
                            }
                        } else {
                            key.push(next);
                        }
                    }
                    if chars.next() != Some(']') {
                        return None;
                    }
                    cursor = cursor.get(&key)?;
                } else {
                    let mut index = String::new();
                    while let Some(next) = chars.peek().copied() {
                        if next == ']' {
                            break;
                        }
                        index.push(next);
                        chars.next();
                    }
                    if chars.next() != Some(']') {
                        return None;
                    }
                    let parsed = index.parse::<usize>().ok()?;
                    cursor = cursor.get(parsed)?;
                }
            }
            _ => return None,
        }
    }

    Some(cursor.clone())
}

macro_rules! impl_validate_with_serde {
    ($($ty:ty),* $(,)?) => {
        $(
            impl Validate for $ty {
                fn validate(value: Value) -> IValidation<Self> {
                    validate_with_serde(value)
                }

                fn validate_equals(value: Value) -> IValidation<Self> {
                    validate_with_serde(value)
                }
            }
        )*
    };
}

impl_validate_with_serde!(
    bool, String, char, i8, i16, i32, i64, i128, isize, u8, u16, u32, u64, u128, usize, f32, f64,
);

impl Validate for Value {
    fn validate(value: Value) -> IValidation<Self> {
        IValidation::Success { data: value }
    }

    fn validate_equals(value: Value) -> IValidation<Self> {
        IValidation::Success { data: value }
    }
}

impl<T> Validate for Option<T>
where
    T: Validate,
{
    fn validate(value: Value) -> IValidation<Self> {
        if value.is_null() {
            return IValidation::Success { data: None };
        }

        let original = value.clone();
        match T::validate(value) {
            IValidation::Success { data } => IValidation::Success { data: Some(data) },
            IValidation::Failure { errors, .. } => IValidation::Failure {
                data: original,
                errors,
            },
        }
    }

    fn validate_equals(value: Value) -> IValidation<Self> {
        if value.is_null() {
            return IValidation::Success { data: None };
        }

        let original = value.clone();
        match T::validate_equals(value) {
            IValidation::Success { data } => IValidation::Success { data: Some(data) },
            IValidation::Failure { errors, .. } => IValidation::Failure {
                data: original,
                errors,
            },
        }
    }
}

impl<T> Validate for Vec<T>
where
    T: Validate,
{
    fn validate(value: Value) -> IValidation<Self> {
        validate_vec(value, false)
    }

    fn validate_equals(value: Value) -> IValidation<Self> {
        validate_vec(value, true)
    }
}

fn validate_vec<T>(value: Value, strict: bool) -> IValidation<Vec<T>>
where
    T: Validate,
{
    let original = value.clone();
    let array = match value {
        Value::Array(array) => array,
        other => {
            return IValidation::Failure {
                data: other.clone(),
                errors: vec![IValidationError {
                    path: "$input".to_owned(),
                    expected: "array".to_owned(),
                    value: other,
                    description: Some("expected an array value".to_owned()),
                }],
            };
        }
    };

    let mut data = Vec::with_capacity(array.len());
    let mut errors = Vec::new();
    for (index, item) in array.iter().cloned().enumerate() {
        let validated = if strict {
            T::validate_equals(item)
        } else {
            T::validate(item)
        };
        match validated {
            IValidation::Success { data: item } => data.push(item),
            IValidation::Failure { errors: nested, .. } => {
                merge_prefixed_errors(&mut errors, &join_index_path("$input", index), nested);
            }
        }
    }

    if errors.is_empty() {
        IValidation::Success { data }
    } else {
        IValidation::Failure {
            data: original,
            errors,
        }
    }
}

impl<T> Validate for HashMap<String, T>
where
    T: Validate,
{
    fn validate(value: Value) -> IValidation<Self> {
        validate_hash_map(value, false)
    }

    fn validate_equals(value: Value) -> IValidation<Self> {
        validate_hash_map(value, true)
    }
}

fn validate_hash_map<T>(value: Value, strict: bool) -> IValidation<HashMap<String, T>>
where
    T: Validate,
{
    let original = value.clone();
    let object = match value {
        Value::Object(object) => object,
        other => {
            return IValidation::Failure {
                data: other.clone(),
                errors: vec![IValidationError {
                    path: "$input".to_owned(),
                    expected: "object".to_owned(),
                    value: other,
                    description: Some("expected an object value".to_owned()),
                }],
            };
        }
    };

    let mut data = HashMap::with_capacity(object.len());
    let mut errors = Vec::new();
    for (key, item) in object.iter() {
        let validated = if strict {
            T::validate_equals(item.clone())
        } else {
            T::validate(item.clone())
        };
        match validated {
            IValidation::Success { data: item } => {
                data.insert(key.clone(), item);
            }
            IValidation::Failure { errors: nested, .. } => {
                merge_prefixed_errors(&mut errors, &join_object_path("$input", key), nested);
            }
        }
    }

    if errors.is_empty() {
        IValidation::Success { data }
    } else {
        IValidation::Failure {
            data: original,
            errors,
        }
    }
}

impl<T> Validate for BTreeMap<String, T>
where
    T: Validate,
{
    fn validate(value: Value) -> IValidation<Self> {
        validate_btree_map(value, false)
    }

    fn validate_equals(value: Value) -> IValidation<Self> {
        validate_btree_map(value, true)
    }
}

fn validate_btree_map<T>(value: Value, strict: bool) -> IValidation<BTreeMap<String, T>>
where
    T: Validate,
{
    let original = value.clone();
    let object = match value {
        Value::Object(object) => object,
        other => {
            return IValidation::Failure {
                data: other.clone(),
                errors: vec![IValidationError {
                    path: "$input".to_owned(),
                    expected: "object".to_owned(),
                    value: other,
                    description: Some("expected an object value".to_owned()),
                }],
            };
        }
    };

    let mut data = BTreeMap::new();
    let mut errors = Vec::new();
    for (key, item) in object.iter() {
        let validated = if strict {
            T::validate_equals(item.clone())
        } else {
            T::validate(item.clone())
        };
        match validated {
            IValidation::Success { data: item } => {
                data.insert(key.clone(), item);
            }
            IValidation::Failure { errors: nested, .. } => {
                merge_prefixed_errors(&mut errors, &join_object_path("$input", key), nested);
            }
        }
    }

    if errors.is_empty() {
        IValidation::Success { data }
    } else {
        IValidation::Failure {
            data: original,
            errors,
        }
    }
}

pub fn merge_prefixed_errors(
    target: &mut Vec<IValidationError>,
    prefix: &str,
    mut nested: Vec<IValidationError>,
) {
    for error in &mut nested {
        error.path = prepend_path(prefix, &error.path);
    }
    target.extend(nested);
}

pub fn prepend_path(prefix: &str, path: &str) -> String {
    if path == "$input" {
        prefix.to_owned()
    } else if let Some(suffix) = path.strip_prefix("$input") {
        format!("{prefix}{suffix}")
    } else if path.starts_with('[') {
        format!("{prefix}{path}")
    } else {
        format!("{prefix}.{path}")
    }
}

pub fn join_object_path(base: &str, key: &str) -> String {
    if key
        .chars()
        .next()
        .is_some_and(|ch| ch == '_' || ch.is_ascii_alphabetic())
        && key
            .chars()
            .all(|ch| ch == '_' || ch.is_ascii_alphanumeric())
    {
        format!("{base}.{key}")
    } else {
        let escaped = key.replace('\\', "\\\\").replace('"', "\\\"");
        format!("{base}[\"{escaped}\"]")
    }
}

pub fn join_index_path(base: &str, index: usize) -> String {
    format!("{base}[{index}]")
}

pub fn apply_tags(
    value: &Value,
    path: &str,
    tags: &[TagRuntime],
    errors: &mut Vec<IValidationError>,
) {
    for tag in tags {
        match tag {
            TagRuntime::MinLength(min) => {
                if let Some(text) = value.as_str()
                    && text.chars().count() < *min
                {
                    errors.push(tag_error(
                        path,
                        &format!("string & MinLength<{min}>"),
                        value,
                        Some(format!("string length must be >= {min}")),
                    ));
                }
            }
            TagRuntime::MaxLength(max) => {
                if let Some(text) = value.as_str()
                    && text.chars().count() > *max
                {
                    errors.push(tag_error(
                        path,
                        &format!("string & MaxLength<{max}>"),
                        value,
                        Some(format!("string length must be <= {max}")),
                    ));
                }
            }
            TagRuntime::MinItems(min) => {
                if let Some(items) = value.as_array()
                    && items.len() < *min
                {
                    errors.push(tag_error(
                        path,
                        &format!("array & MinItems<{min}>"),
                        value,
                        Some(format!("array length must be >= {min}")),
                    ));
                }
            }
            TagRuntime::MaxItems(max) => {
                if let Some(items) = value.as_array()
                    && items.len() > *max
                {
                    errors.push(tag_error(
                        path,
                        &format!("array & MaxItems<{max}>"),
                        value,
                        Some(format!("array length must be <= {max}")),
                    ));
                }
            }
            TagRuntime::UniqueItems(enabled) => {
                if *enabled
                    && let Some(items) = value.as_array()
                    && !is_unique_items(items)
                {
                    errors.push(tag_error(
                        path,
                        "array & UniqueItems<true>",
                        value,
                        Some("array items must be unique".to_owned()),
                    ));
                }
            }
            TagRuntime::Minimum(minimum) => {
                if let Some(number) = json_number_to_f64(value)
                    && number < *minimum
                {
                    errors.push(tag_error(
                        path,
                        &format!("number & Minimum<{minimum}>"),
                        value,
                        Some(format!("number must be >= {minimum}")),
                    ));
                }
            }
            TagRuntime::Maximum(maximum) => {
                if let Some(number) = json_number_to_f64(value)
                    && number > *maximum
                {
                    errors.push(tag_error(
                        path,
                        &format!("number & Maximum<{maximum}>"),
                        value,
                        Some(format!("number must be <= {maximum}")),
                    ));
                }
            }
            TagRuntime::ExclusiveMinimum(minimum) => {
                if let Some(number) = json_number_to_f64(value)
                    && number <= *minimum
                {
                    errors.push(tag_error(
                        path,
                        &format!("number & ExclusiveMinimum<{minimum}>"),
                        value,
                        Some(format!("number must be > {minimum}")),
                    ));
                }
            }
            TagRuntime::ExclusiveMaximum(maximum) => {
                if let Some(number) = json_number_to_f64(value)
                    && number >= *maximum
                {
                    errors.push(tag_error(
                        path,
                        &format!("number & ExclusiveMaximum<{maximum}>"),
                        value,
                        Some(format!("number must be < {maximum}")),
                    ));
                }
            }
            TagRuntime::MultipleOf(divisor) => {
                if let Some(number) = json_number_to_f64(value)
                    && !is_multiple_of(number, *divisor)
                {
                    errors.push(tag_error(
                        path,
                        &format!("number & MultipleOf<{divisor}>"),
                        value,
                        Some(format!("number must be a multiple of {divisor}")),
                    ));
                }
            }
            TagRuntime::Pattern(pattern) => {
                if let Some(text) = value.as_str() {
                    match Regex::new(pattern) {
                        Ok(regex) => {
                            if !regex.is_match(text) {
                                errors.push(tag_error(
                                    path,
                                    &format!("string & Pattern<{pattern}>"),
                                    value,
                                    Some("string does not match the required pattern".to_owned()),
                                ));
                            }
                        }
                        Err(error) => {
                            errors.push(tag_error(
                                path,
                                &format!("string & Pattern<{pattern}>"),
                                value,
                                Some(format!("invalid pattern: {error}")),
                            ));
                        }
                    }
                }
            }
            TagRuntime::Format(format_name) => {
                if let Some(text) = value.as_str()
                    && !is_valid_format(format_name, text)
                {
                    errors.push(tag_error(
                        path,
                        &format!("string & Format<{format_name}>"),
                        value,
                        Some(format!("string does not satisfy format `{format_name}`")),
                    ));
                }
            }
            TagRuntime::Type(type_name) => {
                if !matches_numeric_type(type_name, value) {
                    errors.push(tag_error(
                        path,
                        &format!("number & Type<{type_name}>"),
                        value,
                        Some(format!("value does not satisfy numeric type `{type_name}`")),
                    ));
                }
            }
            TagRuntime::Items(nested) => {
                if let Some(items) = value.as_array() {
                    for (index, item) in items.iter().enumerate() {
                        apply_tags(item, &join_index_path(path, index), nested, errors);
                    }
                }
            }
            TagRuntime::Keys(nested) => {
                if let Some(object) = value.as_object() {
                    for key in object.keys() {
                        let key_value = Value::String(key.clone());
                        apply_tags(&key_value, &join_object_path(path, key), nested, errors);
                    }
                }
            }
            TagRuntime::Values(nested) => {
                if let Some(object) = value.as_object() {
                    for (key, item) in object {
                        apply_tags(item, &join_object_path(path, key), nested, errors);
                    }
                }
            }
            TagRuntime::Metadata { .. } => {}
        }
    }
}

fn tag_error(
    path: &str,
    expected: &str,
    value: &Value,
    description: Option<String>,
) -> IValidationError {
    IValidationError {
        path: path.to_owned(),
        expected: expected.to_owned(),
        value: value.clone(),
        description,
    }
}

fn is_unique_items(items: &[Value]) -> bool {
    for i in 0..items.len() {
        for j in (i + 1)..items.len() {
            if items[i] == items[j] {
                return false;
            }
        }
    }
    true
}

fn json_number_to_f64(value: &Value) -> Option<f64> {
    match value {
        Value::Number(number) => number
            .as_f64()
            .or_else(|| number.as_i64().map(|number| number as f64))
            .or_else(|| number.as_u64().map(|number| number as f64)),
        _ => None,
    }
}

fn is_multiple_of(value: f64, divisor: f64) -> bool {
    if divisor == 0.0 {
        return false;
    }
    let quotient = value / divisor;
    (quotient - quotient.round()).abs() <= 1e-12
}

fn is_valid_format(format_name: &str, input: &str) -> bool {
    match format_name {
        "byte" => base64::engine::general_purpose::STANDARD
            .decode(input)
            .is_ok(),
        "password" => true,
        "regex" => Regex::new(input).is_ok(),
        "uuid" => Uuid::parse_str(input).is_ok(),
        "email" => Regex::new(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")
            .map(|regex| regex.is_match(input))
            .unwrap_or(false),
        "hostname" => is_valid_hostname(input),
        "idn-email" => is_valid_idn_email(input),
        "idn-hostname" => !input.is_empty(),
        "iri" | "iri-reference" | "uri-reference" | "uri-template" => {
            !input.trim().is_empty() && !input.contains(char::is_whitespace)
        }
        "ipv4" => input.parse::<std::net::Ipv4Addr>().is_ok(),
        "ipv6" => input.parse::<std::net::Ipv6Addr>().is_ok(),
        "uri" | "url" => Url::parse(input).is_ok(),
        "date-time" => chrono::DateTime::parse_from_rfc3339(input).is_ok(),
        "date" => chrono::NaiveDate::parse_from_str(input, "%Y-%m-%d").is_ok(),
        "time" => is_valid_time(input),
        "duration" => is_valid_duration(input),
        "json-pointer" => is_valid_json_pointer(input),
        "relative-json-pointer" => is_valid_relative_json_pointer(input),
        _ => false,
    }
}

fn is_valid_hostname(input: &str) -> bool {
    if input.is_empty() || input.len() > 253 {
        return false;
    }
    for label in input.split('.') {
        if label.is_empty() || label.len() > 63 {
            return false;
        }
        let bytes = label.as_bytes();
        if bytes.first() == Some(&b'-') || bytes.last() == Some(&b'-') {
            return false;
        }
        if !label
            .chars()
            .all(|character| character.is_ascii_alphanumeric() || character == '-')
        {
            return false;
        }
    }
    true
}

fn is_valid_idn_email(input: &str) -> bool {
    let mut parts = input.split('@');
    let local = match parts.next() {
        Some(local) if !local.is_empty() => local,
        _ => return false,
    };
    let domain = match parts.next() {
        Some(domain) if !domain.is_empty() => domain,
        _ => return false,
    };
    if parts.next().is_some() {
        return false;
    }
    !local.contains(char::is_whitespace) && !domain.contains(char::is_whitespace)
}

fn is_valid_time(input: &str) -> bool {
    let formats = ["%H:%M:%S", "%H:%M:%S%.f", "%H:%M:%S%:z", "%H:%M:%S%.f%:z"];
    formats
        .iter()
        .any(|format| chrono::NaiveTime::parse_from_str(input, format).is_ok())
}

fn is_valid_json_pointer(input: &str) -> bool {
    if input.is_empty() {
        return true;
    }
    if !input.starts_with('/') {
        return false;
    }
    let mut chars = input.chars().peekable();
    while let Some(character) = chars.next() {
        if character == '~' {
            match chars.next() {
                Some('0') | Some('1') => {}
                _ => return false,
            }
        }
    }
    true
}

fn is_valid_relative_json_pointer(input: &str) -> bool {
    let Some(first_non_digit) = input.find(|character: char| !character.is_ascii_digit()) else {
        return !input.is_empty() && input != "00";
    };
    let (digits, suffix) = input.split_at(first_non_digit);
    if digits.is_empty() || (digits.starts_with('0') && digits.len() > 1) {
        return false;
    }
    suffix == "#" || is_valid_json_pointer(suffix)
}

fn is_valid_duration(input: &str) -> bool {
    let Ok(regex) = Regex::new(r"^P(\d+Y)?(\d+M)?(\d+W)?(\d+D)?(T(\d+H)?(\d+M)?(\d+(\.\d+)?S)?)?$")
    else {
        return false;
    };

    if !regex.is_match(input) {
        return false;
    }

    input != "P" && input != "PT"
}

fn matches_numeric_type(type_name: &str, value: &Value) -> bool {
    match type_name {
        "int32" => value
            .as_i64()
            .is_some_and(|number| i32::MIN as i64 <= number && number <= i32::MAX as i64),
        "uint32" => value
            .as_u64()
            .is_some_and(|number| number <= u32::MAX as u64),
        "int64" => value.as_i64().is_some(),
        "uint64" => value.as_u64().is_some(),
        "float" => value
            .as_f64()
            .is_some_and(|number| number.is_finite() && (number as f32).is_finite()),
        "double" => value.as_f64().is_some_and(f64::is_finite),
        _ => false,
    }
}

#[doc(hidden)]
pub mod __private {
    pub use super::{
        TagRuntime, apply_tags, join_index_path, join_object_path, merge_prefixed_errors,
        prepend_path, validate_with_serde,
    };
}
