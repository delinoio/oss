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
/// This result is minted only after the original controller proves that no
/// receipt exists. Receipt observation can never produce retry authority.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum RejectionReason {
    Busy,
    Stopped,
    InvalidInput,
}
#[derive(Debug, PartialEq, Eq)]
pub enum Admission {
    New(Vec<u8>),
    Retained(Outcome),
    Rejected(RejectionReason),
}
#[derive(Serialize)]
#[serde(tag = "status", rename_all = "kebab-case")]
pub enum NonAdmission {
    NotAdmitted {
        #[serde(rename = "operationId")]
        operation_id: String,
        reason: RejectionReason,
    },
}
#[derive(Serialize)]
#[serde(untagged)]
pub enum SaveResult {
    Receipt(Outcome),
    Rejection(NonAdmission),
}
impl SaveResult {
    pub fn not_admitted(operation_id: String, reason: RejectionReason) -> Self {
        Self::Rejection(NonAdmission::NotAdmitted {
            operation_id,
            reason,
        })
    }
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
    pub fn begin(&self, scope: &str, request: &Request) -> Result<Admission, NativeFailure> {
        // A poisoned lock cannot prove non-admission. Inspect retained receipts
        // before stopped/input checks so an admitted original never gains
        // retry.
        let mut receipts = self.receipts.lock().map_err(|_| NativeFailure::Busy)?;
        if let Some(receipt) = receipts.get(&request.operation_id) {
            if receipt.scope != scope || receipt.identity != request.identity() {
                return Err(NativeFailure::PermissionDenied);
            }
            request.bytes()?;
            return Ok(Admission::Retained(receipt.outcome));
        }
        if self.stopped.load(Ordering::Acquire) {
            return Ok(Admission::Rejected(RejectionReason::Stopped));
        }
        let bytes = match request.bytes() {
            Ok(bytes) => bytes,
            Err(_) => return Ok(Admission::Rejected(RejectionReason::InvalidInput)),
        };
        // Never evict an uncertain receipt and accidentally make its replay a
        // write. No receipt is created for these positively proved refusals.
        if receipts.len() >= 128 || receipts.values().any(|v| v.outcome == Outcome::Pending) {
            return Ok(Admission::Rejected(RejectionReason::Busy));
        }
        receipts.insert(
            request.operation_id.clone(),
            Receipt {
                scope: scope.into(),
                identity: request.identity(),
                outcome: Outcome::Pending,
            },
        );
        Ok(Admission::New(bytes))
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
        assert_eq!(
            c.begin("original", &r).unwrap(),
            Admission::New(r.bytes().unwrap())
        );
        assert_eq!(
            c.begin("original", &r).unwrap(),
            Admission::Retained(Outcome::Pending)
        );
        assert!(c.begin("other", &r).is_err());
        c.finish("original", &r.operation_id, Outcome::Uncertain)
            .unwrap();
        assert_eq!(
            c.begin("original", &r).unwrap(),
            Admission::Retained(Outcome::Uncertain)
        );
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
        assert_eq!(
            c.begin("original", &request()).unwrap(),
            Admission::Rejected(RejectionReason::Stopped)
        );
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
    #[test]
    fn pre_admission_busy_releases_only_the_absent_original() {
        let c = Controller::default();
        let a = request();
        let b = request();
        assert!(matches!(
            c.begin("window-a", &a).unwrap(),
            Admission::New(_)
        ));
        assert_eq!(
            c.begin("window-b", &b).unwrap(),
            Admission::Rejected(RejectionReason::Busy)
        );
        assert!(c.read("window-b", &b.operation_id).is_err());
        assert_eq!(
            c.begin("window-a", &a).unwrap(),
            Admission::Retained(Outcome::Pending)
        );
        c.finish("window-a", &a.operation_id, Outcome::Canceled)
            .unwrap();
        assert!(matches!(
            c.begin("window-b", &b).unwrap(),
            Admission::New(_)
        ));
        c.finish("window-b", &b.operation_id, Outcome::Uncertain)
            .unwrap();
        c.request_stop();
        assert_eq!(
            c.begin("window-b", &b).unwrap(),
            Admission::Retained(Outcome::Uncertain)
        );
        assert!(c.begin("window-a", &b).is_err());
    }
    #[test]
    fn invalid_fresh_input_proves_absence_but_changed_receipt_does_not() {
        let c = Controller::default();
        let mut r = request();
        r.png = "invalid".into();
        assert_eq!(
            c.begin("original", &r).unwrap(),
            Admission::Rejected(RejectionReason::InvalidInput)
        );
        assert!(c.read("original", &r.operation_id).is_err());
        let mut retained = request();
        c.begin("original", &retained).unwrap();
        retained.png = "invalid".into();
        assert!(c.begin("original", &retained).is_err());
        assert_eq!(
            c.read("original", &retained.operation_id).unwrap(),
            Outcome::Pending
        );
    }
    #[test]
    fn poisoned_lock_cannot_prove_non_admission() {
        let c = Controller::default();
        let _ = std::panic::catch_unwind(|| {
            let _guard = c.receipts.lock().unwrap();
            panic!("fixture poison");
        });
        assert!(c.begin("original", &request()).is_err());
    }
    #[test]
    fn non_admission_wire_is_closed_and_bound_to_original_operation() {
        let value = serde_json::to_value(SaveResult::not_admitted(
            "original-operation".into(),
            RejectionReason::Busy,
        ))
        .unwrap();
        assert_eq!(
            value,
            serde_json::json!({"status":"not-admitted","operationId":"original-operation","reason":"busy"})
        );
        assert_eq!(
            serde_json::to_value(SaveResult::Receipt(Outcome::Saved)).unwrap(),
            "saved"
        );
    }
}
