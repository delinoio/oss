// SPDX-License-Identifier: Apache-2.0
use serde::Deserialize;

use crate::NativeFailure;

#[derive(Clone, Copy, Deserialize, Debug, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum GuidanceAction {
    Documentation,
    ApiKeys,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Registry {
    version: u32,
    entries: Vec<Entry>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Entry {
    id: String,
    documentation: String,
    key_creation_url: String,
}

pub fn destination(preset: &str, action: GuidanceAction) -> Result<String, NativeFailure> {
    if preset.is_empty()
        || preset.len() > 64
        || !preset.bytes().all(|c| c.is_ascii_lowercase() || c == b'-')
    {
        return Err(NativeFailure::InvalidInput);
    }
    // Generated from Go's canonical registry; RPC metadata cannot supply or
    // expand this native allowlist, even on an older/foreign selected server.
    let registry: Registry =
        serde_json::from_str(include_str!("../provider-guidance.generated.json"))
            .map_err(|_| NativeFailure::InvalidEvidence)?;
    if registry.version != 1 || registry.entries.len() != 35 {
        return Err(NativeFailure::InvalidEvidence);
    }
    let entry = registry
        .entries
        .into_iter()
        .find(|entry| entry.id == preset)
        .ok_or(NativeFailure::InvalidInput)?;
    let target = match action {
        GuidanceAction::Documentation => entry.documentation,
        GuidanceAction::ApiKeys => entry.key_creation_url,
    };
    let url = url::Url::parse(&target).map_err(|_| NativeFailure::InvalidInput)?;
    if url.scheme() != "https"
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.port().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || target.len() > 2048
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(target)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn fixed_help_selectors_preserve_region_and_never_accept_urls() {
        assert_eq!(
            destination("gemini", GuidanceAction::ApiKeys).unwrap(),
            "https://aistudio.google.com/apikey"
        );
        assert_eq!(
            destination("minimax-cn", GuidanceAction::ApiKeys).unwrap(),
            "https://platform.minimax.cn/user-center/basic-information/interface-key"
        );
        assert_eq!(
            destination("siliconflow-cn", GuidanceAction::ApiKeys).unwrap(),
            "https://cloud.siliconflow.cn/account/ak"
        );
        assert_ne!(
            destination("moonshot", GuidanceAction::ApiKeys).unwrap(),
            destination("moonshot-cn", GuidanceAction::ApiKeys).unwrap()
        );
        for input in [
            "https://untrusted.invalid",
            "../groq",
            "Groq",
            "gemini?key=secret",
            "unsupported",
            "",
        ] {
            assert_eq!(
                destination(input, GuidanceAction::ApiKeys),
                Err(NativeFailure::InvalidInput)
            );
        }
        assert_eq!(
            destination("ollama", GuidanceAction::ApiKeys),
            Err(NativeFailure::InvalidInput)
        );
        assert_eq!(
            destination("ollama", GuidanceAction::Documentation).unwrap(),
            "https://docs.ollama.com/api/openai-compatibility"
        );
        assert!(serde_json::from_str::<GuidanceAction>("\"arbitrary\"").is_err());
    }
}
