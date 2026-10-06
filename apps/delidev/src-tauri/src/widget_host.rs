//! Native presentation persistence only; no credentials, RPCs or actions.
use delidev_desktop::{NativeFailure, widget_writer::Publication};

#[cfg(target_os = "macos")]
unsafe extern "C" {
    fn delidev_widget_publish(bytes: *const u8, size: usize) -> i32;
    fn delidev_widget_set_language(bytes: *const u8, size: usize) -> i32;
}

pub fn set_language(
    language: delidev_desktop::language::LanguagePreference,
) -> Result<(), NativeFailure> {
    #[cfg(target_os = "macos")]
    {
        let bytes = serde_json::to_vec(&serde_json::json!({ "version": 1, "language": language }))
            .map_err(|_| NativeFailure::InvalidEvidence)?;
        // This presentation preference contains no server identity or content.
        if unsafe { delidev_widget_set_language(bytes.as_ptr(), bytes.len()) } != 0 {
            tracing::warn!(operation = "widget_language", code = "storage-unavailable");
            return Err(NativeFailure::StorageUnavailable);
        }
    }
    #[cfg(not(target_os = "macos"))]
    let _ = language;
    Ok(())
}

pub fn apply(publication: Publication) -> Result<(), NativeFailure> {
    #[cfg(target_os = "macos")]
    {
        let bytes = serde_json::to_vec(&publication).map_err(|_| NativeFailure::InvalidEvidence)?;
        if bytes.len() > 128 * 1024 {
            return Err(NativeFailure::InvalidEvidence);
        }
        // Only the joined widget worker calls this synchronous Swift ABI. It
        // holds no tray/window state or admission mutex during disk writes.
        // OS paths and diagnostics cannot cross this borrowed-buffer boundary.
        if unsafe { delidev_widget_publish(bytes.as_ptr(), bytes.len()) } != 0 {
            tracing::warn!(operation = "widget_snapshot", code = "storage-unavailable");
            return Err(NativeFailure::StorageUnavailable);
        }
        tracing::debug!(operation = "widget_snapshot", state = "published");
    }
    #[cfg(not(target_os = "macos"))]
    let _ = publication;
    Ok(())
}
