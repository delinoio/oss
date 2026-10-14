// SPDX-License-Identifier: Apache-2.0
//! Window-owned callback infrastructure. Go alone creates PKCE, exchanges the
//! code, and owns account/credential state. No callback is broadcast or saved.
use std::{
    collections::BTreeMap,
    io::{Read, Write},
    net::{Ipv4Addr, Ipv6Addr, TcpListener, TcpStream},
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread::{self, JoinHandle},
    time::{Duration, Instant},
};

use serde::{Deserialize, Serialize};
use zeroize::{Zeroize, Zeroizing};

use crate::{NativeFailure, canonical_id};

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum OAuthAction {
    Begin,
    BeginHuggingFace,
    BeginGoogleGemini,
    BeginBaseten,
    Profiles,
    BindOpen,
    SubscriptionOpen,
    SubscriptionReopen,
    ClaudeSubscriptionOpen,
    ClaudeSubscriptionReopen,
    GrokSubscriptionOpen,
    GrokSubscriptionReopen,
    // Stateless: opens the validated Grok device verification page in the
    // system default browser. It binds no listener and records no attempt.
    GrokDeviceAuthorizationOpen,
    Reopen,
    Take,
    Dispose,
}
#[derive(Default, Serialize)]
pub struct OAuthResult {
    pub generation: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub state: Option<Vec<u8>>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub profiles: Vec<OAuthProfile>,
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub denied: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub callback_url: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub code: Option<Vec<u8>>,
}
#[derive(Clone, Copy, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum OAuthProfile {
    #[default]
    Openrouter,
    HuggingFace,
    GoogleGemini,
    Baseten,
}

fn registered_client(profile: OAuthProfile) -> Option<String> {
    if profile == OAuthProfile::Openrouter {
        return Some(String::new());
    }
    let registrations: serde_json::Value = serde_json::from_str(include_str!(
        "../../../../cmds/delidev-cli/internal/providers/oauth_clients.json"
    ))
    .ok()?;
    let (key, redirect) = match profile {
        OAuthProfile::HuggingFace => (
            "hugging-face",
            "http://localhost/oauth/hugging-face/callback",
        ),
        OAuthProfile::GoogleGemini => ("gemini", "http://127.0.0.1/oauth/google-gemini/callback"),
        OAuthProfile::Baseten => ("baseten", ""),
        OAuthProfile::Openrouter => return Some(String::new()),
    };
    let registration = registrations.get(key)?;
    let id = registration.get("client_id")?.as_str()?;
    (registration.get("registration")?.as_str()? == "registered"
        && registration.get("api_compatibility")?.as_str()? == "accepted"
        && registration.get("redirect_uri")?.as_str()? == redirect
        && (profile != OAuthProfile::Baseten || registered_verification_uri().is_some())
        && !id.is_empty()
        && !id.chars().any(char::is_control)
        && id.len() <= 256)
        .then(|| id.to_owned())
}

fn valid_device_authorization(raw: &str) -> bool {
    let Ok(u) = url::Url::parse(raw) else {
        return false;
    };
    u.scheme() == "https"
        && u.host_str() == Some("app.baseten.co")
        && u.port().is_none()
        && u.username().is_empty()
        && u.password().is_none()
        && u.path() != "/"
        && u.query().is_none()
        && u.fragment().is_none()
        && u.as_str() == raw
        && !raw.contains("..")
}
fn registered_verification_uri() -> Option<String> {
    let v: serde_json::Value = serde_json::from_str(include_str!(
        "../../../../cmds/delidev-cli/internal/providers/oauth_clients.json"
    ))
    .ok()?;
    let raw = v.get("baseten")?.get("verification_uri")?.as_str()?;
    valid_device_authorization(raw).then(|| raw.to_owned())
}
fn validate_device_authorization(raw: &str, registered: &str) -> Result<(), NativeFailure> {
    if raw != registered || !valid_device_authorization(raw) {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(())
}
impl Drop for OAuthResult {
    fn drop(&mut self) {
        if let Some(state) = &mut self.state {
            state.zeroize();
        }
        if let Some(code) = &mut self.code {
            code.zeroize();
        }
    }
}
#[derive(Clone, PartialEq, Eq, PartialOrd, Ord)]
pub struct OAuthScope {
    pub window: String,
    pub instance: String,
    pub server: String,
    pub opening: String,
    pub window_epoch: u64,
}
struct Shared {
    expected_state: Option<Zeroizing<String>>,
    api_state: Mutex<Option<Zeroizing<String>>>,
    api_profile: Option<OAuthProfile>,
    bound: AtomicBool,
    consumed: AtomicBool,
    stop: AtomicBool,
    code: Mutex<Option<Zeroizing<Vec<u8>>>>,
}
#[derive(Clone, Copy, PartialEq, Eq)]
enum BrowserBindingKind {
    Provider,
    CodexSubscription,
    ClaudeSubscription,
    GrokSubscription,
}
struct Attempt {
    kind: BrowserBindingKind,
    profile: OAuthProfile,
    scope: OAuthScope,
    generation: String,
    callback: String,
    attempt: String,
    authorization: Zeroizing<String>,
    until: Instant,
    shared: Arc<Shared>,
    thread: Option<JoinHandle<()>>,
}
impl Drop for Attempt {
    fn drop(&mut self) {
        self.shared.stop.store(true, Ordering::Release);
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
        if let Ok(mut code) = self.shared.code.lock() {
            *code = None;
        }
    }
}
// One admitted Grok device verification dispatch. It owns a cancellation flag
// that only retirement of its own scope (Dispose), its window (close), or the
// whole host (stop/exhaustion) can set, so a dispatch that was admitted just
// before a Settings screen closed still observes that closure.
struct DeviceOpening {
    scope: OAuthScope,
    cancel: Arc<AtomicBool>,
}
// Unregisters an admitted device opening when its dispatch settles, whether the
// opener succeeded, failed or panicked.
struct DeviceOpeningGuard<'a> {
    host: &'a OAuthHost,
    cancel: Arc<AtomicBool>,
}
impl Drop for DeviceOpeningGuard<'_> {
    fn drop(&mut self) {
        self.host
            .device_openings
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .retain(|opening| !Arc::ptr_eq(&opening.cancel, &self.cancel));
    }
}
#[derive(Default)]
pub struct OAuthHost {
    disposed: Mutex<std::collections::BTreeSet<OAuthScope>>,
    attempts: Mutex<BTreeMap<String, Attempt>>,
    // Lock order: `attempts` is always taken before `device_openings`, and
    // `device_openings` is never held while taking another lock.
    device_openings: Mutex<Vec<DeviceOpening>>,
    epochs: Mutex<BTreeMap<String, u64>>,
    stopped: AtomicBool,
    // Remote Grok login needs the Go server to validate and forward the captured
    // Grok callback. It does not yet, so production leaves this false and a
    // remote Grok open is refused before any listener or browser exists.
    grok_remote_forwarding: bool,
    // Deterministic unit scheduling after pre-admission and before attempts.
    #[cfg(test)]
    admission_barrier: Mutex<Option<Arc<std::sync::Barrier>>>,
}

impl OAuthHost {
    // Cancels every admitted device opening selected by `retired`. A poisoned
    // registry must still deliver cancellation, so poisoning is ignored.
    fn cancel_device_openings(&self, retired: impl Fn(&OAuthScope) -> bool) {
        for opening in self
            .device_openings
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .iter()
            .filter(|opening| retired(&opening.scope))
        {
            opening.cancel.store(true, Ordering::Release);
        }
    }

    fn exhausted(&self, attempts: &mut BTreeMap<String, Attempt>) -> NativeFailure {
        self.stopped.store(true, Ordering::Release);
        self.cancel_device_openings(|_| true);
        // Retained revocations are never evicted. Stop and join every original
        // listener so capacity failure cannot leave active callback authority.
        attempts.clear();
        tracing::warn!(
            operation = "account_oauth_callback",
            state = "admission-closed",
            code = "revocation-capacity"
        );
        NativeFailure::Stopped
    }

    fn epoch(
        &self,
        window: &str,
        attempts: &mut BTreeMap<String, Attempt>,
    ) -> Result<u64, NativeFailure> {
        if self.stopped.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        let mut epochs = self.epochs.lock().map_err(|_| NativeFailure::Busy)?;
        if !epochs.contains_key(window) && epochs.len() >= 4096 {
            return Err(self.exhausted(attempts));
        }
        Ok(*epochs.entry(window.into()).or_default())
    }

    fn remember_disposed(
        &self,
        scope: OAuthScope,
        attempts: &mut BTreeMap<String, Attempt>,
    ) -> Result<(), NativeFailure> {
        let mut disposed = self.disposed.lock().map_err(|_| NativeFailure::Busy)?;
        if !disposed.contains(&scope) && disposed.len() >= 4096 {
            return Err(self.exhausted(attempts));
        }
        disposed.insert(scope);
        Ok(())
    }

    pub fn window_epoch(&self, window: &str) -> Result<u64, NativeFailure> {
        let mut attempts = self.attempts.lock().map_err(|_| NativeFailure::Busy)?;
        self.epoch(window, &mut attempts)
    }

    pub fn close_window(&self, window: &str) -> Result<(), NativeFailure> {
        // Joining a listener never waits for CEF or a business operation.
        let mut attempts = self.attempts.lock().map_err(|_| NativeFailure::Busy)?;
        let epoch = self.epoch(window, &mut attempts)?;
        let next = epoch
            .checked_add(1)
            .ok_or_else(|| self.exhausted(&mut attempts))?;
        // The epoch advances under the attempts lock, so a device opening is
        // either already registered here or is refused as a stale epoch.
        self.cancel_device_openings(|scope| scope.window == window);
        *self
            .epochs
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .get_mut(window)
            .ok_or(NativeFailure::InvalidEvidence)? = next;
        if let Some(original) = attempts.remove(window) {
            self.remember_disposed(original.scope.clone(), &mut attempts)?;
        }
        Ok(())
    }

    pub fn stop(&self) {
        self.stopped.store(true, Ordering::Release);
        if let Ok(mut attempts) = self.attempts.lock() {
            attempts.clear();
        }
        self.cancel_device_openings(|_| true);
    }

    pub fn control(
        &self,
        scope: OAuthScope,
        action: OAuthAction,
        generation: &str,
        attempt_id: &str,
        authorization: &str,
    ) -> Result<OAuthResult, NativeFailure> {
        self.control_with_opener(
            scope,
            action,
            generation,
            attempt_id,
            authorization,
            open_authorization,
        )
    }

    pub fn control_subscription(
        &self,
        scope: OAuthScope,
        action: OAuthAction,
        generation: &str,
        attempt_id: &str,
        authorization: &str,
        local: bool,
    ) -> Result<OAuthResult, NativeFailure> {
        self.control_with_browser(
            scope,
            action,
            generation,
            attempt_id,
            authorization,
            local,
            open_authorization,
        )
    }

    fn control_with_opener(
        &self,
        scope: OAuthScope,
        action: OAuthAction,
        generation: &str,
        attempt_id: &str,
        authorization: &str,
        opener: impl FnOnce(&str, &AtomicBool) -> Result<(), NativeFailure>,
    ) -> Result<OAuthResult, NativeFailure> {
        self.control_with_browser(
            scope,
            action,
            generation,
            attempt_id,
            authorization,
            true,
            opener,
        )
    }

