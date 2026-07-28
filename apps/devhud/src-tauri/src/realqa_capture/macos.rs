#[cfg(target_os = "macos")]
use std::ffi::{CStr, c_char, c_int, c_void};
use std::{
    collections::HashMap,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
};

#[cfg(target_os = "macos")]
use serde::Deserialize;

use super::{
    BackendFailure, BackendFrame, CaptureBackend, CapturePermission, CapturePermissionGuidance,
    CapturePermissionStatus, CapturePlatform, CaptureSessionId, CaptureSourceSelection,
    DisplayDescriptor, DisplayId, DisplaySnapshot, LogicalRect, PointerInclusion,
    ResolvedCaptureRequest, WindowAvailability, WindowMetadata, WindowSource, WindowSourceId,
    decoded_byte_len,
    geometry::{PhysicalSize, PixelRect, ScaleFactor},
};

const DISPLAY_ID_PREFIX: &str = "macos-display-";
const WINDOW_ID_PREFIX: &str = "macos-window-";
#[cfg(target_os = "macos")]
const MAX_CATALOG_BYTES: usize = 4 * 1024 * 1024;
const MAX_PROCESS_NAME_CHARS: usize = 128;
const MAX_WINDOW_TITLE_CHARS: usize = 256;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum NativePermission {
    Granted,
    PromptRequired,
    Denied,
}

#[derive(Debug, Clone, PartialEq)]
pub(crate) struct NativeDisplay {
    pub(crate) id: u32,
    pub(crate) frame: LogicalRect,
    pub(crate) width: u32,
    pub(crate) height: u32,
    pub(crate) primary: bool,
}

#[derive(Debug, Clone, PartialEq)]
pub(crate) struct NativeWindow {
    pub(crate) id: u32,
    pub(crate) frame: LogicalRect,
    pub(crate) on_screen: bool,
    pub(crate) process_name: Option<String>,
    pub(crate) title: Option<String>,
}

#[derive(Debug, Clone, PartialEq)]
pub(crate) struct NativeCatalog {
    pub(crate) displays: Vec<NativeDisplay>,
    pub(crate) windows: Vec<NativeWindow>,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub(crate) enum NativeCaptureSource {
    Display { id: u32, source_rect: LogicalRect },
    Window { id: u32 },
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub(crate) struct NativeCaptureRequest {
    pub(crate) source: NativeCaptureSource,
    pub(crate) width: u32,
    pub(crate) height: u32,
    pub(crate) pointer: PointerInclusion,
}

pub(crate) trait MacosNativeAdapter: Send + Sync {
    fn permission(&self) -> Result<NativePermission, BackendFailure>;
    fn request_permission(&self) -> Result<NativePermission, BackendFailure>;
    fn catalog(&self) -> Result<NativeCatalog, BackendFailure>;
    fn capture(&self, request: NativeCaptureRequest) -> Result<BackendFrame, BackendFailure>;
}

pub(crate) struct MacosCaptureBackend<A: MacosNativeAdapter> {
    native: A,
    catalog: Mutex<Option<CachedCatalog>>,
    active: Mutex<HashMap<CaptureSessionId, Arc<AtomicBool>>>,
}

#[derive(Debug, Clone)]
struct CachedCatalog {
    snapshot_id: super::DisplaySnapshotId,
    windows: Vec<WindowSource>,
}

impl<A: MacosNativeAdapter> MacosCaptureBackend<A> {
    pub(crate) fn new(native: A) -> Self {
        Self {
            native,
            catalog: Mutex::new(None),
            active: Mutex::new(HashMap::new()),
        }
    }

    fn translated_catalog(
        &self,
        native: NativeCatalog,
    ) -> Result<(Vec<DisplayDescriptor>, Vec<WindowSource>), BackendFailure> {
        if native.displays.is_empty() {
            return Err(BackendFailure::DisplayChanged);
        }
        let mut displays = native
            .displays
            .into_iter()
            .map(|display| {
                let scale = scale_from_dimensions(&display)?;
                Ok(DisplayDescriptor {
                    id: display_id(display.id),
                    logical_bounds: display.frame,
                    physical_size: PhysicalSize {
                        width: display.width,
                        height: display.height,
                    },
                    scale,
                    primary: display.primary,
                })
            })
            .collect::<Result<Vec<_>, BackendFailure>>()?;
        displays.sort_by(|left, right| left.id.0.cmp(&right.id.0));
        let windows = native
            .windows
            .into_iter()
            .filter_map(|window| translate_window(window, &displays))
            .collect();
        Ok((displays, windows))
    }

    fn begin_session(
        &self,
        session_id: &CaptureSessionId,
    ) -> Result<ActiveCapture<'_, A>, BackendFailure> {
        let cancelled = Arc::new(AtomicBool::new(false));
        let mut active = self
            .active
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?;
        if active.contains_key(session_id) {
            return Err(BackendFailure::CaptureFailed);
        }
        active.insert(session_id.clone(), cancelled.clone());
        Ok(ActiveCapture {
            backend: self,
            session_id: session_id.clone(),
            cancelled,
        })
    }

    fn capture_display_regions(
        &self,
        request: &ResolvedCaptureRequest,
        active: &ActiveCapture<'_, A>,
    ) -> Result<BackendFrame, BackendFailure> {
        let mut canvas = BackendFrame {
            width: request.expected_frame_size.width,
            height: request.expected_frame_size.height,
            rgba: vec![
                0;
                decoded_byte_len(
                    request.expected_frame_size.width,
                    request.expected_frame_size.height,
                )
                .map_err(|_| BackendFailure::CaptureFailed)?
            ],
        };
        let output_scale = request
            .pixel_regions
            .iter()
            .filter_map(|region| {
                request
                    .snapshot
                    .display(&region.display_id)
                    .map(|display| display.scale)
            })
            .max_by(|left, right| {
                (u64::from(left.numerator) * u64::from(right.denominator))
                    .cmp(&(u64::from(right.numerator) * u64::from(left.denominator)))
            })
            .ok_or(BackendFailure::DisplayChanged)?;

        for region in &request.pixel_regions {
            active.check_cancelled()?;
            let display = request
                .snapshot
                .display(&region.display_id)
                .ok_or(BackendFailure::DisplayChanged)?;
            let native_id = parse_source_id(&display.id.0, DISPLAY_ID_PREFIX)
                .ok_or(BackendFailure::DisplayChanged)?;
            let rounded_source_rect = pixel_rect_to_logical(region.pixels, display.scale)?;
            let rounded_global = LogicalRect {
                x: display.logical_bounds.x + rounded_source_rect.x,
                y: display.logical_bounds.y + rounded_source_rect.y,
                width: rounded_source_rect.width,
                height: rounded_source_rect.height,
            };
            let exact_global = request
                .logical_bounds
                .intersection(display.logical_bounds)
                .filter(|intersection| {
                    display.pixel_region(*intersection).ok().flatten().as_ref() == Some(region)
                })
                .unwrap_or(rounded_global);
            let source_rect = LogicalRect {
                x: exact_global.x - display.logical_bounds.x,
                y: exact_global.y - display.logical_bounds.y,
                width: exact_global.width,
                height: exact_global.height,
            };
            let captured = self.capture_native(
                NativeCaptureRequest {
                    source: NativeCaptureSource::Display {
                        id: native_id,
                        source_rect,
                    },
                    width: region.pixels.width,
                    height: region.pixels.height,
                    pointer: request.pointer,
                },
                active,
            )?;
            validate_native_frame(&captured, region.pixels.width, region.pixels.height)?;

            let destination =
                destination_rect(exact_global, request.logical_bounds, output_scale, &canvas)?;
            blit_scaled(&captured, &mut canvas, destination)?;
        }
        active.check_cancelled()?;
        Ok(canvas)
    }

    fn capture_native(
        &self,
        request: NativeCaptureRequest,
        active: &ActiveCapture<'_, A>,
    ) -> Result<BackendFrame, BackendFailure> {
        let result = self.native.capture(request);
        active.check_cancelled()?;
        result
    }
}

impl<A: MacosNativeAdapter> CaptureBackend for MacosCaptureBackend<A> {
    fn platform(&self) -> CapturePlatform {
        CapturePlatform::Macos
    }

