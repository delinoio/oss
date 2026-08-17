fn main() {
    #[cfg(target_os = "macos")]
    tauri_plugin::mobile::update_entitlements(|entitlements| {
        entitlements.insert(
            "com.apple.security.application-groups".to_string(),
            vec!["group.io.delino.devhud"].into(),
        );
        entitlements.insert(
            "keychain-access-groups".to_string(),
            vec!["$(AppIdentifierPrefix)io.delino.devhud.shared"].into(),
        );
    })
    .expect("configure DevHUD shared iOS entitlements");
    tauri_plugin::Builder::new(&[])
        .ios_path("mobile/ios")
        .build();
    tauri_build::build();
}
