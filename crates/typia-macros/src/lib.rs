#![forbid(unsafe_code)]

//! Proc-macro derive implementation for `typia`.

use std::collections::{BTreeSet, HashMap};

use heck::{
    ToKebabCase, ToLowerCamelCase, ToShoutyKebabCase, ToShoutySnakeCase, ToSnakeCase,
    ToUpperCamelCase,
};
use proc_macro::TokenStream;
use proc_macro_crate::{FoundCrate, crate_name};
use proc_macro2::{Span, TokenStream as TokenStream2};
use quote::{ToTokens, quote};
use syn::{
    Data, DataEnum, DataStruct, DeriveInput, Field, Fields, GenericParam, Ident, LitBool, LitInt,
    LitStr, Token, Type, TypePath, meta::ParseNestedMeta, parse_macro_input,
    punctuated::Punctuated, spanned::Spanned,
};

#[proc_macro_derive(LLMData, attributes(typia, serde))]
pub fn derive_llm_data(input: TokenStream) -> TokenStream {
    let input = parse_macro_input!(input as DeriveInput);
    match expand_llm_data(&input) {
        Ok(tokens) => tokens.into(),
        Err(error) => error.into_compile_error().into(),
    }
}

fn expand_llm_data(input: &DeriveInput) -> syn::Result<TokenStream2> {
    match input.data {
        Data::Struct(_) | Data::Enum(_) => {}
        Data::Union(_) => {
            return Err(syn::Error::new_spanned(
                input,
                "`LLMData` can only be derived for structs and enums",
            ));
        }
    }

    let typia_path = typia_path();
    let ident = &input.ident;
    let validate_generics = add_validate_bounds(&input.generics, &typia_path);
    let (impl_generics, ty_generics, where_clause) = validate_generics.split_for_impl();

    let validate_impl = match &input.data {
        Data::Struct(data) => expand_struct_validate(input, data, &typia_path)?,
        Data::Enum(data) => expand_enum_validate(input, data, &typia_path)?,
        Data::Union(_) => unreachable!(),
    };

    Ok(quote! {
        impl #impl_generics #typia_path::LLMData for #ident #ty_generics #where_clause {}
        #validate_impl
    })
}

fn expand_struct_validate(
    input: &DeriveInput,
    data: &DataStruct,
    typia_path: &TokenStream2,
) -> syn::Result<TokenStream2> {
    let ident = &input.ident;
    let validate_generics = add_validate_bounds(&input.generics, typia_path);
    let (impl_generics, ty_generics, where_clause) = validate_generics.split_for_impl();

    let body = match &data.fields {
        Fields::Named(fields) => {
            let struct_options = parse_struct_serde_options(input)?;
            expand_named_struct_validate(fields, &struct_options, typia_path)?
        }
        Fields::Unnamed(_) | Fields::Unit => {
            quote! {
                if __strict {
                    #typia_path::__private::validate_with_serde::<Self>(__input)
                } else {
                    #typia_path::__private::validate_with_serde::<Self>(__input)
                }
            }
        }
    };

    Ok(quote! {
        impl #impl_generics #typia_path::Validate for #ident #ty_generics #where_clause {
            fn validate(value: #typia_path::serde_json::Value) -> #typia_path::IValidation<Self> {
                let __input = value;
                let __strict = false;
                #body
            }

            fn validate_equals(value: #typia_path::serde_json::Value) -> #typia_path::IValidation<Self> {
                let __input = value;
                let __strict = true;
                #body
            }
        }
    })
}

