use std::collections::BTreeSet;

use keyring::{Entry, Error};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use tracing::error;

const SERVICE: &str = "io.delino.devhud.secure-settings.v1";
const INDEX_ACCOUNT: &str = "__index__";
const CHUNK_MANIFEST_PREFIX: &str = "devhud-credential-chunks-v1:";
const WINDOWS_CREDENTIAL_CHUNK_BYTES: usize = 1024;
const WINDOWS_CREDENTIAL_MAX_CHUNKS: usize = 64;

#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
struct SettingRef {
    kind: String,
    profile_id: String,
}

impl SettingRef {
    fn account(&self) -> String {
        format!("{}:{}", self.kind, self.profile_id)
    }
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
struct ChunkManifest {
    version: u8,
    slot: u8,
    chunks: usize,
    bytes: usize,
}

trait CredentialBackend {
    fn get(&self, account: &str) -> Result<Option<String>, String>;
    fn set(&self, account: &str, value: &str) -> Result<(), String>;
    fn delete(&self, account: &str) -> Result<(), String>;
}

struct KeyringBackend;

impl KeyringBackend {
    fn entry(account: &str) -> Result<Entry, String> {
        Entry::new(SERVICE, account).map_err(|_| "storage-failure".to_string())
    }
}

impl CredentialBackend for KeyringBackend {
    fn get(&self, account: &str) -> Result<Option<String>, String> {
        match Self::entry(account)?.get_password() {
            Ok(value) => Ok(Some(value)),
            Err(Error::NoEntry) => Ok(None),
            Err(_) => Err("storage-failure".to_string()),
        }
    }

    fn set(&self, account: &str, value: &str) -> Result<(), String> {
        Self::entry(account)?
            .set_password(value)
            .map_err(|_| "storage-failure".to_string())
    }

