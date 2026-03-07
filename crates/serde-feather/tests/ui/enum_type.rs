use serde_feather::FeatherSerialize;

#[derive(FeatherSerialize)]
#[serde(tag = "type")]
enum Shape {
    Circle,
}

fn main() {}
