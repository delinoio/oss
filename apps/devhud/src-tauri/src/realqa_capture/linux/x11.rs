use std::{
    collections::{HashMap, hash_map::RandomState},
    fs,
    hash::BuildHasher,
    sync::Mutex,
};

use x11rb::{
    connection::Connection,
    image::Image,
    protocol::{
        randr::ConnectionExt as _,
        xfixes::ConnectionExt as _,
        xproto::{Atom, AtomEnum, ConnectionExt as _, Drawable, MapState, Visualtype, Window},
    },
    rust_connection::RustConnection,
};

use super::{
    super::{
        BackendFailure, BackendFrame, CaptureCapabilities, CaptureDisplayProtocol, CaptureMode,
        CapturePermission, CaptureSessionId, CaptureSourceSelection, DisplayDescriptor, DisplayId,
        DisplaySnapshot, LogicalRect, PointerInclusion, ResolvedCaptureRequest, WindowAvailability,
        WindowMetadata, WindowSource, WindowSourceId,
        geometry::{PhysicalSize, ScaleFactor},
    },
    LinuxCaptureProvider, direct_capabilities,
};

struct Atoms {
    client_list_stacking: Atom,
    client_list: Atom,
    net_wm_name: Atom,
    utf8_string: Atom,
    net_wm_pid: Atom,
    net_wm_state: Atom,
    net_wm_state_hidden: Atom,
    wm_name: Atom,
}

pub(super) struct X11CaptureProvider {
    connection: Mutex<RustConnection>,
    window_id_hasher: RandomState,
    window_sources: Mutex<HashMap<WindowSourceId, Window>>,
    screen_number: usize,
    protocol: CaptureDisplayProtocol,
    atoms: Atoms,
}

impl X11CaptureProvider {
    pub(super) fn connect(protocol: CaptureDisplayProtocol) -> Result<Self, BackendFailure> {
        if !matches!(
            protocol,
            CaptureDisplayProtocol::X11 | CaptureDisplayProtocol::Xwayland
        ) {
            return Err(BackendFailure::Unavailable);
        }
        let (connection, screen_number) =
            x11rb::connect(None).map_err(|_| BackendFailure::Unavailable)?;
        let atoms = Atoms {
            client_list_stacking: intern(&connection, b"_NET_CLIENT_LIST_STACKING")?,
            client_list: intern(&connection, b"_NET_CLIENT_LIST")?,
            net_wm_name: intern(&connection, b"_NET_WM_NAME")?,
            utf8_string: intern(&connection, b"UTF8_STRING")?,
            net_wm_pid: intern(&connection, b"_NET_WM_PID")?,
            net_wm_state: intern(&connection, b"_NET_WM_STATE")?,
            net_wm_state_hidden: intern(&connection, b"_NET_WM_STATE_HIDDEN")?,
            wm_name: AtomEnum::WM_NAME.into(),
        };
        Ok(Self {
            connection: Mutex::new(connection),
            window_id_hasher: RandomState::new(),
            window_sources: Mutex::new(HashMap::new()),
            screen_number,
            protocol,
            atoms,
        })
    }

    fn root(&self, connection: &RustConnection) -> Result<Window, BackendFailure> {
        connection
            .setup()
            .roots
            .get(self.screen_number)
            .map(|screen| screen.root)
            .ok_or(BackendFailure::Unavailable)
    }

