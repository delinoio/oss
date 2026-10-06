// SPDX-License-Identifier: Apache-2.0
//! Automatic Yarn runtime activation in owned Node workloads.
//! Preparation also runs in pre-main native constructors; keep runtime tracing
//! in the supervisor and exec mediation callers.

use std::{
    ffi::{OsStr, OsString},
    path::{Path, PathBuf},
};

use crate::{
    diagnostic::{Code, Error, Result},
    graph::Snapshot,
};

pub struct Loader {
    commonjs: PathBuf,
    esm: Option<PathBuf>,
}

impl Loader {
    pub fn from_snapshot(snapshot: &Snapshot) -> Self {
        let esm = snapshot.manifest_path.with_file_name(".pnp.loader.mjs");
        Self {
            commonjs: snapshot.manifest_path.clone(),
            esm: snapshot
                .inputs
                .iter()
                .any(|input| input.path == esm)
                .then_some(esm),
        }
    }

    pub fn has_esm(&self) -> bool {
        self.esm.is_some()
    }

    /// Activate Yarn before caller preloads, retaining unrelated option bytes
    /// and their order. Native programs ignore these Node options.
    pub fn options(&self, inherited: Option<&OsStr>) -> Result<OsString> {
        self.options_in(inherited, None)
    }

    /// Bind relative selected loaders only when the workload's cwd is
    /// known. Opaque spawn actions require another pass in the new process.
    pub fn options_in(&self, inherited: Option<&OsStr>, cwd: Option<&Path>) -> Result<OsString> {
        let inherited = inherited.unwrap_or_else(|| OsStr::new(""));
        let tokens = tokens(inherited.as_encoded_bytes());
        // Keep malformed caller options malformed. Node must retain its own
        // diagnostic instead of pnport repairing or reinterpreting them.
        if tokens.is_none() {
            return Ok(inherited.to_owned());
        }
        let tokens = tokens.unwrap();
        let requires: Vec<_> = tokens
            .iter()
            .enumerate()
            .filter_map(|(index, _)| require_token(&tokens, index))
            .collect();
        let selected: Vec<_> = requires
            .iter()
            .filter(|(value, _)| {
                *value == self.commonjs.as_os_str().as_encoded_bytes()
                    || commonjs_path(value, cwd).as_deref() == Some(&self.commonjs)
            })
            .map(|(_, span)| span.clone())
            .collect();
        let mut result = if selected.len() == 1
            && requires
                .first()
                .is_some_and(|(value, _)| *value == self.commonjs.as_os_str().as_encoded_bytes())
            && requires
                .first()
                .is_some_and(|(_, span)| *span == selected[0])
        {
            inherited.to_owned()
        } else {
            // Move only the selected loader. Requoting every caller argument
            // would change its spelling and could change Node's diagnostics.
            let mut result = OsString::new();
            append_loader(&mut result, "--require", loader_path(&self.commonjs)?);
            let bytes = inherited.as_encoded_bytes();
            let mut cursor = 0;
            for span in selected {
                append_option_bytes(&mut result, &bytes[cursor..span.start]);
                cursor = span.end;
            }
            append_option_bytes(&mut result, &bytes[cursor..]);
            result
        };
        if let Some(path) = &self.esm {
            result = selected_esm_options(result, path, cwd)?;
        }
        Ok(result)
    }
}

fn commonjs_path(value: &[u8], cwd: Option<&Path>) -> Option<PathBuf> {
    // Token decoding removes only ASCII option syntax from the original OsStr.
    // CommonJS paths are native filenames, not URLs: '#', '?' and '%' stay
    // literal.
    let path = Path::new(unsafe { OsStr::from_encoded_bytes_unchecked(value) });
    let relative = value.starts_with(b"./")
        || value.starts_with(b"../")
        || cfg!(windows) && (value.starts_with(b".\\") || value.starts_with(b"..\\"));
    let path = if path.is_absolute() {
        path.to_owned()
    } else if relative {
        cwd?.join(path)
    } else {
        // Bare package requests and Windows drive-relative paths need Node's
        // module/drive lookup; do not guess their identity in native setup.
        return None;
    };
    Some(crate::graph::normalize(&path))
}

