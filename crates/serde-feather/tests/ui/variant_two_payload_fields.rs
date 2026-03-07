use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
enum VariantTwoPayloadFields {
    Value(u8, u8),
}

fn main() {}
