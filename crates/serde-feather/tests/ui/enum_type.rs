use serde_feather::{FeatherDeserialize, FeatherSerialize};

#[derive(FeatherSerialize, FeatherDeserialize)]
enum Shape {
    Circle { radius: u8 },
}

fn main() {}
