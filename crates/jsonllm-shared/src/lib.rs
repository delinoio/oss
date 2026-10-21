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
    #[error("Clairic request failed")]
    Upstream(#[from] clairic_client::Error),
    #[error("Clairic returned a truncated response")]
    Truncated,
    #[error("Clairic response is missing field {field}")]
    MissingField { field: String },
    #[error("Clairic returned an invalid value for field {field}")]
    InvalidField {
        field: String,
        #[source]
        source: serde_json::Error,
    },
}

/// Map a Rust value to a Clairic field and decode its answer.
/// Implement this trait for domain-specific scalar values.
pub trait JsonField: Sized {
    fn field() -> Field;
    fn decode(value: &Value) -> Result<Self, serde_json::Error>;
}

/// Scalar types that Clairic can return inside a list.
pub trait ListItem: JsonField {
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

/// Decode a required answer entry. Optional fields still require an entry.
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
