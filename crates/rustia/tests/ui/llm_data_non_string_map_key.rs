use std::collections::HashMap;

use rustia::LLMData;

#[derive(LLMData)]
struct NonStringMapKey {
    #[rustia(tags(values(tags(minLength(1)))))]
    names: HashMap<u32, String>,
}

fn main() {}
