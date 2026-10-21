use std::collections::BTreeMap;

use base64::{Engine, engine::general_purpose::STANDARD};
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use serde_json::Value;

use crate::Error;

/// Field types supported by Clairic.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum FieldType {
    Text,
    Number,
    Date,
    Time,
    Datetime,
    Duration,
    List,
    Estimate,
    Forecast,
}

/// Element type of a list field.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ListItemType {
    Text,
    Number,
    Date,
}

/// Estimate grid scale.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Scale {
    Linear,
    Log,
}

/// Time between forecast steps.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Interval {
    Step,
    Minute,
    Hour,
    Day,
    Week,
    Month,
    Quarter,
    Year,
}

/// Numeric bound or ISO date for an estimate.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum Bound {
    Number(f64),
    Date(String),
}

/// Calendar grid increment.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum CalendarStep {
    Month,
    Year,
}

/// Positive numeric increment (days for dates), or a calendar increment.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum Step {
    Number(f64),
    Calendar(CalendarStep),
}

/// Definition of one requested value. Use `new` and set the applicable options.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Field {
    #[serde(rename = "type")]
    pub kind: FieldType,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub instructions: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub unit: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub integer: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub decimals: Option<u8>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub items: Option<ListItemType>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub min: Option<Bound>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max: Option<Bound>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub step: Option<Step>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub scale: Option<Scale>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub null_threshold: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub horizon: Option<u8>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub interval: Option<Interval>,
}

impl Field {
    /// Create a field with all optional settings omitted.
    pub fn new(kind: FieldType) -> Self {
        Self {
            kind,
            instructions: None,
            unit: None,
            integer: None,
            decimals: None,
            items: None,
            min: None,
            max: None,
            step: None,
            scale: None,
            null_threshold: None,
            horizon: None,
            interval: None,
        }
    }

    /// Describe what the model should extract or predict.
    pub fn with_instructions(mut self, instructions: impl Into<String>) -> Self {
        self.instructions = Some(instructions.into());
        self
    }
}

/// MIME type of an embedded image.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum ImageType {
    #[serde(rename = "image/png")]
    Png,
    #[serde(rename = "image/jpeg")]
    Jpeg,
    #[serde(rename = "image/webp")]
    Webp,
}

/// Embedded image. Remote URLs are not supported by the API.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum Image {
    DataUrl(String),
    Base64 {
        content_type: ImageType,
        base64: String,
    },
}

impl Image {
    /// Encode bytes for upload. The server validates image contents.
    pub fn from_bytes(content_type: ImageType, bytes: &[u8]) -> Self {
        Self::Base64 {
            content_type,
            base64: STANDARD.encode(bytes),
        }
    }

    fn decoded_len(&self) -> Result<usize, Error> {
        let data = match self {
            Self::Base64 { base64, .. } => base64.as_str(),
            Self::DataUrl(url) => {
                let (header, data) = url
                    .split_once(',')
                    .ok_or_else(|| Error::validation("invalid image data URL"))?;
                if !matches!(
                    header.to_ascii_lowercase().as_str(),
                    "data:image/png;base64" | "data:image/jpeg;base64" | "data:image/webp;base64"
                ) {
                    return Err(Error::validation(
                        "image must be an embedded PNG, JPEG, or WebP",
                    ));
                }
                data
            }
        };
        // Bound allocation before decoding untrusted base64 input.
        if data.len() > 4 * 1024 * 1024 * 4 / 3 + 4 {
            return Err(Error::validation("image exceeds 4 MiB"));
        }
        let len = STANDARD
            .decode(data)
            .map_err(|_| Error::validation("invalid image base64"))?
            .len();
        if len == 0 || len > 4 * 1024 * 1024 {
            return Err(Error::validation("image must contain 1 byte to 4 MiB"));
        }
        Ok(len)
    }
}

/// Optional response details.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Options {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub include_quantiles: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub return_state_text: Option<bool>,
}

/// Model selector accepted by the upstream API.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
pub enum Model {
    #[default]
    #[serde(rename = "clairic")]
    Clairic,
    #[serde(rename = "@cf/cloudflare/clairic")]
    Qualified,
}

