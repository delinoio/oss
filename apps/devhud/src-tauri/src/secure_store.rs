use std::collections::BTreeSet;

use keyring::{Entry, Error};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use tracing::error;
use zeroize::Zeroizing;

const SERVICE: &str = "io.delino.devhud.secure-settings.v1";
const INDEX_ACCOUNT: &str = "__index__";
const GITHUB_PAT_KIND: &str = "github-pat";
const GITHUB_PAT_SCOPE_KIND: &str = "github-pat-scope";
const REALQA_DRAFT_KEY_KIND: &str = "realqa-draft-key";
const REALQA_DRAFT_KEY_PROFILE: &str = "device-v1";
const CHUNK_MANIFEST_PREFIX: &str = "devhud-credential-chunks-v1:";
const CHUNK_MANIFEST_VERSION: u8 = 2;
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
    fn get_secret(&self, account: &str) -> Result<Option<Vec<u8>>, String>;
    fn set(&self, account: &str, value: &str) -> Result<(), String>;
    fn set_secret(&self, account: &str, value: &[u8]) -> Result<(), String>;
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

    fn get_secret(&self, account: &str) -> Result<Option<Vec<u8>>, String> {
        match Self::entry(account)?.get_secret() {
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

    fn set_secret(&self, account: &str, value: &[u8]) -> Result<(), String> {
        Self::entry(account)?
            .set_secret(value)
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

/// Returns the device-local RealQA draft key, creating it inside the platform
/// credential store when capture is used for the first time. The key is never
/// exposed through the frontend bridge and is indexed so the existing logout
/// purge remains authoritative while recoverable account deletion preserves it.
pub fn realqa_draft_key() -> Result<[u8; 32], String> {
    let backend = KeyringBackend;
    let setting = setting(REALQA_DRAFT_KEY_KIND, REALQA_DRAFT_KEY_PROFILE);
    if let Some(encoded) = read_value(&backend, &setting, cfg!(target_os = "windows"))? {
        return decode_draft_key(&encoded);
    }
    let key = rand::random::<[u8; 32]>();
    write_setting(
        &backend,
        &setting,
        &encode_draft_key(&key),
        cfg!(target_os = "windows"),
    )?;
    Ok(key)
}

pub(crate) struct R2Credentials {
    pub(crate) access_key_id: Zeroizing<String>,
    pub(crate) secret_access_key: Zeroizing<String>,
}

pub(crate) fn github_pat(profile_id: &str, scope_id: &str) -> Result<Zeroizing<String>, String> {
    let response = handle(&serde_json::json!({
        "operation": "secure.read",
        "setting": {
            "kind": GITHUB_PAT_KIND,
            "profileId": profile_id,
            "scopeId": scope_id,
        }
    }))?;
    response
        .get("value")
        .and_then(Value::as_str)
        .filter(|value| !value.trim().is_empty())
        .map(|value| Zeroizing::new(value.to_string()))
        .ok_or_else(|| "not-configured".to_string())
}

pub(crate) fn r2_credentials(profile_id: &str) -> Result<R2Credentials, String> {
    let backend = KeyringBackend;
    let access_key_id = read_value(
        &backend,
        &setting("r2-access-key-id", profile_id),
        cfg!(target_os = "windows"),
    )?
    .filter(|value| !value.trim().is_empty())
    .ok_or_else(|| "not-configured".to_string())?;
    let secret_access_key = read_value(
        &backend,
        &setting("r2-secret-access-key", profile_id),
        cfg!(target_os = "windows"),
    )?
    .filter(|value| !value.is_empty())
    .ok_or_else(|| "not-configured".to_string())?;
    Ok(R2Credentials {
        access_key_id: Zeroizing::new(access_key_id),
        secret_access_key: Zeroizing::new(secret_access_key),
    })
}

fn encode_draft_key(key: &[u8; 32]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut encoded = String::with_capacity(64);
    for byte in key {
        encoded.push(HEX[(byte >> 4) as usize] as char);
        encoded.push(HEX[(byte & 0x0f) as usize] as char);
    }
    encoded
}

fn decode_draft_key(encoded: &str) -> Result<[u8; 32], String> {
    if encoded.len() != 64 || !encoded.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err("storage-failure".to_string());
    }
    let mut key = [0_u8; 32];
    for (index, target) in key.iter_mut().enumerate() {
        let start = index * 2;
        *target = u8::from_str_radix(&encoded[start..start + 2], 16)
            .map_err(|_| "storage-failure".to_string())?;
    }
    Ok(key)
}

fn handle_with_backend<B: CredentialBackend>(
    request: &Value,
    backend: &B,
    chunk_values: bool,
) -> Result<Value, String> {
    match request.get("operation").and_then(Value::as_str) {
        Some("secure.read") => {
            let setting = parse_setting(request)?;
            let value = if setting.kind == GITHUB_PAT_KIND {
                read_github_pat(request, backend, &setting, chunk_values)?
            } else {
                read_value(backend, &setting, chunk_values)?
            };
            Ok(serde_json::json!({
                "kind": "secure-value",
                "value": value
            }))
        }
        Some("secure.write") => {
            let setting = parse_setting(request)?;
            let value = request
                .get("value")
                .and_then(Value::as_str)
                .ok_or("invalid-argument")?;
            if setting.kind == GITHUB_PAT_KIND {
                write_github_pat(request, backend, &setting, value, chunk_values)?;
            } else {
                write_setting(backend, &setting, value, chunk_values)?;
            }
            Ok(serde_json::json!({ "kind": "ok" }))
        }
        Some("secure.remove") => {
            let setting = parse_setting(request)?;
            if setting.kind == GITHUB_PAT_KIND {
                remove_github_pat_scope(request, backend, &setting.profile_id, chunk_values)?;
            } else {
                delete_value(backend, &setting, chunk_values)?;
                let mut index = read_index(backend, chunk_values)?;
                index.remove(&setting);
                write_index(backend, &index, chunk_values)?;
            }
            Ok(serde_json::json!({ "kind": "ok" }))
        }
        Some("secure.reconcile-github-pats") => {
            reconcile_github_pats(request, backend, chunk_values)?;
            Ok(serde_json::json!({ "kind": "ok" }))
        }
        Some("secure.purge") => {
            purge(request, backend, chunk_values)?;
            Ok(serde_json::json!({ "kind": "ok" }))
        }
        _ => Err("invalid-argument".to_string()),
    }
}

fn reconcile_github_pats<B: CredentialBackend>(
    request: &Value,
    backend: &B,
    chunk_values: bool,
) -> Result<(), String> {
    let scope_id = request
        .get("scopeId")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    let retained: BTreeSet<&str> = request
        .get("profileIds")
        .and_then(Value::as_array)
        .ok_or("invalid-argument")?
        .iter()
        .map(|value| value.as_str().ok_or("invalid-argument"))
        .collect::<Result<_, _>>()?;
    for profile_id in &retained {
        let pat = setting(GITHUB_PAT_KIND, profile_id);
        let marker = github_pat_scope(scope_id, profile_id);
        if read_value(backend, &pat, chunk_values)?.is_some()
            && read_value(backend, &marker, chunk_values)?.is_none()
        {
            write_setting(backend, &marker, "1", chunk_values)?;
        }
    }
    let index = read_index(backend, chunk_values)?;
    let targets: Vec<_> = index
        .iter()
        .filter_map(|candidate| github_pat_scope_profile(candidate, scope_id))
        .filter(|profile_id| !retained.contains(profile_id))
        .map(str::to_string)
        .collect();
    for profile_id in targets {
        remove_github_pat_scope(request, backend, &profile_id, chunk_values)?;
    }
    Ok(())
}

fn write_github_pat<B: CredentialBackend>(
    request: &Value,
    backend: &B,
    pat: &SettingRef,
    value: &str,
    chunk_values: bool,
) -> Result<(), String> {
    let marker = github_pat_scope(github_pat_scope_id(request)?, &pat.profile_id);
    let previous_marker = read_value(backend, &marker, chunk_values)?;
    let marker_was_indexed = read_index(backend, chunk_values)?.contains(&marker);
    write_setting(backend, &marker, "1", chunk_values)?;
    if let Err(reason) = write_setting(backend, pat, value, chunk_values) {
        if restore_setting(
            backend,
            &marker,
            previous_marker.as_deref(),
            marker_was_indexed,
            chunk_values,
        )
        .is_err()
        {
            error!(event = "github_pat_scope_rollback_failed");
        }
        return Err(reason);
    }
    Ok(())
}

fn read_github_pat<B: CredentialBackend>(
    request: &Value,
    backend: &B,
    pat: &SettingRef,
    chunk_values: bool,
) -> Result<Option<String>, String> {
    let marker = github_pat_scope(github_pat_scope_id(request)?, &pat.profile_id);
    if read_value(backend, &marker, chunk_values)?.is_none() {
        return Ok(None);
    }
    read_value(backend, pat, chunk_values)
}

fn github_pat_scope_id(request: &Value) -> Result<&str, String> {
    request
        .get("setting")
        .and_then(|setting| setting.get("scopeId"))
        .and_then(Value::as_str)
        .ok_or("invalid-argument".to_string())
}

fn remove_github_pat_scope<B: CredentialBackend>(
    request: &Value,
    backend: &B,
    profile_id: &str,
    chunk_values: bool,
) -> Result<(), String> {
    let scope_id = request
        .get("scopeId")
        .or_else(|| {
            request
                .get("setting")
                .and_then(|setting| setting.get("scopeId"))
        })
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    let marker = github_pat_scope(scope_id, profile_id);
    let pat = setting(GITHUB_PAT_KIND, profile_id);
    let mut index = read_index(backend, chunk_values)?;
    let retained_elsewhere = index.iter().any(|candidate| {
        candidate != &marker
            && github_pat_scope_profile(candidate, "")
                .is_some_and(|candidate_profile| candidate_profile == profile_id)
    });
    if !retained_elsewhere {
        delete_value(backend, &pat, chunk_values)?;
        index.remove(&pat);
    }
    delete_value(backend, &marker, chunk_values)?;
    index.remove(&marker);
    write_index(backend, &index, chunk_values)
}

fn setting(kind: &str, profile_id: &str) -> SettingRef {
    SettingRef {
        kind: kind.to_string(),
        profile_id: profile_id.to_string(),
    }
}

fn github_pat_scope(scope_id: &str, profile_id: &str) -> SettingRef {
    setting(GITHUB_PAT_SCOPE_KIND, &format!("{scope_id}:{profile_id}"))
}

fn github_pat_scope_profile<'a>(candidate: &'a SettingRef, scope_id: &str) -> Option<&'a str> {
    if candidate.kind != GITHUB_PAT_SCOPE_KIND {
        return None;
    }
    let (candidate_scope, profile_id) = candidate.profile_id.split_once(':')?;
    (scope_id.is_empty() || candidate_scope == scope_id).then_some(profile_id)
}

