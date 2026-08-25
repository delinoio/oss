pub mod auth;
pub mod endpoint;
pub mod framing;
pub mod protocol;
pub mod registration;

#[cfg(target_os = "linux")]
use std::{
    fs::{self, OpenOptions},
    io::{self, Write},
    os::unix::fs::{DirBuilderExt, OpenOptionsExt},
    path::{Path, PathBuf},
};

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
#[cfg(target_os = "linux")]
const PAIRING_MARKER_RELATIVE_PATH: &str =
    ".local/share/io.delino.devhud/native-messaging-pairing-v1";

#[cfg(target_os = "linux")]
fn pairing_marker_path() -> Result<PathBuf, String> {
    dirs::home_dir()
        .filter(|home| home.is_absolute())
        .map(|home| home.join(PAIRING_MARKER_RELATIVE_PATH))
        .ok_or_else(|| "storage-failure".to_string())
}

#[cfg(target_os = "linux")]
fn ensure_pairing_marker_at(path: &Path) -> io::Result<bool> {
    let parent = path
        .parent()
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "marker has no parent"))?;
    fs::DirBuilder::new()
        .recursive(true)
        .mode(0o700)
        .create(parent)?;
    let mut marker = match OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(path)
    {
        Ok(marker) => marker,
        Err(error) if error.kind() == io::ErrorKind::AlreadyExists => return Ok(false),
        Err(error) => return Err(error),
    };
    if let Err(error) = marker.write_all(b"v1\n").and_then(|()| marker.sync_all()) {
        let _ = fs::remove_file(path);
        return Err(error);
    }
    Ok(true)
}

#[cfg(target_os = "linux")]
fn remove_pairing_marker_at(path: &Path) -> io::Result<()> {
    match fs::remove_file(path) {
        Ok(()) => Ok(()),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(error),
    }
}

#[cfg(target_os = "linux")]
fn ensure_pairing_marker() -> Result<bool, String> {
    ensure_pairing_marker_at(&pairing_marker_path()?).map_err(|_| "storage-failure".to_string())
}

#[cfg(target_os = "linux")]
fn remove_pairing_marker() -> Result<(), String> {
    remove_pairing_marker_at(&pairing_marker_path()?).map_err(|_| "storage-failure".to_string())
}

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

#[cfg(target_os = "linux")]
fn write_pairing_secret_after_marker(
    marker_result: Result<bool, String>,
    write_secret: impl FnOnce() -> Result<(), String>,
) -> Result<(), String> {
    marker_result?;
    write_secret()
}

pub fn write_pairing_secret(secret: &[u8]) -> Result<(), String> {
    if secret.len() != 32 {
        return Err("invalid-secret".to_string());
    }
    let write_secret = || {
        Entry::new(PAIRING_SERVICE, PAIRING_ACCOUNT)
            .map_err(|_| "storage-failure".to_string())
            .and_then(|entry| {
                entry
                    .set_secret(secret)
                    .map_err(|_| "storage-failure".to_string())
            })
    };
    #[cfg(target_os = "linux")]
    {
        return write_pairing_secret_after_marker(ensure_pairing_marker(), write_secret);
    }
    #[cfg(not(target_os = "linux"))]
    write_secret()
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

fn complete_pairing_secret_deletion(
    secret_result: Result<(), String>,
    completion_result: Result<(), String>,
    remove_marker: impl FnOnce() -> Result<(), String>,
) -> Result<(), String> {
    secret_result.and(completion_result)?;
    remove_marker()
}

pub fn delete_pairing_secret() -> Result<(), String> {
    let entry =
        Entry::new(PAIRING_SERVICE, PAIRING_ACCOUNT).map_err(|_| "storage-failure".to_string())?;
    let secret_result = match entry.delete_credential() {
        Ok(()) | Err(KeyringError::NoEntry) => Ok(()),
        Err(_) => Err("storage-failure".to_string()),
    };
    let completion_result = clear_pairing_complete();
    complete_pairing_secret_deletion(secret_result, completion_result, || {
        #[cfg(target_os = "linux")]
        return remove_pairing_marker();
        #[cfg(not(target_os = "linux"))]
        Ok(())
    })
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

    #[cfg(target_os = "linux")]
    #[test]
    fn pairing_marker_is_idempotent_and_removable() {
        let root =
            std::env::temp_dir().join(format!("devhud-pairing-marker-{}", uuid::Uuid::now_v7()));
        let marker = root.join(PAIRING_MARKER_RELATIVE_PATH);
        assert!(ensure_pairing_marker_at(&marker).unwrap());
        assert_eq!(fs::read(&marker).unwrap(), b"v1\n");
        assert!(!ensure_pairing_marker_at(&marker).unwrap());
        remove_pairing_marker_at(&marker).unwrap();
        assert!(!marker.exists());
        remove_pairing_marker_at(&marker).unwrap();
        let _ = fs::remove_dir_all(root);
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn pairing_marker_remains_when_secret_write_fails() {
        let root =
            std::env::temp_dir().join(format!("devhud-pairing-marker-{}", uuid::Uuid::now_v7()));
        let marker = root.join(PAIRING_MARKER_RELATIVE_PATH);
        let result = write_pairing_secret_after_marker(
            ensure_pairing_marker_at(&marker).map_err(|_| "storage-failure".to_string()),
            || Err("storage-failure".to_string()),
        );
        assert_eq!(result, Err("storage-failure".to_string()));
        assert!(marker.exists());
        remove_pairing_marker_at(&marker).unwrap();
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn pairing_marker_remains_until_both_credentials_are_deleted() {
        use std::cell::Cell;

        let marker_was_removed = Cell::new(false);
        let result =
            complete_pairing_secret_deletion(Err("storage-failure".to_string()), Ok(()), || {
                marker_was_removed.set(true);
                Ok(())
            });
        assert_eq!(result, Err("storage-failure".to_string()));
        assert!(!marker_was_removed.get());

        complete_pairing_secret_deletion(Ok(()), Ok(()), || {
            marker_was_removed.set(true);
            Ok(())
        })
        .unwrap();
        assert!(marker_was_removed.get());
    }
}
