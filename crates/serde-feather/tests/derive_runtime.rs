#![cfg(feature = "derive")]

use serde_feather::{
    serde::{
        self,
        de::{self, IntoDeserializer as _},
        Deserialize as _,
    },
    FeatherDeserialize, FeatherSerialize,
};
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

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
struct SeqSkipAlignmentModel {
    first: u8,
    #[serde(skip_deserializing)]
    legacy: u8,
    second: u8,
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
struct RawIdentifierModel {
    r#type: u8,
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
enum EnumModel {
    Unit,
    #[serde(rename = "payload")]
    Newtype(u8),
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
#[serde(rename = "renamed_enum")]
enum RenamedEnumModel {
    Unit,
    Newtype(u16),
}

#[derive(Debug, PartialEq, FeatherDeserialize)]
struct RequiredMapModel {
    required: u8,
}

#[derive(Debug, PartialEq, FeatherDeserialize)]
struct RequiredSeqModel {
    first: u8,
    second: u8,
}

#[derive(Debug, PartialEq, FeatherDeserialize)]
struct OnlySkippedModel {
    #[serde(skip_deserializing)]
    skipped_number: u8,
    #[serde(skip_deserializing)]
    skipped_flag: bool,
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
#[allow(non_camel_case_types)]
enum RawIdentifierEnumModel {
    r#type,
    r#yield(u8),
}

#[derive(Debug, Clone, Copy)]
struct NumericEnumDeserializer {
    variant_index: u32,
    payload: Option<u8>,
}

impl NumericEnumDeserializer {
    fn unit(variant_index: u32) -> Self {
        Self {
            variant_index,
            payload: None,
        }
    }

    fn newtype_u8(variant_index: u32, payload: u8) -> Self {
        Self {
            variant_index,
            payload: Some(payload),
        }
    }
}

#[derive(Debug, Clone, Copy)]
struct NumericEnumAccess {
    variant_index: u32,
    payload: Option<u8>,
}

#[derive(Debug, Clone, Copy)]
struct NumericVariantAccess {
    payload: Option<u8>,
}

impl<'de> de::Deserializer<'de> for NumericEnumDeserializer {
    type Error = de::value::Error;

    serde::forward_to_deserialize_any! {
        bool i8 i16 i32 i64 i128 u8 u16 u32 u64 u128 f32 f64 char str string
        bytes byte_buf option unit unit_struct newtype_struct seq tuple
        tuple_struct map struct identifier ignored_any
    }

    fn deserialize_any<V>(self, _visitor: V) -> Result<V::Value, Self::Error>
    where
        V: de::Visitor<'de>,
    {
        Err(de::Error::custom(
            "NumericEnumDeserializer only supports enum deserialization",
        ))
    }

    fn deserialize_enum<V>(
        self,
        _name: &str,
        _variants: &[&str],
        visitor: V,
    ) -> Result<V::Value, Self::Error>
    where
        V: de::Visitor<'de>,
    {
        visitor.visit_enum(NumericEnumAccess {
            variant_index: self.variant_index,
            payload: self.payload,
        })
    }
}

impl<'de> de::EnumAccess<'de> for NumericEnumAccess {
    type Error = de::value::Error;
    type Variant = NumericVariantAccess;

    fn variant_seed<V>(self, seed: V) -> Result<(V::Value, Self::Variant), Self::Error>
    where
        V: de::DeserializeSeed<'de>,
    {
        let key = seed.deserialize(self.variant_index.into_deserializer())?;
        Ok((
            key,
            NumericVariantAccess {
                payload: self.payload,
            },
        ))
    }
}

impl<'de> de::VariantAccess<'de> for NumericVariantAccess {
    type Error = de::value::Error;

    fn unit_variant(self) -> Result<(), Self::Error> {
        if self.payload.is_some() {
            return Err(de::Error::custom(
                "unit variant cannot contain a payload value",
            ));
        }

        Ok(())
    }

    fn newtype_variant_seed<T>(self, seed: T) -> Result<T::Value, Self::Error>
    where
        T: de::DeserializeSeed<'de>,
    {
        let payload = self.payload.ok_or_else(|| {
            de::Error::custom("newtype variant payload is missing in NumericEnumDeserializer")
        })?;
        seed.deserialize(payload.into_deserializer())
    }

    fn tuple_variant<V>(self, _len: usize, _visitor: V) -> Result<V::Value, Self::Error>
    where
        V: de::Visitor<'de>,
    {
        Err(de::Error::custom(
            "tuple variants are not supported in NumericEnumDeserializer",
        ))
    }

