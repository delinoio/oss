#![forbid(unsafe_code)]

//! Proc-macro derive implementation for `serde-feather`.

use std::collections::HashMap;

use proc_macro::TokenStream;
use proc_macro2::{Span, TokenStream as TokenStream2};
use proc_macro_crate::{crate_name, FoundCrate};
use quote::{format_ident, quote};
use syn::{
    ext::IdentExt, parse_macro_input, spanned::Spanned, Attribute, Data, DeriveInput, Field,
    Fields, Ident, LitStr,
};

#[proc_macro_derive(FeatherSerialize, attributes(serde))]
pub fn derive_feather_serialize(input: TokenStream) -> TokenStream {
    let input = parse_macro_input!(input as DeriveInput);
    match expand_serialize(&input) {
        Ok(output) => output.into(),
        Err(error) => error.into_compile_error().into(),
    }
}

#[proc_macro_derive(FeatherDeserialize, attributes(serde))]
pub fn derive_feather_deserialize(input: TokenStream) -> TokenStream {
    let input = parse_macro_input!(input as DeriveInput);
    match expand_deserialize(&input) {
        Ok(output) => output.into(),
        Err(error) => error.into_compile_error().into(),
    }
}

struct ContainerAttrOptions {
    rename: Option<LitStr>,
}

struct VariantAttrOptions {
    rename: Option<LitStr>,
}

#[derive(Default)]
struct FieldAttrOptions {
    rename: Option<LitStr>,
    default: bool,
    skip_serializing: bool,
    skip_deserializing: bool,
}

struct ParsedField {
    ident: Ident,
    ty: syn::Type,
    serialized_name: LitStr,
    default: bool,
    skip_serializing: bool,
    skip_deserializing: bool,
}

struct ParsedStruct {
    ident: Ident,
    struct_name: LitStr,
    fields: Vec<ParsedField>,
}

struct ParsedEnum {
    ident: Ident,
    enum_name: LitStr,
    variants: Vec<ParsedEnumVariant>,
}

struct ParsedEnumVariant {
    ident: Ident,
    serialized_name: LitStr,
    kind: ParsedEnumVariantKind,
}

enum ParsedEnumVariantKind {
    Unit,
    Newtype { ty: Box<syn::Type> },
}

enum ParsedInput {
    Struct(ParsedStruct),
    Enum(ParsedEnum),
}

#[derive(Clone, Copy)]
enum WireDirection {
    Serialize,
    Deserialize,
}

impl WireDirection {
    fn includes(self, field: &ParsedField) -> bool {
        match self {
            Self::Serialize => !field.skip_serializing,
            Self::Deserialize => !field.skip_deserializing,
        }
    }

    fn name(self) -> &'static str {
        match self {
            Self::Serialize => "serialization",
            Self::Deserialize => "deserialization",
        }
    }
}

fn expand_serialize(input: &DeriveInput) -> syn::Result<TokenStream2> {
    let parsed = parse_input(input, "FeatherSerialize")?;
    let crate_path = serde_feather_path();

    match parsed {
        ParsedInput::Struct(parsed_struct) => expand_serialize_struct(&parsed_struct, &crate_path),
        ParsedInput::Enum(parsed_enum) => expand_serialize_enum(&parsed_enum, &crate_path),
    }
}

fn expand_serialize_struct(
    parsed: &ParsedStruct,
    crate_path: &TokenStream2,
) -> syn::Result<TokenStream2> {
    let included_fields: Vec<&ParsedField> = parsed
        .fields
        .iter()
        .filter(|field| !field.skip_serializing)
        .collect();

    let field_count = included_fields.len();
    let serialize_fields = included_fields.into_iter().map(|field| {
        let field_ident = &field.ident;
        let field_name = &field.serialized_name;
        quote! {
            #crate_path::serde::ser::SerializeStruct::serialize_field(
                &mut state,
                #field_name,
                &self.#field_ident,
            )?;
        }
    });

    let struct_ident = &parsed.ident;
    let struct_name = &parsed.struct_name;

    Ok(quote! {
        impl #crate_path::serde::ser::Serialize for #struct_ident {
            fn serialize<S>(
                &self,
                serializer: S,
            ) -> ::core::result::Result<S::Ok, S::Error>
            where
                S: #crate_path::serde::ser::Serializer,
            {
                let mut state = #crate_path::serde::ser::Serializer::serialize_struct(
                    serializer,
                    #struct_name,
                    #field_count,
                )?;
                #(#serialize_fields)*
                #crate_path::serde::ser::SerializeStruct::end(state)
            }
        }
    })
}

