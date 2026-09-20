//! Cleanup errors remain visible even on early-return and disposable-index
//! paths.
use std::sync::{
    Arc, Mutex, Weak,
    atomic::{AtomicBool, Ordering},
};
static CLEANUP_FAILED: AtomicBool = AtomicBool::new(false);
static STORAGE_ROOT: Mutex<Weak<Directory>> = Mutex::new(Weak::new());

// A weak registry shares one tracer-private namespace with retained report
// indexes, without keeping disposable storage alive after the last owner exits.
pub(crate) fn storage_root() -> std::io::Result<Arc<Directory>> {
    let mut current = STORAGE_ROOT
        .lock()
        .map_err(|_| std::io::Error::other("private storage lock failed"))?;
    if let Some(root) = current.upgrade() {
        return Ok(root);
    }
    let root = Arc::new(Directory::new("runlens-private-")?);
    *current = Arc::downgrade(&root);
    Ok(root)
}
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

    pub(crate) fn new_in(root: &std::path::Path, prefix: &str) -> std::io::Result<Self> {
        tempfile::Builder::new()
            .prefix(prefix)
            .tempdir_in(root)
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