fn expand_named_struct_validate(
    fields: &syn::FieldsNamed,
    struct_options: &StructSerdeOptions,
    typia_path: &TokenStream2,
) -> syn::Result<TokenStream2> {
    let mut field_blocks = Vec::new();
    let mut known_fields = Vec::new();
    let mut has_flatten = false;

    for field in &fields.named {
        let field_ty = &field.ty;
        let field_options = field_serde_options(field, struct_options)?;
        if field_options.flatten {
            has_flatten = true;
        }
        let field_name = field_options.wire_name;
        let field_name_lit = LitStr::new(&field_name, Span::call_site());
        if !field_options.flatten {
            known_fields.push(field_name_lit.clone());
        }

        let tags = parse_typia_tags(&field.attrs)?;
        validate_tags_for_type(&tags, &field.ty)?;

        if field_options.skip_deserializing {
            // Serde skips these fields during deserialization and injects
            // defaults instead, so runtime validation must not enforce
            // requiredness or field-level value constraints for input keys.
            continue;
        }

        let optional = is_option_type(field_ty) || field_options.has_default;
        let apply_tags = if tags.is_empty() {
            quote! {}
        } else {
            let tag_tokens = quote_runtime_tags(&tags, typia_path);
            if optional {
                quote! {
                    if !__field_value.is_null() {
                        let __typia_tags = #tag_tokens;
                        #typia_path::__private::apply_tags(__field_value, &__field_path, &__typia_tags, &mut __errors);
                    }
                }
            } else {
                quote! {
                    let __typia_tags = #tag_tokens;
                    #typia_path::__private::apply_tags(__field_value, &__field_path, &__typia_tags, &mut __errors);
                }
            }
        };

        let missing_behavior = if optional {
            quote! {}
        } else {
            quote! {
                __errors.push(#typia_path::IValidationError {
                    path: __field_path,
                    expected: "required property".to_owned(),
                    value: #typia_path::serde_json::Value::Null,
                    description: Some("missing required field".to_owned()),
                });
            }
        };

        if field_options.flatten {
            field_blocks.push(quote! {
                // Flattened fields consume keys from the parent object.
                // Keep this validation non-strict even in validate_equals to
                // avoid false unknown-field errors from sibling parent keys.
                // Remove this workaround once strict flattened-key ownership
                // analysis is implemented in the derive validator.
                let __validated_field = <#field_ty as #typia_path::Validate>::validate(__root.clone());
                match __validated_field {
                    #typia_path::IValidation::Success { .. } => {}
                    #typia_path::IValidation::Failure { errors: __nested_errors, .. } => {
                        #typia_path::__private::merge_prefixed_errors(
                            &mut __errors,
                            "$input",
                            __nested_errors,
                        );
                    }
                }
            });
        } else {
            field_blocks.push(quote! {
                let __field_path = #typia_path::__private::join_object_path("$input", #field_name_lit);
                match __object.get(#field_name_lit) {
                    Some(__field_value) => {
                        let __validated_field = if __strict {
                            <#field_ty as #typia_path::Validate>::validate_equals(__field_value.clone())
                        } else {
                            <#field_ty as #typia_path::Validate>::validate(__field_value.clone())
                        };
                        match __validated_field {
                            #typia_path::IValidation::Success { .. } => {}
                            #typia_path::IValidation::Failure { errors: __nested_errors, .. } => {
                                #typia_path::__private::merge_prefixed_errors(
                                    &mut __errors,
                                    &__field_path,
                                    __nested_errors,
                                );
                            }
                        }
                        #apply_tags
                    }
                    None => {
                        #missing_behavior
                    }
                }
            });
        }
    }

    let strict_unknown_check = if has_flatten {
        quote! {}
    } else {
        quote! {
            if __strict {
                let __known_fields = [#(#known_fields),*];
                for __unknown_key in __object.keys() {
                    if !__known_fields.contains(&__unknown_key.as_str()) {
                        __errors.push(#typia_path::IValidationError {
                            path: #typia_path::__private::join_object_path("$input", __unknown_key),
                            expected: "undefined".to_owned(),
                            value: __object
                                .get(__unknown_key)
                                .cloned()
                                .unwrap_or(#typia_path::serde_json::Value::Null),
                            description: Some("unexpected property".to_owned()),
                        });
                    }
                }
            }
        }
    };

    Ok(quote! {
        let __root = __input.clone();
        let __object = match __root.as_object() {
            Some(__object) => __object,
            None => {
                return #typia_path::IValidation::Failure {
                    data: __root.clone(),
                    errors: vec![#typia_path::IValidationError {
                        path: "$input".to_owned(),
                        expected: "object".to_owned(),
                        value: __root,
                        description: Some("expected an object value".to_owned()),
                    }],
                };
            }
        };

        let mut __errors = Vec::<#typia_path::IValidationError>::new();
        #(#field_blocks)*
        #strict_unknown_check

        if __errors.is_empty() {
            match #typia_path::__private::validate_with_serde::<Self>(__root.clone()) {
                #typia_path::IValidation::Success { data } => #typia_path::IValidation::Success { data },
                #typia_path::IValidation::Failure { errors, .. } => {
                    #typia_path::IValidation::Failure {
                        data: __root,
                        errors,
                    }
                }
            }
        } else {
            #typia_path::IValidation::Failure {
                data: __root,
                errors: __errors,
            }
        }
    })
}

#[allow(clippy::enum_variant_names)]
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
                "unsupported serde rename rule",
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

struct StructSerdeOptions {
    rename_all_deserialize: Option<RenameRule>,
    default: bool,
}

struct FieldSerdeOptions {
    wire_name: String,
    has_default: bool,
    flatten: bool,
    skip_deserializing: bool,
}