    fn display_descriptors(
        &self,
        connection: &RustConnection,
    ) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
        let root = self.root(connection)?;
        let monitors = connection
            .randr_get_monitors(root, true)
            .map_err(|_| BackendFailure::Unavailable)?
            .reply()
            .map_err(|_| BackendFailure::Unavailable)?;
        let mut displays = Vec::with_capacity(monitors.monitors.len());
        for (index, monitor) in monitors.monitors.into_iter().enumerate() {
            if monitor.width == 0 || monitor.height == 0 {
                continue;
            }
            let atom_name = connection
                .get_atom_name(monitor.name)
                .ok()
                .and_then(|cookie| cookie.reply().ok())
                .and_then(|reply| String::from_utf8(reply.name).ok())
                .filter(|name| !name.is_empty())
                .unwrap_or_else(|| format!("monitor-{index}"));
            displays.push(DisplayDescriptor {
                id: DisplayId(format!("x11-{}", safe_identifier(&atom_name, index))),
                logical_bounds: LogicalRect {
                    x: f64::from(monitor.x),
                    y: f64::from(monitor.y),
                    width: f64::from(monitor.width),
                    height: f64::from(monitor.height),
                },
                physical_size: PhysicalSize {
                    width: u32::from(monitor.width),
                    height: u32::from(monitor.height),
                },
                // RandR coordinates and GetImage pixels share the root pixmap
                // coordinate space. Fractional compositor scaling is handled by
                // the portal path, not guessed from millimetre dimensions.
                scale: ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
                primary: monitor.primary,
            });
        }
        if displays.is_empty() {
            let screen = connection
                .setup()
                .roots
                .get(self.screen_number)
                .ok_or(BackendFailure::Unavailable)?;
            displays.push(DisplayDescriptor {
                id: DisplayId("x11-root".to_owned()),
                logical_bounds: LogicalRect {
                    x: 0.0,
                    y: 0.0,
                    width: f64::from(screen.width_in_pixels),
                    height: f64::from(screen.height_in_pixels),
                },
                physical_size: PhysicalSize {
                    width: u32::from(screen.width_in_pixels),
                    height: u32::from(screen.height_in_pixels),
                },
                scale: ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
                primary: true,
            });
        }
        Ok(displays)
    }

    fn client_windows(
        &self,
        connection: &RustConnection,
        root: Window,
    ) -> Result<Vec<Window>, BackendFailure> {
        for atom in [self.atoms.client_list_stacking, self.atoms.client_list] {
            if let Some(reply) = connection
                .get_property(false, root, atom, AtomEnum::WINDOW, 0, 16_384)
                .ok()
                .and_then(|cookie| cookie.reply().ok())
            {
                let windows = reply
                    .value32()
                    .map(Iterator::collect::<Vec<_>>)
                    .unwrap_or_default();
                if !windows.is_empty() {
                    return Ok(windows);
                }
            }
        }
        connection
            .query_tree(root)
            .map_err(|_| BackendFailure::Unavailable)?
            .reply()
            .map(|reply| reply.children)
            .map_err(|_| BackendFailure::Unavailable)
    }

    fn title(&self, connection: &RustConnection, window: Window) -> Option<String> {
        property_bytes(
            connection,
            window,
            self.atoms.net_wm_name,
            self.atoms.utf8_string,
        )
        .or_else(|| {
            property_bytes(
                connection,
                window,
                self.atoms.wm_name,
                AtomEnum::STRING.into(),
            )
        })
        .and_then(|bytes| String::from_utf8(bytes).ok())
    }

    fn process_name(&self, connection: &RustConnection, window: Window) -> Option<String> {
        let pid = connection
            .get_property(
                false,
                window,
                self.atoms.net_wm_pid,
                AtomEnum::CARDINAL,
                0,
                1,
            )
            .ok()?
            .reply()
            .ok()?
            .value32()?
            .next()?;
        fs::read_to_string(format!("/proc/{pid}/comm")).ok()
    }

    fn hidden(&self, connection: &RustConnection, window: Window) -> bool {
        connection
            .get_property(
                false,
                window,
                self.atoms.net_wm_state,
                AtomEnum::ATOM,
                0,
                64,
            )
            .ok()
            .and_then(|cookie| cookie.reply().ok())
            .and_then(|reply| {
                reply
                    .value32()
                    .map(|mut values| values.any(|atom| atom == self.atoms.net_wm_state_hidden))
            })
            .unwrap_or(false)
    }

    fn visual<'a>(&self, connection: &'a RustConnection, visual_id: u32) -> Option<&'a Visualtype> {
        connection
            .setup()
            .roots
            .iter()
            .flat_map(|screen| &screen.allowed_depths)
            .flat_map(|depth| &depth.visuals)
            .find(|visual| visual.visual_id == visual_id)
    }

    fn capture_drawable(
        &self,
        connection: &RustConnection,
        drawable: Drawable,
        bounds: LogicalRect,
        desktop_bounds: LogicalRect,
        pointer: PointerInclusion,
    ) -> Result<BackendFrame, BackendFailure> {
        let x = checked_i16(bounds.x)?;
        let y = checked_i16(bounds.y)?;
        let width = checked_u16(bounds.width)?;
        let height = checked_u16(bounds.height)?;
        let cursor = if pointer == PointerInclusion::Include {
            Some(
                connection
                    .xfixes_get_cursor_image()
                    .map_err(|_| BackendFailure::ModeUnavailable)?
                    .reply()
                    .map_err(|_| BackendFailure::ModeUnavailable)?,
            )
        } else {
            None
        };
        let (image, visual_id) = Image::get(connection, drawable, x, y, width, height)
            .map_err(|_| BackendFailure::CaptureFailed)?;
        let visual = self
            .visual(connection, visual_id)
            .ok_or(BackendFailure::CaptureFailed)?;
        let mut rgba = Vec::with_capacity(usize::from(width) * usize::from(height) * 4);
        for row in 0..height {
            for column in 0..width {
                let pixel = image.get_pixel(column, row);
                rgba.extend_from_slice(&[
                    channel(pixel, visual.red_mask),
                    channel(pixel, visual.green_mask),
                    channel(pixel, visual.blue_mask),
                    255,
                ]);
            }
        }
        if let Some(cursor) = cursor {
            composite_cursor(
                &mut rgba,
                u32::from(width),
                u32::from(height),
                desktop_bounds,
                cursor.x,
                cursor.y,
                cursor.xhot,
                cursor.yhot,
                cursor.width,
                cursor.height,
                &cursor.cursor_image,
            );
        }
        Ok(BackendFrame {
            width: u32::from(width),
            height: u32::from(height),
            rgba,
            approved_layout: None,
        })
    }
}