fn expand_serialize_enum(
    parsed: &ParsedEnum,
    crate_path: &TokenStream2,
) -> syn::Result<TokenStream2> {
    let enum_ident = &parsed.ident;
    let enum_name = &parsed.enum_name;

    let serialize_match_arms = parsed
        .variants
        .iter()
        .enumerate()
        .map(|(variant_index, variant)| {
            let variant_index = variant_index as u32;
            let variant_ident = &variant.ident;
            let variant_name = &variant.serialized_name;
            match &variant.kind {
                ParsedEnumVariantKind::Unit => quote! {
                    Self::#variant_ident => #crate_path::serde::ser::Serializer::serialize_unit_variant(
                        serializer,
                        #enum_name,
                        #variant_index,
                        #variant_name,
                    )
                },
                ParsedEnumVariantKind::Newtype { .. } => quote! {
                    Self::#variant_ident(__feather_value) => #crate_path::serde::ser::Serializer::serialize_newtype_variant(
                        serializer,
                        #enum_name,
                        #variant_index,
                        #variant_name,
                        __feather_value,
                    )
                },
            }
        });

    Ok(quote! {
        impl #crate_path::serde::ser::Serialize for #enum_ident {
            fn serialize<S>(
                &self,
                serializer: S,
            ) -> ::core::result::Result<S::Ok, S::Error>
            where
                S: #crate_path::serde::ser::Serializer,
            {
                match self {
                    #(#serialize_match_arms,)*
                }
            }
        }
    })
}

fn expand_deserialize(input: &DeriveInput) -> syn::Result<TokenStream2> {
    let parsed = parse_input(input, "FeatherDeserialize")?;
    let crate_path = serde_feather_path();

    match parsed {
        ParsedInput::Struct(parsed_struct) => {
            expand_deserialize_struct(&parsed_struct, &crate_path)
        }
        ParsedInput::Enum(parsed_enum) => expand_deserialize_enum(&parsed_enum, &crate_path),
    }
}

