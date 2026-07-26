// swift-tools-version: 5.9

import PackageDescription

let package = Package(
    name: "tauri-plugin-devhud-diagnostics",
    platforms: [
        .iOS(.v17),
        .macOS(.v14),
    ],
    products: [
        .library(
            name: "tauri-plugin-devhud-diagnostics",
            type: .static,
            targets: ["DevHudDiagnosticsPlugin"]
        ),
    ],
    dependencies: [
        .package(name: "Tauri", path: "../.tauri/tauri-api"),
    ],
    targets: [
        .target(
            name: "DevHudDiagnosticsPlugin",
            dependencies: [.byName(name: "Tauri")],
            path: "Sources/DevHudDiagnosticsPlugin"
        ),
    ]
)
