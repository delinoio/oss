// SPDX-License-Identifier: Apache-2.0
use std::{
    collections::HashMap,
    ffi::OsString,
    io::{BufRead, BufReader, Read, Write},
    process::{Child, ChildStdin, Command},
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
        mpsc,
    },
    thread,
    time::{Duration, Instant},
};

use base64::{Engine, engine::general_purpose::STANDARD};
use serde::{Deserialize, Serialize};

use crate::{
    CliEnvelope, Connector, NativeFailure, ORIGINS, OUTPUT_LIMIT, Result, canonical_id,
    read_bounded,
};

const SHUTDOWN_TIMEOUT: Duration = Duration::from_secs(35);
const MAX_HOST_CHILDREN: usize = 8;
const STOP: &[u8] = b"{\"version\":1,\"action\":\"stop\"}\n";

// Retain the original child rather than deriving termination authority from a
// server PID or endpoint. Even a launch without a ready reply remains owned.
pub(crate) struct DesktopChild {
    child: Child,
    input: Option<ChildStdin>,
    output: thread::JoinHandle<Result<Vec<u8>>>,
    diagnostic: thread::JoinHandle<Result<Vec<u8>>>,
    worker: bool,
    generation: Option<String>,
    admission_confirmed: bool,
}

impl DesktopChild {
    fn join(self) {
        let _ = self.output.join();
        let _ = self.diagnostic.join();
    }
}

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Runtime {
    pub endpoint: String,
    pub generation: String,
    pub key: String,
}
impl Drop for Runtime {
    fn drop(&mut self) {
        use zeroize::Zeroize;
        self.key.zeroize();
    }
}
impl Runtime {
    fn validate(&self) -> Result<()> {
        canonical_id(&self.generation)?;
        let u = url::Url::parse(&self.endpoint).map_err(|_| NativeFailure::InvalidEvidence)?;
        if u.scheme() != "http"
            || u.host_str() != Some("127.0.0.1")
            || u.port().is_none_or(|p| p == 0)
            || u.origin().ascii_serialization() != self.endpoint
            || base64::engine::general_purpose::URL_SAFE_NO_PAD
                .decode(&self.key)
                .map_or(true, |v| v.len() != 32)
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(())
    }
}
impl Connector {
    pub(crate) fn run_desktop_host(&self, action: &str) -> Result<serde_json::Value> {
        if self.exiting.load(std::sync::atomic::Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        let mode = match action {
            "desktop-launch" => "launch",
            "desktop-retry" => "retry",
            "ensure" => "ensure",
            _ => return Err(NativeFailure::InvalidInput),
        };
        let mut args = self.server_arguments("desktop-host");
        args.extend(["--mode".into(), mode.into()]);
        self.run_host_child(&self.executable, &args, false)
    }

    pub(crate) fn reap_worker_children(&self) {
        let mut hosted = self.hosted.lock().unwrap_or_else(|e| e.into_inner());
        let mut i = 0;
        while i < hosted.len() {
            if matches!(hosted[i].child.try_wait(), Ok(Some(_))) {
                let exited = hosted.remove(i);
                if exited.worker && exited.generation.is_some() {
                    *self.worker_exited.lock().unwrap_or_else(|e| e.into_inner()) =
                        exited.generation.clone();
                }
                exited.join();
            } else {
                i += 1;
            }
        }
    }

    pub(crate) fn owns_worker_generation(&self, generation: &str) -> bool {
        self.hosted
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .iter()
            .any(|child| child.worker && child.generation.as_deref() == Some(generation))
    }

    pub(crate) fn worker_admission_unconfirmed(&self) -> bool {
        self.hosted
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .iter()
            .any(|child| child.worker && !child.admission_confirmed)
    }

    pub(crate) fn run_host_child(
        &self,
        executable: &std::path::Path,
        args: &[std::ffi::OsString],
        worker: bool,
    ) -> Result<serde_json::Value> {
        if self.exiting.load(std::sync::atomic::Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        self.reap_worker_children();
        {
            let hosted = self.hosted.lock().unwrap_or_else(|e| e.into_inner());
            if hosted.len() >= MAX_HOST_CHILDREN {
                return Err(NativeFailure::Busy);
            }
        }
        let mut command = self.sidecar_command_at(executable, args, true)?;
        // Isolate development terminal/process-group signals too. No
        // kill-on-parent-exit job or EOF shutdown is installed.
        #[cfg(unix)]
        {
            use std::os::unix::process::CommandExt;
            command.process_group(0);
        }
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x0000_0208); // NEW_PROCESS_GROUP | DETACHED_PROCESS
        }
        let mut child = command.spawn().map_err(|_| NativeFailure::SidecarFailed)?;
        let original_child = child.id();
        let input = child.stdin.take();
        let stdout = child.stdout.take().ok_or(NativeFailure::SidecarFailed)?;
        let stderr = child.stderr.take().ok_or(NativeFailure::SidecarFailed)?;
        let (send, receive) = mpsc::sync_channel(1);
        let output = thread::spawn(move || {
            let mut reader = BufReader::new(stdout);
            let mut first = Vec::new();
            let result = reader
                .by_ref()
                .take(OUTPUT_LIMIT + 1)
                .read_until(b'\n', &mut first)
                .map_err(|_| NativeFailure::SidecarFailed)
                .and_then(|_| {
                    if first.len() as u64 > OUTPUT_LIMIT {
                        return Err(NativeFailure::InvalidEvidence);
                    }
                    host_result(&first)
                });
            let _ = send.send(result);
            read_bounded(reader, OUTPUT_LIMIT)
        });
        let diagnostic = thread::spawn(move || read_bounded(stderr, OUTPUT_LIMIT));
        {
            let mut hosted = self.hosted.lock().unwrap_or_else(|e| e.into_inner());
            hosted.push(DesktopChild {
                child,
                input,
                output,
                diagnostic,
                worker,
                generation: None,
                admission_confirmed: false,
            });
        }
        let started = Instant::now();
        loop {
            if self.exiting.load(std::sync::atomic::Ordering::Acquire) {
                return Err(NativeFailure::Stopped);
            }
            match receive.recv_timeout(Duration::from_millis(25)) {
                Ok(result) => {
                    if let Ok(value) = &result {
                        let generation =
                            if value.get("started").and_then(|v| v.as_bool()) == Some(true) {
                                Some(
                                    value
                                        .get("generation")
                                        .and_then(|v| v.as_str())
                                        .ok_or(NativeFailure::InvalidEvidence)?
                                        .to_owned(),
                                )
                            } else {
                                None
                            };
                        let mut hosted = self.hosted.lock().unwrap_or_else(|e| e.into_inner());
                        if let Some(child) = hosted
                            .iter_mut()
                            .find(|child| child.child.id() == original_child)
                        {
                            child.generation = generation;
                            child.admission_confirmed = true;
                        }
                    }
                    return result;
                }
                Err(mpsc::RecvTimeoutError::Disconnected) => {
                    return Err(NativeFailure::SidecarFailed);
                }
                Err(mpsc::RecvTimeoutError::Timeout)
                    if started.elapsed() < self.command_timeout => {}
                Err(_) => return Err(NativeFailure::TimedOut),
            }
        }
    }

