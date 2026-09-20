//! Bounded I/O, cancellation, and deliberately content-free diagnostics.
use std::{
    io::{Read, Write},
    sync::{
        atomic::{AtomicUsize, Ordering},
        mpsc, Arc,
    },
    time::Duration,
};

pub const LIMIT: usize = 64 * 1024 * 1024;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Failure {
    Arguments,
    Encoding,
    InputLimit,
    OutputLimit,
    DotenvSyntax,
    YamlSyntax,
    YamlVersion,
    YamlTag,
    YamlKey,
    DuplicateKey,
    MergeOperand,
    Alias,
    Cycle,
    Depth,
    Read,
    Write,
    DestinationExists,
    UnsafeDestination,
    Permissions,
    Publish,
    Cancelled,
    Internal,
}

impl Failure {
    pub fn guidance(self) -> &'static str {
        match self {
            Self::Arguments => {
                "Invalid, missing, or conflicting arguments; use the selected command's --help."
            }
            Self::Encoding => {
                "Use valid UTF-8 without NUL bytes; an initial UTF-8 BOM is accepted."
            }
            Self::InputLimit => "Reduce aggregate raw input to at most 64 MiB.",
            Self::OutputLimit => {
                "Reduce serialized output or YAML alias expansion to at most 64 MiB."
            }
            Self::DotenvSyntax => {
                "Check the indicated assignment: ASCII identifier, equals sign, balanced quotes, \
                 and no trailing content after quotes."
            }
            Self::YamlSyntax => {
                "Check YAML syntax or unresolved aliases at the indicated position."
            }
            Self::YamlVersion => "Use YAML 1.2 or omit the version directive.",
            Self::YamlTag => {
                "Use only YAML Core types or the merge-key extension, with a valid value for the \
                 selected type."
            }
            Self::YamlKey => {
                "Use string mapping keys; quote keys that resemble numbers, booleans, or null."
            }
            Self::DuplicateKey => "Remove the duplicate mapping key at the indicated position.",
            Self::MergeOperand => {
                "A merge requires a mapping or a sequence containing only mappings."
            }
            Self::Alias => "Define anchors before use within the same document.",
            Self::Cycle => "Remove cyclic YAML references.",
            Self::Depth => {
                "Reduce collection nesting to at most 128 levels, including expanded aliases."
            }
            Self::Read => {
                "Check that the selected input is readable with your current permissions."
            }
            Self::Write => {
                "Check destination access and available space; stdout may contain partial output."
            }
            Self::DestinationExists => {
                "Output already exists; use --force to authorize replacement."
            }
            Self::UnsafeDestination => {
                "Select a regular destination with no symbolic link or additional hard links."
            }
            Self::Permissions => {
                "Existing access permissions could not be preserved; check file access permissions."
            }
            Self::Publish => {
                "Publication failed; check destination directory access and available space."
            }
            Self::Cancelled => {
                "Operation interrupted; completed writes are not undone and stdout may be partial."
            }
            Self::Internal => {
                "The operation could not complete; retry after checking system resources."
            }
        }
    }
}

#[derive(Debug)]
pub struct Error {
    pub kind: Failure,
    pub input: Option<usize>,
    pub document: Option<usize>,
    pub line: Option<usize>,
    pub column: Option<usize>,
}
pub type Result<T> = std::result::Result<T, Error>;
impl From<Failure> for Error {
    fn from(kind: Failure) -> Self {
        Self {
            kind,
            input: None,
            document: None,
            line: None,
            column: None,
        }
    }
}
impl Error {
    pub fn at(mut self, line: usize, column: usize) -> Self {
        self.line = Some(line);
        self.column = Some(column);
        self
    }

    pub fn input(mut self, ordinal: usize) -> Self {
        self.input = Some(ordinal);
        self
    }

    pub fn document(mut self, ordinal: usize) -> Self {
        self.document = Some(ordinal);
        self
    }

    pub fn report(&self, operation: &'static str) {
        if tracing::enabled!(tracing::Level::ERROR) {
            tracing::error!(operation, classification = ?self.kind, input = self.input,
                document = self.document, line = self.line, column = self.column,
                "error: {}", self.kind.guidance());
        } else {
            // Ambient logging filters must not silence actionable failures.
            // Only the same static guidance, enum, and numeric positions cross
            // this fallback. Ignore write errors so closed stderr cannot panic
            // or replace the command's failure/cancellation status.
            let _ = writeln!(
                std::io::stderr(),
                "error: {} operation={operation} classification={:?} input={:?} document={:?} \
                 line={:?} column={:?}",
                self.kind.guidance(),
                self.kind,
                self.input,
                self.document,
                self.line,
                self.column,
            );
        }
    }
}

#[derive(Clone, Default)]
pub struct Cancellation(Arc<AtomicUsize>);
impl Cancellation {
    pub fn install() -> Result<Self> {
        let token = Self::default();
        #[cfg(unix)]
        for (signal, code) in [
            (signal_hook::consts::SIGINT, 130),
            (signal_hook::consts::SIGTERM, 143),
        ] {
            signal_hook::flag::register_usize(signal, token.0.clone(), code)
                .map_err(|_| Failure::Internal)?;
        }
        #[cfg(windows)]
        {
            let state = token.0.clone();
            ctrlc::set_handler(move || {
                state.store(130, Ordering::SeqCst);
            })
            .map_err(|_| Failure::Internal)?;
        }
        Ok(token)
    }

