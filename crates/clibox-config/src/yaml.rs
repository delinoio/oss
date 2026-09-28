//! YAML syntax events stay separate from Core resolution and bounded emission.
//! Numbers never pass through machine integers or floating-point conversion.
use std::{
    collections::{BTreeMap, HashMap},
    rc::Rc,
    sync::LazyLock,
};

use imbl::{ordmap::DiffItem, shared_ptr::RcK, GenericOrdMap};
use regex::Regex;
use yaml_rust2::{
    parser::{Event, Parser, Tag},
    scanner::{Marker, Scanner, TScalarStyle, TokenType},
};

use crate::config_runtime::{Cancellation, Error, Failure, Output, Result, LIMIT};

// Keep structural sharing local to this single-threaded parser.
type OrdMap<K, V> = GenericOrdMap<K, V, RcK>;

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
    Scalar(Scalar),
    Sequence(Vec<Rc<Value>>),
    Mapping(Mapping),
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

#[derive(Clone)]
struct Entry {
    value: Rc<Value>,
    prefix: usize,
}
impl PartialEq for Entry {
    fn eq(&self, other: &Self) -> bool {
        // Diffing maps must not recursively compare their expanded values.
        Rc::ptr_eq(&self.value, &other.value)
    }
}
impl Entry {
    fn weight(&self) -> (u64, u64) {
        let block = usize::from(self.value.block());
        let size = self
            .prefix
            .saturating_add(self.value.size)
            .saturating_add(self.value.lines.saturating_mul(2 * block).saturating_add(1));
        let lines = self.value.lines.saturating_add(block);
        (size.min(LIMIT + 1) as u64, lines.min(LIMIT + 1) as u64)
    }
}

#[derive(Clone, Default)]
struct Mapping {
    entries: OrdMap<Rc<str>, Entry>,
    depths: OrdMap<usize, usize>,
    size: u64,
    lines: u64,
}
impl Mapping {
    fn is_empty(&self) -> bool {
        self.entries.is_empty()
    }

    fn insert(&mut self, key: Rc<str>, entry: Entry) {
        if let Some(old) = self.entries.insert(key, entry.clone()) {
            let (size, lines) = old.weight();
            self.size -= size;
            self.lines -= lines;
            let count = self.depths[&old.value.depth];
            if count == 1 {
                self.depths.remove(&old.value.depth);
            } else {
                self.depths.insert(old.value.depth, count - 1);
            }
        }
        let (size, lines) = entry.weight();
        // Each contribution is capped independently, not the aggregate. This
        // keeps subtraction exact when an oversized value is shadowed later.
        // At most LIMIT source bytes can introduce distinct keys; multiplying
        // that count by LIMIT + 1 fits u64 even on a 32-bit host.
        self.size += size;
        self.lines += lines;
        let count = self.depths.get(&entry.value.depth).copied().unwrap_or(0);
        self.depths.insert(entry.value.depth, count + 1);
    }

