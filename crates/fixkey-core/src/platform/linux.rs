//! X11 hardware-key observation and AT-SPI editable-text transactions.
//!
//! XTEST devices and `SendEvent` input are never accepted as typed input. Only
//! focused, enabled, non-password Text/Entry objects exposing `EditableText`
//! and Text are read. A bounded tree search reads states and interfaces, not
//! labels or document text. Wayland is deliberately unsupported.

use std::{
    collections::HashSet,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

use x11rb::{
    connection::Connection,
    protocol::{
        Event,
        xinput::{ConnectionExt as _, DeviceType, EventMask, XIEventMask, XIGetPropertyItems},
        xproto::{AtomEnum, ConnectionExt as _},
    },
    rust_connection::RustConnection,
};
use zbus::{
    blocking::{Connection as Bus, Proxy, connection::Builder},
    zvariant::OwnedObjectPath,
};

use super::{Application, Cursor, Gate, Handler, Identity, Key, KeyEvent};
use crate::{
    SkipReason,
    session::{Accessibility, FieldSensitivity, FieldSnapshot, TargetId, WriteError},
};

type Object = (String, OwnedObjectPath);
const ACCESSIBLE: &str = "org.a11y.atspi.Accessible";
const TEXT: &str = "org.a11y.atspi.Text";
const EDITABLE: &str = "org.a11y.atspi.EditableText";
const ROOT: &str = "/org/a11y/atspi/accessible/root";

pub struct Adapter {
    allowed: Option<Identity>,
    bus: Option<Bus>,
    x: Option<RustConnection>,
    last: Option<(Object, FieldSnapshot)>,
}
impl Default for Adapter {
    fn default() -> Self {
        Self::new()
    }
}
fn bus() -> Result<Bus, SkipReason> {
    if std::env::var("DBUS_SESSION_BUS_ADDRESS").is_ok_and(|address| {
        !address
            .split(';')
            .all(|address| address.starts_with("unix:"))
    }) {
        return Err(SkipReason::PermissionMissing);
    }
    let session = Builder::session()
        .map_err(|_| SkipReason::PermissionMissing)?
        .method_timeout(Duration::from_millis(50))
        .build()
        .map_err(|_| SkipReason::PermissionMissing)?;
    let address: String = Proxy::new(&session, "org.a11y.Bus", "/org/a11y/bus", "org.a11y.Bus")
        .map_err(|_| SkipReason::PermissionMissing)?
        .call("GetAddress", &())
        .map_err(|_| SkipReason::PermissionMissing)?;
    if !address
        .split(';')
        .all(|address| address.starts_with("unix:"))
    {
        return Err(SkipReason::PermissionMissing);
    }
    Builder::address(address.as_str())
        .map_err(|_| SkipReason::PermissionMissing)?
        .method_timeout(Duration::from_millis(50))
        .build()
        .map_err(|_| SkipReason::PermissionMissing)
}
fn proxy<'a>(
    bus: &'a Bus,
    object: &'a Object,
    interface: &'a str,
) -> Result<Proxy<'a>, SkipReason> {
    Proxy::new(bus, object.0.as_str(), object.1.as_str(), interface)
        .map_err(|_| SkipReason::UnknownField)
}
fn atom(x: &RustConnection, name: &[u8]) -> Result<u32, SkipReason> {
    x.intern_atom(false, name)
        .map_err(|_| SkipReason::UnknownField)?
        .reply()
        .map(|reply| reply.atom)
        .map_err(|_| SkipReason::UnknownField)
}
fn property(
    x: &RustConnection,
    window: u32,
    name: &[u8],
    kind: AtomEnum,
) -> Result<Vec<u8>, SkipReason> {
    x.get_property(false, window, atom(x, name)?, kind, 0, 4096)
        .map_err(|_| SkipReason::UnknownField)?
        .reply()
        .map(|reply| reply.value)
        .map_err(|_| SkipReason::UnknownField)
}
fn word(bytes: &[u8]) -> Result<u32, SkipReason> {
    bytes
        .get(..4)
        .and_then(|bytes| bytes.try_into().ok())
        .map(u32::from_ne_bytes)
        .ok_or(SkipReason::UnknownField)
}
fn application(x: &RustConnection, window: u32) -> Result<(u32, Application), SkipReason> {
    let pid = word(&property(x, window, b"_NET_WM_PID", AtomEnum::CARDINAL)?)?;
    let class = property(x, window, b"WM_CLASS", AtomEnum::STRING)?;
    let class = std::str::from_utf8(&class)
        .map_err(|_| SkipReason::UnknownField)?
        .split('\0')
        .nth(1)
        .filter(|class| !class.is_empty())
        .ok_or(SkipReason::UnknownField)?
        .to_owned();
    let hints: Vec<_> = [
        b"_GTK_APPLICATION_ID".as_slice(),
        b"_KDE_NET_WM_DESKTOP_FILE".as_slice(),
    ]
    .into_iter()
    .filter_map(|name| property(x, window, name, AtomEnum::ANY).ok())
    .filter_map(|value| String::from_utf8(value).ok())
    .map(|value| value.trim_end_matches('\0').to_owned())
    .collect();
    let desktop_entry_id = super::linux_identity::resolve(&hints, &desktop_entry_roots());
    // Explicit application hints must resolve to an installed desktop entry.
    // WM_CLASS remains the fallback when no unique entry can be established.
    Ok((
        pid,
        Application {
            name: class.clone(),
            identity: Identity::Linux {
                class,
                desktop_entry_id,
            },
        },
    ))
}
fn desktop_entry_roots() -> Vec<std::path::PathBuf> {
    let mut roots = Vec::new();
    let data_home = std::env::var_os("XDG_DATA_HOME")
        .map(std::path::PathBuf::from)
        .filter(|path| path.is_absolute())
        .or_else(|| {
            std::env::var_os("HOME").map(|home| std::path::PathBuf::from(home).join(".local/share"))
        });
    if let Some(home) = data_home.filter(|path| path.is_absolute()) {
        roots.push(home.join("applications"));
    }
    let directories = std::env::var_os("XDG_DATA_DIRS")
        .filter(|value| !value.is_empty())
        .unwrap_or_else(|| "/usr/local/share:/usr/share".into());
    roots.extend(
        std::env::split_paths(&directories)
            .filter(|path| path.is_absolute())
            .map(|path| path.join("applications")),
    );
    roots
}