/// Inference input. Validation also runs automatically before sending.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Request {
    pub model: Model,
    #[serde(default)]
    pub state: String,
    pub fields: BTreeMap<String, Field>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub images: Vec<Image>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub reference_date: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub options: Option<Options>,
}

impl Request {
    /// Start a request. Add at least one field before calling `run`.
    pub fn new(state: impl Into<String>) -> Self {
        Self {
            model: Model::Clairic,
            state: state.into(),
            fields: BTreeMap::new(),
            images: vec![],
            reference_date: None,
            options: None,
        }
    }

    /// Insert or replace a named field.
    pub fn with_field(mut self, name: impl Into<String>, field: Field) -> Self {
        self.fields.insert(name.into(), field);
        self
    }

    /// Check documented structural constraints. Dates and field-specific
    /// semantics are validated by Cloudflare; this is not a complete server
    /// schema validator.
    pub fn validate(&self) -> Result<(), Error> {
        if self.state.is_empty() && self.images.is_empty() {
            return Err(Error::validation("state or images must be provided"));
        }
        if self.fields.is_empty() {
            return Err(Error::validation("at least one field is required"));
        }
        if self.images.len() > 4 {
            return Err(Error::validation("at most four images are allowed"));
        }
        let mut total = 0;
        for image in &self.images {
            total += image.decoded_len()?;
        }
        if total > 8 * 1024 * 1024 {
            return Err(Error::validation("images exceed 8 MiB in total"));
        }
        for field in self.fields.values() {
            if field.kind == FieldType::List && field.items.is_none() {
                return Err(Error::validation("list fields require items"));
            }
            if matches!(field.kind, FieldType::Estimate | FieldType::Forecast)
                && (field.min.is_none() || field.max.is_none())
            {
                return Err(Error::validation(
                    "estimate and forecast fields require min and max",
                ));
            }
            if field.decimals.is_some_and(|v| v > 6)
                || field.horizon.is_some_and(|v| !(1..=64).contains(&v))
                || field
                    .null_threshold
                    .is_some_and(|v| !v.is_finite() || v <= 0.0 || v > 1.0)
                || matches!(field.step, Some(Step::Number(v)) if !v.is_finite() || v <= 0.0)
            {
                return Err(Error::validation(
                    "field option is outside its supported range",
                ));
            }
            for bound in [&field.min, &field.max] {
                if matches!(bound, Some(Bound::Number(v)) if !v.is_finite()) {
                    return Err(Error::validation("numeric bounds must be finite"));
                }
            }
            match (&field.min, &field.max) {
                (Some(Bound::Number(min)), Some(Bound::Number(max))) if min >= max => {
                    return Err(Error::validation("min must be less than max"));
                }
                (Some(Bound::Number(_)), Some(Bound::Date(_)))
                | (Some(Bound::Date(_)), Some(Bound::Number(_))) => {
                    return Err(Error::validation("bounds must have the same type"));
                }
                _ => {}
            }
        }
        Ok(())
    }
}

/// One answer, retaining unspecified JSON shapes and future metadata.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Answer {
    /// String retained because the preview response schema does not constrain
    /// it.
    #[serde(rename = "type")]
    pub kind: String,
    pub value: Value,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub guess: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub p_null: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub confidence: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub span: Option<Vec<u64>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub spans: Option<Vec<Vec<u64>>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub mean: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub median: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub p10: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub p90: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub p_below: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub p_above: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub quantiles: Option<Vec<Value>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub steps: Option<Vec<Value>>,
    #[serde(flatten)]
    pub extra: BTreeMap<String, Value>,
}

impl Answer {
    /// Deserialize the value, including nullable values via `Option<T>`.
    pub fn value_as<T: DeserializeOwned>(&self) -> Result<T, serde_json::Error> {
        serde_json::from_value(self.value.clone())
    }
}

/// Token accounting from the model.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Usage {
    pub input_tokens: u64,
    pub output_tokens: u64,
    pub state_tokens: Option<u64>,
    pub image_tokens: Option<u64>,
    pub schema_tokens: Option<u64>,
    pub passes: Option<u64>,
}

/// Successful model result, unwrapped from the REST envelope.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Response {
    pub model: String,
    pub answers: BTreeMap<String, Answer>,
    pub truncated: bool,
    pub state_text: Option<String>,
    pub usage: Usage,
}
