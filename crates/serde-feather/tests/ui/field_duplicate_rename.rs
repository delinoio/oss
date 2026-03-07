use serde_feather::FeatherSerialize;

#[derive(FeatherSerialize)]
struct DuplicateRenameField {
    #[serde(rename = "first", rename = "second")]
    value: u8,
}

fn main() {}