    #[allow(clippy::too_many_arguments)]
    fn control_with_browser(
        &self,
        scope: OAuthScope,
        action: OAuthAction,
        generation: &str,
        attempt_id: &str,
        authorization: &str,
        local: bool,
        opener: impl FnOnce(&str, &AtomicBool) -> Result<(), NativeFailure>,
    ) -> Result<OAuthResult, NativeFailure> {
        if self.stopped.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        canonical_id(&scope.server)?;
        canonical_id(&scope.opening)?;
        #[cfg(test)]
        {
            let barrier = self.admission_barrier.lock().unwrap().take();
            if let Some(barrier) = barrier {
                barrier.wait();
            }
        }
        let mut attempts = self.attempts.lock().map_err(|_| NativeFailure::Busy)?;
        // Admission may stop while this action waits. Dispose deliberately
        // skips stale-epoch equality, so it also needs this shared
        // post-lock fence.
        if self.stopped.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        if action != OAuthAction::Dispose
            && self.epoch(&scope.window, &mut attempts)? != scope.window_epoch
        {
            return Err(NativeFailure::Stopped);
        }
        let expired: Vec<_> = attempts
            .values()
            .filter(|v| Instant::now() >= v.until)
            .map(|v| v.scope.clone())
            .collect();
        for expired in expired {
            self.remember_disposed(expired.clone(), &mut attempts)?;
            attempts.remove(&expired.window);
        }
        if action == OAuthAction::GrokDeviceAuthorizationOpen {
            // Device sign-in has no PKCE listener, callback or attempt. The
            // only native authority is opening the fixed xAI verification page
            // through the OAuth dispatcher, which always uses the operating
            // system default browser and ignores the General/Local choices.
            canonical_id(attempt_id)?;
            if !generation.is_empty() {
                return Err(NativeFailure::InvalidInput);
            }
            if self
                .disposed
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .contains(&scope)
            {
                return Err(NativeFailure::Stopped);
            }
            validate_grok_device_authorization(authorization)?;
            let url = Zeroizing::new(authorization.to_string());
            // The attempts lock is held from the disposed check through
            // registration, and Dispose, window close and stop hold it while
            // retiring a scope. Every dispatch is therefore either refused
            // above or registered here and cancelled by the retirement that
            // follows it.
            let cancel = Arc::new(AtomicBool::new(false));
            self.device_openings
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .push(DeviceOpening {
                    scope,
                    cancel: Arc::clone(&cancel),
                });
            let _guard = DeviceOpeningGuard {
                host: self,
                cancel: Arc::clone(&cancel),
            };
            drop(attempts);
            // The dispatcher checks this per-opening flag immediately before it
            // starts the browser process and while it waits for it.
            opener(&url, &cancel)?;
            return Ok(OAuthResult::default());
        }
        if matches!(
            action,
            OAuthAction::SubscriptionOpen
                | OAuthAction::SubscriptionReopen
                | OAuthAction::ClaudeSubscriptionOpen
                | OAuthAction::ClaudeSubscriptionReopen
                | OAuthAction::GrokSubscriptionOpen
                | OAuthAction::GrokSubscriptionReopen
        ) {
            let kind = match action {
                OAuthAction::ClaudeSubscriptionOpen | OAuthAction::ClaudeSubscriptionReopen => {
                    BrowserBindingKind::ClaudeSubscription
                }
                OAuthAction::GrokSubscriptionOpen | OAuthAction::GrokSubscriptionReopen => {
                    BrowserBindingKind::GrokSubscription
                }
                _ => BrowserBindingKind::CodexSubscription,
            };
            canonical_id(attempt_id)?;
            if self
                .disposed
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .contains(&scope)
            {
                return Err(NativeFailure::Stopped);
            }
            if let Some(original) = attempts.get(&scope.window) {
                if original.scope != scope
                    || original.attempt != attempt_id
                    || original.authorization.as_str() != authorization
                    || !generation.is_empty() && original.generation != generation
                    || original.kind != kind
                    || kind != BrowserBindingKind::ClaudeSubscription
                        && original.shared.expected_state.is_none()
                {
                    return Err(NativeFailure::InvalidEvidence);
                }
                // Exact initial binding replay never opens another browser.
                // Deliberate reopen recovers a lost binding reply and opens
                // this same original address exactly once.
                if matches!(
                    action,
                    OAuthAction::SubscriptionReopen
                        | OAuthAction::ClaudeSubscriptionReopen
                        | OAuthAction::GrokSubscriptionReopen
                ) {
                    let shared = Arc::clone(&original.shared);
                    let url = Zeroizing::new(original.authorization.to_string());
                    let generation = original.generation.clone();
                    drop(attempts);
                    opener(&url, &shared.stop)?;
                    return Ok(OAuthResult {
                        generation,
                        callback_url: String::new(),
                        code: None,
                        state: None,
                        profiles: Vec::new(),
                        denied: false,
                    });
                }
                return Ok(OAuthResult {
                    generation: original.generation.clone(),
                    callback_url: String::new(),
                    code: None,
                    state: None,
                    profiles: Vec::new(),
                    denied: false,
                });
            }
            if !generation.is_empty()
                || attempts.len() >= 32
                || self.disposed.lock().map_err(|_| NativeFailure::Busy)?.len() >= 4096
            {
                return Err(NativeFailure::Busy);
            }
            let original = match kind {
                BrowserBindingKind::ClaudeSubscription => {
                    begin_claude_subscription(scope, attempt_id, authorization)?
                }
                BrowserBindingKind::GrokSubscription => begin_grok_subscription(
                    scope,
                    attempt_id,
                    authorization,
                    local,
                    self.grok_remote_forwarding,
                )?,
                _ => begin_subscription(scope, attempt_id, authorization, local)?,
            };
            let result = OAuthResult {
                generation: original.generation.clone(),
                callback_url: String::new(),
                code: None,
                state: None,
                profiles: Vec::new(),
                denied: false,
            };
            let shared = Arc::clone(&original.shared);
            let url = Zeroizing::new(original.authorization.to_string());
            attempts.insert(original.scope.window.clone(), original);
            drop(attempts);
            opener(&url, &shared.stop)?;
            return Ok(result);
        }
        if action == OAuthAction::Profiles {
            return Ok(OAuthResult {
                profiles: [
                    OAuthProfile::Openrouter,
                    OAuthProfile::HuggingFace,
                    OAuthProfile::GoogleGemini,
                    OAuthProfile::Baseten,
                ]
                .into_iter()
                .filter(|p| registered_client(*p).is_some())
                .collect(),
                generation: String::new(),
                callback_url: String::new(),
                code: None,
                state: None,
                denied: false,
            });
        }
        if matches!(
            action,
            OAuthAction::Begin
                | OAuthAction::BeginHuggingFace
                | OAuthAction::BeginGoogleGemini
                | OAuthAction::BeginBaseten
        ) {
            let profile = if action == OAuthAction::Begin {
                OAuthProfile::Openrouter
            } else if action == OAuthAction::BeginHuggingFace {
                OAuthProfile::HuggingFace
            } else if action == OAuthAction::BeginGoogleGemini {
                OAuthProfile::GoogleGemini
            } else {
                OAuthProfile::Baseten
            };
            if registered_client(profile).is_none() {
                return Err(NativeFailure::InvalidEvidence);
            }
            let disposed = self.disposed.lock().map_err(|_| NativeFailure::Busy)?;
            if disposed.len() >= 4096 || disposed.contains(&scope) {
                return Err(NativeFailure::Stopped);
            }
            drop(disposed);
            if !generation.is_empty() || !attempt_id.is_empty() || !authorization.is_empty() {
                return Err(NativeFailure::InvalidInput);
            }
            if let Some(original) = attempts.get(&scope.window) {
                if original.scope == scope
                    && original.profile == profile
                    && original.kind == BrowserBindingKind::Provider
                {
                    return Ok(OAuthResult {
                        generation: original.generation.clone(),
                        callback_url: original.callback.clone(),
                        code: None,
                        state: None,
                        profiles: Vec::new(),
                        denied: false,
                    });
                }
                return Err(NativeFailure::Busy);
            }
            if attempts.len() >= 32 {
                return Err(NativeFailure::Busy);
            }
            let attempt = begin_profile(scope, profile)?;
            let result = OAuthResult {
                generation: attempt.generation.clone(),
                callback_url: attempt.callback.clone(),
                code: None,
                state: None,
                profiles: Vec::new(),
                denied: false,
            };
            attempts.insert(attempt.scope.window.clone(), attempt);
            tracing::info!(
                operation = "account_oauth_callback",
                phase = "listener-created"
            );
            return Ok(result);
        }
        // Disposal is idempotent only for the exact old generation. A late
        // Settings cleanup cannot close a replacement listener.
        if action == OAuthAction::Dispose {
            // Retire admitted device dispatches first so a disposal that fails
            // on capacity still cancels them.
            self.cancel_device_openings(|opening| *opening == scope);
            self.remember_disposed(scope.clone(), &mut attempts)?;
            if let Some(original) = attempts.get(&scope.window)
                && original.scope == scope
                && (generation.is_empty() || original.generation == generation)
            {
                attempts.remove(&scope.window);
            }
            return Ok(OAuthResult {
                generation: generation.into(),
                callback_url: String::new(),
                code: None,
                state: None,
                profiles: Vec::new(),
                denied: false,
            });
        }
        let original = attempts
            .get_mut(&scope.window)
            .ok_or(NativeFailure::InvalidEvidence)?;
        if original.scope != scope || original.generation != generation {
            return Err(NativeFailure::InvalidEvidence);
        }
        match action {
            OAuthAction::BindOpen => {
                if original.kind != BrowserBindingKind::Provider {
                    return Err(NativeFailure::InvalidEvidence);
                }
                canonical_id(attempt_id)?;
                if !original.attempt.is_empty() {
                    if original.attempt != attempt_id
                        || original.authorization.as_str() != authorization
                    {
                        return Err(NativeFailure::InvalidEvidence);
                    }
                    // Exact binding replay settles pre/post-admission
                    // uncertainty without dispatching
                    // another browser. Reopen remains explicit.
                    return Ok(OAuthResult {
                        generation: generation.into(),
                        callback_url: String::new(),
                        code: None,
                        state: None,
                        profiles: Vec::new(),
                        denied: false,
                    });
                }
                if original.profile == OAuthProfile::Openrouter {
                    validate_authorization(authorization, &original.callback)?;
                } else if original.profile == OAuthProfile::Baseten {
                    validate_device_authorization(
                        authorization,
                        &registered_verification_uri().ok_or(NativeFailure::InvalidEvidence)?,
                    )?;
                } else {
                    let client = registered_client(original.profile)
                        .ok_or(NativeFailure::InvalidEvidence)?;
                    let state = validate_public_authorization(
                        authorization,
                        &original.callback,
                        &client,
                        original.profile,
                    )?;
                    *original
                        .shared
                        .api_state
                        .lock()
                        .map_err(|_| NativeFailure::Busy)? = Some(state);
                }
                original.attempt = attempt_id.into();
                original.authorization = Zeroizing::new(authorization.into());
                original.shared.bound.store(true, Ordering::Release);
                // Original binding stays retained after an uncertain opener
                // result; only deliberate Reopen may dispatch it again.
            }
            OAuthAction::Reopen => {
                if original.attempt != attempt_id
                    || !authorization.is_empty()
                    || original.shared.consumed.load(Ordering::Acquire)
                {
                    return Err(NativeFailure::InvalidEvidence);
                }
            }
            OAuthAction::Take => {
                if original.profile == OAuthProfile::Baseten
                    || original.kind == BrowserBindingKind::ClaudeSubscription
                {
                    return Err(NativeFailure::InvalidEvidence);
                }
                if original.attempt != attempt_id || !authorization.is_empty() {
                    return Err(NativeFailure::InvalidEvidence);
                }
                let code = original
                    .shared
                    .code
                    .lock()
                    .map_err(|_| NativeFailure::Busy)?
                    .take();
                if original.profile != OAuthProfile::Openrouter {
                    let mut result = OAuthResult {
                        generation: generation.into(),
                        callback_url: String::new(),
                        code: None,
                        state: None,
                        profiles: Vec::new(),
                        denied: false,
                    };
                    if let Some(value) = code {
                        let query = std::str::from_utf8(&value)
                            .map_err(|_| NativeFailure::InvalidEvidence)?;
                        for part in query.split('&') {
                            let (name, raw) =
                                part.split_once('=').ok_or(NativeFailure::InvalidEvidence)?;
                            if name == "code" {
                                result.code = Some(
                                    decode_code(raw)
                                        .ok_or(NativeFailure::InvalidEvidence)?
                                        .to_vec(),
                                );
                            }
                            if name == "state" {
                                result.state = Some(
                                    decode_code(raw)
                                        .ok_or(NativeFailure::InvalidEvidence)?
                                        .to_vec(),
                                );
                            }
                            if name == "error" {
                                result.denied = true;
                            }
                        }
                    }
                    return Ok(result);
                }
                return Ok(OAuthResult {
                    generation: generation.into(),
                    callback_url: String::new(),
                    code: code.map(|mut value| {
                        let result = value.to_vec();
                        value.zeroize();
                        result
                    }),
                    state: None,
                    profiles: Vec::new(),
                    denied: false,
                });
            }
            _ => return Err(NativeFailure::InvalidInput),
        }
        let url = Zeroizing::new(original.authorization.to_string());
        let shared = Arc::clone(&original.shared);
        drop(attempts);
        if shared.stop.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        opener(&url, &shared.stop)?;
        Ok(OAuthResult {
            generation: generation.into(),
            callback_url: String::new(),
            code: None,
            state: None,
            profiles: Vec::new(),
            denied: false,
        })
    }
}
impl Drop for OAuthHost {
    fn drop(&mut self) {
        self.stop();
    }
}

fn begin_profile(scope: OAuthScope, profile: OAuthProfile) -> Result<Attempt, NativeFailure> {
    if profile == OAuthProfile::Baseten {
        return Ok(Attempt {
            kind: BrowserBindingKind::Provider,
            profile,
            scope,
            generation: uuid::Uuid::now_v7().to_string(),
            callback: String::new(),
            attempt: String::new(),
            authorization: Zeroizing::new(String::new()),
            until: Instant::now() + Duration::from_secs(600),
            shared: Arc::new(Shared {
                expected_state: None,
                api_state: Mutex::new(None),
                api_profile: Some(profile),
                bound: AtomicBool::new(false),
                consumed: AtomicBool::new(false),
                stop: AtomicBool::new(false),
                code: Mutex::new(None),
            }),
            thread: None,
        });
    }
    // Both families use the same ephemeral port. Never bind a wildcard or
    // silently omit one family: localhost resolver choice cannot change scope.
    let mut sockets = None;
    for _ in 0..16 {
        let v4 = TcpListener::bind((Ipv4Addr::LOCALHOST, 0))
            .map_err(|_| NativeFailure::SidecarFailed)?;
        let port = v4
            .local_addr()
            .map_err(|_| NativeFailure::SidecarFailed)?
            .port();
        if let Ok(v6) = TcpListener::bind((Ipv6Addr::LOCALHOST, port)) {
            sockets = Some((v4, v6, port));
            break;
        }
    }
    let (v4, v6, port) = sockets.ok_or(NativeFailure::SidecarFailed)?;
    v4.set_nonblocking(true)
        .map_err(|_| NativeFailure::SidecarFailed)?;
    v6.set_nonblocking(true)
        .map_err(|_| NativeFailure::SidecarFailed)?;
    // Each UUID-v7 has fresh cryptographic random bits. Their concatenation
    // supplies an unpredictable 32-byte path without a persistent identifier.
    let path = if profile == OAuthProfile::GoogleGemini {
        "/oauth/google-gemini/callback".into()
    } else if profile == OAuthProfile::HuggingFace {
        "/oauth/hugging-face/callback".into()
    } else {
        format!(
            "/oauth/openrouter/{}{}",
            uuid::Uuid::now_v7().simple(),
            uuid::Uuid::now_v7().simple()
        )
    };
    let host_name = if profile == OAuthProfile::GoogleGemini {
        "127.0.0.1"
    } else {
        "localhost"
    };
    let callback = format!("http://{host_name}:{port}{path}");
    let generation = uuid::Uuid::now_v7().to_string();
    let shared = Arc::new(Shared {
        expected_state: None,
        api_state: Mutex::new(None),
        api_profile: (profile != OAuthProfile::Openrouter).then_some(profile),
        bound: AtomicBool::new(false),
        consumed: AtomicBool::new(false),
        stop: AtomicBool::new(false),
        code: Mutex::new(None),
    });
    let control = Arc::clone(&shared);
    let until = Instant::now() + Duration::from_secs(600);
    let handle = thread::Builder::new()
        .name("delidev-oauth-callback".into())
        .spawn(move || {
            let host = format!("{host_name}:{port}");
            while !control.stop.load(Ordering::Acquire) && Instant::now() < until {
                for listener in [&v4, &v6] {
                    if let Ok((mut stream, peer)) = listener.accept() {
                        if !peer.ip().is_loopback() {
                            continue;
                        }
                        handle_request(&mut stream, &host, &path, &control, until);
                    }
                }
                thread::sleep(Duration::from_millis(10));
            }
        })
        .map_err(|_| NativeFailure::SidecarFailed)?;
    Ok(Attempt {
        kind: BrowserBindingKind::Provider,
        profile,
        scope,
        generation,
        callback,
        attempt: String::new(),
        authorization: Zeroizing::new(String::new()),
        until,
        shared,
        thread: Some(handle),
    })
}
fn valid_code(code: &[u8]) -> bool {
    (1..=8192).contains(&code.len())
        && std::str::from_utf8(code).is_ok_and(|v| !v.chars().any(char::is_control))
}
fn decode_code(raw: &str) -> Option<Zeroizing<Vec<u8>>> {
    let mut result = Zeroizing::new(Vec::new());
    let input = raw.as_bytes();
    let mut index = 0;
    while index < input.len() {
        match input[index] {
            b'%' => {
                let digits = input.get(index + 1..index + 3)?;
                let hex = std::str::from_utf8(digits).ok()?;
                result.push(u8::from_str_radix(hex, 16).ok()?);
                index += 3
            }
            b'+' => {
                result.push(b' ');
                index += 1
            }
            value => {
                result.push(value);
                index += 1
            }
        }
    }
    valid_code(&result).then_some(result)
}
#[cfg(test)]
fn parse_request(raw: &[u8], host: &str, path: &str) -> Option<Option<Zeroizing<Vec<u8>>>> {
    parse_request_mode(raw, host, path, None)
}
#[cfg(test)]
fn parse_request_mode(
    raw: &[u8],
    host: &str,
    path: &str,
    state: Option<&str>,
) -> Option<Option<Zeroizing<Vec<u8>>>> {
    parse_request_profile(raw, host, path, state, None)
}
fn parse_request_profile(
    raw: &[u8],
    host: &str,
    path: &str,
    state: Option<&str>,
    api: Option<OAuthProfile>,
) -> Option<Option<Zeroizing<Vec<u8>>>> {
    let text = std::str::from_utf8(raw).ok()?;
    let (first, headers) = text.split_once("\r\n")?;
    let mut parts = first.split(' ');
    if parts.next() != Some("GET") {
        return None;
    }
    let target = parts.next()?;
    if target.len() > 16 << 10 || parts.next() != Some("HTTP/1.1") || parts.next().is_some() {
        return None;
    }
    if headers.len() > 16 << 10 || !headers.ends_with("\r\n\r\n") {
        return None;
    }
    let mut hosts = 0;
    for line in headers[..headers.len() - 4].split("\r\n") {
        let (name, value) = line.split_once(':')?;
        if !name.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-')
            || value.chars().any(|c| c.is_control() && c != '\t')
        {
            return None;
        }
        if name.eq_ignore_ascii_case("host") {
            hosts += 1;
            if value.trim() != host {
                return None;
            }
        }
        // The callback has no body. Refuse request smuggling/ambiguous framing.
        if state.is_some() && name.eq_ignore_ascii_case("origin")
            || name.eq_ignore_ascii_case("transfer-encoding")
            || name.eq_ignore_ascii_case("content-length") && value.trim() != "0"
        {
            return None;
        }
    }
    if hosts != 1 {
        return None;
    }
    if target == format!("{path}/complete") {
        return Some(None);
    }
    let (request_path, query) = target.split_once('?')?;
    if request_path != path || query.contains('#') {
        return None;
    }
    if let Some(state) = state {
        if query.len() > 16 << 10 {
            return None;
        }
        let mut fields = BTreeMap::new();
        for part in query.split('&') {
            let (key, value) = part.split_once('=')?;
            if !(matches!(key, "code" | "state" | "scope")
                || api.is_some() && key == "error"
                || api == Some(OAuthProfile::GoogleGemini) && matches!(key, "authuser" | "prompt"))
                || fields.insert(key, decode_code(value)?).is_some()
            {
                return None;
            }
        }
        if api == Some(OAuthProfile::GoogleGemini)
            && (fields
                .get("authuser")
                .is_some_and(|v| v.len() > 2 || !v.iter().all(u8::is_ascii_digit))
                || fields
                    .get("prompt")
                    .is_some_and(|v| !matches!(v.as_slice(), b"consent" | b"none")))
        {
            return None;
        }
        let supplied = fields.get("state")?;
        if (!fields.contains_key("code")
            && !(api.is_some()
                && fields
                    .get("error")
                    .is_some_and(|v| v.as_slice() == b"access_denied")))
            || fields.contains_key("code") && fields.contains_key("error")
            || supplied.len() != state.len()
            || supplied
                .iter()
                .zip(state.as_bytes())
                .fold(0u8, |diff, (a, b)| diff | (*a ^ *b))
                != 0
        {
            return None;
        }
        return Some(Some(Zeroizing::new(query.as_bytes().to_vec())));
    }
    if query.contains('&') {
        return None;
    }
    let code = query.strip_prefix("code=")?;
    decode_code(code).map(Some)
}
// The one-shot claim of a callback. The deadline is rechecked here, immediately
// before the compare-and-swap, because the owning loop only evaluates it
// between connections.
fn claim_callback(shared: &Shared, deadline: Instant) -> bool {
    shared.bound.load(Ordering::Acquire)
        && !shared.stop.load(Ordering::Acquire)
        && Instant::now() < deadline
        && shared
            .consumed
            .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
            .is_ok()
}

