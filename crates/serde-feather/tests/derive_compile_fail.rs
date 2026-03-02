#![cfg(feature = "derive")]

#[test]
fn rejects_unsupported_inputs() {
    let tests = trybuild::TestCases::new();
    tests.compile_fail("tests/ui/*.rs");
}