    fn permission(&self) -> Result<CapturePermission, BackendFailure> {
        self.native.permission().map(translate_permission)
    }

    fn permission_status(&self) -> Result<CapturePermissionStatus, BackendFailure> {
        let permission = self.permission()?;
        Ok(CapturePermissionStatus {
            permission,
            guidance: guidance(permission),
        })
    }

    fn request_permission(&self) -> Result<CapturePermissionStatus, BackendFailure> {
        let permission = translate_permission(self.native.request_permission()?);
        Ok(CapturePermissionStatus {
            permission,
            guidance: guidance(permission),
        })
    }

    fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
        let (displays, windows) = self.translated_catalog(self.native.catalog()?)?;
        let snapshot = DisplaySnapshot::checked(displays.clone())
            .map_err(|_| BackendFailure::DisplayChanged)?;
        *self
            .catalog
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)? = Some(CachedCatalog {
            snapshot_id: snapshot.snapshot_id,
            windows,
        });
        Ok(displays)
    }

    fn windows(&self, snapshot: &DisplaySnapshot) -> Result<Vec<WindowSource>, BackendFailure> {
        let catalog = self
            .catalog
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?;
        let cached = catalog.as_ref().ok_or(BackendFailure::DisplayChanged)?;
        if cached.snapshot_id != snapshot.snapshot_id {
            return Err(BackendFailure::DisplayChanged);
        }
        Ok(cached.windows.clone())
    }

    fn capture(&self, request: &ResolvedCaptureRequest) -> Result<BackendFrame, BackendFailure> {
        let active = self.begin_session(&request.session_id)?;
        let frame = match &request.source {
            CaptureSourceSelection::Window { window_id } => {
                let native_id = parse_source_id(&window_id.0, WINDOW_ID_PREFIX)
                    .ok_or(BackendFailure::WindowLost)?;
                let frame = self.capture_native(
                    NativeCaptureRequest {
                        source: NativeCaptureSource::Window { id: native_id },
                        width: request.expected_frame_size.width,
                        height: request.expected_frame_size.height,
                        pointer: request.pointer,
                    },
                    &active,
                )?;
                validate_native_frame(
                    &frame,
                    request.expected_frame_size.width,
                    request.expected_frame_size.height,
                )?;
                frame
            }
            CaptureSourceSelection::Region { .. }
            | CaptureSourceSelection::Display { .. }
            | CaptureSourceSelection::MultiMonitor { .. } => {
                self.capture_display_regions(request, &active)?
            }
        };
        active.check_cancelled()?;
        Ok(frame)
    }

    fn cancel(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        if let Some(cancelled) = self
            .active
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?
            .get(session_id)
        {
            cancelled.store(true, Ordering::Release);
        }
        Ok(())
    }
}

