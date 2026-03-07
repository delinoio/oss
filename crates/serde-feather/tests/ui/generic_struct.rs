use serde_feather::FeatherDeserialize;

struct NotDeserialize;

#[derive(FeatherDeserialize)]
struct GenericModel<T> {
    value: T,
}

fn main() {
    let _ = serde_json::from_str::<GenericModel<NotDeserialize>>("{\"value\":1}");
}
