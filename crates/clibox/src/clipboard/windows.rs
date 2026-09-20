use std::{mem::size_of, ptr};

use windows_sys::Win32::{
    Foundation::*,
    System::{DataExchange::*, Memory::*},
    UI::WindowsAndMessaging::*,
};

use super::*;

#[derive(Default)]
pub struct Native {}
struct Window(HWND);
impl Drop for Window {
    fn drop(&mut self) {
        unsafe {
            DestroyWindow(self.0);
        }
    }
}
struct Clipboard;
impl Drop for Clipboard {
    fn drop(&mut self) {
        unsafe {
            CloseClipboard();
        }
    }
}
struct Memory(HGLOBAL);
impl Drop for Memory {
    fn drop(&mut self) {
        if !self.0.is_null() {
            unsafe {
                GlobalFree(self.0);
            }
        }
    }
}
fn access() -> Result<(Window, Clipboard)> {
    // A real owner HWND is required for EmptyClipboard/SetClipboardData. The
    // built-in message-only window avoids any visible UI or delayed-rendering
    // owner.
    let class: Vec<u16> = "STATIC\0".encode_utf16().collect();
    let hwnd = unsafe {
        CreateWindowExW(
            0,
            class.as_ptr(),
            ptr::null(),
            0,
            0,
            0,
            0,
            0,
            HWND_MESSAGE,
            ptr::null_mut(),
            ptr::null_mut(),
            ptr::null(),
        )
    };
    if hwnd.is_null() {
        return Err(Failure::new(
            Code::SessionUnavailable,
            "Could not access the Windows desktop session.",
        ));
    }
    let window = Window(hwnd);
    if unsafe { OpenClipboard(hwnd) } == 0 {
        return Err(Failure::new(
            Code::PermissionDenied,
            "Windows clipboard is busy or inaccessible; close the competing operation and try \
             again.",
        ));
    }
    Ok((window, Clipboard))
}
impl Backend for Native {
    fn copy(&mut self, text: &str) -> Result<()> {
        let wide: Vec<u16> = text.encode_utf16().chain(Some(0)).collect();
        let mut memory = Memory(unsafe {
            GlobalAlloc(GMEM_MOVEABLE | GMEM_ZEROINIT, wide.len() * size_of::<u16>())
        });
        if memory.0.is_null() {
            return Err(Failure::new(
                Code::IoFailed,
                "Could not allocate clipboard memory.",
            ));
        }
        let pointer = unsafe { GlobalLock(memory.0) }.cast::<u16>();
        if pointer.is_null() {
            return Err(Failure::new(
                Code::IoFailed,
                "Could not access clipboard memory.",
            ));
        }
        unsafe {
            ptr::copy_nonoverlapping(wide.as_ptr(), pointer, wide.len());
            GlobalUnlock(memory.0);
        }
        let (_window, _clipboard) = access()?;
        runtime::check_cancelled()?;
        if unsafe { EmptyClipboard() } == 0 || unsafe { SetClipboardData(13, memory.0) }.is_null() {
            return Err(Failure::new(
                Code::PermissionDenied,
                "Windows could not replace clipboard text; check desktop-session access.",
            ));
        }
        memory.0 = ptr::null_mut(); // The OS owns the eager allocation after a successful set.
        Ok(())
    }

    fn paste(&mut self) -> Result<Content> {
        let (_window, _clipboard) = access()?;
        let revision = unsafe { GetClipboardSequenceNumber() };
        if unsafe { CountClipboardFormats() } == 0 {
            return Ok(Content::Empty);
        }
        if unsafe { IsClipboardFormatAvailable(13) } == 0 {
            return Ok(Content::NonText);
        }
        let memory = unsafe { GetClipboardData(13) };
        if memory.is_null() {
            return Err(Failure::new(
                Code::PermissionDenied,
                "Could not read Windows clipboard text.",
            ));
        }
        let size = unsafe { GlobalSize(memory) };
        // Any valid text within the UTF-8 limit uses no more than LIMIT UTF-16
        // code units. Bound the native allocation before making a Rust copy.
        if size > (LIMIT + 1) * 2 + 16 {
            return Err(Failure::new(
                Code::TextTooLarge,
                "Clipboard text exceeds the 16 MiB limit; no output was written.",
            ));
        }
        if size < 2 || size % 2 != 0 {
            return Err(Failure::new(
                Code::InvalidText,
                "Windows clipboard text has invalid encoding.",
            ));
        }
        let pointer = unsafe { GlobalLock(memory) }.cast::<u16>();
        if pointer.is_null() {
            return Err(Failure::new(
                Code::PermissionDenied,
                "Could not access Windows clipboard text.",
            ));
        }
        let text = {
            let wide = unsafe { std::slice::from_raw_parts(pointer, size / 2) };
            wide.iter()
                .position(|c| *c == 0)
                .and_then(|end| String::from_utf16(&wide[..end]).ok())
        };
        unsafe {
            GlobalUnlock(memory);
        }
        if unsafe { GetClipboardSequenceNumber() } != revision {
            return Err(Failure::new(
                Code::ClipboardChanged,
                "Clipboard changed during reading; run paste again if needed.",
            ));
        }
        Ok(Content::Text(
            text.ok_or_else(|| {
                Failure::new(
                    Code::InvalidText,
                    "Windows clipboard text has invalid or unterminated Unicode encoding.",
                )
            })?
            .into_bytes(),
        ))
    }
}
