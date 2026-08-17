//! Privacy-preserving global shortcut matching.
//!
//! Platform hooks necessarily receive input notifications. This module never
//! stores, serializes, emits, or logs an input event unless it matches one of
//! the six already configured shortcut bindings. Backends must pass all input
//! through unchanged.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum ShortcutAction {
    #[serde(rename = "shell.command-palette")]
    ShellCommandPalette,
    #[serde(rename = "realqa.capture.display")]
    RealqaCaptureDisplay,
    #[serde(rename = "realqa.capture.active-window")]
    RealqaCaptureActiveWindow,
    #[serde(rename = "realqa.capture.all-displays")]
    RealqaCaptureAllDisplays,
    #[serde(rename = "realqa.capture.selection")]
    RealqaCaptureSelection,
    #[serde(rename = "realqa.capture.toolbar")]
    RealqaCaptureToolbar,
}

impl ShortcutAction {
    pub const ALL: [Self; 6] = [
        Self::ShellCommandPalette,
        Self::RealqaCaptureDisplay,
        Self::RealqaCaptureActiveWindow,
        Self::RealqaCaptureAllDisplays,
        Self::RealqaCaptureSelection,
        Self::RealqaCaptureToolbar,
    ];
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ShortcutModifier {
    RightPrimary,
    Shift,
    Alt,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ShortcutKey {
    KeyK,
    Digit1,
    Digit2,
    Digit3,
    Digit4,
    Digit5,
    Space,
    Tab,
    KeyQ,
    Delete,
    Backspace,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ShortcutBinding {
    pub enabled: bool,
    pub modifiers: Vec<ShortcutModifier>,
    pub key: ShortcutKey,
}

pub type ShortcutBindings = BTreeMap<ShortcutAction, ShortcutBinding>;

pub fn default_bindings() -> ShortcutBindings {
    BTreeMap::from([
        (
            ShortcutAction::ShellCommandPalette,
            ShortcutBinding {
                enabled: true,
                modifiers: vec![ShortcutModifier::RightPrimary],
                key: ShortcutKey::KeyK,
            },
        ),
        (
            ShortcutAction::RealqaCaptureDisplay,
            ShortcutBinding {
                enabled: true,
                modifiers: vec![],
                key: ShortcutKey::Digit1,
            },
        ),
        (
            ShortcutAction::RealqaCaptureActiveWindow,
            ShortcutBinding {
                enabled: true,
                modifiers: vec![],
                key: ShortcutKey::Digit2,
            },
        ),
        (
            ShortcutAction::RealqaCaptureAllDisplays,
            ShortcutBinding {
                enabled: true,
                modifiers: vec![],
                key: ShortcutKey::Digit3,
            },
        ),
        (
            ShortcutAction::RealqaCaptureSelection,
            ShortcutBinding {
                enabled: true,
                modifiers: vec![],
                key: ShortcutKey::Digit4,
            },
        ),
        (
            ShortcutAction::RealqaCaptureToolbar,
            ShortcutBinding {
                enabled: true,
                modifiers: vec![],
                key: ShortcutKey::Digit5,
            },
        ),
    ])
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ShortcutFailure {
    Malformed,
    Conflict,
    Reserved,
    RegistrationFailed,
    PermissionDenied,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ShortcutPermission {
    Available,
    NotDetermined,
    Denied,
    X11Unavailable,
    Unsupported,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ShortcutPlatform {
    Macos,
    Windows,
    X11,
    Unsupported,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum NativeKey {
    RightPrimary,
    LeftPrimary,
    LeftShift,
    RightShift,
    LeftAlt,
    RightAlt,
    Key(ShortcutKey),
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct NativeKeyEvent {
    pub key: NativeKey,
    pub pressed: bool,
}

#[cfg(desktop)]
pub fn normalize_global_event(event: &rdev::EventType) -> Option<NativeKeyEvent> {
    use rdev::{EventType, Key};
    let (key, pressed) = match event {
        EventType::KeyPress(key) => (*key, true),
        EventType::KeyRelease(key) => (*key, false),
        _ => return None,
    };
    let native = match key {
        Key::ShiftLeft => NativeKey::LeftShift,
        Key::ShiftRight => NativeKey::RightShift,
        Key::Alt => NativeKey::LeftAlt,
        Key::AltGr => NativeKey::RightAlt,
        Key::KeyK => NativeKey::Key(ShortcutKey::KeyK),
        Key::Num1 => NativeKey::Key(ShortcutKey::Digit1),
        Key::Num2 => NativeKey::Key(ShortcutKey::Digit2),
        Key::Num3 => NativeKey::Key(ShortcutKey::Digit3),
        Key::Num4 => NativeKey::Key(ShortcutKey::Digit4),
        Key::Num5 => NativeKey::Key(ShortcutKey::Digit5),
        Key::Space => NativeKey::Key(ShortcutKey::Space),
        Key::Tab => NativeKey::Key(ShortcutKey::Tab),
        Key::KeyQ => NativeKey::Key(ShortcutKey::KeyQ),
        Key::Delete => NativeKey::Key(ShortcutKey::Delete),
        Key::Backspace => NativeKey::Key(ShortcutKey::Backspace),
        #[cfg(target_os = "macos")]
        Key::MetaRight => NativeKey::RightPrimary,
        #[cfg(not(target_os = "macos"))]
        Key::ControlRight => NativeKey::RightPrimary,
        Key::ControlLeft | Key::MetaLeft => NativeKey::LeftPrimary,
        _ => return None,
    };
    Some(NativeKeyEvent {
        key: native,
        pressed,
    })
}

/// Converts only the physical keys that this contract understands. The values
/// are platform-native physical codes: macOS virtual key codes, Windows virtual
/// key codes, and X11/XInput keycodes. Unknown codes are ignored rather than
/// being captured, recorded, or sent across the bridge.
pub fn normalize_native_key(platform: ShortcutPlatform, code: u32) -> Option<NativeKey> {
    match platform {
        // kVK_RightCommand / kVK_Command and physical US ANSI key positions.
        ShortcutPlatform::Macos => match code {
            54 => Some(NativeKey::RightPrimary),
            55 => Some(NativeKey::LeftPrimary),
            56 => Some(NativeKey::LeftShift),
            60 => Some(NativeKey::RightShift),
            58 => Some(NativeKey::LeftAlt),
            61 => Some(NativeKey::RightAlt),
            40 => Some(NativeKey::Key(ShortcutKey::KeyK)),
            18 => Some(NativeKey::Key(ShortcutKey::Digit1)),
            19 => Some(NativeKey::Key(ShortcutKey::Digit2)),
            20 => Some(NativeKey::Key(ShortcutKey::Digit3)),
            21 => Some(NativeKey::Key(ShortcutKey::Digit4)),
            23 => Some(NativeKey::Key(ShortcutKey::Digit5)),
            49 => Some(NativeKey::Key(ShortcutKey::Space)),
            48 => Some(NativeKey::Key(ShortcutKey::Tab)),
            12 => Some(NativeKey::Key(ShortcutKey::KeyQ)),
            117 => Some(NativeKey::Key(ShortcutKey::Delete)),
            51 => Some(NativeKey::Key(ShortcutKey::Backspace)),
            _ => None,
        },
        // VK_RCONTROL / VK_LCONTROL and printable virtual-key codes.
        ShortcutPlatform::Windows => match code {
            0xa3 => Some(NativeKey::RightPrimary),
            0xa2 => Some(NativeKey::LeftPrimary),
            0xa0 => Some(NativeKey::LeftShift),
            0xa1 => Some(NativeKey::RightShift),
            0xa4 => Some(NativeKey::LeftAlt),
            0xa5 => Some(NativeKey::RightAlt),
            0x4b => Some(NativeKey::Key(ShortcutKey::KeyK)),
            0x31..=0x35 => digit_key(code - 0x30),
            0x20 => Some(NativeKey::Key(ShortcutKey::Space)),
            0x09 => Some(NativeKey::Key(ShortcutKey::Tab)),
            0x51 => Some(NativeKey::Key(ShortcutKey::KeyQ)),
            0x2e => Some(NativeKey::Key(ShortcutKey::Delete)),
            0x08 => Some(NativeKey::Key(ShortcutKey::Backspace)),
            _ => None,
        },
        // Common X11 keycodes from XKB's default evdev mapping. XInput2
        // supplies the same physical distinction for Control_L/Control_R.
        ShortcutPlatform::X11 => match code {
            105 => Some(NativeKey::RightPrimary),
            37 => Some(NativeKey::LeftPrimary),
            50 => Some(NativeKey::LeftShift),
            62 => Some(NativeKey::RightShift),
            64 => Some(NativeKey::LeftAlt),
            108 => Some(NativeKey::RightAlt),
            45 => Some(NativeKey::Key(ShortcutKey::KeyK)),
            10..=14 => digit_key(code - 9),
            65 => Some(NativeKey::Key(ShortcutKey::Space)),
            23 => Some(NativeKey::Key(ShortcutKey::Tab)),
            24 => Some(NativeKey::Key(ShortcutKey::KeyQ)),
            119 => Some(NativeKey::Key(ShortcutKey::Delete)),
            22 => Some(NativeKey::Key(ShortcutKey::Backspace)),
            _ => None,
        },
        ShortcutPlatform::Unsupported => None,
    }
}

fn digit_key(digit: u32) -> Option<NativeKey> {
    Some(NativeKey::Key(match digit {
        1 => ShortcutKey::Digit1,
        2 => ShortcutKey::Digit2,
        3 => ShortcutKey::Digit3,
        4 => ShortcutKey::Digit4,
        5 => ShortcutKey::Digit5,
        _ => return None,
    }))
}

pub trait NativeShortcutBackend {
    /// Install the full candidate set transactionally. On error, the prior
    /// native registration must still be live. Implementations never suppress
    /// or record key events.
    fn install(&mut self, bindings: &ShortcutBindings) -> Result<(), ShortcutFailure>;
    fn permission(&self) -> ShortcutPermission;
    fn refresh_permission(&mut self) -> ShortcutPermission {
        self.permission()
    }
    fn request_permission(&mut self) -> ShortcutPermission {
        self.permission()
    }
    fn platform(&self) -> ShortcutPlatform;
}

pub struct ShortcutService<B> {
    backend: B,
    active: ShortcutBindings,
    staged: Option<ShortcutBindings>,
    right_primary: bool,
    left_primary: bool,
    left_shift: bool,
    right_shift: bool,
    left_alt: bool,
    right_alt: bool,
}

impl<B: NativeShortcutBackend> ShortcutService<B> {
    pub fn new(backend: B) -> Self {
        Self {
            backend,
            active: default_bindings(),
            staged: None,
            right_primary: false,
            left_primary: false,
            left_shift: false,
            right_shift: false,
            left_alt: false,
            right_alt: false,
        }
    }

    pub fn active(&self) -> &ShortcutBindings {
        &self.active
    }

    pub fn permission(&self) -> ShortcutPermission {
        self.backend.permission()
    }

    pub fn platform(&self) -> ShortcutPlatform {
        self.backend.platform()
    }

    pub fn request_permission(&mut self) -> ShortcutPermission {
        self.backend.request_permission()
    }

    pub fn apply(&mut self, candidate: ShortcutBindings) -> Result<(), ShortcutFailure> {
        self.stage(candidate.clone())?;
        self.commit_staged(&candidate)
    }

    /// Registers a candidate without allowing it to emit actions. The caller
    /// must commit it only after its persisted settings snapshot succeeds.
    pub fn stage(&mut self, candidate: ShortcutBindings) -> Result<(), ShortcutFailure> {
        validate_bindings(&candidate, self.platform())?;
        if self.backend.refresh_permission() != ShortcutPermission::Available {
            return Err(ShortcutFailure::PermissionDenied);
        }
        self.backend.install(&candidate)?;
        self.staged = Some(candidate);
        Ok(())
    }

    /// Activates the exact candidate that was successfully staged.
    pub fn commit_staged(&mut self, candidate: &ShortcutBindings) -> Result<(), ShortcutFailure> {
        if self.staged.as_ref() != Some(candidate) {
            return Err(ShortcutFailure::Malformed);
        }
        self.active = candidate.clone();
        self.staged = None;
        Ok(())
    }

    /// Discards a pending candidate. The active matcher remains on the last
    /// persisted binding even if restoring backend registration fails.
    pub fn rollback_staged(&mut self) -> Result<(), ShortcutFailure> {
        self.staged = None;
        self.backend.install(&self.active.clone())?;
        Ok(())
    }

    /// Returns only a configured action. Unrelated input has no observable
    /// output and is intentionally not retained anywhere.
    pub fn process(&mut self, event: NativeKeyEvent) -> Option<ShortcutAction> {
        let key = match event.key {
            NativeKey::RightPrimary => {
                self.right_primary = event.pressed;
                return None;
            }
            NativeKey::LeftPrimary => {
                self.left_primary = event.pressed;
                return None;
            }
            NativeKey::LeftShift => {
                self.left_shift = event.pressed;
                return None;
            }
            NativeKey::RightShift => {
                self.right_shift = event.pressed;
                return None;
            }
            NativeKey::LeftAlt => {
                self.left_alt = event.pressed;
                return None;
            }
            NativeKey::RightAlt => {
                self.right_alt = event.pressed;
                return None;
            }
            NativeKey::Key(key) if event.pressed => key,
            NativeKey::Key(_) => return None,
        };
        self.active.iter().find_map(|(action, binding)| {
            (binding.enabled && binding.key == key && self.modifiers_match(binding))
                .then_some(*action)
        })
    }

    fn modifiers_match(&self, binding: &ShortcutBinding) -> bool {
        let modifiers = binding.modifiers.iter().copied().collect::<BTreeSet<_>>();
        modifiers.contains(&ShortcutModifier::RightPrimary) == self.right_primary
            && !self.left_primary
            && modifiers.contains(&ShortcutModifier::Shift) == (self.left_shift || self.right_shift)
            && modifiers.contains(&ShortcutModifier::Alt) == (self.left_alt || self.right_alt)
    }
}

pub fn validate_bindings(
    bindings: &ShortcutBindings,
    platform: ShortcutPlatform,
) -> Result<(), ShortcutFailure> {
    if bindings.len() != ShortcutAction::ALL.len()
        || ShortcutAction::ALL
            .iter()
            .any(|action| !bindings.contains_key(action))
    {
        return Err(ShortcutFailure::Malformed);
    }
    let mut seen = BTreeSet::new();
    for binding in bindings.values() {
        if binding.modifiers.iter().collect::<BTreeSet<_>>().len() != binding.modifiers.len() {
            return Err(ShortcutFailure::Malformed);
        }
        if binding.enabled
            && binding.modifiers.is_empty()
            && !matches!(
                binding.key,
                ShortcutKey::Digit1
                    | ShortcutKey::Digit2
                    | ShortcutKey::Digit3
                    | ShortcutKey::Digit4
                    | ShortcutKey::Digit5
            )
        {
            return Err(ShortcutFailure::Malformed);
        }
        if !binding.enabled {
            continue;
        }
        if reserved(binding, platform) {
            return Err(ShortcutFailure::Reserved);
        }
        let identifier = (
            binding.modifiers.iter().copied().collect::<BTreeSet<_>>(),
            binding.key,
        );
        if !seen.insert(identifier) {
            return Err(ShortcutFailure::Conflict);
        }
    }
    Ok(())
}

fn reserved(binding: &ShortcutBinding, platform: ShortcutPlatform) -> bool {
    let has_primary = binding.modifiers.contains(&ShortcutModifier::RightPrimary);
    let has_alt = binding.modifiers.contains(&ShortcutModifier::Alt);
    match platform {
        ShortcutPlatform::Macos => {
            has_primary
                && matches!(
                    binding.key,
                    ShortcutKey::Space | ShortcutKey::Tab | ShortcutKey::KeyQ
                )
        }
        ShortcutPlatform::Windows => has_primary && has_alt && binding.key == ShortcutKey::Delete,
        ShortcutPlatform::X11 => has_primary && has_alt && binding.key == ShortcutKey::Backspace,
        ShortcutPlatform::Unsupported => false,
    }
}

/// Platform registration state used by the desktop host. The native adapter
/// boundary intentionally accepts only physical-key events (`RightPrimary` is
/// right Command on macOS and right Control on Windows/X11) and emits only a
/// configured action. It never exposes raw input to the renderer or logs.
pub struct PlatformShortcutBackend {
    permission: ShortcutPermission,
    platform: ShortcutPlatform,
    installed: ShortcutBindings,
    fail_next: bool,
}
impl PlatformShortcutBackend {
    pub fn current() -> Self {
        #[cfg(target_os = "macos")]
        let (platform, permission) = (
            ShortcutPlatform::Macos,
            macos_accessibility_permission(false),
        );
        #[cfg(target_os = "windows")]
        let (platform, permission) = (ShortcutPlatform::Windows, ShortcutPermission::Available);
        #[cfg(target_os = "linux")]
        let (platform, permission) = (
            ShortcutPlatform::X11,
            if std::env::var_os("DISPLAY").is_some() {
                ShortcutPermission::Available
            } else {
                ShortcutPermission::X11Unavailable
            },
        );
        #[cfg(not(any(target_os = "macos", target_os = "windows", target_os = "linux")))]
        let (platform, permission) = (
            ShortcutPlatform::Unsupported,
            ShortcutPermission::Unsupported,
        );
        Self {
            permission,
            platform,
            installed: default_bindings(),
            fail_next: false,
        }
    }
}
impl NativeShortcutBackend for PlatformShortcutBackend {
    fn install(&mut self, bindings: &ShortcutBindings) -> Result<(), ShortcutFailure> {
        if self.fail_next {
            self.fail_next = false;
            return Err(ShortcutFailure::RegistrationFailed);
        }
        self.installed = bindings.clone();
        Ok(())
    }

    fn permission(&self) -> ShortcutPermission {
        self.permission
    }

    fn refresh_permission(&mut self) -> ShortcutPermission {
        #[cfg(target_os = "macos")]
        {
            self.permission = macos_accessibility_permission(false);
        }
        self.permission
    }

    fn request_permission(&mut self) -> ShortcutPermission {
        #[cfg(target_os = "macos")]
        {
            self.permission = macos_accessibility_permission(true);
        }
        self.permission
    }

    fn platform(&self) -> ShortcutPlatform {
        self.platform
    }
}

#[cfg(target_os = "macos")]
fn macos_accessibility_permission(prompt: bool) -> ShortcutPermission {
    use std::ffi::c_void;

    #[link(name = "ApplicationServices", kind = "framework")]
    #[link(name = "CoreFoundation", kind = "framework")]
    unsafe extern "C" {
        fn AXIsProcessTrusted() -> bool;
        fn AXIsProcessTrustedWithOptions(options: *const c_void) -> bool;
        fn CFDictionaryCreate(
            allocator: *const c_void,
            keys: *const *const c_void,
            values: *const *const c_void,
            value_count: isize,
            key_callbacks: *const c_void,
            value_callbacks: *const c_void,
        ) -> *const c_void;
        fn CFRelease(value: *const c_void);
        static kAXTrustedCheckOptionPrompt: *const c_void;
        static kCFBooleanTrue: *const c_void;
    }

    let trusted = unsafe {
        if !prompt {
            AXIsProcessTrusted()
        } else {
            let key = kAXTrustedCheckOptionPrompt;
            let value = kCFBooleanTrue;
            let options = CFDictionaryCreate(
                std::ptr::null(),
                &key,
                &value,
                1,
                std::ptr::null(),
                std::ptr::null(),
            );
            let trusted = AXIsProcessTrustedWithOptions(options);
            if !options.is_null() {
                CFRelease(options);
            }
            trusted
        }
    };
    if trusted {
        ShortcutPermission::Available
    } else {
        ShortcutPermission::NotDetermined
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    struct Fake {
        permission: ShortcutPermission,
        refreshed_permission: Option<ShortcutPermission>,
        platform: ShortcutPlatform,
        fail: bool,
        fail_on_install: Option<usize>,
        installs: usize,
    }
    impl Default for Fake {
        fn default() -> Self {
            Self {
                permission: ShortcutPermission::Available,
                refreshed_permission: None,
                platform: ShortcutPlatform::X11,
                fail: false,
                fail_on_install: None,
                installs: 0,
            }
        }
    }
    impl NativeShortcutBackend for Fake {
        fn install(&mut self, _: &ShortcutBindings) -> Result<(), ShortcutFailure> {
            self.installs += 1;
            if self.fail || self.fail_on_install == Some(self.installs) {
                Err(ShortcutFailure::RegistrationFailed)
            } else {
                Ok(())
            }
        }

        fn permission(&self) -> ShortcutPermission {
            self.permission
        }

        fn refresh_permission(&mut self) -> ShortcutPermission {
            if let Some(permission) = self.refreshed_permission {
                self.permission = permission;
            }
            self.permission
        }

        fn platform(&self) -> ShortcutPlatform {
            self.platform
        }
    }
    #[test]
    fn matches_only_exact_primary_modifier_state() {
        let mut service = ShortcutService::new(Fake::default());
        assert_eq!(
            service.process(NativeKeyEvent {
                key: NativeKey::LeftPrimary,
                pressed: true
            }),
            None
        );
        assert_eq!(
            service.process(NativeKeyEvent {
                key: NativeKey::Key(ShortcutKey::Digit1),
                pressed: true
            }),
            None
        );
        assert_eq!(
            service.process(NativeKeyEvent {
                key: NativeKey::LeftPrimary,
                pressed: false
            }),
            None
        );
        assert_eq!(
            service.process(NativeKeyEvent {
                key: NativeKey::Key(ShortcutKey::Digit1),
                pressed: true
            }),
            Some(ShortcutAction::RealqaCaptureDisplay)
        );
        assert_eq!(
            service.process(NativeKeyEvent {
                key: NativeKey::Key(ShortcutKey::KeyK),
                pressed: true
            }),
            None
        );
        assert_eq!(
            service.process(NativeKeyEvent {
                key: NativeKey::LeftPrimary,
                pressed: true
            }),
            None
        );
        assert_eq!(
            service.process(NativeKeyEvent {
                key: NativeKey::Key(ShortcutKey::KeyK),
                pressed: true
            }),
            None
        );
        assert_eq!(
            service.process(NativeKeyEvent {
                key: NativeKey::LeftPrimary,
                pressed: false
            }),
            None
        );
        assert_eq!(
            service.process(NativeKeyEvent {
                key: NativeKey::RightPrimary,
                pressed: true
            }),
            None
        );
        assert_eq!(
            service.process(NativeKeyEvent {
                key: NativeKey::Key(ShortcutKey::KeyK),
                pressed: true
            }),
            Some(ShortcutAction::ShellCommandPalette)
        );
        assert_eq!(
            service.process(NativeKeyEvent {
                key: NativeKey::Key(ShortcutKey::KeyQ),
                pressed: true
            }),
            None
        );
    }

    #[test]
    fn keeps_shift_and_alt_active_until_each_physical_key_is_released() {
        let mut service = ShortcutService::new(Fake::default());
        for (first, second) in [
            (NativeKey::LeftShift, NativeKey::RightShift),
            (NativeKey::LeftAlt, NativeKey::RightAlt),
        ] {
            assert_eq!(
                service.process(NativeKeyEvent {
                    key: first,
                    pressed: true
                }),
                None
            );
            assert_eq!(
                service.process(NativeKeyEvent {
                    key: second,
                    pressed: true
                }),
                None
            );
            assert_eq!(
                service.process(NativeKeyEvent {
                    key: first,
                    pressed: false
                }),
                None
            );
            assert_eq!(
                service.process(NativeKeyEvent {
                    key: NativeKey::Key(ShortcutKey::Digit1),
                    pressed: true
                }),
                None
            );
            assert_eq!(
                service.process(NativeKeyEvent {
                    key: second,
                    pressed: false
                }),
                None
            );
            assert_eq!(
                service.process(NativeKeyEvent {
                    key: NativeKey::Key(ShortcutKey::Digit1),
                    pressed: true
                }),
                Some(ShortcutAction::RealqaCaptureDisplay)
            );
        }
    }
    #[test]
    fn rolls_back_active_binding_when_registration_fails() {
        let backend = Fake {
            fail: true,
            ..Default::default()
        };
        let mut service = ShortcutService::new(backend);
        let old = service.active().clone();
        let mut next = old.clone();
        next.get_mut(&ShortcutAction::ShellCommandPalette)
            .expect("binding")
            .key = ShortcutKey::KeyQ;
        assert_eq!(
            service.apply(next),
            Err(ShortcutFailure::RegistrationFailed)
        );
        assert_eq!(service.active(), &old);
    }

    #[test]
    fn stages_candidates_until_persistence_commits_them() {
        let mut service = ShortcutService::new(Fake::default());
        let old = service.active().clone();
        let mut candidate = old.clone();
        candidate
            .get_mut(&ShortcutAction::ShellCommandPalette)
            .expect("binding")
            .key = ShortcutKey::KeyQ;

        assert_eq!(service.stage(candidate.clone()), Ok(()));
        assert_eq!(service.active(), &old);
        assert_eq!(service.commit_staged(&candidate), Ok(()));
        assert_eq!(service.active(), &candidate);
    }

    #[test]
    fn rollback_discards_staged_candidate_before_a_failed_backend_restore() {
        let mut service = ShortcutService::new(Fake {
            fail_on_install: Some(2),
            ..Default::default()
        });
        let old = service.active().clone();
        let mut candidate = old.clone();
        candidate
            .get_mut(&ShortcutAction::ShellCommandPalette)
            .expect("binding")
            .key = ShortcutKey::KeyQ;

        assert_eq!(service.stage(candidate.clone()), Ok(()));
        assert_eq!(
            service.rollback_staged(),
            Err(ShortcutFailure::RegistrationFailed)
        );
        assert_eq!(service.active(), &old);
        assert_eq!(
            service.commit_staged(&candidate),
            Err(ShortcutFailure::Malformed)
        );
        assert_eq!(service.active(), &old);
    }
    #[test]
    fn rejects_conflicts_and_permission_denial() {
        let mut duplicate = default_bindings();
        duplicate
            .get_mut(&ShortcutAction::RealqaCaptureDisplay)
            .expect("binding")
            .key = ShortcutKey::Digit2;
        assert_eq!(
            validate_bindings(&duplicate, ShortcutPlatform::X11),
            Err(ShortcutFailure::Conflict)
        );
        let mut service = ShortcutService::new(Fake {
            permission: ShortcutPermission::Denied,
            ..Default::default()
        });
        assert_eq!(
            service.apply(default_bindings()),
            Err(ShortcutFailure::PermissionDenied)
        );
    }

    #[test]
    fn refreshes_permission_before_applying_bindings() {
        let mut service = ShortcutService::new(Fake {
            permission: ShortcutPermission::NotDetermined,
            refreshed_permission: Some(ShortcutPermission::Available),
            ..Default::default()
        });
        assert_eq!(service.apply(default_bindings()), Ok(()));
    }

    #[test]
    fn applies_reserved_chord_rules_per_platform() {
        let mut bindings = default_bindings();
        let palette = bindings
            .get_mut(&ShortcutAction::ShellCommandPalette)
            .expect("palette binding");
        palette.key = ShortcutKey::Space;
        assert_eq!(
            validate_bindings(&bindings, ShortcutPlatform::Macos),
            Err(ShortcutFailure::Reserved)
        );
        assert_eq!(
            validate_bindings(&bindings, ShortcutPlatform::Windows),
            Ok(())
        );
        assert_eq!(validate_bindings(&bindings, ShortcutPlatform::X11), Ok(()));
    }

    #[test]
    fn platform_smoke_contracts_validate_default_chords() {
        for platform in [
            ShortcutPlatform::Macos,
            ShortcutPlatform::Windows,
            ShortcutPlatform::X11,
        ] {
            assert_eq!(validate_bindings(&default_bindings(), platform), Ok(()));
        }
    }

    #[test]
    fn native_platform_mappings_keep_left_and_right_primary_distinct() {
        for (platform, right, left, key_k) in [
            (ShortcutPlatform::Macos, 54, 55, 40),
            (ShortcutPlatform::Windows, 0xa3, 0xa2, 0x4b),
            (ShortcutPlatform::X11, 105, 37, 45),
        ] {
            assert_eq!(
                normalize_native_key(platform, right),
                Some(NativeKey::RightPrimary)
            );
            assert_eq!(
                normalize_native_key(platform, left),
                Some(NativeKey::LeftPrimary)
            );
            assert_eq!(
                normalize_native_key(platform, key_k),
                Some(NativeKey::Key(ShortcutKey::KeyK))
            );
            assert_eq!(normalize_native_key(platform, 0), None);
        }
    }

    #[test]
    fn serializes_contracted_dotted_action_ids() {
        let bindings = default_bindings();
        let serialized = serde_json::to_value(bindings).expect("serialize bindings");
        assert!(serialized.get("shell.command-palette").is_some());
        assert!(serialized.get("realqa.capture.active-window").is_some());
        assert!(serialized.get("shell-command-palette").is_none());
    }

    #[test]
    fn normalizes_every_selectable_key_on_every_platform() {
        let cases = [
            (
                ShortcutPlatform::Macos,
                [40, 18, 19, 20, 21, 23, 49, 48, 12, 117, 51],
            ),
            (
                ShortcutPlatform::Windows,
                [
                    0x4b, 0x31, 0x32, 0x33, 0x34, 0x35, 0x20, 0x09, 0x51, 0x2e, 0x08,
                ],
            ),
            (
                ShortcutPlatform::X11,
                [45, 10, 11, 12, 13, 14, 65, 23, 24, 119, 22],
            ),
        ];
        for (platform, codes) in cases {
            for code in codes {
                assert!(matches!(
                    normalize_native_key(platform, code),
                    Some(NativeKey::Key(_))
                ));
            }
        }
    }

    #[cfg(desktop)]
    #[test]
    fn global_listener_accepts_only_the_contracted_physical_keys() {
        assert_eq!(
            normalize_global_event(&rdev::EventType::KeyPress(rdev::Key::KeyK)),
            Some(NativeKeyEvent {
                key: NativeKey::Key(ShortcutKey::KeyK),
                pressed: true
            })
        );
        assert_eq!(
            normalize_global_event(&rdev::EventType::KeyRelease(rdev::Key::KeyK)),
            Some(NativeKeyEvent {
                key: NativeKey::Key(ShortcutKey::KeyK),
                pressed: false
            })
        );
        assert_eq!(
            normalize_global_event(&rdev::EventType::KeyPress(rdev::Key::KeyW)),
            None
        );
    }
}
