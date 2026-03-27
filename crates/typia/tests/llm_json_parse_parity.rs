use typia::{LlmJsonParseResult, parse_lenient_json_value, serde_json::Value};

#[derive(Debug)]
enum Expected {
    Success(Value),
    FailureContains(&'static str),
}

#[derive(Debug)]
struct TestCase {
    upstream: &'static str,
    input: String,
    expected: Expected,
}

fn success(upstream: &'static str, input: impl Into<String>, expected: Value) -> TestCase {
    TestCase {
        upstream,
        input: input.into(),
        expected: Expected::Success(expected),
    }
}

fn failure(
    upstream: &'static str,
    input: impl Into<String>,
    expected_substring: &'static str,
) -> TestCase {
    TestCase {
        upstream,
        input: input.into(),
        expected: Expected::FailureContains(expected_substring),
    }
}

fn nested_array(value: Value, depth: usize) -> Value {
    let mut output = value;
    for _ in 0..depth {
        output = Value::Array(vec![output]);
    }
    output
}

#[test]
fn parse_lenient_json_upstream_parity_cases() {
    // Explicit parity exclusions (documented by design):
    // 1) JS `undefined` expectations are not representable in `serde_json::Value`.
    // 2) JS `Infinity` / `-Infinity` are not representable as JSON numbers.
    // 3) Lone-surrogate code-unit expectations differ because Rust `String` follows
    //    valid Unicode scalar value rules.

    let mut cases = vec![
        success(
            "test_llm_json_parse_lenient_bom_prefix.ts",
            "\u{FEFF}{\"name\":\"test\"}",
            serde_json::json!({ "name": "test" }),
        ),
        success(
            "test_llm_json_parse_lenient_boolean_coercion.ts",
            "YES",
            serde_json::json!(true),
        ),
        success(
            "test_llm_json_parse_lenient_comma_optional.ts",
            "{\"a\": 1 \"b\": 2}",
            serde_json::json!({ "a": 1, "b": 2 }),
        ),
        failure(
            "test_llm_json_parse_lenient_comment_only_input.ts",
            "// this is a comment",
            "JSON value",
        ),
        success(
            "test_llm_json_parse_lenient_comments.ts",
            "{\"name\": /* comment */ \"test\"}",
            serde_json::json!({ "name": "test" }),
        ),
        success(
            "test_llm_json_parse_lenient_comments_edge.ts",
            "{\"key\": 1 /* { } [ ] */}",
            serde_json::json!({ "key": 1 }),
        ),
        success(
            "test_llm_json_parse_lenient_consecutive_commas.ts",
            "[1,,,2]",
            serde_json::json!([1, 2]),
        ),
        success(
            "test_llm_json_parse_lenient_duplicate_keys.ts",
            "{\"key\": 1, \"key\": 2}",
            serde_json::json!({ "key": 2 }),
        ),
        success(
            "test_llm_json_parse_lenient_empty_containers.ts",
            "{\"obj\": {}, \"arr\": []}",
            serde_json::json!({ "obj": {}, "arr": [] }),
        ),
        failure(
            "test_llm_json_parse_lenient_error_output_format.ts",
            "{\"name\": invalid_token}",
            "JSON value",
        ),
        success(
            "test_llm_json_parse_lenient_escape_in_lenient_path.ts",
            "\"hello\\nworld",
            serde_json::json!("hello\nworld"),
        ),
        success(
            "test_llm_json_parse_lenient_escape_slash_sequences.ts",
            "{\"url\": \"http:\\/\\/example.com\"}",
            serde_json::json!({ "url": "http://example.com" }),
        ),
        success(
            "test_llm_json_parse_lenient_escape_standard_path.ts",
            "{\"quote\": \"\\\"\", \"tab\": \"\\t\"}",
            serde_json::json!({ "quote": "\"", "tab": "\t" }),
        ),
        success(
            "test_llm_json_parse_lenient_findJsonStart_comment_skip.ts",
            "// see {here}\n{\"key\": 1}",
            serde_json::json!({ "key": 1 }),
        ),
        success(
            "test_llm_json_parse_lenient_findJsonStart_junk_prefix.ts",
            "The result is: {\"value\": 42}",
            serde_json::json!({ "value": 42 }),
        ),
        success(
            "test_llm_json_parse_lenient_findJsonStart_junk_strings.ts",
            "He said \"{hello}\" then {\"real\": 1}",
            serde_json::json!({ "real": 1 }),
        ),
        failure(
            "test_llm_json_parse_lenient_identifier_keywords.ts",
            "{\"val\": TRUE}",
            "JSON value",
        ),
        success(
            "test_llm_json_parse_lenient_incomplete_keyword_context.ts",
            "{\"flag\": tru",
            serde_json::json!({ "flag": true }),
        ),
        success(
            "test_llm_json_parse_lenient_incomplete_keyword_followed.ts",
            "{\"active\": tru, \"name\": \"test\"}",
            serde_json::json!({ "active": true, "name": "test" }),
        ),
        failure(
            "test_llm_json_parse_lenient_invalid_object_key.ts",
            "{123key: \"value\"}",
            "string key",
        ),
        failure(
            "test_llm_json_parse_lenient_invalid_value.ts",
            "{\"key\": @invalid}",
            "JSON value",
        ),
        success(
            "test_llm_json_parse_lenient_llm_streaming.ts",
            "{\"name\": \"John\", \"age\": 3",
            serde_json::json!({ "name": "John", "age": 3 }),
        ),
        success(
            "test_llm_json_parse_lenient_markdown_advanced.ts",
            "```json   \n{\"key\": 1}\n```",
            serde_json::json!({ "key": 1 }),
        ),
        success(
            "test_llm_json_parse_lenient_markdown_block.ts",
            "Here is the result:\n\n```json\n{\"value\": 42}\n```",
            serde_json::json!({ "value": 42 }),
        ),
        success(
            "test_llm_json_parse_lenient_markdown_case_insensitive.ts",
            "```JSON\n{\"key\": 1}\n```",
            serde_json::json!({ "key": 1 }),
        ),
        failure(
            "test_llm_json_parse_lenient_markdown_edge.ts",
            "```json",
            "JSON value",
        ),
        success(
            "test_llm_json_parse_lenient_markdown_primitive.ts",
            "```json\n42\n```",
            serde_json::json!(42),
        ),
        success(
            "test_llm_json_parse_lenient_mixed_types_array.ts",
            "[1, \"hello\", true, null, {\"a\": 1}, [2, 3]]",
            serde_json::json!([1, "hello", true, null, { "a": 1 }, [2, 3]]),
        ),
        success(
            "test_llm_json_parse_lenient_mixed_unclosed_deep.ts",
            "{\"items\": [{\"name\": \"test\"",
            serde_json::json!({ "items": [{ "name": "test" }] }),
        ),
        success(
            "test_llm_json_parse_lenient_null_after_length2.ts",
            "nu",
            serde_json::json!(null),
        ),
        success(
            "test_llm_json_parse_lenient_number_edge_cases.ts",
            "{\"val\": 1E10}",
            serde_json::json!({ "val": 1.0e10 }),
        ),
        failure(
            "test_llm_json_parse_lenient_number_format_nonstandard.ts",
            "{\"val\": .5}",
            "JSON value",
        ),
        success(
            "test_llm_json_parse_lenient_number_in_lenient_path.ts",
            "{val: 3.14e2}",
            serde_json::json!({ "val": 314.0 }),
        ),
        success(
            "test_llm_json_parse_lenient_number_incomplete.ts",
            "{\"value\": 1e-",
            serde_json::json!({ "value": 0 }),
        ),
        failure(
            "test_llm_json_parse_lenient_object_mismatched_brackets.ts",
            "{\"a\": ]}",
            "string key",
        ),
        failure(
            "test_llm_json_parse_lenient_object_syntax_error.ts",
            "{\"key\":: \"value\"}",
            "':'",
        ),
        success(
            "test_llm_json_parse_lenient_object_value_missing.ts",
            "{\"key\":}",
            serde_json::json!({ "key": null }),
        ),
        success(
            "test_llm_json_parse_lenient_primitive_number.ts",
            "42",
            serde_json::json!(42),
        ),
        success(
            "test_llm_json_parse_lenient_primitive_precedence.ts",
            "true {\"key\": 1}",
            serde_json::json!(true),
        ),
        success(
            "test_llm_json_parse_lenient_primitive_string.ts",
            "\"hello\"",
            serde_json::json!("hello"),
        ),
        success(
            "test_llm_json_parse_lenient_single_char_inputs.ts",
            "-",
            serde_json::json!(0),
        ),
        success(
            "test_llm_json_parse_lenient_special_keys.ts",
            "{\"@#$%^&*()\": \"special\"}",
            serde_json::json!({ "@#$%^&*()": "special" }),
        ),
        success(
            "test_llm_json_parse_lenient_stall_guard_invalid_token.ts",
            "[}]",
            serde_json::json!([]),
        ),
        success(
            "test_llm_json_parse_lenient_standard_roundtrip.ts",
            "{\"a\":1,\"b\":[2,3],\"c\":true}",
            serde_json::json!({ "a": 1, "b": [2, 3], "c": true }),
        ),
        success(
            "test_llm_json_parse_lenient_string_boundary_escapes.ts",
            "{\"text\": \"path\\\\\"}",
            serde_json::json!({ "text": "path\\" }),
        ),
        success(
            "test_llm_json_parse_lenient_string_consecutive_escapes.ts",
            "{\"text\": \"\\n\\t\\r\\b\\f\"}",
            serde_json::json!({ "text": "\n\t\r\u{0008}\u{000C}" }),
        ),
        success(
            "test_llm_json_parse_lenient_string_control_chars.ts",
            "{\"text\": \"line1\nline2\"}",
            serde_json::json!({ "text": "line1\nline2" }),
        ),
        success(
            "test_llm_json_parse_lenient_string_multi_level_escape.ts",
            "{\"data\": \"{\\\"key\\\": \\\"value\\\"}\"}",
            serde_json::json!({ "data": "{\"key\": \"value\"}" }),
        ),
        success(
            "test_llm_json_parse_lenient_string_nested_json.ts",
            "{\"arr\": \"[1, 2, \\\"hello\\\"]\"}",
            serde_json::json!({ "arr": "[1, 2, \"hello\"]" }),
        ),
        success(
            "test_llm_json_parse_lenient_string_only_escapes.ts",
            "{\"t\": \"\\n\\n\\n\"}",
            serde_json::json!({ "t": "\n\n\n" }),
        ),
        success(
            "test_llm_json_parse_lenient_string_special_chars.ts",
            "{\"code\": \"const x = `hello`\"}",
            serde_json::json!({ "code": "const x = `hello`" }),
        ),
        success(
            "test_llm_json_parse_lenient_string_with_json_delimiters.ts",
            "{\"msg\": \"use { and } carefully\"}",
            serde_json::json!({ "msg": "use { and } carefully" }),
        ),
        success(
            "test_llm_json_parse_lenient_surrogate_pair_boundary.ts",
            "{\"t\": \"\\uD83D\\uDE00\"}",
            serde_json::json!({ "t": "😀" }),
        ),
        success(
            "test_llm_json_parse_lenient_trailing_junk.ts",
            "{\"name\": \"test\"} and some trailing text",
            serde_json::json!({ "name": "test" }),
        ),
        success(
            "test_llm_json_parse_lenient_truncation_array_systematic.ts",
            "[1, \"hello\", true, nu",
            serde_json::json!([1, "hello", true, null]),
        ),
        success(
            "test_llm_json_parse_lenient_truncation_object_systematic.ts",
            "{\"name\": \"John\", \"age\": 3",
            serde_json::json!({ "name": "John", "age": 3 }),
        ),
        success(
            "test_llm_json_parse_lenient_unicode_adjacent.ts",
            "{\"text\": \"\\u0041\\u0042\\u0043\"}",
            serde_json::json!({ "text": "ABC" }),
        ),
        success(
            "test_llm_json_parse_lenient_unicode_multiple_surrogates.ts",
            "{\"emoji\": \"\\uD83D\\uDE00\\uD83D\\uDE01\"}",
            serde_json::json!({ "emoji": "😀😁" }),
        ),
        success(
            "test_llm_json_parse_lenient_unicode_truncation_systematic.ts",
            "{\"t\": \"\\u004",
            serde_json::json!({ "t": "\\u004" }),
        ),
        success(
            "test_llm_json_parse_lenient_unquoted_keys.ts",
            "{name: \"John\"}",
            serde_json::json!({ "name": "John" }),
        ),
        success(
            "test_llm_json_parse_lenient_unquoted_keys_edge.ts",
            "{trueValue: 1}",
            serde_json::json!({ "trueValue": 1 }),
        ),
        success(
            "test_llm_json_parse_lenient_unquoted_keys_single_char.ts",
            "{$: 1}",
            serde_json::json!({ "$": 1 }),
        ),
        success(
            "test_llm_json_parse_lenient_whitespace_variations.ts",
            "  {  \"key\"  :  \"value\"  }  ",
            serde_json::json!({ "key": "value" }),
        ),
    ];

    cases.push(success(
        "test_llm_json_parse_lenient_deep_nesting_arrays.ts",
        "[[[[[[[[[[42]]]]]]]]]]",
        nested_array(serde_json::json!(42), 10),
    ));

    let long_str = "a".repeat(10_000);
    cases.push(success(
        "test_llm_json_parse_lenient_string_long.ts",
        format!("{{\"text\":\"{long_str}\"}}"),
        serde_json::json!({ "text": long_str }),
    ));

    let mut too_deep = String::new();
    for _ in 0..513 {
        too_deep.push('[');
    }
    too_deep.push('0');
    for _ in 0..513 {
        too_deep.push(']');
    }
    cases.push(failure(
        "test_llm_json_parse_lenient_max_depth.ts",
        too_deep,
        "max depth exceeded",
    ));

    assert_eq!(cases.len(), 66, "must cover all 66 upstream parse files");

    for case in cases {
        match (&case.expected, parse_lenient_json_value(&case.input)) {
            (Expected::Success(expected), LlmJsonParseResult::Success { data }) => {
                assert_eq!(data, *expected, "mismatch for {}", case.upstream);
            }
            (
                Expected::FailureContains(expected_substring),
                LlmJsonParseResult::Failure { input, errors, .. },
            ) => {
                assert_eq!(
                    input, case.input,
                    "failure input mismatch for {}",
                    case.upstream
                );
                assert!(
                    errors
                        .iter()
                        .any(|error| error.expected.contains(expected_substring)),
                    "expected an error containing '{}' for {} but got {:?}",
                    expected_substring,
                    case.upstream,
                    errors,
                );
            }
            (Expected::Success(_), other) => {
                panic!("expected success for {} but got {other:?}", case.upstream);
            }
            (Expected::FailureContains(_), other) => {
                panic!("expected failure for {} but got {other:?}", case.upstream);
            }
        }
    }
}