struct ActiveCapture<'a, A: MacosNativeAdapter> {
    backend: &'a MacosCaptureBackend<A>,
    session_id: CaptureSessionId,
    cancelled: Arc<AtomicBool>,
}

impl<A: MacosNativeAdapter> ActiveCapture<'_, A> {
    fn check_cancelled(&self) -> Result<(), BackendFailure> {
        if self.cancelled.load(Ordering::Acquire) {
            Err(BackendFailure::Cancelled)
        } else {
            Ok(())
        }
    }
}

impl<A: MacosNativeAdapter> Drop for ActiveCapture<'_, A> {
    fn drop(&mut self) {
        if let Ok(mut active) = self.backend.active.lock() {
            active.remove(&self.session_id);
        }
    }
}

fn translate_permission(permission: NativePermission) -> CapturePermission {
    match permission {
        NativePermission::Granted => CapturePermission::Granted,
        NativePermission::PromptRequired => CapturePermission::PromptRequired,
        NativePermission::Denied => CapturePermission::Denied,
    }
}

fn guidance(permission: CapturePermission) -> CapturePermissionGuidance {
    match permission {
        CapturePermission::Granted => CapturePermissionGuidance::None,
        CapturePermission::PromptRequired => CapturePermissionGuidance::RequestSystemPrompt,
        CapturePermission::Denied => CapturePermissionGuidance::OpenSystemSettings,
    }
}

fn display_id(id: u32) -> DisplayId {
    DisplayId(format!("{DISPLAY_ID_PREFIX}{id}"))
}

fn parse_source_id(id: &str, prefix: &str) -> Option<u32> {
    id.strip_prefix(prefix)?.parse().ok()
}

fn scale_from_dimensions(display: &NativeDisplay) -> Result<ScaleFactor, BackendFailure> {
    if display.width == 0
        || display.height == 0
        || display.frame.width <= 0.0
        || display.frame.height <= 0.0
        || !display.frame.width.is_finite()
        || !display.frame.height.is_finite()
    {
        return Err(BackendFailure::DisplayChanged);
    }
    let logical_width = display.frame.width.round();
    let logical_height = display.frame.height.round();
    if (logical_width - display.frame.width).abs() > 0.001
        || (logical_height - display.frame.height).abs() > 0.001
        || logical_width > f64::from(u32::MAX)
        || logical_height > f64::from(u32::MAX)
    {
        return Err(BackendFailure::DisplayChanged);
    }
    let logical_width = logical_width as u32;
    let logical_height = logical_height as u32;
    let divisor = gcd(display.width, logical_width);
    let scale = ScaleFactor {
        numerator: display.width / divisor,
        denominator: logical_width / divisor,
    };
    let expected_height =
        f64::from(logical_height) * f64::from(scale.numerator) / f64::from(scale.denominator);
    if (expected_height - f64::from(display.height)).abs() > 1.0 {
        return Err(BackendFailure::DisplayChanged);
    }
    Ok(scale)
}

const fn gcd(mut left: u32, mut right: u32) -> u32 {
    while right != 0 {
        let remainder = left % right;
        left = right;
        right = remainder;
    }
    left
}

fn translate_window(window: NativeWindow, displays: &[DisplayDescriptor]) -> Option<WindowSource> {
    let display_id = displays
        .iter()
        .max_by(|left, right| {
            intersection_area(window.frame, left.logical_bounds)
                .total_cmp(&intersection_area(window.frame, right.logical_bounds))
        })
        .or_else(|| displays.iter().find(|display| display.primary))?
        .id
        .clone();
    let availability =
        if window.on_screen && window.frame.width >= 1.0 && window.frame.height >= 1.0 {
            WindowAvailability::Available
        } else {
            WindowAvailability::Minimized
        };
    Some(WindowSource {
        id: WindowSourceId(format!("{WINDOW_ID_PREFIX}{}", window.id)),
        display_id,
        bounds: window.frame,
        availability,
        metadata: WindowMetadata {
            process_name: safe_metadata(window.process_name, MAX_PROCESS_NAME_CHARS, true),
            title: safe_metadata(window.title, MAX_WINDOW_TITLE_CHARS, false),
        },
    })
}

fn intersection_area(left: LogicalRect, right: LogicalRect) -> f64 {
    left.intersection(right)
        .map(|intersection| intersection.width * intersection.height)
        .unwrap_or(0.0)
}

