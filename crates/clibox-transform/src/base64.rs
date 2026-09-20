use std::io::Write;

use base64::{
    alphabet,
    engine::{
        general_purpose::{GeneralPurpose, GeneralPurposeConfig},
        DecodePaddingMode,
    },
    Engine,
};

use crate::{
    cli::Base64Args,
    io::{read, Cancellation, CHUNK},
    transform::write,
    transform_error::{Code, Error, Result},
};

fn engine(args: &Base64Args) -> GeneralPurpose {
    GeneralPurpose::new(
        if args.url_safe {
            &alphabet::URL_SAFE
        } else {
            &alphabet::STANDARD
        },
        GeneralPurposeConfig::new()
            .with_encode_padding(!args.no_padding)
            .with_decode_padding_mode(if args.no_padding {
                DecodePaddingMode::RequireNone
            } else {
                DecodePaddingMode::RequireCanonical
            })
            .with_decode_allow_trailing_bits(false),
    )
}

pub fn encode(args: Base64Args, writer: &mut dyn Write, cancel: &Cancellation) -> Result<u8> {
    let engine = engine(&args);
    let mut reader = args.source.reader()?;
    let mut encoder = base64::write::EncoderWriter::new(writer, &engine);
    let mut buffer = [0; CHUNK];
    loop {
        let size = read(&mut *reader, &mut buffer, cancel)?;
        if size == 0 {
            break;
        }
        write(&mut encoder, &buffer[..size])?;
    }
    cancel.check()?;
    encoder
        .finish()
        .map_err(|_| Error::runtime(Code::WriteFailed))?;
    Ok(0)
}

pub fn decode(args: Base64Args, writer: &mut dyn Write, cancel: &Cancellation) -> Result<u8> {
    let engine = engine(&args);
    let mut reader = args.source.reader()?;
    let mut buffer = [0; CHUNK];
    let mut pending = Vec::with_capacity(CHUNK + 4);
    loop {
        let size = read(&mut *reader, &mut buffer, cancel)?;
        if size == 0 {
            break;
        }
        for byte in buffer[..size]
            .iter()
            .copied()
            .filter(|byte| !byte.is_ascii_whitespace() && *byte != 0x0b)
        {
            pending.push(byte);
        }
        // Keep the final quartet until EOF so padding is legal only at the end.
        // Any earlier padding is rejected before decoding subsequent data.
        let prefix = pending.len().saturating_sub(1) / 4 * 4;
        if prefix > 0 {
            if pending[..prefix].contains(&b'=') {
                return Err(Error::runtime(Code::InvalidBase64));
            } else {
                let decoded = engine
                    .decode(&pending[..prefix])
                    .map_err(|_| Error::runtime(Code::InvalidBase64))?;
                write(writer, &decoded)?;
                pending.drain(..prefix);
            }
        }
    }
    cancel.check()?;
    let decoded = engine
        .decode(&pending)
        .map_err(|_| Error::runtime(Code::InvalidBase64))?;
    write(writer, &decoded)?;
    Ok(0)
}
