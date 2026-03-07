use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
struct AmbiguousTupleStruct(#[serde(default)] u8, u8);

#[derive(FeatherDeserialize)]
enum AmbiguousTupleVariant {
    Value(#[serde(default)] u8, u8),
}

fn main() {}
