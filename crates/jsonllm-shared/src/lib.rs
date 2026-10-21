#![forbid(unsafe_code)]
#![doc = include_str!("../README.md")]

pub use clairic_client;
use clairic_client::{Field, FieldType, ListItemType, Response};
use serde::de::DeserializeOwned;
pub use serde_json;
use serde_json::Value;

/// Generation or typed response decoding failure.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    /// Request validation, transport, or upstream API failure.
    ///
    /// The source can contain sensitive details. Filter it before logging.
    #[error("Clairic request failed")]
    Upstream(#[from] clairic_client::Error),
    /// The response was truncated and cannot be accepted as a complete value.
    #[error("Clairic returned a truncated response")]
    Truncated,
    /// An answer entry is absent, including for an optional field.
    #[error("Clairic response is missing field {field}")]
    MissingField {
        /// Wire name of the missing answer.
        field: String,
    },
    /// An answer value cannot be decoded as the requested Rust type.
    #[error("Clairic returned an invalid value for field {field}")]
    InvalidField {
        /// Wire name of the invalid answer.
        field: String,
        /// Decoder failure. Custom decoders can include sensitive details.
        #[source]
        source: serde_json::Error,
    },
}

/// Map a Rust value to a Clairic field and decode its answer.
///
/// Implement this trait for domain-specific scalar values. [`Self::field`]
/// must describe the value shape accepted by [`Self::decode`]. Implement
/// [`ListItem`] as well if the type can appear in a scalar list.
///
/// Built-in implementations cover `String`, integer and float primitives,
/// `Option<T>` where `T: JsonField`, and `Vec<T>` where `T: ListItem`.
/// Optional nulls become `None`; other values use the inner decoder.
/// Numbers must fit the target Rust type. Float decoding rejects nonfinite
/// results. Values are not coerced from strings.
///
/// ```
/// use jsonllm_shared::{JsonField, serde_json::json};
///
/// assert_eq!(u8::decode(&json!(42))?, 42);
/// assert!(u8::decode(&json!(256)).is_err());
/// assert!(u8::decode(&json!("42")).is_err());
/// assert_eq!(Option::<String>::decode(&json!(null))?, None);
/// assert_eq!(Vec::<i16>::decode(&json!([-2, 3]))?, vec![-2, 3]);
/// # Ok::<(), jsonllm_shared::serde_json::Error>(())
/// ```
pub trait JsonField: Sized {
    /// Return the field kind and options used to request this value.
    fn field() -> Field;

    /// Decode and validate one answer value.
    ///
    /// # Errors
    ///
    /// Return a serde error if the value has an incompatible shape or violates
    /// the type's constraints. Avoid including answer values in error messages.
    fn decode(value: &Value) -> Result<Self, serde_json::Error>;
}

/// Scalar types that Clairic can return inside a list.
///
/// Implementations must choose an item kind compatible with their [`JsonField`]
/// decoder. Built-in implementations cover strings and numeric primitives.
/// `Option<T>` and `Vec<T>` do not implement this trait, so nullable elements
/// and nested lists have no automatic mapping.
///
/// ```
/// use jsonllm_shared::{ListItem, clairic_client::ListItemType};
///
/// assert_eq!(String::item_type(), ListItemType::Text);
/// assert_eq!(u32::item_type(), ListItemType::Number);
/// ```
pub trait ListItem: JsonField {
    /// Return the scalar item kind for a list of this type.
    fn item_type() -> ListItemType;
}

fn decode<T: DeserializeOwned>(value: &Value) -> Result<T, serde_json::Error> {
    serde_json::from_value(value.clone())
}

impl JsonField for String {
    fn field() -> Field {
        Field::new(FieldType::Text)
    }

    fn decode(value: &Value) -> Result<Self, serde_json::Error> {
        decode(value)
    }
}
impl ListItem for String {
    fn item_type() -> ListItemType {
        ListItemType::Text
    }
}

