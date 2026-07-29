use std::{
    fs::{self, OpenOptions},
    io::{self, Read, Write},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    thread::JoinHandle,
    time::Duration,
};

use base64::{Engine as _, engine::general_purpose::STANDARD as BASE64};
use interprocess::{
    ConnectWaitMode,
    local_socket::{
        GenericFilePath, GenericNamespaced, ListenerOptions, NameType as _, Stream, ToFsName as _,
        ToNsName as _,
        traits::{Listener as _, Stream as _},
    },
};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

const FIXTURE_EXTENSION_ID: &str = "neiiglibncgobmehenjkhicabgfpggff";
const HOST_STATE_DIRECTORY: &str = "realqa-native-host";
const PAIRING_FILE: &str = "pairing.v1.json";
const COMPOSER_ENDPOINT_FILE: &str = "composer-endpoint.v1.json";
const COMPOSER_SOCKET_PREFIX: &str = "dev.deli.devhud.realqa";
const MAX_EXTENSION_MESSAGE_BYTES: usize = 64 * 1024 * 1024;
const MAX_HOST_RESPONSE_BYTES: usize = 1024 * 1024;
const MAX_ENCODED_IMAGE_BYTES: usize = 25 * 1024 * 1024;
const COMPOSER_WAIT: Duration = Duration::from_secs(10);
const COMPOSER_CONNECT_WAIT: Duration = Duration::from_millis(500);
const COMPOSER_RESPONSE_WAIT: Duration = Duration::from_secs(10);

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) enum NativeHostFailure {
    OriginRejected,
    InvalidMessage,
    MessageTooLarge,
    ResponseTooLarge,
    PairingRejected,
    StateUnavailable,
    ComposerUnavailable,
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct PairingRecord {
    version: u8,
    extension_origin: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ComposerEndpointRecord {
    version: u8,
    token: String,
}

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct VisualBoundary {
    x: f64,
    y: f64,
    width: f64,
    height: f64,
}

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct ViewportDimensions {
    width: f64,
    height: f64,
    device_pixel_ratio: f64,
}

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct DomSelection {
    #[serde(skip_serializing_if = "Option::is_none")]
    boundary: Option<VisualBoundary>,
    #[serde(skip_serializing_if = "Option::is_none")]
    selector: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    tag: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    role: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    accessible_name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    viewport: Option<ViewportDimensions>,
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct PageMetadata {
    #[serde(skip_serializing_if = "Option::is_none")]
    url: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    title: Option<String>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum BrowserCaptureMode {
    VisibleViewport,
    OsCapture,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
pub(crate) enum BrowserImageMediaType {
    Png,
    Jpeg,
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct BrowserImage {
    media_type: BrowserImageMediaType,
    base64: String,
    encoded_bytes: usize,
}

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(
    tag = "kind",
    rename_all = "kebab-case",
    rename_all_fields = "camelCase",
    deny_unknown_fields
)]
pub(crate) enum NativeHostRequest {
    SubmitCapture {
        version: u8,
        request_id: String,
        capture_mode: BrowserCaptureMode,
        #[serde(skip_serializing_if = "Option::is_none")]
        page: Option<PageMetadata>,
        #[serde(skip_serializing_if = "Option::is_none")]
        image: Option<BrowserImage>,
        #[serde(skip_serializing_if = "Option::is_none")]
        selection: Option<DomSelection>,
    },
}

impl NativeHostRequest {
    pub(crate) fn request_id(&self) -> &str {
        match self {
            Self::SubmitCapture { request_id, .. } => request_id,
        }
    }

    #[cfg_attr(not(feature = "desktop-cef"), allow(dead_code))]
    pub(crate) fn encoded_image_bytes(&self) -> usize {
        match self {
            Self::SubmitCapture { image, .. } => {
                image.as_ref().map_or(0, |image| image.encoded_bytes)
            }
        }
    }

    fn validate(&mut self) -> Result<(), NativeHostFailure> {
        let Self::SubmitCapture {
            version,
            request_id,
            capture_mode,
            page,
            image,
            selection,
        } = self;
        if *version != 1 || Uuid::parse_str(request_id).is_err() {
            return Err(NativeHostFailure::InvalidMessage);
        }
        if let Some(page) = page {
            page.sanitize();
        }
        match capture_mode {
            BrowserCaptureMode::VisibleViewport => {
                image
                    .as_ref()
                    .ok_or(NativeHostFailure::InvalidMessage)?
                    .validate()?;
                if let Some(selection) = selection {
                    selection.validate()?;
                }
            }
            BrowserCaptureMode::OsCapture => {
                if image.is_some() || selection.is_some() {
                    return Err(NativeHostFailure::InvalidMessage);
                }
            }
        }
        Ok(())
    }
}

impl PageMetadata {
    fn sanitize(&mut self) {
        self.url = self.url.take().and_then(|value| sanitize_url(&value));
        self.title = self
            .title
            .take()
            .and_then(|value| sanitize_text(&value, 256));
    }
}

impl BrowserImage {
    fn validate(&self) -> Result<(), NativeHostFailure> {
        if self.base64.len() > 35 * 1024 * 1024 {
            return Err(NativeHostFailure::MessageTooLarge);
        }
        let decoded = BASE64
            .decode(&self.base64)
            .map_err(|_| NativeHostFailure::InvalidMessage)?;
        if decoded.len() != self.encoded_bytes || decoded.len() > MAX_ENCODED_IMAGE_BYTES {
            return Err(NativeHostFailure::MessageTooLarge);
        }
        let signature_matches = match self.media_type {
            BrowserImageMediaType::Png => decoded.starts_with(b"\x89PNG\r\n\x1a\n"),
            BrowserImageMediaType::Jpeg => {
                decoded.starts_with(&[0xff, 0xd8]) && decoded.ends_with(&[0xff, 0xd9])
            }
        };
        signature_matches
            .then_some(())
            .ok_or(NativeHostFailure::InvalidMessage)
    }
}

impl DomSelection {
    fn validate(&self) -> Result<(), NativeHostFailure> {
        if let Some(boundary) = &self.boundary {
            validate_rect(boundary.x, boundary.y, boundary.width, boundary.height)?;
        }
        if let Some(viewport) = &self.viewport {
            validate_rect(0.0, 0.0, viewport.width, viewport.height)?;
            if !viewport.device_pixel_ratio.is_finite()
                || viewport.device_pixel_ratio <= 0.0
                || viewport.device_pixel_ratio > 16.0
            {
                return Err(NativeHostFailure::InvalidMessage);
            }
        }
        if let (Some(boundary), Some(viewport)) = (&self.boundary, &self.viewport)
            && (boundary.x + boundary.width > viewport.width
                || boundary.y + boundary.height > viewport.height)
        {
            return Err(NativeHostFailure::InvalidMessage);
        }
        if self
            .selector
            .as_deref()
            .is_some_and(|selector| !safe_selector(selector))
        {
            return Err(NativeHostFailure::InvalidMessage);
        }
        if self
            .tag
            .as_deref()
            .is_some_and(|value| !safe_identifier(value, 32))
            || self
                .role
                .as_deref()
                .is_some_and(|value| !safe_identifier(value, 64))
            || self
                .accessible_name
                .as_deref()
                .is_some_and(|value| sanitize_text(value, 256).as_deref() != Some(value))
        {
            return Err(NativeHostFailure::InvalidMessage);
        }
        Ok(())
    }
}

fn validate_rect(x: f64, y: f64, width: f64, height: f64) -> Result<(), NativeHostFailure> {
    if [x, y, width, height].iter().all(|value| value.is_finite())
        && x >= 0.0
        && y >= 0.0
        && width > 0.0
        && height > 0.0
        && width <= 1_000_000.0
        && height <= 1_000_000.0
    {
        Ok(())
    } else {
        Err(NativeHostFailure::InvalidMessage)
    }
}

fn safe_identifier(value: &str, maximum: usize) -> bool {
    !value.is_empty()
        && value.len() <= maximum
        && value.as_bytes().first().is_some_and(u8::is_ascii_lowercase)
        && value.bytes().all(|byte| {
            byte.is_ascii_lowercase() || byte.is_ascii_digit() || matches!(byte, b'_' | b'-')
        })
}

fn safe_selector(value: &str) -> bool {
    if value.is_empty() || value.len() > 512 {
        return false;
    }
    let segments = value.split(" > ").collect::<Vec<_>>();
    if segments.is_empty() || segments.len() > 8 || segments.join(" > ") != value {
        return false;
    }
    segments.into_iter().all(|segment| {
        let suffix_start = segment.find(['#', '.', ':']).unwrap_or(segment.len());
        let (tag, suffix) = segment.split_at(suffix_start);
        if tag.is_empty()
            || tag.len() > 32
            || !tag
                .bytes()
                .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
            || !tag.as_bytes()[0].is_ascii_lowercase()
        {
            return false;
        }
        if suffix.is_empty() {
            return true;
        }
        if let Some(identifier) = suffix
            .strip_prefix('#')
            .or_else(|| suffix.strip_prefix('.'))
        {
            return identifier
                .as_bytes()
                .first()
                .is_some_and(|byte| byte.is_ascii_lowercase() || *byte == b'_')
                && identifier.len() <= 64
                && identifier.bytes().all(|byte| {
                    byte.is_ascii_lowercase()
                        || byte.is_ascii_digit()
                        || matches!(byte, b'_' | b'-')
                });
        }
        suffix
            .strip_prefix(":nth-of-type(")
            .and_then(|value| value.strip_suffix(')'))
            .and_then(|value| value.parse::<u32>().ok())
            .is_some_and(|position| (1..=99_999).contains(&position))
    })
}

fn sanitize_text(value: &str, maximum: usize) -> Option<String> {
    let normalized = value
        .chars()
        .map(|character| {
            if character.is_control() {
                ' '
            } else {
                character
            }
        })
        .collect::<String>()
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ");
    (!normalized.is_empty()).then(|| normalized.chars().take(maximum).collect())
}

fn sanitize_url(value: &str) -> Option<String> {
    if value.len() > 4096 || value.chars().any(char::is_control) {
        return None;
    }
    let mut url = url::Url::parse(value).ok()?;
    let _ = url.set_username("");
    let _ = url.set_password(None);
    url.set_query(None);
    url.set_fragment(None);
    let sanitized = url.to_string();
    (sanitized.len() <= 2048).then_some(sanitized)
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct NativeHostResponse<'a> {
    version: u8,
    request_id: &'a str,
    status: NativeHostStatus,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
enum NativeHostStatus {
    Accepted,
    Rejected,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(
    tag = "kind",
    rename_all = "kebab-case",
    rename_all_fields = "camelCase",
    deny_unknown_fields
)]
enum ComposerIpcRequest {
    Ping {
        version: u8,
    },
    SubmitCapture {
        version: u8,
        capture: Box<NativeHostRequest>,
    },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
enum ComposerIpcResponse {
    Ready,
    Accepted,
    Rejected,
}

pub(crate) trait ComposerDelivery {
    fn is_ready(&self) -> bool;
    fn launch(&self) -> Result<(), NativeHostFailure>;
    fn wait_until_ready(&self, timeout: Duration) -> Result<(), NativeHostFailure>;
    fn enqueue(&self, request: &NativeHostRequest) -> Result<(), NativeHostFailure>;
}

#[derive(Clone)]
pub(crate) struct SocketComposerDelivery {
    root: PathBuf,
}

impl SocketComposerDelivery {
    fn new(root: PathBuf) -> Self {
        Self { root }
    }

    fn app_path(&self) -> Result<PathBuf, NativeHostFailure> {
        if let Some(path) = std::env::var_os("DEVHUD_APPLICATION_PATH") {
            return Ok(PathBuf::from(path));
        }
        let current =
            std::env::current_exe().map_err(|_| NativeHostFailure::ComposerUnavailable)?;
        let parent = current
            .parent()
            .ok_or(NativeHostFailure::ComposerUnavailable)?;
        Ok(parent.join(if cfg!(target_os = "windows") {
            "devhud.exe"
        } else {
            "devhud"
        }))
    }

    fn endpoint(&self) -> Result<ComposerEndpointRecord, NativeHostFailure> {
        let bytes = fs::read(self.root.join(COMPOSER_ENDPOINT_FILE))
            .map_err(|_| NativeHostFailure::ComposerUnavailable)?;
        let endpoint: ComposerEndpointRecord =
            serde_json::from_slice(&bytes).map_err(|_| NativeHostFailure::ComposerUnavailable)?;
        if endpoint.version != 1 || Uuid::parse_str(&endpoint.token).is_err() {
            return Err(NativeHostFailure::ComposerUnavailable);
        }
        Ok(endpoint)
    }

    fn exchange(
        &self,
        request: &ComposerIpcRequest,
    ) -> Result<ComposerIpcResponse, NativeHostFailure> {
        let endpoint = self.endpoint()?;
        let name = composer_socket_name(&endpoint)?;
        let mut stream = interprocess::local_socket::ConnectOptions::new()
            .name(name)
            .wait_mode(ConnectWaitMode::Timeout(COMPOSER_CONNECT_WAIT))
            .connect_sync()
            .map_err(|_| NativeHostFailure::ComposerUnavailable)?;
        let response_wait = match request {
            ComposerIpcRequest::Ping { .. } => COMPOSER_CONNECT_WAIT,
            ComposerIpcRequest::SubmitCapture { .. } => COMPOSER_RESPONSE_WAIT,
        };
        stream
            .set_recv_timeout(Some(response_wait))
            .and_then(|()| stream.set_send_timeout(Some(COMPOSER_CONNECT_WAIT)))
            .map_err(|_| NativeHostFailure::ComposerUnavailable)?;
        write_ipc_frame(&mut stream, request, MAX_EXTENSION_MESSAGE_BYTES)?;
        read_ipc_frame(&mut stream, MAX_HOST_RESPONSE_BYTES)
    }
}

impl ComposerDelivery for SocketComposerDelivery {
    fn is_ready(&self) -> bool {
        self.exchange(&ComposerIpcRequest::Ping { version: 1 })
            .is_ok_and(|response| response == ComposerIpcResponse::Ready)
    }

    fn launch(&self) -> Result<(), NativeHostFailure> {
        Command::new(self.app_path()?)
            .arg("--realqa-composer")
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .map(|_| ())
            .map_err(|_| NativeHostFailure::ComposerUnavailable)
    }

    fn wait_until_ready(&self, timeout: Duration) -> Result<(), NativeHostFailure> {
        let started = std::time::Instant::now();
        while started.elapsed() < timeout {
            if self.is_ready() {
                return Ok(());
            }
            std::thread::sleep(Duration::from_millis(50));
        }
        Err(NativeHostFailure::ComposerUnavailable)
    }

    fn enqueue(&self, request: &NativeHostRequest) -> Result<(), NativeHostFailure> {
        let response = self.exchange(&ComposerIpcRequest::SubmitCapture {
            version: 1,
            capture: Box::new(request.clone()),
        })?;
        (response == ComposerIpcResponse::Accepted)
            .then_some(())
            .ok_or(NativeHostFailure::ComposerUnavailable)
    }
}

fn composer_socket_name(
    endpoint: &ComposerEndpointRecord,
) -> Result<interprocess::local_socket::Name<'static>, NativeHostFailure> {
    if GenericNamespaced::is_supported() {
        format!("{COMPOSER_SOCKET_PREFIX}.{}", endpoint.token)
            .to_ns_name::<GenericNamespaced>()
            .map_err(|_| NativeHostFailure::StateUnavailable)
    } else {
        let token = endpoint.token.replace('-', "");
        std::env::temp_dir()
            .join(format!("dhrq-{token}"))
            .to_fs_name::<GenericFilePath>()
            .map_err(|_| NativeHostFailure::StateUnavailable)
    }
}

fn write_ipc_frame(
    stream: &mut Stream,
    value: &impl Serialize,
    maximum: usize,
) -> Result<(), NativeHostFailure> {
    let payload = serde_json::to_vec(value).map_err(|_| NativeHostFailure::InvalidMessage)?;
    if payload.is_empty() || payload.len() >= maximum {
        return Err(NativeHostFailure::MessageTooLarge);
    }
    let length = u32::try_from(payload.len()).map_err(|_| NativeHostFailure::MessageTooLarge)?;
    stream
        .write_all(&length.to_ne_bytes())
        .and_then(|()| stream.write_all(&payload))
        .and_then(|()| stream.flush())
        .map_err(|_| NativeHostFailure::ComposerUnavailable)
}

fn read_ipc_frame<T: for<'de> Deserialize<'de>>(
    stream: &mut Stream,
    maximum: usize,
) -> Result<T, NativeHostFailure> {
    let mut length = [0_u8; 4];
    stream
        .read_exact(&mut length)
        .map_err(|_| NativeHostFailure::ComposerUnavailable)?;
    let length = u32::from_ne_bytes(length) as usize;
    if length == 0 || length >= maximum {
        return Err(NativeHostFailure::MessageTooLarge);
    }
    let mut payload = vec![0; length];
    stream
        .read_exact(&mut payload)
        .map_err(|_| NativeHostFailure::ComposerUnavailable)?;
    serde_json::from_slice(&payload).map_err(|_| NativeHostFailure::InvalidMessage)
}

fn deliver_capture(
    delivery: &dyn ComposerDelivery,
    request: &NativeHostRequest,
) -> Result<(), NativeHostFailure> {
    if !delivery.is_ready() {
        delivery.launch()?;
        delivery.wait_until_ready(COMPOSER_WAIT)?;
    }
    delivery.enqueue(request)
}

#[derive(Debug, Clone)]
pub(crate) struct NativeHostState {
    root: PathBuf,
}

pub(crate) struct ComposerReadyGuard {
    path: PathBuf,
    shutdown: Arc<AtomicBool>,
    thread: Option<JoinHandle<()>>,
    wake: SocketComposerDelivery,
}

impl Drop for ComposerReadyGuard {
    fn drop(&mut self) {
        self.shutdown.store(true, Ordering::Release);
        let _ = self.wake.exchange(&ComposerIpcRequest::Ping { version: 1 });
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
        let _ = fs::remove_file(&self.path);
    }
}

impl NativeHostState {
    pub(crate) fn new(application_data: &Path) -> Result<Self, NativeHostFailure> {
        let root = application_data.join(HOST_STATE_DIRECTORY);
        create_private_directory(&root).map_err(|_| NativeHostFailure::StateUnavailable)?;
        Ok(Self { root })
    }

    pub(crate) fn platform() -> Result<Self, NativeHostFailure> {
        let application_data = dirs::data_local_dir()
            .ok_or(NativeHostFailure::StateUnavailable)?
            .join(crate::APPLICATION_ID);
        Self::new(&application_data)
    }

    fn ensure_paired(&self, origin: &str) -> Result<(), NativeHostFailure> {
        let pairing_path = self.root.join(PAIRING_FILE);
        match fs::read(&pairing_path) {
            Ok(bytes) => {
                let pairing: PairingRecord = serde_json::from_slice(&bytes)
                    .map_err(|_| NativeHostFailure::PairingRejected)?;
                if pairing.version == 1 && pairing.extension_origin == origin {
                    Ok(())
                } else {
                    Err(NativeHostFailure::PairingRejected)
                }
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                let pairing = PairingRecord {
                    version: 1,
                    extension_origin: origin.to_owned(),
                };
                let bytes = serde_json::to_vec(&pairing)
                    .map_err(|_| NativeHostFailure::StateUnavailable)?;
                write_private_new_file(&pairing_path, &bytes)
                    .map_err(|_| NativeHostFailure::StateUnavailable)
            }
            Err(_) => Err(NativeHostFailure::StateUnavailable),
        }
    }

    #[cfg_attr(not(any(feature = "desktop-cef", test)), allow(dead_code))]
    pub(crate) fn start_composer_listener(
        &self,
        handler: impl Fn(NativeHostRequest) -> Result<(), NativeHostFailure> + Send + Sync + 'static,
    ) -> Result<ComposerReadyGuard, NativeHostFailure> {
        let endpoint = ComposerEndpointRecord {
            version: 1,
            token: Uuid::now_v7().to_string(),
        };
        let name = composer_socket_name(&endpoint)?;
        let options = ListenerOptions::new().name(name);
        #[cfg(unix)]
        let options = {
            use interprocess::os::unix::local_socket::ListenerOptionsExt as _;
            options.mode(0o600)
        };
        let listener = options
            .create_sync()
            .map_err(|_| NativeHostFailure::StateUnavailable)?;
        let path = self.root.join(COMPOSER_ENDPOINT_FILE);
        remove_file_if_present(&path)?;
        let bytes =
            serde_json::to_vec(&endpoint).map_err(|_| NativeHostFailure::StateUnavailable)?;
        write_private_new_file(&path, &bytes).map_err(|_| NativeHostFailure::StateUnavailable)?;
        let handler = Arc::new(handler);
        let shutdown = Arc::new(AtomicBool::new(false));
        let shutdown_for_thread = shutdown.clone();
        let thread = std::thread::Builder::new()
            .name("devhud-realqa-composer-ipc".to_owned())
            .spawn(move || {
                while let Ok(stream) = listener.accept() {
                    if shutdown_for_thread.load(Ordering::Acquire) {
                        break;
                    }
                    let _ = handle_composer_connection(stream, handler.as_ref());
                }
            });
        let thread = thread.map_err(|_| {
            let _ = fs::remove_file(&path);
            NativeHostFailure::StateUnavailable
        })?;
        Ok(ComposerReadyGuard {
            path,
            shutdown,
            thread: Some(thread),
            wake: SocketComposerDelivery::new(self.root.clone()),
        })
    }

    #[cfg_attr(not(any(feature = "desktop-cef", test)), allow(dead_code))]
    pub(crate) fn preflight_reset(&self) -> Result<(), NativeHostFailure> {
        let metadata =
            fs::symlink_metadata(&self.root).map_err(|_| NativeHostFailure::StateUnavailable)?;
        if metadata.file_type().is_symlink() || !metadata.is_dir() {
            return Err(NativeHostFailure::StateUnavailable);
        }
        for path in [
            self.root.join(PAIRING_FILE),
            self.root.join(COMPOSER_ENDPOINT_FILE),
        ] {
            match fs::symlink_metadata(path) {
                Ok(metadata) if metadata.is_file() && !metadata.file_type().is_symlink() => {}
                Ok(_) => return Err(NativeHostFailure::StateUnavailable),
                Err(error) if error.kind() == io::ErrorKind::NotFound => {}
                Err(_) => return Err(NativeHostFailure::StateUnavailable),
            }
        }
        Ok(())
    }

    #[cfg_attr(not(any(feature = "desktop-cef", test)), allow(dead_code))]
    pub(crate) fn reset(&self) -> Result<(), NativeHostFailure> {
        self.preflight_reset()?;
        remove_file_if_present(&self.root.join(PAIRING_FILE))?;
        Ok(())
    }
}

fn handle_composer_connection(
    mut stream: Stream,
    handler: &dyn Fn(NativeHostRequest) -> Result<(), NativeHostFailure>,
) -> Result<(), NativeHostFailure> {
    stream
        .set_recv_timeout(Some(COMPOSER_CONNECT_WAIT))
        .and_then(|()| stream.set_send_timeout(Some(COMPOSER_RESPONSE_WAIT)))
        .map_err(|_| NativeHostFailure::ComposerUnavailable)?;
    let request: ComposerIpcRequest = read_ipc_frame(&mut stream, MAX_EXTENSION_MESSAGE_BYTES)?;
    let response = match request {
        ComposerIpcRequest::Ping { version: 1 } => ComposerIpcResponse::Ready,
        ComposerIpcRequest::SubmitCapture {
            version: 1,
            mut capture,
        } => match capture.validate().and_then(|()| handler(*capture)) {
            Ok(()) => ComposerIpcResponse::Accepted,
            Err(_) => ComposerIpcResponse::Rejected,
        },
        ComposerIpcRequest::Ping { .. } | ComposerIpcRequest::SubmitCapture { .. } => {
            ComposerIpcResponse::Rejected
        }
    };
    write_ipc_frame(&mut stream, &response, MAX_HOST_RESPONSE_BYTES)
}

#[cfg_attr(not(any(feature = "desktop-cef", test)), allow(dead_code))]
fn remove_file_if_present(path: &Path) -> Result<(), NativeHostFailure> {
    match fs::remove_file(path) {
        Ok(()) => Ok(()),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(_) => Err(NativeHostFailure::StateUnavailable),
    }
}

fn create_private_directory(path: &Path) -> io::Result<()> {
    fs::create_dir_all(path)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt as _;
        fs::set_permissions(path, fs::Permissions::from_mode(0o700))?;
    }
    Ok(())
}

fn write_private_new_file(path: &Path, bytes: &[u8]) -> io::Result<()> {
    let mut options = OpenOptions::new();
    options.create_new(true).write(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt as _;
        options.mode(0o600);
    }
    let mut file = options.open(path)?;
    file.write_all(bytes)?;
    file.sync_all()
}

fn allowed_extension_origin() -> Result<String, NativeHostFailure> {
    let id = option_env!("DEVHUD_CHROME_EXTENSION_ID")
        .or(if cfg!(debug_assertions) {
            Some(FIXTURE_EXTENSION_ID)
        } else {
            None
        })
        .ok_or(NativeHostFailure::OriginRejected)?;
    if id.len() != 32 || !id.bytes().all(|byte| matches!(byte, b'a'..=b'p')) {
        return Err(NativeHostFailure::OriginRejected);
    }
    Ok(format!("chrome-extension://{id}/"))
}

fn validate_origin(origin: &str) -> Result<(), NativeHostFailure> {
    if origin == allowed_extension_origin()? {
        Ok(())
    } else {
        Err(NativeHostFailure::OriginRejected)
    }
}

fn read_frame(reader: &mut impl Read) -> Result<Option<Vec<u8>>, NativeHostFailure> {
    let mut length = [0_u8; 4];
    let mut read = 0;
    while read < length.len() {
        match reader.read(&mut length[read..]) {
            Ok(0) if read == 0 => return Ok(None),
            Ok(0) => return Err(NativeHostFailure::InvalidMessage),
            Ok(count) => read += count,
            Err(error) if error.kind() == io::ErrorKind::Interrupted => {}
            Err(_) => return Err(NativeHostFailure::InvalidMessage),
        }
    }
    let length = u32::from_ne_bytes(length) as usize;
    if length == 0 || length >= MAX_EXTENSION_MESSAGE_BYTES {
        return Err(NativeHostFailure::MessageTooLarge);
    }
    let mut payload = vec![0; length];
    reader
        .read_exact(&mut payload)
        .map_err(|_| NativeHostFailure::InvalidMessage)?;
    Ok(Some(payload))
}

fn write_frame(writer: &mut impl Write, payload: &[u8]) -> Result<(), NativeHostFailure> {
    if payload.len() >= MAX_HOST_RESPONSE_BYTES {
        return Err(NativeHostFailure::ResponseTooLarge);
    }
    let length = u32::try_from(payload.len()).map_err(|_| NativeHostFailure::ResponseTooLarge)?;
    writer
        .write_all(&length.to_ne_bytes())
        .and_then(|()| writer.write_all(payload))
        .and_then(|()| writer.flush())
        .map_err(|_| NativeHostFailure::StateUnavailable)
}

fn run_host(
    origin: &str,
    reader: &mut impl Read,
    writer: &mut impl Write,
    state: &NativeHostState,
    delivery: &dyn ComposerDelivery,
) -> Result<(), NativeHostFailure> {
    validate_origin(origin)?;
    state.ensure_paired(origin)?;
    while let Some(payload) = read_frame(reader)? {
        let mut request: NativeHostRequest =
            serde_json::from_slice(&payload).map_err(|_| NativeHostFailure::InvalidMessage)?;
        request.validate()?;
        let status = if deliver_capture(delivery, &request).is_ok() {
            NativeHostStatus::Accepted
        } else {
            NativeHostStatus::Rejected
        };
        let response = NativeHostResponse {
            version: 1,
            request_id: request.request_id(),
            status,
        };
        let response =
            serde_json::to_vec(&response).map_err(|_| NativeHostFailure::ResponseTooLarge)?;
        write_frame(writer, &response)?;
    }
    Ok(())
}

pub fn run_native_host() {
    #[cfg(target_os = "windows")]
    set_windows_binary_stdio();
    let Some(origin) = std::env::args().nth(1) else {
        std::process::exit(64);
    };
    let Ok(state) = NativeHostState::platform() else {
        std::process::exit(70);
    };
    let delivery = SocketComposerDelivery::new(state.root.clone());
    if run_host(
        &origin,
        &mut io::stdin().lock(),
        &mut io::stdout().lock(),
        &state,
        &delivery,
    )
    .is_err()
    {
        std::process::exit(65);
    }
}

#[cfg(target_os = "windows")]
fn set_windows_binary_stdio() {
    unsafe extern "C" {
        fn _setmode(file_descriptor: i32, mode: i32) -> i32;
    }
    const BINARY: i32 = 0x8000;
    // Chrome framing is binary; Windows text mode would rewrite newlines.
    unsafe {
        _setmode(0, BINARY);
        _setmode(1, BINARY);
    }
}

#[cfg(test)]
mod tests {
    use std::{
        cell::{Cell, RefCell},
        sync::{Arc, Mutex},
    };

    use super::*;

    struct FixtureDelivery {
        ready: Cell<bool>,
        launches: Cell<usize>,
        queued: RefCell<Vec<String>>,
        ready_after_wait: bool,
    }

    impl FixtureDelivery {
        fn ready() -> Self {
            Self {
                ready: Cell::new(true),
                launches: Cell::new(0),
                queued: RefCell::new(Vec::new()),
                ready_after_wait: true,
            }
        }
    }

    impl ComposerDelivery for FixtureDelivery {
        fn is_ready(&self) -> bool {
            self.ready.get()
        }

        fn launch(&self) -> Result<(), NativeHostFailure> {
            self.launches.set(self.launches.get() + 1);
            Ok(())
        }

        fn wait_until_ready(&self, _timeout: Duration) -> Result<(), NativeHostFailure> {
            if self.ready_after_wait {
                self.ready.set(true);
                Ok(())
            } else {
                Err(NativeHostFailure::ComposerUnavailable)
            }
        }

        fn enqueue(&self, request: &NativeHostRequest) -> Result<(), NativeHostFailure> {
            self.queued
                .borrow_mut()
                .push(request.request_id().to_owned());
            Ok(())
        }
    }

    fn temp_directory(name: &str) -> PathBuf {
        let path = std::env::temp_dir().join(format!(
            "devhud-native-host-{name}-{}-{}",
            std::process::id(),
            Uuid::now_v7()
        ));
        fs::create_dir_all(&path).unwrap();
        path
    }

    fn request(mode: BrowserCaptureMode) -> NativeHostRequest {
        let png = b"\x89PNG\r\n\x1a\nfixture";
        NativeHostRequest::SubmitCapture {
            version: 1,
            request_id: Uuid::now_v7().to_string(),
            capture_mode: mode,
            page: Some(PageMetadata {
                url: Some("https://example.com/path?secret=value".to_owned()),
                title: Some("Example".to_owned()),
            }),
            image: (mode == BrowserCaptureMode::VisibleViewport).then(|| BrowserImage {
                media_type: BrowserImageMediaType::Png,
                base64: BASE64.encode(png),
                encoded_bytes: png.len(),
            }),
            selection: None,
        }
    }

    fn framed(request: &NativeHostRequest) -> Vec<u8> {
        let payload = serde_json::to_vec(request).unwrap();
        let length = u32::try_from(payload.len()).unwrap();
        let mut framed = length.to_ne_bytes().to_vec();
        framed.extend(payload);
        framed
    }

    #[test]
    fn rejects_every_origin_except_the_exact_allowed_extension_origin() {
        assert_eq!(
            validate_origin("chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"),
            Err(NativeHostFailure::OriginRejected)
        );
        assert!(validate_origin(&allowed_extension_origin().unwrap()).is_ok());
    }

    #[test]
    fn redacts_urls_and_rejects_unknown_page_content() {
        let mut request = request(BrowserCaptureMode::VisibleViewport);
        request.validate().unwrap();
        let NativeHostRequest::SubmitCapture { page, .. } = request;
        assert_eq!(
            page.unwrap().url.as_deref(),
            Some("https://example.com/path")
        );
        let payload = serde_json::json!({
            "kind": "submit-capture",
            "version": 1,
            "requestId": Uuid::now_v7().to_string(),
            "captureMode": "os-capture",
            "page": {"url": "https://example.com", "html": "<p>secret</p>"}
        });
        assert!(serde_json::from_value::<NativeHostRequest>(payload).is_err());
        assert!(!safe_selector("body page-text"));
        assert!(!safe_selector("div[data-secret]"));
        assert!(safe_selector("body > main#content > button:nth-of-type(2)"));

        let mut outside_viewport: NativeHostRequest = serde_json::from_value(serde_json::json!({
            "kind": "submit-capture",
            "version": 1,
            "requestId": Uuid::now_v7().to_string(),
            "captureMode": "visible-viewport",
            "image": {
                "mediaType": "png",
                "base64": BASE64.encode(b"\x89PNG\r\n\x1a\nfixture"),
                "encodedBytes": 15
            },
            "selection": {
                "boundary": {"x": 790, "y": 590, "width": 20, "height": 20},
                "viewport": {"width": 800, "height": 600, "devicePixelRatio": 1}
            }
        }))
        .unwrap();
        assert_eq!(
            outside_viewport.validate(),
            Err(NativeHostFailure::InvalidMessage)
        );
    }

    #[test]
    fn rejects_full_page_incognito_and_invalid_image_shapes() {
        for payload in [
            serde_json::json!({
                "kind": "submit-capture", "version": 1,
                "requestId": Uuid::now_v7().to_string(), "captureMode": "full-page"
            }),
            serde_json::json!({
                "kind": "submit-capture", "version": 1,
                "requestId": Uuid::now_v7().to_string(), "captureMode": "os-capture",
                "incognito": true
            }),
        ] {
            assert!(serde_json::from_value::<NativeHostRequest>(payload).is_err());
        }
        let image = BrowserImage {
            media_type: BrowserImageMediaType::Png,
            base64: BASE64.encode(b"\x89PNG\r\n\x1a\n"),
            encoded_bytes: MAX_ENCODED_IMAGE_BYTES + 1,
        };
        assert_eq!(image.validate(), Err(NativeHostFailure::MessageTooLarge));
    }

    #[test]
    fn launches_when_absent_waits_and_queues_once() {
        let delivery = FixtureDelivery {
            ready: Cell::new(false),
            launches: Cell::new(0),
            queued: RefCell::new(Vec::new()),
            ready_after_wait: true,
        };
        let request = request(BrowserCaptureMode::OsCapture);
        deliver_capture(&delivery, &request).unwrap();
        assert_eq!(delivery.launches.get(), 1);
        assert_eq!(delivery.queued.borrow().as_slice(), [request.request_id()]);
    }

    #[test]
    fn framing_and_response_limits_are_enforced() {
        let oversized_length = (MAX_EXTENSION_MESSAGE_BYTES as u32).to_ne_bytes();
        let mut oversized = oversized_length.as_slice();
        assert_eq!(
            read_frame(&mut oversized),
            Err(NativeHostFailure::MessageTooLarge)
        );
        assert_eq!(
            write_frame(&mut Vec::new(), &vec![0; MAX_HOST_RESPONSE_BYTES]),
            Err(NativeHostFailure::ResponseTooLarge)
        );
        let root = temp_directory("framing");
        let state = NativeHostState::new(&root).unwrap();
        let request = request(BrowserCaptureMode::VisibleViewport);
        let bytes = framed(&request);
        let mut input = bytes.as_slice();
        let mut output = Vec::new();
        run_host(
            &allowed_extension_origin().unwrap(),
            &mut input,
            &mut output,
            &state,
            &FixtureDelivery::ready(),
        )
        .unwrap();
        assert!(output.len() < MAX_HOST_RESPONSE_BYTES);
        state.reset().unwrap();
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn pairing_rejects_origin_change_and_reset_preserves_the_live_endpoint() {
        let root = temp_directory("pairing");
        let state = NativeHostState::new(&root).unwrap();
        state
            .ensure_paired(&allowed_extension_origin().unwrap())
            .unwrap();
        assert_eq!(
            state.ensure_paired("chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"),
            Err(NativeHostFailure::PairingRejected)
        );
        let accepted = Arc::new(Mutex::new(Vec::new()));
        let accepted_for_handler = accepted.clone();
        let ready = state
            .start_composer_listener(move |capture| {
                accepted_for_handler
                    .lock()
                    .map_err(|_| NativeHostFailure::ComposerUnavailable)?
                    .push(capture.request_id().to_owned());
                Ok(())
            })
            .unwrap();
        let delivery = SocketComposerDelivery::new(state.root.clone());
        assert!(delivery.is_ready());
        let capture = request(BrowserCaptureMode::VisibleViewport);
        delivery.enqueue(&capture).unwrap();
        assert_eq!(accepted.lock().unwrap().as_slice(), [capture.request_id()]);
        let endpoint = fs::read(state.root.join(COMPOSER_ENDPOINT_FILE)).unwrap();
        assert!(!String::from_utf8_lossy(&endpoint).contains("iVBOR"));
        state.reset().unwrap();
        assert!(!state.root.join(PAIRING_FILE).exists());
        assert!(state.root.join(COMPOSER_ENDPOINT_FILE).exists());
        drop(ready);
        assert!(!state.root.join(COMPOSER_ENDPOINT_FILE).exists());
        fs::remove_dir_all(root).unwrap();
    }
}
