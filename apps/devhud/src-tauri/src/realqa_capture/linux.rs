use std::sync::Arc;

use super::{
    BackendFailure, BackendFrame, CaptureBackend, CaptureCapabilities, CaptureDisplayProtocol,
    CaptureMode, CaptureModeCapability, CapturePermission, CapturePlatform, CaptureSessionId,
    DisplayDescriptor, DisplaySnapshot, PointerInclusion, ResolvedCaptureRequest,
    SelectionAdjustmentAuthority, WindowSource,
};

#[cfg(feature = "linux-capture-backend")]
mod portal;
#[cfg(feature = "linux-capture-backend")]
mod x11;

trait LinuxCaptureProvider: Send + Sync {
    fn protocol(&self) -> CaptureDisplayProtocol;
    fn capabilities(&self) -> Result<CaptureCapabilities, BackendFailure>;
    fn permission(&self) -> Result<CapturePermission, BackendFailure>;
    fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure>;
    fn windows(&self, snapshot: &DisplaySnapshot) -> Result<Vec<WindowSource>, BackendFailure>;
    fn capture(&self, request: &ResolvedCaptureRequest) -> Result<BackendFrame, BackendFailure>;
    fn cancel(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure>;
}

pub(crate) struct LinuxCaptureBackend {
    provider: Arc<dyn LinuxCaptureProvider>,
}

impl LinuxCaptureBackend {
    #[cfg(feature = "linux-capture-backend")]
    pub(super) fn current() -> Result<Self, BackendFailure> {
        let protocol = detect_protocol(
            std::env::var("XDG_SESSION_TYPE").ok().as_deref(),
            std::env::var_os("WAYLAND_DISPLAY").is_some(),
            std::env::var_os("DISPLAY").is_some(),
            std::env::var("GDK_BACKEND").ok().as_deref(),
            std::env::args(),
        )
        .ok_or(BackendFailure::Unavailable)?;
        let provider: Arc<dyn LinuxCaptureProvider> = match protocol {
            CaptureDisplayProtocol::X11 | CaptureDisplayProtocol::Xwayland => {
                Arc::new(x11::X11CaptureProvider::connect(protocol)?)
            }
            CaptureDisplayProtocol::WaylandPortal => {
                Arc::new(portal::PortalCaptureProvider::new()?)
            }
            CaptureDisplayProtocol::Native => return Err(BackendFailure::Unavailable),
        };
        Ok(Self { provider })
    }

    #[cfg(not(feature = "linux-capture-backend"))]
    pub(super) fn current() -> Result<Self, BackendFailure> {
        Err(BackendFailure::Unavailable)
    }

    #[cfg(test)]
    fn with_provider(provider: Arc<dyn LinuxCaptureProvider>) -> Self {
        Self { provider }
    }
}

impl CaptureBackend for LinuxCaptureBackend {
    fn platform(&self) -> CapturePlatform {
        CapturePlatform::Linux
    }

    fn capabilities(&self) -> Result<CaptureCapabilities, BackendFailure> {
        let capabilities = self.provider.capabilities()?;
        if capabilities.platform != CapturePlatform::Linux
            || capabilities.display_protocol != self.provider.protocol()
        {
            return Err(BackendFailure::Unavailable);
        }
        Ok(capabilities)
    }

    fn permission(&self) -> Result<CapturePermission, BackendFailure> {
        self.provider.permission()
    }

    fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
        self.provider.displays()
    }

    fn windows(&self, snapshot: &DisplaySnapshot) -> Result<Vec<WindowSource>, BackendFailure> {
        self.provider.windows(snapshot)
    }

    fn capture(&self, request: &ResolvedCaptureRequest) -> Result<BackendFrame, BackendFailure> {
        self.provider.capture(request)
    }

    fn cancel(&self, session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        self.provider.cancel(session_id)
    }
}

fn direct_capabilities(
    protocol: CaptureDisplayProtocol,
    pointer_inclusion_available: bool,
) -> CaptureCapabilities {
    CaptureCapabilities {
        platform: CapturePlatform::Linux,
        display_protocol: protocol,
        modes: [
            CaptureMode::Region,
            CaptureMode::Window,
            CaptureMode::Display,
            CaptureMode::MultiMonitor,
        ]
        .into_iter()
        .map(|mode| CaptureModeCapability {
            mode,
            pointer_options: if pointer_inclusion_available {
                vec![PointerInclusion::Include, PointerInclusion::Exclude]
            } else {
                vec![PointerInclusion::Exclude]
            },
            portal_approval_required: false,
            selection_adjustment: if mode == CaptureMode::Region {
                SelectionAdjustmentAuthority::Application
            } else {
                SelectionAdjustmentAuthority::Unavailable
            },
        })
        .collect(),
    }
}

