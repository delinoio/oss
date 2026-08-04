const NATIVE_COMMANDS: &[&str] = &[
    "readConfiguration",
    "writeConfiguration",
    "notificationAuthorizationStatus",
    "prepareReset",
    "resetConfiguration",
];

fn main() {
    tauri_plugin::Builder::new(NATIVE_COMMANDS)
        .android_path("android")
        .ios_path("ios")
        .build();
}
