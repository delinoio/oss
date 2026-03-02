use serde_feather::FeatherSerialize;

#[derive(FeatherSerialize)]
struct SkipAttributeOrderModel {
    #[serde(skip_serializing, skip)]
    value: u8,
}

fn main() {}
