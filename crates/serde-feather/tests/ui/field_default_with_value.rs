use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
struct DefaultWithValueField {
    #[serde(default = "make_default")]
    value: u8,
}

fn make_default() -> u8 {
    42
}

fn main() {}
