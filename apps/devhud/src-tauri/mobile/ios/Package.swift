// swift-tools-version:5.9

import PackageDescription

let package = Package(
    name: "devhud",
    platforms: [.iOS(.v16)],
    products: [.library(name: "devhud", type: .static, targets: ["devhud"])],
    dependencies: [.package(name: "Tauri", path: "../.tauri/tauri-api")],
    targets: [
        .target(name: "devhud", dependencies: [.byName(name: "Tauri")], path: "Sources")
    ]
)