// `deadline` is the owning attempt's expiry. A delivery that was accepted just
// before it can still be read for up to a second, so the handler reads no
// longer than the deadline and rechecks it immediately before it consumes the
// callback. An expired delivery is answered as a bad request and never stored.
fn handle_request(
    stream: &mut TcpStream,
    host: &str,
    path: &str,
    shared: &Shared,
    deadline: Instant,
) {
    let _ = stream.set_read_timeout(Some(Duration::from_millis(25)));
    let _ = stream.set_write_timeout(Some(Duration::from_millis(100)));
    let until = (Instant::now() + Duration::from_secs(1)).min(deadline);
    let mut raw = Zeroizing::new(Vec::new());
    let mut buffer = [0u8; 1024];
    while raw.len() <= 32 << 10 && Instant::now() < until && !shared.stop.load(Ordering::Acquire) {
        match stream.read(&mut buffer) {
            Ok(0) => break,
            Ok(count) => {
                raw.extend_from_slice(&buffer[..count]);
                buffer.zeroize();
                if raw.windows(4).any(|v| v == b"\r\n\r\n") {
                    break;
                }
            }
            Err(e)
                if e.kind() == std::io::ErrorKind::WouldBlock
                    || e.kind() == std::io::ErrorKind::TimedOut => {}
            Err(_) => break,
        }
    }
    buffer.zeroize();
    let api_state = shared.api_state.lock().ok();
    let state = api_state
        .as_ref()
        .and_then(|v| v.as_deref())
        .map(String::as_str);
    let parsed = parse_request_profile(
        &raw,
        host,
        path,
        state.or_else(|| shared.expected_state.as_deref().map(String::as_str)),
        shared.api_profile,
    );
    let common = "Cache-Control: no-store\r\nReferrer-Policy: \
                  no-referrer\r\nContent-Security-Policy: default-src 'none'; frame-ancestors \
                  'none'\r\nConnection: close\r\n";
    let response = match parsed {
        Some(Some(code)) if claim_callback(shared, deadline) => {
            if let Ok(mut slot) = shared.code.lock() {
                *slot = Some(code);
            }
            format!(
                "HTTP/1.1 303 See Other\r\n{common}Location: {path}/complete\r\nContent-Length: \
                 0\r\n\r\n"
            )
        }
        Some(None) if shared.consumed.load(Ordering::Acquire) => {
            let body = "<!doctype html><html lang=\"en\"><meta \
                        charset=\"utf-8\"><title>DeliDev</title><p>Authorization received. You \
                        can return to DeliDev.</p></html>";
            format!(
                "HTTP/1.1 200 OK\r\n{common}Content-Type: text/html; \
                 charset=utf-8\r\nContent-Length: {}\r\n\r\n{body}",
                body.len()
            )
        }
        _ => format!("HTTP/1.1 400 Bad Request\r\n{common}Content-Length: 0\r\n\r\n"),
    };
    raw.zeroize();
    let _ = stream.write_all(response.as_bytes());
}
pub fn validate_authorization(raw: &str, callback: &str) -> Result<(), NativeFailure> {
    if raw.len() > 4096 || raw.chars().any(char::is_control) {
        return Err(NativeFailure::InvalidInput);
    }
    let url = url::Url::parse(raw).map_err(|_| NativeFailure::InvalidInput)?;
    if url.scheme() != "https"
        || url.host_str() != Some("openrouter.ai")
        || url.port().is_some()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/auth"
        || url.fragment().is_some()
        || url.as_str() != raw
    {
        return Err(NativeFailure::InvalidInput);
    }
    let mut fields = BTreeMap::new();
    for (key, value) in url.query_pairs() {
        if fields
            .insert(key.into_owned(), value.into_owned())
            .is_some()
        {
            return Err(NativeFailure::InvalidInput);
        }
    }
    if fields.len() != 4
        || fields.get("callback_url").map(String::as_str) != Some(callback)
        || fields.get("key_label").map(String::as_str) != Some("DeliDev")
        || fields.get("code_challenge_method").map(String::as_str) != Some("S256")
        || fields.get("code_challenge").is_none_or(|v| {
            v.len() != 43
                || !v
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
        })
    {
        return Err(NativeFailure::InvalidInput);
    }
    Ok(())
}
#[cfg(test)]
fn validate_hugging_face_authorization(
    raw: &str,
    callback: &str,
    client: &str,
) -> Result<Zeroizing<String>, NativeFailure> {
    validate_public_authorization(raw, callback, client, OAuthProfile::HuggingFace)
}
fn validate_public_authorization(
    raw: &str,
    callback: &str,
    client: &str,
    profile: OAuthProfile,
) -> Result<Zeroizing<String>, NativeFailure> {
    if raw.len() > 4096 || raw.chars().any(char::is_control) {
        return Err(NativeFailure::InvalidInput);
    }
    let (host, path, scope, count) = match profile {
        OAuthProfile::HuggingFace => ("huggingface.co", "/oauth/authorize", "inference-api", 7),
        OAuthProfile::GoogleGemini => (
            "accounts.google.com",
            "/o/oauth2/v2/auth",
            "https://www.googleapis.com/auth/cloud-platform",
            9,
        ),
        OAuthProfile::Openrouter | OAuthProfile::Baseten => {
            return Err(NativeFailure::InvalidInput);
        }
    };
    let url = url::Url::parse(raw).map_err(|_| NativeFailure::InvalidInput)?;
    if url.scheme() != "https"
        || url.host_str() != Some(host)
        || url.path() != path
        || url.port().is_some()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.fragment().is_some()
        || url.as_str() != raw
    {
        return Err(NativeFailure::InvalidInput);
    }
    let mut fields = BTreeMap::new();
    for (key, value) in url.query_pairs() {
        if fields
            .insert(key.into_owned(), Zeroizing::new(value.into_owned()))
            .is_some()
        {
            return Err(NativeFailure::InvalidInput);
        }
    }
    let get = |name: &str| fields.get(name).map(|v| v.as_str());
    if fields.len() != count
        || profile == OAuthProfile::GoogleGemini
            && (get("access_type") != Some("offline") || get("prompt") != Some("consent"))
        || get("client_id") != Some(client)
        || get("redirect_uri") != Some(callback)
        || get("response_type") != Some("code")
        || get("scope") != Some(scope)
        || get("code_challenge_method") != Some("S256")
        || get("code_challenge").is_none_or(|v| {
            v.len() != 43
                || !v
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
        })
        || get("state").is_none_or(|v| {
            v.len() != 43
                || !v
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
        })
    {
        return Err(NativeFailure::InvalidInput);
    }
    fields.remove("state").ok_or(NativeFailure::InvalidInput)
}
fn open_authorization(url: &str, stopped: &AtomicBool) -> Result<(), NativeFailure> {
    crate::browser_opener::dispatch(url, stopped)
}

#[cfg(test)]
mod tests {
    use super::*;

    // Parallel OAuth tests can reuse an ephemeral callback port just after its
    // owner closes it, which makes post-disposal connection probes flaky.
    // Serialize only tests that create these loopback listeners.
    static LOOPBACK_LISTENER_TESTS: Mutex<()> = Mutex::new(());