fn expand_deserialize_struct(
    parsed: &ParsedStruct,
    crate_path: &TokenStream2,
) -> syn::Result<TokenStream2> {
    struct DeserBinding {
        field_index: usize,
        binding_ident: Ident,
        field_name: LitStr,
        field_ty: syn::Type,
        default: bool,
    }

    let mut bindings = Vec::<DeserBinding>::new();
    for (index, field) in parsed.fields.iter().enumerate() {
        if field.skip_deserializing {
            continue;
        }

        bindings.push(DeserBinding {
            field_index: index,
            binding_ident: format_ident!("__feather_field_{index}"),
            field_name: field.serialized_name.clone(),
            field_ty: field.ty.clone(),
            default: field.default,
        });
    }

    let field_bindings: Vec<TokenStream2> = bindings
        .iter()
        .map(|binding| {
            let binding_ident = &binding.binding_ident;
            let field_ty = &binding.field_ty;
            quote! { let mut #binding_ident: ::core::option::Option<#field_ty> = ::core::option::Option::None; }
        })
        .collect();
    let field_bindings_in_map = field_bindings.clone();
    let field_bindings_in_seq = field_bindings;

    let field_setter_match_arms = bindings.iter().enumerate().map(|(binding_index, binding)| {
        let field_index = binding_index;
        let binding_ident = &binding.binding_ident;
        let field_name = &binding.field_name;
        let field_ty = &binding.field_ty;
        quote! {
            #field_index => {
                if #binding_ident.is_some() {
                    return ::core::result::Result::Err(
                        #crate_path::serde::de::Error::duplicate_field(#field_name),
                    );
                }
                #binding_ident = ::core::option::Option::Some(#crate_path::serde::de::MapAccess::next_value::<#field_ty>(&mut map)?);
            }
        }
    });

    let known_fields: Vec<LitStr> = bindings
        .iter()
        .map(|binding| binding.field_name.clone())
        .collect();
    let known_fields_in_map = known_fields.clone();

    let construct_fields: Vec<TokenStream2> = parsed
        .fields
        .iter()
        .enumerate()
        .map(|(index, field)| {
            let field_ident = &field.ident;
            let field_name = &field.serialized_name;
            if field.skip_deserializing {
                return quote! {
                    #field_ident: ::core::default::Default::default()
                };
            }

            let binding_ident = bindings
                .iter()
                .find(|binding| binding.field_index == index)
                .expect("binding for non-skipped field")
                .binding_ident
                .clone();

            if field.default {
                quote! {
                    #field_ident: #binding_ident.unwrap_or_default()
                }
            } else {
                quote! {
                    #field_ident: match #binding_ident {
                        ::core::option::Option::Some(value) => value,
                        ::core::option::Option::None => {
                            return ::core::result::Result::Err(
                                #crate_path::serde::de::Error::missing_field(#field_name),
                            );
                        }
                    }
                }
            }
        })
        .collect();
    let construct_fields_in_map = construct_fields.clone();
    let construct_fields_in_seq = construct_fields;

    let seq_field_decode_steps = bindings.iter().enumerate().map(|(seq_index, binding)| {
        let binding_ident = &binding.binding_ident;
        let field_ty = &binding.field_ty;
        if binding.default {
            quote! {
                if let ::core::option::Option::Some(value) =
                    #crate_path::serde::de::SeqAccess::next_element::<#field_ty>(&mut seq)?
                {
                    #binding_ident = ::core::option::Option::Some(value);
                }
            }
        } else {
            quote! {
                #binding_ident =
                    match #crate_path::serde::de::SeqAccess::next_element::<#field_ty>(&mut seq)? {
                        ::core::option::Option::Some(value) => ::core::option::Option::Some(value),
                        ::core::option::Option::None => {
                            return ::core::result::Result::Err(
                                #crate_path::serde::de::Error::invalid_length(#seq_index, &self),
                            );
                        }
                    };
            }
        }
    });

    let seq_expected_len = bindings.len();

    let struct_ident = &parsed.ident;
    let struct_name = &parsed.struct_name;

    Ok(quote! {
        impl<'de> #crate_path::serde::de::Deserialize<'de> for #struct_ident {
            fn deserialize<D>(deserializer: D) -> ::core::result::Result<Self, D::Error>
            where
                D: #crate_path::serde::de::Deserializer<'de>,
            {
                struct __FeatherVisitor;

                impl<'de> #crate_path::serde::de::Visitor<'de> for __FeatherVisitor {
                    type Value = #struct_ident;

                    fn expecting(
                        &self,
                        formatter: &mut ::core::fmt::Formatter<'_>,
                    ) -> ::core::fmt::Result {
                        ::core::write!(formatter, "struct {}", #struct_name)
                    }

                    fn visit_map<V>(
                        self,
                        mut map: V,
                    ) -> ::core::result::Result<Self::Value, V::Error>
                    where
                        V: #crate_path::serde::de::MapAccess<'de>,
                    {
                        const __FEATHER_FIELDS: &[&str] = &[#(#known_fields_in_map),*];
                        #(#field_bindings_in_map)*
                        while let ::core::option::Option::Some(key) = #crate_path::serde::de::MapAccess::next_key::<#crate_path::__private::OwnedFieldName>(&mut map)?
                        {
                            match #crate_path::__private::select_field_index(key.as_str(), __FEATHER_FIELDS) {
                                ::core::option::Option::Some(index) => match index {
                                    #(#field_setter_match_arms)*
                                    _ => {
                                        let _: #crate_path::serde::de::IgnoredAny =
                                            #crate_path::serde::de::MapAccess::next_value(&mut map)?;
                                    }
                                },
                                ::core::option::Option::None => {
                                    let _: #crate_path::serde::de::IgnoredAny =
                                        #crate_path::serde::de::MapAccess::next_value(&mut map)?;
                                }
                            }
                        }

                        ::core::result::Result::Ok(#struct_ident {
                            #(#construct_fields_in_map,)*
                        })
                    }

                    fn visit_seq<V>(
                        self,
                        mut seq: V,
                    ) -> ::core::result::Result<Self::Value, V::Error>
                    where
                        V: #crate_path::serde::de::SeqAccess<'de>,
                    {
                        #(#field_bindings_in_seq)*
                        #(#seq_field_decode_steps)*

                        if #crate_path::serde::de::SeqAccess::next_element::<#crate_path::serde::de::IgnoredAny>(&mut seq)?.is_some() {
                            return ::core::result::Result::Err(
                                #crate_path::serde::de::Error::invalid_length(#seq_expected_len + 1, &self),
                            );
                        }

                        ::core::result::Result::Ok(#struct_ident {
                            #(#construct_fields_in_seq,)*
                        })
                    }
                }

                const __FEATHER_FIELDS: &[&str] = &[#(#known_fields),*];
                #crate_path::serde::de::Deserializer::deserialize_struct(
                    deserializer,
                    #struct_name,
                    __FEATHER_FIELDS,
                    __FeatherVisitor,
                )
            }
        }
    })
}

