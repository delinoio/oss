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
    cli::{Base64Command, Command, HashCommand, TextCommand},
    error::{Code, Error, Result},
    io::{Cancellation, CHUNK},
    publication::Publication,
};

enum Event {
    Processed(Result<u8>),
    Written(Result<()>),
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
            publication.publish()?;
            tracing::debug!(action = "complete", status, "operation completed");
            return Ok(status);
        }
    }
}

fn process(command: Command, writer: &mut dyn Write, cancel: &Cancellation) -> Result<u8> {
    match command {
        Command::Text {
            command: TextCommand::Replace(args),
        } => crate::text::replace(args, writer, cancel),
        Command::Base64 {
            command: Base64Command::Encode(args),
        } => crate::base64::encode(args, writer, cancel),
        Command::Base64 {
            command: Base64Command::Decode(args),
        } => crate::base64::decode(args, writer, cancel),
        Command::Time { command } => crate::time::run(command, writer),
        Command::Hash {
            command: HashCommand::Encode(args),
        } => crate::hash::encode(args, writer, cancel),
        Command::Hash {
            command: HashCommand::Verify(args),
        } => crate::hash::verify(args, writer, cancel),
    }
}