    pub fn shutdown_owned(&self) -> Result<()> {
        self.exiting
            .store(true, std::sync::atomic::Ordering::Release);
        let resident = self.session.shutdown(SHUTDOWN_TIMEOUT);
        let hosted = self.shutdown_owned_with_timeout(SHUTDOWN_TIMEOUT);
        resident.and(hosted)
    }

    fn shutdown_owned_with_timeout(&self, timeout: Duration) -> Result<()> {
        self.exiting
            .store(true, std::sync::atomic::Ordering::Release);
        // Join commands admitted before Quit before taking the complete set.
        // New commands cannot cross their exiting checks after this gate.
        let _gate = self.gate.lock().unwrap_or_else(|e| e.into_inner());
        let mut children =
            std::mem::take(&mut *self.hosted.lock().unwrap_or_else(|e| e.into_inner()));
        let deadline = Instant::now() + timeout;
        for owned in &mut children {
            if let Some(mut input) = owned.input.take() {
                let _ = input.write_all(STOP);
            }
            tracing::info!(operation = "desktop_sidecar_shutdown", phase = "requested");
        }
        let mut children = children.into_iter();
        while let Some(mut owned) = children.next() {
            loop {
                match owned.child.try_wait() {
                    Ok(Some(status)) => {
                        tracing::info!(
                            operation = "desktop_sidecar_shutdown",
                            phase = "process-exit-confirmed",
                            forced = false,
                            successful = status.success()
                        );
                        break;
                    }
                    Ok(None) | Err(_) if Instant::now() < deadline => {
                        thread::sleep(Duration::from_millis(25))
                    }
                    _ => {
                        tracing::warn!(
                            operation = "desktop_sidecar_shutdown",
                            phase = "force-requested",
                            native_cleanup = "unconfirmed"
                        );
                        if owned.child.kill().is_err() {
                            // An exit may race Kill. Recheck the retained
                            // child; never fall
                            // back to signaling a discovered PID.
                            if !matches!(owned.child.try_wait(), Ok(Some(_))) {
                                tracing::error!(
                                    operation = "desktop_sidecar_shutdown",
                                    phase = "process-exit-unconfirmed"
                                );
                                // Preserve handles when termination is
                                // uncertain.
                                let mut retained =
                                    self.hosted.lock().unwrap_or_else(|e| e.into_inner());
                                retained.push(owned);
                                retained.extend(children);
                                return Err(NativeFailure::SidecarFailed);
                            }
                        }
                        if owned.child.wait().is_err() {
                            let mut retained =
                                self.hosted.lock().unwrap_or_else(|e| e.into_inner());
                            retained.push(owned);
                            retained.extend(children);
                            return Err(NativeFailure::SidecarFailed);
                        }
                        tracing::info!(
                            operation = "desktop_sidecar_shutdown",
                            phase = "process-exit-confirmed",
                            forced = true,
                            native_cleanup = "unconfirmed"
                        );
                        break;
                    }
                }
            }
            // Join drains only after observed exit. They cannot inherit server
            // business logs or keep the native UI loop waiting for this
            // process.
            if owned.child.try_wait().is_ok_and(|s| s.is_some()) {
                owned.join();
            }
        }
        Ok(())
    }
}

