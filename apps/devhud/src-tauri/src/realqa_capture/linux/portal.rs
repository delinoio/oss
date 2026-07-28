use std::{
    collections::{HashMap, hash_map::Entry},
    fs,
    io::Read,
    sync::Mutex,
    time::Duration,
};

use ashpd::{
    Error as PortalError, PortalError as PortalMethodError,
    desktop::{
        ResponseError,
        screenshot::{AvailableTargets, Screenshot, ScreenshotProxy},
    },
};

use super::{
    super::{
        BackendFailure, BackendFrame, CaptureCapabilities, CaptureMode, CapturePermission,
        CaptureSessionId, CaptureSourceSelection, DecodedImage, DisplayDescriptor, DisplayId,
        DisplayPixelRegion, DisplaySnapshot, EncodedImage, ImageMediaType, LogicalRect,
        PortalApprovedLayout, ResolvedCaptureRequest, WindowAvailability, WindowMetadata,
        WindowSource, WindowSourceId, decode_image,
        geometry::{PhysicalSize, PixelRect, ScaleFactor},
        image_boundary::MAX_ENCODED_IMAGE_BYTES,
    },
    LinuxCaptureProvider, portal_capabilities,
};

const PORTAL_DISPLAY_ID: &str = "portal-display-picker";
const PORTAL_WINDOW_ID: &str = "portal-window-picker";
const PORTAL_APPROVED_ID: &str = "portal-approved-source";

pub(super) struct PortalCaptureProvider {
    sessions: Mutex<HashMap<CaptureSessionId, bool>>,
}

impl PortalCaptureProvider {
    pub(super) fn new() -> Result<Self, BackendFailure> {
        Ok(Self {
            sessions: Mutex::new(HashMap::new()),
        })
    }

    fn run<T>(
        future: impl std::future::Future<Output = Result<T, BackendFailure>>,
    ) -> Result<T, BackendFailure> {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .map_err(|_| BackendFailure::Unavailable)?
            .block_on(future)
    }

    async fn inspect_modes() -> Result<Vec<CaptureMode>, BackendFailure> {
        let proxy = ScreenshotProxy::new().await.map_err(map_portal_error)?;
        if !portal_supports_target_selection(proxy.version()) {
            return Err(BackendFailure::ModeUnavailable);
        }
        let targets = proxy.available_targets().await.map_err(map_portal_error)?;
        let modes = modes_for_targets(
            targets.contains(AvailableTargets::Area),
            targets.contains(AvailableTargets::Window),
            targets.contains(AvailableTargets::ActiveWindow),
            targets.contains(AvailableTargets::Screen),
        );
        if modes.is_empty() {
            return Err(BackendFailure::ModeUnavailable);
        }
        Ok(modes)
    }

    async fn capture_portal(
        request: &ResolvedCaptureRequest,
    ) -> Result<BackendFrame, BackendFailure> {
        // A request-owned D-Bus connection makes dropping the future on
        // application cancellation revoke that request's portal authority.
        let connection = ashpd::zbus::Connection::session()
            .await
            .map_err(|_| BackendFailure::Unavailable)?;
        let proxy = ScreenshotProxy::with_connection(connection.clone())
            .await
            .map_err(map_portal_error)?;
        let mut builder = Screenshot::request()
            .connection(Some(connection))
            .interactive(true)
            .modal(true);
        if !portal_supports_target_selection(proxy.version()) {
            return Err(BackendFailure::ModeUnavailable);
        }
        let available = proxy.available_targets().await.map_err(map_portal_error)?;
        let target = match request.mode {
            CaptureMode::Region if available.contains(AvailableTargets::Area) => {
                AvailableTargets::Area
            }
            CaptureMode::Window if available.contains(AvailableTargets::Window) => {
                AvailableTargets::Window
            }
            CaptureMode::MultiMonitor if available.contains(AvailableTargets::Screen) => {
                AvailableTargets::Screen
            }
            _ => return Err(BackendFailure::ModeUnavailable),
        };
        builder = builder.target(target);
        let response = builder
            .send()
            .await
            .map_err(map_portal_error)?
            .response()
            .map_err(map_portal_error)?;
        let uri =
            url::Url::parse(response.uri().as_str()).map_err(|_| BackendFailure::CaptureFailed)?;
        let path = uri
            .to_file_path()
            .map_err(|()| BackendFailure::CaptureFailed)?;
        let file = fs::File::open(path).map_err(|_| BackendFailure::CaptureFailed)?;
        let bytes = read_bounded(file, MAX_ENCODED_IMAGE_BYTES)?;
        let DecodedImage {
            width,
            height,
            rgba,
        } = decode_image(&EncodedImage {
            media_type: ImageMediaType::Png,
            bytes,
        })
        .map_err(|_| BackendFailure::CaptureFailed)?;
        Ok(BackendFrame {
            width,
            height,
            rgba,
            approved_layout: Some(PortalApprovedLayout {
                logical_bounds: LogicalRect {
                    x: 0.0,
                    y: 0.0,
                    width: f64::from(width),
                    height: f64::from(height),
                },
                pixel_regions: vec![DisplayPixelRegion {
                    display_id: DisplayId(PORTAL_APPROVED_ID.to_owned()),
                    pixels: PixelRect {
                        x: 0,
                        y: 0,
                        width,
                        height,
                    },
                }],
            }),
        })
    }