fn safe_metadata(value: Option<String>, maximum: usize, process_name: bool) -> Option<String> {
    let value = value?;
    if looks_like_path(&value) || (process_name && value.contains(['/', '\\'])) {
        return None;
    }
    let sanitized = value
        .chars()
        .filter(|character| !character.is_control())
        .take(maximum)
        .collect::<String>();
    let sanitized = sanitized.trim();
    (!sanitized.is_empty()).then(|| sanitized.to_owned())
}

fn looks_like_path(value: &str) -> bool {
    let lower = value.to_ascii_lowercase();
    lower.starts_with('/')
        || lower.starts_with("~/")
        || lower.starts_with("file:")
        || value.contains(['/', '\\'])
        || lower.contains("/users/")
        || lower.contains("/home/")
        || lower.contains("\\users\\")
        || value.as_bytes().windows(3).any(|window| {
            window[0].is_ascii_alphabetic() && window[1] == b':' && window[2] == b'\\'
        })
}

fn pixel_rect_to_logical(
    pixels: PixelRect,
    scale: ScaleFactor,
) -> Result<LogicalRect, BackendFailure> {
    if scale.numerator == 0 || scale.denominator == 0 {
        return Err(BackendFailure::DisplayChanged);
    }
    let inverse = f64::from(scale.denominator) / f64::from(scale.numerator);
    Ok(LogicalRect {
        x: f64::from(pixels.x) * inverse,
        y: f64::from(pixels.y) * inverse,
        width: f64::from(pixels.width) * inverse,
        height: f64::from(pixels.height) * inverse,
    })
}

fn destination_rect(
    global: LogicalRect,
    selection: LogicalRect,
    scale: ScaleFactor,
    canvas: &BackendFrame,
) -> Result<PixelRect, BackendFailure> {
    let factor = f64::from(scale.numerator) / f64::from(scale.denominator);
    let canvas_x = (selection.x * factor).floor();
    let canvas_y = (selection.y * factor).floor();
    let left = ((global.x * factor).floor() - canvas_x).max(0.0);
    let top = ((global.y * factor).floor() - canvas_y).max(0.0);
    let right = ((global.right() * factor).ceil() - canvas_x).min(f64::from(canvas.width));
    let bottom = ((global.bottom() * factor).ceil() - canvas_y).min(f64::from(canvas.height));
    if right <= left || bottom <= top {
        return Err(BackendFailure::CaptureFailed);
    }
    Ok(PixelRect {
        x: left as u32,
        y: top as u32,
        width: (right - left) as u32,
        height: (bottom - top) as u32,
    })
}

fn validate_native_frame(
    frame: &BackendFrame,
    width: u32,
    height: u32,
) -> Result<(), BackendFailure> {
    let expected = decoded_byte_len(width, height).map_err(|_| BackendFailure::CaptureFailed)?;
    if frame.width != width || frame.height != height || frame.rgba.len() != expected {
        return Err(BackendFailure::CaptureFailed);
    }
    Ok(())
}

fn blit_scaled(
    source: &BackendFrame,
    destination: &mut BackendFrame,
    rect: PixelRect,
) -> Result<(), BackendFailure> {
    if rect
        .x
        .checked_add(rect.width)
        .is_none_or(|right| right > destination.width)
        || rect
            .y
            .checked_add(rect.height)
            .is_none_or(|bottom| bottom > destination.height)
        || rect.width == 0
        || rect.height == 0
    {
        return Err(BackendFailure::CaptureFailed);
    }
    for destination_y in 0..rect.height {
        let source_y = u64::from(destination_y) * u64::from(source.height) / u64::from(rect.height);
        for destination_x in 0..rect.width {
            let source_x =
                u64::from(destination_x) * u64::from(source.width) / u64::from(rect.width);
            let source_offset =
                usize::try_from((source_y * u64::from(source.width) + source_x) * 4)
                    .map_err(|_| BackendFailure::CaptureFailed)?;
            let destination_offset = usize::try_from(
                (u64::from(rect.y + destination_y) * u64::from(destination.width)
                    + u64::from(rect.x + destination_x))
                    * 4,
            )
            .map_err(|_| BackendFailure::CaptureFailed)?;
            destination.rgba[destination_offset..destination_offset + 4]
                .copy_from_slice(&source.rgba[source_offset..source_offset + 4]);
        }
    }
    Ok(())
}

#[cfg(target_os = "macos")]
pub(crate) struct SystemMacosNativeAdapter {
    prompt_attempted: AtomicBool,
}

#[cfg(target_os = "macos")]
impl SystemMacosNativeAdapter {
    pub(crate) const fn new() -> Self {
        Self {
            prompt_attempted: AtomicBool::new(false),
        }
    }
}

#[cfg(target_os = "macos")]
#[repr(C)]
struct NativeBytes {
    bytes: *mut u8,
    len: usize,
    width: u32,
    height: u32,
    status: c_int,
}

#[cfg(target_os = "macos")]
unsafe extern "C" {
    fn realqa_macos_preflight_permission() -> bool;
    fn realqa_macos_request_permission() -> bool;
    fn realqa_macos_copy_catalog_json() -> *mut c_char;
    fn realqa_macos_capture(
        source_kind: c_int,
        source_id: u32,
        source_x: f64,
        source_y: f64,
        source_width: f64,
        source_height: f64,
        output_width: u32,
        output_height: u32,
        shows_cursor: bool,
    ) -> NativeBytes;
    fn realqa_macos_free(pointer: *mut c_void);
}