fn loader_path(path: &std::path::Path) -> Result<&str> {
    path.to_str().ok_or_else(|| {
        Error::new(
            Code::PnportUnsupportedOperation,
            "The selected Node loader path cannot be encoded.",
        )
    })
}

fn append_loader(result: &mut OsString, flag: &str, value: &str) {
    if !result.is_empty() {
        result.push(" ");
    }
    result.push(flag);
    result.push(" \"");
    // NODE_OPTIONS uses double quotes and backslash escapes inside them.
    // JSON escaping would change literal tabs/newlines in paths.
    result.push(value.replace('\\', "\\\\").replace('"', "\\\""));
    result.push("\"");
}

fn append_option_bytes(result: &mut OsString, bytes: &[u8]) {
    if !bytes.is_empty() {
        result.push(" ");
        // Token spans end at ASCII separators in the original OsStr encoding.
        // Slicing at those boundaries preserves its platform encoding.
        result.push(unsafe { OsStr::from_encoded_bytes_unchecked(bytes) });
    }
}

fn file_url(path: &str) -> String {
    use std::fmt::Write;
    let mut result = String::from("file://");
    #[cfg(windows)]
    let path = path.replace('\\', "/");
    if !path.starts_with('/') {
        result.push('/');
    }
    for byte in path.as_bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'/' | b':' | b'-' | b'_' | b'.' | b'~') {
            result.push(*byte as char);
        } else {
            write!(result, "%{byte:02X}").unwrap();
        }
    }
    result
}

// Match Node's ParseNodeOptionsEnvVar: only spaces delimit arguments, only
// double quotes group them, and backslashes escape inside double quotes.
struct Token {
    value: Vec<u8>,
    span: std::ops::Range<usize>,
}

fn tokens(value: &[u8]) -> Option<Vec<Token>> {
    let mut result = Vec::new();
    let mut i = 0;
    while i < value.len() {
        if value[i] == b' ' {
            i += 1;
            continue;
        }
        let start = i;
        let mut decoded = Vec::new();
        let mut quoted = false;
        while i < value.len() {
            let mut byte = value[i];
            if byte == b'\\' && quoted {
                i += 1;
                byte = *value.get(i)?;
            } else if byte == b' ' && !quoted {
                break;
            } else if byte == b'"' {
                quoted = !quoted;
                i += 1;
                continue;
            }
            decoded.push(byte);
            i += 1;
        }
        if quoted {
            return None;
        }
        if !decoded.is_empty() {
            result.push(Token {
                value: decoded,
                span: start..i,
            });
        }
    }
    Some(result)
}

fn require_token(tokens: &[Token], index: usize) -> Option<(&[u8], std::ops::Range<usize>)> {
    let token = &tokens[index];
    for flag in [b"--require".as_slice(), b"-r".as_slice()] {
        if token.value == flag {
            let argument = tokens.get(index + 1)?;
            return Some((&argument.value, token.span.start..argument.span.end));
        }
        if let Some(value) = token
            .value
            .strip_prefix(flag)
            .and_then(|value| value.strip_prefix(b"="))
        {
            return Some((value, token.span.clone()));
        }
    }
    None
}