fn portal_capabilities(modes: impl IntoIterator<Item = CaptureMode>) -> CaptureCapabilities {
    CaptureCapabilities {
        platform: CapturePlatform::Linux,
        display_protocol: CaptureDisplayProtocol::WaylandPortal,
        modes: modes
            .into_iter()
            .map(|mode| CaptureModeCapability {
                mode,
                // The Screenshot portal does not define a cursor option. Cursor
                // inclusion must not be guessed from compositor-specific UI.
                pointer_options: vec![PointerInclusion::Exclude],
                portal_approval_required: true,
                selection_adjustment: if mode == CaptureMode::Region {
                    SelectionAdjustmentAuthority::Portal
                } else {
                    SelectionAdjustmentAuthority::Unavailable
                },
            })
            .collect(),
    }
}

fn detect_protocol(
    session_type: Option<&str>,
    has_wayland_display: bool,
    has_x11_display: bool,
    gdk_backend: Option<&str>,
    arguments: impl IntoIterator<Item = String>,
) -> Option<CaptureDisplayProtocol> {
    let session_type = session_type.unwrap_or_default().trim().to_ascii_lowercase();
    let gdk_backend = gdk_backend.unwrap_or_default().trim().to_ascii_lowercase();
    let ozone_platform = arguments.into_iter().find_map(|argument| {
        argument
            .strip_prefix("--ozone-platform=")
            .map(str::to_ascii_lowercase)
    });
    let preferred_gdk_backend = gdk_backend
        .split(',')
        .map(str::trim)
        .find(|backend| !backend.is_empty());

    if session_type == "wayland" || has_wayland_display {
        if has_x11_display
            && (preferred_gdk_backend == Some("x11") || ozone_platform.as_deref() == Some("x11"))
        {
            return Some(CaptureDisplayProtocol::Xwayland);
        }
        return Some(CaptureDisplayProtocol::WaylandPortal);
    }
    if session_type == "x11" || has_x11_display {
        return Some(CaptureDisplayProtocol::X11);
    }
    None
}

#[cfg(test)]
mod tests {
    use std::sync::Mutex;

    use super::*;
    use crate::realqa_capture::{
        CaptureCore, CaptureFailure, CaptureSourceSelection, DisplayId, DisplayPixelRegion,
        DisplaySnapshotId, ImageMediaType, LogicalRect, PortalApprovedLayout, SelectionGeometry,
        WindowMetadata, WindowSourceId,
        geometry::{PhysicalSize, PixelRect, ScaleFactor},
    };

    struct FixtureProvider {
        protocol: CaptureDisplayProtocol,
        capabilities: CaptureCapabilities,
        result: Mutex<Result<BackendFrame, BackendFailure>>,
    }

    impl FixtureProvider {
        fn new(protocol: CaptureDisplayProtocol) -> Self {
            let capabilities = if protocol == CaptureDisplayProtocol::WaylandPortal {
                portal_capabilities([
                    CaptureMode::Region,
                    CaptureMode::Window,
                    CaptureMode::Display,
                    CaptureMode::MultiMonitor,
                ])
            } else {
                direct_capabilities(protocol, true)
            };
            Self {
                protocol,
                capabilities,
                result: Mutex::new(Ok(BackendFrame {
                    width: 2,
                    height: 2,
                    rgba: vec![0; 16],
                    approved_layout: None,
                })),
            }
        }
    }

    impl LinuxCaptureProvider for FixtureProvider {
        fn protocol(&self) -> CaptureDisplayProtocol {
            self.protocol
        }

        fn capabilities(&self) -> Result<CaptureCapabilities, BackendFailure> {
            Ok(self.capabilities.clone())
        }

        fn permission(&self) -> Result<CapturePermission, BackendFailure> {
            Ok(CapturePermission::Granted)
        }

        fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
            Ok(vec![DisplayDescriptor {
                id: DisplayId("display-1".to_owned()),
                logical_bounds: LogicalRect {
                    x: -1.0,
                    y: -1.0,
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
                primary: true,
            }])
        }

        fn windows(
            &self,
            _snapshot: &DisplaySnapshot,
        ) -> Result<Vec<WindowSource>, BackendFailure> {
            Ok(vec![WindowSource {
                id: WindowSourceId("window-1".to_owned()),
                display_id: DisplayId("display-1".to_owned()),
                bounds: LogicalRect {
                    x: -1.0,
                    y: -1.0,
                    width: 2.0,
                    height: 2.0,
                },
                availability: super::super::WindowAvailability::Available,
                metadata: WindowMetadata::default(),
            }])
        }

        fn capture(
            &self,
            _request: &ResolvedCaptureRequest,
        ) -> Result<BackendFrame, BackendFailure> {
            self.result.lock().expect("result lock").clone()
        }

        fn cancel(&self, _session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
            Ok(())
        }
    }

    fn request(
        snapshot_id: DisplaySnapshotId,
        pointer: PointerInclusion,
    ) -> super::super::CaptureRequest {
        super::super::CaptureRequest {
            session_id: CaptureSessionId("session-1".to_owned()),
            snapshot_id: snapshot_id.clone(),
            source: CaptureSourceSelection::Region {
                selection: SelectionGeometry {
                    snapshot_id,
                    bounds: LogicalRect {
                        x: -1.0,
                        y: -1.0,
                        width: 2.0,
                        height: 2.0,
                    },
                },
            },
            pointer,
            output_media_type: ImageMediaType::Png,
        }
    }

