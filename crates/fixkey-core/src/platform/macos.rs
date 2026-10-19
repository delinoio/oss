//! macOS AX text-range writes and a listen-only `CGEvent` tap.
//!
//! Field classification and write capability checks precede `AXValue` reads.
//! No clipboard or generated keyboard event is used.

use std::{
    ffi::{CStr, c_char, c_void},
    ptr,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
};

use super::{
    Application, Cursor, Gate, Handler, Identity, Key, KeyEvent, character_offset,
    empty_paragraph_text, utf16_offset, write_ack::wait_for_transition,
};
use crate::{
    SkipReason,
    session::{Accessibility, FieldSensitivity, FieldSnapshot, TargetId, WriteError},
};
const WRITE_ACK_TIMEOUT: std::time::Duration = std::time::Duration::from_millis(100);
type Ref = *const c_void;
#[repr(C)]
#[derive(Clone, Copy)]
struct Range {
    location: isize,
    length: isize,
}
#[repr(C)]
struct Point {
    x: f64,
    y: f64,
}
#[repr(C)]
struct Size {
    width: f64,
    height: f64,
}
#[repr(C)]
struct Rect {
    origin: Point,
    size: Size,
}
#[link(name = "ApplicationServices", kind = "framework")]
unsafe extern "C" {
    fn AXIsProcessTrusted() -> bool;
    fn AXUIElementCreateSystemWide() -> Ref;
    fn AXUIElementCopyAttributeValue(element: Ref, attribute: Ref, value: *mut Ref) -> i32;
    fn AXUIElementCopyParameterizedAttributeValue(
        element: Ref,
        attribute: Ref,
        parameter: Ref,
        value: *mut Ref,
    ) -> i32;
    fn AXUIElementSetAttributeValue(element: Ref, attribute: Ref, value: Ref) -> i32;
    fn AXUIElementIsAttributeSettable(element: Ref, attribute: Ref, value: *mut bool) -> i32;
    fn AXUIElementSetMessagingTimeout(element: Ref, timeout: f32) -> i32;
    fn AXUIElementGetPid(element: Ref, pid: *mut i32) -> i32;
    fn AXValueCreate(kind: i32, value: *const c_void) -> Ref;
    fn AXValueGetTypeID() -> usize;
    fn AXValueGetValue(value: Ref, kind: i32, out: *mut c_void) -> bool;
}
#[link(name = "CoreFoundation", kind = "framework")]
unsafe extern "C" {
    fn CFRelease(value: Ref);
    fn CFRetain(value: Ref) -> Ref;
    fn CFEqual(a: Ref, b: Ref) -> bool;
    fn CFGetTypeID(value: Ref) -> usize;
    fn CFStringGetTypeID() -> usize;
    fn CFStringCreateWithCString(allocator: Ref, text: *const c_char, encoding: u32) -> Ref;
    fn CFStringGetLength(value: Ref) -> isize;
    fn CFStringGetCString(value: Ref, buffer: *mut c_char, size: isize, encoding: u32) -> bool;
    fn CFBooleanGetTypeID() -> usize;
    fn CFBooleanGetValue(value: Ref) -> bool;
    fn CFURLCreateFromFileSystemRepresentation(
        allocator: Ref,
        path: *const u8,
        length: isize,
        directory: bool,
    ) -> Ref;
    fn CFBundleCreate(allocator: Ref, url: Ref) -> Ref;
    fn CFBundleGetIdentifier(bundle: Ref) -> Ref;
    fn CFMachPortCreateRunLoopSource(allocator: Ref, port: Ref, order: isize) -> Ref;
    fn CFRunLoopGetCurrent() -> Ref;
    fn CFRunLoopAddSource(loop_ref: Ref, source: Ref, mode: Ref);
    fn CFRunLoopRun();
    fn CFRunLoopRemoveSource(loop_ref: Ref, source: Ref, mode: Ref);
    fn CFRunLoopStop(loop_ref: Ref);
    static kCFRunLoopCommonModes: Ref;
}
#[link(name = "CoreGraphics", kind = "framework")]
unsafe extern "C" {
    fn CGPreflightListenEventAccess() -> bool;
    fn CGEventTapCreate(
        tap: u32,
        place: u32,
        options: u32,
        mask: u64,
        callback: extern "C" fn(Ref, u32, Ref, *mut c_void) -> Ref,
        info: *mut c_void,
    ) -> Ref;
    fn CGEventGetFlags(event: Ref) -> u64;
    fn CGEventGetIntegerValueField(event: Ref, field: u32) -> i64;
    fn CGEventTapEnable(tap: Ref, enable: bool);
}
#[link(name = "proc")]
unsafe extern "C" {
    fn proc_pidpath(pid: i32, buffer: *mut c_void, size: u32) -> i32;
}

