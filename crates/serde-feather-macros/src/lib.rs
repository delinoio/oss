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

fn expand_deserialize(input: &DeriveInput) -> syn::Result<TokenStream2> {
    let parsed = parse_input(input, "FeatherDeserialize")?;
    let crate_path = serde_feather_path();

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

fn parse_input(input: &DeriveInput, macro_name: &str) -> syn::Result<ParsedStruct> {
    if !input.generics.params.is_empty() || input.generics.where_clause.is_some() {
        return Err(syn::Error::new_spanned(
            &input.generics,
            format!("{macro_name} only supports non-generic structs in this MVP"),
        ));
    }

    let container_options = parse_container_attributes(&input.attrs)?;
    let struct_name = container_options
        .rename
        .unwrap_or_else(|| LitStr::new(&input.ident.to_string(), input.ident.span()));

    let named_fields = match &input.data {
        Data::Struct(data_struct) => match &data_struct.fields {
            Fields::Named(fields) => &fields.named,
            _ => {
                return Err(syn::Error::new_spanned(
                    &data_struct.fields,
                    format!("{macro_name} only supports structs with named fields in this MVP"),
                ))
            }
        },
        _ => {
            return Err(syn::Error::new_spanned(
                &input.ident,
                format!("{macro_name} only supports structs in this MVP"),
            ))
        }
    };

    let mut parsed_fields = Vec::with_capacity(named_fields.len());
    for field in named_fields {
        parsed_fields.push(parse_field(field)?);
    }

    validate_unique_wire_field_names(&parsed_fields, WireDirection::Serialize)?;
    validate_unique_wire_field_names(&parsed_fields, WireDirection::Deserialize)?;

    Ok(ParsedStruct {
        ident: input.ident.clone(),
        struct_name,
        fields: parsed_fields,
    })
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