fn expand_deserialize_enum(
    parsed: &ParsedEnum,
    crate_path: &TokenStream2,
) -> syn::Result<TokenStream2> {
    let enum_ident = &parsed.ident;
    let enum_name = &parsed.enum_name;
    let known_variants: Vec<LitStr> = parsed
        .variants
        .iter()
        .map(|variant| variant.serialized_name.clone())
        .collect();

    let variant_match_arms = parsed
        .variants
        .iter()
        .enumerate()
        .map(|(variant_index, variant)| {
            let variant_ident = &variant.ident;
            match &variant.kind {
                ParsedEnumVariantKind::Unit => quote! {
                    #variant_index => {
                        #crate_path::serde::de::VariantAccess::unit_variant(variant_access)?;
                        ::core::result::Result::Ok(#enum_ident::#variant_ident)
                    }
                },
                ParsedEnumVariantKind::Newtype { ty } => quote! {
                    #variant_index => {
                        let value = #crate_path::serde::de::VariantAccess::newtype_variant::<#ty>(variant_access)?;
                        ::core::result::Result::Ok(#enum_ident::#variant_ident(value))
                    }
                },
            }
        });

    Ok(quote! {
        impl<'de> #crate_path::serde::de::Deserialize<'de> for #enum_ident {
            fn deserialize<D>(deserializer: D) -> ::core::result::Result<Self, D::Error>
            where
                D: #crate_path::serde::de::Deserializer<'de>,
            {
                const __FEATHER_VARIANTS: &[&str] = &[#(#known_variants),*];

                enum __FeatherVariantField {
                    __Index(usize),
                }

                impl<'de> #crate_path::serde::de::Deserialize<'de> for __FeatherVariantField {
                    fn deserialize<D>(
                        deserializer: D,
                    ) -> ::core::result::Result<Self, D::Error>
                    where
                        D: #crate_path::serde::de::Deserializer<'de>,
                    {
                        struct __FeatherVariantFieldVisitor;

                        impl<'de> #crate_path::serde::de::Visitor<'de>
                            for __FeatherVariantFieldVisitor
                        {
                            type Value = __FeatherVariantField;

                            fn expecting(
                                &self,
                                formatter: &mut ::core::fmt::Formatter<'_>,
                            ) -> ::core::fmt::Result {
                                formatter.write_str("enum variant name or index")
                            }

                            fn visit_str<E>(
                                self,
                                value: &str,
                            ) -> ::core::result::Result<Self::Value, E>
                            where
                                E: #crate_path::serde::de::Error,
                            {
                                match #crate_path::__private::select_field_index(value, __FEATHER_VARIANTS) {
                                    ::core::option::Option::Some(index) => {
                                        ::core::result::Result::Ok(__FeatherVariantField::__Index(index))
                                    }
                                    ::core::option::Option::None => {
                                        ::core::result::Result::Err(
                                            #crate_path::serde::de::Error::unknown_variant(value, __FEATHER_VARIANTS)
                                        )
                                    }
                                }
                            }

                            fn visit_u64<E>(
                                self,
                                value: u64,
                            ) -> ::core::result::Result<Self::Value, E>
                            where
                                E: #crate_path::serde::de::Error,
                            {
                                if value > usize::MAX as u64 {
                                    return ::core::result::Result::Err(
                                        #crate_path::serde::de::Error::invalid_value(
                                            #crate_path::serde::de::Unexpected::Unsigned(value),
                                            &self,
                                        ),
                                    );
                                }

                                let index = value as usize;
                                if index >= __FEATHER_VARIANTS.len() {
                                    return ::core::result::Result::Err(
                                        #crate_path::serde::de::Error::invalid_value(
                                            #crate_path::serde::de::Unexpected::Unsigned(value),
                                            &self,
                                        ),
                                    );
                                }

                                ::core::result::Result::Ok(__FeatherVariantField::__Index(index))
                            }

                            fn visit_i64<E>(
                                self,
                                value: i64,
                            ) -> ::core::result::Result<Self::Value, E>
                            where
                                E: #crate_path::serde::de::Error,
                            {
                                if value < 0 {
                                    return ::core::result::Result::Err(
                                        #crate_path::serde::de::Error::invalid_value(
                                            #crate_path::serde::de::Unexpected::Signed(value),
                                            &self,
                                        ),
                                    );
                                }

                                self.visit_u64(value as u64)
                            }

                            fn visit_bytes<E>(
                                self,
                                value: &[u8],
                            ) -> ::core::result::Result<Self::Value, E>
                            where
                                E: #crate_path::serde::de::Error,
                            {
                                let value = ::core::str::from_utf8(value).map_err(|_| {
                                    #crate_path::serde::de::Error::invalid_value(
                                        #crate_path::serde::de::Unexpected::Bytes(value),
                                        &self,
                                    )
                                })?;
                                self.visit_str(value)
                            }
                        }

                        #crate_path::serde::de::Deserializer::deserialize_identifier(
                            deserializer,
                            __FeatherVariantFieldVisitor,
                        )
                    }
                }

                struct __FeatherVisitor;

                impl<'de> #crate_path::serde::de::Visitor<'de> for __FeatherVisitor {
                    type Value = #enum_ident;

                    fn expecting(
                        &self,
                        formatter: &mut ::core::fmt::Formatter<'_>,
                    ) -> ::core::fmt::Result {
                        ::core::write!(formatter, "enum {}", #enum_name)
                    }

                    fn visit_enum<A>(
                        self,
                        data: A,
                    ) -> ::core::result::Result<Self::Value, A::Error>
                    where
                        A: #crate_path::serde::de::EnumAccess<'de>,
                    {
                        let (variant_key, variant_access) =
                            #crate_path::serde::de::EnumAccess::variant::<__FeatherVariantField>(
                                data,
                            )?;
                        let variant_index = match variant_key {
                            __FeatherVariantField::__Index(index) => index,
                        };

                        match variant_index {
                            #(#variant_match_arms)*
                            _ => {
                                ::core::unreachable!()
                            }
                        }
                    }
                }

                #crate_path::serde::de::Deserializer::deserialize_enum(
                    deserializer,
                    #enum_name,
                    __FEATHER_VARIANTS,
                    __FeatherVisitor,
                )
            }
        }
    })
}

