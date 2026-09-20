use std::{
    ffi::OsString,
    io::{BufRead, BufReader, Read, Write},
    path::{Path, PathBuf},
};

use base64::{engine::general_purpose::STANDARD, Engine};
use serde::Serialize;
use sha2::{Digest, Sha256, Sha512};

use crate::{
    cli::{Algorithm, EncodeFormat, HashEncode, HashVerify, Input, VerifyFormat},
    error::{Code, Error, Result},
    io::{read, reader, Cancellation, CHUNK},
    runtime::write,
};

enum Hasher {
    Sha256(Sha256),
    Sha512(Sha512),
    Blake3(Box<blake3::Hasher>),
}

fn digest(
    algorithm: Algorithm,
    mut input: Box<dyn Read>,
    cancel: &Cancellation,
) -> Result<Vec<u8>> {
    let mut hasher = match algorithm {
        Algorithm::Sha256 => Hasher::Sha256(Sha256::new()),
        Algorithm::Sha512 => Hasher::Sha512(Sha512::new()),
        Algorithm::Blake3 => Hasher::Blake3(Box::new(blake3::Hasher::new())),
    };
    let mut buffer = [0; CHUNK];
    loop {
        let size = read(&mut *input, &mut buffer, cancel)?;
        if size == 0 {
            break;
        }
        match &mut hasher {
            Hasher::Sha256(hasher) => hasher.update(&buffer[..size]),
            Hasher::Sha512(hasher) => hasher.update(&buffer[..size]),
            Hasher::Blake3(hasher) => {
                hasher.update(&buffer[..size]);
            }
        }
    }
    Ok(match hasher {
        Hasher::Sha256(hasher) => hasher.finalize().to_vec(),
        Hasher::Sha512(hasher) => hasher.finalize().to_vec(),
        Hasher::Blake3(hasher) => hasher.finalize().as_bytes().to_vec(),
    })
}

fn hex(bytes: &[u8]) -> String {
    const DIGITS: &[u8; 16] = b"0123456789abcdef";
    let mut result = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        result.push(DIGITS[(byte >> 4) as usize] as char);
        result.push(DIGITS[(byte & 15) as usize] as char);
    }
    result
}

fn expected(value: &[u8], format: VerifyFormat, algorithm: Algorithm) -> Result<Vec<u8>> {
    let length = if matches!(algorithm, Algorithm::Sha512) {
        64
    } else {
        32
    };
    let invalid = || Error::argument(Code::InvalidDigest);
    let bytes = match format {
        VerifyFormat::Hex => {
            if value.len() != length * 2 {
                return Err(invalid());
            }
            value
                .chunks_exact(2)
                .map(|pair| {
                    let upper = (pair[0] as char).to_digit(16).ok_or_else(invalid)?;
                    let lower = (pair[1] as char).to_digit(16).ok_or_else(invalid)?;
                    Ok((upper * 16 + lower) as u8)
                })
                .collect::<Result<Vec<_>>>()?
        }
        VerifyFormat::Base64 => STANDARD.decode(value).map_err(|_| invalid())?,
    };
    if bytes.len() != length {
        return Err(invalid());
    }
    Ok(bytes)
}

#[cfg(unix)]
fn path_bytes(path: &Path) -> Vec<u8> {
    use std::os::unix::ffi::OsStrExt;
    path.as_os_str().as_bytes().to_vec()
}

#[cfg(windows)]
fn path_bytes(path: &Path) -> Vec<u8> {
    path.to_string_lossy().as_bytes().to_vec()
}

#[cfg(unix)]
fn path_from_bytes(bytes: Vec<u8>) -> Result<PathBuf> {
    use std::os::unix::ffi::OsStringExt;
    Ok(OsString::from_vec(bytes).into())
}

#[cfg(windows)]
fn path_from_bytes(bytes: Vec<u8>) -> Result<PathBuf> {
    String::from_utf8(bytes)
        .map(|value| OsString::from(value).into())
        .map_err(|_| Error::runtime(Code::MalformedRecord))
}