fn write_setting<B: CredentialBackend>(
    backend: &B,
    setting: &SettingRef,
    value: &str,
    chunk_values: bool,
) -> Result<(), String> {
    let previous = read_value(backend, setting, chunk_values)?;
    let mut index = read_index(backend, chunk_values)?;
    let already_indexed = index.contains(setting);
    write_value(backend, setting, value, chunk_values)?;
    if already_indexed {
        return Ok(());
    }
    index.insert(setting.clone());
    if let Err(reason) = write_index(backend, &index, chunk_values) {
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

fn restore_setting<B: CredentialBackend>(
    backend: &B,
    setting: &SettingRef,
    previous: Option<&str>,
    was_indexed: bool,
    chunk_values: bool,
) -> Result<(), String> {
    match previous {
        Some(value) => write_value(backend, setting, value, chunk_values)?,
        None => delete_value(backend, setting, chunk_values)?,
    }
    let mut index = read_index(backend, chunk_values)?;
    if was_indexed {
        index.insert(setting.clone());
    } else {
        index.remove(setting);
    }
    write_index(backend, &index, chunk_values)
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
    let mut index = read_index(backend, chunk_values)?;
    let targets: Vec<_> = index
        .iter()
        .filter(|setting| should_remove(setting, scope, profile))
        .cloned()
        .collect();
    for setting in targets {
        delete_value(backend, &setting, chunk_values)?;
        index.remove(&setting);
    }
    write_index(backend, &index, chunk_values)
}

fn should_remove(setting: &SettingRef, scope: &str, profile: Option<&str>) -> bool {
    match scope {
        "logout" => true,
        "account-deletion" => {
            setting.kind != REALQA_DRAFT_KEY_KIND
                && (setting.kind != "logto-session" || profile != Some(setting.profile_id.as_str()))
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
    if !chunk_values {
        return backend.get(&account);
    }
    read_chunked_value(backend, &account)
}

fn read_chunked_value<B: CredentialBackend>(
    backend: &B,
    account: &str,
) -> Result<Option<String>, String> {
    let Some(stored) = backend.get(account)? else {
        return Ok(None);
    };
    let Some(manifest) = parse_manifest(&stored)? else {
        return Ok(Some(stored));
    };
    let mut value = Vec::with_capacity(manifest.bytes);
    for index in 0..manifest.chunks {
        let chunk_account = chunk_account(account, manifest.slot, index);
        let chunk = if manifest.version == 1 {
            backend
                .get(&chunk_account)?
                .map(String::into_bytes)
                .ok_or_else(|| "storage-failure".to_string())?
        } else {
            backend
                .get_secret(&chunk_account)?
                .ok_or_else(|| "storage-failure".to_string())?
        };
        if chunk.len() > WINDOWS_CREDENTIAL_CHUNK_BYTES {
            return Err("storage-failure".to_string());
        }
        value.extend_from_slice(&chunk);
    }
    if value.len() != manifest.bytes {
        return Err("storage-failure".to_string());
    }
    String::from_utf8(value)
        .map(Some)
        .map_err(|_| "storage-failure".to_string())
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
    write_chunked_value(backend, &account, value)
}

fn write_chunked_value<B: CredentialBackend>(
    backend: &B,
    account: &str,
    value: &str,
) -> Result<(), String> {
    let old_manifest = backend
        .get(account)?
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
        if let Err(reason) = backend.set_secret(&chunk_account(account, slot, index), chunk) {
            cleanup_chunks(backend, account, slot, written);
            return Err(reason);
        }
        written += 1;
    }
    let manifest = ChunkManifest {
        version: CHUNK_MANIFEST_VERSION,
        slot,
        chunks: chunks.len(),
        bytes: value.len(),
    };
    let encoded = format!(
        "{CHUNK_MANIFEST_PREFIX}{}",
        serde_json::to_string(&manifest).map_err(|_| "storage-failure")?
    );
    if let Err(reason) = backend.set(account, &encoded) {
        cleanup_chunks(backend, account, slot, written);
        return Err(reason);
    }
    if let Some(old) = old_manifest {
        cleanup_chunks(backend, account, old.slot, old.chunks);
    }
    for index in chunks.len()..WINDOWS_CREDENTIAL_MAX_CHUNKS {
        if backend
            .delete(&chunk_account(account, slot, index))
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
    if !chunk_values {
        return backend.delete(&account);
    }
    delete_chunked_value(backend, &account)
}

fn delete_chunked_value<B: CredentialBackend>(backend: &B, account: &str) -> Result<(), String> {
    for slot in 0..=1 {
        for index in 0..WINDOWS_CREDENTIAL_MAX_CHUNKS {
            backend.delete(&chunk_account(account, slot, index))?;
        }
    }
    backend.delete(account)
}

fn parse_manifest(value: &str) -> Result<Option<ChunkManifest>, String> {
    let Some(value) = value.strip_prefix(CHUNK_MANIFEST_PREFIX) else {
        return Ok(None);
    };
    let manifest: ChunkManifest =
        serde_json::from_str(value).map_err(|_| "storage-failure".to_string())?;
    if !matches!(manifest.version, 1 | CHUNK_MANIFEST_VERSION)
        || manifest.slot > 1
        || manifest.chunks == 0
        || manifest.chunks > WINDOWS_CREDENTIAL_MAX_CHUNKS
        || manifest.bytes > WINDOWS_CREDENTIAL_CHUNK_BYTES * WINDOWS_CREDENTIAL_MAX_CHUNKS
    {
        return Err("storage-failure".to_string());
    }
    Ok(Some(manifest))
}

fn split_chunks(value: &str) -> Result<Vec<&[u8]>, String> {
    if value.len() > WINDOWS_CREDENTIAL_CHUNK_BYTES * WINDOWS_CREDENTIAL_MAX_CHUNKS {
        return Err("storage-failure".to_string());
    }
    if value.is_empty() {
        return Ok(vec![&[]]);
    }
    Ok(value
        .as_bytes()
        .chunks(WINDOWS_CREDENTIAL_CHUNK_BYTES)
        .collect())
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

fn read_index<B: CredentialBackend>(
    backend: &B,
    chunk_values: bool,
) -> Result<BTreeSet<SettingRef>, String> {
    let value = if chunk_values {
        read_chunked_value(backend, INDEX_ACCOUNT)?
    } else {
        backend.get(INDEX_ACCOUNT)?
    };
    let Some(value) = value else {
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
    chunk_values: bool,
) -> Result<(), String> {
    if index.is_empty() {
        return if chunk_values {
            delete_chunked_value(backend, INDEX_ACCOUNT)
        } else {
            backend.delete(INDEX_ACCOUNT)
        };
    }
    let tuples: Vec<_> = index
        .iter()
        .map(|setting| (&setting.kind, &setting.profile_id))
        .collect();
    let encoded = serde_json::to_string(&tuples).map_err(|_| "storage-failure")?;
    if chunk_values {
        write_chunked_value(backend, INDEX_ACCOUNT, &encoded)
    } else {
        backend.set(INDEX_ACCOUNT, &encoded)
    }
}

#[cfg(test)]
mod tests {
    use std::{cell::RefCell, collections::BTreeMap};

    use super::{
        CHUNK_MANIFEST_PREFIX, ChunkManifest, CredentialBackend, INDEX_ACCOUNT,
        REALQA_DRAFT_KEY_KIND, REALQA_DRAFT_KEY_PROFILE, SettingRef, chunk_account, delete_value,
        handle_with_backend, read_index, read_value, reconcile_github_pats, should_remove,
        split_chunks, write_index, write_setting, write_value,
    };

    #[derive(Clone, Debug, Eq, PartialEq)]
    enum MemoryCredential {
        Password(String),
        Secret(Vec<u8>),
    }

    #[derive(Default)]
    struct MemoryBackend {
        values: RefCell<BTreeMap<String, MemoryCredential>>,
        fail_next_set: RefCell<Option<String>>,
    }

    impl CredentialBackend for MemoryBackend {
        fn get(&self, account: &str) -> Result<Option<String>, String> {
            match self.values.borrow().get(account) {
                Some(MemoryCredential::Password(value)) => Ok(Some(value.clone())),
                Some(MemoryCredential::Secret(_)) => Err("storage-failure".to_string()),
                None => Ok(None),
            }
        }

        fn get_secret(&self, account: &str) -> Result<Option<Vec<u8>>, String> {
            match self.values.borrow().get(account) {
                Some(MemoryCredential::Secret(value)) => Ok(Some(value.clone())),
                Some(MemoryCredential::Password(_)) => Err("storage-failure".to_string()),
                None => Ok(None),
            }
        }

        fn set(&self, account: &str, value: &str) -> Result<(), String> {
            if self.fail_next_set.borrow().as_deref() == Some(account) {
                self.fail_next_set.borrow_mut().take();
                return Err("storage-failure".to_string());
            }
            self.values.borrow_mut().insert(
                account.to_string(),
                MemoryCredential::Password(value.to_string()),
            );
            Ok(())
        }

        fn set_secret(&self, account: &str, value: &[u8]) -> Result<(), String> {
            if self.fail_next_set.borrow().as_deref() == Some(account) {
                self.fail_next_set.borrow_mut().take();
                return Err("storage-failure".to_string());
            }
            self.values.borrow_mut().insert(
                account.to_string(),
                MemoryCredential::Secret(value.to_vec()),
            );
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
    fn account_deletion_retains_the_recovery_session_and_realqa_key() {
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
        assert!(!should_remove(
            &setting(REALQA_DRAFT_KEY_KIND, REALQA_DRAFT_KEY_PROFILE),
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
    fn github_pat_reconciliation_preserves_other_origin_profiles() {
        let backend = MemoryBackend::default();
        let old_origin = setting("github-pat", "old-origin");
        let current_origin = setting("github-pat", "current-origin");
        let r2 = setting("r2-secret-access-key", "removed");
        handle_with_backend(
            &serde_json::json!({ "operation": "secure.write", "setting": { "kind": "github-pat", "profileId": "old-origin", "scopeId": "origin.old" }, "value": "old-pat" }),
            &backend,
            false,
        )
        .expect("old-origin PAT");
        handle_with_backend(
            &serde_json::json!({ "operation": "secure.write", "setting": { "kind": "github-pat", "profileId": "current-origin", "scopeId": "origin.current" }, "value": "current-pat" }),
            &backend,
            false,
        )
        .expect("current-origin PAT");
        write_setting(&backend, &r2, "r2-secret", false).expect("R2 secret");

        reconcile_github_pats(
            &serde_json::json!({ "scopeId": "origin.current", "profileIds": [] }),
            &backend,
            false,
        )
        .expect("reconcile PATs");

        assert_eq!(
            read_value(&backend, &old_origin, false),
            Ok(Some("old-pat".to_string()))
        );
        assert_eq!(read_value(&backend, &current_origin, false), Ok(None));
        assert_eq!(
            read_value(&backend, &r2, false),
            Ok(Some("r2-secret".to_string()))
        );
    }

    #[test]
    fn github_pat_reconciliation_deletes_only_after_the_last_scope_releases_it() {
        let backend = MemoryBackend::default();
        let pat = setting("github-pat", "shared");
        for scope_id in ["origin.first", "origin.second"] {
            handle_with_backend(
                &serde_json::json!({ "operation": "secure.write", "setting": { "kind": "github-pat", "profileId": "shared", "scopeId": scope_id }, "value": "shared-pat" }),
                &backend,
                false,
            )
            .expect("scoped PAT");
        }

        for (scope_id, expected) in [
            ("origin.first", Some("shared-pat".to_string())),
            ("origin.second", None),
        ] {
            reconcile_github_pats(
                &serde_json::json!({ "scopeId": scope_id, "profileIds": [] }),
                &backend,
                false,
            )
            .expect("reconcile scope");
            assert_eq!(read_value(&backend, &pat, false), Ok(expected));
        }
    }

    #[test]
    fn github_pat_reads_require_the_matching_origin_scope() {
        let backend = MemoryBackend::default();
        handle_with_backend(
            &serde_json::json!({ "operation": "secure.write", "setting": { "kind": "github-pat", "profileId": "shared", "scopeId": "origin.first" }, "value": "shared-pat" }),
            &backend,
            false,
        )
        .expect("scoped PAT");

        for (scope_id, expected) in [
            ("origin.first", Some("shared-pat")),
            ("origin.second", None),
        ] {
            let response = handle_with_backend(
                &serde_json::json!({ "operation": "secure.read", "setting": { "kind": "github-pat", "profileId": "shared", "scopeId": scope_id } }),
                &backend,
                false,
            )
            .expect("PAT read");
            assert_eq!(response["value"].as_str(), expected);
        }
    }

    #[test]
    fn failed_github_pat_write_rolls_back_its_new_scope_marker() {
        let backend = MemoryBackend::default();
        handle_with_backend(
            &serde_json::json!({ "operation": "secure.write", "setting": { "kind": "github-pat", "profileId": "shared", "scopeId": "origin.first" }, "value": "old-pat" }),
            &backend,
            false,
        )
        .expect("existing PAT");
        *backend.fail_next_set.borrow_mut() = Some("github-pat:shared".to_string());

        assert_eq!(
            handle_with_backend(
                &serde_json::json!({ "operation": "secure.write", "setting": { "kind": "github-pat", "profileId": "shared", "scopeId": "origin.failed" }, "value": "new-pat" }),
                &backend,
                false,
            ),
            Err("storage-failure".to_string())
        );

        let marker = setting("github-pat-scope", "origin.failed:shared");
        assert_eq!(read_value(&backend, &marker, false), Ok(None));
        assert!(
            !read_index(&backend, false)
                .expect("index")
                .contains(&marker)
        );
        let response = handle_with_backend(
            &serde_json::json!({ "operation": "secure.read", "setting": { "kind": "github-pat", "profileId": "shared", "scopeId": "origin.first" } }),
            &backend,
            false,
        )
        .expect("existing PAT read");
        assert_eq!(response["value"], "old-pat");
    }

    #[test]
    fn windows_credentials_round_trip_the_full_utf8_contract_in_bounded_chunks() {
        let backend = MemoryBackend::default();
        let setting = setting("logto-session", "profile");
        let value = "한".repeat(21_845);
        assert_eq!(value.len(), (64 * 1024) - 1);
        write_value(&backend, &setting, &value, true).expect("write chunks");
        let chunks = split_chunks(&value).expect("chunks");
        assert_eq!(chunks.len(), 64);
        assert!(chunks.iter().all(|chunk| chunk.len() <= 1024));
        assert_eq!(chunks.concat(), "한".repeat(21_845).as_bytes());
        assert_eq!(read_value(&backend, &setting, true), Ok(Some(value)));
        assert!(
            backend
                .values
                .borrow()
                .values()
                .all(|credential| match credential {
                    MemoryCredential::Password(value) => value.len() <= 1024,
                    MemoryCredential::Secret(value) => value.len() <= 1024,
                })
        );
    }

    #[test]
    fn windows_credentials_read_legacy_string_chunks() {
        let backend = MemoryBackend::default();
        let setting = setting("logto-session", "profile");
        let first = "한".repeat(300);
        let second = "글".repeat(100);
        let value = format!("{first}{second}");
        backend
            .set(&chunk_account(&setting.account(), 0, 0), &first)
            .expect("first legacy chunk");
        backend
            .set(&chunk_account(&setting.account(), 0, 1), &second)
            .expect("second legacy chunk");
        let manifest = ChunkManifest {
            version: 1,
            slot: 0,
            chunks: 2,
            bytes: value.len(),
        };
        backend
            .set(
                &setting.account(),
                &format!(
                    "{CHUNK_MANIFEST_PREFIX}{}",
                    serde_json::to_string(&manifest).expect("manifest")
                ),
            )
            .expect("legacy manifest");

        assert_eq!(read_value(&backend, &setting, true), Ok(Some(value)));
    }

    #[test]
    fn windows_index_round_trips_across_bounded_physical_chunks() {
        let backend = MemoryBackend::default();
        let index = (0..24)
            .map(|number| setting("github-pat", &format!("{number:03}{}", "p".repeat(125))))
            .collect();
        write_index(&backend, &index, true).expect("write chunked index");

        assert_eq!(read_index(&backend, true), Ok(index));
        assert!(matches!(
            backend.values.borrow().get(INDEX_ACCOUNT),
            Some(MemoryCredential::Password(value)) if value.starts_with(CHUNK_MANIFEST_PREFIX)
        ));
        assert!(backend.values.borrow().iter().all(|(account, credential)| {
            !account.starts_with(&format!("{INDEX_ACCOUNT}:__chunk_v1:"))
                || matches!(credential, MemoryCredential::Secret(value) if value.len() <= 1024)
        }));
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
            MemoryCredential::Secret(b"stale".to_vec()),
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
