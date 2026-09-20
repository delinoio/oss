use std::io::Write;

use regex::Regex;

use crate::{
    cli::TextReplace,
    error::{Code, Error, Result},
    io::{read, Cancellation, CHUNK},
    runtime::write,
};

fn validate_replacement(regex: &Regex, replacement: &str) -> Result<()> {
    let bytes = replacement.as_bytes();
    let mut position = 0;
    while position < bytes.len() {
        if bytes[position] != b'$' {
            position += 1;
            continue;
        }
        position += 1;
        if bytes.get(position) == Some(&b'$') {
            position += 1;
            continue;
        }
        let start = position;
        let name = if bytes.get(position) == Some(&b'{') {
            let Some(end) = replacement[position + 1..].find('}') else {
                continue;
            };
            position += end + 2;
            &replacement[start + 1..position - 1]
        } else {
            while bytes
                .get(position)
                .is_some_and(|byte| byte.is_ascii_alphanumeric() || *byte == b'_')
            {
                position += 1;
            }
            if start == position {
                continue;
            }
            &replacement[start..position]
        };
        let exists = if !name.is_empty() && name.bytes().all(|byte| byte.is_ascii_digit()) {
            name.parse::<usize>()
                .is_ok_and(|index| index < regex.captures_len())
        } else {
            regex
                .capture_names()
                .flatten()
                .any(|capture| capture == name)
        };
        if !exists {
            return Err(Error::argument(Code::InvalidCapture));
        }
    }
    Ok(())
}

pub fn prepare(args: &TextReplace) -> Result<Option<Regex>> {
    if args.pattern.is_empty() {
        return Err(Error::argument(Code::InvalidPattern));
    }
    if args.regex {
        let regex = Regex::new(&args.pattern).map_err(|_| Error::argument(Code::InvalidPattern))?;
        validate_replacement(&regex, &args.replacement)?;
        Ok(Some(regex))
    } else {
        Ok(None)
    }
}

pub fn replace(
    args: TextReplace,
    regex: Option<Regex>,
    writer: &mut dyn Write,
    cancel: &Cancellation,
) -> Result<u8> {
    let mut reader = args.source.reader()?;
    let mut input = Vec::new();
    let mut buffer = [0; CHUNK];
    loop {
        let size = read(&mut *reader, &mut buffer, cancel)?;
        if size == 0 {
            break;
        }
        input.extend_from_slice(&buffer[..size]);
    }
    let input = String::from_utf8(input).map_err(|_| Error::runtime(Code::InvalidUtf8))?;
    let mut output = String::new();
    let mut end = 0;
    let mut matched = false;
    if let Some(regex) = regex {
        for captures in regex.captures_iter(&input) {
            cancel.check()?;
            let found = captures.get(0).unwrap();
            output.push_str(&input[end..found.start()]);
            captures.expand(&args.replacement, &mut output);
            end = found.end();
            matched = true;
            if args.first {
                break;
            }
        }
    } else {
        for (start, found) in input.match_indices(&args.pattern) {
            cancel.check()?;
            output.push_str(&input[end..start]);
            output.push_str(&args.replacement);
            end = start + found.len();
            matched = true;
            if args.first {
                break;
            }
        }
    }
    if !matched && args.require_match {
        return Err(Error::runtime(Code::NoMatch));
    }
    cancel.check()?;
    output.push_str(&input[end..]);
    write(writer, output.as_bytes())?;
    Ok(0)
}