fn active(x: &RustConnection) -> Result<(u32, Application), SkipReason> {
    let root = x
        .setup()
        .roots
        .first()
        .ok_or(SkipReason::UnknownField)?
        .root;
    let window = word(&property(x, root, b"_NET_ACTIVE_WINDOW", AtomEnum::WINDOW)?)?;
    application(x, window)
}
fn target(object: &Object) -> TargetId {
    super::identity_token(format!("{}:{}", object.0, object.1))
}

fn state(bus: &Bus, object: &Object) -> Result<Vec<u32>, SkipReason> {
    proxy(bus, object, ACCESSIBLE)?
        .call("GetState", &())
        .map_err(|_| SkipReason::UnknownField)
}
fn has(states: &[u32], bit: usize) -> bool {
    states
        .get(bit / 32)
        .is_some_and(|word| word & (1 << (bit % 32)) != 0)
}
fn eligible(bus: &Bus, object: &Object) -> Result<(), SkipReason> {
    let accessible = proxy(bus, object, ACCESSIBLE)?;
    let role: u32 = accessible
        .call("GetRole", &())
        .map_err(|_| SkipReason::UnknownField)?;
    if role == 40 {
        return Err(SkipReason::ProtectedField);
    }
    if !matches!(role, 61 | 79) {
        return Err(SkipReason::UnknownField);
    }
    let states = state(bus, object)?;
    if ![7, 8, 12, 24, 25].iter().all(|bit| has(&states, *bit))
        || has(&states, 6)
        || has(&states, 27)
    {
        return Err(SkipReason::UnknownField);
    }
    let interfaces: Vec<String> = accessible
        .call("GetInterfaces", &())
        .map_err(|_| SkipReason::UnsupportedWrite)?;
    if !interfaces.iter().any(|interface| interface == TEXT)
        || !interfaces.iter().any(|interface| interface == EDITABLE)
    {
        return Err(SkipReason::UnsupportedWrite);
    }
    Ok(())
}
impl Adapter {
    #[must_use]
    pub fn new() -> Self {
        if !Self::permission() {
            return Self {
                allowed: None,
                bus: None,
                x: None,
                last: None,
            };
        }
        Self {
            allowed: None,
            bus: bus().ok(),
            x: x11rb::connect(None).ok().map(|(x, _)| x),
            last: None,
        }
    }