#[cfg(target_os = "macos")]
impl MacosNativeAdapter for SystemMacosNativeAdapter {
    fn permission(&self) -> Result<NativePermission, BackendFailure> {
        // SAFETY: This parameter-free Core Graphics wrapper has no retained result.
        if unsafe { realqa_macos_preflight_permission() } {
            Ok(NativePermission::Granted)
        } else if self.prompt_attempted.load(Ordering::Acquire) {
            Ok(NativePermission::Denied)
        } else {
            Ok(NativePermission::PromptRequired)
        }
    }

    fn request_permission(&self) -> Result<NativePermission, BackendFailure> {
        self.prompt_attempted.store(true, Ordering::Release);
        // SAFETY: The wrapper synchronously invokes the documented Core Graphics
        // request API.
        if unsafe { realqa_macos_request_permission() } {
            Ok(NativePermission::Granted)
        } else {
            Ok(NativePermission::Denied)
        }
    }

    fn catalog(&self) -> Result<NativeCatalog, BackendFailure> {
        // SAFETY: The wrapper returns either null or one malloc allocation owned by
        // this caller.
        let pointer = unsafe { realqa_macos_copy_catalog_json() };
        if pointer.is_null() {
            return Err(
                if matches!(self.permission(), Ok(NativePermission::Granted)) {
                    BackendFailure::CaptureFailed
                } else {
                    BackendFailure::PermissionLost
                },
            );
        }
        let bytes = unsafe { CStr::from_ptr(pointer) }.to_bytes();
        let result = if bytes.len() > MAX_CATALOG_BYTES {
            Err(BackendFailure::CaptureFailed)
        } else {
            serde_json::from_slice::<FfiCatalog>(bytes)
                .map(FfiCatalog::into_native)
                .map_err(|_| BackendFailure::CaptureFailed)
        };
        // SAFETY: `pointer` came from the matching wrapper allocation.
        unsafe { realqa_macos_free(pointer.cast()) };
        result
    }

    fn capture(&self, request: NativeCaptureRequest) -> Result<BackendFrame, BackendFailure> {
        let (kind, id, rect) = match request.source {
            NativeCaptureSource::Display { id, source_rect } => (0, id, source_rect),
            NativeCaptureSource::Window { id } => (
                1,
                id,
                LogicalRect {
                    x: 0.0,
                    y: 0.0,
                    width: 0.0,
                    height: 0.0,
                },
            ),
        };
        // SAFETY: All values are bounded scalar DTO fields; ownership of any returned
        // allocation transfers to this caller and is released with the paired
        // free function below.
        let native = unsafe {
            realqa_macos_capture(
                kind,
                id,
                rect.x,
                rect.y,
                rect.width,
                rect.height,
                request.width,
                request.height,
                request.pointer == PointerInclusion::Include,
            )
        };
        if native.status != 0 {
            if !native.bytes.is_null() {
                unsafe { realqa_macos_free(native.bytes.cast()) };
            }
            return Err(native_status(native.status, kind));
        }
        if native.bytes.is_null() {
            return Err(BackendFailure::CaptureFailed);
        }
        let expected = match decoded_byte_len(native.width, native.height) {
            Ok(expected) => expected,
            Err(_) => {
                unsafe { realqa_macos_free(native.bytes.cast()) };
                return Err(BackendFailure::CaptureFailed);
            }
        };
        if native.len != expected {
            unsafe { realqa_macos_free(native.bytes.cast()) };
            return Err(BackendFailure::CaptureFailed);
        }
        let rgba = unsafe { std::slice::from_raw_parts(native.bytes, native.len) }.to_vec();
        unsafe { realqa_macos_free(native.bytes.cast()) };
        Ok(BackendFrame {
            width: native.width,
            height: native.height,
            rgba,
        })
    }
}

#[cfg(target_os = "macos")]
fn native_status(status: c_int, source_kind: c_int) -> BackendFailure {
    match status {
        1 => BackendFailure::PermissionLost,
        2 => BackendFailure::Cancelled,
        3 => BackendFailure::ProtectedContent,
        4 if source_kind == 1 => BackendFailure::WindowLost,
        4 => BackendFailure::DisplayRemoved,
        _ => BackendFailure::CaptureFailed,
    }
}

#[cfg(target_os = "macos")]
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct FfiCatalog {
    displays: Vec<FfiDisplay>,
    windows: Vec<FfiWindow>,
}

#[cfg(target_os = "macos")]
impl FfiCatalog {
    fn into_native(self) -> NativeCatalog {
        NativeCatalog {
            displays: self
                .displays
                .into_iter()
                .map(|display| NativeDisplay {
                    id: display.id,
                    frame: display.frame.into(),
                    width: display.width,
                    height: display.height,
                    primary: display.primary,
                })
                .collect(),
            windows: self
                .windows
                .into_iter()
                .map(|window| NativeWindow {
                    id: window.id,
                    frame: window.frame.into(),
                    on_screen: window.on_screen,
                    process_name: window.process_name,
                    title: window.title,
                })
                .collect(),
        }
    }
}

