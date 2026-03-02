use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
struct DuplicateWireNameModel {
    #[serde(rename = "id")]
    first: u8,
    #[serde(rename = "id")]
    second: u8,
}

fn main() {}