    pub fn clear(&mut self) {
        self.last = None;
        self.allowed = None;
    }

    /// Restricts future text reads to the application approved by the caller.
    pub fn set_application(&mut self, identity: &Identity) {
        self.allowed = Some(identity.clone());
    }

    #[must_use]
    pub fn permission() -> bool {
        std::env::var("XDG_SESSION_TYPE").is_ok_and(|kind| kind == "x11")
            && std::env::var("DISPLAY")
                .is_ok_and(|display| display.starts_with(':') || display.starts_with("unix:"))
            && std::env::var_os("WAYLAND_DISPLAY").is_none()
    }

    fn focus(&self) -> Result<(Object, Application), SkipReason> {
        let bus = self.bus.as_ref().ok_or(SkipReason::PermissionMissing)?;
        let (pid, application) = active(self.x.as_ref().ok_or(SkipReason::PermissionMissing)?)?;
        if pid == std::process::id() {
            return Err(SkipReason::UnknownField);
        }
        let desktop = (
            "org.a11y.atspi.Registry".into(),
            OwnedObjectPath::try_from(ROOT).map_err(|_| SkipReason::UnknownField)?,
        );
        let apps: Vec<Object> = proxy(bus, &desktop, ACCESSIBLE)?
            .call("GetChildren", &())
            .map_err(|_| SkipReason::UnknownField)?;
        let dbus = Proxy::new(
            bus,
            "org.freedesktop.DBus",
            "/org/freedesktop/DBus",
            "org.freedesktop.DBus",
        )
        .map_err(|_| SkipReason::UnknownField)?;
        let mut pending = Vec::new();
        for app in apps {
            let owner: Result<u32, _> = dbus.call("GetConnectionUnixProcessID", &(app.0.as_str(),));
            if owner.is_ok_and(|owner| owner == pid) {
                pending.push(app);
            }
        }
        let deadline = Instant::now() + Duration::from_millis(100);
        let mut seen = HashSet::new();
        while let Some(object) = pending.pop() {
            if Instant::now() > deadline || seen.len() >= 4096 {
                return Err(SkipReason::UnknownField);
            }
            if !seen.insert(object.clone()) {
                continue;
            }
            if has(&state(bus, &object)?, 12) {
                eligible(bus, &object)?;
                return Ok((object, application));
            }
            let children: Vec<Object> = proxy(bus, &object, ACCESSIBLE)?
                .call("GetChildren", &())
                .map_err(|_| SkipReason::UnknownField)?;
            pending.extend(children);
        }
        Err(SkipReason::UnknownField)
    }

    /// Gets a classified X11/AT-SPI target without reading its text.
    /// # Errors
    /// Refuses unknown identities, roles, state or safe-write interfaces.
    pub fn metadata(&mut self) -> Result<(TargetId, Application), SkipReason> {
        let (object, application) = self.focus()?;
        Ok((target(&object), application))
    }

