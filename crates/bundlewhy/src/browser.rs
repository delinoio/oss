//! Opens the local UI in the default browser without a shell.

use std::process::{Command, Stdio};

use crate::error::{BwError, BwResult, ErrorCode};

pub fn open(url: &str) -> BwResult<()> {
    let mut command = if cfg!(target_os = "macos") {
        let mut command = Command::new("open");
        command.arg(url);
        command
    } else if cfg!(windows) {
        // `start` is a cmd builtin; the empty string is the window title.
        let mut command = Command::new("cmd");
        command.args(["/C", "start", "", url]);
        command
    } else {
        let mut command = Command::new("xdg-open");
        command.arg(url);
        command
    };
    command
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    command.spawn().map(drop).map_err(|error| {
        BwError::new(
            ErrorCode::BrowserOpenFailed,
            format!("Could not open a browser: {error}"),
            "Open the printed URL manually.",
        )
    })
}
