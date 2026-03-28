use rustia::LLMData;

#[derive(LLMData)]
struct InvalidTagTarget {
    #[rustia(tags(minLength(1)))]
    id: u32,
}

fn main() {}