fn expand_enum_validate(
    input: &DeriveInput,
    data: &DataEnum,
    typia_path: &TokenStream2,
) -> syn::Result<TokenStream2> {
    // Parse and validate tags on variant fields now, while enum runtime path
    // still delegates to serde validation. This keeps compile-time diagnostics
    // for tag syntax and target compatibility stable.
    for variant in &data.variants {
        match &variant.fields {
            Fields::Named(fields) => {
                for field in &fields.named {
                    let tags = parse_typia_tags(&field.attrs)?;
                    validate_tags_for_type(&tags, &field.ty)?;
                }
            }
            Fields::Unnamed(fields) => {
                for field in &fields.unnamed {
                    let tags = parse_typia_tags(&field.attrs)?;
                    validate_tags_for_type(&tags, &field.ty)?;
                }
            }
            Fields::Unit => {}
        }
    }

    let ident = &input.ident;
    let validate_generics = add_validate_bounds(&input.generics, typia_path);
    let (impl_generics, ty_generics, where_clause) = validate_generics.split_for_impl();

    Ok(quote! {
        impl #impl_generics #typia_path::Validate for #ident #ty_generics #where_clause {
            fn validate(value: #typia_path::serde_json::Value) -> #typia_path::IValidation<Self> {
                #typia_path::__private::validate_with_serde::<Self>(value)
            }

            fn validate_equals(value: #typia_path::serde_json::Value) -> #typia_path::IValidation<Self> {
                #typia_path::__private::validate_with_serde::<Self>(value)
            }
        }
    })
}

#[derive(Clone)]
enum ParsedTag {
    MinLength {
        value: usize,
        span: Span,
    },
    MaxLength {
        value: usize,
        span: Span,
    },
    MinItems {
        value: usize,
        span: Span,
    },
    MaxItems {
        value: usize,
        span: Span,
    },
    UniqueItems {
        value: bool,
        span: Span,
    },
    Minimum {
        value: f64,
        span: Span,
    },
    Maximum {
        value: f64,
        span: Span,
    },
    ExclusiveMinimum {
        value: f64,
        span: Span,
    },
    ExclusiveMaximum {
        value: f64,
        span: Span,
    },
    MultipleOf {
        value: f64,
        span: Span,
    },
    Pattern {
        value: String,
        span: Span,
    },
    Format {
        value: String,
        span: Span,
    },
    Type {
        value: String,
        span: Span,
    },
    Items {
        tags: Vec<ParsedTag>,
        span: Span,
    },
    Keys {
        tags: Vec<ParsedTag>,
        span: Span,
    },
    Values {
        tags: Vec<ParsedTag>,
        span: Span,
    },
    Metadata {
        kind: String,
        args: Vec<String>,
        span: Span,
    },
}

impl ParsedTag {
    fn kind_name(&self) -> &str {
        match self {
            Self::MinLength { .. } => "minLength",
            Self::MaxLength { .. } => "maxLength",
            Self::MinItems { .. } => "minItems",
            Self::MaxItems { .. } => "maxItems",
            Self::UniqueItems { .. } => "uniqueItems",
            Self::Minimum { .. } => "minimum",
            Self::Maximum { .. } => "maximum",
            Self::ExclusiveMinimum { .. } => "exclusiveMinimum",
            Self::ExclusiveMaximum { .. } => "exclusiveMaximum",
            Self::MultipleOf { .. } => "multipleOf",
            Self::Pattern { .. } => "pattern",
            Self::Format { .. } => "format",
            Self::Type { .. } => "type",
            Self::Items { .. } => "items",
            Self::Keys { .. } => "keys",
            Self::Values { .. } => "values",
            Self::Metadata { kind, .. } => kind,
        }
    }

    fn span(&self) -> Span {
        match self {
            Self::MinLength { span, .. }
            | Self::MaxLength { span, .. }
            | Self::MinItems { span, .. }
            | Self::MaxItems { span, .. }
            | Self::UniqueItems { span, .. }
            | Self::Minimum { span, .. }
            | Self::Maximum { span, .. }
            | Self::ExclusiveMinimum { span, .. }
            | Self::ExclusiveMaximum { span, .. }
            | Self::MultipleOf { span, .. }
            | Self::Pattern { span, .. }
            | Self::Format { span, .. }
            | Self::Type { span, .. }
            | Self::Items { span, .. }
            | Self::Keys { span, .. }
            | Self::Values { span, .. }
            | Self::Metadata { span, .. } => *span,
        }
    }

    fn is_duplicate_exclusive(&self) -> bool {
        if matches!(
            self,
            Self::MinLength { .. }
                | Self::MaxLength { .. }
                | Self::MinItems { .. }
                | Self::MaxItems { .. }
                | Self::UniqueItems { .. }
                | Self::Minimum { .. }
                | Self::Maximum { .. }
                | Self::ExclusiveMinimum { .. }
                | Self::ExclusiveMaximum { .. }
                | Self::MultipleOf { .. }
                | Self::Pattern { .. }
                | Self::Format { .. }
                | Self::Type { .. }
        ) {
            return true;
        }

        match self {
            Self::Metadata { kind, .. } => {
                kind == "default" || kind == "example" || kind == "examples" || kind == "sequence"
            }
            _ => false,
        }
    }
}

