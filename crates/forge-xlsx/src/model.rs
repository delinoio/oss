use std::collections::HashSet;

use forge_document::{Chart, Style};
use forge_tree_doc::{ErrorCode, Result, error};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Workbook {
    #[serde(default = "Uuid::now_v7")]
    pub id: Uuid,
    pub sheets: Vec<Sheet>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Sheet {
    #[serde(default = "Uuid::now_v7")]
    pub id: Uuid,
    pub name: String,
    #[serde(default)]
    pub cells: Vec<Cell>,
    #[serde(default)]
    pub merges: Vec<Range>,
    #[serde(default)]
    pub rows: Vec<RowDimension>,
    #[serde(default)]
    pub columns: Vec<ColumnDimension>,
    pub freeze: Option<Address>,
    pub autofilter: Option<Range>,
    #[serde(default)]
    pub conditional_formats: Vec<ConditionalFormat>,
    #[serde(default)]
    pub validations: Vec<Validation>,
    #[serde(default)]
    pub charts: Vec<PlacedChart>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Address {
    pub row: u32,
    pub column: u16,
}
impl Address {
    pub fn validate(self) -> Result<()> {
        if self.row >= 1_048_576 || self.column >= 16_384 {
            return error(
                ErrorCode::InvalidField,
                "cell/address",
                "Cell address exceeds worksheet dimensions",
            );
        }
        Ok(())
    }

    pub fn a1(self) -> String {
        let mut column = u32::from(self.column) + 1;
        let mut letters = String::new();
        while column > 0 {
            letters.insert(0, (b'A' + ((column - 1) % 26) as u8) as char);
            column = (column - 1) / 26;
        }
        format!("{letters}{}", self.row + 1)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Range {
    pub first: Address,
    pub last: Address,
}
impl Range {
    pub fn validate(self) -> Result<()> {
        self.first.validate()?;
        self.last.validate()?;
        if self.first.row > self.last.row || self.first.column > self.last.column {
            return error(
                ErrorCode::InvalidField,
                "range",
                "Range coordinates are reversed",
            );
        }
        Ok(())
    }

    pub fn a1(self) -> String {
        format!("{}:{}", self.first.a1(), self.last.a1())
    }

    pub fn overlaps(self, other: Self) -> bool {
        self.first.row <= other.last.row
            && other.first.row <= self.last.row
            && self.first.column <= other.last.column
            && other.first.column <= self.last.column
    }

    pub fn contains(self, cell: Address) -> bool {
        self.first.row <= cell.row
            && cell.row <= self.last.row
            && self.first.column <= cell.column
            && cell.column <= self.last.column
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Cell {
    #[serde(default = "Uuid::now_v7")]
    pub id: Uuid,
    pub address: Address,
    pub value: Value,
    pub format: Option<CellFormat>,
    pub hyperlink: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(
    tag = "type",
    content = "value",
    rename_all = "snake_case",
    deny_unknown_fields
)]
pub enum Value {
    Text(String),
    Number(f64),
    Boolean(bool),
    Date(String),
    Formula(Formula),
    Blank,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Formula {
    pub expression: String,
    pub cached: Option<CachedValue>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(
    tag = "type",
    content = "value",
    rename_all = "snake_case",
    deny_unknown_fields
)]
pub enum CachedValue {
    Number(f64),
    Text(String),
    Boolean(bool),
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CellFormat {
    #[serde(default)]
    pub style: Style,
    pub number_format: Option<String>,
    #[serde(default)]
    pub wrap: bool,
    #[serde(default)]
    pub border: bool,
}
impl CellFormat {
    pub fn validate(&self) -> Result<()> {
        self.style.validate()?;
        if let Some(value) = &self.number_format {
            forge_document::text(value)?;
        }
        Ok(())
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RowDimension {
    pub row: u32,
    pub height: f64,
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ColumnDimension {
    pub column: u16,
    pub width: f64,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PlacedChart {
    #[serde(default = "Uuid::now_v7")]
    pub id: Uuid,
    pub at: Address,
    pub width: u32,
    pub height: u32,
    pub alt: String,
    pub chart: Chart,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Comparison {
    Between,
    NotBetween,
    Equal,
    NotEqual,
    GreaterThan,
    LessThan,
    GreaterOrEqual,
    LessOrEqual,
}
impl Comparison {
    pub fn xml(self) -> &'static str {
        match self {
            Self::Between => "between",
            Self::NotBetween => "notBetween",
            Self::Equal => "equal",
            Self::NotEqual => "notEqual",
            Self::GreaterThan => "greaterThan",
            Self::LessThan => "lessThan",
            Self::GreaterOrEqual => "greaterThanOrEqual",
            Self::LessOrEqual => "lessThanOrEqual",
        }
    }

    pub fn arity(self) -> usize {
        if matches!(self, Self::Between | Self::NotBetween) {
            2
        } else {
            1
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ThresholdKind {
    Minimum,
    Maximum,
    Number,
    Percent,
    Percentile,
    Formula,
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Threshold {
    pub kind: ThresholdKind,
    pub value: Option<String>,
}
impl Threshold {
    pub fn validate(&self) -> Result<()> {
        if matches!(self.kind, ThresholdKind::Minimum | ThresholdKind::Maximum) {
            if self.value.is_some() {
                return error(
                    ErrorCode::InvalidField,
                    "threshold",
                    "Min/max thresholds have no value",
                );
            }
            return Ok(());
        }
        let value = self.value.as_ref().ok_or_else(|| {
            forge_tree_doc::Diagnostic::new(
                ErrorCode::InvalidField,
                "threshold",
                "Threshold value is required",
            )
        })?;
        forge_document::text(value)?;
        if self.kind == ThresholdKind::Formula {
            if value.is_empty() {
                return error(ErrorCode::InvalidField, "threshold", "Formula is empty");
            }
            return Ok(());
        }
        let number = value.parse::<f64>().ok().filter(|n| n.is_finite());
        if number.is_none()
            || matches!(
                self.kind,
                ThresholdKind::Percent | ThresholdKind::Percentile
            ) && !number.is_some_and(|n| (0.0..=100.0).contains(&n))
        {
            return error(
                ErrorCode::InvalidField,
                "threshold",
                "Invalid numeric threshold",
            );
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum IconSet {
    ThreeArrows,
    ThreeTrafficLights,
    FourArrows,
    FiveArrows,
}
impl IconSet {
    pub fn count(self) -> usize {
        match self {
            Self::ThreeArrows | Self::ThreeTrafficLights => 3,
            Self::FourArrows => 4,
            Self::FiveArrows => 5,
        }
    }

    pub fn xml(self) -> &'static str {
        match self {
            Self::ThreeArrows => "3Arrows",
            Self::ThreeTrafficLights => "3TrafficLights1",
            Self::FourArrows => "4Arrows",
            Self::FiveArrows => "5Arrows",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ConditionalFormat {
    #[serde(default = "Uuid::now_v7")]
    pub id: Uuid,
    pub range: Range,
    pub rule: ConditionalRule,
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case", deny_unknown_fields)]
pub enum ConditionalRule {
    CellValue {
        operator: Comparison,
        values: Vec<String>,
        format: CellFormat,
    },
    Formula {
        formula: String,
        format: CellFormat,
    },
    ColorScale {
        thresholds: Vec<Threshold>,
        colors: Vec<String>,
    },
    DataBar {
        minimum: Threshold,
        maximum: Threshold,
        color: String,
        #[serde(default)]
        hide_value: bool,
    },
    IconSet {
        icons: IconSet,
        thresholds: Vec<Threshold>,
        #[serde(default)]
        reverse: bool,
        #[serde(default)]
        hide_value: bool,
    },
}
impl ConditionalFormat {
    pub fn validate(&self) -> Result<()> {
        self.range.validate()?;
        match &self.rule {
            ConditionalRule::CellValue {
                operator,
                values,
                format,
            } => {
                if values.len() != operator.arity() {
                    return error(
                        ErrorCode::InvalidField,
                        "conditional_format",
                        "Incorrect comparison value count",
                    );
                }
                for value in values {
                    forge_document::text(value)?;
                }
                format.validate()?;
            }
            ConditionalRule::Formula { formula, format } => {
                validate_formula(formula)?;
                format.validate()?;
            }
            ConditionalRule::ColorScale { thresholds, colors } => {
                if !(2..=3).contains(&thresholds.len()) || thresholds.len() != colors.len() {
                    return error(
                        ErrorCode::InvalidField,
                        "conditional_format",
                        "Color scale requires two or three matching stops",
                    );
                }
                for stop in thresholds {
                    stop.validate()?;
                }
                for value in colors {
                    forge_document::color(value)?;
                }
            }
            ConditionalRule::DataBar {
                minimum,
                maximum,
                color,
                ..
            } => {
                minimum.validate()?;
                maximum.validate()?;
                forge_document::color(color)?;
            }
            ConditionalRule::IconSet {
                icons, thresholds, ..
            } => {
                if thresholds.len() != icons.count() {
                    return error(
                        ErrorCode::InvalidField,
                        "conditional_format",
                        "Icon threshold count does not match icon set",
                    );
                }
                for stop in thresholds {
                    stop.validate()?;
                }
            }
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ValidationKind {
    List,
    Integer,
    Decimal,
    Date,
    Time,
    TextLength,
    Custom,
}
impl ValidationKind {
    pub fn xml(self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Integer => "whole",
            Self::Decimal => "decimal",
            Self::Date => "date",
            Self::Time => "time",
            Self::TextLength => "textLength",
            Self::Custom => "custom",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Validation {
    #[serde(default = "Uuid::now_v7")]
    pub id: Uuid,
    pub range: Range,
    pub kind: ValidationKind,
    pub operator: Option<Comparison>,
    pub formulas: Vec<String>,
    #[serde(default)]
    pub allow_blank: bool,
    pub prompt: Option<String>,
    pub error: Option<String>,
}
impl Validation {
    pub fn validate(&self) -> Result<()> {
        self.range.validate()?;
        let arity = if matches!(self.kind, ValidationKind::List | ValidationKind::Custom) {
            1
        } else {
            self.operator.unwrap_or(Comparison::Between).arity()
        };
        if self.formulas.len() != arity {
            return error(
                ErrorCode::InvalidField,
                "validation",
                "Incorrect validation formula count",
            );
        }
        for formula in &self.formulas {
            validate_formula(formula)?;
        }
        for message in [&self.prompt, &self.error].into_iter().flatten() {
            forge_document::text(message)?;
            if message.chars().count() > 255 {
                return error(
                    ErrorCode::InvalidField,
                    "validation",
                    "Validation message exceeds 255 characters",
                );
            }
        }
        Ok(())
    }
}

pub fn validate_formula(value: &str) -> Result<()> {
    forge_document::text(value)?;
    if value.is_empty() || value.chars().count() > 8192 {
        return error(
            ErrorCode::InvalidField,
            "formula",
            "Formula must contain 1 to 8192 characters",
        );
    }
    Ok(())
}

pub fn validate(workbook: &Workbook) -> Result<()> {
    let mut ids = HashSet::new();
    let mut names = HashSet::new();
    let mut count = 0;
    let mut identity = |id: Uuid| -> Result<()> {
        count += 1;
        if count > 20_000 {
            return error(
                ErrorCode::ResourceLimit,
                "workbook",
                "Workbook exceeds 20000 nodes",
            );
        }
        if id.get_version_num() != 7 || !ids.insert(id) {
            return error(
                ErrorCode::DuplicateIdentity,
                "id",
                "Expected unique UUID-v7 identities",
            );
        }
        Ok(())
    };
    identity(workbook.id)?;
    if workbook.sheets.is_empty() {
        return error(
            ErrorCode::InvalidField,
            "sheets",
            "Workbook requires worksheets",
        );
    }
    for sheet in &workbook.sheets {
        forge_tree_doc::cancellation::checkpoint()?;
        identity(sheet.id)?;
        forge_document::text(&sheet.name)?;
        if sheet.name.is_empty()
            || sheet.name.chars().count() > 31
            || sheet.name.contains(['[', ']', ':', '*', '?', '/', '\\'])
            || sheet.name.starts_with('\'')
            || sheet.name.ends_with('\'')
            || !names.insert(sheet.name.to_lowercase())
        {
            return error(
                ErrorCode::InvalidField,
                "sheet/name",
                "Worksheet name is invalid or duplicated",
            );
        }
        let mut addresses = HashSet::new();
        for cell in &sheet.cells {
            forge_tree_doc::cancellation::checkpoint()?;
            identity(cell.id)?;
            cell.address.validate()?;
            if !addresses.insert((cell.address.row, cell.address.column)) {
                return error(
                    ErrorCode::DuplicateIdentity,
                    "cell",
                    "Duplicate cell address",
                );
            }
            if let Some(format) = &cell.format {
                format.validate()?;
            }
            if let Some(href) = &cell.hyperlink {
                forge_document::hyperlink(href)?;
                if !matches!(cell.value, Value::Text(_)) {
                    return error(
                        ErrorCode::InvalidField,
                        "cell/hyperlink",
                        "Hyperlinks require a text cell",
                    );
                }
            }
            match &cell.value {
                Value::Text(value) => {
                    forge_document::text(value)?;
                    if value.chars().count() > 32767 {
                        return error(
                            ErrorCode::ResourceLimit,
                            "cell/text",
                            "Cell text exceeds 32767 characters",
                        );
                    }
                }
                Value::Number(value) if !value.is_finite() => {
                    return error(
                        ErrorCode::InvalidField,
                        "cell/value",
                        "Cell number must be finite",
                    );
                }
                Value::Formula(formula) => {
                    validate_formula(&formula.expression)?;
                    match &formula.cached {
                        Some(CachedValue::Number(n)) if !n.is_finite() => {
                            return error(
                                ErrorCode::InvalidField,
                                "formula/cached",
                                "Cached value must be finite",
                            );
                        }
                        Some(CachedValue::Text(t)) => {
                            forge_document::text(t)?;
                            if t.chars().count() > 32767 {
                                return error(
                                    ErrorCode::ResourceLimit,
                                    "formula/cached",
                                    "Cached cell text exceeds 32767 characters",
                                );
                            }
                        }
                        _ => {}
                    }
                }
                Value::Date(date) => {
                    rust_xlsxwriter::ExcelDateTime::parse_from_str(date)
                        .map_err(forge_package::failure)?;
                }
                _ => {}
            }
        }
        // The writer materializes blank cells for merges. Bound their XML node
        // cost before expansion, even when the input model is only a few bytes.
        let mut expanded_cells = sheet.cells.len() as u64;
        for (i, merge) in sheet.merges.iter().enumerate() {
            forge_tree_doc::cancellation::checkpoint()?;
            merge.validate()?;
            expanded_cells += u64::from(merge.last.row - merge.first.row + 1)
                * u64::from(merge.last.column - merge.first.column + 1);
            if expanded_cells > 250_000 {
                return error(
                    ErrorCode::ResourceLimit,
                    "merge",
                    "Expanded worksheet exceeds the XML node budget",
                );
            }
            if merge.first == merge.last
                || sheet.merges[..i].iter().any(|other| merge.overlaps(*other))
            {
                return error(
                    ErrorCode::InvalidField,
                    "merge",
                    "Worksheet merges overlap or contain one cell",
                );
            }
            if sheet.cells.iter().any(|cell| {
                merge.contains(cell.address)
                    && cell.address != merge.first
                    && !matches!(cell.value, Value::Blank)
            }) {
                return error(
                    ErrorCode::InvalidField,
                    "merge",
                    "Merge would discard a populated cell",
                );
            }
        }
        if let Some(freeze) = sheet.freeze {
            freeze.validate()?;
        }
        if let Some(filter) = sheet.autofilter {
            filter.validate()?;
        }
        let mut rows = HashSet::new();
        for dimension in &sheet.rows {
            if !rows.insert(dimension.row) {
                return error(
                    ErrorCode::DuplicateIdentity,
                    "row",
                    "Duplicate row dimension",
                );
            }
            Address {
                row: dimension.row,
                column: 0,
            }
            .validate()?;
            if !dimension.height.is_finite() || dimension.height <= 0.0 || dimension.height > 409.0
            {
                return error(
                    ErrorCode::InvalidGeometry,
                    "row",
                    "Row height must be within (0,409]",
                );
            }
        }
        let mut columns = HashSet::new();
        for dimension in &sheet.columns {
            if !columns.insert(dimension.column) {
                return error(
                    ErrorCode::DuplicateIdentity,
                    "column",
                    "Duplicate column dimension",
                );
            }
            Address {
                row: 0,
                column: dimension.column,
            }
            .validate()?;
            if !dimension.width.is_finite() || dimension.width <= 0.0 || dimension.width > 255.0 {
                return error(
                    ErrorCode::InvalidGeometry,
                    "column",
                    "Column width must be within (0,255]",
                );
            }
        }
        for rule in &sheet.conditional_formats {
            identity(rule.id)?;
            rule.validate()?;
        }
        for rule in &sheet.validations {
            identity(rule.id)?;
            rule.validate()?;
        }
        for chart in &sheet.charts {
            identity(chart.id)?;
            chart.at.validate()?;
            chart.chart.validate()?;
            forge_document::text(&chart.alt)?;
            if chart.width == 0
                || chart.height == 0
                || chart.width > 100_000
                || chart.height > 100_000
            {
                return error(
                    ErrorCode::InvalidGeometry,
                    "chart",
                    "Chart dimensions are invalid",
                );
            }
        }
    }
    Ok(())
}
