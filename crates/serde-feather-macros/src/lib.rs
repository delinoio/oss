#![forbid(unsafe_code)]

//! Proc-macro derive implementation for `serde-feather`.

use std::collections::{BTreeSet, HashMap};

use heck::{
    ToKebabCase, ToLowerCamelCase, ToShoutyKebabCase, ToShoutySnakeCase, ToSnakeCase,
    ToUpperCamelCase,
};
use proc_macro::TokenStream;
use proc_macro2::{Span, TokenStream as TokenStream2};
use proc_macro_crate::{crate_name, FoundCrate};
use quote::{format_ident, quote};
use syn::{
    ext::IdentExt, parse_macro_input, parse_quote, spanned::Spanned, visit::Visit, Attribute, Data,
    DeriveInput, Field, Fields, GenericParam, Generics, Ident, LitStr,
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

#[derive(Clone, Copy)]
enum RenameRule {
    LowerCase,
    UpperCase,
    PascalCase,
    CamelCase,
    SnakeCase,
    ScreamingSnakeCase,
    KebabCase,
    ScreamingKebabCase,
}

impl RenameRule {
    fn parse(literal: &LitStr) -> syn::Result<Self> {
        match literal.value().as_str() {
            "lowercase" => Ok(Self::LowerCase),
            "UPPERCASE" => Ok(Self::UpperCase),
            "PascalCase" => Ok(Self::PascalCase),
            "camelCase" => Ok(Self::CamelCase),
            "snake_case" => Ok(Self::SnakeCase),
            "SCREAMING_SNAKE_CASE" => Ok(Self::ScreamingSnakeCase),
            "kebab-case" => Ok(Self::KebabCase),
            "SCREAMING-KEBAB-CASE" => Ok(Self::ScreamingKebabCase),
            _ => Err(syn::Error::new(
                literal.span(),
                "unsupported rename rule; supported values: `lowercase`, `UPPERCASE`, \
                 `PascalCase`, `camelCase`, `snake_case`, `SCREAMING_SNAKE_CASE`, `kebab-case`, \
                 `SCREAMING-KEBAB-CASE`",
            )),
        }
    }

    fn apply(self, value: &str) -> String {
        match self {
            Self::LowerCase => value.to_lowercase(),
            Self::UpperCase => value.to_uppercase(),
            Self::PascalCase => value.to_upper_camel_case(),
            Self::CamelCase => value.to_lower_camel_case(),
            Self::SnakeCase => value.to_snake_case(),
            Self::ScreamingSnakeCase => value.to_shouty_snake_case(),
            Self::KebabCase => value.to_kebab_case(),
            Self::ScreamingKebabCase => value.to_shouty_kebab_case(),
        }
    }
}

struct ContainerAttrOptions {
    rename: Option<LitStr>,
    rename_all: Option<RenameRule>,
}

struct VariantAttrOptions {
    rename: Option<LitStr>,
    aliases: Vec<LitStr>,
    rename_all: Option<RenameRule>,
}

#[derive(Default)]
struct FieldAttrOptions {
    rename: Option<LitStr>,
    default: bool,
    skip_serializing: bool,
    skip_deserializing: bool,
    skip_serializing_if: Option<syn::Path>,
    with: Option<syn::Path>,
}

struct ParsedContainer {
    ident: Ident,
    generics: Generics,
    container_name: LitStr,
    data: ParsedData,
}

enum ParsedData {
    Struct(ParsedStruct),
    Enum(ParsedEnum),
}

struct ParsedStruct {
    kind: ParsedStructKind,
}

enum ParsedStructKind {
    Unit,
    Named(Vec<ParsedField>),
    Tuple(Vec<ParsedField>),
}

struct ParsedEnum {
    variants: Vec<ParsedEnumVariant>,
}

struct ParsedEnumVariant {
    ident: Ident,
    serialized_name: LitStr,
    deserialize_names: Vec<LitStr>,
    kind: ParsedEnumVariantKind,
}

enum ParsedEnumVariantKind {
    Unit,
    Unnamed(Vec<ParsedField>),
    Named(Vec<ParsedField>),
}

#[derive(Clone)]
struct ParsedField {
    accessor: FieldAccessor,
    ty: syn::Type,
    wire_name: Option<LitStr>,
    default: bool,
    skip_serializing: bool,
    skip_deserializing: bool,
    skip_serializing_if: Option<syn::Path>,
    with: Option<syn::Path>,
}

#[derive(Clone)]
enum FieldAccessor {
    Named(Ident),
    Unnamed(usize),
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

enum FieldShape {
    Named { rename_all: Option<RenameRule> },
    Tuple,
}

fn expand_serialize(input: &DeriveInput) -> syn::Result<TokenStream2> {
    let parsed = parse_input(input)?;
    let crate_path = serde_feather_path();

    let known_type_params = collect_type_param_names(&parsed.generics);
    let used_type_params =
        collect_used_type_params(&parsed, WireDirection::Serialize, &known_type_params);
    let serialize_generics = add_serialize_bounds(&parsed.generics, &used_type_params, &crate_path);

    let (impl_generics, _, where_clause) = serialize_generics.split_for_impl();
    let (_, ty_generics, _) = parsed.generics.split_for_impl();
    let ident = &parsed.ident;

    let serialize_body = match &parsed.data {
        ParsedData::Struct(parsed_struct) => {
            expand_serialize_struct(parsed_struct, &parsed.container_name, &crate_path)?
        }
        ParsedData::Enum(parsed_enum) => {
            expand_serialize_enum(parsed_enum, &parsed.container_name, &crate_path)?
        }
    };

    Ok(quote! {
        impl #impl_generics #crate_path::serde::ser::Serialize for #ident #ty_generics #where_clause {
            fn serialize<S>(
                &self,
                serializer: S,
            ) -> ::core::result::Result<S::Ok, S::Error>
            where
                S: #crate_path::serde::ser::Serializer,
            {
                #serialize_body
            }
        }
    })
}

fn expand_serialize_struct(
    parsed: &ParsedStruct,
    struct_name: &LitStr,
    crate_path: &TokenStream2,
) -> syn::Result<TokenStream2> {
    match &parsed.kind {
        ParsedStructKind::Unit => Ok(quote! {
            #crate_path::serde::ser::Serializer::serialize_unit_struct(serializer, #struct_name)
        }),
        ParsedStructKind::Tuple(fields) => {
            let included_fields: Vec<(usize, &ParsedField)> = fields
                .iter()
                .enumerate()
                .filter(|(_, field)| !field.skip_serializing)
                .collect();

            let base_len = included_fields.len();

            let len_adjustments = included_fields
                .iter()
                .filter_map(|(_, field)| {
                    let predicate = field.skip_serializing_if.as_ref()?;
                    let access = member_access(&field.accessor);
                    Some(quote! {
                        if #predicate(&self.#access) {
                            __feather_len -= 1;
                        }
                    })
                })
                .collect::<Vec<_>>();

            let serialize_steps = included_fields
                .iter()
                .map(|(field_index, field)| {
                    let access = member_access(&field.accessor);
                    let wrapper_ident =
                        format_ident!("__FeatherSerializeWithTupleStructField{field_index}");
                    let serialize_stmt = tuple_serialize_stmt(
                        field,
                        quote!(&self.#access),
                        &wrapper_ident,
                        crate_path,
                    );

                    if let Some(predicate) = &field.skip_serializing_if {
                        quote! {
                            if !(#predicate(&self.#access)) {
                                #serialize_stmt
                            }
                        }
                    } else {
                        serialize_stmt
                    }
                })
                .collect::<Vec<_>>();

            Ok(quote! {
                let mut __feather_len = #base_len;
                #(#len_adjustments)*
                let mut state = #crate_path::serde::ser::Serializer::serialize_tuple_struct(
                    serializer,
                    #struct_name,
                    __feather_len,
                )?;
                #(#serialize_steps)*
                #crate_path::serde::ser::SerializeTupleStruct::end(state)
            })
        }
        ParsedStructKind::Named(fields) => {
            let included_fields: Vec<(usize, &ParsedField)> = fields
                .iter()
                .enumerate()
                .filter(|(_, field)| !field.skip_serializing)
                .collect();

            let base_len = included_fields.len();

            let len_adjustments = included_fields
                .iter()
                .filter_map(|(_, field)| {
                    let predicate = field.skip_serializing_if.as_ref()?;
                    let access = member_access(&field.accessor);
                    Some(quote! {
                        if #predicate(&self.#access) {
                            __feather_len -= 1;
                        }
                    })
                })
                .collect::<Vec<_>>();

            let serialize_steps = included_fields
                .iter()
                .map(|(field_index, field)| {
                    let access = member_access(&field.accessor);
                    let wrapper_ident =
                        format_ident!("__FeatherSerializeWithNamedStructField{field_index}");
                    let field_name = field
                        .wire_name
                        .as_ref()
                        .expect("named struct field wire name");
                    let serialize_stmt = named_serialize_stmt(
                        field,
                        field_name,
                        quote!(&self.#access),
                        &wrapper_ident,
                        crate_path,
                    );

                    if let Some(predicate) = &field.skip_serializing_if {
                        quote! {
                            if !(#predicate(&self.#access)) {
                                #serialize_stmt
                            }
                        }
                    } else {
                        serialize_stmt
                    }
                })
                .collect::<Vec<_>>();

            Ok(quote! {
                let mut __feather_len = #base_len;
                #(#len_adjustments)*
                let mut state = #crate_path::serde::ser::Serializer::serialize_struct(
                    serializer,
                    #struct_name,
                    __feather_len,
                )?;
                #(#serialize_steps)*
                #crate_path::serde::ser::SerializeStruct::end(state)
            })
        }
    }
}

