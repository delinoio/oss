use std::collections::{BTreeMap, BTreeSet};

#[cfg(feature = "desktop-cef")]
use global_hotkey::{
    GlobalHotKeyManager,
    hotkey::{Code, HotKey, Modifiers},
};
use serde::{Deserialize, Serialize};

pub(crate) const MAX_DECK_SHORTCUT_DEFINITIONS: usize = 20;
pub(crate) const MAX_REALQA_SHORTCUT_DEFINITIONS: usize = 20;

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
    Replaced {
        shortcut: StructuredShortcut,
    },
    Unchanged {
        reason: ShortcutFailure,
        #[serde(skip_serializing_if = "Option::is_none")]
        shortcut: Option<StructuredShortcut>,
    },
    Cancelled,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum BackendError {
    Conflict,
    PermissionDenied,
    Failed,
}

/// Typed feature identity used only inside the local registration coordinator.
/// Account and definition strings are opaque identifiers: no diagnostic or
/// serialized outcome includes them.
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord)]
enum ShortcutOwner {
    DevHud,
    Deck {
        account: String,
        view: String,
    },
    #[cfg_attr(not(test), allow(dead_code))]
    RealQa {
        preset: String,
    },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
enum FeatureShortcutOutcome {
    Active,
    InactiveConflict,
    InactiveUnavailable,
    InactiveLimitExceeded,
    Unchanged(ShortcutFailure),
}

trait ShortcutBackend {
    type Binding: Copy + PartialEq;

    fn binding(shortcut: &StructuredShortcut) -> Self::Binding;
    fn register(&mut self, binding: Self::Binding) -> Result<(), BackendError>;
    fn unregister(&mut self, binding: Self::Binding) -> Result<(), BackendError>;
    fn id(binding: Self::Binding) -> u32;
}

#[cfg(test)]
struct ShortcutCoordinator<B: ShortcutBackend> {
    backend: B,
    active: Option<(StructuredShortcut, B::Binding)>,
    pending_cleanup: Vec<B::Binding>,
}

#[cfg(test)]
impl<B: ShortcutBackend> ShortcutCoordinator<B> {
    fn new(backend: B) -> Self {
        Self {
            backend,
            active: None,
            pending_cleanup: Vec::new(),
        }
    }

    fn replace(&mut self, shortcut: StructuredShortcut) -> ShortcutReplacementOutcome {
        if let Err(reason) = shortcut.validate() {
            return ShortcutReplacementOutcome::Unchanged {
                reason,
                shortcut: None,
            };
        }

        if let Err(reason) = self.cleanup_pending() {
            return ShortcutReplacementOutcome::Unchanged {
                reason,
                shortcut: None,
            };
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
                shortcut: None,
            };
        }

        if let Some((_, previous)) = self.active.as_ref()
            && self.backend.unregister(*previous).is_err()
        {
            if let Err(error) = self.backend.unregister(candidate) {
                self.pending_cleanup.push(candidate);
                return ShortcutReplacementOutcome::Unchanged {
                    reason: map_backend_error(error),
                    shortcut: None,
                };
            }
            return ShortcutReplacementOutcome::Unchanged {
                reason: ShortcutFailure::RegistrationFailed,
                shortcut: None,
            };
        }

        self.active = Some((shortcut.clone(), candidate));
        ShortcutReplacementOutcome::Replaced { shortcut }
    }

    fn active_id(&self) -> Option<u32> {
        self.active.as_ref().map(|(_, binding)| B::id(*binding))
    }

    fn clear(&mut self) -> Result<(), ShortcutFailure> {
        self.cleanup_pending()?;
        let Some((_, binding)) = self.active.as_ref() else {
            return Ok(());
        };
        self.backend
            .unregister(*binding)
            .map_err(map_backend_error)?;
        self.active = None;
        Ok(())
    }

    fn cleanup_pending(&mut self) -> Result<(), ShortcutFailure> {
        while let Some(binding) = self.pending_cleanup.pop() {
            if let Err(error) = self.backend.unregister(binding) {
                self.pending_cleanup.push(binding);
                return Err(map_backend_error(error));
            }
        }
        Ok(())
    }
}

