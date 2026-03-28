use std::borrow::Cow;

use serde_json::{Map, Number, Value};

use crate::{LlmJsonParseError, LlmJsonParseResult};

const MAX_DEPTH: usize = 512;

pub fn parse_lenient_json_value(input: &str) -> LlmJsonParseResult<Value> {
    if let Ok(data) = serde_json::from_str::<Value>(input) {
        return LlmJsonParseResult::Success { data };
    }
    iterate(input)
}

fn iterate(input: &str) -> LlmJsonParseResult<Value> {
    let source = match extract_markdown_code_block(input) {
        Some(content) => Cow::Owned(content),
        None => Cow::Borrowed(input),
    };
    let json_source = source.as_ref();
    let trimmed = json_source.trim();

    if trimmed.is_empty() {
        return LlmJsonParseResult::Failure {
            data: None,
            input: input.to_owned(),
            errors: vec![LlmJsonParseError {
                path: "$input".to_owned(),
                expected: "JSON value".to_owned(),
                description: "empty input".to_owned(),
            }],
        };
    }

    let parse_source = if starts_with_primitive(trimmed) {
        json_source
    } else if let Some(json_start) = find_json_start(json_source) {
        &json_source[json_start..]
    } else {
        let skipped = skip_comments_and_whitespace(json_source);
        if skipped.is_empty() || !starts_with_primitive(skipped) {
            return LlmJsonParseResult::Failure {
                data: None,
                input: input.to_owned(),
                errors: vec![LlmJsonParseError {
                    path: "$input".to_owned(),
                    expected: "JSON value".to_owned(),
                    description: json_source.to_owned(),
                }],
            };
        }
        skipped
    };

    let mut errors = Vec::new();
    let mut parser = LenientJsonParser::new(parse_source, &mut errors);
    let data = parser.parse();

    if errors.is_empty() {
        if let Some(value) = data {
            LlmJsonParseResult::Success { data: value }
        } else {
            LlmJsonParseResult::Failure {
                data: None,
                input: input.to_owned(),
                errors: vec![LlmJsonParseError {
                    path: "$input".to_owned(),
                    expected: "JSON value".to_owned(),
                    description: "unable to parse input".to_owned(),
                }],
            }
        }
    } else {
        LlmJsonParseResult::Failure {
            data,
            input: input.to_owned(),
            errors,
        }
    }
}

struct LenientJsonParser<'a> {
    chars: Vec<char>,
    pos: usize,
    depth: usize,
    errors: &'a mut Vec<LlmJsonParseError>,
}

impl<'a> LenientJsonParser<'a> {
    fn new(input: &str, errors: &'a mut Vec<LlmJsonParseError>) -> Self {
        Self {
            chars: input.chars().collect(),
            pos: 0,
            depth: 0,
            errors,
        }
    }

    fn parse(&mut self) -> Option<Value> {
        self.skip_whitespace();
        if self.pos >= self.chars.len() {
            return None;
        }
        self.parse_value("$input")
    }

    fn parse_value(&mut self, path: &str) -> Option<Value> {
        self.skip_whitespace();

        if self.pos >= self.chars.len() {
            return None;
        }

        if self.depth >= MAX_DEPTH {
            self.errors.push(LlmJsonParseError {
                path: path.to_owned(),
                expected: "value (max depth exceeded)".to_owned(),
                description: "maximum parser nesting depth exceeded".to_owned(),
            });
            return None;
        }

        match self.current_char() {
            Some('{') => self.parse_object(path),
            Some('[') => self.parse_array(path),
            Some('"') => Some(Value::String(self.parse_string(path))),
            Some('-') => Some(Value::Number(self.parse_number())),
            Some(ch) if ch.is_ascii_digit() => Some(Value::Number(self.parse_number())),
            Some(ch) if is_identifier_start(ch) => self.parse_keyword_or_identifier(path),
            Some('}') | Some(']') | Some(',') => None,
            Some(_) => {
                self.errors.push(LlmJsonParseError {
                    path: path.to_owned(),
                    expected: "JSON value (string, number, boolean, null, object, or array)"
                        .to_owned(),
                    description: self.get_error_context(),
                });
                self.pos += 1;
                None
            }
            None => None,
        }
    }

