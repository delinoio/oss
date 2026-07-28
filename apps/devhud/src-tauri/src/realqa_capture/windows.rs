use std::{
    collections::{HashMap, HashSet},
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
};

use super::{
    BackendFailure, BackendFrame, CaptureBackend, CapturePermission, CapturePlatform,
    CaptureSessionId, CaptureSourceSelection, DisplayDescriptor, DisplayId, DisplaySnapshot,
    LogicalRect, MAX_SAFE_PROCESS_NAME_BYTES, MAX_SAFE_WINDOW_TITLE_BYTES, PointerInclusion,
    ResolvedCaptureRequest, WindowAvailability, WindowMetadata, WindowSource, WindowSourceId,
    geometry::{PhysicalSize, PixelRect, ScaleFactor},
    image_boundary::{MAX_DECODED_PIXELS, decoded_byte_len},
};

const MAX_INTERNAL_SOURCE_KEY_BYTES: usize = 1_024;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
struct NativeSourceId(usize);

#[derive(Debug, Clone)]
struct WindowsDisplay {
    source: NativeSourceId,
    stable_key: String,
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

#[derive(Debug, Clone)]
struct WindowsWindow {
    source: NativeSourceId,
    stable_key: String,
    display_key: String,
    bounds: LogicalRect,
    state: WindowsWindowState,
    process_name: Option<String>,
    title: Option<String>,
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
        source: NativeSourceId,
        pointer: PointerInclusion,
        cancel: &AtomicBool,
    ) -> Result<NativeFrame, WindowsAdapterFailure>;
}

#[derive(Default)]
struct SourceRegistry {
    next_display_id: u64,
    next_window_id: u64,
    display_ids: HashMap<String, DisplayId>,
    window_ids: HashMap<String, WindowSourceId>,
    displays: HashMap<DisplayId, WindowsDisplay>,
    windows: HashMap<WindowSourceId, WindowsWindow>,
}

impl SourceRegistry {
    fn public_display_id(&mut self, stable_key: &str) -> DisplayId {
        if let Some(id) = self.display_ids.get(stable_key) {
            return id.clone();
        }
        self.next_display_id += 1;
        let id = DisplayId(format!("windows-display-{:016x}", self.next_display_id));
        self.display_ids.insert(stable_key.to_owned(), id.clone());
        id
    }

