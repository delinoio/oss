use typia::{
    IValidation, LLMData, LlmJsonParseResult, Validate,
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
fn parse_incomplete_json_recovers_to_success() {
    let result = User::parse(r#"{"id":1,"name":"alice""#);

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

    let validated = match User::validate(value) {
        IValidation::Success { data } => data,
        IValidation::Failure { errors, .. } => panic!("validation should succeed, got {errors:?}"),
    };
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

#[test]
fn validate_reports_missing_required_field() {
    let value = typia::serde_json::json!({
        "id": 7
    });

    match User::validate(value) {
        IValidation::Success { data } => panic!("validation should fail, got {data:?}"),
        IValidation::Failure { errors, .. } => {
            assert!(
                errors
                    .iter()
                    .any(|error| error.expected == "required property"),
                "expected missing field error"
            );
        }
    }
}

#[test]
fn validate_reports_type_mismatch() {
    let value = typia::serde_json::json!({
        "id": "not-a-number",
        "name": "eve"
    });

    match User::validate(value) {
        IValidation::Success { data } => panic!("validation should fail, got {data:?}"),
        IValidation::Failure { errors, .. } => {
            assert!(
                !errors.is_empty(),
                "expected at least one type mismatch error"
            );
        }
    }
}

#[test]
fn validate_equals_reports_extra_fields() {
    let value = typia::serde_json::json!({
        "id": 9,
        "name": "frank",
        "unexpected": true
    });

    match User::validate_equals(value) {
        IValidation::Success { data } => panic!("validation should fail, got {data:?}"),
        IValidation::Failure { errors, .. } => {
            assert!(
                errors.iter().any(|error| error.path == "$input.unexpected"),
                "expected extra field error"
            );
        }
    }
}

#[test]
fn stringify_roundtrip_through_validate() {
    let user = User {
        id: 9,
        name: "frank".to_owned(),
    };

    let encoded = user.stringify().expect("stringify should succeed");
    let decoded: typia::serde_json::Value =
        typia::serde_json::from_str(&encoded).expect("must be valid JSON");

    let validated = match User::validate(decoded) {
        IValidation::Success { data } => data,
        IValidation::Failure { errors, .. } => panic!("validation should succeed, got {errors:?}"),
    };
    assert_eq!(validated, user);
}

#[derive(Debug, PartialEq, Serialize, Deserialize, LLMData)]
struct TaggedPayload {
    #[typia(tags(minLength(1), maxLength(5), pattern("^[a-z]+$")))]
    name: String,
    #[typia(tags(minimum(1), maximum(10), multipleOf(1)))]
    score: i32,
    #[typia(tags(minItems(1), maxItems(3), uniqueItems(), items(tags(minLength(2)))))]
    tags: Vec<String>,
}

fn default_country() -> String {
    "KR".to_owned()
}

#[derive(Debug, PartialEq, Serialize, Deserialize, LLMData)]
struct SerdeDefaultPayload {
    id: u32,
    #[serde(default)]
    nickname: String,
    #[serde(default = "default_country")]
    country: String,
}

#[derive(Debug, PartialEq, Serialize, Deserialize, LLMData)]
#[serde(rename_all = "camelCase")]
struct SerdeRenameAllPayload {
    first_name: String,
    last_name: String,
}

#[derive(Debug, PartialEq, Serialize, Deserialize, LLMData)]
struct FlattenedAddress {
    city: String,
    country: String,
}

#[derive(Debug, PartialEq, Serialize, Deserialize, LLMData)]
struct FlattenedProfile {
    id: u32,
    #[serde(flatten)]
    address: FlattenedAddress,
}

#[derive(Debug, PartialEq, Serialize, Deserialize, LLMData)]
struct SignedNumericTagPayload {
    #[typia(tags(minimum(-1), maximum(3)))]
    value: i32,
}

#[test]
fn validate_collects_multiple_tag_errors() {
    let value = typia::serde_json::json!({
        "name": "TOO-LONG",
        "score": 11,
        "tags": ["x", "x", "ok", "more"]
    });

    match TaggedPayload::validate(value) {
        IValidation::Success { data } => panic!("validation should fail, got {data:?}"),
        IValidation::Failure { errors, .. } => {
            assert!(
                errors.iter().any(|error| error.path == "$input.name"),
                "expected name tag errors"
            );
            assert!(
                errors.iter().any(|error| error.path == "$input.score"),
                "expected score tag errors"
            );
            assert!(
                errors.iter().any(|error| error.path == "$input.tags"),
                "expected tags array-level tag errors"
            );
            assert!(
                errors.iter().any(|error| error.path == "$input.tags[0]"),
                "expected nested item tag errors"
            );
        }
    }
}

#[test]
fn validate_respects_serde_default_field_rules() {
    let value = typia::serde_json::json!({
        "id": 7
    });

    match SerdeDefaultPayload::validate(value) {
        IValidation::Success { data } => {
            assert_eq!(
                data,
                SerdeDefaultPayload {
                    id: 7,
                    nickname: String::new(),
                    country: "KR".to_owned(),
                }
            );
        }
        IValidation::Failure { errors, .. } => panic!("validation should succeed, got {errors:?}"),
    }
}

#[test]
fn validate_respects_serde_rename_all_for_field_lookup() {
    let value = typia::serde_json::json!({
        "firstName": "alice",
        "lastName": "smith"
    });

    match SerdeRenameAllPayload::validate_equals(value) {
        IValidation::Success { data } => {
            assert_eq!(
                data,
                SerdeRenameAllPayload {
                    first_name: "alice".to_owned(),
                    last_name: "smith".to_owned(),
                }
            );
        }
        IValidation::Failure { errors, .. } => panic!("validation should succeed, got {errors:?}"),
    }
}

#[test]
fn validate_equals_supports_serde_flatten_fields() {
    let value = typia::serde_json::json!({
        "id": 1,
        "city": "Seoul",
        "country": "KR"
    });

    match FlattenedProfile::validate_equals(value) {
        IValidation::Success { data } => {
            assert_eq!(
                data,
                FlattenedProfile {
                    id: 1,
                    address: FlattenedAddress {
                        city: "Seoul".to_owned(),
                        country: "KR".to_owned(),
                    },
                }
            );
        }
        IValidation::Failure { errors, .. } => panic!("validation should succeed, got {errors:?}"),
    }
}

#[test]
fn validate_accepts_signed_numeric_tag_literals() {
    let success = typia::serde_json::json!({
        "value": -1
    });
    match SignedNumericTagPayload::validate(success) {
        IValidation::Success { data } => {
            assert_eq!(data, SignedNumericTagPayload { value: -1 });
        }
        IValidation::Failure { errors, .. } => panic!("validation should succeed, got {errors:?}"),
    }

    let failure = typia::serde_json::json!({
        "value": -2
    });
    match SignedNumericTagPayload::validate(failure) {
        IValidation::Success { data } => panic!("validation should fail, got {data:?}"),
        IValidation::Failure { errors, .. } => {
            assert!(
                errors
                    .iter()
                    .any(|error| error.expected == "number & Minimum<-1>"),
                "expected minimum(-1) tag failure"
            );
        }
    }
}
