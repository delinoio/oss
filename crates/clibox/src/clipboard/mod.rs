use std::{
    ffi::OsString,
    io::{self, Write},
};

use clap::Subcommand;

use crate::{
    error::{Code, Failure, Result},
    runtime,
};

#[cfg(target_os = "linux")]
mod linux;
#[cfg(target_os = "macos")]
mod macos;
#[cfg(windows)]
mod windows;
#[cfg(target_os = "linux")]
use linux::Native;
#[cfg(target_os = "macos")]
use macos::Native;
#[cfg(windows)]
use windows::Native;

pub const LIMIT: usize = 16 * 1024 * 1024;
#[derive(Subcommand)]
pub enum Action {
    /// Copy one text argument, or read stdin to EOF when no argument is
    /// supplied.
    #[command(
        after_help = "Examples:\n  clibox clipboard copy \"hello\"\n  clibox clipboard copy \
                      \"\"\n  cat notes.txt | clibox clipboard copy\n\nTEXT takes precedence over \
                      stdin. Accepts at most 16 MiB of NUL-free UTF-8. Linux requires xclip (X11) \
                      or wl-copy (Wayland), retaining ownership in the background."
    )]
    Copy { text: Option<OsString> },
    /// Write UTF-8 clipboard text without adding a newline.
    #[command(
        after_help = "Example:\n  clibox clipboard paste > notes.txt\n\nAn empty clipboard \
                      succeeds with empty output. Non-text-only data fails. Linux requires xclip \
                      or wl-paste in the current desktop session."
    )]
    Paste,
}

pub enum Content {
    Empty,
    Text(Vec<u8>),
    NonText,
}
pub trait Backend {
    fn copy(&mut self, text: &str) -> Result<()>;
    fn paste(&mut self) -> Result<Content>;
}

pub fn validate(bytes: &[u8]) -> Result<&str> {
    if bytes.len() > LIMIT {
        return Err(Failure::new(
            Code::TextTooLarge,
            "Text exceeds the 16 MiB UTF-8 limit; the clipboard and stdout were not changed.",
        ));
    }
    if bytes.contains(&0) {
        return Err(Failure::new(
            Code::InvalidText,
            "Clipboard text must not contain NUL; the clipboard and stdout were not changed.",
        ));
    }
    std::str::from_utf8(bytes).map_err(|_| {
        Failure::new(
            Code::InvalidText,
            "Clipboard text must be valid UTF-8; the clipboard and stdout were not changed.",
        )
    })
}

fn copy(backend: &mut impl Backend, bytes: &[u8]) -> Result<()> {
    let text = validate(bytes)?;
    runtime::check_cancelled()?;
    backend.copy(text)
}
fn paste(backend: &mut impl Backend, out: &mut impl Write) -> Result<()> {
    let bytes = match backend.paste()? {
        Content::Empty => Vec::new(),
        Content::Text(bytes) => bytes,
        Content::NonText => {
            return Err(Failure::new(
                Code::ClipboardNonText,
                "The clipboard contains no supported plain text; copy text in a desktop \
                 application first.",
            ))
        }
    };
    validate(&bytes)?;
    runtime::check_cancelled()?;
    out.write_all(&bytes).map_err(|e| Failure::io(&e))
}

pub fn execute(action: Action) -> Result<()> {
    tracing::debug!(
        operation = match &action {
            Action::Copy { .. } => "clipboard-copy",
            Action::Paste => "clipboard-paste",
        },
        backend = std::env::consts::OS,
        "Clipboard operation started"
    );
    match action {
        Action::Copy { text } => {
            let bytes = if let Some(text) = text {
                text.into_string()
                    .map_err(|_| {
                        Failure::new(Code::InvalidText, "The text argument must be valid UTF-8.")
                    })?
                    .into_bytes()
            } else {
                runtime::interruptible(|| runtime::read_bounded(io::stdin(), LIMIT))?
            };
            validate(&bytes)?;
            #[cfg(target_os = "linux")]
            {
                copy(&mut Native::default(), &bytes)
            }
            #[cfg(not(target_os = "linux"))]
            {
                runtime::interruptible(move || copy(&mut Native::default(), &bytes))
            }
        }
        Action::Paste => {
            let read = || {
                let mut bytes = Vec::new();
                paste(&mut Native::default(), &mut bytes)?;
                Ok(bytes)
            };
            #[cfg(target_os = "linux")]
            let bytes = read()?;
            #[cfg(not(target_os = "linux"))]
            let bytes = runtime::interruptible(read)?;
            runtime::check_cancelled()?;
            io::stdout()
                .lock()
                .write_all(&bytes)
                .map_err(|e| Failure::io(&e))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[derive(Default)]
    struct Fake {
        content: Vec<u8>,
        copies: usize,
        non_text: bool,
    }
    impl Backend for Fake {
        fn copy(&mut self, text: &str) -> Result<()> {
            self.copies += 1;
            self.content = text.as_bytes().to_vec();
            Ok(())
        }

        fn paste(&mut self) -> Result<Content> {
            if self.non_text {
                Ok(Content::NonText)
            } else if self.content.is_empty() {
                Ok(Content::Empty)
            } else {
                Ok(Content::Text(self.content.clone()))
            }
        }
    }
    #[test]
    fn exact_text_roundtrips_and_empty_is_a_copy() {
        for text in ["", " \t\n", "한글 🦀\r\nlast\r"] {
            let mut fake = Fake::default();
            copy(&mut fake, text.as_bytes()).unwrap();
            let mut out = Vec::new();
            paste(&mut fake, &mut out).unwrap();
            assert_eq!(out, text.as_bytes());
            assert_eq!(fake.copies, 1);
        }
    }
    #[test]
    fn invalid_data_never_replaces_or_emits() {
        for bytes in [vec![0xff], b"before\0after".to_vec(), vec![b'x'; LIMIT + 1]] {
            let mut fake = Fake {
                content: b"previous".to_vec(),
                ..Default::default()
            };
            assert!(copy(&mut fake, &bytes).is_err());
            assert_eq!(fake.copies, 0);
            assert_eq!(fake.content, b"previous");
            fake.content = bytes;
            let mut out = Vec::new();
            assert!(paste(&mut fake, &mut out).is_err());
            assert!(out.is_empty());
        }
    }
    #[test]
    fn byte_boundary_and_nontext() {
        assert!(validate(&vec![b'x'; LIMIT]).is_ok());
        assert!(validate("🦀".repeat(LIMIT / 4).as_bytes()).is_ok());
        assert_eq!(
            validate("🦀".repeat(LIMIT / 4 + 1).as_bytes())
                .unwrap_err()
                .code,
            Code::TextTooLarge
        );
        let mut out = Vec::new();
        assert_eq!(
            paste(
                &mut Fake {
                    non_text: true,
                    ..Default::default()
                },
                &mut out
            )
            .unwrap_err()
            .code,
            Code::ClipboardNonText
        );
        assert!(out.is_empty());
    }
}