impl LinuxCaptureProvider for X11CaptureProvider {
    fn protocol(&self) -> CaptureDisplayProtocol {
        self.protocol
    }

    fn capabilities(&self) -> Result<CaptureCapabilities, BackendFailure> {
        let connection = self
            .connection
            .lock()
            .map_err(|_| BackendFailure::Unavailable)?;
        let pointer_inclusion_available = connection
            .xfixes_get_cursor_image()
            .ok()
            .and_then(|cookie| cookie.reply().ok())
            .is_some();
        Ok(direct_capabilities(
            self.protocol,
            pointer_inclusion_available,
        ))
    }

    fn permission(&self) -> Result<CapturePermission, BackendFailure> {
        Ok(CapturePermission::Granted)
    }

    fn displays(&self) -> Result<Vec<DisplayDescriptor>, BackendFailure> {
        let connection = self
            .connection
            .lock()
            .map_err(|_| BackendFailure::Unavailable)?;
        self.display_descriptors(&connection)
    }

    fn windows(&self, snapshot: &DisplaySnapshot) -> Result<Vec<WindowSource>, BackendFailure> {
        let connection = self
            .connection
            .lock()
            .map_err(|_| BackendFailure::Unavailable)?;
        let root = self.root(&connection)?;
        let mut windows = Vec::new();
        let mut window_sources = HashMap::new();
        for window in self.client_windows(&connection, root)? {
            let Some(attributes) = connection
                .get_window_attributes(window)
                .ok()
                .and_then(|cookie| cookie.reply().ok())
            else {
                continue;
            };
            let Some(geometry) = connection
                .get_geometry(window)
                .ok()
                .and_then(|cookie| cookie.reply().ok())
            else {
                continue;
            };
            let Some(position) = connection
                .translate_coordinates(window, root, 0, 0)
                .ok()
                .and_then(|cookie| cookie.reply().ok())
            else {
                continue;
            };
            if geometry.width == 0 || geometry.height == 0 {
                continue;
            }
            let bounds = LogicalRect {
                x: f64::from(position.dst_x),
                y: f64::from(position.dst_y),
                width: f64::from(geometry.width),
                height: f64::from(geometry.height),
            };
            let display_id = snapshot
                .displays
                .iter()
                .max_by_key(|display| intersection_area(bounds, display.logical_bounds))
                .map(|display| display.id.clone())
                .ok_or(BackendFailure::DisplayChanged)?;
            let minimized =
                attributes.map_state != MapState::VIEWABLE || self.hidden(&connection, window);
            let source_id = opaque_window_id(&self.window_id_hasher, window);
            if window_sources.insert(source_id.clone(), window).is_some() {
                return Err(BackendFailure::Unavailable);
            }
            windows.push(WindowSource {
                id: source_id,
                display_id,
                bounds: if minimized {
                    LogicalRect {
                        x: bounds.x,
                        y: bounds.y,
                        width: 0.0,
                        height: 0.0,
                    }
                } else {
                    bounds
                },
                availability: if minimized {
                    WindowAvailability::Minimized
                } else {
                    WindowAvailability::Available
                },
                metadata: WindowMetadata::bounded(
                    self.process_name(&connection, window),
                    self.title(&connection, window),
                ),
            });
        }
        *self
            .window_sources
            .lock()
            .map_err(|_| BackendFailure::Unavailable)? = window_sources;
        Ok(windows)
    }