    async fn capture_with_cancellation(
        &self,
        request: &ResolvedCaptureRequest,
    ) -> Result<BackendFrame, BackendFailure> {
        let capture = Self::capture_portal(request);
        tokio::pin!(capture);
        loop {
            match tokio::time::timeout(Duration::from_millis(25), capture.as_mut()).await {
                Ok(result) => return result,
                Err(_) if self.session_cancelled(&request.session_id)? => {
                    return Err(BackendFailure::Cancelled);
                }
                Err(_) => {}
            }
        }
    }

    fn session_cancelled(&self, session_id: &CaptureSessionId) -> Result<bool, BackendFailure> {
        self.sessions
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?
            .get(session_id)
            .copied()
            .ok_or(BackendFailure::Cancelled)
    }

    fn register_session(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        let mut sessions = self
            .sessions
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?;
        match sessions.entry(session_id.clone()) {
            Entry::Vacant(entry) => {
                entry.insert(false);
                Ok(())
            }
            Entry::Occupied(entry) if *entry.get() => {
                entry.remove();
                Err(BackendFailure::Cancelled)
            }
            Entry::Occupied(_) => Err(BackendFailure::CaptureFailed),
        }
    }
}

impl LinuxCaptureProvider for PortalCaptureProvider {
    fn protocol(&self) -> super::super::CaptureDisplayProtocol {
        super::super::CaptureDisplayProtocol::WaylandPortal
    }

    fn capabilities(&self) -> Result<CaptureCapabilities, BackendFailure> {
        Self::run(async { Ok(portal_capabilities(Self::inspect_modes().await?)) })
    }

    fn permission(&self) -> Result<CapturePermission, BackendFailure> {
        // The portal's per-request dialog is the permission boundary. Merely
        // connecting to inspect properties grants no screen authority.
        Self::run(async {
            ScreenshotProxy::new()
                .await
                .map_err(map_portal_error)
                .map(|_| CapturePermission::Granted)
        })
    }

    fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
        Ok(vec![DisplayDescriptor {
            id: DisplayId(PORTAL_DISPLAY_ID.to_owned()),
            logical_bounds: LogicalRect {
                x: 0.0,
                y: 0.0,
                width: 1.0,
                height: 1.0,
            },
            physical_size: PhysicalSize {
                width: 1,
                height: 1,
            },
            scale: ScaleFactor {
                numerator: 1,
                denominator: 1,
            },
            primary: true,
        }])
    }

    fn windows(&self, _snapshot: &DisplaySnapshot) -> Result<Vec<WindowSource>, BackendFailure> {
        let capabilities = self.capabilities()?;
        if capabilities
            .modes
            .iter()
            .all(|capability| capability.mode != CaptureMode::Window)
        {
            return Ok(Vec::new());
        }
        // Native Wayland deliberately exposes no compositor window list or
        // process metadata. This opaque picker entry transfers selection to
        // the portal after the user's explicit capture action.
        Ok(vec![WindowSource {
            id: WindowSourceId(PORTAL_WINDOW_ID.to_owned()),
            display_id: DisplayId(PORTAL_DISPLAY_ID.to_owned()),
            bounds: LogicalRect {
                x: 0.0,
                y: 0.0,
                width: 1.0,
                height: 1.0,
            },
            availability: WindowAvailability::Available,
            metadata: WindowMetadata::default(),
        }])
    }

    fn capture(&self, request: &ResolvedCaptureRequest) -> Result<BackendFrame, BackendFailure> {
        if request.pointer != super::super::PointerInclusion::Exclude
            || matches!(
                &request.source,
                CaptureSourceSelection::Window { window_id }
                    if window_id.0 != PORTAL_WINDOW_ID
            )
        {
            return Err(BackendFailure::ModeUnavailable);
        }
        self.register_session(&request.session_id)?;
        let result = Self::run(self.capture_with_cancellation(request));
        let cancelled = self.session_cancelled(&request.session_id)?;
        self.sessions
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?
            .remove(&request.session_id);
        if cancelled {
            Err(BackendFailure::Cancelled)
        } else {
            result
        }
    }

    fn cancel(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        self.sessions
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?
            .entry(session_id.clone())
            .and_modify(|cancelled| *cancelled = true)
            .or_insert(true);
        Ok(())
    }
}

