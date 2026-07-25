#[cfg(feature = "desktop-cef")]
use global_hotkey::{
    GlobalHotKeyManager,
    hotkey::{Code, HotKey, Modifiers},
};
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct StructuredShortcut {
    modifiers: Vec<ShortcutModifier>,
    key: ShortcutKey,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
enum ShortcutModifier {
    Control,
    Alt,
    Shift,
    Meta,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
enum ShortcutKey {
    A,
    B,
    C,
    D,
    E,
    F,
    G,
    H,
    I,
    J,
    K,
    L,
    M,
    N,
    O,
    P,
    Q,
    R,
    S,
    T,
    U,
    V,
    W,
    X,
    Y,
    Z,
    #[serde(rename = "0")]
    Digit0,
    #[serde(rename = "1")]
    Digit1,
    #[serde(rename = "2")]
    Digit2,
    #[serde(rename = "3")]
    Digit3,
    #[serde(rename = "4")]
    Digit4,
    #[serde(rename = "5")]
    Digit5,
    #[serde(rename = "6")]
    Digit6,
    #[serde(rename = "7")]
    Digit7,
    #[serde(rename = "8")]
    Digit8,
    #[serde(rename = "9")]
    Digit9,
    F1,
    F2,
    F3,
    F4,
    F5,
    F6,
    F7,
    F8,
    F9,
    F10,
    F11,
    F12,
    Space,
    Enter,
}

impl StructuredShortcut {
    pub(crate) fn validate(&self) -> Result<(), ShortcutFailure> {
        if self.modifiers.is_empty() {
            return Err(ShortcutFailure::Malformed);
        }
        let unique = self
            .modifiers
            .iter()
            .copied()
            .collect::<std::collections::HashSet<_>>();
        if unique.len() != self.modifiers.len() {
            return Err(ShortcutFailure::Malformed);
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum ShortcutFailure {
    Malformed,
    Conflict,
    PermissionDenied,
    RegistrationFailed,
    #[cfg_attr(
        all(feature = "desktop-cef", not(target_os = "linux"), not(test)),
        allow(dead_code)
    )]
    UnsupportedDisplay,
    StorageFailed,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(tag = "status", rename_all = "kebab-case")]
pub(crate) enum ShortcutReplacementOutcome {
    Replaced { shortcut: StructuredShortcut },
    Unchanged { reason: ShortcutFailure },
    Cancelled,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum BackendError {
    Conflict,
    PermissionDenied,
    Failed,
}

trait ShortcutBackend {
    type Binding: Copy + PartialEq;

    fn binding(shortcut: &StructuredShortcut) -> Self::Binding;
    fn register(&mut self, binding: Self::Binding) -> Result<(), BackendError>;
    fn unregister(&mut self, binding: Self::Binding) -> Result<(), BackendError>;
    fn id(binding: Self::Binding) -> u32;
}

struct ShortcutCoordinator<B: ShortcutBackend> {
    backend: B,
    active: Option<(StructuredShortcut, B::Binding)>,
}

impl<B: ShortcutBackend> ShortcutCoordinator<B> {
    fn new(backend: B) -> Self {
        Self {
            backend,
            active: None,
        }
    }

    fn replace(&mut self, shortcut: StructuredShortcut) -> ShortcutReplacementOutcome {
        if let Err(reason) = shortcut.validate() {
            return ShortcutReplacementOutcome::Unchanged { reason };
        }

        let candidate = B::binding(&shortcut);
        if self
            .active
            .as_ref()
            .is_some_and(|(_, active)| *active == candidate)
        {
            return ShortcutReplacementOutcome::Replaced { shortcut };
        }

        if let Err(error) = self.backend.register(candidate) {
            return ShortcutReplacementOutcome::Unchanged {
                reason: map_backend_error(error),
            };
        }

        if let Some((_, previous)) = self.active.as_ref()
            && self.backend.unregister(*previous).is_err()
        {
            let _ = self.backend.unregister(candidate);
            return ShortcutReplacementOutcome::Unchanged {
                reason: ShortcutFailure::RegistrationFailed,
            };
        }

        self.active = Some((shortcut.clone(), candidate));
        ShortcutReplacementOutcome::Replaced { shortcut }
    }

    fn active_id(&self) -> Option<u32> {
        self.active.as_ref().map(|(_, binding)| B::id(*binding))
    }
}

const fn map_backend_error(error: BackendError) -> ShortcutFailure {
    match error {
        BackendError::Conflict => ShortcutFailure::Conflict,
        BackendError::PermissionDenied => ShortcutFailure::PermissionDenied,
        BackendError::Failed => ShortcutFailure::RegistrationFailed,
    }
}

#[cfg(feature = "desktop-cef")]
struct SendableGlobalHotKeyManager(GlobalHotKeyManager);

// SAFETY: the manager is created during Tauri setup and is accessed only from
// setup or synchronous Tauri commands, which execute on the main thread.
// The outer Mutex satisfies managed state's Sync requirement, but its contents
// must be Send even though the native manager remains main-thread-affine.
#[cfg(feature = "desktop-cef")]
unsafe impl Send for SendableGlobalHotKeyManager {}

#[cfg(feature = "desktop-cef")]
struct NativeShortcutBackend {
    manager: SendableGlobalHotKeyManager,
}

#[cfg(feature = "desktop-cef")]
impl ShortcutBackend for NativeShortcutBackend {
    type Binding = HotKey;

    fn binding(shortcut: &StructuredShortcut) -> Self::Binding {
        let mut modifiers = Modifiers::empty();
        for modifier in &shortcut.modifiers {
            modifiers |= match modifier {
                ShortcutModifier::Control => Modifiers::CONTROL,
                ShortcutModifier::Alt => Modifiers::ALT,
                ShortcutModifier::Shift => Modifiers::SHIFT,
                ShortcutModifier::Meta => Modifiers::SUPER,
            };
        }
        HotKey::new(Some(modifiers), native_key(shortcut.key))
    }

    fn register(&mut self, binding: Self::Binding) -> Result<(), BackendError> {
        self.manager
            .0
            .register(binding)
            .map_err(classify_native_error)
    }

    fn unregister(&mut self, binding: Self::Binding) -> Result<(), BackendError> {
        self.manager
            .0
            .unregister(binding)
            .map_err(classify_native_error)
    }

    fn id(binding: Self::Binding) -> u32 {
        binding.id()
    }
}

#[cfg(feature = "desktop-cef")]
fn classify_native_error(error: global_hotkey::Error) -> BackendError {
    match error {
        global_hotkey::Error::AlreadyRegistered(_) => BackendError::Conflict,
        global_hotkey::Error::OsError(error)
            if error.kind() == std::io::ErrorKind::PermissionDenied =>
        {
            BackendError::PermissionDenied
        }
        _ => BackendError::Failed,
    }
}

#[cfg(feature = "desktop-cef")]
const fn native_key(key: ShortcutKey) -> Code {
    match key {
        ShortcutKey::A => Code::KeyA,
        ShortcutKey::B => Code::KeyB,
        ShortcutKey::C => Code::KeyC,
        ShortcutKey::D => Code::KeyD,
        ShortcutKey::E => Code::KeyE,
        ShortcutKey::F => Code::KeyF,
        ShortcutKey::G => Code::KeyG,
        ShortcutKey::H => Code::KeyH,
        ShortcutKey::I => Code::KeyI,
        ShortcutKey::J => Code::KeyJ,
        ShortcutKey::K => Code::KeyK,
        ShortcutKey::L => Code::KeyL,
        ShortcutKey::M => Code::KeyM,
        ShortcutKey::N => Code::KeyN,
        ShortcutKey::O => Code::KeyO,
        ShortcutKey::P => Code::KeyP,
        ShortcutKey::Q => Code::KeyQ,
        ShortcutKey::R => Code::KeyR,
        ShortcutKey::S => Code::KeyS,
        ShortcutKey::T => Code::KeyT,
        ShortcutKey::U => Code::KeyU,
        ShortcutKey::V => Code::KeyV,
        ShortcutKey::W => Code::KeyW,
        ShortcutKey::X => Code::KeyX,
        ShortcutKey::Y => Code::KeyY,
        ShortcutKey::Z => Code::KeyZ,
        ShortcutKey::Digit0 => Code::Digit0,
        ShortcutKey::Digit1 => Code::Digit1,
        ShortcutKey::Digit2 => Code::Digit2,
        ShortcutKey::Digit3 => Code::Digit3,
        ShortcutKey::Digit4 => Code::Digit4,
        ShortcutKey::Digit5 => Code::Digit5,
        ShortcutKey::Digit6 => Code::Digit6,
        ShortcutKey::Digit7 => Code::Digit7,
        ShortcutKey::Digit8 => Code::Digit8,
        ShortcutKey::Digit9 => Code::Digit9,
        ShortcutKey::F1 => Code::F1,
        ShortcutKey::F2 => Code::F2,
        ShortcutKey::F3 => Code::F3,
        ShortcutKey::F4 => Code::F4,
        ShortcutKey::F5 => Code::F5,
        ShortcutKey::F6 => Code::F6,
        ShortcutKey::F7 => Code::F7,
        ShortcutKey::F8 => Code::F8,
        ShortcutKey::F9 => Code::F9,
        ShortcutKey::F10 => Code::F10,
        ShortcutKey::F11 => Code::F11,
        ShortcutKey::F12 => Code::F12,
        ShortcutKey::Space => Code::Space,
        ShortcutKey::Enter => Code::Enter,
    }
}

#[cfg(feature = "desktop-cef")]
pub(crate) struct ShortcutState {
    coordinator: Option<ShortcutCoordinator<NativeShortcutBackend>>,
    unavailable: Option<ShortcutFailure>,
    restoration_failure: Option<ShortcutFailure>,
}

#[cfg(feature = "desktop-cef")]
impl ShortcutState {
    pub(crate) fn initialize(restored: Option<StructuredShortcut>) -> Self {
        let has_restored_shortcut = restored.is_some();
        #[cfg(target_os = "linux")]
        if std::env::var_os("DISPLAY").is_none_or(|display| display.is_empty()) {
            return Self {
                coordinator: None,
                unavailable: Some(ShortcutFailure::UnsupportedDisplay),
                restoration_failure: has_restored_shortcut
                    .then_some(ShortcutFailure::UnsupportedDisplay),
            };
        }

        match GlobalHotKeyManager::new() {
            Ok(manager) => {
                let backend = NativeShortcutBackend {
                    manager: SendableGlobalHotKeyManager(manager),
                };
                let mut coordinator = ShortcutCoordinator::new(backend);
                let mut restoration_failure = None;
                if let Some(shortcut) = restored
                    && shortcut.validate().is_ok()
                {
                    let binding = NativeShortcutBackend::binding(&shortcut);
                    match coordinator.backend.manager.0.register(binding) {
                        Ok(()) => coordinator.active = Some((shortcut, binding)),
                        Err(error) => {
                            restoration_failure =
                                Some(map_backend_error(classify_native_error(error)));
                        }
                    }
                }
                Self {
                    coordinator: Some(coordinator),
                    unavailable: None,
                    restoration_failure,
                }
            }
            Err(error) => {
                let failure = match classify_native_error(error) {
                    BackendError::PermissionDenied => ShortcutFailure::PermissionDenied,
                    _ => ShortcutFailure::RegistrationFailed,
                };
                Self {
                    coordinator: None,
                    unavailable: Some(failure),
                    restoration_failure: has_restored_shortcut.then_some(failure),
                }
            }
        }
    }

    pub(crate) fn replace(
        &mut self,
        candidate: Option<serde_json::Value>,
    ) -> ShortcutReplacementOutcome {
        let Some(candidate) = candidate else {
            return ShortcutReplacementOutcome::Cancelled;
        };
        let shortcut = match serde_json::from_value::<StructuredShortcut>(candidate) {
            Ok(shortcut) => shortcut,
            Err(_) => {
                return ShortcutReplacementOutcome::Unchanged {
                    reason: ShortcutFailure::Malformed,
                };
            }
        };
        let outcome = match &mut self.coordinator {
            Some(coordinator) => coordinator.replace(shortcut),
            None => ShortcutReplacementOutcome::Unchanged {
                reason: self
                    .unavailable
                    .unwrap_or(ShortcutFailure::RegistrationFailed),
            },
        };
        if matches!(outcome, ShortcutReplacementOutcome::Replaced { .. }) {
            self.restoration_failure = None;
        }
        outcome
    }

    pub(crate) fn active_id(&self) -> Option<u32> {
        self.coordinator
            .as_ref()
            .and_then(ShortcutCoordinator::active_id)
    }

    pub(crate) fn active_shortcut(&self) -> Option<StructuredShortcut> {
        self.coordinator
            .as_ref()
            .and_then(|coordinator| coordinator.active.as_ref())
            .map(|(shortcut, _)| shortcut.clone())
    }

    pub(crate) const fn restoration_failure(&self) -> Option<ShortcutFailure> {
        self.restoration_failure
    }

    pub(crate) fn rollback(&mut self, previous: Option<StructuredShortcut>) {
        let Some(coordinator) = &mut self.coordinator else {
            return;
        };
        if let Some(previous) = previous {
            let _ = coordinator.replace(previous);
        } else if let Some((_, active)) = coordinator.active.as_ref()
            && coordinator.backend.unregister(*active).is_ok()
        {
            coordinator.active = None;
        }
    }
}

#[cfg(test)]
mod tests {
    use std::collections::VecDeque;

    use super::*;

    #[derive(Default)]
    struct FakeBackend {
        register_results: VecDeque<Result<(), BackendError>>,
        unregister_results: VecDeque<Result<(), BackendError>>,
        registered: Vec<u32>,
    }

    impl ShortcutBackend for FakeBackend {
        type Binding = u32;

        fn binding(shortcut: &StructuredShortcut) -> Self::Binding {
            shortcut.key as u32
        }

        fn register(&mut self, binding: Self::Binding) -> Result<(), BackendError> {
            let result = self.register_results.pop_front().unwrap_or(Ok(()));
            if result.is_ok() {
                self.registered.push(binding);
            }
            result
        }

        fn unregister(&mut self, binding: Self::Binding) -> Result<(), BackendError> {
            let result = self.unregister_results.pop_front().unwrap_or(Ok(()));
            if result.is_ok() {
                self.registered.retain(|registered| *registered != binding);
            }
            result
        }

        fn id(binding: Self::Binding) -> u32 {
            binding
        }
    }

    fn shortcut(key: ShortcutKey) -> StructuredShortcut {
        StructuredShortcut {
            modifiers: vec![ShortcutModifier::Control, ShortcutModifier::Shift],
            key,
        }
    }

    #[test]
    fn invalid_and_conflicting_replacements_preserve_the_working_binding() {
        let mut coordinator = ShortcutCoordinator::new(FakeBackend::default());
        assert!(matches!(
            coordinator.replace(shortcut(ShortcutKey::K)),
            ShortcutReplacementOutcome::Replaced { .. }
        ));
        let working_id = coordinator.active_id();

        assert_eq!(
            coordinator.replace(StructuredShortcut {
                modifiers: vec![],
                key: ShortcutKey::P,
            }),
            ShortcutReplacementOutcome::Unchanged {
                reason: ShortcutFailure::Malformed
            }
        );
        coordinator
            .backend
            .register_results
            .push_back(Err(BackendError::Conflict));
        assert_eq!(
            coordinator.replace(shortcut(ShortcutKey::P)),
            ShortcutReplacementOutcome::Unchanged {
                reason: ShortcutFailure::Conflict
            }
        );
        assert_eq!(coordinator.active_id(), working_id);
        assert_eq!(coordinator.backend.registered, vec![working_id.unwrap()]);
    }

    #[test]
    fn permission_and_registration_failures_preserve_the_working_binding() {
        for failure in [BackendError::PermissionDenied, BackendError::Failed] {
            let mut backend = FakeBackend::default();
            backend.register_results.push_back(Ok(()));
            backend.register_results.push_back(Err(failure));
            let mut coordinator = ShortcutCoordinator::new(backend);
            coordinator.replace(shortcut(ShortcutKey::K));
            let working_id = coordinator.active_id();

            let outcome = coordinator.replace(shortcut(ShortcutKey::P));
            assert!(matches!(
                outcome,
                ShortcutReplacementOutcome::Unchanged { .. }
            ));
            assert_eq!(coordinator.active_id(), working_id);
        }
    }

    #[test]
    fn failed_old_binding_removal_rolls_back_the_new_binding() {
        let mut coordinator = ShortcutCoordinator::new(FakeBackend::default());
        coordinator.replace(shortcut(ShortcutKey::K));
        let working_id = coordinator.active_id().unwrap();
        coordinator
            .backend
            .unregister_results
            .push_back(Err(BackendError::Failed));

        assert_eq!(
            coordinator.replace(shortcut(ShortcutKey::P)),
            ShortcutReplacementOutcome::Unchanged {
                reason: ShortcutFailure::RegistrationFailed
            }
        );
        assert_eq!(coordinator.active_id(), Some(working_id));
        assert_eq!(coordinator.backend.registered, vec![working_id]);
    }

    #[test]
    fn terminal_failures_have_stable_serialized_outcomes() {
        assert_eq!(
            serde_json::to_value(ShortcutReplacementOutcome::Cancelled).unwrap(),
            serde_json::json!({ "status": "cancelled" })
        );
        assert_eq!(
            serde_json::to_value(ShortcutReplacementOutcome::Unchanged {
                reason: ShortcutFailure::UnsupportedDisplay,
            })
            .unwrap(),
            serde_json::json!({
                "status": "unchanged",
                "reason": "unsupported-display"
            })
        );
        assert_eq!(
            serde_json::to_value(ShortcutReplacementOutcome::Unchanged {
                reason: ShortcutFailure::StorageFailed,
            })
            .unwrap(),
            serde_json::json!({
                "status": "unchanged",
                "reason": "storage-failed"
            })
        );
    }
}
