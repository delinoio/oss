use serde_feather::FeatherDeserialize;

#[derive(FeatherDeserialize)]
enum GenericEnum<T> {
    Unit,
    Payload(T),
}

fn main() {}
