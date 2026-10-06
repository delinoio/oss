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
    Profiles,
    BindOpen,
    SubscriptionOpen,
    SubscriptionReopen,
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
}

fn registered_client(profile: OAuthProfile) -> Option<String> {
    if profile == OAuthProfile::Openrouter {
        return Some(String::new());
    }
    let registrations: serde_json::Value = serde_json::from_str(include_str!(
        "../../../../cmds/delidev-cli/internal/providers/oauth_clients.json"
    ))
    .ok()?;
    let registration = registrations.get("hugging-face")?;
    let id = registration.get("client_id")?.as_str()?;
    (registration.get("registration")?.as_str()? == "registered"
        && registration.get("api_compatibility")?.as_str()? == "accepted"
        && registration.get("redirect_uri")?.as_str()?
            == "http://localhost/oauth/hugging-face/callback"
        && !id.is_empty()
        && id.len() <= 256)
        .then(|| id.to_owned())
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
    bound: AtomicBool,
    consumed: AtomicBool,
    stop: AtomicBool,
    code: Mutex<Option<Zeroizing<Vec<u8>>>>,
}
struct Attempt {
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
#[derive(Default)]
pub struct OAuthHost {
    disposed: Mutex<std::collections::BTreeSet<OAuthScope>>,
    attempts: Mutex<BTreeMap<String, Attempt>>,
    epochs: Mutex<BTreeMap<String, u64>>,
    stopped: AtomicBool,
}

impl OAuthHost {
    pub fn window_epoch(&self, window: &str) -> Result<u64, NativeFailure> {
        let mut epochs = self.epochs.lock().map_err(|_| NativeFailure::Busy)?;
        if !epochs.contains_key(window) && epochs.len() >= 4096 {
            return Err(NativeFailure::Busy);
        }
        Ok(*epochs.entry(window.into()).or_default())
    }

    pub fn close_window(&self, window: &str) {
        // Joining a listener never waits for CEF or a business operation.
        if let Ok(mut attempts) = self.attempts.lock() {
            if let Ok(mut epochs) = self.epochs.lock() {
                let epoch = epochs.entry(window.into()).or_default();
                if let Some(next) = epoch.checked_add(1) {
                    *epoch = next;
                } else {
                    self.stopped.store(true, Ordering::Release);
                }
            }
            if let Some(original) = attempts.remove(window)
                && let Ok(mut disposed) = self.disposed.lock()
            {
                disposed.insert(original.scope.clone());
            }
        }
    }

