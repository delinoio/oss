//! Append style resources without renumbering or reserializing existing styles.
use std::collections::BTreeMap;

use forge_package::*;
use forge_tree_doc::{ErrorCode, Result, error};

const ORDER: &[&str] = &[
    "numFmts",
    "fonts",
    "fills",
    "borders",
    "cellStyleXfs",
    "cellXfs",
    "cellStyles",
    "dxfs",
    "tableStyles",
    "colors",
    "extLst",
];

pub fn style_part(parts: &Package, workbook: &str) -> Result<String> {
    let relations: Vec<_> = relationships(parts, workbook)?
        .into_iter()
        .filter(|r| r.kind == format!("{R}/styles") && !r.external)
        .collect();
    if relations.len() != 1 {
        return error(
            ErrorCode::UnsupportedEdit,
            "styles",
            "Editing formatted cells requires one unambiguous style part",
        );
    }
    resolve(workbook, &relations[0].target)
}

pub fn count(bytes: &[u8], name: &str) -> Result<usize> {
    let doc = xml(bytes)?;
    let nodes: Vec<_> = doc
        .root_element()
        .children()
        .filter(|n| n.has_tag_name((S, name)))
        .collect();
    if nodes.len() > 1 {
        return error(
            ErrorCode::InvalidPackage,
            "styles",
            "Duplicate style collection",
        );
    }
    Ok(nodes
        .first()
        .map(|n| n.children().filter(|c| c.is_element()).count())
        .unwrap_or(0))
}

fn standalone(raw: &str) -> String {
    let at = raw
        .find(|c: char| c == '>' || c == '/' || c.is_whitespace())
        .unwrap();
    format!("{} xmlns=\"{S}\"{}", &raw[..at], &raw[at..])
}

pub fn append(bytes: &[u8], collection: &str, fragments: &[String]) -> Result<Vec<u8>> {
    if fragments.is_empty() {
        return Ok(bytes.to_vec());
    }
    let doc = xml(bytes)?;
    if let Some(node) = doc
        .root_element()
        .children()
        .find(|n| n.has_tag_name((S, collection)))
    {
        let old = node.children().filter(|c| c.is_element()).count();
        let mut ranges = Vec::new();
        if let Some(attr) = node.attribute_node("count") {
            ranges.push((attr.range_value(), (old + fragments.len()).to_string()));
        } else {
            let start = std::str::from_utf8(bytes).map_err(failure)?;
            let at = start[node.range()]
                .find(|c: char| c == '>' || c == '/' || c.is_whitespace())
                .unwrap()
                + node.range().start;
            ranges.push((at..at, format!(" count=\"{}\"", old + fragments.len())));
        }
        let raw = &std::str::from_utf8(bytes).map_err(failure)?[node.range()];
        if raw.ends_with("/>") {
            let qname = raw[1..]
                .split(|c: char| c.is_whitespace() || c == '/' || c == '>')
                .next()
                .unwrap();
            ranges.push((
                node.range().end - 2..node.range().end,
                format!(">{}</{qname}>", fragments.concat()),
            ));
        } else {
            let at =
                node.range().start + raw.rfind("</").ok_or_else(|| failure("style collection"))?;
            ranges.push((at..at, fragments.concat()));
        }
        ranges.sort_by_key(|(r, _)| r.start);
        let mut result = bytes.to_vec();
        for (range, text) in ranges.into_iter().rev() {
            result = replace_range(&result, range, &text);
        }
        Ok(result)
    } else {
        let fragment = format!(
            "<{collection} xmlns=\"{S}\" count=\"{}\">{}</{collection}>",
            fragments.len(),
            fragments.concat()
        );
        let position = ORDER
            .iter()
            .position(|name| *name == collection)
            .ok_or_else(|| failure("collection"))?;
        if let Some(next) = doc
            .root_element()
            .children()
            .find(|n| ORDER[position + 1..].contains(&n.tag_name().name()))
        {
            Ok(replace_range(
                bytes,
                next.range().start..next.range().start,
                &fragment,
            ))
        } else {
            insert_before_close(bytes, &fragment)
        }
    }
}

pub fn append_dxf(parts: &mut Package, path: &str, fragment: String) -> Result<usize> {
    let bytes = parts.get(path).ok_or_else(|| failure("styles"))?;
    let index = count(bytes, "dxfs")?;
    let fragment = standalone(&fragment);
    let bytes = append(bytes, "dxfs", &[fragment])?;
    parts.insert(path.into(), bytes);
    Ok(index)
}

pub fn graft(parts: &mut Package, path: &str, generated: &[u8], style: usize) -> Result<usize> {
    let original = parts.get(path).ok_or_else(|| failure("styles"))?.clone();
    let source = xml(generated)?;
    let target = xml(&original)?;
    let text = std::str::from_utf8(generated).map_err(failure)?;
    let mut maps: BTreeMap<&str, Vec<usize>> = BTreeMap::new();
    let mut output = original.clone();
    let mut format_ids = BTreeMap::new();
    let mut next = target
        .descendants()
        .filter_map(|n| {
            n.attribute("numFmtId")
                .and_then(|v| v.parse::<usize>().ok())
        })
        .max()
        .unwrap_or(163)
        .max(163)
        + 1;
    for collection in [
        "numFmts",
        "fonts",
        "fills",
        "borders",
        "cellStyleXfs",
        "cellXfs",
    ] {
        let start = count(&output, collection)?;
        let mut fragments = Vec::new();
        let mut mapping = Vec::new();
        if let Some(group) = source
            .root_element()
            .children()
            .find(|n| n.has_tag_name((S, collection)))
        {
            for node in group.children().filter(|n| n.is_element()) {
                let mut replacements = Vec::new();
                for attr in node.attributes() {
                    let map = match attr.name() {
                        "fontId" => Some("fonts"),
                        "fillId" => Some("fills"),
                        "borderId" => Some("borders"),
                        "xfId" => Some("cellStyleXfs"),
                        _ => None,
                    };
                    if let Some(map) = map {
                        let old = attr.value().parse::<usize>().map_err(failure)?;
                        let new = maps
                            .get(map)
                            .and_then(|m| m.get(old))
                            .ok_or_else(|| failure("style reference"))?;
                        replacements.push((
                            attr.range_value().start - node.range().start
                                ..attr.range_value().end - node.range().start,
                            new.to_string(),
                        ));
                    } else if attr.name() == "numFmtId" {
                        let old = attr.value().parse::<usize>().map_err(failure)?;
                        if old >= 164 {
                            let new = if collection == "numFmts" {
                                let id = next;
                                next += 1;
                                format_ids.insert(old, id);
                                id
                            } else {
                                *format_ids
                                    .get(&old)
                                    .ok_or_else(|| failure("number format"))?
                            };
                            replacements.push((
                                attr.range_value().start - node.range().start
                                    ..attr.range_value().end - node.range().start,
                                new.to_string(),
                            ));
                        }
                    }
                }
                let mut fragment = text[node.range()].as_bytes().to_vec();
                replacements.sort_by_key(|(r, _)| r.start);
                for (range, value) in replacements.into_iter().rev() {
                    fragment = replace_range(&fragment, range, &value);
                }
                fragments.push(standalone(std::str::from_utf8(&fragment).map_err(failure)?));
                mapping.push(start + mapping.len());
            }
        }
        output = append(&output, collection, &fragments)?;
        maps.insert(collection, mapping);
    }
    let mapped = *maps
        .get("cellXfs")
        .and_then(|m| m.get(style))
        .ok_or_else(|| failure("cell style"))?;
    parts.insert(path.into(), output);
    Ok(mapped)
}