fn parse_input(input: &DeriveInput, macro_name: &str) -> syn::Result<ParsedInput> {
    let derive_target_kind = match &input.data {
        Data::Struct(_) => "structs",
        Data::Enum(_) => "enums",
        _ => "structs or enums",
    };

    if !input.generics.params.is_empty() || input.generics.where_clause.is_some() {
        return Err(syn::Error::new_spanned(
            &input.generics,
            format!("{macro_name} only supports non-generic {derive_target_kind} in this MVP"),
        ));
    }

    let container_options = parse_container_attributes(&input.attrs)?;
    let container_name = container_options
        .rename
        .unwrap_or_else(|| LitStr::new(&input.ident.to_string(), input.ident.span()));

    match &input.data {
        Data::Struct(data_struct) => {
            let named_fields = match &data_struct.fields {
                Fields::Named(fields) => &fields.named,
                _ => {
                    return Err(syn::Error::new_spanned(
                        &data_struct.fields,
                        format!("{macro_name} only supports structs with named fields in this MVP"),
                    ))
                }
            };

            let mut parsed_fields = Vec::with_capacity(named_fields.len());
            for field in named_fields {
                parsed_fields.push(parse_field(field)?);
            }

            validate_unique_wire_field_names(&parsed_fields, WireDirection::Serialize)?;
            validate_unique_wire_field_names(&parsed_fields, WireDirection::Deserialize)?;

            Ok(ParsedInput::Struct(ParsedStruct {
                ident: input.ident.clone(),
                struct_name: container_name,
                fields: parsed_fields,
            }))
        }
        Data::Enum(data_enum) => {
            let mut parsed_variants = Vec::with_capacity(data_enum.variants.len());
            for variant in &data_enum.variants {
                parsed_variants.push(parse_enum_variant(variant, macro_name)?);
            }

            validate_unique_wire_variant_names(&parsed_variants)?;

            Ok(ParsedInput::Enum(ParsedEnum {
                ident: input.ident.clone(),
                enum_name: container_name,
                variants: parsed_variants,
            }))
        }
        _ => Err(syn::Error::new_spanned(
            &input.ident,
            format!("{macro_name} only supports structs or enums in this MVP"),
        )),
    }
}

