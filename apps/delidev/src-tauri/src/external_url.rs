// SPDX-License-Identifier: Apache-2.0
//! Native HTTP(S) validation and local/general classification for
//! user-activated external links. The renderer may detect candidate URLs, but
//! only this module decides whether a string is openable and which destination
//! class applies.
use std::net::IpAddr;

use serde::{Deserialize, Serialize};
use url::{Host, Url};

use crate::NativeFailure;

const URL_LIMIT: usize = 2048;

/// Destination class. Classification is a pure function of the parsed host and
/// never resolves DNS.
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum UrlClass {
    General,
    Local,
}

/// A validated, credential-free HTTP(S) URL in its canonical serialization.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ExternalUrl {
    url: String,
    class: UrlClass,
}

impl ExternalUrl {
    pub fn as_str(&self) -> &str {
        &self.url
    }

    pub fn class(&self) -> UrlClass {
        self.class
    }
}

/// Accepts only explicit `http`/`https` URLs with a host and no embedded
/// credentials, whitespace or control characters. Bare hostnames, files and
/// other schemes are rejected.
pub fn classify(input: &str) -> Result<ExternalUrl, NativeFailure> {
    if input.is_empty()
        || input.len() > URL_LIMIT
        || input
            .chars()
            .any(|character| character.is_control() || character.is_whitespace())
    {
        return Err(NativeFailure::InvalidInput);
    }
    let parsed = Url::parse(input).map_err(|_| NativeFailure::InvalidInput)?;
    if !matches!(parsed.scheme(), "http" | "https")
        || !parsed.username().is_empty()
        || parsed.password().is_some()
    {
        return Err(NativeFailure::InvalidInput);
    }
    let class = match parsed.host().ok_or(NativeFailure::InvalidInput)? {
        Host::Ipv4(address) if IpAddr::V4(address).is_loopback() => UrlClass::Local,
        Host::Ipv6(address) if address == std::net::Ipv6Addr::LOCALHOST => UrlClass::Local,
        Host::Domain("localhost") => UrlClass::Local,
        _ => UrlClass::General,
    };
    let url = parsed.to_string();
    if url.len() > URL_LIMIT {
        return Err(NativeFailure::InvalidInput);
    }
    Ok(ExternalUrl { url, class })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn class(input: &str) -> UrlClass {
        classify(input).unwrap().class()
    }

    #[test]
    fn loopback_hosts_are_local_regardless_of_port_path_or_query() {
        for input in [
            "http://localhost",
            "http://localhost:3000",
            "https://LOCALHOST/path?q=1#part",
            "http://127.0.0.1:8080/",
            "http://127.0.0.2:5173",
            "http://127.255.255.254/x",
            "http://127.1/",
            "http://[::1]:8080",
            "https://[::1]/a?b=c",
        ] {
            assert_eq!(class(input), UrlClass::Local, "{input}");
        }
    }

    #[test]
    fn every_other_host_is_general_without_name_resolution() {
        for input in [
            "http://192.168.1.10:3000",
            "http://10.0.0.1",
            "http://0.0.0.0",
            "https://localhost.example",
            "https://example.com/?next=localhost",
            "https://example.com/localhost",
            "https://notlocalhost",
            "https://localhost.",
            "http://[::2]",
            "http://[::ffff:127.0.0.1]",
            "https://example.com/path?q=1#part",
        ] {
            assert_eq!(class(input), UrlClass::General, "{input}");
        }
    }

    #[test]
    fn only_explicit_credential_free_http_urls_are_accepted() {
        for input in [
            "",
            "javascript:alert(1)",
            "data:text/html,hi",
            "file:///etc/passwd",
            "ftp://example.com",
            "localhost:3000",
            "example.com",
            "//example.com",
            "https://",
            "https://user@example.com",
            "https://user:secret@example.com",
            "https://exa mple.com",
            "https://example.com/\nsecond",
            "https://example.com/\u{0}",
            "/usr/bin/open",
            "C:\\Windows\\System32\\cmd.exe",
        ] {
            assert_eq!(
                classify(input),
                Err(NativeFailure::InvalidInput),
                "{input:?}"
            );
        }
        let long = format!("https://example.com/{}", "a".repeat(URL_LIMIT));
        assert_eq!(classify(&long), Err(NativeFailure::InvalidInput));
    }

    #[test]
    fn shell_metacharacters_stay_inside_one_url_value() {
        let url = classify("https://example.com/?a=$(touch%20x)&b=;|`id`").unwrap();
        assert!(
            url.as_str()
                .starts_with("https://example.com/?a=$(touch%20x)&b=")
        );
        assert!(!url.as_str().contains(char::is_whitespace));
    }
}