fn read_bounded(reader: impl Read, limit: u64) -> Result<Vec<u8>, BackendFailure> {
    let read_limit = limit.checked_add(1).ok_or(BackendFailure::CaptureFailed)?;
    let mut bytes = Vec::new();
    reader
        .take(read_limit)
        .read_to_end(&mut bytes)
        .map_err(|_| BackendFailure::CaptureFailed)?;
    let encoded_len = u64::try_from(bytes.len()).map_err(|_| BackendFailure::CaptureFailed)?;
    if encoded_len == 0 || encoded_len > limit {
        return Err(BackendFailure::CaptureFailed);
    }
    Ok(bytes)
}

fn portal_supports_target_selection(version: u32) -> bool {
    version >= 3
}

fn modes_for_targets(
    area_available: bool,
    window_picker_available: bool,
    _active_window_available: bool,
    whole_screen_available: bool,
) -> Vec<CaptureMode> {
    // Active-window capture does not transfer source choice to the user.
    let mut modes = Vec::new();
    if area_available {
        modes.push(CaptureMode::Region);
    }
    if window_picker_available {
        modes.push(CaptureMode::Window);
    }
    if whole_screen_available {
        modes.push(CaptureMode::MultiMonitor);
    }
    modes
}

fn map_portal_error(error: PortalError) -> BackendFailure {
    match error {
        PortalError::Response(ResponseError::Cancelled) => BackendFailure::PortalCancelled,
        PortalError::Response(ResponseError::Other) => BackendFailure::PermissionDenied,
        PortalError::Portal(PortalMethodError::Cancelled(_)) => BackendFailure::PortalCancelled,
        PortalError::Portal(PortalMethodError::NotAllowed(_)) => BackendFailure::PermissionDenied,
        PortalError::Portal(PortalMethodError::WindowDestroyed(_)) => BackendFailure::WindowLost,
        PortalError::Portal(PortalMethodError::NotFound(_)) => BackendFailure::ModeUnavailable,
        PortalError::RequiresVersion(_, _) => BackendFailure::ModeUnavailable,
        _ => BackendFailure::Unavailable,
    }
}

#[cfg(test)]
mod tests {
    use std::io::Cursor;

    use super::*;

    #[test]
    fn portal_file_reads_are_bounded() {
        assert_eq!(
            read_bounded(Cursor::new([1, 2, 3, 4]), 4),
            Ok(vec![1, 2, 3, 4])
        );
        assert_eq!(
            read_bounded(Cursor::new([1, 2, 3, 4, 5]), 4),
            Err(BackendFailure::CaptureFailed)
        );
        assert_eq!(
            read_bounded(Cursor::new(Vec::<u8>::new()), 4),
            Err(BackendFailure::CaptureFailed)
        );
    }

    #[test]
    fn target_modes_preserve_portal_selection_semantics() {
        assert!(!portal_supports_target_selection(1));
        assert!(!portal_supports_target_selection(2));
        assert!(portal_supports_target_selection(3));
        assert_eq!(
            modes_for_targets(true, true, true, true),
            [
                CaptureMode::Region,
                CaptureMode::Window,
                CaptureMode::MultiMonitor
            ]
        );
        assert_eq!(
            modes_for_targets(false, false, false, true),
            [CaptureMode::MultiMonitor]
        );
        assert!(modes_for_targets(false, false, true, false).is_empty());
    }

    #[test]
    fn application_cancellation_is_preserved_until_portal_request_registration() {
        let provider = PortalCaptureProvider::new().expect("portal provider");
        let active = CaptureSessionId("active".to_owned());
        let early = CaptureSessionId("early".to_owned());
        provider
            .register_session(&active)
            .expect("register active request");

        provider.cancel(&active).expect("cancel active request");
        provider.cancel(&early).expect("cancel pending request");

        assert!(provider.session_cancelled(&active).expect("active request"));
        assert_eq!(
            provider.register_session(&early),
            Err(BackendFailure::Cancelled)
        );
        assert_eq!(
            provider.session_cancelled(&early),
            Err(BackendFailure::Cancelled)
        );
    }
}
