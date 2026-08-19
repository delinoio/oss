pub mod auth;
pub mod endpoint;
pub mod framing;
pub mod protocol;
pub mod registration;

use keyring::{Entry, Error as KeyringError};

pub const HOST_NAME: &str = "io.delino.devhud.native_messaging";
pub const PROTOCOL_VERSION: u16 = 1;
pub const SCHEMA_VERSION: u16 = 1;
pub const MAX_JSON_BYTES: usize = 256 * 1024;
pub const MAX_OUTER_HTML_BYTES: usize = 128 * 1024;
pub const REQUEST_DEADLINE_MILLIS: i64 = 5_000;
pub const TEST_EXTENSION_ID: &str = "lmillpebkoiadcjhfimemdbcdhpafhgg";

const PAIRING_SERVICE: &str = "io.delino.devhud.native-messaging.v1";
const PAIRING_ACCOUNT: &str = "pairing-secret";
const PAIRING_COMPLETE_ACCOUNT: &str = "pairing-complete";

pub fn configured_extension_id() -> &'static str {
    env!("DEVHUD_CHROME_EXTENSION_ID")
}

pub fn expected_extension_origin() -> String {
    format!("chrome-extension://{}/", configured_extension_id())
}

pub fn validate_extension_id(value: &str) -> bool {
    value.len() == 32 && value.bytes().all(|byte| (b'a'..=b'p').contains(&byte))
}

pub fn read_pairing_secret() -> Result<Option<Vec<u8>>, String> {
    let entry = Entry::new(PAIRING_SERVICE, PAIRING_ACCOUNT).map_err(|_| "storage-failure")?;
    match entry.get_secret() {
        Ok(secret) => Ok(Some(secret)),
        Err(KeyringError::NoEntry) => Ok(None),
        Err(_) => Err("storage-failure".to_string()),
    }
}

pub fn write_pairing_secret(secret: &[u8]) -> Result<(), String> {
    if secret.len() != 32 {
        return Err("invalid-secret".to_string());
    }
    Entry::new(PAIRING_SERVICE, PAIRING_ACCOUNT)
        .map_err(|_| "storage-failure".to_string())?
        .set_secret(secret)
        .map_err(|_| "storage-failure".to_string())
}

pub fn pairing_is_complete() -> Result<bool, String> {
    let entry = Entry::new(PAIRING_SERVICE, PAIRING_COMPLETE_ACCOUNT)
        .map_err(|_| "storage-failure".to_string())?;
    match entry.get_password() {
        Ok(value) => Ok(value == "v1"),
        Err(KeyringError::NoEntry) => Ok(false),
        Err(_) => Err("storage-failure".to_string()),
    }
}

pub fn mark_pairing_complete() -> Result<(), String> {
    Entry::new(PAIRING_SERVICE, PAIRING_COMPLETE_ACCOUNT)
        .map_err(|_| "storage-failure".to_string())?
        .set_password("v1")
        .map_err(|_| "storage-failure".to_string())
}

pub fn clear_pairing_complete() -> Result<(), String> {
    let entry = Entry::new(PAIRING_SERVICE, PAIRING_COMPLETE_ACCOUNT)
        .map_err(|_| "storage-failure".to_string())?;
    match entry.delete_credential() {
        Ok(()) | Err(KeyringError::NoEntry) => Ok(()),
        Err(_) => Err("storage-failure".to_string()),
    }
}

pub fn generate_pairing_secret() -> [u8; 32] {
    use rand::RngCore;
    let mut secret = [0_u8; 32];
    rand::rng().fill_bytes(&mut secret);
    secret
}

pub fn delete_pairing_secret() -> Result<(), String> {
    let entry =
        Entry::new(PAIRING_SERVICE, PAIRING_ACCOUNT).map_err(|_| "storage-failure".to_string())?;
    let secret_result = match entry.delete_credential() {
        Ok(()) | Err(KeyringError::NoEntry) => Ok(()),
        Err(_) => Err("storage-failure".to_string()),
    };
    let completion_result = clear_pairing_complete();
    secret_result.and(completion_result)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn release_identity_contract_is_exact() {
        assert_eq!(HOST_NAME, "io.delino.devhud.native_messaging");
        assert!(validate_extension_id(configured_extension_id()));
        assert_eq!(
            expected_extension_origin().len(),
            32 + "chrome-extension:///".len()
        );
    }
}
