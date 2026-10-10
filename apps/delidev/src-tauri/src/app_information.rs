// SPDX-License-Identifier: Apache-2.0
use serde::Deserialize;

/// Closed presentation destinations; renderer URLs and paths are never
/// accepted.
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
    fn stopped_dispatch_never_opens_a_destination() {
        let stopped = std::sync::atomic::AtomicBool::new(true);
        for action in [
            AppInformationLink::Releases,
            AppInformationLink::License,
            AppInformationLink::Notices,
        ] {
            assert_eq!(
                crate::browser_opener::dispatch(action.destination(), &stopped),
                Err(crate::NativeFailure::Stopped)
            );
        }
    }
    #[test]
    fn only_closed_source_destinations_are_accepted() {
        for (action, destination) in [
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
            let value: AppInformationLink = serde_json::from_str(&format!("\"{action}\"")).unwrap();
            assert_eq!(value.destination(), destination);
        }
        for unknown in [
            "https://example.invalid",
            "file:///private",
            "release",
            "unknown",
            "../LICENSE",
        ] {
            assert!(serde_json::from_str::<AppInformationLink>(&format!("\"{unknown}\"")).is_err());
        }
    }
}
