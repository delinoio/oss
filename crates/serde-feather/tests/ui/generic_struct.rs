use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
struct GenericModel<T> {
    value: T,
}

fn main() {}
