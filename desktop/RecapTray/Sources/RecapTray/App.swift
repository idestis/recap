import AppKit
import SwiftUI

@main
struct RecapApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @StateObject private var model = StatusModel.shared

    var body: some Scene {
        MenuBarExtra {
            TrayMenu()
                .environmentObject(model)
                .environmentObject(HotKeyManager.shared)
        } label: {
            TrayLabel(active: model.status.active)
        }
        .menuBarExtraStyle(.menu)
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        HotKeyManager.shared.register()
        StatusModel.shared.startPolling()
        // `open Recap.app --args --show-settings` (also used for screenshots in development).
        if CommandLine.arguments.contains("--show-settings") {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { SettingsController.shared.show() }
        }
        if CommandLine.arguments.contains("--show-overlay") {
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { OverlayController.shared.show() }
        }
    }
}