    fn struct_variant<V>(
        self,
        _fields: &'static [&'static str],
        _visitor: V,
    ) -> Result<V::Value, Self::Error>
    where
        V: de::Visitor<'de>,
    {
        Err(de::Error::custom(
            "struct variants are not supported in NumericEnumDeserializer",
        ))
    }
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

#[test]
fn deserializes_sequence_with_skip_deserializing_omission() {
    let values = vec![5_u8, 9_u8];
    let deserializer = serde_feather::serde::de::value::SeqDeserializer::<
        _,
        serde_feather::serde::de::value::Error,
    >::new(values.into_iter());

    let decoded = SeqSkipAlignmentModel::deserialize(deserializer)
        .expect("deserialize sequence with skip_deserializing omission");
    assert_eq!(
        decoded,
        SeqSkipAlignmentModel {
            first: 5,
            legacy: 0,
            second: 9,
        }
    );
}

#[test]
fn rejects_sequence_with_skip_deserializing_placeholder_value() {
    let values = vec![5_u8, 77_u8, 9_u8];
    let deserializer = serde_feather::serde::de::value::SeqDeserializer::<
        _,
        serde_feather::serde::de::value::Error,
    >::new(values.into_iter());

    let result = SeqSkipAlignmentModel::deserialize(deserializer);
    assert!(result.is_err(), "placeholder value should not be consumed");
}

#[test]
fn normalizes_raw_identifier_field_names() {
    let model = RawIdentifierModel { r#type: 3 };
    let encoded = serde_json::to_string(&model).expect("serialize raw identifier field");
    assert_eq!(encoded, r#"{"type":3}"#);

    let decoded: RawIdentifierModel =
        serde_json::from_str(r#"{"type":8}"#).expect("deserialize raw identifier field");
    assert_eq!(decoded, RawIdentifierModel { r#type: 8 });
}

#[test]
fn round_trip_enum_variants() {
    let unit_encoded = serde_json::to_string(&EnumModel::Unit).expect("serialize unit variant");
    assert_eq!(unit_encoded, r#""Unit""#);

    let newtype_encoded =
        serde_json::to_string(&EnumModel::Newtype(9)).expect("serialize newtype variant");
    assert_eq!(newtype_encoded, r#"{"payload":9}"#);

    let decoded_unit: EnumModel =
        serde_json::from_str(r#""Unit""#).expect("deserialize unit variant");
    assert_eq!(decoded_unit, EnumModel::Unit);

    let decoded_newtype: EnumModel =
        serde_json::from_str(r#"{"payload":9}"#).expect("deserialize newtype variant");
    assert_eq!(decoded_newtype, EnumModel::Newtype(9));
}

#[test]
fn enum_container_rename_changes_variant_tokens() {
    assert_tokens(
        &RenamedEnumModel::Unit,
        &[Token::UnitVariant {
            name: "renamed_enum",
            variant: "Unit",
        }],
    );

    assert_tokens(
        &RenamedEnumModel::Newtype(7),
        &[
            Token::NewtypeVariant {
                name: "renamed_enum",
                variant: "Newtype",
            },
            Token::U16(7),
        ],
    );
}

#[test]
fn deserializes_enum_from_numeric_discriminants() {
    let unit = EnumModel::deserialize(NumericEnumDeserializer::unit(0))
        .expect("deserialize unit variant from numeric discriminant");
    assert_eq!(unit, EnumModel::Unit);

    let newtype = EnumModel::deserialize(NumericEnumDeserializer::newtype_u8(1, 23))
        .expect("deserialize newtype variant from numeric discriminant");
    assert_eq!(newtype, EnumModel::Newtype(23));
}

#[test]
fn rejects_out_of_range_numeric_discriminants() {
    let error = EnumModel::deserialize(NumericEnumDeserializer::unit(7))
        .expect_err("out-of-range numeric discriminant should fail");
    let message = error.to_string();
    assert!(
        message.contains("invalid value"),
        "unexpected error for invalid numeric discriminant: {message}"
    );
}

#[test]
fn rejects_unknown_enum_variant() {
    let error = serde_json::from_str::<EnumModel>(r#""Missing""#)
        .expect_err("unknown enum variant should fail");
    let message = error.to_string();
    assert!(
        message.contains("unknown variant"),
        "unexpected error for unknown variant: {message}"
    );
}

#[test]
fn map_deserialization_ignores_unknown_fields() {
    let decoded: BasicModel = serde_json::from_str(
        r#"{
            "id": 9,
            "name": "known",
            "unknown_a": true,
            "unknown_b": "ignored"
        }"#,
    )
    .expect("deserialize map with unknown fields");
    assert_eq!(
        decoded,
        BasicModel {
            id: 9,
            name: "known".to_owned(),
        }
    );
}

#[test]
fn map_deserialization_rejects_duplicate_fields() {
    let error = serde_json::from_str::<RequiredMapModel>(r#"{"required": 1, "required": 2}"#)
        .expect_err("duplicate map fields should fail");
    let message = error.to_string();
    assert!(
        message.contains("duplicate field"),
        "unexpected error for duplicate field input: {message}"
    );
}

#[test]
fn map_deserialization_reports_missing_required_field() {
    let error = serde_json::from_str::<RequiredMapModel>(r#"{}"#)
        .expect_err("missing required field should fail");
    let message = error.to_string();
    assert!(
        message.contains("missing field"),
        "unexpected error for missing required field: {message}"
    );
}

#[test]
fn sequence_deserialization_reports_missing_required_element() {
    let values = vec![1_u8];
    let deserializer = serde_feather::serde::de::value::SeqDeserializer::<
        _,
        serde_feather::serde::de::value::Error,
    >::new(values.into_iter());

    let error = RequiredSeqModel::deserialize(deserializer)
        .expect_err("missing required sequence element should fail");
    let message = error.to_string();
    assert!(
        message.contains("invalid length"),
        "unexpected error for missing required sequence element: {message}"
    );
}

#[test]
fn sequence_deserialization_rejects_extra_elements() {
    let values = vec![1_u8, 2_u8, 3_u8];
    let deserializer = serde_feather::serde::de::value::SeqDeserializer::<
        _,
        serde_feather::serde::de::value::Error,
    >::new(values.into_iter());

    let error = RequiredSeqModel::deserialize(deserializer)
        .expect_err("extra sequence elements should fail");
    let message = error.to_string();
    assert!(
        message.contains("invalid length"),
        "unexpected error for extra sequence elements: {message}"
    );
}

#[test]
fn all_skip_deserializing_fields_default_from_map() {
    let decoded: OnlySkippedModel = serde_json::from_str(
        r#"{
            "skipped_number": 10,
            "skipped_flag": true,
            "unknown": "value"
        }"#,
    )
    .expect("deserialize model with only skipped fields");
    assert_eq!(
        decoded,
        OnlySkippedModel {
            skipped_number: 0,
            skipped_flag: false,
        }
    );
}

#[test]
fn all_skip_deserializing_fields_default_from_empty_sequence() {
    let values = Vec::<u8>::new();
    let deserializer = serde_feather::serde::de::value::SeqDeserializer::<
        _,
        serde_feather::serde::de::value::Error,
    >::new(values.into_iter());

    let decoded =
        OnlySkippedModel::deserialize(deserializer).expect("deserialize empty sequence input");
    assert_eq!(
        decoded,
        OnlySkippedModel {
            skipped_number: 0,
            skipped_flag: false,
        }
    );
}

#[test]
fn all_skip_deserializing_fields_reject_non_empty_sequence() {
    let values = vec![1_u8];
    let deserializer = serde_feather::serde::de::value::SeqDeserializer::<
        _,
        serde_feather::serde::de::value::Error,
    >::new(values.into_iter());

    let error = OnlySkippedModel::deserialize(deserializer)
        .expect_err("non-empty sequence should fail for skipped-only model");
    let message = error.to_string();
    assert!(
        message.contains("invalid length"),
        "unexpected error for skipped-only sequence input: {message}"
    );
}

#[test]
fn rejects_negative_numeric_discriminants() {
    let error = EnumModel::deserialize(serde_feather::serde::de::value::I64Deserializer::<
        serde_feather::serde::de::value::Error,
    >::new(-1))
    .expect_err("negative discriminant should fail");
    let message = error.to_string();
    assert!(
        message.contains("invalid value") || message.contains("invalid type"),
        "unexpected error for negative discriminant: {message}"
    );
}

#[test]
fn rejects_unit_variant_with_payload_from_numeric_discriminant() {
    let error = EnumModel::deserialize(NumericEnumDeserializer::newtype_u8(0, 1))
        .expect_err("unit variant with payload should fail");
    let message = error.to_string();
    assert!(
        message.contains("unit variant cannot contain a payload"),
        "unexpected error for payload on unit variant: {message}"
    );
}

#[test]
fn rejects_newtype_variant_without_payload_from_numeric_discriminant() {
    let error = EnumModel::deserialize(NumericEnumDeserializer::unit(1))
        .expect_err("newtype variant without payload should fail");
    let message = error.to_string();
    assert!(
        message.contains("payload is missing"),
        "unexpected error for missing payload on newtype variant: {message}"
    );
}

#[test]
fn rejects_newtype_variant_without_payload_from_json() {
    let error = serde_json::from_str::<EnumModel>(r#"{"payload":null}"#)
        .expect_err("null payload for newtype variant should fail");
    let message = error.to_string();
    assert!(
        message.contains("invalid type"),
        "unexpected error for null newtype payload: {message}"
    );
}

#[test]
fn normalizes_raw_identifier_enum_variant_names() {
    let unit_encoded = serde_json::to_string(&RawIdentifierEnumModel::r#type)
        .expect("serialize raw identifier unit variant");
    assert_eq!(unit_encoded, r#""type""#);

    let newtype_encoded = serde_json::to_string(&RawIdentifierEnumModel::r#yield(4))
        .expect("serialize raw identifier newtype variant");
    assert_eq!(newtype_encoded, r#"{"yield":4}"#);

    let decoded_unit: RawIdentifierEnumModel =
        serde_json::from_str(r#""type""#).expect("deserialize raw identifier unit variant");
    assert_eq!(decoded_unit, RawIdentifierEnumModel::r#type);

    let decoded_newtype: RawIdentifierEnumModel =
        serde_json::from_str(r#"{"yield":4}"#).expect("deserialize raw identifier newtype variant");
    assert_eq!(decoded_newtype, RawIdentifierEnumModel::r#yield(4));
}
