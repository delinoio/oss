use std::collections::BTreeMap;

use crate::config_runtime::{Cancellation, Error, Failure, Output, Result};

pub type Values = BTreeMap<String, String>;

struct Cursor<'a> {
    text: &'a str,
    offset: usize,
    line: usize,
    column: usize,
    cancel: &'a Cancellation,
}
impl<'a> Cursor<'a> {
    fn peek(&self) -> Option<u8> {
        self.text.as_bytes().get(self.offset).copied()
    }

    fn advance(&mut self) -> Result<()> {
        let ch = self.text[self.offset..]
            .chars()
            .next()
            .ok_or(Failure::Internal)?;
        let next = self.offset + ch.len_utf8();
        // Character boundaries can skip a byte checkpoint; check crossings too.
        if self.offset == 0 || self.offset / 4096 != next / 4096 {
            self.cancel.check()?;
        }
        if ch == '\n' {
            self.line += 1;
            self.column = 1;
        } else {
            self.column += 1;
        }
        self.offset = next;
        Ok(())
    }

    fn space(&mut self) -> Result<()> {
        while matches!(self.peek(), Some(b' ' | b'\t' | b'\r')) {
            self.advance()?;
        }
        Ok(())
    }

    fn comment(&mut self) -> Result<()> {
        while !matches!(self.peek(), None | Some(b'\n')) {
            self.advance()?;
        }
        Ok(())
    }

    fn key(&mut self) -> Result<&'a str> {
        let start = self.offset;
        if !self
            .peek()
            .is_some_and(|b| b.is_ascii_alphabetic() || b == b'_')
        {
            return Err(self.error());
        }
        while self
            .peek()
            .is_some_and(|b| b.is_ascii_alphanumeric() || b == b'_')
        {
            self.advance()?;
        }
        Ok(&self.text[start..self.offset])
    }

    fn error(&self) -> Error {
        Error::from(Failure::DotenvSyntax).at(self.line, self.column)
    }
}

pub fn parse(text: &str, values: &mut Values, cancel: &Cancellation) -> Result<()> {
    let mut c = Cursor {
        text,
        offset: 0,
        line: 1,
        column: 1,
        cancel,
    };
    while c.peek().is_some() {
        c.space()?;
        if c.peek() == Some(b'#') {
            c.comment()?;
        }
        if matches!(c.peek(), None | Some(b'\n')) {
            if c.peek().is_some() {
                c.advance()?;
            }
            continue;
        }
        let mut key = c.key()?;
        // Node recognizes the prefix only with an immediate ASCII space.
        let export_prefix = key == "export" && c.peek() == Some(b' ');
        c.space()?;
        // `export` remains an ordinary key when followed by the assignment sign.
        if export_prefix && c.peek() != Some(b'=') {
            key = c.key()?;
            c.space()?;
        }
        if c.peek() != Some(b'=') {
            return Err(c.error());
        }
        c.advance()?;
        c.space()?;
        let start = c.offset;
        let end;
        if let Some(quote @ (b'\'' | b'"')) = c.peek() {
            c.advance()?;
            // Quotes delimit tokens; escapes and internal CRLF are literal bytes.
            // Node's dotenv baseline does not apply shell/JSON escape decoding.
            while c.peek() != Some(quote) {
                if c.peek().is_none() {
                    return Err(c.error());
                }
                c.advance()?;
            }
            c.advance()?;
            end = c.offset;
            c.space()?;
            if !matches!(c.peek(), None | Some(b'#' | b'\n')) {
                return Err(c.error());
            }
        } else {
            while !matches!(c.peek(), None | Some(b'\n' | b'#')) {
                c.advance()?;
            }
            end = text[start..c.offset]
                .trim_end_matches([' ', '\t', '\r'])
                .len()
                + start;
        }
        if c.peek() == Some(b'#') {
            c.comment()?;
        }
        values.insert(key.to_owned(), text[start..end].to_owned());
    }
    cancel.check()
}

pub fn render(values: Values, include_values: bool, cancel: &Cancellation) -> Result<Vec<u8>> {
    let mut output = Output::new();
    for (key, value) in values {
        cancel.check()?;
        output.push(&key)?;
        if include_values {
            output.push("=")?;
            output.push(&value)?;
        }
        output.push("\n")?;
    }
    Ok(output.bytes)
}
