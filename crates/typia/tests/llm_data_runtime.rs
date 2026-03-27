use typia::{
    LLMData, LlmJsonParseResult,
    serde::{Deserialize, Serialize},
};

#[derive(Debug, PartialEq, Serialize, Deserialize, LLMData)]
struct User {
    id: u32,
    name: String,
}

#[derive(Debug, PartialEq, Serialize, Deserialize, LLMData)]
struct Flags {
    vip: bool,
}

#[derive(Debug, PartialEq, Serialize, Deserialize, LLMData)]
enum Command {
    Create { id: u32 },
    Delete(u32),
    Ping,
}

#[test]
fn parse_valid_json_fast_path() {
    let result = User::parse(r#"{"id":1,"name":"alice"}"#);
    match result {
        LlmJsonParseResult::Success { data } => {
            assert_eq!(
                data,
                User {
                    id: 1,
                    name: "alice".to_owned(),
                }
            );
        }
        other => panic!("expected success, got {other:?}"),
    }
}

#[test]
fn parse_markdown_code_block_with_prefix() {
    let input = "Here is your result:\n```json\n{\"id\":2,\"name\":\"bob\"}\n```";
    let result = User::parse(input);

    match result {
        LlmJsonParseResult::Success { data } => {
            assert_eq!(
                data,
                User {
                    id: 2,
                    name: "bob".to_owned(),
                }
            );
        }
        other => panic!("expected success, got {other:?}"),
    }
}

#[test]
fn parse_unquoted_keys_and_trailing_comma() {
    let result = User::parse("{id: 3, name: \"charlie\",}");

    match result {
        LlmJsonParseResult::Success { data } => {
            assert_eq!(
                data,
                User {
                    id: 3,
                    name: "charlie".to_owned(),
                }
            );
        }
        other => panic!("expected success, got {other:?}"),
    }
}

#[test]
fn parse_with_comments() {
    let input = "{\n  // user id\n  id: 4,\n  /* inline comment */\n  name: \"dana\",\n}";
    let result = User::parse(input);

    match result {
        LlmJsonParseResult::Success { data } => {
            assert_eq!(
                data,
                User {
                    id: 4,
                    name: "dana".to_owned(),
                }
            );
        }
        other => panic!("expected success, got {other:?}"),
    }
}

#[test]
fn parse_partial_keyword_recovery() {
    let result = Flags::parse("{ vip: tru }");

    match result {
        LlmJsonParseResult::Success { data } => {
            assert_eq!(data, Flags { vip: true });
        }
        other => panic!("expected success, got {other:?}"),
    }
}

#[test]
fn parse_unicode_surrogate_pair() {
    let result = User::parse(r#"{"id":5,"name":"\ud83d\ude00"}"#);

    match result {
        LlmJsonParseResult::Success { data } => {
            assert_eq!(
                data,
                User {
                    id: 5,
                    name: "😀".to_owned(),
                }
            );
        }
        other => panic!("expected success, got {other:?}"),
    }
}

#[test]
fn parse_incomplete_json_reports_failure_with_partial_data() {
    let result = User::parse(r#"{"id":1,"name":"alice""#);

    match result {
        LlmJsonParseResult::Failure { data, errors, .. } => {
            assert!(data.is_some(), "expected partial data");
            assert!(!errors.is_empty(), "expected parser errors");
            assert!(
                errors.iter().any(|error| error.expected.contains("'}'")),
                "expected missing object terminator error"
            );
        }
        other => panic!("expected failure, got {other:?}"),
    }
}

#[test]
fn parse_enforces_max_depth() {
    let mut input = String::new();
    for _ in 0..513 {
        input.push('[');
    }
    input.push('0');
    for _ in 0..513 {
        input.push(']');
    }

    let result = User::parse(&input);
    match result {
        LlmJsonParseResult::Failure { errors, .. } => {
            assert!(
                errors
                    .iter()
                    .any(|error| error.expected.contains("max depth exceeded")),
                "expected max depth error"
            );
        }
        other => panic!("expected failure, got {other:?}"),
    }
}

#[test]
fn parse_reports_serde_path_on_type_mismatch() {
    let result = User::parse(r#"{"id":"not-a-number","name":"alice"}"#);

    match result {
        LlmJsonParseResult::Failure { errors, data, .. } => {
            assert!(data.is_some(), "expected parsed JSON payload");
            assert!(
                errors.iter().any(|error| error.path.contains("$input.id")),
                "expected serde path for invalid field"
            );
        }
        other => panic!("expected failure, got {other:?}"),
    }
}

#[test]
fn enum_parse_success() {
    let result = Command::parse(r#"{"Create":{"id":7}}"#);

    match result {
        LlmJsonParseResult::Success { data } => {
            assert_eq!(data, Command::Create { id: 7 });
        }
        other => panic!("expected success, got {other:?}"),
    }
}

#[test]
fn validate_and_stringify_use_serde() {
    let value = typia::serde_json::json!({
        "id": 42,
        "name": "eve"
    });

    let validated = User::validate(value).expect("validation should succeed");
    assert_eq!(
        validated,
        User {
            id: 42,
            name: "eve".to_owned(),
        }
    );

    let encoded = validated.stringify().expect("stringify should succeed");
    assert_eq!(encoded, r#"{"id":42,"name":"eve"}"#);
}
