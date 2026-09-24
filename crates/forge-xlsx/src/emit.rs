use std::collections::BTreeMap;

use forge_document::{Align, ChartKind, Direction};
use forge_package::*;
use forge_tree_doc::Result;
use rust_xlsxwriter::{
    Chart as NativeChart, ChartDataLabel, ChartType, Color, ExcelDateTime, Format, FormatAlign,
    FormatBorder, Formula as NativeFormula, Url, Workbook as NativeWorkbook,
};

use crate::*;

pub(crate) fn format(value: &CellFormat) -> Result<Format> {
    let s = &value.style;
    let mut f = Format::new();
    if let Some(family) = &s.font_family {
        f = f.set_font_name(family);
    }
    if let Some(size) = s.font_size {
        f = f.set_font_size(size);
    }
    if s.bold.unwrap_or(false) {
        f = f.set_bold();
    }
    if s.italic.unwrap_or(false) {
        f = f.set_italic();
    }
    if s.underline.unwrap_or(false) {
        f = f.set_underline(rust_xlsxwriter::FormatUnderline::Single);
    }
    if let Some(color) = &s.color {
        f = f.set_font_color(Color::RGB(forge_document::color(color)?));
    }
    if let Some(color) = &s.background {
        f = f.set_background_color(Color::RGB(forge_document::color(color)?));
    }
    if let Some(align) = s.align {
        f = f.set_align(match align {
            Align::Left => FormatAlign::Left,
            Align::Center => FormatAlign::Center,
            Align::Right => FormatAlign::Right,
            Align::Justify => FormatAlign::Justify,
        });
    }
    if s.direction == Direction::Rtl {
        f = f.set_reading_direction(2);
    }
    if let Some(number) = &value.number_format {
        f = f.set_num_format(number);
    }
    if value.wrap {
        f = f.set_text_wrap();
    }
    if value.border {
        f = f.set_border(FormatBorder::Thin);
    }
    Ok(f)
}

pub(crate) fn differential(value: &CellFormat) -> Result<String> {
    value.validate()?;
    let s = &value.style;
    let mut out = String::from("<dxf><font>");
    if let Some(name) = &s.font_family {
        out.push_str(&format!("<name val=\"{}\"/>", escape(name)));
    }
    if let Some(size) = s.font_size {
        out.push_str(&format!("<sz val=\"{size}\"/>"));
    }
    if let Some(enabled) = s.bold {
        out.push_str(if enabled { "<b/>" } else { "<b val=\"0\"/>" });
    }
    if let Some(enabled) = s.italic {
        out.push_str(if enabled { "<i/>" } else { "<i val=\"0\"/>" });
    }
    if let Some(enabled) = s.underline {
        out.push_str(if enabled { "<u/>" } else { "<u val=\"none\"/>" });
    }
    if let Some(color) = &s.color {
        out.push_str(&format!("<color rgb=\"FF{}\"/>", &color[1..]));
    }
    out.push_str("</font>");
    if let Some(number) = &value.number_format {
        out.push_str(&format!(
            "<numFmt numFmtId=\"164\" formatCode=\"{}\"/>",
            escape(number)
        ));
    }
    if let Some(color) = &s.background {
        out.push_str(&format!(
            "<fill><patternFill patternType=\"solid\"><fgColor rgb=\"FF{}\"/><bgColor \
             indexed=\"64\"/></patternFill></fill>",
            &color[1..]
        ));
    }
    let align = s
        .align
        .map(|align| match align {
            Align::Left => "left",
            Align::Center => "center",
            Align::Right => "right",
            Align::Justify => "justify",
        })
        .map(|align| format!(" horizontal=\"{align}\""))
        .unwrap_or_default();
    out.push_str(&format!(
        "<alignment{align} wrapText=\"{}\" readingOrder=\"{}\"/>",
        u8::from(value.wrap),
        if s.direction == Direction::Rtl { 2 } else { 0 }
    ));
    if value.border {
        out.push_str(
            "<border><left style=\"thin\"/><right style=\"thin\"/><top style=\"thin\"/><bottom \
             style=\"thin\"/></border>",
        );
    }
    out.push_str("</dxf>");
    Ok(out)
}

pub(crate) fn threshold(value: &Threshold) -> String {
    let kind = match value.kind {
        ThresholdKind::Minimum => "min",
        ThresholdKind::Maximum => "max",
        ThresholdKind::Number => "num",
        ThresholdKind::Percent => "percent",
        ThresholdKind::Percentile => "percentile",
        ThresholdKind::Formula => "formula",
    };
    format!(
        "<cfvo type=\"{kind}\" val=\"{}\"/>",
        escape(
            value
                .value
                .as_deref()
                .unwrap_or("0")
                .trim_start_matches('=')
        )
    )
}