fn parse_typia_tags(attrs: &[syn::Attribute]) -> syn::Result<Vec<ParsedTag>> {
    let mut tags = Vec::new();
    for attr in attrs {
        if !attr.path().is_ident("typia") {
            continue;
        }
        attr.parse_nested_meta(|meta| {
            if meta.path.is_ident("tags") {
                parse_tag_list(meta, &mut tags)
            } else {
                Err(syn::Error::new_spanned(
                    meta.path,
                    "unsupported `#[typia(...)]` item; expected `tags(...)`",
                ))
            }
        })?;
    }
    Ok(tags)
}

fn parse_tag_list(meta: ParseNestedMeta<'_>, output: &mut Vec<ParsedTag>) -> syn::Result<()> {
    meta.parse_nested_meta(|tag_meta| {
        output.push(parse_one_tag(tag_meta)?);
        Ok(())
    })
}

fn parse_one_tag(meta: ParseNestedMeta<'_>) -> syn::Result<ParsedTag> {
    let Some(ident) = meta.path.get_ident() else {
        return Err(syn::Error::new_spanned(
            meta.path,
            "tag name must be a simple lowerCamelCase identifier",
        ));
    };
    let name = ident.to_string();
    let span = ident.span();
    match name.as_str() {
        "minLength" => Ok(ParsedTag::MinLength {
            value: parse_usize_arg(&meta)?,
            span,
        }),
        "maxLength" => Ok(ParsedTag::MaxLength {
            value: parse_usize_arg(&meta)?,
            span,
        }),
        "minItems" => Ok(ParsedTag::MinItems {
            value: parse_usize_arg(&meta)?,
            span,
        }),
        "maxItems" => Ok(ParsedTag::MaxItems {
            value: parse_usize_arg(&meta)?,
            span,
        }),
        "uniqueItems" => Ok(ParsedTag::UniqueItems {
            value: parse_optional_bool_arg(&meta)?,
            span,
        }),
        "minimum" => Ok(ParsedTag::Minimum {
            value: parse_f64_arg(&meta)?,
            span,
        }),
        "maximum" => Ok(ParsedTag::Maximum {
            value: parse_f64_arg(&meta)?,
            span,
        }),
        "exclusiveMinimum" => Ok(ParsedTag::ExclusiveMinimum {
            value: parse_f64_arg(&meta)?,
            span,
        }),
        "exclusiveMaximum" => Ok(ParsedTag::ExclusiveMaximum {
            value: parse_f64_arg(&meta)?,
            span,
        }),
        "multipleOf" => Ok(ParsedTag::MultipleOf {
            value: parse_f64_arg(&meta)?,
            span,
        }),
        "pattern" => Ok(ParsedTag::Pattern {
            value: parse_string_arg(&meta)?,
            span,
        }),
        "format" => {
            let value = parse_string_arg(&meta)?;
            let allowed = BTreeSet::from([
                "byte",
                "password",
                "regex",
                "uuid",
                "email",
                "hostname",
                "idn-email",
                "idn-hostname",
                "iri",
                "iri-reference",
                "ipv4",
                "ipv6",
                "uri",
                "uri-reference",
                "uri-template",
                "url",
                "date-time",
                "date",
                "time",
                "duration",
                "json-pointer",
                "relative-json-pointer",
            ]);
            if !allowed.contains(value.as_str()) {
                return Err(syn::Error::new(
                    span,
                    format!("unsupported format `{value}`"),
                ));
            }
            Ok(ParsedTag::Format { value, span })
        }
        "type" => {
            let value = parse_string_arg(&meta)?;
            let allowed = BTreeSet::from(["int32", "uint32", "int64", "uint64", "float", "double"]);
            if !allowed.contains(value.as_str()) {
                return Err(syn::Error::new(
                    span,
                    format!("unsupported type tag `{value}`"),
                ));
            }
            Ok(ParsedTag::Type { value, span })
        }
        "items" => Ok(ParsedTag::Items {
            tags: parse_nested_tags_group(&meta, "items")?,
            span,
        }),
        "keys" => Ok(ParsedTag::Keys {
            tags: parse_nested_tags_group(&meta, "keys")?,
            span,
        }),
        "values" => Ok(ParsedTag::Values {
            tags: parse_nested_tags_group(&meta, "values")?,
            span,
        }),
        "default" | "example" | "examples" | "sequence" | "contentMediaType"
        | "jsonSchemaPlugin" | "constant" => Ok(ParsedTag::Metadata {
            kind: name,
            args: parse_metadata_args(&meta)?,
            span,
        }),
        _ => Err(syn::Error::new(
            span,
            format!("unsupported typia tag `{name}`"),
        )),
    }
}