    pub(super) fn loopback_listener_test_guard() -> std::sync::MutexGuard<'static, ()> {
        LOOPBACK_LISTENER_TESTS
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }

    fn scope() -> OAuthScope {
        OAuthScope {
            window: "main".into(),
            instance: "fixture-window".into(),
            server: uuid::Uuid::now_v7().to_string(),
            opening: uuid::Uuid::now_v7().to_string(),
            window_epoch: 0,
        }
    }
    fn inert_attempt(scope: OAuthScope, expired: bool) -> Attempt {
        Attempt {
            kind: BrowserBindingKind::Provider,
            profile: OAuthProfile::Openrouter,
            scope,
            generation: uuid::Uuid::now_v7().to_string(),
            callback: String::new(),
            attempt: String::new(),
            authorization: Zeroizing::new(String::new()),
            until: if expired {
                Instant::now() - Duration::from_secs(1)
            } else {
                Instant::now() + Duration::from_secs(600)
            },
            shared: Arc::new(Shared {
                expected_state: None,
                api_state: Mutex::new(None),
                api_profile: None,
                bound: AtomicBool::new(true),
                consumed: AtomicBool::new(false),
                stop: AtomicBool::new(false),
                code: Mutex::new(Some(Zeroizing::new(b"synthetic callback".to_vec()))),
            }),
            thread: None,
        }
    }
    fn fill_disposed(host: &OAuthHost) -> OAuthScope {
        let original = scope();
        for index in 0..4096 {
            let mut next = original.clone();
            if index != 0 {
                next.opening = uuid::Uuid::now_v7().to_string();
            }
            host.control(next, OAuthAction::Dispose, "", "", "")
                .unwrap();
        }
        original
    }
    #[test]
    fn queued_dispose_after_epoch_exhaustion_cannot_insert_or_expire() {
        let host = Arc::new(OAuthHost::default());
        let known = scope();
        host.disposed.lock().unwrap().insert(known.clone());
        for index in 0..4096 {
            host.epochs
                .lock()
                .unwrap()
                .insert(format!("window-{index}"), 0);
        }
        let pending = scope();
        let expired = inert_attempt(scope(), true);
        let callback = Arc::clone(&expired.shared);
        let mut attempts = host.attempts.lock().unwrap();
        attempts.insert(expired.scope.window.clone(), expired);
        let barrier = Arc::new(std::sync::Barrier::new(2));
        *host.admission_barrier.lock().unwrap() = Some(Arc::clone(&barrier));
        let waiter_host = Arc::clone(&host);
        let waiter_scope = pending.clone();
        let waiter = thread::spawn(move || {
            waiter_host.control(waiter_scope, OAuthAction::Dispose, "", "", "")
        });
        // The waiter has passed the initial stopped check and is now queued
        // behind this exact attempts guard, rather than relying on a sleep.
        barrier.wait();
        assert!(matches!(
            host.epoch("overflow", &mut attempts),
            Err(NativeFailure::Stopped)
        ));
        drop(attempts);
        assert!(matches!(
            waiter.join().unwrap(),
            Err(NativeFailure::Stopped)
        ));
        let disposed = host.disposed.lock().unwrap();
        assert_eq!(disposed.len(), 1);
        assert!(disposed.contains(&known));
        assert!(!disposed.contains(&pending));
        assert_eq!(host.epochs.lock().unwrap().len(), 4096);
        assert!(callback.stop.load(Ordering::Acquire));
        assert!(callback.code.lock().unwrap().is_none());
    }

    #[test]
    fn queued_actions_after_explicit_stop_cannot_insert_or_expire() {
        for action in [
            OAuthAction::Dispose,
            OAuthAction::Begin,
            OAuthAction::BeginHuggingFace,
            OAuthAction::BeginGoogleGemini,
            OAuthAction::BeginBaseten,
            OAuthAction::Profiles,
            OAuthAction::BindOpen,
            OAuthAction::Take,
            OAuthAction::Reopen,
            OAuthAction::SubscriptionOpen,
            OAuthAction::SubscriptionReopen,
            OAuthAction::ClaudeSubscriptionOpen,
            OAuthAction::ClaudeSubscriptionReopen,
            OAuthAction::GrokSubscriptionOpen,
            OAuthAction::GrokSubscriptionReopen,
            OAuthAction::GrokDeviceAuthorizationOpen,
        ] {
            let host = Arc::new(OAuthHost::default());
            let known = scope();
            host.disposed.lock().unwrap().insert(known.clone());
            let pending = scope();
            let expired = inert_attempt(scope(), true);
            let callback = Arc::clone(&expired.shared);
            let mut attempts = host.attempts.lock().unwrap();
            attempts.insert(expired.scope.window.clone(), expired);
            let barrier = Arc::new(std::sync::Barrier::new(2));
            *host.admission_barrier.lock().unwrap() = Some(Arc::clone(&barrier));
            let waiter_host = Arc::clone(&host);
            let waiter_scope = pending.clone();
            let waiter = thread::spawn(move || {
                waiter_host.control_with_opener(waiter_scope, action, "", "", "", |_, _| {
                    panic!("stopped action cannot open a browser")
                })
            });
            barrier.wait();
            let stopper_host = Arc::clone(&host);
            let stopper = thread::spawn(move || stopper_host.stop());
            let deadline = Instant::now() + Duration::from_secs(5);
            while !host.stopped.load(Ordering::Acquire) {
                assert!(Instant::now() < deadline, "explicit Stop did not latch");
                thread::yield_now();
            }
            drop(attempts);
            assert!(matches!(
                waiter.join().unwrap(),
                Err(NativeFailure::Stopped)
            ));
            stopper.join().unwrap();
            let disposed = host.disposed.lock().unwrap();
            assert_eq!(disposed.len(), 1);
            assert!(disposed.contains(&known));
            assert!(!disposed.contains(&pending));
            assert!(host.epochs.lock().unwrap().is_empty());
            assert!(host.attempts.lock().unwrap().is_empty());
            assert!(callback.stop.load(Ordering::Acquire));
            assert!(callback.code.lock().unwrap().is_none());
        }
    }

    #[test]
    fn live_old_epoch_disposal_retains_replacement_and_exact_idempotency() {
        let host = OAuthHost::default();
        let old = scope();
        let mut replacement = old.clone();
        replacement.window_epoch = 1;
        replacement.opening = uuid::Uuid::now_v7().to_string();
        host.epochs.lock().unwrap().insert(old.window.clone(), 1);
        let attempt = inert_attempt(replacement.clone(), false);
        let generation = attempt.generation.clone();
        let callback = Arc::clone(&attempt.shared);
        host.attempts
            .lock()
            .unwrap()
            .insert(replacement.window.clone(), attempt);
        for _ in 0..2 {
            host.control(
                old.clone(),
                OAuthAction::Dispose,
                "original-generation",
                "",
                "",
            )
            .unwrap();
        }
        assert_eq!(host.disposed.lock().unwrap().len(), 1);
        assert!(host.disposed.lock().unwrap().contains(&old));
        let attempts = host.attempts.lock().unwrap();
        assert!(attempts.get(&replacement.window).unwrap().scope == replacement);
        assert_eq!(
            attempts.get(&replacement.window).unwrap().generation,
            generation
        );
        assert!(!callback.stop.load(Ordering::Acquire));
        assert!(callback.code.lock().unwrap().is_some());
        assert!(!host.stopped.load(Ordering::Acquire));
    }

    #[test]
    fn disposed_capacity_is_idempotent_and_exhaustion_joins_all_callback_authority() {
        let host = OAuthHost::default();
        let known = fill_disposed(&host);
        host.control(known.clone(), OAuthAction::Dispose, "", "", "")
            .unwrap();
        assert_eq!(host.disposed.lock().unwrap().len(), 4096);
        let active = inert_attempt(scope(), false);
        let callback = Arc::clone(&active.shared);
        host.attempts
            .lock()
            .unwrap()
            .insert(active.scope.window.clone(), active);
        let overflow = scope();
        assert!(matches!(
            host.control(overflow.clone(), OAuthAction::Dispose, "", "", ""),
            Err(NativeFailure::Stopped)
        ));
        assert_eq!(host.disposed.lock().unwrap().len(), 4096);
        assert!(!host.disposed.lock().unwrap().contains(&overflow));
        assert!(host.attempts.lock().unwrap().is_empty());
        assert!(callback.stop.load(Ordering::Acquire));
        assert!(callback.code.lock().unwrap().is_none());
        assert!(matches!(
            host.window_epoch("replacement"),
            Err(NativeFailure::Stopped)
        ));
        assert!(matches!(
            host.control(known, OAuthAction::Take, "", "", ""),
            Err(NativeFailure::Stopped)
        ));
    }
    #[test]
    fn expiry_uses_same_bound_without_evicting_original_revocations() {
        for known_expiry in [true, false] {
            let host = OAuthHost::default();
            let known = fill_disposed(&host);
            let expired = inert_attempt(if known_expiry { known.clone() } else { scope() }, true);
            let callback = Arc::clone(&expired.shared);
            host.attempts
                .lock()
                .unwrap()
                .insert(expired.scope.window.clone(), expired);
            let result = host.control(scope(), OAuthAction::Profiles, "", "", "");
            if known_expiry {
                assert!(result.is_ok());
                assert!(!host.stopped.load(Ordering::Acquire));
            } else {
                assert!(matches!(result, Err(NativeFailure::Stopped)));
            }
            assert_eq!(host.disposed.lock().unwrap().len(), 4096);
            assert!(host.disposed.lock().unwrap().contains(&known));
            assert!(host.attempts.lock().unwrap().is_empty());
            assert!(callback.stop.load(Ordering::Acquire));
            assert!(callback.code.lock().unwrap().is_none());
        }
    }
    #[test]
    fn window_close_bounds_all_epoch_insertions_and_denies_replacement_on_overflow() {
        let host = OAuthHost::default();
        for index in 0..4096 {
            host.close_window(&format!("window-{index}")).unwrap();
        }
        host.close_window("window-0").unwrap();
        assert_eq!(host.epochs.lock().unwrap().len(), 4096);
        let active = inert_attempt(scope(), false);
        let callback = Arc::clone(&active.shared);
        host.attempts
            .lock()
            .unwrap()
            .insert(active.scope.window.clone(), active);
        assert!(matches!(
            host.close_window("overflow"),
            Err(NativeFailure::Stopped)
        ));
        assert_eq!(host.epochs.lock().unwrap().len(), 4096);
        assert!(!host.epochs.lock().unwrap().contains_key("overflow"));
        assert!(callback.stop.load(Ordering::Acquire));
        assert!(callback.code.lock().unwrap().is_none());
        assert!(matches!(
            host.window_epoch("window-0"),
            Err(NativeFailure::Stopped)
        ));
    }
    #[test]
    fn window_close_also_bounds_disposed_scope_insertion() {
        let host = OAuthHost::default();
        fill_disposed(&host);
        let original = inert_attempt(scope(), false);
        let callback = Arc::clone(&original.shared);
        host.attempts
            .lock()
            .unwrap()
            .insert(original.scope.window.clone(), original);
        assert!(matches!(
            host.close_window("main"),
            Err(NativeFailure::Stopped)
        ));
        assert_eq!(host.disposed.lock().unwrap().len(), 4096);
        assert!(host.attempts.lock().unwrap().is_empty());
        assert!(callback.stop.load(Ordering::Acquire));
        assert!(callback.code.lock().unwrap().is_none());
    }
    fn authorization(callback: &str) -> String {
        let mut url = url::Url::parse("https://openrouter.ai/auth").unwrap();
        url.query_pairs_mut()
            .append_pair("code_challenge", &"x".repeat(43))
            .append_pair("code_challenge_method", "S256")
            .append_pair("key_label", "DeliDev")
            .append_pair("callback_url", callback);
        url.into()
    }
    fn request(port: u16, path: &str, v6: bool) -> String {
        let mut stream = if v6 {
            TcpStream::connect((Ipv6Addr::LOCALHOST, port))
        } else {
            TcpStream::connect((Ipv4Addr::LOCALHOST, port))
        }
        .unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(2)))
            .unwrap();
        stream
            .write_all(format!("GET {path} HTTP/1.1\r\nHost: localhost:{port}\r\n\r\n").as_bytes())
            .unwrap();
        let mut response = String::new();
        stream.read_to_string(&mut response).unwrap();
        response
    }
    #[test]
    fn device_owner_has_no_listener_and_requires_the_registered_approval_uri() {
        assert!(registered_client(OAuthProfile::Baseten).is_none());
        let attempt = begin_profile(scope(), OAuthProfile::Baseten).unwrap();
        assert!(attempt.callback.is_empty());
        assert!(attempt.thread.is_none());
        assert!(attempt.shared.api_state.lock().unwrap().is_none());
        let registered = "https://app.baseten.co/delidev-fixture-approval";
        assert!(validate_device_authorization(registered, registered).is_ok());
        for raw in [
            "https://evil.example/approve",
            "https://app.baseten.co/delidev-fixture-approval?device_code=private",
            "https://app.baseten.co:443/delidev-fixture-approval",
            "https://app.baseten.co/other",
        ] {
            assert!(validate_device_authorization(raw, registered).is_err());
        }
        let host = OAuthHost::default();
        assert!(
            host.control(scope(), OAuthAction::BeginBaseten, "", "", "")
                .is_err()
        );
    }

    #[test]
    fn google_profile_binds_loopback_scope_and_closed_callback_fields() {
        let _listener_guard = loopback_listener_test_guard();
        assert!(registered_client(OAuthProfile::GoogleGemini).is_none());
        let attempt = begin_profile(scope(), OAuthProfile::GoogleGemini).unwrap();
        assert!(attempt.callback.starts_with("http://127.0.0.1:"));
        let state = "s".repeat(43);
        let mut auth = url::Url::parse("https://accounts.google.com/o/oauth2/v2/auth").unwrap();
        auth.query_pairs_mut()
            .append_pair("client_id", "fixture.apps.googleusercontent.com")
            .append_pair("redirect_uri", &attempt.callback)
            .append_pair("response_type", "code")
            .append_pair("scope", "https://www.googleapis.com/auth/cloud-platform")
            .append_pair("code_challenge", &"c".repeat(43))
            .append_pair("code_challenge_method", "S256")
            .append_pair("state", &state)
            .append_pair("access_type", "offline")
            .append_pair("prompt", "consent");
        assert!(
            validate_public_authorization(
                auth.as_str(),
                &attempt.callback,
                "fixture.apps.googleusercontent.com",
                OAuthProfile::GoogleGemini
            )
            .is_ok()
        );
        assert!(
            validate_public_authorization(
                auth.as_str(),
                &attempt.callback,
                "foreign.apps.googleusercontent.com",
                OAuthProfile::GoogleGemini
            )
            .is_err()
        );
        let host = "127.0.0.1:55451";
        let raw = format!(
            "GET /oauth/google-gemini/callback?code=opaque&state={state}&scope=scope&authuser=0&\
             prompt=consent HTTP/1.1\r\nHost: {host}\r\n\r\n"
        );
        assert!(
            parse_request_profile(
                raw.as_bytes(),
                host,
                "/oauth/google-gemini/callback",
                Some(&state),
                Some(OAuthProfile::GoogleGemini)
            )
            .is_some()
        );
        assert!(
            parse_request_profile(
                raw.as_bytes(),
                host,
                "/oauth/google-gemini/callback",
                Some(&state),
                Some(OAuthProfile::HuggingFace)
            )
            .is_none()
        );
        assert!(
            parse_request_profile(
                raw.replace("authuser=0", "authuser=email").as_bytes(),
                host,
                "/oauth/google-gemini/callback",
                Some(&state),
                Some(OAuthProfile::GoogleGemini)
            )
            .is_none()
        );
    }

    #[test]
    fn hugging_face_profile_requires_registration_and_original_state() {
        let _listener_guard = loopback_listener_test_guard();
        assert!(registered_client(OAuthProfile::HuggingFace).is_none());
        let host = OAuthHost::default();
        let profiles = host
            .control(scope(), OAuthAction::Profiles, "", "", "")
            .unwrap();
        assert_eq!(profiles.profiles.len(), 1);
        assert!(
            host.control(scope(), OAuthAction::BeginHuggingFace, "", "", "")
                .is_err()
        );
        let attempt = begin_profile(scope(), OAuthProfile::HuggingFace).unwrap();
        let callback = url::Url::parse(&attempt.callback).unwrap();
        let state = "s".repeat(43);
        let mut authorization = url::Url::parse("https://huggingface.co/oauth/authorize").unwrap();
        authorization
            .query_pairs_mut()
            .append_pair("client_id", "fixture-public-client")
            .append_pair("redirect_uri", &attempt.callback)
            .append_pair("response_type", "code")
            .append_pair("scope", "inference-api")
            .append_pair("code_challenge", &"c".repeat(43))
            .append_pair("code_challenge_method", "S256")
            .append_pair("state", &state);
        let expected = validate_hugging_face_authorization(
            authorization.as_str(),
            &attempt.callback,
            "fixture-public-client",
        )
        .unwrap();
        assert_eq!(expected.as_str(), state);
        for bad in [
            authorization
                .to_string()
                .replace("huggingface.co", "foreign.test"),
            format!("{authorization}&state=duplicate"),
            authorization.to_string().replace("inference-api", "openid"),
        ] {
            assert!(
                validate_hugging_face_authorization(
                    &bad,
                    &attempt.callback,
                    "fixture-public-client"
                )
                .is_err()
            );
        }
        *attempt.shared.api_state.lock().unwrap() = Some(expected);
        attempt.shared.bound.store(true, Ordering::Release);
        let port = callback.port().unwrap();
        let wrong = format!("{}?code=opaque&state={}", callback.path(), "x".repeat(43));
        assert!(request(port, &wrong, false).starts_with("HTTP/1.1 400"));
        assert!(!attempt.shared.consumed.load(Ordering::Acquire));
        let denied = format!("{}?error=access_denied&state={state}", callback.path());
        assert!(request(port, &denied, true).starts_with("HTTP/1.1 303"));
        assert!(attempt.shared.consumed.load(Ordering::Acquire));
        let bytes = attempt.shared.code.lock().unwrap().take().unwrap();
        assert!(bytes.starts_with(b"error=access_denied&state="));
        assert!(request(port, &denied, false).starts_with("HTTP/1.1 400"));
    }

    #[test]
    fn lost_begin_replays_and_disposal_or_window_close_blocks_late_begin() {
        let _listener_guard = loopback_listener_test_guard();
        let host = OAuthHost::default();
        let scope = scope();
        let first = host
            .control(scope.clone(), OAuthAction::Begin, "", "", "")
            .unwrap();
        let replay = host
            .control(scope.clone(), OAuthAction::Begin, "", "", "")
            .unwrap();
        assert_eq!(first.generation, replay.generation);
        assert_eq!(first.callback_url, replay.callback_url);
        host.close_window("main").unwrap();
        assert!(
            host.control(scope.clone(), OAuthAction::Begin, "", "", "")
                .is_err()
        );
        let mut replacement = scope.clone();
        replacement.window_epoch = host.window_epoch("main").unwrap();
        replacement.opening = uuid::Uuid::now_v7().to_string();
        host.control(replacement.clone(), OAuthAction::Dispose, "", "", "")
            .unwrap();
        assert!(
            host.control(replacement.clone(), OAuthAction::Begin, "", "", "")
                .is_err()
        );
        replacement.opening = uuid::Uuid::now_v7().to_string();
        host.control(replacement.clone(), OAuthAction::Begin, "", "", "")
            .unwrap();
        host.attempts.lock().unwrap().get_mut("main").unwrap().until = Instant::now();
        assert!(
            host.control(replacement, OAuthAction::Begin, "", "", "")
                .is_err()
        );
        host.stop();
    }
    #[test]
    fn dual_loopback_callback_is_one_shot_and_clears_displayed_url() {
        let _listener_guard = loopback_listener_test_guard();
        for v6 in [false, true] {
            let host = OAuthHost::default();
            let scope = scope();
            let result = host
                .control(scope.clone(), OAuthAction::Begin, "", "", "")
                .unwrap();
            let url = url::Url::parse(&result.callback_url).unwrap();
            let port = url.port().unwrap();
            let id = uuid::Uuid::now_v7().to_string();
            let auth = authorization(&result.callback_url);
            let mut opens = 0;
            host.control_with_opener(
                scope.clone(),
                OAuthAction::BindOpen,
                &result.generation,
                &id,
                &auth,
                |_, _| {
                    opens += 1;
                    Ok(())
                },
            )
            .unwrap();
            assert_eq!(opens, 1);
            assert!(
                host.control_with_opener(
                    scope.clone(),
                    OAuthAction::BindOpen,
                    &result.generation,
                    &id,
                    &auth,
                    |_, _| panic!("duplicate opener")
                )
                .is_ok()
            );
            let response = request(
                port,
                &format!("{}?code=native-code-sentinel", url.path()),
                v6,
            );
            assert!(response.starts_with("HTTP/1.1 303"));
            assert!(!response.contains("native-code-sentinel"));
            assert!(response.contains("Cache-Control: no-store"));
            assert!(response.contains("Referrer-Policy: no-referrer"));
            let final_page = request(port, &format!("{}/complete", url.path()), v6);
            assert!(final_page.starts_with("HTTP/1.1 200"));
            assert!(!final_page.contains("native-code-sentinel"));
            assert!(
                request(port, &format!("{}?code=second-code", url.path()), v6)
                    .starts_with("HTTP/1.1 400")
            );
            let mut taken = host
                .control(
                    scope.clone(),
                    OAuthAction::Take,
                    &result.generation,
                    &id,
                    "",
                )
                .unwrap();
            assert_eq!(
                taken.code.as_deref(),
                Some(b"native-code-sentinel".as_slice())
            );
            taken.code.as_mut().unwrap().zeroize();
            assert!(
                host.control(
                    scope.clone(),
                    OAuthAction::Take,
                    &result.generation,
                    &id,
                    ""
                )
                .unwrap()
                .code
                .is_none()
            );
            host.control(scope, OAuthAction::Dispose, &result.generation, &id, "")
                .unwrap();
            assert!(TcpStream::connect((Ipv4Addr::LOCALHOST, port)).is_err());
            assert!(TcpStream::connect((Ipv6Addr::LOCALHOST, port)).is_err());
        }
    }
    #[test]
    fn callback_rejects_ambiguous_framing_scope_and_opaque_code_changes() {
        let host = "localhost:12345";
        let path = "/oauth/openrouter/test";
        let valid = format!("GET {path}?code=a%20b HTTP/1.1\r\nHost: {host}\r\n\r\n");
        assert_eq!(
            parse_request(valid.as_bytes(), host, path)
                .unwrap()
                .unwrap()
                .as_slice(),
            b"a b"
        );
        for invalid in [
            valid.replace("GET ", "POST "),
            valid.replace(host, "127.0.0.1:12345"),
            valid.replace("Host:", "Host: wrong\r\nHost:"),
            valid.replace("a%20b", "%ff"),
            valid.replace("a%20b", "%00"),
            valid.replace("a%20b", "a&code=b"),
            valid.replace("a%20b", "a&state=b"),
            valid.replace("a%20b", "%zz"),
            valid.replace("\r\n\r\n", "\r\nTransfer-Encoding: chunked\r\n\r\n"),
            valid.replace("/test?", "/wrong?"),
        ] {
            assert!(parse_request(invalid.as_bytes(), host, path).is_none());
        }
        assert!(
            parse_request(
                valid.replace("a%20b", &"x".repeat(8193)).as_bytes(),
                host,
                path
            )
            .is_none()
        );
        assert!(
            parse_request(
                valid
                    .replace(
                        "\r\n\r\n",
                        &format!("\r\nX-Extra: {}\r\n\r\n", "x".repeat(16 << 10))
                    )
                    .as_bytes(),
                host,
                path
            )
            .is_none()
        );
    }
    #[test]
    fn original_window_server_generation_and_closed_opener_remain_authoritative() {
        let _listener_guard = loopback_listener_test_guard();
        let host = OAuthHost::default();
        let scope = scope();
        let initial = host
            .control(scope.clone(), OAuthAction::Begin, "", "", "")
            .unwrap();
        let id = uuid::Uuid::now_v7().to_string();
        let auth = authorization(&initial.callback_url);
        for bad in [
            auth.replace("openrouter.ai", "evil.test"),
            auth.replace("/auth?", "/other?"),
            format!("{auth}&code_challenge_method=S256"),
            auth.replace("S256", "plain"),
            auth.replace("DeliDev", "other"),
            auth.replace("localhost", "127.0.0.1"),
        ] {
            assert!(validate_authorization(&bad, &initial.callback_url).is_err());
        }
        let mut wrong = scope.clone();
        wrong.server = uuid::Uuid::now_v7().to_string();
        assert!(
            host.control_with_opener(
                wrong,
                OAuthAction::BindOpen,
                &initial.generation,
                &id,
                &auth,
                |_, _| panic!("foreign opener")
            )
            .is_err()
        );
        host.control_with_opener(
            scope.clone(),
            OAuthAction::BindOpen,
            &initial.generation,
            &id,
            &auth,
            |_, _| Err(NativeFailure::SidecarFailed),
        )
        .err()
        .unwrap();
        host.control_with_opener(
            scope.clone(),
            OAuthAction::BindOpen,
            &initial.generation,
            &id,
            &auth,
            |_, _| panic!("binding replay opened twice"),
        )
        .unwrap();
        assert!(
            host.control_with_opener(
                scope.clone(),
                OAuthAction::BindOpen,
                &initial.generation,
                &uuid::Uuid::now_v7().to_string(),
                &auth,
                |_, _| panic!("foreign binding replay opened"),
            )
            .is_err()
        );
        host.control_with_opener(
            scope.clone(),
            OAuthAction::Reopen,
            &initial.generation,
            &id,
            "",
            |_, _| Ok(()),
        )
        .unwrap();
        host.close_window("main").unwrap();
        assert!(
            host.control(
                scope.clone(),
                OAuthAction::Take,
                &initial.generation,
                &id,
                ""
            )
            .is_err()
        );
        let mut replacement_scope = scope.clone();
        replacement_scope.opening = uuid::Uuid::now_v7().to_string();
        replacement_scope.window_epoch = host.window_epoch("main").unwrap();
        let replacement = host
            .control(replacement_scope.clone(), OAuthAction::Begin, "", "", "")
            .unwrap();
        host.control(
            scope.clone(),
            OAuthAction::Dispose,
            &initial.generation,
            &id,
            "",
        )
        .unwrap();
        let new_id = uuid::Uuid::now_v7().to_string();
        host.control_with_opener(
            replacement_scope.clone(),
            OAuthAction::BindOpen,
            &replacement.generation,
            &new_id,
            &authorization(&replacement.callback_url),
            |_, _| Ok(()),
        )
        .unwrap();
        host.stop();
        assert!(
            host.control(
                scope,
                OAuthAction::Take,
                &replacement.generation,
                &new_id,
                ""
            )
            .is_err()
        );
    }
}

#[derive(Clone, Copy)]
enum SubscriptionCallback {
    Localhost,
    Ipv4,
}

impl SubscriptionCallback {
    fn parse(uri: &str) -> Result<Self, NativeFailure> {
        match uri {
            "http://localhost:1457/auth/callback" => Ok(Self::Localhost),
            "http://127.0.0.1:1457/auth/callback" => Ok(Self::Ipv4),
            _ => Err(NativeFailure::InvalidInput),
        }
    }

    fn uri(self) -> &'static str {
        match self {
            Self::Localhost => "http://localhost:1457/auth/callback",
            Self::Ipv4 => "http://127.0.0.1:1457/auth/callback",
        }
    }

    fn authority(self) -> &'static str {
        match self {
            Self::Localhost => "localhost:1457",
            Self::Ipv4 => "127.0.0.1:1457",
        }
    }
}

fn subscription_authorization(
    raw: &str,
) -> Result<(Zeroizing<String>, SubscriptionCallback), NativeFailure> {
    if raw.len() > 8192 || raw.chars().any(char::is_control) {
        return Err(NativeFailure::InvalidInput);
    }
    let url = url::Url::parse(raw).map_err(|_| NativeFailure::InvalidInput)?;
    if url.scheme() != "https"
        || url.host_str() != Some("auth.openai.com")
        || url.port().is_some()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/oauth/authorize"
        || url.fragment().is_some()
        || url.as_str() != raw
    {
        return Err(NativeFailure::InvalidInput);
    }
    let mut fields = BTreeMap::new();
    for (key, value) in url.query_pairs() {
        if fields
            .insert(key.into_owned(), value.into_owned())
            .is_some()
        {
            return Err(NativeFailure::InvalidInput);
        }
    }
    let callback = SubscriptionCallback::parse(
        fields
            .get("redirect_uri")
            .ok_or(NativeFailure::InvalidInput)?,
    )?;
    let state = fields.remove("state").ok_or(NativeFailure::InvalidInput)?;
    if !(16..=512).contains(&state.len())
        || !state
            .bytes()
            .all(|v| v.is_ascii_alphanumeric() || v == b'-' || v == b'_')
        || fields.get("code_challenge_method").map(String::as_str) != Some("S256")
        || fields.get("response_type").map(String::as_str) != Some("code")
    {
        return Err(NativeFailure::InvalidInput);
    }
    Ok((Zeroizing::new(state), callback))
}