    fn merge(&mut self, other: &Self, cancel: &Cancellation) -> Result<()> {
        cancel.check()?;
        if self.is_empty() {
            *self = other.clone();
            return Ok(());
        }
        // Persistent trees share unchanged branches and key strings. Diff also
        // skips shared branches, so repeated/near-identical merge operands do
        // not rewalk every inherited key. Earlier operands retain precedence.
        let prior = self.entries.clone();
        for change in prior.diff(&other.entries) {
            cancel.check()?;
            if let DiffItem::Add(key, entry) = change {
                self.insert(key.clone(), entry.clone());
            }
        }
        Ok(())
    }
}
fn at(kind: Failure, mark: Marker) -> Error {
    Error::from(kind).at(mark.line(), mark.col() + 1)
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum CoreTag {
    String,
    Null,
    Bool,
    Integer,
    Float,
    Merge,
    Sequence,
    Mapping,
    NonSpecific,
}
impl CoreTag {
    fn named(name: &str) -> Option<Self> {
        Some(match name {
            "str" => Self::String,
            "null" => Self::Null,
            "bool" => Self::Bool,
            "int" => Self::Integer,
            "float" => Self::Float,
            "merge" => Self::Merge,
            "seq" => Self::Sequence,
            "map" => Self::Mapping,
            _ => return None,
        })
    }

    fn code(self) -> char {
        match self {
            Self::String => 's',
            Self::Null => 'n',
            Self::Bool => 'b',
            Self::Integer => 'i',
            Self::Float => 'f',
            Self::Merge => 'g',
            Self::Sequence => 'q',
            Self::Mapping => 'm',
            Self::NonSpecific => '!',
        }
    }
}
struct Edit {
    start: usize,
    end: usize,
    tag: Option<CoreTag>,
}

// yaml-rust2 tolerates version directives and drops earlier %TAG declarations
// while processing a directive block. Validate/resolve scanner tokens ourselves
// and present private short tags to its grammar parser. Replace directives with
// comments and pad tag tokens to the same character width, preserving all
// source positions. Remove this adapter when the dependency offers strict
// versions and correct document-local directive handling. Scalars are never
// text-rewritten.
fn prepare_tags(text: &str, cancel: &Cancellation) -> Result<Vec<Edit>> {
    let mut scanner = Scanner::new(text.chars().take_while(|_| cancel.code() == 0));
    let mut chars = text.chars().enumerate().peekable();
    let mut edits = Vec::new();
    let mut directives = HashMap::<String, String>::new();
    let mut document = 0;
    let mut in_document = false;
    let mut pending = false;
    let mut version_seen = false;
    for token in scanner.by_ref() {
        cancel.check()?;
        let mark = token.0;
        match token.1 {
            TokenType::StreamStart(_) => continue,
            TokenType::StreamEnd => {
                if pending {
                    return Err(at(Failure::YamlSyntax, mark).document(document + 1));
                }
                continue;
            }
            TokenType::VersionDirective(major, minor) => {
                if in_document || version_seen {
                    return Err(at(Failure::YamlSyntax, mark).document(document + 1));
                }
                if (major, minor) != (1, 2) {
                    return Err(at(Failure::YamlVersion, mark).document(document + 1));
                }
                if !pending {
                    directives.clear();
                }
                pending = true;
                version_seen = true;
                continue;
            }
            TokenType::TagDirective(handle, prefix) => {
                if in_document {
                    return Err(at(Failure::YamlSyntax, mark).document(document + 1));
                }
                if !pending {
                    directives.clear();
                }
                pending = true;
                if !handle.is_empty() && directives.insert(handle, prefix).is_some() {
                    return Err(at(Failure::YamlSyntax, mark).document(document + 1));
                }
                edits.push(Edit {
                    start: mark.index(),
                    end: mark.index() + 1,
                    tag: None,
                });
                continue;
            }
            TokenType::DocumentStart => {
                if !pending {
                    directives.clear();
                }
                pending = false;
                version_seen = false;
                document += 1;
                in_document = true;
                continue;
            }
            TokenType::DocumentEnd => {
                if pending {
                    return Err(at(Failure::YamlSyntax, mark).document(document + 1));
                }
                directives.clear();
                in_document = false;
                continue;
            }
            _ => (),
        }
        if pending {
            return Err(at(Failure::YamlSyntax, mark).document(document + 1));
        }
        if !in_document {
            document += 1;
            in_document = true;
        }
        if let TokenType::Tag(handle, suffix) = token.1 {
            let tag = if handle.is_empty() && suffix == "!" {
                CoreTag::NonSpecific
            } else {
                let prefix =
                    directives
                        .get(&handle)
                        .map(String::as_str)
                        .unwrap_or(if handle == "!!" {
                            "tag:yaml.org,2002:"
                        } else {
                            &handle
                        });
                let uri = format!("{prefix}{suffix}");
                uri.strip_prefix("tag:yaml.org,2002:")
                    .and_then(CoreTag::named)
                    .ok_or_else(|| at(Failure::YamlTag, mark).document(document))?
            };
            while chars.peek().is_some_and(|(index, _)| *index < mark.index()) {
                if chars
                    .peek()
                    .is_some_and(|(index, _)| index.is_multiple_of(4096))
                {
                    cancel.check()?;
                }
                chars.next();
            }
            let start = chars.next().ok_or_else(|| at(Failure::YamlSyntax, mark))?.0;
            let verbatim = chars.peek().is_some_and(|(_, ch)| *ch == '<');
            let mut end = start + 1;
            if verbatim {
                for (index, ch) in chars.by_ref() {
                    end = index + 1;
                    if ch == '>' {
                        break;
                    }
                }
            } else {
                while let Some(&(index, ch)) = chars.peek() {
                    if ch.is_ascii_whitespace() || matches!(ch, ',' | '[' | ']' | '{' | '}') {
                        break;
                    }
                    end = index + 1;
                    chars.next();
                }
            }
            edits.push(Edit {
                start,
                end,
                tag: Some(tag),
            });
        }
    }
    cancel.check()?;
    // Grammar parsing reports scanner failures with the exact document ordinal.
    Ok(edits)
}
fn tag_name(tag: Option<&Tag>) -> Result<Option<CoreTag>> {
    Ok(match tag {
        None => None,
        Some(tag) if tag.handle.is_empty() && tag.suffix == "!" => Some(CoreTag::NonSpecific),
        Some(tag) if tag.handle == "!" => Some(match tag.suffix.as_str() {
            "s" => CoreTag::String,
            "n" => CoreTag::Null,
            "b" => CoreTag::Bool,
            "i" => CoreTag::Integer,
            "f" => CoreTag::Float,
            "g" => CoreTag::Merge,
            "q" => CoreTag::Sequence,
            "m" => CoreTag::Mapping,
            _ => return Err(Failure::YamlTag.into()),
        }),
        _ => return Err(Failure::YamlTag.into()),
    })
}
fn scalar(text: String, style: TScalarStyle, tag: Option<&Tag>) -> Result<Scalar> {
    use CoreTag::*;
    let tag = tag_name(tag)?;
    let null = matches!(text.as_str(), "" | "~" | "null" | "Null" | "NULL");
    let boolean = matches!(
        text.as_str(),
        "true" | "True" | "TRUE" | "false" | "False" | "FALSE"
    );
    let integer = INTEGER.is_match(&text);
    let float = FLOAT.is_match(&text);
    let resolved = match tag {
        Some(String | NonSpecific) => String,
        Some(Null) if null => Null,
        Some(Bool) if boolean => Bool,
        Some(Integer) if integer => Integer,
        Some(Float) if float => Float,
        Some(Merge) if text == "<<" => Merge,
        Some(_) => return Err(Failure::YamlTag.into()),
        None if style != TScalarStyle::Plain => String,
        None if text == "<<" => Merge,
        None if null => Null,
        None if boolean => Bool,
        None if integer => Integer,
        None if float => Float,
        None => String,
    };
    Ok(match resolved {
        Null => Scalar::Atom("null".into()),
        Bool => Scalar::Atom(text.to_ascii_lowercase()),
        Integer => Scalar::Atom(text),
        // Explicit tags preserve !!float 1 without converting decimal precision
        // or expanding arbitrarily large exponents into allocated digits.
        Float => Scalar::Atom(format!("!!float {text}")),
        Merge => Scalar::Merge,
        _ => Scalar::String(text),
    })
}
fn collection_tag(tag: Option<&Tag>, expected: CoreTag) -> Result<()> {
    match tag_name(tag)? {
        None | Some(CoreTag::NonSpecific) => Ok(()),
        Some(tag) if tag == expected => Ok(()),
        _ => Err(Failure::YamlTag.into()),
    }
}

fn quote(
    text: &str,
    cancel: &Cancellation,
    mut push: impl FnMut(&str) -> Result<()>,
) -> Result<()> {
    push("\"")?;
    for (index, ch) in text.chars().enumerate() {
        if index.is_multiple_of(4096) {
            cancel.check()?;
        }
        match ch {
            '"' => push("\\\"")?,
            '\\' => push("\\\\")?,
            '\n' => push("\\n")?,
            '\r' => push("\\r")?,
            '\t' => push("\\t")?,
            ch if ch.is_control()
                || matches!(
                    ch,
                    '\u{2028}' | '\u{2029}' | '\u{feff}' | '\u{fffe}' | '\u{ffff}'
                ) =>
            {
                // YAML permits escaped BMP noncharacters in scalar values but
                // excludes them from literal source. Keep output valid input.
                let mut escape = *b"\\u0000";
                for (index, digit) in escape[2..].iter_mut().enumerate() {
                    *digit = b"0123456789abcdef"[((ch as u32 >> (12 - index * 4)) & 0xf) as usize];
                }
                push(std::str::from_utf8(&escape).expect("ASCII escape"))?
            }
            ch => push(ch.encode_utf8(&mut [0; 4]))?,
        }
    }
    push("\"")?;
    Ok(())
}

// Size accounting must not allocate an encoded scalar or apply the output
// ceiling: a fully validated merge operand may be shadowed in the final graph.
// Use the same escaping visitor for measurement and actual bounded emission.
fn quoted_size(text: &str, cancel: &Cancellation) -> Result<usize> {
    let mut size = 0usize;
    quote(text, cancel, |part| {
        size = size.saturating_add(part.len());
        Ok(())
    })?;
    Ok(size)
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
            Raw::Scalar(scalar) => Kind::Scalar(scalar),
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
                        Kind::Scalar(Scalar::Merge) => {
                            merges.push((value, self.nodes[pair[1]].mark))
                        }
                        Kind::Scalar(Scalar::String(key)) => {
                            if explicit.insert(key.clone(), value).is_some() {
                                return Err(at(Failure::DuplicateKey, self.nodes[pair[0]].mark));
                            }
                        }
                        _ => return Err(at(Failure::YamlKey, self.nodes[pair[0]].mark)),
                    }
                }
                let mut result = Mapping::default();
                for (merge, mark) in merges {
                    match &merge.kind {
                        Kind::Mapping(map) => result.merge(map, self.cancel)?,
                        Kind::Sequence(sequence) => {
                            for value in sequence {
                                if let Kind::Mapping(map) = &value.kind {
                                    result.merge(map, self.cancel)?;
                                } else {
                                    return Err(at(Failure::MergeOperand, mark));
                                }
                            }
                        }
                        _ => return Err(at(Failure::MergeOperand, mark)),
                    }
                    self.cancel.check()?;
                }
                for (key, value) in explicit {
                    self.cancel.check()?;
                    let prefix = quoted_size(&key, self.cancel)?.saturating_add(1);
                    result.insert(key.into(), Entry { value, prefix });
                }
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
            Kind::Scalar(scalar) => {
                size = match scalar {
                    Scalar::String(value) => quoted_size(value, self.cancel)?,
                    Scalar::Atom(value) => value.len(),
                    Scalar::Merge => quoted_size("<<", self.cancel)?,
                }
                .saturating_add(1);
                lines = 1;
            }
            Kind::Sequence(values) => {
                for value in values {
                    add(1, value);
                }
            }
            Kind::Mapping(values) => {
                size = values.size.min((LIMIT + 1) as u64) as usize;
                lines = values.lines.min((LIMIT + 1) as u64) as usize;
                depth = values.depths.get_max().map_or(0, |(depth, _)| depth + 1);
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
}