fn selected_esm_options(options: OsString, path: &Path, cwd: Option<&Path>) -> Result<OsString> {
    let tokens =
        tokens(options.as_encoded_bytes()).expect("generated options retain valid quoting");
    let mut selected = Vec::new();
    let mut first_loader = None;
    for (index, token) in tokens.iter().enumerate() {
        for flag in ["--experimental-loader", "--loader"] {
            let value = if token.value == flag.as_bytes() {
                tokens.get(index + 1).map(|argument| {
                    (
                        argument.value.as_slice(),
                        token.span.start..argument.span.end,
                    )
                })
            } else {
                token
                    .value
                    .strip_prefix(format!("{flag}=").as_bytes())
                    .map(|value| (value, token.span.clone()))
            };
            if let Some((value, span)) = value {
                first_loader.get_or_insert_with(|| span.clone());
                if value == path.as_os_str().as_encoded_bytes()
                    || file_url_path(value, cwd).as_deref()
                        == Some(path.as_os_str().as_encoded_bytes())
                {
                    selected.push((span, value.starts_with(b"./") || value.starts_with(b"../")));
                }
            }
        }
    }
    if selected.is_empty() {
        let mut result = OsString::new();
        append_loader(
            &mut result,
            "--experimental-loader",
            &file_url(loader_path(path)?),
        );
        append_option_bytes(&mut result, options.as_encoded_bytes());
        return Ok(result);
    }
    let relative = selected[0].1;
    if selected.len() == 1 && !relative && first_loader.as_ref() == Some(&selected[0].0) {
        return Ok(options);
    }
    // A selected relative loader must stay bound when a descendant changes
    // cwd. Keep unrelated relative loaders untouched and remove only repeats
    // of the selected, untagged module.
    let bytes = options.as_encoded_bytes();
    let mut result = OsString::new();
    // Node loads each later loader through earlier registered hooks. Yarn must
    // be registered first so caller loaders can import PnP-only dependencies.
    if relative {
        append_loader(
            &mut result,
            "--experimental-loader",
            &file_url(loader_path(path)?),
        );
    } else {
        result.push(unsafe { OsStr::from_encoded_bytes_unchecked(&bytes[selected[0].0.clone()]) });
    }
    let mut cursor = 0;
    for (span, _) in selected {
        // Spans use ASCII argument boundaries in the original OsStr encoding.
        append_option_bytes(&mut result, &bytes[cursor..span.start]);
        cursor = span.end;
    }
    append_option_bytes(&mut result, &bytes[cursor..]);
    Ok(result)
}

