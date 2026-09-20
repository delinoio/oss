//! YAML syntax events stay separate from Core resolution and bounded emission.
//! Numbers never pass through machine integers or floating-point conversion.
use std::{
    collections::{BTreeMap, HashMap},
    rc::Rc,
    sync::LazyLock,
};

use regex::Regex;
use yaml_rust2::{
    parser::{Event, Parser, Tag},
    scanner::{Marker, Scanner, TScalarStyle, TokenType},
};

use crate::runtime::{Cancellation, Error, Failure, Output, Result, LIMIT};

const DEPTH: usize = 128;
static INTEGER: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"\A(?:[-+]?[0-9]+|0o[0-7]+|0x[0-9a-fA-F]+)\z").unwrap());
static FLOAT: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"\A(?:[-+]?(?:\.[0-9]+|[0-9]+(?:\.[0-9]*)?)(?:[eE][-+]?[0-9]+)?|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))\z").unwrap()
});

#[derive(Clone)]
enum Scalar {
    String(String),
    Atom(String),
    Merge,
}
#[derive(Clone)]
enum Raw {
    Scalar(Scalar),
    Sequence(Vec<usize>),
    Mapping(Vec<usize>),
    Alias(usize),
}
struct Node {
    raw: Raw,
    mark: Marker,
}
enum Kind {
    Scalar(Scalar, String),
    Sequence(Vec<Rc<Value>>),
    Mapping(BTreeMap<String, Rc<Value>>),
}
struct Value {
    kind: Kind,
    size: usize,
    lines: usize,
    depth: usize,
}
impl Value {
    fn block(&self) -> bool {
        match &self.kind {
            Kind::Sequence(v) => !v.is_empty(),
            Kind::Mapping(v) => !v.is_empty(),
            _ => false,
        }
    }
}
fn at(kind: Failure, mark: Marker) -> Error {
    Error::from(kind).at(mark.line(), mark.col() + 1)
}

// The dependency intentionally tolerates version directives. Check scanner
// tokens (not source lines, which can be quoted/block-scalar content) until it
// exposes a strict-version option. No dependency error text is ever forwarded.
fn versions(text: &str, cancel: &Cancellation) -> Result<()> {
    let mut scanner = Scanner::new(text.chars().take_while(|_| cancel.code() == 0));
    let mut document = 1;
    let mut started = false;
    for token in scanner.by_ref() {
        cancel.check()?;
        match token.1 {
            TokenType::VersionDirective(1, 2) => (),
            TokenType::VersionDirective(..) => {
                return Err(at(Failure::YamlVersion, token.0).document(if started {
                    document + 1
                } else {
                    document
                }))
            }
            TokenType::DocumentStart => {
                if started {
                    document += 1;
                }
                started = true;
            }
            _ => (),
        }
    }
    cancel.check()?;
    if let Some(error) = scanner.get_error() {
        return Err(at(Failure::YamlSyntax, *error.marker()).document(document));
    }
    Ok(())
}

fn tag_name(tag: Option<&Tag>) -> Result<Option<&str>> {
    match tag {
        None => Ok(None),
        Some(tag) if tag.handle == "tag:yaml.org,2002:" => Ok(Some(&tag.suffix)),
        Some(tag) if tag.handle.is_empty() && tag.suffix.starts_with("tag:yaml.org,2002:") => {
            Ok(tag.suffix.strip_prefix("tag:yaml.org,2002:"))
        }
        // The non-specific ! tag disables implicit scalar resolution.
        Some(tag) if tag.handle.is_empty() && tag.suffix == "!" => Ok(Some("str")),
        _ => Err(Failure::YamlTag.into()),
    }
}
fn scalar(text: String, style: TScalarStyle, tag: Option<&Tag>) -> Result<Scalar> {
    let tag = tag_name(tag)?;
    let null = matches!(text.as_str(), "" | "~" | "null" | "Null" | "NULL");
    let boolean = matches!(
        text.as_str(),
        "true" | "True" | "TRUE" | "false" | "False" | "FALSE"
    );
    let integer = INTEGER.is_match(&text);
    let float = FLOAT.is_match(&text);
    let resolved = match tag {
        Some("str") => "str",
        Some("null") if null => "null",
        Some("bool") if boolean => "bool",
        Some("int") if integer => "int",
        Some("float") if float => "float",
        Some("merge") if text == "<<" => "merge",
        Some(_) => return Err(Failure::YamlTag.into()),
        None if style != TScalarStyle::Plain => "str",
        None if text == "<<" => "merge",
        None if null => "null",
        None if boolean => "bool",
        None if integer => "int",
        None if float => "float",
        None => "str",
    };
    Ok(match resolved {
        "null" => Scalar::Atom("null".into()),
        "bool" => Scalar::Atom(text.to_ascii_lowercase()),
        "int" => Scalar::Atom(text),
        // An explicit tag preserves !!float 1 as float. Keeping the exact
        // decimal lexeme avoids both precision loss and huge exponent expansion.
        "float" => Scalar::Atom(format!("!!float {text}")),
        "merge" => Scalar::Merge,
        _ => Scalar::String(text),
    })
}
fn collection_tag(tag: Option<&Tag>, expected: &str) -> Result<()> {
    if tag.is_some_and(|tag| tag.handle.is_empty() && tag.suffix == "!") {
        return Ok(());
    }
    match tag_name(tag)? {
        None => Ok(()),
        Some(name) if name == expected => Ok(()),
        _ => Err(Failure::YamlTag.into()),
    }
}

