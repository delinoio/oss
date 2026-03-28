use rustia::LLMData;

#[derive(LLMData)]
struct InvalidTagAlias {
    #[rustia(tags(minLen(1)))]
    name: String,
}

fn main() {}