fn expand_serialize_enum(
    parsed: &ParsedEnum,
    enum_name: &LitStr,
    crate_path: &TokenStream2,
) -> syn::Result<TokenStream2> {
    let variant_arms = parsed
        .variants
        .iter()
        .enumerate()
        .map(|(variant_index, variant)| {
            let variant_index = variant_index as u32;
            let variant_ident = &variant.ident;
            let variant_name = &variant.serialized_name;

            match &variant.kind {
                ParsedEnumVariantKind::Unit => {
                    quote! {
                        Self::#variant_ident => {
                            #crate_path::serde::ser::Serializer::serialize_unit_variant(
                                serializer,
                                #enum_name,
                                #variant_index,
                                #variant_name,
                            )
                        }
                    }
                }
                ParsedEnumVariantKind::Unnamed(fields) => {
                    let bindings: Vec<Ident> = (0..fields.len())
                        .map(|index| format_ident!("__feather_field_{index}"))
                        .collect();
                    let pattern = quote! {
                        Self::#variant_ident(#(#bindings),*)
                    };

                    let included_fields: Vec<(usize, &ParsedField)> = fields
                        .iter()
                        .enumerate()
                        .filter(|(_, field)| !field.skip_serializing)
                        .collect();

                    let is_newtype = fields.len() == 1
                        && included_fields.len() == 1
                        && included_fields[0].1.skip_serializing_if.is_none();

                    if is_newtype {
                        let field = included_fields[0].1;
                        let binding = &bindings[0];
                        let wrapper_ident = format_ident!("__FeatherSerializeWithNewtypeVariant{variant_index}");
                        let serialize_value = serialize_value_expression(
                            field,
                            quote!(#binding),
                            &wrapper_ident,
                            crate_path,
                        );

                        quote! {
                            #pattern => {
                                #crate_path::serde::ser::Serializer::serialize_newtype_variant(
                                    serializer,
                                    #enum_name,
                                    #variant_index,
                                    #variant_name,
                                    #serialize_value,
                                )
                            }
                        }
                    } else {
                        let base_len = included_fields.len();

                        let len_adjustments = included_fields
                            .iter()
                            .filter_map(|(field_index, field)| {
                                let predicate = field.skip_serializing_if.as_ref()?;
                                let binding = &bindings[*field_index];
                                Some(quote! {
                                    if #predicate(#binding) {
                                        __feather_len -= 1;
                                    }
                                })
                            })
                            .collect::<Vec<_>>();

                        let serialize_steps = included_fields
                            .iter()
                            .map(|(field_index, field)| {
                                let binding = &bindings[*field_index];
                                let wrapper_ident = format_ident!(
                                    "__FeatherSerializeWithTupleVariant{variant_index}Field{field_index}"
                                );
                                let serialize_stmt = tuple_variant_serialize_stmt(
                                    field,
                                    quote!(#binding),
                                    &wrapper_ident,
                                    crate_path,
                                );

                                if let Some(predicate) = &field.skip_serializing_if {
                                    quote! {
                                        if !(#predicate(#binding)) {
                                            #serialize_stmt
                                        }
                                    }
                                } else {
                                    serialize_stmt
                                }
                            })
                            .collect::<Vec<_>>();

                        quote! {
                            #pattern => {
                                let mut __feather_len = #base_len;
                                #(#len_adjustments)*
                                let mut state = #crate_path::serde::ser::Serializer::serialize_tuple_variant(
                                    serializer,
                                    #enum_name,
                                    #variant_index,
                                    #variant_name,
                                    __feather_len,
                                )?;
                                #(#serialize_steps)*
                                #crate_path::serde::ser::SerializeTupleVariant::end(state)
                            }
                        }
                    }
                }
                ParsedEnumVariantKind::Named(fields) => {
                    let bindings: Vec<Ident> = (0..fields.len())
                        .map(|index| format_ident!("__feather_field_{index}"))
                        .collect();
                    let pattern_members = fields
                        .iter()
                        .enumerate()
                        .map(|(index, field)| {
                            let member_ident = match &field.accessor {
                                FieldAccessor::Named(ident) => ident,
                                FieldAccessor::Unnamed(_) => unreachable!("named variant uses named fields"),
                            };
                            let binding = &bindings[index];
                            quote! { #member_ident: #binding }
                        })
                        .collect::<Vec<_>>();

                    let included_fields: Vec<(usize, &ParsedField)> = fields
                        .iter()
                        .enumerate()
                        .filter(|(_, field)| !field.skip_serializing)
                        .collect();

                    let base_len = included_fields.len();

                    let len_adjustments = included_fields
                        .iter()
                        .filter_map(|(field_index, field)| {
                            let predicate = field.skip_serializing_if.as_ref()?;
                            let binding = &bindings[*field_index];
                            Some(quote! {
                                if #predicate(#binding) {
                                    __feather_len -= 1;
                                }
                            })
                        })
                        .collect::<Vec<_>>();

                    let serialize_steps = included_fields
                        .iter()
                        .map(|(field_index, field)| {
                            let field_name = field
                                .wire_name
                                .as_ref()
                                .expect("named enum variant field wire name");
                            let binding = &bindings[*field_index];
                            let wrapper_ident = format_ident!(
                                "__FeatherSerializeWithNamedVariant{variant_index}Field{field_index}"
                            );
                            let serialize_stmt = named_variant_serialize_stmt(
                                field,
                                field_name,
                                quote!(#binding),
                                &wrapper_ident,
                                crate_path,
                            );

                            if let Some(predicate) = &field.skip_serializing_if {
                                quote! {
                                    if !(#predicate(#binding)) {
                                        #serialize_stmt
                                    }
                                }
                            } else {
                                serialize_stmt
                            }
                        })
                        .collect::<Vec<_>>();

                    quote! {
                        Self::#variant_ident { #(#pattern_members),* } => {
                            let mut __feather_len = #base_len;
                            #(#len_adjustments)*
                            let mut state = #crate_path::serde::ser::Serializer::serialize_struct_variant(
                                serializer,
                                #enum_name,
                                #variant_index,
                                #variant_name,
                                __feather_len,
                            )?;
                            #(#serialize_steps)*
                            #crate_path::serde::ser::SerializeStructVariant::end(state)
                        }
                    }
                }
            }
        })
        .collect::<Vec<_>>();

    Ok(quote! {
        match self {
            #(#variant_arms),*
        }
    })
}

fn expand_deserialize(input: &DeriveInput) -> syn::Result<TokenStream2> {
    let parsed = parse_input(input)?;
    let crate_path = serde_feather_path();

    let known_type_params = collect_type_param_names(&parsed.generics);
    let used_type_params =
        collect_used_type_params(&parsed, WireDirection::Deserialize, &known_type_params);

    let de_lifetime = next_deserialize_lifetime(&parsed.generics);
    let mut deserialize_generics = add_deserialize_bounds(
        &parsed.generics,
        &used_type_params,
        &crate_path,
        &de_lifetime,
    );
    deserialize_generics.params.insert(
        0,
        GenericParam::Lifetime(syn::LifetimeParam::new(de_lifetime.clone())),
    );

    let (impl_generics, _, where_clause) = deserialize_generics.split_for_impl();
    let (_, ty_generics, _) = parsed.generics.split_for_impl();
    let ident = &parsed.ident;

    let deserialize_body = match &parsed.data {
        ParsedData::Struct(parsed_struct) => expand_deserialize_struct(
            ident,
            &parsed.generics,
            &used_type_params,
            &ty_generics,
            parsed_struct,
            &parsed.container_name,
            &crate_path,
            &de_lifetime,
        )?,
        ParsedData::Enum(parsed_enum) => expand_deserialize_enum(
            ident,
            &parsed.generics,
            &used_type_params,
            &ty_generics,
            parsed_enum,
            &parsed.container_name,
            &crate_path,
            &de_lifetime,
        )?,
    };

    Ok(quote! {
        impl #impl_generics #crate_path::serde::de::Deserialize<#de_lifetime> for #ident #ty_generics #where_clause {
            fn deserialize<D>(deserializer: D) -> ::core::result::Result<Self, D::Error>
            where
                D: #crate_path::serde::de::Deserializer<#de_lifetime>,
            {
                #deserialize_body
            }
        }
    })
}

