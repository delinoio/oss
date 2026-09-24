//! Shared validated text, styles, assets and editable chart data. Each format
//! retains its own document model and decides its own layout and edit boundary.
#![forbid(unsafe_code)]
use std::{collections::BTreeMap, io::Cursor};

use forge_tree_doc::{ErrorCode, Result, error};
use serde::{Deserialize, Serialize};

pub mod fonts;

pub type Assets = BTreeMap<String, Vec<u8>>;

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Direction {
    #[default]
    Auto,
    Ltr,
    Rtl,
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Align {
    #[default]
    Left,
    Center,
    Right,
    Justify,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Style {
    pub font_family: Option<String>,
    pub font_size: Option<f64>,
    #[serde(default)]
    pub bold: bool,
    #[serde(default)]
    pub italic: bool,
    #[serde(default)]
    pub underline: bool,
    pub color: Option<String>,
    pub background: Option<String>,
    pub language: Option<String>,
    #[serde(default)]
    pub direction: Direction,
    #[serde(default)]
    pub align: Align,
}

impl Style {
    pub fn validate(&self) -> Result<()> {
        if self
            .font_size
            .is_some_and(|size| !size.is_finite() || size <= 0.0 || size > 4096.0)
        {
            return error(
                ErrorCode::InvalidField,
                "style/font_size",
                "Font size must be positive and at most 4096 points",
            );
        }
        for value in [&self.color, &self.background].into_iter().flatten() {
            color(value)?;
        }
        for value in [&self.font_family, &self.language].into_iter().flatten() {
            text(value)?;
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Run {
    pub text: String,
    #[serde(default)]
    pub style: Style,
    pub hyperlink: Option<String>,
}

impl Run {
    pub fn validate(&self) -> Result<()> {
        text(&self.text)?;
        self.style.validate()?;
        if let Some(target) = &self.hyperlink {
            hyperlink(target)?;
        }
        Ok(())
    }
}

pub fn text(value: &str) -> Result<()> {
    if value.chars().any(|c| !matches!(c, '\t' | '\n' | '\r' | '\u{20}'..='\u{d7ff}' | '\u{e000}'..='\u{fffd}' | '\u{10000}'..='\u{10ffff}')) {
        return error(ErrorCode::InvalidField, "text", "Text contains a forbidden XML character");
    }
    Ok(())
}

pub fn color(value: &str) -> Result<u32> {
    if value.len() != 7 || !value.starts_with('#') {
        return error(ErrorCode::InvalidField, "color", "Color must be #RRGGBB");
    }
    u32::from_str_radix(&value[1..], 16).map_err(|_| {
        forge_tree_doc::Diagnostic::new(ErrorCode::InvalidField, "color", "Color must be #RRGGBB")
    })
}

pub fn hyperlink(value: &str) -> Result<()> {
    text(value)?;
    let valid = url::Url::parse(value).ok().is_some_and(|url| {
        matches!(url.scheme(), "https" | "http" | "mailto")
            && url.username().is_empty()
            && url.password().is_none()
    });
    if !valid {
        return error(
            ErrorCode::InvalidField,
            "hyperlink",
            "Link must use HTTP, HTTPS or mailto without credentials",
        );
    }
    Ok(())
}

pub fn image(bytes: &[u8]) -> Result<(&'static str, u32, u32)> {
    if bytes.len() > 64 * 1024 * 1024 {
        return error(ErrorCode::ResourceLimit, "image", "Image exceeds 64 MiB");
    }
    let format = image::guess_format(bytes).map_err(|_| forge_package::failure("image"))?;
    let extension = match format {
        image::ImageFormat::Png => "png",
        image::ImageFormat::Jpeg => "jpeg",
        _ => {
            return error(
                ErrorCode::InvalidField,
                "image",
                "Only PNG and JPEG images are supported",
            );
        }
    };
    let reader = image::ImageReader::with_format(Cursor::new(bytes), format);
    let (width, height) = reader
        .into_dimensions()
        .map_err(|_| forge_package::failure("image dimensions"))?;
    if width == 0 || height == 0 || u64::from(width) * u64::from(height) > 64_000_000 {
        return error(
            ErrorCode::ResourceLimit,
            "image",
            "Image exceeds 64 million pixels",
        );
    }
    // Decode once under image's allocation limits so corrupt compressed payloads
    // never become apparently valid Office image parts.
    let mut reader = image::ImageReader::with_format(Cursor::new(bytes), format);
    let mut limits = image::Limits::default();
    limits.max_alloc = Some(256 * 1024 * 1024);
    reader.limits(limits);
    reader
        .decode()
        .map_err(|_| forge_package::failure("image payload"))?;
    Ok((extension, width, height))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ChartKind {
    Bar,
    Line,
    Pie,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Series {
    pub name: String,
    pub values: Vec<f64>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Chart {
    pub kind: ChartKind,
    pub title: Option<String>,
    pub categories: Vec<String>,
    pub series: Vec<Series>,
    #[serde(default = "yes")]
    pub legend: bool,
    #[serde(default)]
    pub labels: bool,
}
fn yes() -> bool {
    true
}

impl Chart {
    pub fn validate(&self) -> Result<()> {
        if self.categories.is_empty()
            || self.categories.len() > 20_000
            || self.series.is_empty()
            || self.series.len() > 255
        {
            return error(
                ErrorCode::InvalidField,
                "chart",
                "Chart requires bounded nonempty categories and series",
            );
        }
        if self.kind == ChartKind::Pie && self.series.len() != 1 {
            return error(
                ErrorCode::InvalidField,
                "chart/series",
                "A pie chart requires exactly one series",
            );
        }
        for category in &self.categories {
            text(category)?;
        }
        if let Some(title) = &self.title {
            text(title)?;
        }
        for series in &self.series {
            text(&series.name)?;
            if series.values.len() != self.categories.len()
                || series.values.iter().any(|v| !v.is_finite())
            {
                return error(
                    ErrorCode::InvalidField,
                    "chart/series",
                    "Chart series must have one finite value per category",
                );
            }
        }
        Ok(())
    }

    /// Produce a native chart plus its editable data workbook. The consumer
    /// binds externalData to this workbook within its own package namespace.
    pub fn office_parts(&self) -> Result<(Vec<u8>, Vec<u8>)> {
        use rust_xlsxwriter::{Chart as NativeChart, ChartDataLabel, ChartType, Workbook};
        self.validate()?;
        let mut workbook = Workbook::new();
        let worksheet = workbook.add_worksheet();
        worksheet.set_name("Data").map_err(forge_package::failure)?;
        for (index, category) in self.categories.iter().enumerate() {
            worksheet
                .write_string(index as u32 + 1, 0, category)
                .map_err(forge_package::failure)?;
        }
        let mut chart = NativeChart::new(match self.kind {
            ChartKind::Bar => ChartType::Column,
            ChartKind::Line => ChartType::Line,
            ChartKind::Pie => ChartType::Pie,
        });
        for (index, series) in self.series.iter().enumerate() {
            worksheet
                .write_string(0, index as u16 + 1, &series.name)
                .map_err(forge_package::failure)?;
            for (row, value) in series.values.iter().enumerate() {
                worksheet
                    .write_number(row as u32 + 1, index as u16 + 1, *value)
                    .map_err(forge_package::failure)?;
            }
            let series = chart
                .add_series()
                .set_name(("Data", 0, index as u16 + 1))
                .set_categories(("Data", 1, 0, self.categories.len() as u32, 0))
                .set_values((
                    "Data",
                    1,
                    index as u16 + 1,
                    self.categories.len() as u32,
                    index as u16 + 1,
                ));
            if self.labels {
                series.set_data_label(&ChartDataLabel::new().show_value());
            }
        }
        if let Some(title) = &self.title {
            chart.title().set_name(title);
        }
        if !self.legend {
            chart.legend().set_hidden();
        }
        worksheet
            .insert_chart(0, self.series.len() as u16 + 3, &chart)
            .map_err(forge_package::failure)?;
        let bytes = workbook.save_to_buffer().map_err(forge_package::failure)?;
        let parts = forge_package::read(&bytes)?;
        let chart = parts
            .get("xl/charts/chart1.xml")
            .ok_or_else(|| forge_package::failure("chart"))?
            .clone();
        Ok((chart, bytes))
    }
}
pub mod geometry;