fn begin_subscription(
    scope: OAuthScope,
    attempt_id: &str,
    authorization: &str,
    local: bool,
) -> Result<Attempt, NativeFailure> {
    let (state, callback) = subscription_authorization(authorization)?;
    let shared = Arc::new(Shared {
        expected_state: Some(state),
        api_state: Mutex::new(None),
        api_profile: None,
        bound: AtomicBool::new(true),
        consumed: AtomicBool::new(false),
        stop: AtomicBool::new(false),
        code: Mutex::new(None),
    });
    let until = Instant::now() + Duration::from_secs(900);
    let handle = if local {
        None
    } else {
        // The registered fallback callback belongs to the original remote
        // Codex login. A conflict is explicit; never remap or use device codes.
        let v4 = TcpListener::bind((Ipv4Addr::LOCALHOST, 1457))
            .map_err(|_| NativeFailure::SidecarFailed)?;
        let v6 = TcpListener::bind((Ipv6Addr::LOCALHOST, 1457))
            .map_err(|_| NativeFailure::SidecarFailed)?;
        v4.set_nonblocking(true)
            .map_err(|_| NativeFailure::SidecarFailed)?;
        v6.set_nonblocking(true)
            .map_err(|_| NativeFailure::SidecarFailed)?;
        let control = Arc::clone(&shared);
        Some(
            thread::Builder::new()
                .name("delidev-subscription-callback".into())
                .spawn(move || {
                    while !control.stop.load(Ordering::Acquire) && Instant::now() < until {
                        for listener in [&v4, &v6] {
                            if let Ok((mut stream, peer)) = listener.accept()
                                && peer.ip().is_loopback()
                            {
                                handle_request(
                                    &mut stream,
                                    callback.authority(),
                                    "/auth/callback",
                                    &control,
                                    until,
                                );
                            }
                        }
                        thread::sleep(Duration::from_millis(10));
                    }
                })
                .map_err(|_| NativeFailure::SidecarFailed)?,
        )
    };
    Ok(Attempt {
        kind: BrowserBindingKind::CodexSubscription,
        profile: OAuthProfile::Openrouter,
        scope,
        generation: uuid::Uuid::now_v7().to_string(),
        callback: callback.uri().into(),
        attempt: attempt_id.into(),
        authorization: Zeroizing::new(authorization.into()),
        until,
        shared,
        thread: handle,
    })
}

#[cfg(test)]
mod subscription_tests {
    use super::*;
    const AUTH: &str = "https://auth.openai.com/oauth/authorize?state=fixture-original-state-123456&redirect_uri=http%3A%2F%2Flocalhost%3A1457%2Fauth%2Fcallback&response_type=code&code_challenge_method=S256";
    fn authorization_for(callback: SubscriptionCallback) -> String {
        AUTH.replace("localhost", callback.authority().split(':').next().unwrap())
    }

    #[test]
    fn subscription_authorization_accepts_only_registered_original_callbacks() {
        for callback in [SubscriptionCallback::Localhost, SubscriptionCallback::Ipv4] {
            let (state, parsed) = subscription_authorization(&authorization_for(callback)).unwrap();
            assert_eq!(state.as_str(), "fixture-original-state-123456");
            assert_eq!(parsed.uri(), callback.uri());
        }
        for callback in [
            "http://127.1:1457/auth/callback",
            "http://2130706433:1457/auth/callback",
            "http://[::1]:1457/auth/callback",
            "http://localhost:1455/auth/callback",
            "http://127.0.0.1:1457/other",
            "https://localhost:1457/auth/callback",
            "http://localhost.evil.invalid:1457/auth/callback",
            "http://user@localhost:1457/auth/callback",
            "http://localhost:1457/auth/callback#fragment",
        ] {
            let mut authorization = url::Url::parse(AUTH).unwrap();
            authorization
                .query_pairs_mut()
                .clear()
                .append_pair("state", "fixture-original-state-123456")
                .append_pair("redirect_uri", callback)
                .append_pair("response_type", "code")
                .append_pair("code_challenge_method", "S256");
            assert!(subscription_authorization(authorization.as_str()).is_err());
        }
        for authorization in [
            format!("{AUTH}&redirect_uri=http%3A%2F%2F127.0.0.1%3A1457%2Fauth%2Fcallback"),
            format!("{AUTH}&state=fixture-original-state-123456"),
            AUTH.replace("fixture-original-state-123456", "short"),
            AUTH.replace("fixture-original-state-123456", "invalid.state-value-1234"),
        ] {
            assert!(subscription_authorization(&authorization).is_err());
        }
    }

    fn scope() -> OAuthScope {
        OAuthScope {
            window: "main".into(),
            server: uuid::Uuid::now_v7().to_string(),
            instance: uuid::Uuid::now_v7().to_string(),
            opening: uuid::Uuid::now_v7().to_string(),
            window_epoch: 0,
        }
    }
    #[test]
    fn local_browser_binding_replays_once_and_reopens_only_original() {
        for callback in [SubscriptionCallback::Localhost, SubscriptionCallback::Ipv4] {
            let auth = authorization_for(callback);
            let host = OAuthHost::default();
            let scope = scope();
            let operation = uuid::Uuid::now_v7().to_string();
            let opened = std::sync::atomic::AtomicUsize::new(0);
            let opener = |url: &str, _: &AtomicBool| {
                assert_eq!(url, &auth);
                opened.fetch_add(1, Ordering::SeqCst);
                Ok(())
            };
            let first = host
                .control_with_browser(
                    scope.clone(),
                    OAuthAction::SubscriptionOpen,
                    "",
                    &operation,
                    &auth,
                    true,
                    opener,
                )
                .unwrap();
            let replay = host
                .control_with_browser(
                    scope.clone(),
                    OAuthAction::SubscriptionOpen,
                    "",
                    &operation,
                    &auth,
                    true,
                    opener,
                )
                .unwrap();
            assert_eq!(first.generation, replay.generation);
            assert_eq!(opened.load(Ordering::SeqCst), 1);
            let replacement = auth.replace(
                callback.authority().split(':').next().unwrap(),
                match callback {
                    SubscriptionCallback::Localhost => "127.0.0.1",
                    SubscriptionCallback::Ipv4 => "localhost",
                },
            );
            assert!(
                host.control_with_browser(
                    scope.clone(),
                    OAuthAction::SubscriptionReopen,
                    "",
                    &operation,
                    &replacement,
                    true,
                    opener
                )
                .is_err()
            );

            assert!(
                host.control_with_browser(
                    scope.clone(),
                    OAuthAction::SubscriptionOpen,
                    "",
                    &uuid::Uuid::now_v7().to_string(),
                    &auth,
                    true,
                    opener
                )
                .is_err()
            );
            host.control_with_browser(
                scope.clone(),
                OAuthAction::Reopen,
                &first.generation,
                &operation,
                "",
                true,
                opener,
            )
            .unwrap();
            assert_eq!(opened.load(Ordering::SeqCst), 2);
            host.control_with_browser(
                scope.clone(),
                OAuthAction::SubscriptionReopen,
                "",
                &operation,
                &auth,
                true,
                opener,
            )
            .unwrap();
            assert_eq!(opened.load(Ordering::SeqCst), 3);
            assert!(
                host.control(
                    scope.clone(),
                    OAuthAction::Take,
                    &first.generation,
                    &operation,
                    ""
                )
                .unwrap()
                .code
                .is_none()
            );
            host.control(scope.clone(), OAuthAction::Dispose, "", "", "")
                .unwrap();
            assert!(
                host.control_with_browser(
                    scope,
                    OAuthAction::SubscriptionOpen,
                    "",
                    &operation,
                    &auth,
                    true,
                    opener
                )
                .is_err()
            );
        }
    }
    #[test]
    fn subscription_query_requires_original_state_closed_keys_and_framing() {
        let valid = "code=fixture-code&state=fixture-original-state-123456&scope=openid";
        let request = |query: &str| {
            format!("GET /auth/callback?{query} HTTP/1.1\r\nHost: localhost:1457\r\n\r\n")
        };
        let expected = Some("fixture-original-state-123456");
        assert_eq!(
            parse_request_mode(
                request(valid).as_bytes(),
                "localhost:1457",
                "/auth/callback",
                expected
            )
            .unwrap()
            .unwrap()
            .as_slice(),
            valid.as_bytes()
        );
        for query in ["code=fixture&state=foreign", "code=fixture&state=fixture-original-state-123456&code=other", "code=fixture&state=fixture-original-state-123456&redirect_uri=https://external.invalid", "code=%00&state=fixture-original-state-123456", "state=fixture-original-state-123456"] {
            assert!(parse_request_mode(request(query).as_bytes(), "localhost:1457", "/auth/callback", expected).is_none());
        }
        assert!(
            parse_request_mode(
                request(valid)
                    .replace("Host: localhost:1457", "Host: external.invalid")
                    .as_bytes(),
                "localhost:1457",
                "/auth/callback",
                expected
            )
            .is_none()
        );
        assert!(
            parse_request_mode(
                request(valid)
                    .replace("\r\n\r\n", "\r\nOrigin: https://external.invalid\r\n\r\n")
                    .as_bytes(),
                "localhost:1457",
                "/auth/callback",
                expected
            )
            .is_none()
        );
    }
    #[test]
    fn remote_receiver_delivers_once_and_joins_on_disposal() {
        for callback in [SubscriptionCallback::Localhost, SubscriptionCallback::Ipv4] {
            let auth = authorization_for(callback);
            let host = OAuthHost::default();
            let scope = scope();
            let operation = uuid::Uuid::now_v7().to_string();
            let first = host
                .control_with_browser(
                    scope.clone(),
                    OAuthAction::SubscriptionOpen,
                    "",
                    &operation,
                    &auth,
                    false,
                    |_, _| Ok(()),
                )
                .unwrap();
            let receive = |peer: std::net::IpAddr, authority: &str| {
                let mut stream = std::net::TcpStream::connect((peer, 1457)).unwrap();
                stream
                    .set_read_timeout(Some(Duration::from_secs(2)))
                    .unwrap();
                stream
                    .write_all(
                        format!(
                            "GET /auth/callback?code=fixture&state=fixture-original-state-123456 \
                             HTTP/1.1\r\nHost: {authority}\r\n\r\n"
                        )
                        .as_bytes(),
                    )
                    .unwrap();
                let mut result = String::new();
                stream.read_to_string(&mut result).unwrap();
                result
            };
            let v4 = std::net::IpAddr::V4(Ipv4Addr::LOCALHOST);
            let v6 = std::net::IpAddr::V6(Ipv6Addr::LOCALHOST);
            let other_authority = match callback {
                SubscriptionCallback::Localhost => "127.0.0.1:1457",
                SubscriptionCallback::Ipv4 => "localhost:1457",
            };
            assert!(receive(v4, other_authority).starts_with("HTTP/1.1 400"));
            assert!(receive(v6, callback.authority()).starts_with("HTTP/1.1 303"));
            let result = host
                .control(
                    scope.clone(),
                    OAuthAction::Take,
                    &first.generation,
                    &operation,
                    "",
                )
                .unwrap();
            assert_eq!(
                result.code.as_ref().unwrap(),
                b"code=fixture&state=fixture-original-state-123456"
            );
            assert!(
                host.control(
                    scope.clone(),
                    OAuthAction::Take,
                    &first.generation,
                    &operation,
                    ""
                )
                .unwrap()
                .code
                .is_none()
            );
            assert!(receive(v4, callback.authority()).starts_with("HTTP/1.1 400"));
            host.control(scope, OAuthAction::Dispose, "", "", "")
                .unwrap();
            let listener = TcpListener::bind((Ipv4Addr::LOCALHOST, 1457)).unwrap();
            drop(listener);
            drop(TcpListener::bind((Ipv6Addr::LOCALHOST, 1457)).unwrap());
        }
    }
}

// This is a browser-only binding to the original CLI's subscription URL. The
// selected Runner retains the native callback/readline process and exchanges
// the code. Desktop never creates a Claude callback listener or token client.
fn begin_claude_subscription(
    scope: OAuthScope,
    attempt_id: &str,
    raw: &str,
) -> Result<Attempt, NativeFailure> {
    validate_claude_subscription_authorization(raw)?;
    Ok(Attempt {
        kind: BrowserBindingKind::ClaudeSubscription,
        profile: OAuthProfile::Openrouter,
        scope,
        generation: uuid::Uuid::now_v7().to_string(),
        callback: String::new(),
        attempt: attempt_id.into(),
        authorization: Zeroizing::new(raw.into()),
        until: Instant::now() + Duration::from_secs(900),
        shared: Arc::new(Shared {
            expected_state: None,
            api_state: Mutex::new(None),
            api_profile: None,
            bound: AtomicBool::new(true),
            consumed: AtomicBool::new(false),
            stop: AtomicBool::new(false),
            code: Mutex::new(None),
        }),
        thread: None,
    })
}

fn validate_claude_subscription_authorization(raw: &str) -> Result<(), NativeFailure> {
    let invalid = NativeFailure::InvalidInput;
    if raw.len() > 8192 || !raw.bytes().all(|b| (33..=126).contains(&b)) {
        return Err(invalid);
    }
    let url = url::Url::parse(raw).map_err(|_| invalid)?;
    if url.scheme() != "https"
        || url.host_str() != Some("claude.com")
        || url.port().is_some()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/cai/oauth/authorize"
        || url.fragment().is_some()
        || url.as_str() != raw
    {
        return Err(invalid);
    }
    let mut fields = BTreeMap::new();
    let allowed = [
        "code",
        "client_id",
        "response_type",
        "redirect_uri",
        "scope",
        "code_challenge",
        "code_challenge_method",
        "state",
        "orgUUID",
        "login_hint",
        "login_method",
    ];
    for (key, value) in url.query_pairs() {
        if !allowed.contains(&key.as_ref())
            || value.len() > 2048
            || value.chars().any(char::is_control)
            || fields
                .insert(key.into_owned(), value.into_owned())
                .is_some()
        {
            return Err(invalid);
        }
    }
    let field = |name| fields.get(name).map(String::as_str).unwrap_or("");
    let opaque = |value: &str| {
        (16..=512).contains(&value.len())
            && value
                .bytes()
                .all(|v| v.is_ascii_alphanumeric() || v == b'-' || v == b'_')
    };
    if field("client_id") != "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
        || field("response_type") != "code"
        || field("code_challenge_method") != "S256"
        || field("code_challenge").len() != 43
        || !opaque(field("code_challenge"))
        || !opaque(field("state"))
        || !["", "true"].contains(&field("code"))
    {
        return Err(invalid);
    }
    let scopes: Vec<_> = field("scope").split_whitespace().collect();
    // The server-owned setup-token profile is separately constrained: exactly
    // the inference scope, the manual code callback and the code=true
    // marker. It never accepts the auth-login scopes or a localhost
    // callback.
    if scopes == ["user:inference"] {
        return if field("redirect_uri") == "https://platform.claude.com/oauth/code/callback"
            && field("code") == "true"
        {
            Ok(())
        } else {
            Err(invalid)
        };
    }
    if field("redirect_uri") != "https://platform.claude.com/oauth/code/callback" {
        let callback = url::Url::parse(field("redirect_uri")).map_err(|_| invalid)?;
        if callback.scheme() != "http"
            || callback.host_str() != Some("localhost")
            || callback.port().is_none_or(|port| port == 0)
            || !callback.username().is_empty()
            || callback.password().is_some()
            || callback.path() != "/callback"
            || callback.query().is_some()
            || callback.fragment().is_some()
            || callback.as_str() != field("redirect_uri")
        {
            return Err(invalid);
        }
    }
    if !scopes.contains(&"user:profile") || !scopes.contains(&"user:inference") {
        return Err(invalid);
    }
    let allowed_scopes = [
        "org:create_api_key",
        "user:profile",
        "user:inference",
        "user:sessions:claude_code",
        "user:mcp_servers",
        "user:file_upload",
    ];
    for (index, scope) in scopes.iter().enumerate() {
        if !allowed_scopes.contains(scope) || scopes[..index].contains(scope) {
            return Err(invalid);
        }
    }
    Ok(())
}

#[cfg(test)]
mod claude_subscription_tests {
    use std::sync::atomic::AtomicUsize;