struct Owned(Ref);
impl Drop for Owned {
    fn drop(&mut self) {
        if !self.0.is_null() {
            // SAFETY: Owned holds one retained reference and releases it
            // exactly once.
            unsafe { CFRelease(self.0) }
        }
    }
}
fn string(value: &str) -> Result<Owned, SkipReason> {
    let text = std::ffi::CString::new(value).map_err(|_| SkipReason::UnknownField)?;
    // SAFETY: The CString is NUL terminated and remains alive for this copying
    // call.
    let value = unsafe { CFStringCreateWithCString(ptr::null(), text.as_ptr(), 0x0800_0100) };
    if value.is_null() {
        Err(SkipReason::UnknownField)
    } else {
        Ok(Owned(value))
    }
}
fn text(value: Ref) -> Result<String, SkipReason> {
    // SAFETY: The pointer is a live retained CF object; null is checked before
    // access.
    if value.is_null() || unsafe { CFGetTypeID(value) != CFStringGetTypeID() } {
        return Err(SkipReason::UnknownField);
    }
    // SAFETY: The live object was checked to have CFString type.
    let length = unsafe { CFStringGetLength(value) };
    let capacity = usize::try_from(length)
        .ok()
        .and_then(|len| len.checked_mul(4))
        .and_then(|len| len.checked_add(1))
        .ok_or(SkipReason::UnknownField)?;
    let mut bytes = vec![0u8; capacity];
    // SAFETY: The UTF-8 buffer is writable for the supplied capacity and the
    // CFString is live.
    if !unsafe {
        CFStringGetCString(
            value,
            bytes.as_mut_ptr().cast(),
            isize::try_from(capacity).map_err(|_| SkipReason::UnknownField)?,
            0x0800_0100,
        )
    } {
        return Err(SkipReason::UnknownField);
    }
    CStr::from_bytes_until_nul(&bytes)
        .map_err(|_| SkipReason::UnknownField)?
        .to_str()
        .map(str::to_owned)
        .map_err(|_| SkipReason::UnknownField)
}
fn attribute(element: Ref, name: &str) -> Result<Owned, SkipReason> {
    let name = string(name)?;
    let mut value = ptr::null();
    // SAFETY: The retained element and attribute name outlive this call; value
    // is a writable output.
    if unsafe { AXUIElementCopyAttributeValue(element, name.0, &raw mut value) } != 0
        || value.is_null()
    {
        Err(SkipReason::UnknownField)
    } else {
        Ok(Owned(value))
    }
}
fn settable(element: Ref, name: &str) -> bool {
    let Ok(name) = string(name) else { return false };
    let mut result = false;
    // SAFETY: Both CF handles are retained and result points to a writable
    // boolean.
    unsafe { AXUIElementIsAttributeSettable(element, name.0, &raw mut result) == 0 && result }
}
fn focus() -> Result<Owned, SkipReason> {
    if !Adapter::permission() {
        return Err(SkipReason::PermissionMissing);
    }
    // SAFETY: This parameterless constructor returns a retained object, checked
    // for null.
    let system = Owned(unsafe { AXUIElementCreateSystemWide() });
    if system.0.is_null() {
        return Err(SkipReason::UnknownField);
    }
    // SAFETY: The non-null AX element is retained; the timeout is a finite
    // positive value.
    unsafe {
        AXUIElementSetMessagingTimeout(system.0, 0.05);
    }
    let element = attribute(system.0, "AXFocusedUIElement")?;
    // SAFETY: The non-null AX element is retained; the timeout is a finite
    // positive value.
    unsafe {
        AXUIElementSetMessagingTimeout(element.0, 0.05);
    }
    let role = text(attribute(element.0, "AXRole")?.0)?;
    if !matches!(role.as_str(), "AXTextField" | "AXTextArea") {
        return Err(SkipReason::UnknownField);
    }
    let subrole = attribute(element.0, "AXSubrole")
        .ok()
        .map(|value| text(value.0))
        .transpose()?;
    if subrole.as_deref() == Some("AXSecureTextField") || protected(element.0)? {
        return Err(SkipReason::ProtectedField);
    }
    let selected_text_writable = settable(element.0, "AXSelectedText");
    let selection_writable = settable(element.0, "AXSelectedTextRange");
    let value_writable = settable(element.0, "AXValue");
    let capabilities = (selected_text_writable, selection_writable, value_writable);
    LAST_WRITE_CAPABILITIES.with(|last| {
        if last.get() != Some(capabilities) {
            last.set(Some(capabilities));
            tracing::info!(
                reason = "native-write-capabilities",
                selected_text_writable,
                selection_writable,
                value_writable,
                "fixkey accessibility write capabilities changed"
            );
        }
    });
    if !selected_text_writable || !selection_writable {
        return Err(SkipReason::UnsupportedWrite);
    }
    Ok(element)
}
fn protected(element: Ref) -> Result<bool, SkipReason> {
    let Ok(value) = attribute(element, "AXProtectedContent") else {
        return Ok(false);
    };
    // SAFETY: The retained value is inspected before the boolean accessor.
    if unsafe { CFGetTypeID(value.0) != CFBooleanGetTypeID() } {
        return Err(SkipReason::UnknownField);
    }
    // SAFETY: The retained attribute has just been checked to be a CFBoolean.
    Ok(unsafe { CFBooleanGetValue(value.0) })
}
unsafe fn checked_value(value: Ref, kind: i32, out: *mut c_void) -> bool {
    // SAFETY: The caller supplies a retained CF object and a writable stack
    // value matching kind. The type check precedes AXValueGetValue.
    unsafe {
        !value.is_null()
            && CFGetTypeID(value) == AXValueGetTypeID()
            && AXValueGetValue(value, kind, out)
    }
}
fn range(element: Ref) -> Result<Range, SkipReason> {
    let value = attribute(element, "AXSelectedTextRange")?;
    let mut range = Range {
        location: 0,
        length: 0,
    };
    // SAFETY: The checked AXValue accessor writes a CFRange into its matching
    // stack buffer.
    if unsafe { checked_value(value.0, 4, (&raw mut range).cast()) }
        && range.location >= 0
        && range.length >= 0
    {
        Ok(range)
    } else {
        Err(SkipReason::UnknownField)
    }
}
fn application(element: Ref) -> Result<Application, SkipReason> {
    let mut pid = 0;
    // SAFETY: The AX element is retained and pid is a writable process-ID
    // output.
    if unsafe { AXUIElementGetPid(element, &raw mut pid) } != 0 {
        return Err(SkipReason::UnknownField);
    }
    let mut buffer = vec![0u8; 4096];
    // SAFETY: The buffer is writable for all 4096 bytes passed to the OS.
    if unsafe { proc_pidpath(pid, buffer.as_mut_ptr().cast(), 4096) } <= 0 {
        return Err(SkipReason::UnknownField);
    }
    let path = CStr::from_bytes_until_nul(&buffer)
        .map_err(|_| SkipReason::UnknownField)?
        .to_str()
        .map_err(|_| SkipReason::UnknownField)?;
    let root = std::path::Path::new(path)
        .ancestors()
        .find(|path| path.extension().is_some_and(|ext| ext == "app"))
        .ok_or(SkipReason::UnknownField)?;
    let bytes = root.as_os_str().as_encoded_bytes();
    // SAFETY: The path bytes remain live for this copying CFURL constructor.
    let url = Owned(unsafe {
        CFURLCreateFromFileSystemRepresentation(
            ptr::null(),
            bytes.as_ptr(),
            isize::try_from(bytes.len()).map_err(|_| SkipReason::UnknownField)?,
            true,
        )
    });
    if url.0.is_null() {
        return Err(SkipReason::UnknownField);
    }
    // SAFETY: The URL is a non-null retained CFURL; the returned bundle is null
    // checked.
    let bundle = Owned(unsafe { CFBundleCreate(ptr::null(), url.0) });
    if bundle.0.is_null() {
        return Err(SkipReason::UnknownField);
    }
    // SAFETY: The retained bundle keeps the borrowed identifier alive during
    // conversion.
    let identity = text(unsafe { CFBundleGetIdentifier(bundle.0) })?;
    Ok(Application {
        identity: Identity::Macos(identity),
        name: root
            .file_stem()
            .and_then(|name| name.to_str())
            .unwrap_or("Application")
            .to_owned(),
    })
}