    pub fn code(&self) -> usize {
        self.0.load(Ordering::Relaxed)
    }

    pub fn check(&self) -> Result<()> {
        if self.code() == 0 {
            Ok(())
        } else {
            Err(Failure::Cancelled.into())
        }
    }

    #[cfg(test)]
    pub fn cancel(&self) {
        self.0.store(130, Ordering::Relaxed);
    }

    // Blocking pipes/FIFOs cannot be polled portably with std::io. Keep only the
    // I/O operation in a disposable worker; all temporary-file ownership stays
    // with the main thread so cancellation still runs its cleanup destructors.
    // A blocked worker owns no publication authority or application state.
    pub fn blocking<T: Send + 'static>(
        &self,
        operation: impl FnOnce() -> Result<T> + Send + 'static,
    ) -> Result<T> {
        self.check()?;
        let (tx, rx) = mpsc::sync_channel(1);
        std::thread::Builder::new()
            .spawn(move || {
                let _ = tx.send(operation());
            })
            .map_err(|_| Failure::Internal)?;
        loop {
            self.check()?;
            match rx.recv_timeout(Duration::from_millis(20)) {
                Ok(result) => {
                    self.check()?;
                    return result;
                }
                Err(mpsc::RecvTimeoutError::Timeout) => (),
                Err(_) => return Err(Failure::Internal.into()),
            }
        }
    }
}

pub fn read(mut reader: impl Read, remaining: usize, cancel: &Cancellation) -> Result<Vec<u8>> {
    let mut bytes = Vec::new();
    let mut chunk = [0; 16 * 1024];
    loop {
        cancel.check()?;
        let amount = chunk.len().min(remaining - bytes.len() + 1);
        let count = reader
            .read(&mut chunk[..amount])
            .map_err(|_| Failure::Read)?;
        if count == 0 {
            break;
        }
        if count > remaining - bytes.len() {
            return Err(Failure::InputLimit.into());
        }
        bytes.extend_from_slice(&chunk[..count]);
    }
    Ok(bytes)
}

pub fn decode(bytes: Vec<u8>) -> Result<String> {
    if bytes.contains(&0) {
        return Err(Failure::Encoding.into());
    }
    let mut text = String::from_utf8(bytes).map_err(|_| Failure::Encoding)?;
    if text.starts_with('\u{feff}') {
        text.drain(..3);
    }
    Ok(text)
}

pub struct Output {
    pub bytes: Vec<u8>,
}
impl Output {
    pub fn new() -> Self {
        Self { bytes: Vec::new() }
    }

    pub fn push(&mut self, text: &str) -> Result<()> {
        if text.len() > LIMIT - self.bytes.len() {
            return Err(Failure::OutputLimit.into());
        }
        self.bytes.extend_from_slice(text.as_bytes());
        Ok(())
    }
}
pub fn write(mut writer: impl Write, bytes: &[u8], cancel: &Cancellation) -> Result<()> {
    for chunk in bytes.chunks(16 * 1024) {
        cancel.check()?;
        writer.write_all(chunk).map_err(|_| Failure::Write)?;
    }
    cancel.check()?;
    writer.flush().map_err(|_| Failure::Write.into())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn exact_raw_and_serialized_limits_are_independent() {
        let cancel = Cancellation::default();
        let raw = vec![b'x'; LIMIT];
        assert_eq!(read(raw.as_slice(), LIMIT, &cancel).unwrap().len(), LIMIT);
        assert_eq!(
            read(raw.as_slice(), LIMIT - 1, &cancel).unwrap_err().kind,
            Failure::InputLimit
        );
        let mut output = Output::new();
        output.push(std::str::from_utf8(&raw).unwrap()).unwrap();
        assert_eq!(output.push("x").unwrap_err().kind, Failure::OutputLimit);
        assert_eq!(output.bytes.len(), LIMIT);
    }
    #[test]
    fn cancellation_interrupts_reading_writing_and_processing() {
        struct Reader(Cancellation);
        impl Read for Reader {
            fn read(&mut self, bytes: &mut [u8]) -> std::io::Result<usize> {
                self.0.cancel();
                bytes[0] = b'x';
                Ok(1)
            }
        }
        let cancel = Cancellation::default();
        assert_eq!(
            read(Reader(cancel.clone()), LIMIT, &cancel)
                .unwrap_err()
                .kind,
            Failure::Cancelled
        );
        assert_eq!(
            crate::yaml::normalize("a: 1", &cancel).unwrap_err().kind,
            Failure::Cancelled
        );
        assert_eq!(
            crate::dotenv::parse("A=1", &mut crate::dotenv::Values::new(), &cancel)
                .unwrap_err()
                .kind,
            Failure::Cancelled
        );
        struct Writer(Cancellation);
        impl Write for Writer {
            fn write(&mut self, bytes: &[u8]) -> std::io::Result<usize> {
                self.0.cancel();
                Ok(bytes.len())
            }

            fn flush(&mut self) -> std::io::Result<()> {
                Ok(())
            }
        }
        let cancel = Cancellation::default();
        assert_eq!(
            write(Writer(cancel.clone()), &[0; 32768], &cancel)
                .unwrap_err()
                .kind,
            Failure::Cancelled
        );
    }
}
