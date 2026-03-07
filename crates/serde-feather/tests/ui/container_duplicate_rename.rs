use serde_feather::FeatherSerialize;

#[derive(FeatherSerialize)]
#[serde(rename = "first", rename = "second")]
struct DuplicateContainerRename {
    value: u8,
}

fn main() {}