fn quote(text: &str, cancel: &Cancellation) -> Result<String> {
    let mut out = Output::new();
    out.push("\"")?;
    for (index, ch) in text.chars().enumerate() {
        if index.is_multiple_of(4096) {
            cancel.check()?;
        }
        match ch {
            '"' => out.push("\\\"")?,
            '\\' => out.push("\\\\")?,
            '\n' => out.push("\\n")?,
            '\r' => out.push("\\r")?,
            '\t' => out.push("\\t")?,
            ch if ch.is_control() || matches!(ch, '\u{2028}' | '\u{2029}' | '\u{feff}') => {
                out.push(&format!("\\u{:04x}", ch as u32))?
            }
            ch => out.push(ch.encode_utf8(&mut [0; 4]))?,
        }
    }
    out.push("\"")?;
    Ok(String::from_utf8(out.bytes).expect("UTF-8 encoder"))
}

struct Graph<'a> {
    nodes: Vec<Node>,
    done: Vec<Option<Rc<Value>>>,
    active: Vec<bool>,
    cancel: &'a Cancellation,
}
impl Graph<'_> {
    fn resolve(&mut self, id: usize) -> Result<Rc<Value>> {
        self.cancel.check()?;
        if let Some(value) = &self.done[id] {
            return Ok(value.clone());
        }
        let mark = self.nodes[id].mark;
        if self.active[id] {
            return Err(at(Failure::Cycle, mark));
        }
        self.active[id] = true;
        let raw = self.nodes[id].raw.clone();
        let kind = match raw {
            Raw::Alias(target) => {
                let result = self.resolve(target)?;
                self.active[id] = false;
                self.done[id] = Some(result.clone());
                return Ok(result);
            }
            Raw::Scalar(scalar) => {
                let encoded = match &scalar {
                    Scalar::String(value) => quote(value, self.cancel)?,
                    Scalar::Atom(value) => value.clone(),
                    Scalar::Merge => quote("<<", self.cancel)?,
                };
                Kind::Scalar(scalar, encoded)
            }
            Raw::Sequence(ids) => {
                let mut values = Vec::new();
                for child in ids {
                    let value = self.resolve(child)?;
                    values.push(value);
                }
                Kind::Sequence(values)
            }
            Raw::Mapping(ids) => {
                let mut explicit = BTreeMap::new();
                let mut merges = Vec::new();
                for pair in ids.chunks_exact(2) {
                    let key = self.resolve(pair[0])?;
                    let value = self.resolve(pair[1])?;
                    match &key.kind {
                        Kind::Scalar(Scalar::Merge, _) => {
                            merges.push((value, self.nodes[pair[1]].mark))
                        }
                        Kind::Scalar(Scalar::String(key), _) => {
                            if explicit.insert(key.clone(), value).is_some() {
                                return Err(at(Failure::DuplicateKey, self.nodes[pair[0]].mark));
                            }
                        }
                        _ => return Err(at(Failure::YamlKey, self.nodes[pair[0]].mark)),
                    }
                }
                let mut result = BTreeMap::new();
                for (merge, mark) in merges {
                    match &merge.kind {
                        Kind::Mapping(map) => self.merge(&mut result, map)?,
                        Kind::Sequence(sequence) => {
                            for value in sequence {
                                if let Kind::Mapping(map) = &value.kind {
                                    self.merge(&mut result, map)?;
                                } else {
                                    return Err(at(Failure::MergeOperand, mark));
                                }
                            }
                        }
                        _ => return Err(at(Failure::MergeOperand, mark)),
                    }
                    self.cancel.check()?;
                }
                result.extend(explicit);
                Kind::Mapping(result)
            }
        };
        let mut size = 0usize;
        let mut lines = 0usize;
        let mut depth = 0usize;
        // Calculate the exact block encoding size on the shared graph. Saturate
        // counters, never expand aliases to owned copies. Only final documents
        // consume the output budget, so shadowed merge values are still valid.
        let mut add = |prefix: usize, child: &Value| {
            let extra = if child.block() {
                child.lines.saturating_mul(2).saturating_add(1)
            } else {
                1
            };
            size = size
                .saturating_add(prefix)
                .saturating_add(extra)
                .saturating_add(child.size);
            lines = lines
                .saturating_add(child.lines)
                .saturating_add(usize::from(child.block()));
            depth = depth.max(child.depth.saturating_add(1));
        };
        match &kind {
            Kind::Scalar(_, encoded) => {
                size = encoded.len().saturating_add(1);
                lines = 1;
            }
            Kind::Sequence(values) => {
                for value in values {
                    add(1, value);
                }
            }
            Kind::Mapping(values) => {
                for (key, value) in values {
                    add(quote(key, self.cancel)?.len() + 1, value);
                }
            }
        }
        if matches!(&kind, Kind::Sequence(v) if v.is_empty())
            || matches!(&kind, Kind::Mapping(v) if v.is_empty())
        {
            size = 3;
            lines = 1;
            depth = 1;
        }
        if depth > DEPTH {
            return Err(at(Failure::Depth, mark));
        }
        let value = Rc::new(Value {
            kind,
            size,
            lines,
            depth,
        });
        self.active[id] = false;
        self.done[id] = Some(value.clone());
        Ok(value)
    }

    fn merge(
        &self,
        result: &mut BTreeMap<String, Rc<Value>>,
        map: &BTreeMap<String, Rc<Value>>,
    ) -> Result<()> {
        for (key, value) in map {
            self.cancel.check()?;
            result.entry(key.clone()).or_insert_with(|| value.clone());
        }
        Ok(())
    }
}

