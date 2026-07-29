mod composer;
mod geometry;
mod image_boundary;
#[cfg(target_os = "linux")]
mod linux;
#[cfg(any(target_os = "windows", test))]
mod windows;

use std::sync::Arc;

#[cfg(feature = "desktop-cef")]
pub(crate) use composer::{
    ComposerCore, ComposerImage, ComposerImageId, ComposerImageRequest, ComposerSessionId,
};
pub(crate) use geometry::{
    DisplayDescriptor, DisplayId, DisplayPixelRegion, DisplaySnapshot, DisplaySnapshotId,
    LogicalRect, SelectionAdjustment, SelectionGeometry, adjust_selection,
};
pub(crate) use image_boundary::{
    DecodedImage, EncodedImage, ImageBoundaryFailure, ImageMediaType, ImageSessionBudget,
    decode_image, decoded_byte_len, encode_image, sanitize_image,
};
use serde::{Deserialize, Serialize};

const MAX_CAPTURE_DISPLAYS: usize = 64;
const MAX_CAPTURE_WINDOWS: usize = 512;
const MAX_SAFE_PROCESS_NAME_BYTES: usize = 128;
const MAX_SAFE_WINDOW_TITLE_BYTES: usize = 512;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum CapturePlatform {
    Macos,
    Windows,
    Linux,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum CaptureMode {
    Region,
    Window,
    Display,
    MultiMonitor,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum CaptureDisplayProtocol {
    Native,
    X11,
    Xwayland,
    WaylandPortal,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum SelectionAdjustmentAuthority {
    Application,
    Portal,
    Unavailable,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum PointerInclusion {
    Include,
    Exclude,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum CapturePermission {
    Granted,
    PromptRequired,
    Denied,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[allow(dead_code)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum CaptureFailure {
    UnsupportedPlatform,
    BackendUnavailable,
    PermissionRequired,
    PermissionDenied,
    PermissionLost,
    Cancelled,
    PortalCancelled,
    ProtectedContent,
    WindowMinimized,
    WindowClosed,
    WindowLost,
    DisplayRemoved,
    ModeUnavailable,
    DisplaySnapshotChanged,
    InvalidDisplaySnapshot,
    InvalidSelection,
    MalformedImage,
    UnsupportedImage,
    DecompressionBomb,
    ImageEncodedLimitExceeded,
    SessionEncodedLimitExceeded,
    EncodingFailed,
    CaptureFailed,
}

impl From<ImageBoundaryFailure> for CaptureFailure {
    fn from(value: ImageBoundaryFailure) -> Self {
        match value {
            ImageBoundaryFailure::MalformedImage => Self::MalformedImage,
            ImageBoundaryFailure::UnsupportedImage => Self::UnsupportedImage,
            ImageBoundaryFailure::DecompressionBomb => Self::DecompressionBomb,
            ImageBoundaryFailure::ImageEncodedLimitExceeded => Self::ImageEncodedLimitExceeded,
            ImageBoundaryFailure::SessionEncodedLimitExceeded => Self::SessionEncodedLimitExceeded,
            ImageBoundaryFailure::EncodingFailed => Self::EncodingFailed,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[allow(dead_code)]
pub(crate) enum BackendFailure {
    Unavailable,
    PermissionDenied,
    PermissionLost,
    Cancelled,
    PortalCancelled,
    ProtectedContent,
    WindowMinimized,
    WindowClosed,
    WindowLost,
    DisplayRemoved,
    ModeUnavailable,
    DisplayChanged,
    CaptureFailed,
}

impl From<BackendFailure> for CaptureFailure {
    fn from(value: BackendFailure) -> Self {
        match value {
            BackendFailure::Unavailable => Self::BackendUnavailable,
            BackendFailure::PermissionDenied => Self::PermissionDenied,
            BackendFailure::PermissionLost => Self::PermissionLost,
            BackendFailure::Cancelled => Self::Cancelled,
            BackendFailure::PortalCancelled => Self::PortalCancelled,
            BackendFailure::ProtectedContent => Self::ProtectedContent,
            BackendFailure::WindowMinimized => Self::WindowMinimized,
            BackendFailure::WindowClosed => Self::WindowClosed,
            BackendFailure::WindowLost => Self::WindowLost,
            BackendFailure::DisplayRemoved => Self::DisplayRemoved,
            BackendFailure::ModeUnavailable => Self::ModeUnavailable,
            BackendFailure::DisplayChanged => Self::DisplaySnapshotChanged,
            BackendFailure::CaptureFailed => Self::CaptureFailed,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Hash, Deserialize, Serialize)]
#[serde(transparent)]
pub(crate) struct WindowSourceId(pub(crate) String);

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum WindowAvailability {
    Available,
    Minimized,
}

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct WindowSource {
    pub(crate) id: WindowSourceId,
    pub(crate) display_id: DisplayId,
    pub(crate) bounds: LogicalRect,
    pub(crate) availability: WindowAvailability,
    pub(crate) metadata: WindowMetadata,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct WindowMetadata {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) process_name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) title: Option<String>,
}

impl WindowMetadata {
    fn checked(self) -> Result<Self, CaptureFailure> {
        if !safe_metadata_field(
            self.process_name.as_deref(),
            MAX_SAFE_PROCESS_NAME_BYTES,
            true,
        ) || !safe_metadata_field(self.title.as_deref(), MAX_SAFE_WINDOW_TITLE_BYTES, false)
        {
            return Err(CaptureFailure::InvalidDisplaySnapshot);
        }
        Ok(self)
    }
}

fn safe_metadata_field(value: Option<&str>, maximum_bytes: usize, process_name: bool) -> bool {
    value.is_none_or(|value| {
        !value.is_empty()
            && value.len() <= maximum_bytes
            && value.trim() == value
            && !value.chars().any(char::is_control)
            && (!process_name || !value.contains(['/', '\\', ':']))
    })
}

impl WindowSource {
    fn checked(mut self, snapshot: &DisplaySnapshot) -> Result<Self, CaptureFailure> {
        if self.id.0.is_empty()
            || self.id.0.len() > 128
            || snapshot.display(&self.display_id).is_none()
        {
            return Err(CaptureFailure::InvalidDisplaySnapshot);
        }
        if self.availability == WindowAvailability::Available {
            self.bounds.checked()?;
        }
        self.metadata = self.metadata.checked()?;
        Ok(self)
    }
}

impl WindowMetadata {
    const MAX_PROCESS_NAME_BYTES: usize = 128;
    const MAX_TITLE_BYTES: usize = 512;

    #[cfg(target_os = "linux")]
    #[cfg_attr(not(feature = "linux-capture-backend"), allow(dead_code))]
    fn bounded(process_name: Option<String>, title: Option<String>) -> Self {
        Self {
            process_name: bounded_metadata_value(process_name, Self::MAX_PROCESS_NAME_BYTES),
            title: bounded_metadata_value(title, Self::MAX_TITLE_BYTES),
        }
    }
}

#[cfg(target_os = "linux")]
#[cfg_attr(not(feature = "linux-capture-backend"), allow(dead_code))]
fn bounded_metadata_value(value: Option<String>, maximum: usize) -> Option<String> {
    let value = value?;
    let value = value.trim();
    if value.is_empty() {
        return None;
    }
    let mut bounded = String::with_capacity(value.len().min(maximum));
    for character in value.chars() {
        if character.is_control() || bounded.len() + character.len_utf8() > maximum {
            break;
        }
        bounded.push(character);
    }
    (!bounded.is_empty()).then_some(bounded)
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct CaptureModeCapability {
    pub(crate) mode: CaptureMode,
    pub(crate) pointer_options: Vec<PointerInclusion>,
    pub(crate) portal_approval_required: bool,
    pub(crate) selection_adjustment: SelectionAdjustmentAuthority,
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct CaptureCapabilities {
    pub(crate) platform: CapturePlatform,
    pub(crate) display_protocol: CaptureDisplayProtocol,
    pub(crate) modes: Vec<CaptureModeCapability>,
}

impl CaptureCapabilities {
    fn checked(mut self) -> Result<Self, CaptureFailure> {
        self.modes.sort_by_key(|capability| capability.mode as u8);
        if self.modes.is_empty()
            || self
                .modes
                .windows(2)
                .any(|pair| pair[0].mode == pair[1].mode)
            || self.modes.iter().any(|capability| {
                capability.pointer_options.is_empty()
                    || capability.pointer_options.len() > 2
                    || (capability.pointer_options.len() == 2
                        && capability.pointer_options[0] == capability.pointer_options[1])
            })
        {
            return Err(CaptureFailure::BackendUnavailable);
        }
        Ok(self)
    }

    fn mode(&self, mode: CaptureMode) -> Option<&CaptureModeCapability> {
        self.modes.iter().find(|capability| capability.mode == mode)
    }
}

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct CaptureSourceCatalog {
    pub(crate) platform: CapturePlatform,
    pub(crate) permission: CapturePermission,
    pub(crate) capabilities: CaptureCapabilities,
    pub(crate) snapshot: DisplaySnapshot,
    pub(crate) windows: Vec<WindowSource>,
}

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(
    tag = "mode",
    rename_all = "kebab-case",
    rename_all_fields = "camelCase"
)]
pub(crate) enum CaptureSourceSelection {
    Region { selection: SelectionGeometry },
    Window { window_id: WindowSourceId },
    Display { display_id: DisplayId },
    MultiMonitor { display_ids: Vec<DisplayId> },
}

impl CaptureSourceSelection {
    const fn mode(&self) -> CaptureMode {
        match self {
            Self::Region { .. } => CaptureMode::Region,
            Self::Window { .. } => CaptureMode::Window,
            Self::Display { .. } => CaptureMode::Display,
            Self::MultiMonitor { .. } => CaptureMode::MultiMonitor,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Hash, Deserialize, Serialize)]
#[serde(transparent)]
pub(crate) struct CaptureSessionId(pub(crate) String);

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct CaptureRequest {
    pub(crate) session_id: CaptureSessionId,
    pub(crate) snapshot_id: DisplaySnapshotId,
    pub(crate) source: CaptureSourceSelection,
    pub(crate) pointer: PointerInclusion,
    pub(crate) output_media_type: ImageMediaType,
}

#[derive(Debug, Clone, PartialEq)]
pub(crate) struct ResolvedWindowSource {
    pub(crate) id: WindowSourceId,
    pub(crate) display_id: DisplayId,
    pub(crate) bounds: LogicalRect,
}

#[derive(Debug, Clone, PartialEq)]
pub(crate) struct ResolvedCaptureRequest {
    pub(crate) session_id: CaptureSessionId,
    pub(crate) snapshot: DisplaySnapshot,
    pub(crate) source: CaptureSourceSelection,
    pub(crate) window: Option<ResolvedWindowSource>,
    pub(crate) mode: CaptureMode,
    pub(crate) pointer: PointerInclusion,
    pub(crate) output_media_type: ImageMediaType,
    pub(crate) logical_bounds: LogicalRect,
    pub(crate) pixel_regions: Vec<DisplayPixelRegion>,
    pub(crate) expected_frame_size: geometry::PhysicalSize,
}

#[derive(Debug, Clone, PartialEq)]
pub(crate) struct BackendFrame {
    pub(crate) width: u32,
    pub(crate) height: u32,
    pub(crate) rgba: Vec<u8>,
    pub(crate) approved_layout: Option<PortalApprovedLayout>,
}

#[derive(Debug, Clone, PartialEq)]
pub(crate) struct PortalApprovedLayout {
    pub(crate) logical_bounds: LogicalRect,
    pub(crate) pixel_regions: Vec<DisplayPixelRegion>,
}

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct CaptureResult {
    pub(crate) mode: CaptureMode,
    pub(crate) pointer: PointerInclusion,
    pub(crate) logical_bounds: LogicalRect,
    pub(crate) pixel_regions: Vec<DisplayPixelRegion>,
    pub(crate) image: EncodedImage,
}

pub(crate) trait CaptureBackend: Send + Sync {
    fn platform(&self) -> CapturePlatform;
    fn start_capture(&self, _session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        Ok(())
    }
    fn finish_capture(&self, _session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        Ok(())
    }
    fn capabilities(&self) -> Result<CaptureCapabilities, BackendFailure>;
    fn permission(&self) -> Result<CapturePermission, BackendFailure>;
    fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure>;
    fn windows(&self, snapshot: &DisplaySnapshot) -> Result<Vec<WindowSource>, BackendFailure>;
    fn capture(&self, request: &ResolvedCaptureRequest) -> Result<BackendFrame, BackendFailure>;
    fn cancel(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure>;
}

#[derive(Clone)]
pub(crate) struct CaptureCore {
    backend: Arc<dyn CaptureBackend>,
}

impl CaptureCore {
    pub(crate) fn new(backend: Arc<dyn CaptureBackend>) -> Self {
        Self { backend }
    }

    pub(crate) fn inspect_capabilities(&self) -> Result<CaptureCapabilities, CaptureFailure> {
        self.backend
            .capabilities()
            .map_err(CaptureFailure::from)?
            .checked()
    }

    pub(crate) fn source_catalog(&self) -> Result<CaptureSourceCatalog, CaptureFailure> {
        let capabilities = self.inspect_capabilities()?;
        let permission = self.require_granted_permission()?;
        let snapshot = self.current_snapshot()?;
        let mut windows = self
            .backend
            .windows(&snapshot)
            .map_err(CaptureFailure::from)?
            .into_iter()
            .map(|window| window.checked(&snapshot))
            .collect::<Result<Vec<_>, _>>()?;
        if windows.len() > MAX_CAPTURE_WINDOWS {
            return Err(CaptureFailure::InvalidDisplaySnapshot);
        }
        windows.sort_by(|left, right| left.id.0.cmp(&right.id.0));
        if windows.windows(2).any(|pair| pair[0].id == pair[1].id) {
            return Err(CaptureFailure::InvalidDisplaySnapshot);
        }
        Ok(CaptureSourceCatalog {
            platform: self.backend.platform(),
            permission,
            capabilities,
            snapshot,
            windows,
        })
    }

    pub(crate) fn adjust_selection(
        &self,
        selection: &SelectionGeometry,
        adjustment: SelectionAdjustment,
    ) -> Result<SelectionGeometry, CaptureFailure> {
        self.require_granted_permission()?;
        let capabilities = self.inspect_capabilities()?;
        if capabilities
            .mode(CaptureMode::Region)
            .is_none_or(|capability| {
                capability.selection_adjustment != SelectionAdjustmentAuthority::Application
            })
        {
            return Err(CaptureFailure::ModeUnavailable);
        }
        let snapshot = self.current_snapshot()?;
        adjust_selection(&snapshot, selection, adjustment)
    }

    pub(crate) fn begin(&self, request: CaptureRequest) -> Result<CaptureResult, CaptureFailure> {
        if request.session_id.0.is_empty() || request.session_id.0.len() > 128 {
            return Err(CaptureFailure::InvalidSelection);
        }
        let session_id = request.session_id.clone();
        self.backend
            .start_capture(&session_id)
            .map_err(CaptureFailure::from)?;
        let result = self.begin_started(request);
        let finish_result = self
            .backend
            .finish_capture(&session_id)
            .map_err(CaptureFailure::from);
        match result {
            Err(failure) => Err(failure),
            Ok(result) => finish_result.map(|()| result),
        }
    }

    fn begin_started(&self, request: CaptureRequest) -> Result<CaptureResult, CaptureFailure> {
        self.require_granted_permission()?;
        let capabilities = self.inspect_capabilities()?;
        let requested_mode = request.source.mode();
        let mode_capability = capabilities
            .mode(requested_mode)
            .ok_or(CaptureFailure::ModeUnavailable)?;
        if !mode_capability.pointer_options.contains(&request.pointer) {
            return Err(CaptureFailure::ModeUnavailable);
        }
        let snapshot = self.current_snapshot()?;
        if snapshot.snapshot_id != request.snapshot_id {
            return Err(CaptureFailure::DisplaySnapshotChanged);
        }
        let resolved = self.resolve(request, snapshot)?;
        decoded_byte_len(
            resolved.expected_frame_size.width,
            resolved.expected_frame_size.height,
        )
        .map_err(CaptureFailure::from)?;
        let frame = self
            .backend
            .capture(&resolved)
            .map_err(CaptureFailure::from)?;
        let (logical_bounds, pixel_regions) = match frame.approved_layout {
            Some(layout) if mode_capability.portal_approval_required => {
                validate_portal_layout(&layout, frame.width, frame.height)?;
                (layout.logical_bounds, layout.pixel_regions)
            }
            Some(_) => return Err(CaptureFailure::MalformedImage),
            None => {
                if frame.width != resolved.expected_frame_size.width
                    || frame.height != resolved.expected_frame_size.height
                {
                    return Err(CaptureFailure::MalformedImage);
                }
                (resolved.logical_bounds, resolved.pixel_regions)
            }
        };
        let expected_len =
            decoded_byte_len(frame.width, frame.height).map_err(CaptureFailure::from)?;
        if frame.rgba.len() != expected_len {
            return Err(CaptureFailure::MalformedImage);
        }
        let image = encode_image(
            &DecodedImage {
                width: frame.width,
                height: frame.height,
                rgba: frame.rgba,
            },
            resolved.output_media_type,
        )
        .map_err(CaptureFailure::from)?;
        Ok(CaptureResult {
            mode: resolved.mode,
            pointer: resolved.pointer,
            logical_bounds,
            pixel_regions,
            image,
        })
    }

    pub(crate) fn cancel(&self, session_id: &CaptureSessionId) -> Result<(), CaptureFailure> {
        if session_id.0.is_empty() || session_id.0.len() > 128 {
            return Err(CaptureFailure::InvalidSelection);
        }
        self.backend
            .cancel(session_id)
            .map_err(CaptureFailure::from)
    }

    fn current_snapshot(&self) -> Result<DisplaySnapshot, CaptureFailure> {
        let displays = self.backend.displays().map_err(CaptureFailure::from)?;
        if displays.len() > MAX_CAPTURE_DISPLAYS {
            return Err(CaptureFailure::InvalidDisplaySnapshot);
        }
        DisplaySnapshot::checked(displays)
    }

    fn require_granted_permission(&self) -> Result<CapturePermission, CaptureFailure> {
        match self.backend.permission().map_err(CaptureFailure::from)? {
            CapturePermission::Granted => Ok(CapturePermission::Granted),
            CapturePermission::PromptRequired => Err(CaptureFailure::PermissionRequired),
            CapturePermission::Denied => Err(CaptureFailure::PermissionDenied),
        }
    }

    fn resolve(
        &self,
        request: CaptureRequest,
        snapshot: DisplaySnapshot,
    ) -> Result<ResolvedCaptureRequest, CaptureFailure> {
        let (logical_bounds, window) = match &request.source {
            CaptureSourceSelection::Region { selection } => {
                if selection.snapshot_id != snapshot.snapshot_id {
                    return Err(CaptureFailure::DisplaySnapshotChanged);
                }
                (selection.bounds.checked()?, None)
            }
            CaptureSourceSelection::Window { window_id } => {
                let windows = self
                    .backend
                    .windows(&snapshot)
                    .map_err(CaptureFailure::from)?;
                if windows.len() > MAX_CAPTURE_WINDOWS {
                    return Err(CaptureFailure::InvalidDisplaySnapshot);
                }
                let mut window_ids = windows.iter().map(|window| &window.id).collect::<Vec<_>>();
                window_ids.sort_by(|left, right| left.0.cmp(&right.0));
                if window_ids.windows(2).any(|pair| pair[0] == pair[1]) {
                    return Err(CaptureFailure::InvalidDisplaySnapshot);
                }
                let window = windows
                    .into_iter()
                    .find(|window| &window.id == window_id)
                    .ok_or(CaptureFailure::WindowClosed)?;
                if window.availability == WindowAvailability::Minimized {
                    return Err(CaptureFailure::WindowMinimized);
                }
                let window = window.checked(&snapshot)?;
                (
                    window.bounds,
                    Some(ResolvedWindowSource {
                        id: window.id,
                        display_id: window.display_id,
                        bounds: window.bounds,
                    }),
                )
            }
            CaptureSourceSelection::Display { display_id } => (
                snapshot
                    .display(display_id)
                    .map(|display| display.logical_bounds)
                    .ok_or(CaptureFailure::DisplaySnapshotChanged)?,
                None,
            ),
            CaptureSourceSelection::MultiMonitor { display_ids } => {
                if display_ids.is_empty() {
                    return Err(CaptureFailure::InvalidSelection);
                }
                let mut unique = display_ids.clone();
                unique.sort_by(|left, right| left.0.cmp(&right.0));
                unique.dedup();
                if unique.len() != display_ids.len() {
                    return Err(CaptureFailure::InvalidSelection);
                }
                (
                    LogicalRect::bounding(
                        display_ids
                            .iter()
                            .map(|id| {
                                snapshot
                                    .display(id)
                                    .map(|display| display.logical_bounds)
                                    .ok_or(CaptureFailure::DisplaySnapshotChanged)
                            })
                            .collect::<Result<Vec<_>, _>>()?
                            .into_iter(),
                    )
                    .ok_or(CaptureFailure::InvalidSelection)?,
                    None,
                )
            }
        };
        let pixel_regions = match &request.source {
            CaptureSourceSelection::Display { display_id } => {
                let display = snapshot
                    .display(display_id)
                    .ok_or(CaptureFailure::DisplaySnapshotChanged)?;
                vec![DisplayPixelRegion {
                    display_id: display.id.clone(),
                    pixels: geometry::PixelRect {
                        x: 0,
                        y: 0,
                        width: display.physical_size.width,
                        height: display.physical_size.height,
                    },
                }]
            }
            CaptureSourceSelection::MultiMonitor { display_ids } => {
                snapshot.selected_pixel_regions(display_ids, logical_bounds)?
            }
            CaptureSourceSelection::Window { .. } => snapshot.non_overlapping_pixel_regions(
                &window
                    .as_ref()
                    .ok_or(CaptureFailure::InvalidDisplaySnapshot)?
                    .display_id,
                logical_bounds,
            )?,
            CaptureSourceSelection::Region { .. } => snapshot.pixel_regions(logical_bounds)?,
        };
        let mode = request.source.mode();
        let expected_frame_size = expected_frame_size(&snapshot, logical_bounds, &pixel_regions)?;
        Ok(ResolvedCaptureRequest {
            session_id: request.session_id,
            snapshot,
            source: request.source,
            window,
            mode,
            pointer: request.pointer,
            output_media_type: request.output_media_type,
            logical_bounds,
            pixel_regions,
            expected_frame_size,
        })
    }
}

fn validate_portal_layout(
    layout: &PortalApprovedLayout,
    width: u32,
    height: u32,
) -> Result<(), CaptureFailure> {
    layout.logical_bounds.checked()?;
    if layout.pixel_regions.is_empty() || layout.pixel_regions.len() > 64 {
        return Err(CaptureFailure::InvalidDisplaySnapshot);
    }
    for region in &layout.pixel_regions {
        let pixels = region.pixels;
        if region.display_id.0.is_empty()
            || region.display_id.0.len() > 128
            || pixels.width == 0
            || pixels.height == 0
            || pixels
                .x
                .checked_add(pixels.width)
                .is_none_or(|right| right > width)
            || pixels
                .y
                .checked_add(pixels.height)
                .is_none_or(|bottom| bottom > height)
        {
            return Err(CaptureFailure::InvalidDisplaySnapshot);
        }
    }
    Ok(())
}

fn expected_frame_size(
    snapshot: &DisplaySnapshot,
    logical_bounds: LogicalRect,
    pixel_regions: &[DisplayPixelRegion],
) -> Result<geometry::PhysicalSize, CaptureFailure> {
    if let [pixel_region] = pixel_regions {
        let display = snapshot
            .display(&pixel_region.display_id)
            .ok_or(CaptureFailure::InvalidDisplaySnapshot)?;
        if logical_bounds.x >= display.logical_bounds.x
            && logical_bounds.y >= display.logical_bounds.y
            && logical_bounds.right() <= display.logical_bounds.right()
            && logical_bounds.bottom() <= display.logical_bounds.bottom()
        {
            return Ok(geometry::PhysicalSize {
                width: pixel_region.pixels.width,
                height: pixel_region.pixels.height,
            });
        }
    }

    let scale = pixel_regions
        .iter()
        .map(|region| {
            snapshot
                .display(&region.display_id)
                .map(|display| display.scale)
                .ok_or(CaptureFailure::InvalidDisplaySnapshot)
        })
        .collect::<Result<Vec<_>, _>>()?
        .into_iter()
        .max_by(|left, right| {
            (u64::from(left.numerator) * u64::from(right.denominator))
                .cmp(&(u64::from(right.numerator) * u64::from(left.denominator)))
        })
        .ok_or(CaptureFailure::InvalidSelection)?;

    Ok(geometry::PhysicalSize {
        width: scale.apply_span(logical_bounds.x, logical_bounds.right())?,
        height: scale.apply_span(logical_bounds.y, logical_bounds.bottom())?,
    })
}

#[cfg_attr(not(feature = "desktop-cef"), allow(dead_code))]
pub(crate) fn record_outcome<T>(result: &Result<T, CaptureFailure>) {
    use crate::diagnostics::{DiagnosticClassification as Classification, DiagnosticEventId};

    let classification = match result {
        Ok(_) => Classification::RealqaCaptureCompleted,
        Err(CaptureFailure::UnsupportedPlatform) => {
            Classification::RealqaCaptureUnsupportedPlatform
        }
        Err(CaptureFailure::BackendUnavailable) => Classification::RealqaCaptureBackendUnavailable,
        Err(CaptureFailure::PermissionRequired) => Classification::RealqaCapturePermissionRequired,
        Err(CaptureFailure::PermissionDenied) => Classification::RealqaCapturePermissionDenied,
        Err(CaptureFailure::PermissionLost) => Classification::RealqaCapturePermissionLost,
        Err(CaptureFailure::Cancelled) => Classification::RealqaCaptureCancelled,
        Err(CaptureFailure::PortalCancelled) => Classification::RealqaCapturePortalCancelled,
        Err(CaptureFailure::ProtectedContent) => Classification::RealqaCaptureProtectedContent,
        Err(CaptureFailure::WindowMinimized) => Classification::RealqaCaptureWindowMinimized,
        Err(CaptureFailure::WindowClosed) => Classification::RealqaCaptureWindowClosed,
        Err(CaptureFailure::WindowLost) => Classification::RealqaCaptureWindowLost,
        Err(CaptureFailure::DisplayRemoved) => Classification::RealqaCaptureDisplayRemoved,
        Err(CaptureFailure::ModeUnavailable) => Classification::RealqaCaptureModeUnavailable,
        Err(CaptureFailure::DisplaySnapshotChanged) => Classification::RealqaCaptureDisplayChanged,
        Err(CaptureFailure::InvalidDisplaySnapshot | CaptureFailure::InvalidSelection) => {
            Classification::RealqaCaptureInvalidRequest
        }
        Err(
            CaptureFailure::MalformedImage
            | CaptureFailure::UnsupportedImage
            | CaptureFailure::DecompressionBomb
            | CaptureFailure::ImageEncodedLimitExceeded
            | CaptureFailure::SessionEncodedLimitExceeded
            | CaptureFailure::EncodingFailed,
        ) => Classification::RealqaCaptureImageRejected,
        Err(CaptureFailure::CaptureFailed) => Classification::RealqaCaptureFailed,
    };
    if result.is_ok() {
        crate::diagnostics::emit(DiagnosticEventId::RealqaCaptureOutcome, classification);
    } else {
        crate::diagnostics::emit_warning(DiagnosticEventId::RealqaCaptureOutcome, classification);
    }
}

#[cfg(test)]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum CaptureDiagnosticEventId {
    RealqaCaptureOutcome,
}

#[cfg(test)]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum CaptureDiagnosticClassification {
    Completed,
    UnsupportedPlatform,
    BackendUnavailable,
    PermissionRequired,
    PermissionDenied,
    PermissionLost,
    Cancelled,
    PortalCancelled,
    ProtectedContent,
    WindowMinimized,
    WindowClosed,
    WindowLost,
    DisplayRemoved,
    ModeUnavailable,
    DisplayChanged,
    InvalidRequest,
    ImageRejected,
    CaptureFailed,
}

#[cfg(test)]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct CaptureDiagnostic {
    event_id: CaptureDiagnosticEventId,
    classification: CaptureDiagnosticClassification,
}

#[cfg(test)]
impl CaptureDiagnostic {
    pub(crate) const fn completed() -> Self {
        Self {
            event_id: CaptureDiagnosticEventId::RealqaCaptureOutcome,
            classification: CaptureDiagnosticClassification::Completed,
        }
    }

    pub(crate) const fn failed(failure: CaptureFailure) -> Self {
        let classification = match failure {
            CaptureFailure::UnsupportedPlatform => {
                CaptureDiagnosticClassification::UnsupportedPlatform
            }
            CaptureFailure::BackendUnavailable => {
                CaptureDiagnosticClassification::BackendUnavailable
            }
            CaptureFailure::PermissionRequired => {
                CaptureDiagnosticClassification::PermissionRequired
            }
            CaptureFailure::PermissionDenied => CaptureDiagnosticClassification::PermissionDenied,
            CaptureFailure::PermissionLost => CaptureDiagnosticClassification::PermissionLost,
            CaptureFailure::Cancelled => CaptureDiagnosticClassification::Cancelled,
            CaptureFailure::PortalCancelled => CaptureDiagnosticClassification::PortalCancelled,
            CaptureFailure::ProtectedContent => CaptureDiagnosticClassification::ProtectedContent,
            CaptureFailure::WindowMinimized => CaptureDiagnosticClassification::WindowMinimized,
            CaptureFailure::WindowClosed => CaptureDiagnosticClassification::WindowClosed,
            CaptureFailure::WindowLost => CaptureDiagnosticClassification::WindowLost,
            CaptureFailure::DisplayRemoved => CaptureDiagnosticClassification::DisplayRemoved,
            CaptureFailure::ModeUnavailable => CaptureDiagnosticClassification::ModeUnavailable,
            CaptureFailure::DisplaySnapshotChanged => {
                CaptureDiagnosticClassification::DisplayChanged
            }
            CaptureFailure::InvalidDisplaySnapshot | CaptureFailure::InvalidSelection => {
                CaptureDiagnosticClassification::InvalidRequest
            }
            CaptureFailure::MalformedImage
            | CaptureFailure::UnsupportedImage
            | CaptureFailure::DecompressionBomb
            | CaptureFailure::ImageEncodedLimitExceeded
            | CaptureFailure::SessionEncodedLimitExceeded
            | CaptureFailure::EncodingFailed => CaptureDiagnosticClassification::ImageRejected,
            CaptureFailure::CaptureFailed => CaptureDiagnosticClassification::CaptureFailed,
        };
        Self {
            event_id: CaptureDiagnosticEventId::RealqaCaptureOutcome,
            classification,
        }
    }
}

pub(crate) struct PlatformCaptureBackend {
    platform: CapturePlatform,
    #[cfg(target_os = "windows")]
    windows: Option<windows::WindowsCaptureBackend>,
    #[cfg(target_os = "linux")]
    linux: Option<linux::LinuxCaptureBackend>,
}

impl PlatformCaptureBackend {
    #[cfg(test)]
    pub(crate) const fn new(platform: CapturePlatform) -> Self {
        Self {
            platform,
            #[cfg(target_os = "windows")]
            windows: None,
            #[cfg(target_os = "linux")]
            linux: None,
        }
    }

    #[cfg(not(any(target_os = "android", target_os = "ios")))]
    pub(crate) fn current() -> Self {
        #[cfg(target_os = "windows")]
        {
            return Self {
                platform: CapturePlatform::Windows,
                windows: windows::WindowsCaptureBackend::system(),
            };
        }
        #[cfg(target_os = "linux")]
        {
            Self {
                platform: CapturePlatform::Linux,
                linux: linux::LinuxCaptureBackend::current().ok(),
            }
        }
        #[cfg(not(any(target_os = "windows", target_os = "linux")))]
        {
            Self {
                platform: CapturePlatform::Macos,
            }
        }
    }
}

impl CaptureBackend for PlatformCaptureBackend {
    fn platform(&self) -> CapturePlatform {
        self.platform
    }

    fn start_capture(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        #[cfg(target_os = "windows")]
        if let Some(backend) = &self.windows {
            return backend.start_capture(session_id);
        }
        let _ = session_id;
        Ok(())
    }

    fn finish_capture(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        #[cfg(target_os = "windows")]
        if let Some(backend) = &self.windows {
            return backend.finish_capture(session_id);
        }
        let _ = session_id;
        Ok(())
    }

    fn capabilities(&self) -> Result<CaptureCapabilities, BackendFailure> {
        #[cfg(target_os = "windows")]
        if let Some(backend) = &self.windows {
            return backend.capabilities();
        }
        #[cfg(target_os = "linux")]
        if let Some(backend) = &self.linux {
            return backend.capabilities();
        }
        Err(BackendFailure::Unavailable)
    }

    fn permission(&self) -> Result<CapturePermission, BackendFailure> {
        #[cfg(target_os = "windows")]
        if let Some(backend) = &self.windows {
            return backend.permission();
        }
        #[cfg(target_os = "linux")]
        if let Some(backend) = &self.linux {
            return backend.permission();
        }
        Err(BackendFailure::Unavailable)
    }

    fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
        #[cfg(target_os = "windows")]
        if let Some(backend) = &self.windows {
            return backend.displays();
        }
        #[cfg(target_os = "linux")]
        if let Some(backend) = &self.linux {
            return backend.displays();
        }
        Err(BackendFailure::Unavailable)
    }

    fn windows(&self, snapshot: &DisplaySnapshot) -> Result<Vec<WindowSource>, BackendFailure> {
        #[cfg(target_os = "windows")]
        if let Some(backend) = &self.windows {
            return backend.windows(snapshot);
        }
        #[cfg(target_os = "linux")]
        if let Some(backend) = &self.linux {
            return backend.windows(snapshot);
        }
        let _ = snapshot;
        Err(BackendFailure::Unavailable)
    }

    fn capture(&self, request: &ResolvedCaptureRequest) -> Result<BackendFrame, BackendFailure> {
        #[cfg(target_os = "windows")]
        if let Some(backend) = &self.windows {
            return backend.capture(request);
        }
        #[cfg(target_os = "linux")]
        if let Some(backend) = &self.linux {
            return backend.capture(request);
        }
        let _ = request;
        Err(BackendFailure::Unavailable)
    }

    fn cancel(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        #[cfg(target_os = "windows")]
        if let Some(backend) = &self.windows {
            return backend.cancel(session_id);
        }
        #[cfg(target_os = "linux")]
        if let Some(backend) = &self.linux {
            return backend.cancel(session_id);
        }
        let _ = session_id;
        Err(BackendFailure::Unavailable)
    }
}

#[cfg(test)]
mod tests {
    use std::sync::{
        Mutex,
        atomic::{AtomicUsize, Ordering},
    };

    use geometry::{PhysicalSize, PixelRect, ScaleFactor};

    use super::*;

    struct FixtureBackend {
        platform: CapturePlatform,
        permission: Mutex<Result<CapturePermission, BackendFailure>>,
        displays: Mutex<Vec<DisplayDescriptor>>,
        windows: Mutex<Vec<WindowSource>>,
        display_calls: AtomicUsize,
        window_calls: AtomicUsize,
        capture_result: Mutex<Result<Option<BackendFrame>, BackendFailure>>,
        finish_result: Mutex<Result<(), BackendFailure>>,
        last_request: Mutex<Option<ResolvedCaptureRequest>>,
    }

    impl FixtureBackend {
        fn new(platform: CapturePlatform) -> Self {
            Self {
                platform,
                permission: Mutex::new(Ok(CapturePermission::Granted)),
                displays: Mutex::new(vec![DisplayDescriptor {
                    id: DisplayId("display-1".to_owned()),
                    logical_bounds: LogicalRect {
                        x: -100.0,
                        y: 0.0,
                        width: 200.0,
                        height: 100.0,
                    },
                    physical_size: PhysicalSize {
                        width: 400,
                        height: 200,
                    },
                    scale: ScaleFactor {
                        numerator: 2,
                        denominator: 1,
                    },
                    primary: true,
                }]),
                windows: Mutex::new(vec![WindowSource {
                    id: WindowSourceId("window-1".to_owned()),
                    display_id: DisplayId("display-1".to_owned()),
                    bounds: LogicalRect {
                        x: -50.0,
                        y: 10.0,
                        width: 50.0,
                        height: 50.0,
                    },
                    availability: WindowAvailability::Available,
                    metadata: WindowMetadata::default(),
                }]),
                display_calls: AtomicUsize::new(0),
                window_calls: AtomicUsize::new(0),
                capture_result: Mutex::new(Ok(None)),
                finish_result: Mutex::new(Ok(())),
                last_request: Mutex::new(None),
            }
        }
    }

    impl CaptureBackend for FixtureBackend {
        fn platform(&self) -> CapturePlatform {
            self.platform
        }

        fn capabilities(&self) -> Result<CaptureCapabilities, BackendFailure> {
            Ok(CaptureCapabilities {
                platform: self.platform,
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

        fn permission(&self) -> Result<CapturePermission, BackendFailure> {
            *self.permission.lock().expect("permission lock")
        }

        fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
            self.display_calls.fetch_add(1, Ordering::Relaxed);
            Ok(self.displays.lock().expect("display lock").clone())
        }

        fn windows(
            &self,
            _snapshot: &DisplaySnapshot,
        ) -> Result<Vec<WindowSource>, BackendFailure> {
            self.window_calls.fetch_add(1, Ordering::Relaxed);
            Ok(self.windows.lock().expect("window lock").clone())
        }

        fn capture(
            &self,
            request: &ResolvedCaptureRequest,
        ) -> Result<BackendFrame, BackendFailure> {
            *self.last_request.lock().expect("request lock") = Some(request.clone());
            match self.capture_result.lock().expect("capture lock").clone()? {
                Some(frame) => Ok(frame),
                None => {
                    let mut rgba = vec![
                        0;
                        decoded_byte_len(
                            request.expected_frame_size.width,
                            request.expected_frame_size.height,
                        )
                        .expect("fixture frame dimensions must be valid")
                    ];
                    rgba[..4].copy_from_slice(&[0, 1, 2, 255]);
                    Ok(BackendFrame {
                        width: request.expected_frame_size.width,
                        height: request.expected_frame_size.height,
                        rgba,
                        approved_layout: None,
                    })
                }
            }
        }

        fn finish_capture(&self, _session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
            *self.finish_result.lock().expect("finish lock")
        }

        fn cancel(&self, _session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
            Ok(())
        }
    }

    fn request(catalog: &CaptureSourceCatalog) -> CaptureRequest {
        CaptureRequest {
            session_id: CaptureSessionId("session-1".to_owned()),
            snapshot_id: catalog.snapshot.snapshot_id.clone(),
            source: CaptureSourceSelection::Region {
                selection: SelectionGeometry {
                    snapshot_id: catalog.snapshot.snapshot_id.clone(),
                    bounds: LogicalRect {
                        x: -25.0,
                        y: 10.0,
                        width: 50.0,
                        height: 25.0,
                    },
                },
            },
            pointer: PointerInclusion::Include,
            output_media_type: ImageMediaType::Png,
        }
    }

    #[test]
    fn fixture_backend_runs_identically_for_all_desktop_platforms() {
        for platform in [
            CapturePlatform::Macos,
            CapturePlatform::Windows,
            CapturePlatform::Linux,
        ] {
            let backend = Arc::new(FixtureBackend::new(platform));
            let core = CaptureCore::new(backend.clone());
            let catalog = core.source_catalog().expect("catalog must load");
            assert_eq!(catalog.platform, platform);
            let result = core.begin(request(&catalog)).expect("capture must work");
            assert_eq!(result.mode, CaptureMode::Region);
            assert_eq!(result.pointer, PointerInclusion::Include);
            let decoded = decode_image(&result.image).expect("result image must decode");
            assert_eq!((decoded.width, decoded.height), (100, 50));
            assert_eq!(&decoded.rgba[..4], &[0, 1, 2, 255]);
            let backend_request = backend
                .last_request
                .lock()
                .expect("request lock")
                .clone()
                .expect("backend must receive request");
            assert_eq!(backend_request.pixel_regions[0].pixels.x, 150);
            assert_eq!(
                backend_request.expected_frame_size,
                PhysicalSize {
                    width: 100,
                    height: 50,
                }
            );
            let adjusted = core
                .adjust_selection(
                    &SelectionGeometry {
                        snapshot_id: catalog.snapshot.snapshot_id.clone(),
                        bounds: LogicalRect {
                            x: -25.0,
                            y: 10.0,
                            width: 50.0,
                            height: 25.0,
                        },
                    },
                    SelectionAdjustment::Move {
                        delta_x: 1.0,
                        delta_y: 1.0,
                    },
                )
                .expect("selection must adjust");
            assert_eq!(adjusted.bounds.x, -24.0);
            core.cancel(&CaptureSessionId("session-1".to_owned()))
                .expect("fixture cancellation must work");
        }
    }

    #[test]
    fn begin_rejects_oversized_frames_before_backend_capture() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Linux));
        *backend.displays.lock().expect("display lock") = vec![DisplayDescriptor {
            id: DisplayId("oversized".to_owned()),
            logical_bounds: LogicalRect {
                x: 0.0,
                y: 0.0,
                width: 10_001.0,
                height: 10_000.0,
            },
            physical_size: PhysicalSize {
                width: 10_001,
                height: 10_000,
            },
            scale: ScaleFactor {
                numerator: 1,
                denominator: 1,
            },
            primary: true,
        }];
        backend.windows.lock().expect("window lock").clear();
        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        let mut capture_request = request(&catalog);
        capture_request.source = CaptureSourceSelection::Display {
            display_id: DisplayId("oversized".to_owned()),
        };

        assert_eq!(
            core.begin(capture_request),
            Err(CaptureFailure::DecompressionBomb)
        );
        assert!(backend.last_request.lock().expect("request lock").is_none());
    }

    #[test]
    fn source_selection_deserializes_camel_case_variant_fields() {
        for (value, expected) in [
            (
                serde_json::json!({"mode": "window", "windowId": "window-1"}),
                CaptureSourceSelection::Window {
                    window_id: WindowSourceId("window-1".to_owned()),
                },
            ),
            (
                serde_json::json!({"mode": "display", "displayId": "display-1"}),
                CaptureSourceSelection::Display {
                    display_id: DisplayId("display-1".to_owned()),
                },
            ),
            (
                serde_json::json!({
                    "mode": "multi-monitor",
                    "displayIds": ["display-1", "display-2"]
                }),
                CaptureSourceSelection::MultiMonitor {
                    display_ids: vec![
                        DisplayId("display-1".to_owned()),
                        DisplayId("display-2".to_owned()),
                    ],
                },
            ),
        ] {
            assert_eq!(
                serde_json::from_value::<CaptureSourceSelection>(value)
                    .expect("camel-case source selection must deserialize"),
                expected
            );
        }
    }

    #[test]
    fn source_catalog_accepts_minimized_windows_without_active_bounds() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
        let mut window = backend.windows.lock().expect("window lock");
        window[0].availability = WindowAvailability::Minimized;
        window[0].bounds.width = 0.0;
        window[0].bounds.height = 0.0;
        drop(window);

        let catalog = CaptureCore::new(backend)
            .source_catalog()
            .expect("minimized window must not invalidate the catalog");
        assert_eq!(catalog.windows.len(), 1);
        assert_eq!(
            catalog.windows[0].availability,
            WindowAvailability::Minimized
        );
    }

    #[test]
    fn source_catalog_rejects_unauthorized_permission_before_enumerating_sources() {
        for (permission, expected) in [
            (
                CapturePermission::PromptRequired,
                CaptureFailure::PermissionRequired,
            ),
            (CapturePermission::Denied, CaptureFailure::PermissionDenied),
        ] {
            let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
            *backend.permission.lock().expect("permission lock") = Ok(permission);

            assert_eq!(
                CaptureCore::new(backend.clone()).source_catalog(),
                Err(expected)
            );
            assert_eq!(backend.display_calls.load(Ordering::Relaxed), 0);
            assert_eq!(backend.window_calls.load(Ordering::Relaxed), 0);
        }
    }

    #[test]
    fn source_catalog_bounds_display_and_window_enumeration() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
        let core = CaptureCore::new(backend.clone());
        *backend.displays.lock().expect("display lock") = (0..=MAX_CAPTURE_DISPLAYS)
            .map(|index| DisplayDescriptor {
                id: DisplayId(format!("display-{index}")),
                logical_bounds: LogicalRect {
                    x: f64::from(u32::try_from(index).expect("bounded index")) * 10.0,
                    y: 0.0,
                    width: 10.0,
                    height: 10.0,
                },
                physical_size: PhysicalSize {
                    width: 10,
                    height: 10,
                },
                scale: ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
                primary: index == 0,
            })
            .collect();
        assert_eq!(
            core.source_catalog(),
            Err(CaptureFailure::InvalidDisplaySnapshot)
        );

        *backend.displays.lock().expect("display lock") =
            FixtureBackend::new(CapturePlatform::Windows)
                .displays
                .into_inner()
                .expect("fixture displays");
        let display_id = backend.displays.lock().expect("display lock")[0].id.clone();
        *backend.windows.lock().expect("window lock") = (0..=MAX_CAPTURE_WINDOWS)
            .map(|index| WindowSource {
                id: WindowSourceId(format!("window-{index}")),
                display_id: display_id.clone(),
                bounds: LogicalRect {
                    x: -10.0,
                    y: 0.0,
                    width: 2.0,
                    height: 2.0,
                },
                availability: WindowAvailability::Available,
                metadata: WindowMetadata::default(),
            })
            .collect();
        assert_eq!(
            core.source_catalog(),
            Err(CaptureFailure::InvalidDisplaySnapshot)
        );
    }

    #[test]
    fn source_catalog_rejects_path_bearing_or_control_character_metadata() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
        let core = CaptureCore::new(backend.clone());
        backend.windows.lock().expect("window lock")[0].metadata = WindowMetadata {
            process_name: Some(r"C:\private\capture.exe".to_owned()),
            title: Some("private\nwindow".to_owned()),
        };
        assert_eq!(
            core.source_catalog(),
            Err(CaptureFailure::InvalidDisplaySnapshot)
        );
    }

    #[test]
    fn selection_adjustment_rejects_revoked_permission_before_reading_displays() {
        for (permission, expected) in [
            (
                CapturePermission::PromptRequired,
                CaptureFailure::PermissionRequired,
            ),
            (CapturePermission::Denied, CaptureFailure::PermissionDenied),
        ] {
            let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
            let core = CaptureCore::new(backend.clone());
            let catalog = core.source_catalog().expect("catalog must load");
            backend.display_calls.store(0, Ordering::Relaxed);
            backend.window_calls.store(0, Ordering::Relaxed);
            *backend.permission.lock().expect("permission lock") = Ok(permission);

            assert_eq!(
                core.adjust_selection(
                    &SelectionGeometry {
                        snapshot_id: catalog.snapshot.snapshot_id,
                        bounds: LogicalRect {
                            x: -25.0,
                            y: 10.0,
                            width: 50.0,
                            height: 25.0,
                        },
                    },
                    SelectionAdjustment::Move {
                        delta_x: 1.0,
                        delta_y: 1.0,
                    },
                ),
                Err(expected)
            );
            assert_eq!(backend.display_calls.load(Ordering::Relaxed), 0);
            assert_eq!(backend.window_calls.load(Ordering::Relaxed), 0);
        }
    }

    #[test]
    fn begin_rejects_revoked_permission_before_resolving_sources() {
        for (permission, expected) in [
            (
                CapturePermission::PromptRequired,
                CaptureFailure::PermissionRequired,
            ),
            (CapturePermission::Denied, CaptureFailure::PermissionDenied),
        ] {
            let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
            let core = CaptureCore::new(backend.clone());
            let catalog = core.source_catalog().expect("catalog must load");
            let mut capture_request = request(&catalog);
            capture_request.source = CaptureSourceSelection::Window {
                window_id: WindowSourceId("window-1".to_owned()),
            };
            backend.display_calls.store(0, Ordering::Relaxed);
            backend.window_calls.store(0, Ordering::Relaxed);
            *backend.permission.lock().expect("permission lock") = Ok(permission);

            assert_eq!(core.begin(capture_request), Err(expected));
            assert_eq!(backend.display_calls.load(Ordering::Relaxed), 0);
            assert_eq!(backend.window_calls.load(Ordering::Relaxed), 0);
            assert!(backend.last_request.lock().expect("request lock").is_none());
        }
    }

    #[test]
    fn display_capture_excludes_overlapping_unselected_displays() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
        let overlapping_bounds = LogicalRect {
            x: 0.0,
            y: 0.0,
            width: 100.0,
            height: 100.0,
        };
        *backend.displays.lock().expect("display lock") = vec![
            DisplayDescriptor {
                id: DisplayId("mirrored-1".to_owned()),
                logical_bounds: overlapping_bounds,
                physical_size: PhysicalSize {
                    width: 100,
                    height: 100,
                },
                scale: ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
                primary: true,
            },
            DisplayDescriptor {
                id: DisplayId("mirrored-2".to_owned()),
                logical_bounds: overlapping_bounds,
                physical_size: PhysicalSize {
                    width: 200,
                    height: 200,
                },
                scale: ScaleFactor {
                    numerator: 2,
                    denominator: 1,
                },
                primary: false,
            },
        ];
        backend.windows.lock().expect("window lock").clear();

        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        let mut capture_request = request(&catalog);
        capture_request.source = CaptureSourceSelection::Display {
            display_id: DisplayId("mirrored-2".to_owned()),
        };
        core.begin(capture_request).expect("capture must work");

        let backend_request = backend
            .last_request
            .lock()
            .expect("request lock")
            .clone()
            .expect("backend must receive request");
        assert_eq!(
            backend_request.pixel_regions,
            vec![DisplayPixelRegion {
                display_id: DisplayId("mirrored-2".to_owned()),
                pixels: PixelRect {
                    x: 0,
                    y: 0,
                    width: 200,
                    height: 200,
                },
            }]
        );
        assert_eq!(
            backend_request.expected_frame_size,
            PhysicalSize {
                width: 200,
                height: 200,
            }
        );
    }

    #[test]
    fn window_capture_excludes_overlapping_unselected_displays() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
        let overlapping_bounds = LogicalRect {
            x: 0.0,
            y: 0.0,
            width: 100.0,
            height: 100.0,
        };
        *backend.displays.lock().expect("display lock") = vec![
            DisplayDescriptor {
                id: DisplayId("mirrored-1".to_owned()),
                logical_bounds: overlapping_bounds,
                physical_size: PhysicalSize {
                    width: 100,
                    height: 100,
                },
                scale: ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
                primary: true,
            },
            DisplayDescriptor {
                id: DisplayId("mirrored-2".to_owned()),
                logical_bounds: overlapping_bounds,
                physical_size: PhysicalSize {
                    width: 200,
                    height: 200,
                },
                scale: ScaleFactor {
                    numerator: 2,
                    denominator: 1,
                },
                primary: false,
            },
        ];
        *backend.windows.lock().expect("window lock") = vec![WindowSource {
            id: WindowSourceId("window-1".to_owned()),
            display_id: DisplayId("mirrored-2".to_owned()),
            bounds: LogicalRect {
                x: 10.0,
                y: 20.0,
                width: 30.0,
                height: 40.0,
            },
            availability: WindowAvailability::Available,
            metadata: WindowMetadata::default(),
        }];

        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        let mut capture_request = request(&catalog);
        capture_request.source = CaptureSourceSelection::Window {
            window_id: WindowSourceId("window-1".to_owned()),
        };
        core.begin(capture_request).expect("capture must work");

        let backend_request = backend
            .last_request
            .lock()
            .expect("request lock")
            .clone()
            .expect("backend must receive request");
        assert_eq!(
            backend_request.pixel_regions,
            vec![DisplayPixelRegion {
                display_id: DisplayId("mirrored-2".to_owned()),
                pixels: PixelRect {
                    x: 20,
                    y: 40,
                    width: 60,
                    height: 80,
                },
            }]
        );
        assert_eq!(
            backend_request.expected_frame_size,
            PhysicalSize {
                width: 60,
                height: 80,
            }
        );
    }

    #[test]
    fn window_capture_reresolves_moved_and_resized_source_after_selection() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Linux));
        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        let moved_bounds = LogicalRect {
            x: -80.0,
            y: 20.0,
            width: 25.0,
            height: 15.0,
        };
        backend.windows.lock().expect("window lock")[0].bounds = moved_bounds;

        let mut capture_request = request(&catalog);
        capture_request.source = CaptureSourceSelection::Window {
            window_id: WindowSourceId("window-1".to_owned()),
        };
        let result = core.begin(capture_request).expect("capture must work");

        assert_eq!(result.logical_bounds, moved_bounds);
        let backend_request = backend
            .last_request
            .lock()
            .expect("request lock")
            .clone()
            .expect("backend must receive request");
        assert_eq!(backend_request.logical_bounds, moved_bounds);
        assert_eq!(
            backend_request.expected_frame_size,
            PhysicalSize {
                width: 50,
                height: 30,
            }
        );
    }

    #[test]
    fn window_capture_includes_each_display_spanned_by_the_window() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
        *backend.displays.lock().expect("display lock") = vec![
            DisplayDescriptor {
                id: DisplayId("left".to_owned()),
                logical_bounds: LogicalRect {
                    x: 0.0,
                    y: 0.0,
                    width: 100.0,
                    height: 100.0,
                },
                physical_size: PhysicalSize {
                    width: 100,
                    height: 100,
                },
                scale: ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
                primary: true,
            },
            DisplayDescriptor {
                id: DisplayId("right".to_owned()),
                logical_bounds: LogicalRect {
                    x: 100.0,
                    y: 0.0,
                    width: 100.0,
                    height: 100.0,
                },
                physical_size: PhysicalSize {
                    width: 200,
                    height: 200,
                },
                scale: ScaleFactor {
                    numerator: 2,
                    denominator: 1,
                },
                primary: false,
            },
        ];
        *backend.windows.lock().expect("window lock") = vec![WindowSource {
            id: WindowSourceId("window-1".to_owned()),
            display_id: DisplayId("left".to_owned()),
            bounds: LogicalRect {
                x: 50.0,
                y: 10.0,
                width: 100.0,
                height: 40.0,
            },
            availability: WindowAvailability::Available,
            metadata: WindowMetadata::default(),
        }];

        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        let mut capture_request = request(&catalog);
        capture_request.source = CaptureSourceSelection::Window {
            window_id: WindowSourceId("window-1".to_owned()),
        };
        core.begin(capture_request).expect("capture must work");

        let backend_request = backend
            .last_request
            .lock()
            .expect("request lock")
            .clone()
            .expect("backend must receive request");
        assert_eq!(
            backend_request.pixel_regions,
            vec![
                DisplayPixelRegion {
                    display_id: DisplayId("left".to_owned()),
                    pixels: PixelRect {
                        x: 50,
                        y: 10,
                        width: 50,
                        height: 40,
                    },
                },
                DisplayPixelRegion {
                    display_id: DisplayId("right".to_owned()),
                    pixels: PixelRect {
                        x: 0,
                        y: 20,
                        width: 100,
                        height: 80,
                    },
                },
            ]
        );
        assert_eq!(
            backend_request.expected_frame_size,
            PhysicalSize {
                width: 200,
                height: 80,
            }
        );
    }

    #[test]
    fn window_capture_rejects_duplicate_source_ids_at_begin() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        let duplicate = backend.windows.lock().expect("window lock")[0].clone();
        backend.windows.lock().expect("window lock").push(duplicate);

        let mut capture_request = request(&catalog);
        capture_request.source = CaptureSourceSelection::Window {
            window_id: WindowSourceId("window-1".to_owned()),
        };
        assert_eq!(
            core.begin(capture_request),
            Err(CaptureFailure::InvalidDisplaySnapshot)
        );
        assert!(backend.last_request.lock().expect("request lock").is_none());
    }

    #[test]
    fn multi_display_frame_size_uses_rounded_logical_edges() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
        *backend.displays.lock().expect("display lock") = vec![
            DisplayDescriptor {
                id: DisplayId("left".to_owned()),
                logical_bounds: LogicalRect {
                    x: -100.0,
                    y: 0.0,
                    width: 100.0,
                    height: 100.0,
                },
                physical_size: PhysicalSize {
                    width: 200,
                    height: 200,
                },
                scale: ScaleFactor {
                    numerator: 2,
                    denominator: 1,
                },
                primary: true,
            },
            DisplayDescriptor {
                id: DisplayId("right".to_owned()),
                logical_bounds: LogicalRect {
                    x: 0.0,
                    y: 0.0,
                    width: 100.0,
                    height: 100.0,
                },
                physical_size: PhysicalSize {
                    width: 200,
                    height: 200,
                },
                scale: ScaleFactor {
                    numerator: 2,
                    denominator: 1,
                },
                primary: false,
            },
        ];
        backend.windows.lock().expect("window lock").clear();

        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        let mut capture_request = request(&catalog);
        capture_request.source = CaptureSourceSelection::Region {
            selection: SelectionGeometry {
                snapshot_id: catalog.snapshot.snapshot_id.clone(),
                bounds: LogicalRect {
                    x: -0.25,
                    y: 0.25,
                    width: 1.0,
                    height: 1.0,
                },
            },
        };
        core.begin(capture_request).expect("capture must work");

        let backend_request = backend
            .last_request
            .lock()
            .expect("request lock")
            .clone()
            .expect("backend must receive request");
        assert_eq!(
            backend_request.expected_frame_size,
            PhysicalSize {
                width: 3,
                height: 3,
            }
        );
    }

    #[test]
    fn single_display_region_preserves_blank_desktop_gap_in_frame_size() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
        *backend.displays.lock().expect("display lock") = ["left", "right"]
            .into_iter()
            .enumerate()
            .map(|(index, id)| DisplayDescriptor {
                id: DisplayId(id.to_owned()),
                logical_bounds: LogicalRect {
                    x: index as f64 * 200.0,
                    y: 0.0,
                    width: 100.0,
                    height: 100.0,
                },
                physical_size: PhysicalSize {
                    width: 100,
                    height: 100,
                },
                scale: ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
                primary: index == 0,
            })
            .collect();
        backend.windows.lock().expect("window lock").clear();

        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        let mut capture_request = request(&catalog);
        capture_request.source = CaptureSourceSelection::Region {
            selection: SelectionGeometry {
                snapshot_id: catalog.snapshot.snapshot_id.clone(),
                bounds: LogicalRect {
                    x: 50.0,
                    y: 10.0,
                    width: 100.0,
                    height: 40.0,
                },
            },
        };
        core.begin(capture_request).expect("capture must work");

        let backend_request = backend
            .last_request
            .lock()
            .expect("request lock")
            .clone()
            .expect("backend must receive request");
        assert_eq!(
            backend_request.pixel_regions,
            vec![DisplayPixelRegion {
                display_id: DisplayId("left".to_owned()),
                pixels: PixelRect {
                    x: 50,
                    y: 10,
                    width: 50,
                    height: 40,
                },
            }]
        );
        assert_eq!(
            backend_request.expected_frame_size,
            PhysicalSize {
                width: 100,
                height: 40,
            }
        );
    }

    #[test]
    fn multi_monitor_capture_deduplicates_overlapping_selected_displays() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Linux));
        let overlapping_bounds = LogicalRect {
            x: 0.0,
            y: 0.0,
            width: 100.0,
            height: 100.0,
        };
        *backend.displays.lock().expect("display lock") = vec![
            DisplayDescriptor {
                id: DisplayId("mirrored-2".to_owned()),
                logical_bounds: overlapping_bounds,
                physical_size: PhysicalSize {
                    width: 200,
                    height: 200,
                },
                scale: ScaleFactor {
                    numerator: 2,
                    denominator: 1,
                },
                primary: false,
            },
            DisplayDescriptor {
                id: DisplayId("mirrored-1".to_owned()),
                logical_bounds: overlapping_bounds,
                physical_size: PhysicalSize {
                    width: 100,
                    height: 100,
                },
                scale: ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
                primary: true,
            },
        ];
        backend.windows.lock().expect("window lock").clear();

        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        let mut capture_request = request(&catalog);
        capture_request.source = CaptureSourceSelection::MultiMonitor {
            display_ids: vec![
                DisplayId("mirrored-2".to_owned()),
                DisplayId("mirrored-1".to_owned()),
            ],
        };
        core.begin(capture_request).expect("capture must work");

        let backend_request = backend
            .last_request
            .lock()
            .expect("request lock")
            .clone()
            .expect("backend must receive request");
        assert_eq!(
            backend_request.pixel_regions,
            vec![DisplayPixelRegion {
                display_id: DisplayId("mirrored-1".to_owned()),
                pixels: geometry::PixelRect {
                    x: 0,
                    y: 0,
                    width: 100,
                    height: 100,
                },
            }]
        );
        assert_eq!(
            backend_request.expected_frame_size,
            PhysicalSize {
                width: 100,
                height: 100,
            }
        );
    }

    #[test]
    fn multi_monitor_capture_excludes_unselected_displays() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Linux));
        *backend.displays.lock().expect("display lock") = ["left", "middle", "right"]
            .into_iter()
            .enumerate()
            .map(|(index, id)| {
                let scale = if id == "right" { 2 } else { 1 };
                DisplayDescriptor {
                    id: DisplayId(id.to_owned()),
                    logical_bounds: LogicalRect {
                        x: index as f64 * 100.0,
                        y: 0.0,
                        width: 100.0,
                        height: 100.0,
                    },
                    physical_size: PhysicalSize {
                        width: 100 * scale,
                        height: 100 * scale,
                    },
                    scale: ScaleFactor {
                        numerator: scale,
                        denominator: 1,
                    },
                    primary: index == 1,
                }
            })
            .collect();
        backend.windows.lock().expect("window lock").clear();

        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        let mut capture_request = request(&catalog);
        capture_request.source = CaptureSourceSelection::MultiMonitor {
            display_ids: vec![DisplayId("left".to_owned()), DisplayId("right".to_owned())],
        };
        core.begin(capture_request).expect("capture must work");

        let backend_request = backend
            .last_request
            .lock()
            .expect("request lock")
            .clone()
            .expect("backend must receive request");
        assert_eq!(
            backend_request.logical_bounds,
            LogicalRect {
                x: 0.0,
                y: 0.0,
                width: 300.0,
                height: 100.0,
            }
        );
        assert_eq!(
            backend_request.pixel_regions,
            vec![
                DisplayPixelRegion {
                    display_id: DisplayId("left".to_owned()),
                    pixels: PixelRect {
                        x: 0,
                        y: 0,
                        width: 100,
                        height: 100,
                    },
                },
                DisplayPixelRegion {
                    display_id: DisplayId("right".to_owned()),
                    pixels: PixelRect {
                        x: 0,
                        y: 0,
                        width: 200,
                        height: 200,
                    },
                },
            ]
        );
        assert_eq!(
            backend_request.expected_frame_size,
            PhysicalSize {
                width: 600,
                height: 200,
            }
        );
    }

    #[test]
    fn capture_rejects_backend_frame_dimensions_that_do_not_match_selection() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        let width = 101;
        let height = 50;
        *backend.capture_result.lock().expect("capture lock") = Ok(Some(BackendFrame {
            width,
            height,
            rgba: vec![
                0;
                decoded_byte_len(width, height)
                    .expect("mismatched fixture dimensions must remain bounded")
            ],
            approved_layout: None,
        }));

        assert_eq!(
            core.begin(request(&catalog)),
            Err(CaptureFailure::MalformedImage)
        );
    }

    #[test]
    fn cancellation_observed_during_encoding_discards_the_capture_result() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        *backend.finish_result.lock().expect("finish lock") = Err(BackendFailure::Cancelled);

        assert_eq!(
            core.begin(request(&catalog)),
            Err(CaptureFailure::Cancelled)
        );
        assert!(
            backend.last_request.lock().expect("request lock").is_some(),
            "the backend frame must be captured before final cancellation is observed"
        );
    }

    #[test]
    fn hot_plug_is_detected_before_backend_capture() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Linux));
        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        backend
            .displays
            .lock()
            .expect("display lock")
            .push(DisplayDescriptor {
                id: DisplayId("hot-plugged".to_owned()),
                logical_bounds: LogicalRect {
                    x: 100.0,
                    y: 0.0,
                    width: 100.0,
                    height: 100.0,
                },
                physical_size: PhysicalSize {
                    width: 100,
                    height: 100,
                },
                scale: ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
                primary: false,
            });
        assert_eq!(
            core.begin(request(&catalog)),
            Err(CaptureFailure::DisplaySnapshotChanged)
        );
        assert!(backend.last_request.lock().expect("request lock").is_none());
    }

    #[test]
    fn permission_protected_content_and_window_loss_are_closed_failures() {
        let backend = Arc::new(FixtureBackend::new(CapturePlatform::Windows));
        let core = CaptureCore::new(backend.clone());
        let catalog = core.source_catalog().expect("catalog must load");
        *backend.permission.lock().expect("permission lock") = Ok(CapturePermission::Denied);
        assert_eq!(
            core.begin(request(&catalog)),
            Err(CaptureFailure::PermissionDenied)
        );

        *backend.permission.lock().expect("permission lock") = Ok(CapturePermission::Granted);
        *backend.capture_result.lock().expect("capture lock") =
            Err(BackendFailure::ProtectedContent);
        assert_eq!(
            core.begin(request(&catalog)),
            Err(CaptureFailure::ProtectedContent)
        );

        let mut window_request = request(&catalog);
        window_request.source = CaptureSourceSelection::Window {
            window_id: WindowSourceId("window-1".to_owned()),
        };
        backend.windows.lock().expect("window lock").clear();
        assert_eq!(
            core.begin(window_request),
            Err(CaptureFailure::WindowClosed)
        );
    }

    #[test]
    fn portal_and_permission_loss_classifications_are_stable() {
        for (backend_failure, expected) in [
            (
                BackendFailure::PortalCancelled,
                CaptureFailure::PortalCancelled,
            ),
            (
                BackendFailure::PermissionLost,
                CaptureFailure::PermissionLost,
            ),
            (BackendFailure::WindowLost, CaptureFailure::WindowLost),
            (
                BackendFailure::WindowMinimized,
                CaptureFailure::WindowMinimized,
            ),
            (BackendFailure::WindowClosed, CaptureFailure::WindowClosed),
            (
                BackendFailure::DisplayRemoved,
                CaptureFailure::DisplayRemoved,
            ),
            (
                BackendFailure::ModeUnavailable,
                CaptureFailure::ModeUnavailable,
            ),
            (
                BackendFailure::DisplayChanged,
                CaptureFailure::DisplaySnapshotChanged,
            ),
        ] {
            let backend = Arc::new(FixtureBackend::new(CapturePlatform::Linux));
            let core = CaptureCore::new(backend.clone());
            let catalog = core.source_catalog().expect("catalog must load");
            *backend.capture_result.lock().expect("capture lock") = Err(backend_failure);
            assert_eq!(core.begin(request(&catalog)), Err(expected));
        }
    }

    #[test]
    fn diagnostics_serialize_only_closed_value_free_fields() {
        let serialized =
            serde_json::to_string(&CaptureDiagnostic::failed(CaptureFailure::ProtectedContent))
                .expect("diagnostic must serialize");
        assert_eq!(
            serialized,
            r#"{"eventId":"realqa-capture-outcome","classification":"protected-content"}"#
        );
        for forbidden in ["window-1", "/home/user", "secret", "backend"] {
            assert!(!serialized.contains(forbidden));
        }
        assert_eq!(
            serde_json::to_string(&CaptureDiagnostic::completed())
                .expect("completed diagnostic must serialize"),
            r#"{"eventId":"realqa-capture-outcome","classification":"completed"}"#
        );
        assert_eq!(
            CaptureDiagnostic::failed(CaptureFailure::UnsupportedPlatform).classification,
            CaptureDiagnosticClassification::UnsupportedPlatform
        );
        assert_eq!(
            CaptureDiagnostic::failed(CaptureFailure::PermissionRequired).classification,
            CaptureDiagnosticClassification::PermissionRequired
        );
    }

    #[test]
    fn every_backend_failure_maps_to_one_closed_capture_failure() {
        for (backend, capture) in [
            (
                BackendFailure::Unavailable,
                CaptureFailure::BackendUnavailable,
            ),
            (
                BackendFailure::PermissionDenied,
                CaptureFailure::PermissionDenied,
            ),
            (
                BackendFailure::PermissionLost,
                CaptureFailure::PermissionLost,
            ),
            (BackendFailure::Cancelled, CaptureFailure::Cancelled),
            (
                BackendFailure::PortalCancelled,
                CaptureFailure::PortalCancelled,
            ),
            (
                BackendFailure::ProtectedContent,
                CaptureFailure::ProtectedContent,
            ),
            (BackendFailure::WindowLost, CaptureFailure::WindowLost),
            (
                BackendFailure::WindowMinimized,
                CaptureFailure::WindowMinimized,
            ),
            (BackendFailure::WindowClosed, CaptureFailure::WindowClosed),
            (
                BackendFailure::DisplayRemoved,
                CaptureFailure::DisplayRemoved,
            ),
            (
                BackendFailure::ModeUnavailable,
                CaptureFailure::ModeUnavailable,
            ),
            (
                BackendFailure::DisplayChanged,
                CaptureFailure::DisplaySnapshotChanged,
            ),
            (BackendFailure::CaptureFailed, CaptureFailure::CaptureFailed),
        ] {
            assert_eq!(CaptureFailure::from(backend), capture);
        }
    }

    #[test]
    fn native_backend_is_explicitly_unavailable_until_injected() {
        let _current_platform = PlatformCaptureBackend::current();
        let backend = Arc::new(PlatformCaptureBackend::new(CapturePlatform::Macos));
        assert_eq!(
            CaptureCore::new(backend).source_catalog(),
            Err(CaptureFailure::BackendUnavailable)
        );
    }
}
