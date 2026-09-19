//! Cleanup errors remain visible even on early-return and disposable-index
//! paths.
use std::sync::atomic::{AtomicBool, Ordering};
static CLEANUP_FAILED: AtomicBool = AtomicBool::new(false);
pub fn record_cleanup_failure() {
    CLEANUP_FAILED.store(true, Ordering::Relaxed);
    tracing::error!(
        stage = "cleanup",
        code = "cleanup-failed",
        "private temporary metadata cleanup failed"
    );
}
pub fn cleanup_failed() -> bool {
    CLEANUP_FAILED.load(Ordering::Relaxed)
}
pub struct Directory(Option<tempfile::TempDir>);
impl Directory {
    pub fn new(prefix: &str) -> std::io::Result<Self> {
        tempfile::Builder::new()
            .prefix(prefix)
            .tempdir()
            .map(|value| Self(Some(value)))
    }

    pub fn path(&self) -> &std::path::Path {
        self.0
            .as_ref()
            .expect("directory exists until consumed")
            .path()
    }

    pub fn close(mut self) -> std::io::Result<()> {
        self.0
            .take()
            .expect("directory exists until consumed")
            .close()
    }
}
impl Drop for Directory {
    fn drop(&mut self) {
        if let Some(directory) = self.0.take()
            && directory.close().is_err()
        {
            record_cleanup_failure();
        }
    }
}