struct RawAdapter {
    allowed: Option<Identity>,
    last: Option<(Owned, FieldSnapshot)>,
}
impl Default for RawAdapter {
    fn default() -> Self {
        Self::new()
    }
}
impl RawAdapter {
    #[must_use]
    pub const fn new() -> Self {
        Self {
            allowed: None,
            last: None,
        }
    }

    #[must_use]
    pub fn permission() -> bool {
        // SAFETY: These parameterless permission queries do not prompt or
        // access input text.
        unsafe { AXIsProcessTrusted() && CGPreflightListenEventAccess() }
    }

    /// Returns safe focused-control metadata.
    ///
    /// # Errors
    /// Refuses missing permissions, protected controls or unsupported writes.
    pub fn metadata() -> Result<(TargetId, Application), SkipReason> {
        let element = focus()?;
        Ok((target(&element), application(element.0)?))
    }

    /// Reads caret and selection metadata without reading the control value.
    /// # Errors
    /// Refuses unsafe controls, unapproved applications or invalid ranges.
    pub fn cursor(&self) -> Result<Cursor, SkipReason> {
        let element = focus()?;
        if self.allowed.as_ref() != Some(&application(element.0)?.identity) {
            return Err(SkipReason::ExcludedApp);
        }
        let selected = range(element.0)?;
        Ok(Cursor {
            target: target(&element),
            marker: [
                u64::try_from(selected.location).map_err(|_| SkipReason::UnknownField)?,
                u64::try_from(selected.length).map_err(|_| SkipReason::UnknownField)?,
                0,
            ],
        })
    }

