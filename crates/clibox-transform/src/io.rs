use std::{
    fs::File,
    io::{self, Cursor, Read},
    path::Path,
    sync::{
        atomic::{AtomicUsize, Ordering},
        Arc,
    },
};

use crate::{
    cli::Input,
    transform_error::{Code, Error, Result},
};

pub const CHUNK: usize = 64 * 1024;

#[derive(Clone)]
pub struct Cancellation(Arc<AtomicUsize>);

impl Cancellation {
    pub fn install() -> Result<Self> {
        let token = Self(Arc::new(AtomicUsize::new(0)));
        #[cfg(unix)]
        // Preserve the former ctrlc termination feature's SIGHUP cleanup/status.
        // The common 130/143 contract changes only SIGINT and SIGTERM.
        for (signal, exit) in [(libc::SIGINT, 130), (libc::SIGTERM, 143), (libc::SIGHUP, 1)] {
            let state = token.0.clone();
            // Record only the first cancellation reason. Cleanup and publication
            // stay on the supervisor; handlers perform no I/O or allocation.
            unsafe {
                signal_hook::low_level::register(signal, move || {
                    let _ = state.compare_exchange(0, exit, Ordering::SeqCst, Ordering::SeqCst);
                })
            }
            .map_err(|_| Error::runtime(Code::Runtime))?;
        }
        #[cfg(windows)]
        {
            let state = token.0.clone();
            ctrlc::set_handler(move || {
                let _ = state.compare_exchange(0, 130, Ordering::SeqCst, Ordering::SeqCst);
            })
            .map_err(|_| Error::runtime(Code::Runtime))?;
        }
        Ok(token)
    }

    pub fn check(&self) -> Result<()> {
        let exit = self.0.load(Ordering::Acquire);
        if exit != 0 {
            Err(Error {
                code: Code::Cancelled,
                exit: exit as u8,
            })
        } else {
            Ok(())
        }
    }
}

impl Input {
    pub fn reader(&self) -> Result<Box<dyn Read>> {
        if let Some(text) = &self.text {
            return Ok(Box::new(Cursor::new(text.clone().into_bytes())));
        }
        reader(self.input.as_deref())
    }
}

pub fn reader(path: Option<&Path>) -> Result<Box<dyn Read>> {
    match path {
        Some(path) if path.as_os_str() != "-" => File::open(path)
            .map(|file| Box::new(file) as Box<dyn Read>)
            .map_err(|_| Error::runtime(Code::ReadFailed)),
        _ => Ok(Box::new(io::stdin())),
    }
}

pub fn read(reader: &mut dyn Read, buffer: &mut [u8], cancel: &Cancellation) -> Result<usize> {
    loop {
        cancel.check()?;
        match reader.read(buffer) {
            Ok(size) => return Ok(size),
            Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
            Err(_) => return Err(Error::runtime(Code::ReadFailed)),
        }
    }
}