fn parse_nested_tags_group(
    meta: &ParseNestedMeta<'_>,
    context: &str,
) -> syn::Result<Vec<ParsedTag>> {
    let mut tags = Vec::new();
    meta.parse_nested_meta(|nested| {
        if nested.path.is_ident("tags") {
            parse_tag_list(nested, &mut tags)
        } else {
            Err(syn::Error::new_spanned(
                nested.path,
                format!("`{context}(...)` expects `tags(...)`"),
            ))
        }
    })?;
    if tags.is_empty() {
        return Err(syn::Error::new(
            meta.path.span(),
            format!("`{context}(...)` requires at least one nested tag"),
        ));
    }
    Ok(tags)
}

fn parse_metadata_args(meta: &ParseNestedMeta<'_>) -> syn::Result<Vec<String>> {
    if meta.input.is_empty() {
        return Ok(Vec::new());
    }

    let content;
    syn::parenthesized!(content in meta.input);
    let exprs: Punctuated<syn::Expr, Token![,]> =
        content.parse_terminated(|input| input.parse(), Token![,])?;
    Ok(exprs
        .iter()
        .map(ToTokens::to_token_stream)
        .map(|tokens| tokens.to_string())
        .collect())
}

fn parse_usize_arg(meta: &ParseNestedMeta<'_>) -> syn::Result<usize> {
    let content;
    syn::parenthesized!(content in meta.input);
    let lit: LitInt = content.parse()?;
    if !content.is_empty() {
        return Err(syn::Error::new(
            content.span(),
            "unexpected trailing tokens",
        ));
    }
    lit.base10_parse()
}

fn parse_optional_bool_arg(meta: &ParseNestedMeta<'_>) -> syn::Result<bool> {
    if meta.input.is_empty() {
        return Ok(true);
    }
    let content;
    syn::parenthesized!(content in meta.input);
    if content.is_empty() {
        return Ok(true);
    }
    let lit: LitBool = content.parse()?;
    if !content.is_empty() {
        return Err(syn::Error::new(
            content.span(),
            "unexpected trailing tokens",
        ));
    }
    Ok(lit.value)
}

fn parse_f64_arg(meta: &ParseNestedMeta<'_>) -> syn::Result<f64> {
    let content;
    syn::parenthesized!(content in meta.input);
    let expression: syn::Expr = content.parse()?;
    if !content.is_empty() {
        return Err(syn::Error::new(
            content.span(),
            "unexpected trailing tokens",
        ));
    }
    parse_f64_expression(&expression)
}

fn parse_f64_expression(expression: &syn::Expr) -> syn::Result<f64> {
    match expression {
        syn::Expr::Lit(literal) => match &literal.lit {
            syn::Lit::Float(value) => value.base10_parse(),
            syn::Lit::Int(value) => value.base10_parse(),
            _ => Err(syn::Error::new_spanned(
                literal,
                "expected a numeric literal",
            )),
        },
        syn::Expr::Paren(paren) => parse_f64_expression(&paren.expr),
        syn::Expr::Unary(unary) => match unary.op {
            syn::UnOp::Neg(_) => Ok(-parse_f64_expression(&unary.expr)?),
            _ => Err(syn::Error::new_spanned(
                unary,
                "expected a signed numeric literal",
            )),
        },
        _ => Err(syn::Error::new_spanned(
            expression,
            "expected a numeric literal",
        )),
    }
}

fn parse_string_arg(meta: &ParseNestedMeta<'_>) -> syn::Result<String> {
    let content;
    syn::parenthesized!(content in meta.input);
    let lit: LitStr = content.parse()?;
    if !content.is_empty() {
        return Err(syn::Error::new(
            content.span(),
            "unexpected trailing tokens",
        ));
    }
    Ok(lit.value())
}

fn validate_tags_for_type(tags: &[ParsedTag], ty: &Type) -> syn::Result<()> {
    if tags.is_empty() {
        return Ok(());
    }

    check_exclusive_rules(tags)?;
    validate_tag_targets(tags, ty)
}

fn check_exclusive_rules(tags: &[ParsedTag]) -> syn::Result<()> {
    let mut seen = HashMap::<&str, Span>::new();
    for tag in tags {
        if tag.is_duplicate_exclusive()
            && let Some(previous) = seen.insert(tag.kind_name(), tag.span())
        {
            return Err(syn::Error::new(
                tag.span(),
                format!(
                    "tag `{}` cannot be declared multiple times (previous declaration at {:?})",
                    tag.kind_name(),
                    previous,
                ),
            ));
        }
    }

    let has_format = tags
        .iter()
        .any(|tag| matches!(tag, ParsedTag::Format { .. }));
    let has_pattern = tags
        .iter()
        .any(|tag| matches!(tag, ParsedTag::Pattern { .. }));
    if has_format && has_pattern {
        return Err(syn::Error::new(
            Span::call_site(),
            "`format(...)` and `pattern(...)` are mutually exclusive",
        ));
    }

    let has_minimum = tags
        .iter()
        .any(|tag| matches!(tag, ParsedTag::Minimum { .. }));
    let has_exclusive_minimum = tags
        .iter()
        .any(|tag| matches!(tag, ParsedTag::ExclusiveMinimum { .. }));
    if has_minimum && has_exclusive_minimum {
        return Err(syn::Error::new(
            Span::call_site(),
            "`minimum(...)` and `exclusiveMinimum(...)` are mutually exclusive",
        ));
    }

    let has_maximum = tags
        .iter()
        .any(|tag| matches!(tag, ParsedTag::Maximum { .. }));
    let has_exclusive_maximum = tags
        .iter()
        .any(|tag| matches!(tag, ParsedTag::ExclusiveMaximum { .. }));
    if has_maximum && has_exclusive_maximum {
        return Err(syn::Error::new(
            Span::call_site(),
            "`maximum(...)` and `exclusiveMaximum(...)` are mutually exclusive",
        ));
    }

    Ok(())
}