    /// Reads caret and selection offsets without retrieving document text.
    /// # Errors
    /// Refuses unsafe controls, unapproved applications or invalid ranges.
    pub fn cursor(&mut self) -> Result<Cursor, SkipReason> {
        let (object, application) = self.focus()?;
        if self.allowed.as_ref() != Some(&application.identity) {
            return Err(SkipReason::ExcludedApp);
        }
        let bus = self.bus.as_ref().ok_or(SkipReason::PermissionMissing)?;
        let text = proxy(bus, &object, TEXT)?;
        let caret: i32 = text
            .get_property("CaretOffset")
            .map_err(|_| SkipReason::UnknownField)?;
        let count: i32 = text
            .call("GetNSelections", &())
            .map_err(|_| SkipReason::UnknownField)?;
        let (start, end): (i32, i32) = match count {
            0 => (caret, caret),
            1 => text
                .call("GetSelection", &(0i32,))
                .map_err(|_| SkipReason::UnknownField)?,
            _ => return Err(SkipReason::UnknownField),
        };
        if end < start {
            return Err(SkipReason::UnknownField);
        }
        Ok(Cursor {
            target: target(&object),
            marker: [
                u64::try_from(caret).map_err(|_| SkipReason::UnknownField)?,
                u64::try_from(start).map_err(|_| SkipReason::UnknownField)?,
                u64::try_from(end).map_err(|_| SkipReason::UnknownField)?,
            ],
        })
    }

    /// Gets the AT-SPI insertion geometry.
    /// # Errors
    /// Refuses unknown or inaccessible screen coordinates.
    pub fn location(&mut self) -> Result<(f64, f64), SkipReason> {
        let (object, _) = self.focus()?;
        let bus = self.bus.as_ref().ok_or(SkipReason::PermissionMissing)?;
        let text = proxy(bus, &object, TEXT)?;
        let caret: i32 = text
            .get_property("CaretOffset")
            .map_err(|_| SkipReason::UnknownField)?;
        let (x, y, _, height): (i32, i32, i32, i32) = text
            .call("GetCharacterExtents", &(caret, 0u32))
            .map_err(|_| SkipReason::UnknownField)?;
        if height <= 0 {
            return Err(SkipReason::UnknownField);
        }
        Ok((
            f64::from(x),
            f64::from(y.checked_add(height).ok_or(SkipReason::UnknownField)?),
        ))
    }

