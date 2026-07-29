//! RealQA-only encrypted local draft record family.
//!
//! This is deliberately separate from the three base-shell persistence
//! records. Commands accept neither paths nor account identifiers. The active
//! native authentication binding selects an account-specific OS-vault key and
//! an opaque on-disk namespace.

use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    io::{self, Write},
    path::{Path, PathBuf},
    sync::Mutex,
    time::{SystemTime, UNIX_EPOCH},
};

use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use ring::aead::{AES_256_GCM, Aad, LessSafeKey, Nonce, UnboundKey};
use serde::{Deserialize, Serialize};
use subtle::ConstantTimeEq;
use url::Url;
use uuid::Uuid;
use zeroize::{Zeroize, Zeroizing};

use crate::{
    auth::RealQaDraftAccessContext,
    realqa_capture::{
        ComposerCore, ComposerImage, ComposerImageId, ComposerSessionId, EditorOperation,
        EncodedImage, ImageMediaType, MAX_ENCODED_SESSION_BYTES, deserialize_operations,
    },
};

const RECORD_FAMILY: &str = "devhud.realqa-draft.v1";
const RECORD_VERSION: u8 = 1;
const ENCRYPTION_ALGORITHM: &str = "aes-256-gcm";
const DIRECTORY_NAME: &str = "realqa-drafts.v1";
const DRAFT_EXTENSION: &str = "rqadraft";
const VAULT_SERVICE: &str = "dev.deli.devhud";
const VAULT_ACCOUNT: &str = "realqa-draft-device-keys.v1";
const EXTENSION_PAIRING_VAULT_ACCOUNT: &str = "realqa-extension-pairing.v1";
const MAX_IDENTIFIER_BYTES: usize = 128;
const MAX_TITLE_BYTES: usize = 4_096;
const MAX_BODY_BYTES: usize = 60_000;
const MAX_METADATA_FIELDS: usize = 64;
const MAX_FIELD_LABEL_BYTES: usize = 256;
const MAX_FIELD_VALUE_BYTES: usize = 8_192;
const MAX_URL_BYTES: usize = 8_192;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum DraftError {
    AuthenticationRequired,
    FirstTimeOffline,
    ReauthenticationRequired,
    AccountLocked,
    Conflict,
    Corrupt,
    FutureVersion,
    InvalidRecord,
    StorageUnavailable,
    WriteFailed,
}

#[derive(Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
enum DraftAccess {
    Locked,
    Offline,
    Online,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct DraftStatus {
    access: DraftAccess,
    can_capture: bool,
    can_submit: bool,
}

impl DraftStatus {
    pub(crate) const fn locked() -> Self {
        Self {
            access: DraftAccess::Locked,
            can_capture: false,
            can_submit: false,
        }
    }