    fn parse_keyword_or_identifier(&mut self, path: &str) -> Option<Value> {
        let token = self.parse_identifier();

        match token.as_str() {
            "true" => return Some(Value::Bool(true)),
            "false" => return Some(Value::Bool(false)),
            "null" => return Some(Value::Null),
            _ => {}
        }

        let lower = token.to_ascii_lowercase();
        if lower == "yes" || lower == "y" || lower == "on" {
            return Some(Value::Bool(true));
        }
        if lower == "no" || lower == "off" {
            return Some(Value::Bool(false));
        }

        if "true".starts_with(token.as_str()) && !token.is_empty() {
            return Some(Value::Bool(true));
        }
        if "false".starts_with(token.as_str()) && !token.is_empty() {
            return Some(Value::Bool(false));
        }
        if "null".starts_with(token.as_str()) && token.len() >= 2 {
            return Some(Value::Null);
        }

        if self.current_char() == Some('"') {
            self.pos += 1;
            self.errors.push(LlmJsonParseError {
                path: path.to_owned(),
                expected: "quoted string".to_owned(),
                description: format!("missing opening quote for '{token}'"),
            });
            return Some(Value::String(token));
        }

        self.errors.push(LlmJsonParseError {
            path: path.to_owned(),
            expected: "JSON value (string, number, boolean, null, object, or array)".to_owned(),
            description: format!("unquoted string '{token}' - did you forget quotes?"),
        });
        self.skip_to_recovery_point();
        None
    }

    fn parse_object(&mut self, path: &str) -> Option<Value> {
        self.pos += 1;
        self.depth += 1;

        let mut result = Map::new();
        self.skip_whitespace();

        while self.pos < self.chars.len() {
            self.skip_whitespace();

            if self.pos >= self.chars.len() {
                break;
            }

            match self.current_char() {
                Some('}') => {
                    self.pos += 1;
                    self.depth -= 1;
                    return Some(Value::Object(result));
                }
                Some(',') => {
                    self.pos += 1;
                    self.skip_whitespace();
                    continue;
                }
                _ => {}
            }

            let key = match self.current_char() {
                Some('"') => self.parse_string(path),
                Some(ch) if is_identifier_start(ch) => self.parse_identifier(),
                _ => {
                    self.errors.push(LlmJsonParseError {
                        path: path.to_owned(),
                        expected: "string key".to_owned(),
                        description: self.get_error_context(),
                    });
                    self.depth -= 1;
                    return Some(Value::Object(result));
                }
            };

            self.skip_whitespace();
            if self.pos >= self.chars.len() {
                self.depth -= 1;
                return Some(Value::Object(result));
            }

            if self.current_char() != Some(':') {
                self.errors.push(LlmJsonParseError {
                    path: format!("{path}.{key}"),
                    expected: "':'".to_owned(),
                    description: self.get_error_context(),
                });
                self.depth -= 1;
                return Some(Value::Object(result));
            }
            self.pos += 1;

            self.skip_whitespace();
            if self.pos >= self.chars.len() {
                self.depth -= 1;
                return Some(Value::Object(result));
            }

            let value_path = format!("{path}.{key}");
            let value = self.parse_value(&value_path).unwrap_or(Value::Null);
            result.insert(key, value);

            self.skip_whitespace();
            if self.current_char() == Some(',') {
                self.pos += 1;
            }
        }

        self.depth -= 1;
        Some(Value::Object(result))
    }

    fn parse_array(&mut self, path: &str) -> Option<Value> {
        self.pos += 1;
        self.depth += 1;

        let mut result = Vec::new();
        let mut index = 0usize;

        self.skip_whitespace();

        while self.pos < self.chars.len() {
            self.skip_whitespace();

            if self.pos >= self.chars.len() {
                break;
            }

            match self.current_char() {
                Some(']') => {
                    self.pos += 1;
                    self.depth -= 1;
                    return Some(Value::Array(result));
                }
                Some(',') => {
                    self.pos += 1;
                    self.skip_whitespace();
                    continue;
                }
                _ => {}
            }

            let previous_pos = self.pos;
            let item_path = format!("{path}[{index}]");
            let value = self.parse_value(&item_path).unwrap_or(Value::Null);

            if self.pos == previous_pos && self.pos < self.chars.len() {
                self.pos += 1;
                continue;
            }

            result.push(value);
            index += 1;

            self.skip_whitespace();
            if self.current_char() == Some(',') {
                self.pos += 1;
            }
        }

        self.depth -= 1;
        Some(Value::Array(result))
    }

