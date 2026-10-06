use std::{
    ffi::OsString,
    fs::{self, File},
    io::{Read, Write},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::{
        Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread,
    time::{Duration, Instant},
};

use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use serde::{Deserialize, Serialize};
use zeroize::{Zeroize, Zeroizing};

pub mod appearance;
mod browser_opener;
pub mod oauth;
pub mod provider_guidance;
pub mod updater;
pub mod window_registry;

// Covers 32 bounded profile records, including JSON-escaped display names.
const OUTPUT_LIMIT: u64 = 128 << 10;
const COMMAND_TIMEOUT: Duration = Duration::from_secs(40);
const ORIGINS: &str = "tauri://localhost,http://tauri.localhost,http://127.0.0.1:46311";

// A GUI launch does not run the user's shell. Keep OS utilities first, then
// bounded absolute lookup context and the standard macOS Homebrew locations.
// This supplies executable lookup only; no other inherited variables survive.
fn sidecar_lookup_path(inherited: Option<&std::ffi::OsStr>) -> OsString {
    #[cfg(unix)]
    let mut paths: Vec<PathBuf> = ["/usr/bin", "/bin", "/usr/sbin", "/sbin"]
        .into_iter()
        .map(PathBuf::from)
        .collect();
    #[cfg(windows)]
    let mut paths = {
        let root = std::env::var_os("SystemRoot")
            .map(PathBuf::from)
            .filter(|p| p.is_absolute())
            .unwrap_or_else(|| PathBuf::from(r"C:\Windows"));
        vec![
            root.join("System32"),
            root.clone(),
            root.join("System32/WindowsPowerShell/v1.0"),
        ]
    };
    if let Some(value) = inherited.filter(|value| value.as_encoded_bytes().len() <= 32768) {
        for path in std::env::split_paths(value)
            .take(64)
            .filter(|path| path.is_absolute())
        {
            #[cfg(windows)]
            let duplicate = paths.iter().any(|prior| {
                prior
                    .to_string_lossy()
                    .eq_ignore_ascii_case(&path.to_string_lossy())
            });
            #[cfg(unix)]
            let duplicate = paths.contains(&path);
            if !duplicate {
                paths.push(path);
            }
        }
    }
    #[cfg(target_os = "macos")]
    for path in ["/opt/homebrew/bin", "/usr/local/bin"] {
        let path = PathBuf::from(path);
        if !paths.contains(&path) {
            paths.push(path);
        }
    }
    #[cfg(target_os = "linux")]
    if !paths.contains(&PathBuf::from("/usr/local/bin")) {
        paths.push(PathBuf::from("/usr/local/bin"));
    }
    std::env::join_paths(paths).unwrap_or_default()
}

pub mod browser;
mod connections;
pub use connections::{
    RemovalMetadata, RemovedConnections, SavedConnection, SavedConnectionState, canonical_id,
    connection_origin,
};

mod desktop_recovery;
pub use desktop_recovery::{DesktopRegistration, DesktopRegistrationState};

mod local_worker;
mod worker_network;
pub use local_worker::{LocalWorkerAction, LocalWorkerState, LocalWorkerStatus};
pub use worker_network::WorkerNetworkAction;

mod desktop_host;
mod supervision;
pub use supervision::{LocalServerState, LocalServerStatus, Supervision};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum NativeFailure {
    ServiceManaged,
    Busy,
    SidecarMissing,
    SidecarFailed,
    TimedOut,
    Incompatible,
    CredentialUnavailable,
    PermissionDenied,
    InvalidEvidence,
    InvalidInput,
    StorageUnavailable,
    Stopped,
}

type Result<T> = std::result::Result<T, NativeFailure>;

#[derive(Clone, Serialize)]
pub struct Connection {
    pub endpoint: String,
    pub server_id: String,
    pub device_id: String,
    pub token: String,
}

impl Drop for Connection {
    fn drop(&mut self) {
        self.token.zeroize();
    }
}

#[derive(Serialize)]
pub struct LocalWorkerProof {
    pub endpoint: String,
    pub server_id: String,
    pub machine_id: String,
    pub token: String,
}

impl Drop for LocalWorkerProof {
    fn drop(&mut self) {
        self.token.zeroize();
    }
}

#[derive(Clone, Copy, Deserialize, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
enum DeviceType {
    Client,
    Worker,
}

#[derive(Clone, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct DeviceMetadata {
    version: u32,
    #[serde(rename = "type")]
    kind: DeviceType,
    endpoint: String,
    server_id: String,
    device_id: String,
    pairing_id: String,
    #[serde(default)]
    machine_id: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Credential {
    #[serde(flatten)]
    metadata: DeviceMetadata,
    token: String,
}

#[derive(Deserialize)]
struct CliEnvelope {
    version: u32,
    result: Option<serde_json::Value>,
    error: Option<CliFailure>,
}
#[derive(Deserialize)]
struct CliFailure {
    code: String,
}

pub struct Connector {
    executable: PathBuf,
    root: PathBuf,
    gate: Mutex<()>,
    oauth_identity: Mutex<Option<DeviceMetadata>>,
    command_timeout: Duration,
    listen: String,
    exiting: AtomicBool,
    hosted: Mutex<Vec<desktop_host::DesktopChild>>,
}

impl Connector {
    // The verified local connection captures non-secret authority once. Empty
    // callback polling rechecks that fixed descriptor without starting a CLI or
    // competing with unrelated connector commands. Changed descriptors require
    // a new Go-verified connection before receiving callback authority.
    pub fn oauth_server_identity(&self) -> Result<String> {
        if self.exiting.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        let original = self
            .oauth_identity
            .lock()
            .map_err(|_| NativeFailure::Busy)?;
        let original = original
            .as_ref()
            .ok_or(NativeFailure::CredentialUnavailable)?;
        let path = self.root.join("desktop-client").join("device.json");
        let file = fs::symlink_metadata(&path).map_err(|_| NativeFailure::CredentialUnavailable)?;
        if !file.is_file() || file.file_type().is_symlink() || file.len() > 16 << 10 {
            return Err(NativeFailure::InvalidEvidence);
        }
        let bytes = Zeroizing::new(read_bounded(
            File::open(path).map_err(|_| NativeFailure::CredentialUnavailable)?,
            16 << 10,
        )?);
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Descriptor {
            #[serde(flatten)]
            metadata: DeviceMetadata,
            #[serde(rename = "token")]
            _token: serde::de::IgnoredAny,
        }
        let current: Descriptor =
            serde_json::from_slice(&bytes).map_err(|_| NativeFailure::InvalidEvidence)?;
        if current.metadata != *original
            || original.kind != DeviceType::Client
            || !original.machine_id.is_empty()
            || original.endpoint != "http://127.0.0.1:46310"
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        canonical_id(&original.server_id)?;
        Ok(original.server_id.clone())
    }

    pub fn open_provider_guidance(
        &self,
        preset: &str,
        action: provider_guidance::GuidanceAction,
    ) -> Result<()> {
        let url = provider_guidance::destination(preset, action)?;
        let result = browser_opener::dispatch(&url, &self.exiting);
        match &result {
            Ok(()) => tracing::info!(operation = "provider_guidance", phase = "dispatched"),
            Err(code) => tracing::warn!(operation = "provider_guidance", phase = "failed", ?code),
        }
        result
    }

    pub fn open_github(&self, url: &str) -> Result<()> {
        // Go applies the complete closed destination contract. Bound this
        // infrastructure argument before starting its credential-free sidecar.
        if url.len() > 2048
            || !url.starts_with("https://github.com/")
            || url.chars().any(char::is_control)
        {
            return Err(NativeFailure::InvalidInput);
        }
        let value = self.run_with_input(
            &[
                "presentation".into(),
                "open-github".into(),
                "--url-stdin".into(),
            ],
            Some(Zeroizing::new(url.as_bytes().to_vec())),
        )?;
        if value.as_object().is_none_or(|v| {
            v.len() != 1 || v.get("dispatched") != Some(&serde_json::Value::Bool(true))
        }) {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(())
    }

    pub fn new(executable: PathBuf, root: PathBuf) -> Result<Self> {
        if !executable.is_absolute() || !root.is_absolute() {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(Self {
            executable,
            root,
            gate: Mutex::new(()),
            oauth_identity: Mutex::new(None),
            command_timeout: COMMAND_TIMEOUT,
            listen: "127.0.0.1:46310".into(),
            exiting: AtomicBool::new(false),
            hosted: Mutex::new(Vec::new()),
        })
    }

    pub fn connect(&self) -> Result<Connection> {
        let _guard = self.gate.lock().map_err(|_| NativeFailure::Busy)?;
        tracing::info!(operation = "local_connect", phase = "start");
        // Advanced Start intentionally reopens ordinary stopped intent, but
        // shares Go's pinned desktop admission instead of ordinary CLI Start.
        let result = self.connect_inner("desktop-launch");
        match &result {
            Ok(_) => tracing::info!(operation = "local_connect", phase = "ready"),
            Err(code) => tracing::warn!(operation = "local_connect", phase = "failed", ?code),
        }
        result
    }

    // This read-only boundary never pairs, starts or replaces a Worker. The Go
    // inspector validates private files; product RPCs recheck current
    // revocation. Only the fixed CLI-owned local Worker scope can prove
    // this computer.
    pub fn local_worker_proof(&self) -> Result<LocalWorkerProof> {
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        let result = self.local_worker_proof_inner();
        match &result {
            Ok(_) => tracing::info!(operation = "local_worker_proof", phase = "ready"),
            Err(code) => tracing::warn!(operation = "local_worker_proof", phase = "failed", ?code),
        }
        result
    }

    fn local_worker_proof_inner(&self) -> Result<LocalWorkerProof> {
        let client: DeviceMetadata = serde_json::from_value(self.run(&[
            "device".into(),
            "inspect".into(),
            "--device-dir".into(),
            self.root.join("desktop-client").into_os_string(),
        ])?)
        .map_err(|_| NativeFailure::InvalidEvidence)?;
        let worker_root = self.root.join("worker");
        let metadata: DeviceMetadata = serde_json::from_value(self.run(&[
            "worker".into(),
            "inspect".into(),
            "--worker-dir".into(),
            worker_root.clone().into_os_string(),
        ])?)
        .map_err(|_| NativeFailure::InvalidEvidence)?;
        if client.kind != DeviceType::Client
            || !client.machine_id.is_empty()
            || metadata.kind != DeviceType::Worker
            || metadata.endpoint != client.endpoint
            || metadata.server_id != client.server_id
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        let path = worker_root.join("device.json");
        let file_metadata =
            fs::symlink_metadata(&path).map_err(|_| NativeFailure::CredentialUnavailable)?;
        if !file_metadata.is_file() || file_metadata.len() > 16 << 10 {
            return Err(NativeFailure::InvalidEvidence);
        }
        let bytes = Zeroizing::new(read_bounded(
            File::open(path).map_err(|_| NativeFailure::CredentialUnavailable)?,
            16 << 10,
        )?);
        let verified = verified_connection(&bytes, &metadata, DeviceType::Worker)?;
        Ok(LocalWorkerProof {
            endpoint: verified.endpoint.clone(),
            server_id: verified.server_id.clone(),
            machine_id: metadata.machine_id,
            token: verified.token.clone(),
        })
    }

    fn retry_launch(&self) -> Result<Connection> {
        let _guard = self.gate.lock().map_err(|_| NativeFailure::Busy)?;
        tracing::info!(operation = "desktop_launch", phase = "explicit-retry");
        self.connect_inner("desktop-retry")
    }

    fn launch(&self) -> Result<Connection> {
        let _guard = self.gate.lock().map_err(|_| NativeFailure::Busy)?;
        tracing::info!(operation = "desktop_launch", phase = "starting");
        let result = self.connect_inner("desktop-launch");
        match &result {
            Ok(_) => tracing::info!(operation = "desktop_launch", phase = "ready"),
            Err(code) => tracing::warn!(operation = "desktop_launch", phase = "failed", ?code),
        }
        result
    }

    // Cached launch credentials are never current-readiness authority. This
    // read cannot start/pair and rechecks durable Stop plus the original
    // identity.
    fn observe_launch(&self, original: &Connection) -> Result<Connection> {
        let _guard = self.gate.lock().map_err(|_| NativeFailure::Busy)?;
        let status = self.run(&self.server_arguments("desktop-status"))?;
        if self.server_state(&status)? != LocalServerState::Ready {
            return Err(NativeFailure::Stopped);
        }
        let metadata: DeviceMetadata = serde_json::from_value(self.run(&[
            "device".into(),
            "inspect".into(),
            "--join-existing".into(),
            "--device-dir".into(),
            self.root.join("desktop-client").into_os_string(),
        ])?)
        .map_err(|_| NativeFailure::InvalidEvidence)?;
        let current = self.read_local_connection(metadata)?;
        if current.endpoint != original.endpoint
            || current.server_id != original.server_id
            || current.device_id != original.device_id
            || current.token != original.token
        {
            return Err(NativeFailure::CredentialUnavailable);
        }
        Ok(current)
    }

    fn server_arguments(&self, action: &str) -> Vec<OsString> {
        vec![
            "server".into(),
            action.into(),
            "--listen".into(),
            self.listen.clone().into(),
            "--allowed-origins".into(),
            ORIGINS.into(),
        ]
    }

    fn connect_inner(&self, action: &str) -> Result<Connection> {
        let started = self.run_desktop_host(action)?;
        if self.server_state(&started)? != LocalServerState::Ready {
            return Err(NativeFailure::Stopped);
        }
        tracing::info!(operation = "local_connect", phase = "runtime-ready");
        let client_root = self.root.join("desktop-client");
        let metadata = self.run(&[
            "device".into(),
            "pair-local".into(),
            "--join-existing".into(),
            "--device-dir".into(),
            client_root.clone().into_os_string(),
        ])?;
        let metadata: DeviceMetadata =
            serde_json::from_value(metadata).map_err(|_| NativeFailure::InvalidEvidence)?;
        tracing::info!(
            operation = "local_connect",
            phase = "client-pairing-verified"
        );
        self.read_local_connection(metadata)
    }

    fn read_local_connection(&self, metadata: DeviceMetadata) -> Result<Connection> {
        let client_root = self.root.join("desktop-client");
        // Go enforces platform-specific privacy and strict credential
        // validation before this fixed file is read. No owner material
        // crosses the bridge.
        let inspected = self.run(&[
            "device".into(),
            "inspect".into(),
            "--join-existing".into(),
            "--device-dir".into(),
            client_root.clone().into_os_string(),
        ])?;
        let inspected: DeviceMetadata =
            serde_json::from_value(inspected).map_err(|_| NativeFailure::InvalidEvidence)?;
        if metadata != inspected {
            return Err(NativeFailure::InvalidEvidence);
        }
        let path = client_root.join("device.json");
        let file_metadata =
            fs::symlink_metadata(&path).map_err(|_| NativeFailure::CredentialUnavailable)?;
        if !file_metadata.is_file() || file_metadata.len() > 16 << 10 {
            return Err(NativeFailure::InvalidEvidence);
        }
        let bytes = Zeroizing::new(read_bounded(
            File::open(path).map_err(|_| NativeFailure::CredentialUnavailable)?,
            16 << 10,
        )?);
        let connection = connection_from_bytes(&bytes, &metadata)?;
        *self
            .oauth_identity
            .lock()
            .map_err(|_| NativeFailure::Busy)? = Some(metadata);
        Ok(connection)
    }

    fn sidecar_command(&self, arguments: &[OsString], input: bool) -> Result<Command> {
        let metadata =
            fs::symlink_metadata(&self.executable).map_err(|_| NativeFailure::SidecarMissing)?;
        if !metadata.is_file() || metadata.file_type().is_symlink() {
            return Err(NativeFailure::SidecarMissing);
        }
        let mut command = Command::new(&self.executable);
        command
            .arg("--data-dir")
            .arg(&self.root)
            .args(arguments)
            .stdin(if input { Stdio::piped() } else { Stdio::null() })
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .env_clear();
        for name in [
            "HOME",
            "USERPROFILE",
            "APPDATA",
            "LOCALAPPDATA",
            "SystemRoot",
            "WINDIR",
            "XDG_CONFIG_HOME",
            "XDG_DATA_HOME",
            "XDG_RUNTIME_DIR",
            "DBUS_SESSION_BUS_ADDRESS",
            "TMPDIR",
            "TEMP",
            "TMP",
        ] {
            if let Some(value) = std::env::var_os(name) {
                command.env(name, value);
            }
        }
        command.env(
            "PATH",
            sidecar_lookup_path(std::env::var_os("PATH").as_deref()),
        );
        // Only the closed OS opener needs desktop-session display context.
        // No renderer-controlled environment or provider credentials are used.
        if arguments.first().is_some_and(|v| v == "presentation") {
            for name in [
                "DISPLAY",
                "XAUTHORITY",
                "XDG_CURRENT_DESKTOP",
                "DESKTOP_SESSION",
            ] {
                if let Some(value) = std::env::var_os(name) {
                    command.env(name, value);
                }
            }
        }
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x0800_0000); // CREATE_NO_WINDOW applies only to the short CLI controller.
        }
        Ok(command)
    }

    fn run(&self, arguments: &[OsString]) -> Result<serde_json::Value> {
        self.run_with_input(arguments, None)
    }

    fn run_with_input(
        &self,
        arguments: &[OsString],
        input: Option<Zeroizing<Vec<u8>>>,
    ) -> Result<serde_json::Value> {
        self.run_with_input_bound(arguments, input, self.command_timeout)
    }

    fn run_with_input_bound(
        &self,
        arguments: &[OsString],
        input: Option<Zeroizing<Vec<u8>>>,
        timeout: Duration,
    ) -> Result<serde_json::Value> {
        if self.exiting.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        let mut command = self.sidecar_command(arguments, input.is_some())?;
        let mut child = command.spawn().map_err(|_| NativeFailure::SidecarFailed)?;
        let stdout = child.stdout.take().ok_or(NativeFailure::SidecarFailed)?;
        let stderr = child.stderr.take().ok_or(NativeFailure::SidecarFailed)?;
        let out = thread::spawn(move || read_bounded(stdout, OUTPUT_LIMIT));
        let err = thread::spawn(move || read_bounded(stderr, OUTPUT_LIMIT));
        let writer = input.map(|bytes| {
            let mut stdin = child.stdin.take().expect("piped input for closed command");
            thread::spawn(move || {
                stdin
                    .write_all(&bytes)
                    .map_err(|_| NativeFailure::SidecarFailed)
            })
        });
        let started = Instant::now();
        let result = loop {
            if self.exiting.load(Ordering::Acquire) {
                break Err(NativeFailure::Stopped);
            }
            match child.try_wait() {
                Ok(Some(status)) => break Ok(status),
                Ok(None) if started.elapsed() < timeout => thread::sleep(Duration::from_millis(25)),
                Ok(None) => break Err(NativeFailure::TimedOut),
                Err(_) => break Err(NativeFailure::SidecarFailed),
            }
        };
        if result.is_err() {
            let _ = child.kill();
            let _ = child.wait();
        }
        // These closed CLI operations cannot leave descendants holding these
        // pipes: detached servers redirect both streams to their private log.
        let output = out.join().map_err(|_| NativeFailure::SidecarFailed)?;
        let diagnostic = err.join().map_err(|_| NativeFailure::SidecarFailed)?;
        let written = writer
            .map(|writer| writer.join().map_err(|_| NativeFailure::SidecarFailed))
            .transpose()?;
        let status = result?;
        if let Some(written) = written {
            written?;
        }
        diagnostic?; // Drain and bound diagnostics, but never reflect their contents.
        let envelope: CliEnvelope =
            serde_json::from_slice(&output?).map_err(|_| NativeFailure::InvalidEvidence)?;
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
        if !status.success() {
            return Err(NativeFailure::SidecarFailed);
        }
        envelope.result.ok_or(NativeFailure::InvalidEvidence)
    }

    fn ensure(&self) -> Result<LocalServerState> {
        let _guard = self.gate.lock().map_err(|_| NativeFailure::Busy)?;
        let value = self.run_desktop_host("ensure")?;
        self.server_state(&value)
    }

    fn server_state(&self, value: &serde_json::Value) -> Result<LocalServerState> {
        if value.get("state").and_then(|value| value.as_str()) == Some("service-managed") {
            return Err(NativeFailure::ServiceManaged);
        }
        if value.get("state").and_then(|value| value.as_str()) == Some("stopped") {
            return Ok(LocalServerState::Stopped);
        }
        let status = if value.get("reused").and_then(|v| v.as_bool()) == Some(true) {
            value.get("status")
        } else if value.get("started").and_then(|v| v.as_bool()) == Some(true) {
            value.get("server").and_then(|v| v.get("status"))
        } else {
            None
        }
        .ok_or(NativeFailure::InvalidEvidence)?;
        if status.get("version").and_then(|v| v.as_str()) != Some("0.1.0")
            || status.get("protocol_version").and_then(|v| v.as_u64()) != Some(1)
            || (self.listen != "127.0.0.1:0"
                && status.get("listener").and_then(|v| v.as_str())
                    != Some(format!("http://{}", self.listen).as_str()))
        {
            return Err(NativeFailure::Incompatible);
        }
        Ok(LocalServerState::Ready)
    }
}

fn read_bounded(mut reader: impl Read, limit: u64) -> Result<Vec<u8>> {
    let mut bytes = Vec::new();
    reader
        .by_ref()
        .take(limit + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| NativeFailure::SidecarFailed)?;
    if bytes.len() as u64 > limit {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(bytes)
}

fn connection_from_bytes(bytes: &[u8], expected: &DeviceMetadata) -> Result<Connection> {
    verified_connection(bytes, expected, DeviceType::Client)
}

fn verified_connection(
    bytes: &[u8],
    expected: &DeviceMetadata,
    kind: DeviceType,
) -> Result<Connection> {
    validated_connection(bytes, expected, kind, false)
}
fn validated_connection(
    bytes: &[u8],
    expected: &DeviceMetadata,
    kind: DeviceType,
    saved: bool,
) -> Result<Connection> {
    let credential: Credential =
        serde_json::from_slice(bytes).map_err(|_| NativeFailure::InvalidEvidence)?;
    let token = Zeroizing::new(credential.token);
    if credential.metadata != *expected
        || expected.version != 1
        || expected.kind != kind
        || (kind == DeviceType::Client && !expected.machine_id.is_empty())
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    for id in [
        &expected.server_id,
        &expected.device_id,
        &expected.pairing_id,
    ] {
        let parsed = uuid::Uuid::parse_str(id).map_err(|_| NativeFailure::InvalidEvidence)?;
        if parsed.get_version_num() != 7 || parsed.to_string() != *id {
            return Err(NativeFailure::InvalidEvidence);
        }
    }
    if kind == DeviceType::Worker {
        let id = uuid::Uuid::parse_str(&expected.machine_id)
            .map_err(|_| NativeFailure::InvalidEvidence)?;
        if id.get_version_num() != 7 || id.to_string() != expected.machine_id {
            return Err(NativeFailure::InvalidEvidence);
        }
    }
    let decoded = Zeroizing::new(
        URL_SAFE_NO_PAD
            .decode(token.as_bytes())
            .map_err(|_| NativeFailure::InvalidEvidence)?,
    );
    if decoded.len() != 32 || URL_SAFE_NO_PAD.encode(&*decoded) != *token {
        return Err(NativeFailure::InvalidEvidence);
    }
    if saved {
        connection_origin(&expected.endpoint)?;
    } else if !expected.endpoint.starts_with("http://127.0.0.1:")
        || expected.endpoint[17..]
            .parse::<u16>()
            .ok()
            .filter(|port| *port > 0)
            .is_none()
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(Connection {
        endpoint: expected.endpoint.clone(),
        server_id: expected.server_id.clone(),
        device_id: expected.device_id.clone(),
        token: token.to_string(),
    })
}

pub fn default_data_root() -> Result<PathBuf> {
    dirs::config_dir()
        .map(|root| root.join("delidev"))
        .ok_or(NativeFailure::StorageUnavailable)
}

pub fn bundled_sidecar(executable: &Path) -> Result<PathBuf> {
    let parent = executable.parent().ok_or(NativeFailure::SidecarMissing)?;
    #[cfg(target_os = "linux")]
    if let Some(sidecar) = debian_sidecar(executable) {
        return Ok(sidecar);
    }
    Ok(parent.join(if cfg!(windows) {
        "delidev.exe"
    } else {
        "delidev"
    }))
}

// The pinned CEF Debian bundler relocates only the main binary to this fixed
// product directory. Its external binaries remain in /usr/bin. Resolve only
// that installed layout, never PATH or renderer input; AppImage/development
// executables keep the adjacent sidecar. Remove this case if the pinned bundler
// starts installing the sidecar beside the CEF executable on Debian.
#[cfg(any(target_os = "linux", test))]
fn debian_sidecar(executable: &Path) -> Option<PathBuf> {
    (executable == Path::new("/usr/share/DeliDev/delidev-desktop"))
        .then(|| PathBuf::from("/usr/bin/delidev"))
}

pub mod notifications;
pub mod presentation;
#[cfg(test)]
mod tests;

#[cfg(all(test, feature = "desktop-host"))]
mod permission_tests;

#[cfg(test)]
mod desktop_capability_tests {
    #[test]
    fn debian_sidecar_accepts_only_the_pinned_installed_layout() {
        use std::path::{Path, PathBuf};
        assert_eq!(
            super::debian_sidecar(Path::new("/usr/share/DeliDev/delidev-desktop")),
            Some(PathBuf::from("/usr/bin/delidev"))
        );
        for path in [
            "/tmp/DeliDev/delidev-desktop",
            "/usr/share/other/delidev-desktop",
            "/usr/share/DeliDev/other",
            "/mount/DeliDev.AppDir/bin/delidev-desktop",
            "usr/share/DeliDev/delidev-desktop",
        ] {
            assert_eq!(super::debian_sidecar(Path::new(path)), None);
        }
    }

    #[test]
    fn native_authority_belongs_only_to_trusted_webviews() {
        for (source, labels) in [
            (
                include_str!("../capabilities/main.json"),
                vec!["main", "local-*"],
            ),
            (include_str!("../capabilities/saved.json"), vec!["server-*"]),
        ] {
            let capability: serde_json::Value = serde_json::from_str(source).unwrap();
            assert!(capability.get("windows").is_none());
            assert!(capability.get("remote").is_none());
            assert_eq!(capability["webviews"], serde_json::json!(labels));
            assert!(!capability["permissions"].as_array().unwrap().is_empty());
        }
    }
}

// The picker returns native path bytes only when they fit the existing
// inspection wire bound. It never canonicalizes or opens the selected
// directory.
pub fn repository_folder_path(path: &std::path::Path) -> Result<String> {
    let value = path.to_str().ok_or(NativeFailure::InvalidEvidence)?;
    if !path.is_absolute() || value.is_empty() || value.len() > 4096 || value.contains('\0') {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(value.to_owned())
}

#[cfg(test)]
mod repository_folder_tests {
    use super::*;
    #[test]
    fn selection_retains_native_path_and_bounds_only() {
        let root = std::env::temp_dir();
        let path = root.join("repository with spaces").join("not-created");
        assert_eq!(
            repository_folder_path(&path).unwrap(),
            path.to_str().unwrap()
        );
        assert_eq!(
            repository_folder_path(std::path::Path::new("relative")),
            Err(NativeFailure::InvalidEvidence)
        );
        assert_eq!(
            repository_folder_path(&root.join("a".repeat(4097))),
            Err(NativeFailure::InvalidEvidence)
        );
        assert_eq!(
            repository_folder_path(&root.join("invalid\0path")),
            Err(NativeFailure::InvalidEvidence)
        );
    }
    #[cfg(unix)]
    #[test]
    fn non_utf8_selection_cannot_change_wire_path_bytes() {
        use std::os::unix::ffi::OsStringExt;
        let path = std::env::temp_dir().join(std::ffi::OsString::from_vec(vec![0xff]));
        assert_eq!(
            repository_folder_path(&path),
            Err(NativeFailure::InvalidEvidence)
        );
    }
}