    pub fn stop(&self) {
        self.stopped.store(true, Ordering::Release);
        if let Ok(mut attempts) = self.attempts.lock() {
            attempts.clear();
        }
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
        let mut attempts = self.attempts.lock().map_err(|_| NativeFailure::Busy)?;
        if action != OAuthAction::Dispose && self.window_epoch(&scope.window)? != scope.window_epoch
        {
            return Err(NativeFailure::Stopped);
        }
        {
            let mut disposed = self.disposed.lock().map_err(|_| NativeFailure::Busy)?;
            attempts.retain(|_, v| {
                if Instant::now() >= v.until {
                    disposed.insert(v.scope.clone());
                    false
                } else {
                    true
                }
            });
        }
        if matches!(
            action,
            OAuthAction::SubscriptionOpen | OAuthAction::SubscriptionReopen
        ) {
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
                    || original.shared.expected_state.is_none()
                {
                    return Err(NativeFailure::InvalidEvidence);
                }
                // Exact initial binding replay never opens another browser.
                // Deliberate reopen recovers a lost binding reply and opens
                // this same original address exactly once.
                if action == OAuthAction::SubscriptionReopen {
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
            let original = begin_subscription(scope, attempt_id, authorization, local)?;
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
                profiles: [OAuthProfile::Openrouter, OAuthProfile::HuggingFace]
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
        if matches!(action, OAuthAction::Begin | OAuthAction::BeginHuggingFace) {
            let profile = if action == OAuthAction::Begin {
                OAuthProfile::Openrouter
            } else {
                OAuthProfile::HuggingFace
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
                if original.scope == scope && original.profile == profile {
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
            self.disposed
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .insert(scope.clone());
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
                } else {
                    let client = registered_client(original.profile)
                        .ok_or(NativeFailure::InvalidEvidence)?;
                    let state = validate_hugging_face_authorization(
                        authorization,
                        &original.callback,
                        &client,
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
                if original.attempt != attempt_id || !authorization.is_empty() {
                    return Err(NativeFailure::InvalidEvidence);
                }
                let code = original
                    .shared
                    .code
                    .lock()
                    .map_err(|_| NativeFailure::Busy)?
                    .take();
                if original.profile == OAuthProfile::HuggingFace {
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
    let path = if profile == OAuthProfile::HuggingFace {
        "/oauth/hugging-face/callback".into()
    } else {
        format!(
            "/oauth/openrouter/{}{}",
            uuid::Uuid::now_v7().simple(),
            uuid::Uuid::now_v7().simple()
        )
    };
    let callback = format!("http://localhost:{port}{path}");
    let generation = uuid::Uuid::now_v7().to_string();
    let shared = Arc::new(Shared {
        expected_state: None,
        api_state: Mutex::new(None),
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
            let host = format!("localhost:{port}");
            while !control.stop.load(Ordering::Acquire) && Instant::now() < until {
                for listener in [&v4, &v6] {
                    if let Ok((mut stream, peer)) = listener.accept() {
                        if !peer.ip().is_loopback() {
                            continue;
                        }
                        handle_request(&mut stream, &host, &path, &control);
                    }
                }
                thread::sleep(Duration::from_millis(10));
            }
        })
        .map_err(|_| NativeFailure::SidecarFailed)?;
    Ok(Attempt {
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
    parse_request_profile(raw, host, path, state, false)
}
fn parse_request_profile(
    raw: &[u8],
    host: &str,
    path: &str,
    state: Option<&str>,
    api: bool,
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
            if !(matches!(key, "code" | "state" | "scope") || api && key == "error")
                || fields.insert(key, decode_code(value)?).is_some()
            {
                return None;
            }
        }
        let supplied = fields.get("state")?;
        if (!fields.contains_key("code")
            && !(api
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
fn handle_request(stream: &mut TcpStream, host: &str, path: &str, shared: &Shared) {
    let _ = stream.set_read_timeout(Some(Duration::from_millis(25)));
    let _ = stream.set_write_timeout(Some(Duration::from_millis(100)));
    let until = Instant::now() + Duration::from_secs(1);
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
        state.is_some(),
    );
    let common = "Cache-Control: no-store\r\nReferrer-Policy: \
                  no-referrer\r\nContent-Security-Policy: default-src 'none'; frame-ancestors \
                  'none'\r\nConnection: close\r\n";
    let response = match parsed {
        Some(Some(code))
            if shared.bound.load(Ordering::Acquire)
                && !shared.stop.load(Ordering::Acquire)
                && shared
                    .consumed
                    .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
                    .is_ok() =>
        {
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
fn validate_hugging_face_authorization(
    raw: &str,
    callback: &str,
    client: &str,
) -> Result<Zeroizing<String>, NativeFailure> {
    if raw.len() > 4096 || raw.chars().any(char::is_control) {
        return Err(NativeFailure::InvalidInput);
    }
    let url = url::Url::parse(raw).map_err(|_| NativeFailure::InvalidInput)?;
    if url.scheme() != "https"
        || url.host_str() != Some("huggingface.co")
        || url.path() != "/oauth/authorize"
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
    if fields.len() != 7
        || get("client_id") != Some(client)
        || get("redirect_uri") != Some(callback)
        || get("response_type") != Some("code")
        || get("scope") != Some("inference-api")
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
    fn scope() -> OAuthScope {
        OAuthScope {
            window: "main".into(),
            instance: "fixture-window".into(),
            server: uuid::Uuid::now_v7().to_string(),
            opening: uuid::Uuid::now_v7().to_string(),
            window_epoch: 0,
        }
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
    fn hugging_face_profile_requires_registration_and_original_state() {
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
        host.close_window("main");
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
        host.close_window("main");
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
