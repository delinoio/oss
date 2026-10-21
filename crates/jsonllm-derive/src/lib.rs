#![forbid(unsafe_code)]
#![doc = include_str!("../README.md")]

use std::collections::BTreeSet;

use proc_macro::TokenStream;
use proc_macro2::{Span, TokenStream as Tokens};
use quote::quote;
use syn::{Data, DeriveInput, Fields, LitStr, parse_macro_input};

/// Derive the `jsonllm::JsonGen` trait for a nonempty named struct.
///
/// Use this macro through the `jsonllm` facade. Every field type must implement
/// `jsonllm::JsonField`; generic parameters and existing where clauses are
/// preserved, with field-type bounds added to the generated implementation.
/// A serde derive is not required, and serde attributes do not control mapping.
///
/// # Field attributes
///
/// - `#[jsonllm(rename = "wire_name")]` sets the request and answer name.
/// - `#[jsonllm(instructions = "Extract this value")]` replaces instructions
///   collected from the field's doc comments. Multiple doc lines are joined
///   with newlines.
///
/// Names must match `[A-Za-z_][A-Za-z0-9_.-]{0,63}` and be unique within the
/// struct. Raw identifiers use the name without `r#`. Unknown or duplicate
/// options, struct-level attributes, enums, unions, empty structs, and tuple
/// structs are rejected. Unsupported field types produce trait-bound errors.
///
/// See the [facade guide](https://docs.rs/jsonllm) for executable examples of
/// generation, field attributes, generic structs, and custom scalar mappings.
#[proc_macro_derive(JsonGen, attributes(jsonllm))]
pub fn derive_json_gen(input: TokenStream) -> TokenStream {
    let input = parse_macro_input!(input as DeriveInput);
    let path = match proc_macro_crate::crate_name("jsonllm") {
        Ok(proc_macro_crate::FoundCrate::Itself) => quote!(::jsonllm),
        Ok(proc_macro_crate::FoundCrate::Name(name)) => {
            let ident = syn::Ident::new(&name, Span::call_site());
            quote!(::#ident)
        }
        Err(error) => {
            return syn::Error::new_spanned(&input.ident, error)
                .into_compile_error()
                .into();
        }
    };
    expand(&input, &path)
        .unwrap_or_else(syn::Error::into_compile_error)
        .into()
}

fn expand(input: &DeriveInput, path: &Tokens) -> syn::Result<Tokens> {
    if let Some(attr) = input.attrs.iter().find(|a| a.path().is_ident("jsonllm")) {
        return Err(syn::Error::new_spanned(
            attr,
            "jsonllm attributes belong on fields",
        ));
    }
    let fields = match &input.data {
        Data::Struct(data) => match &data.fields {
            Fields::Named(fields) if !fields.named.is_empty() => &fields.named,
            _ => {
                return Err(syn::Error::new_spanned(
                    input,
                    "JsonGen requires a nonempty named struct",
                ));
            }
        },
        _ => {
            return Err(syn::Error::new_spanned(
                input,
                "JsonGen requires a named struct",
            ));
        }
    };
    let mut generics = input.generics.clone();
    let mut schemas = Vec::new();
    let mut decoders = Vec::new();
    let mut names = BTreeSet::new();
    for field in fields {
        let ident = field.ident.as_ref().expect("named field");
        let ty = &field.ty;
        let mut rename: Option<LitStr> = None;
        let mut instructions: Option<LitStr> = None;
        let mut docs = Vec::new();
        for attr in &field.attrs {
            if attr.path().is_ident("jsonllm") {
                attr.parse_nested_meta(|meta| {
                    let target = if meta.path.is_ident("rename") {
                        &mut rename
                    } else if meta.path.is_ident("instructions") {
                        &mut instructions
                    } else {
                        return Err(meta.error("expected rename or instructions"));
                    };
                    if target.is_some() {
                        return Err(meta.error("duplicate jsonllm option"));
                    }
                    *target = Some(meta.value()?.parse()?);
                    Ok(())
                })?;
            } else if attr.path().is_ident("doc")
                && let syn::Meta::NameValue(meta) = &attr.meta
                && let syn::Expr::Lit(expr) = &meta.value
                && let syn::Lit::Str(value) = &expr.lit
            {
                docs.push(value.value().trim().to_owned());
            }
        }
        let rust_name = ident.to_string();
        let name = rename.unwrap_or_else(|| {
            LitStr::new(
                rust_name.strip_prefix("r#").unwrap_or(&rust_name),
                ident.span(),
            )
        });
        let value = name.value();
        let mut bytes = value.bytes();
        if value.len() > 64
            || !bytes
                .next()
                .is_some_and(|b| b.is_ascii_alphabetic() || b == b'_')
            || !bytes.all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'.' | b'-'))
        {
            return Err(syn::Error::new_spanned(&name, "invalid Clairic field name"));
        }
        if !names.insert(value) {
            return Err(syn::Error::new_spanned(
                &name,
                "duplicate Clairic field name",
            ));
        }
        let description = instructions
            .or_else(|| (!docs.is_empty()).then(|| LitStr::new(&docs.join("\n"), ident.span())));
        let description =
            description.map(|text| quote!(field.instructions = Some(#text.to_owned());));
        generics
            .make_where_clause()
            .predicates
            .push(syn::parse_quote!(#ty: #path::JsonField));
        schemas.push(quote! {
            let mut field = <#ty as #path::JsonField>::field();
            #description
            fields.insert(#name.to_owned(), field);
        });
        decoders.push(quote!(#ident: #path::decode_field::<#ty>(response, #name)?));
    }
    let ident = &input.ident;
    let (impl_generics, ty_generics, where_clause) = generics.split_for_impl();
    Ok(quote! {
        impl #impl_generics #path::JsonGen for #ident #ty_generics #where_clause {
            fn fields() -> ::std::collections::BTreeMap<::std::string::String, #path::Field> {
                let mut fields = ::std::collections::BTreeMap::new();
                #(#schemas)*
                fields
            }
            fn from_response(response: &#path::Response) -> ::std::result::Result<Self, #path::Error> {
                if response.truncated { return Err(#path::Error::Truncated); }
                Ok(Self { #(#decoders),* })
            }
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_unsupported_shapes_and_attributes() {
        for source in [
            "enum X { A }",
            "union X { a: u32 }",
            "struct X;",
            "struct X {}",
            "struct X(String);",
            "#[jsonllm(rename = \"x\")] struct X { a: String }",
            "struct X { #[jsonllm(unknown = \"x\")] a: String }",
            "struct X { #[jsonllm(rename = \"\")] a: String }",
            "struct X { #[jsonllm(rename = \"1bad\")] a: String }",
            "struct X { #[jsonllm(rename = \"a\")] a: String, #[jsonllm(rename = \"a\")] b: \
             String }",
            "struct X { #[jsonllm(rename = \"a\", rename = \"b\")] a: String }",
        ] {
            let input = syn::parse_str(source).unwrap();
            assert!(expand(&input, &quote!(::jsonllm)).is_err(), "{source}");
        }
    }
}
