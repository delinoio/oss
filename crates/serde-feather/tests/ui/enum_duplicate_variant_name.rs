use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
enum Shape {
    #[serde(rename = "circle")]
    Circle,
    #[serde(rename = "circle")]
    Round,
}

fn main() {}
