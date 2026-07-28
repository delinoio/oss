// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "tauri-plugin-devhud-auth",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [
        .library(name: "tauri-plugin-devhud-auth", type: .static, targets: ["DevHudAuthPlugin"]),
    ],
    dependencies: [.package(name: "Tauri", path: "../.tauri/tauri-api")],
    targets: [
        .target(
            name: "DevHudAuthPlugin",
            dependencies: [.byName(name: "Tauri")],
            path: "Sources/DevHudAuthPlugin"
        ),
    ]
)
