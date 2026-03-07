use serde_feather::FeatherSerialize;

#[derive(FeatherSerialize)]
enum Shape {
    #[serde(alias = "circle")]
    Circle,
}

fn main() {}
