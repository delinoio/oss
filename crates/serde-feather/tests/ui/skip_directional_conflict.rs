use serde_feather::FeatherSerialize;

#[derive(FeatherSerialize)]
struct SkipDirectionalConflictModel {
    #[serde(skip_serializing, skip_deserializing)]
    value: u8,
}

fn main() {}