    /// Returns the accessible insertion location in screen points.
    ///
    /// # Errors
    /// Refuses unknown or inaccessible text geometry.
    pub fn location() -> Result<(f64, f64), SkipReason> {
        let element = focus()?;
        let range = range(element.0)?;
        // SAFETY: The stack CFRange matches AXValue type 4 and remains live
        // during copying.
        let value = Owned(unsafe { AXValueCreate(4, (&raw const range).cast()) });
        if value.0.is_null() {
            return Err(SkipReason::UnknownField);
        }
        let name = string("AXBoundsForRange")?;
        let mut bounds = ptr::null();
        // SAFETY: The AX handles and CFRange are retained; bounds is a writable
        // CF output pointer.
        if unsafe {
            AXUIElementCopyParameterizedAttributeValue(element.0, name.0, value.0, &raw mut bounds)
        } != 0
            || bounds.is_null()
        {
            return Err(SkipReason::UnsupportedWrite);
        }
        let bounds = Owned(bounds);
        let mut rect = Rect {
            origin: Point { x: 0.0, y: 0.0 },
            size: Size {
                width: 0.0,
                height: 0.0,
            },
        };
        // SAFETY: The checked AXValue accessor writes a CGRect into its
        // matching stack buffer.
        if unsafe { checked_value(bounds.0, 3, (&raw mut rect).cast()) }
            && rect.origin.x.is_finite()
            && rect.origin.y.is_finite()
            && rect.size.height.is_finite()
            && rect.size.height > 0.0
        {
            Ok((rect.origin.x, rect.origin.y + rect.size.height))
        } else {
            Err(SkipReason::UnsupportedWrite)
        }
    }