    fn delete(&self, account: &str) -> Result<(), String> {
        match Self::entry(account)?.delete_credential() {
            Ok(()) | Err(Error::NoEntry) => Ok(()),
            Err(_) => Err("storage-failure".to_string()),
        }
    }
}

pub fn handle(request: &Value) -> Result<Value, String> {
    handle_with_backend(request, &KeyringBackend, cfg!(target_os = "windows"))
}

fn handle_with_backend<B: CredentialBackend>(
    request: &Value,
    backend: &B,
    chunk_values: bool,
) -> Result<Value, String> {
    match request.get("operation").and_then(Value::as_str) {
        Some("secure.read") => {
            let setting = parse_setting(request)?;
            Ok(serde_json::json!({
                "kind": "secure-value",
                "value": read_value(backend, &setting, chunk_values)?
            }))
        }
        Some("secure.write") => {
            let setting = parse_setting(request)?;
            let value = request
                .get("value")
                .and_then(Value::as_str)
                .ok_or("invalid-argument")?;
            write_setting(backend, &setting, value, chunk_values)?;
            Ok(serde_json::json!({ "kind": "ok" }))
        }
        Some("secure.remove") => {
            let setting = parse_setting(request)?;
            delete_value(backend, &setting, chunk_values)?;
            let mut index = read_index(backend)?;
            index.remove(&setting);
            write_index(backend, &index)?;
            Ok(serde_json::json!({ "kind": "ok" }))
        }
        Some("secure.purge") => {
            purge(request, backend, chunk_values)?;
            Ok(serde_json::json!({ "kind": "ok" }))
        }
        _ => Err("invalid-argument".to_string()),
    }
}

fn write_setting<B: CredentialBackend>(
    backend: &B,
    setting: &SettingRef,
    value: &str,
    chunk_values: bool,
) -> Result<(), String> {
    let previous = read_value(backend, setting, chunk_values)?;
    let mut index = read_index(backend)?;
    let already_indexed = index.contains(setting);
    write_value(backend, setting, value, chunk_values)?;
    if already_indexed {
        return Ok(());
    }
    index.insert(setting.clone());
    if let Err(reason) = write_index(backend, &index) {
        let rollback = match previous {
            Some(previous) => write_value(backend, setting, &previous, chunk_values),
            None => delete_value(backend, setting, chunk_values),
        };
        if rollback.is_err() {
            error!(event = "secure_store_write_rollback_failed");
        }
        return Err(reason);
    }
    Ok(())
}

fn purge<B: CredentialBackend>(
    request: &Value,
    backend: &B,
    chunk_values: bool,
) -> Result<(), String> {
    let scope = request
        .get("scope")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    let profile = request.get("profileId").and_then(Value::as_str);
    if !matches!(scope, "logout" | "account-deletion" | "api-change")
        || (scope != "logout" && profile.is_none())
    {
        return Err("invalid-argument".to_string());
    }
    let mut index = read_index(backend)?;
    let targets: Vec<_> = index
        .iter()
        .filter(|setting| should_remove(setting, scope, profile))
        .cloned()
        .collect();
    for setting in targets {
        delete_value(backend, &setting, chunk_values)?;
        index.remove(&setting);
    }
    write_index(backend, &index)
}

fn should_remove(setting: &SettingRef, scope: &str, profile: Option<&str>) -> bool {
    match scope {
        "logout" => true,
        "account-deletion" => {
            setting.kind != "logto-session" || profile != Some(setting.profile_id.as_str())
        }
        "api-change" => {
            setting.kind == "logto-session" && profile == Some(setting.profile_id.as_str())
        }
        _ => false,
    }
}

fn parse_setting(request: &Value) -> Result<SettingRef, String> {
    let setting = request.get("setting").ok_or("invalid-argument")?;
    Ok(SettingRef {
        kind: setting
            .get("kind")
            .and_then(Value::as_str)
            .ok_or("invalid-argument")?
            .to_string(),
        profile_id: setting
            .get("profileId")
            .and_then(Value::as_str)
            .ok_or("invalid-argument")?
            .to_string(),
    })
}

fn read_value<B: CredentialBackend>(
    backend: &B,
    setting: &SettingRef,
    chunk_values: bool,
) -> Result<Option<String>, String> {
    let account = setting.account();
    let Some(stored) = backend.get(&account)? else {
        return Ok(None);
    };
    if !chunk_values {
        return Ok(Some(stored));
    }
    let Some(manifest) = parse_manifest(&stored)? else {
        return Ok(Some(stored));
    };
    let mut value = String::with_capacity(manifest.bytes);
    for index in 0..manifest.chunks {
        let chunk = backend
            .get(&chunk_account(&account, manifest.slot, index))?
            .ok_or_else(|| "storage-failure".to_string())?;
        if chunk.len() > WINDOWS_CREDENTIAL_CHUNK_BYTES {
            return Err("storage-failure".to_string());
        }
        value.push_str(&chunk);
    }
    if value.len() != manifest.bytes {
        return Err("storage-failure".to_string());
    }
    Ok(Some(value))
}

fn write_value<B: CredentialBackend>(
    backend: &B,
    setting: &SettingRef,
    value: &str,
    chunk_values: bool,
) -> Result<(), String> {
    let account = setting.account();
    if !chunk_values {
        return backend.set(&account, value);
    }
    let old_manifest = backend
        .get(&account)?
        .as_deref()
        .map(parse_manifest)
        .transpose()?
        .flatten();
    let slot = old_manifest
        .as_ref()
        .map_or(0, |manifest| 1 - manifest.slot);
    let chunks = split_chunks(value)?;
    let mut written = 0;
    for (index, chunk) in chunks.iter().enumerate() {
        if let Err(reason) = backend.set(&chunk_account(&account, slot, index), chunk) {
            cleanup_chunks(backend, &account, slot, written);
            return Err(reason);
        }
        written += 1;
    }
    let manifest = ChunkManifest {
        version: 1,
        slot,
        chunks: chunks.len(),
        bytes: value.len(),
    };
    let encoded = format!(
        "{CHUNK_MANIFEST_PREFIX}{}",
        serde_json::to_string(&manifest).map_err(|_| "storage-failure")?
    );
    if let Err(reason) = backend.set(&account, &encoded) {
        cleanup_chunks(backend, &account, slot, written);
        return Err(reason);
    }
    if let Some(old) = old_manifest {
        cleanup_chunks(backend, &account, old.slot, old.chunks);
    }
    for index in chunks.len()..WINDOWS_CREDENTIAL_MAX_CHUNKS {
        if backend
            .delete(&chunk_account(&account, slot, index))
            .is_err()
        {
            error!(event = "secure_store_stale_chunk_cleanup_failed");
            break;
        }
    }
    Ok(())
}

fn delete_value<B: CredentialBackend>(
    backend: &B,
    setting: &SettingRef,
    chunk_values: bool,
) -> Result<(), String> {
    let account = setting.account();
    if chunk_values {
        for slot in 0..=1 {
            for index in 0..WINDOWS_CREDENTIAL_MAX_CHUNKS {
                backend.delete(&chunk_account(&account, slot, index))?;
            }
        }
    }
    backend.delete(&account)
}

fn parse_manifest(value: &str) -> Result<Option<ChunkManifest>, String> {
    let Some(value) = value.strip_prefix(CHUNK_MANIFEST_PREFIX) else {
        return Ok(None);
    };
    let manifest: ChunkManifest =
        serde_json::from_str(value).map_err(|_| "storage-failure".to_string())?;
    if manifest.version != 1
        || manifest.slot > 1
        || manifest.chunks == 0
        || manifest.chunks > WINDOWS_CREDENTIAL_MAX_CHUNKS
        || manifest.bytes > WINDOWS_CREDENTIAL_CHUNK_BYTES * WINDOWS_CREDENTIAL_MAX_CHUNKS
    {
        return Err("storage-failure".to_string());
    }
    Ok(Some(manifest))
}

fn split_chunks(value: &str) -> Result<Vec<&str>, String> {
    if value.len() > WINDOWS_CREDENTIAL_CHUNK_BYTES * WINDOWS_CREDENTIAL_MAX_CHUNKS {
        return Err("storage-failure".to_string());
    }
    if value.is_empty() {
        return Ok(vec![""]);
    }
    let mut chunks = Vec::new();
    let mut start = 0;
    while start < value.len() {
        let mut end = (start + WINDOWS_CREDENTIAL_CHUNK_BYTES).min(value.len());
        while !value.is_char_boundary(end) {
            end -= 1;
        }
        if end == start {
            return Err("storage-failure".to_string());
        }
        chunks.push(&value[start..end]);
        start = end;
    }
    Ok(chunks)
}

fn chunk_account(account: &str, slot: u8, index: usize) -> String {
    format!("{account}:__chunk_v1:{slot}:{index}")
}

fn cleanup_chunks<B: CredentialBackend>(backend: &B, account: &str, slot: u8, chunks: usize) {
    for index in 0..chunks {
        if backend
            .delete(&chunk_account(account, slot, index))
            .is_err()
        {
            error!(event = "secure_store_chunk_cleanup_failed");
        }
    }
}

fn read_index<B: CredentialBackend>(backend: &B) -> Result<BTreeSet<SettingRef>, String> {
    let Some(value) = backend.get(INDEX_ACCOUNT)? else {
        return Ok(BTreeSet::new());
    };
    let tuples: Vec<(String, String)> =
        serde_json::from_str(&value).map_err(|_| "storage-failure")?;
    Ok(tuples
        .into_iter()
        .map(|(kind, profile_id)| SettingRef { kind, profile_id })
        .collect())
}

fn write_index<B: CredentialBackend>(
    backend: &B,
    index: &BTreeSet<SettingRef>,
) -> Result<(), String> {
    if index.is_empty() {
        return backend.delete(INDEX_ACCOUNT);
    }
    let tuples: Vec<_> = index
        .iter()
        .map(|setting| (&setting.kind, &setting.profile_id))
        .collect();
    backend.set(
        INDEX_ACCOUNT,
        &serde_json::to_string(&tuples).map_err(|_| "storage-failure")?,
    )
}

#[cfg(test)]
mod tests {
    use std::{cell::RefCell, collections::BTreeMap};