/// One device-local registry for generic DevHud and future feature bindings.
/// It intentionally stores effective registrations only; synchronizable Deck
/// definitions and device-scoped RealQA presets remain feature-owned data.
struct UnifiedShortcutRegistry<B: ShortcutBackend> {
    backend: B,
    active: BTreeMap<ShortcutOwner, (StructuredShortcut, B::Binding)>,
    inactive: BTreeSet<ShortcutOwner>,
    pending_cleanup: Vec<B::Binding>,
}

impl<B: ShortcutBackend> UnifiedShortcutRegistry<B> {
    fn new(backend: B) -> Self {
        Self {
            backend,
            active: BTreeMap::new(),
            inactive: BTreeSet::new(),
            pending_cleanup: Vec::new(),
        }
    }

    fn apply(
        &mut self,
        owner: ShortcutOwner,
        shortcut: StructuredShortcut,
        available: bool,
    ) -> FeatureShortcutOutcome {
        if let Err(reason) = self.cleanup_pending() {
            return FeatureShortcutOutcome::Unchanged(reason);
        }
        if let Err(reason) = shortcut.validate() {
            return FeatureShortcutOutcome::Unchanged(reason);
        }
        if !available && owner != ShortcutOwner::DevHud {
            if let Some((_, binding)) = self.active.get(&owner)
                && let Err(error) = self.backend.unregister(*binding)
            {
                return FeatureShortcutOutcome::Unchanged(map_backend_error(error));
            }
            self.active.remove(&owner);
            self.inactive.insert(owner);
            return FeatureShortcutOutcome::InactiveUnavailable;
        }
        if !self.within_limit(&owner) {
            self.inactive.insert(owner);
            return FeatureShortcutOutcome::InactiveLimitExceeded;
        }
        let candidate = B::binding(&shortcut);
        if self
            .active
            .iter()
            .any(|(existing_owner, (_, binding))| existing_owner != &owner && *binding == candidate)
        {
            // The generic binding is transactional user configuration. A Deck
            // conflict must not remove the user's still-working shortcut.
            if owner == ShortcutOwner::DevHud {
                return FeatureShortcutOutcome::InactiveConflict;
            }
            if let Some((_, binding)) = self.active.get(&owner)
                && let Err(error) = self.backend.unregister(*binding)
            {
                return FeatureShortcutOutcome::Unchanged(map_backend_error(error));
            }
            self.active.remove(&owner);
            self.inactive.insert(owner);
            return FeatureShortcutOutcome::InactiveConflict;
        }
        if let Some((_, active)) = self.active.get(&owner)
            && *active == candidate
        {
            self.inactive.remove(&owner);
            return FeatureShortcutOutcome::Active;
        }
        // Register before removing a working binding. A platform conflict,
        // permission denial, or concurrent registration therefore preserves it.
        if let Err(error) = self.backend.register(candidate) {
            return FeatureShortcutOutcome::Unchanged(map_backend_error(error));
        }
        if let Some((_, previous)) = self.active.get(&owner)
            && self.backend.unregister(*previous).is_err()
        {
            if self.backend.unregister(candidate).is_err() {
                self.pending_cleanup.push(candidate);
            }
            return FeatureShortcutOutcome::Unchanged(ShortcutFailure::RegistrationFailed);
        }
        self.active.insert(owner.clone(), (shortcut, candidate));
        self.inactive.remove(&owner);
        FeatureShortcutOutcome::Active
    }

    fn clear_all(&mut self) -> Result<(), ShortcutFailure> {
        self.cleanup_pending()?;
        // Preserve entries whose removal fails so process shutdown or reset can
        // retry without claiming that an OS registration is gone.
        let owners: Vec<_> = self.active.keys().cloned().collect();
        for owner in owners {
            let binding = self.active[&owner].1;
            self.backend
                .unregister(binding)
                .map_err(map_backend_error)?;
            self.active.remove(&owner);
        }
        self.inactive.clear();
        Ok(())
    }

    fn remove_owner(&mut self, owner: &ShortcutOwner) -> Result<(), ShortcutFailure> {
        self.cleanup_pending()?;
        if let Some((_, binding)) = self.active.get(owner) {
            self.backend
                .unregister(*binding)
                .map_err(map_backend_error)?;
            self.active.remove(owner);
        }
        self.inactive.remove(owner);
        Ok(())
    }