    fn public_window_id(&mut self, stable_key: &str) -> WindowSourceId {
        if let Some(id) = self.window_ids.get(stable_key) {
            return id.clone();
        }
        self.next_window_id += 1;
        let id = WindowSourceId(format!("windows-window-{:016x}", self.next_window_id));
        self.window_ids.insert(stable_key.to_owned(), id.clone());
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

    #[cfg(target_os = "windows")]
    pub(super) fn system() -> Self {
        Self::new(Arc::new(system::SystemWindowsAdapter))
    }

    fn checked_displays(
        &self,
        displays: Vec<WindowsDisplay>,
    ) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
        let mut stable_keys = HashSet::with_capacity(displays.len());
        if displays.is_empty()
            || displays.iter().any(|display| {
                display.stable_key.is_empty()
                    || display.stable_key.len() > MAX_INTERNAL_SOURCE_KEY_BYTES
                    || !stable_keys.insert(display.stable_key.clone())
            })
        {
            return Err(BackendFailure::DisplayChanged);
        }

        let mut sources = self
            .sources
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?;
        let mut current = HashMap::with_capacity(displays.len());
        let descriptors = displays
            .into_iter()
            .map(|display| {
                let id = sources.public_display_id(&display.stable_key);
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
        sources.displays = current;
        Ok(descriptors)
    }

    fn current_windows(&self) -> Result<Vec<WindowsWindow>, BackendFailure> {
        let windows = self.adapter.windows().map_err(BackendFailure::from)?;
        let mut stable_keys = HashSet::with_capacity(windows.len());
        if windows.iter().any(|window| {
            window.stable_key.is_empty()
                || window.stable_key.len() > MAX_INTERNAL_SOURCE_KEY_BYTES
                || !stable_keys.insert(window.stable_key.clone())
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

    fn finish_capture(&self, session_id: &CaptureSessionId) {
        if let Ok(mut active) = self.active_captures.lock() {
            active.remove(session_id);
        }
    }

    fn revalidate_snapshot(&self, request: &ResolvedCaptureRequest) -> Result<(), BackendFailure> {
        let current = DisplaySnapshot::checked(self.displays()?)
            .map_err(|_| BackendFailure::DisplayChanged)?;
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
        Ok(())
    }

    fn capture_inner(
        &self,
        request: &ResolvedCaptureRequest,
        cancel: &AtomicBool,
    ) -> Result<BackendFrame, BackendFailure> {
        self.revalidate_snapshot(request)?;
        if cancel.load(Ordering::Acquire) {
            return Err(BackendFailure::Cancelled);
        }

        if let CaptureSourceSelection::Window { window_id } = &request.source {
            return self.capture_window(request, window_id, cancel);
        }
        self.capture_display_regions(request, cancel)
    }

    fn capture_window(
        &self,
        request: &ResolvedCaptureRequest,
        window_id: &WindowSourceId,
        cancel: &AtomicBool,
    ) -> Result<BackendFrame, BackendFailure> {
        let registered = self
            .sources
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?
            .windows
            .get(window_id)
            .cloned()
            .ok_or(BackendFailure::WindowClosed)?;
        let window = self
            .current_windows()?
            .into_iter()
            .find(|candidate| candidate.stable_key == registered.stable_key)
            .ok_or(BackendFailure::WindowClosed)?;
        match window.state {
            WindowsWindowState::Available => {}
            WindowsWindowState::Minimized => return Err(BackendFailure::WindowMinimized),
            WindowsWindowState::Protected => return Err(BackendFailure::ProtectedContent),
        }
        let frame = self
            .adapter
            .capture(window.source, request.pointer, cancel)
            .map_err(BackendFailure::from)?;
        checked_native_frame(&frame)?;
        resize_frame(
            &frame,
            request.expected_frame_size.width,
            request.expected_frame_size.height,
        )
    }

    fn capture_display_regions(
        &self,
        request: &ResolvedCaptureRequest,
        cancel: &AtomicBool,
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
                    .capture(display.source, request.pointer, cancel)
                    .map_err(BackendFailure::from)?;
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

impl CaptureBackend for WindowsCaptureBackend {
    fn platform(&self) -> CapturePlatform {
        CapturePlatform::Windows
    }

    fn permission(&self) -> Result<CapturePermission, BackendFailure> {
        self.adapter.permission().map_err(BackendFailure::from)
    }

    fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
        let displays = self.adapter.displays().map_err(BackendFailure::from)?;
        self.checked_displays(displays)
    }

    fn windows(&self, snapshot: &DisplaySnapshot) -> Result<Vec<WindowSource>, BackendFailure> {
        let windows = self.current_windows()?;
        let mut sources = self
            .sources
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?;
        let display_ids = sources
            .display_ids
            .iter()
            .map(|(stable, public)| (stable.clone(), public.clone()))
            .collect::<HashMap<_, _>>();
        let mut current = HashMap::with_capacity(windows.len());
        let mut output = Vec::with_capacity(windows.len());
        for window in windows {
            let display_id = display_ids
                .get(&window.display_key)
                .filter(|id| snapshot.display(id).is_some())
                .cloned()
                .ok_or(BackendFailure::DisplayChanged)?;
            let id = sources.public_window_id(&window.stable_key);
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
        let cancel = self.register_capture(&request.session_id)?;
        let result = self.capture_inner(request, &cancel);
        self.finish_capture(&request.session_id);
        result
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
    if trimmed.is_empty() {
        return None;
    }
    let mut end = trimmed.len().min(maximum_bytes);
    while !trimmed.is_char_boundary(end) {
        end -= 1;
    }
    Some(trimmed[..end].to_owned())
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
    if request.pixel_regions.len() == 1 {
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

fn resize_frame(
    frame: &NativeFrame,
    width: u32,
    height: u32,
) -> Result<BackendFrame, BackendFailure> {
    let mut output = BackendFrame {
        width,
        height,
        rgba: vec![0; decoded_byte_len(width, height).map_err(|_| BackendFailure::CaptureFailed)?],
    };
    blit_scaled(
        frame,
        PixelRect {
            x: 0,
            y: 0,
            width: frame.width,
            height: frame.height,
        },
        &mut output,
        PixelRect {
            x: 0,
            y: 0,
            width,
            height,
        },
    )?;
    Ok(output)
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
        Graphics::Capture::GraphicsCaptureSession,
        Win32::{
            Foundation::{HWND, RECT},
            Graphics::Gdi::{GetMonitorInfoW, HMONITOR, MONITORINFO, MONITORINFOF_PRIMARY},
            UI::{
                HiDpi::{GetDpiForMonitor, MDT_EFFECTIVE_DPI},
                WindowsAndMessaging::{GetWindowDisplayAffinity, IsIconic},
            },
        },
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

    pub(super) struct SystemWindowsAdapter;

    impl WindowsPlatformAdapter for SystemWindowsAdapter {
        fn permission(&self) -> Result<CapturePermission, WindowsAdapterFailure> {
            GraphicsCaptureSession::IsSupported()
                .map(|supported| {
                    if supported {
                        CapturePermission::Granted
                    } else {
                        CapturePermission::Denied
                    }
                })
                .map_err(|_| WindowsAdapterFailure::Unavailable)
        }

        fn displays(&self) -> Result<Vec<WindowsDisplay>, WindowsAdapterFailure> {
            Monitor::enumerate()
                .map_err(|_| WindowsAdapterFailure::Failed)?
                .into_iter()
                .map(display_record)
                .collect()
        }

        fn windows(&self) -> Result<Vec<WindowsWindow>, WindowsAdapterFailure> {
            let displays = self.displays()?;
            Window::enumerate()
                .map_err(|_| WindowsAdapterFailure::Failed)?
                .into_iter()
                .filter_map(|window| window_record(window, &displays).transpose())
                .collect()
        }

        fn capture(
            &self,
            source: NativeSourceId,
            pointer: PointerInclusion,
            cancel: &AtomicBool,
        ) -> Result<NativeFrame, WindowsAdapterFailure> {
            let source = SystemCaptureSource::from_token(source)?;
            if let SystemCaptureSource::Window(window) = source {
                if unsafe { IsIconic(HWND(window.as_raw_hwnd())).as_bool() } {
                    return Err(WindowsAdapterFailure::WindowMinimized);
                }
                if window_is_protected(window) {
                    return Err(WindowsAdapterFailure::ProtectedContent);
                }
            }
            capture_one(source, pointer, cancel)
        }
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
        let width = u32::try_from(info.rcMonitor.right - info.rcMonitor.left)
            .map_err(|_| WindowsAdapterFailure::Failed)?;
        let height = u32::try_from(info.rcMonitor.bottom - info.rcMonitor.top)
            .map_err(|_| WindowsAdapterFailure::Failed)?;
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
        Ok(WindowsDisplay {
            source: NativeSourceId(raw as usize),
            stable_key: monitor
                .device_name()
                .map_err(|_| WindowsAdapterFailure::Failed)?,
            logical_bounds: LogicalRect {
                x: f64::from(info.rcMonitor.left) / scale_value,
                y: f64::from(info.rcMonitor.top) / scale_value,
                width: f64::from(width) / scale_value,
                height: f64::from(height) / scale_value,
            },
            physical_size: PhysicalSize { width, height },
            scale,
            primary: info.dwFlags & MONITORINFOF_PRIMARY != 0,
        })
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
        let Some(display) = displays
            .iter()
            .find(|display| display.stable_key == monitor_key)
        else {
            return Ok(None);
        };
        let rect = window
            .rect()
            .map_err(|_| WindowsAdapterFailure::WindowClosed)?;
        let width = rect.right - rect.left;
        let height = rect.bottom - rect.top;
        let minimized = unsafe { IsIconic(HWND(window.as_raw_hwnd())).as_bool() };
        if !minimized && (width <= 0 || height <= 0) {
            return Ok(None);
        }
        let scale = f64::from(display.scale.numerator) / f64::from(display.scale.denominator);
        let bounds = if minimized {
            LogicalRect {
                x: 0.0,
                y: 0.0,
                width: 0.0,
                height: 0.0,
            }
        } else {
            let monitor_rect = physical_monitor_rect(monitor)?;
            LogicalRect {
                x: display.logical_bounds.x + f64::from(rect.left - monitor_rect.left) / scale,
                y: display.logical_bounds.y + f64::from(rect.top - monitor_rect.top) / scale,
                width: f64::from(width) / scale,
                height: f64::from(height) / scale,
            }
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
            display_key: monitor_key,
            bounds,
            state,
            process_name: window.process_name().ok(),
            title: window.title().ok(),
        }))
    }

    fn physical_monitor_rect(monitor: Monitor) -> Result<RECT, WindowsAdapterFailure> {
        let mut info = MONITORINFO {
            cbSize: u32::try_from(mem::size_of::<MONITORINFO>())
                .map_err(|_| WindowsAdapterFailure::Failed)?,
            ..MONITORINFO::default()
        };
        if !unsafe { GetMonitorInfoW(HMONITOR(monitor.as_raw_hmonitor()), &mut info).as_bool() } {
            return Err(WindowsAdapterFailure::Failed);
        }
        Ok(info.rcMonitor)
    }

    fn window_is_protected(window: Window) -> bool {
        let mut affinity = 0_u32;
        unsafe {
            GetWindowDisplayAffinity(HWND(window.as_raw_hwnd()), &mut affinity).as_bool()
                && affinity != 0
        }
    }

    #[derive(Clone, Copy)]
    enum SystemCaptureSource {
        Monitor(Monitor),
        Window(Window),
    }

    impl SystemCaptureSource {
        fn from_token(source: NativeSourceId) -> Result<Self, WindowsAdapterFailure> {
            let raw = source.0 as *mut std::ffi::c_void;
            let window = Window::from_raw_hwnd(raw);
            if window.is_valid() {
                return Ok(Self::Window(window));
            }
            let monitor = Monitor::from_raw_hmonitor(raw);
            if Monitor::enumerate()
                .map_err(|_| WindowsAdapterFailure::Failed)?
                .contains(&monitor)
            {
                return Ok(Self::Monitor(monitor));
            }
            Err(WindowsAdapterFailure::DisplayRemoved)
        }
    }

    impl TryInto<GraphicsCaptureItemType> for SystemCaptureSource {
        type Error = windows::core::Error;

        fn try_into(self) -> Result<GraphicsCaptureItemType, Self::Error> {
            match self {
                Self::Monitor(monitor) => monitor.try_into(),
                Self::Window(window) => window.try_into(),
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
                let mut buffer = frame.buffer().map_err(|_| WindowsAdapterFailure::Failed)?;
                let mut contiguous = Vec::new();
                let rgba = buffer.as_nopadding_buffer(&mut contiguous).to_vec();
                Ok(NativeFrame {
                    width,
                    height,
                    rgba,
                })
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
        cancel: &AtomicBool,
    ) -> Result<NativeFrame, WindowsAdapterFailure> {
        let source_lost = match source {
            SystemCaptureSource::Monitor(_) => WindowsAdapterFailure::DisplayRemoved,
            SystemCaptureSource::Window(_) => WindowsAdapterFailure::WindowClosed,
        };
        let cancelled = Arc::new(AtomicBool::new(cancel.load(Ordering::Acquire)));
        let shared = Arc::new(CaptureShared {
            result: Mutex::new(None),
            cancelled: cancelled.clone(),
            source_lost,
        });
        let settings = Settings::new(
            source,
            match pointer {
                PointerInclusion::Include => CursorCaptureSettings::WithCursor,
                PointerInclusion::Exclude => CursorCaptureSettings::WithoutCursor,
            },
            DrawBorderSettings::WithoutBorder,
            SecondaryWindowSettings::Exclude,
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
                cancelled.store(true, Ordering::Release);
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
        let result = shared
            .result
            .lock()
            .map_err(|_| WindowsAdapterFailure::Failed)?
            .take()
            .ok_or(WindowsAdapterFailure::Failed)?;
        result
    }
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::AtomicUsize;

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
        captures: Mutex<Vec<(NativeSourceId, PointerInclusion)>>,
        display_calls: AtomicUsize,
        remove_display_on_call: Mutex<Option<usize>>,
    }

    impl FixtureAdapter {
        fn mixed_dpi() -> Self {
            let left = WindowsDisplay {
                source: NativeSourceId(1),
                stable_key: "private-device-left".to_owned(),
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
            }
        }
    }

    impl WindowsPlatformAdapter for FixtureAdapter {
        fn permission(&self) -> Result<CapturePermission, WindowsAdapterFailure> {
            *self.permission.lock().expect("permission lock")
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
            source: NativeSourceId,
            pointer: PointerInclusion,
            cancel: &AtomicBool,
        ) -> Result<NativeFrame, WindowsAdapterFailure> {
            if cancel.load(Ordering::Acquire) {
                return Err(WindowsAdapterFailure::Cancelled);
            }
            self.captures
                .lock()
                .expect("captures lock")
                .push((source, pointer));
            self.frames
                .lock()
                .expect("frames lock")
                .get(&source)
                .cloned()
                .unwrap_or(Err(WindowsAdapterFailure::Failed))
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
                (NativeSourceId(1), PointerInclusion::Exclude),
                (NativeSourceId(2), PointerInclusion::Exclude),
            ]
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
                display_key: display.stable_key.clone(),
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
                display_key: display.stable_key,
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
}