    /// Runs hardware-only `XInput2` observation on the calling thread.
    /// # Errors
    /// Refuses unsupported sessions and unavailable X11/accessibility services.
    pub fn observe(
        active: &Arc<AtomicBool>,
        ready: &Arc<AtomicBool>,
        gate: &Gate,
        handler: &Handler,
    ) -> Result<(), SkipReason> {
        if !Self::permission() {
            return Err(SkipReason::PermissionMissing);
        }
        let (x, screen) = x11rb::connect(None).map_err(|_| SkipReason::PermissionMissing)?;
        x.xinput_xi_query_version(2, 0)
            .map_err(|_| SkipReason::PermissionMissing)?
            .reply()
            .map_err(|_| SkipReason::PermissionMissing)?;
        x.xinput_xi_select_events(
            x.setup().roots[screen].root,
            &[EventMask {
                deviceid: 1,
                mask: vec![XIEventMask::RAW_KEY_PRESS],
            }],
        )
        .map_err(|_| SkipReason::PermissionMissing)?
        .check()
        .map_err(|_| SkipReason::PermissionMissing)?;
        x.flush().map_err(|_| SkipReason::PermissionMissing)?;
        let node = atom(&x, b"Device Node")?;
        let mut adapter = Self::new();
        ready.store(true, Ordering::Release);
        let result = (|| loop {
            let event = x
                .wait_for_event()
                .map_err(|_| SkipReason::PermissionMissing)?;
            let Event::XinputRawKeyPress(event) = event else {
                continue;
            };
            if !active.load(Ordering::Acquire) {
                continue;
            }
            let observed_at = std::time::Instant::now();
            let (target, application) = adapter.metadata()?;
            if !gate(&application) {
                continue;
            }
            // Query the current source on every event, so hotplug/reused device
            // IDs cannot turn an earlier trusted device into an XTEST source.
            let device = x
                .xinput_xi_query_device(event.sourceid)
                .map_err(|_| SkipReason::UnknownField)?
                .reply()
                .map_err(|_| SkipReason::UnknownField)?;
            let physical = device.infos.first().is_some_and(|device| {
                device.type_ == DeviceType::SLAVE_KEYBOARD
                    && device.enabled
                    && !String::from_utf8_lossy(&device.name).contains("XTEST")
            });
            let property = x
                .xinput_xi_get_property(
                    event.sourceid,
                    false,
                    node,
                    AtomEnum::STRING.into(),
                    0,
                    1024,
                )
                .map_err(|_| SkipReason::UnknownField)?
                .reply()
                .map_err(|_| SkipReason::UnknownField)?;
            let has_node = matches!(&property.items,XIGetPropertyItems::Data8(bytes) if bytes.starts_with(b"/dev/input/event"));
            let pointer = x
                .query_pointer(x.setup().roots[screen].root)
                .map_err(|_| SkipReason::UnknownField)?
                .reply()
                .map_err(|_| SkipReason::UnknownField)?;
            let modifiers = u16::from(pointer.mask);
            let shift = modifiers & 1 != 0;
            let caps = modifiers & 2 != 0;
            let scan = event.detail.saturating_sub(8);
            let key = if !physical || !has_node || event.response_type & 0x80 != 0 {
                Key::Untrusted
            } else if modifiers & 4 != 0 && scan == 47 {
                Key::Paste
            } else if modifiers & (4 | 8 | 64) != 0 {
                Key::Reset
            } else {
                super::pc_key(scan, shift, caps)
            };
            if !matches!(key, Key::Modifier) {
                handler(KeyEvent {
                    key,
                    target,
                    observed_at,
                });
            }
        })();
        ready.store(false, Ordering::Release);
        result
    }
}
impl Adapter {
    // EditableText range methods preserve untouched text and rich-text spans.
    // Insertion lengths are UTF-8 bytes; start/end remain character offsets.
    fn write_range(
        &mut self,
        object: &Object,
        before: &FieldSnapshot,
        start: usize,
        end: usize,
        replacement: &str,
        expected: &str,
    ) -> Result<(), WriteError> {
        let bus = self.bus.clone().ok_or(WriteError::Failed)?;
        let editable = proxy(&bus, object, EDITABLE).map_err(|_| WriteError::Failed)?;
        let first = i32::try_from(start).map_err(|_| WriteError::Failed)?;
        let last = i32::try_from(end).map_err(|_| WriteError::Failed)?;
        let length = i32::try_from(replacement.len()).map_err(|_| WriteError::Failed)?;
        let mut intermediate = before.clone();
        let mut chars: Vec<char> = before.text.chars().collect();
        chars.drain(start..end);
        intermediate.text = chars.into_iter().collect();
        intermediate.caret = before.caret.saturating_sub(end - start);
        if start != end {
            let deleted: bool = editable
                .call("DeleteText", &(first, last))
                .map_err(|_| WriteError::Uncertain)?;
            if !deleted {
                return Err(WriteError::Uncertain);
            }
        }
        if self.snapshot().map_err(|_| WriteError::Uncertain)? != intermediate
            || self.focus().map_err(|_| WriteError::Uncertain)?.0 != *object
        {
            return Err(WriteError::Uncertain);
        }
        let inserted: Result<bool, _> = editable.call("InsertText", &(first, replacement, length));
        let after = self.snapshot().map_err(|_| WriteError::Uncertain)?;
        if !inserted.unwrap_or(false) {
            return Err(if after == intermediate {
                WriteError::Partial {
                    written: String::new(),
                }
            } else {
                WriteError::Uncertain
            });
        }
        if after.target != before.target || after.text != expected || after.selection_len != 0 {
            return Err(WriteError::Uncertain);
        }
        Ok(())
    }
}

