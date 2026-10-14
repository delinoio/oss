// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Optional SWC minification of edited bundle chunks (`--minify`).
//!
//! Bundle-mode transformations leave code that a minifier can shorten
//! further (folded branches such as `true?a:b`, single-use bindings, parameter
//! names exposed by inlining). After the pipeline, every edited ES module
//! chunk can be minified with SWC's compressor and mangler. The result is
//! kept only when it compresses smaller: Brotli (quality 11) must shrink and
//! gzip (level 9) must not grow, because minifying already-minified bundler
//! output sometimes compresses worse. Legal comments (`/*!`, `@license`,
//! `@preserve`) are kept; other comments are dropped after the compressor has
//! read annotations such as `/*#__PURE__*/`.
use std::io::Write as _;

use swc_core::{
    common::{
        FileName, GLOBALS, Globals, Mark, SourceMap, comments::SingleThreadedComments, sync::Lrc,
    },
    ecma::{
        ast::{EsVersion, Program},
        codegen::{
            Config, Emitter,
            text_writer::{JsWriter, omit_trailing_semi},
        },
        minifier::{
            optimize,
            option::{ExtraOptions, MangleOptions, MinifyOptions, terser::TerserCompressorOptions},
        },
        parser::{EsSyntax, Syntax, parse_file_as_module},
        transforms::base::{fixer::fixer, resolver},
        visit::VisitMutWith,
    },
};

/// Minified code and a source map from it to the input code.
pub(crate) struct Minified {
    pub code: String,
    pub map: sourcemap::SourceMap,
}

/// Compressed sizes that decide whether a minified chunk is kept.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) struct Compressed {
    pub gzip: usize,
    pub brotli: usize,
}

pub(crate) fn compressed(code: &str) -> Compressed {
    let mut gzip = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::best());
    let gzip = gzip
        .write_all(code.as_bytes())
        .ok()
        .and_then(|()| gzip.finish().ok())
        .map_or(usize::MAX, |bytes| bytes.len());
    let mut brotli = Vec::new();
    let parameters = brotli::enc::BrotliEncoderParams {
        quality: 11,
        lgwin: 22,
        ..Default::default()
    };
    let brotli = brotli::BrotliCompress(&mut code.as_bytes(), &mut brotli, &parameters)
        .map_or(usize::MAX, |_| brotli.len());
    Compressed { gzip, brotli }
}

/// Whether a minified version is worth publishing instead of `before`.
pub(crate) fn better(before: Compressed, after: Compressed) -> bool {
    after.brotli < before.brotli && after.gzip <= before.gzip
}

fn legal(text: &str) -> bool {
    text.starts_with('!') || text.contains("@license") || text.contains("@preserve")
}

/// Minifies an ES module. `None` when SWC cannot parse it or emit it; the
/// chunk then stays as the pipeline left it.
pub(crate) fn minify_module(code: &str) -> Option<Minified> {
    let cm: Lrc<SourceMap> = Default::default();
    let file = cm.new_source_file(Lrc::new(FileName::Anon), code.to_owned());
    GLOBALS.set(&Globals::new(), || {
        let unresolved = Mark::new();
        let top_level = Mark::new();
        let comments = SingleThreadedComments::default();
        let mut errors = Vec::new();
        let module = parse_file_as_module(
            &file,
            Syntax::Es(EsSyntax::default()),
            EsVersion::latest(),
            Some(&comments as &dyn swc_core::common::comments::Comments),
            &mut errors,
        )
        .ok()?;
        if !errors.is_empty() {
            return None;
        }
        let mut program = Program::Module(module);
        program.visit_mut_with(&mut resolver(unresolved, top_level, false));
        // The compressor defaults of SWC's terser-compatible options, as
        // `@swc/core` applies them, for a module.
        let compressor: TerserCompressorOptions =
            serde_json::from_str(r#"{"module":true,"toplevel":true}"#).ok()?;
        let options = MinifyOptions {
            compress: Some(compressor.into_config(cm.clone())),
            mangle: Some(MangleOptions {
                top_level: Some(true),
                ..Default::default()
            }),
            ..Default::default()
        };
        let mut program = optimize(
            program,
            cm.clone(),
            Some(&comments),
            None,
            &options,
            &ExtraOptions {
                unresolved_mark: unresolved,
                top_level_mark: top_level,
                mangle_name_cache: None,
            },
        );
        program.visit_mut_with(&mut fixer(Some(&comments)));
        {
            let (mut leading, mut trailing) = comments.borrow_all_mut();
            for list in leading.values_mut().chain(trailing.values_mut()) {
                list.retain(|comment| legal(&comment.text));
            }
        }
        let mut output = Vec::new();
        let mut mappings = Vec::new();
        {
            let mut emitter = Emitter {
                cfg: Config::default()
                    .with_minify(true)
                    .with_target(EsVersion::latest()),
                cm: cm.clone(),
                comments: Some(&comments),
                wr: omit_trailing_semi(JsWriter::new(
                    cm.clone(),
                    "\n",
                    &mut output,
                    Some(&mut mappings),
                )),
            };
            emitter.emit_program(&program).ok()?;
        }
        let code = String::from_utf8(output).ok()?;
        let mut json = Vec::new();
        cm.build_source_map(
            &mappings,
            None,
            swc_core::common::source_map::DefaultSourceMapGenConfig,
        )
        .to_writer(&mut json)
        .ok()?;
        let map = sourcemap::SourceMap::from_slice(&json).ok()?;
        Some(Minified { code, map })
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn minifies_and_maps_back_to_the_input() {
        let input = "const longName = 1 + 2;\nexport function compute(value) {\n  return value * \
                     longName;\n}\n";
        let minified = minify_module(input).unwrap();
        assert!(minified.code.len() < input.len(), "{}", minified.code);
        assert!(minified.code.contains("export"), "{}", minified.code);
        // `compute` stays exported under its name.
        assert!(minified.code.contains("compute"), "{}", minified.code);
        assert!(minified.map.tokens().count() > 0);
    }

    #[test]
    fn keeps_legal_comments_and_drops_others() {
        let input = "/*! keep me */\n// drop me\nexport const a = /*#__PURE__*/ f();\nfunction \
                     f() { return 1 }\n";
        let minified = minify_module(input).unwrap();
        assert!(minified.code.contains("keep me"), "{}", minified.code);
        assert!(!minified.code.contains("drop me"), "{}", minified.code);
    }

    #[test]
    fn rejects_unparsable_input() {
        assert!(minify_module("export const = ;").is_none());
    }

    #[test]
    fn keeps_minified_code_only_when_it_compresses_smaller() {
        let before = Compressed {
            gzip: 100,
            brotli: 90,
        };
        assert!(better(
            before,
            Compressed {
                gzip: 100,
                brotli: 89
            }
        ));
        assert!(!better(
            before,
            Compressed {
                gzip: 101,
                brotli: 80
            }
        ));
        assert!(!better(
            before,
            Compressed {
                gzip: 90,
                brotli: 90
            }
        ));
    }
}
