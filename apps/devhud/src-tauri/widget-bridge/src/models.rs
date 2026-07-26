use serde::{Deserialize, Serialize};

#[derive(Debug, Default, Serialize)]
pub struct EmptyRequest {}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct WriteConfigurationRequest {
    pub record: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ReadConfigurationResponse {
    pub record: Option<String>,
}

#[derive(Debug, Deserialize)]
pub struct PrepareResetResponse {
    pub prepared: bool,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct WidgetRefreshResponse {
    pub refreshed_widget_count: u32,
}
