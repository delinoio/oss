// SPDX-License-Identifier: Apache-2.0
//! Closed native fallback with Cancel as the default and Escape decision.
use tauri::AppHandle;
use tauri_runtime_cef::CefRuntime;
#[cfg(target_os = "macos")]
pub fn confirm(
    app: &AppHandle<CefRuntime>,
    title: String,
    body: String,
    cancel: String,
    quit: String,
) -> bool {
    use objc2::{
        msg_send,
        rc::Retained,
        runtime::{AnyClass, AnyObject},
    };
    use objc2_foundation::NSString;
    let (send, receive) = std::sync::mpsc::sync_channel(1);
    if app
        .run_on_main_thread(move || {
            // AppKit's modal loop keeps native events responsive. Only this
            // worker's decision waits; no shutdown, supervision or
            // sidecar fence has started.
            let result = unsafe {
                let Some(class) = AnyClass::get(c"NSAlert") else {
                    let _ = send.send(false);
                    return;
                };
                let pointer: *mut AnyObject = msg_send![class, new];
                let Some(alert) = Retained::from_raw(pointer) else {
                    let _ = send.send(false);
                    return;
                };
                let title = NSString::from_str(&title);
                let body = NSString::from_str(&body);
                let cancel = NSString::from_str(&cancel);
                let quit = NSString::from_str(&quit);
                let _: () = msg_send![&*alert,setMessageText:&*title];
                let _: () = msg_send![&*alert,setInformativeText:&*body];
                let button: *mut AnyObject = msg_send![&*alert,addButtonWithTitle:&*cancel];
                let _other: *mut AnyObject = msg_send![&*alert,addButtonWithTitle:&*quit];
                let escape = NSString::from_str("\u{1b}");
                let _: () = msg_send![button,setKeyEquivalent:&*escape];
                let window: *mut AnyObject = msg_send![&*alert, window];
                let cell: *mut AnyObject = msg_send![button, cell];
                let _: () = msg_send![window,setDefaultButtonCell:cell];
                let _: bool = msg_send![window,makeFirstResponder:button];
                let response: isize = msg_send![&*alert, runModal];
                response == 1001
            };
            let _ = send.send(result);
        })
        .is_err()
    {
        return false;
    }
    receive.recv().unwrap_or(false)
}
#[cfg(target_os = "windows")]
pub fn confirm(
    _app: &AppHandle<CefRuntime>,
    title: String,
    body: String,
    cancel: String,
    quit: String,
) -> bool {
    use windows_sys::Win32::UI::Controls::{
        TASKDIALOG_BUTTON, TASKDIALOGCONFIG, TDF_ALLOW_DIALOG_CANCELLATION, TDF_SIZE_TO_CONTENT,
        TaskDialogIndirect,
    };
    let wide = |value: &str| value.encode_utf16().chain([0]).collect::<Vec<u16>>();
    let title = wide(&title);
    let body = wide(&body);
    let cancel = wide(&cancel);
    let quit = wide(&quit);
    let buttons = [
        TASKDIALOG_BUTTON {
            nButtonID: 100,
            pszButtonText: cancel.as_ptr(),
        },
        TASKDIALOG_BUTTON {
            nButtonID: 101,
            pszButtonText: quit.as_ptr(),
        },
    ];
    let config = TASKDIALOGCONFIG {
        cbSize: std::mem::size_of::<TASKDIALOGCONFIG>() as u32,
        dwFlags: TDF_ALLOW_DIALOG_CANCELLATION | TDF_SIZE_TO_CONTENT,
        pszWindowTitle: title.as_ptr(),
        pszContent: body.as_ptr(),
        cButtons: 2,
        pButtons: buttons.as_ptr(),
        nDefaultButton: 100,
        ..Default::default()
    };
    let mut selected = 0;
    let outcome = unsafe {
        TaskDialogIndirect(
            &config,
            &mut selected,
            std::ptr::null_mut(),
            std::ptr::null_mut(),
        )
    };
    if outcome < 0 {
        tracing::warn!(
            operation = "desktop_quit_confirmation",
            phase = "native-presentation-unavailable"
        );
    }
    outcome >= 0 && selected == 101
}
#[cfg(target_os = "linux")]
pub fn confirm(
    _app: &AppHandle<CefRuntime>,
    title: String,
    body: String,
    cancel: String,
    quit: String,
) -> bool {
    use std::process::{Command, Stdio};
    // The packaging contract already requires zenity for native confirmations.
    // No renderer content, executable, path or arguments are accepted here.
    let mut command = Command::new("zenity");
    command.env_clear().env("PATH", "/usr/bin:/bin");
    for name in [
        "DISPLAY",
        "WAYLAND_DISPLAY",
        "XAUTHORITY",
        "XDG_RUNTIME_DIR",
        "DBUS_SESSION_BUS_ADDRESS",
        "LANG",
        "LC_ALL",
    ] {
        if let Some(value) = std::env::var_os(name) {
            command.env(name, value);
        }
    }
    command
        .args([
            "--question",
            "--default-cancel",
            "--title",
            &title,
            "--text",
            &body,
            "--cancel-label",
            &cancel,
            "--ok-label",
            &quit,
        ])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    // Bind the original helper to this desktop even on crash or forced exit.
    // pre_exec uses only async-signal-safe syscalls; check the parent again to
    // close the race where it exits before the death signal is installed.
    use std::os::unix::process::CommandExt;
    let parent = unsafe { libc::getpid() };
    unsafe {
        command.pre_exec(move || {
            if libc::prctl(libc::PR_SET_PDEATHSIG, libc::SIGKILL) != 0 {
                return Err(std::io::Error::last_os_error());
            }
            if libc::getppid() != parent {
                return Err(std::io::Error::other("original desktop exited"));
            }
            Ok(())
        });
    }
    let status = command.spawn().and_then(|mut child| child.wait());
    if status.is_err() {
        tracing::warn!(
            operation = "desktop_quit_confirmation",
            phase = "native-presentation-unavailable"
        );
    }
    status.is_ok_and(|value| value.success())
}
#[cfg(not(any(target_os = "macos", target_os = "windows", target_os = "linux")))]
pub fn confirm(
    _app: &AppHandle<CefRuntime>,
    _title: String,
    _body: String,
    _cancel: String,
    _quit: String,
) -> bool {
    false
}
