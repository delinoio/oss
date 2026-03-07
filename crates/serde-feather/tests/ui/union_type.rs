use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
union UnsupportedUnion {
    bytes: [u8; 4],
    value: u32,
}

fn main() {}
