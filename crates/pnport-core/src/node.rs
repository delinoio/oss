// SPDX-License-Identifier: Apache-2.0
//! Automatic Yarn runtime activation in owned Node workloads.

use std::{
    ffi::{OsStr, OsString},
    path::PathBuf,
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

    /// Preserve caller options byte-for-byte and add only missing selected
    /// loaders. Node processes consume the result; native programs ignore it.
    pub fn options(&self, inherited: Option<&OsStr>) -> Result<OsString> {
        let inherited = inherited.unwrap_or_else(|| OsStr::new(""));
        let mut result = inherited.to_owned();
        let tokens = tokens(inherited.as_encoded_bytes());
        // Keep malformed caller options malformed. Node must retain its own
        // diagnostic instead of pnport repairing or reinterpreting them.
        if tokens.is_none() {
            return Ok(result);
        }
        let tokens = tokens.unwrap();
        for (flag, path) in [
            ("--require", Some(&self.commonjs)),
            ("--experimental-loader", self.esm.as_ref()),
        ] {
            let Some(path) = path else { continue };
            if contains_loader(&tokens, flag, path.as_os_str().as_encoded_bytes()) {
                continue;
            }
            let path = path.to_str().ok_or_else(|| {
                Error::new(
                    Code::PnportUnsupportedOperation,
                    "The selected Node loader path cannot be encoded.",
                )
            })?;
            let value = if flag == "--require" {
                path.to_owned()
            } else {
                // ESM treats loader specifiers as URLs. Encode path characters
                // such as '#', '?' and '%' rather than changing their meaning.
                file_url(path)
            };
            if !result.is_empty() {
                result.push(" ");
            }
            result.push(flag);
            result.push(" \"");
            // NODE_OPTIONS uses double quotes and backslash escapes inside
            // them. JSON escaping would change literal tabs/newlines in paths.
            result.push(value.replace('\\', "\\\\").replace('"', "\\\""));
            result.push("\"");
        }
        Ok(result)
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
fn tokens(value: &[u8]) -> Option<Vec<Vec<u8>>> {
    let mut result: Vec<Vec<u8>> = Vec::new();
    let mut quoted = false;
    let mut new_argument = true;
    let mut i = 0;
    while i < value.len() {
        let mut byte = value[i];
        if byte == b'\\' && quoted {
            i += 1;
            byte = *value.get(i)?;
        } else if byte == b' ' && !quoted {
            new_argument = true;
            i += 1;
            continue;
        } else if byte == b'"' {
            quoted = !quoted;
            i += 1;
            continue;
        }
        if new_argument {
            result.push(Vec::new());
            new_argument = false;
        }
        result.last_mut()?.push(byte);
        i += 1;
    }
    (!quoted).then_some(result)
}

fn contains_loader(tokens: &[Vec<u8>], flag: &str, path: &[u8]) -> bool {
    let flags: &[&str] = if flag == "--require" {
        &["--require", "-r"]
    } else {
        &["--experimental-loader", "--loader"]
    };
    tokens.iter().enumerate().any(|(index, token)| {
        flags.iter().any(|flag| {
            let value = if token == flag.as_bytes() {
                tokens.get(index + 1).map(Vec::as_slice)
            } else {
                token.strip_prefix(format!("{flag}=").as_bytes())
            };
            value
                .is_some_and(|value| value == path || file_url_path(value).as_deref() == Some(path))
        })
    })
}

fn file_url_path(value: &[u8]) -> Option<Vec<u8>> {
    let mut source = value.strip_prefix(b"file://")?;
    if source.starts_with(b"localhost/") {
        source = &source[b"localhost".len()..];
    } else if !source.starts_with(b"/") {
        return None;
    }
    let mut result = Vec::new();
    let mut i = 0;
    while i < source.len() {
        if source[i] == b'%' {
            let high = (*source.get(i + 1)? as char).to_digit(16)?;
            let low = (*source.get(i + 2)? as char).to_digit(16)?;
            result.push((high * 16 + low) as u8);
            i += 3;
        } else {
            result.push(source[i]);
            i += 1;
        }
    }
    Some(result)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::graph::Input;

    fn loader(esm: bool) -> Loader {
        let path = PathBuf::from("/project with spaces/.pnp.cjs");
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
            .starts_with(original.to_str().unwrap()));
        assert_eq!(loader.options(Some(&options)).unwrap(), options);
        let alternate = OsStr::new("-r \"/project with spaces/.pnp.cjs\" --loader=file:///project%20with%20spaces/.pnp.loader.mjs");
        assert_eq!(loader.options(Some(alternate)).unwrap(), alternate);
    }

    #[test]
    fn quotes_native_path_bytes_without_json_or_shell_reinterpretation() {
        let mut loader = loader(false);
        loader.commonjs = PathBuf::from("/quote\"slash\\tab\tline\n雪/.pnp.cjs");
        let options = loader.options(None).unwrap();
        assert_eq!(
            tokens(options.as_encoded_bytes()).unwrap(),
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
    fn esm_paths_preserve_url_significant_and_unicode_characters() {
        let mut loader = loader(true);
        loader.esm = Some(PathBuf::from("/space #?%雪/.pnp.loader.mjs"));
        let options = loader.options(None).unwrap();
        assert!(options.to_str().unwrap().contains(
            "--experimental-loader \"file:///space%20%23%3F%25%E9%9B%AA/.pnp.loader.mjs\""
        ));
        assert_eq!(loader.options(Some(&options)).unwrap(), options);
        assert_eq!(
            file_url_path(b"file://localhost/project%20with%20spaces/.pnp.loader.mjs"),
            Some(b"/project with spaces/.pnp.loader.mjs".to_vec())
        );
        assert!(file_url_path(b"file://remote/project/.pnp.loader.mjs").is_none());
        assert!(file_url_path(b"file://localhost-other/project/.pnp.loader.mjs").is_none());
    }
}
