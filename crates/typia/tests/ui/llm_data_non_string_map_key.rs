use std::collections::HashMap;

use typia::LLMData;

#[derive(LLMData)]
struct NonStringMapKey {
    #[typia(tags(values(tags(minLength(1)))))]
    names: HashMap<u32, String>,
}

fn main() {}
