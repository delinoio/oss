use serde_feather::FeatherSerialize;

#[derive(FeatherSerialize)]
struct UnsupportedAttributeModel {
    #[serde(flatten)]
    value: u8,
}

fn main() {}