    /// Runs a listen-only event tap on the calling thread.
    ///
    /// # Errors
    /// Refuses missing input monitoring permission or failed tap creation.
    pub fn observe(
        active: &Arc<AtomicBool>,
        ready: &Arc<AtomicBool>,
        gate: &Gate,
        handler: &Handler,
    ) -> Result<(), SkipReason> {
        if !Self::permission() {
            return Err(SkipReason::PermissionMissing);
        }
        let context = Box::new(Tap {
            active: active.clone(),
            ready: ready.clone(),
            gate: gate.clone(),
            handler: handler.clone(),
        });
        let pointer = Box::into_raw(context);
        // SAFETY: The callback context stays boxed until the tap is disabled
        // and removed.
        let tap = unsafe { CGEventTapCreate(1, 0, 1, 1 << 10, callback, pointer.cast()) };
        if tap.is_null() {
            // SAFETY: This pointer came from Box::into_raw; no run-loop
            // callback can access it on this failure path.
            unsafe {
                drop(Box::from_raw(pointer));
            }
            return Err(SkipReason::PermissionMissing);
        }
        // SAFETY: The retained tap port is valid and its source is null
        // checked.
        let source = Owned(unsafe { CFMachPortCreateRunLoopSource(ptr::null(), tap, 0) });
        let tap = Owned(tap);
        if source.0.is_null() {
            // SAFETY: This pointer came from Box::into_raw; no run-loop
            // callback can access it on this failure path.
            unsafe {
                drop(Box::from_raw(pointer));
            }
            return Err(SkipReason::UnknownField);
        }
        // SAFETY: pointer remains boxed while this thread owns the event tap.
        unsafe { &*pointer }.ready.store(true, Ordering::Release);
        // SAFETY: The source and port stay retained during this thread-owned
        // run loop. Disable and remove the source before freeing its callback
        // context.
        unsafe {
            CFRunLoopAddSource(CFRunLoopGetCurrent(), source.0, kCFRunLoopCommonModes);
            CGEventTapEnable(tap.0, true);
            CFRunLoopRun();
            (*pointer).ready.store(false, Ordering::Release);
            CGEventTapEnable(tap.0, false);
            CFRunLoopRemoveSource(CFRunLoopGetCurrent(), source.0, kCFRunLoopCommonModes);
            drop(Box::from_raw(pointer));
        }
        Ok(())
    }
}
fn select_range(element: &Owned, selection: Range) -> Result<(), ()> {
    // SAFETY: The stack CFRange matches AXValue type 4 and remains live
    // during copying.
    let value = Owned(unsafe { AXValueCreate(4, (&raw const selection).cast()) });
    if value.0.is_null() {
        return Err(());
    }
    let name = string("AXSelectedTextRange").map_err(|_| ())?;
    // SAFETY: All handles are retained and match the named AX attribute.
    let ax_error = unsafe { AXUIElementSetAttributeValue(element.0, name.0, value.0) };
    if ax_error != 0 {
        tracing::warn!(
            reason = "ax-selection-set-failed",
            ax_error,
            "fixkey selection write refused"
        );
        return Err(());
    }
    Ok(())
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum TextWrite {
    Value,
    Selected,
}
fn write_text(element: &Owned, expected: &str, replacement: &str) -> Result<TextWrite, WriteError> {
    // Some Chromium versions advertise AXSelectedText as writable but
    // acknowledge it without replacing text. Prefer the native value
    // setter when available, preserving the exact prefix and suffix,
    // as with UIA ValuePattern. Choose before the write: an uncertain
    // result must never trigger a second setter. Revisit this choice
    // when selected-text writes are reliable across supported hosts.
    let (method, attribute, payload) = if settable(element.0, "AXValue") {
        (TextWrite::Value, "AXValue", expected)
    } else {
        (TextWrite::Selected, "AXSelectedText", replacement)
    };
    let text_name = string(attribute).map_err(|_| WriteError::Failed)?;
    let text_value = string(payload).map_err(|_| WriteError::Failed)?;
    tracing::info!(
        write_attribute = attribute,
        "fixkey native text write requested"
    );
    // SAFETY: All handles are retained and their types match the named
    // AX attribute.
    if unsafe { AXUIElementSetAttributeValue(element.0, text_name.0, text_value.0) } != 0 {
        return Err(WriteError::Uncertain);
    }
    Ok(method)
}

fn text_ack(
    adapter: &mut RawAdapter,
    selected_snapshot: &FieldSnapshot,
    expected_states: &[FieldSnapshot],
    final_snapshot: &FieldSnapshot,
) -> Result<FieldSnapshot, WriteError> {
    wait_for_transition(
        selected_snapshot,
        expected_states,
        WRITE_ACK_TIMEOUT,
        || {
            let observed = adapter.snapshot();
            if let Ok(current) = &observed {
                if current != selected_snapshot && !expected_states.contains(current) {
                    tracing::warn!(
                        reason = "native-text-readback-mismatch",
                        text_matches = current.text == final_snapshot.text,
                        caret_at_start = current.caret == 0,
                        selection_empty = current.selection_len == 0,
                        target_matches = current.target == final_snapshot.target,
                        "fixkey native text readback differed"
                    );
                }
            } else if let Err(reason) = &observed {
                tracing::warn!(
                    reason = reason.as_str(),
                    "fixkey native text readback unavailable"
                );
            }
            observed
        },
    )
    .map_err(|()| WriteError::Uncertain)
}

#[derive(Clone, Copy)]
enum WriteStage {
    Target,
    Snapshot,
    Selection,
    SelectionAck,
    Text,
    TextAck,
    Caret,
    FinalSnapshot,
}
impl WriteStage {
    const fn as_str(self) -> &'static str {
        match self {
            Self::Target => "target",
            Self::Snapshot => "snapshot",
            Self::Selection => "selection",
            Self::SelectionAck => "selection-ack",
            Self::Text => "text",
            Self::TextAck => "text-ack",
            Self::Caret => "caret",
            Self::FinalSnapshot => "final-snapshot",
        }
    }
}

impl Accessibility for RawAdapter {
    fn snapshot(&mut self) -> Result<FieldSnapshot, SkipReason> {
        let element = focus()?;
        let application = application(element.0)?;
        if self.allowed.as_ref() != Some(&application.identity) {
            return Err(SkipReason::ExcludedApp);
        }
        if attribute(element.0, "AXMarkedTextRange").is_ok_and(|value| {
            let mut range = Range {
                location: 0,
                length: 0,
            };
            // SAFETY: The checked AXValue accessor writes a CFRange into its
            // matching stack buffer.
            unsafe { checked_value(value.0, 4, (&raw mut range).cast()) && range.length > 0 }
        }) {
            return Err(SkipReason::Composing);
        }
        let selected = range(element.0)?;
        let content = text(attribute(element.0, "AXValue")?.0)?;
        let caret = character_offset(
            &content,
            usize::try_from(selected.location).map_err(|_| SkipReason::UnknownField)?,
        )
        .ok_or(SkipReason::UnknownField)?;
        let end = character_offset(
            &content,
            usize::try_from(
                selected
                    .location
                    .checked_add(selected.length)
                    .ok_or(SkipReason::UnknownField)?,
            )
            .map_err(|_| SkipReason::UnknownField)?,
        )
        .ok_or(SkipReason::UnknownField)?;
        let snapshot = FieldSnapshot {
            target: target(&element),
            sensitivity: FieldSensitivity::Normal,
            supports_write: true,
            text: empty_paragraph_text(content, caret, end - caret),
            caret,
            selection_len: end - caret,
        };
        self.last = Some((element, snapshot.clone()));
        Ok(snapshot)
    }

