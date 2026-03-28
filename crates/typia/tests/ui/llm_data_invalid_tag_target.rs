use typia::LLMData;

#[derive(LLMData)]
struct InvalidTagTarget {
    #[typia(tags(minLength(1)))]
    id: u32,
}

fn main() {}
