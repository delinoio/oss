use serde::Serialize;

pub type Result<T> = std::result::Result<T, Failure>;

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum Code {
    InvalidInput,
    IoFailed,
    SpawnFailed,
    PermissionDenied,
    EnumerationFailed,
    IdentityChanged,
    IdentityUnverifiable,
    OwnershipChanged,
    TerminationTimeout,
    BackendUnavailable,
    SessionUnavailable,
    UnsupportedCapability,
    LaunchFailed,
    WaitUnavailable,
    ClipboardNonText,
    ClipboardChanged,
    InvalidText,
    TextTooLarge,
    Cancelled,
}

#[derive(Clone, Debug, Serialize)]
pub struct Failure {
    pub code: Code,
    pub message: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pid: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub port: Option<u16>,
}

impl Failure {
    pub fn new(code: Code, message: &'static str) -> Self {
        Self {
            code,
            message,
            pid: None,
            port: None,
        }
    }

    pub fn pid(mut self, pid: u32) -> Self {
        self.pid = Some(pid);
        self
    }

    pub fn port(mut self, port: u16) -> Self {
        self.port = Some(port);
        self
    }

    pub fn io(error: &std::io::Error) -> Self {
        let code = if error.kind() == std::io::ErrorKind::PermissionDenied {
            Code::PermissionDenied
        } else {
            Code::IoFailed
        };
        Self::new(
            code,
            "Operating system access failed; check the current user's permissions and session.",
        )
    }

    pub fn report(&self, operation: &'static str) {
        // Never format a raw OS error: it can contain paths, arguments or clipboard
        // data.
        if tracing::enabled!(tracing::Level::ERROR) {
            tracing::error!(operation, code = ?self.code, pid = self.pid, port = self.port, "error: {}", self.message);
        } else {
            // Filtering must not hide actionable failures. Ignore closed stderr
            // rather than panicking or overriding the operation's exit status.
            use std::io::Write;
            let _ = writeln!(
                std::io::stderr(),
                "error: {} operation={operation} code={:?} pid={:?} port={:?}",
                self.message,
                self.code,
                self.pid,
                self.port
            );
        }
    }
}
