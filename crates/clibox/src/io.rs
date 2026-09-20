use std::{
    fs::File,
    io::{self, Cursor, Read},
    path::Path,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
};

use crate::{
    cli::Input,
    error::{Code, Error, Result},
};

pub const CHUNK: usize = 64 * 1024;

#[derive(Clone)]
pub struct Cancellation(pub Arc<AtomicBool>);

impl Cancellation {
    pub fn check(&self) -> Result<()> {
        if self.0.load(Ordering::Acquire) {
            Err(Error::runtime(Code::Cancelled))
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
