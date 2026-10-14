// SPDX-License-Identifier: Apache-2.0
use serde::Deserialize;

/// Renderer input chooses a compiled destination; it cannot supply a URL or
/// path.
#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum AppInformationLink {
    Releases,
    License,
    Notices,
}
impl AppInformationLink {
    pub fn destination(self) -> &'static str {
        match self {
            Self::Releases => {
                "https://github.com/delinoio/oss/releases?q=delidev%40v&expanded=true"
            }
            Self::License => "https://github.com/delinoio/oss/blob/main/LICENSE",
            Self::Notices => "https://github.com/delinoio/oss/tree/main/apps/delidev/public",
        }
    }
}
#[cfg(test)]
mod tests {
    use std::sync::atomic::AtomicBool;

    use super::*;
    #[test]
    fn selectors_are_closed_and_destinations_are_compiled() {
        for (selector, action, destination) in [
            (
                "releases",
                AppInformationLink::Releases,
                "https://github.com/delinoio/oss/releases?q=delidev%40v&expanded=true",
            ),
            (
                "license",
                AppInformationLink::License,
                "https://github.com/delinoio/oss/blob/main/LICENSE",
            ),
            (
                "notices",
                AppInformationLink::Notices,
                "https://github.com/delinoio/oss/tree/main/apps/delidev/public",
            ),
        ] {
            assert_eq!(
                serde_json::from_value::<AppInformationLink>(serde_json::json!(selector)).unwrap(),
                action
            );
            assert_eq!(action.destination(), destination);
            assert!(crate::browser_opener::dispatch(destination, &AtomicBool::new(true)).is_err());
        }
        for value in [
            serde_json::json!("https://evil.invalid"),
            serde_json::json!("../LICENSE"),
            serde_json::json!("other"),
            serde_json::json!({"action":"releases","url":"https://evil.invalid"}),
        ] {
            assert!(serde_json::from_value::<AppInformationLink>(value).is_err());
        }
    }
}