macro_rules! numbers {
    ($integer:expr; $($ty:ty),+ $(,)?) => {$(
        impl JsonField for $ty {
            fn field() -> Field {
                let mut field = Field::new(FieldType::Number);
                field.integer = Some($integer);
                field
            }
            fn decode(value: &Value) -> Result<Self, serde_json::Error> {
                decode(value)
            }
        }
        impl ListItem for $ty {
            fn item_type() -> ListItemType { ListItemType::Number }
        }
    )+};
}
numbers!(true; i8, i16, i32, i64, i128, isize, u8, u16, u32, u64, u128, usize);
macro_rules! floats {
    ($($ty:ty),+) => {$(
        impl JsonField for $ty {
            fn field() -> Field {
                let mut field = Field::new(FieldType::Number);
                field.integer = Some(false);
                field
            }
            fn decode(value: &Value) -> Result<Self, serde_json::Error> {
                let number: Self = decode(value)?;
                if !number.is_finite() {
                    return Err(<serde_json::Error as serde::de::Error>::custom("number exceeds the finite target range"));
                }
                Ok(number)
            }
        }
        impl ListItem for $ty {
            fn item_type() -> ListItemType { ListItemType::Number }
        }
    )+};
}
floats!(f32, f64);

impl<T: JsonField> JsonField for Option<T> {
    fn field() -> Field {
        T::field()
    }

    fn decode(value: &Value) -> Result<Self, serde_json::Error> {
        if value.is_null() {
            Ok(None)
        } else {
            T::decode(value).map(Some)
        }
    }
}

impl<T: ListItem> JsonField for Vec<T> {
    fn field() -> Field {
        let mut field = Field::new(FieldType::List);
        field.items = Some(T::item_type());
        field
    }

    fn decode(value: &Value) -> Result<Self, serde_json::Error> {
        let values = value
            .as_array()
            .ok_or_else(|| <serde_json::Error as serde::de::Error>::custom("expected a list"))?;
        values.iter().map(T::decode).collect()
    }
}

/// Decode one answer entry by its wire name.
///
/// Optional fields still require an entry: a present null decodes to `None`,
/// while an absent answer returns [`Error::MissingField`]. Only the answer's
/// value is passed to [`JsonField::decode`]; metadata and other answers are
/// ignored. This helper does not check truncation. Use the facade's derived
/// `JsonGen::from_response` to reject a truncated response before decoding.
///
/// # Errors
///
/// Returns [`Error::MissingField`] if the entry is absent, or
/// [`Error::InvalidField`] if the decoder rejects its value.
///
/// ```
/// use jsonllm_shared::{Error, decode_field, clairic_client::Response, serde_json};
/// use serde_json::json;
///
/// let response: Response = serde_json::from_value(json!({
///     "model": "clairic", "truncated": false,
///     "usage": {"input_tokens": 1, "output_tokens": 1},
///     "answers": {"count": {"type": "number", "value": 3}}
/// }))?;
/// assert_eq!(decode_field::<u8>(&response, "count")?, 3);
/// assert!(matches!(
///     decode_field::<Option<String>>(&response, "absent"),
///     Err(Error::MissingField { .. })
/// ));
/// # Ok::<(), Box<dyn std::error::Error>>(())
/// ```
pub fn decode_field<T: JsonField>(response: &Response, field: &str) -> Result<T, Error> {
    let answer = response
        .answers
        .get(field)
        .ok_or_else(|| Error::MissingField {
            field: field.to_owned(),
        })?;
    T::decode(&answer.value).map_err(|source| Error::InvalidField {
        field: field.to_owned(),
        source,
    })
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;

    #[test]
    fn preserves_optional_and_list_values() {
        assert_eq!(Option::<u16>::decode(&json!(null)).unwrap(), None);
        assert_eq!(Option::<u16>::decode(&json!(42)).unwrap(), Some(42));
        assert_eq!(Vec::<i16>::decode(&json!([-2, 3])).unwrap(), vec![-2, 3]);
        assert!(Vec::<i16>::decode(&json!([32768])).is_err());
        assert!(Vec::<String>::decode(&json!(null)).is_err());
    }

    #[test]
    fn rejects_float_overflow() {
        assert!(f32::decode(&json!(1e100)).is_err());
        assert!(f32::decode(&json!(-1e100)).is_err());
        assert_eq!(f64::decode(&json!(1e100)).unwrap(), 1e100);
    }
}