fn file_url_path(value: &[u8], cwd: Option<&Path>) -> Option<Vec<u8>> {
    let value = std::str::from_utf8(value).ok()?;
    let url = url::Url::parse(value).ok().or_else(|| {
        if value.starts_with("./") || value.starts_with("../") || value.starts_with('/') {
            url::Url::from_directory_path(cwd?).ok()?.join(value).ok()
        } else {
            None
        }
    })?;
    if url.scheme() != "file"
        || url.host_str().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return None;
    }
    // Node normalizes file URLs before loading ESM, but rejects encoded path
    // separators. Queries/fragments identify separate modules and must remain
    // separate from the selected, untagged loader.
    if url
        .path()
        .as_bytes()
        .windows(3)
        .any(|part| part.eq_ignore_ascii_case(b"%2f") || part.eq_ignore_ascii_case(b"%5c"))
    {
        return None;
    }
    let path = url.to_file_path().ok()?;
    let bytes = path.as_os_str().as_encoded_bytes();
    (!bytes.contains(&0)).then(|| bytes.to_vec())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::graph::Input;

    fn loader(esm: bool) -> Loader {
        #[cfg(unix)]
        let path = PathBuf::from("/project with spaces/.pnp.cjs");
        #[cfg(windows)]
        let path = PathBuf::from(r"C:\project with spaces\.pnp.cjs");
        Loader::from_snapshot(&Snapshot {
            schema_version: 1,
            manifest_path: path.clone(),
            data: serde_json::Value::Null,
            inputs: if esm {
                vec![Input {
                    path: path.with_file_name(".pnp.loader.mjs"),
                    sha256: String::new(),
                }]
            } else {
                vec![]
            },
        })
    }

    #[test]
    fn preserves_user_options_and_deduplicates_selected_loaders() {
        let loader = loader(true);
        let original = OsStr::new("--no-warnings --require=\"/other/preload.cjs\"");
        let options = loader.options(Some(original)).unwrap();
        assert!(options
            .to_str()
            .unwrap()
            .contains(original.to_str().unwrap()));
        assert_eq!(loader.options(Some(&options)).unwrap(), options);
        #[cfg(unix)]
        let alternate = OsStr::new("-r \"/project with spaces/.pnp.cjs\" --loader=file:///project%20with%20spaces/.pnp.loader.mjs");
        #[cfg(windows)]
        let alternate = OsStr::new(
            "-r \"C:\\\\project with spaces\\\\.pnp.cjs\" \
             --loader=file:///C:/project%20with%20spaces/.pnp.loader.mjs",
        );
        assert_eq!(loader.options(Some(alternate)).unwrap(), alternate);
    }

    #[test]
    fn quotes_native_path_bytes_without_json_or_shell_reinterpretation() {
        let mut loader = loader(false);
        loader.commonjs = PathBuf::from("/quote\"slash\\tab\tline\n雪/.pnp.cjs");
        let options = loader.options(None).unwrap();
        assert_eq!(
            tokens(options.as_encoded_bytes())
                .unwrap()
                .into_iter()
                .map(|token| token.value)
                .collect::<Vec<_>>(),
            vec![
                b"--require".to_vec(),
                loader.commonjs.as_os_str().as_encoded_bytes().to_vec()
            ]
        );
        for malformed in ["--require \"unterminated", "--require \"escape\\"] {
            assert_eq!(
                loader.options(Some(OsStr::new(malformed))).unwrap(),
                malformed
            );
        }
    }

    #[test]
    fn activates_yarn_before_caller_preloads_and_moves_existing_selected_requires() {
        let loader = loader(false);
        let selected = loader.commonjs.to_str().unwrap().replace('\\', "\\\\");
        let original = format!(
            "--no-warnings --require=\"/first preload.cjs\"  -r \"{selected}\" \
             --require=/second.cjs -r=\"{selected}\""
        );
        let options = loader.options(Some(OsStr::new(&original))).unwrap();
        let parsed = tokens(options.as_encoded_bytes()).unwrap();
        let requires: Vec<_> = parsed
            .iter()
            .enumerate()
            .filter_map(|(index, _)| require_token(&parsed, index).map(|(value, _)| value))
            .collect();
        assert_eq!(
            requires,
            vec![
                loader.commonjs.as_os_str().as_encoded_bytes(),
                b"/first preload.cjs",
                b"/second.cjs"
            ]
        );
        assert!(options
            .to_str()
            .unwrap()
            .contains("--no-warnings --require=\"/first preload.cjs\""));
        assert_eq!(loader.options(Some(&options)).unwrap(), options);
    }

    #[test]
    fn binds_selected_relative_commonjs_preloads_to_the_workload_cwd() {
        let loader = loader(false);
        let root = loader.commonjs.parent().unwrap();
        for (specifier, cwd) in [
            ("./.pnp.cjs", root.to_owned()),
            ("../.pnp.cjs", root.join("child")),
        ] {
            let inherited = format!(
                "--no-warnings --require=\"/first preload.cjs\"  -r \"{specifier}\" \
                 --require=\"{specifier}\" --require=/second.cjs"
            );
            let provisional = loader.options(Some(OsStr::new(&inherited))).unwrap();
            for inherited in [OsStr::new(&inherited), &provisional] {
                let options = loader.options_in(Some(inherited), Some(&cwd)).unwrap();
                let parsed = tokens(options.as_encoded_bytes()).unwrap();
                let requires: Vec<_> = parsed
                    .iter()
                    .enumerate()
                    .filter_map(|(index, _)| require_token(&parsed, index).map(|(value, _)| value))
                    .collect();
                assert_eq!(
                    requires,
                    [
                        loader.commonjs.as_os_str().as_encoded_bytes(),
                        b"/first preload.cjs",
                        b"/second.cjs"
                    ]
                );
                assert!(options
                    .to_str()
                    .unwrap()
                    .contains("--no-warnings --require=\"/first preload.cjs\""));
                assert_eq!(
                    loader
                        .options_in(Some(&options), Some(&root.join("outside")))
                        .unwrap(),
                    options
                );
            }
        }
    }

    #[test]
    fn commonjs_paths_preserve_literal_characters_and_unrelated_requests() {
        let mut loader = loader(false);
        loader.commonjs = loader
            .commonjs
            .parent()
            .unwrap()
            .with_file_name("project #?%雪")
            .join(".pnp.cjs");
        let root = loader.commonjs.parent().unwrap();
        let bound = loader
            .options_in(Some(OsStr::new("--require ./.pnp.cjs")), Some(root))
            .unwrap();
        assert!(!bound.to_str().unwrap().contains("./.pnp.cjs"));
        for (specifier, cwd) in [
            ("./.pnp.cjs", root.join("other")),
            ("./.pnp.cjs?tag", root.to_owned()),
            ("./.pnp.cjs#tag", root.to_owned()),
            ("loader-package", root.to_owned()),
        ] {
            let inherited = format!("--require {specifier}");
            let options = loader
                .options_in(Some(OsStr::new(&inherited)), Some(&cwd))
                .unwrap();
            assert!(options.to_str().unwrap().contains(&inherited));
        }
    }

    #[test]
    fn registers_yarn_before_caller_esm_loaders_without_reordering_callers() {
        let loader = loader(true);
        let selected = file_url(loader.esm.as_ref().unwrap().to_str().unwrap());
        let callers = "--no-warnings --loader=\"file:///first%20loader.mjs\"  \
                       --experimental-loader \"file:///second.mjs\"";
        for inherited in [
            callers.to_owned(),
            format!("{callers} --loader={selected}"),
            format!("{callers} --loader={selected} --experimental-loader={selected}"),
        ] {
            let options = loader.options(Some(OsStr::new(&inherited))).unwrap();
            let parsed = tokens(options.as_encoded_bytes()).unwrap();
            let loaders: Vec<_> = parsed
                .iter()
                .enumerate()
                .filter_map(|(index, token)| {
                    if token.value == b"--experimental-loader" || token.value == b"--loader" {
                        Some(parsed[index + 1].value.as_slice())
                    } else {
                        token
                            .value
                            .strip_prefix(b"--loader=")
                            .or_else(|| token.value.strip_prefix(b"--experimental-loader="))
                    }
                })
                .collect();
            assert_eq!(
                loaders,
                [
                    selected.as_bytes(),
                    b"file:///first%20loader.mjs",
                    b"file:///second.mjs"
                ]
            );
            assert!(options.to_str().unwrap().contains(callers));
            assert_eq!(loader.options(Some(&options)).unwrap(), options);
        }
    }

    #[test]
    #[cfg(unix)]
    fn esm_paths_preserve_url_significant_and_unicode_characters() {
        let mut loader = loader(true);
        loader.esm = Some(PathBuf::from("/space #?%雪/.pnp.loader.mjs"));
        let options = loader.options(None).unwrap();
        assert!(options.to_str().unwrap().contains(
            "--experimental-loader \"file:///space%20%23%3F%25%E9%9B%AA/.pnp.loader.mjs\""
        ));
        assert_eq!(loader.options(Some(&options)).unwrap(), options);
        #[cfg(unix)]
        assert_eq!(
            file_url_path(
                b"file://localhost/project%20with%20spaces/.pnp.loader.mjs",
                None
            ),
            Some(b"/project with spaces/.pnp.loader.mjs".to_vec())
        );
        assert!(file_url_path(b"file://remote/project/.pnp.loader.mjs", None).is_none());
        assert!(file_url_path(b"file://localhost-other/project/.pnp.loader.mjs", None).is_none());
    }

    #[test]
    #[cfg(unix)]
    fn deduplicates_normalized_file_urls_without_merging_distinct_modules() {
        let loader = loader(true);
        for url in [
            "FILE:///project%20with%20spaces/.pnp.loader.mjs",
            "file://LOCALHOST/project%20with%20spaces/.pnp.loader.mjs",
            "file:/project%20with%20spaces/child/../.pnp.loader.mjs",
        ] {
            let options = format!("--require \"/project with spaces/.pnp.cjs\" --loader={url}");
            assert_eq!(
                loader.options(Some(OsStr::new(&options))).unwrap(),
                OsStr::new(&options)
            );
        }
        for url in [
            "file:///project%20with%20spaces/.pnp.loader.mjs?tag",
            "file:///project%20with%20spaces/.pnp.loader.mjs#tag",
            "file:///project%20with%20spaces%2f.pnp.loader.mjs",
            "file:///project%20with%20spaces%5C.pnp.loader.mjs",
            "file:///project%20with%20spaces/.pnp.loader.mjs%00",
            "https://localhost/project%20with%20spaces/.pnp.loader.mjs",
        ] {
            assert!(file_url_path(url.as_bytes(), None).is_none(), "{url}");
        }
    }

    #[test]
    fn binds_selected_relative_esm_loaders_to_the_workload_cwd() {
        let loader = loader(true);
        let root = loader.commonjs.parent().unwrap();
        for (specifier, cwd) in [
            ("./.pnp.loader.mjs", root.to_owned()),
            ("../.pnp.loader.mjs", root.join("child")),
        ] {
            let inherited = format!("--no-warnings --loader=\"{specifier}\"");
            let options = loader
                .options_in(Some(OsStr::new(&inherited)), Some(&cwd))
                .unwrap();
            assert!(!options.to_str().unwrap().contains(specifier));
            assert_eq!(
                loader
                    .options_in(Some(&options), Some(&root.join("outside")))
                    .unwrap(),
                options
            );
            let provisional = loader.options(Some(OsStr::new(&inherited))).unwrap();
            let normalized = loader.options_in(Some(&provisional), Some(&cwd)).unwrap();
            let tokens = tokens(normalized.as_encoded_bytes()).unwrap();
            assert_eq!(
                tokens
                    .iter()
                    .filter(|token| token.value == b"--loader"
                        || token.value == b"--experimental-loader"
                        || token.value.starts_with(b"--loader="))
                    .count(),
                1
            );
        }
    }

    #[test]
    fn keeps_relative_loaders_for_other_directories_and_tagged_modules() {
        let loader = loader(true);
        for (specifier, cwd) in [
            (
                "./.pnp.loader.mjs",
                loader.commonjs.parent().unwrap().join("other"),
            ),
            (
                "./.pnp.loader.mjs?tag",
                loader.commonjs.parent().unwrap().to_owned(),
            ),
            (
                "./.pnp.loader.mjs#tag",
                loader.commonjs.parent().unwrap().to_owned(),
            ),
            (
                "loader-package",
                loader.commonjs.parent().unwrap().to_owned(),
            ),
        ] {
            let inherited = format!("--loader=\"{specifier}\"");
            let options = loader
                .options_in(Some(OsStr::new(&inherited)), Some(&cwd))
                .unwrap();
            assert!(options.to_str().unwrap().contains(&inherited));
            assert!(options.to_str().unwrap().contains("--experimental-loader"));
        }
    }

    #[test]
    #[cfg(windows)]
    fn normalizes_windows_local_file_urls() {
        assert_eq!(
            file_url_path(
                b"FILE://LOCALHOST/C:/project%20with%20spaces/.pnp.loader.mjs",
                None
            ),
            Some(br"C:\project with spaces\.pnp.loader.mjs".to_vec())
        );
    }
}
