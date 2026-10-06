// SPDX-License-Identifier: Apache-2.0
use std::collections::BTreeMap;

use tauri::{
    ipc::{Origin, RuntimeAuthority},
    utils::{
        acl::{APP_ACL_KEY, capability::Capability, manifest::Manifest, resolved::Resolved},
        platform::Target,
    },
};

// Use the actual build outputs: mocked invokes cannot detect a missing native
// permission or a capability accidentally granted to external child webviews.
const MANIFESTS: &str = include_str!(concat!(env!("OUT_DIR"), "/acl-manifests.json"));
const CAPABILITIES: &str = include_str!(concat!(env!("OUT_DIR"), "/capabilities.json"));
const COMMAND: &str = "open_provider_guidance";

#[test]
fn provider_guidance_has_one_closed_declared_command() {
    let manifests: BTreeMap<String, Manifest> = serde_json::from_str(MANIFESTS).unwrap();
    let app = &manifests[APP_ACL_KEY];
    assert!(app.commands.iter().any(|command| command == COMMAND));
    let permission = &app.permissions["provider-guidance"];
    assert_eq!(permission.commands.allow, [COMMAND]);
    assert!(permission.commands.deny.is_empty());
}

#[test]
fn provider_guidance_allows_only_trusted_local_webviews() {
    for target in [Target::MacOS, Target::Windows, Target::Linux] {
        let manifests: BTreeMap<String, Manifest> = serde_json::from_str(MANIFESTS).unwrap();
        let capabilities: BTreeMap<String, Capability> =
            serde_json::from_str(CAPABILITIES).unwrap();
        let resolved = Resolved::resolve(&manifests, capabilities, target).unwrap();
        assert!(resolved.has_app_acl);
        assert!(!resolved.allowed_commands.contains_key("*"));
        let authority = RuntimeAuthority::new(
            #[cfg(debug_assertions)]
            manifests,
            resolved,
        );
        for label in ["main", "server-fixture"] {
            assert!(
                authority
                    .resolve_access(COMMAND, label, label, &Origin::Local)
                    .is_some(),
                "trusted webview must receive provider guidance on {target}"
            );
            for child in ["external-fixture", "browser-fixture"] {
                assert!(
                    authority
                        .resolve_access(COMMAND, label, child, &Origin::Local)
                        .is_none(),
                    "parent window cannot grant its child provider guidance on {target}"
                );
            }
            assert!(
                authority
                    .resolve_access(
                        COMMAND,
                        label,
                        label,
                        &Origin::Remote {
                            url: "https://untrusted.invalid/".parse().unwrap(),
                        },
                    )
                    .is_none(),
                "remote origin cannot receive provider guidance on {target}"
            );
        }
        assert!(
            authority
                .resolve_access(COMMAND, "other", "other", &Origin::Local)
                .is_none()
        );
        assert!(
            authority
                .resolve_access("open_arbitrary_url", "main", "main", &Origin::Local)
                .is_none()
        );
    }
}