    use super::{
        CredentialBackend, INDEX_ACCOUNT, SettingRef, chunk_account, delete_value, read_value,
        should_remove, split_chunks, write_setting, write_value,
    };

    #[derive(Default)]
    struct MemoryBackend {
        values: RefCell<BTreeMap<String, String>>,
        fail_next_set: RefCell<Option<String>>,
    }

    impl CredentialBackend for MemoryBackend {
        fn get(&self, account: &str) -> Result<Option<String>, String> {
            Ok(self.values.borrow().get(account).cloned())
        }

        fn set(&self, account: &str, value: &str) -> Result<(), String> {
            if self.fail_next_set.borrow().as_deref() == Some(account) {
                self.fail_next_set.borrow_mut().take();
                return Err("storage-failure".to_string());
            }
            self.values
                .borrow_mut()
                .insert(account.to_string(), value.to_string());
            Ok(())
        }

        fn delete(&self, account: &str) -> Result<(), String> {
            self.values.borrow_mut().remove(account);
            Ok(())
        }
    }

    fn setting(kind: &str, profile_id: &str) -> SettingRef {
        SettingRef {
            kind: kind.to_string(),
            profile_id: profile_id.to_string(),
        }
    }

    #[test]
    fn account_deletion_retains_only_the_current_recovery_session() {
        assert!(!should_remove(
            &setting("logto-session", "current"),
            "account-deletion",
            Some("current")
        ));
        assert!(should_remove(
            &setting("logto-session", "old-api"),
            "account-deletion",
            Some("current")
        ));
        assert!(should_remove(
            &setting("github-pat", "current"),
            "account-deletion",
            Some("current")
        ));
        assert!(should_remove(
            &setting("r2-secret-access-key", "current"),
            "account-deletion",
            Some("current")
        ));
    }