    use super::*;
    fn authorization() -> String {
        let mut u = url::Url::parse("https://claude.com/cai/oauth/authorize").unwrap();
        u.query_pairs_mut()
            .append_pair("client_id", "9d1c250a-e61b-44d9-88ed-5944d1962f5e")
            .append_pair("response_type", "code")
            .append_pair(
                "redirect_uri",
                "https://platform.claude.com/oauth/code/callback",
            )
            .append_pair(
                "scope",
                "user:profile user:inference user:sessions:claude_code",
            )
            .append_pair("code_challenge", &"a".repeat(43))
            .append_pair("code_challenge_method", "S256")
            .append_pair("state", &"b".repeat(32))
            .append_pair("code", "true");
        u.into()
    }
    #[test]
    fn original_claude_browser_has_no_listener_or_code_receiver() {
        let host = OAuthHost::default();
        let server = uuid::Uuid::now_v7().to_string();
        let opening = uuid::Uuid::now_v7().to_string();
        let attempt = uuid::Uuid::now_v7().to_string();
        let scope = OAuthScope {
            window: "main".into(),
            instance: uuid::Uuid::now_v7().to_string(),
            server,
            opening,
            window_epoch: host.window_epoch("main").unwrap(),
        };
        let url = authorization();
        let opened = AtomicUsize::new(0);
        let result = host
            .control_with_opener(
                scope.clone(),
                OAuthAction::ClaudeSubscriptionOpen,
                "",
                &attempt,
                &url,
                |observed, _| {
                    assert_eq!(observed, url);
                    opened.fetch_add(1, Ordering::SeqCst);
                    Ok(())
                },
            )
            .unwrap();
        assert_eq!(opened.load(Ordering::SeqCst), 1);
        assert!(result.callback_url.is_empty());
        let replay = host
            .control_with_opener(
                scope.clone(),
                OAuthAction::ClaudeSubscriptionOpen,
                "",
                &attempt,
                &url,
                |_, _| panic!("replayed opener"),
            )
            .unwrap();
        assert_eq!(replay.generation, result.generation);
        assert!(
            host.control_with_opener(
                scope.clone(),
                OAuthAction::Take,
                &result.generation,
                &attempt,
                "",
                |_, _| panic!("code receiver")
            )
            .is_err()
        );
        assert!(
            host.control_with_opener(
                scope.clone(),
                OAuthAction::SubscriptionOpen,
                "",
                &attempt,
                &url,
                |_, _| panic!("other service")
            )
            .is_err()
        );
        host.control_with_opener(
            scope.clone(),
            OAuthAction::ClaudeSubscriptionReopen,
            &result.generation,
            &attempt,
            &url,
            |observed, _| {
                assert_eq!(observed, url);
                opened.fetch_add(1, Ordering::SeqCst);
                Ok(())
            },
        )
        .unwrap();
        assert_eq!(opened.load(Ordering::SeqCst), 2);
        host.control(scope.clone(), OAuthAction::Dispose, "", "", "")
            .unwrap();
        assert!(
            host.control_with_opener(
                scope,
                OAuthAction::ClaudeSubscriptionOpen,
                "",
                &attempt,
                &url,
                |_, _| panic!("disposed opener")
            )
            .is_err()
        );
    }
    fn setup_token_authorization() -> String {
        authorization().replace(
            "user%3Aprofile+user%3Ainference+user%3Asessions%3Aclaude_code",
            "user%3Ainference",
        )
    }
    #[test]
    fn claude_browser_accepts_only_the_constrained_setup_token_profile() {
        let url = setup_token_authorization();
        assert!(url.contains("scope=user%3Ainference&"));
        assert!(validate_claude_subscription_authorization(&url).is_ok());
        // The setup-token profile must keep the manual code callback and
        // code=true.
        for changed in [
            url.replace(
                "https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback",
                "http%3A%2F%2Flocalhost%3A54545%2Fcallback",
            ),
            url.replace("&code=true", ""),
            url.replace("&code=true", "&code=false"),
            url.replace("user%3Ainference", "user%3Ainference+user%3Amcp_servers"),
            url.replace("user%3Ainference", "user%3Aprofile"),
            url.replace("scope=user%3Ainference", "scope="),
        ] {
            assert!(
                validate_claude_subscription_authorization(&changed).is_err(),
                "{changed}"
            );
        }
        // The auth-login profile keeps requiring both of its scopes.
        let login = authorization();
        assert!(validate_claude_subscription_authorization(&login).is_ok());
        assert!(
            validate_claude_subscription_authorization(&login.replace("user%3Aprofile+", ""))
                .is_err()
        );
    }
    #[test]
    fn setup_token_browser_opens_through_the_native_control() {
        let host = OAuthHost::default();
        let scope = OAuthScope {
            window: "main".into(),
            instance: uuid::Uuid::now_v7().to_string(),
            server: uuid::Uuid::now_v7().to_string(),
            opening: uuid::Uuid::now_v7().to_string(),
            window_epoch: host.window_epoch("main").unwrap(),
        };
        let url = setup_token_authorization();
        let opened = AtomicUsize::new(0);
        host.control_with_opener(
            scope,
            OAuthAction::ClaudeSubscriptionOpen,
            "",
            &uuid::Uuid::now_v7().to_string(),
            &url,
            |observed, _| {
                assert_eq!(observed, url);
                opened.fetch_add(1, Ordering::SeqCst);
                Ok(())
            },
        )
        .unwrap();
        assert_eq!(opened.load(Ordering::SeqCst), 1);
    }
    #[test]
    fn claude_browser_rejects_substituted_authority() {
        let url = authorization();
        assert!(validate_claude_subscription_authorization(&url).is_ok());
        for changed in [
            url.replace("claude.com/cai", "attacker.invalid/cai"),
            url.clone() + "&state=replacement-state-value",
            url.replace(
                "client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e",
                "client_id=replacement",
            ),
            url.replace("user%3Ainference", "unknown%3Ainference"),
        ] {
            assert!(validate_claude_subscription_authorization(&changed).is_err());
        }
    }
}

// Grok Build browser login. The Go server owns PKCE, the loopback listener on
// its own machine and the token exchange. When the desktop is local to that
// loopback the browser redirect reaches the server directly, so this receiver
// only opens the validated URL. When the desktop is remote it binds the same
// IPv4 loopback address and numeric port, accepts exactly one redirect and
// hands the code/state query to the authenticated Connect forwarder via Take.
// A bind conflict is explicit; the port is never remapped.
//
// The Go server's `ForwardSubscriptionCallback` accepts only the Codex login
// today (ChatGPT accounts, `auth.openai.com`, fixed port 1457). A captured Grok
// callback could not be forwarded, and the one-shot code would be consumed for
// nothing, so a remote Grok open is refused up front with
// `NativeFailure::Incompatible` until the server gains Grok forwarding.
const GROK_ATTEMPT_LIFETIME: Duration = Duration::from_secs(900);
const GROK_AUTHORIZATION_HOST: &str = "auth.x.ai";
const GROK_AUTHORIZATION_PATH: &str = "/oauth2/authorize";
const GROK_CLIENT_ID: &str = "b1a00492-073a-47ea-816f-4c329264a828";
const GROK_CALLBACK_PATH: &str = "/callback";
const GROK_REFERRER: &str = "grok-build";

fn grok_opaque_value(value: &str) -> bool {
    (1..=512).contains(&value.len())
        && value
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_' | b'.' | b'~'))
}

// Returns the original state and the numeric loopback port of the redirect.
fn validate_grok_subscription_authorization(
    raw: &str,
) -> Result<(Zeroizing<String>, u16), NativeFailure> {
    let invalid = NativeFailure::InvalidInput;
    if raw.len() > 4096 || !raw.bytes().all(|b| (33..=126).contains(&b)) {
        return Err(invalid);
    }
    let url = url::Url::parse(raw).map_err(|_| invalid)?;
    if url.scheme() != "https"
        || url.host_str() != Some(GROK_AUTHORIZATION_HOST)
        || url.port().is_some()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != GROK_AUTHORIZATION_PATH
        || url.fragment().is_some()
        || url.as_str() != raw
    {
        return Err(invalid);
    }
    let mut fields = BTreeMap::new();
    for (key, value) in url.query_pairs() {
        if !matches!(
            key.as_ref(),
            "response_type"
                | "client_id"
                | "redirect_uri"
                | "scope"
                | "code_challenge"
                | "code_challenge_method"
                | "state"
                | "nonce"
                | "referrer"
        ) || fields
            .insert(key.into_owned(), Zeroizing::new(value.into_owned()))
            .is_some()
        {
            return Err(invalid);
        }
    }
    let get = |name: &str| fields.get(name).map(|v| v.as_str());
    let port = get("redirect_uri")
        .and_then(|v| v.strip_prefix("http://127.0.0.1:"))
        .and_then(|v| v.strip_suffix(GROK_CALLBACK_PATH))
        .filter(|v| {
            (1..=5).contains(&v.len())
                && v.bytes().all(|b| b.is_ascii_digit())
                && !v.starts_with('0')
        })
        .and_then(|v| v.parse::<u16>().ok())
        .filter(|port| *port >= 1024)
        .ok_or(invalid)?;
    if get("response_type") != Some("code")
        || get("client_id") != Some(GROK_CLIENT_ID)
        || get("code_challenge_method") != Some("S256")
        || get("code_challenge").is_none_or(|v| {
            v.len() != 43
                || !v
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
        })
        || !get("state").is_some_and(grok_opaque_value)
        || !get("nonce").is_some_and(grok_opaque_value)
        || get("referrer").is_some_and(|v| v != GROK_REFERRER)
        || get("scope").is_some_and(|v| {
            v.is_empty()
                || v.len() > 1024
                || v.split(' ').any(|part| {
                    part.is_empty()
                        || !part.bytes().all(|b| {
                            b.is_ascii_alphanumeric() || matches!(b, b':' | b'_' | b'.' | b'-')
                        })
                })
        })
    {
        return Err(invalid);
    }
    let state = fields.remove("state").ok_or(invalid)?;
    Ok((Zeroizing::new(state.as_str().to_owned()), port))
}

fn begin_grok_subscription(
    scope: OAuthScope,
    attempt_id: &str,
    authorization: &str,
    local: bool,
    remote_forwarding: bool,
) -> Result<Attempt, NativeFailure> {
    begin_grok_subscription_for(
        scope,
        attempt_id,
        authorization,
        local,
        remote_forwarding,
        GROK_ATTEMPT_LIFETIME,
    )
}

fn begin_grok_subscription_for(
    scope: OAuthScope,
    attempt_id: &str,
    authorization: &str,
    local: bool,
    remote_forwarding: bool,
    lifetime: Duration,
) -> Result<Attempt, NativeFailure> {
    let (state, port) = validate_grok_subscription_authorization(authorization)?;
    if !local && !remote_forwarding {
        tracing::warn!(
            operation = "grok_subscription_callback",
            phase = "remote-forwarding-unavailable"
        );
        return Err(NativeFailure::Incompatible);
    }
    let shared = Arc::new(Shared {
        expected_state: Some(state),
        api_state: Mutex::new(None),
        api_profile: None,
        bound: AtomicBool::new(true),
        consumed: AtomicBool::new(false),
        stop: AtomicBool::new(false),
        code: Mutex::new(None),
    });
    let until = Instant::now() + lifetime;
    let authority = format!("127.0.0.1:{port}");
    let handle = if local {
        None
    } else {
        // IPv4 only: the original redirect names 127.0.0.1. A conflict is
        // explicit and the numeric port is never remapped.
        let listener = TcpListener::bind((Ipv4Addr::LOCALHOST, port)).map_err(|_| {
            tracing::warn!(
                operation = "grok_subscription_callback",
                phase = "bind-failed"
            );
            NativeFailure::SidecarFailed
        })?;
        listener
            .set_nonblocking(true)
            .map_err(|_| NativeFailure::SidecarFailed)?;
        let control = Arc::clone(&shared);
        let host = authority.clone();
        Some(
            thread::Builder::new()
                .name("delidev-grok-callback".into())
                .spawn(move || {
                    while !control.stop.load(Ordering::Acquire) && Instant::now() < until {
                        if let Ok((mut stream, peer)) = listener.accept()
                            && peer.ip().is_loopback()
                        {
                            handle_request(&mut stream, &host, GROK_CALLBACK_PATH, &control, until);
                        }
                        thread::sleep(Duration::from_millis(10));
                    }
                })
                .map_err(|_| NativeFailure::SidecarFailed)?,
        )
    };
    tracing::info!(
        operation = "grok_subscription_callback",
        phase = "bound",
        local
    );
    Ok(Attempt {
        kind: BrowserBindingKind::GrokSubscription,
        profile: OAuthProfile::Openrouter,
        scope,
        generation: uuid::Uuid::now_v7().to_string(),
        callback: format!("http://{authority}{GROK_CALLBACK_PATH}"),
        attempt: attempt_id.into(),
        authorization: Zeroizing::new(authorization.into()),
        until,
        shared,
        thread: handle,
    })
}

const GROK_DEVICE_HOST: &str = "accounts.x.ai";
const GROK_DEVICE_PATH: &str = "/oauth2/device";
const GROK_DEVICE_CODE_PARAMETER: &str = "user_code";

fn grok_device_user_code(value: &str) -> bool {
    (4..=32).contains(&value.len())
        && value
            .bytes()
            .all(|b| b.is_ascii_uppercase() || b.is_ascii_digit() || b == b'-')
}

// The device verification address is the fixed xAI page with, at most, one
// `user_code` parameter. The renderer additionally pins it to the displayed
// code; native only has to keep the destination closed.
fn validate_grok_device_authorization(raw: &str) -> Result<(), NativeFailure> {
    let invalid = NativeFailure::InvalidInput;
    if raw.len() > 512 || !raw.bytes().all(|b| (33..=126).contains(&b)) {
        return Err(invalid);
    }
    let url = url::Url::parse(raw).map_err(|_| invalid)?;
    if url.scheme() != "https"
        || url.host_str() != Some(GROK_DEVICE_HOST)
        || url.port().is_some()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != GROK_DEVICE_PATH
        || url.fragment().is_some()
        || url.as_str() != raw
    {
        return Err(invalid);
    }
    if url.query().is_none() {
        return Ok(());
    }
    let mut pairs = url.query_pairs();
    match (pairs.next(), pairs.next()) {
        (Some((key, value)), None)
            if key == GROK_DEVICE_CODE_PARAMETER && grok_device_user_code(&value) =>
        {
            Ok(())
        }
        _ => Err(invalid),
    }
}

#[cfg(test)]
mod grok_subscription_tests {
    use super::*;

    const CHALLENGE: &str = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM";
    const STATE: &str = "fixture-grok-state_123";

    fn free_port() -> u16 {
        TcpListener::bind((Ipv4Addr::LOCALHOST, 0))
            .unwrap()
            .local_addr()
            .unwrap()
            .port()
    }

    fn authorization_with(port: u16, edit: impl Fn(&mut url::Url)) -> String {
        let mut u = url::Url::parse("https://auth.x.ai/oauth2/authorize").unwrap();
        u.query_pairs_mut()
            .append_pair("response_type", "code")
            .append_pair("client_id", GROK_CLIENT_ID)
            .append_pair("redirect_uri", &format!("http://127.0.0.1:{port}/callback"))
            .append_pair(
                "scope",
                "openid profile email offline_access grok-cli:access",
            )
            .append_pair("code_challenge", CHALLENGE)
            .append_pair("code_challenge_method", "S256")
            .append_pair("state", STATE)
            .append_pair("nonce", "fixture-grok-nonce")
            .append_pair("referrer", "grok-build");
        edit(&mut u);
        u.to_string()
    }

    fn authorization(port: u16) -> String {
        authorization_with(port, |_| {})
    }

    fn without(name: &'static str) -> impl Fn(&mut url::Url) {
        move |u| {
            let kept: Vec<_> = u
                .query_pairs()
                .filter(|(k, _)| k != name)
                .map(|(k, v)| (k.into_owned(), v.into_owned()))
                .collect();
            u.query_pairs_mut().clear().extend_pairs(kept);
        }
    }

    fn replaced(name: &'static str, value: &'static str) -> impl Fn(&mut url::Url) {
        move |u| {
            let kept: Vec<_> = u
                .query_pairs()
                .map(|(k, v)| {
                    let v = if k == name {
                        value.into()
                    } else {
                        v.into_owned()
                    };
                    (k.into_owned(), v)
                })
                .collect();
            u.query_pairs_mut().clear().extend_pairs(kept);
        }
    }

    // Production leaves remote Grok forwarding disabled; these tests exercise
    // the receiver itself.
    fn remote_host() -> OAuthHost {
        let mut host = OAuthHost::default();
        host.grok_remote_forwarding = true;
        host
    }

    fn scope() -> OAuthScope {
        OAuthScope {
            window: "main".into(),
            instance: uuid::Uuid::now_v7().to_string(),
            server: uuid::Uuid::now_v7().to_string(),
            opening: uuid::Uuid::now_v7().to_string(),
            window_epoch: 0,
        }
    }

