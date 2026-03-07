use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
struct DuplicateDefaultField {
    #[serde(default, default)]
    value: u8,
}

fn main() {}
