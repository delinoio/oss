#[test]
fn derive_llm_data_rejects_union() {
    let tests = trybuild::TestCases::new();
    tests.compile_fail("tests/ui/llm_data_union.rs");
    tests.compile_fail("tests/ui/llm_data_invalid_tag_alias.rs");
    tests.compile_fail("tests/ui/llm_data_invalid_tag_target.rs");
    tests.compile_fail("tests/ui/llm_data_non_string_map_key.rs");
}