fn host_result(bytes: &[u8]) -> Result<serde_json::Value> {
    let envelope: CliEnvelope =
        serde_json::from_slice(bytes).map_err(|_| NativeFailure::InvalidEvidence)?;
    if envelope.version != 1 {
        return Err(NativeFailure::Incompatible);
    }
    if let Some(error) = envelope.error {
        return Err(match error.code.as_str() {
            "unsupported" => NativeFailure::Incompatible,
            "invalid_argument" | "missing_input" => NativeFailure::InvalidInput,
            "unauthenticated" => NativeFailure::CredentialUnavailable,
            "permission_denied" => NativeFailure::PermissionDenied,
            "conflict" => NativeFailure::Busy,
            "recovery_required" => NativeFailure::InvalidEvidence,
            _ => NativeFailure::SidecarFailed,
        });
    }
    let result = envelope.result.ok_or(NativeFailure::InvalidEvidence)?;
    if result.get("started").and_then(|v| v.as_bool()) == Some(true) {
        canonical_id(
            result
                .get("generation")
                .and_then(|v| v.as_str())
                .ok_or(NativeFailure::InvalidEvidence)?,
        )?;
    }
    Ok(result)
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Reply {
    version: u32,
    #[serde(default)]
    id: String,
    result: Option<serde_json::Value>,
    error: Option<crate::CliFailure>,
}
#[derive(Serialize)]
struct Request {
    version: u32,
    id: String,
    operation: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    arguments: Vec<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    input: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    scope: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    request_id: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    cancel_id: String,
    timeout_ms: u64,
}
impl Request {
    fn control(operation: &str) -> Self {
        Self {
            version: 2,
            id: uuid::Uuid::now_v7().to_string(),
            operation: operation.into(),
            arguments: vec![],
            input: String::new(),
            scope: String::new(),
            request_id: String::new(),
            cancel_id: String::new(),
            timeout_ms: 0,
        }
    }
}
type Pending = Arc<Mutex<HashMap<String, mpsc::SyncSender<Result<serde_json::Value>>>>>;
struct Pipe {
    runtime: Runtime,
    input: mpsc::SyncSender<Vec<u8>>,
    writer_stop: Arc<AtomicBool>,
    pending: Pending,
    broken: AtomicBool,
}
impl Pipe {
    fn write(&self, request: &Request) -> Result<()> {
        if self.writer_stop.load(Ordering::Acquire) {
            return Err(NativeFailure::SidecarFailed);
        }
        let mut raw = zeroize::Zeroizing::new(
            serde_json::to_vec(request).map_err(|_| NativeFailure::InvalidInput)?,
        );
        if raw.len() > 256 << 10 {
            return Err(NativeFailure::InvalidInput);
        }
        raw.push(b'\n');
        // Callers and the UI fence never wait for a blocked child stdin. The
        // bounded writer is joined only after original-child exit/force.
        match self.input.try_send(std::mem::take(&mut *raw)) {
            Ok(()) => Ok(()),
            Err(error) => {
                use zeroize::Zeroize;
                let (mut frame, code) = match error {
                    mpsc::TrySendError::Full(frame) => (frame, NativeFailure::Busy),
                    mpsc::TrySendError::Disconnected(frame) => {
                        (frame, NativeFailure::SidecarFailed)
                    }
                };
                frame.zeroize();
                Err(code)
            }
        }
    }

    fn request(
        &self,
        request: &Request,
        exiting: &AtomicBool,
        timeout: Duration,
    ) -> Result<serde_json::Value> {
        if exiting.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        if self.broken.load(Ordering::Acquire) {
            return Err(NativeFailure::SidecarFailed);
        }
        let (send, receive) = mpsc::sync_channel(1);
        {
            let mut pending = self.pending.lock().map_err(|_| NativeFailure::Busy)?;
            if pending.len() >= 32 {
                return Err(NativeFailure::Busy);
            }
            pending.insert(request.id.clone(), send);
        }
        if let Err(e) = self.write(request) {
            self.pending
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .remove(&request.id);
            return Err(e);
        }
        let deadline = Instant::now() + timeout;
        let result = loop {
            if exiting.load(Ordering::Acquire) {
                break Err(NativeFailure::Stopped);
            }
            match receive.recv_timeout(Duration::from_millis(25)) {
                Ok(result) => break result,
                Err(mpsc::RecvTimeoutError::Disconnected) => {
                    break Err(NativeFailure::SidecarFailed);
                }
                Err(_) if Instant::now() < deadline => {}
                Err(_) => break Err(NativeFailure::TimedOut),
            }
        };
        self.pending
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .remove(&request.id);
        if result.is_err() {
            let mut cancel = Request::control("runtime.cancel");
            cancel.cancel_id = request.id.clone();
            let _ = self.write(&cancel);
        }
        result
    }

    fn cancel_all(&self) {
        let ids: Vec<_> = self
            .pending
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .keys()
            .cloned()
            .collect();
        for id in ids {
            let mut cancel = Request::control("runtime.cancel");
            cancel.cancel_id = id;
            let _ = self.write(&cancel);
        }
    }
}
struct Owned {
    child: Child,
    pipe: Option<Arc<Pipe>>,
    startup_input: Option<ChildStdin>,
    output: Option<thread::JoinHandle<()>>,
    writer: Option<thread::JoinHandle<()>>,
    diagnostic: thread::JoinHandle<()>,
    _containment: Containment,
}
impl Owned {
    fn finish(mut self, timeout: Duration) -> Result<()> {
        let deadline = Instant::now() + timeout;
        let pipe = self.pipe.clone();
        let startup_input = self.startup_input.take();
        // A pipe writer cannot hold the original-child exit/force deadline.
        // Force joins its blocked write only after the child's observed exit.
        let shutdown = thread::spawn(move || {
            if let Some(pipe) = pipe {
                let _ = pipe.write(&Request::control("runtime.shutdown"));
            }
            if let Some(mut input) = startup_input
                && let Ok(mut frame) = serde_json::to_vec(&Request::control("runtime.shutdown"))
            {
                frame.push(b'\n');
                let _ = input.write_all(&frame);
            }
        });
        let mut forced = false;
        let exit_success;
        loop {
            match self.child.try_wait() {
                Ok(Some(status)) => {
                    exit_success = status.success();
                    break;
                }
                Ok(None) if Instant::now() < deadline => thread::sleep(Duration::from_millis(25)),
                _ => {
                    forced = true;
                    if self.child.kill().is_err() && !matches!(self.child.try_wait(), Ok(Some(_))) {
                        tracing::error!(
                            operation = "desktop_sidecar_shutdown",
                            phase = "force-unconfirmed"
                        );
                        // Never release original native containment or replace
                        // a child whose actual exit could not be established.
                        std::mem::forget(self);
                        return Err(NativeFailure::SidecarFailed);
                    }
                    exit_success = match self.child.wait() {
                        Ok(status) => status.success(),
                        Err(_) => {
                            tracing::error!(
                                operation = "desktop_sidecar_shutdown",
                                phase = "exit-unconfirmed"
                            );
                            std::mem::forget(self);
                            return Err(NativeFailure::SidecarFailed);
                        }
                    };
                    break;
                }
            }
        }
        tracing::info!(
            operation = "desktop_sidecar_shutdown",
            phase = "process-exit-confirmed",
            forced,
            exit_success,
            native_cleanup = if !forced && exit_success && self.pipe.is_some() {
                "joined"
            } else {
                "unconfirmed"
            }
        );
        if let Some(pipe) = &self.pipe {
            pipe.writer_stop.store(true, Ordering::Release);
        }
        let _ = shutdown.join();
        if let Some(writer) = self.writer.take() {
            let _ = writer.join();
        }
        if let Some(output) = self.output.take() {
            let _ = output.join();
        }
        let _ = self.diagnostic.join();
        Ok(())
    }
}
enum Control {
    #[cfg(test)]
    Crash(mpsc::SyncSender<Result<()>>),
    Get(
        Box<Command>,
        Duration,
        bool,
        mpsc::SyncSender<Result<Arc<Pipe>>>,
    ),
    Shutdown(Duration, mpsc::SyncSender<Result<()>>),
}
// The original spawn thread remains alive for every owned child. Linux's
// parent-death signal is tied to that thread, not just the process ID.
pub(crate) struct Session {
    control: mpsc::Sender<Control>,
    task: Mutex<Option<thread::JoinHandle<()>>>,
    stopped: AtomicBool,
    fenced: Arc<AtomicBool>,
    pinned: Arc<Mutex<Option<String>>>,
    pipe: Mutex<Option<Arc<Pipe>>>,
}
impl Session {
    pub fn new() -> Self {
        let (send, receive) = mpsc::channel();
        let pinned = Arc::new(Mutex::new(None));
        let chosen = Arc::clone(&pinned);
        let fenced = Arc::new(AtomicBool::new(false));
        let lifetime_fence = Arc::clone(&fenced);
        let task = thread::spawn(move || {
            let mut owned: Option<Owned> = None;
            while let Ok(control) = receive.recv() {
                match control {
                    #[cfg(test)]
                    Control::Crash(send) => {
                        let result = if let Some(owned) = &mut owned {
                            owned
                                .child
                                .kill()
                                .and_then(|_| owned.child.wait())
                                .map(|_| ())
                                .map_err(|_| NativeFailure::SidecarFailed)
                        } else {
                            Err(NativeFailure::SidecarFailed)
                        };
                        let _ = send.send(result);
                    }
                    Control::Get(command, timeout, allow_spawn, send) => {
                        if let Some(child) = &mut owned {
                            match child.child.try_wait() {
                                Ok(Some(_)) => {
                                    if owned.take().unwrap().finish(Duration::ZERO).is_err() {
                                        lifetime_fence.store(true, Ordering::Release);
                                        let _ = send.send(Err(NativeFailure::SidecarFailed));
                                        continue;
                                    }
                                }
                                Ok(None) => {
                                    let result =
                                        child.pipe.clone().ok_or(NativeFailure::SidecarFailed);
                                    let _ = send.send(result);
                                    continue;
                                }
                                Err(_) => {
                                    let _ = send.send(Err(NativeFailure::SidecarFailed));
                                    continue;
                                }
                            }
                        }
                        if !allow_spawn || lifetime_fence.load(Ordering::Acquire) {
                            let _ = send.send(Err(NativeFailure::Stopped));
                            continue;
                        }
                        let result = spawn(*command, timeout, &chosen, &lifetime_fence);
                        match result {
                            Ok(child) => {
                                let pipe = child.pipe.clone().ok_or(NativeFailure::SidecarFailed);
                                owned = Some(child);
                                let _ = send.send(pipe);
                            }
                            Err(e) => {
                                let _ = send.send(Err(e));
                            }
                        }
                    }
                    Control::Shutdown(timeout, send) => {
                        let result = owned.take().map_or(Ok(()), |child| child.finish(timeout));
                        let _ = send.send(result);
                        break;
                    }
                }
            }
            if let Some(child) = owned {
                let _ = child.finish(SHUTDOWN_TIMEOUT);
            }
        });
        Self {
            control: send,
            task: Mutex::new(Some(task)),
            stopped: AtomicBool::new(false),
            fenced,
            pinned,
            pipe: Mutex::new(None),
        }
    }

    fn get(&self, connector: &Connector) -> Result<Arc<Pipe>> {
        if self.stopped.load(Ordering::Acquire) || connector.exiting.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        let listen = self
            .pinned
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .clone()
            .map(|u| u.trim_start_matches("http://").to_owned())
            .unwrap_or_else(|| connector.listen.clone());
        let args = [
            "server".into(),
            "desktop-host".into(),
            "--control-version".into(),
            "2".into(),
            "--listen".into(),
            listen.into(),
            "--allowed-origins".into(),
            ORIGINS.into(),
            "--parent-pid".into(),
            std::process::id().to_string().into(),
        ];
        let command = connector.sidecar_command(&args, true)?;
        let (send, receive) = mpsc::sync_channel(1);
        self.control
            .send(Control::Get(
                Box::new(command),
                connector.command_timeout,
                !self.fenced.load(Ordering::Acquire),
                send,
            ))
            .map_err(|_| NativeFailure::SidecarFailed)?;
        let result = receive.recv().map_err(|_| NativeFailure::SidecarFailed)??;
        *self.pipe.lock().map_err(|_| NativeFailure::Busy)? = Some(Arc::clone(&result));
        Ok(result)
    }

    pub fn fence(&self) {
        self.fenced.store(true, Ordering::Release);
        if let Some(pipe) = &*self.pipe.lock().unwrap_or_else(|e| e.into_inner()) {
            let _ = pipe.write(&Request::control("runtime.fence"));
        }
        self.cancel_all();
    }

    #[cfg(test)]
    pub fn crash_for_test(&self) -> Result<()> {
        let (send, receive) = mpsc::sync_channel(1);
        self.control
            .send(Control::Crash(send))
            .map_err(|_| NativeFailure::SidecarFailed)?;
        receive.recv().map_err(|_| NativeFailure::SidecarFailed)?
    }

    pub fn cancel_all(&self) {
        if let Some(pipe) = &*self.pipe.lock().unwrap_or_else(|e| e.into_inner()) {
            pipe.cancel_all();
        }
    }

    fn shutdown(&self, timeout: Duration) -> Result<()> {
        if self.stopped.swap(true, Ordering::AcqRel) {
            return Ok(());
        }
        self.fence();
        let (send, receive) = mpsc::sync_channel(1);
        self.control
            .send(Control::Shutdown(timeout, send))
            .map_err(|_| NativeFailure::SidecarFailed)?;
        let result = receive.recv().map_err(|_| NativeFailure::SidecarFailed)?;
        if let Some(task) = self.task.lock().unwrap_or_else(|e| e.into_inner()).take() {
            let _ = task.join();
        }
        result
    }
}
impl Drop for Session {
    fn drop(&mut self) {
        let _ = self.shutdown(SHUTDOWN_TIMEOUT);
    }
}
fn frame(reader: &mut impl BufRead) -> Result<Reply> {
    let mut raw = zeroize::Zeroizing::new(Vec::new());
    reader
        .take(OUTPUT_LIMIT + 1)
        .read_until(b'\n', &mut raw)
        .map_err(|_| NativeFailure::SidecarFailed)?;
    if raw.len() as u64 > OUTPUT_LIMIT || raw.last() != Some(&b'\n') {
        return Err(NativeFailure::InvalidEvidence);
    }
    let reply: Reply = serde_json::from_slice(&raw).map_err(|_| NativeFailure::InvalidEvidence)?;
    if reply.version != 2 {
        return Err(NativeFailure::Incompatible);
    }
    Ok(reply)
}
fn reply_result(reply: Reply) -> Result<serde_json::Value> {
    if let Some(error) = reply.error {
        return Err(match error.code.as_str() {
            "unsupported" => NativeFailure::Incompatible,
            "invalid_argument" | "missing_input" => NativeFailure::InvalidInput,
            "unauthenticated" => NativeFailure::CredentialUnavailable,
            "permission_denied" => NativeFailure::PermissionDenied,
            "conflict" => NativeFailure::Busy,
            "recovery_required" => NativeFailure::InvalidEvidence,
            _ => NativeFailure::SidecarFailed,
        });
    }
    reply.result.ok_or(NativeFailure::InvalidEvidence)
}
fn spawn(
    mut command: Command,
    timeout: Duration,
    pinned: &Mutex<Option<String>>,
    fenced: &AtomicBool,
) -> Result<Owned> {
    let (mut child, containment) = contained_spawn(&mut command)?;
    let (input, stdout, mut stderr) =
        match (child.stdin.take(), child.stdout.take(), child.stderr.take()) {
            (Some(input), Some(stdout), Some(stderr)) => (input, stdout, stderr),
            _ => {
                if child.kill().is_err() && !matches!(child.try_wait(), Ok(Some(_)))
                    || child.wait().is_err()
                {
                    tracing::error!(
                        operation = "desktop_sidecar_spawn",
                        phase = "exit-unconfirmed"
                    );
                    std::mem::forget((child, containment));
                }
                return Err(NativeFailure::SidecarFailed);
            }
        };
    let diagnostic = thread::spawn(move || {
        let mut chunk = [0; 4096];
        while let Ok(n) = stderr.read(&mut chunk) {
            if n == 0 {
                break;
            } /* Drain continuously; never retain secret diagnostic contents. */
        }
    });
    let (send, receive) = mpsc::sync_channel(1);
    let reader = thread::spawn(move || {
        let mut reader = BufReader::new(stdout);
        let result = frame(&mut reader);
        let _ = send.send((reader, result));
    });
    let mut owned = Owned {
        child,
        pipe: None,
        startup_input: Some(input),
        output: Some(reader),
        writer: None,
        diagnostic,
        _containment: containment,
    };
    let deadline = Instant::now() + timeout;
    let hello = loop {
        if fenced.load(Ordering::Acquire) {
            break Err(NativeFailure::Stopped);
        }
        match receive.recv_timeout(Duration::from_millis(25)) {
            Ok(value) => break Ok(value),
            Err(mpsc::RecvTimeoutError::Disconnected) => break Err(NativeFailure::SidecarFailed),
            Err(_) if Instant::now() < deadline => {}
            Err(_) => break Err(NativeFailure::TimedOut),
        }
    };
    let (mut reader, reply) = match hello {
        Ok(value) => value,
        Err(e) => {
            if owned
                .finish(if e == NativeFailure::Stopped {
                    SHUTDOWN_TIMEOUT
                } else {
                    Duration::ZERO
                })
                .is_err()
            {
                fenced.store(true, Ordering::Release);
                return Err(NativeFailure::SidecarFailed);
            }
            return Err(e);
        }
    };
    let value = reply
        .and_then(|r| {
            if !r.id.is_empty() {
                Err(NativeFailure::InvalidEvidence)
            } else {
                reply_result(r)
            }
        })
        .and_then(|v| {
            serde_json::from_value::<Runtime>(v).map_err(|_| NativeFailure::InvalidEvidence)
        });
    let runtime = match value.and_then(|r| {
        r.validate()?;
        let mut chosen = pinned.lock().map_err(|_| NativeFailure::Busy)?;
        if chosen
            .as_ref()
            .is_some_and(|endpoint| endpoint != &r.endpoint)
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        *chosen = Some(r.endpoint.clone());
        Ok(r)
    }) {
        Ok(r) => r,
        Err(e) => {
            if owned.finish(Duration::ZERO).is_err() {
                fenced.store(true, Ordering::Release);
                return Err(NativeFailure::SidecarFailed);
            }
            return Err(e);
        }
    };
    let pending: Pending = Arc::new(Mutex::new(HashMap::new()));
    let (input, frames) = mpsc::sync_channel::<Vec<u8>>(64);
    let writer_stop = Arc::new(AtomicBool::new(false));
    let stopped = Arc::clone(&writer_stop);
    let mut stdin = owned.startup_input.take().expect("retained piped stdin");
    owned.writer = Some(thread::spawn(move || {
        use zeroize::Zeroize;
        while !stopped.load(Ordering::Acquire) {
            match frames.recv_timeout(Duration::from_millis(25)) {
                Ok(mut frame) => {
                    let result = stdin.write_all(&frame).and_then(|_| stdin.flush());
                    frame.zeroize();
                    if result.is_err() {
                        break;
                    }
                }
                Err(mpsc::RecvTimeoutError::Timeout) => continue,
                Err(_) => break,
            }
        }
        stopped.store(true, Ordering::Release);
        for mut frame in frames.try_iter() {
            frame.zeroize();
        }
    }));
    let pipe = Arc::new(Pipe {
        runtime,
        input,
        writer_stop,
        pending: Arc::clone(&pending),
        broken: AtomicBool::new(false),
    });
    let output_pipe = Arc::clone(&pipe);
    if let Some(first) = owned.output.take() {
        let _ = first.join();
    }
    owned.output = Some(thread::spawn(move || {
        while let Ok(reply) = frame(&mut reader) {
            if canonical_id(&reply.id).is_err() {
                break;
            }
            let request = pending
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .remove(&reply.id);
            if let Some(send) = request {
                let _ = send.send(reply_result(reply));
            }
        }
        output_pipe.broken.store(true, Ordering::Release);
        for (_, send) in pending.lock().unwrap_or_else(|e| e.into_inner()).drain() {
            let _ = send.send(Err(NativeFailure::SidecarFailed));
        }
    }));
    owned.pipe = Some(pipe);
    tracing::info!(operation = "desktop_sidecar", phase = "pipe-ready");
    Ok(owned)
}
impl Connector {
    pub fn current_runtime_endpoint(&self) -> Result<String> {
        self.session
            .pinned
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .clone()
            .ok_or(NativeFailure::SidecarFailed)
    }

    pub fn runtime_endpoint(&self) -> Result<String> {
        Ok(self.session.get(self)?.runtime.endpoint.clone())
    }

    pub(crate) fn runtime(&self) -> Result<Runtime> {
        Ok(self.session.get(self)?.runtime.clone())
    }

    pub(crate) fn resident_request(
        &self,
        arguments: &[OsString],
        input: Option<zeroize::Zeroizing<Vec<u8>>>,
        timeout: Duration,
    ) -> Result<serde_json::Value> {
        let mut args: Vec<String> = arguments
            .iter()
            .map(|v| {
                v.to_str()
                    .map(str::to_owned)
                    .ok_or(NativeFailure::InvalidInput)
            })
            .collect::<Result<_>>()?;
        let mut scope = String::new();
        let mut request_id = String::new();
        let mut i = 0;
        while i < args.len() {
            let name = args[i].as_str();
            if [
                "--data-dir",
                "--request-id",
                "--device-dir",
                "--worker-dir",
                "--listen",
                "--allowed-origins",
            ]
            .contains(&name)
            {
                let value = args.get(i + 1).ok_or(NativeFailure::InvalidInput)?.clone();
                match name {
                    "--data-dir" => {
                        let path = std::path::PathBuf::from(value);
                        if path == self.root.join("desktop-client") {
                            scope = "local".into();
                        } else {
                            let relative = path
                                .strip_prefix(self.root.join("connections"))
                                .map_err(|_| NativeFailure::InvalidInput)?;
                            let parts: Vec<_> = relative.iter().collect();
                            if parts.len() != 2 || parts[1] != "client" {
                                return Err(NativeFailure::InvalidInput);
                            }
                            let id = parts[0].to_str().ok_or(NativeFailure::InvalidInput)?;
                            canonical_id(id)?;
                            scope = format!("saved:{id}");
                        }
                    }
                    "--request-id" => {
                        canonical_id(&value)?;
                        request_id = value;
                    }
                    "--device-dir"
                        if std::path::Path::new(&value) != self.root.join("desktop-client") =>
                    {
                        return Err(NativeFailure::InvalidInput);
                    }
                    "--worker-dir" if std::path::Path::new(&value) != self.root.join("worker") => {
                        return Err(NativeFailure::InvalidInput);
                    }
                    _ => {}
                }
                args.drain(i..i + 2);
            } else if name == "--join-existing" {
                args.remove(i);
            } else {
                i += 1;
            }
        }
        let count = if args.first().is_some_and(|v| v == "worker")
            && args.get(1).is_some_and(|v| v == "network")
        {
            3
        } else {
            2
        };
        if args.len() < count {
            return Err(NativeFailure::InvalidInput);
        }
        let mut request = Request::control(&args[..count].join("."));
        request.arguments = args[count..].to_vec();
        request.scope = scope;
        request.request_id = request_id;
        request.timeout_ms = timeout.as_millis().min(660000) as u64;
        if let Some(bytes) = &input {
            request.input = STANDARD.encode(bytes);
        }
        let result = self
            .session
            .get(self)?
            .request(&request, &self.exiting, timeout);
        use zeroize::Zeroize;
        request.input.zeroize();
        result
    }
}
#[cfg(not(windows))]
struct Containment;
#[cfg(not(windows))]
fn contained_spawn(command: &mut Command) -> Result<(Child, Containment)> {
    use std::os::unix::process::CommandExt;
    command.process_group(0);
    #[cfg(target_os = "linux")]
    unsafe {
        let parent = libc::getpid();
        command.pre_exec(move || {
            if libc::prctl(libc::PR_SET_PDEATHSIG, libc::SIGKILL) != 0 {
                return Err(std::io::Error::last_os_error());
            }
            if libc::getppid() != parent {
                return Err(std::io::Error::other("original desktop exited"));
            }
            Ok(())
        });
    }
    Ok((
        command.spawn().map_err(|_| NativeFailure::SidecarFailed)?,
        Containment,
    ))
}
#[cfg(windows)]
#[path = "desktop_job.rs"]
mod job;
#[cfg(windows)]
use job::{Containment, contained_spawn};

#[cfg(all(test, unix))]
mod tests {
    use std::{fs, process::Stdio};

    use super::*;
    fn binary() -> std::path::PathBuf {
        std::env::var_os("DELIDEV_TEST_SIDECAR")
            .expect("explicit fixture binary")
            .into()
    }
    fn fixture(root: &std::path::Path, body: &str) -> Arc<Connector> {
        use std::os::unix::fs::PermissionsExt;
        let script = root.join("lifetime-fixture");
        fs::write(
            &script,
            format!(
                "#!/usr/bin/python3\nimport \
                 sys,os,json,time\nroot=sys.argv[2]\nos.makedirs(root,exist_ok=True)\nopen(os.\
                 path.join(root,'entered'),'w').write(str(os.getpid()))\n{body}\n"
            ),
        )
        .unwrap();
        fs::set_permissions(&script, fs::Permissions::from_mode(0o700)).unwrap();
        Arc::new(Connector::new(script, root.join("private")).unwrap())
    }
    const HELLO: &str = "print(json.dumps({'version':2,'result':{'endpoint':'http://127.0.0.1:51234','generation':'019c2381-9300-7000-8000-000000000001','key':'CQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQk'}}),flush=True)";

    #[test]
    fn quit_before_hello_joins_the_original_child() {
        let temp = tempfile::tempdir().unwrap();
        let connector = fixture(
            temp.path(),
            "for line in sys.stdin:\n if json.loads(line)['operation']=='runtime.shutdown': break",
        );
        let starting = Arc::clone(&connector);
        let task = thread::spawn(move || starting.runtime_endpoint());
        let marker = connector.root.join("entered");
        let deadline = Instant::now() + Duration::from_secs(5);
        while !marker.exists() && Instant::now() < deadline {
            thread::sleep(Duration::from_millis(10));
        }
        assert!(marker.exists());
        connector.shutdown_owned().unwrap();
        assert_eq!(task.join().unwrap(), Err(NativeFailure::Stopped));
        assert_eq!(connector.runtime_endpoint(), Err(NativeFailure::Stopped));
    }

    #[test]
    fn continuous_stderr_does_not_consume_the_frame_bound() {
        let temp = tempfile::tempdir().unwrap();
        let connector = fixture(
            temp.path(),
            &format!(
                "sys.stderr.write('x'*(1024*1024))\nsys.stderr.flush()\n{HELLO}\nfor line in \
                 sys.stdin:\n if json.loads(line)['operation']=='runtime.shutdown': break"
            ),
        );
        assert_eq!(
            connector.runtime_endpoint().unwrap(),
            "http://127.0.0.1:51234"
        );
        connector.shutdown_owned().unwrap();
    }

    #[test]
    #[ignore = "real original-child force after the product's 35-second shutdown allowance"]
    fn delayed_shutdown_forces_and_observes_only_the_original_child() {
        let temp = tempfile::tempdir().unwrap();
        let connector = fixture(temp.path(), &format!("{HELLO}\nwhile True: time.sleep(1)"));
        connector.runtime_endpoint().unwrap();
        let started = Instant::now();
        connector.shutdown_owned().unwrap();
        assert!(started.elapsed() >= SHUTDOWN_TIMEOUT);
        assert!(started.elapsed() < SHUTDOWN_TIMEOUT + Duration::from_secs(5));
        assert_eq!(connector.runtime_endpoint(), Err(NativeFailure::Stopped));
    }
    #[test]
    #[ignore = "requires an explicitly built Go sidecar; occupies only its own listener"]
    fn pinned_port_collision_never_adopts_or_remaps() {
        let temp = tempfile::tempdir().unwrap();
        let connector = Connector::new(binary(), temp.path().join("server")).unwrap();
        let original = connector.connect().unwrap();
        connector.session.crash_for_test().unwrap();
        let listener =
            std::net::TcpListener::bind(original.endpoint.trim_start_matches("http://")).unwrap();
        listener.set_nonblocking(true).unwrap();
        assert!(matches!(connector.ensure(), Err(NativeFailure::Busy)));
        assert_eq!(
            connector.current_runtime_endpoint().unwrap(),
            original.endpoint
        );
        assert_eq!(
            listener.accept().unwrap_err().kind(),
            std::io::ErrorKind::WouldBlock
        );
        connector.shutdown_owned().unwrap();
    }
    #[test]
    #[ignore = "only an explicitly reexecuted lifetime fixture enters this process"]
    fn owned_parent_fixture() {
        let root: std::path::PathBuf = std::env::var_os("DELIDEV_TEST_PARENT_ROOT")
            .expect("explicit parent root")
            .into();
        let connector = Connector::new(binary(), root.clone()).unwrap();
        let connection = connector.connect().unwrap();
        fs::write(root.join("parent-ready"), connection.endpoint.as_bytes()).unwrap();
        loop {
            thread::park();
        }
    }
    #[test]
    #[ignore = "requires an explicitly built sidecar; kills only its original fixture parent"]
    fn forced_parent_exit_retires_its_owned_host() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("server");
        let mut parent = Command::new(std::env::current_exe().unwrap())
            .args([
                "--exact",
                "desktop_host::tests::owned_parent_fixture",
                "--ignored",
                "--nocapture",
            ])
            .env("DELIDEV_TEST_PARENT_ROOT", &root)
            .env("DELIDEV_TEST_SIDECAR", binary())
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .unwrap();
        struct ParentCleanup<'a>(&'a mut Child);
        impl Drop for ParentCleanup<'_> {
            fn drop(&mut self) {
                let _ = self.0.kill();
                let _ = self.0.wait();
            }
        }
        let parent = ParentCleanup(&mut parent);
        let deadline = Instant::now() + Duration::from_secs(40);
        while !root.join("parent-ready").exists() {
            assert!(Instant::now() < deadline);
            assert!(parent.0.try_wait().unwrap().is_none());
            thread::sleep(Duration::from_millis(25));
        }
        let endpoint = fs::read_to_string(root.join("parent-ready")).unwrap();
        parent.0.kill().unwrap();
        parent.0.wait().unwrap();
        let deadline = Instant::now() + Duration::from_secs(40);
        while std::net::TcpStream::connect(endpoint.trim_start_matches("http://")).is_ok() {
            assert!(
                Instant::now() < deadline,
                "original parent loss left its listener alive"
            );
            thread::sleep(Duration::from_millis(25));
        }
        // macOS EOF/NOTE_EXIT joins retire the locator. Linux SIGKILL confirms
        // process exit but does not claim a joined native cleanup observation.
        #[cfg(target_os = "macos")]
        {
            while root.join("desktop-runtime.json").exists() {
                assert!(Instant::now() < deadline);
                thread::sleep(Duration::from_millis(25));
            }
        }
    }

    fn worker_fixture(script: &str) -> (tempfile::TempDir, Arc<Connector>) {
        use std::os::unix::fs::PermissionsExt;

        let root = tempfile::tempdir().unwrap();
        let executable = root.path().join("sidecar");
        // A joined writer child prevents sibling fixture forks inheriting an
        // open writable executable description (Linux ETXTBSY).
        let mut writer = Command::new("/bin/sh")
            .env_clear()
            .args(["-c", "umask 077; /bin/cat > \"$1\"", "fixture"])
            .arg(&executable)
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .unwrap();
        writer
            .stdin
            .take()
            .unwrap()
            .write_all(script.as_bytes())
            .unwrap();
        assert!(writer.wait().unwrap().success());
        std::fs::set_permissions(&executable, std::fs::Permissions::from_mode(0o700)).unwrap();
        let connector = Arc::new(Connector::new(executable, root.path().to_path_buf()).unwrap());
        (root, connector)
    }

    fn ready() -> String {
        format!(
            "printf '%s\\n' \
             '{{\"version\":1,\"result\":{{\"started\":true,\"generation\":\"{}\"}}}}'\n",
            uuid::Uuid::now_v7()
        )
    }

    #[test]
    fn worker_exit_records_only_the_retained_original_generation() {
        let generation = uuid::Uuid::now_v7().to_string();
        let admission =
            serde_json::json!({"version":1,"result":{"started":true,"generation":generation}});
        let (_root, connector) = worker_fixture(&format!(
            "#!/bin/sh\nprintf '%s\\n' '{admission}'\nread action\n"
        ));
        connector
            .run_host_child(&connector.executable, &[], true)
            .unwrap();
        assert!(connector.owns_worker_generation(&generation));
        {
            let mut children = connector.hosted.lock().unwrap();
            children[0].child.kill().unwrap();
            children[0].child.wait().unwrap();
        }
        connector.reap_worker_children();
        assert_eq!(*connector.worker_exited.lock().unwrap(), Some(generation));
        assert!(connector.hosted.lock().unwrap().is_empty());
    }

    #[test]
    fn unconfirmed_worker_admission_blocks_another_native_child() {
        let (_root, connector) = worker_fixture("#!/bin/sh\nprintf '%s\\n' '{}'\nread action\n");
        assert!(
            connector
                .run_host_child(&connector.executable, &[], true)
                .is_err()
        );
        assert!(connector.worker_admission_unconfirmed());
        connector.manage_worker();
        assert_eq!(
            connector.worker_management.lock().unwrap().state,
            crate::LocalWorkerManagementState::Blocked
        );
        assert_eq!(connector.hosted.lock().unwrap().len(), 1);
        connector.shutdown_owned().unwrap();
    }

    #[test]
    fn quit_requests_original_child_and_joins_it_once() {
        let script = format!(
            "#!/bin/sh\n{}IFS= read -r control\n[ \"$control\" = \
             '{{\"version\":1,\"action\":\"stop\"}}' ] || exit 3\nprintf done > \"$2/joined\"\n",
            ready()
        );
        let (root, connector) = worker_fixture(&script);
        assert_eq!(
            connector.run_desktop_host("desktop-launch").unwrap()["started"],
            true
        );
        connector.shutdown_owned().unwrap();
        assert_eq!(std::fs::read(root.path().join("joined")).unwrap(), b"done");
        assert!(connector.hosted.lock().unwrap().is_empty());
        connector.shutdown_owned().unwrap();
        assert_eq!(
            connector.run_desktop_host("ensure"),
            Err(NativeFailure::Stopped)
        );
    }

    #[test]
    fn hung_original_child_is_forced_after_the_grace_period() {
        let (root, connector) =
            worker_fixture(&format!("#!/bin/sh\n{}while :; do :; done\n", ready()));
        connector.run_desktop_host("desktop-launch").unwrap();
        let started = Instant::now();
        connector
            .shutdown_owned_with_timeout(Duration::from_millis(75))
            .unwrap();
        assert!(started.elapsed() >= Duration::from_millis(75));
        assert!(started.elapsed() < Duration::from_secs(3));
        assert!(connector.hosted.lock().unwrap().is_empty());
        assert!(!root.path().join("joined").exists());
    }

    #[test]
    #[ignore = "Runs the production 35-second grace period against a hung process fixture."]
    fn production_quit_deadline_forces_and_joins_original_child() {
        let (_root, connector) =
            worker_fixture(&format!("#!/bin/sh\n{}exec /bin/sleep 120\n", ready()));
        connector.run_desktop_host("desktop-launch").unwrap();
        let started = Instant::now();
        connector.shutdown_owned().unwrap();
        assert!(started.elapsed() >= SHUTDOWN_TIMEOUT);
        assert!(started.elapsed() < SHUTDOWN_TIMEOUT + Duration::from_secs(10));
        assert!(connector.hosted.lock().unwrap().is_empty());
    }

    #[test]
    fn quit_during_startup_retains_and_joins_the_unreported_child() {
        let (root, connector) = worker_fixture(
            "#!/bin/sh\nprintf started > \"$2/started\"\nIFS= read -r control\nprintf stopped > \
             \"$2/stopped\"\n",
        );
        let launching = Arc::clone(&connector);
        let task = thread::spawn(move || {
            let _gate = launching.gate.lock().unwrap();
            launching.run_desktop_host("desktop-launch")
        });
        let limit = Instant::now() + Duration::from_secs(3);
        while !root.path().join("started").exists() {
            assert!(Instant::now() < limit);
            thread::sleep(Duration::from_millis(10));
        }
        connector.shutdown_owned().unwrap();
        assert_eq!(task.join().unwrap(), Err(NativeFailure::Stopped));
        assert!(root.path().join("stopped").exists());
        assert!(connector.hosted.lock().unwrap().is_empty());
    }

    #[test]
    fn control_eof_preserves_running_child_until_explicit_fixture_cleanup() {
        let script = format!(
            "#!/bin/sh\n{}if IFS= read -r control; then exit 0; fi\nprintf eof > \
             \"$2/eof\"\nwhile :; do :; done\n",
            ready()
        );
        let (root, connector) = worker_fixture(&script);
        connector.run_desktop_host("desktop-launch").unwrap();
        connector.hosted.lock().unwrap()[0].input.take();
        let limit = Instant::now() + Duration::from_secs(3);
        while !root.path().join("eof").exists() {
            assert!(Instant::now() < limit);
            thread::sleep(Duration::from_millis(10));
        }
        assert!(
            connector.hosted.lock().unwrap()[0]
                .child
                .try_wait()
                .unwrap()
                .is_none()
        );
        connector
            .shutdown_owned_with_timeout(Duration::ZERO)
            .unwrap();
    }

    #[test]
    fn reused_controller_has_no_authority_over_an_external_server() {
        let (root, connector) = worker_fixture(
            "#!/bin/sh\nprintf '%s\\n' '{\"version\":1,\"result\":{\"reused\":true}}'\n",
        );
        assert_eq!(
            connector.run_desktop_host("desktop-launch").unwrap()["reused"],
            true
        );
        connector.shutdown_owned().unwrap();
        assert!(!root.path().join("joined").exists());
    }
}
