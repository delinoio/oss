use std::collections::HashSet;

use forge_package::*;
use forge_tree_doc::{ErrorCode, Result, error};
use uuid::Uuid;

use crate::*;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TargetKind {
    Cell,
    ConditionalFormat,
    Validation,
    Chart,
    Opaque,
}
#[derive(Debug, Clone)]
pub struct Target {
    pub id: Uuid,
    pub kind: TargetKind,
    pub region: Region,
    pub sheet: String,
    pub address: Option<Address>,
    pub range: Option<Range>,
    pub text: String,
}
#[derive(Debug, Clone)]
pub struct Imported {
    pub parts: Package,
    pub main: String,
    pub targets: Vec<Target>,
}

pub fn address(value: &str) -> Result<Address> {
    let split = value
        .find(|c: char| c.is_ascii_digit())
        .ok_or_else(|| failure("cell address"))?;
    if split == 0 {
        return Err(failure("cell address"));
    }
    let mut col = 0_u32;
    for c in value[..split].bytes() {
        if !c.is_ascii_uppercase() {
            return Err(failure("cell address"));
        }
        col = col
            .checked_mul(26)
            .and_then(|v| v.checked_add(u32::from(c - b'A') + 1))
            .filter(|n| *n <= 16384)
            .ok_or_else(|| failure("cell address"))?;
    }
    let row = value[split..]
        .parse::<u32>()
        .ok()
        .filter(|n| *n > 0 && *n <= 1048576)
        .ok_or_else(|| failure("cell address"))?;
    let result = Address {
        row: row - 1,
        column: (col - 1) as u16,
    };
    if result.a1() != value {
        return Err(failure("cell address"));
    }
    Ok(result)
}
pub fn range(value: &str) -> Result<Range> {
    let (a, b) = value.split_once(':').unwrap_or((value, value));
    let value = Range {
        first: address(a)?,
        last: address(b)?,
    };
    value.validate()?;
    Ok(value)
}

fn standard_rule(node: roxmltree::Node<'_, '_>) -> bool {
    node.descendants().filter(|n| n.is_element()).all(|n| {
        n.tag_name().namespace() == Some(S)
            && n.attributes().all(|a| a.namespace().is_none())
            && matches!(
                n.tag_name().name(),
                "conditionalFormatting"
                    | "cfRule"
                    | "formula"
                    | "colorScale"
                    | "dataBar"
                    | "iconSet"
                    | "cfvo"
                    | "color"
                    | "dataValidation"
                    | "formula1"
                    | "formula2"
            )
    })
}

