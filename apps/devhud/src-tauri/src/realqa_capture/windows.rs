use std::{
    collections::{HashMap, HashSet},
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
};

use super::{
    BackendFailure, BackendFrame, CaptureBackend, CaptureCapabilities, CaptureDisplayProtocol,
    CaptureMode, CaptureModeCapability, CapturePermission, CapturePlatform, CaptureSessionId,
    CaptureSourceSelection, DisplayDescriptor, DisplayId, DisplaySnapshot, LogicalRect,
    MAX_SAFE_PROCESS_NAME_BYTES, MAX_SAFE_WINDOW_TITLE_BYTES, PointerInclusion,
    ResolvedCaptureRequest, ResolvedWindowSource, SelectionAdjustmentAuthority, WindowAvailability,
    WindowMetadata, WindowSource, WindowSourceId,
    geometry::{PhysicalSize, PixelRect, ScaleFactor},
    image_boundary::{MAX_DECODED_PIXELS, decoded_byte_len},
    metadata_value_looks_like_path,
};

const MAX_INTERNAL_SOURCE_KEY_BYTES: usize = 1_024;
const WINDOWS_11_MINIMUM_BUILD: u32 = 22_000;

const fn is_supported_windows_version(major: u32, build: u32) -> bool {
    major > 10 || (major == 10 && build >= WINDOWS_11_MINIMUM_BUILD)
}

#[cfg(target_os = "windows")]
pub(super) fn operating_system_supported() -> bool {
    #[cfg(any(target_arch = "x86_64", target_arch = "aarch64"))]
    {
        return system::is_supported();
    }
    #[cfg(not(any(target_arch = "x86_64", target_arch = "aarch64")))]
    {
        false
    }
}

const fn permission_from_support(
    supported: bool,
) -> Result<CapturePermission, WindowsAdapterFailure> {
    if supported {
        Ok(CapturePermission::Granted)
    } else {
        Err(WindowsAdapterFailure::Unavailable)
    }
}

