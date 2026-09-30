//! Native presentation persistence only; no credentials, RPCs or actions.
use delidev_desktop::{NativeFailure, presentation::TraySummary};

#[cfg(target_os = "macos")]
unsafe extern "C" {
    fn delidev_widget_publish(bytes: *const u8, size: usize) -> i32;
}

#[derive(serde::Serialize)]
#[serde(rename_all = "kebab-case")]
enum Action {
    Publish,
    Remove,
    Stop,
}
#[derive(serde::Serialize)]
struct Publication<'a> {
    action: Action,
    id: Option<&'a str>,
    name: Option<&'a str>,
    summary: Option<&'a TraySummary>,
}
fn apply(publication: Publication<'_>) -> Result<(), NativeFailure> {
    #[cfg(target_os = "macos")]
    {
        let bytes = serde_json::to_vec(&publication).map_err(|_| NativeFailure::InvalidEvidence)?;
        if bytes.len() > 128 * 1024 {
            return Err(NativeFailure::InvalidEvidence);
        }
        // The Swift ABI consumes this borrowed buffer synchronously and returns
        // only a closed outcome. OS paths and diagnostics cannot cross it.
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
pub fn publish(id: &str, name: &str, summary: &TraySummary) -> Result<(), NativeFailure> {
    summary.validate()?;
    apply(Publication {
        action: Action::Publish,
        id: Some(id),
        name: Some(name),
        summary: Some(summary),
    })
}
pub fn remove(id: &str) -> Result<(), NativeFailure> {
    apply(Publication {
        action: Action::Remove,
        id: Some(id),
        name: None,
        summary: None,
    })
}
pub fn stop() {
    let _ = apply(Publication {
        action: Action::Stop,
        id: None,
        name: None,
        summary: None,
    });
}