fn emit(value: &Value, indent: usize, output: &mut Output, cancel: &Cancellation) -> Result<()> {
    cancel.check()?;
    let padding = " ".repeat(indent);
    match &value.kind {
        Kind::Scalar(scalar) => {
            output.push(&padding)?;
            match scalar {
                Scalar::String(text) => quote(text, cancel, |part| output.push(part))?,
                Scalar::Atom(text) => output.push(text)?,
                Scalar::Merge => quote("<<", cancel, |part| output.push(part))?,
            }
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
            for (key, entry) in &values.entries {
                cancel.check()?;
                output.push(&padding)?;
                quote(key, cancel, |part| output.push(part))?;
                output.push(":")?;
                emit_child(&entry.value, indent, output, cancel)?;
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
    let (mut line, mut column) = (1, 1);
    let mut previous_cr = false;
    for (index, ch) in text.chars().enumerate() {
        if index.is_multiple_of(4096) {
            cancel.check()?;
        }
        if !matches!(ch, '\t' | '\n' | '\r' | '\u{20}'..='\u{7e}' | '\u{85}' | '\u{a0}'..='\u{d7ff}' | '\u{e000}'..='\u{fffd}' | '\u{10000}'..='\u{10ffff}')
        {
            return Err(Error::from(Failure::YamlSyntax).at(line, column));
        }
        if matches!(ch, '\r' | '\n') {
            if ch == '\r' || !previous_cr {
                line += 1;
            }
            column = 1;
        } else {
            column += 1;
        }
        previous_cr = ch == '\r';
    }
    let edits = prepare_tags(text, cancel)?;
    let mut edits = edits.iter().peekable();
    let input = text
        .chars()
        .enumerate()
        .map(|(index, ch)| {
            while edits.peek().is_some_and(|edit| index >= edit.end) {
                edits.next();
            }
            if let Some(edit) = edits.peek().filter(|edit| index >= edit.start) {
                match (edit.tag, index - edit.start) {
                    (None, _) => '#',
                    (Some(_), 0) => '!',
                    (Some(CoreTag::NonSpecific), _) => ' ',
                    (Some(tag), 1) => tag.code(),
                    _ => ' ',
                }
            } else {
                ch
            }
        })
        .take_while(|_| cancel.code() == 0);
    let mut parser = Parser::new(input);
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
                collection_tag(tag.as_ref(), CoreTag::Sequence).map_err(|e| make(e.kind))?;
                (Raw::Sequence(Vec::new()), anchor, true)
            }
            Event::MappingStart(anchor, tag) => {
                collection_tag(tag.as_ref(), CoreTag::Mapping).map_err(|e| make(e.kind))?;
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
