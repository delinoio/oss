use typia::LLMData;

#[derive(LLMData)]
union InvalidUnion {
    value: u32,
}

fn main() {
    let _ = InvalidUnion { value: 1 };
}
