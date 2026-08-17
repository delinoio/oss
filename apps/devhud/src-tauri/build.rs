fn main() {
    #[cfg(target_os = "macos")]
    tauri_plugin::mobile::update_entitlements(|entitlements| {
        entitlements.insert(
            "com.apple.security.application-groups".to_string(),
            plist::Value::Array(vec![plist::Value::String(
                "group.io.delino.devhud".to_string(),
            )]),
        );
        entitlements.insert(
            "keychain-access-groups".to_string(),
            plist::Value::Array(vec![plist::Value::String(
                "$(AppIdentifierPrefix)io.delino.devhud.shared".to_string(),
            )]),
        );
    })
    .expect("configure DevHUD shared iOS entitlements");
    tauri_plugin::Builder::new(&[])
        .ios_path("mobile/ios")
        .build();
    tauri_build::build();
}