    fn replace(
        &mut self,
        target: TargetId,
        start: usize,
        end: usize,
        replacement: &str,
    ) -> Result<(), WriteError> {
        let stage = std::cell::Cell::new(WriteStage::Target);
        let result = (|| {
            let Some((element, before)) = self.last.take() else {
                return Err(WriteError::Failed);
            };
            let current = focus().map_err(|_| WriteError::Failed)?;
            // SAFETY: Both arguments are retained, live AX elements.
            if !unsafe { CFEqual(current.0, element.0) }
                || target != before.target
                || start > end
                || end > before.text.chars().count()
            {
                return Err(WriteError::Failed);
            }
            stage.set(WriteStage::Snapshot);
            if self.snapshot().map_err(|_| WriteError::Failed)? != before {
                return Err(WriteError::Failed);
            }
            stage.set(WriteStage::Selection);
            let first = utf16_offset(&before.text, start).ok_or(WriteError::Failed)?;
            let last = utf16_offset(&before.text, end).ok_or(WriteError::Failed)?;
            let selection = Range {
                location: isize::try_from(first).map_err(|_| WriteError::Failed)?,
                length: isize::try_from(last - first).map_err(|_| WriteError::Failed)?,
            };
            select_range(&element, selection).map_err(|()| WriteError::Failed)?;
            stage.set(WriteStage::SelectionAck);
            let selected_snapshot = FieldSnapshot {
                caret: start,
                selection_len: end - start,
                ..before.clone()
            };
            // Browser-backed controls can acknowledge an AX write before the
            // renderer publishes the updated selection. Poll only exact safe
            // states; never repeat the write or accept an unrelated edit.
            wait_for_transition(
                &before,
                std::slice::from_ref(&selected_snapshot),
                WRITE_ACK_TIMEOUT,
                || self.snapshot(),
            )
            .map_err(|()| WriteError::Failed)?;
            stage.set(WriteStage::Text);
            let mut expected: Vec<char> = before.text.chars().collect();
            expected.splice(start..end, replacement.chars());
            let expected: String = expected.into_iter().collect();
            let method = write_text(&element, &expected, replacement)?;
            stage.set(WriteStage::TextAck);
            let caret = before.caret.saturating_sub(end - start) + replacement.chars().count();
            let offset = utf16_offset(&expected, caret).ok_or_else(|| WriteError::Partial {
                written: replacement.into(),
            })?;
            let inserted = FieldSnapshot {
                text: expected,
                caret: start + replacement.chars().count(),
                selection_len: 0,
                ..before
            };
            let selected_insert = FieldSnapshot {
                caret: start,
                selection_len: replacement.chars().count(),
                ..inserted.clone()
            };
            let final_snapshot = FieldSnapshot {
                caret,
                ..inserted.clone()
            };
            let mut expected_states = vec![inserted, selected_insert, final_snapshot.clone()];
            if method == TextWrite::Value {
                // Replacing a contenteditable value can collapse the DOM
                // selection to the start when its text nodes are rebuilt.
                // Accept only that exact text/target state, then restore the
                // proven insertion caret through the native range API.
                expected_states.push(FieldSnapshot {
                    caret: 0,
                    ..final_snapshot.clone()
                });
            }
            let written = text_ack(self, &selected_snapshot, &expected_states, &final_snapshot)?;
            stage.set(WriteStage::Caret);
            let collapsed = Range {
                location: isize::try_from(offset).map_err(|_| WriteError::Failed)?,
                length: 0,
            };
            select_range(&element, collapsed).map_err(|()| WriteError::Partial {
                written: replacement.into(),
            })?;
            stage.set(WriteStage::FinalSnapshot);
            wait_for_transition(&written, &[final_snapshot], WRITE_ACK_TIMEOUT, || {
                self.snapshot()
            })
            .map_err(|()| WriteError::Partial {
                written: replacement.into(),
            })?;
            Ok(())
        })();
        if result.is_err() {
            tracing::warn!(
                reason = "native-write-failed",
                stage = stage.get().as_str(),
                "fixkey accessibility write failed"
            );
        }
        result
    }
}
struct Tap {
    active: Arc<AtomicBool>,
    ready: Arc<AtomicBool>,
    gate: Gate,
    handler: Handler,
}
extern "C" fn callback(_: Ref, kind: u32, event: Ref, info: *mut c_void) -> Ref {
    let _ = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        // SAFETY: The tap owns this boxed context until callback delivery has
        // been stopped.
        let context = unsafe { &*info.cast::<Tap>() };
        if kind == 0xffff_fffe || kind == 0xffff_ffff {
            context.ready.store(false, Ordering::Release);
            context.active.store(false, Ordering::Release);
            // SAFETY: The callback runs on this run-loop thread; stopping it
            // ends delivery before its owner frees the context.
            unsafe {
                CFRunLoopStop(CFRunLoopGetCurrent());
            }
            return;
        }
        if kind != 10 || !context.active.load(Ordering::Acquire) {
            return;
        }
        let observed_at = std::time::Instant::now();
        let mut adapter = Adapter::new();
        let Ok((target, application)) = adapter.metadata() else {
            return;
        };
        if !(context.gate)(&application) {
            return;
        }
        // SAFETY: The event is borrowed from the active callback and remains
        // live until return.
        let flags = unsafe { CGEventGetFlags(event) };
        // SAFETY: The event is borrowed from the active callback and remains
        // live until return.
        let code = unsafe { CGEventGetIntegerValueField(event, 9) };
        // SAFETY: The event is borrowed from the active callback and remains
        // live until return.
        let external_source = unsafe { CGEventGetIntegerValueField(event, 41) } != 0;
        // SAFETY: The event is borrowed from the active callback and remains
        // live until return.
        let hardware_state = unsafe { CGEventGetIntegerValueField(event, 45) } == 1;
        LAST_INPUT_PROVENANCE.with(|last| {
            let provenance = (external_source, hardware_state);
            if last.get() != Some(provenance) {
                last.set(Some(provenance));
                tracing::info!(
                    reason = "native-input-provenance",
                    external_source,
                    hardware_state,
                    "fixkey input provenance changed"
                );
            }
        });
        let key = if external_source || !hardware_state {
            Key::Untrusted
        } else if flags & ((1 << 18) | (1 << 20)) != 0 {
            if code == 9 { Key::Paste } else { Key::Reset }
        } else if flags & (1 << 19) != 0 {
            Key::Reset
        } else {
            map_key(code, flags & (1 << 17) != 0, flags & (1 << 16) != 0)
        };
        (context.handler)(KeyEvent {
            key,
            target,
            observed_at,
        });
    }));
    event
}
fn map_key(code: i64, shift: bool, caps: bool) -> Key {
    if code == 51 {
        return Key::Backspace;
    }
    if code == 36 {
        return Key::Enter;
    }
    if code == 48 {
        return Key::Tab;
    }
    let letters = [
        (0, 'a'),
        (1, 's'),
        (2, 'd'),
        (3, 'f'),
        (4, 'h'),
        (5, 'g'),
        (6, 'z'),
        (7, 'x'),
        (8, 'c'),
        (9, 'v'),
        (11, 'b'),
        (12, 'q'),
        (13, 'w'),
        (14, 'e'),
        (15, 'r'),
        (16, 'y'),
        (17, 't'),
        (31, 'o'),
        (32, 'u'),
        (34, 'i'),
        (35, 'p'),
        (37, 'l'),
        (38, 'j'),
        (40, 'k'),
        (45, 'n'),
        (46, 'm'),
    ];
    if let Some((_, ch)) = letters.iter().find(|(key, _)| *key == code) {
        return Key::Character(if shift ^ caps {
            ch.to_ascii_uppercase()
        } else {
            *ch
        });
    }
    let symbols = [
        (18, '1', '!'),
        (19, '2', '@'),
        (20, '3', '#'),
        (21, '4', '$'),
        (23, '5', '%'),
        (22, '6', '^'),
        (26, '7', '&'),
        (28, '8', '*'),
        (25, '9', '('),
        (29, '0', ')'),
        (27, '-', '_'),
        (24, '=', '+'),
        (33, '[', '{'),
        (30, ']', '}'),
        (42, '\\', '|'),
        (41, ';', ':'),
        (39, '\'', '"'),
        (43, ',', '<'),
        (47, '.', '>'),
        (44, '/', '?'),
        (50, '`', '~'),
        (49, ' ', ' '),
    ];
    symbols
        .iter()
        .find(|(key, _, _)| *key == code)
        .map_or(Key::Reset, |(_, plain, upper)| {
            Key::Character(if shift { *upper } else { *plain })
        })
}

