//! Privacy-preserving global shortcut matching.
//!
//! Platform hooks necessarily receive input notifications. This module never
//! stores, serializes, emits, or logs an input event unless it matches one of
//! the six already configured shortcut bindings. Backends must pass all input
//! through unchanged.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ShortcutAction {
    ShellCommandPalette,
    RealqaCaptureDisplay,
    RealqaCaptureActiveWindow,
    RealqaCaptureAllDisplays,
    RealqaCaptureSelection,
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
    Shift,
    Alt,
    Key(ShortcutKey),
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct NativeKeyEvent {
    pub key: NativeKey,
    pub pressed: bool,
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
            56 | 60 => Some(NativeKey::Shift),
            58 | 61 => Some(NativeKey::Alt),
            40 => Some(NativeKey::Key(ShortcutKey::KeyK)),
            18..=23 => digit_key(code - 18),
            _ => None,
        },
        // VK_RCONTROL / VK_LCONTROL and printable virtual-key codes.
        ShortcutPlatform::Windows => match code {
            0xa3 => Some(NativeKey::RightPrimary),
            0xa2 => Some(NativeKey::LeftPrimary),
            0x10 => Some(NativeKey::Shift),
            0x12 => Some(NativeKey::Alt),
            0x4b => Some(NativeKey::Key(ShortcutKey::KeyK)),
            0x31..=0x35 => digit_key(code - 0x30),
            _ => None,
        },
        // Common X11 keycodes from XKB's default evdev mapping. XInput2
        // supplies the same physical distinction for Control_L/Control_R.
        ShortcutPlatform::X11 => match code {
            105 => Some(NativeKey::RightPrimary),
            37 => Some(NativeKey::LeftPrimary),
            50 | 62 => Some(NativeKey::Shift),
            64 | 108 => Some(NativeKey::Alt),
            45 => Some(NativeKey::Key(ShortcutKey::KeyK)),
            10..=14 => digit_key(code - 9),
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
    fn request_permission(&mut self) -> ShortcutPermission {
        self.permission()
    }
    fn platform(&self) -> ShortcutPlatform;
}

pub struct ShortcutService<B> {
    backend: B,
    active: ShortcutBindings,
    right_primary: bool,
    shift: bool,
    alt: bool,
}

impl<B: NativeShortcutBackend> ShortcutService<B> {
    pub fn new(backend: B) -> Self {
        Self {
            backend,
            active: default_bindings(),
            right_primary: false,
            shift: false,
            alt: false,
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
        validate_bindings(&candidate, self.platform())?;
        if self.permission() != ShortcutPermission::Available {
            return Err(ShortcutFailure::PermissionDenied);
        }
        self.backend.install(&candidate)?;
        self.active = candidate;
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
            NativeKey::LeftPrimary => return None,
            NativeKey::Shift => {
                self.shift = event.pressed;
                return None;
            }
            NativeKey::Alt => {
                self.alt = event.pressed;
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
            && modifiers.contains(&ShortcutModifier::Shift) == self.shift
            && modifiers.contains(&ShortcutModifier::Alt) == self.alt
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
        let (platform, permission) = if cfg!(target_os = "macos") {
            (ShortcutPlatform::Macos, ShortcutPermission::NotDetermined)
        } else if cfg!(target_os = "windows") {
            (ShortcutPlatform::Windows, ShortcutPermission::Available)
        } else if cfg!(target_os = "linux") {
            (
                ShortcutPlatform::X11,
                if std::env::var_os("DISPLAY").is_some() {
                    ShortcutPermission::Available
                } else {
                    ShortcutPermission::X11Unavailable
                },
            )
        } else {
            (
                ShortcutPlatform::Unsupported,
                ShortcutPermission::Unsupported,
            )
        };
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

    fn request_permission(&mut self) -> ShortcutPermission {
        self.permission
    }

    fn platform(&self) -> ShortcutPlatform {
        self.platform
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    struct Fake {
        permission: ShortcutPermission,
        platform: ShortcutPlatform,
        fail: bool,
        installs: usize,
    }
    impl Default for Fake {
        fn default() -> Self {
            Self {
                permission: ShortcutPermission::Available,
                platform: ShortcutPlatform::X11,
                fail: false,
                installs: 0,
            }
        }
    }
    impl NativeShortcutBackend for Fake {
        fn install(&mut self, _: &ShortcutBindings) -> Result<(), ShortcutFailure> {
            self.installs += 1;
            if self.fail {
                Err(ShortcutFailure::RegistrationFailed)
            } else {
                Ok(())
            }
        }

        fn permission(&self) -> ShortcutPermission {
            self.permission
        }

        fn platform(&self) -> ShortcutPlatform {
            self.platform
        }
    }
    #[test]
    fn matches_right_not_left_and_discards_unrelated_input() {
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
                key: NativeKey::Key(ShortcutKey::KeyK),
                pressed: true
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
}
