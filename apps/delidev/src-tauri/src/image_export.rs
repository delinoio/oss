// SPDX-License-Identifier: Apache-2.0
//! Explicit copies of authenticated original PNGs. Exported files are user
//! owned; session cleanup must never remove them or interpret their paths as
//! native output.
use std::{
    collections::BTreeMap,
    fs::{self, OpenOptions},
    io::Write,
    path::Path,
    sync::{
        Mutex,
        atomic::{AtomicBool, Ordering},
    },
};

use base64::{Engine, engine::general_purpose::STANDARD};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::NativeFailure;

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Request {
    pub operation_id: String,
    pub session_id: String,
    pub attachment_id: String,
    pub sha256: String,
    pub byte_length: u64,
    pub png: String,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum Outcome {
    Pending,
    Saved,
    Canceled,
    Failed,
    Uncertain,
}
#[derive(Clone)]
struct Receipt {
    scope: String,
    identity: String,
    outcome: Outcome,
}
#[derive(Default)]
pub struct Controller {
    receipts: Mutex<BTreeMap<String, Receipt>>,
    publication: Mutex<()>,
    stopped: AtomicBool,
}
fn digest(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
impl Request {
    pub fn bytes(&self) -> Result<Vec<u8>, NativeFailure> {
        for id in [&self.operation_id, &self.session_id, &self.attachment_id] {
            let parsed = uuid::Uuid::parse_str(id).map_err(|_| NativeFailure::InvalidInput)?;
            if parsed.to_string() != *id {
                return Err(NativeFailure::InvalidInput);
            }
        }
        if self.byte_length == 0 || self.byte_length > 10 << 20 || self.png.len() > 14 << 20 {
            return Err(NativeFailure::InvalidInput);
        }
        let bytes = STANDARD
            .decode(&self.png)
            .map_err(|_| NativeFailure::InvalidInput)?;
        if bytes.len() as u64 != self.byte_length
            || !bytes.starts_with(b"\x89PNG\r\n\x1a\n")
            || digest(&bytes) != self.sha256
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(bytes)
    }

    fn identity(&self) -> String {
        format!(
            "{}:{}:{}:{}",
            self.session_id, self.attachment_id, self.sha256, self.byte_length
        )
    }
}
impl Controller {
    /// Replays return the original receipt without opening another dialog.
    pub fn begin(&self, scope: &str, request: &Request) -> Result<Option<Outcome>, NativeFailure> {
        if self.stopped.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        let mut receipts = self.receipts.lock().map_err(|_| NativeFailure::Busy)?;
        if let Some(receipt) = receipts.get(&request.operation_id) {
            if receipt.scope != scope || receipt.identity != request.identity() {
                return Err(NativeFailure::PermissionDenied);
            }
            return Ok(Some(receipt.outcome));
        }
        // Never evict an uncertain receipt and accidentally make its replay a
        // write.
        if receipts.len() >= 128 || receipts.values().any(|v| v.outcome == Outcome::Pending) {
            return Err(NativeFailure::Busy);
        }
        receipts.insert(
            request.operation_id.clone(),
            Receipt {
                scope: scope.into(),
                identity: request.identity(),
                outcome: Outcome::Pending,
            },
        );
        Ok(None)
    }

    pub fn finish(
        &self,
        scope: &str,
        operation: &str,
        outcome: Outcome,
    ) -> Result<Outcome, NativeFailure> {
        let mut receipts = self.receipts.lock().map_err(|_| NativeFailure::Busy)?;
        let receipt = receipts
            .get_mut(operation)
            .ok_or(NativeFailure::InvalidEvidence)?;
        if receipt.scope != scope {
            return Err(NativeFailure::PermissionDenied);
        }
        if receipt.outcome != Outcome::Pending {
            return Ok(receipt.outcome);
        }
        receipt.outcome = outcome;
        Ok(outcome)
    }

    pub fn request_stop(&self) {
        self.stopped.store(true, Ordering::Release);
    }

    /// Join only disk publication off the UI loop. A late dialog is fenced from
    /// writing by stopped, and never holds the publication lock while choosing.
    pub fn join(&self) {
        drop(
            self.publication
                .lock()
                .unwrap_or_else(|error| error.into_inner()),
        );
    }

    pub fn publish(
        &self,
        scope: &str,
        operation: &str,
        path: &Path,
        bytes: &[u8],
    ) -> Result<Outcome, NativeFailure> {
        let _publication = self.publication.lock().map_err(|_| NativeFailure::Busy)?;
        if self.stopped.load(Ordering::Acquire) {
            return self.finish(scope, operation, Outcome::Canceled);
        }
        // Verify pending ownership before touching the user's chosen
        // filesystem.
        if self.read(scope, operation)? != Outcome::Pending {
            return self.read(scope, operation);
        }
        self.finish(scope, operation, save_new(path, bytes))
    }

    pub fn read(&self, scope: &str, operation: &str) -> Result<Outcome, NativeFailure> {
        let receipts = self.receipts.lock().map_err(|_| NativeFailure::Busy)?;
        let receipt = receipts
            .get(operation)
            .ok_or(NativeFailure::InvalidEvidence)?;
        if receipt.scope != scope {
            return Err(NativeFailure::PermissionDenied);
        }
        Ok(receipt.outcome)
    }
}
/// Same-directory publication is atomic and never replaces any existing file.
/// After publication, failures are uncertain; they cannot authorize another
/// write.
pub fn save_new(path: &Path, bytes: &[u8]) -> Outcome {
    let Some(parent) = path.parent().filter(|p| p.is_absolute()) else {
        return Outcome::Failed;
    };
    let temporary = parent.join(format!(".delidev-export-{}.tmp", uuid::Uuid::now_v7()));
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    // Creation failure grants no ownership of that path, even on a UUID
    // collision.
    let Ok(mut file) = options.open(&temporary) else {
        return Outcome::Failed;
    };
    let before_publish = || -> std::io::Result<()> {
        file.write_all(bytes)?;
        file.sync_all()?;
        // A hard link is an atomic create-new, unlike a replacing rename.
        fs::hard_link(&temporary, path)?;
        Ok(())
    };
    let mut before_publish = before_publish;
    if before_publish().is_err() {
        drop(file);
        return match fs::remove_file(&temporary) {
            Ok(()) => Outcome::Failed,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Outcome::Failed,
            Err(_) => Outcome::Uncertain,
        };
    }
    drop(file);
    let removed = fs::remove_file(&temporary);
    #[cfg(unix)]
    let synced = fs::File::open(parent).and_then(|f| f.sync_all());
    #[cfg(not(unix))]
    let synced: std::io::Result<()> = Ok(());
    if removed.is_err() || synced.is_err() {
        Outcome::Uncertain
    } else {
        Outcome::Saved
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    fn request() -> Request {
        let bytes = b"\x89PNG\r\n\x1a\nfixture";
        Request {
            operation_id: uuid::Uuid::now_v7().to_string(),
            session_id: uuid::Uuid::now_v7().to_string(),
            attachment_id: uuid::Uuid::now_v7().to_string(),
            sha256: digest(bytes),
            byte_length: bytes.len() as u64,
            png: STANDARD.encode(bytes),
        }
    }
    #[test]
    fn exact_original_bytes_and_no_overwrite() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("image.png");
        let bytes = request().bytes().unwrap();
        assert_eq!(save_new(&path, &bytes), Outcome::Saved);
        assert_eq!(fs::read(&path).unwrap(), bytes);
        assert_eq!(save_new(&path, b"replacement"), Outcome::Failed);
        assert_eq!(fs::read(&path).unwrap(), bytes);
        assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
    }
    #[test]
    fn replays_and_uncertainty_keep_original_scope() {
        let c = Controller::default();
        let mut r = request();
        assert_eq!(c.begin("original", &r).unwrap(), None);
        assert_eq!(c.begin("original", &r).unwrap(), Some(Outcome::Pending));
        assert!(c.begin("other", &r).is_err());
        c.finish("original", &r.operation_id, Outcome::Uncertain)
            .unwrap();
        assert_eq!(c.begin("original", &r).unwrap(), Some(Outcome::Uncertain));
        r.attachment_id = uuid::Uuid::now_v7().to_string();
        assert!(c.begin("original", &r).is_err());
    }
    #[test]
    fn refuses_changed_bytes_and_oversized_requests() {
        let mut r = request();
        r.sha256 = "0".repeat(64);
        assert!(r.bytes().is_err());
        r.byte_length = (10 << 20) + 1;
        assert!(r.bytes().is_err());
    }
    #[test]
    fn quit_fences_late_dialog_publication() {
        let c = Controller::default();
        let r = request();
        c.begin("original", &r).unwrap();
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("late.png");
        c.request_stop();
        c.join();
        assert_eq!(
            c.publish("original", &r.operation_id, &path, &r.bytes().unwrap())
                .unwrap(),
            Outcome::Canceled
        );
        assert!(!path.exists());
        assert!(c.begin("original", &request()).is_err());
    }
    #[test]
    fn cancellation_is_a_terminal_receipt() {
        let c = Controller::default();
        let r = request();
        c.begin("original", &r).unwrap();
        c.finish("original", &r.operation_id, Outcome::Canceled)
            .unwrap();
        assert_eq!(
            c.finish("original", &r.operation_id, Outcome::Saved)
                .unwrap(),
            Outcome::Canceled
        );
    }
}