fn validate_unique_wire_field_names(
    parsed_fields: &[ParsedField],
    direction: WireDirection,
) -> syn::Result<()> {
    let mut seen_by_name: HashMap<String, String> = HashMap::new();

    for field in parsed_fields {
        if !direction.includes(field) {
            continue;
        }

        let wire_name = field.serialized_name.value();
        let current_field = field.ident.to_string();
        if let Some(previous_field) = seen_by_name.insert(wire_name.clone(), current_field) {
            return Err(syn::Error::new(
                field.serialized_name.span(),
                format!(
                    "duplicate wire field name `{wire_name}` in {}; conflicts with field \
                     `{previous_field}`",
                    direction.name()
                ),
            ));
        }
    }

    Ok(())
}

fn validate_unique_wire_variant_names(parsed_variants: &[ParsedEnumVariant]) -> syn::Result<()> {
    let mut seen_by_name: HashMap<String, String> = HashMap::new();

    for variant in parsed_variants {
        let wire_name = variant.serialized_name.value();
        let current_variant = variant.ident.to_string();
        if let Some(previous_variant) = seen_by_name.insert(wire_name.clone(), current_variant) {
            return Err(syn::Error::new(
                variant.serialized_name.span(),
                format!(
                    "duplicate wire enum variant name `{wire_name}`; conflicts with variant \
                     `{previous_variant}`"
                ),
            ));
        }
    }

    Ok(())
}

fn parse_enum_variant(variant: &syn::Variant, macro_name: &str) -> syn::Result<ParsedEnumVariant> {
    let options = parse_variant_attributes(&variant.attrs)?;
    let serialized_name = options
        .rename
        .unwrap_or_else(|| LitStr::new(&variant.ident.unraw().to_string(), variant.ident.span()));

    let kind = match &variant.fields {
        Fields::Unit => ParsedEnumVariantKind::Unit,
        Fields::Unnamed(fields) => {
            if fields.unnamed.len() != 1 {
                return Err(syn::Error::new_spanned(
                    &variant.fields,
                    format!(
                        "{macro_name} only supports unit variants or newtype variants with one \
                         unnamed field"
                    ),
                ));
            }

            let payload_field = fields
                .unnamed
                .first()
                .expect("single enum payload field is present");
            for attr in &payload_field.attrs {
                if !attr.path().is_ident("serde") {
                    continue;
                }

                return Err(syn::Error::new_spanned(
                    attr,
                    "unsupported serde field attribute on enum variant payload; field attributes \
                     are not supported for enum payloads",
                ));
            }

            ParsedEnumVariantKind::Newtype {
                ty: Box::new(payload_field.ty.clone()),
            }
        }
        Fields::Named(_) => {
            return Err(syn::Error::new_spanned(
                &variant.fields,
                format!(
                    "{macro_name} only supports unit variants or newtype variants with one \
                     unnamed field"
                ),
            ))
        }
    };

    Ok(ParsedEnumVariant {
        ident: variant.ident.clone(),
        serialized_name,
        kind,
    })
}

fn parse_container_attributes(attrs: &[Attribute]) -> syn::Result<ContainerAttrOptions> {
    let mut options = ContainerAttrOptions { rename: None };

    for attr in attrs {
        if !attr.path().is_ident("serde") {
            continue;
        }

        attr.parse_nested_meta(|meta| {
            if meta.path.is_ident("rename") {
                let rename_value: LitStr = meta.value()?.parse()?;
                if options.rename.replace(rename_value).is_some() {
                    return Err(meta.error("duplicate serde container attribute `rename`"));
                }
                return Ok(());
            }

            Err(meta.error("unsupported serde container attribute; supported attributes: `rename`"))
        })?;
    }

    Ok(options)
}