impl Accessibility for Adapter {
    fn snapshot(&mut self) -> Result<FieldSnapshot, SkipReason> {
        let (object, application) = self.focus()?;
        if self.allowed.as_ref() != Some(&application.identity) {
            return Err(SkipReason::ExcludedApp);
        }
        let bus = self.bus.as_ref().ok_or(SkipReason::PermissionMissing)?;
        let text = proxy(bus, &object, TEXT)?;
        let caret: i32 = text
            .get_property("CaretOffset")
            .map_err(|_| SkipReason::UnknownField)?;
        let count: i32 = text
            .call("GetNSelections", &())
            .map_err(|_| SkipReason::UnknownField)?;
        let selection_len = match count {
            0 => 0,
            1 => {
                let (start, end): (i32, i32) = text
                    .call("GetSelection", &(0i32,))
                    .map_err(|_| SkipReason::UnknownField)?;
                if start != caret || end < start {
                    return Err(SkipReason::UnknownField);
                }
                usize::try_from(end - start).map_err(|_| SkipReason::UnknownField)?
            }
            _ => return Err(SkipReason::UnknownField),
        };
        let content: String = text
            .call("GetText", &(0i32, -1i32))
            .map_err(|_| SkipReason::UnknownField)?;
        let caret = usize::try_from(caret).map_err(|_| SkipReason::UnknownField)?;
        if caret + selection_len > content.chars().count() {
            return Err(SkipReason::UnknownField);
        }
        let snapshot = FieldSnapshot {
            target: target(&object),
            text: content,
            caret,
            selection_len,
            sensitivity: FieldSensitivity::Normal,
            supports_write: true,
        };
        drop(text);
        self.last = Some((object, snapshot.clone()));
        Ok(snapshot)
    }

    fn replace(
        &mut self,
        id: TargetId,
        start: usize,
        end: usize,
        replacement: &str,
    ) -> Result<(), WriteError> {
        let Some((object, before)) = self.last.take() else {
            return Err(WriteError::Failed);
        };
        if before.target != id
            || start > end
            || end > before.text.chars().count()
            || self.snapshot().map_err(|_| WriteError::Failed)? != before
            || self.focus().map_err(|_| WriteError::Failed)?.0 != object
        {
            return Err(WriteError::Failed);
        }
        let mut chars: Vec<char> = before.text.chars().collect();
        chars.splice(start..end, replacement.chars());
        let expected: String = chars.into_iter().collect();
        self.write_range(&object, &before, start, end, replacement, &expected)?;
        let bus = self.bus.as_ref().ok_or(WriteError::Uncertain)?;
        let text = proxy(bus, &object, TEXT).map_err(|_| WriteError::Uncertain)?;
        let caret = before.caret.saturating_sub(end - start) + replacement.chars().count();
        let changed: bool = text
            .call(
                "SetCaretOffset",
                &(i32::try_from(caret).map_err(|_| WriteError::Uncertain)?,),
            )
            .map_err(|_| WriteError::Uncertain)?;
        if !changed {
            return Err(WriteError::Partial {
                written: replacement.into(),
            });
        }
        drop(text);
        let after = self.snapshot().map_err(|_| WriteError::Uncertain)?;
        if after.target != id
            || after.text != expected
            || after.caret != caret
            || after.selection_len != 0
        {
            return Err(WriteError::Uncertain);
        }
        Ok(())
    }
}
#[must_use]
pub fn available_applications() -> Vec<Application> {
    let Ok((x, screen)) = x11rb::connect(None) else {
        return Vec::new();
    };
    let Ok(windows) = property(
        &x,
        x.setup().roots[screen].root,
        b"_NET_CLIENT_LIST",
        AtomEnum::WINDOW,
    ) else {
        return Vec::new();
    };
    windows
        .as_chunks::<4>()
        .0
        .iter()
        .filter_map(|bytes| word(bytes).ok())
        .filter_map(|window| application(&x, window).ok())
        .filter(|(pid, _)| *pid != std::process::id())
        .map(|(_, app)| app)
        .collect()
}
