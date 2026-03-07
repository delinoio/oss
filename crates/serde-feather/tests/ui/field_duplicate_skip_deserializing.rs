use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
struct DuplicateSkipDeserializingField {
    #[serde(skip_deserializing, skip_deserializing)]
    value: u8,
}

fn main() {}