    fn logout_account(&mut self, account: &str) -> Result<(), ShortcutFailure> {
        self.cleanup_pending()?;
        let owners: Vec<_> = self
            .active
            .keys()
            .filter_map(|owner| match owner {
                ShortcutOwner::Deck {
                    account: owner_account,
                    ..
                } if owner_account == account => Some(owner.clone()),
                _ => None,
            })
            .collect();
        for owner in owners {
            let binding = self.active[&owner].1;
            self.backend
                .unregister(binding)
                .map_err(map_backend_error)?;
            self.active.remove(&owner);
        }
        self.inactive.retain(|owner| !matches!(owner, ShortcutOwner::Deck { account: owner_account, .. } if owner_account == account));
        Ok(())
    }

    fn synchronize_deck(
        &mut self,
        account_id: String,
        definitions: Vec<DeckShortcutDefinition>,
    ) -> Vec<DeckShortcutRegistration> {
        debug_assert!(
            definitions
                .iter()
                .all(|definition| definition.account_id == account_id)
        );
        let retained = definitions
            .iter()
            .map(|definition| ShortcutOwner::Deck {
                account: account_id.clone(),
                view: definition.view_id.clone(),
            })
            .collect::<BTreeSet<_>>();
        let mut registrations = definitions
            .iter()
            .map(|definition| {
                let owner = ShortcutOwner::Deck {
                    account: account_id.clone(),
                    view: definition.view_id.clone(),
                };
                DeckShortcutRegistration {
                    view_id: definition.view_id.clone(),
                    outcome: self.apply(owner, definition.shortcut.clone(), true),
                }
            })
            .collect::<Vec<_>>();

        let stale = self
            .active
            .keys()
            .chain(self.inactive.iter())
            .filter(|owner| {
                matches!(owner, ShortcutOwner::Deck { account, .. } if account == &account_id)
                    && !retained.contains(*owner)
            })
            .cloned()
            .collect::<BTreeSet<_>>();
        for owner in stale {
            if let Err(reason) = self.remove_owner(&owner) {
                tracing::warn!(?reason, "failed to remove a stale Deck shortcut");
                break;
            }
        }

        // A new definition can initially conflict with a stale owner or exceed
        // the limit until stale owners have been removed. Retry only those
        // outcomes; registration failures retain the owner's working binding.
        for (definition, registration) in definitions.iter().zip(&mut registrations) {
            if matches!(
                registration.outcome,
                FeatureShortcutOutcome::InactiveConflict
                    | FeatureShortcutOutcome::InactiveLimitExceeded
            ) {
                registration.outcome = self.apply(
                    ShortcutOwner::Deck {
                        account: account_id.clone(),
                        view: definition.view_id.clone(),
                    },
                    definition.shortcut.clone(),
                    true,
                );
            }
        }
        registrations
    }

    fn within_limit(&self, owner: &ShortcutOwner) -> bool {
        match owner {
            ShortcutOwner::DevHud => true,
            ShortcutOwner::Deck { account, .. } => self.active.keys().filter(|candidate| matches!(candidate, ShortcutOwner::Deck { account: candidate_account, .. } if candidate_account == account)).count() < MAX_DECK_SHORTCUT_DEFINITIONS || self.active.contains_key(owner),
            ShortcutOwner::RealQa { .. } => self.active.keys().filter(|candidate| matches!(candidate, ShortcutOwner::RealQa { .. })).count() < MAX_REALQA_SHORTCUT_DEFINITIONS || self.active.contains_key(owner),
        }
    }

