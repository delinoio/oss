use serde_feather::FeatherSerialize;

#[derive(FeatherSerialize)]
enum VariantDuplicateRename {
    #[serde(rename = "first", rename = "second")]
    Value,
}

fn main() {}
