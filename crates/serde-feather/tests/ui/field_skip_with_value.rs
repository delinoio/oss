use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
struct SkipWithValueField {
    #[serde(skip = true)]
    value: u8,
}

fn main() {}