// AX operations and their retained target identity stay on the UI thread.
// Event observation runs on its own run loop and sends only physical keys.
#[link(name = "System")]
unsafe extern "C" {
    static _dispatch_main_q: c_void;
    fn dispatch_sync_f(queue: Ref, context: *mut c_void, work: extern "C" fn(*mut c_void));
    fn pthread_main_np() -> i32;
}
fn on_main<T: Send, F: FnOnce() -> T + Send>(operation: F) -> T {
    struct Call<F, T> {
        operation: Option<F>,
        result: Option<std::thread::Result<T>>,
    }
    extern "C" fn perform<T: Send, F: FnOnce() -> T + Send>(pointer: *mut c_void) {
        // SAFETY: dispatch_sync_f receives this exact live stack allocation;
        // the caller stays blocked, and the callback executes exactly once.
        let call = unsafe { &mut *pointer.cast::<Call<F, T>>() };
        call.result = Some(std::panic::catch_unwind(std::panic::AssertUnwindSafe(
            || call.operation.take().expect("one main-thread operation")(),
        )));
    }
    // SAFETY: pthread_main_np is a parameterless thread-identity query.
    if unsafe { pthread_main_np() } != 0 {
        return operation();
    }
    let mut call = Call {
        operation: Some(operation),
        result: None,
    };
    // SAFETY: The main queue is process-owned. Send bounds allow the operation
    // and result to cross threads; synchronous dispatch keeps the stack live.
    unsafe {
        dispatch_sync_f(
            (&raw const _dispatch_main_q).cast(),
            (&raw mut call).cast(),
            perform::<T, F>,
        );
    }
    match call.result.expect("main-thread operation completed") {
        Ok(value) => value,
        Err(error) => std::panic::resume_unwind(error),
    }
}
thread_local! {
    static LAST_INPUT_PROVENANCE: std::cell::Cell<Option<(bool, bool)>> = const { std::cell::Cell::new(None) };
    static LAST_WRITE_CAPABILITIES: std::cell::Cell<Option<(bool, bool, bool)>> = const { std::cell::Cell::new(None) };
    static CURRENT: std::cell::RefCell<RawAdapter> = const { std::cell::RefCell::new(RawAdapter::new()) };
    static TARGET: std::cell::RefCell<Option<(Owned,TargetId)>> = const { std::cell::RefCell::new(None) };
}
fn target(element: &Owned) -> TargetId {
    TARGET.with(|target| {
        let mut target = target.borrow_mut();
        // SAFETY: Both arguments are live retained AX objects on this thread.
        if let Some((previous, id)) = &*target
            // SAFETY: Both AX elements are retained on this UI thread.
            && unsafe { CFEqual(previous.0, element.0) }
        {
            return *id;
        }
        let id = super::next_target();
        // SAFETY: Retain the live AX object so identity cannot be reused after
        // release. Equality, replacement and release remain on this UI thread.
        *target = Some((Owned(unsafe { CFRetain(element.0) }), id));
        id
    })
}
/// UI-thread accessibility facade with a separate listen-only event loop.
pub struct Adapter;
impl Default for Adapter {
    fn default() -> Self {
        Self::new()
    }
}
impl Adapter {
    #[must_use]
    pub const fn new() -> Self {
        Self
    }