pub fn encode(args: HashEncode, writer: &mut dyn Write, cancel: &Cancellation) -> Result<u8> {
    let checksum_path = if matches!(args.format, EncodeFormat::Checksum) {
        let path = args
            .source
            .input
            .as_ref()
            .filter(|path| path.as_os_str() != "-")
            .ok_or_else(|| Error::argument(Code::Arguments))?;
        Some(path_bytes(path))
    } else {
        None
    };
    let digest = digest(args.algorithm, args.source.reader()?, cancel)?;
    match args.format {
        EncodeFormat::Hex => write(writer, hex(&digest).as_bytes())?,
        EncodeFormat::Base64 => write(writer, STANDARD.encode(&digest).as_bytes())?,
        EncodeFormat::Checksum => {
            let path = checksum_path.unwrap();
            let escaped = path
                .iter()
                .any(|byte| matches!(byte, b'\\' | b'\n' | b'\r'));
            if escaped {
                write(writer, b"\\")?;
            }
            write(writer, hex(&digest).as_bytes())?;
            write(writer, b" *")?;
            let mut encoded = Vec::with_capacity(path.len());
            for byte in path {
                match byte {
                    b'\\' => encoded.extend_from_slice(b"\\\\"),
                    b'\n' => encoded.extend_from_slice(b"\\n"),
                    b'\r' => encoded.extend_from_slice(b"\\r"),
                    byte => encoded.push(byte),
                }
            }
            write(writer, &encoded)?;
        }
    }
    write(writer, b"\n")?;
    Ok(0)
}

#[derive(Serialize)]
#[serde(tag = "kind", rename_all = "kebab-case")]
enum Source {
    File { path: String },
    Stdin,
    Text,
}

impl Source {
    fn file(path: &Path) -> Self {
        Self::File {
            path: display_path(path),
        }
    }

    fn input(input: &Input) -> Self {
        match &input.input {
            Some(path) if path.as_os_str() != "-" => Self::file(path),
            _ if input.text.is_some() => Self::Text,
            _ => Self::Stdin,
        }
    }

    fn human(&self) -> String {
        match self {
            Self::File { path } => path
                .chars()
                .flat_map(|ch| {
                    if ch.is_control() {
                        ch.escape_default().collect::<Vec<_>>()
                    } else {
                        vec![ch]
                    }
                })
                .collect(),
            Self::Stdin => "stdin".into(),
            Self::Text => "text".into(),
        }
    }
}

fn display_path(path: &Path) -> String {
    match path.to_str() {
        Some(path) => path.to_owned(),
        // Unix permits non-UTF-8 filenames. Preserve their identity with byte
        // escapes instead of conflating distinct names as replacement chars.
        None => path_bytes(path)
            .iter()
            .flat_map(|byte| std::ascii::escape_default(*byte))
            .map(char::from)
            .collect(),
    }
}

#[derive(Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
enum Status {
    Ok,
    Mismatch,
    Error,
}

#[derive(Serialize)]
struct Verification {
    source: Source,
    status: Status,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<Code>,
}

#[derive(Serialize)]
struct ManifestError {
    code: Code,
    #[serde(skip_serializing_if = "Option::is_none")]
    line: Option<usize>,
}

#[derive(Default, Serialize)]
struct Report {
    results: Vec<Verification>,
    errors: Vec<ManifestError>,
}

fn verify_one(source: Source, expected: &[u8], actual: Result<Vec<u8>>) -> Result<Verification> {
    let (status, code) = match actual {
        Ok(actual) => (
            if expected == actual {
                Status::Ok
            } else {
                Status::Mismatch
            },
            None,
        ),
        Err(error) if error.code == Code::Cancelled => return Err(error),
        Err(error) => (Status::Error, Some(error.code)),
    };
    Ok(Verification {
        source,
        status,
        code,
    })
}

fn record(line: &[u8], algorithm: Algorithm) -> Result<(Vec<u8>, PathBuf)> {
    let invalid = || Error::runtime(Code::MalformedRecord);
    let (escaped, line) = match line.strip_prefix(b"\\") {
        Some(line) => (true, line),
        None => (false, line),
    };
    let size = if matches!(algorithm, Algorithm::Sha512) {
        128
    } else {
        64
    };
    if line.len() <= size + 2 || line[size] != b' ' || !matches!(line[size + 1], b'*' | b' ') {
        return Err(invalid());
    }
    let digest = expected(&line[..size], VerifyFormat::Hex, algorithm).map_err(|_| invalid())?;
    let mut name = Vec::new();
    let mut bytes = line[size + 2..].iter().copied();
    while let Some(byte) = bytes.next() {
        let byte = if escaped && byte == b'\\' {
            match bytes.next() {
                Some(b'\\') => b'\\',
                Some(b'n') => b'\n',
                Some(b'r') => b'\r',
                _ => return Err(invalid()),
            }
        } else {
            byte
        };
        if byte == 0 {
            return Err(invalid());
        }
        name.push(byte);
    }
    Ok((digest, path_from_bytes(name)?))
}

