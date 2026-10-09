// SPDX-License-Identifier: Apache-2.0
fn main() {
    let mobile =
        std::env::var("CARGO_CFG_TARGET_OS").is_ok_and(|os| os == "ios" || os == "android");
    if mobile {
        tauri_plugin::Builder::new(&[])
            .ios_path("mobile/ios")
            .build();
        tauri_build::build();
    }
}