    fn capture(&self, request: &ResolvedCaptureRequest) -> Result<BackendFrame, BackendFailure> {
        let connection = self
            .connection
            .lock()
            .map_err(|_| BackendFailure::CaptureFailed)?;
        let current = DisplaySnapshot::checked(self.display_descriptors(&connection)?)
            .map_err(|_| BackendFailure::DisplayChanged)?;
        if current.snapshot_id != request.snapshot.snapshot_id {
            return Err(BackendFailure::DisplayChanged);
        }
        let root = self.root(&connection)?;
        let (drawable, drawable_bounds) = match &request.source {
            CaptureSourceSelection::Window { window_id } => {
                let window = self
                    .window_sources
                    .lock()
                    .map_err(|_| BackendFailure::CaptureFailed)?
                    .get(window_id)
                    .copied()
                    .ok_or(BackendFailure::WindowLost)?;
                let geometry = connection
                    .get_geometry(window)
                    .map_err(|_| BackendFailure::WindowLost)?
                    .reply()
                    .map_err(|_| BackendFailure::WindowLost)?;
                if geometry.width == 0 || geometry.height == 0 {
                    return Err(BackendFailure::WindowLost);
                }
                (
                    window,
                    LogicalRect {
                        x: 0.0,
                        y: 0.0,
                        width: f64::from(geometry.width),
                        height: f64::from(geometry.height),
                    },
                )
            }
            _ => (root, request.logical_bounds),
        };
        let mut frame = self.capture_drawable(
            &connection,
            drawable,
            drawable_bounds,
            request.logical_bounds,
            request.pointer,
        )?;
        if request.mode == CaptureMode::MultiMonitor {
            blank_unselected_gaps(&mut frame, request);
        }
        if frame.width != request.expected_frame_size.width
            || frame.height != request.expected_frame_size.height
        {
            return Err(BackendFailure::CaptureFailed);
        }
        Ok(frame)
    }

    fn cancel(&self, _session_id: &CaptureSessionId) -> Result<(), BackendFailure> {
        // X11 GetImage is a bounded synchronous request. Cancellation is
        // idempotent and applies before a subsequent request starts.
        Ok(())
    }
}

fn intern(connection: &RustConnection, name: &[u8]) -> Result<Atom, BackendFailure> {
    connection
        .intern_atom(false, name)
        .map_err(|_| BackendFailure::Unavailable)?
        .reply()
        .map(|reply| reply.atom)
        .map_err(|_| BackendFailure::Unavailable)
}

fn property_bytes(
    connection: &RustConnection,
    window: Window,
    property: Atom,
    kind: Atom,
) -> Option<Vec<u8>> {
    connection
        .get_property(false, window, property, kind, 0, 1024)
        .ok()?
        .reply()
        .ok()
        .map(|reply| reply.value)
}

fn safe_identifier(name: &str, fallback: usize) -> String {
    let identifier = name
        .chars()
        .take(96)
        .map(|character| {
            if character.is_ascii_alphanumeric() || matches!(character, '-' | '_') {
                character
            } else {
                '-'
            }
        })
        .collect::<String>();
    if identifier.is_empty() {
        fallback.to_string()
    } else {
        identifier
    }
}

fn checked_i16(value: f64) -> Result<i16, BackendFailure> {
    if !value.is_finite()
        || value.fract() != 0.0
        || value < f64::from(i16::MIN)
        || value > f64::from(i16::MAX)
    {
        return Err(BackendFailure::CaptureFailed);
    }
    Ok(value as i16)
}

fn checked_u16(value: f64) -> Result<u16, BackendFailure> {
    if !value.is_finite() || value.fract() != 0.0 || value < 1.0 || value > f64::from(u16::MAX) {
        return Err(BackendFailure::CaptureFailed);
    }
    Ok(value as u16)
}

fn channel(pixel: u32, mask: u32) -> u8 {
    if mask == 0 {
        return 0;
    }
    let shift = mask.trailing_zeros();
    let maximum = mask >> shift;
    let value = (pixel & mask) >> shift;
    u8::try_from((u64::from(value) * 255 + u64::from(maximum / 2)) / u64::from(maximum))
        .unwrap_or(255)
}