fn check(args: &HashVerify, path: &Path, cancel: &Cancellation) -> Result<Report> {
    let mut report = Report::default();
    let input = match reader(Some(path)) {
        Ok(input) => input,
        Err(error) => {
            report.errors.push(ManifestError {
                code: error.code,
                line: None,
            });
            return Ok(report);
        }
    };
    let parent = if path.as_os_str() == "-" {
        Path::new(".")
    } else {
        path.parent().unwrap_or(Path::new("."))
    };
    let mut input = BufReader::new(input);
    let mut line = Vec::new();
    let mut number = 0;
    loop {
        cancel.check()?;
        line.clear();
        let length = match input.read_until(b'\n', &mut line) {
            Ok(length) => length,
            Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
            Err(_) => {
                report.errors.push(ManifestError {
                    code: Code::ReadFailed,
                    line: Some(number + 1),
                });
                break;
            }
        };
        if length == 0 {
            break;
        }
        number += 1;
        if line.last() == Some(&b'\n') {
            line.pop();
            if line.last() == Some(&b'\r') {
                line.pop();
            }
        }
        match record(&line, args.algorithm) {
            Ok((expected, filename)) => {
                let resolved = parent.join(&filename);
                // A record names a file even when its name is "-", unlike the
                // explicit manifest/input stdin selectors.
                let actual = std::fs::File::open(&resolved)
                    .map_err(|_| Error::runtime(Code::ReadFailed))
                    .and_then(|file| digest(args.algorithm, Box::new(file), cancel));
                report
                    .results
                    .push(verify_one(Source::file(&filename), &expected, actual)?);
            }
            Err(error) => report.errors.push(ManifestError {
                code: error.code,
                line: Some(number),
            }),
        }
    }
    if number == 0 && report.errors.is_empty() {
        report.errors.push(ManifestError {
            code: Code::EmptyManifest,
            line: None,
        });
    }
    Ok(report)
}

pub fn verify(args: HashVerify, writer: &mut dyn Write, cancel: &Cancellation) -> Result<u8> {
    let report = if let Some(path) = &args.check {
        check(&args, path, cancel)?
    } else {
        let expected = expected(
            args.expected.as_ref().unwrap().as_bytes(),
            args.format.unwrap_or(VerifyFormat::Hex),
            args.algorithm,
        )?;
        let actual = args
            .source
            .reader()
            .and_then(|reader| digest(args.algorithm, reader, cancel));
        Report {
            results: vec![verify_one(Source::input(&args.source), &expected, actual)?],
            errors: vec![],
        }
    };
    cancel.check()?;
    let failed = !report.errors.is_empty()
        || report
            .results
            .iter()
            .any(|result| result.status != Status::Ok);
    if !args.quiet {
        if args.json {
            serde_json::to_writer(&mut *writer, &report)
                .map_err(|_| Error::runtime(Code::WriteFailed))?;
            write(writer, b"\n")?;
        } else {
            for result in &report.results {
                let status = match result.status {
                    Status::Ok => "ok",
                    Status::Mismatch => "mismatch",
                    Status::Error => "error",
                };
                write(
                    writer,
                    format!("{}: {status}\n", result.source.human()).as_bytes(),
                )?;
            }
            for error in &report.errors {
                let code = serde_json::to_string(&error.code).unwrap();
                let line = error
                    .line
                    .map(|line| format!(" line {line}"))
                    .unwrap_or_default();
                write(
                    writer,
                    format!("manifest{line}: error ({})\n", code.trim_matches('"')).as_bytes(),
                )?;
            }
        }
    }
    tracing::debug!(
        action = "verify",
        entries = report.results.len(),
        errors = report.errors.len(),
        failed,
        "verification completed"
    );
    Ok(u8::from(failed))
}
