use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
enum Shape {
    Circle(#[serde(default)] u8),
}

fn main() {}
