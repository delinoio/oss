use std::{
    io::{self, Write},
    sync::{
        atomic::{AtomicBool, Ordering},
        mpsc::{self, RecvTimeoutError, SyncSender},
        Arc,
    },
    thread,
    time::Duration,
};

use crate::{
    cli::{
        Base64Args, Base64Command, Command, EncodeFormat, HashCommand, HashEncode, HashVerify,
        TextCommand, TextReplace, VerifyFormat,
    },
    error::{Code, Error, Result},
    io::{Cancellation, CHUNK},
    publication::Publication,
};

enum Event {
    Processed(Result<u8>),
    Written(Result<()>),
}

enum Prepared {
    Text(TextReplace, Option<regex::Regex>),
    Time(String),
    Base64Encode(Base64Args),
    Base64Decode(Base64Args),
    HashEncode(HashEncode),
    HashVerify(HashVerify, Option<Vec<u8>>),
}

impl Prepared {
    fn new(command: Command) -> Result<Self> {
        match command {
            Command::Text {
                command: TextCommand::Replace(args),
            } => {
                let regex = crate::text::prepare(&args)?;
                Ok(Self::Text(args, regex))
            }
            Command::Time { command } => Ok(Self::Time(crate::time::prepare(&command)?)),
            Command::Base64 {
                command: Base64Command::Encode(args),
            } => Ok(Self::Base64Encode(args)),
            Command::Base64 {
                command: Base64Command::Decode(args),
            } => Ok(Self::Base64Decode(args)),
            Command::Hash {
                command: HashCommand::Encode(args),
            } => {
                if matches!(args.format, EncodeFormat::Checksum)
                    && args
                        .source
                        .input
                        .as_ref()
                        .is_none_or(|path| path.as_os_str() == "-")
                {
                    return Err(Error::argument(Code::Arguments));
                }
                Ok(Self::HashEncode(args))
            }
            Command::Hash {
                command: HashCommand::Verify(args),
            } => {
                let expected = args
                    .expected
                    .as_ref()
                    .map(|value| {
                        crate::hash::expected(
                            value.as_bytes(),
                            args.format.unwrap_or(VerifyFormat::Hex),
                            args.algorithm,
                        )
                    })
                    .transpose()?;
                Ok(Self::HashVerify(args, expected))
            }
        }
    }
}

struct ChannelWriter(SyncSender<Vec<u8>>);

impl Write for ChannelWriter {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        let length = bytes.len().min(CHUNK);
        if length == 0 {
            return Ok(0);
        }
        self.0
            .send(bytes[..length].to_vec())
            .map_err(|_| io::Error::from(io::ErrorKind::BrokenPipe))?;
        Ok(length)
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

pub fn write(writer: &mut dyn Write, bytes: &[u8]) -> Result<()> {
    writer
        .write_all(bytes)
        .map_err(|_| Error::runtime(Code::WriteFailed))
}

pub fn execute(command: Command) -> Result<u8> {
    let cancel = Cancellation(Arc::new(AtomicBool::new(false)));
    let signal = cancel.clone();
    ctrlc::set_handler(move || signal.0.store(true, Ordering::Release))
        .map_err(|_| Error::runtime(Code::Runtime))?;
    let (path, replace) = command.output()?;
    let (ready_tx, ready_rx) = mpsc::channel();
    thread::Builder::new()
        .name("clibox-validate".into())
        .spawn(move || {
            let _ = ready_tx.send(Prepared::new(command));
        })
        .map_err(|_| Error::runtime(Code::Runtime))?;
    // Semantic argument errors take precedence over filesystem failures. Regex
    // compilation/time preparation remain interruptible, with no file effects.
    let command = loop {
        cancel.check()?;
        match ready_rx.recv_timeout(Duration::from_millis(20)) {
            Ok(result) => break result?,
            Err(RecvTimeoutError::Timeout) => {}
            Err(RecvTimeoutError::Disconnected) => return Err(Error::runtime(Code::Runtime)),
        }
    };
    cancel.check()?;
    let (publication, output) = Publication::prepare(path, replace)?;
    let (events_tx, events_rx) = mpsc::channel();
    let (chunks_tx, chunks_rx) = mpsc::sync_channel::<Vec<u8>>(2);
    let output_events = events_tx.clone();
    thread::Builder::new()
        .name("clibox-output".into())
        .spawn(move || {
            let result = (|| {
                let mut output: Box<dyn Write> = match output {
                    Some(file) => Box::new(file),
                    None => Box::new(io::stdout()),
                };
                for bytes in chunks_rx {
                    write(&mut *output, &bytes)?;
                }
                output
                    .flush()
                    .map_err(|_| Error::runtime(Code::WriteFailed))
            })();
            let _ = output_events.send(Event::Written(result));
        })
        .map_err(|_| Error::runtime(Code::Runtime))?;
    let worker_cancel = cancel.clone();
    thread::Builder::new()
        .name("clibox-transform".into())
        .spawn(move || {
            let mut writer = ChannelWriter(chunks_tx);
            let result = process(command, &mut writer, &worker_cancel);
            drop(writer);
            let _ = events_tx.send(Event::Processed(result));
        })
        .map_err(|_| Error::runtime(Code::Runtime))?;

    // The supervisor alone owns the temporary path and publication authority.
    // It never joins a blocked stdin/stdout/regex worker on cancellation. Returning
    // from main terminates those threads and closes their handles; Windows temp
    // handles permit delete sharing, so cleanup also works while a write is
    // pending. The bounded channel limits streaming memory without imposing an
    // input limit.
    let mut processed = None;
    let mut written = false;
    loop {
        cancel.check()?;
        match events_rx.recv_timeout(Duration::from_millis(20)) {
            Ok(Event::Processed(result)) => processed = Some(result?),
            Ok(Event::Written(result)) => {
                result?;
                written = true;
            }
            Err(RecvTimeoutError::Timeout) => continue,
            Err(RecvTimeoutError::Disconnected) => return Err(Error::runtime(Code::Runtime)),
        }
        if let (Some(status), true) = (processed, written) {
            cancel.check()?;
            publication.publish(|| cancel.check())?;
            tracing::debug!(action = "complete", status, "operation completed");
            return Ok(status);
        }
    }
}

fn process(command: Prepared, writer: &mut dyn Write, cancel: &Cancellation) -> Result<u8> {
    match command {
        Prepared::Text(args, regex) => crate::text::replace(args, regex, writer, cancel),
        Prepared::Base64Encode(args) => crate::base64::encode(args, writer, cancel),
        Prepared::Base64Decode(args) => crate::base64::decode(args, writer, cancel),
        Prepared::Time(value) => {
            write(writer, value.as_bytes())?;
            Ok(0)
        }
        Prepared::HashEncode(args) => crate::hash::encode(args, writer, cancel),
        Prepared::HashVerify(args, expected) => crate::hash::verify(args, expected, writer, cancel),
    }
}