pub(crate) fn conditional(
    rule: &ConditionalFormat,
    priority: usize,
    dxf: Option<usize>,
) -> Result<String> {
    rule.validate()?;
    let (kind, attributes, body) = match &rule.rule {
        ConditionalRule::CellValue {
            operator, values, ..
        } => (
            "cellIs",
            format!(
                " operator=\"{}\" dxfId=\"{}\"",
                operator.xml(),
                dxf.unwrap()
            ),
            values
                .iter()
                .map(|v| format!("<formula>{}</formula>", escape(v.trim_start_matches('='))))
                .collect::<String>(),
        ),
        ConditionalRule::Formula { formula, .. } => (
            "expression",
            format!(" dxfId=\"{}\"", dxf.unwrap()),
            format!(
                "<formula>{}</formula>",
                escape(formula.trim_start_matches('='))
            ),
        ),
        ConditionalRule::ColorScale { thresholds, colors } => {
            let body = format!(
                "<colorScale>{}{}</colorScale>",
                thresholds.iter().map(threshold).collect::<String>(),
                colors
                    .iter()
                    .map(|c| format!("<color rgb=\"FF{}\"/>", &c[1..]))
                    .collect::<String>()
            );
            ("colorScale", String::new(), body)
        }
        ConditionalRule::DataBar {
            minimum,
            maximum,
            color,
            hide_value,
        } => (
            "dataBar",
            String::new(),
            format!(
                "<dataBar showValue=\"{}\">{}{}<color rgb=\"FF{}\"/></dataBar>",
                u8::from(!hide_value),
                threshold(minimum),
                threshold(maximum),
                &color[1..]
            ),
        ),
        ConditionalRule::IconSet {
            icons,
            thresholds,
            reverse,
            hide_value,
        } => (
            "iconSet",
            String::new(),
            format!(
                "<iconSet iconSet=\"{}\" reverse=\"{}\" showValue=\"{}\">{}</iconSet>",
                icons.xml(),
                u8::from(*reverse),
                u8::from(!hide_value),
                thresholds.iter().map(threshold).collect::<String>()
            ),
        ),
    };
    Ok(format!(
        "<conditionalFormatting xmlns=\"{S}\" sqref=\"{}\"><cfRule type=\"{kind}\" \
         priority=\"{priority}\"{attributes}>{body}</cfRule></conditionalFormatting>",
        rule.range.a1()
    ))
}

pub(crate) fn validation(rule: &Validation) -> Result<String> {
    validation_with_visibility(rule, rule.prompt.is_some(), true)
}

pub(crate) fn validation_with_visibility(
    rule: &Validation,
    show_input: bool,
    show_error: bool,
) -> Result<String> {
    rule.validate()?;
    let mut attrs = format!(
        "type=\"{}\" sqref=\"{}\" allowBlank=\"{}\" showInputMessage=\"{}\" \
         showErrorMessage=\"{}\" errorStyle=\"stop\"",
        rule.kind.xml(),
        rule.range.a1(),
        u8::from(rule.allow_blank),
        u8::from(show_input),
        u8::from(show_error)
    );
    if !matches!(rule.kind, ValidationKind::List | ValidationKind::Custom) {
        attrs.push_str(&format!(
            " operator=\"{}\"",
            rule.operator.unwrap_or(Comparison::Between).xml()
        ));
    }
    if let Some(prompt) = &rule.prompt {
        attrs.push_str(&format!(" prompt=\"{}\"", escape(prompt)));
    }
    if let Some(error) = &rule.error {
        attrs.push_str(&format!(" error=\"{}\"", escape(error)));
    }
    let mut out = format!("<dataValidation xmlns=\"{S}\" {attrs}>");
    for (i, formula) in rule.formulas.iter().enumerate() {
        out.push_str(&format!(
            "<formula{}>{}</formula{}>",
            i + 1,
            escape(formula.trim_start_matches('=')),
            i + 1
        ));
    }
    out.push_str("</dataValidation>");
    Ok(out)
}

/// Insert worksheet children in schema order, keeping existing bytes intact.
pub(crate) fn insert_sheet_child(bytes: &[u8], fragment: &str, before: &[&str]) -> Result<Vec<u8>> {
    let doc = xml(bytes)?;
    if let Some(node) = doc
        .root_element()
        .children()
        .find(|n| n.tag_name().namespace() == Some(S) && before.contains(&n.tag_name().name()))
    {
        Ok(replace_range(
            bytes,
            node.range().start..node.range().start,
            fragment,
        ))
    } else {
        insert_before_close(bytes, fragment)
    }
}

