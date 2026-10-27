// swift-tools-version:5.9
// Recap menu bar app (tray + overlay). UI only: all logic lives in the
// `recap` CLI / daemon, which this app calls (SPEC D-04).
// `swift build` works for development; packaging/macos/build-app.sh assembles
// the real Recap.app with its Info.plist.
import PackageDescription

let package = Package(
    name: "RecapTray",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "Recap", targets: ["RecapTray"])
    ],
    targets: [
        .executableTarget(
            name: "RecapTray",
            path: "Sources/RecapTray",
            swiftSettings: [.enableExperimentalFeature("StrictConcurrency")]
        )
    ]
)