pub fn import(bytes: &[u8]) -> Result<Imported> {
    let parts = read(bytes)?;
    let main = validate_office(&parts, OfficeKind::Workbook)?;
    let root = xml(&parts[&main])?;
    let sheets: Vec<_> = root
        .descendants()
        .filter(|n| n.has_tag_name((S, "sheet")))
        .collect();
    if sheets.is_empty() {
        return error(
            ErrorCode::InvalidPackage,
            "workbook",
            "Workbook has no worksheets",
        );
    }
    let mut names = HashSet::new();
    let mut sheet_ids = HashSet::new();
    let mut sheet_parts = HashSet::new();
    let styles = styles::style_part(&parts, &main).ok();
    let style_count = styles
        .as_ref()
        .map(|p| styles::count(&parts[p], "cellXfs"))
        .transpose()?
        .unwrap_or(1);
    let dxf_count = styles
        .as_ref()
        .map(|p| styles::count(&parts[p], "dxfs"))
        .transpose()?
        .unwrap_or(0);
    let shared: Vec<_> = relationships(&parts, &main)?
        .into_iter()
        .filter(|r| r.kind == format!("{R}/sharedStrings") && !r.external)
        .collect();
    if shared.len() > 1 {
        return error(
            ErrorCode::InvalidPackage,
            "shared_strings",
            "Duplicate shared-string roots",
        );
    }
    let mut strings = Vec::new();
    if let Some(relation) = shared.first() {
        let path = resolve(&main, &relation.target)?;
        let doc = xml(&parts[&path])?;
        for node in doc
            .root_element()
            .children()
            .filter(|n| n.has_tag_name((S, "si")))
        {
            strings.push(
                node.descendants()
                    .filter(|n| n.has_tag_name((S, "t")))
                    .filter_map(|n| n.text())
                    .collect::<String>(),
            );
        }
    }
    let mut targets = Vec::new();
    let mut charts = Vec::new();
    for sheet in sheets {
        let name = sheet
            .attribute("name")
            .ok_or_else(|| failure("sheet name"))?;
        let id = sheet
            .attribute("sheetId")
            .ok_or_else(|| failure("sheet id"))?;
        if !names.insert(name.to_lowercase()) || !sheet_ids.insert(id) {
            return error(
                ErrorCode::DuplicateIdentity,
                "sheet",
                "Duplicate worksheet identity",
            );
        }
        let rid = sheet
            .attribute((R, "id"))
            .ok_or_else(|| failure("sheet relationship"))?;
        let relations = relationships(&parts, &main)?;
        let relation = relations
            .iter()
            .find(|r| r.id == rid)
            .ok_or_else(|| failure("sheet relationship"))?;
        if relation.external || relation.kind != format!("{R}/worksheet") {
            continue;
        }
        let path = resolve(&main, &relation.target)?;
        if !sheet_parts.insert(path.clone()) {
            return error(
                ErrorCode::DuplicateIdentity,
                "sheet",
                "Worksheets share an ambiguous part",
            );
        }
        let doc = xml(&parts[&path])?;
        if !doc.root_element().has_tag_name((S, "worksheet")) {
            return error(
                ErrorCode::InvalidPackage,
                "sheet",
                "Malformed worksheet root",
            );
        }
        for drawing in doc
            .root_element()
            .children()
            .filter(|n| n.has_tag_name((S, "drawing")))
        {
            let Some(id) = drawing.attribute((R, "id")) else {
                return Err(failure("drawing relationship"));
            };
            let drawing_path = related(&parts, &path, id)?;
            let drawing_doc = xml(&parts[&drawing_path])?;
            for chart in drawing_doc
                .descendants()
                .filter(|n| n.has_tag_name((C, "chart")))
            {
                let id = chart
                    .attribute((R, "id"))
                    .ok_or_else(|| failure("chart relationship"))?;
                charts.push((name.to_owned(), related(&parts, &drawing_path, id)?));
            }
        }
        let data: Vec<_> = doc
            .root_element()
            .children()
            .filter(|n| n.has_tag_name((S, "sheetData")))
            .collect();
        if data.len() != 1 {
            return error(
                ErrorCode::InvalidPackage,
                "sheet",
                "Worksheet requires one sheetData element",
            );
        }
        let mut addresses = HashSet::new();
        let mut row_ids = HashSet::new();
        let mut inferred_row = 0_u32;
        let merges: Vec<_> = doc
            .descendants()
            .filter(|n| n.has_tag_name((S, "mergeCell")))
            .map(|n| range(n.attribute("ref").unwrap_or("")))
            .collect::<Result<_>>()?;
        for (i, merge) in merges.iter().enumerate() {
            if merges[..i].iter().any(|m| merge.overlaps(*m)) {
                return error(
                    ErrorCode::InvalidPackage,
                    "merge",
                    "Imported worksheet merges overlap",
                );
            }
        }
        for row in data[0].children().filter(|n| n.has_tag_name((S, "row"))) {
            let row_id = row
                .attribute("r")
                .map(|r| r.parse::<u32>().map_err(failure))
                .transpose()?
                .unwrap_or(inferred_row + 1);
            if row_id == 0 || row_id > 1048576 || !row_ids.insert(row_id) {
                return error(
                    ErrorCode::DuplicateIdentity,
                    "row",
                    "Invalid or duplicate worksheet row",
                );
            }
            inferred_row = row_id;
            let mut column = 0;
            for cell in row.children().filter(|n| n.has_tag_name((S, "c"))) {
                let at = cell
                    .attribute("r")
                    .map(address)
                    .transpose()?
                    .unwrap_or(Address {
                        row: row_id - 1,
                        column,
                    });
                at.validate()?;
                if at.row != row_id - 1 || !addresses.insert((at.row, at.column)) {
                    return error(
                        ErrorCode::DuplicateIdentity,
                        "cell",
                        "Cell address is duplicated or belongs to another row",
                    );
                }
                column = at.column.saturating_add(1);
                if let Some(style) = cell.attribute("s") {
                    let index = style.parse::<usize>().map_err(failure)?;
                    if index >= style_count {
                        return error(
                            ErrorCode::InvalidReference,
                            "cell/style",
                            "Cell references an undefined style",
                        );
                    }
                }
                let value = cell
                    .children()
                    .find(|n| n.has_tag_name((S, "v")))
                    .and_then(|n| n.text())
                    .unwrap_or("");
                let text = match cell.attribute("t").unwrap_or("n") {
                    "s" => strings
                        .get(value.parse::<usize>().map_err(failure)?)
                        .ok_or_else(|| failure("shared string"))?
                        .clone(),
                    "inlineStr" => cell
                        .descendants()
                        .filter(|n| n.has_tag_name((S, "t")))
                        .filter_map(|n| n.text())
                        .collect(),
                    _ => value.into(),
                };
                let supported = cell
                    .attributes()
                    .all(|a| a.namespace().is_none() && matches!(a.name(), "r" | "s" | "t"))
                    && cell.descendants().filter(|n| n.is_element()).all(|n| {
                        n.tag_name().namespace() == Some(S)
                            && matches!(
                                n.tag_name().name(),
                                "c" | "f"
                                    | "v"
                                    | "is"
                                    | "t"
                                    | "r"
                                    | "rPr"
                                    | "b"
                                    | "i"
                                    | "u"
                                    | "color"
                                    | "sz"
                                    | "rFont"
                                    | "family"
                                    | "scheme"
                            )
                    })
                    && cell
                        .children()
                        .filter(|n| n.has_tag_name((S, "f")))
                        .all(|n| n.attributes().len() == 0)
                    && !merges.iter().any(|m| m.contains(at) && m.first != at)
                    && cell.attribute("t") != Some("e");
                targets.push(Target {
                    id: Uuid::now_v7(),
                    kind: if supported {
                        TargetKind::Cell
                    } else {
                        TargetKind::Opaque
                    },
                    region: Region::new(&path, cell.range(), &parts)?,
                    sheet: name.into(),
                    address: Some(at),
                    range: None,
                    text,
                });
            }
        }
        for node in doc
            .descendants()
            .filter(|n| n.has_tag_name((S, "cfRule")) || n.has_tag_name((S, "dataValidation")))
        {
            let is_cf = node.has_tag_name((S, "cfRule"));
            let reference = if is_cf {
                node.parent().and_then(|p| p.attribute("sqref"))
            } else {
                node.attribute("sqref")
            }
            .and_then(|r| range(r).ok());
            if let Some(dxf) = node.attribute("dxfId")
                && dxf.parse::<usize>().map_err(failure)? >= dxf_count
            {
                return error(
                    ErrorCode::InvalidReference,
                    "conditional_format",
                    "Rule references an undefined differential format",
                );
            }
            let supported = reference.is_some()
                && standard_rule(node)
                && if is_cf {
                    matches!(
                        node.attribute("type"),
                        Some("cellIs" | "expression" | "colorScale" | "dataBar" | "iconSet")
                    )
                } else {
                    matches!(
                        node.attribute("type"),
                        Some(
                            "list"
                                | "whole"
                                | "decimal"
                                | "date"
                                | "time"
                                | "textLength"
                                | "custom"
                        )
                    )
                };
            targets.push(Target {
                id: Uuid::now_v7(),
                kind: if !supported {
                    TargetKind::Opaque
                } else if is_cf {
                    TargetKind::ConditionalFormat
                } else {
                    TargetKind::Validation
                },
                region: Region::new(&path, node.range(), &parts)?,
                sheet: name.into(),
                address: None,
                range: reference,
                text: String::new(),
            });
        }
    }
    for (sheet, path) in &charts {
        let doc = xml(&parts[path])?;
        let plots: Vec<_> = doc
            .descendants()
            .filter(|n| n.has_tag_name((C, "plotArea")))
            .collect();
        let types: Vec<_> = plots
            .iter()
            .flat_map(|n| n.children())
            .filter(|n| n.is_element() && n.tag_name().name().ends_with("Chart"))
            .collect();
        let supported = charts.iter().filter(|(_, p)| p == path).count() == 1
            && doc.root_element().has_tag_name((C, "chartSpace"))
            && types.len() == 1
            && matches!(
                types[0].tag_name().name(),
                "barChart" | "lineChart" | "pieChart"
            )
            && !doc.descendants().any(|n| n.tag_name().name() == "extLst");
        targets.push(Target {
            id: Uuid::now_v7(),
            kind: if supported {
                TargetKind::Chart
            } else {
                TargetKind::Opaque
            },
            region: Region::new(path, doc.root_element().range(), &parts)?,
            sheet: sheet.clone(),
            address: None,
            range: None,
            text: String::new(),
        });
    }
    Ok(Imported {
        parts,
        main,
        targets,
    })
}