pub fn generate(model: &Workbook) -> Result<Vec<u8>> {
    validate(model)?;
    let mut workbook = NativeWorkbook::new();
    let mut data_sheets = Vec::new();
    let mut names: std::collections::HashSet<_> =
        model.sheets.iter().map(|s| s.name.to_lowercase()).collect();
    for sheet in &model.sheets {
        forge_tree_doc::cancellation::checkpoint()?;
        let worksheet = workbook.add_worksheet();
        worksheet.set_name(&sheet.name).map_err(failure)?;
        for merge in &sheet.merges {
            worksheet
                .merge_range(
                    merge.first.row,
                    merge.first.column,
                    merge.last.row,
                    merge.last.column,
                    "",
                    &Format::new(),
                )
                .map_err(failure)?;
        }
        for cell in &sheet.cells {
            forge_tree_doc::cancellation::checkpoint()?;
            let address = cell.address;
            let mut native_format = cell
                .format
                .as_ref()
                .map(format)
                .transpose()?
                .unwrap_or_default();
            if matches!(cell.value, Value::Date(_))
                && cell
                    .format
                    .as_ref()
                    .and_then(|f| f.number_format.as_ref())
                    .is_none()
            {
                native_format = native_format.set_num_format("yyyy-mm-dd");
            }
            if let (Some(href), Value::Text(text)) = (&cell.hyperlink, &cell.value) {
                worksheet
                    .write_url_with_format(
                        address.row,
                        address.column,
                        Url::new(href).set_text(text),
                        &native_format,
                    )
                    .map_err(failure)?;
                continue;
            }
            match &cell.value {
                Value::Text(value) => {
                    worksheet
                        .write_string_with_format(
                            address.row,
                            address.column,
                            value,
                            &native_format,
                        )
                        .map_err(failure)?;
                }
                Value::Number(value) => {
                    worksheet
                        .write_number_with_format(
                            address.row,
                            address.column,
                            *value,
                            &native_format,
                        )
                        .map_err(failure)?;
                }
                Value::Boolean(value) => {
                    worksheet
                        .write_boolean_with_format(
                            address.row,
                            address.column,
                            *value,
                            &native_format,
                        )
                        .map_err(failure)?;
                }
                Value::Date(value) => {
                    let value = ExcelDateTime::parse_from_str(value).map_err(failure)?;
                    worksheet
                        .write_datetime_with_format(
                            address.row,
                            address.column,
                            &value,
                            &native_format,
                        )
                        .map_err(failure)?;
                }
                Value::Formula(value) => {
                    worksheet
                        .write_formula_with_format(
                            address.row,
                            address.column,
                            NativeFormula::new(&value.expression),
                            &native_format,
                        )
                        .map_err(failure)?;
                }
                Value::Blank => {
                    worksheet
                        .write_blank(address.row, address.column, &native_format)
                        .map_err(failure)?;
                }
            }
        }
        for dimension in &sheet.rows {
            worksheet
                .set_row_height(dimension.row, dimension.height)
                .map_err(failure)?;
        }
        for dimension in &sheet.columns {
            worksheet
                .set_column_width(dimension.column, dimension.width)
                .map_err(failure)?;
        }
        if let Some(at) = sheet.freeze {
            worksheet
                .set_freeze_panes(at.row, at.column)
                .map_err(failure)?;
        }
        if let Some(range) = sheet.autofilter {
            worksheet
                .autofilter(
                    range.first.row,
                    range.first.column,
                    range.last.row,
                    range.last.column,
                )
                .map_err(failure)?;
        }
        for chart in &sheet.charts {
            let mut name = format!("Forge{}", &chart.id.simple().to_string()[..20]);
            while !names.insert(name.to_lowercase()) {
                name = format!("Forge{}", &uuid::Uuid::now_v7().simple().to_string()[..20]);
            }
            let mut native = NativeChart::new(match chart.chart.kind {
                ChartKind::Bar => ChartType::Column,
                ChartKind::Line => ChartType::Line,
                ChartKind::Pie => ChartType::Pie,
            });
            // Chart data is isolated on a hidden sheet; applications must still
            // plot it instead of treating hidden source values as absent.
            native.show_hidden_data();
            for (i, _) in chart.chart.series.iter().enumerate() {
                let series = native
                    .add_series()
                    .set_name((name.as_str(), 0, i as u16 + 1))
                    .set_categories((name.as_str(), 1, 0, chart.chart.categories.len() as u32, 0))
                    .set_values((
                        name.as_str(),
                        1,
                        i as u16 + 1,
                        chart.chart.categories.len() as u32,
                        i as u16 + 1,
                    ));
                if chart.chart.labels {
                    series.set_data_label(ChartDataLabel::new().show_value());
                }
            }
            if let Some(title) = &chart.chart.title {
                native.title().set_name(title);
            }
            if !chart.chart.legend {
                native.legend().set_hidden();
            }
            native
                .set_width(chart.width)
                .set_height(chart.height)
                .set_alt_text(&chart.alt);
            worksheet
                .insert_chart(chart.at.row, chart.at.column, &native)
                .map_err(failure)?;
            data_sheets.push((name, &chart.chart));
        }
    }
    for (name, chart) in data_sheets {
        let sheet = workbook.add_worksheet();
        sheet.set_name(&name).map_err(failure)?;
        sheet.set_hidden(true);
        for (row, category) in chart.categories.iter().enumerate() {
            forge_tree_doc::cancellation::checkpoint()?;
            sheet
                .write_string(row as u32 + 1, 0, category)
                .map_err(failure)?;
        }
        for (col, series) in chart.series.iter().enumerate() {
            sheet
                .write_string(0, col as u16 + 1, &series.name)
                .map_err(failure)?;
            for (row, value) in series.values.iter().enumerate() {
                forge_tree_doc::cancellation::checkpoint()?;
                sheet
                    .write_number(row as u32 + 1, col as u16 + 1, *value)
                    .map_err(failure)?;
            }
        }
    }
    let bytes = workbook.save_to_buffer().map_err(failure)?;
    let mut parts = read(&bytes)?;
    let mut dxfs = Vec::new();
    for (index, sheet) in model.sheets.iter().enumerate() {
        let path = format!("xl/worksheets/sheet{}.xml", index + 1);
        let mut bytes = parts[&path].clone();
        let doc = xml(&bytes)?;
        let formulas: BTreeMap<_, _> = sheet
            .cells
            .iter()
            .filter_map(|cell| {
                if let Value::Formula(formula) = &cell.value {
                    Some((cell.address.a1(), formula))
                } else {
                    None
                }
            })
            .collect();
        let mut replacements = Vec::new();
        for cell in doc.descendants().filter(|n| n.has_tag_name((S, "c"))) {
            forge_tree_doc::cancellation::checkpoint()?;
            let Some(formula) = cell
                .attribute("r")
                .and_then(|address| formulas.get(address))
            else {
                continue;
            };
            let expression = cell
                .children()
                .find(|n| n.has_tag_name((S, "f")))
                .and_then(|n| n.text())
                .ok_or_else(|| failure("formula"))?;
            let (kind, value) = match &formula.cached {
                None => ("n", String::new()),
                Some(CachedValue::Number(n)) => ("n", format!("<v>{n}</v>")),
                Some(CachedValue::Text(t)) => ("str", format!("<v>{}</v>", escape(t))),
                Some(CachedValue::Boolean(v)) => ("b", format!("<v>{}</v>", u8::from(*v))),
            };
            let style = cell
                .attribute("s")
                .map(|s| format!(" s=\"{s}\""))
                .unwrap_or_default();
            replacements.push((
                cell.range(),
                format!(
                    "<c r=\"{}\" t=\"{kind}\"{style}><f>{}</f>{value}</c>",
                    cell.attribute("r").unwrap(),
                    escape(expression)
                ),
            ));
        }
        for (range, fragment) in replacements.into_iter().rev() {
            bytes = replace_range(&bytes, range, &fragment);
        }
        let mut rules = String::new();
        for (i, rule) in sheet.conditional_formats.iter().enumerate() {
            let dxf = match &rule.rule {
                ConditionalRule::CellValue { format, .. }
                | ConditionalRule::Formula { format, .. } => {
                    let id = dxfs.len();
                    dxfs.push(differential(format)?);
                    Some(id)
                }
                _ => None,
            };
            rules.push_str(&conditional(rule, i + 1, dxf)?);
        }
        bytes = insert_sheet_child(
            &bytes,
            &rules,
            &[
                "dataValidations",
                "hyperlinks",
                "printOptions",
                "pageMargins",
                "pageSetup",
                "headerFooter",
                "drawing",
                "legacyDrawing",
                "extLst",
            ],
        )?;
        if !sheet.validations.is_empty() {
            let mut validations = format!(
                "<dataValidations xmlns=\"{S}\" count=\"{}\">",
                sheet.validations.len()
            );
            for rule in &sheet.validations {
                validations.push_str(&validation(rule)?);
            }
            validations.push_str("</dataValidations>");
            bytes = insert_sheet_child(
                &bytes,
                &validations,
                &[
                    "hyperlinks",
                    "printOptions",
                    "pageMargins",
                    "pageSetup",
                    "headerFooter",
                    "drawing",
                    "legacyDrawing",
                    "extLst",
                ],
            )?;
        }
        parts.insert(path, bytes);
    }
    for fragment in dxfs {
        crate::styles::append_dxf(&mut parts, "xl/styles.xml", fragment)?;
    }
    validate_office(&parts, OfficeKind::Workbook)?;
    write(&parts)
}
