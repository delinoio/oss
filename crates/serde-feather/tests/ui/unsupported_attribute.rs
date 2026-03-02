use serde_feather::FeatherSerialize;

#[derive(FeatherSerialize)]
struct UnsupportedAttributeModel {
    #[serde(skip_serializing_if = "Option::is_none")]
    maybe: Option<u8>,
}

fn main() {}