#[derive(Debug, Clone)]
pub enum EditValue {
    Cell(Cell),
    ConditionalFormat(ConditionalFormat),
    Validation(Validation),
    Chart(forge_document::Chart),
}

fn singleton(cell: Cell) -> Workbook {
    Workbook {
        id: Uuid::now_v7(),
        sheets: vec![Sheet {
            id: Uuid::now_v7(),
            name: "Edit".into(),
            cells: vec![cell],
            merges: vec![],
            rows: vec![],
            columns: vec![],
            freeze: None,
            autofilter: None,
            conditional_formats: vec![],
            validations: vec![],
            charts: vec![],
        }],
    }
}

pub fn replace(imported: &Imported, edits: &[(Uuid, EditValue)]) -> Result<Vec<u8>> {
    let mut parts = imported.parts.clone();
    let mut fragments = Vec::new();
    let mut selected = Vec::new();
    let mut seen = HashSet::new();
    for (id, value) in edits {
        let target = imported
            .targets
            .iter()
            .find(|t| t.id == *id)
            .ok_or_else(|| failure("target"))?;
        if !seen.insert(id)
            || selected
                .iter()
                .any(|other: &&Target| other.region.overlaps(&target.region))
        {
            return error(
                ErrorCode::RevisionConflict,
                "target",
                "Mounted regions overlap",
            );
        }
        let fragment = match value {
            EditValue::Cell(cell) if target.kind == TargetKind::Cell => {
                if Some(cell.address) != target.address {
                    return error(
                        ErrorCode::UnsupportedEdit,
                        "cell",
                        "Imported cell edits must retain their address",
                    );
                }
                if cell.hyperlink.is_some() {
                    return error(
                        ErrorCode::UnsupportedEdit,
                        "cell/hyperlink",
                        "Adding imported cell hyperlinks requires a hyperlink region",
                    );
                }
                let generated = read(&generate(&singleton(cell.clone()))?)?;
                let sheet = xml(&generated["xl/worksheets/sheet1.xml"])?;
                let candidate = sheet.descendants().find(|n| {
                    n.has_tag_name((S, "c")) && n.attribute("r") == Some(cell.address.a1().as_str())
                });
                let old_doc = xml(&imported.parts[&target.region.part])?;
                let old = old_doc
                    .descendants()
                    .find(|n| n.range() == target.region.range)
                    .ok_or_else(|| failure("cell"))?;
                let style = if cell.format.is_some() || matches!(cell.value, Value::Date(_)) {
                    let path = styles::style_part(&parts, &imported.main)?;
                    let index = candidate
                        .and_then(|n| n.attribute("s"))
                        .unwrap_or("0")
                        .parse::<usize>()
                        .map_err(failure)?;
                    Some(
                        styles::graft(&mut parts, &path, &generated["xl/styles.xml"], index)?
                            .to_string(),
                    )
                } else {
                    old.attribute("s").map(str::to_owned)
                };
                let attr = style.map(|s| format!(" s=\"{s}\"")).unwrap_or_default();
                // Inline strings avoid touching an imported shared-string table.
                match &cell.value {
                    Value::Text(text) => format!(
                        "<c xmlns=\"{S}\" r=\"{}\"{attr} t=\"inlineStr\"><is><t \
                         xml:space=\"preserve\">{}</t></is></c>",
                        cell.address.a1(),
                        escape(text)
                    ),
                    _ => {
                        let body = candidate
                            .map(|n| {
                                n.children()
                                    .filter(|n| n.is_element())
                                    .map(|n| {
                                        std::str::from_utf8(&generated["xl/worksheets/sheet1.xml"])
                                            .unwrap()[n.range()]
                                        .to_string()
                                    })
                                    .collect::<String>()
                            })
                            .unwrap_or_default();
                        let kind = candidate.and_then(|n| n.attribute("t")).unwrap_or("n");
                        format!(
                            "<c xmlns=\"{S}\" r=\"{}\"{attr} t=\"{kind}\">{body}</c>",
                            cell.address.a1()
                        )
                    }
                }
            }
            EditValue::ConditionalFormat(rule) if target.kind == TargetKind::ConditionalFormat => {
                if Some(rule.range) != target.range {
                    return error(
                        ErrorCode::UnsupportedEdit,
                        "conditional_format",
                        "Imported rule edits must retain their range",
                    );
                }
                let dxf = match &rule.rule {
                    ConditionalRule::CellValue { format, .. }
                    | ConditionalRule::Formula { format, .. } => {
                        let path = styles::style_part(&parts, &imported.main)?;
                        Some(styles::append_dxf(
                            &mut parts,
                            &path,
                            crate::emit::differential(format)?,
                        )?)
                    }
                    _ => None,
                };
                let original = xml(&imported.parts[&target.region.part])?;
                let priority = original
                    .descendants()
                    .find(|n| n.range() == target.region.range)
                    .and_then(|n| n.attribute("priority"))
                    .and_then(|n| n.parse().ok())
                    .ok_or_else(|| failure("conditional priority"))?;
                let container = crate::emit::conditional(rule, priority, dxf)?;
                let parsed = xml(container.as_bytes())?;
                let node = parsed
                    .root_element()
                    .children()
                    .find(|n| n.has_tag_name((S, "cfRule")))
                    .ok_or_else(|| failure("conditional format"))?;
                container[node.range()].replacen("<cfRule", &format!("<cfRule xmlns=\"{S}\""), 1)
            }
            EditValue::Validation(rule) if target.kind == TargetKind::Validation => {
                if Some(rule.range) != target.range {
                    return error(
                        ErrorCode::UnsupportedEdit,
                        "validation",
                        "Imported validation edits must retain their range",
                    );
                }
                crate::emit::validation(rule)?
            }
            EditValue::Chart(chart) if target.kind == TargetKind::Chart => {
                let (chart_bytes, workbook_bytes) = chart.office_parts()?;
                let workbook = read(&workbook_bytes)?;
                let chart_doc = xml(&chart_bytes)?;
                let chart_text = std::str::from_utf8(&chart_bytes).map_err(failure)?;
                let current = xml(&parts[&imported.main])?;
                let names: HashSet<_> = current
                    .descendants()
                    .filter_map(|n| {
                        if n.has_tag_name((S, "sheet")) {
                            n.attribute("name").map(|s| s.to_lowercase())
                        } else {
                            None
                        }
                    })
                    .collect();
                let mut name = format!("Forge{}", &Uuid::now_v7().simple().to_string()[..20]);
                while names.contains(&name.to_lowercase()) {
                    name = format!("Forge{}", &Uuid::now_v7().simple().to_string()[..20]);
                }
                let new_id = current
                    .descendants()
                    .filter(|n| n.has_tag_name((S, "sheet")))
                    .filter_map(|n| n.attribute("sheetId").and_then(|s| s.parse::<u32>().ok()))
                    .max()
                    .unwrap_or(0)
                    .checked_add(1)
                    .ok_or_else(|| failure("sheet id"))?;
                let sheets = current
                    .root_element()
                    .children()
                    .find(|n| n.has_tag_name((S, "sheets")))
                    .ok_or_else(|| failure("sheets"))?;
                let rid = format!("rId{}", Uuid::now_v7().simple());
                let worksheet_path = format!("xl/worksheets/{}.xml", Uuid::now_v7());
                let sheet_tag = format!(
                    "<sheet xmlns=\"{S}\" xmlns:r=\"{R}\" name=\"{name}\" sheetId=\"{new_id}\" \
                     state=\"hidden\" r:id=\"{rid}\"/>"
                );
                let main_bytes =
                    insert_element_child(&parts[&imported.main], sheets.range(), &sheet_tag)?;
                parts.insert(imported.main.clone(), main_bytes);
                add_relationship(
                    &mut parts,
                    &imported.main,
                    &rid,
                    &format!("{R}/worksheet"),
                    &format!("/{worksheet_path}"),
                )?;
                add_content_type(
                    &mut parts,
                    &worksheet_path,
                    "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml",
                )?;
                // Store the replacement chart's data in a new private worksheet.
                // Existing worksheet cells and formulas remain byte-for-byte
                // unchanged, including any shared data used by other charts.
                let mut data = String::new();
                for row in 0..=chart.categories.len() {
                    data.push_str(&format!("<row r=\"{}\">", row + 1));
                    if row > 0 {
                        data.push_str(&format!(
                            "<c r=\"A{}\" t=\"inlineStr\"><is><t \
                             xml:space=\"preserve\">{}</t></is></c>",
                            row + 1,
                            escape(&chart.categories[row - 1])
                        ));
                    }
                    for (col, series) in chart.series.iter().enumerate() {
                        let address = Address {
                            row: row as u32,
                            column: col as u16 + 1,
                        }
                        .a1();
                        if row == 0 {
                            data.push_str(&format!(
                                "<c r=\"{address}\" t=\"inlineStr\"><is><t \
                                 xml:space=\"preserve\">{}</t></is></c>",
                                escape(&series.name)
                            ));
                        } else {
                            data.push_str(&format!(
                                "<c r=\"{address}\"><v>{}</v></c>",
                                series.values[row - 1]
                            ));
                        }
                    }
                    data.push_str("</row>");
                }
                parts.insert(
                    worksheet_path,
                    format!("<worksheet xmlns=\"{S}\"><sheetData>{data}</sheetData></worksheet>")
                        .into_bytes(),
                );
                let mut next = chart_text[chart_doc.root_element().range()].to_owned();
                // The shared writer owns these formulas and names its worksheet
                // Data. Rebinding that exact generated prefix cannot affect an
                // imported formula or a caller-provided chart title.
                let mut replacements = Vec::new();
                for formula in chart_doc.descendants().filter(|n| n.has_tag_name((C, "f"))) {
                    let Some(text) = formula.text() else {
                        return Err(failure("chart formula"));
                    };
                    let suffix = text
                        .strip_prefix("Data!")
                        .ok_or_else(|| failure("generated chart formula"))?;
                    let fragment = format!("<c:f>{}</c:f>", escape(&format!("'{name}'!{suffix}")));
                    replacements.push((
                        formula.range().start - chart_doc.root_element().range().start
                            ..formula.range().end - chart_doc.root_element().range().start,
                        fragment,
                    ));
                }
                for (range, fragment) in replacements.into_iter().rev() {
                    let bytes = replace_range(next.as_bytes(), range, &fragment);
                    next = String::from_utf8(bytes).map_err(failure)?;
                }
                // Keep the generated data workbook validated even though only its
                // scalar data and native chart XML are grafted into this package.
                validate_office(&workbook, OfficeKind::Workbook)?;
                next
            }
            _ => {
                return error(
                    ErrorCode::UnsupportedEdit,
                    "target",
                    "Edit does not match a supported imported region",
                );
            }
        };
        selected.push(target);
        fragments.push(fragment);
    }
    let replacements: Vec<_> = selected
        .iter()
        .zip(&fragments)
        .map(|(t, fragment)| Replacement {
            region: &t.region,
            xml: fragment,
        })
        .collect();
    parts = replace_regions(&parts, &replacements)?;
    // Cached results are caller supplied. Ask the application to recalculate
    // after supported edits, preserving the rest of the workbook XML verbatim.
    if !edits.is_empty() {
        let bytes = &parts[&imported.main];
        let doc = xml(bytes)?;
        let calc = doc
            .root_element()
            .children()
            .find(|n| n.has_tag_name((S, "calcPr")));
        let next = if let Some(calc) = calc {
            let mut changes = Vec::new();
            for name in ["fullCalcOnLoad", "forceFullCalc"] {
                if let Some(attr) = calc.attribute_node(name) {
                    changes.push((attr.range_value(), "1".to_owned()));
                } else {
                    let text = std::str::from_utf8(bytes).map_err(failure)?;
                    let at = calc.range().start
                        + text[calc.range()]
                            .find(|c: char| c.is_whitespace() || c == '/' || c == '>')
                            .unwrap();
                    changes.push((at..at, format!(" {name}=\"1\"")));
                }
            }
            changes.sort_by_key(|(r, _)| r.start);
            let mut next = bytes.clone();
            for (r, v) in changes.into_iter().rev() {
                next = replace_range(&next, r, &v);
            }
            next
        } else {
            crate::emit::insert_sheet_child(
                bytes,
                &format!("<calcPr xmlns=\"{S}\" fullCalcOnLoad=\"1\" forceFullCalc=\"1\"/>"),
                &[
                    "oleSize",
                    "customWorkbookViews",
                    "pivotCaches",
                    "smartTagPr",
                    "smartTagTypes",
                    "webPublishing",
                    "fileRecoveryPr",
                    "webPublishObjects",
                    "extLst",
                ],
            )?
        };
        parts.insert(imported.main.clone(), next);
    }
    validate_office(&parts, OfficeKind::Workbook)?;
    write(&parts)
}
