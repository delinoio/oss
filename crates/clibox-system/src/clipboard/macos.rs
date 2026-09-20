use objc2::rc::autoreleasepool;
use objc2_app_kit::{NSPasteboard, NSPasteboardTypeString};
use objc2_foundation::NSData;

use super::*;

#[derive(Default)]
pub struct Native {}
impl Backend for Native {
    fn copy(&mut self, text: &str) -> Result<()> {
        autoreleasepool(|_| {
            let data = NSData::with_bytes(text.as_bytes());
            let board = NSPasteboard::generalPasteboard();
            runtime::check_cancelled()?;
            // Prepare all data before clearing. No cancellation boundary splits the
            // clear/set pair, which would otherwise leave an unintentionally empty board.
            board.clearContents();
            if board.setData_forType(Some(&data), unsafe { NSPasteboardTypeString }) {
                Ok(())
            } else {
                Err(Failure::new(
                    Code::SessionUnavailable,
                    "Could not set the macOS text clipboard; check desktop-session access.",
                ))
            }
        })
    }

    fn paste(&mut self) -> Result<Content> {
        autoreleasepool(|_| {
            let board = NSPasteboard::generalPasteboard();
            let revision = board.changeCount();
            let Some(types) = board.types() else {
                return Ok(Content::Empty);
            };
            if types.is_empty() {
                return Ok(Content::Empty);
            }
            if !types.containsObject(unsafe { NSPasteboardTypeString }) {
                return Ok(Content::NonText);
            }
            let data = board
                .dataForType(unsafe { NSPasteboardTypeString })
                .ok_or_else(|| {
                    Failure::new(
                        Code::SessionUnavailable,
                        "Could not read the macOS text clipboard; check desktop-session access.",
                    )
                })?;
            if data.len() > LIMIT {
                return Err(Failure::new(
                    Code::TextTooLarge,
                    "Clipboard text exceeds the 16 MiB UTF-8 limit; no output was written.",
                ));
            }
            let bytes = data.to_vec();
            if board.changeCount() != revision {
                return Err(Failure::new(
                    Code::ClipboardChanged,
                    "Clipboard changed during reading; run paste again if needed.",
                ));
            }
            Ok(Content::Text(bytes))
        })
    }
}
