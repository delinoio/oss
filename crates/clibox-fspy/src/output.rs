use std::{
    fs,
    io::{self, Write},
    path::{Path, PathBuf},
};

/// Publish report or trace bytes without replacing an existing destination
/// after failed generation, validation, writing, or synchronization.
pub fn write(bytes: &[u8], destination: Option<&Path>, force: bool) -> io::Result<()> {
    let Some(destination) = destination else {
        let mut stdout = io::stdout().lock();
        stdout.write_all(bytes)?;
        return stdout.flush();
    };
    let existing = match fs::symlink_metadata(destination) {
        Ok(metadata) if metadata.file_type().is_file() => Some(metadata),
        Ok(_) => return Err(io::Error::other("output destination is not a regular file")),
        Err(error) if error.kind() == io::ErrorKind::NotFound => None,
        Err(error) => return Err(error),
    };
    if existing.is_some() && !force {
        return Err(io::Error::new(
            io::ErrorKind::AlreadyExists,
            "output already exists",
        ));
    }
    let parent = destination
        .parent()
        .filter(|path| !path.as_os_str().is_empty())
        .unwrap_or_else(|| Path::new("."));
    let mut temporary = tempfile::Builder::new()
        .prefix(".clibox-fspy-")
        .tempfile_in(parent)?;
    temporary.write_all(bytes)?;
    temporary.flush()?;
    temporary.as_file().sync_all()?;
    if let Some(metadata) = existing {
        fs::set_permissions(temporary.path(), metadata.permissions())?;
    }
    if force {
        temporary
            .persist(destination)
            .map_err(|error| error.error)?;
    } else {
        temporary
            .persist_noclobber(destination)
            .map_err(|error| error.error)?;
    }
    if let Ok(parent) = parent.canonicalize() {
        let _ = fs::File::open(parent).and_then(|directory| directory.sync_all());
    }
    Ok(())
}

pub fn destination(value: Option<&PathBuf>, force: bool) -> Result<Option<&Path>, &'static str> {
    let destination = value
        .map(PathBuf::as_path)
        .filter(|path| path.as_os_str() != "-");
    if force && destination.is_none() {
        return Err("--force requires an explicit file destination.");
    }
    Ok(destination)
}