    #[test]
    fn logout_removes_every_secret_and_api_change_only_its_session() {
        for kind in ["logto-session", "github-pat", "r2-access-key-id"] {
            assert!(should_remove(&setting(kind, "profile"), "logout", None));
        }
        assert!(should_remove(
            &setting("logto-session", "old-api"),
            "api-change",
            Some("old-api")
        ));
        assert!(!should_remove(
            &setting("github-pat", "old-api"),
            "api-change",
            Some("old-api")
        ));
    }

    #[test]
    fn windows_credentials_round_trip_the_full_utf8_contract_in_bounded_chunks() {
        let backend = MemoryBackend::default();
        let setting = setting("logto-session", "profile");
        let value = format!("{}{}", "a".repeat(60 * 1024), "한".repeat(1024));
        write_value(&backend, &setting, &value, true).expect("write chunks");
        assert_eq!(read_value(&backend, &setting, true), Ok(Some(value)));
        assert!(
            split_chunks("한글".repeat(1000).as_str())
                .expect("chunks")
                .iter()
                .all(|chunk| chunk.len() <= 1024)
        );
    }

    #[test]
    fn failed_index_update_restores_an_existing_credential() {
        let backend = MemoryBackend::default();
        let setting = setting("logto-session", "profile");
        write_value(&backend, &setting, "old-session", true).expect("initial value");
        *backend.fail_next_set.borrow_mut() = Some(INDEX_ACCOUNT.to_string());
        assert_eq!(
            write_setting(&backend, &setting, "new-session", true),
            Err("storage-failure".to_string())
        );
        assert_eq!(
            read_value(&backend, &setting, true),
            Ok(Some("old-session".to_string()))
        );
    }

    #[test]
    fn windows_deletion_removes_active_and_stale_chunk_slots() {
        let backend = MemoryBackend::default();
        let setting = setting("github-pat", "profile");
        write_value(&backend, &setting, &"a".repeat(2048), true).expect("first generation");
        write_value(&backend, &setting, &"b".repeat(1024), true).expect("second generation");
        backend.values.borrow_mut().insert(
            chunk_account(&setting.account(), 0, 63),
            "stale".to_string(),
        );
        delete_value(&backend, &setting, true).expect("delete");
        assert!(
            backend
                .values
                .borrow()
                .keys()
                .all(|account| !account.starts_with(&setting.account()))
        );
    }

    #[test]
    fn incomplete_windows_credentials_fail_closed() {
        let backend = MemoryBackend::default();
        let setting = setting("logto-session", "profile");
        write_value(&backend, &setting, &"x".repeat(2048), true).expect("write chunks");
        backend
            .values
            .borrow_mut()
            .remove(&chunk_account(&setting.account(), 0, 1));
        assert_eq!(
            read_value(&backend, &setting, true),
            Err("storage-failure".to_string())
        );
    }
}