fn emit(value: &Value, indent: usize, output: &mut Output, cancel: &Cancellation) -> Result<()> {
    cancel.check()?;
    let padding = " ".repeat(indent);
    match &value.kind {
        Kind::Scalar(_, encoded) => {
            output.push(&padding)?;
            output.push(encoded)?;
            output.push("\n")?;
        }
        Kind::Sequence(values) if values.is_empty() => {
            output.push(&padding)?;
            output.push("[]\n")?;
        }
        Kind::Mapping(values) if values.is_empty() => {
            output.push(&padding)?;
            output.push("{}\n")?;
        }
        Kind::Sequence(values) => {
            for value in values {
                output.push(&padding)?;
                output.push("-")?;
                emit_child(value, indent, output, cancel)?;
            }
        }
        Kind::Mapping(values) => {
            for (key, value) in values {
                cancel.check()?;
                output.push(&padding)?;
                output.push(&quote(key, cancel)?)?;
                output.push(":")?;
                emit_child(value, indent, output, cancel)?;
            }
        }
    }
    Ok(())
}
fn emit_child(
    value: &Value,
    indent: usize,
    output: &mut Output,
    cancel: &Cancellation,
) -> Result<()> {
    if value.block() {
        output.push("\n")?;
        emit(value, indent + 2, output, cancel)
    } else {
        output.push(" ")?;
        emit(value, 0, output, cancel)
    }
}

