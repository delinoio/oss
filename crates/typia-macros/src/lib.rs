#![forbid(unsafe_code)]

//! Proc-macro derive implementation for `typia`.

use proc_macro::TokenStream;
use proc_macro_crate::{FoundCrate, crate_name};
use proc_macro2::{Span, TokenStream as TokenStream2};
use quote::quote;
use syn::{Data, DeriveInput, Ident, parse_macro_input};

#[proc_macro_derive(LLMData)]
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
    let generics = &input.generics;
    let (impl_generics, ty_generics, where_clause) = generics.split_for_impl();

    Ok(quote! {
        impl #impl_generics #typia_path::LLMData for #ident #ty_generics #where_clause {}
    })
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