fn validate_tag_targets(tags: &[ParsedTag], ty: &Type) -> syn::Result<()> {
    let unwrapped = unwrap_option_type(ty);
    if let Some((key, _)) = extract_map_types(unwrapped)
        && !is_string_type(key)
    {
        return Err(syn::Error::new_spanned(
            key,
            "map key type must be `String` for typia derive validation",
        ));
    }

    for tag in tags {
        match tag {
            ParsedTag::MinLength { .. }
            | ParsedTag::MaxLength { .. }
            | ParsedTag::Pattern { .. }
            | ParsedTag::Format { .. } => {
                if !is_string_type(unwrapped) {
                    return Err(syn::Error::new_spanned(
                        unwrapped,
                        format!(
                            "tag `{}` can only be applied to string targets",
                            tag.kind_name()
                        ),
                    ));
                }
            }
            ParsedTag::MinItems { .. }
            | ParsedTag::MaxItems { .. }
            | ParsedTag::UniqueItems { .. } => {
                if extract_array_item_type(unwrapped).is_none() {
                    return Err(syn::Error::new_spanned(
                        unwrapped,
                        format!(
                            "tag `{}` can only be applied to array targets",
                            tag.kind_name()
                        ),
                    ));
                }
            }
            ParsedTag::Minimum { .. }
            | ParsedTag::Maximum { .. }
            | ParsedTag::ExclusiveMinimum { .. }
            | ParsedTag::ExclusiveMaximum { .. }
            | ParsedTag::MultipleOf { .. }
            | ParsedTag::Type { .. } => {
                if !is_number_type(unwrapped) {
                    return Err(syn::Error::new_spanned(
                        unwrapped,
                        format!(
                            "tag `{}` can only be applied to numeric targets",
                            tag.kind_name()
                        ),
                    ));
                }
            }
            ParsedTag::Items { tags: nested, .. } => {
                let Some(item_ty) = extract_array_item_type(unwrapped) else {
                    return Err(syn::Error::new_spanned(
                        unwrapped,
                        "`items(tags(...))` can only be applied to array targets",
                    ));
                };
                validate_tags_for_type(nested, item_ty)?;
            }
            ParsedTag::Keys { tags: nested, .. } => {
                let Some((key_ty, _)) = extract_map_types(unwrapped) else {
                    return Err(syn::Error::new_spanned(
                        unwrapped,
                        "`keys(tags(...))` can only be applied to map targets",
                    ));
                };
                if !is_string_type(key_ty) {
                    return Err(syn::Error::new_spanned(
                        key_ty,
                        "map key type must be `String` for `keys(tags(...))`",
                    ));
                }
                validate_tags_for_type(nested, key_ty)?;
            }
            ParsedTag::Values { tags: nested, .. } => {
                let Some((_, value_ty)) = extract_map_types(unwrapped) else {
                    return Err(syn::Error::new_spanned(
                        unwrapped,
                        "`values(tags(...))` can only be applied to map targets",
                    ));
                };
                validate_tags_for_type(nested, value_ty)?;
            }
            ParsedTag::Metadata { .. } => {}
        }
    }
    Ok(())
}