    pub(crate) const fn from_access(access: &RealQaDraftAccessContext) -> Self {
        Self {
            access: if access.online_reauthenticated {
                DraftAccess::Online
            } else {
                DraftAccess::Offline
            },
            can_capture: true,
            can_submit: access.online_reauthenticated,
        }
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct RemovableField {
    id: String,
    label: String,
    value: String,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct RestorableUrl {
    value: String,
    stripped_query: Option<String>,
    stripped_fragment: Option<String>,
    warning: Option<PrivateHostWarning>,
}

#[derive(Clone, Copy, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
enum PrivateHostWarning {
    LocalhostOrPrivateHost,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct DraftImageReference {
    image_id: String,
    source_revision: u64,
    #[serde(deserialize_with = "deserialize_operations")]
    operations: Vec<EditorOperation>,
    output_media_type: ImageMediaType,
}

#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct DraftContentInput {
    title: String,
    body: String,
    preset_id: Option<String>,
    preset_revision: Option<u64>,
    destination_id: Option<String>,
    repository_definition_id: Option<String>,
    environment: Vec<RemovableField>,
    url: Option<RestorableUrl>,
    dom: Vec<RemovableField>,
    images: Vec<DraftImageReference>,
}

#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct SaveDraftRequest {
    draft_id: String,
    composer_session_id: String,
    expected_revision: Option<u64>,
    submission_idempotency_key: String,
    content: DraftContentInput,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct StoredDraftImage {
    image_id: String,
    original: EncodedImage,
    #[serde(deserialize_with = "deserialize_operations")]
    operations: Vec<EditorOperation>,
    output_media_type: ImageMediaType,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct StoredDraftContent {
    title: String,
    body: String,
    preset_id: Option<String>,
    preset_revision: Option<u64>,
    destination_id: Option<String>,
    repository_definition_id: Option<String>,
    environment: Vec<RemovableField>,
    url: Option<RestorableUrl>,
    dom: Vec<RemovableField>,
    images: Vec<StoredDraftImage>,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct StoredDraftRecord {
    version: u8,
    draft_id: String,
    revision: u64,
    submission_idempotency_key: String,
    created_at_unix_ms: u64,
    updated_at_unix_ms: u64,
    content: StoredDraftContent,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct DraftSummary {
    draft_id: String,
    revision: u64,
    created_at_unix_ms: u64,
    updated_at_unix_ms: u64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct LoadedDraftContent {
    title: String,
    body: String,
    preset_id: Option<String>,
    preset_revision: Option<u64>,
    destination_id: Option<String>,
    repository_definition_id: Option<String>,
    environment: Vec<RemovableField>,
    url: Option<RestorableUrl>,
    dom: Vec<RemovableField>,
    images: Vec<LoadedDraftImage>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct LoadedDraftImage {
    #[serde(flatten)]
    composer: ComposerImage,
    operations: Vec<EditorOperation>,
    output_media_type: ImageMediaType,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct LoadedDraft {
    draft_id: String,
    revision: u64,
    submission_idempotency_key: String,
    created_at_unix_ms: u64,
    updated_at_unix_ms: u64,
    content: LoadedDraftContent,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct EncryptedEnvelope {
    version: u8,
    algorithm: String,
    account_binding: String,
    draft_id: String,
    nonce: String,
    ciphertext: String,
}

#[derive(Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct RetainedDraftKeys {
    version: u8,
    keys: BTreeMap<String, String>,
}

impl Drop for RetainedDraftKeys {
    fn drop(&mut self) {
        for key in self.keys.values_mut() {
            key.zeroize();
        }
    }
}

trait DraftKeyVault: Send {
    fn load(&mut self) -> Result<RetainedDraftKeys, DraftError>;
    fn replace(&mut self, keys: &RetainedDraftKeys) -> Result<(), DraftError>;
    fn clear(&mut self) -> Result<(), DraftError>;
    fn clear_extension_pairing(&mut self) -> Result<(), DraftError>;
    #[cfg(test)]
    fn extension_pairing_present(&self) -> bool;
}

#[derive(Default)]
struct PlatformDraftKeyVault;

impl DraftKeyVault for PlatformDraftKeyVault {
    fn load(&mut self) -> Result<RetainedDraftKeys, DraftError> {
        let entry = keyring::Entry::new(VAULT_SERVICE, VAULT_ACCOUNT)
            .map_err(|_| DraftError::StorageUnavailable)?;
        let encoded = match entry.get_password() {
            Ok(value) => Zeroizing::new(value),
            Err(keyring::Error::NoEntry) => {
                return Ok(RetainedDraftKeys {
                    version: RECORD_VERSION,
                    keys: BTreeMap::new(),
                });
            }
            Err(_) => return Err(DraftError::StorageUnavailable),
        };
        let retained: RetainedDraftKeys =
            serde_json::from_str(&encoded).map_err(|_| DraftError::Corrupt)?;
        if retained.version > RECORD_VERSION {
            return Err(DraftError::FutureVersion);
        }
        if retained.version != RECORD_VERSION
            || retained.keys.iter().any(|(binding, key)| {
                !valid_account_binding(binding)
                    || URL_SAFE_NO_PAD
                        .decode(key)
                        .map(|decoded| decoded.len() != 32)
                        .unwrap_or(true)
            })
        {
            return Err(DraftError::Corrupt);
        }
        Ok(retained)
    }

    fn replace(&mut self, keys: &RetainedDraftKeys) -> Result<(), DraftError> {
        let encoded =
            Zeroizing::new(serde_json::to_string(keys).map_err(|_| DraftError::WriteFailed)?);
        keyring::Entry::new(VAULT_SERVICE, VAULT_ACCOUNT)
            .map_err(|_| DraftError::StorageUnavailable)?
            .set_password(&encoded)
            .map_err(|_| DraftError::WriteFailed)
    }

    fn clear(&mut self) -> Result<(), DraftError> {
        match keyring::Entry::new(VAULT_SERVICE, VAULT_ACCOUNT)
            .map_err(|_| DraftError::StorageUnavailable)?
            .delete_credential()
        {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(_) => Err(DraftError::WriteFailed),
        }
    }

    fn clear_extension_pairing(&mut self) -> Result<(), DraftError> {
        match keyring::Entry::new(VAULT_SERVICE, EXTENSION_PAIRING_VAULT_ACCOUNT)
            .map_err(|_| DraftError::StorageUnavailable)?
            .delete_credential()
        {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(_) => Err(DraftError::WriteFailed),
        }
    }

    #[cfg(test)]
    fn extension_pairing_present(&self) -> bool {
        false
    }
}

pub(crate) struct RealQaDraftState {
    directory: Option<PathBuf>,
    vault: Mutex<Box<dyn DraftKeyVault>>,
    write_lock: Mutex<()>,
}

impl RealQaDraftState {
    pub(crate) fn new(app_local_data: PathBuf) -> io::Result<Self> {
        let directory = app_local_data.join(DIRECTORY_NAME);
        prepare_directory(&directory)?;
        Ok(Self {
            directory: Some(directory),
            vault: Mutex::new(Box::<PlatformDraftKeyVault>::default()),
            write_lock: Mutex::new(()),
        })
    }

    pub(crate) fn unavailable() -> Self {
        Self {
            directory: None,
            vault: Mutex::new(Box::<PlatformDraftKeyVault>::default()),
            write_lock: Mutex::new(()),
        }
    }

    fn account_directory(&self, binding: &str) -> Result<PathBuf, DraftError> {
        if !valid_account_binding(binding) {
            return Err(DraftError::AccountLocked);
        }
        self.directory
            .as_ref()
            .map(|directory| directory.join(binding))
            .ok_or(DraftError::StorageUnavailable)
    }

    fn draft_path(&self, binding: &str, draft_id: &str) -> Result<PathBuf, DraftError> {
        validate_uuid_v7(draft_id)?;
        Ok(self
            .account_directory(binding)?
            .join(format!("{draft_id}.{DRAFT_EXTENSION}")))
    }

    fn key(&self, binding: &str, create: bool) -> Result<Option<Zeroizing<Vec<u8>>>, DraftError> {
        let mut vault = self
            .vault
            .lock()
            .map_err(|_| DraftError::StorageUnavailable)?;
        let mut retained = vault.load()?;
        if let Some(encoded) = retained.keys.get(binding) {
            return URL_SAFE_NO_PAD
                .decode(encoded)
                .map(Zeroizing::new)
                .map(Some)
                .map_err(|_| DraftError::Corrupt);
        }
        if !create {
            return Ok(None);
        }
        let mut key = Zeroizing::new(vec![0_u8; 32]);
        getrandom::fill(&mut key).map_err(|_| DraftError::StorageUnavailable)?;
        retained
            .keys
            .insert(binding.to_owned(), URL_SAFE_NO_PAD.encode(&*key));
        vault.replace(&retained)?;
        Ok(Some(key))
    }

    pub(crate) fn list(
        &self,
        access: &RealQaDraftAccessContext,
    ) -> Result<Vec<DraftSummary>, DraftError> {
        let _guard = self
            .write_lock
            .lock()
            .map_err(|_| DraftError::StorageUnavailable)?;
        let directory = self.account_directory(&access.account_binding)?;
        validate_existing_directory(&directory)?;
        let entries = match fs::read_dir(&directory) {
            Ok(entries) => entries,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(Vec::new()),
            Err(_) => return Err(DraftError::StorageUnavailable),
        };
        let Some(key) = self.key(&access.account_binding, false)? else {
            return Ok(Vec::new());
        };
        let mut summaries = Vec::new();
        for entry in entries {
            let entry = entry.map_err(|_| DraftError::StorageUnavailable)?;
            if !entry
                .file_type()
                .map_err(|_| DraftError::StorageUnavailable)?
                .is_file()
                || entry.path().extension().and_then(|value| value.to_str())
                    != Some(DRAFT_EXTENSION)
            {
                continue;
            }
            let record = read_record(&entry.path(), &access.account_binding, &key)?;
            summaries.push(summary(&record));
        }
        summaries.sort_by_key(|summary| (summary.updated_at_unix_ms, summary.draft_id.clone()));
        summaries.reverse();
        Ok(summaries)
    }

    pub(crate) fn save(
        &self,
        access: &RealQaDraftAccessContext,
        composer: &ComposerCore,
        request: SaveDraftRequest,
    ) -> Result<DraftSummary, DraftError> {
        let _guard = self
            .write_lock
            .lock()
            .map_err(|_| DraftError::StorageUnavailable)?;
        validate_uuid_v7(&request.draft_id)?;
        validate_identifier(&request.composer_session_id)?;
        validate_uuid_v7(&request.submission_idempotency_key)?;
        validate_content(&request.content)?;
        let path = self.draft_path(&access.account_binding, &request.draft_id)?;
        let directory = path.parent().ok_or(DraftError::StorageUnavailable)?;
        validate_existing_directory(directory)?;
        let key = self
            .key(&access.account_binding, true)?
            .ok_or(DraftError::StorageUnavailable)?;
        let current = match fs::symlink_metadata(&path) {
            Ok(metadata)
                if metadata.file_type().is_file() && !metadata.file_type().is_symlink() =>
            {
                Some(read_record(&path, &access.account_binding, &key)?)
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => None,
            _ => return Err(DraftError::StorageUnavailable),
        };
        match (&current, request.expected_revision) {
            (None, None) => {}
            (Some(current), Some(expected)) if current.revision == expected => {}
            _ => return Err(DraftError::Conflict),
        }
        if current.as_ref().is_some_and(|record| {
            record.submission_idempotency_key != request.submission_idempotency_key
        }) {
            return Err(DraftError::Conflict);
        }
        let revision = current.as_ref().map_or(Ok(1), |record| {
            record.revision.checked_add(1).ok_or(DraftError::Conflict)
        })?;
        let now = unix_milliseconds()?;
        let mut images = Vec::with_capacity(request.content.images.len());
        let mut original_bytes = 0_u64;
        for image in &request.content.images {
            let original = composer
                .clone_original_for_draft(
                    &ComposerSessionId(request.composer_session_id.clone()),
                    &ComposerImageId(image.image_id.clone()),
                    image.source_revision,
                )
                .map_err(|_| DraftError::InvalidRecord)?;
            original_bytes = original_bytes
                .checked_add(
                    original
                        .bytes
                        .len()
                        .try_into()
                        .map_err(|_| DraftError::InvalidRecord)?,
                )
                .ok_or(DraftError::InvalidRecord)?;
            if original_bytes > MAX_ENCODED_SESSION_BYTES {
                return Err(DraftError::InvalidRecord);
            }
            images.push(StoredDraftImage {
                image_id: image.image_id.clone(),
                original,
                operations: image.operations.clone(),
                output_media_type: image.output_media_type,
            });
        }
        let record = StoredDraftRecord {
            version: RECORD_VERSION,
            draft_id: request.draft_id,
            revision,
            submission_idempotency_key: request.submission_idempotency_key,
            created_at_unix_ms: current
                .as_ref()
                .map_or(now, |record| record.created_at_unix_ms),
            updated_at_unix_ms: now,
            content: StoredDraftContent {
                title: request.content.title,
                body: request.content.body,
                preset_id: request.content.preset_id,
                preset_revision: request.content.preset_revision,
                destination_id: request.content.destination_id,
                repository_definition_id: request.content.repository_definition_id,
                environment: request.content.environment,
                url: request.content.url.map(normalize_url).transpose()?,
                dom: request.content.dom,
                images,
            },
        };
        let envelope = encrypt_record(&record, &access.account_binding, &key)?;
        prepare_directory(directory).map_err(|_| DraftError::StorageUnavailable)?;
        write_atomically(&path, &envelope).map_err(|_| DraftError::WriteFailed)?;
        Ok(summary(&record))
    }

    pub(crate) fn load(
        &self,
        access: &RealQaDraftAccessContext,
        composer: &ComposerCore,
        draft_id: &str,
        composer_session_id: &str,
    ) -> Result<LoadedDraft, DraftError> {
        let _guard = self
            .write_lock
            .lock()
            .map_err(|_| DraftError::StorageUnavailable)?;
        validate_identifier(composer_session_id)?;
        let key = self
            .key(&access.account_binding, false)?
            .ok_or(DraftError::AccountLocked)?;
        validate_existing_directory(&self.account_directory(&access.account_binding)?)?;
        let record = read_record(
            &self.draft_path(&access.account_binding, draft_id)?,
            &access.account_binding,
            &key,
        )?;
        let mut images = Vec::with_capacity(record.content.images.len());
        for image in &record.content.images {
            let restored = composer
                .restore_original_from_draft(
                    ComposerSessionId(composer_session_id.to_owned()),
                    ComposerImageId(image.image_id.clone()),
                    image.original.clone(),
                )
                .map_err(|_| DraftError::InvalidRecord)?;
            images.push(LoadedDraftImage {
                composer: restored,
                operations: image.operations.clone(),
                output_media_type: image.output_media_type,
            });
        }
        Ok(LoadedDraft {
            draft_id: record.draft_id,
            revision: record.revision,
            submission_idempotency_key: record.submission_idempotency_key,
            created_at_unix_ms: record.created_at_unix_ms,
            updated_at_unix_ms: record.updated_at_unix_ms,
            content: LoadedDraftContent {
                title: record.content.title,
                body: record.content.body,
                preset_id: record.content.preset_id,
                preset_revision: record.content.preset_revision,
                destination_id: record.content.destination_id,
                repository_definition_id: record.content.repository_definition_id,
                environment: record.content.environment,
                url: record.content.url,
                dom: record.content.dom,
                images,
            },
        })
    }

    pub(crate) fn delete(
        &self,
        access: &RealQaDraftAccessContext,
        draft_id: &str,
        expected_revision: u64,
    ) -> Result<(), DraftError> {
        let _guard = self
            .write_lock
            .lock()
            .map_err(|_| DraftError::StorageUnavailable)?;
        let key = self
            .key(&access.account_binding, false)?
            .ok_or(DraftError::AccountLocked)?;
        validate_existing_directory(&self.account_directory(&access.account_binding)?)?;
        let path = self.draft_path(&access.account_binding, draft_id)?;
        let record = read_record(&path, &access.account_binding, &key)?;
        if record.revision != expected_revision {
            return Err(DraftError::Conflict);
        }
        fs::remove_file(path).map_err(|_| DraftError::WriteFailed)
    }

    pub(crate) fn assert_submission_allowed(
        &self,
        access: &RealQaDraftAccessContext,
        draft_id: &str,
        expected_revision: u64,
    ) -> Result<(), DraftError> {
        if !access.online_reauthenticated {
            return Err(DraftError::ReauthenticationRequired);
        }
        let _guard = self
            .write_lock
            .lock()
            .map_err(|_| DraftError::StorageUnavailable)?;
        let key = self
            .key(&access.account_binding, false)?
            .ok_or(DraftError::AccountLocked)?;
        validate_existing_directory(&self.account_directory(&access.account_binding)?)?;
        let record = read_record(
            &self.draft_path(&access.account_binding, draft_id)?,
            &access.account_binding,
            &key,
        )?;
        if record.revision != expected_revision {
            return Err(DraftError::Conflict);
        }
        Ok(())
    }

    pub(crate) fn preflight_reset(&self) -> Result<(), DraftError> {
        let _guard = self
            .write_lock
            .lock()
            .map_err(|_| DraftError::StorageUnavailable)?;
        let directory = self
            .directory
            .as_ref()
            .ok_or(DraftError::StorageUnavailable)?;
        match fs::symlink_metadata(directory) {
            Ok(metadata) if metadata.file_type().is_dir() && !metadata.file_type().is_symlink() => {
                Ok(())
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
            _ => Err(DraftError::StorageUnavailable),
        }
    }

    pub(crate) fn reset(&self) -> Result<(), DraftError> {
        let _guard = self
            .write_lock
            .lock()
            .map_err(|_| DraftError::StorageUnavailable)?;
        let directory = self
            .directory
            .as_ref()
            .ok_or(DraftError::StorageUnavailable)?;
        let staged = directory.with_extension(format!("reset-{}", Uuid::now_v7()));
        let staged_current = match fs::rename(directory, &staged) {
            Ok(()) => true,
            Err(error) if error.kind() == io::ErrorKind::NotFound => false,
            Err(_) => return Err(DraftError::WriteFailed),
        };
        if staged_current {
            remove_staged_with_rollback(directory, &staged, |path| fs::remove_dir_all(path))?;
        }
        prepare_directory(directory).map_err(|_| DraftError::WriteFailed)?;
        let mut vault = self
            .vault
            .lock()
            .map_err(|_| DraftError::StorageUnavailable)?;
        let draft_key_clear_failed = vault.clear().is_err();
        let pairing_clear_failed = vault.clear_extension_pairing().is_err();
        if draft_key_clear_failed || pairing_clear_failed {
            Err(DraftError::WriteFailed)
        } else {
            Ok(())
        }
    }
}

fn remove_staged_with_rollback(
    directory: &Path,
    staged: &Path,
    remove: impl FnOnce(&Path) -> io::Result<()>,
) -> Result<(), DraftError> {
    if remove(staged).is_err() {
        fs::rename(staged, directory).map_err(|_| DraftError::WriteFailed)?;
        return Err(DraftError::WriteFailed);
    }
    Ok(())
}

fn summary(record: &StoredDraftRecord) -> DraftSummary {
    DraftSummary {
        draft_id: record.draft_id.clone(),
        revision: record.revision,
        created_at_unix_ms: record.created_at_unix_ms,
        updated_at_unix_ms: record.updated_at_unix_ms,
    }
}

fn validate_uuid_v7(value: &str) -> Result<(), DraftError> {
    let parsed = Uuid::parse_str(value).map_err(|_| DraftError::InvalidRecord)?;
    if parsed.get_version_num() != 7 || parsed.to_string() != value {
        return Err(DraftError::InvalidRecord);
    }
    Ok(())
}

fn validate_identifier(value: &str) -> Result<(), DraftError> {
    if value.is_empty()
        || value.len() > MAX_IDENTIFIER_BYTES
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.'))
    {
        return Err(DraftError::InvalidRecord);
    }
    Ok(())
}

fn valid_account_binding(value: &str) -> bool {
    URL_SAFE_NO_PAD
        .decode(value)
        .map(|decoded| decoded.len() == 32)
        .unwrap_or(false)
}

fn validate_content(content: &DraftContentInput) -> Result<(), DraftError> {
    if content.title.len() > MAX_TITLE_BYTES
        || content.body.len() > MAX_BODY_BYTES
        || content.environment.len() > MAX_METADATA_FIELDS
        || content.dom.len() > MAX_METADATA_FIELDS
    {
        return Err(DraftError::InvalidRecord);
    }
    for value in [
        content.preset_id.as_deref(),
        content.destination_id.as_deref(),
        content.repository_definition_id.as_deref(),
    ]
    .into_iter()
    .flatten()
    {
        validate_identifier(value)?;
    }
    for field in content.environment.iter().chain(&content.dom) {
        validate_identifier(&field.id)?;
        if field.label.is_empty()
            || field.label.len() > MAX_FIELD_LABEL_BYTES
            || field.value.len() > MAX_FIELD_VALUE_BYTES
        {
            return Err(DraftError::InvalidRecord);
        }
    }
    let mut image_ids = BTreeSet::new();
    for image in &content.images {
        validate_identifier(&image.image_id)?;
        if image.source_revision == 0 || !image_ids.insert(&image.image_id) {
            return Err(DraftError::InvalidRecord);
        }
    }
    Ok(())
}

fn normalize_url(mut value: RestorableUrl) -> Result<RestorableUrl, DraftError> {
    if value.value.len() > MAX_URL_BYTES {
        return Err(DraftError::InvalidRecord);
    }
    let parsed = Url::parse(&value.value).map_err(|_| DraftError::InvalidRecord)?;
    if !matches!(parsed.scheme(), "http" | "https")
        || parsed.host_str().is_none()
        || !parsed.username().is_empty()
        || parsed.password().is_some()
    {
        return Err(DraftError::InvalidRecord);
    }
    if parsed.query().is_some()
        || parsed.fragment().is_some()
        || value
            .stripped_query
            .as_ref()
            .is_some_and(|query| !query.starts_with('?') || query.len() > MAX_URL_BYTES)
        || value
            .stripped_fragment
            .as_ref()
            .is_some_and(|fragment| !fragment.starts_with('#') || fragment.len() > MAX_URL_BYTES)
    {
        return Err(DraftError::InvalidRecord);
    }
    value.warning = is_local_or_private_host(parsed.host_str().unwrap_or_default())
        .then_some(PrivateHostWarning::LocalhostOrPrivateHost);
    Ok(value)
}

fn is_local_or_private_host(host: &str) -> bool {
    let host = host.to_ascii_lowercase();
    if host == "localhost" || host.ends_with(".localhost") {
        return true;
    }
    host.parse::<std::net::IpAddr>()
        .is_ok_and(|address| match address {
            std::net::IpAddr::V4(address) => {
                address.is_private()
                    || address.is_loopback()
                    || address.is_link_local()
                    || address.is_unspecified()
            }
            std::net::IpAddr::V6(address) => {
                address.is_loopback()
                    || address.is_unspecified()
                    || (address.segments()[0] & 0xfe00) == 0xfc00
                    || (address.segments()[0] & 0xffc0) == 0xfe80
            }
        })
}

fn associated_data(account_binding: &str, draft_id: &str) -> Vec<u8> {
    format!("{RECORD_FAMILY}\0{account_binding}\0{draft_id}").into_bytes()
}

fn encrypt_record(
    record: &StoredDraftRecord,
    account_binding: &str,
    key: &[u8],
) -> Result<Vec<u8>, DraftError> {
    let mut nonce_bytes = [0_u8; 12];
    getrandom::fill(&mut nonce_bytes).map_err(|_| DraftError::StorageUnavailable)?;
    let mut plaintext = serde_json::to_vec(record).map_err(|_| DraftError::InvalidRecord)?;
    let key = LessSafeKey::new(
        UnboundKey::new(&AES_256_GCM, key).map_err(|_| DraftError::StorageUnavailable)?,
    );
    key.seal_in_place_append_tag(
        Nonce::assume_unique_for_key(nonce_bytes),
        Aad::from(associated_data(account_binding, &record.draft_id)),
        &mut plaintext,
    )
    .map_err(|_| DraftError::WriteFailed)?;
    let ciphertext = URL_SAFE_NO_PAD.encode(&plaintext);
    plaintext.zeroize();
    serde_json::to_vec(&EncryptedEnvelope {
        version: RECORD_VERSION,
        algorithm: ENCRYPTION_ALGORITHM.to_owned(),
        account_binding: account_binding.to_owned(),
        draft_id: record.draft_id.clone(),
        nonce: URL_SAFE_NO_PAD.encode(nonce_bytes),
        ciphertext,
    })
    .map_err(|_| DraftError::WriteFailed)
}

fn decrypt_record(
    envelope: &[u8],
    account_binding: &str,
    key: &[u8],
) -> Result<StoredDraftRecord, DraftError> {
    let envelope: EncryptedEnvelope =
        serde_json::from_slice(envelope).map_err(|_| DraftError::Corrupt)?;
    if envelope.version > RECORD_VERSION {
        return Err(DraftError::FutureVersion);
    }
    if envelope.version != RECORD_VERSION || envelope.algorithm != ENCRYPTION_ALGORITHM {
        return Err(DraftError::Corrupt);
    }
    if !bool::from(
        envelope
            .account_binding
            .as_bytes()
            .ct_eq(account_binding.as_bytes()),
    ) {
        return Err(DraftError::AccountLocked);
    }
    validate_uuid_v7(&envelope.draft_id).map_err(|_| DraftError::Corrupt)?;
    let nonce = URL_SAFE_NO_PAD
        .decode(&envelope.nonce)
        .map_err(|_| DraftError::Corrupt)?;
    let nonce: [u8; 12] = nonce.try_into().map_err(|_| DraftError::Corrupt)?;
    let mut ciphertext = Zeroizing::new(
        URL_SAFE_NO_PAD
            .decode(&envelope.ciphertext)
            .map_err(|_| DraftError::Corrupt)?,
    );
    let key = LessSafeKey::new(
        UnboundKey::new(&AES_256_GCM, key).map_err(|_| DraftError::StorageUnavailable)?,
    );
    let plaintext = key
        .open_in_place(
            Nonce::assume_unique_for_key(nonce),
            Aad::from(associated_data(account_binding, &envelope.draft_id)),
            &mut ciphertext,
        )
        .map_err(|_| DraftError::AccountLocked)?;
    let record: StoredDraftRecord =
        serde_json::from_slice(plaintext).map_err(|_| DraftError::Corrupt)?;
    if record.version > RECORD_VERSION {
        return Err(DraftError::FutureVersion);
    }
    if record.version != RECORD_VERSION || record.draft_id != envelope.draft_id {
        return Err(DraftError::Corrupt);
    }
    Ok(record)
}

fn read_record(
    path: &Path,
    account_binding: &str,
    key: &[u8],
) -> Result<StoredDraftRecord, DraftError> {
    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.file_type().is_file() && !metadata.file_type().is_symlink() => {}
        Ok(_) => return Err(DraftError::StorageUnavailable),
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            return Err(DraftError::InvalidRecord);
        }
        Err(_) => return Err(DraftError::StorageUnavailable),
    }
    let bytes = fs::read(path).map_err(|error| {
        if error.kind() == io::ErrorKind::NotFound {
            DraftError::InvalidRecord
        } else {
            DraftError::StorageUnavailable
        }
    })?;
    decrypt_record(&bytes, account_binding, key)
}

fn validate_existing_directory(path: &Path) -> Result<(), DraftError> {
    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.file_type().is_dir() && !metadata.file_type().is_symlink() => {
            Ok(())
        }
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        _ => Err(DraftError::StorageUnavailable),
    }
}

fn prepare_directory(path: &Path) -> io::Result<()> {
    fs::create_dir_all(path)?;
    if fs::symlink_metadata(path)?.file_type().is_symlink() {
        return Err(io::Error::other(
            "RealQA draft directory must not be a symbolic link",
        ));
    }
    Ok(())
}

fn write_atomically(path: &Path, contents: &[u8]) -> io::Result<()> {
    write_atomically_with(path, contents, replace_file)
}

fn write_atomically_with(
    path: &Path,
    contents: &[u8],
    replace: impl FnOnce(&Path, &Path) -> io::Result<()>,
) -> io::Result<()> {
    let temporary = path.with_extension(format!("{}.{}.tmp", DRAFT_EXTENSION, Uuid::now_v7()));
    let mut file = fs::OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(&temporary)?;
    if let Err(error) = file.write_all(contents).and_then(|()| file.sync_all()) {
        drop(file);
        let _ = fs::remove_file(&temporary);
        return Err(error);
    }
    drop(file);
    if let Err(error) = replace(&temporary, path) {
        let _ = fs::remove_file(&temporary);
        return Err(error);
    }
    Ok(())
}

#[cfg(not(target_os = "windows"))]
fn replace_file(source: &Path, destination: &Path) -> io::Result<()> {
    fs::rename(source, destination)
}

#[cfg(target_os = "windows")]
fn replace_file(source: &Path, destination: &Path) -> io::Result<()> {
    use std::os::windows::ffi::OsStrExt;

    const MOVEFILE_REPLACE_EXISTING: u32 = 0x1;
    const MOVEFILE_WRITE_THROUGH: u32 = 0x8;

    #[link(name = "Kernel32")]
    unsafe extern "system" {
        fn MoveFileExW(
            existing_file_name: *const u16,
            new_file_name: *const u16,
            flags: u32,
        ) -> i32;
    }

    let source = source
        .as_os_str()
        .encode_wide()
        .chain(std::iter::once(0))
        .collect::<Vec<_>>();
    let destination = destination
        .as_os_str()
        .encode_wide()
        .chain(std::iter::once(0))
        .collect::<Vec<_>>();

    // SAFETY: both buffers are stable, NUL-terminated UTF-16 paths for the
    // duration of the call. The files share a directory, so replacement stays
    // on one filesystem and WRITE_THROUGH persists the directory entry.
    let result = unsafe {
        MoveFileExW(
            source.as_ptr(),
            destination.as_ptr(),
            MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH,
        )
    };
    if result == 0 {
        Err(io::Error::last_os_error())
    } else {
        Ok(())
    }
}

fn unix_milliseconds() -> Result<u64, DraftError> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| DraftError::StorageUnavailable)?
        .as_millis()
        .try_into()
        .map_err(|_| DraftError::StorageUnavailable)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::realqa_capture::{DecodedImage, encode_image};

    struct MemoryVault {
        retained: Option<String>,
        fail_write: bool,
        extension_pairing: bool,
    }

    impl Default for MemoryVault {
        fn default() -> Self {
            Self {
                retained: None,
                fail_write: false,
                extension_pairing: true,
            }
        }
    }

    impl DraftKeyVault for MemoryVault {
        fn load(&mut self) -> Result<RetainedDraftKeys, DraftError> {
            self.retained
                .as_ref()
                .map(|value| serde_json::from_str(value).map_err(|_| DraftError::Corrupt))
                .unwrap_or_else(|| {
                    Ok(RetainedDraftKeys {
                        version: RECORD_VERSION,
                        keys: BTreeMap::new(),
                    })
                })
        }

        fn replace(&mut self, keys: &RetainedDraftKeys) -> Result<(), DraftError> {
            if self.fail_write {
                return Err(DraftError::WriteFailed);
            }
            self.retained = Some(serde_json::to_string(keys).map_err(|_| DraftError::WriteFailed)?);
            Ok(())
        }

        fn clear(&mut self) -> Result<(), DraftError> {
            self.retained = None;
            Ok(())
        }

        fn clear_extension_pairing(&mut self) -> Result<(), DraftError> {
            self.extension_pairing = false;
            Ok(())
        }

        fn extension_pairing_present(&self) -> bool {
            self.extension_pairing
        }
    }

    fn temporary_directory(name: &str) -> PathBuf {
        std::env::temp_dir().join(format!(
            "devhud-realqa-draft-{name}-{}-{}",
            std::process::id(),
            Uuid::now_v7()
        ))
    }

    fn state(name: &str) -> RealQaDraftState {
        let directory = temporary_directory(name);
        prepare_directory(&directory).unwrap();
        RealQaDraftState {
            directory: Some(directory),
            vault: Mutex::new(Box::<MemoryVault>::default()),
            write_lock: Mutex::new(()),
        }
    }

    fn access(subject: &str, online: bool) -> RealQaDraftAccessContext {
        use sha2::{Digest, Sha256};
        RealQaDraftAccessContext {
            account_binding: URL_SAFE_NO_PAD.encode(Sha256::digest(subject.as_bytes())),
            online_reauthenticated: online,
        }
    }

    fn request(composer: &ComposerCore, draft_id: &str) -> SaveDraftRequest {
        let original = encode_image(
            &DecodedImage {
                width: 2,
                height: 1,
                rgba: vec![1, 2, 3, 255, 4, 5, 6, 255],
            },
            ImageMediaType::Png,
        )
        .unwrap();
        let image = composer
            .restore_original_from_draft(
                ComposerSessionId(draft_id.to_owned()),
                ComposerImageId("image-1".to_owned()),
                original,
            )
            .unwrap();
        SaveDraftRequest {
            draft_id: draft_id.to_owned(),
            composer_session_id: draft_id.to_owned(),
            expected_revision: None,
            submission_idempotency_key: Uuid::now_v7().to_string(),
            content: DraftContentInput {
                title: "private title".to_owned(),
                body: "private body".to_owned(),
                preset_id: Some("preset".to_owned()),
                preset_revision: Some(2),
                destination_id: Some("destination".to_owned()),
                repository_definition_id: Some("bugs.yml".to_owned()),
                environment: vec![RemovableField {
                    id: "os".to_owned(),
                    label: "OS".to_owned(),
                    value: "private environment".to_owned(),
                }],
                url: Some(RestorableUrl {
                    value: "https://example.com/issue".to_owned(),
                    stripped_query: Some("?token=private".to_owned()),
                    stripped_fragment: Some("#private".to_owned()),
                    warning: None,
                }),
                dom: vec![RemovableField {
                    id: "selector".to_owned(),
                    label: "Selector".to_owned(),
                    value: "#private".to_owned(),
                }],
                images: vec![DraftImageReference {
                    image_id: "image-1".to_owned(),
                    source_revision: image.source_revision,
                    operations: Vec::new(),
                    output_media_type: ImageMediaType::Png,
                }],
            },
        }
    }

    #[test]
    fn encrypts_originals_and_content_and_isolates_accounts() {
        let state = state("isolation");
        let composer = ComposerCore::default();
        let draft_id = Uuid::now_v7().to_string();
        let initial_request = request(&composer, &draft_id);
        let submission_idempotency_key = initial_request.submission_idempotency_key.clone();
        let summary = state
            .save(
                &access("account-a", true),
                &composer,
                initial_request.clone(),
            )
            .unwrap();
        assert_eq!(summary.revision, 1);
        let path = state
            .draft_path(&access("account-a", true).account_binding, &draft_id)
            .unwrap();
        let ciphertext = fs::read_to_string(path).unwrap();
        for sensitive in [
            "private title",
            "private body",
            "private environment",
            "token=private",
            "#private",
        ] {
            assert!(!ciphertext.contains(sensitive));
        }
        let restored_composer = ComposerCore::default();
        let loaded = state
            .load(
                &access("account-a", true),
                &restored_composer,
                &draft_id,
                "load-session",
            )
            .unwrap();
        assert_eq!(loaded.content.images.len(), 1);
        assert_eq!(
            loaded.submission_idempotency_key,
            submission_idempotency_key
        );
        assert_eq!(loaded.content.images[0].composer.width, 2);
        assert_eq!(loaded.content.images[0].composer.height, 1);
        assert_eq!(loaded.content.images[0].composer.preview_width, 2);
        assert_eq!(loaded.content.images[0].composer.preview_height, 1);
        assert!(!loaded.content.images[0].composer.image.bytes.is_empty());
        let restored = restored_composer
            .clone_original_for_draft(
                &ComposerSessionId("load-session".to_owned()),
                &ComposerImageId("image-1".to_owned()),
                loaded.content.images[0].composer.source_revision,
            )
            .unwrap();
        assert!(!restored.bytes.is_empty());
        let mut update = initial_request;
        update.composer_session_id = "load-session".to_owned();
        update.expected_revision = Some(loaded.revision);
        update.content.images[0].source_revision =
            loaded.content.images[0].composer.source_revision;
        update.submission_idempotency_key = Uuid::now_v7().to_string();
        assert_eq!(
            state
                .save(
                    &access("account-a", true),
                    &restored_composer,
                    update.clone()
                )
                .err(),
            Some(DraftError::Conflict)
        );
        update.submission_idempotency_key = submission_idempotency_key;
        assert_eq!(
            state
                .save(&access("account-a", true), &restored_composer, update)
                .unwrap()
                .revision,
            2
        );
        assert!(state.list(&access("account-b", true)).unwrap().is_empty());
        assert_eq!(
            state
                .load(
                    &access("account-b", true),
                    &ComposerCore::default(),
                    &draft_id,
                    "load-session"
                )
                .err(),
            Some(DraftError::AccountLocked)
        );
    }

    #[test]
    fn offline_access_can_edit_but_submission_requires_online_reauthentication() {
        let state = state("offline");
        let composer = ComposerCore::default();
        let draft_id = Uuid::now_v7().to_string();
        state
            .save(
                &access("account-a", false),
                &composer,
                request(&composer, &draft_id),
            )
            .unwrap();
        assert_eq!(
            state.assert_submission_allowed(&access("account-a", false), &draft_id, 1),
            Err(DraftError::ReauthenticationRequired)
        );
        assert!(
            state
                .assert_submission_allowed(&access("account-a", true), &draft_id, 1)
                .is_ok()
        );
    }

    #[test]
    fn rejects_corruption_future_versions_and_wrong_keys_without_exposing_values() {
        let record = StoredDraftRecord {
            version: RECORD_VERSION,
            draft_id: Uuid::now_v7().to_string(),
            revision: 1,
            submission_idempotency_key: Uuid::now_v7().to_string(),
            created_at_unix_ms: 1,
            updated_at_unix_ms: 1,
            content: StoredDraftContent {
                title: "secret".to_owned(),
                body: String::new(),
                preset_id: None,
                preset_revision: None,
                destination_id: None,
                repository_definition_id: None,
                environment: Vec::new(),
                url: None,
                dom: Vec::new(),
                images: Vec::new(),
            },
        };
        let binding = access("account-a", true).account_binding;
        let key = [7_u8; 32];
        let encrypted = encrypt_record(&record, &binding, &key).unwrap();
        assert_eq!(
            decrypt_record(&encrypted, &binding, &[8_u8; 32]).err(),
            Some(DraftError::AccountLocked)
        );
        let mut future: serde_json::Value = serde_json::from_slice(&encrypted).unwrap();
        future["version"] = serde_json::json!(2);
        assert_eq!(
            decrypt_record(&serde_json::to_vec(&future).unwrap(), &binding, &key).err(),
            Some(DraftError::FutureVersion)
        );
        assert_eq!(
            decrypt_record(b"{broken", &binding, &key).err(),
            Some(DraftError::Corrupt)
        );
    }

    #[test]
    fn optimistic_revision_serializes_concurrent_writes_and_delete_is_explicit() {
        let state = std::sync::Arc::new(state("concurrent"));
        let composer = ComposerCore::default();
        let draft_id = Uuid::now_v7().to_string();
        let initial_request = request(&composer, &draft_id);
        let submission_idempotency_key = initial_request.submission_idempotency_key.clone();
        state
            .save(&access("account-a", true), &composer, initial_request)
            .unwrap();
        let updates = (0..2)
            .map(|_| {
                let state = std::sync::Arc::clone(&state);
                let draft_id = draft_id.clone();
                let submission_idempotency_key = submission_idempotency_key.clone();
                std::thread::spawn(move || {
                    let composer = ComposerCore::default();
                    let mut request = request(&composer, &draft_id);
                    request.expected_revision = Some(1);
                    request.submission_idempotency_key = submission_idempotency_key;
                    state.save(&access("account-a", true), &composer, request)
                })
            })
            .collect::<Vec<_>>();
        let results = updates
            .into_iter()
            .map(|update| update.join().unwrap())
            .collect::<Vec<_>>();
        assert_eq!(results.iter().filter(|result| result.is_ok()).count(), 1);
        assert_eq!(
            results
                .iter()
                .filter(|result| matches!(result, Err(DraftError::Conflict)))
                .count(),
            1
        );
        state
            .delete(&access("account-a", true), &draft_id, 2)
            .unwrap();
        assert!(state.list(&access("account-a", true)).unwrap().is_empty());
    }

    #[test]
    fn reset_removes_drafts_device_keys_and_extension_pairing() {
        let state = state("reset");
        let composer = ComposerCore::default();
        let draft_id = Uuid::now_v7().to_string();
        state
            .save(
                &access("account-a", true),
                &composer,
                request(&composer, &draft_id),
            )
            .unwrap();
        state.preflight_reset().unwrap();
        state.reset().unwrap();
        assert!(state.list(&access("account-a", true)).unwrap().is_empty());
        let mut vault = state.vault.lock().unwrap();
        assert!(vault.load().unwrap().keys.is_empty());
        assert!(!vault.extension_pairing_present());
    }

    #[test]
    fn failed_staged_reset_cleanup_restores_the_managed_directory() {
        let directory = temporary_directory("reset-rollback");
        prepare_directory(&directory).unwrap();
        fs::write(directory.join("retained.rqadraft"), b"ciphertext").unwrap();
        let staged = directory.with_extension("reset-fixture");
        fs::rename(&directory, &staged).unwrap();

        assert_eq!(
            remove_staged_with_rollback(&directory, &staged, |_path| {
                Err(io::Error::other("fixture staged cleanup failure"))
            }),
            Err(DraftError::WriteFailed)
        );

        assert_eq!(
            fs::read(directory.join("retained.rqadraft")).unwrap(),
            b"ciphertext"
        );
        assert!(!staged.exists());
    }

    #[test]
    fn rejects_unstripped_url_query_and_fragment_at_the_native_boundary() {
        for value in [
            "https://example.com/report?token=private",
            "https://example.com/report#private",
        ] {
            assert_eq!(
                normalize_url(RestorableUrl {
                    value: value.to_owned(),
                    stripped_query: None,
                    stripped_fragment: None,
                    warning: None,
                })
                .err(),
                Some(DraftError::InvalidRecord)
            );
        }
    }

    #[test]
    fn failed_atomic_replacement_preserves_the_previous_valid_ciphertext() {
        let directory = temporary_directory("failed-write");
        fs::create_dir_all(&directory).unwrap();
        let destination = directory.join("draft.rqadraft");
        fs::write(&destination, b"previous-valid-ciphertext").unwrap();
        let result = write_atomically_with(
            &destination,
            b"incomplete-new-ciphertext",
            |_temporary, _destination| Err(io::Error::other("fixture replacement failure")),
        );
        assert!(result.is_err());
        assert_eq!(
            fs::read(&destination).unwrap(),
            b"previous-valid-ciphertext"
        );
        assert_eq!(fs::read_dir(&directory).unwrap().count(), 1);
    }
}
