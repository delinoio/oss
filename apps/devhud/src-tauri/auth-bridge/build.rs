fn main() {
    tauri_plugin::Builder::new(&[
        "readSession",
        "writeSession",
        "clearSession",
        "openAuthorization",
        "openPullRequest",
        "takeCallback",
    ])
    .android_path("android")
    .ios_path("ios")
    .build();
}