#[allow(clippy::too_many_arguments)]
fn composite_cursor(
    rgba: &mut [u8],
    frame_width: u32,
    frame_height: u32,
    bounds: LogicalRect,
    cursor_x: i16,
    cursor_y: i16,
    hotspot_x: u16,
    hotspot_y: u16,
    cursor_width: u16,
    cursor_height: u16,
    cursor_pixels: &[u32],
) {
    let origin_x = i32::from(cursor_x) - i32::from(hotspot_x) - bounds.x as i32;
    let origin_y = i32::from(cursor_y) - i32::from(hotspot_y) - bounds.y as i32;
    for cursor_row in 0..u32::from(cursor_height) {
        for cursor_column in 0..u32::from(cursor_width) {
            let destination_x = origin_x + cursor_column as i32;
            let destination_y = origin_y + cursor_row as i32;
            if destination_x < 0
                || destination_y < 0
                || destination_x >= frame_width as i32
                || destination_y >= frame_height as i32
            {
                continue;
            }
            let cursor_index = usize::from(cursor_row as u16) * usize::from(cursor_width)
                + usize::from(cursor_column as u16);
            let Some(pixel) = cursor_pixels.get(cursor_index).copied() else {
                return;
            };
            let alpha = (pixel >> 24) & 0xff;
            if alpha == 0 {
                continue;
            }
            let destination_index =
                (destination_y as usize * frame_width as usize + destination_x as usize) * 4;
            for (offset, source) in [
                ((pixel >> 16) & 0xff),
                ((pixel >> 8) & 0xff),
                (pixel & 0xff),
            ]
            .into_iter()
            .enumerate()
            {
                let destination = u32::from(rgba[destination_index + offset]);
                rgba[destination_index + offset] =
                    ((source * alpha + destination * (255 - alpha)) / 255) as u8;
            }
            rgba[destination_index + 3] = 255;
        }
    }
}

fn intersection_area(left: LogicalRect, right: LogicalRect) -> u64 {
    left.intersection(right)
        .map(|intersection| (intersection.width * intersection.height) as u64)
        .unwrap_or(0)
}

fn opaque_window_id(hasher: &RandomState, window: Window) -> WindowSourceId {
    WindowSourceId(format!("x11-source-{:016x}", hasher.hash_one(window)))
}

fn blank_unselected_gaps(frame: &mut BackendFrame, request: &ResolvedCaptureRequest) {
    let CaptureSourceSelection::MultiMonitor { display_ids } = &request.source else {
        return;
    };
    for y in 0..frame.height {
        for x in 0..frame.width {
            let desktop_x = request.logical_bounds.x + f64::from(x);
            let desktop_y = request.logical_bounds.y + f64::from(y);
            let selected = display_ids.iter().any(|display_id| {
                request.snapshot.display(display_id).is_some_and(|display| {
                    desktop_x >= display.logical_bounds.x
                        && desktop_x < display.logical_bounds.right()
                        && desktop_y >= display.logical_bounds.y
                        && desktop_y < display.logical_bounds.bottom()
                })
            });
            if !selected {
                let index = (y as usize * frame.width as usize + x as usize) * 4;
                frame.rgba[index..index + 4].copy_from_slice(&[0, 0, 0, 0]);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn channel_masks_expand_to_full_rgba_range() {
        assert_eq!(channel(0x00ff_0000, 0x00ff_0000), 255);
        assert_eq!(channel(0x0000_7c00, 0x0000_7c00), 255);
        assert_eq!(channel(0, 0x00ff_0000), 0);
    }

    #[test]
    fn cursor_composition_honours_hotspot_and_negative_capture_origin() {
        let mut rgba = vec![0; 4 * 4 * 4];
        composite_cursor(
            &mut rgba,
            4,
            4,
            LogicalRect {
                x: -2.0,
                y: -2.0,
                width: 4.0,
                height: 4.0,
            },
            0,
            0,
            1,
            1,
            1,
            1,
            &[0xffff_0000],
        );
        let index = 5 * 4;
        assert_eq!(&rgba[index..index + 4], &[255, 0, 0, 255]);
    }

    #[test]
    fn metadata_identifiers_are_bounded_and_value_only() {
        assert_eq!(safe_identifier("DP-1 / unsafe", 0), "DP-1---unsafe");
        let source_id = opaque_window_id(&RandomState::new(), 42);
        assert!(source_id.0.starts_with("x11-source-"));
        assert!(!source_id.0.contains("0000002a"));
        let metadata = WindowMetadata::bounded(
            Some(" process\nignored".to_owned()),
            Some("title".to_owned()),
        );
        assert_eq!(metadata.process_name, Some("process".to_owned()));
        assert_eq!(metadata.title, Some("title".to_owned()));
    }
}
