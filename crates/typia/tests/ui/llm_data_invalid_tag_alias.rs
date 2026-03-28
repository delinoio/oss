use typia::LLMData;

#[derive(LLMData)]
struct InvalidTagAlias {
    #[typia(tags(minLen(1)))]
    name: String,
}

fn main() {}