    fn deliver(port: u16, request_line: &str, host: &str) -> String {
        let mut stream = TcpStream::connect((Ipv4Addr::LOCALHOST, port)).unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(2)))
            .unwrap();
        stream
            .write_all(format!("GET {request_line} HTTP/1.1\r\nHost: {host}\r\n\r\n").as_bytes())
            .unwrap();
        let mut result = String::new();
        stream.read_to_string(&mut result).unwrap();
        result
    }

    #[test]
    fn wire_names_are_kebab_case_grok_subscription_actions() {
        for (wire, action) in [
            ("grok-subscription-open", OAuthAction::GrokSubscriptionOpen),
            (
                "grok-subscription-reopen",
                OAuthAction::GrokSubscriptionReopen,
            ),
            (
                "grok-device-authorization-open",
                OAuthAction::GrokDeviceAuthorizationOpen,
            ),
        ] {
            let parsed: OAuthAction = serde_json::from_str(&format!("\"{wire}\"")).unwrap();
            assert_eq!(parsed, action);
        }
    }

    // The renderer (apps/delidev/src/grok-subscription-login.tsx) runs the same
    // table, so both layers agree on every URL.
    #[derive(serde::Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct DeviceVerificationCase {
        name: String,
        url: String,
        native: bool,
    }

    #[derive(serde::Deserialize)]
    struct AuthorizationCase {
        name: String,
        url: String,
        valid: bool,
    }

    #[derive(serde::Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct UrlFixtures {
        device_verification: Vec<DeviceVerificationCase>,
        authorization: Vec<AuthorizationCase>,
    }

    fn url_fixtures() -> UrlFixtures {
        serde_json::from_str(include_str!("../../src/grok-url-validation-fixtures.json")).unwrap()
    }

    #[test]
    fn device_validator_matches_the_shared_url_table() {
        let fixtures = url_fixtures();
        assert!(fixtures.device_verification.len() > 20);
        for row in fixtures.device_verification {
            assert_eq!(
                validate_grok_device_authorization(&row.url).is_ok(),
                row.native,
                "{}",
                row.name
            );
        }
    }

    #[test]
    fn authorization_validator_matches_the_shared_url_table() {
        let fixtures = url_fixtures();
        assert!(fixtures.authorization.len() > 20);
        for row in fixtures.authorization {
            assert_eq!(
                validate_grok_subscription_authorization(&row.url).is_ok(),
                row.valid,
                "{}",
                row.name
            );
        }
    }

    const DEVICE_PAGE: &str = "https://accounts.x.ai/oauth2/device";
    const DEVICE_WITH_CODE: &str = "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234";

    #[test]
    fn device_validator_accepts_only_the_fixed_verification_page() {
        for accepted in [
            DEVICE_PAGE,
            DEVICE_WITH_CODE,
            "https://accounts.x.ai/oauth2/device?user_code=AB12",
        ] {
            assert!(
                validate_grok_device_authorization(accepted).is_ok(),
                "{accepted}"
            );
        }
        let long_code = format!("{DEVICE_PAGE}?user_code={}", "A".repeat(33));
        let too_long = format!("{DEVICE_PAGE}?user_code={}", "A".repeat(600));
        for rejected in [
            "https://auth.x.ai/oauth2/device",
            "https://accounts.x.ai.evil.test/oauth2/device",
            "https://evil.test/oauth2/device?user_code=ABCD-1234",
            "http://accounts.x.ai/oauth2/device",
            "https://accounts.x.ai/oauth2/authorize",
            "https://accounts.x.ai/oauth2/device/",
            "https://accounts.x.ai:8443/oauth2/device",
            "https://user@accounts.x.ai/oauth2/device",
            "https://user:secret@accounts.x.ai/oauth2/device",
            "https://accounts.x.ai/oauth2/device#fragment",
            "https://accounts.x.ai/oauth2/device?",
            "https://accounts.x.ai/oauth2/device?user_code=",
            "https://accounts.x.ai/oauth2/device?user_code=abcd-1234",
            "https://accounts.x.ai/oauth2/device?user_code=AB",
            "https://accounts.x.ai/oauth2/device?next=https://evil.test",
            "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234&next=1",
            "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234&user_code=ABCD-1234",
            "https://accounts.x.ai/oauth2/device?x=1",
            "file:///etc/passwd",
            "https://accounts.x.ai/oauth2/device ",
            "not a url",
            "",
            long_code.as_str(),
            too_long.as_str(),
        ] {
            assert!(
                matches!(
                    validate_grok_device_authorization(rejected),
                    Err(NativeFailure::InvalidInput)
                ),
                "{rejected}"
            );
        }
    }

    #[test]
    fn device_open_dispatches_the_exact_page_without_binding_or_attempt_state() {
        let host = OAuthHost::default();
        let scope = scope();
        let operation = uuid::Uuid::now_v7().to_string();
        let opened = std::sync::atomic::AtomicUsize::new(0);
        let opener = |url: &str, _: &AtomicBool| {
            assert_eq!(url, DEVICE_WITH_CODE);
            opened.fetch_add(1, Ordering::SeqCst);
            Ok(())
        };
        // A browser sign-in already bound in the same window is not disturbed,
        // and the device action never consumes the single attempt slot.
        let auth = authorization(free_port());
        let browser_operation = uuid::Uuid::now_v7().to_string();
        let bound = host
            .control_with_browser(
                scope.clone(),
                OAuthAction::GrokSubscriptionOpen,
                "",
                &browser_operation,
                &auth,
                true,
                |_, _| Ok(()),
            )
            .unwrap();
        for expected in 1..=2 {
            let result = host
                .control_with_browser(
                    scope.clone(),
                    OAuthAction::GrokDeviceAuthorizationOpen,
                    "",
                    &operation,
                    DEVICE_WITH_CODE,
                    false,
                    opener,
                )
                .unwrap();
            // Each deliberate click opens the page once; nothing is retained.
            assert!(result.generation.is_empty());
            assert!(result.callback_url.is_empty());
            assert!(result.code.is_none() && result.state.is_none());
            assert_eq!(opened.load(Ordering::SeqCst), expected);
        }
        let attempts = host.attempts.lock().unwrap();
        assert_eq!(attempts.len(), 1);
        assert_eq!(attempts[&scope.window].generation, bound.generation);
    }

    #[test]
    fn device_open_alone_creates_no_attempt_or_disposed_record() {
        let host = OAuthHost::default();
        host.control_with_browser(
            scope(),
            OAuthAction::GrokDeviceAuthorizationOpen,
            "",
            &uuid::Uuid::now_v7().to_string(),
            DEVICE_PAGE,
            true,
            |url, _| {
                assert_eq!(url, DEVICE_PAGE);
                Ok(())
            },
        )
        .unwrap();
        assert!(host.attempts.lock().unwrap().is_empty());
        assert!(host.disposed.lock().unwrap().is_empty());
    }

    #[test]
    fn device_open_rejects_bad_input_before_the_browser_opens() {
        let host = OAuthHost::default();
        let scope = scope();
        let operation = uuid::Uuid::now_v7().to_string();
        let panics = |_: &str, _: &AtomicBool| -> Result<(), NativeFailure> {
            panic!("rejected device authorization cannot open a browser")
        };
        for (generation, attempt, url) in [
            ("", operation.as_str(), "https://auth.x.ai/oauth2/device"),
            ("", operation.as_str(), "https://evil.test/oauth2/device"),
            (
                "",
                operation.as_str(),
                "https://accounts.x.ai/oauth2/device?x=1",
            ),
            ("", operation.as_str(), ""),
            ("unexpected-generation", operation.as_str(), DEVICE_PAGE),
        ] {
            let result = host.control_with_browser(
                scope.clone(),
                OAuthAction::GrokDeviceAuthorizationOpen,
                generation,
                attempt,
                url,
                true,
                panics,
            );
            assert!(
                matches!(result, Err(NativeFailure::InvalidInput)),
                "{generation:?} {attempt:?} {url:?}"
            );
        }
        // The attempt identity must be the canonical original operation.
        for attempt in ["not-an-operation", ""] {
            assert!(matches!(
                host.control_with_browser(
                    scope.clone(),
                    OAuthAction::GrokDeviceAuthorizationOpen,
                    "",
                    attempt,
                    DEVICE_PAGE,
                    true,
                    panics,
                ),
                Err(NativeFailure::InvalidEvidence)
            ));
        }
        // A browser authorization address is not a device verification page.
        let browser = authorization(43817);
        assert!(matches!(
            host.control_with_browser(
                scope,
                OAuthAction::GrokDeviceAuthorizationOpen,
                "",
                &operation,
                &browser,
                true,
                panics,
            ),
            Err(NativeFailure::InvalidInput)
        ));
        assert!(host.attempts.lock().unwrap().is_empty());
    }

    #[test]
    fn device_open_is_refused_after_dispose_and_surfaces_opener_failure() {
        let host = OAuthHost::default();
        let scope = scope();
        let operation = uuid::Uuid::now_v7().to_string();
        // An opener failure is reported to the renderer and records nothing.
        assert!(matches!(
            host.control_with_browser(
                scope.clone(),
                OAuthAction::GrokDeviceAuthorizationOpen,
                "",
                &operation,
                DEVICE_PAGE,
                true,
                |_, _| Err(NativeFailure::SidecarFailed),
            ),
            Err(NativeFailure::SidecarFailed)
        ));
        assert!(host.attempts.lock().unwrap().is_empty());
        host.control(scope.clone(), OAuthAction::Dispose, "", "", "")
            .unwrap();
        assert!(matches!(
            host.control_with_browser(
                scope,
                OAuthAction::GrokDeviceAuthorizationOpen,
                "",
                &operation,
                DEVICE_PAGE,
                true,
                |_, _| panic!("a disposed opening cannot open a browser"),
            ),
            Err(NativeFailure::Stopped)
        ));
    }

    // Device opener lifecycle (Rust half of the renderer state table):
    //   admit          -> refused when stopped, stale epoch, disposed or
    // invalid;                     otherwise registered with a per-opening
    // cancel flag   Dispose(scope) -> sets the flag of every opening in
    // that scope only   close_window   -> sets the flag of every opening in
    // that window only   stop/exhausted -> sets every flag
    //   settle         -> unregisters on success and on failure
    fn open_device(
        host: &OAuthHost,
        scope: &OAuthScope,
        opener: impl FnOnce(&str, &AtomicBool) -> Result<(), NativeFailure>,
    ) -> Result<OAuthResult, NativeFailure> {
        host.control_with_browser(
            scope.clone(),
            OAuthAction::GrokDeviceAuthorizationOpen,
            "",
            &uuid::Uuid::now_v7().to_string(),
            DEVICE_WITH_CODE,
            true,
            opener,
        )
    }
    fn registered_device_openings(host: &OAuthHost) -> usize {
        host.device_openings.lock().unwrap().len()
    }

    #[test]
    fn device_open_receives_its_own_flag_and_dispose_sets_it_while_dispatching() {
        let host = OAuthHost::default();
        let scope = scope();
        let sibling = OAuthScope {
            opening: uuid::Uuid::now_v7().to_string(),
            ..scope.clone()
        };
        open_device(&host, &scope, |_, cancel| {
            assert!(!cancel.load(Ordering::Acquire));
            assert_eq!(registered_device_openings(&host), 1);
            // A different opening in the same window is not retired.
            host.control(sibling.clone(), OAuthAction::Dispose, "", "", "")
                .unwrap();
            assert!(!cancel.load(Ordering::Acquire));
            // The Settings screen of this opening closes mid-dispatch.
            host.control(scope.clone(), OAuthAction::Dispose, "", "", "")
                .unwrap();
            assert!(cancel.load(Ordering::Acquire));
            assert!(!host.stopped.load(Ordering::Acquire));
            Ok(())
        })
        .unwrap();
        assert_eq!(registered_device_openings(&host), 0);
        // The retired scope refuses the next admission without dispatching.
        assert!(matches!(
            open_device(&host, &scope, |_, _| panic!("retired opening dispatched")),
            Err(NativeFailure::Stopped)
        ));
        assert_eq!(registered_device_openings(&host), 0);
    }

    #[test]
    fn dispose_cancels_every_concurrent_device_opening_of_the_scope() {
        let host = OAuthHost::default();
        let scope = scope();
        open_device(&host, &scope, |_, first| {
            open_device(&host, &scope, |_, second| {
                assert_eq!(registered_device_openings(&host), 2);
                host.control(scope.clone(), OAuthAction::Dispose, "", "", "")
                    .unwrap();
                assert!(first.load(Ordering::Acquire));
                assert!(second.load(Ordering::Acquire));
                Ok(())
            })
            .map(|_| ())
        })
        .unwrap();
        assert_eq!(registered_device_openings(&host), 0);
    }

    #[test]
    fn an_admitted_dispatch_observes_dispose_that_lands_before_it_starts_a_browser() {
        let host = Arc::new(OAuthHost::default());
        let scope = scope();
        let (started_tx, started_rx) = std::sync::mpsc::channel::<()>();
        let (release_tx, release_rx) = std::sync::mpsc::channel::<()>();
        let worker_host = Arc::clone(&host);
        let worker_scope = scope.clone();
        let launched = Arc::new(AtomicBool::new(false));
        let worker_launched = Arc::clone(&launched);
        let worker = thread::spawn(move || {
            // Models the dispatcher: admitted, then checks its flag right
            // before it would start the browser process.
            open_device(&worker_host, &worker_scope, move |_, cancel| {
                started_tx.send(()).unwrap();
                release_rx.recv().unwrap();
                if cancel.load(Ordering::Acquire) {
                    return Err(NativeFailure::Stopped);
                }
                worker_launched.store(true, Ordering::SeqCst);
                Ok(())
            })
        });
        started_rx.recv().unwrap();
        host.control(scope.clone(), OAuthAction::Dispose, "", "", "")
            .unwrap();
        release_tx.send(()).unwrap();
        assert!(matches!(
            worker.join().unwrap(),
            Err(NativeFailure::Stopped)
        ));
        assert!(!launched.load(Ordering::SeqCst));
        assert_eq!(registered_device_openings(&host), 0);
    }

    #[test]
    fn window_close_cancels_only_device_openings_of_that_window_and_refuses_stale_epochs() {
        let host = OAuthHost::default();
        let scope = scope();
        let other_window = OAuthScope {
            window: "secondary".into(),
            ..scope.clone()
        };
        open_device(&host, &other_window, |_, secondary| {
            open_device(&host, &scope, |_, main| {
                host.close_window("secondary").unwrap();
                assert!(secondary.load(Ordering::Acquire));
                assert!(!main.load(Ordering::Acquire));
                host.close_window("main").unwrap();
                assert!(main.load(Ordering::Acquire));
                Ok(())
            })
            .map(|_| ())
        })
        .unwrap();
        assert_eq!(registered_device_openings(&host), 0);
        // The closed window's old epoch can no longer admit a dispatch.
        assert!(matches!(
            open_device(&host, &scope, |_, _| panic!("stale epoch dispatched")),
            Err(NativeFailure::Stopped)
        ));
        assert_eq!(registered_device_openings(&host), 0);
    }

    #[test]
    fn host_stop_and_revocation_exhaustion_cancel_every_device_opening() {
        let host = OAuthHost::default();
        open_device(&host, &scope(), |_, cancel| {
            host.stop();
            assert!(cancel.load(Ordering::Acquire));
            Ok(())
        })
        .unwrap();
        assert!(matches!(
            open_device(&host, &scope(), |_, _| panic!("stopped host dispatched")),
            Err(NativeFailure::Stopped)
        ));

        let host = OAuthHost::default();
        {
            let mut disposed = host.disposed.lock().unwrap();
            for _ in 0..4096 {
                disposed.insert(OAuthScope {
                    opening: uuid::Uuid::now_v7().to_string(),
                    ..scope()
                });
            }
        }
        let admitted = scope();
        // Pre-seed capacity so the admitted opening itself is not in the set.
        open_device(&host, &admitted, |_, cancel| {
            let overflow = OAuthScope {
                opening: uuid::Uuid::now_v7().to_string(),
                ..scope()
            };
            // Capacity exhaustion closes admission for the whole host and
            // retires every admitted dispatch, even one in another scope.
            assert!(matches!(
                host.control(overflow, OAuthAction::Dispose, "", "", ""),
                Err(NativeFailure::Stopped)
            ));
            assert!(cancel.load(Ordering::Acquire));
            Ok(())
        })
        .unwrap();
        assert_eq!(registered_device_openings(&host), 0);
    }

    #[test]
    fn a_failed_device_dispatch_unregisters_and_leaves_the_scope_usable() {
        let host = OAuthHost::default();
        let scope = scope();
        assert!(matches!(
            open_device(&host, &scope, |_, _| Err(NativeFailure::TimedOut)),
            Err(NativeFailure::TimedOut)
        ));
        assert_eq!(registered_device_openings(&host), 0);
        let opened = AtomicBool::new(false);
        open_device(&host, &scope, |_, _| {
            opened.store(true, Ordering::SeqCst);
            Ok(())
        })
        .unwrap();
        assert!(opened.load(Ordering::SeqCst));
        assert_eq!(registered_device_openings(&host), 0);
        assert!(host.disposed.lock().unwrap().is_empty());
    }

    #[test]
    fn validator_accepts_the_exact_cli_shape_and_returns_state_and_port() {
        let (state, port) = validate_grok_subscription_authorization(&authorization(43817))
            .expect("exact Grok authorization");
        assert_eq!(state.as_str(), STATE);
        assert_eq!(port, 43817);
        // Scope and referrer are optional; the required members are not.
        for optional in ["scope", "referrer"] {
            assert!(
                validate_grok_subscription_authorization(&authorization_with(
                    43817,
                    without(optional)
                ))
                .is_ok()
            );
        }
    }

    #[test]
    fn validator_rejects_every_changed_or_missing_member() {
        for name in [
            "response_type",
            "client_id",
            "redirect_uri",
            "code_challenge",
            "code_challenge_method",
            "state",
            "nonce",
        ] {
            assert!(
                validate_grok_subscription_authorization(&authorization_with(43817, without(name)))
                    .is_err(),
                "missing {name}"
            );
        }
        let changed: Vec<String> = vec![
            authorization_with(43817, replaced("response_type", "token")),
            authorization_with(43817, replaced("client_id", "other-client")),
            authorization_with(43817, replaced("code_challenge_method", "plain")),
            authorization_with(43817, replaced("code_challenge", "short")),
            authorization_with(43817, replaced("state", "")),
            authorization_with(43817, replaced("state", "bad state&x=1")),
            authorization_with(43817, replaced("nonce", "")),
            authorization_with(43817, replaced("referrer", "other")),
            authorization_with(43817, replaced("scope", "openid  profile")),
            authorization_with(43817, replaced("scope", "")),
            authorization_with(43817, |u| {
                u.query_pairs_mut().append_pair("extra", "1");
            }),
            authorization_with(43817, |u| {
                u.query_pairs_mut().append_pair("state", "duplicate");
            }),
            authorization_with(43817, |u| u.set_fragment(Some("fragment"))),
            authorization_with(43817, |u| u.set_port(Some(8443)).unwrap()),
            authorization_with(43817, |u| u.set_username("user").unwrap()),
            authorization_with(43817, |u| u.set_path("/oauth2/token")),
            authorization_with(43817, |u| {
                u.set_host(Some("auth.x.ai.evil.invalid")).unwrap()
            }),
            authorization_with(43817, |u| u.set_host(Some("accounts.x.ai")).unwrap()),
            authorization_with(43817, |u| u.set_scheme("http").unwrap()),
            authorization(43817).replace("https://auth.x.ai", "https://auth.x.ai@evil.invalid"),
            authorization(43817).replace("https://auth.x.ai", "https://evil.invalid%2f@auth.x.ai"),
            format!("{} ", authorization(43817)),
            format!("{}\n", authorization(43817)),
        ];
        for raw in changed {
            assert!(
                validate_grok_subscription_authorization(&raw).is_err(),
                "accepted {}",
                raw.len()
            );
        }
    }

    #[test]
    fn validator_binds_the_redirect_to_the_ipv4_loopback_callback() {
        for redirect in [
            "http://localhost:43817/callback",
            "http://[::1]:43817/callback",
            "http://127.0.0.2:43817/callback",
            "http://127.1:43817/callback",
            "http://2130706433:43817/callback",
            "https://127.0.0.1:43817/callback",
            "http://127.0.0.1/callback",
            "http://127.0.0.1:0/callback",
            "http://127.0.0.1:80/callback",
            "http://127.0.0.1:043817/callback",
            "http://127.0.0.1:65536/callback",
            "http://127.0.0.1:+43817/callback",
            "http://127.0.0.1:43817/other",
            "http://127.0.0.1:43817/callback/",
            "http://127.0.0.1:43817/callback?x=1",
            "http://127.0.0.1:43817/callback#f",
            "http://user@127.0.0.1:43817/callback",
            "http://127.0.0.1:43817@evil.invalid/callback",
        ] {
            let raw = authorization_with(43817, |u| {
                let kept: Vec<_> = u
                    .query_pairs()
                    .map(|(k, v)| {
                        let v = if k == "redirect_uri" {
                            redirect.to_owned()
                        } else {
                            v.into_owned()
                        };
                        (k.into_owned(), v)
                    })
                    .collect();
                u.query_pairs_mut().clear().extend_pairs(kept);
            });
            assert!(
                validate_grok_subscription_authorization(&raw).is_err(),
                "accepted redirect {redirect}"
            );
        }
        assert_eq!(
            validate_grok_subscription_authorization(&authorization(65535))
                .unwrap()
                .1,
            65535
        );
    }

    #[test]
    fn local_open_binds_nothing_opens_once_and_reopens_only_the_original() {
        let port = free_port();
        let auth = authorization(port);
        let host = OAuthHost::default();
        let scope = scope();
        let operation = uuid::Uuid::now_v7().to_string();
        let opened = std::sync::atomic::AtomicUsize::new(0);
        let opener = |url: &str, _: &AtomicBool| {
            assert_eq!(url, &auth);
            opened.fetch_add(1, Ordering::SeqCst);
            Ok(())
        };
        let first = host
            .control_with_browser(
                scope.clone(),
                OAuthAction::GrokSubscriptionOpen,
                "",
                &operation,
                &auth,
                true,
                opener,
            )
            .unwrap();
        // Local mode never creates a listener, so the port stays free.
        drop(TcpListener::bind((Ipv4Addr::LOCALHOST, port)).unwrap());
        let replay = host
            .control_with_browser(
                scope.clone(),
                OAuthAction::GrokSubscriptionOpen,
                "",
                &operation,
                &auth,
                true,
                opener,
            )
            .unwrap();
        assert_eq!(first.generation, replay.generation);
        assert_eq!(opened.load(Ordering::SeqCst), 1);
        // A changed URL, another operation or another kind cannot rebind.
        let other_port = authorization(free_port());
        for (action, id, url) in [
            (
                OAuthAction::GrokSubscriptionReopen,
                operation.clone(),
                other_port.clone(),
            ),
            (
                OAuthAction::GrokSubscriptionOpen,
                uuid::Uuid::now_v7().to_string(),
                auth.clone(),
            ),
            (
                OAuthAction::SubscriptionOpen,
                operation.clone(),
                auth.clone(),
            ),
            (
                OAuthAction::ClaudeSubscriptionOpen,
                operation.clone(),
                auth.clone(),
            ),
        ] {
            assert!(
                host.control_with_browser(scope.clone(), action, "", &id, &url, true, opener)
                    .is_err()
            );
        }
        assert_eq!(opened.load(Ordering::SeqCst), 1);
        host.control_with_browser(
            scope.clone(),
            OAuthAction::GrokSubscriptionReopen,
            "",
            &operation,
            &auth,
            true,
            opener,
        )
        .unwrap();
        assert_eq!(opened.load(Ordering::SeqCst), 2);
        host.control_with_browser(
            scope.clone(),
            OAuthAction::Reopen,
            &first.generation,
            &operation,
            "",
            true,
            opener,
        )
        .unwrap();
        assert_eq!(opened.load(Ordering::SeqCst), 3);
        let taken = host
            .control(
                scope.clone(),
                OAuthAction::Take,
                &first.generation,
                &operation,
                "",
            )
            .unwrap();
        assert!(taken.code.is_none());
        host.control(scope.clone(), OAuthAction::Dispose, "", "", "")
            .unwrap();
        assert!(
            host.control_with_browser(
                scope,
                OAuthAction::GrokSubscriptionOpen,
                "",
                &operation,
                &auth,
                true,
                opener
            )
            .is_err()
        );
        assert_eq!(opened.load(Ordering::SeqCst), 3);
    }

    #[test]
    fn invalid_authorization_is_rejected_before_the_browser_opens() {
        let host = OAuthHost::default();
        let operation = uuid::Uuid::now_v7().to_string();
        let bad = authorization_with(43817, replaced("client_id", "other-client"));
        let result = host.control_with_browser(
            scope(),
            OAuthAction::GrokSubscriptionOpen,
            "",
            &operation,
            &bad,
            false,
            |_, _| panic!("invalid URL cannot open a browser"),
        );
        assert!(matches!(result, Err(NativeFailure::InvalidInput)));
        assert!(host.attempts.lock().unwrap().is_empty());
    }

    #[test]
    fn remote_receiver_accepts_one_redirect_on_a_dynamic_ipv4_port() {
        let _guard = super::tests::loopback_listener_test_guard();
        let port = free_port();
        let auth = authorization(port);
        let authority = format!("127.0.0.1:{port}");
        let host = remote_host();
        let scope = scope();
        let operation = uuid::Uuid::now_v7().to_string();
        let opened = std::sync::atomic::AtomicUsize::new(0);
        let first = host
            .control_with_browser(
                scope.clone(),
                OAuthAction::GrokSubscriptionOpen,
                "",
                &operation,
                &auth,
                false,
                |_, _| {
                    opened.fetch_add(1, Ordering::SeqCst);
                    Ok(())
                },
            )
            .unwrap();
        assert_eq!(opened.load(Ordering::SeqCst), 1);
        let good = format!("/callback?code=fixture%2Fcode&state={STATE}");
        // Wrong Host, wrong path, wrong state, missing state, unknown key and
        // denial redirects are all refused without storing a code.
        assert!(deliver(port, &good, "localhost").starts_with("HTTP/1.1 400"));
        assert!(
            deliver(
                port,
                &format!("/other?code=fixture&state={STATE}"),
                &authority
            )
            .starts_with("HTTP/1.1 400")
        );
        assert!(
            deliver(port, "/callback?code=fixture&state=wrong-state", &authority)
                .starts_with("HTTP/1.1 400")
        );
        assert!(deliver(port, "/callback?code=fixture", &authority).starts_with("HTTP/1.1 400"));
        assert!(
            deliver(
                port,
                &format!("/callback?code=fixture&state={STATE}&extra=1"),
                &authority
            )
            .starts_with("HTTP/1.1 400")
        );
        assert!(
            deliver(
                port,
                &format!("/callback?error=access_denied&state={STATE}"),
                &authority
            )
            .starts_with("HTTP/1.1 400")
        );
        assert!(
            host.control(
                scope.clone(),
                OAuthAction::Take,
                &first.generation,
                &operation,
                ""
            )
            .unwrap()
            .code
            .is_none()
        );
        assert!(deliver(port, &good, &authority).starts_with("HTTP/1.1 303"));
        // A second delivery, even a valid one, is dropped and cannot replace
        // the retained code.
        assert!(deliver(port, &good, &authority).starts_with("HTTP/1.1 400"));
        let result = host
            .control(
                scope.clone(),
                OAuthAction::Take,
                &first.generation,
                &operation,
                "",
            )
            .unwrap();
        assert_eq!(
            result.code.as_ref().unwrap(),
            format!("code=fixture%2Fcode&state={STATE}").as_bytes()
        );
        assert!(
            host.control(
                scope.clone(),
                OAuthAction::Take,
                &first.generation,
                &operation,
                ""
            )
            .unwrap()
            .code
            .is_none()
        );
        // Consumed attempts cannot reopen the browser.
        assert!(
            host.control(
                scope.clone(),
                OAuthAction::Reopen,
                &first.generation,
                &operation,
                ""
            )
            .is_err()
        );
        host.control(scope, OAuthAction::Dispose, "", "", "")
            .unwrap();
        // Disposal joins the receiver and releases the port.
        drop(TcpListener::bind((Ipv4Addr::LOCALHOST, port)).unwrap());
        assert!(TcpStream::connect((Ipv4Addr::LOCALHOST, port)).is_err());
    }

    #[test]
    fn remote_receiver_never_binds_ipv6_and_drops_delivery_after_window_close() {
        let _guard = super::tests::loopback_listener_test_guard();
        let port = free_port();
        let auth = authorization(port);
        let host = remote_host();
        let scope = scope();
        let operation = uuid::Uuid::now_v7().to_string();
        host.control_with_browser(
            scope.clone(),
            OAuthAction::GrokSubscriptionOpen,
            "",
            &operation,
            &auth,
            false,
            |_, _| Ok(()),
        )
        .unwrap();
        // The original redirect names IPv4 only, so the IPv6 loopback stays
        // free.
        if let Ok(listener) = TcpListener::bind((Ipv6Addr::LOCALHOST, port)) {
            drop(listener);
        }
        assert!(TcpStream::connect((Ipv6Addr::LOCALHOST, port)).is_err());
        host.close_window("main").unwrap();
        // The receiver is joined on close, so a late callback cannot connect.
        assert!(TcpStream::connect((Ipv4Addr::LOCALHOST, port)).is_err());
        assert!(
            host.control_with_browser(
                scope,
                OAuthAction::GrokSubscriptionOpen,
                "",
                &operation,
                &auth,
                false,
                |_, _| panic!("closed window cannot reopen"),
            )
            .is_err()
        );
    }

    #[test]
    fn remote_bind_conflict_fails_explicitly_and_never_remaps_the_port() {
        let _guard = super::tests::loopback_listener_test_guard();
        let occupied = TcpListener::bind((Ipv4Addr::LOCALHOST, 0)).unwrap();
        let port = occupied.local_addr().unwrap().port();
        let auth = authorization(port);
        let host = remote_host();
        let result = host.control_with_browser(
            scope(),
            OAuthAction::GrokSubscriptionOpen,
            "",
            &uuid::Uuid::now_v7().to_string(),
            &auth,
            false,
            |_, _| panic!("a failed bind cannot open a browser"),
        );
        assert!(matches!(result, Err(NativeFailure::SidecarFailed)));
        assert!(host.attempts.lock().unwrap().is_empty());
        assert_eq!(occupied.local_addr().unwrap().port(), port);
    }

    #[test]
    fn remote_open_is_refused_while_the_server_cannot_forward_grok_callbacks() {
        let _guard = super::tests::loopback_listener_test_guard();
        let port = free_port();
        let auth = authorization(port);
        let host = OAuthHost::default();
        let result = host.control_with_browser(
            scope(),
            OAuthAction::GrokSubscriptionOpen,
            "",
            &uuid::Uuid::now_v7().to_string(),
            &auth,
            false,
            |_, _| panic!("a refused remote login cannot open a browser"),
        );
        assert!(matches!(result, Err(NativeFailure::Incompatible)));
        assert!(host.attempts.lock().unwrap().is_empty());
        // No listener was created, so the numeric port is still free.
        drop(TcpListener::bind((Ipv4Addr::LOCALHOST, port)).unwrap());
        // An invalid URL is still reported as invalid input, and a local login
        // is unaffected because the server owns that loopback listener.
        let bad = authorization_with(port, replaced("client_id", "other-client"));
        let invalid = host.control_with_browser(
            scope(),
            OAuthAction::GrokSubscriptionOpen,
            "",
            &uuid::Uuid::now_v7().to_string(),
            &bad,
            false,
            |_, _| panic!("invalid URL cannot open a browser"),
        );
        assert!(matches!(invalid, Err(NativeFailure::InvalidInput)));
        host.control_with_browser(
            scope(),
            OAuthAction::GrokSubscriptionOpen,
            "",
            &uuid::Uuid::now_v7().to_string(),
            &auth,
            true,
            |_, _| Ok(()),
        )
        .unwrap();
    }

    fn shared_for_handler() -> Shared {
        Shared {
            expected_state: Some(Zeroizing::new(STATE.to_owned())),
            api_state: Mutex::new(None),
            api_profile: None,
            bound: AtomicBool::new(true),
            consumed: AtomicBool::new(false),
            stop: AtomicBool::new(false),
            code: Mutex::new(None),
        }
    }

    // Runs the real `handle_request` against a real loopback connection that
    // already carries one complete, valid Grok redirect.
    fn run_handler(shared: &Shared, deadline: Instant) -> String {
        let listener = TcpListener::bind((Ipv4Addr::LOCALHOST, 0)).unwrap();
        let port = listener.local_addr().unwrap().port();
        let mut client = TcpStream::connect((Ipv4Addr::LOCALHOST, port)).unwrap();
        client
            .set_read_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        client
            .write_all(
                format!(
                    "GET /callback?code=fixture&state={STATE} HTTP/1.1\r\nHost: \
                     127.0.0.1:{port}\r\n\r\n"
                )
                .as_bytes(),
            )
            .unwrap();
        let (mut stream, _) = listener.accept().unwrap();
        handle_request(
            &mut stream,
            &format!("127.0.0.1:{port}"),
            GROK_CALLBACK_PATH,
            shared,
            deadline,
        );
        drop(stream);
        let mut response = String::new();
        let _ = client.read_to_string(&mut response);
        response
    }

    #[test]
    fn handler_stores_an_in_time_callback_and_drops_one_past_the_deadline() {
        let in_time = shared_for_handler();
        let response = run_handler(&in_time, Instant::now() + Duration::from_secs(60));
        assert!(response.starts_with("HTTP/1.1 303"));
        assert!(in_time.consumed.load(Ordering::Acquire));
        assert!(in_time.code.lock().unwrap().is_some());

        // A complete, valid request handled at or after the deadline is dropped
        // without consuming the one-shot slot or storing a code. The peer may
        // observe a reset because the request was never read.
        let expired = shared_for_handler();
        let response = run_handler(&expired, Instant::now());
        assert!(!response.starts_with("HTTP/1.1 303"));
        assert!(!expired.consumed.load(Ordering::Acquire));
        assert!(expired.code.lock().unwrap().is_none());
    }

    #[test]
    fn claim_rechecks_the_deadline_immediately_before_the_compare_and_swap() {
        // The request was read in time, but the deadline passed before the
        // claim.
        let shared = shared_for_handler();
        assert!(!claim_callback(&shared, Instant::now()));
        assert!(!shared.consumed.load(Ordering::Acquire));
        // The slot stays claimable until the deadline, and only once.
        let deadline = Instant::now() + Duration::from_secs(60);
        assert!(claim_callback(&shared, deadline));
        assert!(!claim_callback(&shared, deadline));
        // Stopped and unbound attempts never claim.
        let stopped = shared_for_handler();
        stopped.stop.store(true, Ordering::Release);
        assert!(!claim_callback(&stopped, deadline));
        let unbound = shared_for_handler();
        unbound.bound.store(false, Ordering::Release);
        assert!(!claim_callback(&unbound, deadline));
    }

    #[test]
    fn listener_drops_a_delivery_that_completes_after_the_attempt_expires() {
        let _guard = super::tests::loopback_listener_test_guard();
        let port = free_port();
        let attempt = begin_grok_subscription_for(
            scope(),
            &uuid::Uuid::now_v7().to_string(),
            &authorization(port),
            false,
            true,
            Duration::from_millis(400),
        )
        .unwrap();
        let shared = Arc::clone(&attempt.shared);
        // The peer connects in time (so the listener accepts it) but sends its
        // request only after the attempt expired.
        let mut client = TcpStream::connect((Ipv4Addr::LOCALHOST, port)).unwrap();
        client
            .set_read_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        thread::sleep(Duration::from_millis(600));
        let _ = client.write_all(
            format!(
                "GET /callback?code=fixture&state={STATE} HTTP/1.1\r\nHost: \
                 127.0.0.1:{port}\r\n\r\n"
            )
            .as_bytes(),
        );
        let mut response = String::new();
        let _ = client.read_to_string(&mut response);
        assert!(!response.starts_with("HTTP/1.1 303"));
        assert!(!shared.consumed.load(Ordering::Acquire));
        assert!(shared.code.lock().unwrap().is_none());
        drop(attempt);
    }

    #[test]
    fn expired_remote_attempt_stops_accepting_and_drops_the_code() {
        let _guard = super::tests::loopback_listener_test_guard();
        let port = free_port();
        let auth = authorization(port);
        let host = remote_host();
        let scope = scope();
        let operation = uuid::Uuid::now_v7().to_string();
        let first = host
            .control_with_browser(
                scope.clone(),
                OAuthAction::GrokSubscriptionOpen,
                "",
                &operation,
                &auth,
                false,
                |_, _| Ok(()),
            )
            .unwrap();
        host.attempts
            .lock()
            .unwrap()
            .get_mut(&scope.window)
            .unwrap()
            .until = Instant::now();
        let result = host.control(scope, OAuthAction::Take, &first.generation, &operation, "");
        assert!(matches!(result, Err(NativeFailure::InvalidEvidence)));
        assert!(host.attempts.lock().unwrap().is_empty());
    }
}