fn quote_runtime_tags(tags: &[ParsedTag], typia_path: &TokenStream2) -> TokenStream2 {
    let tags = tags.iter().map(|tag| quote_runtime_tag(tag, typia_path));
    quote!(::std::vec![#(#tags),*])
}

fn quote_runtime_tag(tag: &ParsedTag, typia_path: &TokenStream2) -> TokenStream2 {
    match tag {
        ParsedTag::MinLength { value, .. } => quote!(#typia_path::TagRuntime::MinLength(#value)),
        ParsedTag::MaxLength { value, .. } => quote!(#typia_path::TagRuntime::MaxLength(#value)),
        ParsedTag::MinItems { value, .. } => quote!(#typia_path::TagRuntime::MinItems(#value)),
        ParsedTag::MaxItems { value, .. } => quote!(#typia_path::TagRuntime::MaxItems(#value)),
        ParsedTag::UniqueItems { value, .. } => {
            quote!(#typia_path::TagRuntime::UniqueItems(#value))
        }
        ParsedTag::Minimum { value, .. } => quote!(#typia_path::TagRuntime::Minimum(#value)),
        ParsedTag::Maximum { value, .. } => quote!(#typia_path::TagRuntime::Maximum(#value)),
        ParsedTag::ExclusiveMinimum { value, .. } => {
            quote!(#typia_path::TagRuntime::ExclusiveMinimum(#value))
        }
        ParsedTag::ExclusiveMaximum { value, .. } => {
            quote!(#typia_path::TagRuntime::ExclusiveMaximum(#value))
        }
        ParsedTag::MultipleOf { value, .. } => quote!(#typia_path::TagRuntime::MultipleOf(#value)),
        ParsedTag::Pattern { value, .. } => {
            quote!(#typia_path::TagRuntime::Pattern(::std::string::String::from(#value)))
        }
        ParsedTag::Format { value, .. } => {
            quote!(#typia_path::TagRuntime::Format(::std::string::String::from(#value)))
        }
        ParsedTag::Type { value, .. } => {
            quote!(#typia_path::TagRuntime::Type(::std::string::String::from(#value)))
        }
        ParsedTag::Items { tags, .. } => {
            let inner = quote_runtime_tags(tags, typia_path);
            quote!(#typia_path::TagRuntime::Items(#inner))
        }
        ParsedTag::Keys { tags, .. } => {
            let inner = quote_runtime_tags(tags, typia_path);
            quote!(#typia_path::TagRuntime::Keys(#inner))
        }
        ParsedTag::Values { tags, .. } => {
            let inner = quote_runtime_tags(tags, typia_path);
            quote!(#typia_path::TagRuntime::Values(#inner))
        }
        ParsedTag::Metadata { kind, args, .. } => {
            quote!(#typia_path::TagRuntime::Metadata {
                kind: ::std::string::String::from(#kind),
                args: ::std::vec![#(::std::string::String::from(#args)),*],
            })
        }
    }
}

fn parse_struct_serde_options(input: &DeriveInput) -> syn::Result<StructSerdeOptions> {
    let mut options = StructSerdeOptions {
        rename_all_deserialize: None,
        default: false,
    };

    for attr in &input.attrs {
        if !attr.path().is_ident("serde") {
            continue;
        }
        attr.parse_nested_meta(|meta| {
            if meta.path.is_ident("default") {
                options.default = true;
                if meta.input.peek(Token![=]) {
                    let value = meta.value()?;
                    let _: LitStr = value.parse()?;
                }
                return Ok(());
            }

            if meta.path.is_ident("rename_all") {
                if meta.input.peek(Token![=]) {
                    let value = meta.value()?;
                    let lit: LitStr = value.parse()?;
                    options.rename_all_deserialize = Some(RenameRule::parse(&lit)?);
                    return Ok(());
                }

                meta.parse_nested_meta(|nested| {
                    if nested.path.is_ident("deserialize") {
                        let value = nested.value()?;
                        let lit: LitStr = value.parse()?;
                        options.rename_all_deserialize = Some(RenameRule::parse(&lit)?);
                    } else if nested.path.is_ident("serialize") {
                        let value = nested.value()?;
                        let _: LitStr = value.parse()?;
                    } else {
                        return Err(syn::Error::new_spanned(
                            nested.path,
                            "unsupported `serde(rename_all(...))` entry",
                        ));
                    }
                    Ok(())
                })?;
                return Ok(());
            }

            consume_unknown_serde_meta(&meta)
        })?;
    }

    Ok(options)
}

fn field_serde_options(
    field: &Field,
    struct_options: &StructSerdeOptions,
) -> syn::Result<FieldSerdeOptions> {
    let default_name = field
        .ident
        .as_ref()
        .map(ToString::to_string)
        .ok_or_else(|| {
            syn::Error::new_spanned(field, "unnamed field is not supported in this context")
        })?;

    let mut direct_rename: Option<String> = None;
    let mut deserialize_rename: Option<String> = None;
    let mut has_default = struct_options.default;
    let mut flatten = false;
    let mut skip_deserializing = false;
    for attr in &field.attrs {
        if !attr.path().is_ident("serde") {
            continue;
        }
        attr.parse_nested_meta(|meta| {
            if meta.path.is_ident("flatten") {
                flatten = true;
                return Ok(());
            }

            if meta.path.is_ident("skip") || meta.path.is_ident("skip_deserializing") {
                has_default = true;
                skip_deserializing = true;
                return Ok(());
            }

            if meta.path.is_ident("default") {
                has_default = true;
                if meta.input.peek(Token![=]) {
                    let value = meta.value()?;
                    let _: LitStr = value.parse()?;
                }
                return Ok(());
            }

            if meta.path.is_ident("rename") {
                if meta.input.peek(Token![=]) {
                    let value = meta.value()?;
                    let lit: LitStr = value.parse()?;
                    direct_rename = Some(lit.value());
                    return Ok(());
                }
                meta.parse_nested_meta(|nested| {
                    if nested.path.is_ident("deserialize") {
                        let value = nested.value()?;
                        let lit: LitStr = value.parse()?;
                        deserialize_rename = Some(lit.value());
                        return Ok(());
                    }

                    consume_unknown_serde_meta(&nested)
                })?;
                return Ok(());
            }

            consume_unknown_serde_meta(&meta)
        })?;
    }

    let renamed = if let Some(rule) = struct_options.rename_all_deserialize {
        rule.apply(&default_name)
    } else {
        default_name
    };

    Ok(FieldSerdeOptions {
        wire_name: deserialize_rename.or(direct_rename).unwrap_or(renamed),
        has_default,
        flatten,
        skip_deserializing,
    })
}

fn consume_unknown_serde_meta(meta: &ParseNestedMeta<'_>) -> syn::Result<()> {
    if meta.input.peek(Token![=]) {
        let value = meta.value()?;
        let _: TokenStream2 = value.parse()?;
        return Ok(());
    }

    if meta.input.peek(syn::token::Paren) {
        let content;
        syn::parenthesized!(content in meta.input);
        let _: TokenStream2 = content.parse()?;
    }

    Ok(())
}

fn is_option_type(ty: &Type) -> bool {
    match ty {
        Type::Path(type_path) => type_path
            .path
            .segments
            .last()
            .is_some_and(|segment| segment.ident == "Option"),
        _ => false,
    }
}

fn unwrap_option_type(ty: &Type) -> &Type {
    if let Some(inner) = extract_single_generic(ty, "Option") {
        inner
    } else {
        ty
    }
}

fn extract_array_item_type(ty: &Type) -> Option<&Type> {
    match ty {
        Type::Array(array) => Some(&array.elem),
        _ => extract_single_generic(ty, "Vec"),
    }
}

fn extract_map_types(ty: &Type) -> Option<(&Type, &Type)> {
    let Type::Path(TypePath { path, .. }) = ty else {
        return None;
    };
    let segment = path.segments.last()?;
    if segment.ident != "HashMap" && segment.ident != "BTreeMap" {
        return None;
    }
    let syn::PathArguments::AngleBracketed(args) = &segment.arguments else {
        return None;
    };
    let mut iter = args.args.iter();
    let key = match iter.next() {
        Some(syn::GenericArgument::Type(ty)) => ty,
        _ => return None,
    };
    let value = match iter.next() {
        Some(syn::GenericArgument::Type(ty)) => ty,
        _ => return None,
    };
    Some((key, value))
}

fn extract_single_generic<'a>(ty: &'a Type, ident: &str) -> Option<&'a Type> {
    let Type::Path(TypePath { path, .. }) = ty else {
        return None;
    };
    let segment = path.segments.last()?;
    if segment.ident != ident {
        return None;
    }
    let syn::PathArguments::AngleBracketed(args) = &segment.arguments else {
        return None;
    };
    let mut iter = args.args.iter();
    match iter.next() {
        Some(syn::GenericArgument::Type(ty)) => Some(ty),
        _ => None,
    }
}

fn is_string_type(ty: &Type) -> bool {
    match ty {
        Type::Path(TypePath { path, .. }) => path
            .segments
            .last()
            .is_some_and(|segment| segment.ident == "String"),
        Type::Reference(reference) => {
            matches!(
                &*reference.elem,
                Type::Path(TypePath { path, .. })
                    if path.segments.last().is_some_and(|segment| segment.ident == "str")
            )
        }
        _ => false,
    }
}

fn is_number_type(ty: &Type) -> bool {
    let Type::Path(TypePath { path, .. }) = ty else {
        return false;
    };
    let Some(segment) = path.segments.last() else {
        return false;
    };
    let ident = segment.ident.to_string();
    matches!(
        ident.as_str(),
        "i8" | "i16"
            | "i32"
            | "i64"
            | "i128"
            | "isize"
            | "u8"
            | "u16"
            | "u32"
            | "u64"
            | "u128"
            | "usize"
            | "f32"
            | "f64"
    )
}

fn add_validate_bounds(generics: &syn::Generics, typia_path: &TokenStream2) -> syn::Generics {
    let mut generics = generics.clone();
    for parameter in &mut generics.params {
        if let GenericParam::Type(type_param) = parameter {
            type_param
                .bounds
                .push(syn::parse_quote!(#typia_path::Validate));
        }
    }
    generics
}

fn typia_path() -> TokenStream2 {
    match crate_name("typia") {
        Ok(FoundCrate::Itself) => quote!(crate),
        Ok(FoundCrate::Name(name)) => {
            let ident = Ident::new(&name.replace('-', "_"), Span::call_site());
            quote!(::#ident)
        }
        Err(_) => quote!(::typia),
    }
}
