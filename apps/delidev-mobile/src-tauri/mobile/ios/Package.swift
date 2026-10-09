// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "delidev-mobile", platforms: [.iOS("18.0")], products: [.library(name: "delidev-mobile", type: .static, targets: ["delidev-mobile"])], dependencies: [.package(name: "Tauri", path: "../.tauri/tauri-api")], targets: [.target(name: "delidev-mobile", dependencies: [.byName(name: "Tauri")], path: "Sources")])