#[cfg(target_os = "macos")]
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct FfiDisplay {
    id: u32,
    frame: FfiRect,
    width: u32,
    height: u32,
    primary: bool,
}

#[cfg(target_os = "macos")]
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct FfiWindow {
    id: u32,
    frame: FfiRect,
    on_screen: bool,
    process_name: Option<String>,
    title: Option<String>,
}

#[cfg(target_os = "macos")]
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct FfiRect {
    x: f64,
    y: f64,
    width: f64,
    height: f64,
}

#[cfg(target_os = "macos")]
impl From<FfiRect> for LogicalRect {
    fn from(value: FfiRect) -> Self {
        Self {
            x: value.x,
            y: value.y,
            width: value.width,
            height: value.height,
        }
    }
}

#[cfg(test)]
mod tests {
    use std::sync::{
        atomic::AtomicUsize,
        mpsc::{Receiver, Sender},
    };

    use super::*;
    use crate::realqa_capture::{
        CaptureCore, CaptureFailure, CaptureRequest, ImageMediaType, SelectionGeometry,
    };

    struct FixtureNative {
        permission: Mutex<NativePermission>,
        requested: Mutex<Result<NativePermission, BackendFailure>>,
        catalog: Mutex<NativeCatalog>,
        captures: Mutex<Vec<NativeCaptureRequest>>,
        capture_failure: Mutex<Option<BackendFailure>>,
        capture_override: Mutex<Option<BackendFrame>>,
        active_calls: AtomicUsize,
        capture_started: Mutex<Option<Sender<()>>>,
        capture_release: Mutex<Option<Receiver<()>>>,
    }

    impl FixtureNative {
        fn retina() -> Self {
            Self {
                permission: Mutex::new(NativePermission::Granted),
                requested: Mutex::new(Ok(NativePermission::Granted)),
                catalog: Mutex::new(NativeCatalog {
                    displays: vec![
                        NativeDisplay {
                            id: 7,
                            frame: LogicalRect {
                                x: -1280.0,
                                y: 0.0,
                                width: 1280.0,
                                height: 1024.0,
                            },
                            width: 1280,
                            height: 1024,
                            primary: false,
                        },
                        NativeDisplay {
                            id: 9,
                            frame: LogicalRect {
                                x: 0.0,
                                y: -100.0,
                                width: 1440.0,
                                height: 900.0,
                            },
                            width: 2880,
                            height: 1800,
                            primary: true,
                        },
                    ],
                    windows: vec![NativeWindow {
                        id: 42,
                        frame: LogicalRect {
                            x: -10.0,
                            y: 20.0,
                            width: 110.0,
                            height: 50.0,
                        },
                        on_screen: true,
                        process_name: Some("Fixture Browser".to_owned()),
                        title: Some("Safe title".to_owned()),
                    }],
                }),
                captures: Mutex::new(Vec::new()),
                capture_failure: Mutex::new(None),
                capture_override: Mutex::new(None),
                active_calls: AtomicUsize::new(0),
                capture_started: Mutex::new(None),
                capture_release: Mutex::new(None),
            }
        }
    }

    impl MacosNativeAdapter for Arc<FixtureNative> {
        fn permission(&self) -> Result<NativePermission, BackendFailure> {
            Ok(*self.permission.lock().expect("permission lock"))
        }

        fn request_permission(&self) -> Result<NativePermission, BackendFailure> {
            self.requested.lock().expect("request lock").to_owned()
        }

        fn catalog(&self) -> Result<NativeCatalog, BackendFailure> {
            Ok(self.catalog.lock().expect("catalog lock").clone())
        }

        fn capture(&self, request: NativeCaptureRequest) -> Result<BackendFrame, BackendFailure> {
            self.active_calls.fetch_add(1, Ordering::Relaxed);
            self.captures.lock().expect("capture lock").push(request);
            if let Some(started) = self.capture_started.lock().expect("started lock").take() {
                started.send(()).expect("capture start receiver");
            }
            if let Some(release) = self.capture_release.lock().expect("release lock").take() {
                release.recv().expect("capture release sender");
            }
            if let Some(failure) = *self.capture_failure.lock().expect("failure lock") {
                return Err(failure);
            }
            if let Some(frame) = self
                .capture_override
                .lock()
                .expect("capture override lock")
                .clone()
            {
                return Ok(frame);
            }
            let mut rgba =
                vec![0; decoded_byte_len(request.width, request.height).expect("fixture bounds")];
            for pixel in rgba.chunks_exact_mut(4) {
                pixel.copy_from_slice(&[10, 20, 30, 255]);
            }
            Ok(BackendFrame {
                width: request.width,
                height: request.height,
                rgba,
            })
        }
    }

