use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
enum Shape {
    #[serde(alias = "dup")]
    Circle,
    #[serde(rename = "dup")]
    Square,
}

fn main() {}
