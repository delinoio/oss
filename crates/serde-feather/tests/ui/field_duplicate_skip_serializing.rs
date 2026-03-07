use serde_feather::FeatherSerialize;

#[derive(FeatherSerialize)]
struct DuplicateSkipSerializingField {
    #[serde(skip_serializing, skip_serializing)]
    value: u8,
}

fn main() {}
