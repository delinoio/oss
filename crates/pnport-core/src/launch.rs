// SPDX-License-Identifier: Apache-2.0
//! Private native launch tokens shared by the supervisor and injected images.

pub fn valid_token(token: &str) -> bool {
    token.starts_with("pnport-")
        && token.len() > "pnport-".len()
        && token.len() <= 255
        && token
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-')
}

#[cfg(test)]
mod tests {
    #[test]
    fn tokens_are_bounded_single_native_filename_components() {
        assert!(super::valid_token("pnport-launch-123"));
        for invalid in ["", "pnport-", "other-123", "pnport-../outside", "pnport-雪"] {
            assert!(!super::valid_token(invalid));
        }
        assert!(!super::valid_token(&format!("pnport-{}", "a".repeat(249))));
    }
}
