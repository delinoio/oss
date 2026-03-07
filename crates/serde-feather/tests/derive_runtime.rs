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

fn is_zero(value: &u8) -> bool {
    *value == 0
}

mod hex_u8 {
    use serde_feather::serde::{self, Deserialize as _};

    pub fn serialize<S>(value: &u8, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: serde::ser::Serializer,
    {
        let encoded = format!("{value:02x}");
        serializer.serialize_str(&encoded)
    }

    pub fn deserialize<'de, D>(deserializer: D) -> Result<u8, D::Error>
    where
        D: serde::de::Deserializer<'de>,
    {
        let encoded = String::deserialize(deserializer)?;
        u8::from_str_radix(&encoded, 16).map_err(serde::de::Error::custom)
    }
}

mod passthrough_with {
    use serde_feather::serde;

    pub fn serialize<S, T>(value: &T, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: serde::ser::Serializer,
        T: serde::ser::Serialize,
    {
        serde::ser::Serialize::serialize(value, serializer)
    }

    pub fn deserialize<'de, D, T>(deserializer: D) -> Result<T, D::Error>
    where
        D: serde::de::Deserializer<'de>,
        T: serde::de::Deserialize<'de>,
    {
        serde::de::Deserialize::deserialize(deserializer)
    }
}

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

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
struct UnitStructModel;

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
struct TupleStructModel(u8, #[serde(default)] u8, #[serde(skip_deserializing)] u8);

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
struct NewtypeTupleStructModel(u8);

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
enum ExtendedEnumModel {
    Tuple(u8, #[serde(default)] u8),
    #[serde(rename_all = "camelCase")]
    Named {
        first_field: u8,
        #[serde(rename = "forced_name")]
        second_field: u8,
    },
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
struct RuntimeHookModel {
    #[serde(with = "hex_u8", skip_serializing_if = "is_zero", default)]
    hex: u8,
    #[serde(skip_deserializing, default)]
    server_only: u8,
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
enum RuntimeHookEnum {
    Named {
        #[serde(with = "hex_u8")]
        code: u8,
        #[serde(skip_serializing_if = "is_zero", default)]
        count: u8,
    },
    Tuple(#[serde(with = "hex_u8")] u8, #[serde(default)] u8),
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
enum NewtypeSkipIfEnumModel {
    Value(#[serde(skip_serializing_if = "is_zero", default)] u8),
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
enum NewtypeSkipDirectionalEnumModel {
    SkipSer(#[serde(skip_serializing, default)] u8),
    SkipDe(#[serde(skip_deserializing, default)] u8),
    SkipBoth(#[serde(skip, default)] u8),
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
struct GenericEnvelope<'a, T, const N: usize> {
    #[serde(skip_deserializing, default)]
    marker: &'a str,
    value: T,
    payload: Vec<u8>,
    #[serde(skip, default)]
    phantom: std::marker::PhantomData<[u8; N]>,
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
struct BoundedEnvelope<T>
where
    T: Copy,
{
    value: T,
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
struct GenericWithStruct<T>
where
    T: Copy + serde::ser::Serialize + for<'de> serde::de::Deserialize<'de>,
{
    #[serde(with = "passthrough_with")]
    value: T,
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
enum GenericWithEnum<T>
where
    T: Copy + serde::ser::Serialize + for<'de> serde::de::Deserialize<'de>,
{
    Value(#[serde(with = "passthrough_with")] T),
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
#[serde(rename_all = "snake_case")]
enum AliasRenameEnum {
    FirstCase,
    #[serde(alias = "legacy_second_case")]
    SecondCase,
    #[serde(rename = "manual_case")]
    ThirdCase,
    #[serde(rename_all = "camelCase")]
    NamedPayload {
        first_field: u8,
        #[serde(rename = "forced_name")]
        second_field: u8,
    },
}

#[derive(Debug, PartialEq, FeatherSerialize, FeatherDeserialize)]
enum AliasNumericEnum {
    #[serde(alias = "zero_alias")]
    Zero,
    #[serde(alias = "one_alias")]
    One(u8),
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
fn supports_unit_and_tuple_struct_derives() {
    let unit_encoded = serde_json::to_string(&UnitStructModel).expect("serialize unit struct");
    assert_eq!(unit_encoded, "null");
    let unit_decoded: UnitStructModel =
        serde_json::from_str("null").expect("deserialize unit struct");
    assert_eq!(unit_decoded, UnitStructModel);

    let tuple = TupleStructModel(5, 7, 9);
    let tuple_encoded = serde_json::to_string(&tuple).expect("serialize tuple struct");
    assert_eq!(tuple_encoded, "[5,7,9]");

    let tuple_decoded: TupleStructModel =
        serde_json::from_str("[11,13]").expect("deserialize tuple struct");
    assert_eq!(tuple_decoded, TupleStructModel(11, 13, 0));
}

#[test]
fn serializes_newtype_tuple_struct_as_scalar() {
    let encoded =
        serde_json::to_string(&NewtypeTupleStructModel(7)).expect("serialize newtype tuple struct");
    assert_eq!(encoded, "7");

    let decoded: NewtypeTupleStructModel =
        serde_json::from_str("11").expect("deserialize newtype tuple struct");
    assert_eq!(decoded, NewtypeTupleStructModel(11));
}

#[test]
fn supports_tuple_and_named_enum_variants() {
    let tuple_value = ExtendedEnumModel::Tuple(7, 9);
    let tuple_encoded = serde_json::to_string(&tuple_value).expect("serialize tuple enum variant");
    assert_eq!(tuple_encoded, r#"{"Tuple":[7,9]}"#);
    let tuple_decoded: ExtendedEnumModel =
        serde_json::from_str(r#"{"Tuple":[7]}"#).expect("deserialize tuple enum variant");
    assert_eq!(tuple_decoded, ExtendedEnumModel::Tuple(7, 0));

    let named_value = ExtendedEnumModel::Named {
        first_field: 1,
        second_field: 2,
    };
    let named_encoded = serde_json::to_string(&named_value).expect("serialize named enum variant");
    assert_eq!(
        named_encoded,
        r#"{"Named":{"firstField":1,"forced_name":2}}"#
    );
    let named_decoded: ExtendedEnumModel =
        serde_json::from_str(r#"{"Named":{"firstField":4,"forced_name":8,"unknown":true}}"#)
            .expect("deserialize named enum variant");
    assert_eq!(
        named_decoded,
        ExtendedEnumModel::Named {
            first_field: 4,
            second_field: 8,
        }
    );
}

#[test]
fn supports_runtime_field_hooks() {
    let value = RuntimeHookModel {
        hex: 0,
        server_only: 99,
    };
    let encoded = serde_json::to_string(&value).expect("serialize runtime hook model");
    assert_eq!(encoded, r#"{"server_only":99}"#);

    let decoded: RuntimeHookModel = serde_json::from_str(r#"{"hex":"0f","server_only":55}"#)
        .expect("deserialize runtime hook model");
    assert_eq!(
        decoded,
        RuntimeHookModel {
            hex: 15,
            server_only: 0,
        }
    );
}

#[test]
fn supports_variant_field_hooks() {
    let named = RuntimeHookEnum::Named { code: 10, count: 0 };
    let named_encoded = serde_json::to_string(&named).expect("serialize named hook variant");
    assert_eq!(named_encoded, r#"{"Named":{"code":"0a"}}"#);

    let named_decoded: RuntimeHookEnum =
        serde_json::from_str(r#"{"Named":{"code":"10","count":3}}"#)
            .expect("deserialize named hook variant");
    assert_eq!(named_decoded, RuntimeHookEnum::Named { code: 16, count: 3 });

    let tuple = RuntimeHookEnum::Tuple(31, 7);
    let tuple_encoded = serde_json::to_string(&tuple).expect("serialize tuple hook variant");
    assert_eq!(tuple_encoded, r#"{"Tuple":["1f",7]}"#);

    let tuple_decoded: RuntimeHookEnum =
        serde_json::from_str(r#"{"Tuple":["2a"]}"#).expect("deserialize tuple hook variant");
    assert_eq!(tuple_decoded, RuntimeHookEnum::Tuple(42, 0));
}

#[test]
fn preserves_newtype_encoding_for_skip_serializing_if_variant_fields() {
    let encoded_non_zero =
        serde_json::to_string(&NewtypeSkipIfEnumModel::Value(5)).expect("serialize non-zero");
    assert_eq!(encoded_non_zero, r#"{"Value":5}"#);

    let encoded_zero =
        serde_json::to_string(&NewtypeSkipIfEnumModel::Value(0)).expect("serialize zero");
    assert_eq!(encoded_zero, r#"{"Value":0}"#);

    let decoded: NewtypeSkipIfEnumModel =
        serde_json::from_str(r#"{"Value":9}"#).expect("deserialize newtype skip-if variant");
    assert_eq!(decoded, NewtypeSkipIfEnumModel::Value(9));
}

#[test]
fn preserves_unit_encoding_for_skipped_newtype_variant_payloads() {
    let skip_ser_encoded = serde_json::to_string(&NewtypeSkipDirectionalEnumModel::SkipSer(5))
        .expect("serialize skip-serializing newtype variant");
    assert_eq!(skip_ser_encoded, r#""SkipSer""#);

    let skip_both_encoded = serde_json::to_string(&NewtypeSkipDirectionalEnumModel::SkipBoth(8))
        .expect("serialize skip-both newtype variant");
    assert_eq!(skip_both_encoded, r#""SkipBoth""#);

    let skip_ser_decoded: NewtypeSkipDirectionalEnumModel =
        serde_json::from_str(r#"{"SkipSer":7}"#)
            .expect("deserialize skip-serializing newtype variant from payload");
    assert_eq!(
        skip_ser_decoded,
        NewtypeSkipDirectionalEnumModel::SkipSer(7)
    );

    let skip_de_decoded: NewtypeSkipDirectionalEnumModel = serde_json::from_str(r#""SkipDe""#)
        .expect("deserialize skip-deserializing newtype variant from unit");
    assert_eq!(skip_de_decoded, NewtypeSkipDirectionalEnumModel::SkipDe(0));

    let skip_both_decoded: NewtypeSkipDirectionalEnumModel = serde_json::from_str(r#""SkipBoth""#)
        .expect("deserialize skip-both newtype variant from unit");
    assert_eq!(
        skip_both_decoded,
        NewtypeSkipDirectionalEnumModel::SkipBoth(0)
    );
}

#[test]
fn supports_generic_type_lifetime_and_const_derives() {
    let value = GenericEnvelope::<u16, 3> {
        marker: "marker",
        value: 7,
        payload: vec![1, 2, 3],
        phantom: std::marker::PhantomData,
    };
    let encoded = serde_json::to_string(&value).expect("serialize generic envelope");
    assert_eq!(
        encoded,
        r#"{"marker":"marker","value":7,"payload":[1,2,3]}"#
    );

    let decoded: GenericEnvelope<'static, u16, 3> =
        serde_json::from_str(r#"{"marker":"ignored","value":11,"payload":[4,5,6]}"#)
            .expect("deserialize generic envelope");
    assert_eq!(
        decoded,
        GenericEnvelope {
            marker: "",
            value: 11,
            payload: vec![4, 5, 6],
            phantom: std::marker::PhantomData,
        }
    );
}

#[test]
fn preserves_declared_generic_bounds_for_deserialize_helpers() {
    let value = BoundedEnvelope::<u16> { value: 17 };
    let encoded = serde_json::to_string(&value).expect("serialize bounded envelope");
    assert_eq!(encoded, r#"{"value":17}"#);

    let decoded: BoundedEnvelope<u16> =
        serde_json::from_str(r#"{"value":21}"#).expect("deserialize bounded envelope");
    assert_eq!(decoded, BoundedEnvelope { value: 21 });
}

#[test]
fn supports_with_wrappers_on_generic_structs_and_enums() {
    let struct_value = GenericWithStruct::<u8> { value: 9 };
    let struct_encoded =
        serde_json::to_string(&struct_value).expect("serialize generic with struct");
    assert_eq!(struct_encoded, r#"{"value":9}"#);

    let struct_decoded: GenericWithStruct<u8> =
        serde_json::from_str(r#"{"value":14}"#).expect("deserialize generic with struct");
    assert_eq!(struct_decoded, GenericWithStruct { value: 14 });

    let enum_value = GenericWithEnum::<u8>::Value(31);
    let enum_encoded = serde_json::to_string(&enum_value).expect("serialize generic with enum");
    assert_eq!(enum_encoded, r#"{"Value":31}"#);

    let enum_decoded: GenericWithEnum<u8> =
        serde_json::from_str(r#"{"Value":42}"#).expect("deserialize generic with enum");
    assert_eq!(enum_decoded, GenericWithEnum::Value(42));
}

#[test]
fn applies_rename_all_rename_override_and_alias() {
    let first_encoded =
        serde_json::to_string(&AliasRenameEnum::FirstCase).expect("serialize first case");
    assert_eq!(first_encoded, r#""first_case""#);

    let alias_decoded: AliasRenameEnum =
        serde_json::from_str(r#""legacy_second_case""#).expect("deserialize alias variant");
    assert_eq!(alias_decoded, AliasRenameEnum::SecondCase);

    let renamed_encoded =
        serde_json::to_string(&AliasRenameEnum::ThirdCase).expect("serialize renamed variant");
    assert_eq!(renamed_encoded, r#""manual_case""#);

    let named_encoded = serde_json::to_string(&AliasRenameEnum::NamedPayload {
        first_field: 9,
        second_field: 2,
    })
    .expect("serialize named payload variant");
    assert_eq!(
        named_encoded,
        r#"{"named_payload":{"firstField":9,"forced_name":2}}"#
    );
}

#[test]
fn supports_alias_with_numeric_discriminants() {
    let alias_decoded: AliasNumericEnum =
        serde_json::from_str(r#""zero_alias""#).expect("deserialize alias variant name");
    assert_eq!(alias_decoded, AliasNumericEnum::Zero);

    let numeric_unit = AliasNumericEnum::deserialize(NumericEnumDeserializer::unit(0))
        .expect("deserialize numeric unit variant");
    assert_eq!(numeric_unit, AliasNumericEnum::Zero);

    let numeric_newtype = AliasNumericEnum::deserialize(NumericEnumDeserializer::newtype_u8(1, 12))
        .expect("deserialize numeric newtype variant");
    assert_eq!(numeric_newtype, AliasNumericEnum::One(12));
}