    #[test]
    fn detects_x11_xwayland_and_native_wayland_without_ambient_fallback() {
        assert_eq!(
            detect_protocol(Some("x11"), false, true, None, Vec::new()),
            Some(CaptureDisplayProtocol::X11)
        );
        assert_eq!(
            detect_protocol(Some("wayland"), true, true, Some("x11"), Vec::new()),
            Some(CaptureDisplayProtocol::Xwayland)
        );
        assert_eq!(
            detect_protocol(Some("wayland"), true, true, Some("wayland,x11"), Vec::new()),
            Some(CaptureDisplayProtocol::WaylandPortal)
        );
        assert_eq!(
            detect_protocol(Some("wayland"), true, true, Some("x11,wayland"), Vec::new()),
            Some(CaptureDisplayProtocol::Xwayland)
        );
        assert_eq!(
            detect_protocol(
                Some("wayland"),
                true,
                true,
                None,
                vec!["--ozone-platform=wayland".to_owned()]
            ),
            Some(CaptureDisplayProtocol::WaylandPortal)
        );
        assert_eq!(detect_protocol(None, false, false, None, Vec::new()), None);
    }

    #[test]
    fn direct_protocols_expose_pointer_modes_and_application_region_adjustment() {
        for protocol in [
            CaptureDisplayProtocol::X11,
            CaptureDisplayProtocol::Xwayland,
        ] {
            let capabilities = direct_capabilities(protocol, true);
            assert_eq!(capabilities.modes.len(), 4);
            assert!(capabilities.modes.iter().all(|mode| {
                mode.pointer_options == [PointerInclusion::Include, PointerInclusion::Exclude]
                    && !mode.portal_approval_required
            }));
            assert_eq!(
                capabilities
                    .modes
                    .iter()
                    .find(|mode| mode.mode == CaptureMode::Region)
                    .expect("region capability")
                    .selection_adjustment,
                SelectionAdjustmentAuthority::Application
            );
        }
    }

    #[test]
    fn direct_protocols_hide_pointer_inclusion_when_xfixes_is_unavailable() {
        for protocol in [
            CaptureDisplayProtocol::X11,
            CaptureDisplayProtocol::Xwayland,
        ] {
            let capabilities = direct_capabilities(protocol, false);
            assert!(capabilities.modes.iter().all(|mode| {
                mode.pointer_options == [PointerInclusion::Exclude]
                    && !mode.portal_approval_required
            }));
        }
    }

    #[test]
    fn portal_protocol_requires_approval_and_rejects_unadvertised_pointer_inclusion() {
        let provider = Arc::new(FixtureProvider::new(CaptureDisplayProtocol::WaylandPortal));
        let core = CaptureCore::new(Arc::new(LinuxCaptureBackend::with_provider(
            provider.clone(),
        )));
        let catalog = core.source_catalog().expect("portal catalog");
        assert!(
            catalog
                .capabilities
                .modes
                .iter()
                .all(|mode| mode.portal_approval_required)
        );
        assert_eq!(
            core.begin(request(
                catalog.snapshot.snapshot_id.clone(),
                PointerInclusion::Include,
            )),
            Err(CaptureFailure::ModeUnavailable)
        );

        *provider.result.lock().expect("result lock") = Ok(BackendFrame {
            width: 3,
            height: 2,
            rgba: vec![7; 24],
            approved_layout: Some(PortalApprovedLayout {
                logical_bounds: LogicalRect {
                    x: 0.0,
                    y: 0.0,
                    width: 3.0,
                    height: 2.0,
                },
                pixel_regions: vec![DisplayPixelRegion {
                    display_id: DisplayId("portal-approved".to_owned()),
                    pixels: PixelRect {
                        x: 0,
                        y: 0,
                        width: 3,
                        height: 2,
                    },
                }],
            }),
        });
        let result = core
            .begin(request(
                catalog.snapshot.snapshot_id,
                PointerInclusion::Exclude,
            ))
            .expect("approved portal frame");
        assert_eq!(result.logical_bounds.width, 3.0);
        assert_eq!(result.pixel_regions[0].pixels.width, 3);
    }

    #[test]
    fn portal_failures_remain_closed_user_facing_values() {
        for (backend, expected) in [
            (
                BackendFailure::PortalCancelled,
                CaptureFailure::PortalCancelled,
            ),
            (
                BackendFailure::PermissionDenied,
                CaptureFailure::PermissionDenied,
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
            (
                BackendFailure::ModeUnavailable,
                CaptureFailure::ModeUnavailable,
            ),
        ] {
            let provider = Arc::new(FixtureProvider::new(CaptureDisplayProtocol::WaylandPortal));
            *provider.result.lock().expect("result lock") = Err(backend);
            let core = CaptureCore::new(Arc::new(LinuxCaptureBackend::with_provider(provider)));
            let catalog = core.source_catalog().expect("portal catalog");
            assert_eq!(
                core.begin(request(
                    catalog.snapshot.snapshot_id,
                    PointerInclusion::Exclude,
                )),
                Err(expected)
            );
        }
    }
}