fn parse_variant_attributes(attrs: &[Attribute]) -> syn::Result<VariantAttrOptions> {
    let mut options = VariantAttrOptions { rename: None };

    for attr in attrs {
        if !attr.path().is_ident("serde") {
            continue;
        }

        attr.parse_nested_meta(|meta| {
            if meta.path.is_ident("rename") {
                let rename_value: LitStr = meta.value()?.parse()?;
                if options.rename.replace(rename_value).is_some() {
                    return Err(meta.error("duplicate serde enum variant attribute `rename`"));
                }
                return Ok(());
            }

            Err(meta
                .error("unsupported serde enum variant attribute; supported attributes: `rename`"))
        })?;
    }

    Ok(options)
}

fn parse_field(field: &Field) -> syn::Result<ParsedField> {
    let field_ident = field.ident.clone().ok_or_else(|| {
        syn::Error::new(
            field.span(),
            "Feather derives only support fields with identifiers",
        )
    })?;

    let mut options = FieldAttrOptions::default();

    for attr in &field.attrs {
        if !attr.path().is_ident("serde") {
            continue;
        }

        attr.parse_nested_meta(|meta| {
            if meta.path.is_ident("rename") {
                let rename_value: LitStr = meta.value()?.parse()?;
                if options.rename.replace(rename_value).is_some() {
                    return Err(meta.error("duplicate serde field attribute `rename`"));
                }
                return Ok(());
            }

            if meta.path.is_ident("default") {
                ensure_flag_meta_has_no_value(&meta, "default")?;
                if options.default {
                    return Err(meta.error("duplicate serde field attribute `default`"));
                }
                options.default = true;
                return Ok(());
            }

            if meta.path.is_ident("skip") {
                ensure_flag_meta_has_no_value(&meta, "skip")?;
                if options.skip_serializing || options.skip_deserializing {
                    return Err(meta.error(
                        "serde field attribute `skip` conflicts with previously declared `skip`, \
                         `skip_serializing`, or `skip_deserializing`",
                    ));
                }
                options.skip_serializing = true;
                options.skip_deserializing = true;
                return Ok(());
            }

            if meta.path.is_ident("skip_serializing") {
                ensure_flag_meta_has_no_value(&meta, "skip_serializing")?;
                if options.skip_serializing {
                    return Err(meta.error("duplicate serde field attribute `skip_serializing`"));
                }
                if options.skip_deserializing {
                    return Err(meta.error(
                        "serde field attributes `skip_serializing` and `skip_deserializing` \
                         cannot be combined",
                    ));
                }
                options.skip_serializing = true;
                return Ok(());
            }

            if meta.path.is_ident("skip_deserializing") {
                ensure_flag_meta_has_no_value(&meta, "skip_deserializing")?;
                if options.skip_deserializing {
                    return Err(meta.error("duplicate serde field attribute `skip_deserializing`"));
                }
                if options.skip_serializing {
                    return Err(meta.error(
                        "serde field attributes `skip_serializing` and `skip_deserializing` \
                         cannot be combined",
                    ));
                }
                options.skip_deserializing = true;
                return Ok(());
            }

            Err(meta.error(
                "unsupported serde field attribute; supported attributes: `rename`, `default`, \
                 `skip`, `skip_serializing`, `skip_deserializing`",
            ))
        })?;
    }

    let serialized_name = options
        .rename
        .unwrap_or_else(|| LitStr::new(&field_ident.unraw().to_string(), field_ident.span()));

    Ok(ParsedField {
        ident: field_ident,
        ty: field.ty.clone(),
        serialized_name,
        default: options.default,
        skip_serializing: options.skip_serializing,
        skip_deserializing: options.skip_deserializing,
    })
}

fn ensure_flag_meta_has_no_value(
    meta: &syn::meta::ParseNestedMeta<'_>,
    name: &str,
) -> syn::Result<()> {
    if !meta.input.peek(syn::Token![=]) && !meta.input.peek(syn::token::Paren) {
        return Ok(());
    }

    Err(meta.error(format!(
        "serde field attribute `{name}` does not accept a value"
    )))
}

fn serde_feather_path() -> TokenStream2 {
    match crate_name("serde-feather") {
        Ok(FoundCrate::Itself) => quote!(crate),
        Ok(FoundCrate::Name(name)) => {
            let ident = Ident::new(&name.replace('-', "_"), Span::call_site());
            quote!(::#ident)
        }
        Err(_) => quote!(::serde_feather),
    }
}