pub fn normalize(text: &str, cancel: &Cancellation) -> Result<Vec<u8>> {
    versions(text, cancel)?;
    let mut parser = Parser::new(text.chars().take_while(|_| cancel.code() == 0));
    let mut nodes = Vec::<Node>::new();
    let mut stack = Vec::<usize>::new();
    let mut anchors = HashMap::new();
    let mut root = None;
    let mut documents = 0;
    let mut output = Output::new();
    loop {
        cancel.check()?;
        let event = parser.next_token();
        cancel.check()?;
        let (event, mark) =
            event.map_err(|e| at(Failure::YamlSyntax, *e.marker()).document(documents + 1))?;
        let make = |kind| at(kind, mark).document(documents + 1);
        let (raw, anchor, collection) = match event {
            Event::StreamStart | Event::Nothing => continue,
            Event::StreamEnd => break,
            Event::DocumentStart => {
                anchors.clear();
                continue;
            }
            Event::DocumentEnd => {
                if let Some(root) = root.take() {
                    let count = nodes.len();
                    let mut graph = Graph {
                        nodes: std::mem::take(&mut nodes),
                        done: vec![None; count],
                        active: vec![false; count],
                        cancel,
                    };
                    let value = graph.resolve(root).map_err(|e| e.document(documents + 1))?;
                    if value.depth > DEPTH {
                        return Err(make(Failure::Depth));
                    }
                    let markers = if documents == 1 {
                        8
                    } else if documents > 1 {
                        4
                    } else {
                        0
                    };
                    if value.size
                        > LIMIT
                            .saturating_sub(output.bytes.len())
                            .saturating_sub(markers)
                    {
                        return Err(make(Failure::OutputLimit));
                    }
                    if documents == 1 {
                        output.bytes.splice(..0, b"---\n".iter().copied());
                    }
                    if documents >= 1 {
                        output.push("---\n")?;
                    }
                    emit(&value, 0, &mut output, cancel).map_err(|e| e.document(documents + 1))?;
                    documents += 1;
                }
                continue;
            }
            Event::MappingEnd | Event::SequenceEnd => {
                stack.pop();
                continue;
            }
            Event::Scalar(text, style, anchor, tag) => (
                Raw::Scalar(scalar(text, style, tag.as_ref()).map_err(|e| make(e.kind))?),
                anchor,
                false,
            ),
            Event::Alias(anchor) => (
                Raw::Alias(*anchors.get(&anchor).ok_or_else(|| make(Failure::Alias))?),
                0,
                false,
            ),
            Event::SequenceStart(anchor, tag) => {
                collection_tag(tag.as_ref(), "seq").map_err(|e| make(e.kind))?;
                (Raw::Sequence(Vec::new()), anchor, true)
            }
            Event::MappingStart(anchor, tag) => {
                collection_tag(tag.as_ref(), "map").map_err(|e| make(e.kind))?;
                (Raw::Mapping(Vec::new()), anchor, true)
            }
        };
        if collection && stack.len() >= DEPTH {
            return Err(make(Failure::Depth));
        }
        let id = nodes.len();
        nodes.push(Node { raw, mark });
        if anchor != 0 {
            anchors.insert(anchor, id);
        }
        if let Some(&parent) = stack.last() {
            match &mut nodes[parent].raw {
                Raw::Sequence(v) | Raw::Mapping(v) => v.push(id),
                _ => unreachable!(),
            }
        } else {
            root = Some(id);
        }
        if collection {
            stack.push(id);
        }
    }
    Ok(output.bytes)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn exact_alias_output_boundary_counts_indentation_and_document_markers() {
        // A doubles on every level. The graph stays tiny even when its expanded
        // encoding is exactly the output ceiling. Tune one scalar's byte length
        // using the independently counted expected block encoding.
        let cancel = Cancellation::default();
        let mut text = "a0: &a0 x\n".to_owned();
        for i in 1..18 {
            text.push_str(&format!("a{i}: &a{i} [*a{}, *a{}]\n", i - 1, i - 1));
        }
        let first = normalize(&text, &cancel).unwrap();
        let fill = LIMIT - first.len() - "\"padding\": \"\"\n".len();
        text.push_str("padding: '");
        text.extend(std::iter::repeat_n('x', fill));
        text.push_str("'\n");
        assert_eq!(normalize(&text, &cancel).unwrap().len(), LIMIT);
        text.insert(text.len() - 2, 'x');
        assert_eq!(
            normalize(&text, &cancel).unwrap_err().kind,
            Failure::OutputLimit
        );
    }
    #[test]
    fn expanded_depth_is_checked_even_when_source_is_shallow() {
        let cancel = Cancellation::default();
        let mut text = "a0: &a0 x\n".to_owned();
        for i in 1..129 {
            text.push_str(&format!("a{i}: &a{i} [*a{}]\n", i - 1));
        }
        assert_eq!(normalize(&text, &cancel).unwrap_err().kind, Failure::Depth);
    }
}
