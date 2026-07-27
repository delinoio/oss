mod composer;
mod geometry;
mod image_boundary;

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
    PermissionDenied,
    PermissionLost,
    Cancelled,
    PortalCancelled,
    ProtectedContent,
    WindowLost,
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
    WindowLost,
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
            BackendFailure::WindowLost => Self::WindowLost,
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
}

impl WindowSource {
    fn checked(self, snapshot: &DisplaySnapshot) -> Result<Self, CaptureFailure> {
        if self.id.0.is_empty()
            || self.id.0.len() > 128
            || snapshot.display(&self.display_id).is_none()
        {
            return Err(CaptureFailure::InvalidDisplaySnapshot);
        }
        self.bounds.checked()?;
        Ok(self)
    }
}

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct CaptureSourceCatalog {
    pub(crate) platform: CapturePlatform,
    pub(crate) permission: CapturePermission,
    pub(crate) snapshot: DisplaySnapshot,
    pub(crate) windows: Vec<WindowSource>,
}

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(tag = "mode", rename_all = "kebab-case")]
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
pub(crate) struct ResolvedCaptureRequest {
    pub(crate) session_id: CaptureSessionId,
    pub(crate) snapshot: DisplaySnapshot,
    pub(crate) source: CaptureSourceSelection,
    pub(crate) mode: CaptureMode,
    pub(crate) pointer: PointerInclusion,
    pub(crate) output_media_type: ImageMediaType,
    pub(crate) logical_bounds: LogicalRect,
    pub(crate) pixel_regions: Vec<DisplayPixelRegion>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct BackendFrame {
    pub(crate) width: u32,
    pub(crate) height: u32,
    pub(crate) rgba: Vec<u8>,
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
    fn permission(&self) -> Result<CapturePermission, BackendFailure>;
    fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure>;
    fn windows(&self, snapshot: &DisplaySnapshot) -> Result<Vec<WindowSource>, BackendFailure>;
    fn capture(&self, request: &ResolvedCaptureRequest) -> Result<BackendFrame, BackendFailure>;
    fn cancel(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure>;
}

pub(crate) struct CaptureCore {
    backend: Arc<dyn CaptureBackend>,
}

impl CaptureCore {
    pub(crate) fn new(backend: Arc<dyn CaptureBackend>) -> Self {
        Self { backend }
    }

    pub(crate) fn source_catalog(&self) -> Result<CaptureSourceCatalog, CaptureFailure> {
        let snapshot = self.current_snapshot()?;
        let mut windows = self
            .backend
            .windows(&snapshot)
            .map_err(CaptureFailure::from)?
            .into_iter()
            .map(|window| window.checked(&snapshot))
            .collect::<Result<Vec<_>, _>>()?;
        windows.sort_by(|left, right| left.id.0.cmp(&right.id.0));
        if windows.windows(2).any(|pair| pair[0].id == pair[1].id) {
            return Err(CaptureFailure::InvalidDisplaySnapshot);
        }
        Ok(CaptureSourceCatalog {
            platform: self.backend.platform(),
            permission: self.backend.permission().map_err(CaptureFailure::from)?,
            snapshot,
            windows,
        })
    }

    pub(crate) fn adjust_selection(
        &self,
        selection: &SelectionGeometry,
        adjustment: SelectionAdjustment,
    ) -> Result<SelectionGeometry, CaptureFailure> {
        let snapshot = self.current_snapshot()?;
        adjust_selection(&snapshot, selection, adjustment)
    }

    pub(crate) fn begin(&self, request: CaptureRequest) -> Result<CaptureResult, CaptureFailure> {
        if request.session_id.0.is_empty() || request.session_id.0.len() > 128 {
            return Err(CaptureFailure::InvalidSelection);
        }
        let snapshot = self.current_snapshot()?;
        if snapshot.snapshot_id != request.snapshot_id {
            return Err(CaptureFailure::DisplaySnapshotChanged);
        }
        match self.backend.permission().map_err(CaptureFailure::from)? {
            CapturePermission::Denied => return Err(CaptureFailure::PermissionDenied),
            CapturePermission::Granted | CapturePermission::PromptRequired => {}
        }
        let resolved = self.resolve(request, snapshot)?;
        let frame = self
            .backend
            .capture(&resolved)
            .map_err(CaptureFailure::from)?;
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
            logical_bounds: resolved.logical_bounds,
            pixel_regions: resolved.pixel_regions,
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
        DisplaySnapshot::checked(self.backend.displays().map_err(CaptureFailure::from)?)
    }

    fn resolve(
        &self,
        request: CaptureRequest,
        snapshot: DisplaySnapshot,
    ) -> Result<ResolvedCaptureRequest, CaptureFailure> {
        let logical_bounds = match &request.source {
            CaptureSourceSelection::Region { selection } => {
                if selection.snapshot_id != snapshot.snapshot_id {
                    return Err(CaptureFailure::DisplaySnapshotChanged);
                }
                selection.bounds.checked()?
            }
            CaptureSourceSelection::Window { window_id } => {
                let window = self
                    .backend
                    .windows(&snapshot)
                    .map_err(CaptureFailure::from)?
                    .into_iter()
                    .find(|window| &window.id == window_id)
                    .ok_or(CaptureFailure::WindowLost)?;
                if window.availability == WindowAvailability::Minimized {
                    return Err(CaptureFailure::WindowLost);
                }
                window.checked(&snapshot)?.bounds
            }
            CaptureSourceSelection::Display { display_id } => snapshot
                .display(display_id)
                .map(|display| display.logical_bounds)
                .ok_or(CaptureFailure::DisplaySnapshotChanged)?,
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
                .ok_or(CaptureFailure::InvalidSelection)?
            }
        };
        let pixel_regions = snapshot.pixel_regions(logical_bounds)?;
        let mode = request.source.mode();
        Ok(ResolvedCaptureRequest {
            session_id: request.session_id,
            snapshot,
            source: request.source,
            mode,
            pointer: request.pointer,
            output_media_type: request.output_media_type,
            logical_bounds,
            pixel_regions,
        })
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
    PermissionDenied,
    PermissionLost,
    Cancelled,
    PortalCancelled,
    ProtectedContent,
    WindowLost,
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
            CaptureFailure::PermissionDenied => CaptureDiagnosticClassification::PermissionDenied,
            CaptureFailure::PermissionLost => CaptureDiagnosticClassification::PermissionLost,
            CaptureFailure::Cancelled => CaptureDiagnosticClassification::Cancelled,
            CaptureFailure::PortalCancelled => CaptureDiagnosticClassification::PortalCancelled,
            CaptureFailure::ProtectedContent => CaptureDiagnosticClassification::ProtectedContent,
            CaptureFailure::WindowLost => CaptureDiagnosticClassification::WindowLost,
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
}

impl PlatformCaptureBackend {
    pub(crate) const fn new(platform: CapturePlatform) -> Self {
        Self { platform }
    }

    #[cfg(not(any(target_os = "android", target_os = "ios")))]
    pub(crate) const fn current() -> Self {
        let platform = if cfg!(target_os = "macos") {
            CapturePlatform::Macos
        } else if cfg!(target_os = "windows") {
            CapturePlatform::Windows
        } else {
            CapturePlatform::Linux
        };
        Self::new(platform)
    }
}

impl CaptureBackend for PlatformCaptureBackend {
    fn platform(&self) -> CapturePlatform {
        self.platform
    }

    fn permission(&self) -> Result<CapturePermission, BackendFailure> {
        Err(BackendFailure::Unavailable)
    }

    fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
        Err(BackendFailure::Unavailable)
    }

    fn windows(&self, _snapshot: &DisplaySnapshot) -> Result<Vec<WindowSource>, BackendFailure> {
        Err(BackendFailure::Unavailable)
    }

    fn capture(&self, _request: &ResolvedCaptureRequest) -> Result<BackendFrame, BackendFailure> {
        Err(BackendFailure::Unavailable)
    }

    fn cancel(&self, _session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        Err(BackendFailure::Unavailable)
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Mutex;

    use geometry::{PhysicalSize, ScaleFactor};

    use super::*;

    struct FixtureBackend {
        platform: CapturePlatform,
        permission: Mutex<Result<CapturePermission, BackendFailure>>,
        displays: Mutex<Vec<DisplayDescriptor>>,
        windows: Mutex<Vec<WindowSource>>,
        capture_result: Mutex<Result<BackendFrame, BackendFailure>>,
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
                }]),
                capture_result: Mutex::new(Ok(BackendFrame {
                    width: 1,
                    height: 1,
                    rgba: vec![0, 1, 2, 255],
                })),
                last_request: Mutex::new(None),
            }
        }
    }

    impl CaptureBackend for FixtureBackend {
        fn platform(&self) -> CapturePlatform {
            self.platform
        }

        fn permission(&self) -> Result<CapturePermission, BackendFailure> {
            *self.permission.lock().expect("permission lock")
        }

        fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
            Ok(self.displays.lock().expect("display lock").clone())
        }

        fn windows(
            &self,
            _snapshot: &DisplaySnapshot,
        ) -> Result<Vec<WindowSource>, BackendFailure> {
            Ok(self.windows.lock().expect("window lock").clone())
        }

        fn capture(
            &self,
            request: &ResolvedCaptureRequest,
        ) -> Result<BackendFrame, BackendFailure> {
            *self.last_request.lock().expect("request lock") = Some(request.clone());
            self.capture_result.lock().expect("capture lock").clone()
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
            assert_eq!(
                decode_image(&result.image)
                    .expect("result image must decode")
                    .rgba,
                vec![0, 1, 2, 255]
            );
            let backend_request = backend
                .last_request
                .lock()
                .expect("request lock")
                .clone()
                .expect("backend must receive request");
            assert_eq!(backend_request.pixel_regions[0].pixels.x, 150);
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
        assert_eq!(core.begin(window_request), Err(CaptureFailure::WindowLost));
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
