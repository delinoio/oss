// SPDX-License-Identifier: Apache-2.0
//! Window-owned callback infrastructure. Go alone creates PKCE, exchanges the
//! code, and owns account/credential state. No callback is broadcast or saved.
use std::{
    collections::BTreeMap,
    io::{Read, Write},
    net::{Ipv4Addr, Ipv6Addr, TcpListener, TcpStream},
    process::{Command, Stdio},
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

#[derive(Clone, Copy, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum OAuthAction {
    Begin,
    BindOpen,
    Reopen,
    Take,
    Dispose,
}
#[derive(Default, Serialize)]
pub struct OAuthResult {
    pub generation: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub callback_url: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub code: Option<Vec<u8>>,
}
impl Drop for OAuthResult {
    fn drop(&mut self) {
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
    bound: AtomicBool,
    consumed: AtomicBool,
    stop: AtomicBool,
    code: Mutex<Option<Zeroizing<Vec<u8>>>>,
}
struct Attempt {
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
            if let Some(original) = attempts.remove(window) {
                if let Ok(mut disposed) = self.disposed.lock() {
                    disposed.insert(original.scope.clone());
                }
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

    fn control_with_opener(
        &self,
        scope: OAuthScope,
        action: OAuthAction,
        generation: &str,
        attempt_id: &str,
        authorization: &str,
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
        if action == OAuthAction::Begin {
            let disposed = self.disposed.lock().map_err(|_| NativeFailure::Busy)?;
            if disposed.len() >= 4096 || disposed.contains(&scope) {
                return Err(NativeFailure::Stopped);
            }
            drop(disposed);
            if !generation.is_empty() || !attempt_id.is_empty() || !authorization.is_empty() {
                return Err(NativeFailure::InvalidInput);
            }
            if let Some(original) = attempts.get(&scope.window) {
                if original.scope == scope {
                    return Ok(OAuthResult {
                        generation: original.generation.clone(),
                        callback_url: original.callback.clone(),
                        code: None,
                    });
                }
                return Err(NativeFailure::Busy);
            }
            if attempts.len() >= 32 {
                return Err(NativeFailure::Busy);
            }
            let attempt = begin(scope)?;
            let result = OAuthResult {
                generation: attempt.generation.clone(),
                callback_url: attempt.callback.clone(),
                code: None,
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
            if let Some(original) = attempts.get(&scope.window) {
                if original.scope == scope
                    && (generation.is_empty() || original.generation == generation)
                {
                    attempts.remove(&scope.window);
                }
            }
            return Ok(OAuthResult {
                generation: generation.into(),
                callback_url: String::new(),
                code: None,
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
                    return Err(NativeFailure::Busy);
                }
                validate_authorization(authorization, &original.callback)?;
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
                return Ok(OAuthResult {
                    generation: generation.into(),
                    callback_url: String::new(),
                    code: code.map(|mut value| {
                        let result = value.to_vec();
                        value.zeroize();
                        result
                    }),
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
        })
    }
}
impl Drop for OAuthHost {
    fn drop(&mut self) {
        self.stop();
    }
}

fn begin(scope: OAuthScope) -> Result<Attempt, NativeFailure> {
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
    let path = format!(
        "/oauth/openrouter/{}{}",
        uuid::Uuid::now_v7().simple(),
        uuid::Uuid::now_v7().simple()
    );
    let callback = format!("http://localhost:{port}{path}");
    let generation = uuid::Uuid::now_v7().to_string();
    let shared = Arc::new(Shared {
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
fn parse_request(raw: &[u8], host: &str, path: &str) -> Option<Option<Zeroizing<Vec<u8>>>> {
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
        if name.eq_ignore_ascii_case("transfer-encoding")
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
    if request_path != path || query.contains('&') || query.contains('#') {
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
    let parsed = parse_request(&raw, host, path);
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
fn open_authorization(url: &str, stopped: &AtomicBool) -> Result<(), NativeFailure> {
    if stopped.load(Ordering::Acquire) {
        return Err(NativeFailure::Stopped);
    }
    #[cfg(target_os = "macos")]
    let mut command = Command::new("/usr/bin/open");
    #[cfg(target_os = "linux")]
    let mut command = Command::new("/usr/bin/xdg-open");
    #[cfg(windows)]
    let mut command = {
        let root = std::env::var_os("SystemRoot").ok_or(NativeFailure::SidecarMissing)?;
        let mut command =
            Command::new(std::path::PathBuf::from(root).join("System32/rundll32.exe"));
        command.arg("url.dll,FileProtocolHandler");
        command
    };
    command
        .arg(url)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .env_clear();
    for name in [
        "HOME",
        "USERPROFILE",
        "SystemRoot",
        "WINDIR",
        "TMPDIR",
        "TEMP",
        "TMP",
        "DISPLAY",
        "XAUTHORITY",
        "XDG_RUNTIME_DIR",
        "DBUS_SESSION_BUS_ADDRESS",
        "XDG_CURRENT_DESKTOP",
    ] {
        if let Some(value) = std::env::var_os(name) {
            command.env(name, value);
        }
    }
    #[cfg(unix)]
    command.env("PATH", "/usr/bin:/bin:/usr/sbin:/sbin");
    let mut child = command.spawn().map_err(|_| NativeFailure::SidecarFailed)?;
    let until = Instant::now() + Duration::from_secs(5);
    loop {
        match child.try_wait() {
            Ok(Some(status)) => {
                return if status.success() {
                    Ok(())
                } else {
                    Err(NativeFailure::SidecarFailed)
                };
            }
            Ok(None) if Instant::now() < until && !stopped.load(Ordering::Acquire) => {
                thread::sleep(Duration::from_millis(10))
            }
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return Err(NativeFailure::TimedOut);
            }
        }
    }
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
                .is_err()
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
