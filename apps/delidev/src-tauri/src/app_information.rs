// SPDX-License-Identifier: Apache-2.0
use serde::Deserialize;

#[derive(Debug, Clone, Copy, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum AppInformationLink {
    Releases,
    License,
    Notices,
}
impl AppInformationLink {
    pub fn destination(self) -> &'static str {
        match self {
            Self::Releases => "https://github.com/delinoio/oss/releases?q=delidev-v&expanded=true",
            Self::License => "https://github.com/delinoio/oss/blob/main/LICENSE",
            Self::Notices => "https://github.com/delinoio/oss/tree/main/apps/delidev/public",
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn only_compiled_destinations_are_admitted() {
        for (value, expected) in [
            (
                "releases",
                "https://github.com/delinoio/oss/releases?q=delidev-v&expanded=true",
            ),
            (
                "license",
                "https://github.com/delinoio/oss/blob/main/LICENSE",
            ),
            (
                "notices",
                "https://github.com/delinoio/oss/tree/main/apps/delidev/public",
            ),
        ] {
            let action: AppInformationLink =
                serde_json::from_value(serde_json::json!(value)).unwrap();
            assert_eq!(action.destination(), expected);
        }
        for value in [
            "https://untrusted.invalid",
            "../LICENSE",
            "unknown",
            "releases?url=other",
        ] {
            assert!(
                serde_json::from_value::<AppInformationLink>(serde_json::json!(value)).is_err()
            );
        }
    }
}