#[cfg(any(target_os = "windows", test))]
const fn should_skip_failed_window_retention(window_exists: bool) -> bool {
    !window_exists
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
struct NativeSourceId(usize);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum NativeCaptureSource {
    Display(NativeSourceId),
    Window(NativeSourceId),
}

impl NativeCaptureSource {
    const fn id(self) -> NativeSourceId {
        match self {
            Self::Display(id) | Self::Window(id) => id,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct NativeRect {
    left: i32,
    top: i32,
    right: i32,
    bottom: i32,
}

impl NativeRect {
    fn width(self) -> Result<u32, WindowsAdapterFailure> {
        u32::try_from(self.right - self.left).map_err(|_| WindowsAdapterFailure::Failed)
    }

    fn height(self) -> Result<u32, WindowsAdapterFailure> {
        u32::try_from(self.bottom - self.top).map_err(|_| WindowsAdapterFailure::Failed)
    }

    fn intersection(self, other: Self) -> Option<Self> {
        let left = self.left.max(other.left);
        let top = self.top.max(other.top);
        let right = self.right.min(other.right);
        let bottom = self.bottom.min(other.bottom);
        (right > left && bottom > top).then_some(Self {
            left,
            top,
            right,
            bottom,
        })
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Hash)]
struct DisplayIdentity {
    stable_key: String,
    device_interface_key: String,
    source: NativeSourceId,
}

#[derive(Debug, Clone)]
struct WindowsDisplay {
    source: NativeSourceId,
    stable_key: String,
    device_interface_key: String,
    native_bounds: NativeRect,
    logical_bounds: LogicalRect,
    physical_size: PhysicalSize,
    scale: ScaleFactor,
    primary: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum WindowsWindowState {
    Available,
    Minimized,
    Protected,
}

#[derive(Debug, Clone, PartialEq, Eq, Hash)]
struct WindowIdentity {
    stable_key: String,
    source: NativeSourceId,
    process_id: u32,
    process_started_at: u64,
}

#[derive(Debug, Clone)]
struct WindowsWindow {
    source: NativeSourceId,
    stable_key: String,
    process_id: u32,
    process_started_at: u64,
    display_key: String,
    display_device_interface_key: String,
    display_source: NativeSourceId,
    native_bounds: NativeRect,
    bounds: LogicalRect,
    state: WindowsWindowState,
    process_name: Option<String>,
    title: Option<String>,
}

impl WindowsDisplay {
    fn identity(&self) -> DisplayIdentity {
        DisplayIdentity {
            stable_key: self.stable_key.clone(),
            device_interface_key: self.device_interface_key.clone(),
            source: self.source,
        }
    }
}

impl WindowsWindow {
    fn identity(&self) -> WindowIdentity {
        WindowIdentity {
            stable_key: self.stable_key.clone(),
            source: self.source,
            process_id: self.process_id,
            process_started_at: self.process_started_at,
        }
    }

    fn display_identity(&self) -> DisplayIdentity {
        DisplayIdentity {
            stable_key: self.display_key.clone(),
            device_interface_key: self.display_device_interface_key.clone(),
            source: self.display_source,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct NativeFrame {
    width: u32,
    height: u32,
    rgba: Vec<u8>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum WindowsAdapterFailure {
    Unavailable,
    PermissionDenied,
    PermissionLost,
    Cancelled,
    ProtectedContent,
    WindowMinimized,
    WindowClosed,
    DisplayRemoved,
    Failed,
}

impl From<WindowsAdapterFailure> for BackendFailure {
    fn from(value: WindowsAdapterFailure) -> Self {
        match value {
            WindowsAdapterFailure::Unavailable => Self::Unavailable,
            WindowsAdapterFailure::PermissionDenied => Self::PermissionDenied,
            WindowsAdapterFailure::PermissionLost => Self::PermissionLost,
            WindowsAdapterFailure::Cancelled => Self::Cancelled,
            WindowsAdapterFailure::ProtectedContent => Self::ProtectedContent,
            WindowsAdapterFailure::WindowMinimized => Self::WindowMinimized,
            WindowsAdapterFailure::WindowClosed => Self::WindowClosed,
            WindowsAdapterFailure::DisplayRemoved => Self::DisplayRemoved,
            WindowsAdapterFailure::Failed => Self::CaptureFailed,
        }
    }
}

trait WindowsPlatformAdapter: Send + Sync {
    fn permission(&self) -> Result<CapturePermission, WindowsAdapterFailure>;
    fn displays(&self) -> Result<Vec<WindowsDisplay>, WindowsAdapterFailure>;
    fn windows(&self) -> Result<Vec<WindowsWindow>, WindowsAdapterFailure>;
    fn capture(
        &self,
        source: NativeCaptureSource,
        pointer: PointerInclusion,
        cancel: Arc<AtomicBool>,
    ) -> Result<NativeFrame, WindowsAdapterFailure>;
}

#[derive(Default)]
struct SourceRegistry {
    next_display_id: u64,
    next_window_id: u64,
    display_ids: HashMap<DisplayIdentity, DisplayId>,
    window_ids: HashMap<WindowIdentity, WindowSourceId>,
    displays: HashMap<DisplayId, WindowsDisplay>,
    windows: HashMap<WindowSourceId, WindowsWindow>,
}

impl SourceRegistry {
    fn public_display_id(&mut self, display: &WindowsDisplay) -> DisplayId {
        let identity = display.identity();
        if let Some(id) = self.display_ids.get(&identity) {
            return id.clone();
        }
        self.next_display_id += 1;
        let id = DisplayId(format!("windows-display-{:016x}", self.next_display_id));
        self.display_ids.insert(identity, id.clone());
        id
    }

    fn public_window_id(&mut self, window: &WindowsWindow) -> WindowSourceId {
        let identity = window.identity();
        if let Some(id) = self.window_ids.get(&identity) {
            return id.clone();
        }
        self.next_window_id += 1;
        let id = WindowSourceId(format!("windows-window-{:016x}", self.next_window_id));
        self.window_ids.insert(identity, id.clone());
        id
    }
}

pub(super) struct WindowsCaptureBackend {
    adapter: Arc<dyn WindowsPlatformAdapter>,
    sources: Mutex<SourceRegistry>,
    active_captures: Mutex<HashMap<CaptureSessionId, Arc<AtomicBool>>>,
}

impl WindowsCaptureBackend {
    fn new(adapter: Arc<dyn WindowsPlatformAdapter>) -> Self {
        Self {
            adapter,
            sources: Mutex::new(SourceRegistry::default()),
            active_captures: Mutex::new(HashMap::new()),
        }
    }

    #[cfg(all(
        target_os = "windows",
        any(target_arch = "x86_64", target_arch = "aarch64")
    ))]
    pub(super) fn system() -> Option<Self> {
        system::is_supported().then(|| Self::new(Arc::new(system::SystemWindowsAdapter::default())))
    }

    #[cfg(all(
        target_os = "windows",
        not(any(target_arch = "x86_64", target_arch = "aarch64"))
    ))]
    pub(super) fn system() -> Option<Self> {
        None
    }

    fn checked_displays(
        &self,
        displays: Vec<WindowsDisplay>,
    ) -> Result<(Vec<DisplayDescriptor>, HashMap<DisplayId, WindowsDisplay>), BackendFailure> {
        let mut identities = HashSet::with_capacity(displays.len());
        if displays.is_empty()
            || displays.iter().any(|display| {
                display.stable_key.is_empty()
                    || display.stable_key.len() > MAX_INTERNAL_SOURCE_KEY_BYTES
                    || display.device_interface_key.is_empty()
                    || display.device_interface_key.len() > MAX_INTERNAL_SOURCE_KEY_BYTES
                    || !identities.insert(display.identity())
            })
        {
            return Err(BackendFailure::DisplayChanged);
        }

        let mut sources = self
            .sources
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?;
        sources
            .display_ids
            .retain(|identity, _| identities.contains(identity));
        let mut current = HashMap::with_capacity(displays.len());
        let descriptors = displays
            .into_iter()
            .map(|display| {
                let id = sources.public_display_id(&display);
                let descriptor = DisplayDescriptor {
                    id: id.clone(),
                    logical_bounds: display.logical_bounds,
                    physical_size: display.physical_size,
                    scale: display.scale,
                    primary: display.primary,
                };
                current.insert(id, display);
                descriptor
            })
            .collect();
        sources.displays = current.clone();
        Ok((descriptors, current))
    }

    fn current_windows(&self) -> Result<Vec<WindowsWindow>, BackendFailure> {
        let windows = self.adapter.windows().map_err(BackendFailure::from)?;
        let mut identities = HashSet::with_capacity(windows.len());
        if windows.iter().any(|window| {
            window.stable_key.is_empty()
                || window.stable_key.len() > MAX_INTERNAL_SOURCE_KEY_BYTES
                || !identities.insert(window.identity())
        }) {
            return Err(BackendFailure::CaptureFailed);
        }
        Ok(windows)
    }

    fn register_capture(
        &self,
        session_id: &CaptureSessionId,
    ) -> Result<Arc<AtomicBool>, BackendFailure> {
        let mut active = self
            .active_captures
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?;
        if active.contains_key(session_id) {
            return Err(BackendFailure::CaptureFailed);
        }
        let cancel = Arc::new(AtomicBool::new(false));
        active.insert(session_id.clone(), cancel.clone());
        Ok(cancel)
    }

    fn capture_token(
        &self,
        session_id: &CaptureSessionId,
    ) -> Result<Arc<AtomicBool>, BackendFailure> {
        self.active_captures
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?
            .get(session_id)
            .cloned()
            .ok_or(BackendFailure::CaptureFailed)
    }

    fn unregister_capture(&self, session_id: &CaptureSessionId) -> Result<bool, BackendFailure> {
        let cancelled = self
            .active_captures
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?
            .remove(session_id)
            .is_some_and(|cancel| cancel.load(Ordering::Acquire));
        Ok(cancelled)
    }

    fn revalidate_snapshot(
        &self,
        request: &ResolvedCaptureRequest,
    ) -> Result<HashMap<DisplayId, WindowsDisplay>, BackendFailure> {
        let displays = self.adapter.displays().map_err(BackendFailure::from)?;
        let (descriptors, displays) = self.checked_displays(displays)?;
        let current =
            DisplaySnapshot::checked(descriptors).map_err(|_| BackendFailure::DisplayChanged)?;
        let selected_ids = request
            .pixel_regions
            .iter()
            .map(|region| &region.display_id)
            .collect::<HashSet<_>>();
        if selected_ids.iter().any(|id| current.display(id).is_none()) {
            return Err(BackendFailure::DisplayRemoved);
        }
        if current.snapshot_id != request.snapshot.snapshot_id {
            return Err(BackendFailure::DisplayChanged);
        }
        Ok(displays)
    }

    fn capture_inner(
        &self,
        request: &ResolvedCaptureRequest,
        cancel: &Arc<AtomicBool>,
    ) -> Result<BackendFrame, BackendFailure> {
        self.revalidate_snapshot(request)?;
        if cancel.load(Ordering::Acquire) {
            return Err(BackendFailure::Cancelled);
        }

        if let CaptureSourceSelection::Window { window_id } = &request.source {
            let window = request
                .window
                .as_ref()
                .filter(|window| &window.id == window_id)
                .ok_or(BackendFailure::CaptureFailed)?;
            return self.capture_window(request, window, cancel);
        }
        if request.window.is_some() {
            return Err(BackendFailure::CaptureFailed);
        }
        self.capture_display_regions(request, cancel)
    }

    fn capture_window(
        &self,
        request: &ResolvedCaptureRequest,
        resolved: &ResolvedWindowSource,
        cancel: &Arc<AtomicBool>,
    ) -> Result<BackendFrame, BackendFailure> {
        let registered = {
            let sources = self
                .sources
                .lock()
                .map_err(|_| BackendFailure::CaptureFailed)?;
            let registered = sources
                .windows
                .get(&resolved.id)
                .ok_or(BackendFailure::WindowClosed)?;
            if registered.bounds != resolved.bounds
                || sources.display_ids.get(&registered.display_identity())
                    != Some(&resolved.display_id)
            {
                return Err(BackendFailure::DisplayChanged);
            }
            registered.clone()
        };
        let window = self
            .current_windows()?
            .into_iter()
            .find(|candidate| candidate.identity() == registered.identity())
            .ok_or(BackendFailure::WindowClosed)?;
        validate_window_state(&registered, &window)?;
        let frame = self
            .adapter
            .capture(
                NativeCaptureSource::Window(window.source),
                request.pointer,
                cancel.clone(),
            )
            .map_err(BackendFailure::from)?;
        checked_native_frame(&frame)?;
        if frame.width != window.native_bounds.width().map_err(BackendFailure::from)?
            || frame.height
                != window
                    .native_bounds
                    .height()
                    .map_err(BackendFailure::from)?
        {
            return Err(BackendFailure::DisplayChanged);
        }
        let displays = self.revalidate_snapshot(request)?;
        let captured = self
            .current_windows()?
            .into_iter()
            .find(|candidate| candidate.identity() == window.identity())
            .ok_or(BackendFailure::WindowClosed)?;
        validate_window_state(&window, &captured)?;
        self.compose_window_frame(request, &captured, &frame, &displays)
    }

    fn compose_window_frame(
        &self,
        request: &ResolvedCaptureRequest,
        window: &WindowsWindow,
        frame: &NativeFrame,
        displays: &HashMap<DisplayId, WindowsDisplay>,
    ) -> Result<BackendFrame, BackendFailure> {
        let output_len = decoded_byte_len(
            request.expected_frame_size.width,
            request.expected_frame_size.height,
        )
        .map_err(|_| BackendFailure::CaptureFailed)?;
        let mut output = BackendFrame {
            width: request.expected_frame_size.width,
            height: request.expected_frame_size.height,
            rgba: vec![0; output_len],
            approved_layout: None,
        };
        let canvas_scale = highest_scale(&request.snapshot, &request.pixel_regions)?;
        for region in &request.pixel_regions {
            let display = displays
                .get(&region.display_id)
                .ok_or(BackendFailure::DisplayRemoved)?;
            let source = window_source_rect(window, display, region.pixels, frame)?;
            let destination = destination_rect(request, display, region.pixels, canvas_scale)?;
            blit_scaled(frame, source, &mut output, destination)?;
        }
        Ok(output)
    }

    fn capture_display_regions(
        &self,
        request: &ResolvedCaptureRequest,
        cancel: &Arc<AtomicBool>,
    ) -> Result<BackendFrame, BackendFailure> {
        let output_len = decoded_byte_len(
            request.expected_frame_size.width,
            request.expected_frame_size.height,
        )
        .map_err(|_| BackendFailure::CaptureFailed)?;
        let mut output = BackendFrame {
            width: request.expected_frame_size.width,
            height: request.expected_frame_size.height,
            rgba: vec![0; output_len],
            approved_layout: None,
        };

        let displays = self
            .sources
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?
            .displays
            .clone();
        let mut frames = HashMap::<DisplayId, NativeFrame>::new();
        let canvas_scale = highest_scale(&request.snapshot, &request.pixel_regions)?;
        for region in &request.pixel_regions {
            if cancel.load(Ordering::Acquire) {
                return Err(BackendFailure::Cancelled);
            }
            let display = displays
                .get(&region.display_id)
                .ok_or(BackendFailure::DisplayRemoved)?;
            let frame = if let Some(frame) = frames.get(&region.display_id) {
                frame
            } else {
                let frame = self
                    .adapter
                    .capture(
                        NativeCaptureSource::Display(display.source),
                        request.pointer,
                        cancel.clone(),
                    )
                    .map_err(BackendFailure::from)?;
                self.revalidate_snapshot(request)?;
                checked_native_frame(&frame)?;
                if frame.width != display.physical_size.width
                    || frame.height != display.physical_size.height
                {
                    return Err(BackendFailure::DisplayChanged);
                }
                frames.entry(region.display_id.clone()).or_insert(frame)
            };
            let destination = destination_rect(request, display, region.pixels, canvas_scale)?;
            blit_scaled(frame, region.pixels, &mut output, destination)?;
        }
        Ok(output)
    }
}

fn validate_window_state(
    registered: &WindowsWindow,
    current: &WindowsWindow,
) -> Result<(), BackendFailure> {
    if current.identity() != registered.identity() {
        return Err(BackendFailure::WindowClosed);
    }
    match current.state {
        WindowsWindowState::Available => {}
        WindowsWindowState::Minimized => return Err(BackendFailure::WindowMinimized),
        WindowsWindowState::Protected => return Err(BackendFailure::ProtectedContent),
    }
    if current.display_identity() != registered.display_identity()
        || current.native_bounds != registered.native_bounds
        || current.bounds != registered.bounds
    {
        return Err(BackendFailure::DisplayChanged);
    }
    Ok(())
}

impl CaptureBackend for WindowsCaptureBackend {
    fn platform(&self) -> CapturePlatform {
        CapturePlatform::Windows
    }

    fn capabilities(&self) -> Result<CaptureCapabilities, BackendFailure> {
        self.adapter.permission().map_err(BackendFailure::from)?;
        Ok(CaptureCapabilities {
            platform: CapturePlatform::Windows,
            display_protocol: CaptureDisplayProtocol::Native,
            modes: [
                CaptureMode::Region,
                CaptureMode::Window,
                CaptureMode::Display,
                CaptureMode::MultiMonitor,
            ]
            .into_iter()
            .map(|mode| CaptureModeCapability {
                mode,
                pointer_options: vec![PointerInclusion::Include, PointerInclusion::Exclude],
                portal_approval_required: false,
                selection_adjustment: if mode == CaptureMode::Region {
                    SelectionAdjustmentAuthority::Application
                } else {
                    SelectionAdjustmentAuthority::Unavailable
                },
            })
            .collect(),
        })
    }

    fn start_session(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        self.register_capture(session_id).map(|_| ())
    }

    fn finish_session(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        if self.unregister_capture(session_id)? {
            return Err(BackendFailure::Cancelled);
        }
        Ok(())
    }

    fn permission(&self) -> Result<CapturePermission, BackendFailure> {
        self.adapter.permission().map_err(BackendFailure::from)
    }

    fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
        let displays = self.adapter.displays().map_err(BackendFailure::from)?;
        self.checked_displays(displays)
            .map(|(descriptors, _)| descriptors)
    }

    fn windows(&self, snapshot: &DisplaySnapshot) -> Result<Vec<WindowSource>, BackendFailure> {
        let windows = self.current_windows()?;
        let current_snapshot = DisplaySnapshot::checked(self.displays()?)
            .map_err(|_| BackendFailure::DisplayChanged)?;
        if current_snapshot.snapshot_id != snapshot.snapshot_id {
            return Err(BackendFailure::DisplayChanged);
        }
        let mut sources = self
            .sources
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?;
        let display_ids = sources.display_ids.clone();
        let current_identities = windows
            .iter()
            .map(WindowsWindow::identity)
            .collect::<HashSet<_>>();
        sources
            .window_ids
            .retain(|identity, _| current_identities.contains(identity));
        let mut current = HashMap::with_capacity(windows.len());
        let mut output = Vec::with_capacity(windows.len());
        for window in windows {
            let display_id = display_ids
                .get(&window.display_identity())
                .filter(|id| snapshot.display(id).is_some())
                .cloned()
                .ok_or(BackendFailure::DisplayChanged)?;
            let id = sources.public_window_id(&window);
            let availability = match window.state {
                WindowsWindowState::Available | WindowsWindowState::Protected => {
                    WindowAvailability::Available
                }
                WindowsWindowState::Minimized => WindowAvailability::Minimized,
            };
            output.push(WindowSource {
                id: id.clone(),
                display_id,
                bounds: window.bounds,
                availability,
                metadata: WindowMetadata {
                    process_name: sanitize_metadata(
                        window.process_name.as_deref(),
                        MAX_SAFE_PROCESS_NAME_BYTES,
                    ),
                    title: sanitize_metadata(window.title.as_deref(), MAX_SAFE_WINDOW_TITLE_BYTES),
                },
            });
            current.insert(id, window);
        }
        sources.windows = current;
        Ok(output)
    }

    fn capture(&self, request: &ResolvedCaptureRequest) -> Result<BackendFrame, BackendFailure> {
        let cancel = self.capture_token(&request.session_id)?;
        self.capture_inner(request, &cancel)
    }

    fn cancel(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        let active = self
            .active_captures
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?;
        if let Some(cancel) = active.get(session_id) {
            cancel.store(true, Ordering::Release);
        }
        Ok(())
    }
}

fn sanitize_metadata(value: Option<&str>, maximum_bytes: usize) -> Option<String> {
    let normalized = value?
        .chars()
        .filter(|character| !character.is_control())
        .collect::<String>();
    let trimmed = normalized.trim();
    if trimmed.is_empty() || metadata_value_looks_like_path(trimmed) {
        return None;
    }
    let mut end = trimmed.len().min(maximum_bytes);
    while !trimmed.is_char_boundary(end) {
        end -= 1;
    }
    let truncated = trimmed[..end].trim();
    (!truncated.is_empty()).then(|| truncated.to_owned())
}

fn checked_native_frame(frame: &NativeFrame) -> Result<(), BackendFailure> {
    let pixels = u64::from(frame.width)
        .checked_mul(u64::from(frame.height))
        .ok_or(BackendFailure::CaptureFailed)?;
    if pixels == 0 || pixels > MAX_DECODED_PIXELS {
        return Err(BackendFailure::CaptureFailed);
    }
    let expected =
        decoded_byte_len(frame.width, frame.height).map_err(|_| BackendFailure::CaptureFailed)?;
    if frame.rgba.len() != expected {
        return Err(BackendFailure::CaptureFailed);
    }
    Ok(())
}

fn normalize_display_layout(displays: &mut [WindowsDisplay]) -> Result<(), WindowsAdapterFailure> {
    let primary_indices = displays
        .iter()
        .enumerate()
        .filter_map(|(index, display)| display.primary.then_some(index))
        .collect::<Vec<_>>();
    let [primary_index] = primary_indices.as_slice() else {
        return Err(WindowsAdapterFailure::Failed);
    };
    for display in displays.iter_mut() {
        let width = display.native_bounds.width()?;
        let height = display.native_bounds.height()?;
        if width != display.physical_size.width || height != display.physical_size.height {
            return Err(WindowsAdapterFailure::Failed);
        }
        let scale = scale_value(display.scale)?;
        display.logical_bounds.width = f64::from(width) / scale;
        display.logical_bounds.height = f64::from(height) / scale;
    }

    let primary_scale = scale_value(displays[*primary_index].scale)?;
    displays[*primary_index].logical_bounds.x =
        f64::from(displays[*primary_index].native_bounds.left) / primary_scale;
    displays[*primary_index].logical_bounds.y =
        f64::from(displays[*primary_index].native_bounds.top) / primary_scale;

    let mut resolved = vec![false; displays.len()];
    resolved[*primary_index] = true;
    while resolved.iter().any(|is_resolved| !is_resolved) {
        let mut nearest = None;
        for (candidate_index, candidate) in displays.iter().enumerate() {
            if resolved[candidate_index] {
                continue;
            }
            for (reference_index, reference) in displays.iter().enumerate() {
                if !resolved[reference_index] {
                    continue;
                }
                let distance =
                    native_rect_distance(candidate.native_bounds, reference.native_bounds);
                let key = (distance, candidate_index, reference_index);
                if nearest.is_none_or(|current: (u64, usize, usize)| key < current) {
                    nearest = Some(key);
                }
            }
        }
        let Some((_, candidate_index, reference_index)) = nearest else {
            return Err(WindowsAdapterFailure::Failed);
        };
        let reference = displays[reference_index].clone();
        let candidate = &mut displays[candidate_index];
        let candidate_scale = scale_value(candidate.scale)?;
        let reference_scale = scale_value(reference.scale)?;
        candidate.logical_bounds.x = logical_axis_origin(
            candidate.native_bounds.left,
            candidate.native_bounds.right,
            candidate.logical_bounds.width,
            candidate_scale,
            reference.native_bounds.left,
            reference.native_bounds.right,
            reference.logical_bounds.x,
            reference.logical_bounds.width,
            reference_scale,
        );
        candidate.logical_bounds.y = logical_axis_origin(
            candidate.native_bounds.top,
            candidate.native_bounds.bottom,
            candidate.logical_bounds.height,
            candidate_scale,
            reference.native_bounds.top,
            reference.native_bounds.bottom,
            reference.logical_bounds.y,
            reference.logical_bounds.height,
            reference_scale,
        );
        resolved[candidate_index] = true;
    }
    Ok(())
}

fn scale_value(scale: ScaleFactor) -> Result<f64, WindowsAdapterFailure> {
    if scale.numerator == 0 || scale.denominator == 0 {
        return Err(WindowsAdapterFailure::Failed);
    }
    Ok(f64::from(scale.numerator) / f64::from(scale.denominator))
}

#[allow(clippy::too_many_arguments)]
fn logical_axis_origin(
    candidate_start: i32,
    candidate_end: i32,
    candidate_extent: f64,
    candidate_scale: f64,
    reference_start: i32,
    reference_end: i32,
    reference_logical_start: f64,
    reference_logical_extent: f64,
    reference_scale: f64,
) -> f64 {
    if candidate_start == reference_start {
        return reference_logical_start;
    }
    if candidate_end == reference_end {
        return reference_logical_start + reference_logical_extent - candidate_extent;
    }
    if candidate_start >= reference_end {
        return reference_logical_start
            + reference_logical_extent
            + f64::from(candidate_start - reference_end) / reference_scale;
    }
    if candidate_end <= reference_start {
        return reference_logical_start
            - candidate_extent
            - f64::from(reference_start - candidate_end) / candidate_scale;
    }
    reference_logical_start + f64::from(candidate_start - reference_start) / reference_scale
}

fn native_rect_distance(left: NativeRect, right: NativeRect) -> u64 {
    let horizontal = if left.left >= right.right {
        i64::from(left.left) - i64::from(right.right)
    } else if right.left >= left.right {
        i64::from(right.left) - i64::from(left.right)
    } else {
        0
    };
    let vertical = if left.top >= right.bottom {
        i64::from(left.top) - i64::from(right.bottom)
    } else if right.top >= left.bottom {
        i64::from(right.top) - i64::from(left.bottom)
    } else {
        0
    };
    u64::try_from(horizontal * horizontal + vertical * vertical).unwrap_or(u64::MAX)
}

fn logical_window_bounds(
    native_bounds: NativeRect,
    displays: &[WindowsDisplay],
) -> Result<Option<LogicalRect>, WindowsAdapterFailure> {
    let mut logical_regions = Vec::new();
    for display in displays {
        let Some(intersection) = native_bounds.intersection(display.native_bounds) else {
            continue;
        };
        let scale = scale_value(display.scale)?;
        logical_regions.push(LogicalRect {
            x: display.logical_bounds.x
                + f64::from(intersection.left - display.native_bounds.left) / scale,
            y: display.logical_bounds.y
                + f64::from(intersection.top - display.native_bounds.top) / scale,
            width: f64::from(intersection.right - intersection.left) / scale,
            height: f64::from(intersection.bottom - intersection.top) / scale,
        });
    }
    Ok(LogicalRect::bounding(logical_regions.into_iter()))
}

fn highest_scale(
    snapshot: &DisplaySnapshot,
    regions: &[super::DisplayPixelRegion],
) -> Result<ScaleFactor, BackendFailure> {
    regions
        .iter()
        .filter_map(|region| snapshot.display(&region.display_id))
        .map(|display| display.scale)
        .max_by(|left, right| {
            (u64::from(left.numerator) * u64::from(right.denominator))
                .cmp(&(u64::from(right.numerator) * u64::from(left.denominator)))
        })
        .ok_or(BackendFailure::CaptureFailed)
}

fn destination_rect(
    request: &ResolvedCaptureRequest,
    display: &WindowsDisplay,
    source: PixelRect,
    canvas_scale: ScaleFactor,
) -> Result<PixelRect, BackendFailure> {
    if request.pixel_regions.len() == 1
        && request.logical_bounds.x >= display.logical_bounds.x
        && request.logical_bounds.y >= display.logical_bounds.y
        && request.logical_bounds.right() <= display.logical_bounds.right()
        && request.logical_bounds.bottom() <= display.logical_bounds.bottom()
    {
        return Ok(PixelRect {
            x: 0,
            y: 0,
            width: request.expected_frame_size.width,
            height: request.expected_frame_size.height,
        });
    }
    let display_scale = f64::from(display.scale.numerator) / f64::from(display.scale.denominator);
    let output_scale = f64::from(canvas_scale.numerator) / f64::from(canvas_scale.denominator);
    let logical_left = display.logical_bounds.x + f64::from(source.x) / display_scale;
    let logical_top = display.logical_bounds.y + f64::from(source.y) / display_scale;
    let logical_right =
        display.logical_bounds.x + f64::from(source.x + source.width) / display_scale;
    let logical_bottom =
        display.logical_bounds.y + f64::from(source.y + source.height) / display_scale;
    let canvas_left = (request.logical_bounds.x * output_scale).floor();
    let canvas_top = (request.logical_bounds.y * output_scale).floor();
    let left = (logical_left * output_scale).floor() - canvas_left;
    let top = (logical_top * output_scale).floor() - canvas_top;
    let right = (logical_right * output_scale).ceil() - canvas_left;
    let bottom = (logical_bottom * output_scale).ceil() - canvas_top;
    let values = [left, top, right, bottom];
    if values
        .iter()
        .any(|value| !value.is_finite() || *value < 0.0)
    {
        return Err(BackendFailure::CaptureFailed);
    }
    let left = left as u64;
    let top = top as u64;
    let right = right as u64;
    let bottom = bottom as u64;
    if right <= left
        || bottom <= top
        || right > u64::from(request.expected_frame_size.width)
        || bottom > u64::from(request.expected_frame_size.height)
    {
        return Err(BackendFailure::CaptureFailed);
    }
    Ok(PixelRect {
        x: u32::try_from(left).map_err(|_| BackendFailure::CaptureFailed)?,
        y: u32::try_from(top).map_err(|_| BackendFailure::CaptureFailed)?,
        width: u32::try_from(right - left).map_err(|_| BackendFailure::CaptureFailed)?,
        height: u32::try_from(bottom - top).map_err(|_| BackendFailure::CaptureFailed)?,
    })
}

fn window_source_rect(
    window: &WindowsWindow,
    display: &WindowsDisplay,
    region: PixelRect,
    frame: &NativeFrame,
) -> Result<PixelRect, BackendFailure> {
    let region_right = region
        .x
        .checked_add(region.width)
        .ok_or(BackendFailure::CaptureFailed)?;
    let region_bottom = region
        .y
        .checked_add(region.height)
        .ok_or(BackendFailure::CaptureFailed)?;
    let native_region = NativeRect {
        left: display
            .native_bounds
            .left
            .checked_add(i32::try_from(region.x).map_err(|_| BackendFailure::CaptureFailed)?)
            .ok_or(BackendFailure::CaptureFailed)?,
        top: display
            .native_bounds
            .top
            .checked_add(i32::try_from(region.y).map_err(|_| BackendFailure::CaptureFailed)?)
            .ok_or(BackendFailure::CaptureFailed)?,
        right: display
            .native_bounds
            .left
            .checked_add(i32::try_from(region_right).map_err(|_| BackendFailure::CaptureFailed)?)
            .ok_or(BackendFailure::CaptureFailed)?,
        bottom: display
            .native_bounds
            .top
            .checked_add(i32::try_from(region_bottom).map_err(|_| BackendFailure::CaptureFailed)?)
            .ok_or(BackendFailure::CaptureFailed)?,
    };
    let intersection = native_region
        .intersection(window.native_bounds)
        .ok_or(BackendFailure::CaptureFailed)?;
    let window_width = u64::from(window.native_bounds.width().map_err(BackendFailure::from)?);
    let window_height = u64::from(
        window
            .native_bounds
            .height()
            .map_err(BackendFailure::from)?,
    );
    let left_offset = u64::try_from(intersection.left - window.native_bounds.left)
        .map_err(|_| BackendFailure::CaptureFailed)?;
    let top_offset = u64::try_from(intersection.top - window.native_bounds.top)
        .map_err(|_| BackendFailure::CaptureFailed)?;
    let right_offset = u64::try_from(intersection.right - window.native_bounds.left)
        .map_err(|_| BackendFailure::CaptureFailed)?;
    let bottom_offset = u64::try_from(intersection.bottom - window.native_bounds.top)
        .map_err(|_| BackendFailure::CaptureFailed)?;
    let left = left_offset * u64::from(frame.width) / window_width;
    let top = top_offset * u64::from(frame.height) / window_height;
    let right = right_offset
        .checked_mul(u64::from(frame.width))
        .and_then(|value| value.checked_add(window_width - 1))
        .ok_or(BackendFailure::CaptureFailed)?
        / window_width;
    let bottom = bottom_offset
        .checked_mul(u64::from(frame.height))
        .and_then(|value| value.checked_add(window_height - 1))
        .ok_or(BackendFailure::CaptureFailed)?
        / window_height;
    if right <= left || bottom <= top {
        return Err(BackendFailure::CaptureFailed);
    }
    Ok(PixelRect {
        x: u32::try_from(left).map_err(|_| BackendFailure::CaptureFailed)?,
        y: u32::try_from(top).map_err(|_| BackendFailure::CaptureFailed)?,
        width: u32::try_from(right - left).map_err(|_| BackendFailure::CaptureFailed)?,
        height: u32::try_from(bottom - top).map_err(|_| BackendFailure::CaptureFailed)?,
    })
}

fn blit_scaled(
    source: &NativeFrame,
    source_rect: PixelRect,
    destination: &mut BackendFrame,
    destination_rect: PixelRect,
) -> Result<(), BackendFailure> {
    if source_rect.width == 0
        || source_rect.height == 0
        || destination_rect.width == 0
        || destination_rect.height == 0
        || source_rect.x.checked_add(source_rect.width).is_none()
        || source_rect.y.checked_add(source_rect.height).is_none()
        || source_rect.x + source_rect.width > source.width
        || source_rect.y + source_rect.height > source.height
        || destination_rect.x + destination_rect.width > destination.width
        || destination_rect.y + destination_rect.height > destination.height
    {
        return Err(BackendFailure::CaptureFailed);
    }
    for destination_y in 0..destination_rect.height {
        let source_y = source_rect.y
            + u32::try_from(
                u64::from(destination_y) * u64::from(source_rect.height)
                    / u64::from(destination_rect.height),
            )
            .map_err(|_| BackendFailure::CaptureFailed)?;
        for destination_x in 0..destination_rect.width {
            let source_x = source_rect.x
                + u32::try_from(
                    u64::from(destination_x) * u64::from(source_rect.width)
                        / u64::from(destination_rect.width),
                )
                .map_err(|_| BackendFailure::CaptureFailed)?;
            let source_index = pixel_offset(source.width, source_x, source_y)
                .ok_or(BackendFailure::CaptureFailed)?;
            let destination_index = pixel_offset(
                destination.width,
                destination_rect.x + destination_x,
                destination_rect.y + destination_y,
            )
            .ok_or(BackendFailure::CaptureFailed)?;
            destination.rgba[destination_index..destination_index + 4]
                .copy_from_slice(&source.rgba[source_index..source_index + 4]);
        }
    }
    Ok(())
}

fn pixel_offset(width: u32, x: u32, y: u32) -> Option<usize> {
    u64::from(y)
        .checked_mul(u64::from(width))?
        .checked_add(u64::from(x))?
        .checked_mul(4)?
        .try_into()
        .ok()
}

#[cfg(target_os = "windows")]
mod system {
    use std::{
        mem,
        sync::{Arc, Mutex, atomic::AtomicBool},
        thread,
        time::{Duration, Instant},
    };

    use windows::{
        Foundation::TypedEventHandler,
        Graphics::Capture::{GraphicsCaptureItem, GraphicsCaptureSession},
        Win32::{
            Foundation::{FILETIME, HWND, RECT},
            Graphics::{
                Dwm::{DWMWA_EXTENDED_FRAME_BOUNDS, DwmGetWindowAttribute},
                Gdi::{
                    DISPLAY_DEVICEW, EnumDisplayDevicesW, GetMonitorInfoW, HMONITOR, MONITORINFO,
                },
            },
            System::Threading::{GetProcessTimes, OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION},
            UI::{
                HiDpi::{GetDpiForMonitor, MDT_EFFECTIVE_DPI},
                WindowsAndMessaging::{
                    EDD_GET_DEVICE_INTERFACE_NAME, GetWindowDisplayAffinity, IsIconic, IsWindow,
                    MONITORINFOF_PRIMARY,
                },
            },
        },
        core::{IInspectable, Owned, PCWSTR},
    };
    use windows_capture::{
        capture::{Context, GraphicsCaptureApiError, GraphicsCaptureApiHandler},
        frame::Frame,
        graphics_capture_api::InternalCaptureControl,
        monitor::Monitor,
        settings::{
            ColorFormat, CursorCaptureSettings, DirtyRegionSettings, DrawBorderSettings,
            GraphicsCaptureItemType, MinimumUpdateIntervalSettings, SecondaryWindowSettings,
            Settings,
        },
        window::Window,
    };

    use super::*;

    #[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
    struct SystemWindowIdentity {
        raw_hwnd: usize,
        process_id: u32,
        process_started_at: u64,
    }

    struct RetainedWindowSource {
        id: NativeSourceId,
        item: GraphicsCaptureItem,
        window: Window,
        closed: Arc<AtomicBool>,
    }

    #[derive(Default)]
    struct SystemSourceRegistry {
        next_window_id: usize,
        windows: HashMap<SystemWindowIdentity, RetainedWindowSource>,
    }

    #[derive(Default)]
    pub(super) struct SystemWindowsAdapter {
        sources: Mutex<SystemSourceRegistry>,
    }

    pub(super) fn is_supported() -> bool {
        let version = windows_version::OsVersion::current();
        is_supported_windows_version(version.major, version.build)
    }

    impl WindowsPlatformAdapter for SystemWindowsAdapter {
        fn permission(&self) -> Result<CapturePermission, WindowsAdapterFailure> {
            GraphicsCaptureSession::IsSupported()
                .map_err(|_| WindowsAdapterFailure::Unavailable)
                .and_then(permission_from_support)
        }

        fn displays(&self) -> Result<Vec<WindowsDisplay>, WindowsAdapterFailure> {
            let mut displays = Monitor::enumerate()
                .map_err(|_| WindowsAdapterFailure::Failed)?
                .into_iter()
                .map(display_record)
                .collect::<Result<Vec<_>, _>>()?;
            displays.sort_by(|left, right| {
                (
                    left.native_bounds.left,
                    left.native_bounds.top,
                    &left.stable_key,
                )
                    .cmp(&(
                        right.native_bounds.left,
                        right.native_bounds.top,
                        &right.stable_key,
                    ))
            });
            normalize_display_layout(&mut displays)?;
            Ok(displays)
        }

        fn windows(&self) -> Result<Vec<WindowsWindow>, WindowsAdapterFailure> {
            let displays = self.displays()?;
            let windows = Window::enumerate()
                .map_err(|_| WindowsAdapterFailure::Failed)?
                .into_iter()
                .filter_map(|window| {
                    window_record(window, &displays)
                        .transpose()
                        .map(|result| result.map(|record| (window, record)))
                })
                .collect::<Result<Vec<_>, _>>()?;
            self.retain_window_sources(windows)
        }

        fn capture(
            &self,
            source: NativeCaptureSource,
            pointer: PointerInclusion,
            cancel: Arc<AtomicBool>,
        ) -> Result<NativeFrame, WindowsAdapterFailure> {
            let source = self.capture_source(source)?;
            if let SystemCaptureSource::Window { window, .. } = &source {
                if unsafe { IsIconic(HWND(window.as_raw_hwnd())).as_bool() } {
                    return Err(WindowsAdapterFailure::WindowMinimized);
                }
                if window_is_protected(*window) {
                    return Err(WindowsAdapterFailure::ProtectedContent);
                }
            }
            capture_one(source, pointer, cancel)
        }
    }

    impl SystemWindowsAdapter {
        fn retain_window_sources(
            &self,
            windows: Vec<(Window, WindowsWindow)>,
        ) -> Result<Vec<WindowsWindow>, WindowsAdapterFailure> {
            let mut sources = self
                .sources
                .lock()
                .map_err(|_| WindowsAdapterFailure::Failed)?;
            let mut previous = mem::take(&mut sources.windows);
            let mut current = HashMap::with_capacity(windows.len());
            let mut records = Vec::with_capacity(windows.len());

            for (window, mut record) in windows {
                let identity = SystemWindowIdentity {
                    raw_hwnd: window.as_raw_hwnd() as usize,
                    process_id: record.process_id,
                    process_started_at: record.process_started_at,
                };
                let retained = match previous.remove(&identity) {
                    Some(retained) if !retained.closed.load(Ordering::Acquire) => retained,
                    _ => {
                        sources.next_window_id = sources
                            .next_window_id
                            .checked_add(1)
                            .ok_or(WindowsAdapterFailure::Failed)?;
                        let raw_hwnd = window.as_raw_hwnd();
                        match retained_window_source(window, NativeSourceId(sources.next_window_id))
                        {
                            Ok(retained) => retained,
                            Err(_)
                                if should_skip_failed_window_retention(unsafe {
                                    IsWindow(Some(HWND(raw_hwnd))).as_bool()
                                }) =>
                            {
                                // A transient window can close after metadata enumeration but
                                // before its WGC item is retained; skip only that vanished source.
                                continue;
                            }
                            Err(error) => return Err(error),
                        }
                    }
                };
                record.source = retained.id;
                current.insert(identity, retained);
                records.push(record);
            }

            sources.windows = current;
            Ok(records)
        }

        fn capture_source(
            &self,
            source: NativeCaptureSource,
        ) -> Result<SystemCaptureSource, WindowsAdapterFailure> {
            let id = source.id();
            match source {
                NativeCaptureSource::Window(_) => {
                    let sources = self
                        .sources
                        .lock()
                        .map_err(|_| WindowsAdapterFailure::Failed)?;
                    let retained = sources
                        .windows
                        .values()
                        .find(|retained| retained.id == id)
                        .ok_or(WindowsAdapterFailure::WindowClosed)?;
                    if retained.closed.load(Ordering::Acquire) {
                        return Err(WindowsAdapterFailure::WindowClosed);
                    }
                    Ok(SystemCaptureSource::Window {
                        item: retained.item.clone(),
                        window: retained.window,
                    })
                }
                NativeCaptureSource::Display(_) => {
                    let monitor = Monitor::from_raw_hmonitor(id.0 as *mut std::ffi::c_void);
                    Monitor::enumerate()
                        .map_err(|_| WindowsAdapterFailure::Failed)?
                        .contains(&monitor)
                        .then_some(SystemCaptureSource::Monitor(monitor))
                        .ok_or(WindowsAdapterFailure::DisplayRemoved)
                }
            }
        }
    }

    fn retained_window_source(
        window: Window,
        id: NativeSourceId,
    ) -> Result<RetainedWindowSource, WindowsAdapterFailure> {
        let GraphicsCaptureItemType::Window((item, window)) = window
            .try_into()
            .map_err(|_| WindowsAdapterFailure::Failed)?
        else {
            return Err(WindowsAdapterFailure::Failed);
        };
        let closed = Arc::new(AtomicBool::new(false));
        item.Closed(
            &TypedEventHandler::<GraphicsCaptureItem, IInspectable>::new({
                let closed = closed.clone();
                move |_, _| {
                    closed.store(true, Ordering::Release);
                    Ok(())
                }
            }),
        )
        .map_err(|_| WindowsAdapterFailure::Failed)?;
        Ok(RetainedWindowSource {
            id,
            item,
            window,
            closed,
        })
    }

    fn display_record(monitor: Monitor) -> Result<WindowsDisplay, WindowsAdapterFailure> {
        let raw = monitor.as_raw_hmonitor();
        let mut info = MONITORINFO {
            cbSize: u32::try_from(mem::size_of::<MONITORINFO>())
                .map_err(|_| WindowsAdapterFailure::Failed)?,
            ..MONITORINFO::default()
        };
        if !unsafe { GetMonitorInfoW(HMONITOR(raw), &mut info).as_bool() } {
            return Err(WindowsAdapterFailure::Failed);
        }
        let native_bounds = native_rect(info.rcMonitor);
        let width = native_bounds.width()?;
        let height = native_bounds.height()?;
        let mut dpi_x = 0;
        let mut dpi_y = 0;
        unsafe { GetDpiForMonitor(HMONITOR(raw), MDT_EFFECTIVE_DPI, &mut dpi_x, &mut dpi_y) }
            .map_err(|_| WindowsAdapterFailure::Failed)?;
        if dpi_x == 0 || dpi_x != dpi_y {
            return Err(WindowsAdapterFailure::Failed);
        }
        let divisor = greatest_common_divisor(dpi_x, 96);
        let scale = ScaleFactor {
            numerator: dpi_x / divisor,
            denominator: 96 / divisor,
        };
        let scale_value = f64::from(dpi_x) / 96.0;
        let stable_key = monitor
            .device_name()
            .map_err(|_| WindowsAdapterFailure::Failed)?;
        Ok(WindowsDisplay {
            source: NativeSourceId(raw as usize),
            device_interface_key: monitor_device_interface_key(&stable_key)?,
            stable_key,
            native_bounds,
            logical_bounds: LogicalRect {
                x: 0.0,
                y: 0.0,
                width: f64::from(width) / scale_value,
                height: f64::from(height) / scale_value,
            },
            physical_size: PhysicalSize { width, height },
            scale,
            primary: info.dwFlags & MONITORINFOF_PRIMARY != 0,
        })
    }

    fn monitor_device_interface_key(device_name: &str) -> Result<String, WindowsAdapterFailure> {
        let wide_device_name = device_name
            .encode_utf16()
            .chain(std::iter::once(0))
            .collect::<Vec<_>>();
        let mut device = DISPLAY_DEVICEW {
            cb: u32::try_from(mem::size_of::<DISPLAY_DEVICEW>())
                .map_err(|_| WindowsAdapterFailure::Failed)?,
            ..DISPLAY_DEVICEW::default()
        };
        if !unsafe {
            EnumDisplayDevicesW(
                PCWSTR(wide_device_name.as_ptr()),
                0,
                &mut device,
                EDD_GET_DEVICE_INTERFACE_NAME,
            )
            .as_bool()
        } {
            return Err(WindowsAdapterFailure::Failed);
        }
        let length = device
            .DeviceID
            .iter()
            .position(|unit| *unit == 0)
            .unwrap_or(device.DeviceID.len());
        if length == 0 {
            return Err(WindowsAdapterFailure::Failed);
        }
        String::from_utf16(&device.DeviceID[..length]).map_err(|_| WindowsAdapterFailure::Failed)
    }

    fn greatest_common_divisor(mut left: u32, mut right: u32) -> u32 {
        while right != 0 {
            let remainder = left % right;
            left = right;
            right = remainder;
        }
        left
    }

    fn window_record(
        window: Window,
        displays: &[WindowsDisplay],
    ) -> Result<Option<WindowsWindow>, WindowsAdapterFailure> {
        let Some(monitor) = window.monitor() else {
            return Ok(None);
        };
        let monitor_key = monitor
            .device_name()
            .map_err(|_| WindowsAdapterFailure::Failed)?;
        let Some(display) = displays.iter().find(|display| {
            display.stable_key == monitor_key
                && display.source == NativeSourceId(monitor.as_raw_hmonitor() as usize)
        }) else {
            return Ok(None);
        };
        let Some((process_id, process_started_at)) = window_process_identity(window) else {
            return Ok(None);
        };
        let minimized = unsafe { IsIconic(HWND(window.as_raw_hwnd())).as_bool() };
        let native_bounds = if minimized {
            let Ok(rect) = window.rect() else {
                return Ok(None);
            };
            native_rect(rect)
        } else {
            let Ok(bounds) = visible_window_bounds(window) else {
                return Ok(None);
            };
            bounds
        };
        let width = native_bounds.right - native_bounds.left;
        let height = native_bounds.bottom - native_bounds.top;
        if !minimized && (width <= 0 || height <= 0) {
            return Ok(None);
        }
        let bounds = if minimized {
            LogicalRect {
                x: 0.0,
                y: 0.0,
                width: 0.0,
                height: 0.0,
            }
        } else {
            let Some(bounds) = logical_window_bounds(native_bounds, displays)? else {
                return Ok(None);
            };
            bounds
        };
        let state = if minimized {
            WindowsWindowState::Minimized
        } else if window_is_protected(window) {
            WindowsWindowState::Protected
        } else {
            WindowsWindowState::Available
        };
        Ok(Some(WindowsWindow {
            source: NativeSourceId(window.as_raw_hwnd() as usize),
            stable_key: format!("hwnd:{:016x}", window.as_raw_hwnd() as usize),
            process_id,
            process_started_at,
            display_key: display.stable_key.clone(),
            display_device_interface_key: display.device_interface_key.clone(),
            display_source: display.source,
            native_bounds,
            bounds,
            state,
            process_name: window.process_name().ok(),
            title: window.title().ok(),
        }))
    }

    fn visible_window_bounds(window: Window) -> Result<NativeRect, WindowsAdapterFailure> {
        let mut rect = RECT::default();
        let size =
            u32::try_from(mem::size_of::<RECT>()).map_err(|_| WindowsAdapterFailure::Failed)?;
        unsafe {
            DwmGetWindowAttribute(
                HWND(window.as_raw_hwnd()),
                DWMWA_EXTENDED_FRAME_BOUNDS,
                std::ptr::addr_of_mut!(rect).cast(),
                size,
            )
        }
        .map_err(|_| WindowsAdapterFailure::Failed)?;
        Ok(native_rect(rect))
    }

    fn window_process_identity(window: Window) -> Option<(u32, u64)> {
        let process_id = window.process_id().ok()?;
        let process =
            unsafe { OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, process_id) }.ok()?;
        let process = unsafe { Owned::new(process) };
        let mut created = FILETIME::default();
        let mut exited = FILETIME::default();
        let mut kernel = FILETIME::default();
        let mut user = FILETIME::default();
        unsafe { GetProcessTimes(*process, &mut created, &mut exited, &mut kernel, &mut user) }
            .ok()?;
        let started_at =
            (u64::from(created.dwHighDateTime) << 32) | u64::from(created.dwLowDateTime);
        Some((process_id, started_at))
    }

    const fn native_rect(rect: RECT) -> NativeRect {
        NativeRect {
            left: rect.left,
            top: rect.top,
            right: rect.right,
            bottom: rect.bottom,
        }
    }

    fn window_is_protected(window: Window) -> bool {
        let mut affinity = 0_u32;
        unsafe {
            GetWindowDisplayAffinity(HWND(window.as_raw_hwnd()), &mut affinity).is_ok()
                && affinity != 0
        }
    }

    #[derive(Clone)]
    enum SystemCaptureSource {
        Monitor(Monitor),
        Window {
            item: GraphicsCaptureItem,
            window: Window,
        },
    }

    impl TryInto<GraphicsCaptureItemType> for SystemCaptureSource {
        type Error = windows::core::Error;

        fn try_into(self) -> Result<GraphicsCaptureItemType, Self::Error> {
            match self {
                Self::Monitor(monitor) => monitor.try_into(),
                Self::Window { item, window } => {
                    Ok(GraphicsCaptureItemType::Window((item, window)))
                }
            }
        }
    }

    struct CaptureShared {
        result: Mutex<Option<Result<NativeFrame, WindowsAdapterFailure>>>,
        cancelled: Arc<AtomicBool>,
        source_lost: WindowsAdapterFailure,
    }

    struct OneShotCapture {
        shared: Arc<CaptureShared>,
    }

    impl GraphicsCaptureApiHandler for OneShotCapture {
        type Error = WindowsAdapterFailure;
        type Flags = Arc<CaptureShared>;

        fn new(context: Context<Self::Flags>) -> Result<Self, Self::Error> {
            Ok(Self {
                shared: context.flags,
            })
        }

        fn on_frame_arrived(
            &mut self,
            frame: &mut Frame,
            capture_control: InternalCaptureControl,
        ) -> Result<(), Self::Error> {
            let result = if self.shared.cancelled.load(Ordering::Acquire) {
                Err(WindowsAdapterFailure::Cancelled)
            } else {
                let width = frame.width();
                let height = frame.height();
                if decoded_byte_len(width, height).is_err() {
                    Err(WindowsAdapterFailure::Failed)
                } else {
                    let buffer = frame.buffer().map_err(|_| WindowsAdapterFailure::Failed)?;
                    let mut contiguous = Vec::new();
                    let rgba = buffer.as_nopadding_buffer(&mut contiguous).to_vec();
                    Ok(NativeFrame {
                        width,
                        height,
                        rgba,
                    })
                }
            };
            *self
                .shared
                .result
                .lock()
                .map_err(|_| WindowsAdapterFailure::Failed)? = Some(result);
            capture_control.stop();
            Ok(())
        }

        fn on_closed(&mut self) -> Result<(), Self::Error> {
            *self
                .shared
                .result
                .lock()
                .map_err(|_| WindowsAdapterFailure::Failed)? = Some(Err(self.shared.source_lost));
            Ok(())
        }
    }

    fn capture_one(
        source: SystemCaptureSource,
        pointer: PointerInclusion,
        cancel: Arc<AtomicBool>,
    ) -> Result<NativeFrame, WindowsAdapterFailure> {
        let source_lost = match &source {
            SystemCaptureSource::Monitor(_) => WindowsAdapterFailure::DisplayRemoved,
            SystemCaptureSource::Window { .. } => WindowsAdapterFailure::WindowClosed,
        };
        let shared = Arc::new(CaptureShared {
            result: Mutex::new(None),
            cancelled: cancel.clone(),
            source_lost,
        });
        let settings = Settings::new(
            source,
            match pointer {
                PointerInclusion::Include => CursorCaptureSettings::WithCursor,
                PointerInclusion::Exclude => CursorCaptureSettings::WithoutCursor,
            },
            DrawBorderSettings::WithoutBorder,
            SecondaryWindowSettings::Default,
            MinimumUpdateIntervalSettings::Default,
            DirtyRegionSettings::Default,
            ColorFormat::Rgba8,
            shared.clone(),
        );
        let control = match OneShotCapture::start_free_threaded(settings) {
            Ok(control) => control,
            Err(GraphicsCaptureApiError::ItemConvertFailed) => return Err(source_lost),
            Err(_) => return Err(WindowsAdapterFailure::Failed),
        };
        let started = Instant::now();
        while !control.is_finished() {
            if cancel.load(Ordering::Acquire) {
                control.stop().map_err(|_| WindowsAdapterFailure::Failed)?;
                return Err(WindowsAdapterFailure::Cancelled);
            }
            if started.elapsed() >= Duration::from_secs(10) {
                control.stop().map_err(|_| WindowsAdapterFailure::Failed)?;
                return Err(WindowsAdapterFailure::Failed);
            }
            thread::sleep(Duration::from_millis(2));
        }
        control.wait().map_err(|_| WindowsAdapterFailure::Failed)?;
        shared
            .result
            .lock()
            .map_err(|_| WindowsAdapterFailure::Failed)?
            .take()
            .ok_or(WindowsAdapterFailure::Failed)?
    }
}

#[cfg(test)]
mod tests {
    use std::{
        sync::{Barrier, atomic::AtomicUsize},
        thread,
    };

    use super::*;
    use crate::realqa_capture::{
        CaptureCore, CaptureFailure, CaptureMode, CaptureRequest, CaptureSourceSelection,
        ImageMediaType, SelectionGeometry, decode_image,
    };

    struct FixtureAdapter {
        permission: Mutex<Result<CapturePermission, WindowsAdapterFailure>>,
        displays: Mutex<Vec<WindowsDisplay>>,
        windows: Mutex<Vec<WindowsWindow>>,
        frames: Mutex<HashMap<NativeSourceId, Result<NativeFrame, WindowsAdapterFailure>>>,
        captures: Mutex<Vec<(NativeCaptureSource, PointerInclusion)>>,
        display_calls: AtomicUsize,
        remove_display_on_call: Mutex<Option<usize>>,
        displays_after_capture: Mutex<Option<Vec<WindowsDisplay>>>,
        window_after_capture: Mutex<Option<WindowsWindow>>,
        permission_barrier: Mutex<Option<Arc<Barrier>>>,
    }

    impl FixtureAdapter {
        fn mixed_dpi() -> Self {
            let left = WindowsDisplay {
                source: NativeSourceId(1),
                stable_key: "private-device-left".to_owned(),
                device_interface_key: "private-monitor-left".to_owned(),
                native_bounds: NativeRect {
                    left: -2,
                    top: 0,
                    right: 0,
                    bottom: 2,
                },
                logical_bounds: LogicalRect {
                    x: -2.0,
                    y: 0.0,
                    width: 2.0,
                    height: 2.0,
                },
                physical_size: PhysicalSize {
                    width: 2,
                    height: 2,
                },
                scale: ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
                primary: false,
            };
            let right = WindowsDisplay {
                source: NativeSourceId(2),
                stable_key: "private-device-right".to_owned(),
                device_interface_key: "private-monitor-right".to_owned(),
                native_bounds: NativeRect {
                    left: 0,
                    top: 0,
                    right: 4,
                    bottom: 4,
                },
                logical_bounds: LogicalRect {
                    x: 0.0,
                    y: 0.0,
                    width: 2.0,
                    height: 2.0,
                },
                physical_size: PhysicalSize {
                    width: 4,
                    height: 4,
                },
                scale: ScaleFactor {
                    numerator: 2,
                    denominator: 1,
                },
                primary: true,
            };
            let mut frames = HashMap::new();
            frames.insert(
                left.source,
                Ok(solid_frame(left.physical_size, [255, 0, 0, 255])),
            );
            frames.insert(
                right.source,
                Ok(solid_frame(right.physical_size, [0, 0, 255, 255])),
            );
            Self {
                permission: Mutex::new(Ok(CapturePermission::Granted)),
                displays: Mutex::new(vec![left, right]),
                windows: Mutex::new(Vec::new()),
                frames: Mutex::new(frames),
                captures: Mutex::new(Vec::new()),
                display_calls: AtomicUsize::new(0),
                remove_display_on_call: Mutex::new(None),
                displays_after_capture: Mutex::new(None),
                window_after_capture: Mutex::new(None),
                permission_barrier: Mutex::new(None),
            }
        }
    }

    impl WindowsPlatformAdapter for FixtureAdapter {
        fn permission(&self) -> Result<CapturePermission, WindowsAdapterFailure> {
            let permission = *self.permission.lock().expect("permission lock");
            let barrier = self
                .permission_barrier
                .lock()
                .expect("permission barrier lock")
                .clone();
            if let Some(barrier) = barrier {
                barrier.wait();
                barrier.wait();
            }
            permission
        }

        fn displays(&self) -> Result<Vec<WindowsDisplay>, WindowsAdapterFailure> {
            let call = self.display_calls.fetch_add(1, Ordering::Relaxed) + 1;
            let mut displays = self.displays.lock().expect("display lock").clone();
            if self
                .remove_display_on_call
                .lock()
                .expect("remove call lock")
                .is_some_and(|remove_call| call >= remove_call)
            {
                displays.pop();
            }
            Ok(displays)
        }

        fn windows(&self) -> Result<Vec<WindowsWindow>, WindowsAdapterFailure> {
            Ok(self.windows.lock().expect("window lock").clone())
        }

        fn capture(
            &self,
            source: NativeCaptureSource,
            pointer: PointerInclusion,
            cancel: Arc<AtomicBool>,
        ) -> Result<NativeFrame, WindowsAdapterFailure> {
            if cancel.load(Ordering::Acquire) {
                return Err(WindowsAdapterFailure::Cancelled);
            }
            self.captures
                .lock()
                .expect("captures lock")
                .push((source, pointer));
            let result = self
                .frames
                .lock()
                .expect("frames lock")
                .get(&source.id())
                .cloned()
                .unwrap_or(Err(WindowsAdapterFailure::Failed));
            if let Some(displays) = self
                .displays_after_capture
                .lock()
                .expect("displays after capture lock")
                .take()
            {
                *self.displays.lock().expect("display lock") = displays;
            }
            if let Some(window) = self
                .window_after_capture
                .lock()
                .expect("window after capture lock")
                .take()
            {
                *self.windows.lock().expect("window lock") = vec![window];
            }
            result
        }
    }

    fn solid_frame(size: PhysicalSize, pixel: [u8; 4]) -> NativeFrame {
        NativeFrame {
            width: size.width,
            height: size.height,
            rgba: pixel
                .into_iter()
                .cycle()
                .take((size.width * size.height * 4) as usize)
                .collect(),
        }
    }

    fn vertical_stripes_frame(height: u32, pixels: &[[u8; 4]]) -> NativeFrame {
        let width = u32::try_from(pixels.len()).expect("fixture width");
        let mut rgba = Vec::with_capacity((width * height * 4) as usize);
        for _ in 0..height {
            for pixel in pixels {
                rgba.extend_from_slice(pixel);
            }
        }
        NativeFrame {
            width,
            height,
            rgba,
        }
    }

    fn available_window(
        source: NativeSourceId,
        display: &WindowsDisplay,
        bounds: LogicalRect,
    ) -> WindowsWindow {
        let scale = scale_value(display.scale).expect("fixture scale");
        WindowsWindow {
            source,
            stable_key: format!("private-window-{}", source.0),
            process_id: 30,
            process_started_at: 300,
            display_key: display.stable_key.clone(),
            display_device_interface_key: display.device_interface_key.clone(),
            display_source: display.source,
            native_bounds: NativeRect {
                left: display.native_bounds.left
                    + ((bounds.x - display.logical_bounds.x) * scale) as i32,
                top: display.native_bounds.top
                    + ((bounds.y - display.logical_bounds.y) * scale) as i32,
                right: display.native_bounds.left
                    + ((bounds.right() - display.logical_bounds.x) * scale) as i32,
                bottom: display.native_bounds.top
                    + ((bounds.bottom() - display.logical_bounds.y) * scale) as i32,
            },
            bounds,
            state: WindowsWindowState::Available,
            process_name: Some("safe.exe".to_owned()),
            title: Some("Window".to_owned()),
        }
    }

    fn window_request(
        catalog: &super::super::CaptureSourceCatalog,
        session_id: &str,
    ) -> CaptureRequest {
        CaptureRequest {
            session_id: CaptureSessionId(session_id.to_owned()),
            snapshot_id: catalog.snapshot.snapshot_id.clone(),
            source: CaptureSourceSelection::Window {
                window_id: catalog.windows[0].id.clone(),
            },
            pointer: PointerInclusion::Exclude,
            output_media_type: ImageMediaType::Png,
        }
    }

    fn multi_monitor_request(catalog: &super::super::CaptureSourceCatalog) -> CaptureRequest {
        CaptureRequest {
            session_id: CaptureSessionId("windows-session".to_owned()),
            snapshot_id: catalog.snapshot.snapshot_id.clone(),
            source: CaptureSourceSelection::MultiMonitor {
                display_ids: catalog
                    .snapshot
                    .displays
                    .iter()
                    .map(|display| display.id.clone())
                    .collect(),
            },
            pointer: PointerInclusion::Exclude,
            output_media_type: ImageMediaType::Png,
        }
    }

    #[test]
    fn normalizes_adjacent_mixed_dpi_origins_in_one_logical_space() {
        let mut displays = vec![
            WindowsDisplay {
                source: NativeSourceId(1),
                stable_key: "high-dpi-primary".to_owned(),
                device_interface_key: "monitor-high-dpi-primary".to_owned(),
                native_bounds: NativeRect {
                    left: 0,
                    top: 0,
                    right: 3840,
                    bottom: 2160,
                },
                logical_bounds: LogicalRect {
                    x: 0.0,
                    y: 0.0,
                    width: 0.0,
                    height: 0.0,
                },
                physical_size: PhysicalSize {
                    width: 3840,
                    height: 2160,
                },
                scale: ScaleFactor {
                    numerator: 2,
                    denominator: 1,
                },
                primary: true,
            },
            WindowsDisplay {
                source: NativeSourceId(2),
                stable_key: "low-dpi-right".to_owned(),
                device_interface_key: "monitor-low-dpi-right".to_owned(),
                native_bounds: NativeRect {
                    left: 3840,
                    top: 0,
                    right: 5760,
                    bottom: 1080,
                },
                logical_bounds: LogicalRect {
                    x: 0.0,
                    y: 0.0,
                    width: 0.0,
                    height: 0.0,
                },
                physical_size: PhysicalSize {
                    width: 1920,
                    height: 1080,
                },
                scale: ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
                primary: false,
            },
            WindowsDisplay {
                source: NativeSourceId(3),
                stable_key: "low-dpi-below".to_owned(),
                device_interface_key: "monitor-low-dpi-below".to_owned(),
                native_bounds: NativeRect {
                    left: 0,
                    top: 2160,
                    right: 1920,
                    bottom: 3240,
                },
                logical_bounds: LogicalRect {
                    x: 0.0,
                    y: 0.0,
                    width: 0.0,
                    height: 0.0,
                },
                physical_size: PhysicalSize {
                    width: 1920,
                    height: 1080,
                },
                scale: ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
                primary: false,
            },
        ];

        normalize_display_layout(&mut displays).expect("normalize layout");

        assert_eq!(
            displays[0].logical_bounds,
            LogicalRect {
                x: 0.0,
                y: 0.0,
                width: 1920.0,
                height: 1080.0,
            }
        );
        assert_eq!(displays[1].logical_bounds.x, 1920.0);
        assert_eq!(displays[1].logical_bounds.y, 0.0);
        assert_eq!(displays[2].logical_bounds.x, 0.0);
        assert_eq!(displays[2].logical_bounds.y, 1080.0);
    }

    #[test]
    fn derives_spanning_window_bounds_per_display_scale() {
        let mut adapter = FixtureAdapter::mixed_dpi();
        let displays = adapter.displays.get_mut().expect("display lock");
        let bounds = logical_window_bounds(
            NativeRect {
                left: -1,
                top: 0,
                right: 2,
                bottom: 2,
            },
            displays,
        )
        .expect("logical bounds")
        .expect("visible window");
        assert_eq!(
            bounds,
            LogicalRect {
                x: -1.0,
                y: 0.0,
                width: 2.0,
                height: 2.0,
            }
        );
    }

    #[test]
    fn composites_negative_mixed_dpi_displays_at_the_highest_scale() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter.clone())));
        let catalog = core.source_catalog().expect("catalog");
        let result = core
            .begin(multi_monitor_request(&catalog))
            .expect("capture");
        assert_eq!(result.mode, CaptureMode::MultiMonitor);
        assert_eq!(
            result.logical_bounds,
            LogicalRect {
                x: -2.0,
                y: 0.0,
                width: 4.0,
                height: 2.0,
            }
        );
        let decoded = decode_image(&result.image).expect("decode capture");
        assert_eq!((decoded.width, decoded.height), (8, 4));
        assert_eq!(&decoded.rgba[0..4], &[255, 0, 0, 255]);
        let right_pixel = pixel_offset(decoded.width, 7, 0).expect("pixel offset");
        assert_eq!(
            &decoded.rgba[right_pixel..right_pixel + 4],
            &[0, 0, 255, 255]
        );
        assert_eq!(
            adapter.captures.lock().expect("captures lock").as_slice(),
            &[
                (
                    NativeCaptureSource::Display(NativeSourceId(1)),
                    PointerInclusion::Exclude,
                ),
                (
                    NativeCaptureSource::Display(NativeSourceId(2)),
                    PointerInclusion::Exclude,
                ),
            ]
        );
    }

    #[test]
    fn rejects_display_topology_changed_during_window_enumeration() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        *adapter
            .remove_display_on_call
            .lock()
            .expect("remove call lock") = Some(2);
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter)));

        assert_eq!(
            core.source_catalog(),
            Err(CaptureFailure::DisplaySnapshotChanged)
        );
    }

    #[test]
    fn preserves_desktop_gap_when_region_intersects_only_one_display() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        adapter.displays.lock().expect("display lock")[1]
            .logical_bounds
            .x = 1.0;
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter)));
        let catalog = core.source_catalog().expect("catalog");
        let result = core
            .begin(CaptureRequest {
                session_id: CaptureSessionId("single-display-gap".to_owned()),
                snapshot_id: catalog.snapshot.snapshot_id.clone(),
                source: CaptureSourceSelection::Region {
                    selection: SelectionGeometry {
                        snapshot_id: catalog.snapshot.snapshot_id.clone(),
                        bounds: LogicalRect {
                            x: -1.0,
                            y: 0.0,
                            width: 2.0,
                            height: 1.0,
                        },
                    },
                },
                pointer: PointerInclusion::Exclude,
                output_media_type: ImageMediaType::Png,
            })
            .expect("capture");
        let decoded = decode_image(&result.image).expect("decode capture");
        assert_eq!((decoded.width, decoded.height), (2, 1));
        assert_eq!(&decoded.rgba[0..4], &[255, 0, 0, 255]);
        assert_eq!(&decoded.rgba[4..8], &[0, 0, 0, 0]);
    }

    #[test]
    fn composites_spanning_window_through_each_mixed_dpi_display_region() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        {
            let mut displays = adapter.displays.lock().expect("display lock");
            displays[1].logical_bounds.y = 1.0;
            displays[1].native_bounds.top = 2;
            displays[1].native_bounds.bottom = 6;
        }
        let displays = adapter.displays.lock().expect("display lock").clone();
        let mut window = available_window(
            NativeSourceId(3),
            &displays[1],
            LogicalRect {
                x: -1.0,
                y: 0.0,
                width: 2.0,
                height: 3.0,
            },
        );
        window.native_bounds.left = -1;
        window.bounds = logical_window_bounds(window.native_bounds, &displays)
            .expect("logical bounds")
            .expect("visible window");
        adapter.windows.lock().expect("window lock").push(window);
        adapter.frames.lock().expect("frames lock").insert(
            NativeSourceId(3),
            Ok(vertical_stripes_frame(
                6,
                &[[255, 0, 0, 255], [0, 255, 0, 255], [0, 255, 0, 255]],
            )),
        );
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter.clone())));
        let catalog = core.source_catalog().expect("catalog");
        let result = core
            .begin(window_request(&catalog, "spanning-window"))
            .expect("capture");
        let decoded = decode_image(&result.image).expect("decode capture");
        assert_eq!((decoded.width, decoded.height), (4, 6));
        assert_eq!(
            &decoded.rgba[pixel_offset(decoded.width, 0, 0).expect("top left")
                ..pixel_offset(decoded.width, 0, 0).expect("top left") + 4],
            &[255, 0, 0, 255]
        );
        assert_eq!(
            &decoded.rgba[pixel_offset(decoded.width, 3, 0).expect("top gap")
                ..pixel_offset(decoded.width, 3, 0).expect("top gap") + 4],
            &[0, 0, 0, 0]
        );
        assert_eq!(
            &decoded.rgba[pixel_offset(decoded.width, 0, 5).expect("bottom gap")
                ..pixel_offset(decoded.width, 0, 5).expect("bottom gap") + 4],
            &[0, 0, 0, 0]
        );
        assert_eq!(
            &decoded.rgba[pixel_offset(decoded.width, 3, 5).expect("bottom right")
                ..pixel_offset(decoded.width, 3, 5).expect("bottom right") + 4],
            &[0, 255, 0, 255]
        );
        assert_eq!(
            adapter.captures.lock().expect("captures lock").as_slice(),
            &[(
                NativeCaptureSource::Window(NativeSourceId(3)),
                PointerInclusion::Exclude,
            )]
        );
    }

    #[test]
    fn rejects_spanned_display_replacement_while_waiting_for_window_frame() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        {
            let mut displays = adapter.displays.lock().expect("display lock");
            displays[1].logical_bounds.y = 1.0;
            displays[1].native_bounds.top = 2;
            displays[1].native_bounds.bottom = 6;
        }
        let displays = adapter.displays.lock().expect("display lock").clone();
        let mut window = available_window(
            NativeSourceId(3),
            &displays[1],
            LogicalRect {
                x: -1.0,
                y: 0.0,
                width: 2.0,
                height: 3.0,
            },
        );
        window.native_bounds.left = -1;
        window.bounds = logical_window_bounds(window.native_bounds, &displays)
            .expect("logical bounds")
            .expect("visible window");
        adapter
            .windows
            .lock()
            .expect("window lock")
            .push(window.clone());
        adapter.frames.lock().expect("frames lock").insert(
            window.source,
            Ok(solid_frame(
                PhysicalSize {
                    width: 3,
                    height: 6,
                },
                [0, 255, 0, 255],
            )),
        );
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter.clone())));
        let catalog = core.source_catalog().expect("catalog");
        let mut changed = displays;
        changed[0].source = NativeSourceId(99);
        *adapter
            .displays_after_capture
            .lock()
            .expect("displays after capture lock") = Some(changed);

        assert_eq!(
            core.begin(window_request(&catalog, "spanned-display-replaced")),
            Err(CaptureFailure::DisplayRemoved)
        );
    }

    #[test]
    fn rejects_window_geometry_changed_while_waiting_for_first_frame() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let display = adapter.displays.lock().expect("display lock")[1].clone();
        let window = available_window(
            NativeSourceId(3),
            &display,
            LogicalRect {
                x: 0.0,
                y: 0.0,
                width: 2.0,
                height: 2.0,
            },
        );
        adapter
            .windows
            .lock()
            .expect("window lock")
            .push(window.clone());
        adapter.frames.lock().expect("frames lock").insert(
            window.source,
            Ok(solid_frame(
                PhysicalSize {
                    width: 4,
                    height: 4,
                },
                [0, 255, 0, 255],
            )),
        );
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter.clone())));
        let catalog = core.source_catalog().expect("catalog");
        let mut moved = window;
        moved.bounds.x = 1.0;
        *adapter
            .window_after_capture
            .lock()
            .expect("window after capture lock") = Some(moved);
        assert_eq!(
            core.begin(window_request(&catalog, "moved-during-capture")),
            Err(CaptureFailure::DisplaySnapshotChanged)
        );
    }

    #[test]
    fn rejects_transiently_resized_window_frame() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let display = adapter.displays.lock().expect("display lock")[1].clone();
        let window = available_window(
            NativeSourceId(3),
            &display,
            LogicalRect {
                x: 0.0,
                y: 0.0,
                width: 2.0,
                height: 2.0,
            },
        );
        adapter
            .windows
            .lock()
            .expect("window lock")
            .push(window.clone());
        adapter.frames.lock().expect("frames lock").insert(
            window.source,
            Ok(solid_frame(
                PhysicalSize {
                    width: 2,
                    height: 4,
                },
                [0, 255, 0, 255],
            )),
        );
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter)));
        let catalog = core.source_catalog().expect("catalog");

        assert_eq!(
            core.begin(window_request(&catalog, "transient-window-resize")),
            Err(CaptureFailure::DisplaySnapshotChanged)
        );
    }

    #[test]
    fn rejects_catalog_refresh_that_replaces_resolved_window_geometry() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let display = adapter.displays.lock().expect("display lock")[1].clone();
        let window = available_window(
            NativeSourceId(3),
            &display,
            LogicalRect {
                x: 0.0,
                y: 0.0,
                width: 1.0,
                height: 1.0,
            },
        );
        adapter
            .windows
            .lock()
            .expect("window lock")
            .push(window.clone());
        adapter.frames.lock().expect("frames lock").insert(
            window.source,
            Ok(solid_frame(
                PhysicalSize {
                    width: 2,
                    height: 2,
                },
                [0, 255, 0, 255],
            )),
        );
        let backend = Arc::new(WindowsCaptureBackend::new(adapter.clone()));
        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog");
        let resolved = core
            .resolve(
                window_request(&catalog, "catalog-refresh"),
                catalog.snapshot.clone(),
            )
            .expect("resolved request");

        let mut moved = window;
        moved.native_bounds.left += 2;
        moved.native_bounds.right += 2;
        moved.bounds.x += 1.0;
        adapter.windows.lock().expect("window lock")[0] = moved;
        core.source_catalog().expect("refreshed catalog");
        backend
            .start_session(&resolved.session_id)
            .expect("start capture");

        assert_eq!(
            backend.capture(&resolved),
            Err(BackendFailure::DisplayChanged)
        );
        assert!(
            adapter.captures.lock().expect("captures lock").is_empty(),
            "stale resolved geometry must be rejected before native capture"
        );
        backend
            .finish_session(&resolved.session_id)
            .expect("finish capture");
    }

    #[test]
    fn recycled_hwnd_with_new_process_identity_gets_a_new_window_id() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let display = adapter.displays.lock().expect("display lock")[1].clone();
        let window = available_window(NativeSourceId(3), &display, display.logical_bounds);
        adapter
            .windows
            .lock()
            .expect("window lock")
            .push(window.clone());
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter.clone())));
        let catalog = core.source_catalog().expect("catalog");
        let mut replacement = window;
        replacement.process_id += 1;
        replacement.process_started_at += 1;
        adapter.windows.lock().expect("window lock")[0] = replacement;
        assert_eq!(
            core.begin(window_request(&catalog, "recycled-hwnd")),
            Err(CaptureFailure::WindowClosed)
        );
    }

    #[test]
    fn recycled_hwnd_in_same_process_gets_a_new_window_id() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let display = adapter.displays.lock().expect("display lock")[1].clone();
        let window = available_window(NativeSourceId(3), &display, display.logical_bounds);
        adapter
            .windows
            .lock()
            .expect("window lock")
            .push(window.clone());
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter.clone())));
        let catalog = core.source_catalog().expect("catalog");
        let stale_request = window_request(&catalog, "recycled-same-process-hwnd");
        let stale_id = catalog.windows[0].id.clone();

        let mut replacement = window;
        replacement.source = NativeSourceId(4);
        adapter.windows.lock().expect("window lock")[0] = replacement;
        let replacement_catalog = core.source_catalog().expect("replacement catalog");

        assert_ne!(replacement_catalog.windows[0].id, stale_id);
        assert_eq!(core.begin(stale_request), Err(CaptureFailure::WindowClosed));
    }

    #[test]
    fn retired_hwnd_identity_does_not_reuse_a_stale_window_id() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let display = adapter.displays.lock().expect("display lock")[1].clone();
        let window = available_window(NativeSourceId(3), &display, display.logical_bounds);
        adapter
            .windows
            .lock()
            .expect("window lock")
            .push(window.clone());
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter.clone())));
        let catalog = core.source_catalog().expect("catalog");
        let stale_request = window_request(&catalog, "reused-retired-hwnd");
        let stale_id = catalog.windows[0].id.clone();

        adapter.windows.lock().expect("window lock").clear();
        assert!(
            core.source_catalog()
                .expect("catalog without retired window")
                .windows
                .is_empty()
        );
        adapter.windows.lock().expect("window lock").push(window);
        let replacement_catalog = core.source_catalog().expect("replacement catalog");

        assert_ne!(replacement_catalog.windows[0].id, stale_id);
        assert_eq!(core.begin(stale_request), Err(CaptureFailure::WindowClosed));
    }

    #[test]
    fn replacement_hmonitor_changes_the_display_snapshot_identity() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter.clone())));
        let catalog = core.source_catalog().expect("catalog");
        adapter.displays.lock().expect("display lock")[0].source = NativeSourceId(99);
        assert_eq!(
            core.begin(multi_monitor_request(&catalog)),
            Err(CaptureFailure::DisplaySnapshotChanged)
        );
    }

    #[test]
    fn replacement_monitor_device_interface_changes_the_display_snapshot_identity() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter.clone())));
        let catalog = core.source_catalog().expect("catalog");
        adapter.displays.lock().expect("display lock")[0].device_interface_key =
            "replacement-monitor-left".to_owned();
        assert_eq!(
            core.begin(multi_monitor_request(&catalog)),
            Err(CaptureFailure::DisplaySnapshotChanged)
        );
    }

    #[test]
    fn retired_hmonitor_identity_does_not_reuse_a_stale_display_id() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let backend = Arc::new(WindowsCaptureBackend::new(adapter.clone()));
        let core = CaptureCore::new(backend);
        let catalog = core.source_catalog().expect("catalog");
        let stale_request = multi_monitor_request(&catalog);
        let displays = adapter.displays.lock().expect("display lock").clone();
        let retired_bounds = displays[0].logical_bounds;
        let stale_id = catalog
            .snapshot
            .displays
            .iter()
            .find(|display| display.logical_bounds == retired_bounds)
            .expect("retired display")
            .id
            .clone();

        *adapter.displays.lock().expect("display lock") = vec![displays[1].clone()];
        core.source_catalog()
            .expect("catalog without retired display");
        *adapter.displays.lock().expect("display lock") = displays;
        let replacement_catalog = core.source_catalog().expect("replacement catalog");
        let replacement_id = replacement_catalog
            .snapshot
            .displays
            .iter()
            .find(|display| display.logical_bounds == retired_bounds)
            .expect("replacement display")
            .id
            .clone();

        assert_ne!(replacement_id, stale_id);
        assert_eq!(
            core.begin(stale_request),
            Err(CaptureFailure::DisplaySnapshotChanged)
        );
    }

    #[test]
    fn rejects_display_topology_changed_while_waiting_for_a_frame() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter.clone())));
        let catalog = core.source_catalog().expect("catalog");
        let mut changed = adapter.displays.lock().expect("display lock").clone();
        changed[1].native_bounds.left += 1;
        changed[1].native_bounds.right += 1;
        changed[1].logical_bounds.x += 1.0;
        *adapter
            .displays_after_capture
            .lock()
            .expect("displays after capture lock") = Some(changed);

        assert_eq!(
            core.begin(multi_monitor_request(&catalog)),
            Err(CaptureFailure::DisplaySnapshotChanged)
        );
    }

    #[test]
    fn forwards_each_explicit_pointer_override() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter.clone())));
        let catalog = core.source_catalog().expect("catalog");
        let display_id = catalog
            .snapshot
            .displays
            .iter()
            .find(|display| display.primary)
            .expect("primary")
            .id
            .clone();
        for (index, pointer) in [PointerInclusion::Exclude, PointerInclusion::Include]
            .into_iter()
            .enumerate()
        {
            let request = CaptureRequest {
                session_id: CaptureSessionId(format!("pointer-{index}")),
                snapshot_id: catalog.snapshot.snapshot_id.clone(),
                source: CaptureSourceSelection::Display {
                    display_id: display_id.clone(),
                },
                pointer,
                output_media_type: ImageMediaType::Png,
            };
            core.begin(request).expect("pointer capture");
        }
        let pointers = adapter
            .captures
            .lock()
            .expect("captures lock")
            .iter()
            .map(|(_, pointer)| *pointer)
            .collect::<Vec<_>>();
        assert_eq!(
            pointers,
            vec![PointerInclusion::Exclude, PointerInclusion::Include]
        );
    }

    #[test]
    fn returns_distinct_protected_minimized_closed_and_removed_failures() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let display = adapter.displays.lock().expect("display lock")[1].clone();
        adapter
            .windows
            .lock()
            .expect("windows lock")
            .push(WindowsWindow {
                source: NativeSourceId(3),
                stable_key: "private-window".to_owned(),
                process_id: 30,
                process_started_at: 300,
                display_key: display.stable_key.clone(),
                display_device_interface_key: display.device_interface_key.clone(),
                display_source: display.source,
                native_bounds: display.native_bounds,
                bounds: display.logical_bounds,
                state: WindowsWindowState::Protected,
                process_name: Some("safe.exe".to_owned()),
                title: Some("Protected".to_owned()),
            });
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter.clone())));
        let catalog = core.source_catalog().expect("catalog");
        let window_id = catalog.windows[0].id.clone();
        let request_for_window = |session: &str| CaptureRequest {
            session_id: CaptureSessionId(session.to_owned()),
            snapshot_id: catalog.snapshot.snapshot_id.clone(),
            source: CaptureSourceSelection::Window {
                window_id: window_id.clone(),
            },
            pointer: PointerInclusion::Exclude,
            output_media_type: ImageMediaType::Png,
        };
        assert_eq!(
            core.begin(request_for_window("protected")),
            Err(CaptureFailure::ProtectedContent)
        );

        adapter.windows.lock().expect("windows lock")[0].state = WindowsWindowState::Minimized;
        assert_eq!(
            core.begin(request_for_window("minimized")),
            Err(CaptureFailure::WindowMinimized)
        );
        adapter.windows.lock().expect("windows lock").clear();
        assert_eq!(
            core.begin(request_for_window("closed")),
            Err(CaptureFailure::WindowClosed)
        );

        let fresh_catalog = core.source_catalog().expect("fresh catalog");
        *adapter
            .remove_display_on_call
            .lock()
            .expect("remove call lock") = Some(adapter.display_calls.load(Ordering::Relaxed) + 2);
        assert_eq!(
            core.begin(multi_monitor_request(&fresh_catalog)),
            Err(CaptureFailure::DisplayRemoved)
        );
    }

    #[test]
    fn metadata_is_bounded_and_diagnostics_never_receive_it() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let display = adapter.displays.lock().expect("display lock")[1].clone();
        adapter
            .windows
            .lock()
            .expect("windows lock")
            .push(WindowsWindow {
                source: NativeSourceId(3),
                stable_key: "private-window-handle".to_owned(),
                process_id: 30,
                process_started_at: 300,
                display_key: display.stable_key,
                display_device_interface_key: display.device_interface_key,
                display_source: display.source,
                native_bounds: display.native_bounds,
                bounds: display.logical_bounds,
                state: WindowsWindowState::Available,
                process_name: Some("safe\u{0}process.exe".to_owned()),
                title: Some(format!("  secret-path\n{}", "x".repeat(600))),
            });
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter)));
        let catalog = core.source_catalog().expect("catalog");
        let metadata = &catalog.windows[0].metadata;
        assert_eq!(metadata.process_name.as_deref(), Some("safeprocess.exe"));
        assert_eq!(
            metadata.title.as_ref().expect("title").len(),
            MAX_SAFE_WINDOW_TITLE_BYTES
        );
        let diagnostic = serde_json::to_string(&super::super::CaptureDiagnostic::failed(
            CaptureFailure::ProtectedContent,
        ))
        .expect("diagnostic");
        for private_value in ["private-window-handle", "secret-path", "safeprocess.exe"] {
            assert!(!diagnostic.contains(private_value));
        }
        assert_eq!(
            serde_json::to_value(WindowMetadata::default()).expect("serialize empty metadata"),
            serde_json::json!({})
        );
    }

    #[test]
    fn metadata_truncation_drops_trailing_whitespace() {
        assert_eq!(
            sanitize_metadata(Some("abcd ef"), 5).as_deref(),
            Some("abcd")
        );
    }

    #[test]
    fn path_like_metadata_is_omitted_without_invalidating_the_catalog() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let display = adapter.displays.lock().expect("display lock")[1].clone();
        let mut window = available_window(NativeSourceId(3), &display, display.logical_bounds);
        window.process_name = Some(r"C:\private\capture.exe".to_owned());
        window.title = Some("browser/tab".to_owned());
        adapter.windows.lock().expect("windows lock").push(window);

        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter)));
        let catalog = core.source_catalog().expect("catalog");
        assert_eq!(catalog.windows[0].metadata, WindowMetadata::default());
    }

    #[test]
    fn repeated_capture_failure_and_cancel_cleanup_are_idempotent() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let backend = Arc::new(WindowsCaptureBackend::new(adapter.clone()));
        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog");
        let mut request = multi_monitor_request(&catalog);
        for attempt in 0..3 {
            request.session_id = CaptureSessionId(format!("repeat-{attempt}"));
            core.begin(request.clone()).expect("repeat capture");
            core.cancel(&request.session_id).expect("late cancel");
        }
        *adapter
            .frames
            .lock()
            .expect("frames lock")
            .get_mut(&NativeSourceId(1))
            .expect("left frame") = Err(WindowsAdapterFailure::Failed);
        request.session_id = CaptureSessionId("failed".to_owned());
        assert_eq!(
            core.begin(request.clone()),
            Err(CaptureFailure::CaptureFailed)
        );
        core.cancel(&request.session_id)
            .expect("failed cleanup cancel");
        assert!(
            backend
                .active_captures
                .lock()
                .expect("active lock")
                .is_empty()
        );
    }

    #[test]
    fn cancellation_during_request_resolution_prevents_native_capture() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let backend = Arc::new(WindowsCaptureBackend::new(adapter.clone()));
        let core = Arc::new(CaptureCore::new(backend.clone()));
        let catalog = core.source_catalog().expect("catalog");
        let request = multi_monitor_request(&catalog);
        let session_id = request.session_id.clone();
        let permission_barrier = Arc::new(Barrier::new(2));
        *adapter
            .permission_barrier
            .lock()
            .expect("permission barrier lock") = Some(permission_barrier.clone());

        let capture_core = core.clone();
        let capture = thread::spawn(move || capture_core.begin(request));
        permission_barrier.wait();
        adapter
            .permission_barrier
            .lock()
            .expect("permission barrier lock")
            .take();
        core.cancel(&session_id).expect("cancel during permission");
        permission_barrier.wait();

        assert_eq!(
            capture.join().expect("capture thread"),
            Err(CaptureFailure::Cancelled)
        );
        assert!(
            adapter.captures.lock().expect("captures lock").is_empty(),
            "native capture must not start after cancellation"
        );
        assert!(
            backend
                .active_captures
                .lock()
                .expect("active lock")
                .is_empty()
        );
    }

    #[test]
    fn cancellation_after_preparation_prevents_worker_capture() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let backend = Arc::new(WindowsCaptureBackend::new(adapter.clone()));
        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog");
        let request = multi_monitor_request(&catalog);

        core.prepare_begin(&request).expect("prepare capture");
        core.cancel(&request.session_id)
            .expect("cancel prepared capture");

        assert_eq!(core.begin_prepared(request), Err(CaptureFailure::Cancelled));
        assert!(
            adapter.captures.lock().expect("captures lock").is_empty(),
            "native capture must not start after a prepared request is cancelled"
        );
        assert!(
            backend
                .active_captures
                .lock()
                .expect("active lock")
                .is_empty()
        );
    }

    #[test]
    fn movable_resizable_region_remains_bound_to_snapshot() {
        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        let core = CaptureCore::new(Arc::new(WindowsCaptureBackend::new(adapter)));
        let catalog = core.source_catalog().expect("catalog");
        let selection = SelectionGeometry {
            snapshot_id: catalog.snapshot.snapshot_id.clone(),
            bounds: LogicalRect {
                x: -2.0,
                y: 0.0,
                width: 1.0,
                height: 1.0,
            },
        };
        let moved = core
            .adjust_selection(
                &selection,
                super::super::SelectionAdjustment::Move {
                    delta_x: 2.0,
                    delta_y: 1.0,
                },
            )
            .expect("move");
        assert_eq!(
            moved.bounds,
            LogicalRect {
                x: 0.0,
                y: 1.0,
                width: 1.0,
                height: 1.0,
            }
        );
    }

    #[test]
    fn adapter_failure_variants_remain_closed_and_deterministic() {
        for (adapter, backend) in [
            (
                WindowsAdapterFailure::Unavailable,
                BackendFailure::Unavailable,
            ),
            (
                WindowsAdapterFailure::PermissionDenied,
                BackendFailure::PermissionDenied,
            ),
            (
                WindowsAdapterFailure::PermissionLost,
                BackendFailure::PermissionLost,
            ),
            (WindowsAdapterFailure::Cancelled, BackendFailure::Cancelled),
            (
                WindowsAdapterFailure::ProtectedContent,
                BackendFailure::ProtectedContent,
            ),
            (
                WindowsAdapterFailure::WindowMinimized,
                BackendFailure::WindowMinimized,
            ),
            (
                WindowsAdapterFailure::WindowClosed,
                BackendFailure::WindowClosed,
            ),
            (
                WindowsAdapterFailure::DisplayRemoved,
                BackendFailure::DisplayRemoved,
            ),
            (WindowsAdapterFailure::Failed, BackendFailure::CaptureFailed),
        ] {
            assert_eq!(BackendFailure::from(adapter), backend);
        }
    }

    #[test]
    fn rejects_window_source_and_geometry_changes_before_capture() {
        let registered = WindowsWindow {
            source: NativeSourceId(3),
            stable_key: "private-window".to_owned(),
            process_id: 30,
            process_started_at: 300,
            display_key: "private-device-right".to_owned(),
            display_device_interface_key: "private-monitor-right".to_owned(),
            display_source: NativeSourceId(2),
            native_bounds: NativeRect {
                left: 0,
                top: 0,
                right: 4,
                bottom: 4,
            },
            bounds: LogicalRect {
                x: 0.0,
                y: 0.0,
                width: 2.0,
                height: 2.0,
            },
            state: WindowsWindowState::Available,
            process_name: None,
            title: None,
        };

        let mut current = registered.clone();
        current.source = NativeSourceId(4);
        assert_eq!(
            validate_window_state(&registered, &current),
            Err(BackendFailure::WindowClosed)
        );

        current = registered.clone();
        current.display_key = "private-device-left".to_owned();
        assert_eq!(
            validate_window_state(&registered, &current),
            Err(BackendFailure::DisplayChanged)
        );

        current = registered.clone();
        current.bounds.x = 1.0;
        assert_eq!(
            validate_window_state(&registered, &current),
            Err(BackendFailure::DisplayChanged)
        );
    }

    #[test]
    fn supports_windows_11_and_later_only() {
        assert!(!is_supported_windows_version(10, 21_999));
        assert!(is_supported_windows_version(10, 22_000));
        assert!(is_supported_windows_version(11, 0));
    }

    #[test]
    fn unsupported_graphics_capture_is_unavailable() {
        assert_eq!(
            permission_from_support(false),
            Err(WindowsAdapterFailure::Unavailable)
        );
        assert_eq!(
            permission_from_support(true),
            Ok(CapturePermission::Granted)
        );

        let adapter = Arc::new(FixtureAdapter::mixed_dpi());
        *adapter.permission.lock().expect("permission lock") =
            Err(WindowsAdapterFailure::Unavailable);
        let backend = WindowsCaptureBackend::new(adapter);
        assert!(matches!(
            backend.capabilities(),
            Err(BackendFailure::Unavailable)
        ));
    }

    #[test]
    fn skips_only_vanished_windows_after_retention_failure() {
        assert!(should_skip_failed_window_retention(false));
        assert!(!should_skip_failed_window_retention(true));
    }
}
