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

const OUTPUT_LIMIT: u64 = 64 << 10;
const COMMAND_TIMEOUT: Duration = Duration::from_secs(40);
const ORIGINS: &str = "tauri://localhost,http://tauri.localhost,http://127.0.0.1:46311";

mod connections;
pub use connections::{SavedConnection, SavedConnectionState, canonical_id, connection_origin};

mod local_worker;
pub use local_worker::{LocalWorkerAction, LocalWorkerState, LocalWorkerStatus};

mod supervision;
pub use supervision::{LocalServerState, LocalServerStatus, Supervision};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum NativeFailure {
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

#[derive(Serialize)]
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

#[derive(Deserialize, PartialEq, Eq)]
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
    listen: String,
    exiting: AtomicBool,
}

impl Connector {
    pub fn new(executable: PathBuf, root: PathBuf) -> Result<Self> {
        if !executable.is_absolute() || !root.is_absolute() {
            return Err(NativeFailure::InvalidEvidence);
        }
        let metadata =
            fs::symlink_metadata(&executable).map_err(|_| NativeFailure::SidecarMissing)?;
        if !metadata.is_file() || metadata.file_type().is_symlink() {
            return Err(NativeFailure::SidecarMissing);
        }
        Ok(Self {
            executable,
            root,
            gate: Mutex::new(()),
            listen: "127.0.0.1:46310".into(),
            exiting: AtomicBool::new(false),
        })
    }

    pub fn connect(&self) -> Result<Connection> {
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        tracing::info!(operation = "local_connect", phase = "start");
        let result = self.connect_inner();
        match &result {
            Ok(_) => tracing::info!(operation = "local_connect", phase = "ready"),
            Err(code) => tracing::warn!(operation = "local_connect", phase = "failed", ?code),
        }
        result
    }

    // This read-only boundary never pairs, starts or replaces a Worker. The Go
    // inspector validates private files; product RPCs recheck current revocation.
    // Only the fixed CLI-owned local Worker scope can prove this computer.
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

    fn connect_inner(&self) -> Result<Connection> {
        // The Go executable owns compatibility, startup locking and detachment.
        // Dropping this client never invokes stop or assumes server ownership.
        let started = self.run(&[
            "server".into(),
            "start".into(),
            "--listen".into(),
            self.listen.clone().into(),
            "--allowed-origins".into(),
            ORIGINS.into(),
        ])?;
        if started.get("state").and_then(|value| value.as_str()) == Some("stopped") {
            return Err(NativeFailure::Stopped);
        }
        let client_root = self.root.join("desktop-client");
        let metadata = self.run(&[
            "device".into(),
            "pair-local".into(),
            "--device-dir".into(),
            client_root.clone().into_os_string(),
        ])?;
        let metadata: DeviceMetadata =
            serde_json::from_value(metadata).map_err(|_| NativeFailure::InvalidEvidence)?;
        // Go enforces platform-specific privacy and strict credential validation
        // before this fixed file is read. No owner material crosses the bridge.
        let inspected = self.run(&[
            "device".into(),
            "inspect".into(),
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
        connection_from_bytes(&bytes, &metadata)
    }

    fn run(&self, arguments: &[OsString]) -> Result<serde_json::Value> {
        self.run_with_input(arguments, None)
    }

    fn run_with_input(
        &self,
        arguments: &[OsString],
        input: Option<Zeroizing<Vec<u8>>>,
    ) -> Result<serde_json::Value> {
        if self.exiting.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        let mut command = Command::new(&self.executable);
        command
            .arg("--data-dir")
            .arg(&self.root)
            .args(arguments)
            .stdin(if input.is_some() {
                Stdio::piped()
            } else {
                Stdio::null()
            })
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
        #[cfg(unix)]
        command.env("PATH", "/usr/bin:/bin:/usr/sbin:/sbin");
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x0800_0000); // CREATE_NO_WINDOW applies only to the short CLI controller.
        }
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
                Ok(None) if started.elapsed() < COMMAND_TIMEOUT => {
                    thread::sleep(Duration::from_millis(25))
                }
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
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        let value = self.run(&[
            "server".into(),
            "ensure".into(),
            "--listen".into(),
            self.listen.clone().into(),
            "--allowed-origins".into(),
            ORIGINS.into(),
        ])?;
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
    Ok(parent.join(if cfg!(windows) {
        "delidev.exe"
    } else {
        "delidev"
    }))
}

#[cfg(test)]
mod tests;
