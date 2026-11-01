// SPDX-License-Identifier: Apache-2.0
//! Strict JSON helpers. Persisted records reject unknown fields at the type
//! level (`#[serde(deny_unknown_fields)]`) and trailing values here.

use anyhow::{Result, anyhow};
use serde::{Serialize, de::DeserializeOwned};

/// Decodes exactly one JSON value. Trailing non-whitespace data is an error.
pub fn from_slice_strict<T: DeserializeOwned>(bytes: &[u8]) -> Result<T> {
    let mut de = serde_json::Deserializer::from_slice(bytes);
    let value = T::deserialize(&mut de)?;
    de.end()
        .map_err(|_| anyhow!("trailing data in JSON record"))?;
    Ok(value)
}

/// Compact JSON encoding, like Go's `json.Marshal`.
pub fn to_vec<T: Serialize + ?Sized>(value: &T) -> Result<Vec<u8>> {
    Ok(serde_json::to_vec(value)?)
}

/// SHA-256 hex digest of the compact JSON encoding of `value`.
pub fn digest<T: Serialize + ?Sized>(value: &T) -> String {
    use sha2::Digest;
    let bytes = serde_json::to_vec(value).unwrap_or_default();
    hex::encode(sha2::Sha256::digest(&bytes))
}

/// SHA-256 hex digest of raw bytes.
pub fn sha256_hex(bytes: &[u8]) -> String {
    use sha2::Digest;
    hex::encode(sha2::Sha256::digest(bytes))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(serde::Deserialize, Debug, PartialEq)]
    #[serde(deny_unknown_fields)]
    struct Record {
        a: i64,
    }

    #[test]
    fn rejects_trailing_and_unknown_fields() {
        assert_eq!(
            from_slice_strict::<Record>(b"{\"a\":1}\n").unwrap(),
            Record { a: 1 }
        );
        assert!(from_slice_strict::<Record>(b"{\"a\":1}{}").is_err());
        assert!(from_slice_strict::<Record>(b"{\"a\":1,\"b\":2}").is_err());
    }
}
