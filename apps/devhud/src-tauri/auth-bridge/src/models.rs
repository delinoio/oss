use serde::{Deserialize, Serialize};

#[derive(Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct EmptyRequest {}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SessionRecord {
    pub record: Option<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct WriteSessionRequest {
    pub record: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OpenAuthorizationRequest {
    pub url: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OperationResponse {
    pub completed: bool,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CallbackResponse {
    pub url: Option<String>,
}