    fn cleanup_pending(&mut self) -> Result<(), ShortcutFailure> {
        while let Some(binding) = self.pending_cleanup.pop() {
            if let Err(error) = self.backend.unregister(binding) {
                self.pending_cleanup.push(binding);
                return Err(map_backend_error(error));
            }
        }
        Ok(())
    }
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct DeckShortcutDefinition {
    account_id: String,
    view_id: String,
    shortcut: StructuredShortcut,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct DeckShortcutRegistration {
    view_id: String,
    outcome: FeatureShortcutOutcome,
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
    registry: Option<UnifiedShortcutRegistry<NativeShortcutBackend>>,
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
                registry: None,
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
                let mut registry = UnifiedShortcutRegistry::new(backend);
                let mut restoration_failure = None;
                if let Some(shortcut) = restored
                    && shortcut.validate().is_ok()
                {
                    match registry.apply(ShortcutOwner::DevHud, shortcut, true) {
                        FeatureShortcutOutcome::Active => {}
                        FeatureShortcutOutcome::Unchanged(reason) => {
                            restoration_failure = Some(reason);
                        }
                        FeatureShortcutOutcome::InactiveConflict => {
                            restoration_failure = Some(ShortcutFailure::Conflict);
                        }
                        FeatureShortcutOutcome::InactiveUnavailable
                        | FeatureShortcutOutcome::InactiveLimitExceeded => {
                            restoration_failure = Some(ShortcutFailure::RegistrationFailed);
                        }
                    }
                }
                Self {
                    registry: Some(registry),
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
                    registry: None,
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
                    shortcut: None,
                };
            }
        };
        let outcome = match &mut self.registry {
            Some(registry) => match registry.apply(ShortcutOwner::DevHud, shortcut.clone(), true) {
                FeatureShortcutOutcome::Active => ShortcutReplacementOutcome::Replaced { shortcut },
                FeatureShortcutOutcome::InactiveConflict => ShortcutReplacementOutcome::Unchanged {
                    reason: ShortcutFailure::Conflict,
                    shortcut: None,
                },
                FeatureShortcutOutcome::InactiveUnavailable
                | FeatureShortcutOutcome::InactiveLimitExceeded => {
                    ShortcutReplacementOutcome::Unchanged {
                        reason: ShortcutFailure::RegistrationFailed,
                        shortcut: None,
                    }
                }
                FeatureShortcutOutcome::Unchanged(reason) => {
                    ShortcutReplacementOutcome::Unchanged {
                        reason,
                        shortcut: None,
                    }
                }
            },
            None => ShortcutReplacementOutcome::Unchanged {
                reason: self
                    .unavailable
                    .unwrap_or(ShortcutFailure::RegistrationFailed),
                shortcut: None,
            },
        };
        if matches!(outcome, ShortcutReplacementOutcome::Replaced { .. }) {
            self.restoration_failure = None;
        }
        outcome
    }

    pub(crate) fn active_id(&self) -> Option<u32> {
        self.registry
            .as_ref()?
            .active
            .get(&ShortcutOwner::DevHud)
            .map(|(_, binding)| NativeShortcutBackend::id(*binding))
    }

    pub(crate) fn active_shortcut(&self) -> Option<StructuredShortcut> {
        self.registry
            .as_ref()?
            .active
            .get(&ShortcutOwner::DevHud)
            .map(|(shortcut, _)| shortcut.clone())
    }

    pub(crate) const fn restoration_failure(&self) -> Option<ShortcutFailure> {
        self.restoration_failure
    }

    pub(crate) fn clear(&mut self) -> Result<(), ShortcutFailure> {
        if let Some(registry) = &mut self.registry {
            registry.clear_all()?;
        }
        self.restoration_failure = None;
        Ok(())
    }

    pub(crate) fn rollback(
        &mut self,
        previous: Option<StructuredShortcut>,
    ) -> Result<(), ShortcutFailure> {
        match previous {
            Some(previous) => match self.replace(Some(
                serde_json::to_value(previous).map_err(|_| ShortcutFailure::Malformed)?,
            )) {
                ShortcutReplacementOutcome::Replaced { .. } => Ok(()),
                ShortcutReplacementOutcome::Unchanged { reason, .. } => Err(reason),
                ShortcutReplacementOutcome::Cancelled => Err(ShortcutFailure::RegistrationFailed),
            },
            None => match &mut self.registry {
                Some(registry) => registry.remove_owner(&ShortcutOwner::DevHud),
                None => Ok(()),
            },
        }
    }

    pub(crate) fn synchronize_deck(
        &mut self,
        account_id: String,
        definitions: Vec<DeckShortcutDefinition>,
    ) -> Vec<DeckShortcutRegistration> {
        let mut definitions = definitions;
        definitions.retain(|definition| definition.account_id == account_id);
        definitions.sort_by(|left, right| left.view_id.cmp(&right.view_id));
        definitions.dedup_by(|left, right| left.view_id == right.view_id);
        let Some(registry) = &mut self.registry else {
            return definitions
                .into_iter()
                .map(|definition| DeckShortcutRegistration {
                    view_id: definition.view_id,
                    outcome: FeatureShortcutOutcome::InactiveUnavailable,
                })
                .collect();
        };
        registry.synchronize_deck(account_id, definitions)
    }

    pub(crate) fn deck_view_for_id(&self, id: u32) -> Option<&str> {
        self.registry
            .as_ref()?
            .active
            .iter()
            .find_map(|(owner, (_, binding))| {
                if NativeShortcutBackend::id(*binding) != id {
                    return None;
                }
                match owner {
                    ShortcutOwner::Deck { view, .. } => Some(view.as_str()),
                    _ => None,
                }
            })
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
                reason: ShortcutFailure::Malformed,
                shortcut: None,
            }
        );
        coordinator
            .backend
            .register_results
            .push_back(Err(BackendError::Conflict));
        assert_eq!(
            coordinator.replace(shortcut(ShortcutKey::P)),
            ShortcutReplacementOutcome::Unchanged {
                reason: ShortcutFailure::Conflict,
                shortcut: None,
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
                reason: ShortcutFailure::RegistrationFailed,
                shortcut: None,
            }
        );
        assert_eq!(coordinator.active_id(), Some(working_id));
        assert_eq!(coordinator.backend.registered, vec![working_id]);
    }

    #[test]
    fn failed_candidate_cleanup_is_tracked_and_retried() {
        let mut coordinator = ShortcutCoordinator::new(FakeBackend::default());
        coordinator.replace(shortcut(ShortcutKey::K));
        let working_id = coordinator.active_id().unwrap();
        coordinator.backend.unregister_results.extend([
            Err(BackendError::Failed),
            Err(BackendError::PermissionDenied),
        ]);

        assert_eq!(
            coordinator.replace(shortcut(ShortcutKey::P)),
            ShortcutReplacementOutcome::Unchanged {
                reason: ShortcutFailure::PermissionDenied,
                shortcut: None,
            }
        );
        assert_eq!(coordinator.active_id(), Some(working_id));
        assert_eq!(coordinator.pending_cleanup.len(), 1);
        assert_eq!(coordinator.backend.registered.len(), 2);

        assert_eq!(
            coordinator.replace(shortcut(ShortcutKey::P)),
            ShortcutReplacementOutcome::Replaced {
                shortcut: shortcut(ShortcutKey::P),
            }
        );
        assert!(coordinator.pending_cleanup.is_empty());
        assert_eq!(
            coordinator.backend.registered,
            vec![coordinator.active_id().unwrap()]
        );
    }

    #[test]
    fn clearing_a_binding_unregisters_it_and_preserves_it_on_failure() {
        let mut coordinator = ShortcutCoordinator::new(FakeBackend::default());
        coordinator.replace(shortcut(ShortcutKey::K));
        let working_id = coordinator.active_id().unwrap();

        coordinator
            .backend
            .unregister_results
            .push_back(Err(BackendError::PermissionDenied));
        assert_eq!(coordinator.clear(), Err(ShortcutFailure::PermissionDenied));
        assert_eq!(coordinator.active_id(), Some(working_id));

        assert_eq!(coordinator.clear(), Ok(()));
        assert_eq!(coordinator.active_id(), None);
        assert!(coordinator.backend.registered.is_empty());
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
                shortcut: None,
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
                shortcut: None,
            })
            .unwrap(),
            serde_json::json!({
                "status": "unchanged",
                "reason": "storage-failed"
            })
        );
        assert_eq!(
            serde_json::to_value(ShortcutReplacementOutcome::Unchanged {
                reason: ShortcutFailure::StorageFailed,
                shortcut: Some(shortcut(ShortcutKey::P)),
            })
            .unwrap(),
            serde_json::json!({
                "status": "unchanged",
                "reason": "storage-failed",
                "shortcut": {
                    "modifiers": ["control", "shift"],
                    "key": "p"
                }
            })
        );
    }

    #[test]
    fn unified_registry_keeps_conflicts_inactive_without_replacing_a_binding() {
        let mut registry = UnifiedShortcutRegistry::new(FakeBackend::default());
        assert_eq!(
            registry.apply(ShortcutOwner::DevHud, shortcut(ShortcutKey::K), true),
            FeatureShortcutOutcome::Active
        );
        assert_eq!(
            registry.apply(
                ShortcutOwner::Deck {
                    account: "account-a".into(),
                    view: "view-a".into()
                },
                shortcut(ShortcutKey::K),
                true,
            ),
            FeatureShortcutOutcome::InactiveConflict
        );
        assert_eq!(registry.active.len(), 1);
        assert!(
            registry
                .inactive
                .iter()
                .any(|owner| matches!(owner, ShortcutOwner::Deck { .. }))
        );
    }

    #[test]
    fn unified_registry_enforces_limits_and_account_logout_isolated() {
        let mut registry = UnifiedShortcutRegistry::new(FakeBackend::default());
        for index in 0..MAX_DECK_SHORTCUT_DEFINITIONS {
            let key = match index {
                0 => ShortcutKey::A,
                1 => ShortcutKey::B,
                2 => ShortcutKey::C,
                3 => ShortcutKey::D,
                4 => ShortcutKey::E,
                5 => ShortcutKey::F,
                6 => ShortcutKey::G,
                7 => ShortcutKey::H,
                8 => ShortcutKey::I,
                9 => ShortcutKey::J,
                10 => ShortcutKey::K,
                11 => ShortcutKey::L,
                12 => ShortcutKey::M,
                13 => ShortcutKey::N,
                14 => ShortcutKey::O,
                15 => ShortcutKey::P,
                16 => ShortcutKey::Q,
                17 => ShortcutKey::R,
                18 => ShortcutKey::S,
                _ => ShortcutKey::T,
            };
            assert_eq!(
                registry.apply(
                    ShortcutOwner::Deck {
                        account: "account-a".into(),
                        view: format!("view-{index}")
                    },
                    shortcut(key),
                    true
                ),
                FeatureShortcutOutcome::Active
            );
        }
        assert_eq!(
            registry.apply(
                ShortcutOwner::Deck {
                    account: "account-a".into(),
                    view: "extra".into()
                },
                shortcut(ShortcutKey::U),
                true
            ),
            FeatureShortcutOutcome::InactiveLimitExceeded
        );
        assert_eq!(
            registry.apply(
                ShortcutOwner::Deck {
                    account: "account-b".into(),
                    view: "view".into()
                },
                shortcut(ShortcutKey::V),
                true
            ),
            FeatureShortcutOutcome::Active
        );
        assert_eq!(registry.logout_account("account-a"), Ok(()));
        assert_eq!(registry.active.len(), 1);
        assert!(
            matches!(registry.active.keys().next(), Some(ShortcutOwner::Deck { account, .. }) if account == "account-b")
        );
    }

    #[test]
    fn deck_synchronization_preserves_working_bindings_on_reapply_failure() {
        let mut registry = UnifiedShortcutRegistry::new(FakeBackend::default());
        let owner = ShortcutOwner::Deck {
            account: "account".into(),
            view: "view".into(),
        };
        assert_eq!(
            registry.apply(owner.clone(), shortcut(ShortcutKey::K), true),
            FeatureShortcutOutcome::Active
        );
        registry
            .backend
            .register_results
            .push_back(Err(BackendError::Failed));

        let registrations = registry.synchronize_deck(
            "account".into(),
            vec![DeckShortcutDefinition {
                account_id: "account".into(),
                view_id: "view".into(),
                shortcut: shortcut(ShortcutKey::P),
            }],
        );

        assert!(matches!(
            registrations.as_slice(),
            [DeckShortcutRegistration {
                outcome: FeatureShortcutOutcome::Unchanged(ShortcutFailure::RegistrationFailed),
                ..
            }]
        ));
        assert_eq!(registry.active[&owner].0, shortcut(ShortcutKey::K));
        assert_eq!(registry.backend.registered, vec![ShortcutKey::K as u32]);
    }

    #[test]
    fn deck_synchronization_removes_only_stale_owners_before_retrying_conflicts() {
        let mut registry = UnifiedShortcutRegistry::new(FakeBackend::default());
        let stale = ShortcutOwner::Deck {
            account: "account".into(),
            view: "stale".into(),
        };
        let current = ShortcutOwner::Deck {
            account: "account".into(),
            view: "current".into(),
        };
        assert_eq!(
            registry.apply(stale.clone(), shortcut(ShortcutKey::K), true),
            FeatureShortcutOutcome::Active
        );

        let registrations = registry.synchronize_deck(
            "account".into(),
            vec![DeckShortcutDefinition {
                account_id: "account".into(),
                view_id: "current".into(),
                shortcut: shortcut(ShortcutKey::K),
            }],
        );

        assert!(matches!(
            registrations.as_slice(),
            [DeckShortcutRegistration {
                outcome: FeatureShortcutOutcome::Active,
                ..
            }]
        ));
        assert!(!registry.active.contains_key(&stale));
        assert_eq!(registry.active[&current].0, shortcut(ShortcutKey::K));
    }

    #[test]
    fn unavailable_features_are_inactive_and_reset_cleans_all_registered_bindings() {
        let mut registry = UnifiedShortcutRegistry::new(FakeBackend::default());
        assert_eq!(
            registry.apply(ShortcutOwner::DevHud, shortcut(ShortcutKey::K), true),
            FeatureShortcutOutcome::Active
        );
        assert_eq!(
            registry.apply(
                ShortcutOwner::RealQa {
                    preset: "preset".into()
                },
                shortcut(ShortcutKey::P),
                false
            ),
            FeatureShortcutOutcome::InactiveUnavailable
        );
        assert_eq!(registry.clear_all(), Ok(()));
        assert!(registry.active.is_empty());
        assert!(registry.inactive.is_empty());
    }

    #[test]
    fn unavailable_feature_unregisters_its_existing_binding() {
        let mut registry = UnifiedShortcutRegistry::new(FakeBackend::default());
        let owner = ShortcutOwner::Deck {
            account: "account".into(),
            view: "view".into(),
        };
        assert_eq!(
            registry.apply(owner.clone(), shortcut(ShortcutKey::K), true),
            FeatureShortcutOutcome::Active
        );
        assert_eq!(
            registry.apply(owner.clone(), shortcut(ShortcutKey::K), false),
            FeatureShortcutOutcome::InactiveUnavailable
        );
        assert!(!registry.active.contains_key(&owner));
        assert!(registry.inactive.contains(&owner));
    }

    #[test]
    fn conflicting_feature_update_unregisters_its_previous_binding() {
        let mut registry = UnifiedShortcutRegistry::new(FakeBackend::default());
        let devhud = ShortcutOwner::DevHud;
        let deck = ShortcutOwner::Deck {
            account: "account".into(),
            view: "view".into(),
        };
        assert_eq!(
            registry.apply(devhud, shortcut(ShortcutKey::K), true),
            FeatureShortcutOutcome::Active
        );
        assert_eq!(
            registry.apply(deck.clone(), shortcut(ShortcutKey::P), true),
            FeatureShortcutOutcome::Active
        );
        assert_eq!(
            registry.apply(deck.clone(), shortcut(ShortcutKey::K), true),
            FeatureShortcutOutcome::InactiveConflict
        );
        assert!(!registry.active.contains_key(&deck));
        assert!(registry.inactive.contains(&deck));
    }

    #[test]
    fn deck_conflict_never_removes_the_existing_generic_binding() {
        let mut registry = UnifiedShortcutRegistry::new(FakeBackend::default());
        let deck = ShortcutOwner::Deck {
            account: "account".into(),
            view: "view".into(),
        };
        assert_eq!(
            registry.apply(deck, shortcut(ShortcutKey::K), true),
            FeatureShortcutOutcome::Active
        );
        assert_eq!(
            registry.apply(ShortcutOwner::DevHud, shortcut(ShortcutKey::P), true),
            FeatureShortcutOutcome::Active
        );
        assert_eq!(
            registry.apply(ShortcutOwner::DevHud, shortcut(ShortcutKey::K), true),
            FeatureShortcutOutcome::InactiveConflict
        );
        assert_eq!(
            registry.active[&ShortcutOwner::DevHud].0,
            shortcut(ShortcutKey::P)
        );
    }
}
