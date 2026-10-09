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

const SHORTCUT_COMMANDS: [&str; 2] = ["read_shortcut_preferences", "update_shortcut_preferences"];

#[test]
fn shortcut_preferences_have_complete_closed_compiled_permissions() {
    let manifests: BTreeMap<String, Manifest> = serde_json::from_str(MANIFESTS).unwrap();
    let app = &manifests[APP_ACL_KEY];
    let permission = &app.permissions["device-shortcuts"];
    assert_eq!(permission.commands.allow, SHORTCUT_COMMANDS);
    assert!(permission.commands.deny.is_empty());
    for command in SHORTCUT_COMMANDS {
        assert!(app.commands.iter().any(|declared| declared == command));
    }
}

#[test]
fn shortcut_preferences_admit_product_webviews_and_deny_auxiliary_views() {
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
        for command in SHORTCUT_COMMANDS {
            for label in ["main", "local-fixture", "server-fixture"] {
                assert!(
                    authority
                        .resolve_access(command, label, label, &Origin::Local)
                        .is_some(),
                    "{command} must admit {label} on {target}"
                );
                for child in [
                    "external-fixture",
                    "browser-fixture",
                    "tray-actions",
                    "tray-fixture",
                    "widget-fixture",
                    "auxiliary-fixture",
                ] {
                    assert!(
                        authority
                            .resolve_access(command, label, child, &Origin::Local)
                            .is_none(),
                        "{command} must deny child {child} on {target}"
                    );
                }
                assert!(
                    authority
                        .resolve_access(
                            command,
                            label,
                            label,
                            &Origin::Remote {
                                url: "https://untrusted.invalid/".parse().unwrap()
                            }
                        )
                        .is_none()
                );
            }
            for label in ["tray-actions", "tray-fixture", "widget-fixture", "other"] {
                assert!(
                    authority
                        .resolve_access(command, label, label, &Origin::Local)
                        .is_none()
                );
            }
        }
    }
}
