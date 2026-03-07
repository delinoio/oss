use serde_feather::FeatherSerialize;

#[derive(FeatherSerialize)]
enum Shape {
    #[serde(skip_serializing)]
    Circle,
}

fn main() {}
