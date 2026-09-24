//! Typed document trees, layout and atomic edit plans for Delino Forge.
#![forbid(unsafe_code)]
pub mod cancellation;
mod error;
mod layout;
mod model;
mod patch;
mod validate;
pub use error::*;
pub use layout::*;
pub use model::*;
pub use patch::*;
pub use validate::*;

pub const MAX_JSON_BYTES: usize = 16 * 1024 * 1024;
pub fn parse<T: serde::de::DeserializeOwned>(bytes: &[u8]) -> Result<T> {
    cancellation::checkpoint()?;
    if bytes.len() > MAX_JSON_BYTES {
        return error(ErrorCode::ResourceLimit, "", "JSON input exceeds 16 MiB");
    }
    let result = serde_json::from_slice(bytes).map_err(|_| {
        Diagnostic::new(
            ErrorCode::InvalidJson,
            "",
            "Invalid JSON, unknown field, or incompatible type",
        )
    });
    cancellation::checkpoint()?;
    result
}
pub fn schema() -> serde_json::Value {
    serde_json::json!({ "presentation": schemars::schema_for!(Presentation), "patch": schemars::schema_for!(Patch) })
}