fn expand_deserialize_struct(
    struct_ident: &Ident,
    container_generics: &Generics,
    used_type_params: &BTreeSet<String>,
    ty_generics: &impl quote::ToTokens,
    parsed: &ParsedStruct,
    struct_name: &LitStr,
    crate_path: &TokenStream2,
    de_lifetime: &syn::Lifetime,
) -> syn::Result<TokenStream2> {
    let helper_params = helper_generic_param_decls(container_generics);
    let helper_impl_params = helper_generic_param_decls_for_deserialize(
        container_generics,
        used_type_params,
        crate_path,
        de_lifetime,
    );
    let helper_args = helper_generic_args(container_generics);
    let helper_phantom_types = helper_generic_phantom_types(container_generics);
    let visitor_decl = if helper_params.is_empty() {
        quote! { struct __FeatherVisitor; }
    } else {
        quote! {
            struct __FeatherVisitor<#(#helper_params),*>(
                ::core::marker::PhantomData<(#(#helper_phantom_types),*)>,
            );
        }
    };
    let visitor_ty = if helper_args.is_empty() {
        quote! { __FeatherVisitor }
    } else {
        quote! { __FeatherVisitor<#(#helper_args),*> }
    };
    let visitor_ctor = if helper_args.is_empty() {
        quote! { __FeatherVisitor }
    } else {
        quote! { __FeatherVisitor::<#(#helper_args),*>(::core::marker::PhantomData) }
    };
    let visitor_impl_generics = if helper_impl_params.is_empty() {
        quote! { #de_lifetime }
    } else {
        quote! { #de_lifetime, #(#helper_impl_params),* }
    };

    match &parsed.kind {
        ParsedStructKind::Unit => Ok(quote! {
            #visitor_decl

            impl<#visitor_impl_generics> #crate_path::serde::de::Visitor<#de_lifetime> for #visitor_ty {
                type Value = #struct_ident #ty_generics;

                fn expecting(
                    &self,
                    formatter: &mut ::core::fmt::Formatter<'_>,
                ) -> ::core::fmt::Result {
                    ::core::write!(formatter, "unit struct {}", #struct_name)
                }

                fn visit_unit<E>(self) -> ::core::result::Result<Self::Value, E>
                where
                    E: #crate_path::serde::de::Error,
                {
                    ::core::result::Result::Ok(#struct_ident)
                }
            }

            #crate_path::serde::de::Deserializer::deserialize_unit_struct(
                deserializer,
                #struct_name,
                #visitor_ctor,
            )
        }),
        ParsedStructKind::Tuple(fields) => expand_deserialize_tuple_struct(
            struct_ident,
            container_generics,
            used_type_params,
            ty_generics,
            fields,
            struct_name,
            crate_path,
            de_lifetime,
        ),
        ParsedStructKind::Named(fields) => expand_deserialize_named_struct(
            struct_ident,
            container_generics,
            used_type_params,
            ty_generics,
            fields,
            struct_name,
            crate_path,
            de_lifetime,
        ),
    }
}

fn expand_deserialize_tuple_struct(
    struct_ident: &Ident,
    container_generics: &Generics,
    used_type_params: &BTreeSet<String>,
    ty_generics: &impl quote::ToTokens,
    fields: &[ParsedField],
    struct_name: &LitStr,
    crate_path: &TokenStream2,
    de_lifetime: &syn::Lifetime,
) -> syn::Result<TokenStream2> {
    let helper_params = helper_generic_param_decls(container_generics);
    let helper_impl_params = helper_generic_param_decls_for_deserialize(
        container_generics,
        used_type_params,
        crate_path,
        de_lifetime,
    );
    let helper_args = helper_generic_args(container_generics);
    let helper_phantom_types = helper_generic_phantom_types(container_generics);
    let visitor_decl = if helper_params.is_empty() {
        quote! { struct __FeatherVisitor; }
    } else {
        quote! {
            struct __FeatherVisitor<#(#helper_params),*>(
                ::core::marker::PhantomData<(#(#helper_phantom_types),*)>,
            );
        }
    };
    let visitor_ty = if helper_args.is_empty() {
        quote! { __FeatherVisitor }
    } else {
        quote! { __FeatherVisitor<#(#helper_args),*> }
    };
    let visitor_ctor = if helper_args.is_empty() {
        quote! { __FeatherVisitor }
    } else {
        quote! { __FeatherVisitor::<#(#helper_args),*>(::core::marker::PhantomData) }
    };
    let visitor_impl_generics = if helper_impl_params.is_empty() {
        quote! { #de_lifetime }
    } else {
        quote! { #de_lifetime, #(#helper_impl_params),* }
    };

    let (wrapper_defs, wrapper_by_field) = deserialize_wrapper_definitions(
        fields,
        "TupleStruct",
        crate_path,
        de_lifetime,
        WireDirection::Deserialize,
    );

    let bindings: Vec<TokenStream2> = fields
        .iter()
        .enumerate()
        .filter(|(_, field)| !field.skip_deserializing)
        .map(|(index, field)| {
            let binding = format_ident!("__feather_field_{index}");
            let field_ty = &field.ty;
            quote! {
                let mut #binding: ::core::option::Option<#field_ty> = ::core::option::Option::None;
            }
        })
        .collect();

    let seq_steps: Vec<TokenStream2> = fields
        .iter()
        .enumerate()
        .filter(|(_, field)| !field.skip_deserializing)
        .enumerate()
        .map(|(seq_index, (field_index, field))| {
            let binding = format_ident!("__feather_field_{field_index}");
            let decode_ty = decode_type_for_field(field_index, field, &wrapper_by_field);
            let decode_ident = format_ident!("__feather_decoded_{field_index}");
            let unwrap_decoded = unwrap_decoded_value(field_index, &decode_ident, &wrapper_by_field);

            if field.default {
                quote! {
                    if let ::core::option::Option::Some(#decode_ident) =
                        #crate_path::serde::de::SeqAccess::next_element::<#decode_ty>(&mut seq)?
                    {
                        #binding = ::core::option::Option::Some(#unwrap_decoded);
                    }
                }
            } else {
                quote! {
                    #binding =
                        match #crate_path::serde::de::SeqAccess::next_element::<#decode_ty>(&mut seq)? {
                            ::core::option::Option::Some(#decode_ident) => {
                                ::core::option::Option::Some(#unwrap_decoded)
                            }
                            ::core::option::Option::None => {
                                return ::core::result::Result::Err(
                                    #crate_path::serde::de::Error::invalid_length(#seq_index, &self),
                                );
                            }
                        };
                }
            }
        })
        .collect();

    let expected_len = fields
        .iter()
        .filter(|field| !field.skip_deserializing)
        .count();

    let construct_values: Vec<TokenStream2> = fields
        .iter()
        .enumerate()
        .map(|(index, field)| {
            if field.skip_deserializing {
                return quote! { ::core::default::Default::default() };
            }

            let binding = format_ident!("__feather_field_{index}");
            if field.default {
                quote! { #binding.unwrap_or_default() }
            } else {
                quote! {
                    match #binding {
                        ::core::option::Option::Some(value) => value,
                        ::core::option::Option::None => {
                            return ::core::result::Result::Err(
                                #crate_path::serde::de::Error::invalid_length(#expected_len, &self),
                            );
                        }
                    }
                }
            }
        })
        .collect();

    Ok(quote! {
        #(#wrapper_defs)*

        #visitor_decl

        impl<#visitor_impl_generics> #crate_path::serde::de::Visitor<#de_lifetime> for #visitor_ty {
            type Value = #struct_ident #ty_generics;

            fn expecting(
                &self,
                formatter: &mut ::core::fmt::Formatter<'_>,
            ) -> ::core::fmt::Result {
                ::core::write!(formatter, "tuple struct {}", #struct_name)
            }

            fn visit_seq<V>(
                self,
                mut seq: V,
            ) -> ::core::result::Result<Self::Value, V::Error>
            where
                V: #crate_path::serde::de::SeqAccess<#de_lifetime>,
            {
                #(#bindings)*
                #(#seq_steps)*

                if #crate_path::serde::de::SeqAccess::next_element::<#crate_path::serde::de::IgnoredAny>(&mut seq)?.is_some() {
                    return ::core::result::Result::Err(
                        #crate_path::serde::de::Error::invalid_length(#expected_len + 1, &self),
                    );
                }

                ::core::result::Result::Ok(#struct_ident(
                    #(#construct_values),*
                ))
            }
        }

        #crate_path::serde::de::Deserializer::deserialize_tuple_struct(
            deserializer,
            #struct_name,
            #expected_len,
            #visitor_ctor,
        )
    })
}

fn expand_deserialize_named_struct(
    struct_ident: &Ident,
    container_generics: &Generics,
    used_type_params: &BTreeSet<String>,
    ty_generics: &impl quote::ToTokens,
    fields: &[ParsedField],
    struct_name: &LitStr,
    crate_path: &TokenStream2,
    de_lifetime: &syn::Lifetime,
) -> syn::Result<TokenStream2> {
    let helper_params = helper_generic_param_decls(container_generics);
    let helper_impl_params = helper_generic_param_decls_for_deserialize(
        container_generics,
        used_type_params,
        crate_path,
        de_lifetime,
    );
    let helper_args = helper_generic_args(container_generics);
    let helper_phantom_types = helper_generic_phantom_types(container_generics);
    let visitor_decl = if helper_params.is_empty() {
        quote! { struct __FeatherVisitor; }
    } else {
        quote! {
            struct __FeatherVisitor<#(#helper_params),*>(
                ::core::marker::PhantomData<(#(#helper_phantom_types),*)>,
            );
        }
    };
    let visitor_ty = if helper_args.is_empty() {
        quote! { __FeatherVisitor }
    } else {
        quote! { __FeatherVisitor<#(#helper_args),*> }
    };
    let visitor_ctor = if helper_args.is_empty() {
        quote! { __FeatherVisitor }
    } else {
        quote! { __FeatherVisitor::<#(#helper_args),*>(::core::marker::PhantomData) }
    };
    let visitor_impl_generics = if helper_impl_params.is_empty() {
        quote! { #de_lifetime }
    } else {
        quote! { #de_lifetime, #(#helper_impl_params),* }
    };

    let (wrapper_defs, wrapper_by_field) = deserialize_wrapper_definitions(
        fields,
        "NamedStruct",
        crate_path,
        de_lifetime,
        WireDirection::Deserialize,
    );

    let bindings: Vec<TokenStream2> = fields
        .iter()
        .enumerate()
        .filter(|(_, field)| !field.skip_deserializing)
        .map(|(index, field)| {
            let binding = format_ident!("__feather_field_{index}");
            let field_ty = &field.ty;
            quote! {
                let mut #binding: ::core::option::Option<#field_ty> = ::core::option::Option::None;
            }
        })
        .collect();

    let known_fields: Vec<LitStr> = fields
        .iter()
        .filter(|field| !field.skip_deserializing)
        .map(|field| {
            field
                .wire_name
                .as_ref()
                .expect("named struct field wire name")
                .clone()
        })
        .collect();

    let map_setter_arms = fields
        .iter()
        .enumerate()
        .filter(|(_, field)| !field.skip_deserializing)
        .enumerate()
        .map(|(known_index, (field_index, field))| {
            let binding = format_ident!("__feather_field_{field_index}");
            let decode_ty = decode_type_for_field(field_index, field, &wrapper_by_field);
            let decode_ident = format_ident!("__feather_decoded_{field_index}");
            let unwrap_decoded = unwrap_decoded_value(field_index, &decode_ident, &wrapper_by_field);
            let field_name = field
                .wire_name
                .as_ref()
                .expect("named struct field wire name");

            quote! {
                #known_index => {
                    if #binding.is_some() {
                        return ::core::result::Result::Err(
                            #crate_path::serde::de::Error::duplicate_field(#field_name),
                        );
                    }
                    let #decode_ident = #crate_path::serde::de::MapAccess::next_value::<#decode_ty>(&mut map)?;
                    #binding = ::core::option::Option::Some(#unwrap_decoded);
                }
            }
        })
        .collect::<Vec<_>>();

    let seq_steps: Vec<TokenStream2> = fields
        .iter()
        .enumerate()
        .filter(|(_, field)| !field.skip_deserializing)
        .enumerate()
        .map(|(seq_index, (field_index, field))| {
            let binding = format_ident!("__feather_field_{field_index}");
            let decode_ty = decode_type_for_field(field_index, field, &wrapper_by_field);
            let decode_ident = format_ident!("__feather_decoded_{field_index}");
            let unwrap_decoded = unwrap_decoded_value(field_index, &decode_ident, &wrapper_by_field);

            if field.default {
                quote! {
                    if let ::core::option::Option::Some(#decode_ident) =
                        #crate_path::serde::de::SeqAccess::next_element::<#decode_ty>(&mut seq)?
                    {
                        #binding = ::core::option::Option::Some(#unwrap_decoded);
                    }
                }
            } else {
                quote! {
                    #binding =
                        match #crate_path::serde::de::SeqAccess::next_element::<#decode_ty>(&mut seq)? {
                            ::core::option::Option::Some(#decode_ident) => {
                                ::core::option::Option::Some(#unwrap_decoded)
                            }
                            ::core::option::Option::None => {
                                return ::core::result::Result::Err(
                                    #crate_path::serde::de::Error::invalid_length(#seq_index, &self),
                                );
                            }
                        };
                }
            }
        })
        .collect();

    let expected_len = fields
        .iter()
        .filter(|field| !field.skip_deserializing)
        .count();

    let construct_named_fields: Vec<TokenStream2> = fields
        .iter()
        .enumerate()
        .map(|(index, field)| {
            let field_ident = match &field.accessor {
                FieldAccessor::Named(ident) => ident,
                FieldAccessor::Unnamed(_) => unreachable!("named struct fields are named"),
            };

            if field.skip_deserializing {
                return quote! {
                    #field_ident: ::core::default::Default::default()
                };
            }

            let binding = format_ident!("__feather_field_{index}");
            let field_name = field
                .wire_name
                .as_ref()
                .expect("named struct field wire name");

            if field.default {
                quote! {
                    #field_ident: #binding.unwrap_or_default()
                }
            } else {
                quote! {
                    #field_ident: match #binding {
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

    Ok(quote! {
        #(#wrapper_defs)*

        #visitor_decl

        impl<#visitor_impl_generics> #crate_path::serde::de::Visitor<#de_lifetime> for #visitor_ty {
            type Value = #struct_ident #ty_generics;

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
                V: #crate_path::serde::de::MapAccess<#de_lifetime>,
            {
                const __FEATHER_FIELDS: &[&str] = &[#(#known_fields),*];
                #(#bindings)*

                while let ::core::option::Option::Some(key) =
                    #crate_path::serde::de::MapAccess::next_key::<#crate_path::__private::OwnedFieldName>(&mut map)?
                {
                    match #crate_path::__private::select_field_index(key.as_str(), __FEATHER_FIELDS) {
                        ::core::option::Option::Some(index) => match index {
                            #(#map_setter_arms)*
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
                    #(#construct_named_fields,)*
                })
            }

            fn visit_seq<V>(
                self,
                mut seq: V,
            ) -> ::core::result::Result<Self::Value, V::Error>
            where
                V: #crate_path::serde::de::SeqAccess<#de_lifetime>,
            {
                #(#bindings)*
                #(#seq_steps)*

                if #crate_path::serde::de::SeqAccess::next_element::<#crate_path::serde::de::IgnoredAny>(&mut seq)?.is_some() {
                    return ::core::result::Result::Err(
                        #crate_path::serde::de::Error::invalid_length(#expected_len + 1, &self),
                    );
                }

                ::core::result::Result::Ok(#struct_ident {
                    #(#construct_named_fields,)*
                })
            }
        }

        const __FEATHER_FIELDS: &[&str] = &[#(#known_fields),*];
        #crate_path::serde::de::Deserializer::deserialize_struct(
            deserializer,
            #struct_name,
            __FEATHER_FIELDS,
            #visitor_ctor,
        )
    })
}

fn expand_deserialize_enum(
    enum_ident: &Ident,
    container_generics: &Generics,
    used_type_params: &BTreeSet<String>,
    ty_generics: &impl quote::ToTokens,
    parsed: &ParsedEnum,
    enum_name: &LitStr,
    crate_path: &TokenStream2,
    de_lifetime: &syn::Lifetime,
) -> syn::Result<TokenStream2> {
    let helper_params = helper_generic_param_decls(container_generics);
    let helper_impl_params = helper_generic_param_decls_for_deserialize(
        container_generics,
        used_type_params,
        crate_path,
        de_lifetime,
    );
    let helper_args = helper_generic_args(container_generics);
    let helper_phantom_types = helper_generic_phantom_types(container_generics);
    let visitor_decl = if helper_params.is_empty() {
        quote! { struct __FeatherVisitor; }
    } else {
        quote! {
            struct __FeatherVisitor<#(#helper_params),*>(
                ::core::marker::PhantomData<(#(#helper_phantom_types),*)>,
            );
        }
    };
    let visitor_ty = if helper_args.is_empty() {
        quote! { __FeatherVisitor }
    } else {
        quote! { __FeatherVisitor<#(#helper_args),*> }
    };
    let visitor_ctor = if helper_args.is_empty() {
        quote! { __FeatherVisitor }
    } else {
        quote! { __FeatherVisitor::<#(#helper_args),*>(::core::marker::PhantomData) }
    };
    let visitor_impl_generics = if helper_impl_params.is_empty() {
        quote! { #de_lifetime }
    } else {
        quote! { #de_lifetime, #(#helper_impl_params),* }
    };

    let canonical_variant_names: Vec<LitStr> = parsed
        .variants
        .iter()
        .map(|variant| variant.serialized_name.clone())
        .collect();

    let variant_name_match_arms = parsed
        .variants
        .iter()
        .enumerate()
        .map(|(index, variant)| {
            let index = index as u64;
            let patterns = variant
                .deserialize_names
                .iter()
                .map(|name| quote! { #name })
                .collect::<Vec<_>>();
            quote! {
                #(#patterns)|* => {
                    ::core::result::Result::Ok(__FeatherVariantField::__Index(#index as usize))
                }
            }
        })
        .collect::<Vec<_>>();

    let variant_decode_arms = parsed
        .variants
        .iter()
        .enumerate()
        .map(|(variant_index, variant)| {
            let variant_index = variant_index as usize;
            let variant_ident = &variant.ident;
            let variant_name = &variant.serialized_name;

            match &variant.kind {
                ParsedEnumVariantKind::Unit => quote! {
                    #variant_index => {
                        #crate_path::serde::de::VariantAccess::unit_variant(variant_access)?;
                        ::core::result::Result::Ok(#enum_ident::#variant_ident)
                    }
                },
                ParsedEnumVariantKind::Unnamed(fields) => {
                    if fields.len() == 1 {
                        let field = &fields[0];
                        if field.skip_deserializing {
                            quote! {
                                #variant_index => {
                                    let _: #crate_path::serde::de::IgnoredAny =
                                        #crate_path::serde::de::VariantAccess::newtype_variant(variant_access)?;
                                    ::core::result::Result::Ok(#enum_ident::#variant_ident(
                                        ::core::default::Default::default(),
                                    ))
                                }
                            }
                        } else if let Some(with_path) = &field.with {
                            let wrapper_ident = format_ident!("__FeatherEnumVariantWith{variant_index}");
                            let ty = &field.ty;
                            let wrapper_decl = if helper_params.is_empty() {
                                quote! { struct #wrapper_ident(#ty); }
                            } else {
                                quote! {
                                    struct #wrapper_ident<#(#helper_params),*>(
                                        #ty,
                                        ::core::marker::PhantomData<(#(#helper_phantom_types),*)>,
                                    );
                                }
                            };
                            let wrapper_ty = if helper_args.is_empty() {
                                quote! { #wrapper_ident }
                            } else {
                                quote! { #wrapper_ident<#(#helper_args),*> }
                            };
                            let wrapper_impl_generics = if helper_impl_params.is_empty() {
                                quote! { #de_lifetime }
                            } else {
                                quote! { #de_lifetime, #(#helper_impl_params),* }
                            };
                            let wrapper_ctor = if helper_args.is_empty() {
                                quote! { #wrapper_ident(value) }
                            } else {
                                quote! { #wrapper_ident::<#(#helper_args),*>(value, ::core::marker::PhantomData) }
                            };
                            quote! {
                                #variant_index => {
                                    #wrapper_decl

                                    impl<#wrapper_impl_generics> #crate_path::serde::de::Deserialize<#de_lifetime>
                                        for #wrapper_ty
                                    {
                                        fn deserialize<D>(
                                            deserializer: D,
                                        ) -> ::core::result::Result<Self, D::Error>
                                        where
                                            D: #crate_path::serde::de::Deserializer<#de_lifetime>,
                                        {
                                            #with_path::deserialize(deserializer).map(|value| {
                                                #wrapper_ctor
                                            })
                                        }
                                    }

                                    let value = #crate_path::serde::de::VariantAccess::newtype_variant::<#wrapper_ty>(variant_access)?;
                                    ::core::result::Result::Ok(#enum_ident::#variant_ident(value.0))
                                }
                            }
                        } else {
                            let ty = &field.ty;
                            quote! {
                                #variant_index => {
                                    let value = #crate_path::serde::de::VariantAccess::newtype_variant::<#ty>(variant_access)?;
                                    ::core::result::Result::Ok(#enum_ident::#variant_ident(value))
                                }
                            }
                        }
                    } else {
                        let (wrapper_defs, wrapper_by_field) = deserialize_wrapper_definitions(
                            fields,
                            &format!("TupleVariant{variant_index}"),
                            crate_path,
                            de_lifetime,
                            WireDirection::Deserialize,
                        );

                        let bindings: Vec<TokenStream2> = fields
                            .iter()
                            .enumerate()
                            .filter(|(_, field)| !field.skip_deserializing)
                            .map(|(index, field)| {
                                let binding = format_ident!("__feather_field_{index}");
                                let field_ty = &field.ty;
                                quote! {
                                    let mut #binding: ::core::option::Option<#field_ty> = ::core::option::Option::None;
                                }
                            })
                            .collect();

                        let seq_steps: Vec<TokenStream2> = fields
                            .iter()
                            .enumerate()
                            .filter(|(_, field)| !field.skip_deserializing)
                            .enumerate()
                            .map(|(seq_index, (field_index, field))| {
                                let binding = format_ident!("__feather_field_{field_index}");
                                let decode_ty = decode_type_for_field(field_index, field, &wrapper_by_field);
                                let decode_ident = format_ident!("__feather_decoded_{field_index}");
                                let unwrap_decoded =
                                    unwrap_decoded_value(field_index, &decode_ident, &wrapper_by_field);

                                if field.default {
                                    quote! {
                                        if let ::core::option::Option::Some(#decode_ident) =
                                            #crate_path::serde::de::SeqAccess::next_element::<#decode_ty>(&mut seq)?
                                        {
                                            #binding = ::core::option::Option::Some(#unwrap_decoded);
                                        }
                                    }
                                } else {
                                    quote! {
                                        #binding =
                                            match #crate_path::serde::de::SeqAccess::next_element::<#decode_ty>(&mut seq)? {
                                                ::core::option::Option::Some(#decode_ident) => {
                                                    ::core::option::Option::Some(#unwrap_decoded)
                                                }
                                                ::core::option::Option::None => {
                                                    return ::core::result::Result::Err(
                                                        #crate_path::serde::de::Error::invalid_length(#seq_index, &self),
                                                    );
                                                }
                                            };
                                    }
                                }
                            })
                            .collect();

                        let expected_len =
                            fields.iter().filter(|field| !field.skip_deserializing).count();

                        let construct_values: Vec<TokenStream2> = fields
                            .iter()
                            .enumerate()
                            .map(|(index, field)| {
                                if field.skip_deserializing {
                                    return quote! { ::core::default::Default::default() };
                                }

                                let binding = format_ident!("__feather_field_{index}");
                                if field.default {
                                    quote! { #binding.unwrap_or_default() }
                                } else {
                                    quote! {
                                        match #binding {
                                            ::core::option::Option::Some(value) => value,
                                            ::core::option::Option::None => {
                                                return ::core::result::Result::Err(
                                                    #crate_path::serde::de::Error::invalid_length(#expected_len, &self),
                                                );
                                            }
                                        }
                                    }
                                }
                            })
                            .collect();

                        let variant_visitor_decl = if helper_params.is_empty() {
                            quote! { struct __FeatherVariantVisitor; }
                        } else {
                            quote! {
                                struct __FeatherVariantVisitor<#(#helper_params),*>(
                                    ::core::marker::PhantomData<(#(#helper_phantom_types),*)>,
                                );
                            }
                        };
                        let variant_visitor_ty = if helper_args.is_empty() {
                            quote! { __FeatherVariantVisitor }
                        } else {
                            quote! { __FeatherVariantVisitor<#(#helper_args),*> }
                        };
                        let variant_visitor_ctor = if helper_args.is_empty() {
                            quote! { __FeatherVariantVisitor }
                        } else {
                            quote! { __FeatherVariantVisitor::<#(#helper_args),*>(::core::marker::PhantomData) }
                        };
                        let variant_visitor_impl_generics = if helper_impl_params.is_empty() {
                            quote! { #de_lifetime }
                        } else {
                            quote! { #de_lifetime, #(#helper_impl_params),* }
                        };

                        quote! {
                            #variant_index => {
                                #(#wrapper_defs)*

                                #variant_visitor_decl

                                impl<#variant_visitor_impl_generics> #crate_path::serde::de::Visitor<#de_lifetime>
                                    for #variant_visitor_ty
                                {
                                    type Value = #enum_ident #ty_generics;

                                    fn expecting(
                                        &self,
                                        formatter: &mut ::core::fmt::Formatter<'_>,
                                    ) -> ::core::fmt::Result {
                                        ::core::write!(
                                            formatter,
                                            "tuple variant {}::{}",
                                            #enum_name,
                                            #variant_name,
                                        )
                                    }

                                    fn visit_seq<V>(
                                        self,
                                        mut seq: V,
                                    ) -> ::core::result::Result<Self::Value, V::Error>
                                    where
                                        V: #crate_path::serde::de::SeqAccess<#de_lifetime>,
                                    {
                                        #(#bindings)*
                                        #(#seq_steps)*

                                        if #crate_path::serde::de::SeqAccess::next_element::<#crate_path::serde::de::IgnoredAny>(&mut seq)?.is_some() {
                                            return ::core::result::Result::Err(
                                                #crate_path::serde::de::Error::invalid_length(#expected_len + 1, &self),
                                            );
                                        }

                                        ::core::result::Result::Ok(#enum_ident::#variant_ident(
                                            #(#construct_values),*
                                        ))
                                    }
                                }

                                #crate_path::serde::de::VariantAccess::tuple_variant(
                                    variant_access,
                                    #expected_len,
                                    #variant_visitor_ctor,
                                )
                            }
                        }
                    }
                }
                ParsedEnumVariantKind::Named(fields) => {
                    let (wrapper_defs, wrapper_by_field) = deserialize_wrapper_definitions(
                        fields,
                        &format!("NamedVariant{variant_index}"),
                        crate_path,
                        de_lifetime,
                        WireDirection::Deserialize,
                    );

                    let bindings: Vec<TokenStream2> = fields
                        .iter()
                        .enumerate()
                        .filter(|(_, field)| !field.skip_deserializing)
                        .map(|(index, field)| {
                            let binding = format_ident!("__feather_field_{index}");
                            let field_ty = &field.ty;
                            quote! {
                                let mut #binding: ::core::option::Option<#field_ty> = ::core::option::Option::None;
                            }
                        })
                        .collect();

                    let known_fields: Vec<LitStr> = fields
                        .iter()
                        .filter(|field| !field.skip_deserializing)
                        .map(|field| {
                            field
                                .wire_name
                                .as_ref()
                                .expect("named enum variant field wire name")
                                .clone()
                        })
                        .collect();

                    let map_setter_arms = fields
                        .iter()
                        .enumerate()
                        .filter(|(_, field)| !field.skip_deserializing)
                        .enumerate()
                        .map(|(known_index, (field_index, field))| {
                            let binding = format_ident!("__feather_field_{field_index}");
                            let decode_ty = decode_type_for_field(field_index, field, &wrapper_by_field);
                            let decode_ident = format_ident!("__feather_decoded_{field_index}");
                            let unwrap_decoded = unwrap_decoded_value(
                                field_index,
                                &decode_ident,
                                &wrapper_by_field,
                            );
                            let field_name = field
                                .wire_name
                                .as_ref()
                                .expect("named enum variant field wire name");

                            quote! {
                                #known_index => {
                                    if #binding.is_some() {
                                        return ::core::result::Result::Err(
                                            #crate_path::serde::de::Error::duplicate_field(#field_name),
                                        );
                                    }
                                    let #decode_ident = #crate_path::serde::de::MapAccess::next_value::<#decode_ty>(&mut map)?;
                                    #binding = ::core::option::Option::Some(#unwrap_decoded);
                                }
                            }
                        })
                        .collect::<Vec<_>>();

                    let seq_steps: Vec<TokenStream2> = fields
                        .iter()
                        .enumerate()
                        .filter(|(_, field)| !field.skip_deserializing)
                        .enumerate()
                        .map(|(seq_index, (field_index, field))| {
                            let binding = format_ident!("__feather_field_{field_index}");
                            let decode_ty = decode_type_for_field(field_index, field, &wrapper_by_field);
                            let decode_ident = format_ident!("__feather_decoded_{field_index}");
                            let unwrap_decoded = unwrap_decoded_value(
                                field_index,
                                &decode_ident,
                                &wrapper_by_field,
                            );

                            if field.default {
                                quote! {
                                    if let ::core::option::Option::Some(#decode_ident) =
                                        #crate_path::serde::de::SeqAccess::next_element::<#decode_ty>(&mut seq)?
                                    {
                                        #binding = ::core::option::Option::Some(#unwrap_decoded);
                                    }
                                }
                            } else {
                                quote! {
                                    #binding =
                                        match #crate_path::serde::de::SeqAccess::next_element::<#decode_ty>(&mut seq)? {
                                            ::core::option::Option::Some(#decode_ident) => {
                                                ::core::option::Option::Some(#unwrap_decoded)
                                            }
                                            ::core::option::Option::None => {
                                                return ::core::result::Result::Err(
                                                    #crate_path::serde::de::Error::invalid_length(#seq_index, &self),
                                                );
                                            }
                                        };
                                }
                            }
                        })
                        .collect();

                    let expected_len =
                        fields.iter().filter(|field| !field.skip_deserializing).count();

                    let construct_fields: Vec<TokenStream2> = fields
                        .iter()
                        .enumerate()
                        .map(|(index, field)| {
                            let field_ident = match &field.accessor {
                                FieldAccessor::Named(ident) => ident,
                                FieldAccessor::Unnamed(_) => unreachable!("named enum variant uses named fields"),
                            };

                            if field.skip_deserializing {
                                return quote! {
                                    #field_ident: ::core::default::Default::default()
                                };
                            }

                            let binding = format_ident!("__feather_field_{index}");
                            let field_name = field
                                .wire_name
                                .as_ref()
                                .expect("named enum variant field wire name");

                            if field.default {
                                quote! {
                                    #field_ident: #binding.unwrap_or_default()
                                }
                            } else {
                                quote! {
                                    #field_ident: match #binding {
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

                    let variant_visitor_decl = if helper_params.is_empty() {
                        quote! { struct __FeatherVariantVisitor; }
                    } else {
                        quote! {
                            struct __FeatherVariantVisitor<#(#helper_params),*>(
                                ::core::marker::PhantomData<(#(#helper_phantom_types),*)>,
                            );
                        }
                    };
                    let variant_visitor_ty = if helper_args.is_empty() {
                        quote! { __FeatherVariantVisitor }
                    } else {
                        quote! { __FeatherVariantVisitor<#(#helper_args),*> }
                    };
                    let variant_visitor_ctor = if helper_args.is_empty() {
                        quote! { __FeatherVariantVisitor }
                    } else {
                        quote! { __FeatherVariantVisitor::<#(#helper_args),*>(::core::marker::PhantomData) }
                    };
                    let variant_visitor_impl_generics = if helper_impl_params.is_empty() {
                        quote! { #de_lifetime }
                    } else {
                        quote! { #de_lifetime, #(#helper_impl_params),* }
                    };

                    quote! {
                        #variant_index => {
                            #(#wrapper_defs)*

                            #variant_visitor_decl

                            impl<#variant_visitor_impl_generics> #crate_path::serde::de::Visitor<#de_lifetime>
                                for #variant_visitor_ty
                            {
                                type Value = #enum_ident #ty_generics;

                                fn expecting(
                                    &self,
                                    formatter: &mut ::core::fmt::Formatter<'_>,
                                ) -> ::core::fmt::Result {
                                    ::core::write!(
                                        formatter,
                                        "struct variant {}::{}",
                                        #enum_name,
                                        #variant_name,
                                    )
                                }

                                fn visit_map<V>(
                                    self,
                                    mut map: V,
                                ) -> ::core::result::Result<Self::Value, V::Error>
                                where
                                    V: #crate_path::serde::de::MapAccess<#de_lifetime>,
                                {
                                    const __FEATHER_FIELDS: &[&str] = &[#(#known_fields),*];
                                    #(#bindings)*

                                    while let ::core::option::Option::Some(key) =
                                        #crate_path::serde::de::MapAccess::next_key::<#crate_path::__private::OwnedFieldName>(&mut map)?
                                    {
                                        match #crate_path::__private::select_field_index(key.as_str(), __FEATHER_FIELDS) {
                                            ::core::option::Option::Some(index) => match index {
                                                #(#map_setter_arms)*
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

                                    ::core::result::Result::Ok(#enum_ident::#variant_ident {
                                        #(#construct_fields,)*
                                    })
                                }

                                fn visit_seq<V>(
                                    self,
                                    mut seq: V,
                                ) -> ::core::result::Result<Self::Value, V::Error>
                                where
                                    V: #crate_path::serde::de::SeqAccess<#de_lifetime>,
                                {
                                    #(#bindings)*
                                    #(#seq_steps)*

                                    if #crate_path::serde::de::SeqAccess::next_element::<#crate_path::serde::de::IgnoredAny>(&mut seq)?.is_some() {
                                        return ::core::result::Result::Err(
                                            #crate_path::serde::de::Error::invalid_length(#expected_len + 1, &self),
                                        );
                                    }

                                    ::core::result::Result::Ok(#enum_ident::#variant_ident {
                                        #(#construct_fields,)*
                                    })
                                }
                            }

                            const __FEATHER_FIELDS: &[&str] = &[#(#known_fields),*];
                            #crate_path::serde::de::VariantAccess::struct_variant(
                                variant_access,
                                __FEATHER_FIELDS,
                                #variant_visitor_ctor,
                            )
                        }
                    }
                }
            }
        })
        .collect::<Vec<_>>();

    Ok(quote! {
        const __FEATHER_VARIANTS: &[&str] = &[#(#canonical_variant_names),*];

        enum __FeatherVariantField {
            __Index(usize),
        }

        impl<#de_lifetime> #crate_path::serde::de::Deserialize<#de_lifetime>
            for __FeatherVariantField
        {
            fn deserialize<D>(
                deserializer: D,
            ) -> ::core::result::Result<Self, D::Error>
            where
                D: #crate_path::serde::de::Deserializer<#de_lifetime>,
            {
                struct __FeatherVariantFieldVisitor;

                impl<#de_lifetime> #crate_path::serde::de::Visitor<#de_lifetime>
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
                        match value {
                            #(#variant_name_match_arms)*
                            _ => ::core::result::Result::Err(
                                #crate_path::serde::de::Error::unknown_variant(value, __FEATHER_VARIANTS),
                            ),
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

        #visitor_decl

        impl<#visitor_impl_generics> #crate_path::serde::de::Visitor<#de_lifetime> for #visitor_ty {
            type Value = #enum_ident #ty_generics;

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
                A: #crate_path::serde::de::EnumAccess<#de_lifetime>,
            {
                let (variant_key, variant_access) =
                    #crate_path::serde::de::EnumAccess::variant::<__FeatherVariantField>(data)?;
                let variant_index = match variant_key {
                    __FeatherVariantField::__Index(index) => index,
                };

                match variant_index {
                    #(#variant_decode_arms)*
                    _ => ::core::unreachable!(),
                }
            }
        }

        #crate_path::serde::de::Deserializer::deserialize_enum(
            deserializer,
            #enum_name,
            __FEATHER_VARIANTS,
            #visitor_ctor,
        )
    })
}

fn tuple_serialize_stmt(
    field: &ParsedField,
    value_ref: TokenStream2,
    wrapper_ident: &Ident,
    crate_path: &TokenStream2,
) -> TokenStream2 {
    let value_expr = serialize_value_expression(field, value_ref, wrapper_ident, crate_path);

    quote! {
        #crate_path::serde::ser::SerializeTupleStruct::serialize_field(
            &mut state,
            #value_expr,
        )?;
    }
}

fn tuple_variant_serialize_stmt(
    field: &ParsedField,
    value_ref: TokenStream2,
    wrapper_ident: &Ident,
    crate_path: &TokenStream2,
) -> TokenStream2 {
    let value_expr = serialize_value_expression(field, value_ref, wrapper_ident, crate_path);

    quote! {
        #crate_path::serde::ser::SerializeTupleVariant::serialize_field(
            &mut state,
            #value_expr,
        )?;
    }
}

fn named_serialize_stmt(
    field: &ParsedField,
    field_name: &LitStr,
    value_ref: TokenStream2,
    wrapper_ident: &Ident,
    crate_path: &TokenStream2,
) -> TokenStream2 {
    let value_expr = serialize_value_expression(field, value_ref, wrapper_ident, crate_path);

    quote! {
        #crate_path::serde::ser::SerializeStruct::serialize_field(
            &mut state,
            #field_name,
            #value_expr,
        )?;
    }
}

fn named_variant_serialize_stmt(
    field: &ParsedField,
    field_name: &LitStr,
    value_ref: TokenStream2,
    wrapper_ident: &Ident,
    crate_path: &TokenStream2,
) -> TokenStream2 {
    let value_expr = serialize_value_expression(field, value_ref, wrapper_ident, crate_path);

    quote! {
        #crate_path::serde::ser::SerializeStructVariant::serialize_field(
            &mut state,
            #field_name,
            #value_expr,
        )?;
    }
}

fn serialize_value_expression(
    field: &ParsedField,
    value_ref: TokenStream2,
    wrapper_ident: &Ident,
    crate_path: &TokenStream2,
) -> TokenStream2 {
    if let Some(with_path) = &field.with {
        let ty = &field.ty;
        quote! {{
            struct #wrapper_ident<'a>(&'a #ty);

            impl<'a> #crate_path::serde::ser::Serialize for #wrapper_ident<'a> {
                fn serialize<S>(
                    &self,
                    serializer: S,
                ) -> ::core::result::Result<S::Ok, S::Error>
                where
                    S: #crate_path::serde::ser::Serializer,
                {
                    #with_path::serialize(self.0, serializer)
                }
            }

            &#wrapper_ident(#value_ref)
        }}
    } else {
        quote! { #value_ref }
    }
}

fn deserialize_wrapper_definitions(
    fields: &[ParsedField],
    prefix: &str,
    crate_path: &TokenStream2,
    de_lifetime: &syn::Lifetime,
    direction: WireDirection,
) -> (Vec<TokenStream2>, HashMap<usize, Ident>) {
    let mut defs = Vec::new();
    let mut wrappers = HashMap::new();

    for (index, field) in fields.iter().enumerate() {
        if !direction.includes(field) {
            continue;
        }

        let Some(with_path) = &field.with else {
            continue;
        };

        let wrapper_ident = format_ident!("__FeatherDeserializeWith{prefix}Field{index}");
        let ty = &field.ty;
        defs.push(quote! {
            struct #wrapper_ident(#ty);

            impl<#de_lifetime> #crate_path::serde::de::Deserialize<#de_lifetime> for #wrapper_ident {
                fn deserialize<D>(
                    deserializer: D,
                ) -> ::core::result::Result<Self, D::Error>
                where
                    D: #crate_path::serde::de::Deserializer<#de_lifetime>,
                {
                    #with_path::deserialize(deserializer).map(Self)
                }
            }
        });
        wrappers.insert(index, wrapper_ident);
    }

    (defs, wrappers)
}

fn decode_type_for_field(
    field_index: usize,
    field: &ParsedField,
    wrappers: &HashMap<usize, Ident>,
) -> TokenStream2 {
    if let Some(wrapper_ident) = wrappers.get(&field_index) {
        quote! { #wrapper_ident }
    } else {
        let ty = &field.ty;
        quote! { #ty }
    }
}

fn unwrap_decoded_value(
    field_index: usize,
    decoded_ident: &Ident,
    wrappers: &HashMap<usize, Ident>,
) -> TokenStream2 {
    if wrappers.contains_key(&field_index) {
        quote! { #decoded_ident.0 }
    } else {
        quote! { #decoded_ident }
    }
}

fn parse_input(input: &DeriveInput) -> syn::Result<ParsedContainer> {
    let container_options = parse_container_attributes(&input.attrs)?;
    let container_name = container_options
        .rename
        .unwrap_or_else(|| LitStr::new(&input.ident.to_string(), input.ident.span()));

    let parsed_data = match &input.data {
        Data::Struct(data_struct) => {
            let parsed_struct = match &data_struct.fields {
                Fields::Unit => ParsedStruct {
                    kind: ParsedStructKind::Unit,
                },
                Fields::Named(named_fields) => {
                    let mut parsed_fields = Vec::with_capacity(named_fields.named.len());
                    for (index, field) in named_fields.named.iter().enumerate() {
                        parsed_fields.push(parse_field(
                            field,
                            index,
                            FieldShape::Named {
                                rename_all: container_options.rename_all,
                            },
                        )?);
                    }

                    validate_unique_wire_field_names(&parsed_fields, WireDirection::Serialize)?;
                    validate_unique_wire_field_names(&parsed_fields, WireDirection::Deserialize)?;

                    ParsedStruct {
                        kind: ParsedStructKind::Named(parsed_fields),
                    }
                }
                Fields::Unnamed(unnamed_fields) => {
                    let mut parsed_fields = Vec::with_capacity(unnamed_fields.unnamed.len());
                    for (index, field) in unnamed_fields.unnamed.iter().enumerate() {
                        parsed_fields.push(parse_field(field, index, FieldShape::Tuple)?);
                    }

                    ParsedStruct {
                        kind: ParsedStructKind::Tuple(parsed_fields),
                    }
                }
            };

            ParsedData::Struct(parsed_struct)
        }
        Data::Enum(data_enum) => {
            let mut parsed_variants = Vec::with_capacity(data_enum.variants.len());
            for variant in &data_enum.variants {
                parsed_variants.push(parse_enum_variant(variant, container_options.rename_all)?);
            }

            validate_unique_serialized_variant_names(&parsed_variants)?;
            validate_unique_deserialize_variant_names(&parsed_variants)?;

            ParsedData::Enum(ParsedEnum {
                variants: parsed_variants,
            })
        }
        _ => {
            return Err(syn::Error::new_spanned(
                &input.ident,
                "Feather derives only support structs and enums",
            ))
        }
    };

    Ok(ParsedContainer {
        ident: input.ident.clone(),
        generics: input.generics.clone(),
        container_name,
        data: parsed_data,
    })
}

fn parse_enum_variant(
    variant: &syn::Variant,
    enum_rename_all: Option<RenameRule>,
) -> syn::Result<ParsedEnumVariant> {
    let options = parse_variant_attributes(&variant.attrs)?;

    let default_name = enum_rename_all
        .map(|rule| rule.apply(&variant.ident.unraw().to_string()))
        .unwrap_or_else(|| variant.ident.unraw().to_string());
    let serialized_name = options
        .rename
        .unwrap_or_else(|| LitStr::new(&default_name, variant.ident.span()));

    let mut deserialize_names = Vec::with_capacity(1 + options.aliases.len());
    deserialize_names.push(serialized_name.clone());
    deserialize_names.extend(options.aliases);

    let kind = match &variant.fields {
        Fields::Unit => ParsedEnumVariantKind::Unit,
        Fields::Unnamed(unnamed_fields) => {
            let mut parsed_fields = Vec::with_capacity(unnamed_fields.unnamed.len());
            for (index, field) in unnamed_fields.unnamed.iter().enumerate() {
                parsed_fields.push(parse_field(field, index, FieldShape::Tuple)?);
            }
            ParsedEnumVariantKind::Unnamed(parsed_fields)
        }
        Fields::Named(named_fields) => {
            let mut parsed_fields = Vec::with_capacity(named_fields.named.len());
            for (index, field) in named_fields.named.iter().enumerate() {
                parsed_fields.push(parse_field(
                    field,
                    index,
                    FieldShape::Named {
                        rename_all: options.rename_all,
                    },
                )?);
            }
            validate_unique_wire_field_names(&parsed_fields, WireDirection::Serialize)?;
            validate_unique_wire_field_names(&parsed_fields, WireDirection::Deserialize)?;
            ParsedEnumVariantKind::Named(parsed_fields)
        }
    };

    Ok(ParsedEnumVariant {
        ident: variant.ident.clone(),
        serialized_name,
        deserialize_names,
        kind,
    })
}

fn parse_field(field: &Field, index: usize, shape: FieldShape) -> syn::Result<ParsedField> {
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

            if meta.path.is_ident("skip_serializing_if") {
                let path_lit: LitStr = meta.value()?.parse()?;
                let parsed_path: syn::Path = path_lit.parse().map_err(|error| {
                    syn::Error::new(
                        path_lit.span(),
                        format!(
                            "invalid path in serde field attribute `skip_serializing_if`: {error}"
                        ),
                    )
                })?;
                if options.skip_serializing_if.replace(parsed_path).is_some() {
                    return Err(meta.error("duplicate serde field attribute `skip_serializing_if`"));
                }
                return Ok(());
            }

            if meta.path.is_ident("with") {
                let path_lit: LitStr = meta.value()?.parse()?;
                let parsed_path: syn::Path = path_lit.parse().map_err(|error| {
                    syn::Error::new(
                        path_lit.span(),
                        format!("invalid path in serde field attribute `with`: {error}"),
                    )
                })?;
                if options.with.replace(parsed_path).is_some() {
                    return Err(meta.error("duplicate serde field attribute `with`"));
                }
                return Ok(());
            }

            Err(meta.error(
                "unsupported serde field attribute; supported attributes: `rename`, `default`, \
                 `skip`, `skip_serializing`, `skip_deserializing`, `skip_serializing_if`, `with`",
            ))
        })?;
    }

    let (accessor, wire_name) = match shape {
        FieldShape::Named { rename_all } => {
            let field_ident = field.ident.clone().ok_or_else(|| {
                syn::Error::new(
                    field.span(),
                    "Feather derives only support fields with identifiers in this position",
                )
            })?;

            let default_name = rename_all
                .map(|rule| rule.apply(&field_ident.unraw().to_string()))
                .unwrap_or_else(|| field_ident.unraw().to_string());
            let wire_name = options
                .rename
                .unwrap_or_else(|| LitStr::new(&default_name, field_ident.span()));

            (FieldAccessor::Named(field_ident), Some(wire_name))
        }
        FieldShape::Tuple => {
            if let Some(rename) = options.rename {
                return Err(syn::Error::new(
                    rename.span(),
                    "serde field attribute `rename` is not supported on tuple fields",
                ));
            }
            (FieldAccessor::Unnamed(index), None)
        }
    };

    Ok(ParsedField {
        accessor,
        ty: field.ty.clone(),
        wire_name,
        default: options.default,
        skip_serializing: options.skip_serializing,
        skip_deserializing: options.skip_deserializing,
        skip_serializing_if: options.skip_serializing_if,
        with: options.with,
    })
}

fn validate_unique_wire_field_names(
    fields: &[ParsedField],
    direction: WireDirection,
) -> syn::Result<()> {
    let mut seen = HashMap::<String, String>::new();

    for field in fields {
        if !direction.includes(field) {
            continue;
        }

        let Some(name) = field.wire_name.as_ref() else {
            continue;
        };

        let current_field = match &field.accessor {
            FieldAccessor::Named(ident) => ident.to_string(),
            FieldAccessor::Unnamed(index) => index.to_string(),
        };

        let wire_name = name.value();
        if let Some(previous_field) = seen.insert(wire_name.clone(), current_field) {
            return Err(syn::Error::new(
                name.span(),
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

fn validate_unique_serialized_variant_names(variants: &[ParsedEnumVariant]) -> syn::Result<()> {
    let mut seen = HashMap::<String, String>::new();

    for variant in variants {
        let wire_name = variant.serialized_name.value();
        let current_variant = variant.ident.to_string();
        if let Some(previous_variant) = seen.insert(wire_name.clone(), current_variant) {
            return Err(syn::Error::new(
                variant.serialized_name.span(),
                format!(
                    "duplicate wire enum variant name `{wire_name}` in serialization; conflicts \
                     with variant `{previous_variant}`"
                ),
            ));
        }
    }

    Ok(())
}

fn validate_unique_deserialize_variant_names(variants: &[ParsedEnumVariant]) -> syn::Result<()> {
    let mut seen = HashMap::<String, String>::new();

    for variant in variants {
        for name in &variant.deserialize_names {
            let wire_name = name.value();
            let current_variant = variant.ident.to_string();
            if let Some(previous_variant) = seen.insert(wire_name.clone(), current_variant) {
                return Err(syn::Error::new(
                    name.span(),
                    format!(
                        "duplicate wire enum variant name `{wire_name}` in deserialization; \
                         conflicts with variant `{previous_variant}`"
                    ),
                ));
            }
        }
    }

    Ok(())
}

fn parse_container_attributes(attrs: &[Attribute]) -> syn::Result<ContainerAttrOptions> {
    let mut options = ContainerAttrOptions {
        rename: None,
        rename_all: None,
    };

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

            if meta.path.is_ident("rename_all") {
                let rename_rule_literal: LitStr = meta.value()?.parse()?;
                let rename_rule = RenameRule::parse(&rename_rule_literal)?;
                if options.rename_all.replace(rename_rule).is_some() {
                    return Err(meta.error("duplicate serde container attribute `rename_all`"));
                }
                return Ok(());
            }

            Err(meta.error(
                "unsupported serde container attribute; supported attributes: `rename`, \
                 `rename_all`",
            ))
        })?;
    }

    Ok(options)
}

fn parse_variant_attributes(attrs: &[Attribute]) -> syn::Result<VariantAttrOptions> {
    let mut options = VariantAttrOptions {
        rename: None,
        aliases: Vec::new(),
        rename_all: None,
    };

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

            if meta.path.is_ident("alias") {
                let alias_value: LitStr = meta.value()?.parse()?;
                options.aliases.push(alias_value);
                return Ok(());
            }

            if meta.path.is_ident("rename_all") {
                let rename_rule_literal: LitStr = meta.value()?.parse()?;
                let rename_rule = RenameRule::parse(&rename_rule_literal)?;
                if options.rename_all.replace(rename_rule).is_some() {
                    return Err(meta.error("duplicate serde enum variant attribute `rename_all`"));
                }
                return Ok(());
            }

            Err(meta.error(
                "unsupported serde enum variant attribute; supported attributes: `rename`, \
                 `alias`, `rename_all`",
            ))
        })?;
    }

    Ok(options)
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

fn collect_type_param_names(generics: &Generics) -> BTreeSet<String> {
    generics
        .type_params()
        .map(|param| param.ident.to_string())
        .collect()
}

fn collect_used_type_params(
    parsed: &ParsedContainer,
    direction: WireDirection,
    known_type_params: &BTreeSet<String>,
) -> BTreeSet<String> {
    let mut used = BTreeSet::<String>::new();

    let mut visit_field = |field: &ParsedField| {
        if !direction.includes(field) {
            return;
        }

        // Custom with-hooks handle serialization/deserialization directly.
        if field.with.is_some() {
            return;
        }

        used.extend(collect_type_params_from_type(&field.ty, known_type_params));
    };

    match &parsed.data {
        ParsedData::Struct(parsed_struct) => match &parsed_struct.kind {
            ParsedStructKind::Unit => {}
            ParsedStructKind::Named(fields) | ParsedStructKind::Tuple(fields) => {
                for field in fields {
                    visit_field(field);
                }
            }
        },
        ParsedData::Enum(parsed_enum) => {
            for variant in &parsed_enum.variants {
                match &variant.kind {
                    ParsedEnumVariantKind::Unit => {}
                    ParsedEnumVariantKind::Unnamed(fields)
                    | ParsedEnumVariantKind::Named(fields) => {
                        for field in fields {
                            visit_field(field);
                        }
                    }
                }
            }
        }
    }

    used
}

fn collect_type_params_from_type(
    ty: &syn::Type,
    known_type_params: &BTreeSet<String>,
) -> BTreeSet<String> {
    struct TypeParamCollector<'a> {
        known_type_params: &'a BTreeSet<String>,
        used_type_params: BTreeSet<String>,
    }

    impl<'ast, 'a> Visit<'ast> for TypeParamCollector<'a> {
        fn visit_type_path(&mut self, type_path: &'ast syn::TypePath) {
            if type_path.qself.is_none() && type_path.path.segments.len() == 1 {
                let ident = &type_path.path.segments[0].ident;
                let ident_name = ident.to_string();
                if self.known_type_params.contains(&ident_name) {
                    self.used_type_params.insert(ident_name);
                }
            }

            syn::visit::visit_type_path(self, type_path);
        }
    }

    let mut collector = TypeParamCollector {
        known_type_params,
        used_type_params: BTreeSet::new(),
    };
    collector.visit_type(ty);
    collector.used_type_params
}

fn add_serialize_bounds(
    generics: &Generics,
    used_type_params: &BTreeSet<String>,
    crate_path: &TokenStream2,
) -> Generics {
    let mut output = generics.clone();

    let bounded_type_params = output
        .type_params()
        .filter_map(|type_param| {
            let ident_name = type_param.ident.to_string();
            used_type_params
                .contains(&ident_name)
                .then_some(type_param.ident.clone())
        })
        .collect::<Vec<_>>();

    for ident in bounded_type_params {
        output
            .make_where_clause()
            .predicates
            .push(parse_quote!(#ident: #crate_path::serde::ser::Serialize));
    }

    output
}

fn add_deserialize_bounds(
    generics: &Generics,
    used_type_params: &BTreeSet<String>,
    crate_path: &TokenStream2,
    de_lifetime: &syn::Lifetime,
) -> Generics {
    let mut output = generics.clone();

    let bounded_type_params = output
        .type_params()
        .filter_map(|type_param| {
            let ident_name = type_param.ident.to_string();
            used_type_params
                .contains(&ident_name)
                .then_some(type_param.ident.clone())
        })
        .collect::<Vec<_>>();

    for ident in bounded_type_params {
        output
            .make_where_clause()
            .predicates
            .push(parse_quote!(#ident: #crate_path::serde::de::Deserialize<#de_lifetime>));
    }

    output
}

fn next_deserialize_lifetime(generics: &Generics) -> syn::Lifetime {
    let existing = generics
        .lifetimes()
        .map(|lt| lt.lifetime.ident.to_string())
        .collect::<BTreeSet<_>>();

    let mut suffix: usize = 0;
    loop {
        let candidate = if suffix == 0 {
            "__feather_de".to_owned()
        } else {
            format!("__feather_de_{suffix}")
        };

        if !existing.contains(&candidate) {
            return syn::Lifetime::new(&format!("'{candidate}"), Span::call_site());
        }

        suffix += 1;
    }
}

fn member_access(accessor: &FieldAccessor) -> TokenStream2 {
    match accessor {
        FieldAccessor::Named(ident) => quote!(#ident),
        FieldAccessor::Unnamed(index) => {
            let index = syn::Index::from(*index);
            quote!(#index)
        }
    }
}

fn helper_generic_param_decls(generics: &Generics) -> Vec<TokenStream2> {
    generics
        .params
        .iter()
        .map(|param| match param {
            GenericParam::Lifetime(lifetime) => {
                let lifetime = &lifetime.lifetime;
                quote!(#lifetime)
            }
            GenericParam::Type(ty) => {
                let ident = &ty.ident;
                quote!(#ident)
            }
            GenericParam::Const(const_param) => {
                let ident = &const_param.ident;
                let ty = &const_param.ty;
                quote!(const #ident: #ty)
            }
        })
        .collect()
}

fn helper_generic_param_decls_for_deserialize(
    generics: &Generics,
    used_type_params: &BTreeSet<String>,
    crate_path: &TokenStream2,
    de_lifetime: &syn::Lifetime,
) -> Vec<TokenStream2> {
    generics
        .params
        .iter()
        .map(|param| match param {
            GenericParam::Lifetime(lifetime) => {
                let lifetime = &lifetime.lifetime;
                quote!(#lifetime)
            }
            GenericParam::Type(ty) => {
                let ident = &ty.ident;
                if used_type_params.contains(&ident.to_string()) {
                    quote!(#ident: #crate_path::serde::de::Deserialize<#de_lifetime>)
                } else {
                    quote!(#ident)
                }
            }
            GenericParam::Const(const_param) => {
                let ident = &const_param.ident;
                let ty = &const_param.ty;
                quote!(const #ident: #ty)
            }
        })
        .collect()
}

fn helper_generic_args(generics: &Generics) -> Vec<TokenStream2> {
    generics
        .params
        .iter()
        .map(|param| match param {
            GenericParam::Lifetime(lifetime) => {
                let lifetime = &lifetime.lifetime;
                quote!(#lifetime)
            }
            GenericParam::Type(ty) => {
                let ident = &ty.ident;
                quote!(#ident)
            }
            GenericParam::Const(const_param) => {
                let ident = &const_param.ident;
                quote!(#ident)
            }
        })
        .collect()
}

fn helper_generic_phantom_types(generics: &Generics) -> Vec<TokenStream2> {
    generics
        .params
        .iter()
        .map(|param| match param {
            GenericParam::Lifetime(lifetime) => {
                let lifetime = &lifetime.lifetime;
                quote!(&#lifetime ())
            }
            GenericParam::Type(ty) => {
                let ident = &ty.ident;
                quote!(#ident)
            }
            GenericParam::Const(const_param) => {
                let ident = &const_param.ident;
                quote!([(); #ident])
            }
        })
        .collect()
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
