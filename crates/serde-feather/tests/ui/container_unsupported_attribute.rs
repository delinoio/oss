use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
#[serde(rename_all = "snake_case")]
struct ContainerUnsupportedAttribute {
    value: u8,
}

fn main() {}