    fn region_request(catalog: &super::super::CaptureSourceCatalog) -> CaptureRequest {
        CaptureRequest {
            session_id: CaptureSessionId("macos-session".to_owned()),
            snapshot_id: catalog.snapshot.snapshot_id.clone(),
            source: CaptureSourceSelection::Region {
                selection: SelectionGeometry {
                    snapshot_id: catalog.snapshot.snapshot_id.clone(),
                    bounds: LogicalRect {
                        x: -10.0,
                        y: 0.0,
                        width: 20.0,
                        height: 20.0,
                    },
                },
            },
            pointer: PointerInclusion::Exclude,
            output_media_type: ImageMediaType::Png,
        }
    }

    #[test]
    fn translates_retina_negative_coordinates_and_pointer_override() {
        let fixture = Arc::new(FixtureNative::retina());
        let core = CaptureCore::new(Arc::new(MacosCaptureBackend::new(fixture.clone())));
        let catalog = core.source_catalog().expect("catalog");
        let result = core.begin(region_request(&catalog)).expect("capture");
        assert_eq!(
            (result.logical_bounds.x, result.logical_bounds.width),
            (-10.0, 20.0)
        );
        assert_eq!(result.pointer, PointerInclusion::Exclude);
        assert_eq!(result.image.media_type, ImageMediaType::Png);
        let captures = fixture.captures.lock().expect("capture lock");
        assert_eq!(captures.len(), 2);
        assert!(
            captures
                .iter()
                .all(|capture| capture.pointer == PointerInclusion::Exclude)
        );
        drop(captures);

        let mut included = region_request(&catalog);
        included.pointer = PointerInclusion::Include;
        core.begin(included).expect("pointer-included capture");
        let captures = fixture.captures.lock().expect("capture lock");
        assert_eq!(captures.len(), 4);
        assert!(
            captures[2..]
                .iter()
                .all(|capture| capture.pointer == PointerInclusion::Include)
        );
    }

    #[test]
    fn permission_request_cancellation_denial_and_recovery_are_typed() {
        let fixture = Arc::new(FixtureNative::retina());
        *fixture.permission.lock().expect("permission lock") = NativePermission::PromptRequired;
        *fixture.requested.lock().expect("request lock") = Err(BackendFailure::Cancelled);
        let core = CaptureCore::new(Arc::new(MacosCaptureBackend::new(fixture.clone())));
        assert_eq!(
            core.permission_status().expect("status"),
            CapturePermissionStatus {
                permission: CapturePermission::PromptRequired,
                guidance: CapturePermissionGuidance::RequestSystemPrompt,
            }
        );
        assert_eq!(core.request_permission(), Err(CaptureFailure::Cancelled));

        *fixture.requested.lock().expect("request lock") = Ok(NativePermission::Denied);
        assert_eq!(
            core.request_permission().expect("denied status").guidance,
            CapturePermissionGuidance::OpenSystemSettings
        );
        *fixture.permission.lock().expect("permission lock") = NativePermission::Granted;
        assert_eq!(
            core.permission_status().expect("recovered").permission,
            CapturePermission::Granted
        );
        assert!(core.source_catalog().is_ok());
    }

    #[test]
    fn protected_closed_minimized_and_removed_sources_use_closed_outcomes() {
        let fixture = Arc::new(FixtureNative::retina());
        let core = CaptureCore::new(Arc::new(MacosCaptureBackend::new(fixture.clone())));
        let catalog = core.source_catalog().expect("catalog");
        *fixture.capture_failure.lock().expect("failure lock") =
            Some(BackendFailure::ProtectedContent);
        assert_eq!(
            core.begin(region_request(&catalog)),
            Err(CaptureFailure::ProtectedContent)
        );
        *fixture.capture_failure.lock().expect("failure lock") = None;

        *fixture.capture_failure.lock().expect("failure lock") =
            Some(BackendFailure::DisplayRemoved);
        assert_eq!(
            core.begin(region_request(&catalog)),
            Err(CaptureFailure::DisplayRemoved)
        );
        *fixture.capture_failure.lock().expect("failure lock") = None;

        fixture.catalog.lock().expect("catalog lock").windows[0].on_screen = false;
        let minimized_catalog = core.source_catalog().expect("minimized catalog");
        let mut request = region_request(&minimized_catalog);
        request.source = CaptureSourceSelection::Window {
            window_id: WindowSourceId("macos-window-42".to_owned()),
        };
        assert_eq!(core.begin(request), Err(CaptureFailure::WindowLost));

        fixture.catalog.lock().expect("catalog lock").displays.pop();
        assert_eq!(
            core.begin(region_request(&catalog)),
            Err(CaptureFailure::DisplaySnapshotChanged)
        );
    }

    #[test]
    fn repeated_captures_release_session_state_and_allow_reuse() {
        let fixture = Arc::new(FixtureNative::retina());
        let backend = Arc::new(MacosCaptureBackend::new(fixture.clone()));
        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog");
        for _ in 0..3 {
            core.begin(region_request(&catalog))
                .expect("repeat capture");
            assert!(backend.active.lock().expect("active lock").is_empty());
        }
        assert_eq!(fixture.active_calls.load(Ordering::Relaxed), 6);
    }

