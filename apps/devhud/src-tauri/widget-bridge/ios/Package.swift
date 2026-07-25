// swift-tools-version: 5.9

import PackageDescription

let package = Package(
    name: "tauri-plugin-devhud-widget",
    platforms: [
        .iOS(.v17),
        .macOS(.v14),
    ],
    products: [
        .library(
            name: "tauri-plugin-devhud-widget",
            type: .static,
            targets: ["DevHudWidgetPlugin"]
        ),
    ],
    dependencies: [
        .package(name: "Tauri", path: "../.tauri/tauri-api"),
    ],
    targets: [
        .target(
            name: "DevHudWidgetCore",
            path: "Sources/DevHudWidgetCore"
        ),
        .target(
            name: "DevHudWidgetPlugin",
            dependencies: [
                "DevHudWidgetCore",
                .byName(name: "Tauri"),
            ],
            path: "Sources/DevHudWidgetPlugin"
        ),
    ]
)
