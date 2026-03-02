#![cfg(feature = "derive")]

use serde_feather::{serde::Deserialize as _, FeatherDeserialize, FeatherSerialize};
use serde_test::{assert_tokens, Token};

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
struct BasicModel {
    id: u32,
    name: String,
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
#[serde(rename = "renamed_container")]
struct ContainerRenameModel {
    value: u8,
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
struct FieldBehaviorModel {
    #[serde(rename = "identifier")]
    id: u32,
    #[serde(default)]
    retries: u8,
    #[serde(skip_serializing)]
    skip_ser: u8,
    #[serde(skip_deserializing)]
    skip_de: u8,
    #[serde(skip)]
    skip_both: u8,
}

#[derive(Debug, PartialEq, FeatherDeserialize)]
struct SeqModel {
    first: u8,
    second: u8,
    #[serde(default)]
    third: u8,
}

#[derive(Debug, PartialEq, FeatherDeserialize)]
struct SkippedLeadingFieldModel {
    #[serde(skip_deserializing)]
    skipped: u8,
    required: u8,
}

#[test]
fn round_trip_without_attributes() {
    let value = BasicModel {
        id: 7,
        name: "feather".to_owned(),
    };

    let encoded = serde_json::to_string(&value).expect("serialize basic model");
    assert_eq!(encoded, r#"{"id":7,"name":"feather"}"#);

    let decoded: BasicModel = serde_json::from_str(&encoded).expect("deserialize basic model");
    assert_eq!(decoded, value);
}

#[test]
fn container_rename_changes_struct_name_tokens() {
    assert_tokens(
        &ContainerRenameModel { value: 1 },
        &[
            Token::Struct {
                name: "renamed_container",
                len: 1,
            },
            Token::Str("value"),
            Token::U8(1),
            Token::StructEnd,
        ],
    );
}

#[test]
fn field_attributes_apply_consistently() {
    let value = FieldBehaviorModel {
        id: 11,
        retries: 3,
        skip_ser: 19,
        skip_de: 23,
        skip_both: 29,
    };

    let encoded = serde_json::to_string(&value).expect("serialize field behavior model");
    assert_eq!(encoded, r#"{"identifier":11,"retries":3,"skip_de":23}"#);

    let decoded: FieldBehaviorModel = serde_json::from_str(
        r#"{
            "identifier": 41,
            "skip_ser": 59,
            "skip_de": 61,
            "unknown": true
        }"#,
    )
    .expect("deserialize field behavior model");

    assert_eq!(
        decoded,
        FieldBehaviorModel {
            id: 41,
            retries: 0,
            skip_ser: 59,
            skip_de: 0,
            skip_both: 0,
        }
    );
}

#[test]
fn deserializes_from_sequence_representation() {
    let values = vec![7_u8, 9_u8];
    let deserializer = serde_feather::serde::de::value::SeqDeserializer::<
        _,
        serde_feather::serde::de::value::Error,
    >::new(values.into_iter());

    let decoded = SeqModel::deserialize(deserializer).expect("deserialize from sequence");
    assert_eq!(
        decoded,
        SeqModel {
            first: 7,
            second: 9,
            third: 0,
        }
    );
}

#[test]
fn deserializes_map_with_skipped_leading_field() {
    let decoded: SkippedLeadingFieldModel = serde_json::from_str(r#"{"required": 33}"#)
        .expect("deserialize with skipped leading field");

    assert_eq!(
        decoded,
        SkippedLeadingFieldModel {
            skipped: 0,
            required: 33,
        }
    );
}