    #[must_use]
    pub fn permission() -> bool {
        on_main(RawAdapter::permission)
    }

    /// Clears captured field content when correction loses its context.
    pub fn clear(&mut self) {
        on_main(|| {
            CURRENT.with(|current| {
                let mut current = current.borrow_mut();
                current.last = None;
                current.allowed = None;
            });
        });
    }

    /// Restricts future text reads to the application approved by the caller.
    pub fn set_application(&mut self, identity: &Identity) {
        let identity = identity.clone();
        on_main(move || CURRENT.with(|current| current.borrow_mut().allowed = Some(identity)));
    }

    /// Gets a classified target without reading its text.
    /// # Errors
    /// Refuses unknown, protected or unsupported controls and permissions.
    pub fn metadata(&mut self) -> Result<(TargetId, Application), SkipReason> {
        on_main(RawAdapter::metadata)
    }

    /// Reads caret and selection metadata without reading the control value.
    /// # Errors
    /// Refuses unsafe controls, unapproved applications or invalid ranges.
    pub fn cursor(&mut self) -> Result<Cursor, SkipReason> {
        on_main(|| CURRENT.with(|current| current.borrow().cursor()))
    }

    /// Gets the accessible insertion geometry.
    /// # Errors
    /// Refuses unknown bounds or unsupported controls.
    pub fn location(&mut self) -> Result<(f64, f64), SkipReason> {
        on_main(RawAdapter::location)
    }

    /// Runs a listen-only key tap on the calling thread.
    /// # Errors
    /// Refuses unavailable monitoring permission or tap setup.
    pub fn observe(
        active: &Arc<AtomicBool>,
        ready: &Arc<AtomicBool>,
        gate: &Gate,
        handler: &Handler,
    ) -> Result<(), SkipReason> {
        RawAdapter::observe(active, ready, gate, handler)
    }
}
impl Accessibility for Adapter {
    fn snapshot(&mut self) -> Result<FieldSnapshot, SkipReason> {
        on_main(|| CURRENT.with(|current| current.borrow_mut().snapshot()))
    }

    fn replace(
        &mut self,
        target: TargetId,
        start: usize,
        end: usize,
        replacement: &str,
    ) -> Result<(), WriteError> {
        on_main(|| {
            CURRENT.with(|current| {
                current
                    .borrow_mut()
                    .replace(target, start, end, replacement)
            })
        })
    }
}