    #[test]
    fn cancellation_drops_late_pixels_and_releases_active_session_state() {
        let fixture = Arc::new(FixtureNative::retina());
        let backend = Arc::new(MacosCaptureBackend::new(fixture.clone()));
        let core = Arc::new(CaptureCore::new(backend.clone()));
        let catalog = core.source_catalog().expect("catalog");
        let (started_sender, started_receiver) = std::sync::mpsc::channel();
        let (release_sender, release_receiver) = std::sync::mpsc::channel();
        *fixture.capture_started.lock().expect("started lock") = Some(started_sender);
        *fixture.capture_release.lock().expect("release lock") = Some(release_receiver);

        let capture_core = core.clone();
        let capture = std::thread::spawn(move || capture_core.begin(region_request(&catalog)));
        started_receiver.recv().expect("capture started");
        core.cancel(&CaptureSessionId("macos-session".to_owned()))
            .expect("cancel");
        release_sender.send(()).expect("release capture");

        assert_eq!(
            capture.join().expect("capture thread"),
            Err(CaptureFailure::Cancelled)
        );
        assert!(backend.active.lock().expect("active lock").is_empty());
    }

    #[test]
    fn cancellation_wins_over_a_late_native_failure() {
        let fixture = Arc::new(FixtureNative::retina());
        let backend = Arc::new(MacosCaptureBackend::new(fixture.clone()));
        let core = Arc::new(CaptureCore::new(backend.clone()));
        let catalog = core.source_catalog().expect("catalog");
        let (started_sender, started_receiver) = std::sync::mpsc::channel();
        let (release_sender, release_receiver) = std::sync::mpsc::channel();
        *fixture.capture_failure.lock().expect("failure lock") =
            Some(BackendFailure::CaptureFailed);
        *fixture.capture_started.lock().expect("started lock") = Some(started_sender);
        *fixture.capture_release.lock().expect("release lock") = Some(release_receiver);

        let capture_core = core.clone();
        let capture = std::thread::spawn(move || capture_core.begin(region_request(&catalog)));
        started_receiver.recv().expect("capture started");
        core.cancel(&CaptureSessionId("macos-session".to_owned()))
            .expect("cancel");
        release_sender.send(()).expect("release capture");

        assert_eq!(
            capture.join().expect("capture thread"),
            Err(CaptureFailure::Cancelled)
        );
        assert!(backend.active.lock().expect("active lock").is_empty());
    }

    #[test]
    fn metadata_is_bounded_and_path_like_titles_never_cross_the_backend() {
        let fixture = Arc::new(FixtureNative::retina());
        {
            let mut catalog = fixture.catalog.lock().expect("catalog lock");
            catalog.windows[0].process_name = Some("/Applications/Secret.app".to_owned());
            catalog.windows[0].title = Some("/Users/alice/private/file.txt".to_owned());
        }
        let core = CaptureCore::new(Arc::new(MacosCaptureBackend::new(fixture)));
        let catalog = core.source_catalog().expect("catalog");
        assert_eq!(catalog.windows[0].metadata.process_name, None);
        assert_eq!(catalog.windows[0].metadata.title, None);
        let serialized = serde_json::to_string(&catalog.windows).expect("serialize");
        for forbidden in ["/Applications", "/Users/alice", "private/file.txt"] {
            assert!(!serialized.contains(forbidden));
        }
    }

    #[test]
    fn malformed_native_catalog_and_frames_fail_before_core_pixels() {
        let fixture = Arc::new(FixtureNative::retina());
        fixture.catalog.lock().expect("catalog lock").displays[0].width = 0;
        let core = CaptureCore::new(Arc::new(MacosCaptureBackend::new(fixture)));
        assert_eq!(
            core.source_catalog(),
            Err(CaptureFailure::DisplaySnapshotChanged)
        );

        let fixture = Arc::new(FixtureNative::retina());
        let core = CaptureCore::new(Arc::new(MacosCaptureBackend::new(fixture.clone())));
        let catalog = core.source_catalog().expect("valid catalog");
        *fixture
            .capture_override
            .lock()
            .expect("capture override lock") = Some(BackendFrame {
            width: 1,
            height: 1,
            rgba: vec![0; 3],
        });
        assert_eq!(
            core.begin(region_request(&catalog)),
            Err(CaptureFailure::CaptureFailed)
        );
    }

    #[cfg(target_os = "macos")]
    #[test]
    #[ignore = "requires a foreground macOS 14+ desktop session"]
    fn system_adapter_smoke_uses_current_permission_without_prompting() {
        let adapter = SystemMacosNativeAdapter::new();
        match adapter.permission().expect("permission discovery") {
            NativePermission::PromptRequired | NativePermission::Denied => {}
            NativePermission::Granted => {
                let catalog = adapter.catalog().expect("shareable content");
                let display = catalog.displays.first().expect("active display");
                let result = adapter.capture(NativeCaptureRequest {
                    source: NativeCaptureSource::Display {
                        id: display.id,
                        source_rect: LogicalRect {
                            x: 0.0,
                            y: 0.0,
                            width: 1.0,
                            height: 1.0,
                        },
                    },
                    width: 1,
                    height: 1,
                    pointer: PointerInclusion::Exclude,
                });
                assert!(
                    result.is_ok() || result == Err(BackendFailure::ProtectedContent),
                    "authorized one-pixel capture must return pixels or protected content"
                );
            }
        }
    }
}