    fn parse_string(&mut self, _path: &str) -> String {
        self.pos += 1;
        let mut result = String::new();
        let mut escaped = false;

        while self.pos < self.chars.len() {
            let current = self.chars[self.pos];

            if escaped {
                match current {
                    '"' => result.push('"'),
                    '\\' => result.push('\\'),
                    '/' => result.push('/'),
                    'b' => result.push('\u{0008}'),
                    'f' => result.push('\u{000C}'),
                    'n' => result.push('\n'),
                    'r' => result.push('\r'),
                    't' => result.push('\t'),
                    'u' => {
                        if let Some(high) = self.read_hex4(self.pos + 1) {
                            self.pos += 4;

                            if (0xd800..=0xdbff).contains(&high)
                                && self.peek_char(1) == Some('\\')
                                && self.peek_char(2) == Some('u')
                                && let Some(low) = self.read_hex4(self.pos + 3)
                                && (0xdc00..=0xdfff).contains(&low)
                            {
                                let high_ten = u32::from(high - 0xd800);
                                let low_ten = u32::from(low - 0xdc00);
                                let codepoint = 0x10000 + ((high_ten << 10) | low_ten);

                                if let Some(ch) = char::from_u32(codepoint) {
                                    result.push(ch);
                                }
                                self.pos += 6;
                                escaped = false;
                                self.pos += 1;
                                continue;
                            }

                            if let Some(ch) = char::from_u32(u32::from(high)) {
                                result.push(ch);
                            } else {
                                result.push_str(&format!("\\u{high:04x}"));
                            }
                        } else {
                            let partial = self.collect_chars(self.pos + 1, 4);
                            result.push_str("\\u");
                            result.push_str(&partial);
                            self.pos += partial.chars().count();
                        }
                    }
                    other => result.push(other),
                }

                escaped = false;
                self.pos += 1;
                continue;
            }

            if current == '\\' {
                escaped = true;
                self.pos += 1;
                continue;
            }

            if current == '"' {
                self.pos += 1;
                return result;
            }

            result.push(current);
            self.pos += 1;
        }

        result
    }

    fn parse_number(&mut self) -> Number {
        let start = self.pos;

        if self.current_char() == Some('-') {
            self.pos += 1;
        }

        while matches!(self.current_char(), Some(ch) if ch.is_ascii_digit()) {
            self.pos += 1;
        }

        if self.current_char() == Some('.') {
            self.pos += 1;
            while matches!(self.current_char(), Some(ch) if ch.is_ascii_digit()) {
                self.pos += 1;
            }
        }

        if matches!(self.current_char(), Some('e') | Some('E')) {
            self.pos += 1;
            if matches!(self.current_char(), Some('+') | Some('-')) {
                self.pos += 1;
            }
            while matches!(self.current_char(), Some(ch) if ch.is_ascii_digit()) {
                self.pos += 1;
            }
        }

        let literal: String = self.chars[start..self.pos].iter().collect();
        number_from_literal(&literal)
    }

    fn parse_identifier(&mut self) -> String {
        let start = self.pos;
        while matches!(self.current_char(), Some(ch) if is_identifier_char(ch)) {
            self.pos += 1;
        }
        self.chars[start..self.pos].iter().collect()
    }

    fn skip_to_recovery_point(&mut self) {
        while let Some(ch) = self.current_char() {
            if matches!(ch, ',' | '}' | ']') {
                break;
            }
            self.pos += 1;
        }
    }

    fn skip_whitespace(&mut self) {
        loop {
            match self.current_char() {
                Some(ch) if ch.is_whitespace() => {
                    self.pos += 1;
                }
                Some('/') if self.peek_char(1) == Some('/') => {
                    self.pos += 2;
                    while let Some(ch) = self.current_char() {
                        if matches!(ch, '\n' | '\r') {
                            break;
                        }
                        self.pos += 1;
                    }
                }
                Some('/') if self.peek_char(1) == Some('*') => {
                    self.pos += 2;
                    let mut closed = false;
                    while self.pos + 1 < self.chars.len() {
                        if self.current_char() == Some('*') && self.peek_char(1) == Some('/') {
                            self.pos += 2;
                            closed = true;
                            break;
                        }
                        self.pos += 1;
                    }
                    if !closed {
                        self.pos = self.chars.len();
                    }
                }
                _ => break,
            }
        }
    }

    fn get_error_context(&self) -> String {
        let start = self.pos.saturating_sub(10);
        let end = self.pos.saturating_add(20).min(self.chars.len());
        let before: String = self.chars[start..self.pos].iter().collect();
        let after: String = self.chars[self.pos..end].iter().collect();
        let left = if start > 0 { "..." } else { "" };
        let right = if end < self.chars.len() { "..." } else { "" };
        format!("{left}{before}→{after}{right}")
    }

    fn read_hex4(&self, start: usize) -> Option<u16> {
        if start + 4 > self.chars.len() {
            return None;
        }

        let mut value = 0u16;
        for index in start..start + 4 {
            let digit = self.chars[index].to_digit(16)? as u16;
            value = (value << 4) | digit;
        }
        Some(value)
    }

    fn collect_chars(&self, start: usize, count: usize) -> String {
        self.chars.iter().skip(start).take(count).copied().collect()
    }

    fn current_char(&self) -> Option<char> {
        self.chars.get(self.pos).copied()
    }

    fn peek_char(&self, offset: usize) -> Option<char> {
        self.chars.get(self.pos + offset).copied()
    }
}

