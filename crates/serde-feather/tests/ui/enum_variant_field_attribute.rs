use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
enum Shape {
    Pair(#[serde(rename = "left")] u8, u8),
}

fn main() {}
