use std::{
    collections::HashSet,
    fs,
    io::{Read, Seek, SeekFrom, Write},
    path::{Path, PathBuf},
    sync::{
        Arc, Mutex, MutexGuard, OnceLock,
        atomic::{AtomicBool, AtomicU64, Ordering},
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use aes_gcm::{
    Aes256Gcm, Nonce,
    aead::{Aead, KeyInit, Payload},
};
use image::{Rgba, RgbaImage, imageops};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use uuid::Uuid;
use zeroize::Zeroizing;

pub const MAX_IMAGES: usize = 10;
pub const MAX_PNG_BYTES: usize = 50 * 1024 * 1024;
pub const DEFAULT_DRAFT_QUOTA: u64 = 10 * 1024 * 1024 * 1024;
pub const DRAFT_RETENTION: Duration = Duration::from_secs(30 * 24 * 60 * 60);
const MAX_HISTORY_STATES: usize = 100;
const MAX_OUTPUT_DIMENSION: u32 = 4096;
const WINDOW_TARGETING_GRACE_TICKS: u64 = 20;
const ENCRYPTED_MAGIC: &[u8; 4] = b"RQA1";
const FLATTENED_BUNDLE_MAGIC: &[u8; 8] = b"RQAFB001";
const MAX_ENCRYPTED_PNG_BYTES: u64 = MAX_PNG_BYTES as u64 + 32;
const ANNOTATION_FONT_BYTES: &[u8] =
    include_bytes!("../assets/fonts/noto-sans-kr/NotoSansKR-VF.ttf");
static ANNOTATION_FONT: OnceLock<Option<fontdue::Font>> = OnceLock::new();

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Rect {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}

impl Rect {
    fn right(self) -> f64 {
        self.x + self.width
    }

    fn bottom(self) -> f64 {
        self.y + self.height
    }

    pub fn contains(self, point: Point) -> bool {
        point.x >= self.x && point.x < self.right() && point.y >= self.y && point.y < self.bottom()
    }

    fn intersection(self, other: Self) -> Option<Self> {
        let x = self.x.max(other.x);
        let y = self.y.max(other.y);
        let right = self.right().min(other.right());
        let bottom = self.bottom().min(other.bottom());
        (right > x && bottom > y).then_some(Self {
            x,
            y,
            width: right - x,
            height: bottom - y,
        })
    }

    fn valid(self) -> bool {
        self.x.is_finite()
            && self.y.is_finite()
            && self.width.is_finite()
            && self.height.is_finite()
            && self.width > 0.0
            && self.height > 0.0
    }
}

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Point {
    pub x: f64,
    pub y: f64,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DisplayDescriptor {
    pub id: String,
    pub name: String,
    pub logical_bounds: Rect,
    pub pixel_width: u32,
    pub pixel_height: u32,
    pub scale: f64,
    pub primary: bool,
}

#[derive(Clone, Debug)]
pub struct WindowDescriptor {
    pub id: String,
    pub bounds: Rect,
    pub focused: bool,
    pub minimized: bool,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum CaptureAction {
    Display,
    ActiveWindow,
    AllDisplays,
    Selection,
    Toolbar,
}

impl CaptureAction {
    pub fn from_action_id(value: &str) -> Result<Self, CaptureError> {
        match value {
            "realqa.capture.display" => Ok(Self::Display),
            "realqa.capture.active-window" => Ok(Self::ActiveWindow),
            "realqa.capture.all-displays" => Ok(Self::AllDisplays),
            "realqa.capture.selection" => Ok(Self::Selection),
            "realqa.capture.toolbar" => Ok(Self::Toolbar),
            _ => Err(CaptureError::InvalidArgument),
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CaptureOptions {
    #[serde(default)]
    pub include_pointer: bool,
    #[serde(default)]
    pub remove_shadow: bool,
    #[serde(default)]
    pub delay_seconds: u8,
    #[serde(default)]
    pub selection: Option<Rect>,
    #[serde(default)]
    pub selection_window: bool,
    #[serde(default)]
    pub append_to_draft_id: Option<Uuid>,
}

impl CaptureOptions {
    pub fn validate(&self) -> Result<(), CaptureError> {
        if !matches!(self.delay_seconds, 0 | 5 | 10)
            || self.selection.is_some_and(|rect| !rect.valid())
        {
            return Err(CaptureError::InvalidArgument);
        }
        Ok(())
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum CaptureError {
    InvalidArgument,
    PermissionDenied,
    ProtectedContent,
    TopologyChanged,
    NoDisplay,
    NoWindow,
    Cancelled,
    QuotaExhausted,
    ImageLimit,
    NotFound,
    RevisionConflict,
    StorageFailure,
    PlatformFailure,
}

impl CaptureError {
    pub const fn code(self) -> &'static str {
        match self {
            Self::InvalidArgument => "invalid-argument",
            Self::PermissionDenied => "permission-denied",
            Self::ProtectedContent => "protected-content",
            Self::TopologyChanged => "topology-changed",
            Self::NoDisplay => "no-display",
            Self::NoWindow => "no-window",
            Self::Cancelled => "cancelled",
            Self::QuotaExhausted => "quota-exhausted",
            Self::ImageLimit => "image-limit",
            Self::NotFound => "not-found",
            Self::RevisionConflict => "revision-conflict",
            Self::StorageFailure => "storage-failure",
            Self::PlatformFailure => "platform-failure",
        }
    }
}

pub trait CaptureAdapter: Send + Sync {
    fn platform(&self) -> &'static str;
    fn topology(&self) -> Result<Vec<DisplayDescriptor>, CaptureError>;
    fn pointer_position(&self) -> Result<Point, CaptureError>;
    fn windows(&self) -> Result<Vec<WindowDescriptor>, CaptureError>;
    fn capture_display(&self, id: &str) -> Result<RgbaImage, CaptureError>;
    fn capture_window(&self, id: &str) -> Result<RgbaImage, CaptureError>;
    fn shadow_removal_supported(&self) -> bool;
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DraftImageState {
    pub id: Uuid,
    pub width: u32,
    pub height: u32,
    pub removed: bool,
    pub crop: Option<Rect>,
    pub layers: Vec<EditorLayer>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(
    tag = "tool",
    rename_all = "kebab-case",
    rename_all_fields = "camelCase",
    deny_unknown_fields
)]
pub enum EditorLayer {
    Arrow {
        id: Uuid,
        start: Point,
        end: Point,
        color: String,
        width: u32,
    },
    Rectangle {
        id: Uuid,
        bounds: Rect,
        color: String,
        width: u32,
    },
    Drawing {
        id: Uuid,
        points: Vec<Point>,
        color: String,
        width: u32,
    },
    Text {
        id: Uuid,
        origin: Point,
        text: String,
        color: String,
        size: u32,
    },
    Blur {
        id: Uuid,
        bounds: Rect,
        radius: f32,
    },
    Redaction {
        id: Uuid,
        bounds: Rect,
    },
}

impl EditorLayer {
    fn id(&self) -> Uuid {
        match self {
            Self::Arrow { id, .. }
            | Self::Rectangle { id, .. }
            | Self::Drawing { id, .. }
            | Self::Text { id, .. }
            | Self::Blur { id, .. }
            | Self::Redaction { id, .. } => *id,
        }
    }

    fn valid(&self) -> bool {
        match self {
            Self::Arrow {
                start,
                end,
                color,
                width,
                ..
            } => {
                valid_point(*start)
                    && valid_point(*end)
                    && valid_color(color)
                    && (1..=64).contains(width)
            }
            Self::Rectangle {
                bounds,
                color,
                width,
                ..
            } => bounds.valid() && valid_color(color) && (1..=64).contains(width),
            Self::Drawing {
                points,
                color,
                width,
                ..
            } => {
                (2..=8192).contains(&points.len())
                    && points.iter().copied().all(valid_point)
                    && valid_color(color)
                    && (1..=64).contains(width)
            }
            Self::Text {
                origin,
                text,
                color,
                size,
                ..
            } => {
                valid_point(*origin)
                    && !text.is_empty()
                    && text.chars().count() <= 2048
                    && valid_color(color)
                    && (8..=256).contains(size)
            }
            Self::Blur { bounds, radius, .. } => {
                bounds.valid() && radius.is_finite() && (1.0..=64.0).contains(radius)
            }
            Self::Redaction { bounds, .. } => bounds.valid(),
        }
    }

    fn valid_for_image(&self, width: u32, height: u32) -> bool {
        let point = |point: Point| {
            valid_point(point)
                && point.x >= 0.0
                && point.y >= 0.0
                && point.x <= f64::from(width)
                && point.y <= f64::from(height)
        };
        let bounds = |bounds: Rect| {
            bounds.valid()
                && bounds.x >= 0.0
                && bounds.y >= 0.0
                && bounds.right() <= f64::from(width)
                && bounds.bottom() <= f64::from(height)
        };
        match self {
            Self::Arrow { start, end, .. } => point(*start) && point(*end),
            Self::Rectangle {
                bounds: rectangle, ..
            }
            | Self::Blur {
                bounds: rectangle, ..
            }
            | Self::Redaction {
                bounds: rectangle, ..
            } => bounds(*rectangle),
            Self::Drawing { points, .. } => points.iter().copied().all(point),
            Self::Text { origin, .. } => point(*origin),
        }
    }
}

fn valid_point(point: Point) -> bool {
    point.x.is_finite() && point.y.is_finite()
}

fn valid_color(value: &str) -> bool {
    value.len() == 7
        && value.starts_with('#')
        && value[1..].bytes().all(|byte| byte.is_ascii_hexdigit())
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(
    tag = "kind",
    rename_all = "kebab-case",
    rename_all_fields = "camelCase",
    deny_unknown_fields
)]
pub enum EditorCommand {
    SetCrop {
        image_id: Uuid,
        crop: Option<Rect>,
    },
    AddLayer {
        image_id: Uuid,
        layer: EditorLayer,
    },
    RemoveLayer {
        image_id: Uuid,
        layer_id: Uuid,
    },
    MoveLayer {
        image_id: Uuid,
        layer_id: Uuid,
        to_index: usize,
    },
    RemoveImage {
        image_id: Uuid,
    },
    MoveImage {
        image_id: Uuid,
        to_index: usize,
    },
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
struct DraftState {
    images: Vec<DraftImageState>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
struct DraftDocument {
    schema_version: u32,
    id: Uuid,
    revision: u64,
    created_at: u64,
    updated_at: u64,
    expires_at: u64,
    current: DraftState,
    undo: Vec<DraftState>,
    redo: Vec<DraftState>,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DraftSummary {
    pub id: Uuid,
    pub revision: u64,
    pub created_at: u64,
    pub updated_at: u64,
    pub expires_at: u64,
    pub image_count: usize,
    pub images: Vec<DraftImageSummary>,
    pub can_undo: bool,
    pub can_redo: bool,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DraftImageSummary {
    pub id: Uuid,
    pub width: u32,
    pub height: u32,
    pub preview_url: String,
    pub layers: Vec<EditorLayer>,
    pub crop: Option<Rect>,
}

pub struct DraftList {
    pub drafts: Vec<DraftSummary>,
    pub unreadable_draft_ids: Vec<Uuid>,
}

impl DraftDocument {
    fn summary(&self) -> DraftSummary {
        let images = self
            .current
            .images
            .iter()
            .filter(|image| !image.removed)
            .map(|image| DraftImageSummary {
                id: image.id,
                width: image.width,
                height: image.height,
                preview_url: format!(
                    "realqa://asset/{}/{}/source/{}",
                    self.id, image.id, self.revision
                ),
                layers: image.layers.clone(),
                crop: image.crop,
            })
            .collect::<Vec<_>>();
        DraftSummary {
            id: self.id,
            revision: self.revision,
            created_at: self.created_at,
            updated_at: self.updated_at,
            expires_at: self.expires_at,
            image_count: images.len(),
            images,
            can_undo: !self.undo.is_empty(),
            can_redo: !self.redo.is_empty(),
        }
    }

    fn touch(&mut self, now: u64) {
        self.revision += 1;
        self.updated_at = now;
        self.expires_at = now + DRAFT_RETENTION.as_secs();
    }
}

fn trim_history(document: &mut DraftDocument) {
    while document.undo.len() + document.redo.len() > MAX_HISTORY_STATES {
        discard_farthest_history(document);
    }
}

fn discard_farthest_history(document: &mut DraftDocument) -> bool {
    let history = if document.undo.len() >= document.redo.len() {
        &mut document.undo
    } else {
        &mut document.redo
    };
    if history.is_empty() {
        return false;
    }
    history.remove(0);
    true
}

fn unreferenced_source_payloads(
    directory: &Path,
    document: &DraftDocument,
) -> Result<Vec<PathBuf>, CaptureError> {
    let referenced = document
        .undo
        .iter()
        .chain(std::iter::once(&document.current))
        .chain(document.redo.iter())
        .flat_map(|state| {
            state
                .images
                .iter()
                .filter(|image| !image.removed)
                .map(|image| image.id)
        })
        .collect::<HashSet<_>>();
    let mut unreferenced = Vec::new();
    for child in fs::read_dir(directory).map_err(|_| CaptureError::StorageFailure)? {
        let child = child.map_err(|_| CaptureError::StorageFailure)?;
        let name = child.file_name().to_string_lossy().into_owned();
        let source_id = name
            .strip_prefix("source-")
            .and_then(|name| name.strip_suffix(".bin"))
            .and_then(|name| Uuid::parse_str(name).ok());
        if source_id.is_some_and(|image_id| !referenced.contains(&image_id)) {
            unreferenced.push(child.path());
        }
    }
    Ok(unreferenced)
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct FlattenedImage {
    pub image_id: Uuid,
    pub width: u32,
    pub height: u32,
    pub bytes: usize,
    pub sha256: String,
    pub asset_url: String,
    pub downscaled: bool,
}

pub struct DraftStore {
    root: PathBuf,
    quota: u64,
    key_override: Option<[u8; 32]>,
    lock: Mutex<()>,
}

impl DraftStore {
    pub fn new_default() -> Result<Self, CaptureError> {
        let root = dirs::data_local_dir()
            .ok_or(CaptureError::StorageFailure)?
            .join("io.delino.devhud")
            .join("realqa-drafts-v1");
        Ok(Self::new(root, DEFAULT_DRAFT_QUOTA))
    }

    pub fn new(root: PathBuf, quota: u64) -> Self {
        Self {
            root,
            quota,
            key_override: None,
            lock: Mutex::new(()),
        }
    }

    #[cfg(test)]
    fn new_test(root: PathBuf, quota: u64, key: [u8; 32]) -> Self {
        Self {
            root,
            quota,
            key_override: Some(key),
            lock: Mutex::new(()),
        }
    }

    fn now() -> Result<u64, CaptureError> {
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|value| value.as_secs())
            .map_err(|_| CaptureError::StorageFailure)
    }

    fn key(&self) -> Result<Zeroizing<[u8; 32]>, CaptureError> {
        if let Some(key) = self.key_override {
            return Ok(Zeroizing::new(key));
        }
        crate::secure_store::realqa_draft_key()
            .map(Zeroizing::new)
            .map_err(|_| CaptureError::StorageFailure)
    }

    pub fn recover(&self) -> Result<(), CaptureError> {
        let _guard = self.lock.lock().map_err(|_| CaptureError::StorageFailure)?;
        let key = self.key()?;
        self.recover_locked(&key)
    }

    fn recover_locked(&self, key: &[u8; 32]) -> Result<(), CaptureError> {
        fs::create_dir_all(&self.root).map_err(|_| CaptureError::StorageFailure)?;
        let now = Self::now()?;
        for entry in fs::read_dir(&self.root).map_err(|_| CaptureError::StorageFailure)? {
            let entry = entry.map_err(|_| CaptureError::StorageFailure)?;
            let name = entry.file_name();
            let name = name.to_string_lossy();
            if name.starts_with(".txn-") || name.ends_with(".tmp") {
                remove_path(entry.path())?;
                continue;
            }
            if !entry.path().is_dir() {
                continue;
            }
            for child in fs::read_dir(entry.path()).map_err(|_| CaptureError::StorageFailure)? {
                let child = child.map_err(|_| CaptureError::StorageFailure)?;
                if child
                    .path()
                    .extension()
                    .is_some_and(|extension| extension == "tmp")
                {
                    remove_path(child.path())?;
                }
            }
            let manifest = entry.path().join("manifest.bin");
            let Ok(document) = read_document(&manifest, key) else {
                // An unreadable unexpired draft is retained for explicit user
                // recovery/deletion; startup never guesses that it is safe to evict.
                continue;
            };
            if document.expires_at <= now {
                remove_path(entry.path())?;
                continue;
            }
            let referenced = document
                .undo
                .iter()
                .chain(std::iter::once(&document.current))
                .chain(document.redo.iter())
                .flat_map(|state| {
                    state
                        .images
                        .iter()
                        .filter(|image| !image.removed)
                        .map(|image| image.id)
                })
                .collect::<HashSet<_>>();
            for child in fs::read_dir(entry.path()).map_err(|_| CaptureError::StorageFailure)? {
                let child = child.map_err(|_| CaptureError::StorageFailure)?;
                let name = child.file_name().to_string_lossy().into_owned();
                let source_id = name
                    .strip_prefix("source-")
                    .and_then(|name| name.strip_suffix(".bin"))
                    .and_then(|name| Uuid::parse_str(name).ok());
                let flattened_id = name
                    .strip_prefix("flattened-")
                    .and_then(|name| name.strip_suffix(".bin"));
                let stale_flattened = flattened_id.is_some_and(|name| {
                    name.parse::<u64>()
                        .map(|revision| revision != document.revision)
                        .unwrap_or_else(|_| Uuid::parse_str(name).is_ok())
                });
                if source_id.is_some_and(|image_id| !referenced.contains(&image_id))
                    || stale_flattened
                {
                    remove_path(child.path())?;
                }
            }
        }
        Ok(())
    }

    pub fn list(&self) -> Result<DraftList, CaptureError> {
        let _guard = self.lock.lock().map_err(|_| CaptureError::StorageFailure)?;
        let key = self.key()?;
        self.recover_locked(&key)?;
        let mut drafts = Vec::new();
        let mut unreadable_draft_ids = Vec::new();
        for entry in fs::read_dir(&self.root).map_err(|_| CaptureError::StorageFailure)? {
            let entry = entry.map_err(|_| CaptureError::StorageFailure)?;
            if !entry.path().is_dir() || entry.file_name().to_string_lossy().starts_with('.') {
                continue;
            }
            let Some(id) = entry
                .file_name()
                .to_str()
                .and_then(|name| Uuid::parse_str(name).ok())
            else {
                continue;
            };
            match read_document(&entry.path().join("manifest.bin"), &key) {
                Ok(document) => drafts.push(document.summary()),
                Err(_) => unreadable_draft_ids.push(id),
            }
        }
        drafts.sort_by_key(|draft| std::cmp::Reverse(draft.updated_at));
        unreadable_draft_ids.sort_unstable();
        Ok(DraftList {
            drafts,
            unreadable_draft_ids,
        })
    }

    pub fn open(&self, id: Uuid) -> Result<DraftSummary, CaptureError> {
        let _guard = self.lock.lock().map_err(|_| CaptureError::StorageFailure)?;
        let key = self.key()?;
        Ok(self.read_locked(id, &key)?.summary())
    }

    #[cfg(test)]
    pub fn create(&self, images: Vec<RgbaImage>) -> Result<DraftSummary, CaptureError> {
        self.create_with_commit_check(images, || Ok(()))
    }

    fn create_with_commit_check(
        &self,
        images: Vec<RgbaImage>,
        before_commit: impl FnOnce() -> Result<(), CaptureError>,
    ) -> Result<DraftSummary, CaptureError> {
        if images.is_empty() || images.len() > MAX_IMAGES {
            return Err(CaptureError::ImageLimit);
        }
        let _guard = self.lock.lock().map_err(|_| CaptureError::StorageFailure)?;
        let key = self.key()?;
        self.recover_locked(&key)?;
        let id = Uuid::now_v7();
        let now = Self::now()?;
        let states = images
            .iter()
            .map(|image| DraftImageState {
                id: Uuid::now_v7(),
                width: image.width(),
                height: image.height(),
                removed: false,
                crop: None,
                layers: Vec::new(),
            })
            .collect::<Vec<_>>();
        let document = DraftDocument {
            schema_version: 1,
            id,
            revision: 1,
            created_at: now,
            updated_at: now,
            expires_at: now + DRAFT_RETENTION.as_secs(),
            current: DraftState { images: states },
            undo: Vec::new(),
            redo: Vec::new(),
        };
        let transaction = self.root.join(format!(".txn-{id}"));
        fs::create_dir(&transaction).map_err(|_| CaptureError::StorageFailure)?;
        let result = (|| {
            for (state, image) in document.current.images.iter().zip(images) {
                let png = encode_srgb_png(&image)?;
                let encrypted = encrypt(&key, id, &format!("source:{}", state.id), &png)?;
                write_new_file(
                    &transaction.join(format!("source-{}.bin", state.id)),
                    &encrypted,
                )?;
            }
            let manifest = encrypt_document(&document, &key)?;
            write_new_file(&transaction.join("manifest.bin"), &manifest)?;
            // The transaction directory is already a child of the quota root.
            let projected = directory_size(&self.root)?;
            if projected > self.quota {
                return Err(CaptureError::QuotaExhausted);
            }
            before_commit()?;
            fs::rename(&transaction, self.root.join(id.to_string()))
                .map_err(|_| CaptureError::StorageFailure)?;
            Ok(document.summary())
        })();
        if result.is_err() {
            let _ = remove_path(transaction);
        }
        result
    }

    #[cfg(test)]
    pub fn append(&self, id: Uuid, images: Vec<RgbaImage>) -> Result<DraftSummary, CaptureError> {
        self.append_with_commit_check(id, images, || Ok(()))
    }

    fn append_with_commit_check(
        &self,
        id: Uuid,
        images: Vec<RgbaImage>,
        before_commit: impl FnOnce() -> Result<(), CaptureError>,
    ) -> Result<DraftSummary, CaptureError> {
        let _guard = self.lock.lock().map_err(|_| CaptureError::StorageFailure)?;
        let key = self.key()?;
        self.recover_locked(&key)?;
        let mut document = self.read_locked(id, &key)?;
        let active = document
            .current
            .images
            .iter()
            .filter(|image| !image.removed)
            .count();
        if images.is_empty() || active + images.len() > MAX_IMAGES {
            return Err(CaptureError::ImageLimit);
        }
        let previous = document.current.clone();
        let directory = self.root.join(id.to_string());
        let mut written = Vec::new();
        let result = (|| {
            for image in images {
                let image_id = Uuid::now_v7();
                let png = encode_srgb_png(&image)?;
                let encrypted = encrypt(&key, id, &format!("source:{image_id}"), &png)?;
                let path = directory.join(format!("source-{image_id}.bin"));
                write_new_file(&path, &encrypted)?;
                written.push(path);
                document.current.images.push(DraftImageState {
                    id: image_id,
                    width: image.width(),
                    height: image.height(),
                    removed: false,
                    crop: None,
                    layers: Vec::new(),
                });
            }
            document.undo.push(previous);
            document.redo.clear();
            document.touch(Self::now()?);
            self.write_document_locked_with_commit_check(&mut document, &key, before_commit)?;
            Ok(document.summary())
        })();
        if result.is_err() {
            for path in written {
                let _ = fs::remove_file(path);
            }
        }
        result
    }

    pub fn apply(
        &self,
        id: Uuid,
        expected_revision: u64,
        command: EditorCommand,
    ) -> Result<DraftSummary, CaptureError> {
        let _guard = self.lock.lock().map_err(|_| CaptureError::StorageFailure)?;
        let key = self.key()?;
        self.recover_locked(&key)?;
        let mut document = self.read_locked(id, &key)?;
        ensure_revision(&document, expected_revision)?;
        let previous = document.current.clone();
        apply_command(&mut document.current, command)?;
        document.undo.push(previous);
        document.redo.clear();
        document.touch(Self::now()?);
        self.write_document_locked(&mut document, &key)?;
        Ok(document.summary())
    }

    pub fn undo(&self, id: Uuid, expected_revision: u64) -> Result<DraftSummary, CaptureError> {
        self.history(id, expected_revision, true)
    }

    pub fn redo(&self, id: Uuid, expected_revision: u64) -> Result<DraftSummary, CaptureError> {
        self.history(id, expected_revision, false)
    }

    fn history(
        &self,
        id: Uuid,
        expected_revision: u64,
        undo: bool,
    ) -> Result<DraftSummary, CaptureError> {
        let _guard = self.lock.lock().map_err(|_| CaptureError::StorageFailure)?;
        let key = self.key()?;
        let mut document = self.read_locked(id, &key)?;
        ensure_revision(&document, expected_revision)?;
        let replacement = if undo {
            document.undo.pop()
        } else {
            document.redo.pop()
        }
        .ok_or(CaptureError::InvalidArgument)?;
        let current = std::mem::replace(&mut document.current, replacement);
        if undo {
            document.redo.push(current);
        } else {
            document.undo.push(current);
        }
        document.touch(Self::now()?);
        self.write_document_locked(&mut document, &key)?;
        Ok(document.summary())
    }

    pub fn flatten(
        &self,
        id: Uuid,
        expected_revision: u64,
    ) -> Result<Vec<FlattenedImage>, CaptureError> {
        self.flatten_with_commit(id, expected_revision, replace_staged_file)
    }

    fn flatten_with_commit(
        &self,
        id: Uuid,
        expected_revision: u64,
        commit: impl FnOnce(&Path, &Path) -> Result<(), CaptureError>,
    ) -> Result<Vec<FlattenedImage>, CaptureError> {
        let _guard = self.lock.lock().map_err(|_| CaptureError::StorageFailure)?;
        let key = self.key()?;
        self.recover_locked(&key)?;
        let document = self.read_locked(id, &key)?;
        ensure_revision(&document, expected_revision)?;
        let directory = self.root.join(id.to_string());
        let current = directory_size(&self.root)?;
        let path = directory.join(format!("flattened-{}.bin", document.revision));
        let temporary = path.with_extension("tmp");
        if temporary.exists() {
            remove_path(&temporary)?;
        }
        let result = (|| {
            let active = document
                .current
                .images
                .iter()
                .filter(|image| !image.removed)
                .collect::<Vec<_>>();
            let mut file = fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(&temporary)
                .map_err(|_| CaptureError::StorageFailure)?;
            file.write_all(FLATTENED_BUNDLE_MAGIC)
                .and_then(|()| file.write_all(&(active.len() as u32).to_le_bytes()))
                .map_err(|_| CaptureError::StorageFailure)?;
            let mut outputs = Vec::with_capacity(active.len());
            for state in active {
                let encrypted = fs::read(directory.join(format!("source-{}.bin", state.id)))
                    .map_err(|_| CaptureError::StorageFailure)?;
                let png = decrypt(&key, id, &format!("source:{}", state.id), &encrypted)?;
                let source = image::load_from_memory_with_format(&png, image::ImageFormat::Png)
                    .map_err(|_| CaptureError::StorageFailure)?
                    .into_rgba8();
                let rendered = render_editor(source, state)?;
                let (rendered, mut downscaled) = fit_dimensions(rendered);
                let mut encoded = encode_srgb_png(&rendered)?;
                let mut final_image = rendered;
                while encoded.len() > MAX_PNG_BYTES {
                    downscaled = true;
                    let ratio = ((MAX_PNG_BYTES as f64 / encoded.len() as f64).sqrt() * 0.98)
                        .clamp(0.5, 0.98);
                    let width = ((final_image.width() as f64 * ratio).floor() as u32).max(1);
                    let height = ((final_image.height() as f64 * ratio).floor() as u32).max(1);
                    final_image = imageops::resize(
                        &final_image,
                        width,
                        height,
                        imageops::FilterType::Lanczos3,
                    );
                    encoded = encode_srgb_png(&final_image)?;
                }
                let checksum = hex_digest(Sha256::digest(&encoded));
                let encrypted = encrypt(
                    &key,
                    id,
                    &format!("flattened:{}:{}", document.revision, state.id),
                    &encoded,
                )?;
                file.write_all(state.id.as_bytes())
                    .and_then(|()| file.write_all(&(encrypted.len() as u64).to_le_bytes()))
                    .and_then(|()| file.write_all(&encrypted))
                    .map_err(|_| CaptureError::StorageFailure)?;
                outputs.push(FlattenedImage {
                    image_id: state.id,
                    width: final_image.width(),
                    height: final_image.height(),
                    bytes: encoded.len(),
                    sha256: checksum,
                    asset_url: format!(
                        "realqa://asset/{}/{}/flattened/{}",
                        id, state.id, document.revision
                    ),
                    downscaled,
                });
            }
            file.sync_all().map_err(|_| CaptureError::StorageFailure)?;
            drop(file);
            let replaced = fs::metadata(&path).map(|value| value.len()).unwrap_or(0);
            let added = fs::metadata(&temporary)
                .map_err(|_| CaptureError::StorageFailure)?
                .len();
            if current.saturating_sub(replaced).saturating_add(added) > self.quota {
                return Err(CaptureError::QuotaExhausted);
            }
            commit(&temporary, &path)?;
            if let Ok(stale_payloads) = flattened_payloads(&directory) {
                for stale in stale_payloads {
                    if stale != path {
                        let _ = remove_path(stale);
                    }
                }
            }
            Ok(outputs)
        })();
        if result.is_err() {
            let _ = remove_path(temporary);
        }
        result
    }

    pub fn asset(
        &self,
        id: Uuid,
        image_id: Uuid,
        flattened: bool,
        revision: u64,
    ) -> Result<Vec<u8>, CaptureError> {
        let _guard = self.lock.lock().map_err(|_| CaptureError::StorageFailure)?;
        let key = self.key()?;
        let document = self.read_locked(id, &key)?;
        ensure_revision(&document, revision)?;
        if !document
            .current
            .images
            .iter()
            .any(|image| image.id == image_id)
        {
            return Err(CaptureError::NotFound);
        }
        if flattened {
            let path = self
                .root
                .join(id.to_string())
                .join(format!("flattened-{revision}.bin"));
            let encrypted = read_flattened_bundle_entry(&path, image_id)?;
            decrypt(
                &key,
                id,
                &format!("flattened:{revision}:{image_id}"),
                &encrypted,
            )
        } else {
            let bytes = fs::read(
                self.root
                    .join(id.to_string())
                    .join(format!("source-{image_id}.bin")),
            )
            .map_err(|_| CaptureError::NotFound)?;
            decrypt(&key, id, &format!("source:{image_id}"), &bytes)
        }
    }

    pub fn delete(&self, id: Uuid) -> Result<(), CaptureError> {
        let _guard = self.lock.lock().map_err(|_| CaptureError::StorageFailure)?;
        let path = self.root.join(id.to_string());
        if !path.exists() {
            return Err(CaptureError::NotFound);
        }
        remove_path(path)
    }

    pub fn purge_all(&self) -> Result<(), CaptureError> {
        let _guard = self.lock.lock().map_err(|_| CaptureError::StorageFailure)?;
        if self.root.exists() {
            remove_path(&self.root)?;
        }
        Ok(())
    }

    fn read_locked(&self, id: Uuid, key: &[u8; 32]) -> Result<DraftDocument, CaptureError> {
        let directory = self.root.join(id.to_string());
        let document = read_document(&directory.join("manifest.bin"), key)?;
        if document.expires_at <= Self::now()? {
            remove_path(directory)?;
            Err(CaptureError::NotFound)
        } else {
            Ok(document)
        }
    }

    fn write_document_locked(
        &self,
        document: &mut DraftDocument,
        key: &[u8; 32],
    ) -> Result<(), CaptureError> {
        self.write_document_locked_with_commit_check(document, key, || Ok(()))
    }

    fn write_document_locked_with_commit_check(
        &self,
        document: &mut DraftDocument,
        key: &[u8; 32],
        before_commit: impl FnOnce() -> Result<(), CaptureError>,
    ) -> Result<(), CaptureError> {
        self.write_document_locked_with_replacer(document, key, before_commit, atomic_replace)
    }

    fn write_document_locked_with_replacer(
        &self,
        document: &mut DraftDocument,
        key: &[u8; 32],
        before_commit: impl FnOnce() -> Result<(), CaptureError>,
        replace_manifest: impl FnOnce(&Path, &[u8]) -> Result<(), CaptureError>,
    ) -> Result<(), CaptureError> {
        let current = directory_size(&self.root)?;
        let directory = self.root.join(document.id.to_string());
        let manifest = directory.join("manifest.bin");
        let old = fs::metadata(&manifest)
            .map(|value| value.len())
            .unwrap_or(0);
        let stale_flattened = flattened_payloads(&directory)?;
        let stale_flattened_bytes = stale_flattened
            .iter()
            .map(|path| fs::metadata(path).map(|value| value.len()).unwrap_or(0))
            .sum::<u64>();
        trim_history(document);
        loop {
            let encrypted = encrypt_document(document, key)?;
            let stale_sources = unreferenced_source_payloads(&directory, document)?;
            let stale_source_bytes = stale_sources
                .iter()
                .map(|path| fs::metadata(path).map(|value| value.len()).unwrap_or(0))
                .sum::<u64>();
            if current
                .saturating_sub(old)
                .saturating_sub(stale_flattened_bytes)
                .saturating_sub(stale_source_bytes)
                .saturating_add(encrypted.len() as u64)
                <= self.quota
            {
                before_commit()?;
                replace_manifest(&manifest, &encrypted)?;
                for path in stale_flattened {
                    let _ = remove_path(path);
                }
                for path in stale_sources {
                    let _ = remove_path(path);
                }
                return Ok(());
            }
            if !discard_farthest_history(document) {
                return Err(CaptureError::QuotaExhausted);
            }
        }
    }
}

pub struct CaptureService {
    adapter: Arc<dyn CaptureAdapter>,
    store: Arc<DraftStore>,
    topology: Mutex<Vec<DisplayDescriptor>>,
    lifecycle: Mutex<()>,
    purge_lock: Mutex<()>,
    purging: AtomicBool,
    cancellation_epoch: AtomicU64,
}

pub struct CapturePurgeGuard<'a> {
    service: &'a CaptureService,
    purge: Option<MutexGuard<'a, ()>>,
    lifecycle: Option<MutexGuard<'a, ()>>,
}

impl CapturePurgeGuard<'_> {
    pub fn purge_drafts(&self) -> Result<(), CaptureError> {
        self.service.store.purge_all()
    }
}

impl Drop for CapturePurgeGuard<'_> {
    fn drop(&mut self) {
        self.lifecycle.take();
        self.service.purging.store(false, Ordering::Release);
        self.purge.take();
    }
}

impl CaptureService {
    pub fn new(adapter: Arc<dyn CaptureAdapter>, store: Arc<DraftStore>) -> Self {
        Self {
            adapter,
            store,
            topology: Mutex::new(Vec::new()),
            lifecycle: Mutex::new(()),
            purge_lock: Mutex::new(()),
            purging: AtomicBool::new(false),
            cancellation_epoch: AtomicU64::new(0),
        }
    }

    pub fn platform_default() -> Result<Self, CaptureError> {
        Ok(Self::new(
            Arc::new(platform_adapter()),
            Arc::new(DraftStore::new_default()?),
        ))
    }

    pub fn adapter_platform(&self) -> &'static str {
        self.adapter.platform()
    }

    pub fn shadow_removal_supported(&self) -> bool {
        self.adapter.shadow_removal_supported()
    }

    pub fn topology(&self) -> Result<Vec<DisplayDescriptor>, CaptureError> {
        let topology = self.adapter.topology()?;
        *self
            .topology
            .lock()
            .map_err(|_| CaptureError::PlatformFailure)? = topology.clone();
        Ok(topology)
    }

    #[cfg(test)]
    pub fn capture(
        &self,
        action: CaptureAction,
        options: CaptureOptions,
    ) -> Result<DraftSummary, CaptureError> {
        let epoch = self.begin_capture()?;
        self.capture_with_epoch_after_delay(action, options, epoch, || Ok(()))
    }

    pub fn begin_capture(&self) -> Result<u64, CaptureError> {
        if self.purging.load(Ordering::Acquire) {
            return Err(CaptureError::Cancelled);
        }
        let epoch = self.cancellation_epoch.load(Ordering::Acquire);
        if self.purging.load(Ordering::Acquire) {
            Err(CaptureError::Cancelled)
        } else {
            Ok(epoch)
        }
    }

    pub fn cancel(&self) {
        self.cancellation_epoch.fetch_add(1, Ordering::AcqRel);
    }

    pub fn capture_with_epoch_after_delay(
        &self,
        action: CaptureAction,
        options: CaptureOptions,
        epoch: u64,
        after_delay: impl FnOnce() -> Result<(), CaptureError>,
    ) -> Result<DraftSummary, CaptureError> {
        let _lifecycle = self
            .lifecycle
            .lock()
            .map_err(|_| CaptureError::PlatformFailure)?;
        if self.purging.load(Ordering::Acquire) {
            return Err(CaptureError::Cancelled);
        }
        self.ensure_not_cancelled(epoch)?;
        options.validate()?;
        self.wait_cancellable(epoch, u64::from(options.delay_seconds) * 10)?;
        self.ensure_not_cancelled(epoch)?;
        after_delay()?;
        self.ensure_not_cancelled(epoch)?;
        if matches!(action, CaptureAction::Selection | CaptureAction::Toolbar)
            && options.selection_window
        {
            self.wait_cancellable(epoch, WINDOW_TARGETING_GRACE_TICKS)?;
        }
        match self.capture_attempt(action, &options, epoch) {
            Err(CaptureError::TopologyChanged) => {
                self.ensure_not_cancelled(epoch)?;
                self.capture_attempt(action, &options, epoch)
            }
            result => result,
        }
    }

    fn capture_attempt(
        &self,
        action: CaptureAction,
        options: &CaptureOptions,
        epoch: u64,
    ) -> Result<DraftSummary, CaptureError> {
        let before = self.topology()?;
        let captured = (|| {
            let pointer_required = matches!(action, CaptureAction::Display)
                || (matches!(action, CaptureAction::Selection | CaptureAction::Toolbar)
                    && options.selection_window)
                || options.include_pointer;
            let pointer = if pointer_required {
                Some(normalize_native_pointer(
                    &before,
                    self.adapter.pointer_position()?,
                )?)
            } else {
                None
            };
            let mut pointer_bounds = None;
            let images = match action {
                CaptureAction::Display => {
                    let display =
                        display_for_pointer(&before, pointer.ok_or(CaptureError::NoDisplay)?)
                            .ok_or(CaptureError::NoDisplay)?;
                    vec![self.adapter.capture_display(&display.id)?]
                }
                CaptureAction::ActiveWindow => {
                    let window = self
                        .adapter
                        .windows()?
                        .into_iter()
                        .find(|window| window.focused && !window.minimized)
                        .ok_or(CaptureError::NoWindow)?;
                    let (image, captured_bounds) =
                        self.capture_window(&window, options.remove_shadow)?;
                    if pointer.is_some_and(|pointer| {
                        options.include_pointer && captured_bounds.contains(pointer)
                    }) {
                        pointer_bounds = Some(captured_bounds);
                    }
                    vec![image]
                }
                CaptureAction::AllDisplays => {
                    if before.len() > MAX_IMAGES {
                        return Err(CaptureError::ImageLimit);
                    }
                    let mut displays = before.clone();
                    displays.sort_by(|a, b| {
                        a.logical_bounds
                            .y
                            .total_cmp(&b.logical_bounds.y)
                            .then(a.logical_bounds.x.total_cmp(&b.logical_bounds.x))
                            .then(a.id.cmp(&b.id))
                    });
                    displays
                        .iter()
                        .map(|display| self.adapter.capture_display(&display.id))
                        .collect::<Result<Vec<_>, _>>()?
                }
                CaptureAction::Selection | CaptureAction::Toolbar if options.selection_window => {
                    let pointer = pointer.ok_or(CaptureError::NoWindow)?;
                    let window = self
                        .adapter
                        .windows()?
                        .into_iter()
                        // Native adapters preserve front-to-back z-order.
                        .find(|window| !window.minimized && window.bounds.contains(pointer))
                        .ok_or(CaptureError::NoWindow)?;
                    let (image, captured_bounds) =
                        self.capture_window(&window, options.remove_shadow)?;
                    if options.include_pointer && captured_bounds.contains(pointer) {
                        pointer_bounds = Some(captured_bounds);
                    }
                    vec![image]
                }
                CaptureAction::Selection | CaptureAction::Toolbar => {
                    let selection = options.selection.ok_or(CaptureError::InvalidArgument)?;
                    let (image, captured_bounds) =
                        capture_region(self.adapter.as_ref(), &before, selection)?;
                    if pointer.is_some_and(|pointer| {
                        options.include_pointer && captured_bounds.contains(pointer)
                    }) {
                        pointer_bounds = Some(captured_bounds);
                    }
                    vec![image]
                }
            };
            Ok((images, pointer, pointer_bounds))
        })();
        let (mut images, pointer, pointer_bounds) =
            match captured {
                Ok(captured) => captured,
                Err(error) => {
                    if self.adapter.topology().is_ok_and(|after| {
                        topology_signature(&before) != topology_signature(&after)
                    }) {
                        return Err(CaptureError::TopologyChanged);
                    }
                    return Err(error);
                }
            };
        let after = self.adapter.topology()?;
        self.ensure_not_cancelled(epoch)?;
        if topology_signature(&before) != topology_signature(&after) {
            return Err(CaptureError::TopologyChanged);
        }
        if images.iter().any(protected_frame) {
            return Err(CaptureError::ProtectedContent);
        }
        if let (Some(bounds), Some(pointer)) = (pointer_bounds, pointer) {
            draw_pointer_in_rect(&mut images[0], pointer, bounds);
        }
        if options.include_pointer
            && let Some(pointer) = pointer
        {
            apply_pointer(&before, pointer, &mut images, action);
        }
        self.persist_capture(options, images, epoch)
    }

    fn persist_capture(
        &self,
        options: &CaptureOptions,
        images: Vec<RgbaImage>,
        epoch: u64,
    ) -> Result<DraftSummary, CaptureError> {
        self.ensure_not_cancelled(epoch)?;
        match options.append_to_draft_id {
            Some(id) => self
                .store
                .append_with_commit_check(id, images, || self.ensure_not_cancelled(epoch)),
            None => self
                .store
                .create_with_commit_check(images, || self.ensure_not_cancelled(epoch)),
        }
    }

    fn ensure_not_cancelled(&self, epoch: u64) -> Result<(), CaptureError> {
        if self.cancellation_epoch.load(Ordering::Acquire) == epoch {
            Ok(())
        } else {
            Err(CaptureError::Cancelled)
        }
    }

    fn wait_cancellable(&self, epoch: u64, ticks: u64) -> Result<(), CaptureError> {
        for _ in 0..ticks {
            std::thread::sleep(Duration::from_millis(100));
            self.ensure_not_cancelled(epoch)?;
        }
        Ok(())
    }

    fn capture_window(
        &self,
        window: &WindowDescriptor,
        remove_shadow: bool,
    ) -> Result<(RgbaImage, Rect), CaptureError> {
        let image = self.adapter.capture_window(&window.id)?;
        if remove_shadow && self.adapter.shadow_removal_supported() {
            Ok(trim_transparent_border(image, window.bounds))
        } else {
            Ok((image, window.bounds))
        }
    }

    pub fn store(&self) -> &DraftStore {
        &self.store
    }

    pub fn begin_purge(&self) -> Result<CapturePurgeGuard<'_>, CaptureError> {
        let purge = self
            .purge_lock
            .lock()
            .map_err(|_| CaptureError::StorageFailure)?;
        self.purging.store(true, Ordering::Release);
        self.cancel();
        let lifecycle = match self.lifecycle.lock() {
            Ok(lifecycle) => lifecycle,
            Err(_) => {
                self.purging.store(false, Ordering::Release);
                return Err(CaptureError::StorageFailure);
            }
        };
        Ok(CapturePurgeGuard {
            service: self,
            purge: Some(purge),
            lifecycle: Some(lifecycle),
        })
    }
}

fn display_for_pointer(displays: &[DisplayDescriptor], point: Point) -> Option<&DisplayDescriptor> {
    displays
        .iter()
        .find(|display| display.logical_bounds.contains(point))
        .or_else(|| {
            displays.iter().min_by(|a, b| {
                distance_to_rect(a.logical_bounds, point)
                    .total_cmp(&distance_to_rect(b.logical_bounds, point))
            })
        })
}

fn normalize_native_pointer(
    _displays: &[DisplayDescriptor],
    pointer: Point,
) -> Result<Point, CaptureError> {
    Ok(pointer)
}

fn distance_to_rect(rect: Rect, point: Point) -> f64 {
    let dx = if point.x < rect.x {
        rect.x - point.x
    } else if point.x > rect.right() {
        point.x - rect.right()
    } else {
        0.0
    };
    let dy = if point.y < rect.y {
        rect.y - point.y
    } else if point.y > rect.bottom() {
        point.y - rect.bottom()
    } else {
        0.0
    };
    dx * dx + dy * dy
}

fn topology_signature(
    displays: &[DisplayDescriptor],
) -> Vec<(String, i64, i64, i64, i64, u32, u32)> {
    displays
        .iter()
        .map(|display| {
            (
                display.id.clone(),
                (display.logical_bounds.x * 1000.0) as i64,
                (display.logical_bounds.y * 1000.0) as i64,
                (display.logical_bounds.width * 1000.0) as i64,
                (display.logical_bounds.height * 1000.0) as i64,
                display.pixel_width,
                display.pixel_height,
            )
        })
        .collect()
}

fn protected_frame(image: &RgbaImage) -> bool {
    image.width() == 0 || image.height() == 0 || image.pixels().all(|pixel| pixel.0[3] == 0)
}

fn protected_window_frame(platform: &str, image: &RgbaImage) -> bool {
    protected_frame(image)
        || (matches!(platform, "macos" | "windows")
            && image.pixels().all(|pixel| pixel.0 == [0, 0, 0, 255]))
}

fn capture_region(
    adapter: &dyn CaptureAdapter,
    displays: &[DisplayDescriptor],
    selection: Rect,
) -> Result<(RgbaImage, Rect), CaptureError> {
    let desktop_bounds = virtual_desktop_bounds(displays)?;
    let selection = selection
        .intersection(desktop_bounds)
        .ok_or(CaptureError::NoDisplay)?;
    let intersections = displays
        .iter()
        .filter_map(|display| {
            display
                .logical_bounds
                .intersection(selection)
                .map(|intersection| (display, intersection))
        })
        .collect::<Vec<_>>();
    if intersections.is_empty() {
        return Err(CaptureError::NoDisplay);
    }
    let scale = intersections
        .iter()
        .map(|(display, _)| {
            (f64::from(display.pixel_width) / display.logical_bounds.width)
                .max(f64::from(display.pixel_height) / display.logical_bounds.height)
        })
        .fold(1.0, f64::max);
    let width = checked_pixel_dimension(selection.width, scale)?;
    let height = checked_pixel_dimension(selection.height, scale)?;
    let mut output = new_transparent_image(width, height)?;
    for (display, intersection) in intersections {
        let source = adapter.capture_display(&display.id)?;
        if protected_frame(&source) {
            return Err(CaptureError::ProtectedContent);
        }
        let scale_x = f64::from(source.width()) / display.logical_bounds.width;
        let scale_y = f64::from(source.height()) / display.logical_bounds.height;
        let source_x = rounded_pixel_edge(
            (intersection.x - display.logical_bounds.x) * scale_x,
            source.width(),
        );
        let source_y = rounded_pixel_edge(
            (intersection.y - display.logical_bounds.y) * scale_y,
            source.height(),
        );
        let source_right = rounded_pixel_edge(
            (intersection.right() - display.logical_bounds.x) * scale_x,
            source.width(),
        );
        let source_bottom = rounded_pixel_edge(
            (intersection.bottom() - display.logical_bounds.y) * scale_y,
            source.height(),
        );
        let Some((source_x, source_right)) =
            non_empty_pixel_span(source_x, source_right, source.width())
        else {
            continue;
        };
        let Some((source_y, source_bottom)) =
            non_empty_pixel_span(source_y, source_bottom, source.height())
        else {
            continue;
        };
        let piece = imageops::crop_imm(
            &source,
            source_x,
            source_y,
            source_right - source_x,
            source_bottom - source_y,
        )
        .to_image();
        let target_x = rounded_pixel_edge((intersection.x - selection.x) * scale, width);
        let target_y = rounded_pixel_edge((intersection.y - selection.y) * scale, height);
        let target_right = if intersection.right() >= selection.right() {
            width
        } else {
            rounded_pixel_edge((intersection.right() - selection.x) * scale, width)
        };
        let target_bottom = if intersection.bottom() >= selection.bottom() {
            height
        } else {
            rounded_pixel_edge((intersection.bottom() - selection.y) * scale, height)
        };
        let Some((target_x, target_right)) = non_empty_pixel_span(target_x, target_right, width)
        else {
            continue;
        };
        let Some((target_y, target_bottom)) = non_empty_pixel_span(target_y, target_bottom, height)
        else {
            continue;
        };
        let target_width = target_right - target_x;
        let target_height = target_bottom - target_y;
        let piece = if piece.width() == target_width && piece.height() == target_height {
            piece
        } else {
            imageops::resize(
                &piece,
                target_width,
                target_height,
                imageops::FilterType::Lanczos3,
            )
        };
        imageops::overlay(&mut output, &piece, target_x.into(), target_y.into());
    }
    Ok((output, selection))
}

fn rounded_pixel_edge(value: f64, limit: u32) -> u32 {
    value.round().clamp(0.0, f64::from(limit)) as u32
}

fn non_empty_pixel_span(mut start: u32, mut end: u32, limit: u32) -> Option<(u32, u32)> {
    if limit == 0 {
        return None;
    }
    if end <= start {
        if start < limit {
            end = start + 1;
        } else {
            start = limit - 1;
            end = limit;
        }
    }
    Some((start, end))
}

fn virtual_desktop_bounds(displays: &[DisplayDescriptor]) -> Result<Rect, CaptureError> {
    let mut displays = displays.iter();
    let first = displays.next().ok_or(CaptureError::NoDisplay)?;
    if !first.logical_bounds.valid() || first.pixel_width == 0 || first.pixel_height == 0 {
        return Err(CaptureError::PlatformFailure);
    }
    let mut left = first.logical_bounds.x;
    let mut top = first.logical_bounds.y;
    let mut right = first.logical_bounds.right();
    let mut bottom = first.logical_bounds.bottom();
    for display in displays {
        if !display.logical_bounds.valid() || display.pixel_width == 0 || display.pixel_height == 0
        {
            return Err(CaptureError::PlatformFailure);
        }
        left = left.min(display.logical_bounds.x);
        top = top.min(display.logical_bounds.y);
        right = right.max(display.logical_bounds.right());
        bottom = bottom.max(display.logical_bounds.bottom());
    }
    let bounds = Rect {
        x: left,
        y: top,
        width: right - left,
        height: bottom - top,
    };
    bounds
        .valid()
        .then_some(bounds)
        .ok_or(CaptureError::PlatformFailure)
}

fn checked_pixel_dimension(logical: f64, scale: f64) -> Result<u32, CaptureError> {
    let pixels = (logical * scale).ceil().max(1.0);
    if !pixels.is_finite() || pixels > f64::from(u32::MAX) {
        Err(CaptureError::InvalidArgument)
    } else {
        Ok(pixels as u32)
    }
}

fn new_transparent_image(width: u32, height: u32) -> Result<RgbaImage, CaptureError> {
    let length = (width as usize)
        .checked_mul(height as usize)
        .and_then(|pixels| pixels.checked_mul(4))
        .ok_or(CaptureError::InvalidArgument)?;
    let mut pixels = Vec::new();
    pixels
        .try_reserve_exact(length)
        .map_err(|_| CaptureError::InvalidArgument)?;
    pixels.resize(length, 0);
    RgbaImage::from_raw(width, height, pixels).ok_or(CaptureError::InvalidArgument)
}

fn apply_pointer(
    displays: &[DisplayDescriptor],
    pointer: Point,
    images: &mut [RgbaImage],
    action: CaptureAction,
) {
    let Some(display_index) = displays
        .iter()
        .position(|display| display.logical_bounds.contains(pointer))
    else {
        return;
    };
    match action {
        CaptureAction::AllDisplays => {
            let mut ordered = displays.iter().collect::<Vec<_>>();
            ordered.sort_by(|a, b| {
                a.logical_bounds
                    .y
                    .total_cmp(&b.logical_bounds.y)
                    .then(a.logical_bounds.x.total_cmp(&b.logical_bounds.x))
                    .then(a.id.cmp(&b.id))
            });
            let Some((image_index, display)) = ordered
                .iter()
                .enumerate()
                .find(|(_, display)| display.logical_bounds.contains(pointer))
            else {
                return;
            };
            if image_index >= images.len() {
                return;
            }
            let x = ((pointer.x - display.logical_bounds.x)
                * f64::from(images[image_index].width())
                / display.logical_bounds.width) as i32;
            let y = ((pointer.y - display.logical_bounds.y)
                * f64::from(images[image_index].height())
                / display.logical_bounds.height) as i32;
            draw_pointer(&mut images[image_index], x, y);
        }
        CaptureAction::Display if !images.is_empty() => {
            let display =
                display_for_pointer(displays, pointer).unwrap_or(&displays[display_index]);
            let x = ((pointer.x - display.logical_bounds.x) * f64::from(images[0].width())
                / display.logical_bounds.width) as i32;
            let y = ((pointer.y - display.logical_bounds.y) * f64::from(images[0].height())
                / display.logical_bounds.height) as i32;
            draw_pointer(&mut images[0], x, y);
        }
        _ => {}
    }
}

fn draw_pointer(image: &mut RgbaImage, x: i32, y: i32) {
    for row in 0..18_i32 {
        for column in 0..=(row / 2).min(7) {
            let border = column == 0 || column == (row / 2).min(7) || row == 17;
            put_pixel_checked(
                image,
                x + column,
                y + row,
                if border {
                    Rgba([0, 0, 0, 255])
                } else {
                    Rgba([255, 255, 255, 255])
                },
            );
        }
    }
}

fn draw_pointer_in_rect(image: &mut RgbaImage, pointer: Point, bounds: Rect) {
    let x = ((pointer.x - bounds.x) * image.width() as f64 / bounds.width).round() as i32;
    let y = ((pointer.y - bounds.y) * image.height() as f64 / bounds.height).round() as i32;
    draw_pointer(image, x, y);
}

fn trim_transparent_border(image: RgbaImage, bounds: Rect) -> (RgbaImage, Rect) {
    let mut left = image.width();
    let mut top = image.height();
    let mut right = 0;
    let mut bottom = 0;
    for (x, y, pixel) in image.enumerate_pixels() {
        if pixel.0[3] != 0 {
            left = left.min(x);
            top = top.min(y);
            right = right.max(x + 1);
            bottom = bottom.max(y + 1);
        }
    }
    if right <= left || bottom <= top {
        (image, bounds)
    } else {
        let pixel_width = f64::from(image.width());
        let pixel_height = f64::from(image.height());
        let adjusted_bounds = Rect {
            x: bounds.x + f64::from(left) * bounds.width / pixel_width,
            y: bounds.y + f64::from(top) * bounds.height / pixel_height,
            width: f64::from(right - left) * bounds.width / pixel_width,
            height: f64::from(bottom - top) * bounds.height / pixel_height,
        };
        (
            imageops::crop_imm(&image, left, top, right - left, bottom - top).to_image(),
            adjusted_bounds,
        )
    }
}

fn apply_command(state: &mut DraftState, command: EditorCommand) -> Result<(), CaptureError> {
    match command {
        EditorCommand::SetCrop { image_id, crop } => {
            if crop.is_some_and(|rect| !rect.valid()) {
                return Err(CaptureError::InvalidArgument);
            }
            let image = image_mut(state, image_id)?;
            if crop.is_some_and(|rect| {
                rect.x < 0.0
                    || rect.y < 0.0
                    || rect.right() > f64::from(image.width)
                    || rect.bottom() > f64::from(image.height)
            }) {
                return Err(CaptureError::InvalidArgument);
            }
            image.crop = crop;
        }
        EditorCommand::AddLayer { image_id, layer } => {
            if !layer.valid() {
                return Err(CaptureError::InvalidArgument);
            }
            let image = image_mut(state, image_id)?;
            if !layer.valid_for_image(image.width, image.height) {
                return Err(CaptureError::InvalidArgument);
            }
            if image
                .layers
                .iter()
                .any(|candidate| candidate.id() == layer.id())
            {
                return Err(CaptureError::InvalidArgument);
            }
            image.layers.push(layer);
        }
        EditorCommand::RemoveLayer { image_id, layer_id } => {
            let image = image_mut(state, image_id)?;
            let index = image
                .layers
                .iter()
                .position(|layer| layer.id() == layer_id)
                .ok_or(CaptureError::NotFound)?;
            image.layers.remove(index);
        }
        EditorCommand::MoveLayer {
            image_id,
            layer_id,
            to_index,
        } => {
            let image = image_mut(state, image_id)?;
            let index = image
                .layers
                .iter()
                .position(|layer| layer.id() == layer_id)
                .ok_or(CaptureError::NotFound)?;
            if to_index >= image.layers.len() {
                return Err(CaptureError::InvalidArgument);
            }
            let layer = image.layers.remove(index);
            image.layers.insert(to_index, layer);
        }
        EditorCommand::RemoveImage { image_id } => {
            let image = image_mut(state, image_id)?;
            if image.removed {
                return Err(CaptureError::NotFound);
            }
            image.removed = true;
            if state.images.iter().all(|candidate| candidate.removed) {
                return Err(CaptureError::InvalidArgument);
            }
        }
        EditorCommand::MoveImage { image_id, to_index } => {
            let index = state
                .images
                .iter()
                .position(|image| image.id == image_id && !image.removed)
                .ok_or(CaptureError::NotFound)?;
            let active_count = state.images.iter().filter(|image| !image.removed).count();
            if to_index >= active_count {
                return Err(CaptureError::InvalidArgument);
            }
            let image = state.images.remove(index);
            let active_positions = state
                .images
                .iter()
                .enumerate()
                .filter_map(|(index, image)| (!image.removed).then_some(index))
                .collect::<Vec<_>>();
            let insertion = active_positions
                .get(to_index)
                .copied()
                .or_else(|| active_positions.last().map(|index| index + 1))
                .unwrap_or(0);
            state.images.insert(insertion, image);
        }
    }
    Ok(())
}

fn image_mut(state: &mut DraftState, id: Uuid) -> Result<&mut DraftImageState, CaptureError> {
    state
        .images
        .iter_mut()
        .find(|image| image.id == id)
        .ok_or(CaptureError::NotFound)
}

fn ensure_revision(document: &DraftDocument, expected: u64) -> Result<(), CaptureError> {
    if document.revision == expected {
        Ok(())
    } else {
        Err(CaptureError::RevisionConflict)
    }
}

fn render_editor(mut image: RgbaImage, state: &DraftImageState) -> Result<RgbaImage, CaptureError> {
    for layer in &state.layers {
        render_layer(&mut image, layer)?;
    }
    if let Some(crop) = state.crop {
        let x = crop.x.round().max(0.0) as u32;
        let y = crop.y.round().max(0.0) as u32;
        let width = crop.width.round().max(1.0) as u32;
        let height = crop.height.round().max(1.0) as u32;
        if x >= image.width() || y >= image.height() {
            return Err(CaptureError::InvalidArgument);
        }
        image = imageops::crop_imm(
            &image,
            x,
            y,
            width.min(image.width() - x),
            height.min(image.height() - y),
        )
        .to_image();
    }
    Ok(image)
}

fn render_layer(image: &mut RgbaImage, layer: &EditorLayer) -> Result<(), CaptureError> {
    match layer {
        EditorLayer::Arrow {
            start,
            end,
            color,
            width,
            ..
        } => {
            let color = parse_color(color)?;
            draw_thick_line(image, *start, *end, color, *width);
            let angle = (end.y - start.y).atan2(end.x - start.x);
            let length = (*width as f64 * 4.0).max(12.0);
            for offset in [-0.65, 0.65] {
                let tip = Point {
                    x: end.x - length * (angle + offset).cos(),
                    y: end.y - length * (angle + offset).sin(),
                };
                draw_thick_line(image, *end, tip, color, *width);
            }
        }
        EditorLayer::Rectangle {
            bounds,
            color,
            width,
            ..
        } => {
            let color = parse_color(color)?;
            let a = Point {
                x: bounds.x,
                y: bounds.y,
            };
            let b = Point {
                x: bounds.right(),
                y: bounds.y,
            };
            let c = Point {
                x: bounds.right(),
                y: bounds.bottom(),
            };
            let d = Point {
                x: bounds.x,
                y: bounds.bottom(),
            };
            for (start, end) in [(a, b), (b, c), (c, d), (d, a)] {
                draw_thick_line(image, start, end, color, *width);
            }
        }
        EditorLayer::Drawing {
            points,
            color,
            width,
            ..
        } => {
            let color = parse_color(color)?;
            for pair in points.windows(2) {
                draw_thick_line(image, pair[0], pair[1], color, *width);
            }
        }
        EditorLayer::Text {
            origin,
            text,
            color,
            size,
            ..
        } => render_text(image, *origin, text, parse_color(color)?, *size)?,
        EditorLayer::Blur { bounds, radius, .. } => {
            let (x, y, width, height) = clipped_bounds(image, *bounds)?;
            let region = imageops::crop_imm(image, x, y, width, height).to_image();
            let blurred = imageops::blur(&region, *radius);
            imageops::overlay(image, &blurred, x.into(), y.into());
        }
        EditorLayer::Redaction { bounds, .. } => {
            let (x, y, width, height) = clipped_bounds(image, *bounds)?;
            for py in y..y + height {
                for px in x..x + width {
                    image.put_pixel(px, py, Rgba([0, 0, 0, 255]));
                }
            }
        }
    }
    Ok(())
}

fn clipped_bounds(image: &RgbaImage, bounds: Rect) -> Result<(u32, u32, u32, u32), CaptureError> {
    let x = bounds.x.round().max(0.0) as u32;
    let y = bounds.y.round().max(0.0) as u32;
    if x >= image.width() || y >= image.height() {
        return Err(CaptureError::InvalidArgument);
    }
    Ok((
        x,
        y,
        (bounds.width.round().max(1.0) as u32).min(image.width() - x),
        (bounds.height.round().max(1.0) as u32).min(image.height() - y),
    ))
}

fn render_text(
    image: &mut RgbaImage,
    origin: Point,
    text: &str,
    color: Rgba<u8>,
    size: u32,
) -> Result<(), CaptureError> {
    let font = annotation_font()?;
    let mut pen_x = origin.x.round() as i32;
    let baseline = origin.y.round() as i32;
    for character in text.chars() {
        let (metrics, bitmap) = font.rasterize(character, size as f32);
        for row in 0..metrics.height {
            for column in 0..metrics.width {
                let alpha = bitmap[row * metrics.width + column];
                if alpha == 0 {
                    continue;
                }
                let mut blended = color;
                blended.0[3] = alpha;
                put_pixel_alpha(
                    image,
                    pen_x + metrics.xmin + column as i32,
                    baseline - metrics.ymin - metrics.height as i32 + row as i32,
                    blended,
                );
            }
        }
        pen_x += metrics.advance_width.round() as i32;
    }
    Ok(())
}

fn annotation_font() -> Result<&'static fontdue::Font, CaptureError> {
    ANNOTATION_FONT
        .get_or_init(|| {
            fontdue::Font::from_bytes(ANNOTATION_FONT_BYTES, fontdue::FontSettings::default()).ok()
        })
        .as_ref()
        .ok_or(CaptureError::PlatformFailure)
}

fn draw_thick_line(image: &mut RgbaImage, start: Point, end: Point, color: Rgba<u8>, width: u32) {
    let diameter = width.max(1) as i32;
    let radius = diameter / 2;
    let Some((start, end)) = clip_line_to_image(image, start, end, radius) else {
        return;
    };
    let first_offset = -radius;
    let last_offset = first_offset + diameter - 1;
    let center_offset = if diameter % 2 == 0 { 0.5 } else { 0.0 };
    let brush_radius = if diameter % 2 == 0 {
        f64::from(diameter) / 2.0
    } else {
        f64::from(radius)
    };
    let dx = end.x - start.x;
    let dy = end.y - start.y;
    let steps = dx.abs().max(dy.abs()).ceil().max(1.0) as i32;
    for step in 0..=steps {
        let ratio = step as f64 / steps as f64;
        let x = (start.x + dx * ratio).round() as i32;
        let y = (start.y + dy * ratio).round() as i32;
        for py in y + first_offset..=y + last_offset {
            for px in x + first_offset..=x + last_offset {
                let brush_x = f64::from(px - x) + center_offset;
                let brush_y = f64::from(py - y) + center_offset;
                if brush_x * brush_x + brush_y * brush_y <= brush_radius * brush_radius {
                    put_pixel_alpha(image, px, py, color);
                }
            }
        }
    }
}

fn clip_line_to_image(
    image: &RgbaImage,
    start: Point,
    end: Point,
    radius: i32,
) -> Option<(Point, Point)> {
    if !valid_point(start) || !valid_point(end) || image.width() == 0 || image.height() == 0 {
        return None;
    }
    let radius = f64::from(radius);
    let minimum_x = -radius;
    let minimum_y = -radius;
    let maximum_x = f64::from(image.width().saturating_sub(1)) + radius;
    let maximum_y = f64::from(image.height().saturating_sub(1)) + radius;
    let dx = end.x - start.x;
    let dy = end.y - start.y;
    let mut lower = 0.0_f64;
    let mut upper = 1.0_f64;
    for (direction, distance) in [
        (-dx, start.x - minimum_x),
        (dx, maximum_x - start.x),
        (-dy, start.y - minimum_y),
        (dy, maximum_y - start.y),
    ] {
        if direction == 0.0 {
            if distance < 0.0 {
                return None;
            }
            continue;
        }
        let ratio = distance / direction;
        if direction < 0.0 {
            if ratio > upper {
                return None;
            }
            lower = lower.max(ratio);
        } else {
            if ratio < lower {
                return None;
            }
            upper = upper.min(ratio);
        }
    }
    Some((
        Point {
            x: start.x + lower * dx,
            y: start.y + lower * dy,
        },
        Point {
            x: start.x + upper * dx,
            y: start.y + upper * dy,
        },
    ))
}

fn parse_color(value: &str) -> Result<Rgba<u8>, CaptureError> {
    if !valid_color(value) {
        return Err(CaptureError::InvalidArgument);
    }
    Ok(Rgba([
        u8::from_str_radix(&value[1..3], 16).map_err(|_| CaptureError::InvalidArgument)?,
        u8::from_str_radix(&value[3..5], 16).map_err(|_| CaptureError::InvalidArgument)?,
        u8::from_str_radix(&value[5..7], 16).map_err(|_| CaptureError::InvalidArgument)?,
        255,
    ]))
}

fn put_pixel_checked(image: &mut RgbaImage, x: i32, y: i32, color: Rgba<u8>) {
    if x >= 0 && y >= 0 && (x as u32) < image.width() && (y as u32) < image.height() {
        image.put_pixel(x as u32, y as u32, color);
    }
}

fn put_pixel_alpha(image: &mut RgbaImage, x: i32, y: i32, foreground: Rgba<u8>) {
    if x < 0 || y < 0 || (x as u32) >= image.width() || (y as u32) >= image.height() {
        return;
    }
    let background = *image.get_pixel(x as u32, y as u32);
    let alpha = foreground.0[3] as u16;
    let inverse = 255 - alpha;
    image.put_pixel(
        x as u32,
        y as u32,
        Rgba([
            ((foreground.0[0] as u16 * alpha + background.0[0] as u16 * inverse) / 255) as u8,
            ((foreground.0[1] as u16 * alpha + background.0[1] as u16 * inverse) / 255) as u8,
            ((foreground.0[2] as u16 * alpha + background.0[2] as u16 * inverse) / 255) as u8,
            255,
        ]),
    );
}

fn fit_dimensions(image: RgbaImage) -> (RgbaImage, bool) {
    let scale = (MAX_OUTPUT_DIMENSION as f64 / image.width() as f64)
        .min(MAX_OUTPUT_DIMENSION as f64 / image.height() as f64)
        .min(1.0);
    if scale >= 1.0 {
        (image, false)
    } else {
        let width = (image.width() as f64 * scale).floor().max(1.0) as u32;
        let height = (image.height() as f64 * scale).floor().max(1.0) as u32;
        (
            imageops::resize(&image, width, height, imageops::FilterType::Lanczos3),
            true,
        )
    }
}

pub fn encode_srgb_png(image: &RgbaImage) -> Result<Vec<u8>, CaptureError> {
    let mut output = Vec::new();
    {
        let mut encoder = png::Encoder::new(&mut output, image.width(), image.height());
        encoder.set_color(png::ColorType::Rgba);
        encoder.set_depth(png::BitDepth::Eight);
        encoder.set_source_srgb(png::SrgbRenderingIntent::Perceptual);
        let mut writer = encoder
            .write_header()
            .map_err(|_| CaptureError::StorageFailure)?;
        writer
            .write_image_data(image.as_raw())
            .map_err(|_| CaptureError::StorageFailure)?;
    }
    Ok(output)
}

fn encrypt_document(document: &DraftDocument, key: &[u8; 32]) -> Result<Vec<u8>, CaptureError> {
    let json = serde_json::to_vec(document).map_err(|_| CaptureError::StorageFailure)?;
    encrypt(key, document.id, "manifest", &json)
}

fn read_document(path: &Path, key: &[u8; 32]) -> Result<DraftDocument, CaptureError> {
    let encrypted = fs::read(path).map_err(|_| CaptureError::NotFound)?;
    let id = path
        .parent()
        .and_then(Path::file_name)
        .and_then(|name| name.to_str())
        .and_then(|name| Uuid::parse_str(name).ok())
        .ok_or(CaptureError::StorageFailure)?;
    let json = decrypt(key, id, "manifest", &encrypted)?;
    let document: DraftDocument =
        serde_json::from_slice(&json).map_err(|_| CaptureError::StorageFailure)?;
    if document.schema_version != 1 || document.id != id {
        return Err(CaptureError::StorageFailure);
    }
    Ok(document)
}

fn encrypt(
    key: &[u8; 32],
    draft_id: Uuid,
    role: &str,
    plaintext: &[u8],
) -> Result<Vec<u8>, CaptureError> {
    let cipher = Aes256Gcm::new_from_slice(key).map_err(|_| CaptureError::StorageFailure)?;
    let nonce_bytes = rand::random::<[u8; 12]>();
    let aad = format!("devhud-realqa-v1:{draft_id}:{role}");
    let ciphertext = cipher
        .encrypt(
            Nonce::from_slice(&nonce_bytes),
            Payload {
                msg: plaintext,
                aad: aad.as_bytes(),
            },
        )
        .map_err(|_| CaptureError::StorageFailure)?;
    let mut output = Vec::with_capacity(4 + 12 + ciphertext.len());
    output.extend_from_slice(ENCRYPTED_MAGIC);
    output.extend_from_slice(&nonce_bytes);
    output.extend_from_slice(&ciphertext);
    Ok(output)
}

fn decrypt(
    key: &[u8; 32],
    draft_id: Uuid,
    role: &str,
    encrypted: &[u8],
) -> Result<Vec<u8>, CaptureError> {
    if encrypted.len() < 32 || &encrypted[..4] != ENCRYPTED_MAGIC {
        return Err(CaptureError::StorageFailure);
    }
    let cipher = Aes256Gcm::new_from_slice(key).map_err(|_| CaptureError::StorageFailure)?;
    let aad = format!("devhud-realqa-v1:{draft_id}:{role}");
    cipher
        .decrypt(
            Nonce::from_slice(&encrypted[4..16]),
            Payload {
                msg: &encrypted[16..],
                aad: aad.as_bytes(),
            },
        )
        .map_err(|_| CaptureError::StorageFailure)
}

fn write_new_file(path: &Path, bytes: &[u8]) -> Result<(), CaptureError> {
    let mut file = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path)
        .map_err(|_| CaptureError::StorageFailure)?;
    file.write_all(bytes)
        .and_then(|()| file.sync_all())
        .map_err(|_| CaptureError::StorageFailure)
}

fn atomic_replace(path: &Path, bytes: &[u8]) -> Result<(), CaptureError> {
    let temporary = path.with_extension("tmp");
    if temporary.exists() {
        fs::remove_file(&temporary).map_err(|_| CaptureError::StorageFailure)?;
    }
    write_new_file(&temporary, bytes)?;
    replace_staged_file(&temporary, path)
}

fn replace_staged_file(temporary: &Path, path: &Path) -> Result<(), CaptureError> {
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::ffi::OsStrExt;
        #[link(name = "kernel32")]
        unsafe extern "system" {
            fn MoveFileExW(existing: *const u16, replacement: *const u16, flags: u32) -> i32;
        }
        const MOVEFILE_REPLACE_EXISTING: u32 = 0x1;
        const MOVEFILE_WRITE_THROUGH: u32 = 0x8;
        let existing = temporary
            .as_os_str()
            .encode_wide()
            .chain(std::iter::once(0))
            .collect::<Vec<_>>();
        let replacement = path
            .as_os_str()
            .encode_wide()
            .chain(std::iter::once(0))
            .collect::<Vec<_>>();
        if unsafe {
            MoveFileExW(
                existing.as_ptr(),
                replacement.as_ptr(),
                MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH,
            )
        } == 0
        {
            return Err(CaptureError::StorageFailure);
        }
        return Ok(());
    }
    #[cfg(not(target_os = "windows"))]
    {
        fs::rename(temporary, path).map_err(|_| CaptureError::StorageFailure)
    }
}

fn read_flattened_bundle_entry(path: &Path, image_id: Uuid) -> Result<Vec<u8>, CaptureError> {
    let mut file = fs::File::open(path).map_err(|_| CaptureError::NotFound)?;
    let mut magic = [0_u8; FLATTENED_BUNDLE_MAGIC.len()];
    file.read_exact(&mut magic)
        .map_err(|_| CaptureError::StorageFailure)?;
    if &magic != FLATTENED_BUNDLE_MAGIC {
        return Err(CaptureError::StorageFailure);
    }
    let mut count = [0_u8; 4];
    file.read_exact(&mut count)
        .map_err(|_| CaptureError::StorageFailure)?;
    let count = u32::from_le_bytes(count) as usize;
    if count == 0 || count > MAX_IMAGES {
        return Err(CaptureError::StorageFailure);
    }
    for _ in 0..count {
        let mut id = [0_u8; 16];
        let mut length = [0_u8; 8];
        file.read_exact(&mut id)
            .and_then(|()| file.read_exact(&mut length))
            .map_err(|_| CaptureError::StorageFailure)?;
        let length = u64::from_le_bytes(length);
        if !(32..=MAX_ENCRYPTED_PNG_BYTES).contains(&length) {
            return Err(CaptureError::StorageFailure);
        }
        if Uuid::from_bytes(id) == image_id {
            let mut encrypted = vec![0_u8; length as usize];
            file.read_exact(&mut encrypted)
                .map_err(|_| CaptureError::StorageFailure)?;
            return Ok(encrypted);
        }
        file.seek(SeekFrom::Current(length as i64))
            .map_err(|_| CaptureError::StorageFailure)?;
    }
    Err(CaptureError::NotFound)
}

fn directory_size(path: &Path) -> Result<u64, CaptureError> {
    if !path.exists() {
        return Ok(0);
    }
    let metadata = fs::metadata(path).map_err(|_| CaptureError::StorageFailure)?;
    if metadata.is_file() {
        return Ok(metadata.len());
    }
    let mut total = 0_u64;
    for entry in fs::read_dir(path).map_err(|_| CaptureError::StorageFailure)? {
        total = total.saturating_add(directory_size(
            &entry.map_err(|_| CaptureError::StorageFailure)?.path(),
        )?);
    }
    Ok(total)
}

fn flattened_payloads(directory: &Path) -> Result<Vec<PathBuf>, CaptureError> {
    let mut payloads = Vec::new();
    for entry in fs::read_dir(directory).map_err(|_| CaptureError::StorageFailure)? {
        let entry = entry.map_err(|_| CaptureError::StorageFailure)?;
        let name = entry.file_name();
        let name = name.to_string_lossy();
        let flattened_id = name
            .strip_prefix("flattened-")
            .and_then(|name| name.strip_suffix(".bin"));
        if flattened_id
            .is_some_and(|name| Uuid::parse_str(name).is_ok() || name.parse::<u64>().is_ok())
        {
            payloads.push(entry.path());
        }
    }
    Ok(payloads)
}

fn remove_path(path: impl AsRef<Path>) -> Result<(), CaptureError> {
    let path = path.as_ref();
    if !path.exists() {
        return Ok(());
    }
    if path.is_dir() {
        fs::remove_dir_all(path)
    } else {
        fs::remove_file(path)
    }
    .map_err(|_| CaptureError::StorageFailure)
}

fn hex_digest(digest: impl AsRef<[u8]>) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut result = String::with_capacity(digest.as_ref().len() * 2);
    for byte in digest.as_ref() {
        result.push(HEX[(byte >> 4) as usize] as char);
        result.push(HEX[(byte & 15) as usize] as char);
    }
    result
}

struct XCapAdapter {
    platform: &'static str,
    shadow: bool,
}

#[cfg(target_os = "macos")]
pub struct MacOsCaptureAdapter(XCapAdapter);
#[cfg(target_os = "windows")]
pub struct WindowsCaptureAdapter(XCapAdapter);
#[cfg(target_os = "linux")]
pub struct X11CaptureAdapter(XCapAdapter);

macro_rules! impl_adapter {
    ($name:ty) => {
        impl CaptureAdapter for $name {
            fn platform(&self) -> &'static str {
                self.0.platform
            }

            fn topology(&self) -> Result<Vec<DisplayDescriptor>, CaptureError> {
                xcap_topology()
            }

            fn pointer_position(&self) -> Result<Point, CaptureError> {
                native_pointer_position()
            }

            fn windows(&self) -> Result<Vec<WindowDescriptor>, CaptureError> {
                xcap_windows()
            }

            fn capture_display(&self, id: &str) -> Result<RgbaImage, CaptureError> {
                xcap_capture_display(id)
            }

            fn capture_window(&self, id: &str) -> Result<RgbaImage, CaptureError> {
                let image = xcap_capture_window(id)?;
                if protected_window_frame(self.0.platform, &image) {
                    Err(CaptureError::ProtectedContent)
                } else {
                    Ok(image)
                }
            }

            fn shadow_removal_supported(&self) -> bool {
                self.0.shadow
            }
        }
    };
}

#[cfg(target_os = "macos")]
impl_adapter!(MacOsCaptureAdapter);
#[cfg(target_os = "windows")]
impl_adapter!(WindowsCaptureAdapter);
#[cfg(target_os = "linux")]
impl_adapter!(X11CaptureAdapter);

fn platform_adapter() -> impl CaptureAdapter {
    #[cfg(target_os = "macos")]
    {
        MacOsCaptureAdapter(XCapAdapter {
            platform: "macos",
            shadow: true,
        })
    }
    #[cfg(target_os = "windows")]
    {
        WindowsCaptureAdapter(XCapAdapter {
            platform: "windows",
            shadow: false,
        })
    }
    #[cfg(target_os = "linux")]
    {
        X11CaptureAdapter(XCapAdapter {
            platform: "x11",
            shadow: false,
        })
    }
}

fn xcap_topology() -> Result<Vec<DisplayDescriptor>, CaptureError> {
    let mut displays = xcap::Monitor::all()
        .map_err(map_xcap_error)?
        .into_iter()
        .map(|monitor| {
            let scale = monitor.scale_factor().map_err(map_xcap_error)? as f64;
            let scale = if scale.is_finite() && scale > 0.0 {
                scale
            } else {
                1.0
            };
            let reported_x = monitor.x().map_err(map_xcap_error)? as f64;
            let reported_y = monitor.y().map_err(map_xcap_error)? as f64;
            let reported_width = monitor.width().map_err(map_xcap_error)?;
            let reported_height = monitor.height().map_err(map_xcap_error)?;
            // Windows exposes a contiguous signed virtual desktop in device
            // coordinates. Dividing each origin by its own DPI scale creates
            // overlaps and gaps, so retain that canonical coordinate space
            // while exposing the independent OS scale factor.
            #[cfg(target_os = "windows")]
            let (logical_x, logical_y, logical_width, logical_height, pixel_width, pixel_height) = (
                reported_x,
                reported_y,
                reported_width as f64,
                reported_height as f64,
                reported_width,
                reported_height,
            );
            #[cfg(not(target_os = "windows"))]
            let (logical_x, logical_y, logical_width, logical_height, pixel_width, pixel_height) = (
                reported_x,
                reported_y,
                reported_width as f64,
                reported_height as f64,
                (reported_width as f64 * scale).round().max(1.0) as u32,
                (reported_height as f64 * scale).round().max(1.0) as u32,
            );
            Ok(DisplayDescriptor {
                id: monitor.id().map_err(map_xcap_error)?.to_string(),
                name: monitor.name().map_err(map_xcap_error)?,
                logical_bounds: Rect {
                    x: logical_x,
                    y: logical_y,
                    width: logical_width,
                    height: logical_height,
                },
                pixel_width,
                pixel_height,
                scale,
                primary: monitor.is_primary().map_err(map_xcap_error)?,
            })
        })
        .collect::<Result<Vec<_>, CaptureError>>()?;
    displays.sort_by(|a, b| {
        a.logical_bounds
            .y
            .total_cmp(&b.logical_bounds.y)
            .then(a.logical_bounds.x.total_cmp(&b.logical_bounds.x))
            .then(a.id.cmp(&b.id))
    });
    if displays.is_empty() {
        Err(CaptureError::NoDisplay)
    } else {
        Ok(displays)
    }
}

fn xcap_windows() -> Result<Vec<WindowDescriptor>, CaptureError> {
    let candidates = xcap::Window::all()
        .map_err(map_xcap_error)?
        .into_iter()
        .map(|window| {
            let reported_x = window.x().map_err(map_xcap_error)? as f64;
            let reported_y = window.y().map_err(map_xcap_error)? as f64;
            let reported_width = window.width().map_err(map_xcap_error)? as f64;
            let reported_height = window.height().map_err(map_xcap_error)? as f64;
            let (x, y, width, height) = (reported_x, reported_y, reported_width, reported_height);
            Ok(WindowDescriptor {
                id: window.id().map_err(map_xcap_error)?.to_string(),
                bounds: Rect {
                    x,
                    y,
                    width,
                    height,
                },
                focused: window.is_focused().map_err(map_xcap_error)?,
                minimized: window.is_minimized().map_err(map_xcap_error)?,
            })
        });
    Ok(available_capture_targets(candidates))
}

fn available_capture_targets<T>(
    candidates: impl IntoIterator<Item = Result<T, CaptureError>>,
) -> Vec<T> {
    candidates.into_iter().filter_map(Result::ok).collect()
}

fn xcap_capture_display(id: &str) -> Result<RgbaImage, CaptureError> {
    let monitor = xcap::Monitor::all()
        .map_err(map_xcap_error)?
        .into_iter()
        .find(|monitor| {
            monitor
                .id()
                .ok()
                .is_some_and(|candidate| candidate.to_string() == id)
        })
        .ok_or(CaptureError::NoDisplay)?;
    monitor.capture_image().map_err(map_xcap_error)
}

fn xcap_capture_window(id: &str) -> Result<RgbaImage, CaptureError> {
    let window = xcap::Window::all()
        .map_err(map_xcap_error)?
        .into_iter()
        .find(|window| {
            window
                .id()
                .ok()
                .is_some_and(|candidate| candidate.to_string() == id)
        })
        .ok_or(CaptureError::NoWindow)?;
    window.capture_image().map_err(map_xcap_error)
}

fn map_xcap_error(error: xcap::XCapError) -> CaptureError {
    let category = error.to_string().to_ascii_lowercase();
    if category.contains("permission") || category.contains("denied") {
        CaptureError::PermissionDenied
    } else if category.contains("protected") {
        CaptureError::ProtectedContent
    } else {
        CaptureError::PlatformFailure
    }
}

#[cfg(target_os = "linux")]
fn native_pointer_position() -> Result<Point, CaptureError> {
    use std::ffi::c_void;
    #[link(name = "X11")]
    unsafe extern "C" {
        fn XOpenDisplay(name: *const i8) -> *mut c_void;
        fn XDefaultScreen(display: *mut c_void) -> i32;
        fn XRootWindow(display: *mut c_void, screen: i32) -> u64;
        fn XQueryPointer(
            display: *mut c_void,
            window: u64,
            root: *mut u64,
            child: *mut u64,
            root_x: *mut i32,
            root_y: *mut i32,
            win_x: *mut i32,
            win_y: *mut i32,
            mask: *mut u32,
        ) -> i32;
        fn XCloseDisplay(display: *mut c_void) -> i32;
    }
    unsafe {
        let display = XOpenDisplay(std::ptr::null());
        if display.is_null() {
            return Err(CaptureError::PermissionDenied);
        }
        let root_window = XRootWindow(display, XDefaultScreen(display));
        let mut root = 0;
        let mut child = 0;
        let mut root_x = 0;
        let mut root_y = 0;
        let mut win_x = 0;
        let mut win_y = 0;
        let mut mask = 0;
        let success = XQueryPointer(
            display,
            root_window,
            &mut root,
            &mut child,
            &mut root_x,
            &mut root_y,
            &mut win_x,
            &mut win_y,
            &mut mask,
        );
        XCloseDisplay(display);
        if success == 0 {
            Err(CaptureError::PlatformFailure)
        } else {
            Ok(Point {
                x: root_x as f64,
                y: root_y as f64,
            })
        }
    }
}

#[cfg(target_os = "windows")]
fn native_pointer_position() -> Result<Point, CaptureError> {
    #[repr(C)]
    struct WinPoint {
        x: i32,
        y: i32,
    }
    #[link(name = "user32")]
    unsafe extern "system" {
        fn GetCursorPos(point: *mut WinPoint) -> i32;
    }
    let mut point = WinPoint { x: 0, y: 0 };
    if unsafe { GetCursorPos(&mut point) } == 0 {
        Err(CaptureError::PlatformFailure)
    } else {
        Ok(Point {
            x: point.x as f64,
            y: point.y as f64,
        })
    }
}

#[cfg(target_os = "macos")]
fn native_pointer_position() -> Result<Point, CaptureError> {
    use std::ffi::c_void;
    #[repr(C)]
    struct CGPoint {
        x: f64,
        y: f64,
    }
    #[link(name = "CoreGraphics", kind = "framework")]
    unsafe extern "C" {
        fn CGEventCreate(source: *const c_void) -> *mut c_void;
        fn CGEventGetLocation(event: *mut c_void) -> CGPoint;
        fn CFRelease(value: *mut c_void);
    }
    unsafe {
        let event = CGEventCreate(std::ptr::null());
        if event.is_null() {
            return Err(CaptureError::PermissionDenied);
        }
        let point = CGEventGetLocation(event);
        CFRelease(event);
        Ok(Point {
            x: point.x,
            y: point.y,
        })
    }
}

#[cfg(test)]
mod tests {
    use std::sync::{
        Barrier,
        atomic::{AtomicUsize, Ordering},
        mpsc,
    };

    use image::ImageBuffer;

    use super::*;

    #[test]
    fn window_enumeration_skips_entries_that_disappear() {
        assert_eq!(
            available_capture_targets([Ok(1), Err(CaptureError::PlatformFailure), Ok(2),]),
            vec![1, 2]
        );
    }

    #[test]
    fn shadow_trim_adjusts_bounds_before_pointer_compositing() {
        let mut image = ImageBuffer::from_pixel(10, 10, Rgba([0, 0, 0, 0]));
        for y in 1..9 {
            for x in 2..8 {
                image.put_pixel(x, y, Rgba([12, 34, 56, 255]));
            }
        }
        let (mut image, bounds) = trim_transparent_border(
            image,
            Rect {
                x: 100.0,
                y: 200.0,
                width: 20.0,
                height: 40.0,
            },
        );

        assert_eq!(image.dimensions(), (6, 8));
        assert_eq!(
            bounds,
            Rect {
                x: 104.0,
                y: 204.0,
                width: 12.0,
                height: 32.0,
            }
        );
        draw_pointer_in_rect(&mut image, Point { x: 106.0, y: 208.0 }, bounds);
        assert_eq!(image.get_pixel(1, 1), &Rgba([0, 0, 0, 255]));
    }

    #[test]
    fn bundled_annotation_font_covers_english_and_korean() {
        let font = annotation_font().unwrap();
        for character in ['D', '한', '글'] {
            assert!(font.has_glyph(character));
        }
    }

    struct TestDirectory(PathBuf);

    impl TestDirectory {
        fn new() -> Self {
            let path = std::env::temp_dir().join(format!("devhud-realqa-test-{}", Uuid::now_v7()));
            fs::create_dir(&path).unwrap();
            Self(path)
        }
    }

    impl Drop for TestDirectory {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    struct FakeAdapter {
        displays: Vec<DisplayDescriptor>,
        pointer: Point,
        topology_calls: AtomicUsize,
        capture_calls: AtomicUsize,
        change_topology: bool,
        fail_first_capture: bool,
        pointer_failure: bool,
        protected_display: Option<&'static str>,
        transparent: bool,
    }

    impl CaptureAdapter for FakeAdapter {
        fn platform(&self) -> &'static str {
            "test"
        }

        fn topology(&self) -> Result<Vec<DisplayDescriptor>, CaptureError> {
            let call = self.topology_calls.fetch_add(1, Ordering::SeqCst);
            let mut displays = self.displays.clone();
            if self.change_topology && call > 0 {
                displays[0].pixel_width += 1;
            }
            Ok(displays)
        }

        fn pointer_position(&self) -> Result<Point, CaptureError> {
            if self.pointer_failure {
                Err(CaptureError::PlatformFailure)
            } else {
                Ok(self.pointer)
            }
        }

        fn windows(&self) -> Result<Vec<WindowDescriptor>, CaptureError> {
            Ok(vec![WindowDescriptor {
                id: "window".into(),
                bounds: Rect {
                    x: -80.0,
                    y: 10.0,
                    width: 50.0,
                    height: 40.0,
                },
                focused: true,
                minimized: false,
            }])
        }

        fn capture_display(&self, id: &str) -> Result<RgbaImage, CaptureError> {
            let call = self.capture_calls.fetch_add(1, Ordering::SeqCst);
            if self.fail_first_capture && call == 0 {
                return Err(CaptureError::NoDisplay);
            }
            let display = self
                .displays
                .iter()
                .find(|display| display.id == id)
                .ok_or(CaptureError::NoDisplay)?;
            let color = if self.transparent || self.protected_display == Some(id) {
                Rgba([0, 0, 0, 0])
            } else if id == "left" {
                Rgba([255, 0, 0, 255])
            } else {
                Rgba([0, 0, 255, 255])
            };
            Ok(ImageBuffer::from_pixel(
                display.pixel_width,
                display.pixel_height,
                color,
            ))
        }

        fn capture_window(&self, _id: &str) -> Result<RgbaImage, CaptureError> {
            Ok(ImageBuffer::from_pixel(
                100,
                80,
                if self.transparent {
                    Rgba([0, 0, 0, 0])
                } else {
                    Rgba([12, 34, 56, 255])
                },
            ))
        }

        fn shadow_removal_supported(&self) -> bool {
            true
        }
    }

    fn displays() -> Vec<DisplayDescriptor> {
        vec![
            DisplayDescriptor {
                id: "left".into(),
                name: "Left".into(),
                logical_bounds: Rect {
                    x: -100.0,
                    y: 0.0,
                    width: 100.0,
                    height: 100.0,
                },
                pixel_width: 100,
                pixel_height: 100,
                scale: 1.0,
                primary: false,
            },
            DisplayDescriptor {
                id: "right".into(),
                name: "Right".into(),
                logical_bounds: Rect {
                    x: 0.0,
                    y: 0.0,
                    width: 100.0,
                    height: 100.0,
                },
                pixel_width: 200,
                pixel_height: 200,
                scale: 2.0,
                primary: true,
            },
        ]
    }

    #[test]
    fn geometry_handles_negative_coordinates_and_half_open_edges() {
        let rect = Rect {
            x: -1920.0,
            y: -200.0,
            width: 1920.0,
            height: 1080.0,
        };
        assert!(rect.contains(Point { x: -1.0, y: 0.0 }));
        assert!(!rect.contains(Point { x: 0.0, y: 0.0 }));
        assert_eq!(
            rect.intersection(Rect {
                x: -100.0,
                y: -300.0,
                width: 200.0,
                height: 200.0
            })
            .unwrap(),
            Rect {
                x: -100.0,
                y: -200.0,
                width: 100.0,
                height: 100.0
            }
        );
    }

    #[test]
    fn png_is_srgb_and_contains_no_text_or_time_metadata() {
        let bytes =
            encode_srgb_png(&ImageBuffer::from_pixel(2, 2, Rgba([10, 20, 30, 255]))).unwrap();
        let chunks = png_chunks(&bytes);
        assert_eq!(chunks, vec!["IHDR", "sRGB", "IDAT", "IEND"]);
    }

    #[test]
    fn authenticated_encryption_detects_tampering_and_binds_role() {
        let key = [7_u8; 32];
        let id = Uuid::now_v7();
        let mut bytes = encrypt(&key, id, "source:test", b"sensitive pixels").unwrap();
        assert_eq!(
            decrypt(&key, id, "source:test", &bytes).unwrap(),
            b"sensitive pixels"
        );
        assert!(decrypt(&key, id, "manifest", &bytes).is_err());
        *bytes.last_mut().unwrap() ^= 1;
        assert!(decrypt(&key, id, "source:test", &bytes).is_err());
        assert!(
            !bytes
                .windows(b"sensitive pixels".len())
                .any(|window| window == b"sensitive pixels")
        );
    }

    #[test]
    fn editor_history_validates_layers_and_redaction_is_opaque() {
        let image_id = Uuid::now_v7();
        let layer_id = Uuid::now_v7();
        let mut state = DraftState {
            images: vec![DraftImageState {
                id: image_id,
                width: 8,
                height: 8,
                removed: false,
                crop: None,
                layers: vec![],
            }],
        };
        apply_command(
            &mut state,
            EditorCommand::AddLayer {
                image_id,
                layer: EditorLayer::Redaction {
                    id: layer_id,
                    bounds: Rect {
                        x: 1.0,
                        y: 1.0,
                        width: 4.0,
                        height: 4.0,
                    },
                },
            },
        )
        .unwrap();
        let output = render_editor(
            ImageBuffer::from_pixel(8, 8, Rgba([255, 0, 0, 255])),
            &state.images[0],
        )
        .unwrap();
        assert_eq!(output.get_pixel(2, 2), &Rgba([0, 0, 0, 255]));

        assert_eq!(
            apply_command(
                &mut state,
                EditorCommand::AddLayer {
                    image_id,
                    layer: EditorLayer::Arrow {
                        id: Uuid::now_v7(),
                        start: Point { x: 1.0, y: 1.0 },
                        end: Point {
                            x: 1_000_000_000.0,
                            y: 1_000_000_000.0,
                        },
                        color: "#ef4444".into(),
                        width: 4,
                    },
                },
            )
            .unwrap_err(),
            CaptureError::InvalidArgument
        );
        let mut clipped = ImageBuffer::from_pixel(8, 8, Rgba([255, 255, 255, 255]));
        draw_thick_line(
            &mut clipped,
            Point {
                x: -1_000_000_000.0,
                y: 4.0,
            },
            Point {
                x: 1_000_000_000.0,
                y: 4.0,
            },
            Rgba([0, 0, 0, 255]),
            1,
        );
        assert_eq!(clipped.get_pixel(4, 4), &Rgba([0, 0, 0, 255]));
    }

    #[test]
    fn line_rasterization_preserves_requested_even_and_odd_widths() {
        let thickness = |width| {
            let mut image = RgbaImage::new(32, 32);
            draw_thick_line(
                &mut image,
                Point { x: 4.0, y: 16.0 },
                Point { x: 28.0, y: 16.0 },
                Rgba([0, 0, 0, 255]),
                width,
            );
            (0..image.height())
                .filter(|y| image.get_pixel(16, *y).0[3] != 0)
                .count()
        };

        assert_eq!(thickness(3), 3);
        assert_eq!(thickness(4), 4);
    }

    #[test]
    fn mixed_scale_region_uses_the_highest_density_without_coordinate_gaps() {
        let adapter = FakeAdapter {
            displays: displays(),
            pointer: Point { x: -50.0, y: 20.0 },
            topology_calls: AtomicUsize::new(0),
            capture_calls: AtomicUsize::new(0),
            change_topology: false,
            fail_first_capture: false,
            pointer_failure: false,
            protected_display: None,
            transparent: false,
        };
        let (output, captured_bounds) = capture_region(
            &adapter,
            &adapter.displays,
            Rect {
                x: -50.0,
                y: 0.0,
                width: 100.0,
                height: 50.0,
            },
        )
        .unwrap();
        assert_eq!(
            captured_bounds,
            Rect {
                x: -50.0,
                y: 0.0,
                width: 100.0,
                height: 50.0,
            }
        );
        assert_eq!(output.dimensions(), (200, 100));
        assert_eq!(output.get_pixel(25, 25), &Rgba([255, 0, 0, 255]));
        assert_eq!(output.get_pixel(175, 25), &Rgba([0, 0, 255, 255]));

        let (bounded, bounded_selection) = capture_region(
            &adapter,
            &adapter.displays,
            Rect {
                x: -1_000_000_000.0,
                y: -1_000_000_000.0,
                width: 2_000_000_000.0,
                height: 2_000_000_000.0,
            },
        )
        .unwrap();
        assert_eq!(
            bounded_selection,
            Rect {
                x: -100.0,
                y: 0.0,
                width: 200.0,
                height: 100.0,
            }
        );
        assert_eq!(bounded.dimensions(), (400, 200));

        let (fractional, _) = capture_region(
            &adapter,
            &adapter.displays,
            Rect {
                x: 10.25,
                y: 5.25,
                width: 10.2,
                height: 10.2,
            },
        )
        .unwrap();
        assert_eq!(fractional.dimensions(), (21, 21));
        assert_eq!(fractional.get_pixel(20, 20), &Rgba([0, 0, 255, 255]));
    }

    #[test]
    fn all_display_pointer_uses_the_same_stable_order_as_capture() {
        let mut unordered = displays();
        unordered.reverse();
        let mut images = vec![
            ImageBuffer::from_pixel(100, 100, Rgba([255, 0, 0, 255])),
            ImageBuffer::from_pixel(200, 200, Rgba([0, 0, 255, 255])),
        ];
        apply_pointer(
            &unordered,
            Point { x: 20.0, y: 20.0 },
            &mut images,
            CaptureAction::AllDisplays,
        );
        assert_eq!(images[0].get_pixel(40, 40), &Rgba([255, 0, 0, 255]));
        assert_eq!(images[1].get_pixel(40, 40), &Rgba([0, 0, 0, 255]));
    }

    #[test]
    fn hotplug_retries_once_and_protected_content_is_a_typed_failure() {
        for (change_topology, transparent, expected) in [
            (true, false, None),
            (false, true, Some(CaptureError::ProtectedContent)),
        ] {
            let root = TestDirectory::new();
            let store = Arc::new(DraftStore::new_test(root.0.clone(), 1024 * 1024, [9; 32]));
            let adapter = Arc::new(FakeAdapter {
                displays: displays(),
                pointer: Point { x: -50.0, y: 20.0 },
                topology_calls: AtomicUsize::new(0),
                capture_calls: AtomicUsize::new(0),
                change_topology,
                fail_first_capture: false,
                pointer_failure: false,
                protected_display: None,
                transparent,
            });
            let service = CaptureService::new(adapter, store);
            let result = service.capture(
                CaptureAction::Display,
                CaptureOptions {
                    include_pointer: false,
                    remove_shadow: false,
                    delay_seconds: 0,
                    selection: None,
                    selection_window: false,
                    append_to_draft_id: None,
                },
            );
            match expected {
                Some(error) => assert_eq!(result.unwrap_err(), error),
                None => assert!(result.is_ok()),
            }
        }

        let mut logical_change = displays();
        logical_change[0].logical_bounds.width -= 1.0;
        assert_ne!(
            topology_signature(&displays()),
            topology_signature(&logical_change)
        );

        let root = TestDirectory::new();
        let store = Arc::new(DraftStore::new_test(root.0.clone(), 1024 * 1024, [9; 32]));
        let service = CaptureService::new(
            Arc::new(FakeAdapter {
                displays: displays(),
                pointer: Point { x: -50.0, y: 20.0 },
                topology_calls: AtomicUsize::new(0),
                capture_calls: AtomicUsize::new(0),
                change_topology: false,
                fail_first_capture: false,
                pointer_failure: false,
                protected_display: None,
                transparent: true,
            }),
            store,
        );
        assert_eq!(
            service
                .capture(
                    CaptureAction::ActiveWindow,
                    CaptureOptions {
                        include_pointer: true,
                        remove_shadow: false,
                        delay_seconds: 0,
                        selection: None,
                        selection_window: false,
                        append_to_draft_id: None,
                    },
                )
                .unwrap_err(),
            CaptureError::ProtectedContent
        );

        let root = TestDirectory::new();
        let adapter = Arc::new(FakeAdapter {
            displays: displays(),
            pointer: Point { x: -50.0, y: 20.0 },
            topology_calls: AtomicUsize::new(0),
            capture_calls: AtomicUsize::new(0),
            change_topology: true,
            fail_first_capture: true,
            pointer_failure: false,
            protected_display: None,
            transparent: false,
        });
        let service = CaptureService::new(
            adapter.clone(),
            Arc::new(DraftStore::new_test(root.0.clone(), 1024 * 1024, [10; 32])),
        );
        assert!(
            service
                .capture(
                    CaptureAction::Display,
                    CaptureOptions {
                        include_pointer: false,
                        remove_shadow: false,
                        delay_seconds: 0,
                        selection: None,
                        selection_window: false,
                        append_to_draft_id: None,
                    },
                )
                .is_ok()
        );
        assert_eq!(adapter.capture_calls.load(Ordering::SeqCst), 2);
    }

    #[test]
    fn pointer_independent_captures_do_not_query_a_failed_pointer_adapter() {
        for (action, selection) in [
            (CaptureAction::ActiveWindow, None),
            (CaptureAction::AllDisplays, None),
            (
                CaptureAction::Selection,
                Some(Rect {
                    x: -50.0,
                    y: 0.0,
                    width: 100.0,
                    height: 50.0,
                }),
            ),
        ] {
            let root = TestDirectory::new();
            let service = CaptureService::new(
                Arc::new(FakeAdapter {
                    displays: displays(),
                    pointer: Point { x: -50.0, y: 20.0 },
                    topology_calls: AtomicUsize::new(0),
                    capture_calls: AtomicUsize::new(0),
                    change_topology: false,
                    fail_first_capture: false,
                    pointer_failure: true,
                    protected_display: None,
                    transparent: false,
                }),
                Arc::new(DraftStore::new_test(root.0.clone(), 1024 * 1024, [12; 32])),
            );
            assert!(
                service
                    .capture(
                        action,
                        CaptureOptions {
                            include_pointer: false,
                            remove_shadow: false,
                            delay_seconds: 0,
                            selection,
                            selection_window: false,
                            append_to_draft_id: None,
                        },
                    )
                    .is_ok()
            );
        }
    }

    #[test]
    fn region_capture_rejects_each_protected_source_frame() {
        let adapter = FakeAdapter {
            displays: displays(),
            pointer: Point { x: -50.0, y: 20.0 },
            topology_calls: AtomicUsize::new(0),
            capture_calls: AtomicUsize::new(0),
            change_topology: false,
            fail_first_capture: false,
            pointer_failure: false,
            protected_display: Some("left"),
            transparent: false,
        };
        assert_eq!(
            capture_region(
                &adapter,
                &adapter.displays,
                Rect {
                    x: -50.0,
                    y: 0.0,
                    width: 100.0,
                    height: 50.0,
                },
            )
            .unwrap_err(),
            CaptureError::ProtectedContent
        );
    }

    #[test]
    fn delayed_capture_can_cancel_before_the_window_handoff() {
        let root = TestDirectory::new();
        let service = Arc::new(CaptureService::new(
            Arc::new(FakeAdapter {
                displays: displays(),
                pointer: Point { x: -50.0, y: 20.0 },
                topology_calls: AtomicUsize::new(0),
                capture_calls: AtomicUsize::new(0),
                change_topology: false,
                fail_first_capture: false,
                pointer_failure: false,
                protected_display: None,
                transparent: false,
            }),
            Arc::new(DraftStore::new_test(root.0.clone(), 1024 * 1024, [14; 32])),
        ));
        let epoch = service.begin_capture().unwrap();
        let handoff_called = Arc::new(AtomicBool::new(false));
        let capture_service = service.clone();
        let capture_handoff = handoff_called.clone();
        let capture = std::thread::spawn(move || {
            capture_service.capture_with_epoch_after_delay(
                CaptureAction::ActiveWindow,
                CaptureOptions {
                    include_pointer: false,
                    remove_shadow: false,
                    delay_seconds: 5,
                    selection: None,
                    selection_window: false,
                    append_to_draft_id: None,
                },
                epoch,
                move || {
                    capture_handoff.store(true, Ordering::SeqCst);
                    Ok(())
                },
            )
        });
        std::thread::sleep(Duration::from_millis(50));
        service.cancel();
        assert_eq!(
            capture.join().unwrap().unwrap_err(),
            CaptureError::Cancelled
        );
        assert!(!handoff_called.load(Ordering::SeqCst));
    }

    #[test]
    fn window_targeting_grace_is_cancellable_after_the_window_handoff() {
        let root = TestDirectory::new();
        let adapter = Arc::new(FakeAdapter {
            displays: displays(),
            pointer: Point { x: -50.0, y: 20.0 },
            topology_calls: AtomicUsize::new(0),
            capture_calls: AtomicUsize::new(0),
            change_topology: false,
            fail_first_capture: false,
            pointer_failure: false,
            protected_display: None,
            transparent: false,
        });
        let service = Arc::new(CaptureService::new(
            adapter.clone(),
            Arc::new(DraftStore::new_test(root.0.clone(), 1024 * 1024, [27; 32])),
        ));
        let epoch = service.begin_capture().unwrap();
        let (handoff, handed_off) = mpsc::channel();
        let capture_service = service.clone();
        let capture = std::thread::spawn(move || {
            capture_service.capture_with_epoch_after_delay(
                CaptureAction::Selection,
                CaptureOptions {
                    include_pointer: false,
                    remove_shadow: false,
                    delay_seconds: 0,
                    selection: None,
                    selection_window: true,
                    append_to_draft_id: None,
                },
                epoch,
                move || handoff.send(()).map_err(|_| CaptureError::PlatformFailure),
            )
        });

        handed_off.recv_timeout(Duration::from_secs(1)).unwrap();
        service.cancel();
        assert_eq!(
            capture.join().unwrap().unwrap_err(),
            CaptureError::Cancelled
        );
        assert_eq!(adapter.capture_calls.load(Ordering::SeqCst), 0);
    }

    #[test]
    fn stale_capture_epoch_is_rejected_immediately_before_persistence() {
        let root = TestDirectory::new();
        let store = Arc::new(DraftStore::new_test(root.0.clone(), 1024 * 1024, [15; 32]));
        let service = CaptureService::new(
            Arc::new(FakeAdapter {
                displays: displays(),
                pointer: Point { x: -50.0, y: 20.0 },
                topology_calls: AtomicUsize::new(0),
                capture_calls: AtomicUsize::new(0),
                change_topology: false,
                fail_first_capture: false,
                pointer_failure: false,
                protected_display: None,
                transparent: false,
            }),
            store.clone(),
        );
        let epoch = service.begin_capture().unwrap();
        service.cancel();
        assert_eq!(
            service
                .persist_capture(
                    &CaptureOptions {
                        include_pointer: false,
                        remove_shadow: false,
                        delay_seconds: 0,
                        selection: None,
                        selection_window: false,
                        append_to_draft_id: None,
                    },
                    vec![ImageBuffer::from_pixel(8, 8, Rgba([1, 2, 3, 255]))],
                    epoch,
                )
                .unwrap_err(),
            CaptureError::Cancelled
        );
        assert!(store.list().unwrap().drafts.is_empty());
    }

    #[test]
    fn encrypted_drafts_recover_history_expiry_and_logout_without_plaintext() {
        let root = TestDirectory::new();
        let key = [11; 32];
        let store = DraftStore::new_test(root.0.clone(), 1024 * 1024, key);
        let created = store
            .create(vec![ImageBuffer::from_pixel(
                16,
                16,
                Rgba([91, 72, 53, 255]),
            )])
            .unwrap();
        let draft_path = root.0.join(created.id.to_string());
        let source =
            fs::read(draft_path.join(format!("source-{}.bin", created.images[0].id))).unwrap();
        assert!(source.starts_with(ENCRYPTED_MAGIC));
        assert!(
            !source
                .windows(16)
                .any(|window| window == [91_u8, 72, 53, 255].repeat(4))
        );

        let edited = store
            .apply(
                created.id,
                created.revision,
                EditorCommand::SetCrop {
                    image_id: created.images[0].id,
                    crop: Some(Rect {
                        x: 1.0,
                        y: 1.0,
                        width: 8.0,
                        height: 8.0,
                    }),
                },
            )
            .unwrap();
        let undone = store.undo(created.id, edited.revision).unwrap();
        assert!(undone.images[0].crop.is_none());
        let redone = store.redo(created.id, undone.revision).unwrap();
        assert!(redone.images[0].crop.is_some());

        fs::create_dir(root.0.join(".txn-crash")).unwrap();
        fs::write(root.0.join("orphan.tmp"), b"not image data").unwrap();
        fs::write(draft_path.join("manifest.tmp"), b"interrupted replacement").unwrap();
        let orphan = draft_path.join(format!("source-{}.bin", Uuid::now_v7()));
        fs::write(&orphan, b"interrupted append").unwrap();
        store.recover().unwrap();
        assert!(!root.0.join(".txn-crash").exists());
        assert!(!root.0.join("orphan.tmp").exists());
        assert!(!draft_path.join("manifest.tmp").exists());
        assert!(!orphan.exists());

        let mut document = read_document(&draft_path.join("manifest.bin"), &key).unwrap();
        document.expires_at = 0;
        atomic_replace(
            &draft_path.join("manifest.bin"),
            &encrypt_document(&document, &key).unwrap(),
        )
        .unwrap();
        store.recover().unwrap();
        assert!(!draft_path.exists());

        store
            .create(vec![ImageBuffer::from_pixel(8, 8, Rgba([1, 2, 3, 255]))])
            .unwrap();
        let unreadable = store
            .create(vec![ImageBuffer::from_pixel(8, 8, Rgba([4, 5, 6, 255]))])
            .unwrap();
        fs::write(
            root.0.join(unreadable.id.to_string()).join("manifest.bin"),
            b"corrupt encrypted manifest",
        )
        .unwrap();
        let listed = store.list().unwrap();
        assert_eq!(listed.unreadable_draft_ids, vec![unreadable.id]);
        store.delete(unreadable.id).unwrap();
        assert!(!root.0.join(unreadable.id.to_string()).exists());
        store.purge_all().unwrap();
        assert!(!root.0.exists());
    }

    #[test]
    fn persisted_history_is_bounded_to_one_hundred_states() {
        let root = TestDirectory::new();
        let key = [19; 32];
        let store = DraftStore::new_test(root.0.clone(), 1024 * 1024, key);
        let mut draft = store
            .create(vec![ImageBuffer::from_pixel(16, 16, Rgba([1, 2, 3, 255]))])
            .unwrap();
        for index in 0..MAX_HISTORY_STATES + 5 {
            draft = store
                .apply(
                    draft.id,
                    draft.revision,
                    EditorCommand::SetCrop {
                        image_id: draft.images[0].id,
                        crop: Some(Rect {
                            x: (index % 2) as f64,
                            y: 0.0,
                            width: 8.0,
                            height: 8.0,
                        }),
                    },
                )
                .unwrap();
        }
        let document = read_document(
            &root.0.join(draft.id.to_string()).join("manifest.bin"),
            &key,
        )
        .unwrap();
        assert_eq!(
            document.undo.len() + document.redo.len(),
            MAX_HISTORY_STATES
        );
    }

    #[test]
    fn quota_pressure_prunes_history_so_a_shrinking_edit_can_succeed() {
        let root = TestDirectory::new();
        let key = [21; 32];
        let initial = DraftStore::new_test(root.0.clone(), 1024 * 1024, key);
        let created = initial
            .create(vec![ImageBuffer::from_pixel(16, 16, Rgba([1, 2, 3, 255]))])
            .unwrap();
        let layer_id = Uuid::now_v7();
        let points = (0..1024)
            .map(|index| Point {
                x: f64::from(index % 16),
                y: f64::from((index / 16) % 16),
            })
            .collect();
        let expanded = initial
            .apply(
                created.id,
                created.revision,
                EditorCommand::AddLayer {
                    image_id: created.images[0].id,
                    layer: EditorLayer::Drawing {
                        id: layer_id,
                        points,
                        color: "#ef4444".into(),
                        width: 4,
                    },
                },
            )
            .unwrap();
        let manifest = root.0.join(expanded.id.to_string()).join("manifest.bin");
        let non_manifest_bytes =
            directory_size(&root.0).unwrap() - fs::metadata(&manifest).unwrap().len();
        let constrained = DraftStore::new_test(root.0.clone(), non_manifest_bytes + 2048, key);
        let compacted = constrained
            .apply(
                expanded.id,
                expanded.revision,
                EditorCommand::RemoveLayer {
                    image_id: expanded.images[0].id,
                    layer_id,
                },
            )
            .unwrap();
        assert!(compacted.images[0].layers.is_empty());
        assert!(directory_size(&root.0).unwrap() <= non_manifest_bytes + 2048);
    }

    #[test]
    fn revision_change_reclaims_flattened_outputs_before_quota_check() {
        let root = TestDirectory::new();
        let key = [23; 32];
        let initial = DraftStore::new_test(root.0.clone(), 1024 * 1024, key);
        let created = initial
            .create(vec![ImageBuffer::from_fn(128, 128, |x, y| {
                Rgba([
                    x.wrapping_mul(17) as u8,
                    y.wrapping_mul(29) as u8,
                    x.wrapping_mul(y).wrapping_add(31) as u8,
                    255,
                ])
            })])
            .unwrap();
        initial.flatten(created.id, created.revision).unwrap();
        let flattened = root
            .0
            .join(created.id.to_string())
            .join(format!("flattened-{}.bin", created.revision));
        let used = directory_size(&root.0).unwrap();
        let quota = used - fs::metadata(&flattened).unwrap().len() + 2048;
        assert!(quota < used);
        let constrained = DraftStore::new_test(root.0.clone(), quota, key);

        constrained
            .apply(
                created.id,
                created.revision,
                EditorCommand::SetCrop {
                    image_id: created.images[0].id,
                    crop: Some(Rect {
                        x: 1.0,
                        y: 1.0,
                        width: 64.0,
                        height: 64.0,
                    }),
                },
            )
            .unwrap();

        assert!(!flattened.exists());
        assert!(directory_size(&root.0).unwrap() <= quota);
        assert_eq!(
            constrained
                .asset(created.id, created.images[0].id, true, created.revision,)
                .unwrap_err(),
            CaptureError::RevisionConflict
        );
    }

    #[test]
    fn flattened_bundle_entries_are_bound_to_the_draft_revision() {
        let root = TestDirectory::new();
        let key = [28; 32];
        let store = DraftStore::new_test(root.0.clone(), 1024 * 1024, key);
        let created = store
            .create(vec![ImageBuffer::from_pixel(
                16,
                16,
                Rgba([11, 22, 33, 255]),
            )])
            .unwrap();
        store.flatten(created.id, created.revision).unwrap();
        let directory = root.0.join(created.id.to_string());
        let previous_bundle =
            fs::read(directory.join(format!("flattened-{}.bin", created.revision))).unwrap();
        let edited = store
            .apply(
                created.id,
                created.revision,
                EditorCommand::SetCrop {
                    image_id: created.images[0].id,
                    crop: Some(Rect {
                        x: 1.0,
                        y: 1.0,
                        width: 8.0,
                        height: 8.0,
                    }),
                },
            )
            .unwrap();
        store.flatten(edited.id, edited.revision).unwrap();
        fs::write(
            directory.join(format!("flattened-{}.bin", edited.revision)),
            previous_bundle,
        )
        .unwrap();

        assert_eq!(
            store
                .asset(edited.id, edited.images[0].id, true, edited.revision)
                .unwrap_err(),
            CaptureError::StorageFailure
        );
    }

    #[test]
    fn failed_manifest_commit_preserves_the_current_revision_flattened_bundle() {
        let root = TestDirectory::new();
        let key = [25; 32];
        let store = DraftStore::new_test(root.0.clone(), 1024 * 1024, key);
        let created = store
            .create(vec![ImageBuffer::from_pixel(
                16,
                16,
                Rgba([11, 22, 33, 255]),
            )])
            .unwrap();
        store.flatten(created.id, created.revision).unwrap();
        let directory = root.0.join(created.id.to_string());
        let manifest = directory.join("manifest.bin");
        let bundle = directory.join(format!("flattened-{}.bin", created.revision));
        let previous_manifest = fs::read(&manifest).unwrap();
        let previous_bundle = fs::read(&bundle).unwrap();
        let previous_asset = store
            .asset(created.id, created.images[0].id, true, created.revision)
            .unwrap();
        let mut document = read_document(&manifest, &key).unwrap();
        let previous = document.current.clone();
        document.current.images[0].crop = Some(Rect {
            x: 1.0,
            y: 1.0,
            width: 8.0,
            height: 8.0,
        });
        document.undo.push(previous);
        document.redo.clear();
        document.touch(DraftStore::now().unwrap());

        assert_eq!(
            store
                .write_document_locked_with_replacer(
                    &mut document,
                    &key,
                    || Ok(()),
                    |_, _| Err(CaptureError::StorageFailure),
                )
                .unwrap_err(),
            CaptureError::StorageFailure
        );
        assert_eq!(fs::read(manifest).unwrap(), previous_manifest);
        assert_eq!(fs::read(bundle).unwrap(), previous_bundle);
        assert_eq!(
            store
                .asset(created.id, created.images[0].id, true, created.revision,)
                .unwrap(),
            previous_asset
        );
    }

    #[test]
    fn removed_image_assets_are_reclaimed_after_undo_history_expires() {
        let root = TestDirectory::new();
        let key = [22; 32];
        let store = DraftStore::new_test(root.0.clone(), 1024 * 1024, key);
        let created = store
            .create(vec![
                ImageBuffer::from_pixel(16, 16, Rgba([1, 2, 3, 255])),
                ImageBuffer::from_fn(128, 128, |x, y| {
                    Rgba([
                        x.wrapping_mul(17) as u8,
                        y.wrapping_mul(29) as u8,
                        x.wrapping_mul(y).wrapping_add(31) as u8,
                        255,
                    ])
                }),
            ])
            .unwrap();
        store.flatten(created.id, created.revision).unwrap();
        let retained_image_id = created.images[0].id;
        let removed_image_id = created.images[1].id;
        let draft_path = root.0.join(created.id.to_string());
        let removed_source = draft_path.join(format!("source-{removed_image_id}.bin"));
        let removed_flattened = draft_path.join(format!("flattened-{}.bin", created.revision));
        let mut current = store
            .apply(
                created.id,
                created.revision,
                EditorCommand::RemoveImage {
                    image_id: removed_image_id,
                },
            )
            .unwrap();

        store.recover().unwrap();
        assert!(removed_source.exists());
        assert!(!removed_flattened.exists());

        for index in 0..MAX_HISTORY_STATES - 1 {
            current = store
                .apply(
                    created.id,
                    current.revision,
                    EditorCommand::SetCrop {
                        image_id: retained_image_id,
                        crop: Some(Rect {
                            x: 0.0,
                            y: 0.0,
                            width: 8.0 + f64::from((index % 2) as u8),
                            height: 8.0,
                        }),
                    },
                )
                .unwrap();
        }

        assert!(removed_source.exists());
        let reclaimable = fs::metadata(&removed_source).unwrap().len();
        let quota = directory_size(&root.0).unwrap() - reclaimable + 2048;
        let constrained = DraftStore::new_test(root.0.clone(), quota, key);
        constrained
            .apply(
                current.id,
                current.revision,
                EditorCommand::SetCrop {
                    image_id: retained_image_id,
                    crop: Some(Rect {
                        x: 0.0,
                        y: 0.0,
                        width: 10.0,
                        height: 8.0,
                    }),
                },
            )
            .unwrap();
        assert!(!removed_source.exists());
        assert!(!removed_flattened.exists());
        assert_eq!(constrained.open(created.id).unwrap().id, created.id);
        assert!(directory_size(&root.0).unwrap() <= quota);
    }

    #[test]
    fn opaque_black_window_frames_are_protected_only_on_affected_platforms() {
        let opaque_black = ImageBuffer::from_pixel(8, 8, Rgba([0, 0, 0, 255]));
        let visible = ImageBuffer::from_pixel(8, 8, Rgba([1, 0, 0, 255]));
        assert!(protected_window_frame("macos", &opaque_black));
        assert!(protected_window_frame("windows", &opaque_black));
        assert!(!protected_window_frame("x11", &opaque_black));
        assert!(!protected_window_frame("macos", &visible));
    }

    #[test]
    fn cancellation_guards_prevent_create_and_append_commits() {
        let root = TestDirectory::new();
        let store = DraftStore::new_test(root.0.clone(), 1024 * 1024, [24; 32]);
        let cancelled = store
            .create_with_commit_check(
                vec![ImageBuffer::from_pixel(8, 8, Rgba([1, 2, 3, 255]))],
                || Err(CaptureError::Cancelled),
            )
            .unwrap_err();
        assert_eq!(cancelled, CaptureError::Cancelled);
        assert!(store.list().unwrap().drafts.is_empty());

        let created = store
            .create(vec![ImageBuffer::from_pixel(8, 8, Rgba([4, 5, 6, 255]))])
            .unwrap();
        let cancelled = store
            .append_with_commit_check(
                created.id,
                vec![ImageBuffer::from_pixel(8, 8, Rgba([7, 8, 9, 255]))],
                || Err(CaptureError::Cancelled),
            )
            .unwrap_err();
        assert_eq!(cancelled, CaptureError::Cancelled);
        assert_eq!(store.open(created.id).unwrap().image_count, 1);
        let source_count = fs::read_dir(root.0.join(created.id.to_string()))
            .unwrap()
            .filter_map(Result::ok)
            .filter(|entry| entry.file_name().to_string_lossy().starts_with("source-"))
            .count();
        assert_eq!(source_count, 1);
        assert_eq!(
            store
                .append(
                    created.id,
                    vec![ImageBuffer::from_pixel(8, 8, Rgba([10, 11, 12, 255]))],
                )
                .unwrap()
                .image_count,
            2
        );
    }

    #[test]
    fn purge_cancels_and_joins_an_in_flight_capture_before_deleting_drafts() {
        struct BlockingAdapter {
            entered: Arc<Barrier>,
            release: Arc<Barrier>,
        }

        impl CaptureAdapter for BlockingAdapter {
            fn platform(&self) -> &'static str {
                "test"
            }

            fn topology(&self) -> Result<Vec<DisplayDescriptor>, CaptureError> {
                Ok(displays())
            }

            fn pointer_position(&self) -> Result<Point, CaptureError> {
                Ok(Point { x: -50.0, y: 20.0 })
            }

            fn windows(&self) -> Result<Vec<WindowDescriptor>, CaptureError> {
                Ok(Vec::new())
            }

            fn capture_display(&self, _id: &str) -> Result<RgbaImage, CaptureError> {
                self.entered.wait();
                self.release.wait();
                Ok(ImageBuffer::from_pixel(100, 100, Rgba([1, 2, 3, 255])))
            }

            fn capture_window(&self, _id: &str) -> Result<RgbaImage, CaptureError> {
                Err(CaptureError::NoWindow)
            }

            fn shadow_removal_supported(&self) -> bool {
                false
            }
        }

        let root = TestDirectory::new();
        let store = Arc::new(DraftStore::new_test(root.0.clone(), 1024 * 1024, [23; 32]));
        store
            .create(vec![ImageBuffer::from_pixel(8, 8, Rgba([4, 5, 6, 255]))])
            .unwrap();
        let entered = Arc::new(Barrier::new(2));
        let release = Arc::new(Barrier::new(2));
        let service = Arc::new(CaptureService::new(
            Arc::new(BlockingAdapter {
                entered: entered.clone(),
                release: release.clone(),
            }),
            store,
        ));
        let capture_service = service.clone();
        let capture = std::thread::spawn(move || {
            capture_service.capture(
                CaptureAction::Display,
                CaptureOptions {
                    include_pointer: false,
                    remove_shadow: false,
                    delay_seconds: 0,
                    selection: None,
                    selection_window: false,
                    append_to_draft_id: None,
                },
            )
        });
        entered.wait();
        let purge_service = service.clone();
        let purge = std::thread::spawn(move || {
            let guard = purge_service.begin_purge().unwrap();
            guard.purge_drafts().unwrap();
        });
        while !service.purging.load(Ordering::Acquire) {
            std::thread::yield_now();
        }
        release.wait();
        assert_eq!(
            capture.join().unwrap().unwrap_err(),
            CaptureError::Cancelled
        );
        purge.join().unwrap();
        assert!(!root.0.exists());
    }

    #[test]
    fn quota_rejection_never_evicts_an_unexpired_draft() {
        let root = TestDirectory::new();
        let key = [13; 32];
        let initial = DraftStore::new_test(root.0.clone(), 1024 * 1024, key);
        let saved = initial
            .create(vec![ImageBuffer::from_pixel(32, 32, Rgba([4, 5, 6, 255]))])
            .unwrap();
        let used = directory_size(&root.0).unwrap();
        let constrained = DraftStore::new_test(root.0.clone(), used + 32, key);
        assert_eq!(
            constrained
                .create(vec![ImageBuffer::from_pixel(64, 64, Rgba([7, 8, 9, 255]),)])
                .unwrap_err(),
            CaptureError::QuotaExhausted
        );
        assert_eq!(constrained.open(saved.id).unwrap().id, saved.id);
    }

    #[test]
    fn flatten_quota_rejection_preserves_the_previous_output() {
        let root = TestDirectory::new();
        let key = [17; 32];
        let store = DraftStore::new_test(root.0.clone(), 1024 * 1024, key);
        let saved = store
            .create(vec![ImageBuffer::from_pixel(
                16,
                16,
                Rgba([10, 20, 30, 255]),
            )])
            .unwrap();
        store.flatten(saved.id, saved.revision).unwrap();
        let output_path = root
            .0
            .join(saved.id.to_string())
            .join(format!("flattened-{}.bin", saved.revision));
        let previous = fs::read(&output_path).unwrap();
        let constrained =
            DraftStore::new_test(root.0.clone(), directory_size(&root.0).unwrap() - 1, key);
        assert_eq!(
            constrained.flatten(saved.id, saved.revision).unwrap_err(),
            CaptureError::QuotaExhausted
        );
        assert_eq!(fs::read(output_path).unwrap(), previous);
    }

    #[test]
    fn multi_image_flatten_commit_failure_preserves_the_previous_bundle() {
        let root = TestDirectory::new();
        let key = [26; 32];
        let store = DraftStore::new_test(root.0.clone(), 1024 * 1024, key);
        let saved = store
            .create(vec![
                ImageBuffer::from_pixel(16, 16, Rgba([10, 20, 30, 255])),
                ImageBuffer::from_pixel(16, 16, Rgba([40, 50, 60, 255])),
            ])
            .unwrap();
        store.flatten(saved.id, saved.revision).unwrap();
        let output_path = root
            .0
            .join(saved.id.to_string())
            .join(format!("flattened-{}.bin", saved.revision));
        let previous_bundle = fs::read(&output_path).unwrap();
        let previous_assets = saved
            .images
            .iter()
            .map(|image| {
                store
                    .asset(saved.id, image.id, true, saved.revision)
                    .unwrap()
            })
            .collect::<Vec<_>>();

        assert_eq!(
            store
                .flatten_with_commit(saved.id, saved.revision, |_, _| {
                    Err(CaptureError::StorageFailure)
                })
                .unwrap_err(),
            CaptureError::StorageFailure
        );
        assert_eq!(fs::read(output_path).unwrap(), previous_bundle);
        for (image, previous) in saved.images.iter().zip(previous_assets) {
            assert_eq!(
                store
                    .asset(saved.id, image.id, true, saved.revision)
                    .unwrap(),
                previous
            );
        }
    }

    fn png_chunks(bytes: &[u8]) -> Vec<&str> {
        let mut offset = 8;
        let mut chunks = Vec::new();
        while offset + 12 <= bytes.len() {
            let length = u32::from_be_bytes(bytes[offset..offset + 4].try_into().unwrap()) as usize;
            let name = std::str::from_utf8(&bytes[offset + 4..offset + 8]).unwrap();
            chunks.push(name);
            offset += 12 + length;
        }
        chunks
    }
}