fn number_from_literal(literal: &str) -> Number {
    let is_float = literal
        .as_bytes()
        .iter()
        .any(|byte| matches!(byte, b'.' | b'e' | b'E'));

    if !is_float {
        if let Ok(value) = literal.parse::<i64>() {
            return Number::from(value);
        }
        if let Ok(value) = literal.parse::<u64>() {
            return Number::from(value);
        }
    }

    if let Ok(value) = literal.parse::<f64>()
        && let Some(number) = Number::from_f64(value)
    {
        return number;
    }

    Number::from(0)
}

fn is_identifier_start(ch: char) -> bool {
    ch.is_ascii_alphabetic() || matches!(ch, '_' | '$')
}

fn is_identifier_char(ch: char) -> bool {
    ch.is_ascii_alphanumeric() || matches!(ch, '_' | '$')
}

fn extract_markdown_code_block(input: &str) -> Option<String> {
    let code_block_start = input.find("```json")?;

    if let Some(first) = input.trim_start().chars().next()
        && matches!(first, '{' | '[' | '"')
    {
        return None;
    }

    let bytes = input.as_bytes();
    let mut content_start = code_block_start + "```json".len();

    while content_start < bytes.len() && !matches!(bytes[content_start], b'\n' | b'\r') {
        content_start += 1;
    }
    if content_start >= bytes.len() {
        return None;
    }

    if bytes[content_start] == b'\r' {
        content_start += 1;
    }
    if content_start < bytes.len() && bytes[content_start] == b'\n' {
        content_start += 1;
    }

    if let Some(end_offset) = input[content_start..].find("```") {
        return Some(input[content_start..content_start + end_offset].to_owned());
    }

    Some(input[content_start..].to_owned())
}

fn find_json_start(input: &str) -> Option<usize> {
    let bytes = input.as_bytes();
    let mut pos = 0usize;

    while pos < bytes.len() {
        let byte = bytes[pos];

        if matches!(byte, b'{' | b'[') {
            return Some(pos);
        }

        if byte == b'/' && pos + 1 < bytes.len() && bytes[pos + 1] == b'/' {
            pos += 2;
            while pos < bytes.len() && !matches!(bytes[pos], b'\n' | b'\r') {
                pos += 1;
            }
            continue;
        }

        if byte == b'/' && pos + 1 < bytes.len() && bytes[pos + 1] == b'*' {
            pos += 2;
            let mut closed = false;
            while pos + 1 < bytes.len() {
                if bytes[pos] == b'*' && bytes[pos + 1] == b'/' {
                    pos += 2;
                    closed = true;
                    break;
                }
                pos += 1;
            }
            if !closed {
                pos = bytes.len();
            }
            continue;
        }

        if byte == b'"' {
            pos += 1;
            while pos < bytes.len() {
                if bytes[pos] == b'\\' {
                    pos += 2;
                    continue;
                }
                if bytes[pos] == b'"' {
                    pos += 1;
                    break;
                }
                pos += 1;
            }
            continue;
        }

        pos += 1;
    }

    None
}

fn skip_comments_and_whitespace(input: &str) -> &str {
    let bytes = input.as_bytes();
    let mut pos = 0usize;

    while pos < bytes.len() {
        let byte = bytes[pos];

        if matches!(byte, b' ' | b'\t' | b'\n' | b'\r') {
            pos += 1;
            continue;
        }

        if byte == b'/' && pos + 1 < bytes.len() && bytes[pos + 1] == b'/' {
            pos += 2;
            while pos < bytes.len() && !matches!(bytes[pos], b'\n' | b'\r') {
                pos += 1;
            }
            continue;
        }

        if byte == b'/' && pos + 1 < bytes.len() && bytes[pos + 1] == b'*' {
            pos += 2;
            let mut closed = false;
            while pos + 1 < bytes.len() {
                if bytes[pos] == b'*' && bytes[pos + 1] == b'/' {
                    pos += 2;
                    closed = true;
                    break;
                }
                pos += 1;
            }
            if !closed {
                pos = bytes.len();
            }
            continue;
        }

        break;
    }

    &input[pos..]
}

fn starts_with_primitive(input: &str) -> bool {
    let mut chars = input.chars();
    let Some(first) = chars.next() else {
        return false;
    };

    if matches!(first, '"' | '-') || first.is_ascii_digit() {
        return true;
    }

    if input.starts_with("true") || input.starts_with("false") || input.starts_with("null") {
        return true;
    }

    if "true".starts_with(input) || "false".starts_with(input) {
        return true;
    }

    if input.len() >= 2 && "null".starts_with(input) {
        return true;
    }

    let lower = input.to_ascii_lowercase();
    matches!(lower.as_str(), "yes" | "y" | "on" | "no" | "off")
}
